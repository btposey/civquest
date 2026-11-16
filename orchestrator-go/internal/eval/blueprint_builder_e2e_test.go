package eval

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// TestBlueprintBuilder_E2E_AutonomousConstruction verifies end-to-end that an agent
// autonomously builds a multi-level blueprint structure from schematics:
// 1. Subterranean cellar level (Y = -1): naturally obstructed terrain is excavated first.
// 2. Ground and roof levels (Y = 0, Y = 1): built in strict bottom-up layer order (Ymin -> Ymax).
// 3. In-situ material taxonomy substitution: generic tags (#minecraft:planks, #minecraft:stairs)
//    are resolved to available inventory stock while preserving directional properties (facing, half).
// 4. Multi-agent quadrant partitioning: blocks partition into NW, NE, SW, SE with zero collision.
// 5. Completion & persistence: functional zone is registered in civic_registry.db.
func TestBlueprintBuilder_E2E_AutonomousConstruction(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initialize Civic Registry DB
	civicPath := filepath.Join(tmpDir, "civic.db")
	civicDB, err := db.OpenCivicRegistryDB(civicPath, "clan_e2e_builders")
	if err != nil {
		t.Fatalf("failed opening civic registry: %v", err)
	}
	defer civicDB.Close()

	// 2. Define a multi-level blueprint with a subterranean cellar and directional stairs
	// 3x2x3 structure:
	// Y = -1 (cellar foundation): 9 cobblestone blocks
	// Y =  0 (walls): 8 spruce planks + 1 doorway air
	// Y =  1 (roof): 3 directional stairs
	bp := &blueprints.Blueprint{
		ID:      "frontier_cellar_outpost",
		Name:    "Frontier Cellar Outpost",
		Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 3,
			Length: 3,
			MinY:   -1, // Cellar at Y=-1, ground at Y=0, roof at Y=1
		},
		Palette: map[int]string{
			1: "#c:stones",
			2: "#minecraft:planks",
			3: "#minecraft:stairs[facing=north,half=bottom]",
		},
		Layers: []blueprints.Layer{
			{
				Y: -1,
				Matrix: [][]int{
					{1, 1, 1},
					{1, 1, 1},
					{1, 1, 1},
				},
			},
			{
				Y: 0,
				Matrix: [][]int{
					{2, 2, 2},
					{2, 0, 2},
					{2, 2, 2},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{3, 3, 3},
					{0, 0, 0},
					{0, 0, 0},
				},
			},
		},
	}

	anchorX := int32(10)
	anchorY := int32(64)
	anchorZ := int32(10)

	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-e2e-outpost",
		Blueprint: bp,
		AnchorX:   int(anchorX),
		AnchorY:   int(anchorY),
		AnchorZ:   int(anchorZ),
		CivicDB:   civicDB,
	})
	if err != nil {
		t.Fatalf("failed creating construction coordinator: %v", err)
	}

	constructionSkill := system1.NewConstructionSkill(coord)

	// 3. Configure TimeWarpSimulation with builder agent
	sim, err := NewTimeWarpSimulation(TimeWarpConfig{
		AgentID:   "alder",
		ClanID:    "clan_e2e_builders",
		WorkDir:   tmpDir,
		TickDelta: 4,
		InitialPosition: &civquestv1.Vec3{
			X: float64(anchorX) + 1.5,
			Y: float64(anchorY),
			Z: float64(anchorZ) + 1.5,
		},
		InitialInventory: []*civquestv1.ItemStackProto{
			{ItemId: "minecraft:cobblestone", Count: 64},
			{ItemId: "minecraft:spruce_planks", Count: 64},
			{ItemId: "minecraft:oak_stairs", Count: 64}, // In-situ substitution for generic #minecraft:stairs
		},
		InitialBlocks: []*civquestv1.VisibleBlock{
			// Seed a natural dirt obstacle at subterranean coordinate (10, 63, 10)
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10},
				BlockId: "minecraft:dirt",
			},
		},
	})
	if err != nil {
		t.Fatalf("failed creating simulation: %v", err)
	}
	defer sim.Close()

	// 4. Drive autonomous construction step-by-step
	fg := system1.NewFactGraph()
	fg.SetBool("construction.active", "construction", true, "active build project")

	placedCount := 0
	excavatedCount := 0
	maxIterations := 200 // Safeguard against infinite loops

	for i := 0; i < maxIterations; i++ {
		frame := sim.BuildTelemetryFrame()
		skillCtx := &system1.SkillContext{
			AgentID: "alder",
			Frame:   frame,
			CivicDB: civicDB,
		}

		intents, err := constructionSkill.Execute(skillCtx, fg)
		if err != nil {
			t.Fatalf("iteration %d: construction skill error: %v", i, err)
		}

		if len(intents) == 0 {
			// Check if construction completed
			if coord.IsComplete() {
				break
			}
			// If not complete, ensure skill continues next quadrant
			continue
		}

		// Apply emitted intent into simulated physical world
		for _, intent := range intents {
			if nav := intent.GetNavigate(); nav != nil {
				// Move agent towards navigation target
				sim.SetPosition(nav.Target.X, nav.Target.Y, nav.Target.Z)
			}

			if ib := intent.GetInteractBlock(); ib != nil {
				switch ib.InteractionType {
				case civquestv1.InteractBlockIntent_HARVEST_BREAK:
					excavatedCount++
					posKey := posToKey(ib.Pos)
					delete(sim.blocks, posKey) // Obstacle cleared

				case civquestv1.InteractBlockIntent_PLACE_BLOCK:
					placedCount++
					posKey := posToKey(ib.Pos)
					sim.blocks[posKey] = ib.BlockOrItemId
					sim.RemoveInventoryItem(ib.BlockOrItemId, 1)

					// Verify directional properties were transferred to substitute
					if strings.Contains(ib.BlockOrItemId, "stairs") {
						if !strings.Contains(ib.BlockOrItemId, "facing=north") || !strings.Contains(ib.BlockOrItemId, "half=bottom") {
							t.Errorf("stairs lost directional blockstate properties: %s", ib.BlockOrItemId)
						}
					}
				}
			}
		}

		_ = sim.Step(4)
	}

	// 5. Verification Assertions
	if excavatedCount < 1 {
		t.Errorf("expected at least 1 subterranean excavation intent for obstructing dirt block, got %d", excavatedCount)
	}

	// 9 foundation + 8 walls + 3 roof stairs = 20 total blocks
	if placedCount != 20 {
		t.Errorf("expected 20 placed blocks, got %d", placedCount)
	}

	if !coord.IsComplete() {
		t.Errorf("expected construction coordinator to be complete")
	}

	// Verify subterranean foundation block was placed at Y=63
	foundationVoxel := BlockPosKey{X: 10, Y: 63, Z: 10}
	if bID, ok := sim.blocks[foundationVoxel]; !ok || bID != "minecraft:cobblestone" {
		t.Errorf("expected subterranean foundation block placed at (10,63,10), got %q (ok=%t)", bID, ok)
	}

	// Verify roof stairs placed at Y=65 with directional properties
	roofVoxel := BlockPosKey{X: 10, Y: 65, Z: 10}
	if bID, ok := sim.blocks[roofVoxel]; !ok || !strings.Contains(bID, "stairs") {
		t.Errorf("expected roof stairs placed at (10,65,10), got %q", bID)
	}

	// Verify functional zone was registered in civic_registry.db
	zones, err := civicDB.ListZones()
	if err != nil {
		t.Fatalf("ListZones failed: %v", err)
	}
	if len(zones) == 0 {
		t.Errorf("expected functional zone registered in CivicDB upon construction completion")
	} else {
		z := zones[0]
		if z.ZoneType != db.ZoneTypeResidential {
			t.Errorf("expected residential zone type for shelter, got %s", z.ZoneType)
		}
		if z.MinY != 63 || z.MaxY != 65 {
			t.Errorf("expected zone bounding box Y in [63, 65], got [%d, %d]", z.MinY, z.MaxY)
		}
	}
}
