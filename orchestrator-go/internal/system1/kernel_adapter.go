package system1

// Kernel adapter (ADR-036, bead cq-2sr.5): wires the htpda.Kernel into AgentFSM as the ONE
// activity stack. It provides
//
//   - fsmWorld: the htpda.World view of one FSM tick (frame, facts, derived stage/routine).
//     Legacy activities additionally reach the FSMContext through it; that back door is
//     the transitional bridge that beads cq-2sr.7/.8/.9 remove as each activity's progress
//     moves into its frame Local.
//   - waterfall activities (activity_managed.go): foraging, shelter, task_subroutine,
//     strategic_directive, bootstrap, exploration (each an htpda.Activity).
//     Priority preemption between them is a self-push (OpPush of the higher-priority
//     eligible activity over the running frame); the legacy fall-through-on-empty quirk is a
//     non-halting pop (ChildDone/Resumed re-runs the parent, an emptied stack is refilled
//     by a re-dispatched Tick).
//   - task_subroutine: a TaskSubroutine's progress is its frame Local (taskLocal); a task a
//     waterfall starts inline is held by that frame's walk cursor until it is pushed as the
//     task frame's Init args (AgentState keeps only a value projection, taskView).
//   - emergency / dead: real activities (emergency_activity.go, cq-2sr.6); the preempted
//     frame is suspended beneath the emergency frame and receives Resumed on exit.
//   - receipt seam: receipts are dispatched as htpda.Receipt routed by frame ID; a
//     monotonic batch guard drops late/duplicate receipts before routing.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// maxRefill bounds same-tick re-arbitration after a fall-through pop emptied the stack.
const maxRefill = 8

type noFacts struct{}

func (noFacts) GetBool(string) bool             { return false }
func (noFacts) GetInt(string) (int, bool)       { return 0, false }
func (noFacts) GetFloat(string) (float64, bool) { return 0, false }
func (noFacts) GetString(string) string         { return "" }

// fsmWorld is the htpda.World for one AgentFSM tick. It is built while fsm.mu is held; legacy
// activities read fsm.currentStage/currentRoutine fields directly and never call locking
// AgentFSM methods.
type fsmWorld struct {
	ctx      *FSMContext
	fsm      *AgentFSM
	reg      *EngineRegistry
	skillCtx *SkillContext
	tick     int64

	// receipt is the full proto of the Receipt event being dispatched (htpda.Receipt carries
	// only frame/status/facts; the task needs the failure reason).
	receipt *civquestv1.IntentReceipt

	// Per-tick legacy arbitration scratch (bridges the legacy arbitration loop semantics).
	skipped map[string]bool
	refill  bool
	// pending is the unframed task start held by the stepping frame's walk cursor, offered to
	// the task activity's Propose/args while that frame searches for preemptors.
	pending *taskStart
	// stackEmpty is the stack state at the start of the current Dispatch (when the arbiter
	// runs). Legacy bids are all BandCommitted and can only win on an empty stack (equal band
	// never preempts; priority preemption is the running frame's self-push), so Propose
	// skips the eligibility predicates otherwise.
	stackEmpty bool
	// planProbe caches this tick's read-only evaluation of the plan's next step (plan_activity.go).
	planProbe *planProbe
}

var _ htpda.World = (*fsmWorld)(nil)

func newFSMWorld(fsm *AgentFSM, ctx *FSMContext, reg *EngineRegistry, skillCtx *SkillContext) *fsmWorld {
	w := &fsmWorld{ctx: ctx, fsm: fsm, reg: reg, skillCtx: skillCtx, skipped: map[string]bool{}}
	if ctx != nil && ctx.Frame != nil {
		w.tick = ctx.Frame.WorldTick
	}
	if skillCtx == nil && ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		w.reg = resolveContextRegistry(ctx)
		ensureFSMFacts(ctx, w.reg)
		w.skillCtx = buildSkillContext(ctx, w.reg)
	}
	return w
}

func (w *fsmWorld) Tick() int64 { return w.tick }

func (w *fsmWorld) Frame() *civquestv1.TelemetryFrame {
	if w.ctx == nil {
		return nil
	}
	return w.ctx.Frame
}

func (w *fsmWorld) Facts() htpda.FactReader {
	if w.ctx == nil || w.ctx.Facts == nil {
		return noFacts{}
	}
	return w.ctx.Facts
}

func (w *fsmWorld) Stage() htpda.Stage     { return htpda.Stage(w.fsm.currentStage) }
func (w *fsmWorld) Routine() htpda.Routine { return htpda.Routine(w.fsm.currentRoutine) }

// legacyReady reports whether legacy activities may run this tick (normal stage, live frame).
func (w *fsmWorld) legacyReady() bool {
	if w == nil || w.ctx == nil || w.ctx.Frame == nil || w.ctx.Frame.Status == nil || w.skillCtx == nil {
		return false
	}
	switch w.fsm.currentStage {
	case StageBootstrap, StageSettlement, StageColonize:
		return true
	}
	return false
}

func asFSMWorld(w htpda.World) *fsmWorld {
	lw, _ := w.(*fsmWorld)
	return lw
}

func taskActive(t *TaskSubroutine) bool {
	return t != nil && t.Phase != TaskPhaseComplete && t.Phase != TaskPhaseFailed
}

// legacyResult is the pop Result of a legacy activity. Halt=true ends the tick's cascade
// (legacy "return actIntents"); Halt=false lets the exposed frame run (fall-through). Craft is
// an in-flight craft latch handed to the exposed frame (see adoptCraftLatch).
type legacyResult struct {
	Halt  bool
	Craft craftLatch
}

func halted(result any) bool {
	r, ok := result.(legacyResult)
	return ok && r.Halt
}

func unwrapFSMIntents(in []FSMIntent) []*civquestv1.AgentIntent {
	if len(in) == 0 {
		return nil
	}
	out := make([]*civquestv1.AgentIntent, 0, len(in))
	for _, fi := range in {
		if fi.Intent != nil {
			out = append(out, fi.Intent)
		}
	}
	return out
}

// newActivityKernel builds the per-agent kernel: every activity owns its Propose; the weights
// come from the PriorityTable (activity_priorities.go).
func newActivityKernel(managed []waterfallActivity) *htpda.Kernel {
	acts := []htpda.Activity{emergencyActivity{}, deadActivity{}, expeditionActivity{}, surveyActivity{}, navigateActivity{}, recoverActivity{}}
	for _, m := range managed {
		acts = append(acts, m)
	}
	reg, err := htpda.NewRegistry(acts...)
	if err != nil {
		panic("system1: invalid activity registry: " + err.Error())
	}
	return htpda.NewKernel(reg, activityPriorityTable(), htpda.Options{})
}

// ---- AgentFSM wiring (callers hold fsm.mu) ----

// admitReceiptLocked is the seam guard in front of frame-ID routing: batch IDs are issued
// monotonically and the motor executes batches in order, so a receipt older than the newest
// batch seen is late, and a repeat of the same (batch, status) is a re-delivery.
func (fsm *AgentFSM) admitReceiptLocked(r *civquestv1.IntentReceipt) bool {
	if r == nil {
		return false
	}
	b := r.GetBatchId()
	if b > 0 && b < fsm.rcptBatch {
		return false
	}
	if b == fsm.rcptBatch && r.GetStatus() == fsm.rcptStatus {
		return false
	}
	fsm.rcptBatch, fsm.rcptStatus = b, r.GetStatus()
	return true
}

func (fsm *AgentFSM) dispatchLocked(w *fsmWorld, ev htpda.Event) []FSMIntent {
	if fsm.kernel == nil {
		fsm.kernel = newActivityKernel(fsm.managed)
	}
	w.refill = false
	w.stackEmpty = fsm.kernel.Depth() == 0
	fx := fsm.kernel.Dispatch(w, ev)
	fsm.lastDiag = fx.Diagnostics
	_, isTick := ev.(htpda.Tick)
	fsm.meter.note(isTick, fx)
	fsm.emitLifecycleLocked(w.tick, fx.Lifecycle)
	if w.ctx != nil {
		w.ctx.State.applyStoreWrites(fx.Store)
	}
	fsm.applyExpeditionStoreLocked(w.ctx, fx.Store)
	fsm.planning.applyStore(fx.Store)
	fsm.applyMemoryLocked(w, fx.Memory)
	fsm.publishStackKindsLocked(w.ctx)
	fsm.syncAfterDispatchLocked(w.ctx)
	fsm.pruneNavTargetsLocked()
	var out []FSMIntent
	for _, em := range fx.Emissions {
		for _, in := range em.Intents {
			if in != nil {
				if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
					if fsm.navTargets == nil {
						fsm.navTargets = map[htpda.FrameID][3]float64{}
					}
					fsm.navTargets[em.Frame] = [3]float64{nav.Target.X, nav.Target.Y, nav.Target.Z}
				}
				out = append(out, FSMIntent{Intent: in, Frame: uint64(em.Frame), Priority: em.Kind == activityKindEmergency || isEatIntent(in)})
			}
		}
	}
	return out
}

// noteOrphanStuckLocked handles a stuck receipt whose frame is no longer on the stack: the
// pre-kernel reflex skills (biological needs, paving) emit unframed intents, so nobody owns
// their recovery. Recover then bids as a root frame (recoverActivity.Propose) so the stuck
// target is still given up on, blacklisted and decayed instead of retried forever.
func (fsm *AgentFSM) noteOrphanStuckLocked(w *fsmWorld, ev htpda.Receipt) {
	reason := ""
	if w.receipt != nil {
		reason = w.receipt.GetFailureReason()
	}
	if !isStuckReceipt(ev.Status, ev.Facts, reason) {
		return
	}
	for _, d := range fsm.lastDiag {
		if d.Reason == htpda.ReasonNotOnStack || d.Reason == htpda.ReasonEmptyStack {
			a := recoverArgs{Cause: recoverCauseOf(ev.Facts), Bearing: ev.Facts.BlockedBearingDeg, Facts: ev.Facts, Target: fsm.orphanNav, HasTarget: fsm.hasOrphanNav}
			fsm.orphan, fsm.orphanTick = &a, w.tick
			return
		}
	}
}

// pruneNavTargetsLocked drops navigation targets of frames no longer on the stack.
func (fsm *AgentFSM) pruneNavTargetsLocked() {
	if len(fsm.navTargets) == 0 || fsm.kernel == nil {
		return
	}
	live := map[htpda.FrameID]bool{}
	for _, f := range fsm.kernel.Frames() {
		live[f.ID] = true
	}
	for id := range fsm.navTargets {
		if !live[id] {
			delete(fsm.navTargets, id)
		}
	}
}

// applyMemoryLocked applies Step.Memory writes forwarded by the kernel. Today the only writer
// is Recover (ADR-037 §4): a terrain/enclosed give-up counts as one failure on its target.
func (fsm *AgentFSM) applyMemoryLocked(w *fsmWorld, writes []htpda.MemoryWrite) {
	if w == nil || w.ctx == nil || w.ctx.State == nil {
		return
	}
	for _, wr := range writes {
		if fsm.planning.applyMemory(wr) {
			continue
		}
		if b, ok := wr.(workBackoff); ok {
			if fsm.workBackoffUntil == nil {
				fsm.workBackoffUntil = map[htpda.Kind]int64{}
			}
			fsm.workBackoffUntil[b.Kind] = b.Until
			continue
		}
		if g, ok := wr.(gatherGaveUp); ok {
			if fsm.gatherSkips == nil {
				fsm.gatherSkips = map[string]int64{}
			}
			fsm.gatherSkips[g.Key] = g.Until
			continue
		}
		if b, ok := wr.(surveyBackoff); ok {
			fsm.surveyBackoffUntil = b.Until
			continue
		}
		if l, ok := wr.(recoverEscapeLock); ok {
			if l.Clear {
				fsm.escapeLock = nil
				continue
			}
			l := l
			l.Tick = w.tick
			fsm.escapeLock = &l
			continue
		}
		if g, ok := wr.(recoverGaveUp); ok {
			giveUps := recoverTunables.TerrainGiveUpsToBlacklist
			if g.Stalled {
				giveUps = 1
			}
			black, _ := w.ctx.State.RecordTargetNavigationStuck(g.Target[0], g.Target[1], g.Target[2], g.Tick, DefaultUnreachableTargetTTL, giveUps)
			fsm.recoverOutbox = append(fsm.recoverOutbox, RecoverGiveUp{Target: g.Target, Cause: g.Cause, Tick: g.Tick, Blacklisted: black})
		}
	}
}

// QueueRecoverGiveUp enqueues a give-up event for the runtime to drain (entry point for
// runtime tests and any external recovery source; Recover frames use their Memory write).
func (fsm *AgentFSM) QueueRecoverGiveUp(g RecoverGiveUp) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	fsm.recoverOutbox = append(fsm.recoverOutbox, g)
	fsm.mu.Unlock()
}

// DrainRecoverGiveUps returns and clears the Recover give-ups since the last call (the runtime
// decays the reliability of the POI each failed target belonged to).
func (fsm *AgentFSM) DrainRecoverGiveUps() []RecoverGiveUp {
	if fsm == nil {
		return nil
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	out := fsm.recoverOutbox
	fsm.recoverOutbox = nil
	return out
}

// tickActivitiesLocked delivers this frame's receipt (if any) and a Tick to the stack,
// re-arbitrating while a fall-through pop leaves the stack empty.
func (fsm *AgentFSM) tickActivitiesLocked(ctx *FSMContext, reg *EngineRegistry, skillCtx *SkillContext) []FSMIntent {
	w := newFSMWorld(fsm, ctx, reg, skillCtx)
	var out []FSMIntent
	fsm.emitVitalsLocked(ctx, w.tick)
	if ctx != nil && ctx.Frame != nil {
		if r := ctx.Frame.GetReceipt(); fsm.admitReceiptLocked(r) {
			w.receipt = r
			rev := htpda.Receipt{Frame: htpda.FrameID(r.GetFrameId()), Status: r.GetStatus(), Facts: motorFacts(r.GetFacts())}
			fsm.emitStuckReceiptLocked(w.tick, r, rev.Facts)
			// A receipt can push Recover, whose first attempt is emitted by that dispatch.
			out = append(out, fsm.dispatchLocked(w, rev)...)
			fsm.noteOrphanStuckLocked(w, rev)
			w.receipt = nil
		}
	}
	if ctx != nil && ctx.Frame != nil {
		for _, n := range ctx.Frame.GetReflexNotices() {
			out = append(out, fsm.dispatchLocked(w, htpda.Reflex{Frame: fsm.reflexFrameLocked(n), Kind: n.GetReflexType(), Popped: n.GetPopped()})...)
		}
	}
	// Telemetry-sourced spatial visit (ADR-039): applied before the Tick so this tick's target
	// selection already counts the cell the agent stands in.
	fsm.applyMemoryLocked(w, w.visitWrite())
	out = append(out, fsm.dispatchLocked(w, htpda.Tick{})...)
	for i := 0; i < maxRefill && w.refill && fsm.kernel.Depth() == 0; i++ {
		out = append(out, fsm.dispatchLocked(w, htpda.Tick{})...)
	}
	return out
}

// reflexFrameLocked resolves the frame a Java reflex notice names; 0 (unknown) means the
// frame currently on top.
func (fsm *AgentFSM) reflexFrameLocked(n *civquestv1.ReflexNotice) htpda.FrameID {
	if id := n.GetPreemptedFrameId(); id != 0 {
		return htpda.FrameID(id)
	}
	if top, ok := fsm.kernel.Top(); ok {
		return top.ID
	}
	return 0
}

func isEatIntent(in *civquestv1.AgentIntent) bool {
	return in.GetUseItem() != nil && in.GetUseItem().IsEatingOrDrinking
}

// cancelActivitiesLocked unwinds the whole stack (death) and clears the task view.
func (fsm *AgentFSM) cancelActivitiesLocked(ctx *FSMContext, reason string) {
	if fsm.kernel != nil && fsm.kernel.Depth() > 0 {
		fsm.dispatchLocked(newFSMWorld(fsm, ctx, nil, nil), htpda.Cancel{Reason: reason})
	}
	if ctx != nil && ctx.State != nil {
		ctx.State.publishTask(nil)
		ctx.State.ClearActiveActivityKind()
	}
}

// syncAfterDispatchLocked republishes the derived projections of the stack onto AgentState:
// the task view (topmost task frame's task, else a pending start held by a frame's walk cursor)
// and the top frame's kind.
func (fsm *AgentFSM) syncAfterDispatchLocked(ctx *FSMContext) {
	if ctx == nil || ctx.State == nil || fsm.kernel == nil {
		return
	}
	frames := fsm.kernel.Frames()
	t := topTaskOf(frames)
	if t == nil && taskActive(fsm.startedTask) {
		t = fsm.startedTask
	}
	ctx.State.publishTask(t)
	if len(frames) == 0 {
		ctx.State.ClearActiveActivityKind()
		return
	}
	top := frames[len(frames)-1]
	ctx.State.SetActiveActivityKind(string(top.Kind()))
}

func motorFacts(f *civquestv1.MotorFacts) htpda.MotorFacts {
	if f == nil {
		return htpda.MotorFacts{}
	}
	return htpda.MotorFacts{
		Stuck:             f.GetStuck(),
		Enclosed:          f.GetEnclosed(),
		BlockedBearingDeg: f.GetBlockedBearingDeg(),
		StuckTicks:        f.GetStuckTicks(),
		Progress:          f.GetProgress(),
		BlockedByEntity:   f.GetBlockedByEntity(),
	}
}
