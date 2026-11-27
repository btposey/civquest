package system1

// task_subroutine as a frame-local activity (ADR-036, bead cq-2sr.10). A TaskSubroutine's
// progress is its frame Local (taskLocal); for the legacy task tick it is bound to the walk
// cursor only while the frame steps. The activity owns its eligibility and its waterfall.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// taskLocal is the task_subroutine frame Local: the task's whole progress.
type taskLocal struct {
	Task         *TaskSubroutine
	Receipt      *civquestv1.IntentReceipt // routed to this frame, not yet consumed by a step
	LastStepTick int64
	Walk         walkLocal // bootstrap-gather cursor for the legacy task tick
}

// taskActivity owns its eligibility (Propose) and waterfall; its weight is priorityTask.
type taskActivity struct{ frameBase }

func (a taskActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a taskActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}

func (a taskActivity) Init(w htpda.World, args any) (any, htpda.Lease) {
	st, _ := args.(taskStart)
	if lw := asFSMWorld(w); lw != nil && lw.fsm != nil && st.Task == lw.fsm.startedTask {
		lw.fsm.startedTask, lw.fsm.startedReceipt = nil, nil
	}
	return taskLocal{Task: st.Task, Receipt: st.Receipt, LastStepTick: w.Tick()}, htpda.Lease{}
}

// args: the unframed committed task a stepping waterfall frame started inline.
func (taskActivity) args(lw *fsmWorld) (any, bool) {
	if lw.pending != nil {
		return *lw.pending, true
	}
	if t := lw.fsm.startedTask; taskActive(t) {
		return taskStart{Task: t, Receipt: lw.fsm.startedReceipt}, true
	}
	return nil, false
}

func (taskActivity) fallThrough(lw *fsmWorld) bool {
	return lw.ctx.State == nil || !lw.ctx.State.HasActiveTask()
}

// ended: the task finished while suspended (e.g. abandoned on emergency preemption).
func (taskActivity) ended(local any) bool {
	tl, ok := local.(taskLocal)
	return ok && !taskActive(tl.Task)
}

func (a taskActivity) exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	return popFrame(lw, a.kind, intents, halt)
}

// resume re-issues the current step after a preemptor popped: suspension time is
// compensated and receipts for pre-preemption intents (stale, dropped by frame-ID routing)
// are not waited for.
func (taskActivity) resume(lw *fsmWorld, local any) any {
	tl, ok := local.(taskLocal)
	if !ok || tl.Task == nil || lw == nil {
		return local
	}
	t := tl.Task.Clone()
	t.CompensateSuspension(lw.tick - tl.LastStepTick)
	t.LastActionTick = 0
	t.LastNavigateTick = 0
	if t.LastReceiptStatus == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING {
		t.LastReceiptStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED
	}
	tl.Task = t
	tl.LastStepTick = lw.tick
	return tl
}

// eligible: a committed task, no conversation/trade; bootstrap needs an open window, colonize
// yields to night shelter and to an active directive by day.
func (taskActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	stage, routine := lw.fsm.currentStage, lw.fsm.currentRoutine
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if !ctx.State.HasActiveTask() || len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil {
		return false
	}
	if stage == StageBootstrap {
		return isBootstrapWindowOpen(routine, ctx)
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		if !ctx.IsDuskOrNight() && routine != RoutineSleep && isStrategicDirectiveEligible(stage, routine, ctx) {
			return false
		}
	}
	return true
}

func (taskActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	return tickTaskSubroutineActivity(lw.fsm.currentStage, lw.ctx, lw.skillCtx, lw.reg)
}

// taskStallTicks without progress, or repeated failures, make a task stalled.
const taskStallTicks = int64(200)

func taskStalled(t *TaskSubroutine, tick int64) bool {
	return t.ConsecutiveFailures >= 2 || tick-t.LastProgressTick >= taskStallTicks
}

func tickTaskSubroutineActivity(stage Stage, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry) ([]FSMIntent, bool) {
	walk := skillCtx.Walk
	kind := ""
	if walk != nil && walk.task != nil {
		kind = walk.task.Spec.Kind
	}

	standalone := walk != nil && walk.task != nil && walk.task.Spec.Standalone
	if (stage == StageBootstrap || stage == StageColonize) && !standalone {
		// Advice is read when choosing work, but it may rescue work that has stopped getting
		// anywhere: a stalled harvest is dropped for a directive that asks for something else.
		if kind == "harvest_tree" && stage == StageBootstrap && walk.task != nil && ctx.Facts != nil &&
			ctx.Facts.GetBool("system2.directive_active") &&
			(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
			if dir, ok := ctx.State.ActiveStrategicDirective(ctx.Frame.WorldTick); ok && dir.Action != DirectiveActionGatherTimber &&
				(dir.Rescue || taskStalled(walk.task, ctx.Frame.WorldTick)) &&
				(dir.Action == DirectiveActionScoutFrontier || dir.Action == DirectiveActionMineStone ||
					dir.Action == DirectiveActionSocialize || dir.Action == DirectiveActionBuildShelter ||
					dir.Action == DirectiveActionForageFood && NeedsForaging(ctx.Frame)) {
				walk.clearTask(ctx.State)
				return nil, true
			}
		}

		if kind == "harvest_tree" || kind == "collect_items" || stage == StageColonize {
			var si []*civquestv1.AgentIntent
			if skill, ok := reg.GetSkill("bootstrap_gather"); ok && skill != nil && skill.Preconditions(ctx.Facts) {
				if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
					si = appendNonNilIntents(nil, out)
				}
			}
			// A task that ended inside the skill pops cleanly this tick; re-evaluating here would
			// start the next harvest in the same tick instead of after the frame pops.
			if len(si) == 0 && walk.hasTask() {
				if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); intent != nil {
					si = []*civquestv1.AgentIntent{intent}
				}
			}
			done := !walk.hasTask()
			if len(si) > 0 {
				return wrapStageIntents(si, false), done
			}
			return nil, done
		}
	}

	var intent *civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("task_subroutine"); ok && skill != nil && (ctx.Facts == nil || skill.Preconditions(ctx.Facts)) {
		if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
			out = appendNonNilIntents(nil, out)
			if len(out) > 0 {
				intent = out[0]
			}
		}
	} else {
		stepRes := walk.stepTask(ctx.AgentID, ctx.State, ctx.Frame)
		intent = stepRes.Intent
	}
	done := !walk.hasTask()
	if intent != nil {
		return wrapStageIntents([]*civquestv1.AgentIntent{intent}, false), done
	}
	return nil, done
}

// run binds the frame's task Local to the walk cursor for the legacy task tick and takes the
// cursor's task back as the next Local.
func (a taskActivity) run(lw *fsmWorld, local any) htpda.Step {
	tl, _ := local.(taskLocal)
	st := lw.ctx.State
	walk := tl.Walk
	walk.wantExpedition = false
	walk.task, walk.taskReceipt = tl.Task.Clone(), tl.Receipt
	st.publishTask(walk.task)
	lw.skillCtx.Walk = &walk
	fi, _ := a.tick(lw)
	lw.skillCtx.Walk = nil
	next, unconsumed := walk.task, walk.taskReceipt
	if !taskActive(next) {
		next = nil
	}
	walk.task, walk.taskReceipt = nil, nil
	st.publishTask(next)
	intents := unwrapFSMIntents(fi)
	origin := activityKindExploration
	if lw.fsm.currentStage == StageBootstrap {
		origin = activityKindBootstrap
	}
	want := walk.wantExpedition
	walk.wantExpedition = false
	if args, ok := lw.expeditionStart(origin); ok && want && next == nil {
		// A task waterfall asked for an expedition (a finished harvest with no timber in
		// sight): it takes the finished task's place. A task still active carries on; an
		// expedition is not its child.
		return htpda.Step{Op: htpda.OpReplace, Child: activityKindExpedition, Args: args, Intents: intents}
	}
	if next == nil {
		step := popFrame(lw, a.kind, intents, len(intents) > 0 || !a.fallThrough(lw))
		// A craft issued by the task's last tick outlives this frame: hand its latch down.
		return withCraftLatch(step, walk.craftLatch)
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: taskLocal{Task: next, Receipt: unconsumed, LastStepTick: lw.tick, Walk: walk}}
}
