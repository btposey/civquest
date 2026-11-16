package blueprints

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// TestQuadrant_PartitionNoOverlapAndFullCoverage verifies Acceptance Criterion 1:
// The blueprint bounding box is partitioned into 4 quadrants (NW, NE, SW, SE)
// with zero overlapping voxels and complete coverage of all placed blocks.
func TestQuadrant_PartitionNoOverlapAndFullCoverage(t *testing.T) {
	testCases := []struct {
		name   string
		width  int
		height int
		length int
	}{
		{"even_dimensions", 6, 4, 6},
		{"odd_dimensions", 7, 5, 7},
		{"asymmetric_dimensions", 10, 8, 4},
		{"minimal_dimensions", 2, 2, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vm := NewVoxelMatrix(tc.width, tc.height, tc.length)

			// Fill matrix completely with distinct block identifiers
			totalBlocksExpected := 0
			for y := 0; y < tc.height; y++ {
				for z := 0; z < tc.length; z++ {
					for x := 0; x < tc.width; x++ {
						// Interleave some air blocks to test filtering
						if (x+y+z)%5 == 0 {
							continue // air
						}
						err := vm.SetStateString(x, y, z, fmt.Sprintf("minecraft:stone_bricks[layer=%d]", y))
						if err != nil {
							t.Fatalf("failed to set block at (%d,%d,%d): %v", x, y, z, err)
						}
						totalBlocksExpected++
					}
				}
			}

			// Partition with anchor offset
			anchorX, anchorY, anchorZ := 100, 64, -200
			partitioned := PartitionBlueprintWithAnchor(vm, anchorX, anchorY, anchorZ)

			if len(partitioned) != 4 {
				t.Fatalf("expected 4 quadrants, got %d", len(partitioned))
			}

			// Track every voxel to verify no overlap across quadrants
			type VoxelKey struct {
				X, Y, Z int32
			}
			seenVoxels := make(map[VoxelKey]Quadrant)
			totalBlocksFound := 0

			midX := tc.width / 2
			midZ := tc.length / 2

			for _, q := range AllQuadrants() {
				intents := partitioned[q]
				totalBlocksFound += len(intents)

				for _, intent := range intents {
					pos := intent.TargetVoxel
					key := VoxelKey{X: pos.X, Y: pos.Y, Z: pos.Z}

					// 1. Verify no overlap across quadrants
					if existingQ, alreadySeen := seenVoxels[key]; alreadySeen {
						t.Fatalf("Voxel at (%d,%d,%d) assigned to both %s and %s (spatial collision!)",
							pos.X, pos.Y, pos.Z, existingQ, q)
					}
					seenVoxels[key] = q

					// 2. Verify voxel coordinates match quadrant bounds
					relX := int(pos.X) - anchorX
					relZ := int(pos.Z) - anchorZ

					switch q {
					case QuadrantNW:
						if relX >= midX || relZ >= midZ {
							t.Errorf("NW contains out-of-bounds coord (%d,%d), expected x<%d, z<%d", relX, relZ, midX, midZ)
						}
					case QuadrantNE:
						if relX < midX || relZ >= midZ {
							t.Errorf("NE contains out-of-bounds coord (%d,%d), expected x>=%d, z<%d", relX, relZ, midX, midZ)
						}
					case QuadrantSW:
						if relX >= midX || relZ < midZ {
							t.Errorf("SW contains out-of-bounds coord (%d,%d), expected x<%d, z>=%d", relX, relZ, midX, midZ)
						}
					case QuadrantSE:
						if relX < midX || relZ < midZ {
							t.Errorf("SE contains out-of-bounds coord (%d,%d), expected x>=%d, z>=%d", relX, relZ, midX, midZ)
						}
					}
				}
			}

			// 3. Verify complete coverage: all non-air blocks were partitioned
			if totalBlocksFound != totalBlocksExpected {
				t.Errorf("total blocks partitioned %d != expected non-air blocks %d", totalBlocksFound, totalBlocksExpected)
			}
		})
	}
}

// TestQuadrant_AscendingYOrder verifies Acceptance Criterion 2:
// Builder agents place blocks in strictly ascending Y coordinates (bottom-up: Y_min -> Y_max).
func TestQuadrant_AscendingYOrder(t *testing.T) {
	vm := NewVoxelMatrix(8, 6, 8)

	// Populate multiple elevations
	for y := 0; y < 6; y++ {
		for z := 0; z < 8; z++ {
			for x := 0; x < 8; x++ {
				_ = vm.SetStateString(x, y, z, "minecraft:oak_planks")
			}
		}
	}

	partitioned := PartitionBlueprint(vm)

	for q, intents := range partitioned {
		if len(intents) == 0 {
			t.Fatalf("quadrant %s has no intents", q)
		}

		var prevY int32 = -1
		for i, intent := range intents {
			currY := intent.TargetVoxel.Y
			if currY < prevY {
				t.Fatalf("Quadrant %s violation of bottom-up ordering at index %d: currY=%d < prevY=%d",
					q, i, currY, prevY)
			}
			prevY = currY

			if intent.IsTemporaryScaffold {
				t.Errorf("expected blueprint block not to be marked as temporary scaffold")
			}
		}
	}
}

// TestQuadrant_SubterraneanBasementPartition verifies that blueprints with subterranean levels (MinY < 0)
// partition bottom-up starting at negative Y layers and accurately preserve directional block properties.
func TestQuadrant_SubterraneanBasementPartition(t *testing.T) {
	bp := &Blueprint{
		ID:      "cellar_tavern",
		Name:    "Cellar Tavern",
		Purpose: PurposeTavern,
		Dimensions: Dimensions{
			Width:  4,
			Height: 4,
			Length: 4,
			MinY:   -2, // Basements at Y = -2 and Y = -1, ground at Y = 0, roof at Y = 1
		},
		Palette: map[int]string{
			1: "minecraft:cobblestone",
			2: "minecraft:spruce_planks",
			3: "minecraft:spruce_stairs[facing=north,half=bottom]",
		},
		Layers: []Layer{
			{
				Y: -2,
				Matrix: [][]int{
					{1, 1, 1, 1},
					{1, 0, 0, 1},
					{1, 0, 0, 1},
					{1, 1, 1, 1},
				},
			},
			{
				Y: -1,
				Matrix: [][]int{
					{1, 1, 1, 1},
					{1, 0, 0, 1},
					{1, 0, 0, 1},
					{1, 1, 1, 1},
				},
			},
			{
				Y: 0,
				Matrix: [][]int{
					{2, 2, 2, 2},
					{2, 0, 0, 2},
					{2, 0, 0, 2},
					{2, 2, 2, 2},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{3, 3, 3, 3},
					{3, 0, 0, 3},
					{3, 0, 0, 3},
					{3, 3, 3, 3},
				},
			},
		},
	}

	vm, err := bp.ToVoxelMatrix()
	if err != nil {
		t.Fatalf("ToVoxelMatrix failed: %v", err)
	}

	anchorY := 64
	partitioned := PartitionBlueprintWithAnchor(vm, 100, anchorY, 200)

	totalBlocks := 0
	for q, intents := range partitioned {
		totalBlocks += len(intents)
		prevY := int32(-9999)
		for _, it := range intents {
			if it.TargetVoxel.Y < prevY {
				t.Errorf("quadrant %s violated bottom-up order: got Y=%d after Y=%d", q, it.TargetVoxel.Y, prevY)
			}
			prevY = it.TargetVoxel.Y

			// Verify anchor offset correctly applied to negative MinY
			expectedMinWorldY := int32(anchorY + bp.Dimensions.MinY)
			if it.TargetVoxel.Y < expectedMinWorldY {
				t.Errorf("target voxel Y=%d below expected minimum world Y=%d", it.TargetVoxel.Y, expectedMinWorldY)
			}

			// Verify stairs preserve directional facing and half properties
			if strings.Contains(it.BlockStateString, "stairs") {
				if !strings.Contains(it.BlockStateString, "facing=north") || !strings.Contains(it.BlockStateString, "half=bottom") {
					t.Errorf("stairs lost directional properties: %s", it.BlockStateString)
				}
			}
		}
	}

	// 12 cobblestone at Y=-2, 12 cobblestone at Y=-1, 12 planks at Y=0, 12 stairs at Y=1 = 48 blocks total
	if totalBlocks != 48 {
		t.Fatalf("expected 48 total blocks partitioned, got %d", totalBlocks)
	}
}

// TestCoordinator_AgentAssignmentAndCollisionPrevention verifies that:
// - Builder agents are assigned exclusively to a single quadrant.
// - An agent cannot claim multiple quadrants simultaneously.
// - Two agents cannot claim the same quadrant simultaneously.
func TestCoordinator_AgentAssignmentAndCollisionPrevention(t *testing.T) {
	vm := NewVoxelMatrix(6, 4, 6)
	_ = vm.SetStateString(0, 0, 0, "minecraft:stone")

	coord, err := NewConstructionCoordinator(CoordinatorConfig{
		ProjectID: "proj-alpha",
		Matrix:    vm,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	// 1. Assign agent-1 to NW
	if err := coord.AssignAgent(QuadrantNW, "agent-1"); err != nil {
		t.Fatalf("failed to assign agent-1 to NW: %v", err)
	}

	// 2. Try to assign agent-1 to NE (must fail: agent cannot occupy multiple quadrants)
	if err := coord.AssignAgent(QuadrantNE, "agent-1"); err == nil {
		t.Fatalf("expected error assigning agent-1 to second quadrant NE, got nil")
	}

	// 3. Try to assign agent-2 to NW (must fail: quadrant already claimed)
	if err := coord.AssignAgent(QuadrantNW, "agent-2"); err == nil {
		t.Fatalf("expected error assigning agent-2 to already claimed quadrant NW, got nil")
	}

	// 4. Assign remaining agents to distinct quadrants
	if err := coord.AssignAgent(QuadrantNE, "agent-2"); err != nil {
		t.Fatalf("failed to assign agent-2 to NE: %v", err)
	}
	if err := coord.AssignAgent(QuadrantSW, "agent-3"); err != nil {
		t.Fatalf("failed to assign agent-3 to SW: %v", err)
	}
	if err := coord.AssignAgent(QuadrantSE, "agent-4"); err != nil {
		t.Fatalf("failed to assign agent-4 to SE: %v", err)
	}

	// Verify assignments
	if coord.GetAssignedAgent(QuadrantNW) != "agent-1" {
		t.Errorf("expected agent-1 on NW, got %s", coord.GetAssignedAgent(QuadrantNW))
	}
	if q, ok := coord.GetAssignedQuadrant("agent-3"); !ok || q != QuadrantSW {
		t.Errorf("expected agent-3 on SW, got (%s, %v)", q, ok)
	}

	// Unassign agent-1 and reassign agent-5
	coord.UnassignAgent("agent-1")
	if err := coord.AssignAgent(QuadrantNW, "agent-5"); err != nil {
		t.Fatalf("failed to assign agent-5 to released NW: %v", err)
	}
}

// TestQuadrant_ScaffoldingBottomUpAndRemovalTopDown verifies Acceptance Criterion 3:
// Scaffolding is placed bottom-up (Y_min -> Y_max) and removed top-down (Y_max -> Y_min).
func TestQuadrant_ScaffoldingBottomUpAndRemovalTopDown(t *testing.T) {
	t.Run("height_under_three_requires_no_scaffolding", func(t *testing.T) {
		vm := NewVoxelMatrix(4, 2, 4)
		_ = vm.SetStateString(1, 1, 1, "minecraft:stone")

		plan := GenerateScaffoldingPlan(vm, QuadrantNW, DefaultScaffoldBlock)
		if len(plan.Placements) != 0 || len(plan.Removals) != 0 {
			t.Errorf("expected 0 scaffolding for height<=2, got %d placements, %d removals",
				len(plan.Placements), len(plan.Removals))
		}
	})

	t.Run("height_above_two_places_bottom_up_and_removes_top_down", func(t *testing.T) {
		vm := NewVoxelMatrix(6, 6, 6)
		// Place a block at elevation Y=4
		_ = vm.SetStateString(1, 4, 1, "minecraft:stone_bricks")

		plan := GenerateScaffoldingPlan(vm, QuadrantNW, "minecraft:scaffolding")
		if len(plan.Placements) == 0 {
			t.Fatalf("expected scaffolding placements for height > 2, got 0")
		}
		if len(plan.Placements) != len(plan.Removals) {
			t.Fatalf("mismatched placement count %d vs removal count %d",
				len(plan.Placements), len(plan.Removals))
		}

		// 1. Verify bottom-up placement order: Y_min -> Y_max
		var prevY int32 = -1
		for i, placement := range plan.Placements {
			if !placement.IsTemporaryScaffold {
				t.Errorf("placement %d missing IsTemporaryScaffold flag", i)
			}
			if placement.BlockStateString != "minecraft:scaffolding" {
				t.Errorf("placement %d has unexpected block %s", i, placement.BlockStateString)
			}

			currY := placement.TargetVoxel.Y
			if currY <= prevY && i > 0 {
				t.Fatalf("Scaffolding placement not strictly ascending Y: idx=%d currY=%d prevY=%d",
					i, currY, prevY)
			}
			prevY = currY
		}

		// 2. Verify top-down removal order: Y_max -> Y_min
		var prevRemovalY int32 = 999999
		for i, pos := range plan.Removals {
			currY := pos.Y
			if currY >= prevRemovalY && i > 0 {
				t.Fatalf("Scaffolding removal not strictly descending Y: idx=%d currY=%d prevY=%d",
					i, currY, prevRemovalY)
			}
			prevRemovalY = currY
		}

		// 3. Verify that Removals exactly reverses Placements positions
		n := len(plan.Placements)
		for i := 0; i < n; i++ {
			placementPos := plan.Placements[i].TargetVoxel
			removalPos := plan.Removals[n-1-i]

			if placementPos.X != removalPos.X || placementPos.Y != removalPos.Y || placementPos.Z != removalPos.Z {
				t.Fatalf("Placement[%d] (%d,%d,%d) does not match reversed Removal[%d] (%d,%d,%d)",
					i, placementPos.X, placementPos.Y, placementPos.Z,
					n-1-i, removalPos.X, removalPos.Y, removalPos.Z)
			}
		}

		// 4. Verify RemovalIntents produces HARVEST_BREAK intents in top-down order
		intents := plan.RemovalIntents()
		if len(intents) != len(plan.Removals) {
			t.Fatalf("expected %d removal intents, got %d", len(plan.Removals), len(intents))
		}
		for i, intent := range intents {
			if intent.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
				t.Errorf("expected HARVEST_BREAK, got %v", intent.InteractionType)
			}
			if intent.Pos.Y != plan.Removals[i].Y {
				t.Errorf("intent pos Y mismatch with removal pos Y")
			}
		}
	})

	t.Run("custom_scaffold_block_dirt_steps", func(t *testing.T) {
		vm := NewVoxelMatrix(4, 5, 4)
		_ = vm.SetStateString(2, 4, 2, "minecraft:stone")

		plan := GenerateScaffoldingPlan(vm, QuadrantSE, "minecraft:dirt")
		for _, p := range plan.Placements {
			if p.BlockStateString != "minecraft:dirt" {
				t.Errorf("expected minecraft:dirt, got %s", p.BlockStateString)
			}
		}
	})
}

// TestQuadrant_ZoneRegistration verifies Acceptance Criterion 4:
// Completed structures register their bounding boxes in functional_zones
// to unlock civic functionality (Tavern, Granary, Market).
func TestQuadrant_ZoneRegistration(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_test.db")

	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	testZones := []struct {
		zoneType string
		name     string
		width    int
		height   int
		length   int
		anchorX  int
		anchorY  int
		anchorZ  int
	}{
		{db.ZoneTypeTavern, "Rusty Pickaxe Tavern", 12, 6, 10, 100, 64, 200},
		{db.ZoneTypeGranary, "Settlement Central Granary", 8, 5, 8, 150, 64, 250},
		{db.ZoneTypeMarket, "Grand Bazaar Market", 16, 4, 16, 300, 64, 400},
	}

	for _, tz := range testZones {
		t.Run(tz.name, func(t *testing.T) {
			vm := NewVoxelMatrix(tz.width, tz.height, tz.length)
			_ = vm.SetStateString(0, 0, 0, "minecraft:cobblestone")

			coord, err := NewConstructionCoordinator(CoordinatorConfig{
				ProjectID: fmt.Sprintf("proj-%s", tz.zoneType),
				Matrix:    vm,
				AnchorX:   tz.anchorX,
				AnchorY:   tz.anchorY,
				AnchorZ:   tz.anchorZ,
				CivicDB:   civicDB,
			})
			if err != nil {
				t.Fatalf("NewConstructionCoordinator failed: %v", err)
			}

			// Simulate completion of all 4 quadrants
			for _, q := range AllQuadrants() {
				coord.MarkQuadrantComplete(q)
			}
			if !coord.IsComplete() {
				t.Fatalf("expected coordinator to be complete")
			}

			// Register the completed zone
			zoneID := fmt.Sprintf("zone-%s-001", tz.zoneType)
			registered, err := coord.RegisterCompletedZone(ctx, RegisterCompletedZoneRequest{
				ZoneID:       zoneID,
				ZoneType:     tz.zoneType,
				Name:         tz.name,
				OwnerAgentID: "agent-architect",
				WorldTick:    5000,
			})
			if err != nil {
				t.Fatalf("RegisterCompletedZone failed: %v", err)
			}

			if registered.ZoneID != zoneID {
				t.Errorf("expected zoneID %s, got %s", zoneID, registered.ZoneID)
			}
			if registered.ZoneType != tz.zoneType {
				t.Errorf("expected zoneType %s, got %s", tz.zoneType, registered.ZoneType)
			}

			// Bounding box verification
			expectedMaxX := tz.anchorX + tz.width - 1
			expectedMaxY := tz.anchorY + tz.height - 1
			expectedMaxZ := tz.anchorZ + tz.length - 1

			if registered.MinX != tz.anchorX || registered.MaxX != expectedMaxX {
				t.Errorf("X bounds mismatch: got [%d, %d], expected [%d, %d]",
					registered.MinX, registered.MaxX, tz.anchorX, expectedMaxX)
			}
			if registered.MinY != tz.anchorY || registered.MaxY != expectedMaxY {
				t.Errorf("Y bounds mismatch: got [%d, %d], expected [%d, %d]",
					registered.MinY, registered.MaxY, tz.anchorY, expectedMaxY)
			}
			if registered.MinZ != tz.anchorZ || registered.MaxZ != expectedMaxZ {
				t.Errorf("Z bounds mismatch: got [%d, %d], expected [%d, %d]",
					registered.MinZ, registered.MaxZ, tz.anchorZ, expectedMaxZ)
			}

			// Verify retrieval directly from civic_registry.db
			fetched, err := civicDB.GetZone(zoneID)
			if err != nil {
				t.Fatalf("GetZone failed: %v", err)
			}
			if fetched == nil {
				t.Fatalf("zone %s not found in civic_registry.db", zoneID)
			}
			if fetched.Name != tz.name {
				t.Errorf("expected fetched name %s, got %s", tz.name, fetched.Name)
			}

			// Verify spatial query
			midX := tz.anchorX + tz.width/2
			midY := tz.anchorY + tz.height/2
			midZ := tz.anchorZ + tz.length/2
			foundZone, err := civicDB.FindZoneAt(midX, midY, midZ)
			if err != nil {
				t.Fatalf("FindZoneAt failed: %v", err)
			}
			if foundZone == nil || foundZone.ZoneID != zoneID {
				t.Fatalf("FindZoneAt inside bounding box failed to locate zone %s", zoneID)
			}

			// Point outside bounding box should not find this zone
			outsideZone, err := civicDB.FindZoneAt(tz.anchorX-10, tz.anchorY, tz.anchorZ)
			if err != nil {
				t.Fatalf("FindZoneAt outside failed: %v", err)
			}
			if outsideZone != nil && outsideZone.ZoneID == zoneID {
				t.Errorf("FindZoneAt found zone outside its bounding box")
			}
		})
	}
}

// TestCoordinator_LifecycleWithTracker verifies full construction coordination with ProjectTracker.
func TestCoordinator_LifecycleWithTracker(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_lifecycle.db")

	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_lifecycle")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	tracker := NewProjectTracker(civicDB)

	// Create test blueprint
	bp := GenerateTemplate("granary", "pioneer")
	projectID := "proj-granary-01"

	// Add stock to granary so BOM is satisfied
	bom := bp.CalculateBOM()
	for item, qty := range bom {
		itemID := NormalizeItemID(item)
		if err := civicDB.AddGranaryStockContext(ctx, itemID, qty+20, 100); err != nil {
			t.Fatalf("AddGranaryStock failed for %s: %v", itemID, err)
		}
	}

	// Register project
	_, err = tracker.RegisterProject(ctx, RegisterProjectRequest{
		ProjectID:   projectID,
		BlueprintID: "blueprint:granary",
		AnchorX:     50,
		AnchorY:     64,
		AnchorZ:     50,
		Quadrant:    db.QuadrantAll,
		WorldTick:   100,
	})
	if err != nil {
		t.Fatalf("RegisterProject failed: %v", err)
	}

	// Transition to BUILDING
	_, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 200, bp)
	if err != nil {
		t.Fatalf("TransitionProject to BUILDING failed: %v", err)
	}

	// Initialize ConstructionCoordinator
	coord, err := NewConstructionCoordinator(CoordinatorConfig{
		ProjectID: projectID,
		Blueprint: bp,
		AnchorX:   50,
		AnchorY:   64,
		AnchorZ:   50,
		CivicDB:   civicDB,
		Tracker:   tracker,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	// Assign 4 builders
	agents := []string{"builder-1", "builder-2", "builder-3", "builder-4"}
	for i, q := range AllQuadrants() {
		if err := coord.AssignAgent(q, agents[i]); err != nil {
			t.Fatalf("AssignAgent %s to %s failed: %v", agents[i], q, err)
		}
	}

	// Complete quadrants
	for _, q := range AllQuadrants() {
		coord.MarkQuadrantComplete(q)
	}

	if !coord.IsComplete() {
		t.Fatalf("expected coordinator to be complete")
	}

	// Register completed zone (which also transitions project to COMPLETED)
	zone, err := coord.RegisterCompletedZone(ctx, RegisterCompletedZoneRequest{
		ZoneType:  db.ZoneTypeGranary,
		Name:      "Pioneer Granary #1",
		WorldTick: 500,
	})
	if err != nil {
		t.Fatalf("RegisterCompletedZone failed: %v", err)
	}
	if zone == nil {
		t.Fatal("expected non-nil functional zone")
	}

	// Verify project status is COMPLETED
	proj, err := tracker.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if proj.Status != StateCompleted {
		t.Errorf("expected project status COMPLETED, got %s", proj.Status)
	}
	if proj.ProgressPercent != 100.0 {
		t.Errorf("expected progress 100.0, got %f", proj.ProgressPercent)
	}
}

// TestCoordinator_ConcurrentAccess ensures race-free execution under concurrent reads and writes.
func TestCoordinator_ConcurrentAccess(t *testing.T) {
	vm := NewVoxelMatrix(8, 4, 8)
	_ = vm.SetStateString(2, 2, 2, "minecraft:stone")

	coord, err := NewConstructionCoordinator(CoordinatorConfig{
		ProjectID: "proj-concurrent",
		Matrix:    vm,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	var wg sync.WaitGroup
	quadrants := AllQuadrants()

	// Concurrent assignment and inspection
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			q := quadrants[idx%len(quadrants)]
			agent := fmt.Sprintf("agent-%d", idx)

			_ = coord.AssignAgent(q, agent)
			_ = coord.GetAssignedAgent(q)
			_ = coord.GetIntents(q)
			_ = coord.GetScaffoldingPlan(q)
			if idx%3 == 0 {
				coord.MarkQuadrantComplete(q)
			}
			_ = coord.IsQuadrantComplete(q)
			_ = coord.IsComplete()
		}(i)
	}

	wg.Wait()
}
