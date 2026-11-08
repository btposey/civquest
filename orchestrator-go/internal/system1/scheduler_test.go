package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestEvaluateUtility_TimeBrackets(t *testing.T) {
	agentCfg := &xdg.AgentIdentityConfig{
		Name:           "elena",
		Profession:     "Farmer",
		SecondaryHobby: "APIARIST",
		Traits: xdg.AgentTraits{
			Bravery: 0.6,
		},
	}

	tests := []struct {
		name            string
		timeOfDay       int64
		expectedState   RoutineState
		expectedUtility float64
	}{
		// Dawn Awakening: Ticks 23500–1000
		{"Dawn Start", 23500, RoutineDawnAwakening, 0.80},
		{"Dawn Late Night", 23999, RoutineDawnAwakening, 0.80},
		{"Dawn Zero", 0, RoutineDawnAwakening, 0.80},
		{"Dawn Morning", 500, RoutineDawnAwakening, 0.80},
		{"Dawn End", 999, RoutineDawnAwakening, 0.80},

		// Primary Duty: Ticks 1000–8000
		{"Work Start", 1000, RoutinePrimaryDuty, 0.75},
		{"Work Midday", 4500, RoutinePrimaryDuty, 0.75},
		{"Work End", 7999, RoutinePrimaryDuty, 0.75},

		// Secondary Hobby: Ticks 8000–11000
		{"Hobby Start", 8000, RoutineSecondaryHobby, 0.70},
		{"Hobby Afternoon", 9500, RoutineSecondaryHobby, 0.70},
		{"Hobby End", 10999, RoutineSecondaryHobby, 0.70},

		// Tavern Social: Ticks 11000–13000
		{"Tavern Start", 11000, RoutineTavernSocial, 0.65},
		{"Tavern Evening", 12000, RoutineTavernSocial, 0.65},
		{"Tavern End", 12999, RoutineTavernSocial, 0.65},

		// Sleep: Ticks 13000–23500
		{"Bedtime Start", 13000, RoutineSleep, 0.90},
		{"Midnight Sleep", 18000, RoutineSleep, 0.90},
		{"Late Night Sleep", 23499, RoutineSleep, 0.90},

		// Cycle wrap-around handling
		{"Wrap Around Dawn", 24000, RoutineDawnAwakening, 0.80},
		{"Wrap Around Work", 25000, RoutinePrimaryDuty, 0.75},
		{"Multi-Day Wrap", 48500, RoutineDawnAwakening, 0.80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := &civquestv1.TelemetryFrame{
				WorldTick: 5000,
				TimeOfDay: tt.timeOfDay,
				Status: &civquestv1.AgentStatus{
					AgentId:   "elena",
					Health:    20.0,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				},
			}
			state := NewAgentState()

			decision := EvaluateUtility(frame, agentCfg, state)
			if decision.State != tt.expectedState {
				t.Fatalf("time_of_day %d: expected State %q, got %q (reason: %s)",
					tt.timeOfDay, tt.expectedState, decision.State, decision.Reason)
			}
			if decision.Utility != tt.expectedUtility {
				t.Fatalf("time_of_day %d: expected Utility %v, got %v",
					tt.timeOfDay, tt.expectedUtility, decision.Utility)
			}
			if decision.Profession != "Farmer" {
				t.Errorf("expected profession Farmer, got %q", decision.Profession)
			}
			if decision.Hobby != "APIARIST" {
				t.Errorf("expected hobby APIARIST, got %q", decision.Hobby)
			}
			if state.GetCurrentRoutine() != tt.expectedState {
				t.Errorf("expected state.CurrentRoutine to be %q, got %q",
					tt.expectedState, state.GetCurrentRoutine())
			}
		})
	}
}

func TestEvaluateUtility_PrimaryDutyBlockedFallback(t *testing.T) {
	agentCfg := &xdg.AgentIdentityConfig{
		Name:           "garrick",
		Profession:     "Blacksmith",
		SecondaryHobby: "JAM_MAKER",
	}

	frame := &civquestv1.TelemetryFrame{
		TimeOfDay: 3000, // Normally PrimaryDuty (1000-8000)
		Status: &civquestv1.AgentStatus{
			AgentId:   "garrick",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
		},
	}

	state := NewAgentState()
	state.SetPrimaryDutyBlocked(true)

	decision := EvaluateUtility(frame, agentCfg, state)
	if decision.State != RoutineSecondaryHobby {
		t.Fatalf("expected fallback to SecondaryHobby, got %s", decision.State)
	}
	if decision.Utility != 0.70 {
		t.Fatalf("expected utility 0.70, got %v", decision.Utility)
	}

	// Unblock duty -> should return to PrimaryDuty
	state.SetPrimaryDutyBlocked(false)
	decision2 := EvaluateUtility(frame, agentCfg, state)
	if decision2.State != RoutinePrimaryDuty {
		t.Fatalf("expected return to PrimaryDuty, got %s", decision2.State)
	}
	if decision2.Utility != 0.75 {
		t.Fatalf("expected utility 0.75, got %v", decision2.Utility)
	}
}

func TestEvaluateUtility_EmergencyOverrides(t *testing.T) {
	agentCfg := &xdg.AgentIdentityConfig{
		Name:       "aria",
		Profession: "Guard",
	}

	// 1. On Fire Override
	t.Run("Fire Override", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000, // Midday
			Status: &civquestv1.AgentStatus{
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				IsOnFire:  true,
			},
		}
		decision := EvaluateUtility(frame, agentCfg, nil)
		if decision.State != RoutineEmergency || decision.Utility != 1.0 {
			t.Fatalf("expected Emergency with U=1.0 for fire, got %s (U=%v)", decision.State, decision.Utility)
		}
	})

	// 2. Critical Health Override (<= 35% HP)
	t.Run("Critical Health Override", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				Health:    6.5,
				MaxHealth: 20.0, // 6.5 <= 7.0 (35%)
				FoodLevel: 20,
			},
		}
		decision := EvaluateUtility(frame, agentCfg, nil)
		if decision.State != RoutineEmergency || decision.Utility != 1.0 {
			t.Fatalf("expected Emergency with U=1.0 for critical health, got %s (U=%v)", decision.State, decision.Utility)
		}
	})

	// 3. Critical Hunger Override (<= 6) with edible food
	t.Run("Critical Hunger Override With Food", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 5,
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:bread", Count: 2, Slot: 0},
				},
			},
		}
		decision := EvaluateUtility(frame, agentCfg, nil)
		if decision.State != RoutineEmergency || decision.Utility != 1.0 {
			t.Fatalf("expected Emergency with U=1.0 for critical hunger with food, got %s (U=%v)", decision.State, decision.Utility)
		}
	})

	// 3b. Starvation softlock prevention: Critical hunger with NO edible food allows daily duties
	t.Run("Critical Hunger Without Food No Softlock", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000, // Midday PrimaryDuty
			Status: &civquestv1.AgentStatus{
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 5,
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:rotten_flesh", Count: 10, Slot: 0}, // Only toxic
				},
			},
		}
		decision := EvaluateUtility(frame, agentCfg, nil)
		if decision.State != RoutinePrimaryDuty {
			t.Fatalf("expected fallback to RoutinePrimaryDuty when no edible food is available, got %s", decision.State)
		}
	})

	// 4. Combat Panic Override (Creeper hissing nearby)
	t.Run("Combat Panic Override", func(t *testing.T) {
		frame := &civquestv1.TelemetryFrame{
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			},
			VisibleEntities: []*civquestv1.VisibleEntity{
				{
					EntityId:   1,
					EntityType: "minecraft:creeper",
					Distance:   3.0,
					FuseTicks:  10,
					Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 10},
					IsHostile:  true,
				},
			},
		}
		decision := EvaluateUtility(frame, agentCfg, nil)
		if decision.State != RoutineEmergency || decision.Utility != 1.0 {
			t.Fatalf("expected Emergency with U=1.0 for hissing creeper, got %s (U=%v)", decision.State, decision.Utility)
		}
	})
}

func TestEvaluateUtility_ThunderstormSleeping(t *testing.T) {
	// 1. Sleeping during thunderstorm
	frameSleeping := &civquestv1.TelemetryFrame{
		TimeOfDay: 5000, // Midday
		Weather:   civquestv1.Weather_WEATHER_THUNDER,
		Status: &civquestv1.AgentStatus{
			Health:     20.0,
			MaxHealth:  20.0,
			FoodLevel:  20,
			IsSleeping: true,
		},
	}
	decision := EvaluateUtility(frameSleeping, nil, nil)
	if decision.State != RoutineSleep || decision.Utility != 0.90 {
		t.Fatalf("expected Sleep state during thunderstorm while sleeping, got %s (U=%v)", decision.State, decision.Utility)
	}

	// 2. Awake during thunderstorm (synchronized with needs.go)
	frameAwake := &civquestv1.TelemetryFrame{
		TimeOfDay: 5000, // Midday
		Weather:   civquestv1.Weather_WEATHER_THUNDER,
		Status: &civquestv1.AgentStatus{
			Health:     20.0,
			MaxHealth:  20.0,
			FoodLevel:  20,
			IsSleeping: false,
		},
	}
	decisionAwake := EvaluateUtility(frameAwake, nil, nil)
	if decisionAwake.State != RoutineSleep || decisionAwake.Utility != 0.90 {
		t.Fatalf("expected Sleep state during thunderstorm even when awake, got %s (U=%v)", decisionAwake.State, decisionAwake.Utility)
	}
}

func TestEvaluateUtility_NilFrame(t *testing.T) {
	decision := EvaluateUtility(nil, nil, nil)
	if decision == nil || decision.State != RoutineEmergency {
		t.Fatalf("expected emergency fallback on nil frame, got %+v", decision)
	}
}

func TestEvaluateUtility_CompanionCampingOverride(t *testing.T) {
	state := NewAgentState()
	state.SetCompanionActive(true)

	cfg := &xdg.AgentIdentityConfig{
		Name:       "CompanionAria",
		Profession: "GUARD",
	}

	// 1. Ticks 11000-13000 (Normally Tavern Social) -> Should override to RoutineCamp
	frameTavern := &civquestv1.TelemetryFrame{
		TimeOfDay: 12000,
		WorldTick: 5000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
		},
	}
	decTavern := EvaluateUtility(frameTavern, cfg, state)
	if decTavern.State != RoutineCamp {
		t.Fatalf("expected RoutineCamp during tavern hours, got %s", decTavern.State)
	}
	if decTavern.Utility != 0.85 {
		t.Errorf("expected utility 0.85, got %v", decTavern.Utility)
	}

	// 2. Ticks 13000-23500 (Normally Sleep) -> Should override to RoutineCamp
	frameNight := &civquestv1.TelemetryFrame{
		TimeOfDay: 18000,
		WorldTick: 5500,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
		},
	}
	decNight := EvaluateUtility(frameNight, cfg, state)
	if decNight.State != RoutineCamp {
		t.Fatalf("expected RoutineCamp during night sleep hours, got %s", decNight.State)
	}
	if decNight.Utility != 0.85 {
		t.Errorf("expected utility 0.85, got %v", decNight.Utility)
	}
}
