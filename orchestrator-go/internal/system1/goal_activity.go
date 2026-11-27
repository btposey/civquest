package system1

// The goal frame (ADR-040): the base of every agent's activity stack and the only place that
// chooses between activities. It picks work when it has none, and while work is under way it
// supervises: when something more pressing turns up it parks the whole subtree, intact, does
// the pressing thing, and resumes. No activity launches a sibling or itself.
//
// Stage 3 keeps the earlier priority order (activity_priorities.go) as the goal's policy.

import "github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"

const activityKindGoal = "goal"

// candidate is work the goal could launch.
type candidate struct {
	Kind  htpda.Kind
	Args  any
	score float64
}

// goalLocal is the goal frame Local.
type goalLocal struct {
	// Parked holds the work set aside, oldest first; immutable, extend and shrink by copy.
	Parked [][]htpda.Frame
}

// parkedWork is implemented by locals that hold frames which are no longer on the kernel stack.
func (l goalLocal) parkedWork() [][]htpda.Frame { return l.Parked }

type goalActivity struct{}

var (
	_ htpda.Activity   = goalActivity{}
	_ htpda.Supervisor = goalActivity{}
)

func (goalActivity) Kind() htpda.Kind                         { return activityKindGoal }
func (goalActivity) Scope() htpda.Scope                       { return htpda.ScopeMotor }
func (goalActivity) Level() htpda.Level                       { return htpda.LevelGoal }
func (goalActivity) Init(htpda.World, any) (any, htpda.Lease) { return goalLocal{}, htpda.Lease{} }

// Propose puts the goal under an empty stack; every other band outranks it, so it never
// interrupts anything.
func (goalActivity) Propose(w htpda.World) htpda.Bid {
	if frameIsDead(w.Frame()) {
		return htpda.Bid{}
	}
	return htpda.Bid{Band: htpda.BandIdle, Utility: 1, Reason: "goal"}
}

// Supervise parks the work under way when something more pressing has turned up.
func (goalActivity) Supervise(w htpda.World, _ any, _ htpda.Kind) htpda.Verdict {
	lw := asFSMWorld(w)
	if lw == nil || !lw.legacyReady() {
		return htpda.Verdict{}
	}
	frames := lw.fsm.kernel.FramesInStep()
	next, ok := lw.morePressing(frames[1:])
	if !ok {
		return htpda.Verdict{}
	}
	return htpda.Verdict{Park: true, Reason: "more pressing: " + string(next.Kind), Token: next}
}

func (g goalActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	l := htpda.LocalAs[goalLocal](local)
	switch e := ev.(type) {
	case htpda.Cancel, htpda.Expired:
		return g.abandon(w, l)
	case htpda.ChildDone:
		if p, ok := e.Result.(htpda.Parked); ok {
			next, _ := p.Token.(candidate)
			parked := append(append([][]htpda.Frame(nil), l.Parked...), setAside(p.Frames, w.Tick()))
			return htpda.Step{Op: htpda.OpPush, Child: next.Kind, Args: next.Args, Next: goalLocal{Parked: parked}}
		}
		return g.after(lw, l, e.Child, e.Result)
	case htpda.Resumed:
		return g.after(lw, l, e.From, e.Result)
	case htpda.Started, htpda.Tick:
		if len(l.Parked) > 0 {
			return resumeParked(l, "", nil)
		}
		return g.choose(lw, l)
	}
	return htpda.Step{Op: htpda.OpStay, Next: l}
}

// after decides what follows a piece of work: the work it interrupted resumes (unless
// something still more pressing than that has appeared), otherwise the goal chooses afresh.
func (g goalActivity) after(lw *fsmWorld, l goalLocal, done htpda.Kind, result any) htpda.Step {
	if rejected, ok := result.(htpda.Rejected); ok && lw != nil {
		lw.skipped[string(rejected.Kind)] = true
	}
	if n := len(l.Parked); n > 0 {
		if _, rejected := result.(htpda.Rejected); !rejected && !halted(result) && lw != nil && lw.legacyReady() {
			if next, ok := lw.morePressing(l.Parked[n-1]); ok {
				return htpda.Step{Op: htpda.OpPush, Child: next.Kind, Args: next.Args, Next: l}
			}
		}
		return resumeParked(l, done, result)
	}
	// Only a pop that asked for it (nothing to do, fall through) is followed up at once;
	// otherwise the next tick chooses.
	if halted(result) || lw == nil || !lw.refill {
		return htpda.Step{Op: htpda.OpStay, Next: l}
	}
	lw.refill = false
	return g.choose(lw, l)
}

func resumeParked(l goalLocal, done htpda.Kind, result any) htpda.Step {
	n := len(l.Parked)
	return htpda.Step{Op: htpda.OpResume, Resume: l.Parked[n-1], Child: done, Result: result,
		Next: goalLocal{Parked: l.Parked[: n-1 : n-1]}}
}

func (goalActivity) choose(lw *fsmWorld, l goalLocal) htpda.Step {
	if lw != nil {
		if next, ok := lw.bestWork(); ok {
			return htpda.Step{Op: htpda.OpPush, Child: next.Kind, Args: next.Args, Next: l}
		}
	}
	return htpda.Step{Op: htpda.OpStay, Next: l}
}

// abandon ends the goal (death): parked work banks what it knows, as if cancelled in turn.
func (goalActivity) abandon(w htpda.World, l goalLocal) htpda.Step {
	st := htpda.Step{Op: htpda.OpPop}
	for i := len(l.Parked) - 1; i >= 0; i-- {
		for j := len(l.Parked[i]) - 1; j >= 0; j-- {
			f := l.Parked[i][j]
			out := f.Act.Step(w, f.Local, htpda.Cancel{Reason: "goal abandoned"})
			st.Store = append(st.Store, out.Store...)
			st.Memory = append(st.Memory, out.Memory...)
		}
	}
	return st
}

// setAside prepares frames for parking: a leg or sweep interrupted now and resumed within the
// same tick carries on instead of starting over.
func setAside(frames []htpda.Frame, tick int64) []htpda.Frame {
	top := &frames[len(frames)-1]
	switch loc := top.Local.(type) {
	case navigateLocal:
		loc.LastStep = tick
		top.Local = loc
	case surveyLocal:
		loc.LastStep = tick
		top.Local = loc
	}
	return frames
}

// bestWork is the most pressing thing to do when nothing is under way or parked.
func (w *fsmWorld) bestWork() (candidate, bool) {
	var best candidate
	offer := func(c candidate) {
		if c.score > 0 && (best.Kind == "" || c.score > best.score || c.score == best.score && c.Kind < best.Kind) {
			best = c
		}
	}
	if o := w.fsm.orphan; o != nil && w.Tick()-w.fsm.orphanTick <= recoverTunables.AttemptTicks && !w.skipped[activityKindRecover] {
		offer(candidate{Kind: activityKindRecover, Args: *o, score: priorityRecover})
	}
	if w.legacyReady() {
		if u, ok := w.directiveExpeditionBid(); ok {
			offer(candidate{Kind: activityKindExpedition, Args: expeditionArgs{Origin: activityKindStrategicDirective}, score: priorityExpedition * u})
		}
		for _, a := range w.fsm.managed {
			if w.skipped[string(a.Kind())] {
				continue
			}
			if args, ok := a.args(w); ok && a.eligible(w) {
				offer(candidate{Kind: a.Kind(), Args: args, score: activityPriority(string(a.Kind()))})
			}
		}
	}
	return best, best.Kind != ""
}

// morePressing returns what should interrupt the work in frames (a subtree, bottom first), if
// anything. How pressing work is depends on what it is doing now, its top frame.
func (w *fsmWorld) morePressing(frames []htpda.Frame) (candidate, bool) {
	if len(frames) == 0 {
		return candidate{}, false
	}
	top := frames[len(frames)-1]
	switch loc := top.Local.(type) {
	case navigateLocal:
		if !w.expeditionEligible(loc.Origin) {
			return candidate{}, false
		}
		return w.duringTravel(loc.Origin, loc.Returning, true)
	case surveyLocal:
		k := w.knowledge()
		if !w.expeditionEligible(loc.Origin) || k.SurveyTick > loc.BaseTick && k.SurveyTick >= loc.StartTick {
			return candidate{}, false // the sweep is about to end anyway
		}
		return w.duringTravel(loc.Origin, loc.Returning, false)
	}
	for _, a := range w.fsm.managed {
		if a.Kind() != top.Kind() {
			continue
		}
		if a.ended(top.Local) || !a.eligible(w) {
			return candidate{}, false // it is about to end itself
		}
		floor := activityPriority(string(a.Kind()))
		// A task the frame is about to launch itself already counts for its priority.
		if p := pendingTaskOf(top.Local); p != nil && w.taskEligible(p) {
			floor = max(floor, priorityTask)
		}
		if kind, args := w.preemptorAbove(floor, ""); kind != "" {
			return candidate{Kind: htpda.Kind(kind), Args: args}, true
		}
		return candidate{}, false
	}
	return candidate{}, false
}

// duringTravel is what may interrupt an expedition leg or sweep started by origin: anything
// above the expedition's own priority; for a bootstrap trip, timber within reach (the plan
// takes it when its next step is actionable, cq-dcx.4); and, on a leg, a find worth taking.
func (w *fsmWorld) duringTravel(origin string, returning, leg bool) (candidate, bool) {
	if kind, args := w.preemptorAbove(expeditionPriority, origin); kind != "" {
		return candidate{Kind: htpda.Kind(kind), Args: args}, true
	}
	if origin == activityKindBootstrap && !returning && !w.skipped[activityKindBootstrap] && !w.backedOff(activityKindBootstrap) &&
		bootstrapHarvestAvailable(w.ctx) {
		if pa := newPlanActivity(); !w.skipped[activityKindPlan] && pa.eligible(w) {
			if args, ok := pa.args(w); ok {
				return candidate{Kind: activityKindPlan, Args: args}, true
			}
		}
		return candidate{Kind: activityKindBootstrap}, true
	}
	if leg && !w.skipped[activityKindGather] && (gatherActivity{}).eligible(w) {
		return candidate{Kind: activityKindGather}, true
	}
	return candidate{}, false
}

// taskEligible reports whether the pending task could be framed now.
func (w *fsmWorld) taskEligible(p *taskStart) bool {
	was := w.pending
	w.pending = p
	defer func() { w.pending = was }()
	return (taskActivity{frameBase{activityKindTaskSubroutine}}).eligible(w)
}

// parkedHolder is a Local that keeps parked frames.
type parkedHolder interface{ parkedWork() [][]htpda.Frame }

// logicalFrames lists the agent's activities oldest first, leaving out the goal itself: parked
// work comes where its holder is, followed by what is running.
func logicalFrames(stack []htpda.Frame) []htpda.Frame {
	var out []htpda.Frame
	for _, f := range stack {
		if f.Kind() != activityKindGoal {
			out = append(out, f)
		}
		if h, ok := f.Local.(parkedHolder); ok {
			for _, sub := range h.parkedWork() {
				out = append(out, logicalFrames(sub)...)
			}
		}
	}
	return out
}

// activities is logicalFrames of the kernel stack (outside a Step).
func (fsm *AgentFSM) activities() []htpda.Frame {
	if fsm.kernel == nil {
		return nil
	}
	return logicalFrames(fsm.kernel.Frames())
}

// activitiesInStep is activities for use inside a Step.
func (w *fsmWorld) activitiesInStep() []htpda.Frame {
	if w == nil || w.fsm == nil || w.fsm.kernel == nil {
		return nil
	}
	return logicalFrames(w.fsm.kernel.FramesInStep())
}
