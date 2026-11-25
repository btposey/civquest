package system1

// Foraging as a frame-local activity (ADR-036, bead cq-2sr.9).
//
// The foraging cursor (action throttle, last target key, wander heading/target, recently
// visited container POIs) is the Foraging frame's typed value Local. It survives ticks and
// suspend/resume and is discarded when the frame pops; nothing about it lives on AgentState.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// foragingActivity owns its eligibility (Propose) and waterfall; its weight is priorityForaging.
type foragingActivity struct{ frameBase }

func (a foragingActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a foragingActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return *newForageLocal(), htpda.Lease{}
}
func (a foragingActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (foragingActivity) fallThrough(*fsmWorld) bool { return false }

func (foragingActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	return tickRegisteredSkill(lw, "foraging", NewForagingSkill())
}

// eligible: awake, no conversation/trade, not in the dusk/night window while fed, armory work
// first, then a FORAGE_FOOD directive or the foraging skill's own preconditions.
func (foragingActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	stage, routine := lw.fsm.currentStage, lw.fsm.currentRoutine
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Facts == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if stage == StageSettlement && routine == RoutineArmoryMaintenance && (ctx.State == nil || ctx.State.IsArmoryMaintenanceNeeded()) {
		// Armory work first; once it is done foraging bids like any other routine.
		return false
	}
	if ctx.IsDuskOrNight() && ctx.Frame.Status.FoodLevel > foragingFedFoodLevel {
		return false
	}
	if directiveForcesForaging(stage, routine, ctx) {
		return true
	}
	var fSkill Skill = NewForagingSkill()
	if lw.reg != nil {
		if s, ok := lw.reg.GetSkill("foraging"); ok && s != nil {
			fSkill = s
		}
	}
	return fSkill.Preconditions(ctx.Facts)
}

// foragingFedFoodLevel: above this food level the agent does not forage at dusk/night.
const foragingFedFoodLevel = 6

// forageLocal is the Foraging frame Local.
type forageLocal struct {
	LastActionTick    int64
	LastTargetKey     string
	LastExploreTick   int64
	ExploreHeadingRad float64
	ExploreTarget     *civquestv1.Vec3 // immutable once set; replaced, never mutated
	Visited           map[string]int64 // container POI -> last visit tick; copy-on-step (ADR: Mem)
	// Forced is derived each step: a System 2 FORAGE_FOOD directive keeps foraging eligible
	// while well fed. It is not progress.
	Forced bool
}

func newForageLocal() *forageLocal { return &forageLocal{Visited: map[string]int64{}} }

// clone copies the Local (the map is the only shared reference).
func (l forageLocal) clone() forageLocal {
	v := make(map[string]int64, len(l.Visited))
	for k, t := range l.Visited {
		v[k] = t
	}
	l.Visited = v
	return l
}

// directiveForcesForaging reports whether an active FORAGE_FOOD directive makes foraging
// eligible even when not hungry (the directive is a bid source, not an executor).
func directiveForcesForaging(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	dir, ok := ctx.State.ActiveStrategicDirective(ctx.Frame.WorldTick)
	return ok && dir.Action == DirectiveActionForageFood && isStrategicDirectiveEligible(stage, routine, ctx) &&
		!hasSurvivalPreemption(ctx.Frame)
}

// runForage is one step of the Foraging frame: the waterfall runs with the frame's Local bound
// to the skill context and the updated Local becomes the next Local. A frame whose evaluator
// has nothing to emit this tick stays on the stack (waiting for the wander cooldown or a
// target) instead of dead-ending; only a forced (directive) foraging with nothing to find
// yields to the lower activities.
func (a foragingActivity) run(lw *fsmWorld, local any) htpda.Step {
	fl, _ := local.(forageLocal)
	prog := fl.clone()
	prog.Forced = !NeedsForaging(lw.ctx.Frame) && directiveForcesForaging(lw.fsm.currentStage, lw.fsm.currentRoutine, lw.ctx)
	lw.skillCtx.Forage = &prog
	var intents []*civquestv1.AgentIntent
	if prog.Forced {
		if i := EvaluateForagingDirective(lw.ctx.AgentID, lw.ctx.Frame, lw.ctx.State, &prog); i != nil {
			intents = []*civquestv1.AgentIntent{i}
		}
	} else {
		fi, _ := a.tick(lw)
		intents = unwrapFSMIntents(fi)
	}
	lw.skillCtx.Forage = nil
	if len(intents) == 0 && prog.Forced {
		// Well fed and directed to forage but nothing reachable: yield to normal activities.
		return popFrame(lw, a.kind, nil, false)
	}
	if len(intents) == 0 && !lw.holds() {
		return popFrame(lw, a.kind, nil, true)
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: prog}
}
