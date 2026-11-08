package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Sentinel errors.
var (
	ErrRuntimeStopped = errors.New("agent runtime is stopped")
	ErrQueueFull      = errors.New("outbound intent queue is full")
	ErrNilIntent      = errors.New("intent cannot be nil")
	ErrAgentDead      = errors.New("agent is dead; permadeath lifecycle prohibits new intents")
)

// StreamConnector is an interface allowing abstraction over bridge.Client or custom stream providers.
type StreamConnector interface {
	ConnectAgent(ctx context.Context, opts ...grpc.CallOption) (civquestv1.CivQuestBridgeService_ConnectAgentClient, error)
}

// Config provides initial configuration options for constructing an AgentRuntime.
type Config struct {
	AgentID           string
	AgentUUID         string
	ClanID            string
	IdentityConfig    *xdg.AgentIdentityConfig
	Resolver          *xdg.Resolver
	AgentDB           *db.AgentDB
	ReputationDB      *db.ReputationDB
	BridgeClient      *bridge.Client
	StreamConnector   StreamConnector
	SocketPath        string
	IntentQueueSize   int
	OnDeath           func(*civquestv1.DeathEvent)
	DialogueService   *system2.DialogueService
	ChroniclerService *system2.ChroniclerService
	ToponymyService   *system2.ToponymyService
	CompanionService  *system2.CompanionService
	LandmarksDB       *db.LandmarksDB
}

// SocketPosition represents 3D coordinates in the diagnostic socket payload.
type SocketPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// SocketStatusResponse represents the JSON payload served over the per-agent UNIX domain socket.
type SocketStatusResponse struct {
	Agent           string          `json:"agent"`
	Clan            string          `json:"clan"`
	Status          string          `json:"status"`
	PID             int             `json:"pid"`
	WorldTick       int64           `json:"world_tick"`
	Health          float32         `json:"health"`
	FoodLevel       int32           `json:"food_level"`
	Position        *SocketPosition `json:"position,omitempty"`
	NavigationState string          `json:"navigation_state"`
	UptimeSeconds   float64         `json:"uptime_seconds"`
}

// AgentRuntime manages the isolated OS-process lifecycle for an individual agent.
// It maintains the 5 Hz gRPC telemetry stream, processes incoming sensory frames,
// synchronizes local state and inventory caches, and serves live diagnostics over a UNIX socket.
type AgentRuntime struct {
	agentID        string
	agentUUID      string
	clanID         string
	identityConfig *xdg.AgentIdentityConfig
	resolver       *xdg.Resolver
	agentDB        *db.AgentDB
	reputationDB   *db.ReputationDB
	bridgeClient   *bridge.Client
	connector      StreamConnector

	// Intent sequence numbering and outbound queue
	sequenceID  atomic.Int64
	intentQueue chan *civquestv1.AgentIntent

	// Diagnostics & Socket
	socketPath string
	listenerMu sync.Mutex
	listener   net.Listener
	socketWg   sync.WaitGroup
	pid        int
	startTime  time.Time

	// State and synchronization
	mu                 sync.RWMutex
	status             string
	latestFrame        *civquestv1.TelemetryFrame
	lastProcessedSeqID int64
	isDead             bool
	deathEvent         *civquestv1.DeathEvent
	permadeathResult   *PermadeathResult
	hasCachedInventory bool
	lastCachedItems    []CachedItem
	lastEatingTick     int64
	lastFleeTarget     *civquestv1.Vec3
	lastFleeTick       int64

	// System 1 Reflexes and Safe Node Graph Cache
	safeNodesCache []db.MapNode
	system1State   *system1.AgentState
	pavingTracker  *system1.RoadPavingTracker

	// System 2 Deliberative Dialogue Pipeline
	dialogueService      *system2.DialogueService
	chroniclerService    *system2.ChroniclerService
	toponymyService      *system2.ToponymyService
	companionService     *system2.CompanionService
	landmarksDB          *db.LandmarksDB
	lastChronicleAttempt time.Time
	lastChronicleTick    int64
	lastToponymyAttempt  time.Time
	lastToponymyTick     int64
	lastToponymyPos      *civquestv1.Vec3
	lastFollowNavTarget  *civquestv1.Vec3
	lastFollowNavTick    int64
	dialogueWg           sync.WaitGroup
	tradeActive          atomic.Bool
	chroniclerActive     atomic.Bool
	toponymyActive       atomic.Bool
	lastTradeTick        int64
	lastTradePlayerUUID  string
	lastTradeAttemptAt   time.Time
	debugLogger          *DebugLogger
	lastLoggedRoutine    system1.RoutineState

	// Callbacks
	onDeath func(*civquestv1.DeathEvent)

	// Lifecycle control
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
	socketDone chan struct{}
}

// NewRuntime constructs and validates an AgentRuntime instance.
func NewRuntime(cfg Config) (*AgentRuntime, error) {
	agentID := cfg.AgentID
	if agentID == "" && cfg.IdentityConfig != nil {
		agentID = cfg.IdentityConfig.Name
	}
	if err := xdg.ValidateAgentName(agentID); err != nil {
		return nil, fmt.Errorf("invalid agent_id %q: %w", agentID, err)
	}

	clanID := cfg.ClanID
	if clanID == "" && cfg.IdentityConfig != nil {
		clanID = cfg.IdentityConfig.ClanID
	}

	r := cfg.Resolver
	if r == nil {
		r = xdg.DefaultResolver()
	}

	sockPath := cfg.SocketPath
	if sockPath == "" {
		sockPath = r.AgentSocketPath(agentID)
	}

	queueSize := cfg.IntentQueueSize
	if queueSize <= 0 {
		queueSize = 256
	}

	var connector StreamConnector = cfg.StreamConnector
	if connector == nil && cfg.BridgeClient != nil {
		connector = cfg.BridgeClient
	}

	pavingTracker := system1.NewRoadPavingTracker(1000, 3)
	sys1State := system1.NewAgentState()
	sys1State.PavingTracker = pavingTracker

	agentUUID := cfg.AgentUUID
	if agentUUID == "" {
		agentUUID = OfflinePlayerUUID(agentID)
	}

	companionService := cfg.CompanionService
	if companionService == nil && cfg.AgentDB != nil {
		companionService = system2.NewCompanionService(cfg.AgentDB, cfg.ReputationDB, cfg.IdentityConfig)
	}

	var debugLog *DebugLogger
	if r != nil {
		debugLog = NewDebugLogger(
			r.AgentDebugJSONLPath(agentID),
			r.PlaytestEventsJSONLPath(),
			agentID,
			clanID,
		)
	}

	runtime := &AgentRuntime{
		agentID:           agentID,
		agentUUID:         agentUUID,
		clanID:            clanID,
		identityConfig:    cfg.IdentityConfig,
		resolver:          r,
		agentDB:           cfg.AgentDB,
		reputationDB:      cfg.ReputationDB,
		bridgeClient:      cfg.BridgeClient,
		connector:         connector,
		dialogueService:   cfg.DialogueService,
		chroniclerService: cfg.ChroniclerService,
		toponymyService:   cfg.ToponymyService,
		companionService:  companionService,
		landmarksDB:       cfg.LandmarksDB,
		debugLogger:       debugLog,
		intentQueue:       make(chan *civquestv1.AgentIntent, queueSize),
		socketPath:        sockPath,
		pid:               os.Getpid(),
		startTime:         time.Now().UTC(),
		status:            "INITIALIZING",
		onDeath:           cfg.OnDeath,
		done:              make(chan struct{}),
		socketDone:        make(chan struct{}),
		lastCachedItems:   nil,
		system1State:      sys1State,
		pavingTracker:     pavingTracker,
	}

	if debugLog != nil {
		var voice any
		if cfg.IdentityConfig != nil {
			voice = cfg.IdentityConfig.EffectiveVoice()
		}
		debugLog.Emit(0, "RUNTIME_INIT", fmt.Sprintf("Initialized agent runtime %s", agentID), map[string]any{
			"pid":          runtime.pid,
			"agent_uuid":   agentUUID,
			"socket_path":  sockPath,
			"voice_config": voice,
		})
	}

	runtime.loadSafeNodes()

	return runtime, nil
}

// AgentID returns the unique identifier for this agent avatar.
func (r *AgentRuntime) AgentID() string {
	return r.agentID
}

// AgentUUID returns the agent's offline UUID.
func (r *AgentRuntime) AgentUUID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agentUUID
}

// SetAgentUUID updates the agent's offline UUID.
func (r *AgentRuntime) SetAgentUUID(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if uuid != "" {
		r.agentUUID = uuid
	}
}

// ReputationDB returns the clan reputation database handle.
func (r *AgentRuntime) ReputationDB() *db.ReputationDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reputationDB
}

// SetReputationDB configures the clan reputation database handle.
func (r *AgentRuntime) SetReputationDB(rep *db.ReputationDB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reputationDB = rep
}

// PermadeathResult returns the result of the permadeath teardown, or nil if not dead.
func (r *AgentRuntime) PermadeathResult() *PermadeathResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.permadeathResult
}

// ClanID returns the clan identifier for this agent.
func (r *AgentRuntime) ClanID() string {
	return r.clanID
}

// Resolver returns the XDG filesystem path resolver.
func (r *AgentRuntime) Resolver() *xdg.Resolver {
	return r.resolver
}

// AgentDB returns the local SQLite database handle.
func (r *AgentRuntime) AgentDB() *db.AgentDB {
	return r.agentDB
}

// BridgeClient returns the gRPC bridge client.
func (r *AgentRuntime) BridgeClient() *bridge.Client {
	return r.bridgeClient
}

// IdentityConfig returns the loaded identity YAML profile.
func (r *AgentRuntime) IdentityConfig() *xdg.AgentIdentityConfig {
	return r.identityConfig
}

// SetDialogueService assigns the System 2 deliberative dialogue service.
func (r *AgentRuntime) SetDialogueService(d *system2.DialogueService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialogueService = d
}

// DialogueService returns the configured System 2 dialogue service, or nil if unset.
func (r *AgentRuntime) DialogueService() *system2.DialogueService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dialogueService
}

// ChroniclerService returns the configured System 2 Clan Chronicler service, or nil if unset.
func (r *AgentRuntime) ChroniclerService() *system2.ChroniclerService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.chroniclerService
}

// SetChroniclerService assigns or updates the System 2 Clan Chronicler service.
func (r *AgentRuntime) SetChroniclerService(c *system2.ChroniclerService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chroniclerService = c
}

// ToponymyService returns the configured System 2 Toponymy naming service, or nil if unset.
func (r *AgentRuntime) ToponymyService() *system2.ToponymyService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.toponymyService
}

// SetToponymyService assigns or updates the System 2 Toponymy naming service.
func (r *AgentRuntime) SetToponymyService(t *system2.ToponymyService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toponymyService = t
}

// CompanionService returns the configured System 2 Companion service, or nil if unset.
func (r *AgentRuntime) CompanionService() *system2.CompanionService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.companionService
}

// SetCompanionService assigns or updates the System 2 Companion service.
func (r *AgentRuntime) SetCompanionService(c *system2.CompanionService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.companionService = c
}

// LandmarksDB returns the clan landmarks database handle.
func (r *AgentRuntime) LandmarksDB() *db.LandmarksDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.landmarksDB
}

// SetLandmarksDB configures the clan landmarks database handle.
func (r *AgentRuntime) SetLandmarksDB(l *db.LandmarksDB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.landmarksDB = l
}

// System1State returns the volatile System 1 agent routine, needs, and paving state.
func (r *AgentRuntime) System1State() *system1.AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.system1State
}

// PavingTracker returns the road paving tracker.
func (r *AgentRuntime) PavingTracker() *system1.RoadPavingTracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pavingTracker
}

// AssignedBed returns a defensive copy of the agent's assigned bed position.
func (r *AgentRuntime) AssignedBed() *civquestv1.BlockPosProto {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.system1State == nil {
		return nil
	}
	return r.system1State.GetAssignedBed()
}

// SetAssignedBed assigns the designated bed block position.
func (r *AgentRuntime) SetAssignedBed(pos *civquestv1.BlockPosProto) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.system1State != nil {
		r.system1State.SetAssignedBed(pos)
	}
}

// CurrentRoutine returns the current System 1 routine state.
func (r *AgentRuntime) CurrentRoutine() system1.RoutineState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.system1State == nil {
		return system1.RoutineDawnAwakening
	}
	return r.system1State.CurrentRoutine
}

// IsCompanionActive returns true if the agent is actively accompanying a player as a bonded companion.
func (r *AgentRuntime) IsCompanionActive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.system1State == nil {
		return false
	}
	return r.system1State.IsCompanionActive()
}

// SocketPath returns the bound UNIX domain socket path.
func (r *AgentRuntime) SocketPath() string {
	return r.socketPath
}

// PID returns the current process ID.
func (r *AgentRuntime) PID() int {
	return r.pid
}

// Status returns the current runtime execution status (e.g., INITIALIZING, RUNNING, DEAD, STOPPED).
func (r *AgentRuntime) Status() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Health returns the agent's current health points.
func (r *AgentRuntime) Health() float32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame != nil && r.latestFrame.Status != nil {
		return r.latestFrame.Status.Health
	}
	return 0
}

// FoodLevel returns the agent's current hunger points (0-20).
func (r *AgentRuntime) FoodLevel() int32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame != nil && r.latestFrame.Status != nil {
		return r.latestFrame.Status.FoodLevel
	}
	return 0
}

// Position returns a defensive copy of the agent's current 3D world position coordinates.
func (r *AgentRuntime) Position() *civquestv1.Vec3 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame != nil && r.latestFrame.Status != nil && r.latestFrame.Status.Position != nil {
		p := r.latestFrame.Status.Position
		return &civquestv1.Vec3{
			X: p.X,
			Y: p.Y,
			Z: p.Z,
		}
	}
	return nil
}

// NavigationState returns the current Baritone navigation state enum.
func (r *AgentRuntime) NavigationState() civquestv1.NavigationState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame != nil && r.latestFrame.Status != nil {
		return r.latestFrame.Status.NavigationState
	}
	return civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED
}

// WorldTick returns the latest observed Minecraft world tick.
func (r *AgentRuntime) WorldTick() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame != nil {
		return r.latestFrame.WorldTick
	}
	return 0
}

// LastProcessedSequenceID returns the latest sequence ID acknowledged by the game server.
func (r *AgentRuntime) LastProcessedSequenceID() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastProcessedSeqID
}

// LatestFrame returns a defensive copy of the most recently received sensory TelemetryFrame.
func (r *AgentRuntime) LatestFrame() *civquestv1.TelemetryFrame {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.latestFrame == nil {
		return nil
	}
	return proto.Clone(r.latestFrame).(*civquestv1.TelemetryFrame)
}

// IsDead returns true if a terminal DeathEvent has been observed.
func (r *AgentRuntime) IsDead() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isDead
}

// DeathEvent returns the terminal DeathEvent, if recorded.
func (r *AgentRuntime) DeathEvent() *civquestv1.DeathEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deathEvent
}

// UptimeSeconds returns elapsed runtime duration in seconds.
func (r *AgentRuntime) UptimeSeconds() float64 {
	return time.Since(r.startTime).Seconds()
}

// QueueIntent enqueues an AgentIntent into the outbound queue.
// If the intent's SequenceId is <= 0, it is automatically assigned via atomic increment.
// If the intent's AgentId is blank, it defaults to the runtime's AgentID.
func (r *AgentRuntime) QueueIntent(intent *civquestv1.AgentIntent) error {
	if intent == nil {
		return ErrNilIntent
	}

	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return ErrAgentDead
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return ErrRuntimeStopped
	default:
	}

	if intent.AgentId == "" {
		intent.AgentId = r.agentID
	}
	if intent.SequenceId <= 0 {
		intent.SequenceId = r.sequenceID.Add(1)
	}

	select {
	case r.intentQueue <- intent:
		if r.debugLogger != nil {
			r.debugLogger.Emit(r.WorldTick(), "INTENT_OUT", intentActionName(intent), map[string]any{
				"sequence_id": intent.SequenceId,
				"action":      intentActionName(intent),
			})
		}
		return nil
	case <-r.done:
		return ErrRuntimeStopped
	default:
		return ErrQueueFull
	}
}

// EnqueueIntent is an alias for QueueIntent.
func (r *AgentRuntime) EnqueueIntent(intent *civquestv1.AgentIntent) error {
	return r.QueueIntent(intent)
}

// IntentQueue returns the read-only outbound intent channel for inspection by test harnesses.
func (r *AgentRuntime) IntentQueue() <-chan *civquestv1.AgentIntent {
	return r.intentQueue
}

// QueuePriorityIntent enqueues a critical reflex intent into the outbound queue.
// If the queue is full, it evicts the oldest intent from the front of the channel,
// logging a warning with the dropped intent type, ensuring combat and panic reflexes are never dropped.
func (r *AgentRuntime) QueuePriorityIntent(intent *civquestv1.AgentIntent) error {
	if intent == nil {
		return ErrNilIntent
	}

	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return ErrAgentDead
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return ErrRuntimeStopped
	default:
	}

	if intent.AgentId == "" {
		intent.AgentId = r.agentID
	}
	if intent.SequenceId <= 0 {
		intent.SequenceId = r.sequenceID.Add(1)
	}

	for {
		select {
		case <-r.done:
			return ErrRuntimeStopped
		case r.intentQueue <- intent:
			if r.debugLogger != nil {
				r.debugLogger.Emit(r.WorldTick(), "INTENT_OUT", intentActionName(intent), map[string]any{
					"sequence_id": intent.SequenceId,
					"action":      intentActionName(intent),
					"priority":    true,
				})
			}
			return nil
		default:
		}

		// Queue is full: evict front intent to prioritize reflex
		select {
		case dropped := <-r.intentQueue:
			log.Printf("[WARN] agent %s: intent queue full, evicted older intent %s (seq %d) for priority reflex %s",
				r.agentID, intentActionName(dropped), dropped.SequenceId, intentActionName(intent))
		default:
		}
	}
}

func intentActionName(intent *civquestv1.AgentIntent) string {
	if intent == nil || intent.Action == nil {
		return "nil"
	}
	switch a := intent.Action.(type) {
	case *civquestv1.AgentIntent_Attack:
		return fmt.Sprintf("AttackIntent(target=%d,shield=%v,flee=%v)", a.Attack.TargetEntityId, a.Attack.RaiseShield, a.Attack.FleeImmediately)
	case *civquestv1.AgentIntent_Navigate:
		return "NavigateIntent"
	case *civquestv1.AgentIntent_UseItem:
		return fmt.Sprintf("UseItemIntent(slot=%d,eating=%v)", a.UseItem.InventorySlot, a.UseItem.IsEatingOrDrinking)
	default:
		return fmt.Sprintf("%T", intent.Action)
	}
}

// Start boots the runtime: initializes the UNIX diagnostic socket, establishes
// the bidirectional gRPC stream, sends the initial handshake intent to register the agent
// in the server StreamRegistry, and runs the telemetry loop.
//
// Start blocks until the context is canceled, Stop() is called, or a DeathEvent is processed.
func (r *AgentRuntime) Start(parentCtx context.Context) error {
	if r.connector == nil {
		return fmt.Errorf("no StreamConnector or BridgeClient provided to AgentRuntime")
	}

	r.mu.Lock()
	select {
	case <-r.done:
		r.mu.Unlock()
		return ErrRuntimeStopped
	default:
	}
	r.ctx, r.cancel = context.WithCancel(parentCtx)
	ctx := r.ctx
	cancel := r.cancel
	r.mu.Unlock()

	defer cancel()

	// 1. Initialize and start UNIX Domain Socket listener
	if err := r.startSocketServer(); err != nil {
		if errors.Is(err, ErrRuntimeStopped) {
			return nil
		}
		return fmt.Errorf("failed to start agent socket server: %w", err)
	}

	// 2. Establish bidirectional gRPC stream with Minecraft NeoForge server
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	stream, err := r.connector.ConnectAgent(streamCtx)
	if err != nil {
		r.Stop()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to establish ConnectAgent gRPC stream: %w", err)
	}

	// 3. Send initial handshake intent so server StreamRegistry registers this agent ID immediately
	handshake := &civquestv1.AgentIntent{
		AgentId:    r.agentID,
		SequenceId: r.sequenceID.Add(1),
	}
	if err := stream.Send(handshake); err != nil {
		r.Stop()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to send handshake intent: %w", err)
	}

	r.mu.Lock()
	select {
	case <-r.done:
		r.mu.Unlock()
		return ErrRuntimeStopped
	default:
	}
	if !r.isDead && r.status != "STOPPED" {
		r.status = "RUNNING"
	}
	r.startTime = time.Now().UTC()
	r.mu.Unlock()

	// 4. Run intent sender goroutine
	sendErrCh := make(chan error, 1)
	go r.sendLoop(streamCtx, streamCancel, stream, sendErrCh)

	// 5. Run telemetry receiver loop (blocks until stream terminates or death occurs)
	recvErr := r.recvLoop(streamCtx, stream)

	// Trigger clean shutdown
	r.Stop()

	// Deterministically await sendLoop completion
	sendErr := <-sendErrCh

	// Normal termination conditions:
	// - Permadeath event (clean exit code 0)
	if r.IsDead() {
		return nil
	}

	// Check sendErr first to ensure send errors (e.g. broken pipe) are reported even if recv was canceled
	if sendErr != nil && !errors.Is(sendErr, io.EOF) && !errors.Is(sendErr, context.Canceled) && status.Code(sendErr) != codes.Canceled {
		return fmt.Errorf("intent stream send error: %w", sendErr)
	}

	// Normal cancellation / termination conditions:
	// - Parent context cancellation / Stop()
	// - Clean stream EOF
	if ctx.Err() != nil || errors.Is(recvErr, io.EOF) || errors.Is(recvErr, context.Canceled) || status.Code(recvErr) == codes.Canceled {
		return nil
	}
	if recvErr != nil {
		return fmt.Errorf("telemetry stream error: %w", recvErr)
	}

	return nil
}

// Stop cleanly terminates the runtime, closing active sockets and channels.
func (r *AgentRuntime) Stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		if !r.isDead {
			r.status = "STOPPED"
		}
		cancel := r.cancel
		r.mu.Unlock()

		close(r.done)
		if cancel != nil {
			cancel()
		}

		r.stopSocketServer()

		// Await pending asynchronous dialogue tasks
		r.tradeActive.Store(false)
		r.chroniclerActive.Store(false)
		r.toponymyActive.Store(false)
		r.dialogueWg.Wait()

		// Close dialogue service if assigned
		if r.dialogueService != nil {
			_ = r.dialogueService.Close()
		}

		// Close agent database if open
		if r.agentDB != nil {
			_ = r.agentDB.Close()
		}
	})
}

// sendLoop reads queued AgentIntents and writes them to the gRPC stream.
func (r *AgentRuntime) sendLoop(ctx context.Context, cancel context.CancelFunc, stream civquestv1.CivQuestBridgeService_ConnectAgentClient, errCh chan<- error) {
	var sendErr error
	defer func() {
		errCh <- sendErr
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case intent, ok := <-r.intentQueue:
			if !ok {
				return
			}
			if err := stream.Send(intent); err != nil {
				sendErr = err
				cancel()
				return
			}
		}
	}
}

// recvLoop processes incoming 5 Hz TelemetryFrames from the server.
func (r *AgentRuntime) recvLoop(ctx context.Context, stream civquestv1.CivQuestBridgeService_ConnectAgentClient) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.done:
			return nil
		default:
		}

		frame, err := stream.Recv()
		if err != nil {
			return err
		}

		r.processFrame(frame)

		// If a terminal death event was encountered, exit the loop cleanly
		if frame.GetDeathEvent() != nil {
			return nil
		}
	}
}

// processFrame ingests a single TelemetryFrame in sub-millisecond time.
func (r *AgentRuntime) processFrame(frame *civquestv1.TelemetryFrame) {
	r.HandleTelemetryFrame(frame)
}

// HandleTelemetryFrame processes an incoming sensory TelemetryFrame through
// System 1 survival reflexes, daily utility scheduler, biological needs FSM,
// road paving reflexes, and System 2 deliberative dialogue dispatch.
func (r *AgentRuntime) HandleTelemetryFrame(frame *civquestv1.TelemetryFrame) {
	if frame == nil {
		return
	}

	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	var deathEv *civquestv1.DeathEvent

	r.mu.Lock()
	r.latestFrame = frame
	r.lastProcessedSeqID = frame.LastProcessedSequenceId

	if frame.DeathEvent != nil {
		r.isDead = true
		r.deathEvent = frame.DeathEvent
		r.status = "DEAD"
		deathEv = frame.DeathEvent
	}
	r.mu.Unlock()

	// If a terminal death event occurred, run permadeath teardown immediately
	if deathEv != nil {
		r.mu.Lock()
		cancel := r.cancel
		r.mu.Unlock()

		// 1. Immediately cancel context to halt any in-flight workers
		if cancel != nil {
			cancel()
		}

		// 2. Await in-flight dialogue goroutines so no DB transactions remain active
		r.tradeActive.Store(false)
		r.chroniclerActive.Store(false)
		r.toponymyActive.Store(false)
		r.dialogueWg.Wait()

		// 3. Stop socket server and close dialogue service to prevent IPC queries
		r.stopSocketServer()
		if r.dialogueService != nil {
			_ = r.dialogueService.Close()
		}

		// 4. Run permadeath teardown (checkpoint DB, archive to graveyard, write death certificate & epitaph)
		ctx := r.ctx
		if ctx == nil || ctx.Err() != nil {
			ctx = context.Background()
		}

		res, err := ExecutePermadeath(ctx, PermadeathRequest{
			AgentID:           r.agentID,
			AgentUUID:         r.AgentUUID(),
			ClanID:            r.clanID,
			DeathEvent:        deathEv,
			Resolver:          r.resolver,
			AgentDB:           r.agentDB,
			ReputationDB:      r.ReputationDB(),
			ChroniclerService: r.chroniclerService,
		})
		if err != nil {
			log.Printf("[ERROR] agent %s permadeath teardown failed: %v", r.agentID, err)
		} else {
			r.mu.Lock()
			r.permadeathResult = res
			r.mu.Unlock()
		}

		// 5. Trigger permadeath terminal callback if configured
		if r.onDeath != nil {
			r.onDeath(deathEv)
		}

		// 6. Signal runtime completion by closing r.done cleanly
		r.stopOnce.Do(func() {
			close(r.done)
		})
		return
	}

	// Update inventory cache file atomically if inventory changed
	if frame.Status != nil {
		var inv []*civquestv1.ItemStackProto
		if frame.Status.Inventory != nil {
			inv = frame.Status.Inventory
		}
		newCache := NewInventoryCache(r.agentID, frame.WorldTick, inv)
		r.mu.RLock()
		cachedBefore := r.hasCachedInventory
		itemsMatch := InventoryEqual(r.lastCachedItems, newCache.Items)
		r.mu.RUnlock()

		if !cachedBefore || !itemsMatch {
			cachePath := r.resolver.AgentInventoryCachePath(r.agentID)
			if err := WriteInventoryCache(cachePath, newCache); err == nil {
				r.mu.Lock()
				r.hasCachedInventory = true
				r.lastCachedItems = newCache.Items
				r.mu.Unlock()
			}
		}
	}

	// Evaluate System 2 companion tracking and sync companion state (<1 ms)
	r.evaluateCompanion(frame)

	// Evaluate deterministic System 1 survival reflexes, daily routine, biological needs, and paving (<5 ms)
	r.evaluateSystem1Reflexes(frame)

	// Dispatch System 2 dialogue and barter haggling asynchronously (non-blocking)
	if len(frame.IncomingChats) > 0 || frame.PendingTrade != nil {
		r.dispatchDialogue(frame)
	}

	// Dispatch System 2 Clan Chronicler synthesis at dusk or near library desks (non-blocking)
	r.dispatchChronicler(frame)

	// Dispatch System 2 Toponymy naming for exploratory landmarks (non-blocking)
	r.dispatchToponymy(frame)
}

// dispatchDialogue asynchronously processes incoming chat messages and pending trade requests
// via System 2 without blocking System 1 survival reflexes.
func (r *AgentRuntime) dispatchDialogue(frame *civquestv1.TelemetryFrame) {
	r.mu.RLock()
	svc := r.dialogueService
	compSvc := r.companionService
	isDead := r.isDead
	ctx := r.ctx
	r.mu.RUnlock()

	if isDead || (svc == nil && compSvc == nil) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}

	chats := frame.IncomingChats
	pendingTrade := frame.PendingTrade
	worldTick := frame.WorldTick

	var vitals *system2.DialogueVitals
	if frame.Status != nil {
		vitals = &system2.DialogueVitals{
			Health:    frame.Status.Health,
			FoodLevel: frame.Status.FoodLevel,
		}
	}

	// 1. Dispatch incoming chats asynchronously
	for _, chat := range chats {
		if chat == nil || chat.SenderUuid == r.agentID || (r.identityConfig != nil && chat.SenderName == r.identityConfig.Name) {
			continue
		}

		r.mu.RLock()
		if r.status != "RUNNING" || r.isDead || (r.dialogueService == nil && r.companionService == nil) {
			r.mu.RUnlock()
			return
		}
		r.dialogueWg.Add(1)
		r.mu.RUnlock()

		c := chat
		go func() {
			defer r.dialogueWg.Done()
			select {
			case <-ctx.Done():
				return
			default:
			}

			log.Printf("[civquest-agent] Received chat from %s (dist=%.1f): %q", c.SenderName, c.Distance, c.Message)
			if r.debugLogger != nil {
				r.debugLogger.Emit(worldTick, "INCOMING_CHAT", fmt.Sprintf("Chat from %s: %s", c.SenderName, c.Message), map[string]any{
					"sender_name": c.SenderName,
					"sender_uuid": c.SenderUuid,
					"distance":    c.Distance,
					"message":     c.Message,
				})
			}

			// 1. Check for companion voice/chat command ('follow me', 'wait here', 'return home')
			r.mu.RLock()
			compSvc := r.companionService
			r.mu.RUnlock()

			if compSvc != nil {
				if cmdRes, isCmd := compSvc.ProcessCommand(ctx, c.SenderUuid, c.SenderName, c.Message, worldTick); isCmd {
					log.Printf("[civquest-agent] Companion command %s from %s (stage=%s, obeyed=%v)",
						cmdRes.Command, c.SenderName, cmdRes.BondStage, cmdRes.Obeyed)
					if r.debugLogger != nil {
						r.debugLogger.Emit(worldTick, "COMPANION_COMMAND", fmt.Sprintf("Command %s from %s (obeyed=%v)", cmdRes.Command, c.SenderName, cmdRes.Obeyed), map[string]any{
							"command":    cmdRes.Command,
							"bond_stage": cmdRes.BondStage,
							"obeyed":     cmdRes.Obeyed,
							"reply_text": cmdRes.ReplyText,
						})
					}

					if r.system1State != nil {
						r.system1State.SetCompanionActive(compSvc.IsActive())
					}
					if cmdRes.Command == system2.CommandWait && frame.Status != nil && frame.Status.Position != nil {
						compSvc.SetWaitPosition(frame.Status.Position)
					} else if cmdRes.Command == system2.CommandFollow {
						compSvc.SetWaitPosition(nil)
					}

					// Boost dialogue familiarity (+0.05)
					_, _ = compSvc.RecordDialogueInteraction(ctx, c.SenderUuid, c.SenderName, 0.0, worldTick)

					if cmdRes.ReplyText != "" && !r.IsDead() && ctx.Err() == nil {
						_ = r.QueueIntent(&civquestv1.AgentIntent{
							AgentId: r.agentID,
							Action: &civquestv1.AgentIntent_Chat{
								Chat: &civquestv1.SendChatIntent{
									Message:       cmdRes.ReplyText,
									TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
								},
							},
						})
					}
					return
				}
			}

			// If not a companion command, proceed with LLM dialogue service
			if svc == nil {
				return
			}

			req := system2.ChatDialogueRequest{
				PlayerUUID:   c.SenderUuid,
				PlayerName:   c.SenderName,
				Message:      c.Message,
				Distance:     c.Distance,
				WorldTick:    worldTick,
				Vitals:       vitals,
				SurplusState: system2.SurplusStable,
			}
			startChat := time.Now()
			res, err := svc.ProcessChat(ctx, req)
			chatLatency := time.Since(startChat)
			if err != nil {
				log.Printf("[WARN] agent %s: dialogue ProcessChat failed: %v", r.agentID, err)
				if r.debugLogger != nil {
					r.debugLogger.Emit(worldTick, "DIALOGUE_ERROR", err.Error(), map[string]any{
						"sender_name": c.SenderName,
						"message":     c.Message,
						"latency_ms":  chatLatency.Milliseconds(),
					})
				}
				return
			}
			if res != nil && r.debugLogger != nil {
				activeTask := ""
				if res.Response.DelegatedTask != nil {
					activeTask = res.Response.DelegatedTask.TaskType
				}
				checks := system1.EvaluateDialogueOutcome(
					res.Response.SpokenReply,
					res.Response.TradeDecision,
					res.Response.PrestigeDelta,
					res.Response.MemorySummary,
					res.Disobeyed,
					activeTask,
					res.ChatIntent,
					res.TradeIntent,
					system1.DialogueEvalSpec{
						ExpectNonEmptyReply: true,
						MaxReplyRunes:       250,
						MinPrestigeDelta:    -10.0,
						MaxPrestigeDelta:    10.0,
						RequireChatIntent:   true,
						ForbiddenSubstrings: []string{"```json", "{\"spoken_reply\""},
					},
				)
				sample := system1.ScoreChecks(0, checks, chatLatency, 0.75, nil)
				r.debugLogger.Emit(worldTick, "DIALOGUE_EVAL", fmt.Sprintf("Reply to %s (score=%.2f, passed=%v)", c.SenderName, sample.Score, sample.Passed), map[string]any{
					"player_name":     c.SenderName,
					"player_message":  c.Message,
					"spoken_reply":    res.Response.SpokenReply,
					"paced_segments":  res.PacedSegments,
					"trade_decision":  res.Response.TradeDecision,
					"delegated_task":  res.Response.DelegatedTask,
					"prestige_delta":  res.Response.PrestigeDelta,
					"memory_summary":  res.Response.MemorySummary,
					"disobeyed":       res.Disobeyed,
					"disobey_reason":  res.DisobeyReason,
					"latency_ms":      chatLatency.Milliseconds(),
					"eval_score":      sample.Score,
					"eval_passed":     sample.Passed,
					"eval_checks":     sample.Checks,
				})
			}
			if res != nil && res.ChatIntent != nil {
				log.Printf("[civquest-agent] %s responding to %s: %q", r.agentID, c.SenderName, res.ChatIntent.Message)
				segments := res.PacedSegments
				if len(segments) <= 1 {
					if !r.IsDead() && ctx.Err() == nil {
						_ = r.QueueIntent(&civquestv1.AgentIntent{
							AgentId: r.agentID,
							Action: &civquestv1.AgentIntent_Chat{
								Chat: res.ChatIntent,
							},
						})
					}
				} else {
					for _, seg := range segments {
						if seg.Delay > 0 {
							timer := time.NewTimer(seg.Delay)
							select {
							case <-ctx.Done():
								timer.Stop()
								return
							case <-timer.C:
							}
						}
						if r.IsDead() || ctx.Err() != nil {
							return
						}
						_ = r.QueueIntent(&civquestv1.AgentIntent{
							AgentId: r.agentID,
							Action: &civquestv1.AgentIntent_Chat{
								Chat: &civquestv1.SendChatIntent{
									Message:       seg.Text,
									TargetChannel: res.ChatIntent.TargetChannel,
								},
							},
						})
					}
				}
			}

			// Update companion intimacy with prestige delta and dialogue turn
			if compSvc != nil && res != nil {
				_, _ = compSvc.RecordDialogueInteraction(ctx, c.SenderUuid, c.SenderName, res.Response.PrestigeDelta, worldTick)
			}
		}()
	}

	// 2. Dispatch pending trade asynchronously (prevent duplicate concurrent negotiations and debounce identical proposals)
	if pendingTrade != nil {
		r.mu.Lock()
		debounced := pendingTrade.PlayerUuid == r.lastTradePlayerUUID &&
			((worldTick >= r.lastTradeTick && worldTick-r.lastTradeTick < 100) || time.Since(r.lastTradeAttemptAt) < 5*time.Second)
		if !debounced {
			r.lastTradePlayerUUID = pendingTrade.PlayerUuid
			r.lastTradeTick = worldTick
			r.lastTradeAttemptAt = time.Now()
		}
		r.mu.Unlock()

		if !debounced && r.tradeActive.CompareAndSwap(false, true) {
			r.mu.RLock()
			if r.status != "RUNNING" || r.isDead || r.dialogueService == nil {
				r.mu.RUnlock()
				r.tradeActive.Store(false)
				return
			}
			r.dialogueWg.Add(1)
			r.mu.RUnlock()

			trade := pendingTrade
			go func() {
				defer r.dialogueWg.Done()
				defer r.tradeActive.Store(false)
				select {
				case <-ctx.Done():
					return
				default:
				}

				req := system2.TradeDialogueRequest{
					PlayerEntityID: trade.PlayerEntityId,
					PlayerUUID:     trade.PlayerUuid,
					PlayerName:     trade.PlayerName,
					OfferedItems:   trade.PlayerOfferedItems,
					SpokenProposal: trade.SpokenProposal,
					WorldTick:      worldTick,
					Vitals:         vitals,
					SurplusState:   system2.SurplusStable,
				}
				res, err := svc.ProcessTrade(ctx, req)
				if err != nil {
					log.Printf("[WARN] agent %s: dialogue ProcessTrade failed: %v", r.agentID, err)
					return
				}
				if res != nil && res.TradeIntent != nil {
					if !r.IsDead() && ctx.Err() == nil {
						_ = r.QueueIntent(&civquestv1.AgentIntent{
							AgentId: r.agentID,
							Action: &civquestv1.AgentIntent_TradeResponse{
								TradeResponse: res.TradeIntent,
							},
						})
					}
				}
			}()
		}
	}
}

// dispatchChronicler evaluates conditions for the CHRONICLER profession/hobby:
// triggering narrative synthesis of unchronicled memories at dusk (ticks 11000–13000)
// or when near a lectern / chiseled bookshelf, dispatching CreateWrittenBookIntent.
func (r *AgentRuntime) dispatchChronicler(frame *civquestv1.TelemetryFrame) {
	if frame == nil {
		return
	}

	r.mu.RLock()
	if r.status != "RUNNING" || r.isDead || r.chroniclerService == nil || r.agentDB == nil {
		r.mu.RUnlock()
		return
	}

	isChronicler := false
	if r.identityConfig != nil {
		if strings.EqualFold(r.identityConfig.Profession, "CHRONICLER") ||
			strings.EqualFold(r.identityConfig.SecondaryHobby, "CHRONICLER") {
			isChronicler = true
		}
	}
	if !isChronicler {
		r.mu.RUnlock()
		return
	}

	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDusk := tod >= 11000 && tod <= 13000

	now := time.Now()
	if now.Sub(r.lastChronicleAttempt) < 60*time.Second || (r.lastChronicleTick > 0 && frame.WorldTick-r.lastChronicleTick < 600) {
		r.mu.RUnlock()
		return
	}

	var targetBlock *civquestv1.BlockPosProto
	for _, b := range frame.InterestingBlocks {
		if b != nil && b.Pos != nil {
			id := strings.ToLower(b.BlockId)
			if strings.Contains(id, "lectern") || strings.Contains(id, "chiseled_bookshelf") {
				targetBlock = b.Pos
				break
			}
		}
	}

	if !isDusk && targetBlock == nil {
		r.mu.RUnlock()
		return
	}

	if !r.chroniclerActive.CompareAndSwap(false, true) {
		r.mu.RUnlock()
		return
	}

	r.dialogueWg.Add(1)
	r.mu.RUnlock()

	r.mu.Lock()
	r.lastChronicleAttempt = now
	r.lastChronicleTick = frame.WorldTick
	r.mu.Unlock()

	go func() {
		defer func() {
			r.chroniclerActive.Store(false)
			r.dialogueWg.Done()
		}()

		r.mu.RLock()
		ctx := r.ctx
		if ctx == nil || ctx.Err() != nil {
			r.mu.RUnlock()
			return
		}
		author := r.agentID
		if r.identityConfig != nil && r.identityConfig.Name != "" {
			author = r.identityConfig.Name
		}
		clanID := r.clanID
		r.mu.RUnlock()

		reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()

		req := system2.ChronicleRequest{
			AgentID:              r.agentID,
			ClanID:               clanID,
			AuthorName:           author,
			MinSaliency:          0.70,
			Limit:                10,
			TargetLecternOrShelf: targetBlock,
		}

		chronicle, err := r.chroniclerService.ComposeChronicle(reqCtx, r.agentDB, req)
		if err != nil {
			if !errors.Is(err, system2.ErrNoUnchronicledMemories) && !errors.Is(err, context.Canceled) {
				log.Printf("[WARN] agent %s chronicler compose failed: %v", author, err)
			}
			return
		}

		log.Printf("[civquest-agent] Chronicler %s composed book %q (%d pages)",
			author, chronicle.Title, len(chronicle.Pages))

		if chronicle.Intent != nil {
			if err := r.QueueIntent(chronicle.Intent); err != nil {
				log.Printf("[WARN] agent %s failed to queue chronicle intent: %v", author, err)
			}
		}
	}()
}

// TriggerToponymy invokes the System 2 Toponymy pipeline to name a landmark,
// persist it in landmarks.db, record the discovery in agent episodic memory, and dispatch PlaceSignpostIntent.
func (r *AgentRuntime) TriggerToponymy(ctx context.Context, req system2.ToponymRequest) (*system2.ToponymResult, error) {
	r.mu.RLock()
	toponymySvc := r.toponymyService
	lmDB := r.landmarksDB
	agentID := r.agentID
	clanID := r.clanID
	agentDB := r.agentDB
	isDead := r.isDead
	r.mu.RUnlock()

	if isDead {
		return nil, ErrAgentDead
	}
	if toponymySvc == nil {
		return nil, errors.New("toponymy service not configured")
	}

	if req.AgentID == "" {
		req.AgentID = agentID
	}
	if req.ClanID == "" {
		req.ClanID = clanID
	}
	if req.LandmarksDB == nil {
		req.LandmarksDB = lmDB
	}

	result, err := toponymySvc.GenerateToponym(ctx, req)
	if err != nil {
		return nil, err
	}

	// Persist episodic memory of landmark discovery
	if agentDB != nil {
		memoryDesc := fmt.Sprintf("Named landmark %q (%s) at (%.0f, %.0f, %.0f)",
			result.DisplayName, result.Category, result.Coordinates.X, result.Coordinates.Y, result.Coordinates.Z)
		_, _ = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
			WorldTick:     req.WorldTick,
			EventCategory: "DISCOVERY",
			SummaryText:   memoryDesc,
			SaliencyScore: 0.80,
		}, nil)
	}

	if result.Intent != nil {
		if err := r.QueueIntent(result.Intent); err != nil {
			log.Printf("[WARN] agent %s failed to queue signpost intent: %v", agentID, err)
		}
	}

	return result, nil
}

// dispatchToponymy evaluates exploration conditions:
// triggers when an EXPLORER wanders into uncharted terrain without nearby landmarks (>= 64 blocks away),
// consecrating a new natural or settlement site and placing a signpost.
func (r *AgentRuntime) dispatchToponymy(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return
	}

	r.mu.RLock()
	if r.status != "RUNNING" || r.isDead || r.toponymyService == nil {
		r.mu.RUnlock()
		return
	}

	isExplorer := false
	if r.identityConfig != nil {
		if strings.EqualFold(r.identityConfig.Profession, "EXPLORER") ||
			strings.EqualFold(r.identityConfig.SecondaryHobby, "EXPLORER") {
			isExplorer = true
		}
	}
	if !isExplorer {
		r.mu.RUnlock()
		return
	}

	pos := frame.Status.Position
	now := time.Now()
	// Rate limit: at least 60s and 600 ticks between exploration toponym syntheses
	if now.Sub(r.lastToponymyAttempt) < 60*time.Second || (r.lastToponymyTick > 0 && frame.WorldTick-r.lastToponymyTick < 600) {
		r.mu.RUnlock()
		return
	}

	// Distance check: at least 64 blocks from last toponym location
	if r.lastToponymyPos != nil {
		dx := pos.X - r.lastToponymyPos.X
		dz := pos.Z - r.lastToponymyPos.Z
		if math.Sqrt(dx*dx+dz*dz) < 64.0 {
			r.mu.RUnlock()
			return
		}
	}

	// Check if already in an identified landmark context
	if frame.CurrentLandmark != nil && frame.CurrentLandmark.DisplayName != "" {
		r.mu.RUnlock()
		return
	}

	if !r.toponymyActive.CompareAndSwap(false, true) {
		r.mu.RUnlock()
		return
	}

	r.dialogueWg.Add(1)
	r.mu.RUnlock()

	r.mu.Lock()
	r.lastToponymyAttempt = now
	r.lastToponymyTick = frame.WorldTick
	r.lastToponymyPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	r.mu.Unlock()

	go func() {
		defer func() {
			r.toponymyActive.Store(false)
			r.dialogueWg.Done()
		}()

		r.mu.RLock()
		ctx := r.ctx
		if ctx == nil || ctx.Err() != nil {
			r.mu.RUnlock()
			return
		}
		toponymySvc := r.toponymyService
		lmDB := r.landmarksDB
		agentID := r.agentID
		clanID := r.clanID
		agentDB := r.agentDB
		r.mu.RUnlock()

		reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()

		// Asynchronously verify landmarksDB does not already have a nearby landmark within 64 blocks
		if lmDB != nil {
			nearest, _ := lmDB.FindNearestLandmarkContext(reqCtx, pos.X, pos.Y, pos.Z, "")
			if nearest != nil {
				dx := pos.X - nearest.X
				dz := pos.Z - nearest.Z
				if math.Sqrt(dx*dx+dz*dz) < 64.0 {
					return
				}
			}
		}

		biome := frame.BiomeId
		if biome == "" {
			biome = "frontier"
		}

		req := system2.ToponymRequest{
			AgentID:  agentID,
			ClanID:   clanID,
			Category: db.LandmarkCategoryNatural,
			Coordinates: system2.Vec3Coords{
				X: pos.X,
				Y: pos.Y,
				Z: pos.Z,
			},
			ContextPrompt: fmt.Sprintf("Explored frontier biome %s", biome),
			WorldTick:     frame.WorldTick,
			LandmarksDB:   lmDB,
		}

		res, err := toponymySvc.GenerateToponym(reqCtx, req)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("[WARN] agent %s toponym generation failed: %v", agentID, err)
			}
			return
		}

		log.Printf("[civquest-agent] Toponymist %s named landmark %q (%s) at (%.1f, %.1f, %.1f)",
			agentID, res.DisplayName, res.Category, pos.X, pos.Y, pos.Z)

		if agentDB != nil {
			_, _ = agentDB.InsertEpisodicMemory(reqCtx, db.EpisodicMemory{
				WorldTick:     frame.WorldTick,
				EventCategory: "DISCOVERY",
				SummaryText:   fmt.Sprintf("Discovered and named %s at (%.0f, %.0f, %.0f)", res.DisplayName, pos.X, pos.Y, pos.Z),
				SaliencyScore: 0.80,
			}, nil)
		}

		if res.Intent != nil {
			if err := r.QueueIntent(res.Intent); err != nil {
				log.Printf("[WARN] agent %s failed to queue signpost intent: %v", agentID, err)
			}
		}
	}()
}

// loadSafeNodes pre-loads topological nodes with SafetyScore >= 0.7 from map_graph.db into in-memory cache.
func (r *AgentRuntime) loadSafeNodes() {
	if r.agentDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	nodes, _, err := r.agentDB.GetMapGraph(ctx)
	if err == nil {
		var safe []db.MapNode
		for _, n := range nodes {
			if n.SafetyScore >= 0.7 {
				safe = append(safe, n)
			}
		}
		r.mu.Lock()
		r.safeNodesCache = safe
		r.mu.Unlock()
	}
}

// SetSafeNodes sets cached safe nodes for flee calculations using a defensive copy.
func (r *AgentRuntime) SetSafeNodes(nodes []db.MapNode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if nodes == nil {
		r.safeNodesCache = nil
		return
	}
	cp := make([]db.MapNode, len(nodes))
	copy(cp, nodes)
	r.safeNodesCache = cp
}

// SafeNodes returns a defensive copy of currently cached safe nodes.
func (r *AgentRuntime) SafeNodes() []db.MapNode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.safeNodesCache == nil {
		return nil
	}
	res := make([]db.MapNode, len(r.safeNodesCache))
	copy(res, r.safeNodesCache)
	return res
}

// evaluateCompanion evaluates proximity, shared travel accumulation, combat aid,
// and follow navigation towards the bonded player in <1ms without token costs.
func (r *AgentRuntime) evaluateCompanion(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || r.IsDead() {
		return
	}

	r.mu.RLock()
	compSvc := r.companionService
	s1State := r.system1State
	ctx := r.ctx
	r.mu.RUnlock()

	if compSvc == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Sync companion active state with System 1 state (for tavern/bed overrides)
	isActive := compSvc.IsActive()
	if s1State != nil {
		s1State.SetCompanionActive(isActive)
	}

	agentPos := frame.Status.Position
	if agentPos == nil {
		return
	}

	// 2. Locate bonded player or nearest player in VisibleEntities
	bondedUUID, _ := compSvc.GetBondedPlayer()
	var playerEntity *civquestv1.VisibleEntity

	for _, e := range frame.VisibleEntities {
		if e == nil {
			continue
		}
		if bondedUUID != "" {
			if e.Uuid == bondedUUID {
				playerEntity = e
				break
			}
		} else if (strings.EqualFold(e.EntityType, "PLAYER") || strings.EqualFold(e.EntityType, "minecraft:player")) && playerEntity == nil {
			playerEntity = e
		}
	}

	if playerEntity == nil || playerEntity.Position == nil {
		return
	}

	pPos := playerEntity.Position
	pUUID := playerEntity.Uuid
	pName := playerEntity.DisplayName
	if pName == "" {
		pName = playerEntity.EntityType
	}

	// 3. Accumulate shared travel distance when player is nearby (<16 blocks)
	_, _, _ = compSvc.RecordSharedTravel(ctx, pUUID, pName, pPos, agentPos, frame.WorldTick)

	// 4. Combat aid: if agent took damage near the player, record combat aid
	if frame.LastDamageTaken != nil {
		dx := pPos.X - agentPos.X
		dy := pPos.Y - agentPos.Y
		dz := pPos.Z - agentPos.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist <= system2.MaxCompanionTravelRadius {
			_, _ = compSvc.RecordCombatAid(ctx, pUUID, pName, frame.WorldTick)
		}
	}

	// 5. Follow navigation: when in FOLLOW mode and distance > 4 blocks (and <= 48 blocks)
	if compSvc.GetState() == system2.CompanionStateFollow {
		shouldNav, navIntent := compSvc.EvaluateFollowNavigation(agentPos, pPos)
		if shouldNav && navIntent != nil {
			r.mu.Lock()
			skipNav := false
			if r.lastFollowNavTarget != nil {
				dx := navIntent.Target.X - r.lastFollowNavTarget.X
				dy := navIntent.Target.Y - r.lastFollowNavTarget.Y
				dz := navIntent.Target.Z - r.lastFollowNavTarget.Z
				distTarget := math.Sqrt(dx*dx + dy*dy + dz*dz)
				ticksElapsed := frame.WorldTick - r.lastFollowNavTick
				if distTarget < 3.0 && ticksElapsed <= 15 && ticksElapsed >= 0 {
					skipNav = true
				}
			}
			if !skipNav {
				r.lastFollowNavTarget = &civquestv1.Vec3{
					X: navIntent.Target.X,
					Y: navIntent.Target.Y,
					Z: navIntent.Target.Z,
				}
				r.lastFollowNavTick = frame.WorldTick
			}
			r.mu.Unlock()

			if !skipNav {
				_ = r.QueueIntent(&civquestv1.AgentIntent{
					AgentId: r.agentID,
					Action: &civquestv1.AgentIntent_Navigate{
						Navigate: navIntent,
					},
				})
			}
		}
	}
}

// evaluateSystem1Reflexes evaluates deterministic fight-or-flight combat reflexes,
// daily utility scheduler, biological needs FSM, and road paving reflexes in <5ms.
func (r *AgentRuntime) evaluateSystem1Reflexes(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || r.IsDead() {
		return
	}

	// 1. Evaluate Combat Reflexes
	hostilesPresent := false
	for _, e := range frame.VisibleEntities {
		if system1.IsHostile(e) {
			hostilesPresent = true
			break
		}
	}

	maxHP := float64(frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	lowHealth := float64(frame.Status.Health) <= 0.35*maxHP
	lowFood := frame.Status.FoodLevel <= 6
	damageTaken := frame.LastDamageTaken != nil

	var combatResult system1.ReflexResult
	if hostilesPresent || damageTaken || lowHealth || lowFood {
		bravery := 0.5
		if r.identityConfig != nil {
			bravery = r.identityConfig.Traits.Bravery
		}

		r.mu.RLock()
		var safeNodes []db.MapNode
		if r.safeNodesCache != nil {
			safeNodes = make([]db.MapNode, len(r.safeNodesCache))
			copy(safeNodes, r.safeNodesCache)
		}

		canEat := true
		if r.lastEatingTick > 0 && frame.WorldTick >= r.lastEatingTick && frame.WorldTick-r.lastEatingTick < 32 {
			canEat = false
		}
		r.mu.RUnlock()

		ctx := system1.ReflexContext{
			AgentID:   r.agentID,
			Bravery:   bravery,
			SafeNodes: safeNodes,
			Frame:     frame,
			CanEat:    canEat,
		}

		combatResult = system1.EvaluateReflexes(ctx)
		if combatResult.ShouldAct {
			// MIN-02: Baritone Path Churn Mitigation
			skipFleeNav := false
			if combatResult.IsPanic && combatResult.FleeTarget != nil {
				r.mu.Lock()
				if r.lastFleeTarget != nil {
					dx := combatResult.FleeTarget.X - r.lastFleeTarget.X
					dy := combatResult.FleeTarget.Y - r.lastFleeTarget.Y
					dz := combatResult.FleeTarget.Z - r.lastFleeTarget.Z
					dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
					ticksElapsed := frame.WorldTick - r.lastFleeTick
					if dist < 2.0 && ticksElapsed <= 10 && ticksElapsed >= 0 {
						skipFleeNav = true
					}
				}
				if !skipFleeNav {
					r.lastFleeTarget = &civquestv1.Vec3{
						X: combatResult.FleeTarget.X,
						Y: combatResult.FleeTarget.Y,
						Z: combatResult.FleeTarget.Z,
					}
					r.lastFleeTick = frame.WorldTick
				}
				r.mu.Unlock()
			} else if !combatResult.IsPanic {
				r.mu.Lock()
				r.lastFleeTarget = nil
				r.lastFleeTick = 0
				r.mu.Unlock()
			}

			// Dispatch priority reflex intents; update eating cooldown only if UseItem was queued successfully
			for _, intent := range combatResult.Intents {
				if skipFleeNav && intent.GetNavigate() != nil {
					continue
				}
				err := r.QueuePriorityIntent(intent)
				if err == nil && intent.GetUseItem() != nil && intent.GetUseItem().IsEatingOrDrinking {
					r.mu.Lock()
					r.lastEatingTick = frame.WorldTick
					r.mu.Unlock()
					if r.system1State != nil {
						r.system1State.SetLastEatingTick(frame.WorldTick)
					}
				}
			}
		}
	}

	// 2. Evaluate Daily Utility Schedule
	utilRes := system1.EvaluateUtility(frame, r.identityConfig, r.system1State)
	if utilRes != nil && utilRes.State != "" && utilRes.State != r.lastLoggedRoutine {
		oldRoutine := r.lastLoggedRoutine
		r.lastLoggedRoutine = utilRes.State
		if r.debugLogger != nil {
			r.debugLogger.Emit(frame.WorldTick, "SYSTEM1_ROUTINE", fmt.Sprintf("Routine %s -> %s (%s)", oldRoutine, utilRes.State, utilRes.Reason), map[string]any{
				"previous_routine": oldRoutine,
				"current_routine":  utilRes.State,
				"utility_score":    utilRes.Utility,
				"reason":           utilRes.Reason,
				"time_of_day":      frame.TimeOfDay,
			})
		}
	}

	// If in combat panic, skip biological needs and road paving to prioritize survival
	if combatResult.IsPanic {
		return
	}

	// 3. Evaluate Biological Needs FSM (Hunger & Sleep/Wake)
	needsIntents := system1.EvaluateBiologicalNeeds(r.agentID, frame, r.system1State)
	for _, intent := range needsIntents {
		if intent.GetUseItem() != nil && intent.GetUseItem().IsEatingOrDrinking {
			r.mu.RLock()
			canEat := (r.lastEatingTick == 0 || frame.WorldTick < r.lastEatingTick || frame.WorldTick-r.lastEatingTick >= 32)
			r.mu.RUnlock()
			if canEat {
				err := r.QueuePriorityIntent(intent)
				if err == nil {
					r.mu.Lock()
					r.lastEatingTick = frame.WorldTick
					r.mu.Unlock()
					if r.system1State != nil {
						r.system1State.SetLastEatingTick(frame.WorldTick)
					}
				}
			}
		} else if intent.GetInteractBlock() != nil {
			// Sleep in bed and wake up intents must use standard QueueIntent (not QueuePriorityIntent)
			_ = r.QueueIntent(intent)
		} else if intent.GetNavigate() != nil {
			_ = r.QueueIntent(intent)
		}
	}

	// 4. Evaluate Road Paving Reflex
	if frame.Status != nil && !frame.Status.IsSleeping && r.pavingTracker != nil {
		if paveIntent := r.pavingTracker.EvaluatePaving(r.agentID, frame); paveIntent != nil {
			_ = r.QueueIntent(paveIntent)
		}
	}
}

// startSocketServer establishes the diagnostic UNIX domain socket.
func (r *AgentRuntime) startSocketServer() error {
	dir := filepath.Dir(r.socketPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("creating socket directory %s: %w", dir, err)
	}

	// Remove any stale socket file left behind by previous process
	_ = os.Remove(r.socketPath)

	l, err := net.Listen("unix", r.socketPath)
	if err != nil {
		return fmt.Errorf("listening on unix socket %s: %w", r.socketPath, err)
	}
	_ = os.Chmod(r.socketPath, 0600)

	r.listenerMu.Lock()
	select {
	case <-r.done:
		r.listenerMu.Unlock()
		_ = l.Close()
		_ = os.Remove(r.socketPath)
		return ErrRuntimeStopped
	default:
		r.listener = l
	}
	r.listenerMu.Unlock()

	r.socketWg.Add(1)
	go r.serveSocket(l)
	return nil
}

// stopSocketServer closes the listener and unlinks the socket file.
func (r *AgentRuntime) stopSocketServer() {
	r.listenerMu.Lock()
	l := r.listener
	r.listener = nil
	r.listenerMu.Unlock()

	if l != nil {
		_ = l.Close()
	}

	r.socketWg.Wait()

	if r.socketPath != "" {
		_ = os.Remove(r.socketPath)
	}
}

// serveSocket accepts incoming connections and serves live JSON status snapshots.
func (r *AgentRuntime) serveSocket(l net.Listener) {
	defer r.socketWg.Done()
	defer close(r.socketDone)

	for {
		conn, err := l.Accept()
		if err != nil {
			// Listener was closed
			return
		}

		r.socketWg.Add(1)
		go func(c net.Conn) {
			defer r.socketWg.Done()
			r.handleSocketConn(c)
		}(conn)
	}
}

// handleSocketConn serializes and returns the current status snapshot to a socket client.
func (r *AgentRuntime) handleSocketConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))

	r.mu.RLock()
	statusSnapshot := r.status
	worldTick := int64(0)
	health := float32(0)
	foodLevel := int32(0)
	navState := "NAVIGATION_STATE_UNSPECIFIED"
	var pos *SocketPosition

	if r.latestFrame != nil {
		worldTick = r.latestFrame.WorldTick
		if r.latestFrame.Status != nil {
			health = r.latestFrame.Status.Health
			foodLevel = r.latestFrame.Status.FoodLevel
			navState = r.latestFrame.Status.NavigationState.String()
			if r.latestFrame.Status.Position != nil {
				pos = &SocketPosition{
					X: r.latestFrame.Status.Position.X,
					Y: r.latestFrame.Status.Position.Y,
					Z: r.latestFrame.Status.Position.Z,
				}
			}
		}
	}
	r.mu.RUnlock()

	resp := SocketStatusResponse{
		Agent:           r.agentID,
		Clan:            r.clanID,
		Status:          statusSnapshot,
		PID:             r.pid,
		WorldTick:       worldTick,
		Health:          health,
		FoodLevel:       foodLevel,
		Position:        pos,
		NavigationState: navState,
		UptimeSeconds:   r.UptimeSeconds(),
	}

	data, err := json.Marshal(resp)
	if err == nil {
		data = append(data, '\n')
		_, _ = conn.Write(data)
	}
}
