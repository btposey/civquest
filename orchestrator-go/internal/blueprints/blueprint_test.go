package blueprints

import (
	"path/filepath"
	"testing"
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
