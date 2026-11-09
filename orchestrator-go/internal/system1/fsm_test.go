package system1

import (
	"sync"
	"testing"

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
	fsm := NewAgentFSM(StateBootstrap, func(from, to State, reason string) {
		transitions = append(transitions, string(from)+"->"+string(to))
	})

	state := NewAgentState()

	// 1. In Bootstrap, with survival kit (axe, sword, torch) in daylight -> transitions to EXPLORE
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
	if res.State != StateExplore {
		t.Fatalf("expected state %s after acquiring survival kit, got %s", StateExplore, res.State)
	}

	// 2. Hostile mob appears -> transitions to EMERGENCY
	ctx.Frame.WorldTick = 105
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   101,
			EntityType: "minecraft:zombie",
			IsHostile:  true,
			Distance:   3.0,
			Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 3},
		},
	}

	res = fsm.Tick(ctx)
	if res.State != StateEmergency {
		t.Fatalf("expected state %s when hostile appears, got %s", StateEmergency, res.State)
	}

	// 3. Hostile disappears, day time -> transitions back to EXPLORE
	ctx.Frame.WorldTick = 110
	ctx.Frame.VisibleEntities = nil

	res = fsm.Tick(ctx)
	if res.State != StateExplore {
		t.Fatalf("expected state %s when hostile cleared, got %s", StateExplore, res.State)
	}

	// 4. Dusk falls (time_of_day = 12500) and no bed -> transitions to SHELTER
	ctx.Frame.WorldTick = 120
	ctx.Frame.TimeOfDay = 12500

	res = fsm.Tick(ctx)
	if res.State != StateShelter {
		t.Fatalf("expected state %s at dusk without bed, got %s", StateShelter, res.State)
	}

	// 5. Shelter complete at night -> transitions to REST
	ctx.Frame.WorldTick = 130
	ctx.Frame.TimeOfDay = 14000
	state.SetShelterBlocksPlaced(len(shelterWallOffsets))

	res = fsm.Tick(ctx)
	if res.State != StateRest {
		t.Fatalf("expected state %s at night when shelter complete, got %s", StateRest, res.State)
	}

	// 6. Dawn arrives (time_of_day = 23600), agent not sleeping -> transitions to EXPLORE
	ctx.Frame.WorldTick = 140
	ctx.Frame.TimeOfDay = 23600

	res = fsm.Tick(ctx)
	if res.State != StateExplore {
		t.Fatalf("expected state %s at dawn, got %s", StateExplore, res.State)
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
