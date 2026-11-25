package agent

import (
	"fmt"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const (
	stageNameReceiptIngest      = "stageReceiptIngest"
	stageNameSpatialAndSurvey   = "stageSpatialAndSurvey"
	stageNameFactGraphSystem1   = "stageFactGraphAndSystem1"
	stageNameIdleAndGoalLogging = "stageIdleAndGoalLogging"
	stageNameIntentDispatch     = "stageIntentDispatch"
	stageNameSystem2Dispatchers = "stageSystem2Dispatchers"
)

// FrameStage defines one named, ordered phase of the per-frame telemetry pipeline.
type FrameStage struct {
	Name string
	Run  func(r *AgentRuntime, ctx *FrameContext)
}

// FrameContext carries the incoming telemetry frame and intermediate evaluation
// state across the ordered stages of FramePipeline.
type FrameContext struct {
	Frame        *civquestv1.TelemetryFrame
	Receipt      *civquestv1.IntentReceipt
	FSMContext   *system1.FSMContext
	FSMResult    system1.FSMResult
	HasFSMResult bool
}

// FramePipeline executes an ordered sequence of named telemetry processing stages.
type FramePipeline struct {
	stages  []FrameStage
	onStage func(name string, ctx *FrameContext)
}

// NewFramePipeline constructs a FramePipeline with the supplied ordered stages.
func NewFramePipeline(stages ...FrameStage) *FramePipeline {
	copied := make([]FrameStage, len(stages))
	copy(copied, stages)
	return &FramePipeline{stages: copied}
}

// NewDefaultFramePipeline constructs the canonical 6-stage ADR-034 telemetry pipeline.
func NewDefaultFramePipeline() *FramePipeline {
	return NewFramePipeline(
		FrameStage{Name: stageNameReceiptIngest, Run: stageReceiptIngest},
		FrameStage{Name: stageNameSpatialAndSurvey, Run: stageSpatialAndSurvey},
		FrameStage{Name: stageNameFactGraphSystem1, Run: stageFactGraphAndSystem1},
		FrameStage{Name: stageNameIdleAndGoalLogging, Run: stageIdleAndGoalLogging},
		FrameStage{Name: stageNameIntentDispatch, Run: stageIntentDispatch},
		FrameStage{Name: stageNameSystem2Dispatchers, Run: stageSystem2Dispatchers},
	)
}

// Stages returns the ordered stage names registered in the pipeline.
func (p *FramePipeline) Stages() []string {
	if p == nil {
		return nil
	}
	names := make([]string, len(p.stages))
	for i, s := range p.stages {
		names[i] = s.Name
	}
	return names
}

// Run executes each pipeline stage in order, short-circuiting safely if the runtime,
// context, or frame is nil, or if the agent is already dead.
func (p *FramePipeline) Run(r *AgentRuntime, ctx *FrameContext) {
	if p == nil || r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() {
		return
	}
	for _, stage := range p.stages {
		if r.IsDead() {
			return
		}
		if p.onStage != nil {
			p.onStage(stage.Name, ctx)
		}
		if stage.Run != nil {
			stage.Run(r, ctx)
		}
	}
}

// stageReceiptIngest validates and ingests execution receipts into System 1,
// prunes completed harvest coordinates, and synchronizes the inventory cache.
func stageReceiptIngest(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() {
		return
	}

	r.updateInventoryCache(ctx.Frame)

	receipt := ctx.Frame.GetReceipt()
	if receipt == nil {
		return
	}
	ctx.Receipt = receipt
	r.system1State.IngestReceipt(receipt)

	if receipt.Status == civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED {
		if hpos := r.lastHarvestPos.Swap(nil); hpos != nil {
			r.PruneHarvestedBlockPos(hpos.X, hpos.Y, hpos.Z)
		}
	}
}

// stageSpatialAndSurvey handles sensory POI ingestion,
// close-range POI verification, habit trails, and 360-degree horizon survey processing.
func stageSpatialAndSurvey(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() {
		return
	}

	frame := ctx.Frame
	r.ingestSpatialPOIs(frame)
	r.updateHabitTrails(frame)
	r.processHorizonSurveyAndRendezvous(frame)
}

// stageFactGraphAndSystem1 syncs companion tracking, evaluates System 1 reflexes
// and the HTPDA/FSM tick, and processes pending clan rendezvous synchronization.
func stageFactGraphAndSystem1(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() {
		return
	}

	frame := ctx.Frame
	r.evaluateCompanion(frame)

	if result, fsmCtx, ok := r.evaluateSystem1Reflexes(frame); ok {
		ctx.FSMResult = result
		ctx.FSMContext = fsmCtx
		ctx.HasFSMResult = true
	}

	r.syncPendingRendezvous(frame)
	r.applyRecoverGiveUps()
}

// stageIdleAndGoalLogging updates idle dwell tracking and logs goal hierarchy transitions.
func stageIdleAndGoalLogging(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() || !ctx.HasFSMResult {
		return
	}

	r.updateIdleTracker(ctx.Frame, ctx.FSMResult)
	r.logGoalTransition(ctx.Frame, ctx.FSMResult)
}

// stageIntentDispatch marks periphery flushes, enqueues System 1 intents into slotQueue
// with flee deduplication and eating cooldown tracking, and notifies pending flushes.
func stageIntentDispatch(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() || !ctx.HasFSMResult {
		return
	}

	r.dispatchFSMIntents(ctx.Frame, ctx.FSMResult)
}

// stageSystem2Dispatchers triggers non-blocking asynchronous System 2 pipelines
// for dialogue/trade, chronicler synthesis, toponymy, agrarian routines, and contemplation.
func stageSystem2Dispatchers(r *AgentRuntime, ctx *FrameContext) {
	if r == nil || ctx == nil || ctx.Frame == nil || r.IsDead() {
		return
	}

	frame := ctx.Frame
	if len(frame.IncomingChats) > 0 || frame.PendingTrade != nil {
		r.dispatchDialogue(frame)
	}
	r.dispatchChronicler(frame)
	r.dispatchToponymy(frame)
	r.dispatchAgrarian(frame)
	r.dispatchContemplation(frame)
}

func (r *AgentRuntime) ingestSpatialPOIs(frame *civquestv1.TelemetryFrame) {
	si := r.system1State.SpatialIndex()
	if si == nil {
		return
	}

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

func (r *AgentRuntime) logGoalTransition(frame *civquestv1.TelemetryFrame, result system1.FSMResult) {
	if result.GoalHierarchy == nil || result.GoalHierarchy.ImmediateGoal == "" {
		return
	}

	oldGoal, changed := r.system1State.CompareAndSwapLoggedGoal(result.GoalHierarchy.ImmediateGoal)
	if !changed {
		return
	}

	r.mu.RLock()
	debugLog := r.debugLogger
	r.mu.RUnlock()

	if debugLog != nil {
		debugLog.Emit(frame.WorldTick, "GOAL_TRANSITION", fmt.Sprintf("Goal: %s", result.GoalHierarchy.ImmediateGoal), map[string]any{
			"previous_immediate_goal": oldGoal,
			"immediate_goal":          result.GoalHierarchy.ImmediateGoal,
			"short_term_goal":         result.GoalHierarchy.ShortTermGoal,
			"long_term_goal":          result.GoalHierarchy.LongTermGoal,
			"time_of_day":             frame.TimeOfDay,
		})
	}
}

func (r *AgentRuntime) dispatchFSMIntents(frame *civquestv1.TelemetryFrame, result system1.FSMResult) {
	// Mark periphery flush before queuing intents so flush + new routine intents dispatch in the same IntentBatch.
	if result.FlushPeriphery && r.slotQueue != nil {
		r.slotQueue.MarkFlushPeriphery()
	}

	for _, fsmIntent := range result.Intents {
		if fsmIntent.Intent == nil {
			continue
		}
		r.frameSeam.noteEmit(fsmIntent.Frame)
		if fsmIntent.Priority {
			if r.shouldDeduplicateFleeNav(frame.WorldTick, fsmIntent.Intent) {
				continue
			}

			err := r.QueuePriorityIntent(fsmIntent.Intent)
			if err == nil && fsmIntent.Intent.GetUseItem() != nil && fsmIntent.Intent.GetUseItem().IsEatingOrDrinking {
				r.system1State.SetLastEatingTick(frame.WorldTick)
			}
		} else {
			_ = r.QueueIntent(fsmIntent.Intent)
		}
	}

	if result.FlushPeriphery && r.slotQueue != nil {
		r.slotQueue.NotifyIfFlushPending()
	}

	// Reset flee target if not in emergency.
	if result.State != system1.StateEmergency {
		r.system1State.ClearFleeTarget()
	}
}

func (r *AgentRuntime) shouldDeduplicateFleeNav(worldTick int64, intent *civquestv1.AgentIntent) bool {
	nav := intent.GetNavigate()
	if nav == nil || nav.Target == nil {
		return false
	}
	return r.system1State.ShouldDeduplicateFleeNav(worldTick, nav.Target)
}
