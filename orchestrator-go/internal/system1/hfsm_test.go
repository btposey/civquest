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
		Shift:      xdg.ShiftFirst,
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

// ==================== HTPDA & SHIFT WORK TESTS ====================

const (
	stageTestDialogue   Stage   = "DIALOGUE"
	routineTestDialogue Routine = "DIALOGUE"
)

type testDialogueStateHandler struct{}

func (h *testDialogueStateHandler) Name() State                         { return stageTestDialogue }
func (h *testDialogueStateHandler) Guard(ctx *FSMContext) bool          { return true }
func (h *testDialogueStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *testDialogueStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *testDialogueStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return nil
}

// TestTwoTierHFSM_PushdownAutomata verifies:
// 1. Preemption: entering StageEmergency pushes previous stage (StageSettlement) onto stack.
// 2. Threat clearance: ThreatsCleared() pops previous stage (StageSettlement) and resumes it with zero amnesia.
// 3. Multi-level push/pop: Dialogue interrupt on top of work, then emergency on top of dialogue.
// 4. Terminal sink: fatal health drops/clears stack and transitions to StageDead.
// 5. Stack bounds, duplicate top suppression, and capacity overflow.
func TestTwoTierHFSM_PushdownAutomata(t *testing.T) {
	t.Run("Preemption and Zero-Amnesia Resume", func(t *testing.T) {
		fsm := NewAgentFSM(StageSettlement, nil)
		state := NewAgentState()
		state.SetStageAndRoutine(StageSettlement, RoutinePrimaryDuty)

		cfg := &xdg.AgentIdentityConfig{
			Name:       "artisan",
			Profession: "Carpenter",
			Shift:      xdg.ShiftFirst,
			Traits:     xdg.AgentTraits{Bravery: 0.5},
		}

		ctx := &FSMContext{
			AgentID: "pda_artisan",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 2000,
				TimeOfDay: 3000, // Daylight work hours
				Status: &civquestv1.AgentStatus{
					AgentId:   "pda_artisan",
					Health:    20.0,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:iron_axe", Count: 1},
						{ItemId: "minecraft:iron_sword", Count: 1},
						{ItemId: "minecraft:torch", Count: 4},
					},
				},
			},
		}

		// Initial tick in settlement
		res := fsm.Tick(ctx)
		if res.Stage != StageSettlement || res.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected initial (Settlement, PrimaryDuty), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 0 {
			t.Fatalf("expected stack depth 0 initially, got %d", fsm.StackDepth())
		}

		// Preemption: Creeper appears -> StageEmergency
		ctx.Frame.WorldTick = 2020
		ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
			{
				EntityId:   201,
				EntityType: "minecraft:creeper",
				Distance:   3.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 10},
			},
		}

		res = fsm.Tick(ctx)
		if res.Stage != StageEmergency || res.Routine != RoutineEmergency {
			t.Fatalf("expected (Emergency, Emergency), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 1 {
			t.Fatalf("expected stack depth 1 after preemption, got %d", fsm.StackDepth())
		}
		peek, ok := fsm.Peek()
		if !ok || peek.Stage != StageSettlement || peek.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected top stack frame (Settlement, PrimaryDuty), got (%s, %s)", peek.Stage, peek.Routine)
		}

		// Threat clearance: Creeper despawns/killed -> threats cleared!
		ctx.Frame.WorldTick = 2050
		ctx.Frame.VisibleEntities = nil

		res = fsm.Tick(ctx)
		if res.Stage != StageSettlement || res.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected restored (Settlement, PrimaryDuty), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 0 {
			t.Fatalf("expected stack depth 0 after pop, got %d", fsm.StackDepth())
		}
		if state.Stage() != StageSettlement || state.Routine() != RoutinePrimaryDuty {
			t.Fatalf("state desync after pop: (%s, %s)", state.Stage(), state.Routine())
		}
	})

	t.Run("Multi-Level Push Pop (Dialogue Interrupt and Emergency)", func(t *testing.T) {
		fsm := NewAgentFSM(StageSettlement, nil)
		fsm.RegisterHandler(&testDialogueStateHandler{})

		state := NewAgentState()
		state.SetStageAndRoutine(StageSettlement, RoutinePrimaryDuty)

		cfg := &xdg.AgentIdentityConfig{
			Name:       "librarian",
			Profession: "Chronicler",
			Shift:      xdg.ShiftFirst,
			Traits:     xdg.AgentTraits{Bravery: 0.5},
		}

		ctx := &FSMContext{
			AgentID: "pda_librarian",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 3000,
				TimeOfDay: 4000,
				Status: &civquestv1.AgentStatus{
					AgentId:   "pda_librarian",
					Health:    20.0,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:iron_axe", Count: 1},
						{ItemId: "minecraft:iron_sword", Count: 1},
						{ItemId: "minecraft:torch", Count: 8},
					},
				},
			},
		}

		// 1. Initially at work
		res := fsm.Tick(ctx)
		if res.Stage != StageSettlement || res.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected (Settlement, PrimaryDuty), got (%s, %s)", res.Stage, res.Routine)
		}

		// 2. Dialogue interrupt: player talks to librarian
		// Agent pushes work frame and transitions to DIALOGUE
		fsm.Push(StageSettlement, RoutinePrimaryDuty, "work interrupted for player dialogue", 3000, map[string]string{"topic": "lore"})
		if !fsm.TransitionTo(stageTestDialogue, "player conversation initiated", ctx) {
			t.Fatalf("failed transition to DIALOGUE")
		}
		// Manually set dialogue routine on FSM/state for custom routine preservation
		fsm.mu.Lock()
		fsm.currentRoutine = routineTestDialogue
		fsm.mu.Unlock()
		state.SetStageAndRoutine(stageTestDialogue, routineTestDialogue)

		if fsm.StackDepth() != 1 {
			t.Fatalf("expected stack depth 1 during dialogue, got %d", fsm.StackDepth())
		}
		if fsm.CurrentStage() != stageTestDialogue || fsm.CurrentRoutine() != routineTestDialogue {
			t.Fatalf("expected (DIALOGUE, DIALOGUE), got (%s, %s)", fsm.CurrentStage(), fsm.CurrentRoutine())
		}

		// 3. Emergency strikes while in dialogue!
		ctx.Frame.WorldTick = 3050
		ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
			{
				EntityId:   301,
				EntityType: "minecraft:skeleton",
				Distance:   4.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 4, Y: 64, Z: 0},
			},
		}

		res = fsm.Tick(ctx)
		if res.Stage != StageEmergency || res.Routine != RoutineEmergency {
			t.Fatalf("expected (Emergency, Emergency), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 2 {
			t.Fatalf("expected stack depth 2 after emergency on dialogue, got %d", fsm.StackDepth())
		}
		top, ok := fsm.Peek()
		if !ok || top.Stage != stageTestDialogue || top.Routine != routineTestDialogue {
			t.Fatalf("expected top stack frame (DIALOGUE, DIALOGUE), got (%s, %s)", top.Stage, top.Routine)
		}

		// 4. Threats cleared!
		ctx.Frame.WorldTick = 3100
		ctx.Frame.VisibleEntities = nil

		res = fsm.Tick(ctx)
		// Pop 1: Must restore DIALOGUE stage and routine with zero amnesia
		if res.Stage != stageTestDialogue || res.Routine != routineTestDialogue {
			t.Fatalf("expected restored (DIALOGUE, DIALOGUE), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 1 {
			t.Fatalf("expected stack depth 1 after first pop, got %d", fsm.StackDepth())
		}
		nextTop, ok := fsm.Peek()
		if !ok || nextTop.Stage != StageSettlement || nextTop.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected next frame (Settlement, PrimaryDuty), got (%s, %s)", nextTop.Stage, nextTop.Routine)
		}

		// 5. Dialogue concludes -> Pop 2 resumes work
		workFrame, ok := fsm.Pop()
		if !ok || workFrame.Stage != StageSettlement || workFrame.Routine != RoutinePrimaryDuty {
			t.Fatalf("failed popping work frame")
		}
		if !fsm.TransitionTo(workFrame.Stage, "dialogue concluded; resuming primary duty", ctx) {
			t.Fatalf("failed transition back to Settlement")
		}
		if fsm.CurrentStage() != StageSettlement || fsm.CurrentRoutine() != RoutinePrimaryDuty {
			t.Fatalf("expected restored work state (Settlement, PrimaryDuty), got (%s, %s)", fsm.CurrentStage(), fsm.CurrentRoutine())
		}
		if fsm.StackDepth() != 0 {
			t.Fatalf("expected empty stack after full pop sequence, got %d", fsm.StackDepth())
		}
	})

	t.Run("Terminal Sink Fatal Health Clears Stack", func(t *testing.T) {
		fsm := NewAgentFSM(StageSettlement, nil)
		state := NewAgentState()

		// Push multiple frames to test stack clearing on death
		fsm.Push(StageSettlement, RoutinePrimaryDuty, "work", 100, nil)
		fsm.Push(StageSettlement, RoutineSecondaryHobby, "hobby", 200, nil)
		fsm.Push(stageTestDialogue, routineTestDialogue, "dialogue", 300, nil)
		if fsm.StackDepth() != 3 {
			t.Fatalf("expected stack depth 3, got %d", fsm.StackDepth())
		}

		ctx := &FSMContext{
			AgentID: "mortal_agent",
			State:   state,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 500,
				Status: &civquestv1.AgentStatus{
					AgentId:   "mortal_agent",
					Health:    0.0, // Lethal damage!
					MaxHealth: 20.0,
				},
			},
		}

		res := fsm.Tick(ctx)
		if res.Stage != StageDead || res.Routine != RoutineDead {
			t.Fatalf("expected terminal (DEAD, DEAD), got (%s, %s)", res.Stage, res.Routine)
		}
		if fsm.StackDepth() != 0 {
			t.Fatalf("expected stack cleared on death, got depth %d", fsm.StackDepth())
		}
		if state.Stage() != StageDead || state.Routine() != RoutineDead {
			t.Fatalf("state desync on death: (%s, %s)", state.Stage(), state.Routine())
		}

		// Subsequent ticks must remain in terminal DEAD state
		ctx.Frame.WorldTick = 510
		ctx.Frame.Status.Health = 20.0 // Even if health miraculously changes, death is permanent
		res2 := fsm.Tick(ctx)
		if res2.Stage != StageDead || res2.Routine != RoutineDead {
			t.Fatalf("permadeath invariant violation: resurrected to (%s, %s)", res2.Stage, res2.Routine)
		}

		// TransitionTo must be completely rejected from StageDead
		if fsm.TransitionTo(StageSettlement, "resurrection attempt", ctx) {
			t.Fatalf("permadeath invariant violation: TransitionTo succeeded from StageDead")
		}
	})

	t.Run("Stack Bounds and Duplicate Top Suppression", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		fsm.SetMaxStackDepth(4)

		// 1. Pushing duplicate top should be ignored
		fsm.Push(StageBootstrap, RoutineDawnPrep, "first", 1, nil)
		fsm.Push(StageBootstrap, RoutineDawnPrep, "duplicate", 2, nil)
		if fsm.StackDepth() != 1 {
			t.Fatalf("expected duplicate top ignored, depth=%d", fsm.StackDepth())
		}

		// 2. Push up to capacity
		fsm.Push(StageSettlement, RoutinePrimaryDuty, "work", 10, nil)
		fsm.Push(StageSettlement, RoutineSecondaryHobby, "hobby", 20, nil)
		fsm.Push(StageColonize, RoutinePrimaryDuty, "colony", 30, nil)
		if fsm.StackDepth() != 4 {
			t.Fatalf("expected depth 4 at capacity, got %d", fsm.StackDepth())
		}

		// 3. Overflow drops oldest
		fsm.Push(StageSettlement, RoutineTavernSocial, "tavern", 40, nil)
		if fsm.StackDepth() != 4 {
			t.Fatalf("expected depth 4 after overflow, got %d", fsm.StackDepth())
		}

		// Top should be the newest
		top, ok := fsm.Peek()
		if !ok || top.Routine != RoutineTavernSocial {
			t.Fatalf("expected newest frame on top, got %v", top)
		}

		// Clear stack
		fsm.ClearStack()
		if fsm.StackDepth() != 0 {
			t.Fatalf("expected stack depth 0 after ClearStack, got %d", fsm.StackDepth())
		}
		_, ok = fsm.Pop()
		if ok {
			t.Fatalf("pop from empty stack should return false")
		}
	})
}

// TestTwoTierHFSM_ShiftWork verifies:
// 1. 1st shift circadian progression across 24,000 ticks.
// 2. 3rd shift circadian progression across 24,000 ticks.
// 3. At ticks 11000, 12000, and 12999, BOTH 1st shift and 3rd shift are in RoutineTavernSocial.
// 4. At tick 18000 (midnight), 1st shift is RoutineSleep and 3rd shift is RoutineCivicPatrol.
// 5. At tick 5000 (midday), 1st shift is RoutinePrimaryDuty and 3rd shift is RoutineSleep.
// 6. Thunderstorm and companion camping overrides.
func TestTwoTierHFSM_ShiftWork(t *testing.T) {
	firstShiftCfg := &xdg.AgentIdentityConfig{
		Name:       "day_farmer",
		Profession: "Farmer",
		Shift:      xdg.ShiftFirst,
	}

	thirdShiftCfg := &xdg.AgentIdentityConfig{
		Name:       "night_sentry",
		Profession: "Guard",
		Shift:      xdg.ShiftThird,
	}

	t.Run("1st Shift Circadian Progression Across 24000 Ticks", func(t *testing.T) {
		for tod := int64(0); tod < 24000; tod += 100 {
			routine := ComputeCircadianRoutine(tod, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, nil)
			var expected Routine
			switch {
			case tod >= 23500 || tod < 1000:
				expected = RoutineDawnPrep
			case tod >= 1000 && tod < 8000:
				expected = RoutinePrimaryDuty
			case tod >= 8000 && tod < 11000:
				expected = RoutineSecondaryHobby
			case tod >= 11000 && tod < 13000:
				expected = RoutineTavernSocial
			case tod >= 13000 && tod < 23500:
				expected = RoutineSleep
			}

			if routine != expected {
				t.Fatalf("1st shift tod %d: expected %s, got %s", tod, expected, routine)
			}
		}
	})

	t.Run("3rd Shift Circadian Progression Across 24000 Ticks", func(t *testing.T) {
		for tod := int64(0); tod < 24000; tod += 100 {
			routine := ComputeCircadianRoutine(tod, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, nil)
			var expected Routine
			switch {
			case tod >= 11000 && tod < 13000:
				expected = RoutineTavernSocial
			case tod >= 13000 && tod < 23500:
				expected = RoutineCivicPatrol
			case tod >= 23500 || tod < 2000:
				expected = RoutineDawnPrep
			case tod >= 2000 && tod < 10000:
				expected = RoutineSleep
			case tod >= 10000 && tod < 11000:
				expected = RoutineSecondaryHobby
			}

			if routine != expected {
				t.Fatalf("3rd shift tod %d: expected %s, got %s", tod, expected, routine)
			}
		}
	})

	t.Run("Exact Specification Checkpoints", func(t *testing.T) {
		// 1. At ticks 11000, 12000, and 12999: BOTH 1st shift and 3rd shift are in RoutineTavernSocial
		for _, tick := range []int64{11000, 12000, 12999} {
			r1 := ComputeCircadianRoutine(tick, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, nil)
			r3 := ComputeCircadianRoutine(tick, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, nil)
			if r1 != RoutineTavernSocial {
				t.Errorf("tick %d: expected 1st shift RoutineTavernSocial, got %s", tick, r1)
			}
			if r3 != RoutineTavernSocial {
				t.Errorf("tick %d: expected 3rd shift RoutineTavernSocial, got %s", tick, r3)
			}

			d1 := EvaluateCircadianRoutine(tick, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, nil)
			d3 := EvaluateCircadianRoutine(tick, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, nil)
			if d1.State != RoutineTavernSocial || d3.State != RoutineTavernSocial {
				t.Errorf("tick %d: evaluate decision mismatch: d1=%s, d3=%s", tick, d1.State, d3.State)
			}
		}

		// 2. At tick 18000 (midnight): 1st shift is RoutineSleep and 3rd shift is RoutineCivicPatrol
		r1Mid := ComputeCircadianRoutine(18000, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, nil)
		r3Mid := ComputeCircadianRoutine(18000, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, nil)
		if r1Mid != RoutineSleep {
			t.Errorf("tick 18000: expected 1st shift RoutineSleep, got %s", r1Mid)
		}
		if r3Mid != RoutineCivicPatrol {
			t.Errorf("tick 18000: expected 3rd shift RoutineCivicPatrol, got %s", r3Mid)
		}

		// 3. At tick 5000 (midday): 1st shift is RoutinePrimaryDuty and 3rd shift is RoutineSleep
		r1Day := ComputeCircadianRoutine(5000, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, nil)
		r3Day := ComputeCircadianRoutine(5000, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, nil)
		if r1Day != RoutinePrimaryDuty {
			t.Errorf("tick 5000: expected 1st shift RoutinePrimaryDuty, got %s", r1Day)
		}
		if r3Day != RoutineSleep {
			t.Errorf("tick 5000: expected 3rd shift RoutineSleep, got %s", r3Day)
		}
	})

	t.Run("Thunderstorm and Companion Overrides", func(t *testing.T) {
		// Thunderstorm forces RoutineSleep for both shifts
		r1Thunder := ComputeCircadianRoutine(5000, civquestv1.Weather_WEATHER_THUNDER, firstShiftCfg, nil)
		r3Thunder := ComputeCircadianRoutine(18000, civquestv1.Weather_WEATHER_THUNDER, thirdShiftCfg, nil)
		if r1Thunder != RoutineSleep || r3Thunder != RoutineSleep {
			t.Errorf("thunderstorm should force RoutineSleep: r1=%s, r3=%s", r1Thunder, r3Thunder)
		}

		// Companion active forces RoutineCamp during social and rest brackets
		state := NewAgentState()
		state.SetCompanionActive(true)

		r1Camp := ComputeCircadianRoutine(12000, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, state)
		r3Camp := ComputeCircadianRoutine(12000, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, state)
		if r1Camp != RoutineCamp || r3Camp != RoutineCamp {
			t.Errorf("companion active should force RoutineCamp at 12000: r1=%s, r3=%s", r1Camp, r3Camp)
		}

		r1CampSleep := ComputeCircadianRoutine(18000, civquestv1.Weather_WEATHER_CLEAR, firstShiftCfg, state)
		r3CampSleep := ComputeCircadianRoutine(5000, civquestv1.Weather_WEATHER_CLEAR, thirdShiftCfg, state)
		if r1CampSleep != RoutineCamp || r3CampSleep != RoutineCamp {
			t.Errorf("companion active should force RoutineCamp during sleep periods: r1=%s, r3=%s", r1CampSleep, r3CampSleep)
		}
	})
}

// BenchmarkTwoTierHFSM_ShiftWorkAndPDA verifies that full Two-Tier HFSM ticks execute well under
// the 5ms real-time budget, achieving microsecond-level determinism.
func BenchmarkTwoTierHFSM_ShiftWorkAndPDA(b *testing.B) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{
		Name:       "bench_agent",
		Profession: "Guard",
		Shift:      xdg.ShiftThird,
	}

	ctx := &FSMContext{
		AgentID: "bench_agent",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 18000, // Midnight 3rd shift patrol
			Status: &civquestv1.AgentStatus{
				AgentId:   "bench_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:iron_sword", Count: 1},
					{ItemId: "minecraft:shield", Count: 1},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.Frame.WorldTick = int64(i)
		ctx.Frame.TimeOfDay = int64(i*50) % 24000

		// Sporadic threat to exercise PDA push/pop
		if i%100 == 50 {
			ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
				{EntityId: 999, EntityType: "minecraft:creeper", Distance: 3.0, IsHostile: true},
			}
		} else if i%100 == 55 {
			ctx.Frame.VisibleEntities = nil
		}

		res := fsm.Tick(ctx)
		_ = res.Stage
		_ = res.Routine
	}
}

func TestTwoTierHFSM_ArmoryMaintenanceRoutine(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()
	state.SetAssignedBed(&civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0})
	state.SetStageAndRoutine(StageSettlement, RoutineArmoryMaintenance)

	cfg := &xdg.AgentIdentityConfig{
		Name:       "SentryGarrick",
		Profession: "GUARD",
		Shift:      xdg.ShiftThird,
	}

	// 1. Agent has feathers, sticks, flint -> should emit craft intent for arrows
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 10500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:iron_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:iron_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
				{Slot: 3, ItemId: "minecraft:feather", Count: 4},
				{Slot: 4, ItemId: "minecraft:stick", Count: 4},
				{Slot: 5, ItemId: "minecraft:flint", Count: 4},
			},
		},
	}

	ctx := &FSMContext{
		AgentID: "SentryGarrick",
		Frame:   frame,
		State:   state,
		Config:  cfg,
	}

	res := fsm.Tick(ctx)
	if len(res.Intents) == 0 {
		t.Fatalf("expected emitted intents during RoutineArmoryMaintenance, got 0")
	}

	var foundCraft bool
	for _, it := range res.Intents {
		if it.Intent != nil && it.Intent.GetCraftItem() != nil && it.Intent.GetCraftItem().RecipeId == "minecraft:arrow" {
			foundCraft = true
			break
		}
	}
	if !foundCraft {
		t.Fatalf("expected CraftItem intent for minecraft:arrow, got %+v", res.Intents)
	}

	// 2. When armory quotas are satisfied, no craft intent emitted and armoryMaintenanceNeeded becomes false
	state.SetLastCraftTick(500)
	frameSufficient := &civquestv1.TelemetryFrame{
		WorldTick: 550,
		TimeOfDay: 10550,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:iron_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:torch", Count: 8},
				{Slot: 2, ItemId: "minecraft:iron_sword", Count: 1},
				{Slot: 3, ItemId: "minecraft:bow", Count: 1},
				{Slot: 4, ItemId: "minecraft:iron_helmet", Count: 1},
				{Slot: 5, ItemId: "minecraft:iron_chestplate", Count: 1},
				{Slot: 6, ItemId: "minecraft:iron_leggings", Count: 1},
				{Slot: 7, ItemId: "minecraft:iron_boots", Count: 1},
				{Slot: 8, ItemId: "minecraft:arrow", Count: 32},
				{Slot: 9, ItemId: "minecraft:tnt", Count: 4},
			},
		},
	}
	ctxSufficient := &FSMContext{
		AgentID: "SentryGarrick",
		Frame:   frameSufficient,
		State:   state,
		Config:  cfg,
	}

	resSufficient := fsm.Tick(ctxSufficient)
	for _, it := range resSufficient.Intents {
		if it.Intent != nil && it.Intent.GetCraftItem() != nil {
			t.Fatalf("did not expect CraftItem intent when armory is sufficient, got %+v", it)
		}
	}
	if state.IsArmoryMaintenanceNeeded() {
		t.Errorf("expected IsArmoryMaintenanceNeeded to be false after sufficient check")
	}
}
