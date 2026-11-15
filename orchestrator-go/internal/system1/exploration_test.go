package system1

import (
	"math"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
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

	// Frame 3: Agent now has 2 oak_logs in inventory, no crafting table placed -> should craft crafting_table!
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
	craftTableIntent := EvaluateExploration("Able", frame3, cfg, state)
	if craftTableIntent == nil || craftTableIntent.GetCraftItem() == nil || craftTableIntent.GetCraftItem().RecipeId != "minecraft:crafting_table" {
		t.Fatalf("expected CraftItemIntent for minecraft:crafting_table, got %+v", craftTableIntent)
	}

	// Frame 4: Agent now has 1 oak_log and 1 crafting_table in inventory -> should place crafting table!
	frame4 := &civquestv1.TelemetryFrame{
		WorldTick: 220,
		TimeOfDay: 2120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
				{Slot: 1, ItemId: "minecraft:crafting_table", Count: 1},
			},
		},
	}
	placeTableIntent := EvaluateExploration("Able", frame4, cfg, state)
	if placeTableIntent == nil || placeTableIntent.GetInteractBlock() == nil ||
		placeTableIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK ||
		placeTableIntent.GetInteractBlock().BlockOrItemId != "minecraft:crafting_table" {
		t.Fatalf("expected PLACE_BLOCK intent for minecraft:crafting_table, got %+v", placeTableIntent)
	}

	// Frame 5: Crafting table is placed on the ground nearby -> should craft wooden_axe at crafting table!
	tablePos := placeTableIntent.GetInteractBlock().Pos
	frame5 := &civquestv1.TelemetryFrame{
		WorldTick: 260,
		TimeOfDay: 2160,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     tablePos,
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	craftAxeIntent := EvaluateExploration("Able", frame5, cfg, state)
	if craftAxeIntent == nil || craftAxeIntent.GetCraftItem() == nil || craftAxeIntent.GetCraftItem().RecipeId != "minecraft:wooden_axe" {
		t.Fatalf("expected CraftItemIntent for minecraft:wooden_axe near crafting table, got %+v", craftAxeIntent)
	}
	if craftAxeIntent.GetCraftItem().WorkstationPos == nil ||
		craftAxeIntent.GetCraftItem().WorkstationPos.X != tablePos.X ||
		craftAxeIntent.GetCraftItem().WorkstationPos.Z != tablePos.Z {
		t.Fatalf("expected workstation_pos to match placed crafting table pos, got %+v", craftAxeIntent.GetCraftItem().WorkstationPos)
	}
}

func TestCraftingTableDistantNavigation(t *testing.T) {
	state := NewAgentState()
	state.telemetryFramesSeen = 5
	state.lastExploreTick = 100
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent at (0, 64, 0), crafting table at (10, 64, 0) (distance 10 > 4.5 reach)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	navIntent := EvaluateExploration("Able", frame, cfg, state)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent towards distant crafting table, got %+v", navIntent)
	}
	target := navIntent.GetNavigate().Target
	if target == nil || target.X != 10.5 || target.Z != 0.5 {
		t.Fatalf("expected navigation target (10.5, 64, 0.5), got %+v", target)
	}
}

func TestCraftingTableStoneUpgrade(t *testing.T) {
	state := NewAgentState()
	state.telemetryFramesSeen = 5
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Agent at (0, 64, 0) with cobblestone & stick, crafting table within reach at (1, 64, 0)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 600,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:cobblestone", Count: 3},
				{Slot: 1, ItemId: "minecraft:stick", Count: 2},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0},
				BlockId: "minecraft:crafting_table",
			},
		},
	}
	craftIntent := EvaluateExploration("Able", frame, cfg, state)
	if craftIntent == nil || craftIntent.GetCraftItem() == nil || craftIntent.GetCraftItem().RecipeId != "minecraft:stone_sword" {
		t.Fatalf("expected CraftItemIntent for minecraft:stone_sword, got %+v", craftIntent)
	}
	if craftIntent.GetCraftItem().WorkstationPos == nil || craftIntent.GetCraftItem().WorkstationPos.X != 1 {
		t.Fatalf("expected workstation_pos X=1, got %+v", craftIntent.GetCraftItem().WorkstationPos)
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

	// Step 2: Now has oak_planks and campfire placed nearby -> builds cabin bottom-up (Y=64 floor before Y=65 walls)
	var placedY []int32
	var backstepCount int
	for i := 0; i < 26; i++ {
		f := &civquestv1.TelemetryFrame{
			WorldTick: int64(300 + i*25),
			TimeOfDay: 12500,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_planks", Count: int32(30 - i)},
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
		if nav := placeIntent.GetNavigate(); nav != nil && nav.Target != nil {
			backstepCount++
			f.Status.Position = nav.Target
			placeIntent = EvaluateExploration("Able", f, cfg, state)
		}
		if placeIntent == nil || placeIntent.GetInteractBlock() == nil ||
			placeIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
			t.Fatalf("step %d: expected PLACE_BLOCK for shelter wall, got %+v", i, placeIntent)
		}
		placedY = append(placedY, placeIntent.GetInteractBlock().Pos.GetY())
	}
	if backstepCount == 0 {
		t.Fatalf("expected at least one back-step NavigateIntent to avoid placing shelter block on self")
	}

	for i := 0; i < 25; i++ {
		if placedY[i] != 64 {
			t.Fatalf("expected bottom-up placement: floor block %d at Y=64, got Y=%d", i, placedY[i])
		}
	}
	if placedY[25] != 65 {
		t.Fatalf("expected bottom-up placement: first wall block at Y=65, got Y=%d", placedY[25])
	}
}

func TestShelterConstructionAvoidsSelfCollisionAndStepsBack(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Initialize shelter position at (0, 64, 0)
	state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	state.ShelterBuildIndex = 12 // index 12 is center floor block (0, 64, 0)

	// Agent standing directly inside (0, 64, 0) at (0.5, 64.0, 0.5)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		TimeOfDay: 13000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 10},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}, BlockId: "minecraft:campfire"},
		},
	}
	state.telemetryFramesSeen = 3

	// EvaluateExploration should reject placing on self and emit micro-step NavigateIntent
	intent := EvaluateExploration("Able", frame, cfg, state)
	if intent == nil || intent.GetNavigate() == nil {
		t.Fatalf("expected micro-step NavigateIntent when agent collides with target voxel, got %+v", intent)
	}

	navTarget := intent.GetNavigate().Target
	if navTarget == nil {
		t.Fatalf("expected valid navigation target")
	}

	// Verify build index was NOT advanced
	if state.ShelterBuildIndex != 12 {
		t.Fatalf("expected ShelterBuildIndex to remain 12 during step back, got %d", state.ShelterBuildIndex)
	}

	// Verify navigation target is outside the 5x5 construction boundary
	distFromCenter := math.Hypot(navTarget.X-0.5, navTarget.Z-0.5)
	if distFromCenter < 2.5 {
		t.Fatalf("expected step-back target outside 2.5m construction boundary, got distance %.2f", distFromCenter)
	}

	// Now simulate agent arriving at the stepped-back position
	frame.Status.Position = navTarget
	frame.WorldTick += 25
	placeIntent := EvaluateExploration("Able", frame, cfg, state)
	if placeIntent == nil || placeIntent.GetInteractBlock() == nil ||
		placeIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Fatalf("expected PLACE_BLOCK once positioned outside target voxel, got %+v", placeIntent)
	}
	if state.ShelterBuildIndex != 13 {
		t.Fatalf("expected ShelterBuildIndex to advance to 13 after placement, got %d", state.ShelterBuildIndex)
	}
}

func TestFrontierCabinGeometry(t *testing.T) {
	blueprint := blueprints.FrontierCabinBlueprint()
	if len(blueprint) == 0 {
		t.Fatalf("expected non-empty FrontierCabinBlueprint")
	}

	var minX, maxX, minY, maxY, minZ, maxZ int32
	minX, minZ, minY = math.MaxInt32, math.MaxInt32, math.MaxInt32
	maxX, maxZ, maxY = math.MinInt32, math.MinInt32, math.MinInt32

	hasDoor := false
	hasRoof := false
	hasTorch := false
	roofCount := 0

	for i, intent := range blueprint {
		if intent == nil || intent.TargetVoxel == nil {
			t.Fatalf("nil intent or TargetVoxel at index %d", i)
		}
		pos := intent.TargetVoxel
		state := intent.BlockStateString

		// Assert bottom-up Y-ordering: Y ascending across the returned slice
		if i > 0 {
			prevY := blueprint[i-1].TargetVoxel.Y
			if pos.Y < prevY {
				t.Fatalf("bottom-up Y-ordering violation at index %d: Y=%d < previous Y=%d", i, pos.Y, prevY)
			}
		}

		if pos.X < minX {
			minX = pos.X
		}
		if pos.X > maxX {
			maxX = pos.X
		}
		if pos.Y < minY {
			minY = pos.Y
		}
		if pos.Y > maxY {
			maxY = pos.Y
		}
		if pos.Z < minZ {
			minZ = pos.Z
		}
		if pos.Z > maxZ {
			maxZ = pos.Z
		}

		// Door check at south face (X=0, Z=2, Y=1)
		if strings.Contains(state, "oak_door") {
			hasDoor = true
		}

		// Roof check (Y=4, oak_slab)
		if pos.Y == 4 && strings.Contains(state, "oak_slab") {
			hasRoof = true
			roofCount++
		}

		// Torch check inside on north wall (X=0, Y=2, Z=-1)
		if strings.Contains(state, "torch") {
			hasTorch = true
			if pos.X != 0 || pos.Y != 2 || pos.Z != -1 {
				t.Errorf("expected torch at (0, 2, -1), got (%d, %d, %d)", pos.X, pos.Y, pos.Z)
			}
		}

		// Interior check: At Y=1, 2, 3, interior [-1, 1]x[-1, 1] must be clear of solid blocks
		if pos.Y >= 1 && pos.Y <= 3 {
			if pos.X >= -1 && pos.X <= 1 && pos.Z >= -1 && pos.Z <= 1 {
				// The only interior placement permitted at Y=1, 2, 3 is the wall torch at (0, 2, -1)
				if !strings.Contains(state, "torch") {
					t.Fatalf("interior (X in [-1, 1], Z in [-1, 1]) obstructed at Y=%d (%d,%d,%d) by %s",
						pos.Y, pos.X, pos.Y, pos.Z, state)
				}
			}
		}
	}

	// 1. Assert 5x5 exterior: X in [-2, 2], Z in [-2, 2]
	if minX != -2 || maxX != 2 {
		t.Fatalf("expected 5x5 exterior X in [-2, 2], got minX=%d, maxX=%d", minX, maxX)
	}
	if minZ != -2 || maxZ != 2 {
		t.Fatalf("expected 5x5 exterior Z in [-2, 2], got minZ=%d, maxZ=%d", minZ, maxZ)
	}
	if maxX-minX+1 != 5 || maxZ-minZ+1 != 5 {
		t.Fatalf("expected 5x5 exterior footprint, got width=%d, length=%d", maxX-minX+1, maxZ-minZ+1)
	}

	// 2. Assert Height: Y in [0, 4] (floor Y=0, walls Y=1..3, roof Y=4)
	if minY != 0 || maxY != 4 {
		t.Fatalf("expected height Y in [0, 4], got minY=%d, maxY=%d", minY, maxY)
	}

	// 3. Assert Door present
	if !hasDoor {
		t.Fatalf("expected oak_door in cabin blueprint")
	}

	// 4. Assert Roof present (9 slabs covering 3x3 interior)
	if !hasRoof || roofCount != 9 {
		t.Fatalf("expected 9 roof slabs covering 3x3 interior at Y=4, got %d slabs (hasRoof=%v)", roofCount, hasRoof)
	}

	// 5. Assert Torch present
	if !hasTorch {
		t.Fatalf("expected torch inside cabin blueprint")
	}
}

func TestFrontierCabinIntents_Offset(t *testing.T) {
	center := &civquestv1.Vec3{X: 100, Y: 64, Z: -50}
	intents := blueprints.FrontierCabinIntents(center)
	if len(intents) != blueprints.CabinTotalBlocks {
		t.Fatalf("expected %d blocks, got %d", blueprints.CabinTotalBlocks, len(intents))
	}

	// First block is floor corner (-2, 0, -2) offset by (100, 64, -50) => (98, 64, -52)
	first := intents[0].TargetVoxel
	if first.X != 98 || first.Y != 64 || first.Z != -52 {
		t.Fatalf("expected first block at (98, 64, -52), got (%d, %d, %d)", first.X, first.Y, first.Z)
	}

	for i := 1; i < len(intents); i++ {
		if intents[i].TargetVoxel.Y < intents[i-1].TargetVoxel.Y {
			t.Fatalf("offset intents bottom-up ordering violation at index %d", i)
		}
	}
}

func TestAgentState_ShelterBuildPhaseAndIndex(t *testing.T) {
	state := NewAgentState()
	if state.GetShelterBuildPhase() != 0 {
		t.Fatalf("expected initial phase 0, got %d", state.GetShelterBuildPhase())
	}
	if state.GetShelterBuildIndex() != 0 {
		t.Fatalf("expected initial index 0, got %d", state.GetShelterBuildIndex())
	}

	state.SetShelterBuildPhase(3)
	if state.GetShelterBuildPhase() != 3 {
		t.Fatalf("expected phase 3, got %d", state.GetShelterBuildPhase())
	}

	state.SetShelterBuildIndex(42)
	if state.GetShelterBuildIndex() != 42 {
		t.Fatalf("expected index 42, got %d", state.GetShelterBuildIndex())
	}
}

func TestSectorLockedRadialFrontierScouting_NoOscillationAndChainedOutward(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Frame 1: Warm-up telemetry
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	_ = EvaluateExploration("Able", frame1, cfg, state)

	// Frame 2: Start exploration at tick 1000 at origin (0, 64, 0)
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}

	intent0 := EvaluateExploration("Able", frame2, cfg, state)
	if intent0 == nil || intent0.GetNavigate() == nil {
		t.Fatalf("expected initial NavigateIntent, got %+v", intent0)
	}
	target0 := intent0.GetNavigate().GetTarget()
	dist0 := math.Hypot(target0.X, target0.Z)
	if math.Abs(dist0-48.0) > 1.0 {
		t.Fatalf("expected Leg 0 target at ~48m, got %.2fm (target: %+v)", dist0, target0)
	}
	azimuth0 := math.Atan2(target0.Z, target0.X)

	// Step 2: Simulate travel toward target over 3-second intervals (60 ticks each)
	// Actively navigating (PATHING) with forward progress: must NOT oscillate or re-issue intent
	ticks := []int64{1060, 1120, 1180}
	dists := []float64{15.0, 30.0, 45.0}
	for i, tick := range ticks {
		stepPos := &civquestv1.Vec3{
			X: math.Cos(azimuth0) * dists[i],
			Y: 64,
			Z: math.Sin(azimuth0) * dists[i],
		}
		pathingFrame := &civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 2100 + (tick - 1000),
			Status: &civquestv1.AgentStatus{
				Position:        stepPos,
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
					{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
					{Slot: 2, ItemId: "minecraft:torch", Count: 8},
				},
			},
		}
		midIntent := EvaluateExploration("Able", pathingFrame, cfg, state)
		if midIntent != nil {
			t.Fatalf("expected nil intent while actively pathing at tick %d (eliminated 3-second oscillation), got %+v", tick, midIntent)
		}
		curAz, _ := state.GetFrontierAzimuth()
		if math.Abs(math.Atan2(math.Sin(curAz-azimuth0), math.Cos(curAz-azimuth0))) > 0.001 {
			t.Fatalf("azimuth changed during pathing: was %.4f, now %.4f", azimuth0, curAz)
		}
	}

	// Step 3: Arrival at Leg 0 (~48m, within 2m tolerance)
	arrivePos0 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 47.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 47.5,
	}
	arriveFrame0 := &civquestv1.TelemetryFrame{
		WorldTick: 1220,
		TimeOfDay: 2320,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos0,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent1 := EvaluateExploration("Able", arriveFrame0, cfg, state)
	if intent1 == nil || intent1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 1 upon arrival, got %+v", intent1)
	}
	target1 := intent1.GetNavigate().GetTarget()
	dist1 := math.Hypot(target1.X, target1.Z)
	if math.Abs(dist1-96.0) > 1.0 {
		t.Fatalf("expected Leg 1 target chained outward beyond 64m to ~96m, got %.2fm (target: %+v)", dist1, target1)
	}
	azimuth1 := math.Atan2(target1.Z, target1.X)
	if math.Abs(math.Atan2(math.Sin(azimuth1-azimuth0), math.Cos(azimuth1-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 1 to maintain same sector azimuth (%.4f), got %.4f (angular oscillation detected)", azimuth0, azimuth1)
	}

	// Step 4: Arrival at Leg 1 (~96m) -> Advances to Leg 2 (~144m)
	arrivePos1 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 95.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 95.5,
	}
	arriveFrame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1400,
		TimeOfDay: 2500,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos1,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent2 := EvaluateExploration("Able", arriveFrame1, cfg, state)
	if intent2 == nil || intent2.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 2 upon arrival, got %+v", intent2)
	}
	target2 := intent2.GetNavigate().GetTarget()
	dist2 := math.Hypot(target2.X, target2.Z)
	if math.Abs(dist2-144.0) > 1.0 {
		t.Fatalf("expected Leg 2 target chained to ~144m, got %.2fm", dist2)
	}
	azimuth2 := math.Atan2(target2.Z, target2.X)
	if math.Abs(math.Atan2(math.Sin(azimuth2-azimuth0), math.Cos(azimuth2-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 2 to maintain same sector azimuth, got %.4f vs %.4f", azimuth2, azimuth0)
	}

	// Step 5: Arrival at Leg 2 (~144m) -> Advances to Leg 3 (~192m)
	arrivePos2 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 143.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 143.5,
	}
	arriveFrame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1600,
		TimeOfDay: 2700,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos2,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent3 := EvaluateExploration("Able", arriveFrame2, cfg, state)
	if intent3 == nil || intent3.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 3 upon arrival, got %+v", intent3)
	}
	target3 := intent3.GetNavigate().GetTarget()
	dist3 := math.Hypot(target3.X, target3.Z)
	if math.Abs(dist3-192.0) > 1.0 {
		t.Fatalf("expected Leg 3 target chained to ~192m, got %.2fm", dist3)
	}
	azimuth3 := math.Atan2(target3.Z, target3.X)
	if math.Abs(math.Atan2(math.Sin(azimuth3-azimuth0), math.Cos(azimuth3-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 3 to maintain same sector azimuth, got %.4f vs %.4f", azimuth3, azimuth0)
	}
}

func TestSectorLockedRadialFrontierScouting_TimeoutPivotsSector(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Warm-up
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:    20,
			FoodLevel: 20,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	_ = EvaluateExploration("Able", frame1, cfg, state)

	// Initial start at tick 1000
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent0 := EvaluateExploration("Able", frame2, cfg, state)
	if intent0 == nil {
		t.Fatal("expected initial intent")
	}
	initialAz, _ := state.GetFrontierAzimuth()

	// Zero progress for 30s (600 ticks, at tick 1650)
	stalledFrame := &civquestv1.TelemetryFrame{
		WorldTick: 1650,
		TimeOfDay: 2750,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0}, // No movement from origin
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	pivotIntent := EvaluateExploration("Able", stalledFrame, cfg, state)
	if pivotIntent == nil || pivotIntent.GetNavigate() == nil {
		t.Fatalf("expected new NavigateIntent upon leg timeout, got %+v", pivotIntent)
	}
	newAz, _ := state.GetFrontierAzimuth()
	if math.Abs(math.Atan2(math.Sin(newAz-initialAz), math.Cos(newAz-initialAz))) < 0.1 {
		t.Fatalf("expected sector azimuth to pivot after 30s zero-progress timeout, but azimuth stayed %.4f", initialAz)
	}
	if state.GetFrontierRadialLeg() != 0 {
		t.Fatalf("expected radial leg to reset to 0 in new sector, got %d", state.GetFrontierRadialLeg())
	}
}

func TestNonExplorerRotatesAzimuthAfterLegs(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "CHRONICLER" // Non-explorer profession
	cfg.Traits.Curiosity = 0.5     // legStep = 16.0 + 0.5*8.0 = 20.0

	// Warm-up to establish home origin
	warmupFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	_ = EvaluateExploration("Able", warmupFrame, cfg, state)

	// Tick 1000: Initial exploration target (Leg 0: 12m)
	frame0 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent0 := EvaluateExploration("Able", frame0, cfg, state)
	if intent0 == nil || intent0.GetNavigate() == nil {
		t.Fatalf("expected initial NavigateIntent, got %+v", intent0)
	}
	initialAz, _ := state.GetFrontierAzimuth()
	target0 := intent0.GetNavigate().GetTarget()
	dist0 := math.Hypot(target0.X, target0.Z)
	if math.Abs(dist0-12.0) > 0.5 {
		t.Fatalf("expected leg 0 target ~12m, got %.2f", dist0)
	}

	// Arrive at Leg 0 -> Advances to Leg 1 (32m) along same azimuth
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 2200,
		Status: &civquestv1.AgentStatus{
			Position:        target0,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent1 := EvaluateExploration("Able", frame1, cfg, state)
	if intent1 == nil || intent1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 1, got %+v", intent1)
	}
	target1 := intent1.GetNavigate().GetTarget()
	dist1 := math.Hypot(target1.X, target1.Z)
	if math.Abs(dist1-32.0) > 0.5 {
		t.Fatalf("expected leg 1 target ~32m, got %.2f", dist1)
	}
	az1, _ := state.GetFrontierAzimuth()
	if math.Abs(math.Atan2(math.Sin(az1-initialAz), math.Cos(az1-initialAz))) > 0.001 {
		t.Fatalf("expected Leg 1 to keep same azimuth, got %.4f vs %.4f", az1, initialAz)
	}

	// Arrive at Leg 1 -> Advances to Leg 2 (52m) along same azimuth
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1400,
		TimeOfDay: 2300,
		Status: &civquestv1.AgentStatus{
			Position:        target1,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent2 := EvaluateExploration("Able", frame2, cfg, state)
	if intent2 == nil || intent2.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 2, got %+v", intent2)
	}
	target2 := intent2.GetNavigate().GetTarget()
	dist2 := math.Hypot(target2.X, target2.Z)
	if math.Abs(dist2-52.0) > 0.5 {
		t.Fatalf("expected leg 2 target ~52m, got %.2f", dist2)
	}

	// Arrive at Leg 2 -> Leg completion! Must rotate azimuth (+math.Pi/4) and reset radial leg to 0
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick: 1600,
		TimeOfDay: 2400,
		Status: &civquestv1.AgentStatus{
			Position:        target2,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent3 := EvaluateExploration("Able", frame3, cfg, state)
	if intent3 == nil || intent3.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent after completing Leg 2, got %+v", intent3)
	}
	newAz, _ := state.GetFrontierAzimuth()
	expectedAz := math.Atan2(math.Sin(initialAz+math.Pi/4), math.Cos(initialAz+math.Pi/4))
	azDiff := math.Abs(math.Atan2(math.Sin(newAz-expectedAz), math.Cos(newAz-expectedAz)))
	if azDiff > 0.01 {
		t.Fatalf("expected azimuth to rotate by +45 deg (Pi/4) to %.4f, got %.4f (diff: %.4f)", expectedAz, newAz, azDiff)
	}
	if state.GetFrontierRadialLeg() != 0 {
		t.Fatalf("expected frontierRadialLeg to reset to 0 after completing legs, got %d", state.GetFrontierRadialLeg())
	}
}

