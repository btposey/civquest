package system1

import (
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
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

func TestFSM_FactGraphPredicatesInTransitionRules(t *testing.T) {
	rules := defaultTransitionRules()

	// 1. Emergency Preemption evaluates threat.is_emergency
	facts := NewFactGraph()
	ctx := &FSMContext{Facts: facts}

	// Not emergency initially
	facts.SetBool("threat.is_emergency", "threat", false, "calm")
	for _, r := range rules {
		if r.ToStage == StageEmergency && r.FromStage == StageBootstrap {
			if r.Condition(ctx) {
				t.Errorf("expected emergency preemption condition to be false when threat.is_emergency=false")
			}
		}
	}

	// Threat emergency becomes true
	facts.SetBool("threat.is_emergency", "threat", true, "hostiles present")
	for _, r := range rules {
		if r.ToStage == StageEmergency && r.FromStage == StageBootstrap {
			if !r.Condition(ctx) {
				t.Errorf("expected emergency preemption condition to be true when threat.is_emergency=true")
			}
		}
	}

	// 2. Out of Emergency: threats_cleared + bootstrap.has_kit
	facts.SetBool("threat.is_emergency", "threat", false, "cleared")
	facts.SetBool("threat.threats_cleared", "threat", true, "cleared")
	facts.SetBool("bootstrap.has_kit", "bootstrap", true, "kit ready")
	facts.SetBool("colonize.is_establishing", "colonize", false, "settled")

	for _, r := range rules {
		if r.FromStage == StageEmergency && r.ToStage == StageSettlement {
			if !r.Condition(ctx) {
				t.Errorf("expected Emergency -> Settlement transition when threats_cleared and has_kit")
			}
		}
		if r.FromStage == StageEmergency && r.ToStage == StageBootstrap {
			if r.Condition(ctx) {
				t.Errorf("expected Emergency -> Bootstrap to be false when has_kit=true")
			}
		}
	}

	// 3. Out of Emergency: threats_cleared + colonize.is_establishing
	facts.SetBool("colonize.is_establishing", "colonize", true, "founding new settlement")
	for _, r := range rules {
		if r.FromStage == StageEmergency && r.ToStage == StageColonize {
			if !r.Condition(ctx) {
				t.Errorf("expected Emergency -> Colonize when threats_cleared and colonize.is_establishing=true")
			}
		}
	}

	// 4. Circadian Day/Night and Sleeping
	facts.SetBool("circadian.is_dusk_or_night", "circadian", false, "daytime")
	facts.SetBool("vitals.is_sleeping", "vitals", false, "awake")
	facts.SetBool("bootstrap.has_kit", "bootstrap", true, "has kit")

	for _, r := range rules {
		if r.FromStage == StateRest && r.ToStage == StageSettlement {
			if !r.Condition(ctx) {
				t.Errorf("expected Rest -> Settlement on day awakening")
			}
		}
	}

	// Sleeping blocks awakening
	facts.SetBool("vitals.is_sleeping", "vitals", true, "asleep")
	for _, r := range rules {
		if r.FromStage == StateRest && r.ToStage == StageSettlement {
			if r.Condition(ctx) {
				t.Errorf("expected Rest -> Settlement to be false while sleeping")
			}
		}
	}
}

func TestFSM_TreeHarvestPDAPushEmergencyPreemptAndResume(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("pda_logger")
	cfg.Profession = "EXPLORER"

	ctx := &FSMContext{
		AgentID: "pda_logger",
		State:   state,
		Config:  cfg,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 3000, // Work hours
			Status: &civquestv1.AgentStatus{
				AgentId:         "pda_logger",
				Health:          20.0,
				MaxHealth:       20.0,
				FoodLevel:       20,
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
				{Pos: &civquestv1.BlockPosProto{X: 3, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
				{Pos: &civquestv1.BlockPosProto{X: 3, Y: 66, Z: 0}, BlockId: "minecraft:oak_log"},
			},
		},
	}

	// Tick 1: Agent selects tree (3, 0), enters HARVEST_TREE subroutine, pushes parent onto PDA stack, and breaks Y=64
	res0 := fsm.Tick(ctx)
	if res0.Stage != StageBootstrap {
		t.Fatalf("expected stage %s, got %s", StageBootstrap, res0.Stage)
	}
	if res0.Routine != RoutineHarvestTree {
		t.Fatalf("expected Routine=%s when tree harvest commits, got %s", RoutineHarvestTree, res0.Routine)
	}
	if fsm.StackDepth() != 1 {
		t.Fatalf("expected PDA StackDepth=1 while HarvestTree subroutine active, got %d", fsm.StackDepth())
	}
	if locked, tx, tz, _ := state.GetTargetTree(); !locked || tx != 3 || tz != 0 {
		t.Fatalf("expected locked trunk (3,0), got locked=%v (%d,%d)", locked, tx, tz)
	}

	foundBreak := false
	for _, it := range res0.Intents {
		if it.Intent != nil && it.Intent.GetInteractBlock() != nil &&
			it.Intent.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK &&
			it.Intent.GetInteractBlock().Pos.X == 3 && it.Intent.GetInteractBlock().Pos.Y == 64 {
			foundBreak = true
		}
	}
	if !foundBreak {
		t.Fatalf("expected HARVEST_BREAK on (3,64,0), got intents %+v", res0.Intents)
	}

	// Tick 2 (WorldTick 105, mid-cooldown while (3,64,0) is still breaking): must stay committed without wandering
	ctx.Frame.WorldTick = 105
	res1 := fsm.Tick(ctx)
	if res1.Routine != RoutineHarvestTree || fsm.StackDepth() != 1 {
		t.Fatalf("expected Routine=%s and StackDepth=1 during harvest cooldown, got routine=%s depth=%d",
			RoutineHarvestTree, res1.Routine, fsm.StackDepth())
	}
	if len(res1.Intents) != 0 {
		t.Fatalf("expected no wander/explore intents during harvest cooldown, got %+v", res1.Intents)
	}

	// Tick 3: Y=64 broken, 1 oak_log in inventory + closer distracting tree (1, 64, 1); must stay on (3, 0)
	ctx.Frame.WorldTick = 126
	ctx.Frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
	}
	ctx.Frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 66, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}, BlockId: "minecraft:birch_log"},
	}
	res2 := fsm.Tick(ctx)
	if res2.Routine != RoutineHarvestTree || fsm.StackDepth() != 1 {
		t.Fatalf("expected continued RoutineHarvestTree and StackDepth=1, got routine=%s depth=%d", res2.Routine, fsm.StackDepth())
	}
	foundBreak65 := false
	for _, it := range res2.Intents {
		if it.Intent != nil && it.Intent.GetInteractBlock() != nil &&
			it.Intent.GetInteractBlock().Pos.X == 3 && it.Intent.GetInteractBlock().Pos.Y == 65 {
			foundBreak65 = true
		}
	}
	if !foundBreak65 {
		t.Fatalf("expected continued HARVEST_BREAK on locked trunk (3,65,0), got %+v", res2.Intents)
	}

	// Tick 4: EMERGENCY preempts! Creeper spawns at 2.5m AND health drops <= 35%
	ctx.Frame.WorldTick = 135
	ctx.Frame.Status.Health = 6.0 // 30% HP
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   901,
			EntityType: "minecraft:creeper",
			IsHostile:  true,
			Distance:   2.5,
			Position:   &civquestv1.Vec3{X: 2.5, Y: 64, Z: 0.5},
		},
	}
	resEmg := fsm.Tick(ctx)
	if resEmg.Stage != StageEmergency || resEmg.Routine != RoutineEmergency {
		t.Fatalf("expected (StageEmergency, RoutineEmergency) on threat, got (%s, %s)", resEmg.Stage, resEmg.Routine)
	}
	if fsm.StackDepth() != 2 {
		t.Fatalf("expected PDA StackDepth=2 (parent routine + interrupted HarvestTree), got %d", fsm.StackDepth())
	}

	// Remain in Emergency for 200 ticks (>150-tick progress timeout!) to prove emergency duration doesn't cause false timeout
	ctx.Frame.WorldTick = 335
	resEmgHold := fsm.Tick(ctx)
	if resEmgHold.Stage != StageEmergency {
		t.Fatalf("expected to stay in StageEmergency while threat active, got %s", resEmgHold.Stage)
	}

	// Tick 6: Threat clears and health recovers -> must pop back to RoutineHarvestTree on (3, 0)!
	ctx.Frame.WorldTick = 350
	ctx.Frame.Status.Health = 20.0
	ctx.Frame.VisibleEntities = nil
	resResume := fsm.Tick(ctx)
	if resResume.Stage != StageBootstrap || resResume.Routine != RoutineHarvestTree {
		t.Fatalf("expected restoration to (StageBootstrap, RoutineHarvestTree), got (%s, %s)", resResume.Stage, resResume.Routine)
	}
	if fsm.StackDepth() != 1 {
		t.Fatalf("expected PDA StackDepth=1 after popping emergency back to HarvestTree, got %d", fsm.StackDepth())
	}
	if locked, tx, tz, _ := state.GetTargetTree(); !locked || tx != 3 || tz != 0 {
		t.Fatalf("expected resumed tree lock on (3,0), got locked=%v (%d,%d)", locked, tx, tz)
	}
	foundResumedBreak := false
	for _, it := range resResume.Intents {
		if it.Intent != nil && it.Intent.GetInteractBlock() != nil &&
			it.Intent.GetInteractBlock().Pos.X == 3 && it.Intent.GetInteractBlock().Pos.Y == 65 {
			foundResumedBreak = true
		}
	}
	if !foundResumedBreak {
		t.Fatalf("expected resumed HARVEST_BREAK on (3,65,0) immediately after emergency cleared, got %+v", resResume.Intents)
	}

	// Tick 7: Remaining logs in (3, 0) column cleared, no dropped items -> pops PDA parent frame!
	ctx.Frame.WorldTick = 375
	ctx.Frame.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_log", Count: 3},
	}
	ctx.Frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}, BlockId: "minecraft:birch_log"},
	}
	resDone := fsm.Tick(ctx)
	if fsm.StackDepth() != 0 {
		t.Fatalf("expected PDA StackDepth=0 once HarvestTree completed, got %d", fsm.StackDepth())
	}
	if resDone.Routine != RoutinePrimaryDuty {
		t.Fatalf("expected restored Routine=%s after task pop, got %s", RoutinePrimaryDuty, resDone.Routine)
	}
	if state.HasActiveTask() {
		t.Fatalf("expected no active task after completion")
	}
}

func TestFSM_NonPreemptibleTaskClearedOnEmergency(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()

	// Install non-preemptible task
	state.StartTask(NewTaskFSM(TaskDescriptor{
		ID:          "non_preemptible_op",
		Kind:        "non_preemptible",
		Stage:       StageSettlement,
		Routine:     RoutineSecondaryHobby,
		Preemptible: false,
		IsComplete: func(s *AgentState, f *civquestv1.TelemetryFrame) bool {
			return false
		},
	}, 100))

	ctx := &FSMContext{
		AgentID: "non_preempt_agent",
		State:   state,
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 3000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "non_preempt_agent",
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

	res0 := fsm.Tick(ctx)
	if res0.Routine != RoutineSecondaryHobby || fsm.StackDepth() != 1 {
		t.Fatalf("expected active non-preemptible task routine=%s stack=1, got routine=%s stack=%d",
			RoutineSecondaryHobby, res0.Routine, fsm.StackDepth())
	}

	// Emergency hits -> non-preemptible task must be cleared so it does not resume
	ctx.Frame.WorldTick = 105
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 99, EntityType: "minecraft:zombie", IsHostile: true, Distance: 2.0, Position: &civquestv1.Vec3{X: 2, Y: 64, Z: 0}},
	}
	resEmg := fsm.Tick(ctx)
	if resEmg.Stage != StageEmergency {
		t.Fatalf("expected StageEmergency, got %s", resEmg.Stage)
	}
	if state.HasActiveTask() {
		t.Fatalf("expected non-preemptible task cleared when emergency preempted")
	}

	// Threats clear -> emergency pops, remaining parent task frame unwinds cleanly to stack depth 0
	ctx.Frame.WorldTick = 115
	ctx.Frame.VisibleEntities = nil
	resClear := fsm.Tick(ctx)
	if resClear.Stage != StageSettlement || fsm.StackDepth() != 0 {
		t.Fatalf("expected clean unwind to StageSettlement stack=0, got stage=%s stack=%d", resClear.Stage, fsm.StackDepth())
	}
}

func TestFSM_MacroRoutineTransitionsEmitFlushPeriphery(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	state := NewAgentState()

	makeCtx := func(tick, tod int64) *FSMContext {
		return &FSMContext{
			AgentID: "routine_agent",
			State:   state,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: tick,
				TimeOfDay: tod,
				Status: &civquestv1.AgentStatus{
					AgentId:   "routine_agent",
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
	}

	// Tick 1: DawnPrep -> PrimaryDuty (transition emits FlushPeriphery: true)
	res1 := fsm.Tick(makeCtx(100, 2000))
	if res1.Routine != RoutinePrimaryDuty || !res1.RoutineTransitioned || !res1.FlushPeriphery {
		t.Fatalf("expected PrimaryDuty transition with FlushPeriphery=true, got routine=%s trans=%v flush=%v",
			res1.Routine, res1.RoutineTransitioned, res1.FlushPeriphery)
	}

	// Tick 2: Steady state in PrimaryDuty -> FlushPeriphery: false
	resSteady := fsm.Tick(makeCtx(120, 2500))
	if resSteady.Routine != RoutinePrimaryDuty || resSteady.RoutineTransitioned || resSteady.FlushPeriphery {
		t.Fatalf("expected steady PrimaryDuty with FlushPeriphery=false, got routine=%s trans=%v flush=%v",
			resSteady.Routine, resSteady.RoutineTransitioned, resSteady.FlushPeriphery)
	}

	// Tick 3: PrimaryDuty -> TavernSocial (ToD 11500) -> FlushPeriphery: true
	resTavern := fsm.Tick(makeCtx(200, 11500))
	if resTavern.Routine != RoutineTavernSocial || !resTavern.RoutineTransitioned || !resTavern.FlushPeriphery {
		t.Fatalf("expected PrimaryDuty -> TavernSocial with FlushPeriphery=true, got routine=%s trans=%v flush=%v",
			resTavern.Routine, resTavern.RoutineTransitioned, resTavern.FlushPeriphery)
	}

	// Tick 4: TavernSocial -> Sleep (ToD 14000) -> FlushPeriphery: true
	resSleep := fsm.Tick(makeCtx(300, 14000))
	if resSleep.Routine != RoutineSleep || !resSleep.RoutineTransitioned || !resSleep.FlushPeriphery {
		t.Fatalf("expected TavernSocial -> Sleep with FlushPeriphery=true, got routine=%s trans=%v flush=%v",
			resSleep.Routine, resSleep.RoutineTransitioned, resSleep.FlushPeriphery)
	}
}

func TestFSM_PreviousStageInitializedAndNoEmptyStageTransition(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	if fsm.PreviousStage() != StageBootstrap {
		t.Fatalf("expected initial PreviousStage=%s, got %q", StageBootstrap, fsm.PreviousStage())
	}
	if fsm.PreviousRoutine() != RoutineDawnPrep {
		t.Fatalf("expected initial PreviousRoutine=%s, got %q", RoutineDawnPrep, fsm.PreviousRoutine())
	}

	state := NewAgentState()
	ctx := &FSMContext{
		AgentID: "stage_check_agent",
		State:   state,
		Config:  &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst},
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 100,
			TimeOfDay: 2000, // RoutinePrimaryDuty (routine transitions from DAWN_PREP)
			Status: &civquestv1.AgentStatus{
				AgentId:   "stage_check_agent",
				Health:    20,
				MaxHealth: 20,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			},
		},
	}

	res := fsm.Tick(ctx)
	if !res.RoutineTransitioned || !res.FlushPeriphery {
		t.Fatalf("expected RoutineTransitioned=true and FlushPeriphery=true, got %+v", res)
	}
	if res.StageTransitioned || res.Transitioned {
		t.Fatalf("expected StageTransitioned=false and Transitioned=false on routine-only transition, got stage=%v trans=%v",
			res.StageTransitioned, res.Transitioned)
	}
	if res.PreviousStage != StageBootstrap || res.PreviousState != StageBootstrap {
		t.Fatalf("expected non-empty PreviousStage=%s, got stage=%q state=%q",
			StageBootstrap, res.PreviousStage, res.PreviousState)
	}
}

func TestFSM_StarvationLowHPZeroHostilesDoesNotLockInEmergency(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	state := NewAgentState()

	ctx := &FSMContext{
		AgentID: "carl",
		State:   state,
		Config:  &xdg.AgentIdentityConfig{Shift: xdg.ShiftFirst, Traits: xdg.AgentTraits{Bravery: 0.5}},
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 3000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "carl",
				Health:          5.0, // <= 35% HP from starvation
				MaxHealth:       20.0,
				FoodLevel:       2, // critical hunger, 0 food in inventory
				Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
			LastDamageTaken: &civquestv1.DamageEvent{
				DamageAmount:     1.0,
				SourceType:       civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED,
				AttackerEntityId: 0,
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:sweet_berry_bush"},
			},
		},
	}

	res := fsm.Tick(ctx)
	if res.Stage == StageEmergency {
		t.Fatalf("expected starving agent with 0 hostiles NOT to lock in StageEmergency, got %s", res.Stage)
	}
	if ctx.ShouldPanic() {
		t.Fatalf("expected ctx.ShouldPanic()=false when 0 hostiles")
	}
	for _, it := range res.Intents {
		if att := it.Intent.GetAttack(); att != nil && att.FleeImmediately {
			t.Fatalf("expected no combat-flee AttackIntent during starvation with 0 hostiles, got %+v", att)
		}
	}
	if len(res.Intents) == 0 || res.Intents[0].Intent.GetInteractBlock() == nil {
		t.Fatalf("expected foraging InteractBlock intent on nearby berry bush, got %+v", res.Intents)
	}
	if res.GoalHierarchy == nil || res.GoalHierarchy.ImmediateGoal != ForagingGoalText {
		t.Fatalf("expected ImmediateGoal=%q, got %+v", ForagingGoalText, res.GoalHierarchy)
	}
}

func TestFSM_BootstrapNightWithoutShelterStaysActiveAndExploresOrQueriesSpatialIndex(t *testing.T) {
	t.Run("Night tod=16000 with 0 logs and no POIs falls through to exploration instead of sleeping or tethering", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		// Warm up ticks at night with 0 logs, 0 shelter, 0 bed: first triggers SurveyHorizonIntent, then after report explores outward
		var surveyIntent *civquestv1.AgentIntent
		var exploreIntent *civquestv1.AgentIntent
		for i := int64(0); i < 4; i++ {
			tick := 100 + i*70
			ctx := &FSMContext{
				AgentID: "earl",
				State:   state,
				Config:  cfg,
				Frame: &civquestv1.TelemetryFrame{
					WorldTick: tick,
					TimeOfDay: 16000, // Deep night
					Status: &civquestv1.AgentStatus{
						AgentId:         "earl",
						Health:          20,
						MaxHealth:       20,
						FoodLevel:       20,
						Position:        &civquestv1.Vec3{X: 25, Y: 64, Z: 0},
						NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					},
				},
			}
			res := fsm.Tick(ctx)
			if res.Routine == RoutineSleep || res.Routine == RoutineTavernSocial {
				t.Fatalf("BOOTSTRAP without shelter must NOT enter %s at night, got %s", res.Routine, res.Routine)
			}
			if res.Facts != nil && res.Facts.GetString("circadian.routine") == string(RoutineSleep) {
				t.Fatalf("CircadianEvaluator must NOT report SLEEP in BOOTSTRAP without shelter")
			}
			for _, it := range res.Intents {
				if it.Intent != nil && it.Intent.GetSurveyHorizon() != nil {
					surveyIntent = it.Intent
					// Feed completed survey back so next tick picks a bearing and navigates outward
					sectors := make([]*civquestv1.HorizonSector, 36)
					for s := 0; s < 36; s++ {
						sectors[s] = &civquestv1.HorizonSector{
							SectorIndex: int32(s),
							BearingDeg:  float32(s * 10),
							HitDistance: 48.0,
							FeatureType: "FOREST_CANOPY",
							HitBlockId:  "minecraft:oak_leaves",
						}
					}
					state.IngestHorizonSurveyReport(&civquestv1.HorizonSurveyReport{
						OriginX: 25, OriginY: 64, OriginZ: 0, CompletedTick: tick + 10, MaxRange: 64.0, Sectors: sectors,
					}, tick+10)
				}
				if it.Intent != nil && it.Intent.GetNavigate() != nil {
					exploreIntent = it.Intent
				}
			}
		}
		if surveyIntent == nil {
			t.Fatalf("expected BOOTSTRAP agent at night with 0 logs to emit SurveyHorizonIntent")
		}
		if exploreIntent == nil || exploreIntent.GetNavigate() == nil || exploreIntent.GetNavigate().Target == nil {
			t.Fatalf("expected BOOTSTRAP agent at night with 0 logs to emit exploration NavigateIntent after survey, got nil")
		}
		target := exploreIntent.GetNavigate().Target
		if target.X == 0 && target.Z == 0 {
			t.Fatalf("expected exploration outward rather than tethering back to homeOrigin (0, 64, 0), got %+v", target)
		}
	})

	t.Run("Dusk tod=12000 without shelter suppresses RoutineTavernSocial and stays task-driven", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		ctx := &FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 200,
				TimeOfDay: 12000,
				Status: &civquestv1.AgentStatus{
					AgentId:         "earl",
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
		}
		res := fsm.Tick(ctx)
		if res.Routine != RoutinePrimaryDuty {
			t.Fatalf("expected RoutinePrimaryDuty in BOOTSTRAP dusk without shelter, got %s", res.Routine)
		}
		if res.Facts.GetString("circadian.routine") != string(RoutinePrimaryDuty) {
			t.Fatalf("expected FactGraph circadian.routine=%s, got %s", RoutinePrimaryDuty, res.Facts.GetString("circadian.routine"))
		}
	})

	t.Run("Night tod=16000 with 0 logs queries SpatialIndex for remembered tree POI", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		state.SpatialIndex().UpsertPOI(db.AgentPOI{
			POIID:          FormatPOIID(POICategoryResource, 45, 64, 15),
			Category:       POICategoryResource,
			BlockID:        "minecraft:oak_log",
			X:              45.5,
			Y:              64.0,
			Z:              15.5,
			AccessX:        44.5,
			AccessY:        64.0,
			AccessZ:        15.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		ctx := &FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 300,
				TimeOfDay: 16000,
				Status: &civquestv1.AgentStatus{
					AgentId:         "earl",
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 20, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 || res.Intents[0].Intent.GetNavigate() == nil {
			t.Fatalf("expected NavigateIntent to SpatialIndex log POI at night, got %+v", res.Intents)
		}
		target := res.Intents[0].Intent.GetNavigate().Target
		if target.X != 44.5 || target.Z != 15.5 {
			t.Fatalf("expected target (44.5, 64, 15.5), got %+v", target)
		}
	})

	t.Run("Night tod=16000 without shelter executes active System 2 directive when no shelter action available", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetStrategicDirective(DirectiveActionScoutFrontier, "northern woods", "SCOUT_FRONTIER: Find timber", 100, 6100)
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		ctx := &FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 300,
				TimeOfDay: 16000,
				Status: &civquestv1.AgentStatus{
					AgentId:         "earl",
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 || (res.Intents[0].Intent.GetSurveyHorizon() == nil && res.Intents[0].Intent.GetNavigate() == nil) {
			t.Fatalf("expected System 2 SCOUT_FRONTIER SurveyHorizonIntent or NavigateIntent at night in BOOTSTRAP without shelter, got %+v", res.Intents)
		}
	})
}

func TestFSM_BootstrapNightWithLogsBuildsShelterAndGatesCircadianOnceComplete(t *testing.T) {
	t.Run("Night tod=16000 with logs still crafts planks and builds shelter", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetRecipeGraph(newTestRecipeGraph())
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		ctx := &FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 200,
				TimeOfDay: 16000,
				Status: &civquestv1.AgentStatus{
					AgentId:         "earl",
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{Slot: 0, ItemId: "minecraft:oak_log", Count: 4},
					},
				},
			},
		}

		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 || res.Intents[0].Intent.GetCraftItem() == nil ||
			res.Intents[0].Intent.GetCraftItem().RecipeId != "minecraft:oak_planks" {
			t.Fatalf("expected CraftItemIntent(minecraft:oak_planks) at night with logs, got %+v", res.Intents)
		}
	})

	t.Run("IsShelterComplete unlocks normal circadian TavernSocial and Sleep routines in BOOTSTRAP", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetShelterBlocksPlaced(blueprints.CabinTotalBlocks)
		state.ShelterBuildIndex = blueprints.CabinTotalBlocks
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		// Dusk -> RoutineTavernSocial
		resDusk := fsm.Tick(&FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 200,
				TimeOfDay: 12000,
				Status: &civquestv1.AgentStatus{
					AgentId:   "earl",
					Health:    20,
					MaxHealth: 20,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				},
			},
		})
		if resDusk.Routine != RoutineTavernSocial {
			t.Fatalf("expected RoutineTavernSocial at dusk once shelter complete, got %s", resDusk.Routine)
		}

		// Night -> RoutineSleep (and does not wander out to explore)
		resNight := fsm.Tick(&FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 300,
				TimeOfDay: 16000,
				Status: &civquestv1.AgentStatus{
					AgentId:   "earl",
					Health:    20,
					MaxHealth: 20,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				},
			},
		})
		if resNight.Routine != RoutineSleep {
			t.Fatalf("expected RoutineSleep at night once shelter complete, got %s", resNight.Routine)
		}
		if len(resNight.Intents) != 0 {
			t.Fatalf("expected resting inside completed shelter at night (no exploration/gather intents), got %+v", resNight.Intents)
		}
	})

	t.Run("HasAssignedBed unlocks normal circadian Sleep and emits SLEEP_IN_BED in BOOTSTRAP", func(t *testing.T) {
		fsm := NewAgentFSM(StageBootstrap, nil)
		state := NewAgentState()
		state.SetAssignedBed(&civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0})
		cfg := xdg.DefaultAgentIdentityConfig("earl")

		resNight := fsm.Tick(&FSMContext{
			AgentID: "earl",
			State:   state,
			Config:  cfg,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 300,
				TimeOfDay: 16000,
				Status: &civquestv1.AgentStatus{
					AgentId:         "earl",
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
		})
		if resNight.Routine != RoutineSleep {
			t.Fatalf("expected RoutineSleep at night with assigned bed, got %s", resNight.Routine)
		}
		if len(resNight.Intents) == 0 || resNight.Intents[0].Intent.GetInteractBlock() == nil ||
			resNight.Intents[0].Intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED intent at night with assigned bed, got %+v", resNight.Intents)
		}
	})
}
