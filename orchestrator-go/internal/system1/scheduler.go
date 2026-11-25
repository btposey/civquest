package system1

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluateUtility evaluates the 24000-tick Minecraft day-night cycle, vitals, and sensory environment
// to produce a deterministic RoutineState decision with utility weighting in <1ms without token costs.
func EvaluateUtility(frame *civquestv1.TelemetryFrame, agentCfg *xdg.AgentIdentityConfig, state *AgentState) *UtilityDecision {
	if frame == nil || frame.Status == nil {
		return &UtilityDecision{
			State:   RoutineEmergency,
			Utility: 0.0,
			Reason:  "missing frame or agent status",
		}
	}

	profession := "UNASSIGNED"
	hobby := "NONE"
	bravery := 0.5
	if agentCfg != nil {
		if agentCfg.Profession != "" {
			profession = agentCfg.Profession
		}
		if agentCfg.SecondaryHobby != "" {
			hobby = agentCfg.SecondaryHobby
		}
		bravery = agentCfg.Traits.Bravery
	}

	status := frame.Status
	selfHP := float64(status.Health)
	maxHP := float64(status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}

	// ==================== 1. EMERGENCY REFLEXES (Utility = 1.0) ====================
	// Fire
	if status.IsOnFire {
		decision := &UtilityDecision{
			State:      RoutineEmergency,
			Utility:    1.0,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "agent is on fire",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  frame.TimeOfDay,
		}
		if state != nil {
			state.SetStageAndRoutine(StageEmergency, RoutineEmergency)
		}
		return decision
	}

	// Critical health (<= 35% HP)
	if selfHP <= LowHPThresholdRatio*maxHP {
		decision := &UtilityDecision{
			State:      RoutineEmergency,
			Utility:    1.0,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "health critical",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  frame.TimeOfDay,
		}
		if state != nil {
			state.SetStageAndRoutine(StageEmergency, RoutineEmergency)
		}
		return decision
	}

	// Critical hunger (food_level <= 6)
	// Starvation softlock mitigation: only enter RoutineEmergency if actionable edible food exists in inventory.
	// If inventory has no edible food, allow scheduler to proceed with daily duties so the agent can gather/farm food.
	if status.FoodLevel <= 6 {
		if _, _, hasFood := FindOptimalFood(status.Inventory); hasFood {
			decision := &UtilityDecision{
				State:      RoutineEmergency,
				Utility:    1.0,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "hunger critical",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  frame.TimeOfDay,
			}
			if state != nil {
				state.SetStageAndRoutine(StageEmergency, RoutineEmergency)
			}
			return decision
		}
	}

	// Combat panic check
	var sumHostileThreat, sumAlliesHP float64
	var hostilesCount int
	var creeperHissingNearby bool

	selfPos := status.Position
	for _, e := range frame.VisibleEntities {
		if e == nil {
			continue
		}
		dist := float64(e.Distance)
		if dist <= 0 && e.Position != nil && selfPos != nil {
			dist = math.Hypot(e.Position.X-selfPos.X, e.Position.Z-selfPos.Z)
		}
		if dist > MaxPerceptionRadius {
			continue
		}

		if IsHostile(e) {
			hostilesCount++
			sumHostileThreat += MobThreat(e.EntityType)
			tLower := strings.ToLower(e.EntityType)
			if strings.Contains(tLower, "creeper") && e.FuseTicks > 0 && dist <= CreeperPanicRadius {
				creeperHissingNearby = true
			}
		} else if IsAlly(e) {
			aHP := float64(e.Health)
			if aHP <= 0 {
				aHP = 20.0
			}
			sumAlliesHP += aHP
		}
	}

	isDrowning := isDrowningDamage(frame)
	if hostilesCount == 0 && frame.LastDamageTaken != nil &&
		!IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) &&
		!isDrowning {
		sumHostileThreat += ThreatDefault
	}

	hasThreats := hostilesCount > 0 || (frame.LastDamageTaken != nil &&
		!IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) &&
		!isDrowning)
	gearMult := CalculateGearMultiplier(status.Inventory)
	conf := CalculateConfidence(selfHP, sumAlliesHP, sumHostileThreat, gearMult, bravery)

	if isDrowning || ShouldPanic(selfHP, maxHP, conf, hasThreats, creeperHissingNearby) {
		reason := "combat panic reflex"
		if isDrowning && !ShouldPanic(selfHP, maxHP, conf, hasThreats, creeperHissingNearby) {
			reason = "drowning emergency reflex"
		}
		decision := &UtilityDecision{
			State:      RoutineEmergency,
			Utility:    1.0,
			Profession: profession,
			Hobby:      hobby,
			Reason:     reason,
			WorldTick:  frame.WorldTick,
			TimeOfDay:  frame.TimeOfDay,
		}
		if state != nil {
			state.SetStageAndRoutine(StageEmergency, RoutineEmergency)
		}
		return decision
	}

	// ==================== 2. DAILY ROUTINE UTILITY BRACKETS ====================
	decision := EvaluateCircadianRoutine(frame.TimeOfDay, frame.Weather, agentCfg, state)
	decision.WorldTick = frame.WorldTick
	decision.Profession = profession
	decision.Hobby = hobby

	if state != nil {
		state.SetRoutine(decision.State)
	}

	return decision
}

// ComputeCircadianRoutine calculates the Tier 2 circadian routine deterministically without heap allocations.
func ComputeCircadianRoutine(
	timeOfDay int64,
	weather civquestv1.Weather,
	agentCfg *xdg.AgentIdentityConfig,
	state StateView,
) Routine {
	tod := ((timeOfDay % 24000) + 24000) % 24000

	if weather == civquestv1.Weather_WEATHER_THUNDER {
		return RoutineSleep
	}

	// Late Day Social Time at Tavern (11000 <= tod < 13000):
	// Both 1st shift and 3rd shift evaluate to RoutineTavernSocial!
	// (If companion active, RoutineCamp).
	if tod >= 11000 && tod < 13000 {
		if state != nil && state.IsCompanionActive() {
			return RoutineCamp
		}
		return RoutineTavernSocial
	}

	shift := agentCfg.EffectiveShift()

	if shift == xdg.ShiftThird {
		// 3rd Shift (Night Watch):
		// [13000, 23500): RoutineCivicPatrol ("night watch and perimeter patrol during monster emergence")
		if tod >= 13000 && tod < 23500 {
			if state != nil && state.IsCompanionActive() {
				return RoutineCamp
			}
			return RoutineCivicPatrol
		}
		// [23500, 1000): RoutineDawnPrep ("dawn watch debrief and morning handover to 1st shift")
		if tod >= 23500 || tod < 1000 {
			return RoutineDawnPrep
		}
		// [1000, 2000): Post-dawn off-watch waking hour after dawn handover
		if tod >= 1000 && tod < 2000 {
			if state != nil && state.IsArmoryMaintenanceNeeded() {
				return RoutineArmoryMaintenance
			}
			if state != nil && !state.IsArmoryMaintenanceNeeded() {
				return RoutineSecondaryHobby
			}
			return RoutineDawnPrep
		}
		// [2000, 10000): RoutineSleep ("daytime sleep during safe daylight hours")
		if tod >= 2000 && tod < 10000 {
			if state != nil && state.IsCompanionActive() {
				return RoutineCamp
			}
			return RoutineSleep
		}
		// [10000, 11000): Pre-tavern afternoon waking hour
		if state != nil && state.IsArmoryMaintenanceNeeded() {
			return RoutineArmoryMaintenance
		}
		return RoutineSecondaryHobby
	}

	// 1st Shift (Day Shift):
	// [23500, 1000): RoutineDawnPrep ("dawn awakening and morning routine")
	if tod >= 23500 || tod < 1000 {
		return RoutineDawnPrep
	}

	// [1000, 8000): RoutinePrimaryDuty (or RoutineCivicPatrol if patrol active, or RoutineSecondaryHobby if blocked)
	if tod >= 1000 && tod < 8000 {
		if state != nil && state.IsCivicPatrolActive() {
			return RoutineCivicPatrol
		}
		if state != nil && state.IsPrimaryDutyBlocked() {
			return RoutineSecondaryHobby
		}
		return RoutinePrimaryDuty
	}

	// [8000, 11000): RoutineSecondaryHobby ("gathering and practicing secondary hobby")
	if tod >= 8000 && tod < 11000 {
		return RoutineSecondaryHobby
	}

	// [13000, 23500): RoutineSleep ("sleeping through the night in assigned bed")
	if state != nil && state.IsCompanionActive() {
		return RoutineCamp
	}
	return RoutineSleep
}

// isStateShelterOrBedSatisfied returns true if state records a completed cabin shelter or assigned bed.
func isStateShelterOrBedSatisfied(state StateView) bool {
	if state == nil {
		return false
	}
	if state.GetAssignedBed() != nil {
		return true
	}
	return state.GetCabinBuilt() >= blueprints.CabinTotalBlocks
}

// gateBootstrapCircadianRoutine suppresses passive evening social and sleep idling while in BOOTSTRAP
// until basic shelter or an assigned bed has been secured (ADR-032 §4).
func gateBootstrapCircadianRoutine(stage Stage, routine Routine, state StateView, shelterOrBedSatisfied bool) Routine {
	if stage != StageBootstrap || shelterOrBedSatisfied {
		return routine
	}
	if state != nil && state.IsCompanionActive() {
		return routine
	}
	if routine == RoutineTavernSocial || routine == RoutineSleep {
		return RoutinePrimaryDuty
	}
	return routine
}

// EvaluateCircadianRoutine evaluates time of day, weather, and vocational traits to produce
// a deterministic Tier 2 Circadian Operational Routine decision with utility weighting in <1ms without token costs.
func EvaluateCircadianRoutine(
	timeOfDay int64,
	weather civquestv1.Weather,
	agentCfg *xdg.AgentIdentityConfig,
	state *AgentState,
) *UtilityDecision {
	profession := "UNASSIGNED"
	hobby := "NONE"
	if agentCfg != nil {
		if agentCfg.Profession != "" {
			profession = agentCfg.Profession
		}
		if agentCfg.SecondaryHobby != "" {
			hobby = agentCfg.SecondaryHobby
		}
	}

	tod := ((timeOfDay % 24000) + 24000) % 24000

	// Sleeping during thunderstorm override (synchronized with EvaluateSleep in needs.go)
	if weather == civquestv1.Weather_WEATHER_THUNDER {
		return &UtilityDecision{
			State:      RoutineSleep,
			Utility:    0.90,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "sleeping through thunderstorm",
			TimeOfDay:  tod,
		}
	}

	// Late Day Social Time at Tavern (11000 <= tod < 13000):
	// Both 1st shift and 3rd shift evaluate to RoutineTavernSocial!
	// (If companion active, RoutineCamp).
	if tod >= 11000 && tod < 13000 {
		if state != nil && state.IsCompanionActive() {
			return &UtilityDecision{
				State:      RoutineCamp,
				Utility:    0.85,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "camping with player companion, overriding tavern return",
				TimeOfDay:  tod,
			}
		}
		reason := "gathering at tavern for late-day social consciousness coordination"
		if agentCfg.EffectiveShift() == xdg.ShiftThird {
			reason = "late-day tavern social and shift briefing with 1st shift"
		}
		return &UtilityDecision{
			State:      RoutineTavernSocial,
			Utility:    0.65,
			Profession: profession,
			Hobby:      hobby,
			Reason:     reason,
			TimeOfDay:  tod,
		}
	}

	shift := agentCfg.EffectiveShift()

	if shift == xdg.ShiftThird {
		// [13000, 23500): RoutineCivicPatrol ("night watch and perimeter patrol during monster emergence")
		if tod >= 13000 && tod < 23500 {
			if state != nil && state.IsCompanionActive() {
				return &UtilityDecision{
					State:      RoutineCamp,
					Utility:    0.85,
					Profession: profession,
					Hobby:      hobby,
					Reason:     "camping and resting with player companion, overriding village bed routine",
					TimeOfDay:  tod,
				}
			}
			return &UtilityDecision{
				State:      RoutineCivicPatrol,
				Utility:    0.80,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "night watch and perimeter patrol during monster emergence",
				TimeOfDay:  tod,
			}
		}

		// [23500, 1000): RoutineDawnPrep ("dawn watch debrief and morning handover to 1st shift")
		if tod >= 23500 || tod < 1000 {
			return &UtilityDecision{
				State:      RoutineDawnPrep,
				Utility:    0.75,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "dawn watch debrief and morning handover to 1st shift",
				TimeOfDay:  tod,
			}
		}

		// [1000, 2000): Post-dawn off-watch waking hour after dawn handover
		if tod >= 1000 && tod < 2000 {
			if state != nil && state.IsArmoryMaintenanceNeeded() {
				return &UtilityDecision{
					State:      RoutineArmoryMaintenance,
					Utility:    0.75,
					Profession: profession,
					Hobby:      hobby,
					Reason:     "maintaining settlement armory and crafting munitions",
					TimeOfDay:  tod,
				}
			}
			if state != nil && !state.IsArmoryMaintenanceNeeded() {
				return &UtilityDecision{
					State:      RoutineSecondaryHobby,
					Utility:    0.70,
					Profession: profession,
					Hobby:      hobby,
					Reason:     "armory quotas satisfied; practicing secondary hobby",
					TimeOfDay:  tod,
				}
			}
			return &UtilityDecision{
				State:      RoutineDawnPrep,
				Utility:    0.75,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "dawn watch debrief and morning handover to 1st shift",
				TimeOfDay:  tod,
			}
		}

		// [2000, 10000): RoutineSleep ("daytime sleep during safe daylight hours")
		if tod >= 2000 && tod < 10000 {
			if state != nil && state.IsCompanionActive() {
				return &UtilityDecision{
					State:      RoutineCamp,
					Utility:    0.85,
					Profession: profession,
					Hobby:      hobby,
					Reason:     "camping and resting with player companion, overriding village bed routine",
					TimeOfDay:  tod,
				}
			}
			return &UtilityDecision{
				State:      RoutineSleep,
				Utility:    0.90,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "daytime sleep during safe daylight hours",
				TimeOfDay:  tod,
			}
		}

		// [10000, 11000): Pre-tavern afternoon waking hour
		if state != nil && state.IsArmoryMaintenanceNeeded() {
			return &UtilityDecision{
				State:      RoutineArmoryMaintenance,
				Utility:    0.70,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "maintaining settlement armory and crafting munitions",
				TimeOfDay:  tod,
			}
		}
		reason := "waking, equipment maintenance, and personal hobby"
		if state != nil && !state.IsArmoryMaintenanceNeeded() {
			reason = "armory quotas satisfied; practicing secondary hobby"
		}
		return &UtilityDecision{
			State:      RoutineSecondaryHobby,
			Utility:    0.70,
			Profession: profession,
			Hobby:      hobby,
			Reason:     reason,
			TimeOfDay:  tod,
		}
	}

	// 1st Shift (Day Shift):
	// [23500, 1000): RoutineDawnPrep ("dawn awakening and morning routine")
	if tod >= 23500 || tod < 1000 {
		return &UtilityDecision{
			State:      RoutineDawnPrep,
			Utility:    0.80,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "dawn awakening and morning routine",
			TimeOfDay:  tod,
		}
	}

	// [1000, 8000): RoutinePrimaryDuty (or RoutineCivicPatrol if patrol active, or RoutineSecondaryHobby if blocked)
	if tod >= 1000 && tod < 8000 {
		if state != nil && state.IsCivicPatrolActive() {
			return &UtilityDecision{
				State:      RoutineCivicPatrol,
				Utility:    0.75,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "performing scheduled perimeter security and landmark watch",
				TimeOfDay:  tod,
			}
		}
		if state != nil && state.IsPrimaryDutyBlocked() {
			return &UtilityDecision{
				State:      RoutineSecondaryHobby,
				Utility:    0.70,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "primary duty blocked; falling back to secondary hobby",
				TimeOfDay:  tod,
			}
		}
		return &UtilityDecision{
			State:      RoutinePrimaryDuty,
			Utility:    0.75,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "executing primary profession duties",
			TimeOfDay:  tod,
		}
	}

	// [8000, 11000): RoutineSecondaryHobby ("gathering and practicing secondary hobby")
	if tod >= 8000 && tod < 11000 {
		return &UtilityDecision{
			State:      RoutineSecondaryHobby,
			Utility:    0.70,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "gathering and practicing secondary hobby",
			TimeOfDay:  tod,
		}
	}

	// [13000, 23500): RoutineSleep ("sleeping through the night in assigned bed")
	if state != nil && state.IsCompanionActive() {
		return &UtilityDecision{
			State:      RoutineCamp,
			Utility:    0.85,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "camping and resting with player companion, overriding village bed routine",
			TimeOfDay:  tod,
		}
	}
	return &UtilityDecision{
		State:      RoutineSleep,
		Utility:    0.90,
		Profession: profession,
		Hobby:      hobby,
		Reason:     "sleeping through the night in assigned bed",
		TimeOfDay:  tod,
	}
}
