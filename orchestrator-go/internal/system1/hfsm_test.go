package system1

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// TestTwoTierHFSM_StageTransitions verifies the formal Tier 1 Macro Strategic Stage transitions:
// 1. StageBootstrap -> StageSettlement upon acquiring survival kit (axe, sword, light)
// 2. StageSettlement -> StageEmergency upon creeper hostile threat
// 3. StageEmergency -> StageSettlement upon threats cleared
// 4. StageSettlement -> StageColonize upon founding a new settlement node
// 5. StageColonize -> StageSettlement upon colony site establishment complete
// 6. StageSettlement -> StageBootstrap upon tool loss/breakage in daylight
func TestTwoTierHFSM_StageTransitions(t *testing.T) {
	var stageTransitions []string
	fsm := NewAgentFSM(StageBootstrap, func(from, to State, reason string) {
		stageTransitions = append(stageTransitions, string(from)+"->"+string(to))
	})

	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Name:       "pioneer",
		Profession: "Builder",
		Traits:     xdg.AgentTraits{Bravery: 0.5},
	}

	ctx := &FSMContext{
		AgentID: "hfsm_pioneer",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 2000, // Daylight work hours
			Status: &civquestv1.AgentStatus{
				AgentId:   "hfsm_pioneer",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Inventory: []*civquestv1.ItemStackProto{}, // No survival kit initially
			},
		},
	}

	// 1. Initially in Bootstrap without kit
	res := fsm.Tick(ctx)
	if res.Stage != StageBootstrap {
		t.Fatalf("expected initial stage %s, got %s", StageBootstrap, res.Stage)
	}
	if res.Routine != RoutinePrimaryDuty {
		t.Fatalf("expected routine %s during work hours in bootstrap, got %s", RoutinePrimaryDuty, res.Routine)
	}
	if state.Stage() != StageBootstrap || state.Routine() != RoutinePrimaryDuty {
		t.Fatalf("state desync in initial bootstrap: state.Stage=%s, state.Routine=%s", state.Stage(), state.Routine())
	}

	// Collect survival kit (wooden axe, sword, torches) -> Advances to StageSettlement
	ctx.Frame.WorldTick = 105
	ctx.Frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wooden_axe", Count: 1},
		{ItemId: "minecraft:wooden_sword", Count: 1},
		{ItemId: "minecraft:torch", Count: 4},
	}

	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected transition to %s upon acquiring kit, got %s", StageSettlement, res.Stage)
	}
	if !res.StageTransitioned {
		t.Fatalf("expected StageTransitioned=true on kit acquisition")
	}
	if state.Stage() != StageSettlement {
		t.Fatalf("state.Stage() desync: expected %s, got %s", StageSettlement, state.Stage())
	}

	// 2. Creeper appears nearby -> Preempts to StageEmergency
	ctx.Frame.WorldTick = 110
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   101,
			EntityType: "minecraft:creeper",
			Distance:   3.5,
			IsHostile:  true,
			FuseTicks:  20,
			Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 2},
		},
	}

	res = fsm.Tick(ctx)
	if res.Stage != StageEmergency {
		t.Fatalf("expected transition to %s when creeper appears, got %s", StageEmergency, res.Stage)
	}
	if res.Routine != RoutineEmergency {
		t.Fatalf("expected routine %s in emergency, got %s", RoutineEmergency, res.Routine)
	}
	if state.Stage() != StageEmergency || state.Routine() != RoutineEmergency {
		t.Fatalf("state desync during emergency: stage=%s, routine=%s", state.Stage(), state.Routine())
	}

	// 3. Creeper cleared, daylight -> Returns to StageSettlement
	ctx.Frame.WorldTick = 120
	ctx.Frame.VisibleEntities = nil

	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected transition back to %s when creeper cleared, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutinePrimaryDuty {
		t.Fatalf("expected routine %s after emergency cleared, got %s", RoutinePrimaryDuty, res.Routine)
	}

	// 4. Founding a new frontier settlement node -> Transitions to StageColonize
	state.SetEstablishingNewSettlement(true)
	ctx.Frame.WorldTick = 130

	res = fsm.Tick(ctx)
	if res.Stage != StageColonize {
		t.Fatalf("expected transition to %s when establishing new settlement, got %s", StageColonize, res.Stage)
	}
	if state.Stage() != StageColonize {
		t.Fatalf("state.Stage() desync: expected %s, got %s", StageColonize, state.Stage())
	}

	// 5. Colony node established -> Returns to StageSettlement
	state.SetEstablishingNewSettlement(false)
	ctx.Frame.WorldTick = 140

	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected transition to %s when colonization complete, got %s", StageSettlement, res.Stage)
	}
	if state.Stage() != StageSettlement {
		t.Fatalf("state.Stage() desync: expected %s, got %s", StageSettlement, state.Stage())
	}

	// 6. Tools broken in daylight -> Reverts to StageBootstrap
	ctx.Frame.WorldTick = 150
	ctx.Frame.Status.Inventory = []*civquestv1.ItemStackProto{} // Tools broken

	res = fsm.Tick(ctx)
	if res.Stage != StageBootstrap {
		t.Fatalf("expected revert to %s when tools broken, got %s", StageBootstrap, res.Stage)
	}
	if state.Stage() != StageBootstrap {
		t.Fatalf("state.Stage() desync: expected %s, got %s", StageBootstrap, state.Stage())
	}
}

// TestTwoTierHFSM_CircadianRoutineProgression verifies smooth Tier 2 circadian progression
// across all 24000 ticks of the Minecraft day-night cycle within StageSettlement.
func TestTwoTierHFSM_CircadianRoutineProgression(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	state.SetStage(StageSettlement)
	cfg := &xdg.AgentIdentityConfig{
		Name:           "elena",
		Profession:     "Farmer",
		SecondaryHobby: "BOTANIST",
		Traits:         xdg.AgentTraits{Bravery: 0.6},
	}

	ctx := &FSMContext{
		AgentID: "hfsm_circadian_agent",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "hfsm_circadian_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:iron_axe", Count: 1},
					{ItemId: "minecraft:iron_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 8},
				},
			},
		},
	}

	testCases := []struct {
		name            string
		timeOfDay       int64
		weather         civquestv1.Weather
		companionActive bool
		expectedStage   Stage
		expectedRoutine Routine
	}{
		// Dawn Awakening: Ticks 23500–1000
		{"Dawn Start", 23500, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineDawnPrep},
		{"Dawn Midnight Wrap", 23999, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineDawnPrep},
		{"Dawn Zero", 0, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineDawnPrep},
		{"Dawn Mid-Morning", 500, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineDawnPrep},
		{"Dawn End", 999, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineDawnPrep},

		// Primary Duty: Ticks 1000–8000
		{"Work Hours Start", 1000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutinePrimaryDuty},
		{"Work Hours Midday", 4500, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutinePrimaryDuty},
		{"Work Hours End", 7999, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutinePrimaryDuty},

		// Secondary Hobby: Ticks 8000–11000
		{"Hobby Start", 8000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSecondaryHobby},
		{"Hobby Afternoon", 9500, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSecondaryHobby},
		{"Hobby End", 10999, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSecondaryHobby},

		// Tavern Social: Ticks 11000–13000
		{"Tavern Evening Start", 11000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineTavernSocial},
		{"Tavern Hearth Gathering", 12000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineTavernSocial},
		{"Tavern Evening End", 12999, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineTavernSocial},

		// Sleep: Ticks 13000–23500
		{"Night Bedtime Start", 13000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSleep},
		{"Deep Night Rest", 18000, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSleep},
		{"Late Night Before Dawn", 23499, civquestv1.Weather_WEATHER_CLEAR, false, StageSettlement, RoutineSleep},

		// Thunderstorm Override
		{"Thunderstorm Midday Sleep", 4000, civquestv1.Weather_WEATHER_THUNDER, false, StageSettlement, RoutineSleep},

		// Companion Camping Overrides
		{"Companion Evening Camping", 11500, civquestv1.Weather_WEATHER_CLEAR, true, StageSettlement, RoutineCamp},
		{"Companion Night Camping", 16000, civquestv1.Weather_WEATHER_CLEAR, true, StageSettlement, RoutineCamp},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state.SetCompanionActive(tc.companionActive)
			ctx.Frame.TimeOfDay = tc.timeOfDay
			ctx.Frame.Weather = tc.weather
			ctx.Frame.WorldTick++

			res := fsm.Tick(ctx)
			if res.Stage != tc.expectedStage {
				t.Fatalf("tod=%d: expected Stage %s, got %s", tc.timeOfDay, tc.expectedStage, res.Stage)
			}
			if res.Routine != tc.expectedRoutine {
				t.Fatalf("tod=%d: expected Routine %s, got %s", tc.timeOfDay, tc.expectedRoutine, res.Routine)
			}
			if state.Stage() != tc.expectedStage || state.Routine() != tc.expectedRoutine {
				t.Fatalf("tod=%d: state desync: state=(%s, %s) vs res=(%s, %s)",
					tc.timeOfDay, state.Stage(), state.Routine(), res.Stage, res.Routine)
			}
		})
	}
}

// TestTwoTierHFSM_CivicPatrolRoutine verifies that agents performing sentry/perimeter duty
// transition into RoutineCivicPatrol during scheduled watch hours.
func TestTwoTierHFSM_CivicPatrolRoutine(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	state.SetStage(StageSettlement)
	state.SetCivicPatrolActive(true) // Agent assigned to perimeter sentry watch

	cfg := &xdg.AgentIdentityConfig{
		Name:       "boreas",
		Profession: "Guard",
		Traits:     xdg.AgentTraits{Bravery: 0.8},
	}

	ctx := &FSMContext{
		AgentID: "hfsm_guard",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 3000, // Daylight work shift
			Status: &civquestv1.AgentStatus{
				AgentId:   "hfsm_guard",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:iron_axe", Count: 1},
					{ItemId: "minecraft:iron_sword", Count: 1},
					{ItemId: "minecraft:shield", Count: 1},
					{ItemId: "minecraft:torch", Count: 8},
				},
			},
		},
	}

	res := fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutineCivicPatrol {
		t.Fatalf("expected routine %s for active sentry guard, got %s", RoutineCivicPatrol, res.Routine)
	}
	if state.Routine() != RoutineCivicPatrol {
		t.Fatalf("state.Routine() desync: expected %s, got %s", RoutineCivicPatrol, state.Routine())
	}
}

// TestTwoTierHFSM_ZeroDesyncInvariant verifies that across 10,000 continuous ticks with
// dynamic time progression, threat injections, and settlement events, there is strictly
// zero desynchronization between AgentFSM and AgentState.
func TestTwoTierHFSM_ZeroDesyncInvariant(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Name:       "aria",
		Profession: "Builder",
		Traits:     xdg.AgentTraits{Bravery: 0.5},
	}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 0,
		TimeOfDay: 0,
		Status: &civquestv1.AgentStatus{
			AgentId:   "aria",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: []*civquestv1.ItemStackProto{},
		},
	}

	ctx := &FSMContext{
		AgentID: "aria",
		State:   state,
		Config:  cfg,
		Frame:   frame,
	}

	rng := rand.New(rand.NewSource(20261116))

	for tick := int64(0); tick < 10000; tick++ {
		frame.WorldTick = tick
		frame.TimeOfDay = (tick * 100) % 24000

		// Random events
		// Survival kit acquired at tick 200
		if tick == 200 {
			frame.Status.Inventory = []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 4},
			}
		}

		// Sporadic hostile threat injection
		if tick >= 1000 && tick <= 1050 {
			frame.VisibleEntities = []*civquestv1.VisibleEntity{
				{
					EntityId:   500,
					EntityType: "minecraft:zombie",
					Distance:   2.5,
					IsHostile:  true,
					Position:   &civquestv1.Vec3{X: 1, Y: 64, Z: 1},
				},
			}
		} else if tick > 1050 && len(frame.VisibleEntities) > 0 {
			frame.VisibleEntities = nil
		}

		// Colonize trigger at tick 3000-3500
		if tick == 3000 {
			state.SetEstablishingNewSettlement(true)
		} else if tick == 3500 {
			state.SetEstablishingNewSettlement(false)
		}

		// Occasional hunger dip and eat
		if tick%2000 == 1500 {
			frame.Status.FoodLevel = 5
			frame.Status.Inventory = append(frame.Status.Inventory, &civquestv1.ItemStackProto{
				ItemId: "minecraft:bread", Count: 5,
			})
		} else if tick%2000 == 1520 {
			frame.Status.FoodLevel = 20
		}

		// Random small movement
		frame.Status.Position.X += (rng.Float64() - 0.5) * 0.1
		frame.Status.Position.Z += (rng.Float64() - 0.5) * 0.1

		res := fsm.Tick(ctx)

		// INVARIANT VERIFICATION:
		// 1. FSM Stage matches state.Stage()
		if fsm.CurrentStage() != state.Stage() {
			t.Fatalf("tick %d: FSM.Stage (%s) != state.Stage (%s)", tick, fsm.CurrentStage(), state.Stage())
		}
		// 2. FSM Routine matches state.Routine()
		if fsm.CurrentRoutine() != state.Routine() {
			t.Fatalf("tick %d: FSM.Routine (%s) != state.Routine (%s)", tick, fsm.CurrentRoutine(), state.Routine())
		}
		// 3. FSM Result matches StateTuple
		tuple := state.Tuple()
		if res.Stage != tuple.Stage || res.Routine != tuple.Routine {
			t.Fatalf("tick %d: res (%s, %s) != state.Tuple (%s, %s)",
				tick, res.Stage, res.Routine, tuple.Stage, tuple.Routine)
		}
		// 4. Backward compatibility getters match
		if fsm.CurrentState() != state.GetFSMState() {
			t.Fatalf("tick %d: FSM.CurrentState (%s) != state.GetFSMState (%s)",
				tick, fsm.CurrentState(), state.GetFSMState())
		}
		if fsm.CurrentRoutine() != state.GetCurrentRoutine() {
			t.Fatalf("tick %d: FSM.CurrentRoutine (%s) != state.GetCurrentRoutine (%s)",
				tick, fsm.CurrentRoutine(), state.GetCurrentRoutine())
		}
		// 5. If emergency stage, routine MUST be RoutineEmergency
		if res.Stage == StageEmergency && res.Routine != RoutineEmergency {
			t.Fatalf("tick %d: StageEmergency active but Routine is %s; must be RoutineEmergency", tick, res.Routine)
		}
	}
}

// TestTwoTierHFSM_Sub5msExecutionBudget verifies that the entire Two-Tier HFSM tick
// executes deterministically within the strict <5ms System 1 zero-token budget.
func TestTwoTierHFSM_Sub5msExecutionBudget(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Name:       "speed_agent",
		Profession: "Explorer",
		Traits:     xdg.AgentTraits{Bravery: 0.5},
	}

	ctx := &FSMContext{
		AgentID: "speed_agent",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "speed_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:iron_axe", Count: 1},
					{ItemId: "minecraft:iron_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 16},
				},
			},
		},
	}

	const iterations = 1000
	var totalDuration time.Duration
	var maxDuration time.Duration

	for i := 0; i < iterations; i++ {
		ctx.Frame.WorldTick = int64(i)
		ctx.Frame.TimeOfDay = int64((i * 24) % 24000)

		start := time.Now()
		res := fsm.Tick(ctx)
		dur := time.Since(start)

		totalDuration += dur
		if dur > maxDuration {
			maxDuration = dur
		}

		if res.Stage == "" || res.Routine == "" {
			t.Fatalf("iteration %d: empty stage or routine in result", i)
		}
	}

	meanDuration := totalDuration / iterations
	t.Logf("Two-Tier HFSM Performance: %d iterations, mean=%v, max=%v", iterations, meanDuration, maxDuration)

	// Mean duration must be sub-100 microseconds (far below 5ms budget)
	if meanDuration > 5*time.Millisecond {
		t.Fatalf("VIOLATION: Mean HFSM tick latency (%v) exceeded 5ms budget", meanDuration)
	}
	if maxDuration > 5*time.Millisecond {
		t.Fatalf("VIOLATION: Max HFSM tick latency (%v) exceeded 5ms budget", maxDuration)
	}
}

// TestTwoTierHFSM_ConcurrentSafety verifies that concurrent access to Two-Tier HFSM and
// AgentState across 25 goroutines is completely race-free under 'go test -race'.
func TestTwoTierHFSM_ConcurrentSafety(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Name:       "race_agent",
		Profession: "Miner",
		Traits:     xdg.AgentTraits{Bravery: 0.5},
	}

	var wg sync.WaitGroup
	const goroutines = 25
	const iterations = 200

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				tick := int64(gid*iterations + i)
				ctx := &FSMContext{
					AgentID: "race_agent",
					State:   state,
					Config:  cfg,
					Frame: &civquestv1.TelemetryFrame{
						WorldTick: tick,
						TimeOfDay: (tick * 50) % 24000,
						Status: &civquestv1.AgentStatus{
							AgentId:   "race_agent",
							Health:    20.0,
							MaxHealth: 20.0,
							FoodLevel: 20,
							Position:  &civquestv1.Vec3{X: float64(gid), Y: 64, Z: float64(i)},
							Inventory: []*civquestv1.ItemStackProto{
								{ItemId: "minecraft:iron_pickaxe", Count: 1},
							},
						},
					},
				}

				res := fsm.Tick(ctx)
				_ = res.Stage
				_ = res.Routine
				_ = fsm.CurrentStage()
				_ = fsm.CurrentRoutine()
				_ = fsm.StateTuple()
				_ = state.Stage()
				_ = state.Routine()
				_ = state.Tuple()
			}
		}(g)
	}

	wg.Wait()
}
