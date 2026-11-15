package blueprints

import (
	"math"
	"path/filepath"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestBlueprint_GenerateTemplateAndValidate(t *testing.T) {
	bp := GenerateTemplate("tavern", "pioneer")
	if err := bp.Validate(); err != nil {
		t.Fatalf("generated template failed validation: %v", err)
	}

	if bp.Dimensions.Width != 7 || bp.Dimensions.Height != 5 || bp.Dimensions.Length != 7 {
		t.Errorf("unexpected dimensions: %+v", bp.Dimensions)
	}

	bom := bp.CalculateBOM()
	if len(bom) == 0 {
		t.Errorf("expected non-empty BOM")
	}

	if bom["minecraft:cobblestone"] <= 0 {
		t.Errorf("expected cobblestone in BOM, got %d", bom["minecraft:cobblestone"])
	}

	// Test Save and Load
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "tavern.yaml")
	if err := bp.SaveYAML(path); err != nil {
		t.Fatalf("failed saving blueprint: %v", err)
	}

	loaded, err := LoadYAML(path)
	if err != nil {
		t.Fatalf("failed loading blueprint: %v", err)
	}

	if loaded.Name != bp.Name {
		t.Errorf("expected name %q, got %q", bp.Name, loaded.Name)
	}
}

func TestBlueprint_ValidationErrors(t *testing.T) {
	// 1. Nil blueprint
	var nilBp *Blueprint
	if err := nilBp.Validate(); err != ErrNilBlueprint {
		t.Errorf("expected ErrNilBlueprint, got %v", err)
	}

	// 2. Empty name
	bp := &Blueprint{
		Name:       "",
		Dimensions: Dimensions{Width: 5, Height: 5, Length: 5},
	}
	if err := bp.Validate(); err != ErrEmptyName {
		t.Errorf("expected ErrEmptyName, got %v", err)
	}

	// 3. Invalid dimensions
	bp.Name = "Test"
	bp.Dimensions.Width = 0
	if err := bp.Validate(); err == nil {
		t.Errorf("expected error for 0 width")
	}

	// 4. Layer out of bounds
	bp.Dimensions.Width = 3
	bp.Dimensions.Height = 3
	bp.Dimensions.Length = 3
	bp.Palette = map[int]string{1: "minecraft:stone"}
	bp.Layers = []Layer{
		{Y: 4, Matrix: [][]int{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
	}
	if err := bp.Validate(); err == nil {
		t.Errorf("expected error for layer y=4 > height=3")
	}

	// 5. Layer matrix mismatch
	bp.Layers = []Layer{
		{Y: 0, Matrix: [][]int{{1, 1}, {1, 1}}}, // Expected 3x3
	}
	if err := bp.Validate(); err == nil {
		t.Errorf("expected error for matrix dimension mismatch")
	}
}

func TestIsCollidingWithVoxel(t *testing.T) {
	target := &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}

	// Case 1: Agent directly centered in voxel -> collides
	posCentered := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}
	if !IsCollidingWithVoxel(posCentered, target) {
		t.Errorf("expected collision when centered in voxel")
	}

	// Case 2: Agent standing on block at Y=64 (pos.Y=65.0) within horizontal boundary -> collides
	posAbove := &civquestv1.Vec3{X: 0.5, Y: 65.0, Z: 0.5}
	if !IsCollidingWithVoxel(posAbove, target) {
		t.Errorf("expected collision when standing directly above block")
	}

	// Case 3: Agent standing far outside horizontally -> no collision
	posOutsideH := &civquestv1.Vec3{X: 3.5, Y: 64.0, Z: 0.5}
	if IsCollidingWithVoxel(posOutsideH, target) {
		t.Errorf("expected no collision when 3.5m away horizontally")
	}

	// Case 4: Agent standing far above vertically -> no collision
	posOutsideV := &civquestv1.Vec3{X: 0.5, Y: 68.0, Z: 0.5}
	if IsCollidingWithVoxel(posOutsideV, target) {
		t.Errorf("expected no collision when 4m above vertically")
	}
}

func TestCalculateCabinStepBack(t *testing.T) {
	shelterCenter := &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	targetVoxel := &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	pos := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	stepBack := CalculateCabinStepBack(pos, targetVoxel, shelterCenter)
	if stepBack == nil {
		t.Fatalf("expected non-nil stepBack coordinate")
	}

	// Distance from cabin center should be 3.5 (outside 2.5m construction boundary)
	dist := math.Hypot(stepBack.X-0.5, stepBack.Z-0.5)
	if math.Abs(dist-3.5) > 0.1 {
		t.Errorf("expected stepBack at distance 3.5m from shelter center, got %f", dist)
	}
}
