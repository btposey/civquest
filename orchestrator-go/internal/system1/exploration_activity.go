package system1

// Exploration (patrol / survey / armory rounds) as a frame-local activity (ADR-036, bead
// cq-2sr.9/.10). The frame's Local carries its walk cursor. The activity owns its eligibility
// and waterfall; its weight is priorityExploration.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type explorationLocal struct {
	Walk   walkLocal
	Breaks breakRecord // refused_breaks.go
}

type explorationActivity struct{ frameBase }

func (a explorationActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a explorationActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return explorationLocal{}, htpda.Lease{}
}
func (a explorationActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (explorationActivity) fallThrough(*fsmWorld) bool { return false }

func (a explorationActivity) eligible(lw *fsmWorld) bool {
	return isExplorationActivityEligible(lw.fsm.currentStage, lw.fsm.currentRoutine, lw.ctx)
}

func (a explorationActivity) run(lw *fsmWorld, local any) htpda.Step {
	el, _ := local.(explorationLocal)
	return runWalkFrame(a, lw, el.Walk, func(w walkLocal) any { return explorationLocal{Walk: w} })
}

func (explorationActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	return wrapOrDone(tickExplorationActivity(lw.fsm.currentStage, lw.fsm.currentRoutine, lw.ctx, lw.skillCtx, lw.reg))
}

// isExplorationActivityEligible: awake, no conversation; Colonize unless shelter-gated or
// surveying; Settlement for the patrol-bearing routines.
func isExplorationActivityEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		return ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick)
	}
	if stage != StageSettlement {
		return false
	}
	switch routine {
	case RoutinePrimaryDuty, RoutineCivicPatrol, RoutineArmoryMaintenance, RoutineSecondaryHobby, RoutineSleep:
		return true
	case RoutineHarvestTree:
		// A tree felled on the way (cq-6l3.15) marks the routine until the next tick; the walk
		// it interrupted must keep its route.
		return true
	default:
		return false
	}
}

func tickExplorationActivity(stage Stage, routine Routine, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry) []*civquestv1.AgentIntent {
	if stage == StageColonize {
		executeRegisteredSkill(reg, "survey_explorer", NewSurveyExplorerSkill(), skillCtx, ctx.Facts)
		if skillCtx.Walk != nil && skillCtx.Walk.wantExpedition {
			return nil // the expedition frame takes over
		}
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
		return nil
	}

	switch routine {
	case RoutinePrimaryDuty:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if si := executeRegisteredSkill(reg, "patrol_exploration", NewPatrolExplorationSkill(), skillCtx, ctx.Facts); len(si) > 0 {
			return si
		}
	case RoutineCivicPatrol:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	case RoutineArmoryMaintenance:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB, skillCtx.Walk); armoryIntent != nil {
			return []*civquestv1.AgentIntent{armoryIntent}
		}
		if ctx.State != nil {
			ctx.State.SetArmoryMaintenanceNeeded(false)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	case RoutineSecondaryHobby:
		if ctx.Config != nil && ctx.Config.EffectiveShift() == xdg.ShiftThird && ctx.State != nil && ctx.State.IsArmoryMaintenanceNeeded() {
			if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
				return appendNonNilIntents(nil, skillIntents)
			}
			if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB, skillCtx.Walk); armoryIntent != nil {
				return []*civquestv1.AgentIntent{armoryIntent}
			}
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	}
	return nil
}
