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
