package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
)

// inMemBridgeStream implements grpc.BidiStreamingClient[IntentBatch, TelemetryFrame]
// to run AgentRuntime.Start(ctx) deterministically in-process without TCP/socket overhead.
type inMemBridgeStream struct {
	ctx       context.Context
	frameCh   chan *civquestv1.TelemetryFrame
	recvReady chan struct{}
	onBatch   func(*civquestv1.IntentBatch)
	closed    atomic.Bool
}

func newInMemBridgeStream(ctx context.Context, onBatch func(*civquestv1.IntentBatch)) *inMemBridgeStream {
	return &inMemBridgeStream{
		ctx:       ctx,
		frameCh:   make(chan *civquestv1.TelemetryFrame),
		recvReady: make(chan struct{}, 1),
		onBatch:   onBatch,
	}
}

func (s *inMemBridgeStream) Send(batch *civquestv1.IntentBatch) error {
	if s.closed.Load() {
		return io.EOF
	}
	if s.onBatch != nil && batch != nil {
		s.onBatch(batch)
	}
	return nil
}

func (s *inMemBridgeStream) Recv() (*civquestv1.TelemetryFrame, error) {
	select {
	case s.recvReady <- struct{}{}:
	default:
	}
	select {
	case f, ok := <-s.frameCh:
		if !ok {
			return nil, io.EOF
		}
		return f, nil
	case <-s.ctx.Done():
		return nil, io.EOF
	}
}

func (s *inMemBridgeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *inMemBridgeStream) Trailer() metadata.MD         { return nil }
func (s *inMemBridgeStream) CloseSend() error {
	s.closed.Store(true)
	return nil
}
func (s *inMemBridgeStream) Context() context.Context { return s.ctx }
func (s *inMemBridgeStream) SendMsg(m any) error {
	b, ok := m.(*civquestv1.IntentBatch)
	if !ok {
		return fmt.Errorf("unexpected SendMsg type %T", m)
	}
	return s.Send(b)
}
func (s *inMemBridgeStream) RecvMsg(m any) error {
	f, err := s.Recv()
	if err != nil {
		return err
	}
	dst, ok := m.(*civquestv1.TelemetryFrame)
	if !ok {
		return fmt.Errorf("unexpected RecvMsg type %T", m)
	}
	*dst = *f
	return nil
}

type inMemStreamConnector struct {
	stream *inMemBridgeStream
}

func (c *inMemStreamConnector) ConnectAgent(
	_ context.Context,
	_ ...grpc.CallOption,
) (grpc.BidiStreamingClient[civquestv1.IntentBatch, civquestv1.TelemetryFrame], error) {
	return c.stream, nil
}

// motorFSMState mirrors NeoForge MotorState.java.
type motorFSMState int

const (
	motorIdle motorFSMState = iota
	motorMoving
	motorStuckRecovery
	motorSurveying
)

// actuatorSimAgent faithfully simulates NeoForge's AgentMotorFSM (including 20-tick
// STUCK_RECOVERY hold) + IntentActuator (including pendingBatches FIFO queue and
// back-to-back queue drain receipts) connected to a live running AgentRuntime.
type actuatorSimAgent struct {
	id          string
	runtime     *agent.AgentRuntime
	agentDB     *db.AgentDB
	stream      *inMemBridgeStream
	cancel      context.CancelFunc
	startDone   chan error
	world       *simWorld
	recipeGraph *crafting.RecipeGraph
	recipesByID map[string]*civquestv1.RecipeNode

	// Simulated world & avatar state
	mu        sync.Mutex
	pos       civquestv1.Vec3
	spawnPos  civquestv1.Vec3
	yaw       float32
	health    float32
	foodLevel int32
	inventory []*civquestv1.ItemStackProto

	// AgentMotorFSM mirror
	motorState             motorFSMState
	navState               civquestv1.NavigationState
	navTarget              *civquestv1.Vec3
	navTol                 float64
	navStallTicks          int64
	stuckHold              int // ticks telemetry keeps reporting STUCK after the Move failed
	stuckFacts             *civquestv1.MotorFacts
	lastNavTarget          *civquestv1.Vec3
	lastAsyncSucceeded     bool
	lastAsyncFailureReason string

	// Survey state
	surveyActive       *civquestv1.SurveyHorizonIntent
	surveyOrigin       civquestv1.Vec3
	surveyCompleteTick int64
	pendingReport      *civquestv1.HorizonSurveyReport

	// IntentActuator BatchSession mirror
	activeBatch    *civquestv1.IntentBatch
	activeIndex    int
	batchStatus    civquestv1.IntentReceipt_ReceiptStatus
	failureReason  string
	dispatched     bool
	pendingBatches []*civquestv1.IntentBatch
	lastReceipt    *civquestv1.IntentReceipt

	// Obstructed target predicate (simulates reachable-looking ground coords that Baritone fails to path)
	isObstructedNav func(pos civquestv1.Vec3, target *civquestv1.Vec3) bool

	// Telemetry & RED diagnostic counters
	currentWorldTick            int64
	surveysCompleted            int
	surveyEmissions             []surveyEmitRecord
	navHistory                  []navTargetRecord
	craftedItems                []string
	craftIntentsEmitted         []string
	harvestedLogs               int
	maxDistFromSpawn            float64
	farthestPos                 civquestv1.Vec3
	stuckWindowCount            int
	unstuckInCurrentStuckWindow int
	maxUnstuckInAnyStuckWindow  int
	totalUnstuckNavsEmitted     int
	pendingQueueMaxDepth        int
	queueDrainCompletedCount    int
	allNavTargets               []*civquestv1.Vec3
}

func newActuatorSimAgent(
	t *testing.T,
	id string,
	clanID string,
	spawn civquestv1.Vec3,
	res *xdg.Resolver,
	sockDir string,
	civicDB *db.CivicRegistryDB,
	contemplator *system2.ContemplationService,
	world *simWorld,
) *actuatorSimAgent {
	t.Helper()

	if err := res.InitAgentDirectories(id); err != nil {
		t.Fatalf("InitAgentDirectories(%s): %v", id, err)
	}
	cfg := xdg.DefaultAgentIdentityConfig(id)
	cfg.ClanID = clanID
	cfg.Profession = "EXPLORER"
	cfg.Stage = "BOOTSTRAP"
	if err := res.SaveAgentConfig(cfg); err != nil {
		t.Fatalf("SaveAgentConfig(%s): %v", id, err)
	}

	agentDB, err := db.OpenAgentDB(res.AgentDataDir(id))
	if err != nil {
		t.Fatalf("OpenAgentDB(%s): %v", id, err)
	}

	sa := &actuatorSimAgent{
		id:          id,
		agentDB:     agentDB,
		world:       world,
		pos:         spawn,
		spawnPos:    spawn,
		farthestPos: spawn,
		health:      20.0,
		foodLevel:   20,
		motorState:  motorIdle,
		navState:    civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		batchStatus: civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED,
	}

	nodes := vanillaTestRecipeNodes()
	sa.recipeGraph = crafting.NewRecipeGraph(nodes)
	sa.recipesByID = make(map[string]*civquestv1.RecipeNode, len(nodes))
	for _, n := range nodes {
		sa.recipesByID[strings.ToLower(n.RecipeId)] = n
	}

	ctx, cancel := context.WithCancel(context.Background())
	sa.cancel = cancel
	sa.stream = newInMemBridgeStream(ctx, func(batch *civquestv1.IntentBatch) {
		sa.submitBatch(batch)
	})

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:              id,
		ClanID:               clanID,
		IdentityConfig:       cfg,
		Resolver:             res,
		AgentDB:              agentDB,
		CivicRegistryDB:      civicDB,
		ContemplationService: contemplator,
		StreamConnector:      &inMemStreamConnector{stream: sa.stream},
		SocketPath:           filepath.Join(sockDir, id+".sock"),
	})
	if err != nil {
		cancel()
		_ = agentDB.Close()
		t.Fatalf("NewRuntime(%s): %v", id, err)
	}
	rt.System1State().SetRecipeGraph(sa.recipeGraph)
	sa.runtime = rt
	sa.startDone = make(chan error, 1)
	go func() {
		sa.startDone <- rt.Start(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Status() == "RUNNING" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rt.Status() != "RUNNING" {
		t.Fatalf("agent %s runtime failed to reach RUNNING (status=%s)", id, rt.Status())
	}
	select {
	case <-sa.stream.recvReady:
	case <-time.After(3 * time.Second):
		t.Fatalf("agent %s recvLoop failed to enter initial Recv", id)
	}
	return sa
}

func (a *actuatorSimAgent) close() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.runtime != nil {
		a.runtime.AwaitPOIPersistence()
		a.runtime.Stop()
	}
	if a.startDone != nil {
		select {
		case <-a.startDone:
		case <-time.After(2 * time.Second):
		}
	}
	if a.agentDB != nil {
		_ = a.agentDB.Close()
	}
}

func (a *actuatorSimAgent) deliverFrame(t *testing.T, frame *civquestv1.TelemetryFrame) {
	t.Helper()

	select {
	case a.stream.frameCh <- frame:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: timeout delivering frame tick %d", a.id, frame.WorldTick)
	}

	select {
	case <-a.stream.recvReady:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: timeout waiting for HandleTelemetryFrame completion at tick %d", a.id, frame.WorldTick)
	}

	// Allow sendLoop to flush any queued intents into stream.Send -> submitBatch.
	dl := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(dl) && a.runtime.SlotQueue().HasPending() {
		time.Sleep(200 * time.Microsecond)
	}
	time.Sleep(500 * time.Microsecond)
}

func isSessionInFlightSim(status civquestv1.IntentReceipt_ReceiptStatus, active *civquestv1.IntentBatch) bool {
	return active != nil && (status == civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED ||
		status == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING ||
		status == civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX)
}

func isSurveyOnlyBatchSim(batch *civquestv1.IntentBatch) bool {
	if batch == nil || len(batch.Intents) == 0 {
		return false
	}
	for _, in := range batch.Intents {
		if in == nil || in.GetSurveyHorizon() == nil {
			return false
		}
	}
	return true
}

func (a *actuatorSimAgent) isPocketCraftOnlyBatchSim(batch *civquestv1.IntentBatch) bool {
	if batch == nil || len(batch.Intents) == 0 {
		return false
	}
	for _, in := range batch.Intents {
		ci := in.GetCraftItem()
		if ci == nil {
			return false
		}
		node := a.recipesByID[strings.ToLower(strings.TrimSpace(ci.RecipeId))]
		if node == nil || node.Requires_3X3 {
			return false
		}
	}
	return true
}

func (a *actuatorSimAgent) submitBatch(batch *civquestv1.IntentBatch) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if batch == nil {
		return
	}

	// Ignore handshake / empty non-flush keepalive batch
	if !batch.FlushPeriphery && len(batch.Intents) == 0 && batch.BatchId == 0 {
		return
	}

	// Record telemetry on all non-handshake intents sent by Go
	for _, in := range batch.Intents {
		if in == nil {
			continue
		}
		if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
			a.allNavTargets = append(a.allNavTargets, &civquestv1.Vec3{
				X: nav.Target.X, Y: nav.Target.Y, Z: nav.Target.Z,
			})
			isUnstuckNav := nav.ArrivalTolerance == 1 &&
				a.navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK
			if isUnstuckNav {
				a.totalUnstuckNavsEmitted++
				if a.stuckHold > 0 {
					a.unstuckInCurrentStuckWindow++
					if a.unstuckInCurrentStuckWindow > a.maxUnstuckInAnyStuckWindow {
						a.maxUnstuckInAnyStuckWindow = a.unstuckInCurrentStuckWindow
					}
				}
			}
		}
		if sh := in.GetSurveyHorizon(); sh != nil {
			a.surveyEmissions = append(a.surveyEmissions, surveyEmitRecord{
				Tick:        a.currentWorldTick,
				X:           a.pos.X,
				Y:           a.pos.Y,
				Z:           a.pos.Z,
				InFlight:    a.surveyActive != nil,
				QueuedDepth: len(a.pendingBatches),
			})
		}
		if ci := in.GetCraftItem(); ci != nil {
			a.craftIntentsEmitted = append(a.craftIntentsEmitted, ci.RecipeId)
		}
	}

	if batch.FlushPeriphery {
		a.pendingBatches = nil
		a.activeBatch = nil
		a.activeIndex = 0
		a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED
		a.failureReason = ""
		a.dispatched = false
		a.motorState = motorIdle
		a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
		a.navTarget = nil
		a.surveyActive = nil
		if len(batch.Intents) == 0 {
			a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED
			a.lastReceipt = &civquestv1.IntentReceipt{
				BatchId: batch.BatchId,
				Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
			}
			return
		}
	} else if isSessionInFlightSim(a.batchStatus, a.activeBatch) {
		if isSurveyOnlyBatchSim(batch) && a.motorState == motorSurveying {
			return
		}
		if a.isPocketCraftOnlyBatchSim(batch) {
			for _, in := range batch.Intents {
				if ci := in.GetCraftItem(); ci != nil {
					a.executeCraftSim(ci)
				}
			}
			return
		}
		// Real IntentActuator.java:215 queues all other batches into pendingBatches!
		a.pendingBatches = append(a.pendingBatches, batch)
		if len(a.pendingBatches) > a.pendingQueueMaxDepth {
			a.pendingQueueMaxDepth = len(a.pendingBatches)
		}
		return
	} else if len(batch.Intents) == 0 {
		a.activeBatch = batch
		a.activeIndex = 0
		a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED
		a.lastReceipt = &civquestv1.IntentReceipt{
			BatchId: batch.BatchId,
			Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
		}
		return
	}

	a.activeBatch = batch
	a.activeIndex = 0
	a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED
	a.failureReason = ""
	a.dispatched = false
	a.advanceBatchLocked(a.currentWorldTick)
}

func (a *actuatorSimAgent) actuateIntentLocked(in *civquestv1.AgentIntent, worldTick int64) bool {
	if in == nil {
		return true
	}
	switch act := in.Action.(type) {
	case *civquestv1.AgentIntent_Navigate:
		nav := act.Navigate
		if nav == nil || nav.Target == nil {
			return false
		}
		tgt := &civquestv1.Vec3{X: nav.Target.X, Y: nav.Target.Y, Z: nav.Target.Z}
		a.navHistory = append(a.navHistory, navTargetRecord{
			Tick:   worldTick,
			Target: tgt,
		})
		a.navTarget = tgt
		a.lastNavTarget = tgt
		a.navTol = float64(nav.ArrivalTolerance)
		if a.navTol <= 0 {
			a.navTol = 2.0
		}
		a.navStallTicks = 0
		a.motorState = motorMoving
		a.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING

		// Immediate arrival check for 1-block unstuck navigates already within tolerance
		dist := math.Hypot(tgt.X-a.pos.X, tgt.Z-a.pos.Z)
		if a.navTol <= 1.0 && dist <= 1.5 && (a.isObstructedNav == nil || !a.isObstructedNav(a.pos, tgt)) {
			a.motorState = motorIdle
			a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
			a.navTarget = nil
			a.lastAsyncSucceeded = true
			a.lastAsyncFailureReason = ""
		}
		return true

	case *civquestv1.AgentIntent_SurveyHorizon:
		sh := act.SurveyHorizon
		if sh == nil {
			return false
		}
		numSec := sh.NumSectors
		if numSec <= 0 {
			numSec = 36
		}
		tps := sh.TicksPerSector
		if tps <= 0 {
			tps = 2
		}
		a.surveyActive = sh
		a.surveyOrigin = a.pos
		a.surveyCompleteTick = worldTick + int64(numSec)*int64(tps)
		a.motorState = motorSurveying
		a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
		a.navTarget = nil
		return true

	case *civquestv1.AgentIntent_InteractBlock:
		ib := act.InteractBlock
		if ib == nil || ib.Pos == nil {
			return false
		}
		switch ib.InteractionType {
		case civquestv1.InteractBlockIntent_HARVEST_BREAK:
			bx := float64(ib.Pos.X) + 0.5
			by := float64(ib.Pos.Y) + 0.5
			bz := float64(ib.Pos.Z) + 0.5
			dist3D := math.Sqrt(math.Pow(bx-a.pos.X, 2) + math.Pow(by-a.pos.Y, 2) + math.Pow(bz-a.pos.Z, 2))
			a.world.mu.Lock()
			key := worldBlockKey{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}
			blkID, exists := a.world.blocks[key]
			if exists && dist3D <= 4.5 && math.Abs(float64(ib.Pos.Y)-a.pos.Y) <= 4.5 {
				delete(a.world.blocks, key)
				a.world.mu.Unlock()
				a.addInventoryItemLocked(blkID, 1)
				if strings.HasSuffix(blkID, "_log") {
					a.harvestedLogs++
				}
				a.lastAsyncSucceeded = true
				return true
			}
			a.world.mu.Unlock()
			a.lastAsyncSucceeded = false
			a.lastAsyncFailureReason = "block_unreachable_or_missing"
			return false

		case civquestv1.InteractBlockIntent_PLACE_BLOCK:
			itemID := strings.ToLower(strings.TrimSpace(ib.BlockOrItemId))
			if a.consumeInventoryItemLocked(itemID, 1) {
				a.world.mu.Lock()
				a.world.blocks[worldBlockKey{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}] = itemID
				a.world.mu.Unlock()
				a.lastAsyncSucceeded = true
				return true
			}
			a.lastAsyncSucceeded = false
			a.lastAsyncFailureReason = "missing_place_item"
			return false
		}
		a.lastAsyncSucceeded = true
		return true

	case *civquestv1.AgentIntent_CraftItem:
		ci := act.CraftItem
		if ci == nil {
			return false
		}
		if a.executeCraftSim(ci) {
			a.lastAsyncSucceeded = true
			return true
		}
		a.lastAsyncSucceeded = false
		a.lastAsyncFailureReason = "craft_missing_ingredients_or_table"
		return false

	default:
		a.lastAsyncSucceeded = true
		return true
	}
}

func (a *actuatorSimAgent) advanceBatchLocked(worldTick int64) {
	for a.activeBatch != nil {
		count := len(a.activeBatch.Intents)
		if a.activeIndex >= count {
			a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED
			a.failureReason = ""
			a.lastReceipt = &civquestv1.IntentReceipt{
				BatchId:       a.activeBatch.BatchId,
				ActiveIndex:   int32(a.activeIndex),
				Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
				FailureReason: "",
			}
			if len(a.pendingBatches) > 0 {
				next := a.pendingBatches[0]
				a.pendingBatches = a.pendingBatches[1:]
				a.activeBatch = next
				a.activeIndex = 0
				a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED
				a.failureReason = ""
				a.dispatched = false
				a.queueDrainCompletedCount++
				continue
			}
			a.activeBatch = nil
			break
		}

		cur := a.activeBatch.Intents[a.activeIndex]
		if !a.dispatched {
			a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING
			a.dispatched = true
			ok := a.actuateIntentLocked(cur, worldTick)
			if !ok {
				a.handleBatchFailureLocked()
				if a.activeBatch != nil {
					continue
				}
				break
			}
			if a.motorState == motorIdle {
				if !a.lastAsyncSucceeded {
					a.handleBatchFailureLocked()
					if a.activeBatch != nil {
						continue
					}
					break
				}
				a.activeIndex++
				a.dispatched = false
				continue
			}
			a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING
			break
		} else {
			if a.motorState == motorIdle {
				if !a.lastAsyncSucceeded {
					a.handleBatchFailureLocked()
					if a.activeBatch != nil {
						continue
					}
					break
				}
				a.activeIndex++
				a.dispatched = false
				continue
			}
			a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING
			break
		}
	}
}

func (a *actuatorSimAgent) handleBatchFailureLocked() {
	reason := a.lastAsyncFailureReason
	if reason == "" {
		reason = "navigation_stuck"
	}
	a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED
	a.failureReason = reason
	a.lastReceipt = &civquestv1.IntentReceipt{
		BatchId:       a.activeBatch.BatchId,
		FrameId:       a.activeBatch.FrameId,
		ActiveIndex:   int32(a.activeIndex),
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: reason,
	}
	if reason == "Navigation stuck" {
		a.lastReceipt.Facts, a.stuckFacts = a.stuckFacts, nil
	}
	if len(a.pendingBatches) > 0 {
		next := a.pendingBatches[0]
		a.pendingBatches = a.pendingBatches[1:]
		a.activeBatch = next
		a.activeIndex = 0
		a.batchStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED
		a.failureReason = ""
		a.dispatched = false
		a.queueDrainCompletedCount++
		return
	}
	a.activeBatch = nil
}

func (a *actuatorSimAgent) stepTick(singleTick int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentWorldTick = singleTick

	if a.stuckHold > 0 {
		a.stuckHold--
		if a.stuckHold == 0 && a.navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
			a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
		}
	}
	switch a.motorState {
	case motorSurveying:
		a.yaw = float32(math.Mod(float64(a.yaw)+5.0, 360.0))
		if singleTick >= a.surveyCompleteTick {
			a.pendingReport = a.world.raycastHorizon(
				a.surveyOrigin.X, a.surveyOrigin.Y, a.surveyOrigin.Z,
				singleTick, a.surveyActive.NumSectors, a.surveyActive.MaxRange,
			)
			a.surveysCompleted++
			a.surveyActive = nil
			a.lastAsyncSucceeded = true
			a.lastAsyncFailureReason = ""
			a.motorState = motorIdle
		}

	case motorMoving:
		if a.navTarget == nil {
			a.lastAsyncSucceeded = true
			a.motorState = motorIdle
			break
		}
		dx0, dz0 := a.navTarget.X-a.pos.X, a.navTarget.Z-a.pos.Z
		obstructed := a.isObstructedNav != nil && a.isObstructedNav(a.pos, a.navTarget)
		if obstructed {
			a.navStallTicks++
			if a.navStallTicks >= 16 {
				// Post cq-2sr.14: the Move fails immediately with MotorFacts (no STUCK_RECOVERY).
				a.stuckFacts = &civquestv1.MotorFacts{
					Stuck: true, StuckTicks: 16,
					BlockedBearingDeg: float32(math.Mod(math.Atan2(-dx0, dz0)*180.0/math.Pi+360.0, 360.0)),
					Progress:          math.Hypot(dx0, dz0),
				}
				a.lastAsyncSucceeded = false
				a.lastAsyncFailureReason = "Navigation stuck"
				a.motorState = motorIdle
				a.stuckHold = 20
				a.navState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
				a.navTarget = nil
				a.navStallTicks = 0
				a.stuckWindowCount++
				a.unstuckInCurrentStuckWindow = 0
				if len(a.navHistory) > 0 {
					a.navHistory[len(a.navHistory)-1].Stuck = true
				}
			}
			break
		}

		dx := a.navTarget.X - a.pos.X
		dz := a.navTarget.Z - a.pos.Z
		dist := math.Hypot(dx, dz)
		const stepDist = 0.25
		if dist <= math.Max(a.navTol, 1.5) || dist <= stepDist {
			if a.navTol > 1.0 || dist > a.navTol {
				a.pos.X = a.navTarget.X
				a.pos.Y = a.navTarget.Y
				a.pos.Z = a.navTarget.Z
			}
			a.motorState = motorIdle
			a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
			a.navTarget = nil
			a.lastAsyncSucceeded = true
			a.lastAsyncFailureReason = ""
		} else {
			nx := a.pos.X + (dx/dist)*stepDist
			nz := a.pos.Z + (dz/dist)*stepDist
			a.pos.X = nx
			a.pos.Z = nz
			a.yaw = float32(math.Mod(math.Atan2(-dx, dz)*180.0/math.Pi+360.0, 360.0))
		}

	}

	dSpawn := math.Hypot(a.pos.X-a.spawnPos.X, a.pos.Z-a.spawnPos.Z)
	if dSpawn > a.maxDistFromSpawn {
		a.maxDistFromSpawn = dSpawn
		a.farthestPos = a.pos
	}

	a.advanceBatchLocked(singleTick)
}

func (a *actuatorSimAgent) stepFrame(worldTick, dt int64) {
	startTick := worldTick - dt + 1
	for t := startTick; t <= worldTick; t++ {
		a.stepTick(t)
	}
}

func (a *actuatorSimAgent) buildFrame(worldTick, timeOfDay int64) *civquestv1.TelemetryFrame {
	a.mu.Lock()
	posCopy := a.pos
	yaw := a.yaw
	health := a.health
	food := a.foodLevel
	navState := a.navState
	receipt := a.lastReceipt
	a.lastReceipt = nil
	report := a.pendingReport
	a.pendingReport = nil
	invCopy := make([]*civquestv1.ItemStackProto, 0, len(a.inventory))
	for _, it := range a.inventory {
		if it != nil && it.Count > 0 {
			invCopy = append(invCopy, &civquestv1.ItemStackProto{
				Slot:   it.Slot,
				ItemId: it.ItemId,
				Count:  it.Count,
			})
		}
	}
	a.mu.Unlock()

	a.world.mu.Lock()
	var blocks []*civquestv1.VisibleBlock
	for k, id := range a.world.blocks {
		bx := float64(k.X) + 0.5
		bz := float64(k.Z) + 0.5
		if math.Hypot(bx-posCopy.X, bz-posCopy.Z) <= 16.0 && math.Abs(float64(k.Y)+0.5-posCopy.Y) <= 16.0 {
			blocks = append(blocks, &civquestv1.VisibleBlock{
				Pos:     &civquestv1.BlockPosProto{X: k.X, Y: k.Y, Z: k.Z},
				BlockId: id,
			})
		}
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Pos.X != blocks[j].Pos.X {
			return blocks[i].Pos.X < blocks[j].Pos.X
		}
		if blocks[i].Pos.Y != blocks[j].Pos.Y {
			return blocks[i].Pos.Y < blocks[j].Pos.Y
		}
		return blocks[i].Pos.Z < blocks[j].Pos.Z
	})
	biomeID := a.world.biomeID
	if biomeID == "" {
		biomeID = "minecraft:plains"
	}
	a.world.mu.Unlock()

	return &civquestv1.TelemetryFrame{
		WorldTick: worldTick,
		TimeOfDay: ((timeOfDay % 24000) + 24000) % 24000,
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		BiomeId:   biomeID,
		Status: &civquestv1.AgentStatus{
			AgentId:         a.id,
			Health:          health,
			MaxHealth:       20.0,
			FoodLevel:       food,
			Stamina:         100.0,
			Position:        &civquestv1.Vec3{X: posCopy.X, Y: posCopy.Y, Z: posCopy.Z},
			Yaw:             yaw,
			NavigationState: navState,
			Inventory:       invCopy,
		},
		InterestingBlocks: blocks,
		Receipt:           receipt,
		HorizonSurvey:     report,
	}
}

func (a *actuatorSimAgent) addInventoryItemLocked(itemID string, count int32) {
	if count <= 0 || itemID == "" {
		return
	}
	norm := strings.ToLower(strings.TrimSpace(itemID))
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm && it.Count < 64 {
			add := count
			if it.Count+add > 64 {
				add = 64 - it.Count
			}
			it.Count += add
			count -= add
			if count == 0 {
				return
			}
		}
	}
	for count > 0 {
		add := count
		if add > 64 {
			add = 64
		}
		a.inventory = append(a.inventory, &civquestv1.ItemStackProto{
			Slot:   int32(len(a.inventory)),
			ItemId: norm,
			Count:  add,
		})
		count -= add
	}
}

func (a *actuatorSimAgent) consumeInventoryItemLocked(itemID string, count int32) bool {
	norm := strings.ToLower(strings.TrimSpace(itemID))
	total := int32(0)
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm {
			total += it.Count
		}
	}
	if total < count {
		return false
	}
	rem := count
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm && rem > 0 {
			take := it.Count
			if take > rem {
				take = rem
			}
			it.Count -= take
			rem -= take
		}
	}
	filtered := a.inventory[:0]
	for _, it := range a.inventory {
		if it != nil && it.Count > 0 {
			filtered = append(filtered, it)
		}
	}
	a.inventory = filtered
	return true
}

func (a *actuatorSimAgent) executeCraftSim(ci *civquestv1.CraftItemIntent) bool {
	node := a.recipesByID[strings.ToLower(strings.TrimSpace(ci.RecipeId))]
	if node == nil || node.Result == nil {
		return false
	}
	batches := ci.BatchCount
	if batches <= 0 {
		batches = 1
	}
	if node.Requires_3X3 {
		hasTable := false
		a.world.mu.Lock()
		for k, id := range a.world.blocks {
			if id == "minecraft:crafting_table" &&
				math.Hypot(float64(k.X)+0.5-a.pos.X, float64(k.Z)+0.5-a.pos.Z) <= 4.5 {
				hasTable = true
				break
			}
		}
		a.world.mu.Unlock()
		if !hasTable {
			return false
		}
	}
	for b := int32(0); b < batches; b++ {
		for _, slot := range node.IngredientSlots {
			needed := slot.Count
			var chosenID string
			for _, cand := range slot.AcceptedItemIds {
				for _, it := range a.inventory {
					if it != nil && strings.EqualFold(it.ItemId, cand) && it.Count >= needed {
						chosenID = cand
						break
					}
				}
				if chosenID != "" {
					break
				}
			}
			if chosenID == "" || !a.consumeInventoryItemLocked(chosenID, needed) {
				return false
			}
		}
		a.addInventoryItemLocked(node.Result.ItemId, node.Result.Count)
		a.craftedItems = append(a.craftedItems, node.Result.ItemId)
	}
	return true
}

func newCountingContemplationServer(action, target, goal string) (*httptest.Server, *system2.Client, *system2.ContemplationService, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		payload, _ := json.Marshal(system2.StrategicContemplationResponse{
			ImmediateGoal:    goal,
			ShortTermGoal:    "EXPLORE_AND_ESTABLISH_OUTPOST",
			LongTermGoal:     "CHART_REGIONAL_ATLAS",
			ReflectiveMemory: "Synthesized expedition directive.",
			Directive: &system2.StrategicDirective{
				Action: action,
				Target: target,
				Reason: "regression test stub",
			},
			Rationale: "Test deterministic directive flow.",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "stub-contemplation",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))

	client, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: srv.URL + "/v1", Model: "stub-model"}},
		system2.WithHTTPClient(srv.Client()),
	)
	if err != nil {
		srv.Close()
		panic(err)
	}
	return srv, client, system2.NewContemplationService(client), &calls
}

func makeShortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cq-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// Scenario 1: WorldTick=0 handshake frame followed immediately by daytime WorldTick=336500.
func TestIntegration_WorldTickZeroHandshake_DoesNotTriggerDaytimeIdleOnTickOne(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	sockDir := makeShortSocketDir(t)

	srv, llmClient, contemplator, calls := newCountingContemplationServer(
		system2.DirectiveScoutFrontier,
		"outer valley",
		"SCOUT_FRONTIER: Chart surroundings",
	)
	defer srv.Close()
	defer llmClient.Close()

	world := newPlaytest0849World(false, false, false)
	able := newActuatorSimAgent(
		t, "able", "pioneers",
		civquestv1.Vec3{X: -30.0, Y: 86.0, Z: 0.0},
		res, sockDir, nil, contemplator, world,
	)
	defer able.close()

	// Exact initial handshake frame from CivQuestBridgeServiceImpl.java:84-95:
	// WorldTick=0, TimeOfDay=0, non-nil AgentStatus without Position.
	handshakeFrame := &civquestv1.TelemetryFrame{
		WorldTick: 0,
		TimeOfDay: 0,
		Status: &civquestv1.AgentStatus{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
		},
	}
	able.deliverFrame(t, handshakeFrame)

	// Immediately follow with 20 daytime frames (80 ticks) starting at WorldTick=336500, TimeOfDay=7175.
	for i := 0; i < 20; i++ {
		worldTick := int64(336500) + int64(i)*4
		tod := int64(7175) + int64(i)*4
		able.stepFrame(worldTick, 4)
		frame := able.buildFrame(worldTick, tod)
		able.deliverFrame(t, frame)
	}

	// Give any async contemplation goroutine time to hit the stub LLM server if triggered.
	time.Sleep(120 * time.Millisecond)

	gotCalls := calls.Load()
	dir, dirActive := able.runtime.System1State().ActiveStrategicDirective(336580)
	t.Logf("[RED-DIAG] WorldTickZeroHandshake: contemplationCalls=%d dirActive=%v dirAction=%s",
		gotCalls, dirActive, dir.Action)

	if gotCalls != 0 {
		t.Errorf("FAIL: STRATEGIC_CONTEMPLATION triggered %d time(s) within first 20 frames (80 ticks) after WorldTick=0 handshake (want 0)", gotCalls)
	}
}

// Scenario 2A: Stuck HarvestTreeTask + 20-tick STUCK_RECOVERY + pendingBatches FIFO queue drain
// blacklists the unreachable tree in <= 6 stuck windows without flooding EvaluateUnstuck and escapes.
func TestIntegration_StuckHarvestTaskAndUnstuckQueueDrain_BlacklistsAndEscapes(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	sockDir := makeShortSocketDir(t)

	// Reachable outer forests 35-55m away + obstructed ground-level oak_log at (-46, 86, -8) -> center (-45.5, 86.0, -7.5).
	world := newPlaytest0849World(false, false, false)
	for dy := int32(0); dy <= 3; dy++ {
		world.blocks[worldBlockKey{X: -46, Y: 86 + dy, Z: -8}] = "minecraft:oak_log"
	}

	carl := newActuatorSimAgent(
		t, "carl", "pioneers",
		// Start 5.5m from the trunk's near face so it is genuinely out of vanilla reach (cq-gpa.9: eye to nearest AABB point).
		civquestv1.Vec3{X: -39.5, Y: 86.0, Z: -7.5},
		res, sockDir, nil, nil, world,
	)
	defer carl.close()

	// Navigating toward (-45.5, 86.0, -7.5) is obstructed before reaching 4.5m reach,
	// triggering real 16-tick stall -> 20-tick STUCK_RECOVERY hold.
	// Meanwhile, 1-block ArrivalTolerance=1 unstuck navigates complete cleanly.
	carl.isObstructedNav = func(_ civquestv1.Vec3, tgt *civquestv1.Vec3) bool {
		if tgt == nil {
			return false
		}
		return math.Hypot(tgt.X-(-45.5), tgt.Z-(-7.5)) <= 2.5
	}

	const (
		startTick = int64(336500)
		startTOD  = int64(7175)
		dt        = int64(4)
		numFrames = 113 // 452 ticks (~450 ticks)
	)

	for frameIdx := 0; frameIdx < numFrames; frameIdx++ {
		worldTick := startTick + int64(frameIdx)*dt
		tod := startTOD + int64(frameIdx)*dt
		if frameIdx > 0 {
			carl.stepFrame(worldTick, dt)
		}
		frame := carl.buildFrame(worldTick, tod)
		carl.deliverFrame(t, frame)
	}

	endTick := startTick + int64(numFrames)*dt
	carl.mu.Lock()
	maxUnstuckWindow := carl.maxUnstuckInAnyStuckWindow
	stuckWindows := carl.stuckWindowCount
	totalUnstuck := carl.totalUnstuckNavsEmitted
	maxQDepth := carl.pendingQueueMaxDepth
	qDrainCompleted := carl.queueDrainCompletedCount
	surveyCount := len(carl.surveyEmissions)
	maxDist := carl.maxDistFromSpawn
	posNow := carl.pos
	carl.mu.Unlock()

	blacklisted := carl.runtime.System1State().IsDestinationShunned(-45.5, 86.0, -7.5, endTick)
	hasTask := carl.runtime.System1State().HasActiveTask()
	_, taskKind, taskPhase, _ := carl.runtime.System1State().ActiveTaskInfo()

	t.Logf("[DIAG-2A] carl stuckLoop: stuckWindows=%d maxUnstuckPerWindow=%d totalUnstuckNavs=%d maxPendingQ=%d qDrainCompleted=%d blacklisted=%v hasTask=%v(%s/%s) surveys=%d maxDist=%.1fm pos=(%.1f,%.1f)",
		stuckWindows, maxUnstuckWindow, totalUnstuck, maxQDepth, qDrainCompleted,
		blacklisted, hasTask, taskKind, taskPhase, surveyCount, maxDist, posNow.X, posNow.Z)

	if stuckWindows == 0 {
		t.Fatalf("Test setup error: carl never entered STUCK_RECOVERY")
	}

	// Assertion 1: Unstuck throttle (<= 1 EvaluateUnstuck NavigateIntent per 20-tick STUCK_RECOVERY window)
	if maxUnstuckWindow > 1 {
		t.Errorf("Assertion 1 (Unstuck throttle) FAIL: emitted %d EvaluateUnstuck NavigateIntents inside a single 20-tick STUCK_RECOVERY window (want <= 1)", maxUnstuckWindow)
	}

	// Assertion 2: Blacklisted unreachable tree within <= 6 stuck attempts
	if !blacklisted {
		t.Errorf("Assertion 2 (Blacklist unreachable tree) FAIL: (-45.5, 86.0, -7.5) is NOT blacklisted after %d stuck windows (pendingBatches drained %d queued unstuck batches as COMPLETED)", stuckWindows, qDrainCompleted)
	}
	if stuckWindows > 6 {
		t.Errorf("Assertion 2 (Bounded stuck attempts) FAIL: entered STUCK_RECOVERY %d times on (-45.5, 86.0, -7.5), want <= 6", stuckWindows)
	}

	// Assertion 3: Agent escapes obstructed spawn tree instead of staying trapped
	if surveyCount < 1 && maxDist <= 10.0 {
		t.Errorf("Assertion 3 (Escape obstructed tree) FAIL: carl remained trapped at spawn (surveys=%d, maxDist=%.1fm, activeTask=%s)", surveyCount, maxDist, taskKind)
	}
}

// zeroProgressResult is what runZeroProgressScenario observed.
type zeroProgressResult struct {
	pre300Calls int32
	totalCalls  int32
	// stuckTaskEnded: the harvest task the agent began with was gone at some frame before +300.
	stuckTaskEnded bool
	surveyCount    int
	maxDist        float64
	taskKind       string
	stuckWindows   int
	// maxZeroProgressTicks is the longest run of ticks the agent spent within 1.5m of where the
	// run of ticks began (zero net displacement), sampled once per frame.
	maxZeroProgressTicks int64
	hasTaskAtEnd         bool
}

// runZeroProgressScenario spawns carl among obstructedTrees (10 trees inside 16m, >= 3m apart,
// ground-level logs) and ticks 340 ticks + 240 ticks. obstructed decides which Navigate targets the
// sim cannot reach (it faithfully fails the Move with MotorFacts, as the real mod does).
func runZeroProgressScenario(t *testing.T, obstructedTrees [][2]int32, obstructed func(tgt *civquestv1.Vec3) bool) zeroProgressResult {
	t.Helper()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	sockDir := makeShortSocketDir(t)

	srv, llmClient, contemplator, calls := newCountingContemplationServer(
		system2.DirectiveScoutFrontier,
		"eastern forest",
		"SCOUT_FRONTIER: Abandon obstructed timber and scout outer frontier",
	)
	defer srv.Close()
	defer llmClient.Close()

	world := newPlaytest0849World(false, false, false)
	for _, tr := range obstructedTrees {
		for dy := int32(0); dy <= 3; dy++ {
			world.blocks[worldBlockKey{X: tr[0], Y: 86 + dy, Z: tr[1]}] = "minecraft:oak_log"
		}
	}

	carl := newActuatorSimAgent(
		t, "carl", "pioneers",
		civquestv1.Vec3{X: -40.5, Y: 86.0, Z: -7.5},
		res, sockDir, nil, contemplator, world,
	)
	defer carl.close()

	carl.isObstructedNav = func(_ civquestv1.Vec3, tgt *civquestv1.Vec3) bool {
		return tgt != nil && obstructed(tgt)
	}

	const (
		startTick      = int64(336500)
		startTOD       = int64(7175)
		dt             = int64(4)
		preCheckFrames = 85 // 340 ticks (> 300-tick daytime_idle threshold)
		postFrames     = 60 // 240 ticks for survey + outward dispersal
	)

	var r zeroProgressResult
	sawTask := false
	anchor := carl.pos
	anchorTick := startTick
	for frameIdx := 0; frameIdx < preCheckFrames; frameIdx++ {
		worldTick := startTick + int64(frameIdx)*dt
		tod := startTOD + int64(frameIdx)*dt
		if frameIdx > 0 {
			carl.stepFrame(worldTick, dt)
		}
		frame := carl.buildFrame(worldTick, tod)
		carl.deliverFrame(t, frame)
		if worldTick-startTick < 296 {
			r.pre300Calls = calls.Load()
		}
		switch active := carl.runtime.System1State().HasActiveTask(); {
		case worldTick-startTick >= 300:
		case active:
			sawTask = true
		case sawTask:
			r.stuckTaskEnded = true
		}
		carl.mu.Lock()
		pos := carl.pos
		carl.mu.Unlock()
		if math.Hypot(pos.X-anchor.X, pos.Z-anchor.Z) >= 1.5 {
			anchor, anchorTick = pos, worldTick
		}
		if d := worldTick - anchorTick; d > r.maxZeroProgressTicks {
			r.maxZeroProgressTicks = d
		}
	}

	// Wait for the async contemplation goroutine triggered at +300 ticks to apply SCOUT_FRONTIER.
	dl := time.Now().Add(2 * time.Second)
	for time.Now().Before(dl) {
		_, active := carl.runtime.System1State().ActiveStrategicDirective(startTick + int64(preCheckFrames)*dt)
		if active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Continue ticking so SCOUT_FRONTIER (if any) preempts active harvest_tree, surveys, and moves outward.
	for extra := 0; extra < postFrames; extra++ {
		worldTick := startTick + int64(preCheckFrames+extra)*dt
		tod := startTOD + int64(preCheckFrames+extra)*dt
		carl.stepFrame(worldTick, dt)
		frame := carl.buildFrame(worldTick, tod)
		carl.deliverFrame(t, frame)
	}

	endTick := startTick + int64(preCheckFrames+postFrames)*dt
	carl.mu.Lock()
	r.surveyCount = len(carl.surveyEmissions)
	surveysCompleted := carl.surveysCompleted
	r.maxDist = carl.maxDistFromSpawn
	posNow := carl.pos
	r.stuckWindows = carl.stuckWindowCount
	carl.mu.Unlock()

	dir, dirActive := carl.runtime.System1State().ActiveStrategicDirective(endTick)
	r.hasTaskAtEnd = carl.runtime.System1State().HasActiveTask()
	_, r.taskKind, _, _ = carl.runtime.System1State().ActiveTaskInfo()
	r.totalCalls = calls.Load()
	t.Logf("[DIAG-2B] carl zeroProgress: stuckWindows=%d stuckTaskEnded=%v pre300Calls=%d totalCalls=%d dir=(%s,%v) hasTask=%v(%s) surveysEmitted=%d surveysCompleted=%d maxDist=%.1fm maxZeroProgressTicks=%d pos=(%.1f,%.1f)",
		r.stuckWindows, r.stuckTaskEnded, r.pre300Calls, r.totalCalls, dir.Action, dirActive,
		r.hasTaskAtEnd, r.taskKind, r.surveyCount, surveysCompleted, r.maxDist, r.maxZeroProgressTicks, posNow.X, posNow.Z)
	return r
}

var zeroProgressTrees = [][2]int32{
	{-46, -8}, {-35, -8}, {-41, -2}, {-41, -13}, {-45, -3},
	{-36, -3}, {-45, -12}, {-36, -12}, {-50, -8}, {-31, -8},
}

func nearAnyTree(tgt *civquestv1.Vec3) bool {
	for _, tr := range zeroProgressTrees {
		if math.Hypot(tgt.X-(float64(tr[0])+0.5), tgt.Z-(float64(tr[1])+0.5)) <= 2.5 {
			return true
		}
	}
	return false
}

// Scenario 2B (a): Active HarvestTreeTask against obstructed trees, with the world otherwise open.
//
// Original (baseline) assertion: the agent sits at zero physical displacement for >= 300 ticks on
// an active harvest_tree task, so daytime_idle contemplation fires and SCOUT_FRONTIER preempts it.
// That path is NOT reachable here any more, and this is product behaviour, not a test artefact:
// Java now fails a stuck Move after 16 ticks (cq-2sr.14) and Go's Recover (ADR-037) sidesteps, gives
// up and the task fails/retargets (a tree is only blacklisted after two give-ups on the same
// target; with 10 trees each gets one here, blacklisting is asserted by
// TestIntegration_StuckHarvestTaskAndUnstuckQueueDrain_BlacklistsAndEscapes), so the agent never
// idles on the task for 300 ticks (see TestIntegration_ZeroProgressBoxedIn_* for the case where Recover
// genuinely cannot help). The strongest true invariant, asserted unconditionally: the agent never
// sits at zero progress for 300 ticks, it did get stuck (Recover ran), the stuck harvest task
// ended, and the agent has moved on (survey or > 10m).
func TestIntegration_ZeroProgressActiveTask_RecoversOnItsOwnAndMovesOn(t *testing.T) {
	r := runZeroProgressScenario(t, zeroProgressTrees, nearAnyTree)

	if r.pre300Calls != 0 {
		t.Errorf("pre300Calls = %d, want 0 before 300 ticks", r.pre300Calls)
	}
	// 2 give-ups (each <= 100-tick Recover lease) + retargeting is the product's bound; 300 is the
	// old System 2 trigger, so staying under it proves System 2 was never needed.
	if r.maxZeroProgressTicks >= 300 {
		t.Errorf("agent sat at zero progress for %d ticks (>= 300): Recover must resolve or give up first", r.maxZeroProgressTicks)
	}
	if r.stuckWindows < 1 {
		t.Errorf("test setup error: carl never got stuck (stuckWindows=%d)", r.stuckWindows)
	}
	if !r.stuckTaskEnded {
		t.Errorf("the first harvest task was still active at +300 ticks: the stuck task must have failed (Recover give-up)")
	}
	if r.surveyCount < 1 && r.maxDist <= 10.0 {
		t.Errorf("agent did not move on after the task failed (surveys=%d, maxDist=%.1fm)", r.surveyCount, r.maxDist)
	}
}

// Scenario 2B (b): the original zero-progress assertions, restored UNCONDITIONALLY. The agent is
// boxed in: every Navigate target in the world is unreachable, so Recover gives up on everything
// and nothing it (or anyone) emits can move the agent. It therefore sits at >= 300 ticks of zero
// displacement, daytime_idle System 2 contemplation must fire and its SCOUT_FRONTIER directive must
// preempt the stuck task: the agent emits a horizon survey (it cannot walk, so surveys stand in for
// "moves outward"; maxDist cannot grow).
func TestIntegration_ZeroProgressBoxedIn_TriggersDaytimeIdleAndPreemptsWithScoutFrontier(t *testing.T) {
	r := runZeroProgressScenario(t, zeroProgressTrees, func(*civquestv1.Vec3) bool { return true })

	if r.pre300Calls != 0 {
		t.Errorf("pre300Calls = %d, want 0 before 300 ticks", r.pre300Calls)
	}
	if r.maxZeroProgressTicks < 300 {
		t.Fatalf("test setup error: agent only sat at zero progress for %d ticks (< 300)", r.maxZeroProgressTicks)
	}
	if r.totalCalls < 1 {
		t.Errorf("Assertion (Idle contemplation unstarved) FAIL: contemplationCalls=%d after >= 300 ticks of zero progress (want >= 1)", r.totalCalls)
	}
	if r.surveyCount < 1 && r.maxDist <= 10.0 {
		t.Errorf("Assertion (System 2 directive execution) FAIL: SCOUT_FRONTIER did not preempt harvest_tree (surveys=%d, maxDist=%.1fm, activeTask=%s)", r.surveyCount, r.maxDist, r.taskKind)
	}
}

// Scenario 3: Active HarvestTreeTask while already holding 6 oak_logs in inventory
// must craft survival kit instead of ignoring logs until task finishes.
func TestIntegration_HarvestTreeActiveWithSixLogs_CraftsSurvivalKitInsteadOfIgnoringLogs(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	sockDir := makeShortSocketDir(t)

	world := newPlaytest0849World(false, false, false)
	// Place a visible reachable oak tree 15m West of dave at (-45, 86..91, 0) (walk + 6 logs > 60 ticks)
	// plus another nearby visible tree at (-38, 86..89, 5).
	for dy := int32(0); dy <= 5; dy++ {
		world.blocks[worldBlockKey{X: -45, Y: 86 + dy, Z: 0}] = "minecraft:oak_log"
	}
	for dy := int32(0); dy <= 3; dy++ {
		world.blocks[worldBlockKey{X: -38, Y: 86 + dy, Z: 5}] = "minecraft:oak_log"
	}

	dave := newActuatorSimAgent(
		t, "dave", "pioneers",
		civquestv1.Vec3{X: -30.0, Y: 86.0, Z: 0.0},
		res, sockDir, nil, nil, world,
	)
	defer dave.close()

	// Simulate dave picking up 6 oak_logs into inventory while HarvestTreeTask is active.
	dave.mu.Lock()
	dave.addInventoryItemLocked("minecraft:oak_log", 6)
	dave.mu.Unlock()

	treeTask := system1.NewHarvestTreeTask(-45, 86, 0, system1.StageBootstrap, 336500)
	dave.runtime.AgentFSM().StartTask(dave.runtime.System1State(), treeTask)

	const (
		startTick = int64(336500)
		startTOD  = int64(7175)
		dt        = int64(4)
		numFrames = 16 // 0..60 ticks
	)
	for frameIdx := 0; frameIdx < numFrames; frameIdx++ {
		worldTick := startTick + int64(frameIdx)*dt
		tod := startTOD + int64(frameIdx)*dt
		if frameIdx > 0 {
			dave.stepFrame(worldTick, dt)
		}
		frame := dave.buildFrame(worldTick, tod)
		dave.deliverFrame(t, frame)
	}

	dave.mu.Lock()
	emitted := append([]string(nil), dave.craftIntentsEmitted...)
	crafted := append([]string(nil), dave.craftedItems...)
	harvested := dave.harvestedLogs
	dave.mu.Unlock()

	hasTask := dave.runtime.System1State().HasActiveTask()
	t.Logf("[RED-DIAG] dave 6-log active harvest: craftIntentsEmitted=%v craftedItems=%v harvestedLogs=%d hasActiveTask=%v",
		emitted, crafted, harvested, hasTask)

	if len(emitted) == 0 {
		t.Errorf("FAIL: dave emitted 0 CraftItemIntents over 60 ticks while holding 6 oak_logs because active HarvestTreeTask ran first")
	}
	sawPlanksOrTool := false
	for _, r := range emitted {
		if strings.Contains(r, "planks") || strings.Contains(r, "stick") ||
			strings.Contains(r, "crafting_table") || strings.Contains(r, "wooden_axe") || strings.Contains(r, "wooden_sword") {
			sawPlanksOrTool = true
			break
		}
	}
	if !sawPlanksOrTool {
		t.Errorf("FAIL: expected CraftItemIntent for planks/stick/crafting_table/tools, got %v", emitted)
	}
}

// Scenario 4: All 6 daytime EXPLORERs spawned in a 4.5m x 1.5m cluster with obstructed
// oak_logs inside 16m at spawn must all run >= 1 initial SurveyHorizonIntent and disperse > 20m.
func TestIntegration_SixDaytimeExplorersWithNearbyObstructedTrees_RunInitialSurveysAndDisperse(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	sockDir := makeShortSocketDir(t)

	srv, llmClient, contemplator, calls := newCountingContemplationServer(
		system2.DirectiveScoutFrontier,
		"distant forest perimeter",
		"SCOUT_FRONTIER: Survey outer sectors and disperse",
	)
	defer srv.Close()
	defer llmClient.Close()

	clanID := "pioneers"
	civicDB, err := db.OpenCivicRegistryDBForClan(res, clanID)
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civicDB.Close()

	// Reachable forests 35-55m away across all compass sectors + 2 obstructed oak trees inside 16m of spawn.
	world := newPlaytest0849World(false, false, false)
	obstructedTrees := [][2]int32{
		{-42, -4}, // center (-41.5, 86.0, -3.5) ~12m WNW of (-30, 86, 0)
		{-38, 6},  // center (-37.5, 86.0, 6.5)  ~10m WSW of (-30, 86, 0)
	}
	for _, tr := range obstructedTrees {
		for dy := int32(0); dy <= 3; dy++ {
			world.blocks[worldBlockKey{X: tr[0], Y: 86 + dy, Z: tr[1]}] = "minecraft:oak_log"
		}
	}

	names := []string{"able", "bert", "carl", "dave", "earl", "fred"}
	agents := make([]*actuatorSimAgent, 0, len(names))
	for i, name := range names {
		// 4.5m x 1.5m spawn cluster around (-30.0, 86.0, 0.0)
		offsetX := float64(i%3)*2.25 - 2.25
		offsetZ := float64(i/3)*1.5 - 0.75
		sa := newActuatorSimAgent(
			t, name, clanID,
			civquestv1.Vec3{X: -30.0 + offsetX, Y: 86.0, Z: 0.0 + offsetZ},
			res, sockDir, civicDB, contemplator, world,
		)
		sa.runtime.System1State().SetRendezvousOrigin(-30.0, 86.0, 0.0)
		sa.isObstructedNav = func(_ civquestv1.Vec3, tgt *civquestv1.Vec3) bool {
			if tgt == nil {
				return false
			}
			for _, tr := range obstructedTrees {
				cx := float64(tr[0]) + 0.5
				cz := float64(tr[1]) + 0.5
				if math.Hypot(tgt.X-cx, tgt.Z-cz) <= 2.5 {
					return true
				}
			}
			return false
		}
		agents = append(agents, sa)
	}
	for _, sa := range agents {
		watchdogtest.Expect(t, sa.runtime.AgentFSM(), watchdog.Thrash, "bed", "cq-3sa.2: bed bids with nothing to do")
	}
	defer func() {
		for _, sa := range agents {
			sa.close()
		}
	}()

	const (
		startTick = int64(336500)
		startTOD  = int64(7175)
		dt        = int64(4)
		numFrames = 400 // 1,600 daytime ticks
	)

	for frameIdx := 0; frameIdx < numFrames; frameIdx++ {
		worldTick := startTick + int64(frameIdx)*dt
		tod := (startTOD + int64(frameIdx)*dt) % 24000
		for _, sa := range agents {
			if frameIdx > 0 {
				sa.stepFrame(worldTick, dt)
			}
			frame := sa.buildFrame(worldTick, tod)
			sa.deliverFrame(t, frame)
		}
	}

	for _, sa := range agents {
		sa.runtime.AwaitPOIPersistence()
	}

	quadrants := make(map[string]bool)
	firstTargets := make(map[string]int)
	dispersedOver20 := 0

	for _, sa := range agents {
		sectors, _ := sa.agentDB.GetRecentHorizonSectors(ctx, 200)
		sa.mu.Lock()
		surveys := sa.surveysCompleted
		emitted := len(sa.surveyEmissions)
		stuckWin := sa.stuckWindowCount
		maxUnstuck := sa.maxUnstuckInAnyStuckWindow
		maxDist := sa.maxDistFromSpawn
		farPos := sa.farthestPos
		posNow := sa.pos
		firstTgt := ""
		if len(sa.allNavTargets) > 0 && sa.allNavTargets[0] != nil {
			firstTgt = fmt.Sprintf("(%.1f,%.1f)", sa.allNavTargets[0].X, sa.allNavTargets[0].Z)
			firstTargets[firstTgt]++
		}
		sa.mu.Unlock()

		if maxDist > 20.0 {
			dispersedOver20++
			quadrants[compassQuadrant(farPos.X-sa.spawnPos.X, farPos.Z-sa.spawnPos.Z)] = true
		}

		t.Logf("[RED-DIAG] explorer=%s surveysCompleted=%d surveysEmitted=%d dbSectors=%d stuckWindows=%d maxUnstuckPerWin=%d firstNav=%s maxDist=%.1fm pos=(%.1f,%.1f) phase=%s",
			sa.id, surveys, emitted, len(sectors), stuckWin, maxUnstuck, firstTgt,
			maxDist, posNow.X, posNow.Z, sa.runtime.AgentFSM().ActiveActivityKind())

		if surveys < 1 {
			t.Errorf("Assertion 1 FAIL: explorer %s completed %d SurveyHorizonIntents, want >= 1 (even with obstructed trees near spawn)", sa.id, surveys)
		}
		if maxDist <= 20.0 {
			t.Errorf("Assertion 2 FAIL: explorer %s maxDistFromSpawn=%.1fm, want > 20.0m (congregated at spawn obstructed trees)", sa.id, maxDist)
		}
	}

	t.Logf("[RED-DIAG] clan summary: contemplationCalls=%d dispersedOver20=%d/6 quadrants=%v firstTargets=%v",
		calls.Load(), dispersedOver20, quadrants, firstTargets)

	if len(quadrants) < 3 {
		t.Errorf("Assertion 2 FAIL: explorers dispersed across only %d compass quadrants (%v), want >= 3", len(quadrants), quadrants)
	}
	if len(firstTargets) < 2 {
		t.Errorf("Assertion 2 FAIL: all explorers piled onto %d initial target(s) (%v), want distinct targets/bearings", len(firstTargets), firstTargets)
	}
}

// TestIntegration_SimMotorFactsMatchRecordedJavaFixtures pins the Go simulator's stuck receipts to
// the facts the REAL mod recorded (testdata/motorfacts, written by the Java
// MotorFactsFixtureRecorderTest): a walled-in Move fails "Navigation stuck" with stuck=true,
// stuck_ticks=16 (the navigator threshold), progress = remaining horizontal distance and the
// ADR-032 yaw bearing of the heading. If Java changes these, the fixtures change and this fails.
func TestIntegration_SimMotorFactsMatchRecordedJavaFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "motorfacts", "terrain_stuck.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := &civquestv1.IntentReceipt{}
	if err := protojson.Unmarshal(raw, want); err != nil {
		t.Fatal(err)
	}

	// Same scenario the Java recorder runs: agent pinned at (0,64,0) navigating to (50,64,50).
	// The formulas below are the ones actuatorSimAgent.stepFrame / simAgent.stuckReceipt use.
	dx, dz := 50.0, 50.0
	got := &civquestv1.MotorFacts{
		Stuck: true, StuckTicks: 16,
		BlockedBearingDeg: float32(math.Mod(math.Atan2(-dx, dz)*180.0/math.Pi+360.0, 360.0)),
		Progress:          math.Hypot(dx, dz),
	}
	wf := want.GetFacts()
	if want.GetFailureReason() != "Navigation stuck" || got.Stuck != wf.Stuck || got.StuckTicks != wf.StuckTicks ||
		got.Enclosed != wf.Enclosed || got.BlockedByEntity != wf.BlockedByEntity {
		t.Errorf("sim stuck facts shape drifted from Java fixture: sim=%v java=%v", got, wf)
	}
	if math.Abs(float64(got.BlockedBearingDeg-wf.BlockedBearingDeg)) > 0.5 {
		t.Errorf("bearing convention drifted: sim=%v java=%v", got.BlockedBearingDeg, wf.BlockedBearingDeg)
	}
	// Java measures from where the agent actually stood at stuck time (it creeps ~0.1m first).
	if math.Abs(got.Progress-wf.Progress) > 1.0 {
		t.Errorf("progress semantics drifted: sim=%v java=%v", got.Progress, wf.Progress)
	}

	// The enclosed fixture documents the one fact the sim must NOT assume: Java reports an
	// enclosed pit with stuck_ticks=0 (it never walked), so consumers must key on enclosed.
	raw, err = os.ReadFile(filepath.Join("testdata", "motorfacts", "enclosed_pit.json"))
	if err != nil {
		t.Fatal(err)
	}
	enc := &civquestv1.IntentReceipt{}
	if err := protojson.Unmarshal(raw, enc); err != nil {
		t.Fatal(err)
	}
	if !enc.GetFacts().GetEnclosed() || !enc.GetFacts().GetStuck() {
		t.Errorf("enclosed fixture must carry enclosed+stuck, got %v", enc.GetFacts())
	}
}
