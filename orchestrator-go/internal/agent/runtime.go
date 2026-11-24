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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/memory"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/roles/agrarian"
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
	ConnectAgent(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[civquestv1.IntentBatch, civquestv1.TelemetryFrame], error)
}

// Config provides initial configuration options for constructing an AgentRuntime.
type Config struct {
	AgentID              string
	AgentUUID            string
	ClanID               string
	IdentityConfig       *xdg.AgentIdentityConfig
	Resolver             *xdg.Resolver
	AgentDB              *db.AgentDB
	ReputationDB         *db.ReputationDB
	MemoryPipeline       *memory.Pipeline
	BridgeClient         *bridge.Client
	StreamConnector      StreamConnector
	SocketPath           string
	IntentQueueSize      int
	OnDeath              func(*civquestv1.DeathEvent)
	DialogueService      *system2.DialogueService
	ChroniclerService    *system2.ChroniclerService
	ToponymyService      *system2.ToponymyService
	CompanionService     *system2.CompanionService
	LandmarksDB          *db.LandmarksDB
	ContractsDB          *db.ContractsDB
	SharecroppingEngine  *agrarian.SharecroppingEngine
	CivicRegistryDB      *db.CivicRegistryDB
	RumorsDB             *db.RumorsDB
	DiplomacyDB          *db.DiplomacyDB
	ContemplationService *system2.ContemplationService
}

// SocketPosition represents 3D coordinates in the diagnostic socket payload.
type SocketPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// System1UtilityScores represents real-time System 1 reactive utility and drive scores.
type System1UtilityScores struct {
	Food        float64 `json:"food"`
	Hunger      float64 `json:"hunger"`
	Threat      float64 `json:"threat"`
	Confidence  float64 `json:"confidence"`
	Exploration float64 `json:"exploration"`
	SleepNeed   float64 `json:"sleep_need"`
}

// SocketStatusResponse represents the JSON payload served over the per-agent UNIX domain socket.
type SocketStatusResponse struct {
	Agent                 string                 `json:"agent"`
	Clan                  string                 `json:"clan"`
	Status                string                 `json:"status"`
	PID                   int                    `json:"pid"`
	WorldTick             int64                  `json:"world_tick"`
	Health                float32                `json:"health"`
	FoodLevel             int32                  `json:"food_level"`
	Position              *SocketPosition        `json:"position,omitempty"`
	NavigationState       string                 `json:"navigation_state"`
	NavigationDestination *SocketPosition        `json:"navigation_destination,omitempty"`
	UptimeSeconds         float64                `json:"uptime_seconds"`
	MemoryRSS             uint64                 `json:"memory_rss"`
	MemoryRSSMB           float64                `json:"memory_rss_mb"`
	FrameLatencyMs        float64                `json:"frame_latency_ms"`
	ActiveLLMRequests     int32                  `json:"active_llm_requests"`
	UtilityScores         *System1UtilityScores  `json:"utility_scores,omitempty"`
	Goals                 *system1.GoalHierarchy `json:"goals,omitempty"`

	// Enriched fields for real-time Agent Viewer
	Yaw         float32                   `json:"yaw,omitempty"`
	Pitch       float32                   `json:"pitch,omitempty"`
	MaxHealth   float32                   `json:"max_health,omitempty"`
	Saturation  float32                   `json:"saturation,omitempty"`
	Stamina     float32                   `json:"stamina,omitempty"`
	IsSleeping  bool                      `json:"is_sleeping,omitempty"`
	TimeOfDay   int64                     `json:"time_of_day,omitempty"`
	Weather     string                    `json:"weather,omitempty"`
	BiomeID     string                    `json:"biome_id,omitempty"`
	Stage       string                    `json:"stage,omitempty"`
	Routine     string                    `json:"routine,omitempty"`
	Shift       string                    `json:"shift,omitempty"`
	ArmorRating int32                     `json:"armor_rating,omitempty"`
	StateStack  []system1.StateStackFrame `json:"state_stack,omitempty"`
	Inventory   []CachedItem              `json:"inventory,omitempty"`
	Blackboard  map[string]system1.Fact   `json:"blackboard,omitempty"`
	Reflections *ViewerReflections        `json:"reflections,omitempty"`
}

// ViewerReflections represents recent deliberative System 2 reflections, dialogue, and chronicles.
type ViewerReflections struct {
	LatestThought   string `json:"latest_thought,omitempty"`
	LatestDialogue  string `json:"latest_dialogue,omitempty"`
	LatestChronicle string `json:"latest_chronicle,omitempty"`
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

	// Intent/batch sequence numbering and outbound queue
	sequenceID  atomic.Int64
	nextBatchID atomic.Int64
	slotQueue   *SlotQueue

	// Diagnostics & Socket
	socketPath string
	listenerMu sync.Mutex
	listener   net.Listener
	socketWg   sync.WaitGroup
	pid        int
	startTime  time.Time

	// Diagnostics Telemetry & Metrics
	navDestination     *SocketPosition
	navTargetPOIID     string
	lastHarvestPos     *civquestv1.BlockPosProto
	frameLatencyMicros atomic.Int64
	activeLLMRequests  atomic.Int32

	// State and synchronization
	mu                 sync.RWMutex
	status             string
	latestFrame        *civquestv1.TelemetryFrame
	lastProcessedSeqID int64
	lastReceipt        *civquestv1.IntentReceipt
	isDead             bool
	deathEvent         *civquestv1.DeathEvent
	permadeathResult   *PermadeathResult
	hasCachedInventory bool
	lastCachedItems    []CachedItem
	lastEatingTick     int64
	lastFleeTarget     *civquestv1.Vec3
	lastFleeTick       int64
	lastMotionLogTick  int64

	// System 1 Reflexes and Safe Node Graph Cache
	safeNodesCache        []db.MapNode
	tradeValuations       map[string]db.TradeValuation
	system1State          *system1.AgentState
	agentFSM              *system1.AgentFSM
	pavingTracker         *system1.RoadPavingTracker
	memoryPipeline        *memory.Pipeline
	peerGate              peerConversationGate
	lastCombatMemoryTick  int64
	lastCombatSummary     string
	lastMapPersistTick    int64
	lastTradeReassessTick int64

	// System 2 Deliberative Dialogue Pipeline
	dialogueService            *system2.DialogueService
	chroniclerService          *system2.ChroniclerService
	toponymyService            *system2.ToponymyService
	companionService           *system2.CompanionService
	landmarksDB                *db.LandmarksDB
	contractsDB                *db.ContractsDB
	civicRegistryDB            *db.CivicRegistryDB
	rumorsDB                   *db.RumorsDB
	diplomacyDB                *db.DiplomacyDB
	contemplationService       *system2.ContemplationService
	lastContemplatedDay        int64
	lastIdleContemplationTick  int64
	idleStartTick              int64
	hasIdleStartTick           bool
	lastActivePos              [3]float64
	hasLastActivePos           bool
	idleContemplationThreshold int64
	idleContemplationCooldown  int64
	peerReplyJitterDisabled    bool
	contemplationActive        atomic.Bool
	sharecroppingEngine        *agrarian.SharecroppingEngine
	lastChronicleAttempt       time.Time
	lastChronicleTick          int64
	lastToponymyAttempt        time.Time
	lastToponymyTick           int64
	lastToponymyPos            *civquestv1.Vec3
	lastFollowNavTarget        *civquestv1.Vec3
	lastFollowNavTick          int64
	dialogueWg                 sync.WaitGroup
	poiWg                      sync.WaitGroup
	tradeActive                atomic.Bool
	chroniclerActive           atomic.Bool
	toponymyActive             atomic.Bool
	lastTradeTick              int64
	lastTradePlayerUUID        string
	lastTradeAttemptAt         time.Time
	debugLogger                *DebugLogger
	lastLoggedRoutine          system1.RoutineState
	lastLoggedGoal             string
	latestThought              string
	latestDialogue             string
	latestChronicle            string
	clanSizeResolved           bool
	lastClanSizeTick           int64

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

	// ADR-027: Pioneer Genesis Invariant: every agent initializes in stage BOOTSTRAP as EXPLORER.
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(agentID)
	} else {
		if strings.TrimSpace(cfg.IdentityConfig.Profession) == "" {
			cfg.IdentityConfig.Profession = "EXPLORER"
		}
		if strings.TrimSpace(cfg.IdentityConfig.Stage) == "" {
			cfg.IdentityConfig.Stage = "BOOTSTRAP"
		}
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

	pavingTracker := system1.NewRoadPavingTracker(1000, system1.DefaultPavingThreshold)
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

	memPipe := cfg.MemoryPipeline
	if memPipe == nil && cfg.AgentDB != nil {
		memPipe = memory.NewPipeline(cfg.AgentDB, memory.NewResilientEmbedder(nil))
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

	var agentFSM *system1.AgentFSM
	agentFSM = system1.NewAgentFSM(system1.StateBootstrap, func(from, to system1.State, reason string) {
		if debugLog != nil {
			debugLog.Emit(0, "FSM_TRANSITION", fmt.Sprintf("FSM %s -> %s (%s)", from, to, reason), map[string]any{
				"from_state": from,
				"to_state":   to,
				"reason":     reason,
			})
		}
	})

	runtime := &AgentRuntime{
		agentID:           agentID,
		agentUUID:         agentUUID,
		clanID:            clanID,
		identityConfig:    cfg.IdentityConfig,
		resolver:          r,
		agentDB:           cfg.AgentDB,
		reputationDB:      cfg.ReputationDB,
		memoryPipeline:    memPipe,
		bridgeClient:      cfg.BridgeClient,
		connector:         connector,
		dialogueService:   cfg.DialogueService,
		chroniclerService: cfg.ChroniclerService,
		toponymyService:   cfg.ToponymyService,
		companionService:  companionService,
		landmarksDB:       cfg.LandmarksDB,
		contractsDB:       cfg.ContractsDB,
		civicRegistryDB:   cfg.CivicRegistryDB,
		rumorsDB:          cfg.RumorsDB,
		diplomacyDB:       cfg.DiplomacyDB,
		contemplationService: func() *system2.ContemplationService {
			if cfg.ContemplationService != nil {
				return cfg.ContemplationService
			}
			if cfg.DialogueService != nil && cfg.DialogueService.Client() != nil {
				return system2.NewContemplationService(cfg.DialogueService.Client())
			}
			return nil
		}(),
		lastContemplatedDay:        -1,
		idleContemplationThreshold: 300,
		idleContemplationCooldown:  3600,
		sharecroppingEngine: func() *agrarian.SharecroppingEngine {
			if cfg.SharecroppingEngine != nil {
				return cfg.SharecroppingEngine
			}
			if cfg.ContractsDB != nil {
				return agrarian.NewSharecroppingEngine(agentID, clanID, cfg.ContractsDB)
			}
			return nil
		}(),
		debugLogger:     debugLog,
		slotQueue:       NewSlotQueue(),
		socketPath:      sockPath,
		pid:             os.Getpid(),
		startTime:       time.Now().UTC(),
		status:          "INITIALIZING",
		onDeath:         cfg.OnDeath,
		done:            make(chan struct{}),
		socketDone:      make(chan struct{}),
		lastCachedItems: nil,
		system1State:    sys1State,
		agentFSM:        agentFSM,
		pavingTracker:   pavingTracker,
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
	runtime.loadTradeValuations()
	runtime.loadSpatialPOIs()

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
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.identityConfig == nil {
		return nil
	}
	cp := *r.identityConfig
	return &cp
}

// Profession returns the agent's current primary profession.
// ADR-027: If no settlement POI is recorded in SpatialIndex, specialized professions
// fall back to EXPLORER.
func (r *AgentRuntime) Profession() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.identityConfig == nil {
		return ""
	}
	prof := r.identityConfig.Profession
	if r.system1State != nil {
		if si := r.system1State.SpatialIndexLocked(); si != nil && !si.HasSettlementPOI(nil, 0) {
			if system1.IsSpecializedProfession(prof) {
				return "EXPLORER"
			}
		}
	}
	return prof
}

// SecondaryHobby returns the agent's current secondary hobby.
func (r *AgentRuntime) SecondaryHobby() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.identityConfig == nil {
		return ""
	}
	return r.identityConfig.SecondaryHobby
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

// CivicRegistryDB returns the clan civic registry database handle.
func (r *AgentRuntime) CivicRegistryDB() *db.CivicRegistryDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.civicRegistryDB
}

// SetCivicRegistryDB assigns the clan civic registry database handle.
func (r *AgentRuntime) SetCivicRegistryDB(c *db.CivicRegistryDB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.civicRegistryDB = c
}

// RumorsDB returns the clan rumors database handle.
func (r *AgentRuntime) RumorsDB() *db.RumorsDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rumorsDB
}

// SetRumorsDB assigns the clan rumors database handle.
func (r *AgentRuntime) SetRumorsDB(rum *db.RumorsDB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rumorsDB = rum
}

// DiplomacyDB returns the clan diplomacy database handle.
func (r *AgentRuntime) DiplomacyDB() *db.DiplomacyDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.diplomacyDB
}

// SetDiplomacyDB assigns the clan diplomacy database handle.
func (r *AgentRuntime) SetDiplomacyDB(d *db.DiplomacyDB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.diplomacyDB = d
}

// ContemplationService returns the System 2 Contemplation service if configured.
func (r *AgentRuntime) ContemplationService() *system2.ContemplationService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contemplationService
}

// SetContemplationService assigns or updates the System 2 Contemplation service.
func (r *AgentRuntime) SetContemplationService(c *system2.ContemplationService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contemplationService = c
}

// SetPeerReplyJitterDisabled configures whether staggered peer-reply jitter is disabled (for deterministic tests).
func (r *AgentRuntime) SetPeerReplyJitterDisabled(disabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peerReplyJitterDisabled = disabled
}

func (r *AgentRuntime) peerReplyDelay(listenerName string) time.Duration {
	r.mu.RLock()
	disabled := r.peerReplyJitterDisabled
	r.mu.RUnlock()
	if disabled {
		return 0
	}
	return peerReplyJitterDelay(listenerName)
}

// LastContemplatedDay returns the last in-game day on which contemplation completed.
func (r *AgentRuntime) LastContemplatedDay() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastContemplatedDay
}

// UpdateGoals updates the immediate, short-term, and long-term goals thread-safely
// across identity config and volatile system1 state.
func (r *AgentRuntime) UpdateGoals(immediate, shortTerm, longTerm string) {
	r.mu.Lock()
	if r.identityConfig != nil {
		r.identityConfig.ImmediateGoal = immediate
		r.identityConfig.ShortTermGoal = shortTerm
		r.identityConfig.LongTermGoal = longTerm
	}
	cfgCopy := r.identityConfig
	resolver := r.resolver
	agentID := r.agentID
	r.mu.Unlock()

	if r.system1State != nil {
		r.system1State.SetGoals(system1.GoalHierarchy{
			ImmediateGoal: immediate,
			ShortTermGoal: shortTerm,
			LongTermGoal:  longTerm,
		})
	}

	if resolver != nil && cfgCopy != nil {
		_ = xdg.SaveAgentConfig(cfgCopy, resolver.AgentConfigPath(agentID))
	}
}

// Goals returns the current immediate, short-term, and long-term goals thread-safely.
func (r *AgentRuntime) Goals() (string, string, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.identityConfig != nil && (r.identityConfig.ImmediateGoal != "" || r.identityConfig.ShortTermGoal != "" || r.identityConfig.LongTermGoal != "") {
		return r.identityConfig.ImmediateGoal, r.identityConfig.ShortTermGoal, r.identityConfig.LongTermGoal
	}
	if r.system1State != nil {
		gh := r.system1State.GetGoals()
		return gh.ImmediateGoal, gh.ShortTermGoal, gh.LongTermGoal
	}
	return "", "", ""
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

// Stage returns the active Tier 1 macro strategic stage.
func (r *AgentRuntime) Stage() system1.Stage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.agentFSM != nil {
		return r.agentFSM.CurrentStage()
	}
	if r.system1State != nil {
		return r.system1State.Stage()
	}
	return system1.StageBootstrap
}

// Routine returns the active Tier 2 circadian operational routine.
func (r *AgentRuntime) Routine() system1.Routine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.agentFSM != nil {
		return r.agentFSM.CurrentRoutine()
	}
	if r.system1State != nil {
		return r.system1State.Routine()
	}
	return system1.RoutineDawnPrep
}

// CurrentRoutine returns the current System 1 routine state (backward compatibility).
func (r *AgentRuntime) CurrentRoutine() system1.RoutineState {
	return r.Routine()
}

// AgentFSM returns the System 1 Hierarchical Finite State Machine instance.
func (r *AgentRuntime) AgentFSM() *system1.AgentFSM {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agentFSM
}

// FSMState returns the active formal System 1 HFSM state (backward compatibility).
func (r *AgentRuntime) FSMState() system1.State {
	return r.Stage()
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

// LastReceipt returns a defensive copy of the most recent IntentReceipt received from the server.
func (r *AgentRuntime) LastReceipt() *civquestv1.IntentReceipt {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.lastReceipt == nil {
		return nil
	}
	return proto.Clone(r.lastReceipt).(*civquestv1.IntentReceipt)
}

// SetLastReceipt stores a defensive copy of the latest IntentReceipt received from the server.
func (r *AgentRuntime) SetLastReceipt(receipt *civquestv1.IntentReceipt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if receipt == nil {
		r.lastReceipt = nil
		return
	}
	r.lastReceipt = proto.Clone(receipt).(*civquestv1.IntentReceipt)
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

// SetNavigationDestination updates the current Baritone navigation destination target.
func (r *AgentRuntime) SetNavigationDestination(dest *SocketPosition) {
	r.mu.Lock()
	r.navDestination = dest
	r.mu.Unlock()
}

// NavigationDestination returns the current Baritone navigation destination target, if any.
func (r *AgentRuntime) NavigationDestination() *SocketPosition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.navDestination == nil {
		return nil
	}
	cp := *r.navDestination
	return &cp
}

// SetFrameLatency sets the gRPC frame latency in milliseconds.
func (r *AgentRuntime) SetFrameLatency(ms float64) {
	r.frameLatencyMicros.Store(int64(ms * 1000.0))
}

// FrameLatency returns the latest gRPC frame latency in milliseconds.
func (r *AgentRuntime) FrameLatency() float64 {
	return float64(r.frameLatencyMicros.Load()) / 1000.0
}

// ActiveLLMRequests returns the number of currently active System 2 LLM requests.
func (r *AgentRuntime) ActiveLLMRequests() int32 {
	count := r.activeLLMRequests.Load()
	if count < 0 {
		return 0
	}
	return count
}

// IncrementActiveLLM increments the active System 2 LLM request counter.
func (r *AgentRuntime) IncrementActiveLLM() int32 {
	return r.activeLLMRequests.Add(1)
}

// DecrementActiveLLM decrements the active System 2 LLM request counter.
func (r *AgentRuntime) DecrementActiveLLM() int32 {
	return r.activeLLMRequests.Add(-1)
}

// CurrentUtilityScores evaluates the current System 1 utility scores synchronously in <0.1ms.
func (r *AgentRuntime) CurrentUtilityScores() *System1UtilityScores {
	r.mu.RLock()
	frame := r.latestFrame
	cfg := r.identityConfig
	r.mu.RUnlock()

	bravery := 0.5
	curiosity := 0.5
	if cfg != nil {
		bravery = cfg.Traits.Bravery
		curiosity = cfg.Traits.Curiosity
	}

	scores := &System1UtilityScores{
		Food:        20.0,
		Hunger:      0.0,
		Threat:      0.0,
		Confidence:  1.0,
		Exploration: curiosity,
		SleepNeed:   0.0,
	}

	if frame == nil || frame.Status == nil {
		return scores
	}

	// Food and Hunger
	foodLevel := float64(frame.Status.FoodLevel)
	scores.Food = foodLevel
	if foodLevel <= 6 {
		scores.Hunger = 1.0
	} else {
		scores.Hunger = math.Round(((20.0-foodLevel)/14.0)*100.0) / 100.0
		if scores.Hunger < 0 {
			scores.Hunger = 0
		}
	}

	// Threat and Confidence
	_, conf, hostiles := system1.AssessImmediateThreats(frame, bravery)
	scores.Confidence = math.Round(conf*100.0) / 100.0
	if len(hostiles) > 0 {
		var totalThreat float64
		for _, e := range frame.VisibleEntities {
			if e != nil && system1.IsHostile(e) {
				totalThreat += system1.MobThreat(e.EntityType)
			}
		}
		scores.Threat = totalThreat
	} else if frame.LastDamageTaken != nil && !system1.IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
		scores.Threat = system1.ThreatDefault
	}

	// Sleep Need
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	if frame.Status.IsSleeping {
		scores.SleepNeed = 1.0
	} else if frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		scores.SleepNeed = 0.90
	} else if tod >= 13000 && tod < 23500 {
		scores.SleepNeed = 0.90
	} else if tod >= 11000 && tod < 13000 {
		scores.SleepNeed = math.Round((0.50+float64(tod-11000)/2000.0*0.40)*100.0) / 100.0
	} else {
		scores.SleepNeed = math.Round((float64(tod)/24000.0*0.40)*100.0) / 100.0
	}

	// Exploration
	if tod >= 1000 && tod < 11000 {
		scores.Exploration = math.Round((curiosity*0.8+0.2)*100.0) / 100.0
	} else {
		scores.Exploration = math.Round((curiosity*0.4)*100.0) / 100.0
	}

	return scores
}

// ReadProcessMemoryRSS returns the current resident set size (RSS) in bytes.
// It inspects /proc/self/statm on Linux with fallback to Go runtime.MemStats.
func ReadProcessMemoryRSS() uint64 {
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil && pages > 0 {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.Sys > 0 {
		return ms.Sys
	}
	return ms.Alloc
}

// QueueIntent enqueues an AgentIntent into the outbound SlotQueue.
// If the intent's SequenceId is <= 0, it is automatically assigned via atomic increment.
// If the intent's AgentId is blank, it defaults to the runtime's AgentID.
// Typed slot intents coalesce with overwrite semantics, eliminating backpressure.
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

	if nav := intent.GetNavigate(); nav != nil && nav.Target != nil {
		r.prepareNavigateIntent(intent)
	}

	if ib := intent.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK && ib.Pos != nil {
		r.mu.Lock()
		r.lastHarvestPos = &civquestv1.BlockPosProto{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}
		r.mu.Unlock()
	}

	r.slotQueue.Put(false, intent)
	r.recordPlacedBlockPOI(intent)
	r.PruneHarvestedBlockPOI(intent)
	if r.debugLogger != nil {
		r.debugLogger.Emit(r.WorldTick(), "INTENT_OUT", intentActionName(intent), map[string]any{
			"sequence_id": intent.SequenceId,
			"action":      intentActionName(intent),
		})
	}
	return nil
}

// EnqueueIntent is an alias for QueueIntent.
func (r *AgentRuntime) EnqueueIntent(intent *civquestv1.AgentIntent) error {
	return r.QueueIntent(intent)
}

// SlotQueue returns the outbound SlotQueue.
func (r *AgentRuntime) SlotQueue() *SlotQueue {
	return r.slotQueue
}

// DrainIntents drains all pending intents from the SlotQueue.
func (r *AgentRuntime) DrainIntents() []*civquestv1.AgentIntent {
	return r.slotQueue.Drain()
}

// IntentQueue returns a channel containing all currently pending intents drained from the SlotQueue.
// Kept for backward compatibility with test harnesses and evaluators.
func (r *AgentRuntime) IntentQueue() <-chan *civquestv1.AgentIntent {
	intents := r.slotQueue.Drain()
	ch := make(chan *civquestv1.AgentIntent, len(intents))
	for _, in := range intents {
		ch <- in
	}
	return ch
}

// QueuePriorityIntent enqueues a critical reflex intent into the outbound SlotQueue with high priority.
// Priority intents are drained before normal intents. If a pending intent of the same slot type
// exists, it is overwritten while preserving priority dispatch ordering.
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

	if nav := intent.GetNavigate(); nav != nil && nav.Target != nil {
		r.prepareNavigateIntent(intent)
	}

	if ib := intent.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK && ib.Pos != nil {
		r.mu.Lock()
		r.lastHarvestPos = &civquestv1.BlockPosProto{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}
		r.mu.Unlock()
	}

	r.slotQueue.Put(true, intent)
	r.recordPlacedBlockPOI(intent)
	r.PruneHarvestedBlockPOI(intent)
	if r.debugLogger != nil {
		r.debugLogger.Emit(r.WorldTick(), "INTENT_OUT", intentActionName(intent), map[string]any{
			"sequence_id": intent.SequenceId,
			"action":      intentActionName(intent),
			"priority":    true,
		})
	}
	return nil
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
	case *civquestv1.AgentIntent_InteractBlock:
		return fmt.Sprintf("InteractBlockIntent(type=%s,id=%s)", a.InteractBlock.InteractionType.String(), a.InteractBlock.BlockOrItemId)
	case *civquestv1.AgentIntent_CraftItem:
		return fmt.Sprintf("CraftItemIntent(recipe=%s)", a.CraftItem.RecipeId)
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

	// 3. Send initial handshake IntentBatch so server StreamRegistry registers this agent ID immediately
	handshake := &civquestv1.IntentBatch{
		AgentId: r.agentID,
		BatchId: r.nextBatchID.Add(1),
		Intents: []*civquestv1.AgentIntent{
			{
				AgentId:    r.agentID,
				SequenceId: r.sequenceID.Add(1),
			},
		},
	}
	if err := stream.Send(handshake); err != nil {
		r.Stop()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to send handshake intent batch: %w", err)
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

	// 3b. Sync dynamic runtime crafting recipe catalog from server (ADR-029 §4)
	_ = r.syncRecipeGraph(streamCtx, r.bridgeClient)

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
		r.contemplationActive.Store(false)
		r.dialogueWg.Wait()
		r.poiWg.Wait()

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

// sendLoop reads queued AgentIntents from SlotQueue, wraps them in an IntentBatch, and writes them to the gRPC stream.
// Coalesced slot intents (attacks, navigation, items, crafting, interactions) are dispatched
// immediately with priority ordering. Ordered PlaceBlockIntents bypass the slot queue and are
// dispatched through a 4-tick rate limiter to preserve spatial construction sequencing.
func (r *AgentRuntime) sendLoop(ctx context.Context, cancel context.CancelFunc, stream grpc.BidiStreamingClient[civquestv1.IntentBatch, civquestv1.TelemetryFrame], errCh chan<- error) {
	var sendErr error
	defer func() {
		errCh <- sendErr
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var lastPlaceBlockTick int64 = -100
	var lastPlaceBlockTime time.Time
	const placeBlockRateLimitTicks = 4
	const placeBlockRateLimitDuration = 200 * time.Millisecond

	dispatch := func() bool {
		// 1. Drain all coalesced slot intents (priority-first)
		slotIntents := r.slotQueue.DrainSlots()
		intents := make([]*civquestv1.AgentIntent, 0, len(slotIntents)+1)
		intents = append(intents, slotIntents...)

		// 2. Dispatch ordered PlaceBlockIntents through the 4-tick rate limiter
		if r.slotQueue.PlaceBlockCount() > 0 {
			curTick := r.WorldTick()
			now := time.Now()
			canDispatch := false
			if curTick > 0 && lastPlaceBlockTick >= 0 {
				if curTick-lastPlaceBlockTick >= placeBlockRateLimitTicks {
					canDispatch = true
				}
			} else if !lastPlaceBlockTime.IsZero() {
				if now.Sub(lastPlaceBlockTime) >= placeBlockRateLimitDuration {
					canDispatch = true
				}
			} else {
				canDispatch = true
			}

			if canDispatch {
				if pbIntent := r.slotQueue.PopPlaceBlock(); pbIntent != nil {
					intents = append(intents, pbIntent)
					lastPlaceBlockTick = curTick
					lastPlaceBlockTime = now
				}
			}
		}

		flush := r.slotQueue.ConsumeFlushPeriphery()
		if len(intents) == 0 && !flush {
			return true
		}

		batch := &civquestv1.IntentBatch{
			AgentId:        r.agentID,
			BatchId:        r.nextBatchID.Add(1),
			FlushPeriphery: flush,
			Intents:        intents,
		}
		if err := stream.Send(batch); err != nil {
			sendErr = err
			cancel()
			return false
		}
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case <-r.slotQueue.Ready():
			if !dispatch() {
				return
			}
		case <-ticker.C:
			if r.slotQueue.HasPending() {
				if !dispatch() {
					return
				}
			}
		}
	}
}

// recvLoop processes incoming 5 Hz TelemetryFrames from the server.
func (r *AgentRuntime) recvLoop(ctx context.Context, stream grpc.BidiStreamingClient[civquestv1.IntentBatch, civquestv1.TelemetryFrame]) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.done:
			return nil
		default:
		}

		recvStart := time.Now()
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		r.frameLatencyMicros.Store(time.Since(recvStart).Microseconds())

		if receipt := frame.GetReceipt(); receipt != nil {
			r.SetLastReceipt(receipt)
			if r.system1State != nil {
				r.system1State.IngestReceipt(receipt)
			}
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
	if receipt := frame.GetReceipt(); receipt != nil {
		r.lastReceipt = proto.Clone(receipt).(*civquestv1.IntentReceipt)
	}
	if frame.Status != nil && frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
		r.navDestination = nil
		r.navTargetPOIID = ""
	}

	if frame.DeathEvent != nil {
		r.isDead = true
		r.deathEvent = frame.DeathEvent
		r.status = "DEAD"
		deathEv = frame.DeathEvent
	}
	r.mu.Unlock()

	if frame.Status != nil && frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		r.handleNavigationStuck()
	}

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
		r.contemplationActive.Store(false)
		r.dialogueWg.Wait()
		r.poiWg.Wait()

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

	// Ingest periphery execution receipt into System 1 before reflexes evaluate
	if receipt := frame.GetReceipt(); receipt != nil {
		if r.system1State != nil {
			r.system1State.IngestReceipt(receipt)
		}
		if receipt.Status == civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED {
			r.mu.Lock()
			hpos := r.lastHarvestPos
			r.lastHarvestPos = nil
			r.mu.Unlock()
			if hpos != nil {
				r.PruneHarvestedBlockPos(hpos.X, hpos.Y, hpos.Z)
			}
		}
	}

	// Continuously ingest InterestingBlocks into System 1 SpatialIndex (ADR-022)
	if r.system1State != nil {
		if si := r.system1State.SpatialIndex(); si != nil {
			currentY := 64.0
			if frame.Status != nil && frame.Status.Position != nil {
				currentY = frame.Status.Position.Y
			}
			if updatedPOIs := si.IngestSensoryBlocks(frame, currentY); len(updatedPOIs) > 0 {
				r.persistPOIsAsync(updatedPOIs)
			}
			if frame.Status != nil && frame.Status.Position != nil {
				if prunedIDs := si.VerifyCloseRangePOIs(frame.Status.Position, frame.InterestingBlocks, 3.5); len(prunedIDs) > 0 {
					r.invalidatePOIsAsync(prunedIDs)
				}
			}
		}
	}

	// Update habit trails, record breadcrumbs, advance active corridors, and consolidate arrived trails (ADR-023)
	r.updateHabitTrails(frame)

	// Ingest completed 360-degree HorizonSurveyReports and process clan rendezvous sync (ADR-032)
	r.processHorizonSurveyAndRendezvous(frame)

	// Evaluate deterministic System 1 survival reflexes, daily routine, biological needs, and paving (<5 ms)
	r.evaluateSystem1Reflexes(frame)

	// Immediately sync and broadcast clan rendezvous report if arrival occurred on this tick (ADR-032)
	r.syncPendingRendezvous(frame)

	// Dispatch System 2 dialogue and barter haggling asynchronously (non-blocking)
	if len(frame.IncomingChats) > 0 || frame.PendingTrade != nil {
		r.dispatchDialogue(frame)
	}

	// Dispatch System 2 Clan Chronicler synthesis at dusk or near library desks (non-blocking)
	r.dispatchChronicler(frame)

	// Dispatch System 2 Toponymy naming for exploratory landmarks (non-blocking)
	r.dispatchToponymy(frame)

	// Dispatch Agrarian specialization and farmland sharecropping routines (non-blocking)
	r.dispatchAgrarian(frame)

	// Dispatch System 2 Nighttime Strategic Contemplation during sleep hours (ticks 13000-23500) (non-blocking)
	r.dispatchContemplation(frame)
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

	vitals := r.buildDialogueVitals(frame)

	// 1. Dispatch incoming chats asynchronously
	for _, chat := range chats {
		if chat == nil || chat.SenderUuid == r.agentID || (r.identityConfig != nil && chat.SenderName == r.identityConfig.Name) {
			continue
		}

		// Handle deterministic in-game gift/trade notifications without invoking an LLM round-trip
		if strings.HasPrefix(chat.Message, "[DETERMINISTIC_GIFT]") || strings.HasPrefix(chat.Message, "[DETERMINISTIC_TRADE]") {
			r.recordDeterministicTradeEvent(ctx, chat, frame, worldTick)
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

			// ADR-030: record every heard statement in episodic memory so System 2 gains community awareness
			r.ingestPeerChat(ctx, c, frame, worldTick)

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

			// 2. Check for sharecropping tenancy negotiation reply
			r.mu.RLock()
			cropEng := r.sharecroppingEngine
			r.mu.RUnlock()
			if cropEng != nil {
				if contract, reply, err := cropEng.ProcessChatNegotiation(ctx, c, worldTick); err == nil && contract != nil {
					log.Printf("[civquest-agent] Sharecropping contract %s concluded with %s", contract.ContractID, c.SenderName)
					if reply != "" && !r.IsDead() && ctx.Err() == nil {
						_ = r.QueueIntent(&civquestv1.AgentIntent{
							AgentId: r.agentID,
							Action: &civquestv1.AgentIntent_Chat{
								Chat: &civquestv1.SendChatIntent{
									Message:       reply,
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

			// ADR-030 / ADR-031: peer-chat LLM contention relief (election, 30s global + 90s pairwise cooldowns)
			if isPeerAgentChat(c) {
				listenerName := r.agentID
				if r.identityConfig != nil && r.identityConfig.Name != "" {
					listenerName = r.identityConfig.Name
				}
				allowed, gateReason := r.peerGate.AllowPeerChat(listenerName, c.SenderUuid, c.Message, worldTick)
				if !allowed {
					log.Printf("[civquest-agent] %s peer dialogue gated for %s (%s); heard but not replying", r.agentID, c.SenderName, gateReason)
					if r.debugLogger != nil {
						r.debugLogger.Emit(worldTick, "PEER_DIALOGUE_COOLDOWN", fmt.Sprintf("Suppressed reply to %s (%s)", c.SenderName, gateReason), map[string]any{
							"sender_name": c.SenderName,
							"sender_uuid": c.SenderUuid,
							"reason":      gateReason,
						})
					}
					return
				}

				if delay := r.peerReplyDelay(listenerName); delay > 0 {
					timer := time.NewTimer(delay)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}

			r.activeLLMRequests.Add(1)
			defer r.activeLLMRequests.Add(-1)

			var chatVitals *system2.DialogueVitals
			if vitals != nil {
				vCopy := *vitals
				vCopy.PlayerRelativeLocation = formatPlayerRelativeLocation(frame, c)
				chatVitals = &vCopy
			}

			req := system2.ChatDialogueRequest{
				PlayerUUID:   c.SenderUuid,
				PlayerName:   c.SenderName,
				Message:      c.Message,
				Distance:     c.Distance,
				WorldTick:    worldTick,
				Vitals:       chatVitals,
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
					"player_name":    c.SenderName,
					"player_message": c.Message,
					"spoken_reply":   res.Response.SpokenReply,
					"paced_segments": res.PacedSegments,
					"trade_decision": res.Response.TradeDecision,
					"delegated_task": res.Response.DelegatedTask,
					"prestige_delta": res.Response.PrestigeDelta,
					"memory_summary": res.Response.MemorySummary,
					"disobeyed":      res.Disobeyed,
					"disobey_reason": res.DisobeyReason,
					"latency_ms":     chatLatency.Milliseconds(),
					"eval_score":     sample.Score,
					"eval_passed":    sample.Passed,
					"eval_checks":    sample.Checks,
				})
			}

			// Activate companion follow mode when LLM agrees to ESCORT_PLAYER
			if res != nil && !res.Disobeyed && res.Response.DelegatedTask != nil &&
				strings.EqualFold(res.Response.DelegatedTask.TaskType, "ESCORT_PLAYER") && compSvc != nil {
				compSvc.SetBondedPlayer(c.SenderUuid, c.SenderName)
				compSvc.SetState(system2.CompanionStateFollow)
				compSvc.SetWaitPosition(nil)
				if r.system1State != nil {
					r.system1State.SetCompanionActive(true)
				}
				log.Printf("[civquest-agent] %s accepted ESCORT_PLAYER task; now following %s", r.agentID, c.SenderName)
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

	// 2. Dispatch pending trade (System 1 Quid-Pro-Quo evaluator for deterministic offers, or System 2 with System 1 fallback)
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

		isDeterministicOffer := strings.HasPrefix(pendingTrade.SpokenProposal, "[SYSTEM1_TRADE]") ||
			strings.HasPrefix(pendingTrade.SpokenProposal, "[GIFT]")

		if !debounced && (isDeterministicOffer || svc == nil) {
			r.executeSystem1Trade(ctx, pendingTrade, frame, vitals, worldTick)
			return
		}

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

				r.activeLLMRequests.Add(1)
				defer r.activeLLMRequests.Add(-1)

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
					log.Printf("[WARN] agent %s: dialogue ProcessTrade failed (%v); falling back to System 1 trade evaluator", r.agentID, err)
					r.executeSystem1Trade(ctx, trade, frame, vitals, worldTick)
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

		r.activeLLMRequests.Add(1)
		defer r.activeLLMRequests.Add(-1)

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

	r.activeLLMRequests.Add(1)
	defer r.activeLLMRequests.Add(-1)

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

		r.activeLLMRequests.Add(1)
		defer r.activeLLMRequests.Add(-1)

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

// dispatchAgrarian evaluates emergent specialization from gathered ingredients and handles farmland survey events.
func (r *AgentRuntime) dispatchAgrarian(frame *civquestv1.TelemetryFrame) {
	if frame == nil {
		return
	}

	r.mu.RLock()
	isDead := r.isDead
	cropEng := r.sharecroppingEngine
	agentDB := r.agentDB
	ctx := r.ctx
	identity := r.identityConfig
	r.mu.RUnlock()

	if isDead {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Emergent Specialization evaluation
	// ADR-027: Specialized career transitions require a consecrated Settlement POI in SpatialIndex
	hasSettlement := false
	if r.system1State != nil {
		if si := r.system1State.SpatialIndexLocked(); si != nil {
			hasSettlement = si.HasSettlementPOI(nil, 0)
		}
	}
	if hasSettlement && identity != nil && agrarian.IsTransitionableProfession(identity.Profession) {
		specCtx := agrarian.SpecializationContext{
			AgentID:           r.agentID,
			CurrentProfession: identity.Profession,
			WorldTick:         frame.WorldTick,
		}
		if frame.Status != nil && frame.Status.Inventory != nil {
			specCtx.Inventory = frame.Status.Inventory
		}
		if len(frame.InterestingBlocks) > 0 {
			specCtx.InterestingBlocks = frame.InterestingBlocks
		}
		if len(frame.VisibleEntities) > 0 {
			specCtx.NearbyEntities = frame.VisibleEntities
		}

		if res, ok := agrarian.EvaluateSpecialization(specCtx); ok && res.Eligible {
			r.mu.Lock()
			err := agrarian.ApplySpecialization(r.identityConfig, res)
			r.mu.Unlock()
			if err == nil {
				log.Printf("[civquest-agent] Agent %s experienced vocational awakening: %s -> %s (%s)",
					r.agentID, res.CurrentProfession, res.TargetProfession, res.TriggerReason)
				_ = agrarian.RecordSpecializationMemory(ctx, agentDB, r.agentID, res, frame.WorldTick)
			}
		}
	}

	// 2. Farmland Survey Event detection
	if frame.FarmlandSurvey != nil && cropEng != nil && frame.FarmlandSurvey.Bounds != nil && frame.FarmlandSurvey.Bounds.Min != nil {
		chestPos := &civquestv1.BlockPosProto{
			X: frame.FarmlandSurvey.Bounds.Min.X,
			Y: frame.FarmlandSurvey.Bounds.Min.Y + 1,
			Z: frame.FarmlandSurvey.Bounds.Min.Z,
		}
		if proposal, err := cropEng.HandleSurveyEvent(ctx, frame.FarmlandSurvey, chestPos, frame.WorldTick); err == nil && proposal != nil {
			log.Printf("[civquest-agent] Discovered cultivated farmland of %s, proposing sharecropping", proposal.PlayerUUID)
			_ = r.QueueIntent(&civquestv1.AgentIntent{
				AgentId: r.agentID,
				Action: &civquestv1.AgentIntent_Chat{
					Chat: &civquestv1.SendChatIntent{
						Message:       proposal.SpokenOffer,
						TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
					},
				},
			})
		}
	}
}

// dispatchContemplation evaluates conditions for nighttime strategic contemplation (SLEEP routine / ticks 13000-23500)
// or daytime idle-triggered strategic contemplation (ADR-031), running deliberative inference asynchronously.
func (r *AgentRuntime) dispatchContemplation(frame *civquestv1.TelemetryFrame) {
	if frame == nil {
		return
	}

	r.mu.RLock()
	if r.status != "RUNNING" || r.isDead || r.agentDB == nil || (r.contemplationService == nil && r.dialogueService == nil) {
		r.mu.RUnlock()
		return
	}

	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isBedtimeHours := tod >= 13000 && tod <= 23500
	isSleeping := (frame.Status != nil && frame.Status.IsSleeping) ||
		(r.system1State != nil && r.system1State.GetCurrentRoutine() == system1.RoutineSleep)

	// Calculate in-game day index: 24,000 ticks per day
	currentDay := frame.WorldTick / 24000
	if frame.WorldTick < 0 {
		currentDay = (frame.WorldTick - 23999) / 24000
	}

	nightReady := (isBedtimeHours || isSleeping) && r.lastContemplatedDay < currentDay

	inEmergency := (r.agentFSM != nil && r.agentFSM.CurrentStage() == system1.StageEmergency) ||
		(r.system1State != nil && r.system1State.Stage() == system1.StageEmergency)
	hasActiveDirective := false
	if r.system1State != nil {
		_, hasActiveDirective = r.system1State.ActiveStrategicDirective(frame.WorldTick)
	}
	threshold := r.idleContemplationThreshold
	if threshold <= 0 {
		threshold = 300
	}
	cooldown := r.idleContemplationCooldown
	if cooldown <= 0 {
		cooldown = 3600
	}
	idleDurationOK := r.hasIdleStartTick && frame.WorldTick >= r.idleStartTick &&
		(frame.WorldTick-r.idleStartTick) >= threshold
	idleCooldownOK := r.lastIdleContemplationTick == 0 ||
		frame.WorldTick < r.lastIdleContemplationTick ||
		(frame.WorldTick-r.lastIdleContemplationTick) >= cooldown
	daytimeIdleReady := !isBedtimeHours && !isSleeping && !inEmergency &&
		!hasActiveDirective && idleDurationOK && idleCooldownOK

	if !nightReady && !daytimeIdleReady {
		r.mu.RUnlock()
		return
	}

	if !r.contemplationActive.CompareAndSwap(false, true) {
		r.mu.RUnlock()
		return
	}

	r.dialogueWg.Add(1)
	r.mu.RUnlock()

	triggerReason := "nighttime"
	r.mu.Lock()
	if nightReady {
		r.lastContemplatedDay = currentDay
	} else {
		triggerReason = "daytime_idle"
		r.lastIdleContemplationTick = frame.WorldTick
	}
	r.mu.Unlock()

	go func() {
		defer func() {
			r.contemplationActive.Store(false)
			r.dialogueWg.Done()
		}()

		r.mu.RLock()
		ctx := r.ctx
		if ctx == nil || ctx.Err() != nil {
			r.mu.RUnlock()
			return
		}

		contemplator := r.contemplationService
		if contemplator == nil && r.dialogueService != nil && r.dialogueService.Client() != nil {
			contemplator = system2.NewContemplationService(r.dialogueService.Client())
		}
		if contemplator == nil {
			r.mu.RUnlock()
			return
		}

		agentID := r.agentID
		clanID := r.clanID
		agentCfg := r.identityConfig
		agentDB := r.agentDB
		civicDB := r.civicRegistryDB
		rumorsDB := r.rumorsDB
		diploDB := r.diplomacyDB
		repDB := r.reputationDB
		memPipe := r.memoryPipeline
		res := r.resolver
		r.mu.RUnlock()

		// Attempt to open clan DBs if not pre-injected
		var closeCivic, closeRumors, closeDiplo func()
		if civicDB == nil && res != nil && clanID != "" {
			if c, err := db.OpenCivicRegistryDBForClan(res, clanID); err == nil {
				civicDB = c
				closeCivic = func() { _ = c.Close() }
			}
		}
		if closeCivic != nil {
			defer closeCivic()
		}

		if rumorsDB == nil && res != nil && clanID != "" {
			if rum, err := db.OpenRumorsDBForClan(res, clanID); err == nil {
				rumorsDB = rum
				closeRumors = func() { _ = rum.Close() }
			}
		}
		if closeRumors != nil {
			defer closeRumors()
		}

		if diploDB == nil && res != nil && clanID != "" {
			if d, err := db.OpenDiplomacyDBForClan(res, clanID); err == nil {
				diploDB = d
				closeDiplo = func() { _ = d.Close() }
			}
		}
		if closeDiplo != nil {
			defer closeDiplo()
		}

		reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()

		r.activeLLMRequests.Add(1)
		defer r.activeLLMRequests.Add(-1)

		// Build personal profile
		var prof system2.PersonalProfile
		prof.AgentID = agentID
		prof.ClanID = clanID
		if agentCfg != nil {
			prof.Profession = agentCfg.Profession
			prof.SecondaryHobby = agentCfg.SecondaryHobby
			prof.Traits = map[string]float64{
				"bravery":         agentCfg.Traits.Bravery,
				"industriousness": agentCfg.Traits.Industriousness,
				"sociability":     agentCfg.Traits.Sociability,
				"curiosity":       agentCfg.Traits.Curiosity,
				"aggressiveness":  agentCfg.Traits.Aggressiveness,
			}
			prof.SleepSystemPrompt = agentCfg.EffectiveSleepSystemPrompt()
		}
		if prof.SleepSystemPrompt == "" && res != nil {
			if skill, err := res.LoadAgentSkill(agentID); err == nil && skill != "" {
				prof.SleepSystemPrompt = skill
			}
		}
		if frame.Status != nil {
			prof.Vitals.Health = frame.Status.Health
			prof.Vitals.FoodLevel = frame.Status.FoodLevel
			if p := frame.Status.Position; p != nil {
				prof.Vitals.Position = fmt.Sprintf("(%.0f, %.0f, %.0f)", p.X, p.Y, p.Z)
			}
			for _, item := range frame.Status.Inventory {
				if item != nil && item.Count > 0 {
					prof.InventoryItems = append(prof.InventoryItems, fmt.Sprintf("%dx %s", item.Count, item.ItemId))
				}
			}
		}

		ledger, err := system2.AssembleStrategicLedger(reqCtx, agentDB, civicDB, rumorsDB, diploDB, repDB, prof, frame.WorldTick)
		if err != nil {
			log.Printf("[WARN] agent %s failed to assemble strategic ledger: %v", agentID, err)
			return
		}

		r.mu.RLock()
		fsm := r.agentFSM
		sys1State := r.system1State
		r.mu.RUnlock()
		if fsm != nil && fsm.LastFacts() != nil {
			ledger.WithFactGraph(fsm.LastFacts())
		}
		if sys1State != nil {
			ledger.WithHorizonSurveySummary(sys1State.LatestSurveySummary())
		}

		resp, err := contemplator.Contemplate(reqCtx, ledger)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("[WARN] agent %s nighttime strategic contemplation failed: %v", agentID, err)
			}
			return
		}

		// 1. Update goals thread-safely and publish System 1 executable directive (ADR-031)
		r.UpdateGoals(resp.ImmediateGoal, resp.ShortTermGoal, resp.LongTermGoal)
		r.applyContemplationDirective(resp, frame.WorldTick)

		// 2. Update trade valuations in agentDB and runtime cache
		if len(resp.TradeAdjustments) > 0 && agentDB != nil {
			var valuations []db.TradeValuation
			for _, adj := range resp.TradeAdjustments {
				itemID := strings.ToLower(strings.TrimSpace(adj.ItemID))
				if itemID == "" {
					continue
				}
				if !strings.Contains(itemID, ":") {
					itemID = "minecraft:" + itemID
				}
				valuations = append(valuations, db.TradeValuation{
					ItemID:         itemID,
					BaseValue:      math.Max(0.1, math.Min(100.0, adj.BaseValue)),
					NeedMultiplier: math.Max(0.2, math.Min(4.0, adj.NeedMultiplier)),
					MinAcceptRatio: math.Max(0.5, math.Min(1.5, adj.MinAcceptRatio)),
					Rationale:      adj.Rationale,
					UpdatedTick:    frame.WorldTick,
				})
			}
			if len(valuations) > 0 {
				_ = agentDB.UpsertTradeValuationsBatch(reqCtx, valuations)
				r.mu.Lock()
				if r.tradeValuations == nil {
					r.tradeValuations = make(map[string]db.TradeValuation)
				}
				for _, v := range valuations {
					r.tradeValuations[v.ItemID] = v
				}
				r.mu.Unlock()
			}
		}

		// 3. Record ReflectiveMemory into agentDB.InsertEpisodicMemory (saliency >= 0.80)
		if strings.TrimSpace(resp.ReflectiveMemory) != "" {
			r.RecordThought(strings.TrimSpace(resp.ReflectiveMemory))
			if memPipe != nil {
				_, _ = memPipe.Ingest(reqCtx, memory.MemoryEvent{
					WorldTick:        frame.WorldTick,
					Category:         memory.CategoryDiscovery,
					SaliencyScore:    0.85,
					ExplicitSaliency: true,
					SummaryText:      resp.ReflectiveMemory,
				})
			} else if agentDB != nil {
				_, _ = agentDB.InsertEpisodicMemory(reqCtx, db.EpisodicMemory{
					WorldTick:     frame.WorldTick,
					EventCategory: "DISCOVERY",
					SaliencyScore: 0.85,
					SummaryText:   resp.ReflectiveMemory,
				}, nil)
			}
		}

		if r.debugLogger != nil {
			dirAction := ""
			if resp.Directive != nil {
				dirAction = resp.Directive.Action
			}
			r.debugLogger.Emit(frame.WorldTick, "STRATEGIC_CONTEMPLATION",
				fmt.Sprintf("Contemplation complete (%s): %s", triggerReason, resp.ImmediateGoal), map[string]any{
					"trigger":           triggerReason,
					"directive_action":  dirAction,
					"immediate_goal":    resp.ImmediateGoal,
					"short_term_goal":   resp.ShortTermGoal,
					"long_term_goal":    resp.LongTermGoal,
					"reflective_memory": resp.ReflectiveMemory,
					"proposals_count":   len(resp.CivicProposals),
					"trade_adjustments": len(resp.TradeAdjustments),
				})
		}
	}()
}

// TriggerContemplation executes strategic contemplation synchronously for testing and validation.
func (r *AgentRuntime) TriggerContemplation(ctx context.Context, frame *civquestv1.TelemetryFrame) (*system2.StrategicContemplationResponse, error) {
	if frame == nil {
		return nil, errors.New("frame cannot be nil")
	}

	r.mu.RLock()
	contemplator := r.contemplationService
	if contemplator == nil && r.dialogueService != nil && r.dialogueService.Client() != nil {
		contemplator = system2.NewContemplationService(r.dialogueService.Client())
	}
	agentID := r.agentID
	clanID := r.clanID
	agentCfg := r.identityConfig
	agentDB := r.agentDB
	civicDB := r.civicRegistryDB
	rumorsDB := r.rumorsDB
	diploDB := r.diplomacyDB
	repDB := r.reputationDB
	memPipe := r.memoryPipeline
	res := r.resolver
	r.mu.RUnlock()

	if contemplator == nil {
		return nil, errors.New("contemplation service not configured")
	}

	// Attempt to open clan DBs if not pre-injected
	var closeCivic, closeRumors, closeDiplo func()
	if civicDB == nil && res != nil && clanID != "" {
		if c, err := db.OpenCivicRegistryDBForClan(res, clanID); err == nil {
			civicDB = c
			closeCivic = func() { _ = c.Close() }
		}
	}
	if closeCivic != nil {
		defer closeCivic()
	}

	if rumorsDB == nil && res != nil && clanID != "" {
		if rum, err := db.OpenRumorsDBForClan(res, clanID); err == nil {
			rumorsDB = rum
			closeRumors = func() { _ = rum.Close() }
		}
	}
	if closeRumors != nil {
		defer closeRumors()
	}

	if diploDB == nil && res != nil && clanID != "" {
		if d, err := db.OpenDiplomacyDBForClan(res, clanID); err == nil {
			diploDB = d
			closeDiplo = func() { _ = d.Close() }
		}
	}
	if closeDiplo != nil {
		defer closeDiplo()
	}

	var prof system2.PersonalProfile
	prof.AgentID = agentID
	prof.ClanID = clanID
	if agentCfg != nil {
		prof.Profession = agentCfg.Profession
		prof.SecondaryHobby = agentCfg.SecondaryHobby
		prof.Traits = map[string]float64{
			"bravery":         agentCfg.Traits.Bravery,
			"industriousness": agentCfg.Traits.Industriousness,
			"sociability":     agentCfg.Traits.Sociability,
			"curiosity":       agentCfg.Traits.Curiosity,
			"aggressiveness":  agentCfg.Traits.Aggressiveness,
		}
		prof.SleepSystemPrompt = agentCfg.EffectiveSleepSystemPrompt()
	}
	if prof.SleepSystemPrompt == "" && r.resolver != nil {
		if skill, err := r.resolver.LoadAgentSkill(agentID); err == nil && skill != "" {
			prof.SleepSystemPrompt = skill
		}
	}
	if frame.Status != nil {
		prof.Vitals.Health = frame.Status.Health
		prof.Vitals.FoodLevel = frame.Status.FoodLevel
		if p := frame.Status.Position; p != nil {
			prof.Vitals.Position = fmt.Sprintf("(%.0f, %.0f, %.0f)", p.X, p.Y, p.Z)
		}
		for _, item := range frame.Status.Inventory {
			if item != nil && item.Count > 0 {
				prof.InventoryItems = append(prof.InventoryItems, fmt.Sprintf("%dx %s", item.Count, item.ItemId))
			}
		}
	}

	ledger, err := system2.AssembleStrategicLedger(ctx, agentDB, civicDB, rumorsDB, diploDB, repDB, prof, frame.WorldTick)
	if err != nil {
		return nil, fmt.Errorf("assembling strategic ledger: %w", err)
	}

	r.mu.RLock()
	fsm := r.agentFSM
	sys1State := r.system1State
	r.mu.RUnlock()
	if fsm != nil && fsm.LastFacts() != nil {
		ledger.WithFactGraph(fsm.LastFacts())
	}
	if sys1State != nil {
		ledger.WithHorizonSurveySummary(sys1State.LatestSurveySummary())
	}

	resp, err := contemplator.Contemplate(ctx, ledger)
	if err != nil {
		return nil, err
	}

	r.UpdateGoals(resp.ImmediateGoal, resp.ShortTermGoal, resp.LongTermGoal)
	r.applyContemplationDirective(resp, frame.WorldTick)

	if len(resp.TradeAdjustments) > 0 && agentDB != nil {
		var valuations []db.TradeValuation
		for _, adj := range resp.TradeAdjustments {
			itemID := strings.ToLower(strings.TrimSpace(adj.ItemID))
			if itemID == "" {
				continue
			}
			if !strings.Contains(itemID, ":") {
				itemID = "minecraft:" + itemID
			}
			valuations = append(valuations, db.TradeValuation{
				ItemID:         itemID,
				BaseValue:      math.Max(0.1, math.Min(100.0, adj.BaseValue)),
				NeedMultiplier: math.Max(0.2, math.Min(4.0, adj.NeedMultiplier)),
				MinAcceptRatio: math.Max(0.5, math.Min(1.5, adj.MinAcceptRatio)),
				Rationale:      adj.Rationale,
				UpdatedTick:    frame.WorldTick,
			})
		}
		if len(valuations) > 0 {
			_ = agentDB.UpsertTradeValuationsBatch(ctx, valuations)
			r.mu.Lock()
			if r.tradeValuations == nil {
				r.tradeValuations = make(map[string]db.TradeValuation)
			}
			for _, v := range valuations {
				r.tradeValuations[v.ItemID] = v
			}
			r.mu.Unlock()
		}
	}

	if strings.TrimSpace(resp.ReflectiveMemory) != "" {
		if memPipe != nil {
			_, _ = memPipe.Ingest(ctx, memory.MemoryEvent{
				WorldTick:        frame.WorldTick,
				Category:         memory.CategoryDiscovery,
				SaliencyScore:    0.85,
				ExplicitSaliency: true,
				SummaryText:      resp.ReflectiveMemory,
			})
		} else if agentDB != nil {
			_, _ = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
				WorldTick:     frame.WorldTick,
				EventCategory: "DISCOVERY",
				SaliencyScore: 0.85,
				SummaryText:   resp.ReflectiveMemory,
			}, nil)
		}
	}

	return resp, nil
}

const strategicDirectiveTTLTicks int64 = 6000

func (r *AgentRuntime) applyContemplationDirective(resp *system2.StrategicContemplationResponse, worldTick int64) {
	if resp == nil {
		return
	}
	if r.system1State != nil {
		action := system2.DirectiveGatherTimber
		target := ""
		if resp.Directive != nil {
			if strings.TrimSpace(resp.Directive.Action) != "" {
				action = resp.Directive.Action
			}
			target = resp.Directive.Target
		}
		r.system1State.SetStrategicDirective(action, target, resp.ImmediateGoal, worldTick, worldTick+strategicDirectiveTTLTicks)
	}
	r.mu.Lock()
	r.idleStartTick = worldTick
	r.hasIdleStartTick = true
	if r.latestFrame != nil && r.latestFrame.Status != nil && r.latestFrame.Status.Position != nil {
		p := r.latestFrame.Status.Position
		r.lastActivePos = [3]float64{p.X, p.Y, p.Z}
		r.hasLastActivePos = true
	}
	r.mu.Unlock()
}

func (r *AgentRuntime) updateIdleTracker(frame *civquestv1.TelemetryFrame, result system1.FSMResult) {
	if frame == nil || frame.Status == nil {
		return
	}
	isSleeping := frame.Status.IsSleeping ||
		(r.system1State != nil && r.system1State.GetCurrentRoutine() == system1.RoutineSleep)
	inEmergency := result.Stage == system1.StageEmergency || result.Stage == system1.StageDead
	hasActiveTask := r.system1State != nil && r.system1State.HasActiveTask()

	emittedProductive := false
	for _, fi := range result.Intents {
		if fi.Intent == nil {
			continue
		}
		if fi.Intent.GetCraftItem() != nil ||
			fi.Intent.GetInteractBlock() != nil ||
			fi.Intent.GetAttack() != nil ||
			fi.Intent.GetUseItem() != nil {
			emittedProductive = true
			break
		}
	}

	pos := frame.Status.Position
	navState := frame.Status.NavigationState

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isDead || inEmergency || isSleeping {
		r.idleStartTick = 0
		r.hasIdleStartTick = false
		r.hasLastActivePos = false
		return
	}

	if hasActiveTask || emittedProductive {
		r.idleStartTick = frame.WorldTick
		r.hasIdleStartTick = true
		if pos != nil {
			r.lastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
			r.hasLastActivePos = true
		}
		return
	}

	if !r.hasIdleStartTick || frame.WorldTick < r.idleStartTick {
		r.idleStartTick = frame.WorldTick
		r.hasIdleStartTick = true
		if pos != nil {
			r.lastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
			r.hasLastActivePos = true
		}
		return
	}

	if pos != nil && r.hasLastActivePos {
		dx := pos.X - r.lastActivePos[0]
		dy := pos.Y - r.lastActivePos[1]
		dz := pos.Z - r.lastActivePos[2]
		if dx*dx+dy*dy+dz*dz >= 1.5*1.5 && navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			r.idleStartTick = frame.WorldTick
			r.lastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
		}
	}
}

// ContractsDB returns the handle to the clan contracts database.
func (r *AgentRuntime) ContractsDB() *db.ContractsDB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contractsDB
}

// SharecroppingEngine returns the agrarian sharecropping engine if configured.
func (r *AgentRuntime) SharecroppingEngine() *agrarian.SharecroppingEngine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sharecroppingEngine
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

// loadTradeValuations loads persisted System 1 quid-pro-quo trade valuations from memory.db,
// merging any stored LLM-reassessed overrides onto the baseline profession-weighted valuations.
func (r *AgentRuntime) loadTradeValuations() {
	prof := "EXPLORER"
	if r.identityConfig != nil && r.identityConfig.Profession != "" {
		prof = r.identityConfig.Profession
	}
	defaults := system1.DefaultTradeValuations(prof)
	if r.agentDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if stored, err := r.agentDB.GetTradeValuations(ctx); err == nil && len(stored) > 0 {
			for k, v := range stored {
				defaults[k] = v
			}
		}
	}
	r.mu.Lock()
	r.tradeValuations = defaults
	r.mu.Unlock()
}

// loadSpatialPOIs pre-loads active Points of Interest from map_graph.db into System 1's SpatialIndex.
func (r *AgentRuntime) loadSpatialPOIs() {
	if r.agentDB == nil || r.system1State == nil {
		return
	}
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	pois, err := r.agentDB.ListPOIs(ctx, "", true)
	if err == nil && len(pois) > 0 {
		si.LoadFromPOIs(pois)
	}
	corridors, err := r.agentDB.ListTrailCorridors(ctx, "", "")
	if err == nil && len(corridors) > 0 {
		si.LoadCorridors(corridors)
	}
}

// persistPOIsAsync asynchronously upserts newly discovered or updated POIs to map_graph.db.
func (r *AgentRuntime) persistPOIsAsync(pois []db.AgentPOI) {
	if r.agentDB == nil || len(pois) == 0 {
		return
	}
	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return
	default:
	}

	copied := make([]db.AgentPOI, len(pois))
	copy(copied, pois)
	agentDB := r.agentDB

	r.poiWg.Add(1)
	go func() {
		defer r.poiWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, p := range copied {
			_ = agentDB.AddOrUpdatePOI(ctx, p)
		}
	}()
}

// recordPlacedBlockPOI immediately registers placed POI blocks (workstations, beds, containers, etc.)
// into System 1's SpatialIndex upon outbound PLACE_BLOCK intent dispatch per ADR-022.
func (r *AgentRuntime) recordPlacedBlockPOI(intent *civquestv1.AgentIntent) {
	if intent == nil || r.system1State == nil {
		return
	}
	ib := intent.GetInteractBlock()
	if ib == nil || ib.InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK || ib.Pos == nil {
		return
	}
	cat, isPOI := system1.CategorizeBlockID(ib.BlockOrItemId)
	if !isPOI {
		return
	}
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}

	selfPos := r.Position()
	selfY := float64(ib.Pos.Y)
	if selfPos != nil {
		selfY = selfPos.Y
	}
	ax, ay, az := system1.ComputePOIAccessPos(ib.Pos, selfPos, nil)
	py := float64(ib.Pos.Y)
	safety := 1.0
	if cat == system1.POICategoryHazard {
		safety = 0.0
	}
	tick := r.WorldTick()
	poi := db.AgentPOI{
		POIID:            system1.FormatPOIID(cat, ib.Pos.X, ib.Pos.Y, ib.Pos.Z),
		Category:         cat,
		BlockID:          ib.BlockOrItemId,
		X:                float64(ib.Pos.X) + 0.5,
		Y:                py,
		Z:                float64(ib.Pos.Z) + 0.5,
		AccessX:          ax,
		AccessY:          ay,
		AccessZ:          az,
		ElevationLayer:   system1.ClassifyElevationLayer(py, selfY),
		SafetyScore:      safety,
		DiscoveredTick:   tick,
		LastVerifiedTick: tick,
		Reliability:      1.0,
		IsActive:         true,
	}
	si.UpsertPOI(poi)
	r.persistPOIsAsync([]db.AgentPOI{poi})
}

// PruneHarvestedBlockPOI immediately removes harvested or destroyed POI blocks from
// System 1's SpatialIndex upon outbound HARVEST_BREAK intent dispatch per ADR-022.
func (r *AgentRuntime) PruneHarvestedBlockPOI(intent *civquestv1.AgentIntent) {
	if intent == nil || r.system1State == nil {
		return
	}
	ib := intent.GetInteractBlock()
	if ib == nil || ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK || ib.Pos == nil {
		return
	}
	r.PruneHarvestedBlockPos(ib.Pos.X, ib.Pos.Y, ib.Pos.Z)
}

// PruneHarvestedBlockPos removes any active POIs at the given block coordinates from
// SpatialIndex and asynchronously marks them invalid in AgentDB.
func (r *AgentRuntime) PruneHarvestedBlockPos(x, y, z int32) {
	if r.system1State == nil {
		return
	}
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}

	pois := si.FindPOIsAtBlockPos(x, y, z)
	if len(pois) == 0 {
		return
	}

	for _, p := range pois {
		si.RemovePOI(p.POIID)
		r.invalidatePOIAsync(p.POIID)
	}
}

// SetNavTargetPOIID explicitly sets the tracked POI target ID for navigation.
func (r *AgentRuntime) SetNavTargetPOIID(poiID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.navTargetPOIID = poiID
}

// NavTargetPOIID returns the currently tracked POI target ID.
func (r *AgentRuntime) NavTargetPOIID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.navTargetPOIID
}

// handleNavigationStuck handles NAVIGATION_STATE_STUCK telemetry while pathing toward a POI target.
func (r *AgentRuntime) handleNavigationStuck() {
	r.mu.Lock()
	poiID := r.navTargetPOIID
	var dest *SocketPosition
	if r.navDestination != nil {
		cp := *r.navDestination
		dest = &cp
	}
	r.navTargetPOIID = ""
	r.navDestination = nil
	r.mu.Unlock()

	if r.system1State == nil {
		return
	}
	if dest != nil && !r.system1State.IsExpeditionActive() {
		r.system1State.RecordTargetNavigationStuck(dest.X, dest.Y, dest.Z, r.WorldTick(), system1.DefaultUnreachableTargetTTL)
	}
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}

	if poiID == "" && dest != nil {
		if poi := si.FindPOIByTargetPos(&civquestv1.Vec3{X: dest.X, Y: dest.Y, Z: dest.Z}, 1.5); poi != nil {
			poiID = poi.POIID
		}
	}

	if poiID == "" {
		return
	}

	r.HandleNavigationStuckForPOI(poiID)
}

// HandleNavigationStuckForPOI reduces a POI's reliability by 0.25 in SpatialIndex and AgentDB.
// If reliability drops below threshold (<= 0.1), the POI is removed from SpatialIndex and invalidated in AgentDB.
func (r *AgentRuntime) HandleNavigationStuckForPOI(poiID string) {
	if poiID == "" || r.system1State == nil {
		return
	}
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}

	newRel, found := si.DecayPOIReliability(poiID, 0.25)
	if !found {
		return
	}

	shouldInvalidate := (newRel <= 0.1)
	if shouldInvalidate {
		si.RemovePOI(poiID)
	}

	r.decayAndOptionallyInvalidatePOIAsync(poiID, 0.25, shouldInvalidate)
}

// invalidatePOIAsync asynchronously marks a single POI as inactive in AgentDB.
func (r *AgentRuntime) invalidatePOIAsync(poiID string) {
	if poiID == "" {
		return
	}
	r.invalidatePOIsAsync([]string{poiID})
}

// invalidatePOIsAsync asynchronously marks multiple POIs as inactive in AgentDB.
func (r *AgentRuntime) invalidatePOIsAsync(poiIDs []string) {
	if r.agentDB == nil || len(poiIDs) == 0 {
		return
	}
	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return
	default:
	}

	ids := make([]string, len(poiIDs))
	copy(ids, poiIDs)
	agentDB := r.agentDB

	r.poiWg.Add(1)
	go func() {
		defer r.poiWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, id := range ids {
			_ = agentDB.InvalidatePOI(ctx, id)
		}
	}()
}

// decayPOIReliabilityAsync asynchronously decays a POI's reliability in AgentDB.
func (r *AgentRuntime) decayPOIReliabilityAsync(poiID string, delta float64) {
	r.decayAndOptionallyInvalidatePOIAsync(poiID, delta, false)
}

// decayAndOptionallyInvalidatePOIAsync asynchronously decays a POI's reliability in AgentDB
// and marks it inactive if invalidate is true.
func (r *AgentRuntime) decayAndOptionallyInvalidatePOIAsync(poiID string, delta float64, invalidate bool) {
	if r.agentDB == nil || poiID == "" {
		return
	}
	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return
	default:
	}

	agentDB := r.agentDB
	r.poiWg.Add(1)
	go func() {
		defer r.poiWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = agentDB.DecayPOIReliability(ctx, poiID, delta)
		if invalidate {
			_ = agentDB.InvalidatePOI(ctx, poiID)
		}
	}()
}

// AwaitPOIPersistence waits for background POI persistence and invalidation goroutines to complete.
func (r *AgentRuntime) AwaitPOIPersistence() {
	r.poiWg.Wait()
}

// persistTrailCorridorAsync asynchronously upserts a learned trail corridor to map_graph.db (ADR-023).
func (r *AgentRuntime) persistTrailCorridorAsync(corridor db.TrailCorridor) {
	if r.agentDB == nil {
		return
	}
	r.mu.RLock()
	if r.isDead {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	select {
	case <-r.done:
		return
	default:
	}

	agentDB := r.agentDB
	c := corridor

	r.poiWg.Add(1)
	go func() {
		defer r.poiWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = agentDB.AddTrailCorridor(ctx, c)
	}()
}

// HabitTrailTracker returns the agent's in-memory HabitTrailTracker.
func (r *AgentRuntime) HabitTrailTracker() *system1.HabitTrailTracker {
	if r.system1State == nil {
		return nil
	}
	return r.system1State.HabitTrailTracker()
}

// NavigateViaCorridor attempts to initiate multi-waypoint navigation along a known
// TrailCorridor to destPOIID. Returns true if a corridor route was found and started.
func (r *AgentRuntime) NavigateViaCorridor(destPOIID string, destPos *civquestv1.Vec3) bool {
	if destPOIID == "" || r.system1State == nil {
		return false
	}
	si := r.system1State.SpatialIndex()
	tracker := r.system1State.HabitTrailTracker()
	if si == nil || tracker == nil {
		return false
	}

	destPOI, ok := si.GetPOI(destPOIID)
	if !ok || destPOI == nil {
		return false
	}

	targetPos := destPos
	if targetPos == nil {
		targetPos = &civquestv1.Vec3{X: destPOI.X, Y: destPOI.Y, Z: destPOI.Z}
	}

	originID := tracker.CurrentOriginPOIID()
	agentPos := r.Position()
	if originID == "" || originID == destPOIID {
		if agentPos != nil {
			if nearPOI := si.FindNearestPOI(agentPos, "", "", 8.0, ""); nearPOI != nil && nearPOI.POIID != destPOIID {
				originID = nearPOI.POIID
				tracker.SetOriginPOI(nearPOI.POIID, &civquestv1.Vec3{X: nearPOI.X, Y: nearPOI.Y, Z: nearPOI.Z})
			}
		}
	}

	if originID == "" || originID == destPOIID {
		return false
	}

	corridor, ok := si.GetCorridor(originID, destPOIID)
	if !ok || corridor == nil || corridor.WaypointsJSON == "" || corridor.WaypointsJSON == "[]" {
		return false
	}

	tracker.SetDestinationPOI(destPOIID, targetPos)
	firstIntent, err := tracker.StartCorridorNavigation(r.agentID, corridor, targetPos)
	if err != nil || firstIntent == nil {
		return false
	}

	return r.QueueIntent(firstIntent) == nil
}

// prepareNavigateIntent inspects outbound NavigateIntents, resolves target POIs,
// and initiates corridor navigation along a known TrailCorridor if navigating > 16m away (ADR-023).
func (r *AgentRuntime) prepareNavigateIntent(intent *civquestv1.AgentIntent) {
	nav := intent.GetNavigate()
	if nav == nil || nav.Target == nil {
		return
	}

	r.mu.Lock()
	r.navDestination = &SocketPosition{
		X: nav.Target.X,
		Y: nav.Target.Y,
		Z: nav.Target.Z,
	}

	var si *system1.SpatialIndex
	var tracker *system1.HabitTrailTracker
	if r.system1State != nil {
		si = r.system1State.SpatialIndex()
		tracker = r.system1State.HabitTrailTracker()
	}

	var destPOI *db.AgentPOI
	if si != nil {
		destPOI = si.FindPOIByTargetPos(nav.Target, 1.5)
		if destPOI != nil {
			r.navTargetPOIID = destPOI.POIID
		} else {
			r.navTargetPOIID = ""
		}
	}
	r.mu.Unlock()

	if tracker == nil || si == nil || destPOI == nil {
		return
	}

	destPos := &civquestv1.Vec3{X: destPOI.X, Y: destPOI.Y, Z: destPOI.Z}
	tracker.SetDestinationPOI(destPOI.POIID, destPos)

	if tracker.IsNavigatingCorridor() || tracker.InFinalLeg() {
		return
	}

	agentPos := r.Position()
	if agentPos == nil {
		return
	}

	dist := system1.Distance3D(agentPos, nav.Target)
	if dist <= system1.MinCorridorDistance {
		return
	}

	originPOIID := tracker.CurrentOriginPOIID()
	if originPOIID == "" || originPOIID == destPOI.POIID {
		if nearPOI := si.FindNearestPOI(agentPos, "", "", 8.0, ""); nearPOI != nil && nearPOI.POIID != destPOI.POIID {
			originPOIID = nearPOI.POIID
			tracker.SetOriginPOI(nearPOI.POIID, &civquestv1.Vec3{X: nearPOI.X, Y: nearPOI.Y, Z: nearPOI.Z})
		}
	}

	if originPOIID == "" || originPOIID == destPOI.POIID {
		return
	}

	corridor, ok := si.GetCorridor(originPOIID, destPOI.POIID)
	if !ok || corridor == nil || corridor.WaypointsJSON == "" || corridor.WaypointsJSON == "[]" {
		return
	}

	firstIntent, err := tracker.StartCorridorNavigation(r.agentID, corridor, nav.Target)
	if err == nil && firstIntent != nil && firstIntent.GetNavigate() != nil && firstIntent.GetNavigate().Target != nil {
		firstTarget := firstIntent.GetNavigate().Target
		nav.Target = &civquestv1.Vec3{
			X: firstTarget.X,
			Y: firstTarget.Y,
			Z: firstTarget.Z,
		}
		r.mu.Lock()
		r.navDestination = &SocketPosition{
			X: firstTarget.X,
			Y: firstTarget.Y,
			Z: firstTarget.Z,
		}
		r.mu.Unlock()
	}
}

// updateHabitTrails evaluates breadcrumb recording, active corridor advancement,
// and trail corridor consolidation upon destination POI arrival (ADR-023).
func (r *AgentRuntime) updateHabitTrails(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || r.system1State == nil {
		return
	}
	tracker := r.system1State.HabitTrailTracker()
	si := r.system1State.SpatialIndex()
	if tracker == nil || si == nil {
		return
	}

	agentPos := frame.Status.Position
	navState := frame.Status.NavigationState

	// 1. If stuck, clear breadcrumbs and reset active corridor navigation
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		tracker.ClearBreadcrumbs()
		tracker.ResetNavigation()
		return
	}

	// 2. Track origin POI: when agent is near a confirmed POI (<= 4m) and not actively in transit
	if tracker.CurrentDestPOIID() == "" && !tracker.IsNavigatingCorridor() {
		if nearPOI := si.FindNearestPOI(agentPos, "", "", 4.0, ""); nearPOI != nil {
			tracker.SetOriginPOI(nearPOI.POIID, &civquestv1.Vec3{X: nearPOI.X, Y: nearPOI.Y, Z: nearPOI.Z})
		}
	}

	// 3. Step through active corridor waypoints if navigating a corridor
	if tracker.IsNavigatingCorridor() {
		if nextIntent, advanced := tracker.UpdateNavigation(agentPos, navState); advanced && nextIntent != nil {
			if nav := nextIntent.GetNavigate(); nav != nil && nav.Target != nil {
				r.mu.Lock()
				r.navDestination = &SocketPosition{
					X: nav.Target.X,
					Y: nav.Target.Y,
					Z: nav.Target.Z,
				}
				r.mu.Unlock()
			}
			_ = r.QueueIntent(nextIntent)
		}
	}

	// 4. Sample breadcrumb position while moving towards destination POI
	if tracker.CurrentDestPOIID() != "" {
		tracker.SampleBreadcrumb(agentPos)
	}

	// 5. On arrival at destination POI: consolidate corridor and persist
	isArrived := navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
	if !isArrived && tracker.CurrentDestPos() != nil && system1.Distance3D(agentPos, tracker.CurrentDestPos()) <= 2.0 {
		isArrived = true
	}

	if isArrived && tracker.CurrentDestPOIID() != "" {
		if corridor, ok := tracker.ConsolidateCorridor(si, system1.DefaultRDPEpsilon); ok && corridor != nil {
			r.persistTrailCorridorAsync(*corridor)
		}
		// Arrived destination becomes the new origin for subsequent legs
		destID := tracker.CurrentDestPOIID()
		destPos := tracker.CurrentDestPos()
		if destID != "" && destPos != nil {
			tracker.SetOriginPOI(destID, destPos)
		}
		tracker.ClearDestination()
		tracker.ClearBreadcrumbs()
	}
}

// buildDialogueVitals constructs comprehensive sensory, temporal, and threat context for System 2 prompts.
func (r *AgentRuntime) buildDialogueVitals(frame *civquestv1.TelemetryFrame) *system2.DialogueVitals {
	if frame == nil || frame.Status == nil {
		return nil
	}
	bravery := 0.5
	if r.identityConfig != nil {
		bravery = r.identityConfig.Traits.Bravery
	}
	threatStatus, conf, _ := system1.AssessImmediateThreats(frame, bravery)
	var posStr string
	if p := frame.Status.Position; p != nil {
		posStr = fmt.Sprintf("(%.0f, %.0f, %.0f)", p.X, p.Y, p.Z)
	}
	var routineStr string
	if r.system1State != nil {
		routineStr = string(r.system1State.GetCurrentRoutine())
	}
	goals := system1.EvaluateGoals(frame, r.identityConfig, r.system1State)
	r.mu.RLock()
	immGoal := goals.ImmediateGoal
	stGoal := goals.ShortTermGoal
	ltGoal := goals.LongTermGoal
	if r.identityConfig != nil {
		if r.identityConfig.ImmediateGoal != "" {
			immGoal = r.identityConfig.ImmediateGoal
		}
		if r.identityConfig.ShortTermGoal != "" {
			stGoal = r.identityConfig.ShortTermGoal
		}
		if r.identityConfig.LongTermGoal != "" {
			ltGoal = r.identityConfig.LongTermGoal
		}
	}
	// Under immediate reflex emergency (active hostile threat or critical starvation), reflex overrides
	if goals.ImmediateGoal == "COMBAT_SURVIVAL: Defend against or evade nearby hostile threats" ||
		goals.ImmediateGoal == "CRITICAL_HUNGER: Consume food or forage immediately" {
		immGoal = goals.ImmediateGoal
	}
	recentCombat := r.lastCombatSummary
	r.mu.RUnlock()

	vitals := &system2.DialogueVitals{
		Health:           frame.Status.Health,
		FoodLevel:        frame.Status.FoodLevel,
		TimeOfDay:        frame.TimeOfDay,
		DayPhase:         system1.FormatDayPhase(frame.TimeOfDay),
		Weather:          system1.FormatWeather(frame.Weather),
		BiomeID:          frame.BiomeId,
		Position:         posStr,
		CurrentRoutine:   routineStr,
		ThreatStatus:     threatStatus,
		CombatConfidence: conf,
		RecentCombat:     recentCombat,
		InventorySummary: formatInventorySummary(frame.Status.Inventory),
		ImmediateGoal:    immGoal,
		ShortTermGoal:    stGoal,
		LongTermGoal:     ltGoal,
	}

	r.mu.RLock()
	fsm := r.agentFSM
	r.mu.RUnlock()
	if fsm != nil && fsm.LastFacts() != nil {
		vitals.WithFactGraph(fsm.LastFacts())
	}
	return vitals
}

func formatPlayerRelativeLocation(frame *civquestv1.TelemetryFrame, chat *civquestv1.ChatEvent) string {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || chat == nil {
		return ""
	}
	p := frame.Status.Position
	dist := float64(chat.Distance)
	var playerEntity *civquestv1.VisibleEntity
	for _, e := range frame.VisibleEntities {
		if e == nil {
			continue
		}
		if (chat.SenderUuid != "" && e.Uuid == chat.SenderUuid) ||
			(chat.SenderName != "" && strings.EqualFold(e.DisplayName, chat.SenderName)) {
			playerEntity = e
			break
		}
	}

	if playerEntity != nil && playerEntity.Position != nil {
		pp := playerEntity.Position
		if dist <= 0 {
			dist = math.Sqrt(math.Pow(p.X-pp.X, 2) + math.Pow(p.Y-pp.Y, 2) + math.Pow(p.Z-pp.Z, 2))
		}
		dy := p.Y - pp.Y
		var elevStr string
		if dy <= -2.0 {
			elevStr = fmt.Sprintf(" (%.0f blocks lower in elevation)", math.Abs(dy))
		} else if dy >= 2.0 {
			elevStr = fmt.Sprintf(" (%.0f blocks higher in elevation)", dy)
		}
		return fmt.Sprintf("Agent is at (%.0f, %.0f, %.0f), %.1f blocks away from %s at (%.0f, %.0f, %.0f)%s",
			p.X, p.Y, p.Z, dist, chat.SenderName, pp.X, pp.Y, pp.Z, elevStr)
	}

	return fmt.Sprintf("Agent is at (%.0f, %.0f, %.0f), %.1f blocks away from %s",
		p.X, p.Y, p.Z, dist, chat.SenderName)
}

func formatInventorySummary(inv []*civquestv1.ItemStackProto) string {
	if len(inv) == 0 {
		return "Empty (unarmed, no food)"
	}
	var parts []string
	for _, it := range inv {
		if it == nil || it.Count <= 0 || it.ItemId == "" {
			continue
		}
		short := strings.TrimPrefix(strings.ToLower(it.ItemId), "minecraft:")
		parts = append(parts, fmt.Sprintf("%dx %s", it.Count, short))
		if len(parts) >= 8 {
			break
		}
	}
	if len(parts) == 0 {
		return "Empty (unarmed, no food)"
	}
	return strings.Join(parts, ", ")
}

// recordDeterministicTradeEvent records a completed deterministic gift or GUI barter in memory.db and relationships
// without invoking an LLM round-trip.
func (r *AgentRuntime) recordDeterministicTradeEvent(
	ctx context.Context,
	chat *civquestv1.ChatEvent,
	frame *civquestv1.TelemetryFrame,
	worldTick int64,
) {
	summary := strings.TrimSpace(chat.Message)
	summary = strings.TrimPrefix(summary, "[DETERMINISTIC_GIFT]")
	summary = strings.TrimPrefix(summary, "[DETERMINISTIC_TRADE]")
	summary = strings.TrimSpace(summary)

	r.mu.RLock()
	compSvc := r.companionService
	memPipe := r.memoryPipeline
	r.mu.RUnlock()

	if compSvc != nil && chat.SenderUuid != "" {
		_, _ = compSvc.RecordDialogueInteraction(ctx, chat.SenderUuid, chat.SenderName, 4.0, worldTick)
	}
	if memPipe != nil && summary != "" {
		memCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = memPipe.Ingest(memCtx, memory.MemoryEvent{
			WorldTick:     worldTick,
			Category:      memory.CategoryTrade,
			SummaryText:   summary,
			Actors:        []string{r.agentID, chat.SenderName},
			SaliencyScore: memory.SaliencyTrade,
		})
	}
}

// executeSystem1Trade evaluates a PendingTrade synchronously in <1ms using persisted quid-pro-quo thresholds,
// queues the RespondToTradeIntent, records episodic memory and relationship progression, and schedules periodic
// System 2 valuation table reassessment.
func (r *AgentRuntime) executeSystem1Trade(
	ctx context.Context,
	trade *civquestv1.TradeRequest,
	frame *civquestv1.TelemetryFrame,
	vitals *system2.DialogueVitals,
	worldTick int64,
) {
	prof := "EXPLORER"
	if r.identityConfig != nil && r.identityConfig.Profession != "" {
		prof = r.identityConfig.Profession
	}

	r.mu.RLock()
	compSvc := r.companionService
	memPipe := r.memoryPipeline
	svc := r.dialogueService
	agentDB := r.agentDB
	valCopy := make(map[string]db.TradeValuation, len(r.tradeValuations))
	for k, v := range r.tradeValuations {
		valCopy[k] = v
	}
	r.mu.RUnlock()

	var disp float64
	if compSvc != nil && trade.PlayerUuid != "" {
		if rel, err := compSvc.GetRelationship(ctx, trade.PlayerUuid); err == nil && rel != nil {
			disp = rel.DispositionScore
		}
	}

	decision := system1.EvaluateTradeQuidProQuo(trade, frame, prof, disp, valCopy)
	if decision.TradeIntent != nil && !r.IsDead() && ctx.Err() == nil {
		_ = r.QueueIntent(&civquestv1.AgentIntent{
			AgentId: r.agentID,
			Action: &civquestv1.AgentIntent_TradeResponse{
				TradeResponse: decision.TradeIntent,
			},
		})
	}

	if r.debugLogger != nil {
		r.debugLogger.Emit(worldTick, "SYSTEM1_TRADE_EVAL", decision.SpokenReply, map[string]any{
			"player_name":     trade.PlayerName,
			"decision":        decision.Decision,
			"offered_value":   decision.OfferedValue,
			"requested_value": decision.RequestedValue,
			"required_ratio":  decision.RequiredRatio,
			"prestige_delta":  decision.PrestigeDelta,
		})
	}

	if compSvc != nil && trade.PlayerUuid != "" && decision.PrestigeDelta != 0 {
		_, _ = compSvc.RecordDialogueInteraction(ctx, trade.PlayerUuid, trade.PlayerName, decision.PrestigeDelta, worldTick)
	}

	if memPipe != nil && decision.MemorySummary != "" {
		go func() {
			memCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = memPipe.Ingest(memCtx, memory.MemoryEvent{
				WorldTick:     worldTick,
				Category:      memory.CategoryTrade,
				SummaryText:   decision.MemorySummary,
				Actors:        []string{r.agentID, trade.PlayerName},
				SaliencyScore: memory.SaliencyTrade,
			})
		}()
	}

	// Periodically trigger asynchronous System 2 reassessment of the trade valuation table
	r.mu.Lock()
	shouldReassess := svc != nil && agentDB != nil && (r.lastTradeReassessTick == 0 || worldTick-r.lastTradeReassessTick >= 1200)
	if shouldReassess {
		r.lastTradeReassessTick = worldTick
	}
	r.mu.Unlock()

	if shouldReassess {
		go func() {
			reCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			updated, err := svc.ReassessTradeValuations(reCtx, agentDB, vitals, worldTick)
			if err == nil && len(updated) > 0 {
				r.mu.Lock()
				for _, u := range updated {
					r.tradeValuations[u.ItemID] = u
				}
				r.mu.Unlock()
			}
		}()
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
	if frame.LastDamageTaken != nil && !system1.IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
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
// daily utility scheduler, biological needs, and road paving reflexes via the formal AgentFSM in <5ms.
func (r *AgentRuntime) evaluateSystem1Reflexes(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || r.IsDead() {
		return
	}

	// Motion telemetry: stamina / navigation state sampled every 100 ticks into the debug log
	if r.debugLogger != nil && (r.lastMotionLogTick == 0 || frame.WorldTick < r.lastMotionLogTick || frame.WorldTick-r.lastMotionLogTick >= 100) {
		r.lastMotionLogTick = frame.WorldTick
		r.debugLogger.Emit(frame.WorldTick, "MOTION", fmt.Sprintf("stamina=%.1f nav=%s", frame.Status.Stamina, frame.Status.NavigationState.String()), map[string]any{
			"stamina":             frame.Status.Stamina,
			"navigation_state":    frame.Status.NavigationState.String(),
			"navigation_enclosed": frame.Status.NavigationEnclosed,
		})
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
	fsm := r.agentFSM
	r.mu.RUnlock()

	if fsm == nil {
		r.mu.Lock()
		if r.agentFSM == nil {
			r.agentFSM = system1.NewAgentFSM(system1.StateBootstrap, nil)
		}
		fsm = r.agentFSM
		r.mu.Unlock()
	}

	var hasShield, hasWeapon bool
	var weaponSlot int32 = -1
	if frame.Status != nil && len(frame.Status.Inventory) > 0 {
		hasShield, hasWeapon, weaponSlot = system1.ScanInventoryCombatItems(frame.Status.Inventory)
	}

	ctx := &system1.FSMContext{
		AgentID:        r.agentID,
		Frame:          frame,
		State:          r.system1State,
		Config:         r.identityConfig,
		SafeNodes:      safeNodes,
		CanEat:         canEat,
		LastEatingTick: r.lastEatingTick,
		PavingTracker:  r.pavingTracker,
		HasShield:      hasShield,
		HasWeapon:      hasWeapon,
		WeaponSlot:     weaponSlot,
		CivicDB:        r.civicRegistryDB,
	}

	// Execute formal HFSM tick
	result := fsm.Tick(ctx)

	// Track idle/unproductive dwell duration for daytime System 2 contemplation (ADR-031)
	r.updateIdleTracker(frame, result)

	// Log FSM transitions
	if result.Transitioned && r.debugLogger != nil {
		r.debugLogger.Emit(frame.WorldTick, "FSM_TRANSITION", fmt.Sprintf("FSM %s -> %s (%s)", result.PreviousState, result.State, result.TransitionReason), map[string]any{
			"from_state":  result.PreviousState,
			"to_state":    result.State,
			"reason":      result.TransitionReason,
			"world_tick":  frame.WorldTick,
			"time_of_day": frame.TimeOfDay,
		})
	}

	// Mark periphery flush before queuing intents so flush + new routine intents dispatch in the same IntentBatch
	if result.FlushPeriphery && r.slotQueue != nil {
		r.slotQueue.MarkFlushPeriphery()
	}

	// Dispatch emitted intents
	for _, fsmIntent := range result.Intents {
		if fsmIntent.Intent == nil {
			continue
		}
		if fsmIntent.Priority {
			// Flee navigation deduplication / churn mitigation
			skipNav := false
			if nav := fsmIntent.Intent.GetNavigate(); nav != nil && nav.Target != nil {
				r.mu.Lock()
				if r.lastFleeTarget != nil {
					dx := nav.Target.X - r.lastFleeTarget.X
					dy := nav.Target.Y - r.lastFleeTarget.Y
					dz := nav.Target.Z - r.lastFleeTarget.Z
					dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
					ticksElapsed := frame.WorldTick - r.lastFleeTick
					if dist < 2.0 && ticksElapsed <= 10 && ticksElapsed >= 0 {
						skipNav = true
					}
				}
				if !skipNav {
					r.lastFleeTarget = &civquestv1.Vec3{
						X: nav.Target.X,
						Y: nav.Target.Y,
						Z: nav.Target.Z,
					}
					r.lastFleeTick = frame.WorldTick
				}
				r.mu.Unlock()
			}
			if skipNav {
				continue
			}

			err := r.QueuePriorityIntent(fsmIntent.Intent)
			if err == nil && fsmIntent.Intent.GetUseItem() != nil && fsmIntent.Intent.GetUseItem().IsEatingOrDrinking {
				r.mu.Lock()
				r.lastEatingTick = frame.WorldTick
				r.mu.Unlock()
				if r.system1State != nil {
					r.system1State.SetLastEatingTick(frame.WorldTick)
				}
			}
		} else {
			_ = r.QueueIntent(fsmIntent.Intent)
		}
	}

	if result.FlushPeriphery && r.slotQueue != nil {
		r.slotQueue.NotifyIfFlushPending()
	}

	// Reset flee target if not in emergency
	if result.State != system1.StateEmergency {
		r.mu.Lock()
		r.lastFleeTarget = nil
		r.lastFleeTick = 0
		r.mu.Unlock()
	}

	// Record combat/aggro encounter in recent summary and episodic memory (cooldown: 200 ticks / 10s)
	hostilesPresent := ctx.HostilesPresent()
	damageTaken := ctx.DamageTaken()
	if hostilesPresent || damageTaken {
		bravery := 0.5
		if r.identityConfig != nil {
			bravery = r.identityConfig.Traits.Bravery
		}
		maxHP := float64(frame.Status.MaxHealth)
		if maxHP <= 0 {
			maxHP = 20.0
		}
		_, _, hostileList := system1.AssessImmediateThreats(frame, bravery)
		dayPhase := system1.FormatDayPhase(frame.TimeOfDay)
		actionDesc := "engaged in combat with"
		if ctx.ShouldPanic() {
			actionDesc = "fled from"
		}
		targetDesc := "hostile mob"
		if len(hostileList) > 0 {
			targetDesc = strings.Join(hostileList, ", ")
		} else if frame.LastDamageTaken != nil && frame.LastDamageTaken.GetAttackerName() != "" {
			targetDesc = frame.LastDamageTaken.GetAttackerName()
		} else if frame.LastDamageTaken != nil && !system1.IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
			targetDesc = frame.LastDamageTaken.GetSourceType().String()
		}
		summary := fmt.Sprintf("At tick %d during %s, %s %s (HP: %.1f/%.1f).",
			frame.WorldTick, dayPhase, actionDesc, targetDesc, frame.Status.Health, maxHP)

		r.mu.Lock()
		r.lastCombatSummary = summary
		shouldIngestCombat := r.memoryPipeline != nil && (r.lastCombatMemoryTick == 0 || frame.WorldTick < r.lastCombatMemoryTick || frame.WorldTick-r.lastCombatMemoryTick >= 200)
		if shouldIngestCombat {
			r.lastCombatMemoryTick = frame.WorldTick
		}
		memPipe := r.memoryPipeline
		r.mu.Unlock()

		if shouldIngestCombat && memPipe != nil {
			var locCoords string
			if frame.Status.Position != nil {
				locCoords = fmt.Sprintf("(%.0f, %.0f, %.0f)", frame.Status.Position.X, frame.Status.Position.Y, frame.Status.Position.Z)
			}
			go func(tick int64, text string, coords string) {
				cCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = memPipe.Ingest(cCtx, memory.MemoryEvent{
					WorldTick:      tick,
					Category:       memory.CategoryCombat,
					SummaryText:    text,
					Actors:         []string{r.agentID},
					LocationCoords: coords,
					SaliencyScore:  memory.SaliencyCombat,
				})
			}(frame.WorldTick, summary, locCoords)
		}
	}

	// Evaluate Topological Mapping (records 8x8 sector nodes and edges into map_graph.db)
	if node, edge := system1.EvaluateTopologicalMapping(frame, r.system1State); node != nil && r.agentDB != nil {
		if node.SafetyScore >= 0.7 {
			r.mu.Lock()
			updatedSafe := false
			for i := range r.safeNodesCache {
				if r.safeNodesCache[i].ID == node.ID {
					r.safeNodesCache[i] = *node
					updatedSafe = true
					break
				}
			}
			if !updatedSafe {
				r.safeNodesCache = append(r.safeNodesCache, *node)
			}
			r.mu.Unlock()
		}
		nCopy := *node
		var eCopy *db.MapEdge
		if edge != nil {
			ec := *edge
			eCopy = &ec
		}
		agentDB := r.agentDB
		go func() {
			mCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			_ = agentDB.AddMapNode(mCtx, nCopy)
			if eCopy != nil {
				_ = agentDB.AddMapEdge(mCtx, *eCopy)
			}
		}()
	}

	// Log goal changes
	if result.GoalHierarchy != nil && result.GoalHierarchy.ImmediateGoal != "" && result.GoalHierarchy.ImmediateGoal != r.lastLoggedGoal {
		oldGoal := r.lastLoggedGoal
		r.lastLoggedGoal = result.GoalHierarchy.ImmediateGoal
		if r.debugLogger != nil {
			r.debugLogger.Emit(frame.WorldTick, "GOAL_TRANSITION", fmt.Sprintf("Goal: %s", result.GoalHierarchy.ImmediateGoal), map[string]any{
				"previous_immediate_goal": oldGoal,
				"immediate_goal":          result.GoalHierarchy.ImmediateGoal,
				"short_term_goal":         result.GoalHierarchy.ShortTermGoal,
				"long_term_goal":          result.GoalHierarchy.LongTermGoal,
				"time_of_day":             frame.TimeOfDay,
			})
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

// RecordThought stores the latest System 2 contemplation reflection.
func (r *AgentRuntime) RecordThought(thought string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latestThought = thought
}

// RecordDialogue stores the latest speech utterance or conversation snippet.
func (r *AgentRuntime) RecordDialogue(dialogue string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latestDialogue = dialogue
}

// RecordChronicle stores the latest chronicler diary page or historical event summary.
func (r *AgentRuntime) RecordChronicle(chronicle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latestChronicle = chronicle
}

// CalculateArmorRating estimates total armor defense points from inventory or equipped items.
func CalculateArmorRating(items []CachedItem) int32 {
	armorPoints := map[string]int32{
		"leather_helmet": 1, "leather_chestplate": 3, "leather_leggings": 2, "leather_boots": 1,
		"golden_helmet": 2, "golden_chestplate": 5, "golden_leggings": 3, "golden_boots": 1,
		"chainmail_helmet": 2, "chainmail_chestplate": 5, "chainmail_leggings": 4, "chainmail_boots": 1,
		"iron_helmet": 2, "iron_chestplate": 6, "iron_leggings": 5, "iron_boots": 2,
		"diamond_helmet": 3, "diamond_chestplate": 8, "diamond_leggings": 6, "diamond_boots": 3,
		"netherite_helmet": 3, "netherite_chestplate": 8, "netherite_leggings": 6, "netherite_boots": 3,
		"turtle_helmet": 2,
	}

	var total int32
	hasArmorSlotItems := false
	for _, it := range items {
		if it.Slot >= 36 && it.Slot <= 39 {
			hasArmorSlotItems = true
			norm := strings.ToLower(strings.TrimPrefix(it.ItemID, "minecraft:"))
			if pts, ok := armorPoints[norm]; ok {
				total += pts
			}
		}
	}
	if hasArmorSlotItems {
		return total
	}

	bestPerSlot := make(map[string]int32)
	for _, it := range items {
		norm := strings.ToLower(strings.TrimPrefix(it.ItemID, "minecraft:"))
		pts, ok := armorPoints[norm]
		if !ok {
			continue
		}
		slotType := ""
		switch {
		case strings.HasSuffix(norm, "_helmet") || norm == "turtle_helmet":
			slotType = "helmet"
		case strings.HasSuffix(norm, "_chestplate"):
			slotType = "chestplate"
		case strings.HasSuffix(norm, "_leggings"):
			slotType = "leggings"
		case strings.HasSuffix(norm, "_boots"):
			slotType = "boots"
		}
		if slotType != "" && pts > bestPerSlot[slotType] {
			bestPerSlot[slotType] = pts
		}
	}
	for _, pts := range bestPerSlot {
		total += pts
	}
	return total
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

	var yaw, pitch, maxHealth, saturation, stamina float32
	var isSleeping bool
	var timeOfDay int64
	var weatherStr, biomeID string
	var invItems []CachedItem

	if r.latestFrame != nil {
		worldTick = r.latestFrame.WorldTick
		timeOfDay = r.latestFrame.TimeOfDay
		weatherStr = r.latestFrame.Weather.String()
		biomeID = r.latestFrame.BiomeId
		if r.latestFrame.Status != nil {
			health = r.latestFrame.Status.Health
			maxHealth = r.latestFrame.Status.MaxHealth
			foodLevel = r.latestFrame.Status.FoodLevel
			saturation = r.latestFrame.Status.Saturation
			stamina = r.latestFrame.Status.Stamina
			isSleeping = r.latestFrame.Status.IsSleeping
			yaw = r.latestFrame.Status.Yaw
			pitch = r.latestFrame.Status.Pitch
			navState = r.latestFrame.Status.NavigationState.String()
			if r.latestFrame.Status.Position != nil {
				pos = &SocketPosition{
					X: r.latestFrame.Status.Position.X,
					Y: r.latestFrame.Status.Position.Y,
					Z: r.latestFrame.Status.Position.Z,
				}
			}
			for _, it := range r.latestFrame.Status.Inventory {
				if it != nil {
					invItems = append(invItems, CachedItem{
						Slot:          it.Slot,
						ItemID:        it.ItemId,
						Count:         it.Count,
						Durability:    it.Durability,
						MaxDurability: it.MaxDurability,
						CustomName:    it.CustomName,
					})
				}
			}
		}
	}
	if len(invItems) == 0 && len(r.lastCachedItems) > 0 {
		invItems = make([]CachedItem, len(r.lastCachedItems))
		copy(invItems, r.lastCachedItems)
	}

	var navDest *SocketPosition
	if r.navDestination != nil {
		navDest = &SocketPosition{
			X: r.navDestination.X,
			Y: r.navDestination.Y,
			Z: r.navDestination.Z,
		}
	}

	var currentStage, currentRoutine string
	var stateStack []system1.StateStackFrame
	var blackboard map[string]system1.Fact
	if r.agentFSM != nil {
		currentStage = string(r.agentFSM.CurrentStage())
		currentRoutine = string(r.agentFSM.CurrentRoutine())
		stateStack = r.agentFSM.StackFrames()
		if facts := r.agentFSM.LastFacts(); facts != nil {
			blackboard = facts.AllFacts()
		}
	}

	shiftStr := ""
	if r.identityConfig != nil {
		shiftStr = string(r.identityConfig.EffectiveShift())
	}

	var refl *ViewerReflections
	if r.latestThought != "" || r.latestDialogue != "" || r.latestChronicle != "" {
		refl = &ViewerReflections{
			LatestThought:   r.latestThought,
			LatestDialogue:  r.latestDialogue,
			LatestChronicle: r.latestChronicle,
		}
	}
	r.mu.RUnlock()

	rssBytes := ReadProcessMemoryRSS()
	rssMB := math.Round((float64(rssBytes)/(1024.0*1024.0))*100.0) / 100.0
	scores := r.CurrentUtilityScores()
	latencyMs := r.FrameLatency()
	activeLLM := r.ActiveLLMRequests()
	armorRating := CalculateArmorRating(invItems)

	var goals *system1.GoalHierarchy
	r.mu.RLock()
	if r.identityConfig != nil && (r.identityConfig.ImmediateGoal != "" || r.identityConfig.ShortTermGoal != "" || r.identityConfig.LongTermGoal != "") {
		goals = &system1.GoalHierarchy{
			ImmediateGoal: r.identityConfig.ImmediateGoal,
			ShortTermGoal: r.identityConfig.ShortTermGoal,
			LongTermGoal:  r.identityConfig.LongTermGoal,
		}
	} else if r.system1State != nil {
		gh := r.system1State.GetGoals()
		goals = &gh
	}
	r.mu.RUnlock()

	resp := SocketStatusResponse{
		Agent:                 r.agentID,
		Clan:                  r.clanID,
		Status:                statusSnapshot,
		PID:                   r.pid,
		WorldTick:             worldTick,
		Health:                health,
		FoodLevel:             foodLevel,
		Position:              pos,
		NavigationState:       navState,
		NavigationDestination: navDest,
		UptimeSeconds:         r.UptimeSeconds(),
		MemoryRSS:             rssBytes,
		MemoryRSSMB:           rssMB,
		FrameLatencyMs:        latencyMs,
		ActiveLLMRequests:     activeLLM,
		UtilityScores:         scores,
		Goals:                 goals,
		Yaw:                   yaw,
		Pitch:                 pitch,
		MaxHealth:             maxHealth,
		Saturation:            saturation,
		Stamina:               stamina,
		IsSleeping:            isSleeping,
		TimeOfDay:             timeOfDay,
		Weather:               weatherStr,
		BiomeID:               biomeID,
		Stage:                 currentStage,
		Routine:               currentRoutine,
		Shift:                 shiftStr,
		ArmorRating:           armorRating,
		StateStack:            stateStack,
		Inventory:             invItems,
		Blackboard:            blackboard,
		Reflections:           refl,
	}

	data, err := json.Marshal(resp)
	if err == nil {
		data = append(data, '\n')
		_, _ = conn.Write(data)
	}
}
