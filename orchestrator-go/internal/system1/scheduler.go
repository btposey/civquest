package system1

import (
	"math"
	"strings"

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
			state.SetCurrentRoutine(RoutineEmergency)
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
			state.SetCurrentRoutine(RoutineEmergency)
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
				state.SetCurrentRoutine(RoutineEmergency)
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

	if hostilesCount == 0 && frame.LastDamageTaken != nil {
		sumHostileThreat += ThreatDefault
	}

	hasThreats := hostilesCount > 0 || frame.LastDamageTaken != nil
	gearMult := CalculateGearMultiplier(status.Inventory)
	conf := CalculateConfidence(selfHP, sumAlliesHP, sumHostileThreat, gearMult, bravery)

	if ShouldPanic(selfHP, maxHP, conf, hasThreats, creeperHissingNearby) {
		decision := &UtilityDecision{
			State:      RoutineEmergency,
			Utility:    1.0,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "combat panic reflex",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  frame.TimeOfDay,
		}
		if state != nil {
			state.SetCurrentRoutine(RoutineEmergency)
		}
		return decision
	}

	// ==================== 2. DAILY ROUTINE UTILITY BRACKETS ====================
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	var decision *UtilityDecision

	// Sleeping during thunderstorm override (synchronized with EvaluateSleep in needs.go)
	if frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		decision = &UtilityDecision{
			State:      RoutineSleep,
			Utility:    0.90,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "sleeping through thunderstorm",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  tod,
		}
	} else if tod >= 23500 || tod < 1000 {
		// Dawn Awakening (Ticks 23500–1000)
		decision = &UtilityDecision{
			State:      RoutineDawnAwakening,
			Utility:    0.80,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "dawn awakening and morning routine",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  tod,
		}
	} else if tod >= 1000 && tod < 8000 {
		// Primary Duty (Ticks 1000–8000)
		if state != nil && state.IsPrimaryDutyBlocked() {
			decision = &UtilityDecision{
				State:      RoutineSecondaryHobby,
				Utility:    0.70,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "primary duty blocked; falling back to secondary hobby",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		} else {
			decision = &UtilityDecision{
				State:      RoutinePrimaryDuty,
				Utility:    0.75,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "executing primary profession duties",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		}
	} else if tod >= 8000 && tod < 11000 {
		// Secondary Hobby (Ticks 8000–11000)
		decision = &UtilityDecision{
			State:      RoutineSecondaryHobby,
			Utility:    0.70,
			Profession: profession,
			Hobby:      hobby,
			Reason:     "gathering and practicing secondary hobby",
			WorldTick:  frame.WorldTick,
			TimeOfDay:  tod,
		}
	} else if tod >= 11000 && tod < 13000 {
		// Tavern Social (Ticks 11000–13000)
		if state != nil && state.IsCompanionActive() {
			decision = &UtilityDecision{
				State:      RoutineCamp,
				Utility:    0.85,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "camping with player companion, overriding tavern return",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		} else {
			decision = &UtilityDecision{
				State:      RoutineTavernSocial,
				Utility:    0.65,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "gathering at tavern for social relaxation",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		}
	} else {
		// Sleep (Ticks 13000–23500)
		if state != nil && state.IsCompanionActive() {
			decision = &UtilityDecision{
				State:      RoutineCamp,
				Utility:    0.85,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "camping and resting with player companion, overriding village bed routine",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		} else {
			decision = &UtilityDecision{
				State:      RoutineSleep,
				Utility:    0.90,
				Profession: profession,
				Hobby:      hobby,
				Reason:     "navigating to assigned bed and sleeping through the night",
				WorldTick:  frame.WorldTick,
				TimeOfDay:  tod,
			}
		}
	}

	if state != nil {
		state.SetCurrentRoutine(decision.State)
	}

	return decision
}
