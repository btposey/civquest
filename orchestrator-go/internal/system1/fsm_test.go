package system1

import (
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestFSM_InitialState(t *testing.T) {
	fsm := NewAgentFSM(StateBootstrap, nil)
	if fsm.CurrentState() != StateBootstrap {
		t.Fatalf("expected initial state %s, got %s", StateBootstrap, fsm.CurrentState())
	}
}

func TestFSM_StateTransitions(t *testing.T) {
	var transitions []string
	fsm := NewAgentFSM(StageBootstrap, func(from, to State, reason string) {
		transitions = append(transitions, string(from)+"->"+string(to))
	})

	state := NewAgentState()

	// 1. In Bootstrap, with survival kit (axe, sword, torch) in daylight -> transitions to SETTLEMENT
	ctx := &FSMContext{
		AgentID: "fsm_test_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 2000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "fsm_test_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:wooden_axe", Count: 1},
					{ItemId: "minecraft:wooden_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 4},
				},
			},
		},
	}

	res := fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s after acquiring survival kit, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutinePrimaryDuty {
		t.Fatalf("expected routine %s during work hours, got %s", RoutinePrimaryDuty, res.Routine)
	}
	if state.Stage() != StageSettlement || state.Routine() != RoutinePrimaryDuty {
		t.Fatalf("desync between FSM (%s, %s) and State (%s, %s)",
			res.Stage, res.Routine, state.Stage(), state.Routine())
	}

	// 2. Creeper appears -> transitions to EMERGENCY
	ctx.Frame.WorldTick = 105
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   101,
			EntityType: "minecraft:creeper",
			IsHostile:  true,
			Distance:   3.0,
			Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 3},
		},
	}

	res = fsm.Tick(ctx)
	if res.Stage != StageEmergency {
		t.Fatalf("expected stage %s when creeper appears, got %s", StageEmergency, res.Stage)
	}
	if res.Routine != RoutineEmergency {
		t.Fatalf("expected routine %s during emergency, got %s", RoutineEmergency, res.Routine)
	}
	if state.Stage() != StageEmergency || state.Routine() != RoutineEmergency {
		t.Fatalf("desync between FSM (%s, %s) and State (%s, %s)",
			res.Stage, res.Routine, state.Stage(), state.Routine())
	}

	// 3. Creeper disappears, daylight -> transitions back to SETTLEMENT
	ctx.Frame.WorldTick = 110
	ctx.Frame.VisibleEntities = nil

	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s when creeper cleared, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutinePrimaryDuty {
		t.Fatalf("expected routine %s during work hours, got %s", RoutinePrimaryDuty, res.Routine)
	}

	// 4. Founding new settlement site -> transitions to COLONIZE
	state.SetEstablishingNewSettlement(true)
	ctx.Frame.WorldTick = 115
	res = fsm.Tick(ctx)
	if res.Stage != StageColonize {
		t.Fatalf("expected stage %s when founding new settlement, got %s", StageColonize, res.Stage)
	}

	// 5. Colonization complete -> transitions back to SETTLEMENT
	state.SetEstablishingNewSettlement(false)
	ctx.Frame.WorldTick = 120
	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s when colonization complete, got %s", StageSettlement, res.Stage)
	}

	// 6. Circadian routine progression within StageSettlement
	// Dusk (12000) -> Tavern Social
	ctx.Frame.WorldTick = 130
	ctx.Frame.TimeOfDay = 12000
	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s at dusk, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutineTavernSocial {
		t.Fatalf("expected routine %s at dusk, got %s", RoutineTavernSocial, res.Routine)
	}

	// Night (15000) -> Sleep
	ctx.Frame.WorldTick = 140
	ctx.Frame.TimeOfDay = 15000
	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s at night, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutineSleep {
		t.Fatalf("expected routine %s at night, got %s", RoutineSleep, res.Routine)
	}

	// Dawn (23600) -> Dawn Prep
	ctx.Frame.WorldTick = 150
	ctx.Frame.TimeOfDay = 23600
	res = fsm.Tick(ctx)
	if res.Stage != StageSettlement {
		t.Fatalf("expected stage %s at dawn, got %s", StageSettlement, res.Stage)
	}
	if res.Routine != RoutineDawnPrep {
		t.Fatalf("expected routine %s at dawn, got %s", RoutineDawnPrep, res.Routine)
	}
}

func TestFSM_EmergencyToSleepBlockedMidCombat(t *testing.T) {
	fsm := NewAgentFSM(StateEmergency, nil)
	state := NewAgentState()

	// Night time with bed assigned, but hostile zombie is present
	ctx := &FSMContext{
		AgentID: "combat_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "combat_agent",
				Health:    15.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			},
			VisibleEntities: []*civquestv1.VisibleEntity{
				{
					EntityId:   201,
					EntityType: "minecraft:skeleton",
					IsHostile:  true,
					Distance:   2.0,
					Position:   &civquestv1.Vec3{X: 12, Y: 64, Z: 10},
				},
			},
		},
	}
	state.SetAssignedBed(&civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10})

	res := fsm.Tick(ctx)
	if res.State != StateEmergency {
		t.Fatalf("VIOLATION: FSM transitioned to %s while hostile was present; must stay in %s", res.State, StateEmergency)
	}

	// Attempt forced transition to REST - Guard must block it
	allowed := fsm.TransitionTo(StateRest, "test forced transition", ctx)
	if allowed {
		t.Fatalf("VIOLATION: TransitionTo(StateRest) succeeded while hostiles were present")
	}
	if fsm.CurrentState() != StateEmergency {
		t.Fatalf("expected state to remain %s, got %s", StateEmergency, fsm.CurrentState())
	}
}

func TestFSM_StuckStateFallback(t *testing.T) {
	fsm := NewAgentFSM(StateShelter, nil)
	state := NewAgentState()

	// Agent stuck in SHELTER state beyond timeout (600 ticks)
	ctx := &FSMContext{
		AgentID: "stuck_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 14000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "stuck_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
			},
		},
	}

	// Tick repeatedly with identical position to accumulate stuck ticks
	var transitionedToExplore bool
	for i := int64(0); i < 605; i++ {
		ctx.Frame.WorldTick = 1000 + i
		res := fsm.Tick(ctx)
		if res.State == StateExplore && res.Transitioned {
			transitionedToExplore = true
			break
		}
	}

	if !transitionedToExplore {
		t.Fatalf("expected stuck shelter state to fallback to %s, got %s", StateExplore, fsm.CurrentState())
	}
}

func TestFSM_CriticalHungerNoThrashing(t *testing.T) {
	fsm := NewAgentFSM(StateExplore, nil)
	state := NewAgentState()

	// Agent in EXPLORE drops to foodLevel 5 with bread and survival kit in inventory
	ctx := &FSMContext{
		AgentID: "hungry_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 200,
			TimeOfDay: 2000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "hungry_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 5,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:bread", Count: 3},
					{ItemId: "minecraft:wooden_axe", Count: 1},
					{ItemId: "minecraft:wooden_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 4},
				},
			},
		},
	}

	// First tick: transitions from EXPLORE to EMERGENCY
	res := fsm.Tick(ctx)
	if res.State != StateEmergency {
		t.Fatalf("expected state %s on critical hunger, got %s", StateEmergency, res.State)
	}

	// Subsequent ticks: must stay in EMERGENCY (no thrashing) until food eaten
	for tick := int64(201); tick <= 210; tick++ {
		ctx.Frame.WorldTick = tick
		res := fsm.Tick(ctx)
		if res.State != StateEmergency {
			t.Fatalf("VIOLATION: FSM thrashed from %s to %s while still in critical hunger at tick %d", StateEmergency, res.State, tick)
		}
	}

	// Now food is eaten and foodLevel restored to 20 -> transitions back to EXPLORE
	ctx.Frame.WorldTick = 211
	ctx.Frame.Status.FoodLevel = 20
	res = fsm.Tick(ctx)
	if res.State != StateExplore {
		t.Fatalf("expected state %s once hunger satisfied, got %s", StateExplore, res.State)
	}
}

func TestFSM_SteadyStateExploreUtilitySynchronization(t *testing.T) {
	fsm := NewAgentFSM(StateExplore, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{}
	state.SetAssignedBed(&civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10})

	ctx := &FSMContext{
		AgentID: "explorer_agent",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 2000, // Primary duty (1000-8000)
			Status: &civquestv1.AgentStatus{
				AgentId:   "explorer_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:wooden_axe", Count: 1},
					{ItemId: "minecraft:wooden_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 4},
				},
			},
		},
	}

	fsm.Tick(ctx)
	if state.GetCurrentRoutine() != RoutinePrimaryDuty {
		t.Fatalf("expected routine %s at tick 2000, got %s", RoutinePrimaryDuty, state.GetCurrentRoutine())
	}

	// Advance time of day to 9000 (Secondary hobby 8000-11000) without FSM state boundary transition
	ctx.Frame.WorldTick = 600
	ctx.Frame.TimeOfDay = 9000
	fsm.Tick(ctx)
	if state.GetCurrentRoutine() != RoutineSecondaryHobby {
		t.Fatalf("expected routine %s at tick 9000, got %s", RoutineSecondaryHobby, state.GetCurrentRoutine())
	}

	// Advance time of day to 11500 (Tavern social 11000-13000)
	ctx.Frame.WorldTick = 700
	ctx.Frame.TimeOfDay = 11500
	fsm.Tick(ctx)
	if state.GetCurrentRoutine() != RoutineTavernSocial {
		t.Fatalf("expected routine %s at tick 11500, got %s", RoutineTavernSocial, state.GetCurrentRoutine())
	}
}

func TestFSM_ShelterTimeoutCooldownPreventsImmediateReentry(t *testing.T) {
	fsm := NewAgentFSM(StateShelter, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "timeout_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 15000, // Night time
			Status: &civquestv1.AgentStatus{
				AgentId:   "timeout_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			},
		},
	}

	// Tick 601 times until stuck shelter times out to EXPLORE
	for i := int64(0); i <= 601; i++ {
		ctx.Frame.WorldTick = 1000 + i
		fsm.Tick(ctx)
	}

	if fsm.CurrentState() != StateExplore {
		t.Fatalf("expected state %s after shelter timeout, got %s", StateExplore, fsm.CurrentState())
	}

	// Next tick (tick 1602): Still night, but shelter is on cooldown -> must STAY in EXPLORE
	ctx.Frame.WorldTick = 1602
	res := fsm.Tick(ctx)
	if res.State != StateExplore {
		t.Fatalf("VIOLATION: FSM immediately re-entered %s despite timeout cooldown at tick 1602", res.State)
	}
}

func TestFSM_CriticalHungerBlocksRestGuard(t *testing.T) {
	fsm := NewAgentFSM(StateEmergency, nil)
	state := NewAgentState()

	// Critical hunger (foodLevel 4) with food in inventory
	ctx := &FSMContext{
		AgentID: "hungry_rest_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 18000, // Midnight
			Status: &civquestv1.AgentStatus{
				AgentId:   "hungry_rest_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 4,
				Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:cooked_beef", Count: 5},
				},
			},
		},
	}

	// RestStateHandler.Guard must reject entry when critically hungry
	handler := &RestStateHandler{}
	if handler.Guard(ctx) {
		t.Fatalf("VIOLATION: RestStateHandler.Guard returned true during critical hunger")
	}

	// Attempt forced transition to REST - must be rejected
	allowed := fsm.TransitionTo(StateRest, "force rest during critical hunger", ctx)
	if allowed {
		t.Fatalf("VIOLATION: TransitionTo(StateRest) succeeded during critical hunger")
	}
	if fsm.CurrentState() != StateEmergency {
		t.Fatalf("expected state to remain %s, got %s", StateEmergency, fsm.CurrentState())
	}
}

func TestFSM_ConcurrentSafety(t *testing.T) {
	fsm := NewAgentFSM(StateBootstrap, nil)
	state := NewAgentState()
	cfg := &xdg.AgentIdentityConfig{}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for tick := int64(0); tick < 100; tick++ {
				ctx := &FSMContext{
					AgentID: "concurrent_agent",
					State:   state,
					Config:  cfg,
					Frame: &civquestv1.TelemetryFrame{
						WorldTick: tick,
						TimeOfDay: (tick * 250) % 24000,
						Status: &civquestv1.AgentStatus{
							AgentId:   "concurrent_agent",
							Health:    20.0,
							MaxHealth: 20.0,
							FoodLevel: 20,
							Position:  &civquestv1.Vec3{X: float64(id), Y: 64, Z: float64(tick)},
						},
					},
				}
				_ = fsm.Tick(ctx)
				_ = fsm.CurrentState()
				_ = fsm.PreviousState()
			}
		}(i)
	}
	wg.Wait()
}

func TestFSM_RestDoesNotTransitionToShelterWhenComplete(t *testing.T) {
	fsm := NewAgentFSM(StateRest, nil)
	state := NewAgentState()
	state.SetShelterBlocksPlaced(blueprints.CabinTotalBlocks)

	ctx := &FSMContext{
		AgentID: "rest_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 16000, // Midnight
			Status: &civquestv1.AgentStatus{
				AgentId:    "rest_agent",
				Health:     20.0,
				MaxHealth:  20.0,
				FoodLevel:  20,
				Position:   &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				IsSleeping: false, // Not sleeping, resting inside completed shelter
			},
		},
	}

	for tick := int64(100); tick < 130; tick++ {
		ctx.Frame.WorldTick = tick
		res := fsm.Tick(ctx)
		if res.State != StateRest {
			t.Fatalf("VIOLATION: FSM transitioned from %s to %s at tick %d despite shelter being complete",
				StateRest, res.State, tick)
		}
	}

	if fsm.CurrentState() != StateRest {
		t.Fatalf("expected state to remain %s, got %s", StateRest, fsm.CurrentState())
	}
}

func TestFSM_BootstrapStaysInBootstrapUntilKitCollected(t *testing.T) {
	fsm := NewAgentFSM(StateBootstrap, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "bootstrap_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 3000, // Morning
			Status: &civquestv1.AgentStatus{
				AgentId:   "bootstrap_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Inventory: []*civquestv1.ItemStackProto{}, // Empty inventory - no survival kit
			},
		},
	}

	// Tick 1500 times (> 1200 ticks): must NOT prematurely evict to Explore
	for tick := int64(100); tick <= 1600; tick += 50 {
		ctx.Frame.WorldTick = tick
		res := fsm.Tick(ctx)
		if res.State != StateBootstrap {
			t.Fatalf("VIOLATION: FSM prematurely evicted from %s to %s at tick %d before kit collected",
				StateBootstrap, res.State, tick)
		}
	}

	// Collect survival kit (axe, sword, torch)
	ctx.Frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wooden_axe", Count: 1},
		{ItemId: "minecraft:wooden_sword", Count: 1},
		{ItemId: "minecraft:torch", Count: 4},
	}
	ctx.Frame.WorldTick = 1601
	res := fsm.Tick(ctx)
	if res.State != StateExplore {
		t.Fatalf("expected transition to %s once kit collected, got %s", StateExplore, res.State)
	}
}

func TestFSM_SettlementTransitionsToColonizeWhenEstablishingNewSettlement(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "founder_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 4000, // Daylight
			Status: &civquestv1.AgentStatus{
				AgentId:   "founder_agent",
				Health:    20.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 150, Y: 64, Z: 150},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:iron_axe", Count: 1},
					{ItemId: "minecraft:iron_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 16},
				},
			},
		},
	}

	// Initially in Settlement living
	res0 := fsm.Tick(ctx)
	if res0.Stage != StageSettlement {
		t.Fatalf("expected stage %s, got %s", StageSettlement, res0.Stage)
	}

	// Arrived at new settlement site -> Flag founding of new settlement node!
	state.SetEstablishingNewSettlement(true)
	ctx.Frame.WorldTick = 501
	res1 := fsm.Tick(ctx)
	if res1.Stage != StageColonize {
		t.Fatalf("expected transition from %s to %s when establishing new settlement, got %s",
			StageSettlement, StageColonize, res1.Stage)
	}
	if !res1.Transitioned {
		t.Fatalf("expected Transitioned=true on settlement founding")
	}
}
