package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestClassifyRiverFord(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// Create a shallow 1-block wide water channel at X=2, flanked by grass blocks at X=1 and X=3
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:grass_block"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:water"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 1}, BlockId: "minecraft:water"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: -1}, BlockId: "minecraft:water"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:dirt"},
	}

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeRiverFord {
		t.Fatalf("expected TerrainTypeRiverFord, got %s", profile.Type)
	}
	if profile.FordTarget == nil {
		t.Fatal("expected non-nil FordTarget")
	}
	if profile.FordTarget.X < 2.0 {
		t.Errorf("expected FordTarget across stream (>2.0), got %f", profile.FordTarget.X)
	}
}

func TestClassifyValley(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// Valley running East-West (trough at Z=0, Y=64), flanked by North wall (Z=-4, Y=68) and South wall (Z=+4, Y=68)
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 68, Z: -4}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 68, Z: 4}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:dirt_path"},
		{Pos: &civquestv1.BlockPosProto{X: -4, Y: 64, Z: 0}, BlockId: "minecraft:dirt_path"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}, BlockId: "minecraft:grass_block"},
	}

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeValley {
		t.Fatalf("expected TerrainTypeValley, got %s", profile.Type)
	}
	if profile.ValleyAxisX != 1.0 || profile.ValleyAxisZ != 0.0 {
		t.Errorf("expected East-West valley axis (1, 0), got (%f, %f)", profile.ValleyAxisX, profile.ValleyAxisZ)
	}
}

func TestClassifyRidge(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 70.0, Z: 0.5}

	// Ridge running East-West (crest at Z=0, Y=70), with slopes dropping to North (Z=-4, Y=66) and South (Z=+4, Y=66)
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 66, Z: -4}, BlockId: "minecraft:grass_block"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 66, Z: 4}, BlockId: "minecraft:grass_block"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 70, Z: 0}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: -4, Y: 70, Z: 0}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 70, Z: 0}, BlockId: "minecraft:stone"},
	}

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeRidge {
		t.Fatalf("expected TerrainTypeRidge, got %s", profile.Type)
	}
	if profile.RidgeAxisX != 1.0 || profile.RidgeAxisZ != 0.0 {
		t.Errorf("expected East-West ridge axis (1, 0), got (%f, %f)", profile.RidgeAxisX, profile.RidgeAxisZ)
	}
}

func TestClassifyDepression(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// Sunken sinkhole with floor at Y=60 within 4 blocks of agent
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 60, Z: 2}, BlockId: "minecraft:cobblestone"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 60, Z: 2}, BlockId: "minecraft:gravel"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 60, Z: 3}, BlockId: "minecraft:dirt"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 60, Z: 3}, BlockId: "minecraft:cobblestone"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}, BlockId: "minecraft:grass_block"},
	}

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeDepression {
		t.Fatalf("expected TerrainTypeDepression, got %s", profile.Type)
	}
	if profile.DepressionCenter == nil {
		t.Fatal("expected non-nil DepressionCenter")
	}
}

func TestClassifyDenseVegetation(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// 12 leaf and vine blocks clustered around agent eye level
	var blocks []*civquestv1.VisibleBlock
	for i := int32(0); i < 12; i++ {
		blocks = append(blocks, &civquestv1.VisibleBlock{
			Pos:     &civquestv1.BlockPosProto{X: i % 4, Y: 64 + (i % 3), Z: i / 4},
			BlockId: "minecraft:oak_leaves",
		})
	}

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeDenseVegetation {
		t.Fatalf("expected TerrainTypeDenseVegetation, got %s", profile.Type)
	}
}

func TestClassifyFlatlandsWithDistantPOI(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// 14 flat surface blocks at Y=64 plus a distant oak log at X=12, Y=64, Z=8
	var blocks []*civquestv1.VisibleBlock
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-1); z <= 1; z++ {
			blocks = append(blocks, &civquestv1.VisibleBlock{
				Pos:     &civquestv1.BlockPosProto{X: x, Y: 64, Z: z},
				BlockId: "minecraft:grass_block",
			})
		}
	}
	blocks = append(blocks, &civquestv1.VisibleBlock{
		Pos:     &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 8},
		BlockId: "minecraft:oak_log",
	})

	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
	}

	profile := ClassifyTerrain(frame, agentPos)
	if profile.Type != TerrainTypeFlatlands {
		t.Fatalf("expected TerrainTypeFlatlands, got %s (variance=%f)", profile.Type, profile.ElevationVariance)
	}
	if profile.DistantPOITarget == nil {
		t.Fatal("expected non-nil DistantPOITarget")
	}
	if profile.DistantPOITarget.X < 10.0 {
		t.Errorf("expected distant POI target near X=12.5, got %f", profile.DistantPOITarget.X)
	}
}

func TestEvaluateTerrainExplorationIntents(t *testing.T) {
	agentPos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}
	state := NewAgentState()

	// Valley terrain
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 68, Z: -4}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 68, Z: 4}, BlockId: "minecraft:stone"},
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:dirt"},
		{Pos: &civquestv1.BlockPosProto{X: -4, Y: 64, Z: 0}, BlockId: "minecraft:dirt"},
		{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}, BlockId: "minecraft:grass_block"},
	}
	frame := &civquestv1.TelemetryFrame{
		InterestingBlocks: blocks,
		Status:            &civquestv1.AgentStatus{Position: agentPos},
		WorldTick:         100,
	}

	intent := EvaluateTerrainExploration("Able", frame, agentPos, state, nil)
	if intent == nil {
		t.Fatal("expected non-nil intent for valley exploration")
	}
	nav := intent.GetNavigate()
	if nav == nil {
		t.Fatal("expected NavigateIntent")
	}
	if nav.Target.X <= agentPos.X {
		t.Errorf("expected target to advance East along valley, got X=%f", nav.Target.X)
	}
	if state.GetGoals().ImmediateGoal == "" || state.GetGoals().ImmediateGoal[:14] != "VALLEY_HIGHWAY" {
		t.Errorf("expected VALLEY_HIGHWAY goal, got %s", state.GetGoals().ImmediateGoal)
	}
}
