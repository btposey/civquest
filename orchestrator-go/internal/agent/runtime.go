package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
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
)

// StreamConnector is an interface allowing abstraction over bridge.Client or custom stream providers.
type StreamConnector interface {
	ConnectAgent(ctx context.Context, opts ...grpc.CallOption) (civquestv1.CivQuestBridgeService_ConnectAgentClient, error)
}

// Config provides initial configuration options for constructing an AgentRuntime.
type Config struct {
	AgentID         string
	ClanID          string
	IdentityConfig  *xdg.AgentIdentityConfig
	Resolver        *xdg.Resolver
	AgentDB         *db.AgentDB
	BridgeClient    *bridge.Client
	StreamConnector StreamConnector
	SocketPath      string
	IntentQueueSize int
	OnDeath         func(*civquestv1.DeathEvent)
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

// AgentRuntime manages the isolated OS-process lifecycle for an individual settler.
// It maintains the 5 Hz gRPC telemetry stream, processes incoming sensory frames,
// synchronizes local state and inventory caches, and serves live diagnostics over a UNIX socket.
type AgentRuntime struct {
	agentID        string
	clanID         string
	identityConfig *xdg.AgentIdentityConfig
	resolver       *xdg.Resolver
	agentDB        *db.AgentDB
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
	hasCachedInventory bool
	lastCachedItems    []CachedItem
	lastEatingTick     int64

	// System 1 Reflexes and Safe Node Graph Cache
	safeNodesMu    sync.RWMutex
	safeNodesCache []db.MapNode

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

	runtime := &AgentRuntime{
		agentID:         agentID,
		clanID:          clanID,
		identityConfig:  cfg.IdentityConfig,
		resolver:        r,
		agentDB:         cfg.AgentDB,
		bridgeClient:    cfg.BridgeClient,
		connector:       connector,
		intentQueue:     make(chan *civquestv1.AgentIntent, queueSize),
		socketPath:      sockPath,
		pid:             os.Getpid(),
		startTime:       time.Now().UTC(),
		status:          "INITIALIZING",
		onDeath:         cfg.OnDeath,
		done:            make(chan struct{}),
		socketDone:      make(chan struct{}),
		lastCachedItems: nil,
	}

	runtime.loadSafeNodes()

	return runtime, nil
}

// AgentID returns the unique identifier for this settler avatar.
func (r *AgentRuntime) AgentID() string {
	return r.agentID
}

// ClanID returns the clan identifier for this settler.
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
	r.status = "RUNNING"
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
	if frame == nil {
		return
	}

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

	// Trigger permadeath terminal callback if death event occurred
	if deathEv != nil && r.onDeath != nil {
		r.onDeath(deathEv)
	}

	// Evaluate deterministic System 1 combat reflexes and survival panic
	if deathEv == nil {
		r.evaluateSystem1Reflexes(frame)
	}
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
		r.safeNodesMu.Lock()
		r.safeNodesCache = safe
		r.safeNodesMu.Unlock()
	}
}

// SetSafeNodes sets cached safe nodes for flee calculations.
func (r *AgentRuntime) SetSafeNodes(nodes []db.MapNode) {
	r.safeNodesMu.Lock()
	defer r.safeNodesMu.Unlock()
	r.safeNodesCache = nodes
}

// SafeNodes returns a defensive copy of currently cached safe nodes.
func (r *AgentRuntime) SafeNodes() []db.MapNode {
	r.safeNodesMu.RLock()
	defer r.safeNodesMu.RUnlock()
	res := make([]db.MapNode, len(r.safeNodesCache))
	copy(res, r.safeNodesCache)
	return res
}

// evaluateSystem1Reflexes evaluates deterministic fight-or-flight combat reflexes and survival panic.
func (r *AgentRuntime) evaluateSystem1Reflexes(frame *civquestv1.TelemetryFrame) {
	if frame == nil || frame.Status == nil || r.IsDead() {
		return
	}

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

	if !hostilesPresent && !damageTaken && !lowHealth && !lowFood {
		return
	}

	bravery := 0.5
	if r.identityConfig != nil {
		bravery = r.identityConfig.Traits.Bravery
	}

	r.safeNodesMu.RLock()
	safeNodes := r.safeNodesCache
	r.safeNodesMu.RUnlock()

	canEat := true
	r.mu.RLock()
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

	result := system1.EvaluateReflexes(ctx)
	if !result.ShouldAct {
		return
	}

	if result.EatenFoodSlot >= 0 {
		r.mu.Lock()
		r.lastEatingTick = frame.WorldTick
		r.mu.Unlock()
	}

	for _, intent := range result.Intents {
		_ = r.QueueIntent(intent)
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
