package system1

// The watchdog only reports (ADR-041 section 4). User, 2026-11-27: "Frozen stays frozen.
// Doesn't mask the issue." and "Yes. Fail loudly." It takes nothing off the stack, asks for
// nothing and backs nothing off: an agent that freezes stays frozen until the cause is fixed in
// the activity that owns it. A firing is a bug. It is logged as an error, sent as a play test
// event, counted by the stack meter and handed to the test harnesses, which fail on it.

import (
	"fmt"
	"log"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
)

const (
	// An activity refused for refusalPatience ticks, with the agent getting nowhere
	// meanwhile, is reported; refusals further apart than refusalGap are separate runs.
	refusalPatience = int64(400)
	refusalGap      = int64(200)
	// standstillTicks of daylight with no movement and nothing confirmed is a standstill.
	standstillTicks = int64(900)
	standstillMove  = 1.5
	// An interruption over within thrashLife ticks was no use; thrashLimit of them inside
	// thrashWindow ticks are thrashing.
	thrashLife   = int64(40)
	thrashLimit  = 3
	thrashWindow = int64(200)
	// watchdogRepeatTicks: a condition that lasts is reported again this often, no oftener.
	watchdogRepeatTicks = int64(600)

	// EventWatchdog: an agent is stuck.
	EventWatchdog = "WATCHDOG"
)

// watchdogState is what the watchdog has seen. Nothing an agent decides reads it.
type watchdogState struct {
	// refusals are each activity's current run of withheld walks.
	refusals map[htpda.Kind]refusal
	// interruptions follows the work that interrupts other work.
	interruptions map[htpda.Kind]interruption
	// progress is where and when the agent last got anywhere (progress.go).
	progress progressWatch
	// attempts is what each activity last asked for.
	attempts map[htpda.Kind]attempt
	// reported is when each condition was last reported.
	reported map[reportKey]int64
	// resting is the declared rest last seen on top of the stack (rest.go).
	resting declaredWait
}

type declaredWait struct {
	activity htpda.Kind
	why      string
	since    int64
}

// refusal is an activity's current run of withheld walks.
type refusal struct{ since, last int64 }

type interruption struct {
	pushed, since int64
	short         int
	over          htpda.Kind
}

type attempt struct {
	what string
	tick int64
}

type reportKey struct {
	condition watchdog.Condition
	activity  htpda.Kind
}

// finding is one thing the watchdog has to say.
type finding struct {
	condition watchdog.Condition
	activity  htpda.Kind
	why       string
	// since is when the condition began.
	since int64
	// doing is the activity whose last request says what was being attempted.
	doing htpda.Kind
}

// describeIntent says what an intent asks the body to do.
func describeIntent(in *civquestv1.AgentIntent) string {
	switch a := in.GetAction().(type) {
	case *civquestv1.AgentIntent_Navigate:
		t := a.Navigate.GetTarget()
		return fmt.Sprintf("walk to (%.1f, %.1f, %.1f)", t.GetX(), t.GetY(), t.GetZ())
	case *civquestv1.AgentIntent_InteractBlock:
		p := a.InteractBlock.GetPos()
		return fmt.Sprintf("%s at (%d, %d, %d) %s", a.InteractBlock.GetInteractionType(), p.GetX(), p.GetY(), p.GetZ(),
			a.InteractBlock.GetBlockOrItemId())
	case *civquestv1.AgentIntent_CraftItem:
		return "craft " + a.CraftItem.GetRecipeId()
	case *civquestv1.AgentIntent_Attack:
		return fmt.Sprintf("attack entity %d", a.Attack.GetTargetEntityId())
	case *civquestv1.AgentIntent_UseItem:
		return fmt.Sprintf("use item in slot %d (%s)", a.UseItem.GetInventorySlot(), a.UseItem.GetMode())
	case *civquestv1.AgentIntent_SurveyHorizon:
		return "survey the horizon"
	case *civquestv1.AgentIntent_Chat:
		return "chat"
	}
	return fmt.Sprintf("%T", in.GetAction())
}

func describeWalk(nav *civquestv1.NavigateIntent) string {
	return describeIntent(&civquestv1.AgentIntent{Action: &civquestv1.AgentIntent_Navigate{Navigate: nav}})
}

// attempting notes what kind is trying to do.
func (fsm *AgentFSM) attempting(kind htpda.Kind, what string, tick int64) {
	if fsm.watch.attempts == nil {
		fsm.watch.attempts = map[htpda.Kind]attempt{}
	}
	fsm.watch.attempts[kind] = attempt{what: what, tick: tick}
}

// sent notes an intent that left the stack.
func (fsm *AgentFSM) sent(w *fsmWorld, frame htpda.FrameID, kind htpda.Kind, in *civquestv1.AgentIntent) {
	fsm.attempting(kind, describeIntent(in), w.tick)
	fsm.watch.progress.asked(uint64(frame), in)
}

// report tells of a finding, unless the same one was told of a moment ago.
func (fsm *AgentFSM) report(w *fsmWorld, f finding) {
	key := reportKey{f.condition, f.activity}
	if f.condition == watchdog.NoProgress {
		key.activity = "" // one standstill, whichever activity holds the stack just now
	}
	if last, ok := fsm.watch.reported[key]; ok && w.tick >= last && w.tick-last < watchdogRepeatTicks {
		return
	}
	if fsm.watch.reported == nil {
		fsm.watch.reported = map[reportKey]int64{}
	}
	fsm.watch.reported[key] = w.tick
	agent := ""
	if w.ctx != nil {
		agent = w.ctx.AgentID
	}
	attempted, at := "nothing: it has asked for nothing", int64(0)
	if a, ok := fsm.watch.attempts[f.doing]; ok {
		attempted, at = a.what, a.tick
	}
	log.Printf("[ERROR] [civquest-agent] %s WATCHDOG %s: activity %s: %s (since tick %d, now %d); attempting: %s",
		agent, f.condition, f.activity, f.why, f.since, w.tick, attempted)
	if fsm.eventSink != nil {
		d := map[string]any{"condition": string(f.condition), "activity": string(f.activity), "why": f.why,
			"attempting": attempted, "attempted_tick": at, "since_tick": f.since, "doing": string(f.doing)}
		if p := w.pos(); p != nil {
			d["position"] = []float64{p.X, p.Y, p.Z}
		}
		fsm.eventSink(w.tick, EventWatchdog, fmt.Sprintf("%s: %s: %s; attempting %s", f.condition, f.activity, f.why, attempted), d)
	}
	fsm.meter.watchdog(string(f.condition) + "|" + string(f.activity))
	watchdog.Notify(watchdog.Firing{Source: fsm, Agent: agent, Tick: w.tick, Condition: f.condition,
		Activity: string(f.activity), Reason: f.why, Attempting: attempted})
}

// refused counts a withheld walk against the activity that asked for it and reports whether it
// is the first of its run (the one worth logging).
func (fsm *AgentFSM) refused(w *fsmWorld, kind htpda.Kind) bool {
	if fsm.watch.refusals == nil {
		fsm.watch.refusals = map[htpda.Kind]refusal{}
	}
	r, running := fsm.watch.refusals[kind]
	first := !running || w.tick < r.last || w.tick-r.last > refusalGap
	if first {
		r.since = w.tick
	}
	r.last = w.tick
	fsm.watch.refusals[kind] = r
	if w.tick-r.since >= refusalPatience {
		fsm.report(w, finding{condition: watchdog.Refused, activity: kind, doing: kind, since: r.since,
			why: "it keeps asking to walk to places already given up"})
	}
	return first
}

// interrupted notes that kind has been pushed over the work whose top frame is over.
func (w *fsmWorld) interrupted(kind htpda.Kind, over []htpda.Frame) {
	if w == nil || w.fsm == nil {
		return
	}
	if w.fsm.watch.interruptions == nil {
		w.fsm.watch.interruptions = map[htpda.Kind]interruption{}
	}
	in := w.fsm.watch.interruptions[kind]
	in.pushed = w.tick
	if n := len(over); n > 0 {
		in.over = over[n-1].Kind()
	}
	w.fsm.watch.interruptions[kind] = in
}

// interruptionOver reports kind when it fell straight through, or keeps ending almost as soon
// as it begins (play test 10: pushed and popped every few frames, the work under it never
// moved). Needs, recovery and travel end quickly for good reasons and are left alone.
func (w *fsmWorld) interruptionOver(kind htpda.Kind, halted bool) {
	if w == nil || w.fsm == nil {
		return
	}
	switch kind {
	case activityKindEmergency, activityKindRecover, activityKindTaskSubroutine, activityKindExpedition,
		activityKindNavigate, activityKindSurvey:
		return
	}
	in, ok := w.fsm.watch.interruptions[kind]
	if !ok || w.tick < in.pushed || w.tick-in.pushed > thrashLife {
		delete(w.fsm.watch.interruptions, kind)
		return
	}
	if in.short == 0 || w.tick-in.since > thrashWindow {
		in.short, in.since = 0, w.tick
	}
	in.short++
	fellThrough := w.tick == in.pushed && !halted
	if !fellThrough && in.short < thrashLimit {
		w.fsm.watch.interruptions[kind] = in
		return
	}
	delete(w.fsm.watch.interruptions, kind)
	why := fmt.Sprintf("pushed over %s %d times, each time over almost at once", in.over, in.short)
	if fellThrough {
		why = fmt.Sprintf("pushed over %s, it fell straight through", in.over)
	}
	w.fsm.report(w, finding{condition: watchdog.Thrash, activity: kind, doing: kind, since: in.since, why: why})
}

// watchStandstill reports an agent that has got nowhere through a stretch of daylight. Night
// camp, sleep, fishing, the idle pause, emergencies and a rest the top frame declares
// (rest.go) are waits that are meant.
func (fsm *AgentFSM) watchStandstill(w *fsmWorld) {
	pos := w.pos()
	if pos == nil || w.ctx == nil || w.ctx.Frame == nil || w.ctx.Frame.Status == nil || fsm.kernel == nil {
		return
	}
	if fsm.currentStage == StageEmergency {
		// Not the objective's doing, and not progress either: a threat that comes and goes
		// must not wind the watch back each time.
		return
	}
	p := &fsm.watch.progress
	tod := w.ctx.Frame.TimeOfDay % 24000
	resting, why := fsm.declaredRest()
	fsm.noteRest(w, resting, why)
	meant := tod >= 11500 || w.ctx.Frame.Status.IsSleeping || fishingLineCast(w.ctx.Frame) ||
		w.ctx.State != nil && w.ctx.State.Strategy() == StrategyIdle || why != ""
	if meant {
		p.restart(pos, w.tick)
		return
	}
	if w.tick-p.since < standstillTicks {
		return
	}
	// The objective holding the stack is named, else whatever else does; an agent with
	// nothing at all under way is reported as soon as it takes something up.
	var subject, top htpda.Kind
	for _, f := range fsm.kernel.Frames() {
		if isStrategyKind(f.Kind()) {
			continue
		}
		if subject == "" || activityLevels[f.Kind()] == htpda.LevelObjective && activityLevels[subject] != htpda.LevelObjective {
			subject = f.Kind()
		}
		top = f.Kind()
	}
	if subject == "" {
		return
	}
	doing := top
	if _, ok := fsm.watch.attempts[doing]; !ok {
		doing = subject
	}
	fsm.report(w, finding{condition: watchdog.NoProgress, activity: subject, doing: doing, since: p.since,
		why: fmt.Sprintf("no movement and nothing confirmed for %d ticks of daylight, under %s", w.tick-p.since, top)})
}
