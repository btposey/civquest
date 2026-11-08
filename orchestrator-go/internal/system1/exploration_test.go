package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestResolveAndFormatDayPhase(t *testing.T) {
	cases := []struct {
		tod      int64
		wantEnum DayPhase
	}{
		{0, DayPhaseDawn},
		{23800, DayPhaseDawn},
		{1500, DayPhaseMorning},
		{6000, DayPhaseMidday},
		{9000, DayPhaseAfternoon},
		{12000, DayPhaseDusk},
		{15000, DayPhaseNight},
	}

	for _, tc := range cases {
		got := ResolveDayPhase(tc.tod)
		if got != tc.wantEnum {
			t.Errorf("ResolveDayPhase(%d) = %s, want %s", tc.tod, got, tc.wantEnum)
		}
		if FormatDayPhase(tc.tod) == "" {
			t.Errorf("FormatDayPhase(%d) returned empty string", tc.tod)
		}
	}
}

func TestEvaluateTopologicalMappingAndExploration(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	node1, edge1 := EvaluateTopologicalMapping(frame1, state)
	if node1 == nil || node1.Category != "SETTLEMENT" {
		t.Fatalf("expected initial SETTLEMENT node, got %+v", node1)
	}
	if edge1 != nil {
		t.Fatalf("expected nil edge on first node, got %+v", edge1)
	}

	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1120,
		TimeOfDay: 2120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 16, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	node2, edge2 := EvaluateTopologicalMapping(frame2, state)
	if node2 == nil || edge2 == nil {
		t.Fatalf("expected second node and connecting edge, got node=%+v edge=%+v", node2, edge2)
	}

	// Test terrain unsticking when NAVIGATION_STATE_STUCK
	_ = EvaluateExploration("Able", frame1, cfg, state)
	stuckFrame := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 2200,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 16, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
	}
	detourIntent := EvaluateExploration("Able", stuckFrame, cfg, state)
	if detourIntent == nil || detourIntent.GetNavigate() == nil {
		t.Fatalf("expected terrain detour navigate intent when STUCK, got %+v", detourIntent)
	}
}

func TestEvaluateTradeQuidProQuo(t *testing.T) {
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			FoodLevel: 12,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 1, ItemId: "minecraft:oak_log", Count: 8},
			},
		},
	}
	req := &civquestv1.TradeRequest{
		PlayerEntityId: 7,
		PlayerUuid:     "player-1",
		PlayerName:     "Brian",
		SpokenProposal: "[SYSTEM1_TRADE] iron sword",
		PlayerOfferedItems: []*civquestv1.ItemStackProto{
			{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		},
	}

	dec := EvaluateTradeQuidProQuo(req, frame, "EXPLORER", 10.0, nil)
	if !dec.AcceptDeal || dec.Decision != "ACCEPT" {
		t.Fatalf("expected unarmed explorer to accept iron_sword offer, got %+v", dec)
	}
	if dec.TradeIntent == nil || !dec.TradeIntent.AcceptDeal {
		t.Fatalf("expected RespondTradeIntent with AcceptDeal=true, got %+v", dec.TradeIntent)
	}
	if len(dec.CounterOffer) == 0 {
		t.Fatalf("expected counter-offer from surplus oak_log, got empty counter offer")
	}
}

func TestEvaluateBootstrapTimberAndCrafting(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Frame 1: warm up telemetryFramesSeen
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	_ = EvaluateExploration("Able", frame1, cfg, state)

	// Frame 2: Unarmed agent sees an oak_log within 3 blocks -> should harvest it!
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 140,
		TimeOfDay: 2040,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0},
				BlockId: "minecraft:oak_log",
			},
		},
	}
	harvestIntent := EvaluateExploration("Able", frame2, cfg, state)
	if harvestIntent == nil || harvestIntent.GetInteractBlock() == nil ||
		harvestIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK intent for nearby oak_log, got %+v", harvestIntent)
	}

	// Frame 3: Agent now has 2 oak_logs in inventory -> should craft wooden_axe!
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick: 180,
		TimeOfDay: 2080,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
			},
		},
	}
	craftIntent := EvaluateExploration("Able", frame3, cfg, state)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:wooden_axe" {
		t.Fatalf("expected CraftItemIntent for minecraft:wooden_axe, got %+v", craftIntent)
	}
}

func TestExplorerForwardOutpostAtDuskAndGoals(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Day 1 Morning at (0, 64, 0)
	frameMorning := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	_, _ = EvaluateTopologicalMapping(frameMorning, state)
	_ = EvaluateExploration("Able", frameMorning, cfg, state)

	goalsMorning := EvaluateGoals(frameMorning, cfg, state)
	if goalsMorning.ImmediateGoal == "" || goalsMorning.ShortTermGoal == "" || goalsMorning.LongTermGoal == "" {
		t.Fatalf("expected all 3 goal horizons to be populated, got %+v", goalsMorning)
	}

	// Dusk arrives (tod = 12000) after Explorer has traveled 48 blocks along frontier to (48, 64, 0) with a campfire in inventory
	frameDusk := &civquestv1.TelemetryFrame{
		WorldTick: 2000,
		TimeOfDay: 12000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 48, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:campfire", Count: 1},
			},
		},
	}
	shelterIntent := EvaluateExploration("Able", frameDusk, cfg, state)
	if shelterIntent == nil || shelterIntent.GetInteractBlock() == nil ||
		shelterIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Fatalf("expected Explorer >32 blocks away at dusk to establish forward outpost and place campfire, got %+v", shelterIntent)
	}
	if len(state.GetOutposts()) < 2 {
		t.Fatalf("expected new forward outpost to be recorded in state.outposts, got %d", len(state.GetOutposts()))
	}

	outpostNode, _ := EvaluateTopologicalMapping(frameDusk, state)
	if outpostNode == nil || outpostNode.Category != "OUTPOST" {
		t.Fatalf("expected topological node at forward outpost to have Category=OUTPOST, got %+v", outpostNode)
	}
}

func TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	stuckWithObstruction := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 62, Z: 10.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 62, Z: 10},
				BlockId: "minecraft:dirt",
			},
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 63, Z: 10},
				BlockId: "minecraft:dirt",
			},
		},
	}

	intent := EvaluateExploration("Able", stuckWithObstruction, cfg, state)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected InteractBlockIntent when STUCK next to dirt wall, got %+v", intent)
	}
	ib := intent.GetInteractBlock()
	if ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on obstructing block, got %v", ib.InteractionType)
	}
	if ib.Pos.GetY() != 63 {
		t.Fatalf("expected head-height block (Y=63) to be broken first to form a 1-block step-up, got Y=%d", ib.Pos.GetY())
	}
}

func TestDuskEmergencyShelterFromLogsBottomUp(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Warm-up frame
	_ = EvaluateExploration("Able", &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 11000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}, cfg, state)

	// Step 1: Dusk with 4 oak_logs in inventory -> crafts oak_planks and reports BUILD_SHELTER goal
	frameLogs := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 12000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 4},
			},
		},
	}
	goals := EvaluateGoals(frameLogs, cfg, state)
	if goals.ImmediateGoal != "BUILD_SHELTER: Construct protective night shelter and light source from carried blocks" {
		t.Fatalf("expected BUILD_SHELTER immediate goal at dusk with logs, got %q", goals.ImmediateGoal)
	}

	craftPlanks := EvaluateExploration("Able", frameLogs, cfg, state)
	if craftPlanks == nil || craftPlanks.GetCraftItem() == nil || craftPlanks.GetCraftItem().RecipeId != "minecraft:oak_planks" {
		t.Fatalf("expected CraftItemIntent for minecraft:oak_planks from logs at dusk, got %+v", craftPlanks)
	}

	// Step 2: Now has 8 oak_planks and campfire placed nearby -> builds 8 shelter walls bottom-up (Y=64 before Y=65)
	var placedY []int32
	for i := 0; i < 8; i++ {
		f := &civquestv1.TelemetryFrame{
			WorldTick: int64(300 + i*25),
			TimeOfDay: 12500,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_planks", Count: int32(8 - i)},
				},
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{
					Pos:     &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1},
					BlockId: "minecraft:campfire",
				},
			},
		}
		placeIntent := EvaluateExploration("Able", f, cfg, state)
		if placeIntent == nil || placeIntent.GetInteractBlock() == nil ||
			placeIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
			t.Fatalf("step %d: expected PLACE_BLOCK for shelter wall, got %+v", i, placeIntent)
		}
		placedY = append(placedY, placeIntent.GetInteractBlock().Pos.GetY())
	}

	for i := 0; i < 4; i++ {
		if placedY[i] != 64 {
			t.Fatalf("expected bottom-up placement: wall %d at Y=64, got Y=%d", i, placedY[i])
		}
	}
	for i := 4; i < 8; i++ {
		if placedY[i] != 65 {
			t.Fatalf("expected bottom-up placement: wall %d at Y=65, got Y=%d", i, placedY[i])
		}
	}
}
