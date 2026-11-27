package system1

// Shared frame mechanics of the six waterfall-hosted activities (foraging, shelter, task,
// strategic_directive, bootstrap, exploration; ADR-036, bead cq-2sr.10). Each activity owns its
// eligibility (Propose), its tick and its Local in its own *_activity.go file; this file holds
// only the loop they share: receipt routing, resume bookkeeping, priority preemption by
// self-push and the legacy fall-through pop.

import (
	"google.golang.org/protobuf/proto"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	activityKindForaging           = "foraging"
	activityKindShelter            = "shelter"
	activityKindTaskSubroutine     = "task_subroutine"
	activityKindStrategicDirective = "strategic_directive"
	activityKindBootstrap          = "bootstrap"
	activityKindExploration        = "exploration"
)

// waterfallActivity is an htpda.Activity driven by the shared frame stepper.
type waterfallActivity interface {
	htpda.Activity
	// eligible reports whether the activity may bid for / keep running.
	eligible(lw *fsmWorld) bool
	// tick runs the activity's waterfall once (done=true: nothing left this tick).
	tick(lw *fsmWorld) ([]FSMIntent, bool)
	// fallThrough reports whether an empty pop lets lower activities run this tick.
	fallThrough(lw *fsmWorld) bool
	// run is one step of the activity's own frame (eligibility and preemption already passed).
	run(lw *fsmWorld, local any) htpda.Step
	// args returns Init args for a push; ok=false means the kind cannot be pushed now.
	args(lw *fsmWorld) (any, bool)
	// ended reports a Local that must pop without eligibility (e.g. its task ended).
	ended(local any) bool
	// exit pops the frame (banking the Local where the activity needs it).
	exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step
	// cancel pops on Cancel/Expired.
	cancel(local any) htpda.Step
	// resume applies the bookkeeping of a frame exposed again after a preemptor popped.
	resume(lw *fsmWorld, local any) any
}

// frameBase supplies the defaults every waterfall activity shares.
type frameBase struct {
	kind htpda.Kind
}

func (b frameBase) Kind() htpda.Kind         { return b.kind }
func (frameBase) Scope() htpda.Scope         { return htpda.ScopeMotor }
func (frameBase) args(*fsmWorld) (any, bool) { return nil, true }
func (frameBase) ended(any) bool             { return false }
func (frameBase) cancel(any) htpda.Step      { return htpda.Step{Op: htpda.OpPop} }
func (frameBase) resume(_ *fsmWorld, local any) any {
	return local
}
func (b frameBase) exit(lw *fsmWorld, _ any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	return popFrame(lw, b.kind, intents, halt)
}

// newManagedActivities returns the waterfall activities, highest priority first.
func newManagedActivities() []waterfallActivity {
	return []waterfallActivity{
		foragingActivity{frameBase{activityKindForaging}},
		shelterActivity{frameBase{activityKindShelter}},
		taskActivity{frameBase{activityKindTaskSubroutine}},
		directiveActivity{frameBase{activityKindStrategicDirective}},
		constructionActivity{frameBase{activityKindConstruction}},
		woolActivity{frameBase{activityKindWool}},
		bedActivity{frameBase{activityKindBed}},
		newPlanActivity(),
		bootstrapActivity{frameBase{activityKindBootstrap}},
		gatherActivity{frameBase{activityKindGather}},
		explorationActivity{frameBase{activityKindExploration}},
	}
}

// proposeManaged: activities do not bid for the stack; the goal frame launches them.
func proposeManaged(waterfallActivity, htpda.World) htpda.Bid { return htpda.Bid{} }

// popFrame pops the stepping frame; skipped keeps it from re-bidding this tick.
func popFrame(lw *fsmWorld, kind htpda.Kind, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	lw.skipped[string(kind)] = true
	lw.refill = !halt
	return htpda.Step{Op: htpda.OpPop, Intents: intents, Result: legacyResult{Halt: halt}}
}

// stepManaged is htpda.Activity.Step for every waterfall activity.
func stepManaged(a waterfallActivity, w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	switch e := ev.(type) {
	case htpda.Cancel, htpda.Expired:
		return a.cancel(local)
	case htpda.Receipt:
		if lw == nil || lw.receipt == nil {
			return htpda.Step{Op: htpda.OpStay, Next: local}
		}
		rc := proto.Clone(lw.receipt).(*civquestv1.IntentReceipt)
		next := local
		if tl, ok := local.(taskLocal); ok {
			tl.Receipt = rc
			next = tl
		} else if n, ok := routePendingReceipt(local, rc); ok {
			// A waterfall that started a task inline emitted that task's first intents under
			// this frame; until the task is framed (next tick) its receipts belong to it.
			next = n
		}
		next = releaseCraftOnReceipt(next, rc)
		// The frame that owns the failing intent owns its recovery (ADR-037 §3.2).
		if args, ok := lw.recoverArgsFor(e, 0); ok {
			args.Dig = scoutDigAllowed(local)
			return htpda.Step{Op: htpda.OpPush, Child: activityKindRecover, Args: args, Next: next}
		}
		return htpda.Step{Op: htpda.OpStay, Next: next}
	case htpda.Started:
		return runFrame(a, lw, lw.inheritCraftLatch(local), false)
	case htpda.ChildDone:
		local = adoptCraftLatch(local, e.Result)
		if halted(e.Result) {
			return htpda.Step{Op: htpda.OpStay, Next: local}
		}
		local = clearStuckReceipt(local, e.Result)
		rej, rejected := e.Result.(htpda.Rejected)
		if rejected && rej.Reason == htpda.ReasonNoProgress {
			return htpda.Step{Op: htpda.OpStay, Next: local} // asking again this tick cannot help
		}
		return runFrame(a, lw, a.resume(lw, local), !rejected)
	case htpda.Resumed:
		local = adoptCraftLatch(local, e.Result)
		if halted(e.Result) {
			return htpda.Step{Op: htpda.OpStay, Next: local}
		}
		return runFrame(a, lw, a.resume(lw, local), true)
	case htpda.Tick:
		return runFrame(a, lw, local, true)
	}
	return htpda.Step{Op: htpda.OpStay, Next: local}
}

// runFrame is one iteration of the arbitration loop for this frame.
func runFrame(a waterfallActivity, lw *fsmWorld, local any, searchPreemptors bool) htpda.Step {
	if !lw.legacyReady() {
		return htpda.Step{Op: htpda.OpStay, Next: local}
	}
	if lw.ctx.State != nil {
		lw.ctx.State.SetActiveActivityKind(string(a.Kind()))
	}
	if a.ended(local) || !a.eligible(lw) {
		lw.handOffPendingTask(local)
		return a.exit(lw, local, nil, false)
	}
	if searchPreemptors {
		// A task the walk started inline becomes this frame's child. Anything more pressing
		// than the frame itself is the goal's business (goal_activity.go).
		if p := pendingTaskOf(local); p != nil && lw.taskEligible(p) {
			return htpda.Step{Op: htpda.OpPush, Child: activityKindTaskSubroutine, Args: *p, Next: withoutPendingTask(local)}
		}
	}
	return lw.frameLaunchedTask(a.run(lw, local))
}

// frameLaunchedTask pushes the task a step just committed as the frame's child, so it runs in
// its own frame this same tick. A frame whose task ended this tick waits for the next one.
func (w *fsmWorld) frameLaunchedTask(step htpda.Step) htpda.Step {
	if step.Op != htpda.OpStay || w.skipped[activityKindTaskSubroutine] {
		return step
	}
	if p := pendingTaskOf(step.Next); p != nil && w.taskEligible(p) {
		step.Op, step.Child, step.Args, step.Next = htpda.OpPush, activityKindTaskSubroutine, *p, withoutPendingTask(step.Next)
	}
	return step
}

// executeRegisteredSkill runs the registry's skill name (or fallback) when its preconditions hold.
func executeRegisteredSkill(reg *EngineRegistry, name string, fallback Skill, skillCtx *SkillContext, facts *FactGraph) []*civquestv1.AgentIntent {
	skill := fallback
	if reg != nil {
		if s, ok := reg.GetSkill(name); ok && s != nil {
			skill = s
		}
	}
	if skill == nil {
		return nil
	}
	if facts != nil && !skill.Preconditions(facts) {
		return nil
	}
	si, err := skill.Execute(skillCtx, facts)
	if err != nil {
		return nil
	}
	return appendNonNilIntents(nil, si)
}

// tickRegisteredSkill runs a registered skill as an activity tick (empty = done).
func tickRegisteredSkill(lw *fsmWorld, name string, fallback Skill) ([]FSMIntent, bool) {
	si := executeRegisteredSkill(lw.reg, name, fallback, lw.skillCtx, lw.ctx.Facts)
	if len(si) == 0 {
		return nil, true
	}
	return wrapStageIntents(si, false), false
}

// wrapOrDone wraps waterfall intents as an activity tick result (empty = done).
func wrapOrDone(si []*civquestv1.AgentIntent) ([]FSMIntent, bool) {
	if len(si) == 0 {
		return nil, true
	}
	return wrapStageIntents(si, false), false
}

// clearStuckReceipt drops the stuck FAILED receipt a frame stored for its task once the
// Recover it pushed resolved it: a recovered move, or a congestion give-up ("retry later"),
// is not a task failure. A terrain/enclosed give-up keeps the receipt so the task counts it.
func clearStuckReceipt(local any, result any) any {
	clear := false
	switch r := result.(type) {
	case recovered:
		clear = true
	case recoverBlocked:
		clear = r.Cause == CauseCongestion
	}
	if !clear {
		return local
	}
	stuck := func(rc *civquestv1.IntentReceipt) bool {
		return rc != nil && isStuckReceipt(rc.GetStatus(), motorFacts(rc.GetFacts()), rc.GetFailureReason())
	}
	if tl, ok := local.(taskLocal); ok {
		if stuck(tl.Receipt) {
			tl.Receipt = nil
		}
		return tl
	}
	if w, rebuild, ok := walkCarrier(local); ok && taskActive(w.task) && stuck(w.taskReceipt) {
		w.taskReceipt = nil
		return rebuild(w)
	}
	return local
}

// scoutDigAllowed reports whether a stuck receipt on this frame may be recovered by digging
// through an adjacent obstruction: the walk-driven (scouting) activities, as long as no
// committed task owns the walk (a task handles its own approach failures; the retired
// EvaluateUnstuck never masked an active task's stuck either).
func scoutDigAllowed(local any) bool {
	w, _, ok := walkCarrier(local)
	return ok && !taskActive(w.task)
}
