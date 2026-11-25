package system1

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestConstructionSkill_RegistrationAndPreconditions(t *testing.T) {
	reg := NewDefaultEngineRegistry()
	skill, ok := reg.GetSkill("blueprint_construction")
	if !ok || skill == nil {
		t.Fatalf("expected 'blueprint_construction' skill registered in NewDefaultEngineRegistry()")
	}
	if skill.Category() != "construction" {
		t.Errorf("expected Category 'construction', got %q", skill.Category())
	}
	if !skill.Interruptible() {
		t.Errorf("expected construction skill to be interruptible by emergency reflexes")
	}

	fg := NewFactGraph()
	if skill.Preconditions(fg) {
		t.Errorf("preconditions should be false when no project is active")
	}

	fg.SetBool("construction.active", "construction", true, "project started")
	if !skill.Preconditions(fg) {
		t.Errorf("preconditions should be true when construction.active is set")
	}
}

func TestConstructionSkill_QuadrantExecutionAndSubterraneanExcavation(t *testing.T) {
	tmpDir := t.TempDir()
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "civic.db"), "clan_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	// Blueprint with subterranean basement at Y=-1, ground at Y=0
	bp := &blueprints.Blueprint{
		ID:      "test_structure",
		Name:    "Test Structure",
		Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{
			Width:  4,
			Height: 2,
			Length: 4,
			MinY:   -1,
		},
		Palette: map[int]string{
			1: "minecraft:cobblestone",
			2: "#minecraft:stairs[facing=east,half=bottom]",
		},
		Layers: []blueprints.Layer{
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
		},
	}

	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-101",
		Blueprint: bp,
		AnchorX:   100,
		AnchorY:   64,
		AnchorZ:   200,
		CivicDB:   civicDB,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	skill := NewConstructionSkill(coord)
	fg := NewFactGraph()
	state := NewAgentState()

	// 1. Far away from build site -> should emit NavigateIntent
	farFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 50.0, Y: 64.0, Z: 50.0},
		},
	}
	ctxFar := &SkillContext{
		AgentID: "builder-1",
		Frame:   farFrame,
		State:   state,
		CivicDB: civicDB,
	}

	intents, err := skill.Execute(ctxFar, fg)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(intents) != 1 || intents[0].GetNavigate() == nil {
		t.Fatalf("expected navigate intent when builder is far from site, got %+v", intents)
	}

	// 2. Near build site with subterranean natural terrain obstruction -> should emit HARVEST_BREAK excavation
	nearFrame := &civquestv1.TelemetryFrame{
		WorldTick: 105,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 100.0, Y: 64.0, Z: 200.0},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cobblestone", Count: 64},
				{ItemId: "minecraft:oak_stairs", Count: 64}, // in-situ substitution for #minecraft:stairs
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 100, Y: 63, Z: 200}, // AnchorY (64) + MinY (-1) = 63
				BlockId: "minecraft:dirt",                                 // Obstructing natural terrain
			},
		},
	}
	ctxNear := &SkillContext{
		AgentID: "builder-1",
		Frame:   nearFrame,
		State:   state,
		CivicDB: civicDB,
	}

	digIntents, err := skill.Execute(ctxNear, fg)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(digIntents) != 1 || digIntents[0].GetInteractBlock() == nil {
		t.Fatalf("expected interact block intent, got %+v", digIntents)
	}
	dig := digIntents[0].GetInteractBlock()
	if dig.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Errorf("expected HARVEST_BREAK interaction for subterranean obstruction, got %v", dig.InteractionType)
	}
	if dig.Pos.Y != 63 {
		t.Errorf("expected excavation at subterranean elevation Y=63, got %d", dig.Pos.Y)
	}

	// 3. Obstruction cleared (air present) -> should emit PLACE_BLOCK with in-situ taxonomy and directional preservation
	clearedFrame := &civquestv1.TelemetryFrame{
		WorldTick: 110,
		Status:    nearFrame.Status,
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 100, Y: 63, Z: 200},
				BlockId: "minecraft:air",
			},
		},
	}
	ctxCleared := &SkillContext{
		AgentID: "builder-1",
		Frame:   clearedFrame,
		State:   state,
		CivicDB: civicDB,
	}

	placeIntents, err := skill.Execute(ctxCleared, fg)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(placeIntents) != 1 || placeIntents[0].GetInteractBlock() == nil {
		t.Fatalf("expected place block intent, got %+v", placeIntents)
	}
	place := placeIntents[0].GetInteractBlock()
	if place.InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Errorf("expected PLACE_BLOCK, got %v", place.InteractionType)
	}
	if place.BlockOrItemId != "minecraft:cobblestone" {
		t.Errorf("expected cobblestone placement at Y=63, got %s", place.BlockOrItemId)
	}

	// Verify progress advanced
	if state.ConstructionProgress() != 1 {
		t.Errorf("expected progress = 1, got %d", state.ConstructionProgress())
	}
}

func TestConstructionSkill_MultiBuilderCollaborationAndCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "civic.db"), "clan_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	bp := &blueprints.Blueprint{
		ID:      "small_shack",
		Name:    "Small Shack",
		Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{
			Width:  2,
			Height: 1,
			Length: 2,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "#minecraft:stairs[facing=north,half=bottom]",
		},
		Layers: []blueprints.Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1},
					{1, 1},
				},
			},
		},
	}

	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-shack",
		Blueprint: bp,
		AnchorX:   0,
		AnchorY:   64,
		AnchorZ:   0,
		CivicDB:   civicDB,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	skill := NewConstructionSkill(coord)
	fg := NewFactGraph()

	// 4 builder agents should claim 4 distinct quadrants: NW, NE, SW, SE
	builders := []string{"builder-NW", "builder-NE", "builder-SW", "builder-SE"}
	for _, b := range builders {
		bState := NewAgentState()
		frame := &civquestv1.TelemetryFrame{
			WorldTick: 200,
			Status: &civquestv1.AgentStatus{
				Position: &civquestv1.Vec3{X: 1.0, Y: 64.0, Z: 1.0},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:spruce_stairs", Count: 10}, // in-situ substitution
				},
			},
		}
		ctx := &SkillContext{
			AgentID: b,
			Frame:   frame,
			State:   bState,
			CivicDB: civicDB,
		}

		// Execute placement for single block in quadrant
		intents, err := skill.Execute(ctx, fg)
		if err != nil {
			t.Fatalf("builder %s failed: %v", b, err)
		}
		if len(intents) != 1 || intents[0].GetInteractBlock() == nil {
			t.Fatalf("builder %s expected place intent, got %+v", b, intents)
		}
		placed := intents[0].GetInteractBlock()
		// Verify directional properties were transferred to substitute
		if !strings.Contains(placed.BlockOrItemId, "facing=north") || !strings.Contains(placed.BlockOrItemId, "half=bottom") {
			t.Errorf("builder %s: stairs lost directional properties: %s", b, placed.BlockOrItemId)
		}

		// Next step completes the quadrant
		_, _ = skill.Execute(ctx, fg)
	}

	// Verify all quadrants completed
	if !coord.IsComplete() {
		t.Errorf("expected coordinator to be complete after all quadrants finished")
	}

	// Verify functional zone registered in CivicDB
	zones, err := civicDB.ListZones()
	if err != nil {
		t.Fatalf("ListZones failed: %v", err)
	}
	if len(zones) == 0 {
		t.Errorf("expected functional zone registered in CivicDB upon completion")
	}
}

func TestConstructionSkill_PerAgentStateIsolation(t *testing.T) {
	bp := &blueprints.Blueprint{
		ID:      "isolated_platform",
		Name:    "Isolated Platform",
		Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{
			Width:  2,
			Height: 2,
			Length: 2,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "minecraft:cobblestone",
		},
		Layers: []blueprints.Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1},
					{1, 1},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{1, 1},
					{1, 1},
				},
			},
		},
	}

	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-isolation",
		Blueprint: bp,
		AnchorX:   0,
		AnchorY:   64,
		AnchorZ:   0,
	})
	if err != nil {
		t.Fatalf("NewConstructionCoordinator failed: %v", err)
	}

	// Single shared ConstructionSkill instance across concurrent agents with distinct AgentState instances.
	skill := NewConstructionSkill(coord)
	fg := NewFactGraph()

	stateA := NewAgentState()
	stateB := NewAgentState()
	// Seed pre-existing non-zero progress to confirm quadrant assignment resets each agent's own state independently.
	stateA.SetConstructionProgress(99)
	stateB.SetConstructionProgress(42)

	makeCtx := func(agentID string, st *AgentState) *SkillContext {
		return &SkillContext{
			AgentID: agentID,
			State:   st,
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 300,
				Status: &civquestv1.AgentStatus{
					Position: &civquestv1.Vec3{X: 1.0, Y: 64.0, Z: 1.0},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:cobblestone", Count: 64},
					},
				},
			},
		}
	}

	var wg sync.WaitGroup
	var firstStepWg sync.WaitGroup
	firstStepWg.Add(2)
	var placedA, placedB int
	runBuilder := func(agentID string, st *AgentState, placed *int) {
		defer wg.Done()
		ctx := makeCtx(agentID, st)
		for i := 0; i < 16 && !coord.IsComplete(); i++ {
			intents, execErr := skill.Execute(ctx, fg)
			if execErr != nil {
				t.Errorf("%s Execute error: %v", agentID, execErr)
				if i == 0 {
					firstStepWg.Done()
				}
				return
			}
			for _, in := range intents {
				if in.GetInteractBlock() != nil && in.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
					*placed++
				}
			}
			if i == 0 {
				firstStepWg.Done()
				firstStepWg.Wait()
			}
		}
	}

	wg.Add(2)
	go runBuilder("builder-A", stateA, &placedA)
	go runBuilder("builder-B", stateB, &placedB)
	wg.Wait()

	if !coord.IsComplete() {
		t.Fatalf("expected coordinator to complete across concurrent builders")
	}
	if placedA+placedB != 8 {
		t.Fatalf("expected 8 total placed blocks (placedA=%d, placedB=%d)", placedA, placedB)
	}
	if placedA == 0 || placedB == 0 {
		t.Fatalf("expected both builders to place blocks independently (placedA=%d, placedB=%d)", placedA, placedB)
	}
	if stateA.ConstructionProgress() == 99 || stateB.ConstructionProgress() == 42 {
		t.Fatalf("expected seeded progress reset on quadrant assignment (A=%d, B=%d)",
			stateA.ConstructionProgress(), stateB.ConstructionProgress())
	}
}
