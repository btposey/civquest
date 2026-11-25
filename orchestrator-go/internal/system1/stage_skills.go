package system1

import (
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func ensureFSMFacts(ctx *FSMContext, reg *EngineRegistry) {
	if ctx == nil || ctx.Facts != nil || reg == nil {
		return
	}
	evalCtx := &EvaluatorContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}
	ctx.Facts = reg.EvaluateAll(evalCtx)
}

func buildSkillContext(ctx *FSMContext, reg *EngineRegistry) *SkillContext {
	if ctx == nil {
		return &SkillContext{Registry: reg}
	}
	return &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
		Registry:      reg,
	}
}

func wrapStageIntents(raw []*civquestv1.AgentIntent, emergency bool) []FSMIntent {
	if len(raw) == 0 {
		return nil
	}
	out := make([]FSMIntent, 0, len(raw))
	for _, ni := range raw {
		if ni == nil {
			continue
		}
		priority := emergency || (ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking)
		out = append(out, FSMIntent{Intent: ni, Priority: priority})
	}
	return out
}

func appendNonNilIntents(dst []*civquestv1.AgentIntent, src []*civquestv1.AgentIntent) []*civquestv1.AgentIntent {
	for _, intent := range src {
		if intent != nil {
			dst = append(dst, intent)
		}
	}
	return dst
}

func skillIsDuskOrNight(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("circadian.is_dusk_or_night"); exists {
			return facts.GetBool("circadian.is_dusk_or_night")
		}
	}
	if ctx == nil || ctx.Frame == nil {
		return false
	}
	if ctx.Frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		return true
	}
	tod := ((ctx.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 11000 && tod < 23500
}

func skillHasAssignedBed(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.has_bed"); exists {
			return facts.GetBool("shelter.has_bed")
		}
	}
	if ctx == nil || ctx.State == nil {
		return false
	}
	return ctx.State.GetAssignedBed() != nil
}

func skillIsShelterComplete(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.complete"); exists {
			return facts.GetBool("shelter.complete")
		}
	}
	if ctx == nil || ctx.State == nil {
		return false
	}
	return ctx.State.GetShelterBuildIndex() >= blueprints.CabinTotalBlocks ||
		ctx.State.GetShelterBlocksPlaced() >= blueprints.CabinTotalBlocks
}

func skillIsShelterOnCooldown(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.on_cooldown"); exists {
			return facts.GetBool("shelter.on_cooldown")
		}
	}
	if ctx == nil || ctx.State == nil || ctx.Frame == nil {
		return false
	}
	return ctx.State.IsShelterOnCooldown(ctx.Frame.WorldTick)
}

func executeStageBiologicalNeeds(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry) []*civquestv1.AgentIntent {
	if skillCtx == nil || skillCtx.Frame == nil || skillCtx.Frame.Status == nil {
		return nil
	}
	hadAssignedBed := skillCtx.State != nil && skillCtx.State.GetAssignedBed() != nil

	var needsIntents []*civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("biological_needs"); ok && skill != nil {
		if skill.Preconditions(facts) {
			if si, err := skill.Execute(skillCtx, facts); err == nil {
				needsIntents = si
			}
		}
	} else {
		bioSkill := NewBiologicalNeedsSkill()
		if bioSkill.Preconditions(facts) {
			if si, err := bioSkill.Execute(skillCtx, facts); err == nil {
				needsIntents = si
			}
		} else {
			needsIntents = EvaluateBiologicalNeeds(skillCtx.AgentID, skillCtx.Frame, skillCtx.State)
		}
	}

	// If EvaluateSleep cleared an unreachable AssignedBed during this tick, refresh shelter.has_bed immediately.
	if hadAssignedBed && skillCtx.State != nil && skillCtx.State.GetAssignedBed() == nil && facts != nil && facts.GetBool("shelter.has_bed") {
		facts.SetBool("shelter.has_bed", "shelter", false, "Assigned bed cleared as unreachable")
	}
	return needsIntents
}

func executeStageRoadPaving(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry, tryInfrastructureSkill bool) []*civquestv1.AgentIntent {
	if skillCtx == nil || skillCtx.Frame == nil || skillCtx.Frame.Status == nil {
		return nil
	}
	if skillCtx.State != nil && skillCtx.State.IsExpeditionSurveying(skillCtx.Frame.WorldTick) {
		return nil
	}
	if tryInfrastructureSkill {
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, facts, "infrastructure"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
	}
	if !skillCtx.Frame.Status.IsSleeping && skillCtx.PavingTracker != nil &&
		skillCtx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		!skillCtx.Frame.Status.NavigationEnclosed {
		if skillCtx.State != nil && skillCtx.PavingTracker.GetHomeOrigin() == nil {
			if home := skillCtx.State.GetHomeOrigin(); home != nil {
				skillCtx.PavingTracker.SetHomeOrigin(home)
			}
		}
		if paveIntent := skillCtx.PavingTracker.EvaluatePaving(skillCtx.AgentID, skillCtx.Frame); paveIntent != nil {
			return []*civquestv1.AgentIntent{paveIntent}
		}
	}
	return nil
}

func tryForagingPreemptionRaw(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry) ([]*civquestv1.AgentIntent, bool) {
	if skillCtx == nil || skillCtx.Frame == nil || skillCtx.Frame.Status == nil {
		return nil, false
	}
	if len(skillCtx.Frame.IncomingChats) > 0 || skillCtx.Frame.PendingTrade != nil || skillCtx.Frame.Status.IsSleeping {
		return nil, false
	}
	if skillIsDuskOrNight(skillCtx, facts) && skillCtx.Frame.Status.FoodLevel > 6 {
		return nil, false
	}
	var fSkill Skill = NewForagingSkill()
	if s, ok := reg.GetSkill("foraging"); ok && s != nil {
		fSkill = s
	}
	if !fSkill.Preconditions(facts) {
		return nil, false
	}
	if skillCtx.State != nil {
		skillCtx.State.ClearExpeditionSurveyingIfActive()
		if skillCtx.State.HasActiveTask() {
			skillCtx.State.PauseActiveTask(skillCtx.Frame.WorldTick)
		}
	}
	si, err := fSkill.Execute(skillCtx, facts)
	if err != nil {
		return nil, true
	}
	return appendNonNilIntents(nil, si), true
}

func runBootstrapActivityRaw(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry, intents []*civquestv1.AgentIntent) []*civquestv1.AgentIntent {
	if skillCtx.State != nil && skillCtx.State.HasActiveTask() {
		skillCtx.State.ResumeActiveTask(skillCtx.Frame.WorldTick)
		_, kind, _, _ := skillCtx.State.ActiveTaskInfo()
		if kind != "harvest_tree" && kind != "collect_items" {
			stepRes := skillCtx.State.StepActiveTask(skillCtx.AgentID, skillCtx.Frame)
			if stepRes.Intent != nil {
				intents = append(intents, stepRes.Intent)
			}
			if stepRes.Active {
				return intents
			}
		} else {
			// Allow fresh or stuck-breaking non-GATHER_TIMBER System 2 directives to preempt harvest_tree.
			if kind == "harvest_tree" && facts != nil && facts.GetBool("system2.directive_active") &&
				(HasSurvivalKit(skillCtx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(skillCtx.Frame, skillCtx.State)) {
				dir, dirActive := skillCtx.State.ActiveStrategicDirective(skillCtx.Frame.WorldTick)
				task := skillCtx.State.GetActiveTask()
				if dirActive && task != nil && dir.Action != DirectiveActionGatherTimber &&
					(dir.Action == DirectiveActionScoutFrontier ||
						dir.Action == DirectiveActionMineStone ||
						dir.Action == DirectiveActionSocialize ||
						dir.Action == DirectiveActionBuildShelter ||
						(dir.Action == DirectiveActionForageFood && NeedsForaging(skillCtx.Frame))) {
					isStuckOrStalled := skillCtx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK ||
						task.ConsecutiveFailures > 0 ||
						(skillCtx.Frame.WorldTick >= task.LastProgressTick && skillCtx.Frame.WorldTick-task.LastProgressTick >= 40)
					if dir.IssuedTick >= task.StartedTick || isStuckOrStalled {
						if dirIntent := EvaluateStrategicDirectiveWithID(skillCtx.AgentID, skillCtx.Frame, skillCtx.State, skillCtx.Config); dirIntent != nil {
							skillCtx.State.ClearActiveTask()
							return append(intents, dirIntent)
						} else if dir.Action == DirectiveActionScoutFrontier && skillCtx.State.IsExpeditionActive() {
							skillCtx.State.ClearActiveTask()
							return intents
						}
					}
				}
			}
			if bootstrapIntent := EvaluateBootstrapGoals(skillCtx.AgentID, skillCtx.Frame, skillCtx.Config, skillCtx.State); bootstrapIntent != nil {
				intents = append(intents, bootstrapIntent)
			}
			return intents
		}
	}
	if facts != nil && facts.GetBool("system2.directive_active") &&
		(HasSurvivalKit(skillCtx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(skillCtx.Frame, skillCtx.State)) {
		if dirIntent := EvaluateStrategicDirectiveWithID(skillCtx.AgentID, skillCtx.Frame, skillCtx.State, skillCtx.Config); dirIntent != nil {
			return append(intents, dirIntent)
		}
	}
	if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, facts, "bootstrap"); ok && len(skillIntents) > 0 {
		return appendNonNilIntents(intents, skillIntents)
	} else if skillCtx.State == nil || !skillCtx.State.HasActiveTask() {
		if bootstrapIntent := EvaluateBootstrapGoals(skillCtx.AgentID, skillCtx.Frame, skillCtx.Config, skillCtx.State); bootstrapIntent != nil {
			intents = append(intents, bootstrapIntent)
		}
	}
	return intents
}

// StageBootstrapSkill executes survival kit bootstrap, dusk shelter gating, and foraging preemption.
type StageBootstrapSkill struct{}

// NewStageBootstrapSkill creates a StageBootstrapSkill instance.
func NewStageBootstrapSkill() *StageBootstrapSkill { return &StageBootstrapSkill{} }

func (s *StageBootstrapSkill) Name() string                    { return "stage_bootstrap" }
func (s *StageBootstrapSkill) Category() string                { return "stage_bootstrap" }
func (s *StageBootstrapSkill) Preconditions(f *FactGraph) bool { return f != nil }
func (s *StageBootstrapSkill) Interruptible() bool             { return true }
func (s *StageBootstrapSkill) OnComplete(_ *SkillContext, _ *FactGraph) {
}

func (s *StageBootstrapSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	reg := ctx.Registry
	intents := executeStageBiologicalNeeds(ctx, facts, reg)
	intents = append(intents, executeStageRoadPaving(ctx, facts, reg, false)...)

	if forageIntents, preempted := tryForagingPreemptionRaw(ctx, facts, reg); preempted {
		return append(intents, forageIntents...), nil
	}

	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		shelterSatisfied := skillHasAssignedBed(ctx, facts) || skillIsShelterComplete(ctx, facts)
		isSleepRoutine := ctx.State != nil && ctx.State.Routine() == RoutineSleep
		isTavernRoutine := ctx.State != nil && ctx.State.Routine() == RoutineTavernSocial

		if !shelterSatisfied && skillIsDuskOrNight(ctx, facts) && !skillIsShelterOnCooldown(ctx, facts) {
			deferShelterForActiveHarvest := false
			if ctx.State != nil && ctx.State.HasActiveTask() {
				_, kind, _, _ := ctx.State.ActiveTaskInfo()
				if kind == "harvest_tree" || kind == "collect_items" {
					inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
					if inv.buildingBlockCount == 0 && !inv.hasLight {
						deferShelterForActiveHarvest = true
					}
				}
			}
			if !deferShelterForActiveHarvest {
				if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "shelter"); ok && len(skillIntents) > 0 {
					return appendNonNilIntents(intents, skillIntents), nil
				} else if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
					return append(intents, shelterIntent), nil
				}
			}
		}

		if !shelterSatisfied || (!skillIsDuskOrNight(ctx, facts) && !isSleepRoutine && !isTavernRoutine) {
			return runBootstrapActivityRaw(ctx, facts, reg, intents), nil
		}
	}
	return intents, nil
}

// StageSettlementSkill executes established settlement living, circadian duties, armory work, and exploration.
type StageSettlementSkill struct{}

// NewStageSettlementSkill creates a StageSettlementSkill instance.
func NewStageSettlementSkill() *StageSettlementSkill { return &StageSettlementSkill{} }

func (s *StageSettlementSkill) Name() string                    { return "stage_settlement" }
func (s *StageSettlementSkill) Category() string                { return "stage_settlement" }
func (s *StageSettlementSkill) Preconditions(f *FactGraph) bool { return f != nil }
func (s *StageSettlementSkill) Interruptible() bool             { return true }
func (s *StageSettlementSkill) OnComplete(_ *SkillContext, _ *FactGraph) {
}

func (s *StageSettlementSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	reg := ctx.Registry
	intents := executeStageBiologicalNeeds(ctx, facts, reg)
	intents = append(intents, executeStageRoadPaving(ctx, facts, reg, true)...)

	routine := RoutinePrimaryDuty
	if ctx.State != nil {
		routine = ctx.State.Routine()
	}

	if routine != RoutineArmoryMaintenance {
		if forageIntents, preempted := tryForagingPreemptionRaw(ctx, facts, reg); preempted {
			return append(intents, forageIntents...), nil
		}
	}

	if (skillIsDuskOrNight(ctx, facts) || routine == RoutineSleep) && !skillHasAssignedBed(ctx, facts) && !skillIsShelterComplete(ctx, facts) && !skillIsShelterOnCooldown(ctx, facts) {
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
				return append(intents, shelterIntent), nil
			}
		}
	}

	if ctx.State != nil && ctx.State.HasActiveTask() && len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		ctx.State.ResumeActiveTask(ctx.Frame.WorldTick)
		stepRes := ctx.State.StepActiveTask(ctx.AgentID, ctx.Frame)
		if stepRes.Intent != nil {
			intents = append(intents, stepRes.Intent)
		}
		if stepRes.Active {
			return intents, nil
		}
	}

	if routine != RoutineSleep && routine != RoutineArmoryMaintenance &&
		len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil && !skillIsDuskOrNight(ctx, facts) {
		if facts != nil && facts.GetBool("system2.directive_active") {
			if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
				return append(intents, dirIntent), nil
			}
		}
	}

	if routine != RoutineSleep && routine != RoutineArmoryMaintenance &&
		ctx.State != nil && ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
		return intents, nil
	}

	return executeSettlementRoutine(ctx, facts, reg, routine, intents), nil
}

func executeSettlementRoutine(
	ctx *SkillContext,
	facts *FactGraph,
	reg *EngineRegistry,
	routine Routine,
	intents []*civquestv1.AgentIntent,
) []*civquestv1.AgentIntent {
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil {
		return intents
	}
	switch routine {
	case RoutinePrimaryDuty:
		if ctx.State != nil && ctx.State.GetExpeditionPhase() != ExpeditionIdle {
			if surveyIntent := EvaluateHorizonExplorer(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); surveyIntent != nil {
				return append(intents, surveyIntent)
			}
		}
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(intents, skillIntents)
		}
		prof := "UNASSIGNED"
		if ctx.Config != nil {
			prof = strings.ToUpper(strings.TrimSpace(ctx.Config.Profession))
		}
		var exploreIntent *civquestv1.AgentIntent
		if prof == "EXPLORER" || prof == "TRAILBLAZER" || prof == "SCOUT" {
			exploreIntent = EvaluateScouting(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
		} else {
			exploreIntent = EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
		}
		if exploreIntent != nil {
			intents = append(intents, exploreIntent)
		}
	case RoutineCivicPatrol:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(intents, skillIntents)
		} else if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			intents = append(intents, patrolIntent)
		}
	case RoutineArmoryMaintenance:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "Armory"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(intents, skillIntents)
		} else if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
			intents = append(intents, armoryIntent)
		} else {
			if ctx.State != nil {
				ctx.State.SetArmoryMaintenanceNeeded(false)
			}
			if forageIntents, preempted := tryForagingPreemptionRaw(ctx, facts, reg); preempted {
				return append(intents, forageIntents...)
			}
			if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
				intents = append(intents, patrolIntent)
			}
		}
	case RoutineSecondaryHobby:
		if ctx.Config != nil && ctx.Config.EffectiveShift() == xdg.ShiftThird && ctx.State != nil && ctx.State.IsArmoryMaintenanceNeeded() {
			if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "Armory"); ok && len(skillIntents) > 0 {
				return appendNonNilIntents(intents, skillIntents)
			}
			if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
				return append(intents, armoryIntent)
			}
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			intents = append(intents, patrolIntent)
		}
	}
	return intents
}

// StageColonizeSkill executes frontier colonization, starter shelter construction, and perimeter scouting.
type StageColonizeSkill struct{}

// NewStageColonizeSkill creates a StageColonizeSkill instance.
func NewStageColonizeSkill() *StageColonizeSkill { return &StageColonizeSkill{} }

func (s *StageColonizeSkill) Name() string                    { return "stage_colonize" }
func (s *StageColonizeSkill) Category() string                { return "stage_colonize" }
func (s *StageColonizeSkill) Preconditions(f *FactGraph) bool { return f != nil }
func (s *StageColonizeSkill) Interruptible() bool             { return true }
func (s *StageColonizeSkill) OnComplete(_ *SkillContext, _ *FactGraph) {
}

func (s *StageColonizeSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	reg := ctx.Registry
	intents := executeStageBiologicalNeeds(ctx, facts, reg)
	intents = append(intents, executeStageRoadPaving(ctx, facts, reg, true)...)

	routine := RoutinePrimaryDuty
	if ctx.State != nil {
		routine = ctx.State.Routine()
	}

	if forageIntents, preempted := tryForagingPreemptionRaw(ctx, facts, reg); preempted {
		return append(intents, forageIntents...), nil
	}

	if (skillIsDuskOrNight(ctx, facts) || routine == RoutineSleep) && !skillHasAssignedBed(ctx, facts) && !skillIsShelterComplete(ctx, facts) && !skillIsShelterOnCooldown(ctx, facts) {
		if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
			if shelterIntent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
				return append(intents, shelterIntent), nil
			}
		}
	} else if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if !skillIsDuskOrNight(ctx, facts) && (ctx.State == nil || ctx.State.Routine() != RoutineSleep) {
			if facts != nil && facts.GetBool("system2.directive_active") &&
				(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
				if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
					return append(intents, dirIntent), nil
				}
			}
		}
		if ctx.State != nil && ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
			return intents, nil
		}
		if bootstrapIntent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); bootstrapIntent != nil {
			intents = append(intents, bootstrapIntent)
		} else if ctx.State != nil && ctx.State.HasActiveTask() {
			return intents, nil
		} else if surveyIntent := EvaluateHorizonExplorer(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); surveyIntent != nil {
			intents = append(intents, surveyIntent)
		} else if skillIntents, _, ok := reg.SelectAndExecuteSkill(ctx, facts, "exploration"); ok && len(skillIntents) > 0 {
			intents = appendNonNilIntents(intents, skillIntents)
		} else if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			intents = append(intents, patrolIntent)
		}
	}
	return intents, nil
}

// StageEmergencySkill executes reactive Tier 0 combat, evasion, and survival emergency reflexes.
type StageEmergencySkill struct{}

// NewStageEmergencySkill creates a StageEmergencySkill instance.
func NewStageEmergencySkill() *StageEmergencySkill { return &StageEmergencySkill{} }

func (s *StageEmergencySkill) Name() string                    { return "stage_emergency" }
func (s *StageEmergencySkill) Category() string                { return "stage_emergency" }
func (s *StageEmergencySkill) Preconditions(f *FactGraph) bool { return f != nil }
func (s *StageEmergencySkill) Interruptible() bool             { return false }
func (s *StageEmergencySkill) OnComplete(_ *SkillContext, _ *FactGraph) {
}

func (s *StageEmergencySkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	reg := ctx.Registry
	var skillIntents []*civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("combat_defense"); ok && skill != nil && skill.Preconditions(facts) {
		if si, err := skill.Execute(ctx, facts); err == nil {
			skillIntents = si
		}
	}
	if len(skillIntents) == 0 {
		combatSkill := NewCombatDefenseSkill()
		if si, err := combatSkill.Execute(ctx, facts); err == nil {
			skillIntents = si
		}
	}
	return skillIntents, nil
}
