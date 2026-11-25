package system1

// System 2 strategic directives as a bid source plus a frame-local activity (ADR-036, bead
// cq-2sr.9).
//
// A directive no longer owns progress on AgentState and is not a parallel executor invoked
// inline by other waterfalls. It biases which activity bids:
//
//	FORAGE_FOOD       -> Foraging is eligible (directiveForcesForaging)
//	SCOUT_FRONTIER    -> Expedition bids (directiveExpeditionBid), origin "strategic_directive"
//	BUILD_SHELTER     -> Shelter is eligible when materials are in hand (directiveBuildsShelter),
//	                     so it can place blocks; without materials the directive gathers timber
//	CRAFT_SURVIVAL_KIT-> Bootstrap is eligible (directiveCraftsKit)
//	GATHER_TIMBER, MINE_STONE, SOCIALIZE -> hosted by the strategic_directive frame, whose typed
//	                     Local (directiveLocal) holds the action throttle, nav key and scout waypoint.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// directiveLocal is the strategic_directive frame Local.
type directiveLocal struct {
	LastActionTick int64
	LastNavKey     string
	ScoutTarget    *civquestv1.Vec3 // immutable once set; replaced, never mutated
	ScoutStep      int
	Walk           walkLocal // unstuck heading cursor
}

// runnableDirective is the shared gate for acting on the active directive: alive, awake, no
// conversation, no companion, no survival reflex pending and not in a night window (unless
// bootstrap shelter-gating applies).
func runnableDirective(frame *civquestv1.TelemetryFrame, state *AgentState) (StrategicDirectiveState, bool) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return StrategicDirectiveState{}, false
	}
	if frame.DeathEvent != nil || frame.Status.Health <= 0 || frame.Status.IsSleeping {
		return StrategicDirectiveState{}, false
	}
	if len(frame.IncomingChats) > 0 || frame.PendingTrade != nil {
		return StrategicDirectiveState{}, false
	}
	if state.IsCompanionActive() {
		return StrategicDirectiveState{}, false
	}
	// Survival reflexes ALWAYS take priority over strategic directives.
	if hasSurvivalPreemption(frame) {
		return StrategicDirectiveState{}, false
	}
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11500 && tod < 23500
	shelterGated := state.Stage() == StageBootstrap && !state.IsCompanionActive() && !isStateShelterOrBedSatisfied(state)
	if !shelterGated && (isDuskOrNight || frame.Weather == civquestv1.Weather_WEATHER_THUNDER || state.Routine() == RoutineSleep) {
		return StrategicDirectiveState{}, false
	}
	return state.ActiveStrategicDirective(frame.WorldTick)
}

// directiveAction returns the runnable, stage-eligible directive's action ("" when none).
func directiveAction(stage Stage, routine Routine, ctx *FSMContext) (StrategicDirectiveState, bool) {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil ||
		!isStrategicDirectiveEligible(stage, routine, ctx) {
		return StrategicDirectiveState{}, false
	}
	return runnableDirective(ctx.Frame, ctx.State)
}

// directiveHosted reports whether the strategic_directive frame itself executes the active
// directive (the rest are bids for other activities).
func directiveHosted(stage Stage, routine Routine, ctx *FSMContext) bool {
	dir, ok := directiveAction(stage, routine, ctx)
	if !ok {
		return false
	}
	switch dir.Action {
	case DirectiveActionGatherTimber, DirectiveActionMineStone, DirectiveActionSocialize:
		return true
	case DirectiveActionBuildShelter:
		return inspectBootstrapInventory(ctx.Frame.Status.Inventory).buildingBlockCount == 0
	}
	return false
}

// directiveBuildsShelter reports an active BUILD_SHELTER directive with materials in hand: it
// makes the Shelter activity eligible so the Shelter frame (not the directive) places blocks.
func directiveBuildsShelter(stage Stage, routine Routine, ctx *FSMContext) bool {
	dir, ok := directiveAction(stage, routine, ctx)
	return ok && dir.Action == DirectiveActionBuildShelter &&
		inspectBootstrapInventory(ctx.Frame.Status.Inventory).buildingBlockCount > 0
}

// directiveCraftsKit reports an active CRAFT_SURVIVAL_KIT directive: Bootstrap is eligible.
func directiveCraftsKit(stage Stage, routine Routine, ctx *FSMContext) bool {
	dir, ok := directiveAction(stage, routine, ctx)
	return ok && dir.Action == DirectiveActionCraftSurvivalKit
}

// directiveExpeditionUtility is the Expedition bid of an active SCOUT_FRONTIER directive: the
// directive's own priority, so it outranks bootstrap/exploration-origin starts.
func directiveExpeditionUtility(stage Stage, routine Routine, ctx *FSMContext) (float64, bool) {
	dir, ok := directiveAction(stage, routine, ctx)
	if !ok || dir.Action != DirectiveActionScoutFrontier {
		return 0, false
	}
	return priorityDirective, true
}

// directiveWaiting reports that a hosted directive action is in flight (pathing, or inside the
// action/nav cooldown): the frame holds instead of dead-ending, so the directive's effect does
// not lapse to a lower-priority activity between its own emissions.
func directiveWaiting(l directiveLocal, f *civquestv1.TelemetryFrame) bool {
	if f.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		return true
	}
	return l.LastActionTick > 0 && f.WorldTick >= l.LastActionTick && f.WorldTick-l.LastActionTick < directiveNavCooldownTicks
}

// directiveActivity owns its eligibility (Propose) and waterfall; its weight is priorityDirective.
type directiveActivity struct{ frameBase }

func (a directiveActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a directiveActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return directiveLocal{}, htpda.Lease{}
}
func (a directiveActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (directiveActivity) fallThrough(*fsmWorld) bool { return true }

// eligible: the active directive is one this frame hosts (the rest bias other activities).
func (directiveActivity) eligible(lw *fsmWorld) bool {
	return directiveHosted(lw.fsm.currentStage, lw.fsm.currentRoutine, lw.ctx)
}

func (directiveActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	return tickRegisteredSkill(lw, "strategic_directive", NewStrategicDirectiveSkill())
}

// isStrategicDirectiveEligible reports whether a System 2 directive may act in this stage and
// routine: active, awake, no conversation, and the stage's window is open.
func isStrategicDirectiveEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Facts == nil {
		return false
	}
	if !ctx.Facts.GetBool("system2.directive_active") {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	switch stage {
	case StageBootstrap:
		if !isBootstrapWindowOpen(routine, ctx) {
			return false
		}
		return HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)
	case StageColonize:
		if isColonizeNightShelterGated(routine, ctx) || ctx.IsDuskOrNight() || routine == RoutineSleep {
			return false
		}
		return HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)
	default:
		return routine != RoutineSleep && routine != RoutineArmoryMaintenance && !ctx.IsDuskOrNight()
	}
}

// run is one step of the strategic_directive frame.
func (a directiveActivity) run(lw *fsmWorld, local any) htpda.Step {
	prog, _ := local.(directiveLocal)
	lw.skillCtx.Directive = &prog
	fi, _ := a.tick(lw)
	lw.skillCtx.Directive = nil
	intents := unwrapFSMIntents(fi)
	if len(intents) == 0 && (!directiveWaiting(prog, lw.ctx.Frame) || !lw.holds()) {
		// Nothing to do and nothing in flight: normal activities run this tick.
		return popFrame(lw, a.kind, nil, false)
	}
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: prog}
}
