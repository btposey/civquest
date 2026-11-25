package system1

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestExplorerForwardOutpostAtDuskAndGoals(t *testing.T) {
	state := NewAgentState()
	prog := &shelterLocal{}
	_ = prog
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
	_ = EvaluateExploration("Able", frameMorning, cfg, state, nil)

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
	shelterIntent := EvaluateShelterGoals("Able", frameDusk, cfg, state, prog)
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

func TestDuskEmergencyShelterFromLogsBottomUp(t *testing.T) {
	state := NewAgentState()
	prog := &shelterLocal{}
	_ = prog
	state.SetRecipeGraph(newTestRecipeGraph())
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Warm-up frame
	_ = EvaluateShelterGoals("Able", &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 11000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}, cfg, state, prog)

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

	craftPlanks := EvaluateShelterGoals("Able", frameLogs, cfg, state, prog)
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
		placeIntent := EvaluateShelterGoals("Able", f, cfg, state, prog)
		if nav := placeIntent.GetNavigate(); nav != nil && nav.Target != nil {
			backstepCount++
			f.Status.Position = nav.Target
			placeIntent = EvaluateShelterGoals("Able", f, cfg, state, prog)
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
	prog := &shelterLocal{}
	_ = prog
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Initialize shelter position at (0, 64, 0)
	state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	prog.BuildIndex = 12 // index 12 is center floor block (0, 64, 0)

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
	state.mem.telemetryFramesSeen = 3

	// EvaluateShelterGoals should reject placing on self and emit micro-step NavigateIntent
	intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
	if intent == nil || intent.GetNavigate() == nil {
		t.Fatalf("expected micro-step NavigateIntent when agent collides with target voxel, got %+v", intent)
	}

	navTarget := intent.GetNavigate().Target
	if navTarget == nil {
		t.Fatalf("expected valid navigation target")
	}

	// Verify build index was NOT advanced
	if prog.BuildIndex != 12 {
		t.Fatalf("expected ShelterBuildIndex to remain 12 during step back, got %d", prog.BuildIndex)
	}

	// Verify navigation target is outside the 5x5 construction boundary
	distFromCenter := math.Hypot(navTarget.X-0.5, navTarget.Z-0.5)
	if distFromCenter < 2.5 {
		t.Fatalf("expected step-back target outside 2.5m construction boundary, got distance %.2f", distFromCenter)
	}

	// Now simulate agent arriving at the stepped-back position
	frame.Status.Position = navTarget
	frame.WorldTick += 25
	placeIntent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
	if placeIntent == nil || placeIntent.GetInteractBlock() == nil ||
		placeIntent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Fatalf("expected PLACE_BLOCK once positioned outside target voxel, got %+v", placeIntent)
	}
	if prog.BuildIndex != 13 {
		t.Fatalf("expected ShelterBuildIndex to advance to 13 after placement, got %d", prog.BuildIndex)
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

func TestShelterLocal_SeededFromStoreAndBankedOnExit(t *testing.T) {
	state := NewAgentState()
	if l := newShelterLocal(state); l.BuildIndex != 0 || l.Phase != 0 || l.Pos != nil || l.complete() {
		t.Fatalf("fresh local = %+v", l)
	}
	state.applyStoreWrites([]htpda.StoreWrite{shelterStore{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 2}, Built: 42}})
	l := newShelterLocal(state)
	if l.BuildIndex != 42 || l.Pos == nil || l.Pos.X != 1 || l.Phase != cabinPhase(42) {
		t.Fatalf("seeded local = %+v", l)
	}
	if state.GetCabinBuilt() != 42 {
		t.Fatalf("banked count = %d", state.GetCabinBuilt())
	}
}

func TestEvaluateShelterGoalsDirect(t *testing.T) {
	state := NewAgentState()
	prog := &shelterLocal{}
	_ = prog
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:oak_planks", Count: 10},
			},
		},
	}
	pos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}

	start := time.Now()
	intent := EvaluateShelterGoalsDirect("Able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, &shelterLocal{})
	duration := time.Since(start)

	// Invariant: Sub-5ms execution budget
	if duration > 5*time.Millisecond {
		t.Errorf("EvaluateShelterGoalsDirect took %v, exceeded 5ms budget", duration)
	}
	_ = intent
}

func TestEvaluateShelterGoals_HabitationPOIRecall(t *testing.T) {
	t.Run("Recalls Campfire Before Placing Duplicate", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		cfg := xdg.DefaultAgentIdentityConfig("Able")
		cfg.Profession = "EXPLORER"

		state.SpatialIndex().UpsertPOI(db.AgentPOI{
			POIID:          FormatPOIID(POICategoryHabitation, 0, 64, 0),
			Category:       POICategoryHabitation,
			BlockID:        "minecraft:campfire",
			X:              0.5,
			Y:              64.0,
			Z:              0.5,
			AccessX:        1.5,
			AccessY:        64.0,
			AccessZ:        0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})

		// Agent at (25, 64, 0) carrying a campfire with empty InterestingBlocks
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 2000,
			TimeOfDay: 12000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 25, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:campfire", Count: 1},
					{Slot: 1, ItemId: "minecraft:oak_planks", Count: 16},
				},
			},
		}

		intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
			t.Fatalf("expected NavigateIntent towards recalled campfire instead of placing duplicate, got %+v", intent)
		}
		target := intent.GetNavigate().Target
		if target.X != 1.5 || target.Y != 64.0 || target.Z != 0.5 {
			t.Fatalf("expected target (1.5, 64, 0.5), got %+v", target)
		}
	})

	t.Run("Recalls Bed Beyond 32m Outpost Threshold", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		cfg := xdg.DefaultAgentIdentityConfig("Able")
		cfg.Profession = "EXPLORER"

		state.SpatialIndex().UpsertPOI(db.AgentPOI{
			POIID:          FormatPOIID(POICategoryHabitation, 25, 64, 0),
			Category:       POICategoryHabitation,
			BlockID:        "minecraft:red_bed",
			X:              25.5,
			Y:              64.0,
			Z:              0.5,
			AccessX:        26.5,
			AccessY:        64.0,
			AccessZ:        0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})

		// Agent at (40, 64, 0) (> 32m from homeOrigin) carrying campfire and planks
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 2100,
			TimeOfDay: 12000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 40, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:campfire", Count: 1},
					{Slot: 1, ItemId: "minecraft:oak_planks", Count: 16},
				},
			},
		}

		intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
			t.Fatalf("expected NavigateIntent towards recalled bed instead of placing duplicate campfire, got %+v", intent)
		}
		target := intent.GetNavigate().Target
		if target.X != 26.5 || target.Y != 64.0 || target.Z != 0.5 {
			t.Fatalf("expected target (26.5, 64, 0.5), got %+v", target)
		}
	})
}

func TestShelterLightCrafting_NoCampfireShortcutWithoutCoal(t *testing.T) {
	t.Run("No coal skips bogus campfire craft and places shelter block", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetRecipeGraph(newTestRecipeGraph())
		state.mem.telemetryFramesSeen = 5
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("Able")
		cfg.Profession = "EXPLORER"

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 13000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 3.0, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_planks", Count: 32},
					{Slot: 1, ItemId: "minecraft:stick", Count: 4},
				},
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"},
			},
		}

		intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		if intent == nil || intent.GetInteractBlock() == nil ||
			intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
			t.Fatalf("expected shelter PLACE_BLOCK instead of bogus campfire craft without coal, got %+v", intent)
		}
	})

	t.Run("Stick and coal without nearby light pocket-crafts torch", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetRecipeGraph(newTestRecipeGraph())
		state.mem.telemetryFramesSeen = 5
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("Able")
		cfg.Profession = "EXPLORER"

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 13000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_planks", Count: 32},
					{Slot: 1, ItemId: "minecraft:stick", Count: 1},
					{Slot: 2, ItemId: "minecraft:coal", Count: 1},
				},
			},
		}

		intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		craft := intent.GetCraftItem()
		if craft == nil || craft.RecipeId != "minecraft:torch" {
			t.Fatalf("expected CraftItemIntent(minecraft:torch), got %+v", intent)
		}
	})

	t.Run("Birch logs in shelter craft birch_planks dynamically and planks+coal crafts stick first for torch", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetRecipeGraph(newTestRecipeGraph())
		state.mem.telemetryFramesSeen = 5
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("Able")
		cfg.Profession = "EXPLORER"

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 500,
			TimeOfDay: 13000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:birch_log", Count: 2},
				},
			},
		}
		intent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		if intent == nil || intent.GetCraftItem() == nil || intent.GetCraftItem().RecipeId != "minecraft:birch_planks" {
			t.Fatalf("expected CraftItemIntent(minecraft:birch_planks) from birch_log, got %+v", intent)
		}

		// With 20 birch_planks and 1 coal (0 sticks), light crafting crafts sticks first for torch!
		frame.WorldTick = 530
		frame.Status.Inventory = []*civquestv1.ItemStackProto{
			{Slot: 0, ItemId: "minecraft:birch_planks", Count: 20},
			{Slot: 1, ItemId: "minecraft:coal", Count: 1},
		}
		stickIntent := EvaluateShelterGoals("Able", frame, cfg, state, prog)
		if stickIntent == nil || stickIntent.GetCraftItem() == nil || stickIntent.GetCraftItem().RecipeId != "minecraft:stick" {
			t.Fatalf("expected CraftItemIntent(minecraft:stick) as prerequisite for torch, got %+v", stickIntent)
		}
	})
}

func TestEvaluateShelterGoals_AntiTetherAndSpatialIndexLogFallback(t *testing.T) {
	t.Run("Empty-handed agent far from homeOrigin does not tether back and returns nil when no logs known", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 16000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 25, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		}

		intent := EvaluateShelterGoals("earl", frame, cfg, state, prog)
		if intent != nil {
			t.Fatalf("expected nil intent (fall through to exploration) when 0 materials and 0 known logs, got %+v", intent)
		}
		if state.GetHomeOrigin().X != 0 || state.GetHomeOrigin().Z != 0 {
			t.Fatalf("expected homeOrigin preserved at (0, 64, 0), got %+v", state.GetHomeOrigin())
		}
	})

	t.Run("Empty-handed agent queries SpatialIndex for remembered log POI instead of tethering to homeOrigin", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		state.SpatialIndex().UpsertPOI(db.AgentPOI{
			POIID:          FormatPOIID(POICategoryResource, 55, 64, 10),
			Category:       POICategoryResource,
			BlockID:        "minecraft:oak_log",
			X:              55.5,
			Y:              64.0,
			Z:              10.5,
			AccessX:        54.5,
			AccessY:        64.0,
			AccessZ:        10.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})
		cfg := xdg.DefaultAgentIdentityConfig("earl")
		cfg.Profession = "EXPLORER"

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 16000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 25, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		}

		intent := EvaluateShelterGoals("earl", frame, cfg, state, prog)
		if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
			t.Fatalf("expected NavigateIntent to remembered log POI, got %+v", intent)
		}
		target := intent.GetNavigate().Target
		if target.X != 54.5 || target.Z != 10.5 {
			t.Fatalf("expected NavigateIntent to log POI (54.5, 64, 10.5), not homeOrigin, got %+v", target)
		}
	})

	t.Run("Empty-handed agent harvests nearby visible log instead of tethering to homeOrigin", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("earl")

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 16000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 25, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 27, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			},
		}

		intent := EvaluateShelterGoals("earl", frame, cfg, state, prog)
		if intent == nil || intent.GetInteractBlock() == nil ||
			intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
			t.Fatalf("expected HARVEST_BREAK on nearby oak_log, got %+v", intent)
		}
	})

	t.Run("Planks placement proceeds cleanly once 16+ planks held even with remaining logs", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetRecipeGraph(newTestRecipeGraph())
		state.ShelterPos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		cfg := xdg.DefaultAgentIdentityConfig("earl")

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 16000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_log", Count: 2},
					{Slot: 1, ItemId: "minecraft:oak_planks", Count: 16},
				},
			},
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}, BlockId: "minecraft:campfire"},
			},
		}

		intent := EvaluateShelterGoals("earl", frame, cfg, state, prog)
		if nav := intent.GetNavigate(); nav != nil && nav.Target != nil {
			frame.Status.Position = nav.Target
			intent = EvaluateShelterGoals("earl", frame, cfg, state, prog)
		}
		if intent == nil || intent.GetInteractBlock() == nil ||
			intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
			t.Fatalf("expected PLACE_BLOCK rather than infinite plank craft spam when 16 planks already held, got %+v", intent)
		}
	})

	t.Run("Completed shelter pulls agent back toward ShelterPos for night refuge", func(t *testing.T) {
		state := NewAgentState()
		prog := &shelterLocal{}
		_ = prog
		state.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})
		prog.Pos = &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
		prog.BuildIndex = blueprints.CabinTotalBlocks
		prog.Walk.lastExploreTick = 900
		cfg := xdg.DefaultAgentIdentityConfig("earl")

		frame := &civquestv1.TelemetryFrame{
			WorldTick: 1000,
			TimeOfDay: 16000,
			Status: &civquestv1.AgentStatus{
				Position:        &civquestv1.Vec3{X: 20, Y: 64, Z: 0},
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		}

		intent := EvaluateShelterGoals("earl", frame, cfg, state, prog)
		if intent == nil || intent.GetNavigate() == nil || intent.GetNavigate().Target == nil {
			t.Fatalf("expected NavigateIntent back to completed shelter, got %+v", intent)
		}
		target := intent.GetNavigate().Target
		if target.X != 0.5 || target.Z != 0.5 {
			t.Fatalf("expected NavigateIntent to ShelterPos (0.5, 64, 0.5), got %+v", target)
		}
	})
}
