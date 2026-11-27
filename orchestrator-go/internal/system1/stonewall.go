package system1

// Nothing may stop an agent for good (play test 9). An activity that for a long while only asks
// for walks that are withheld, or that holds the stack through the day without the agent moving
// or doing anything, is made to give way for a while so that another can be chosen. Choosing a
// different destination is still the activity's own business and comes first.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	// An activity refused for refusalPatience ticks, with nothing else sent meanwhile, gives
	// way; refusals further apart than refusalGap are separate runs.
	refusalPatience = int64(400)
	refusalGap      = int64(200)
	// stonewallTicks is how long an activity that gave way stays off the stack.
	stonewallTicks = int64(600)
	// standstillTicks of daylight with no movement and nothing done is a standstill.
	standstillTicks = int64(900)
	standstillMove  = 1.5

	// EventStandstill: an activity was made to give way.
	EventStandstill = "STANDSTILL"
)

// stuckGuard is what the agent remembers so that nothing stops it for good.
type stuckGuard struct {
	refusals   map[htpda.Kind]refusal
	stonewalls map[htpda.Kind]int64
	mark       progressMark
	// pockets are where recent enclosed recoveries began (climb_out.go).
	pockets []gaveUpHere
	// interruptions follows the work that interrupts other work.
	interruptions map[htpda.Kind]interruption
}

const (
	// An interruption over within thrashLife ticks was no use; thrashLimit of them inside
	// thrashWindow ticks keep that activity from interrupting for a while.
	thrashLife   = int64(40)
	thrashLimit  = 3
	thrashWindow = int64(200)
)

type interruption struct {
	pushed, since int64
	short         int
}

// interrupted notes that kind has been pushed over other work.
func (w *fsmWorld) interrupted(kind htpda.Kind) {
	if w == nil || w.fsm == nil {
		return
	}
	if w.fsm.interruptions == nil {
		w.fsm.interruptions = map[htpda.Kind]interruption{}
	}
	in := w.fsm.interruptions[kind]
	in.pushed = w.tick
	w.fsm.interruptions[kind] = in
}

// interruptionOver backs kind off when it fell straight through, or keeps ending almost as soon
// as it begins (play test 10: pushed and popped every few frames, the work under it never moved).
// Needs, recovery and travel end quickly for good reasons and are left alone.
func (w *fsmWorld) interruptionOver(kind htpda.Kind, halted bool) {
	if w == nil || w.fsm == nil {
		return
	}
	switch kind {
	case activityKindEmergency, activityKindRecover, activityKindTaskSubroutine, activityKindExpedition,
		activityKindNavigate, activityKindSurvey:
		return
	}
	in, ok := w.fsm.interruptions[kind]
	if !ok || w.tick < in.pushed || w.tick-in.pushed > thrashLife {
		delete(w.fsm.interruptions, kind)
		return
	}
	if in.short == 0 || w.tick-in.since > thrashWindow {
		in.short, in.since = 0, w.tick
	}
	in.short++
	fellThrough := w.tick == in.pushed && !halted
	if !fellThrough && in.short < thrashLimit {
		w.fsm.interruptions[kind] = in
		return
	}
	delete(w.fsm.interruptions, kind)
	if w.fsm.workBackoffUntil == nil {
		w.fsm.workBackoffUntil = map[htpda.Kind]int64{}
	}
	w.fsm.workBackoffUntil[kind] = w.tick + workBackoffTicks
	w.skipped[string(kind)] = true
}

// refusal is an activity's current run of withheld walks.
type refusal struct{ since, last int64 }

// progressMark is where and when the agent last got anywhere.
type progressMark struct {
	at    [3]float64
	since int64
}

func (w *fsmWorld) stonewalled(kind htpda.Kind) bool {
	return w.fsm != nil && w.Tick() < w.fsm.stonewalls[kind]
}

// stonewall takes kind off the stack until stonewallTicks have passed and asks for a fresh
// look round.
func (fsm *AgentFSM) stonewall(w *fsmWorld, kind htpda.Kind, why string) {
	if fsm.stonewalls == nil {
		fsm.stonewalls = map[htpda.Kind]int64{}
	}
	fsm.stonewalls[kind] = w.tick + stonewallTicks
	delete(fsm.refusals, kind)
	if w.ctx != nil {
		w.ctx.State.wantFreshBearing(why)
	}
	if fsm.eventSink != nil {
		fsm.eventSink(w.tick, EventStandstill, string(kind)+" gives way: "+why,
			map[string]any{"activity": string(kind), "why": why, "until": w.tick + stonewallTicks})
	}
}

// refused counts a withheld walk against the activity that asked for it and reports whether it
// is the first of its run (the one worth logging). A task is left to its own progress timeout:
// taking its frame away would not end it.
func (fsm *AgentFSM) refused(w *fsmWorld, kind htpda.Kind) bool {
	if fsm.refusals == nil {
		fsm.refusals = map[htpda.Kind]refusal{}
	}
	r, running := fsm.refusals[kind]
	first := !running || w.tick < r.last || w.tick-r.last > refusalGap
	if first {
		r.since = w.tick
	}
	r.last = w.tick
	fsm.refusals[kind] = r
	if w.tick-r.since >= refusalPatience && kind != activityKindTaskSubroutine {
		fsm.stonewall(w, kind, "it only asks to walk to places already given up")
	}
	return first
}

// served ends a run of refusals: the activity found something it could do.
func (fsm *AgentFSM) served(kind htpda.Kind) { delete(fsm.refusals, kind) }

// watchStandstill ends an objective that has got nowhere through a stretch of daylight.
// Night camp, sleep, fishing and emergencies are waits that are meant.
func (fsm *AgentFSM) watchStandstill(w *fsmWorld, out []FSMIntent) {
	pos := w.pos()
	if pos == nil || w.ctx == nil || w.ctx.Frame == nil || w.ctx.Frame.Status == nil {
		return
	}
	if fsm.currentStage == StageEmergency {
		// Not the objective's doing, and not progress either: a threat that comes and goes
		// must not wind the watch back each time.
		return
	}
	here := [3]float64{pos.X, pos.Y, pos.Z}
	moved := math.Hypot(here[0]-fsm.mark.at[0], here[2]-fsm.mark.at[2]) >= standstillMove ||
		math.Abs(here[1]-fsm.mark.at[1]) >= standstillMove
	tod := w.ctx.Frame.TimeOfDay % 24000
	meant := tod >= 11500 || w.ctx.Frame.Status.IsSleeping || fishingLineCast(w.ctx.Frame)
	if moved || meant || acted(out) || fsm.mark.since == 0 || w.tick < fsm.mark.since {
		fsm.mark = progressMark{at: here, since: w.tick}
		return
	}
	if w.tick-fsm.mark.since < standstillTicks || fsm.kernel == nil {
		return
	}
	for _, f := range fsm.kernel.Frames() {
		if activityLevels[f.Kind()] == htpda.LevelObjective {
			fsm.stonewall(w, f.Kind(), "no movement and nothing done for too long")
			break
		}
	}
	fsm.mark = progressMark{at: here, since: w.tick}
}

// acted reports an intent that does something where the agent stands.
func acted(out []FSMIntent) bool {
	for _, i := range out {
		switch i.Intent.GetAction().(type) {
		case *civquestv1.AgentIntent_InteractBlock, *civquestv1.AgentIntent_CraftItem,
			*civquestv1.AgentIntent_Attack, *civquestv1.AgentIntent_UseItem:
			return true
		}
	}
	return false
}
