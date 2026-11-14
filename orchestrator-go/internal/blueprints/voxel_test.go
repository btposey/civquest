package blueprints

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlockState_ParsingAndFormatting(t *testing.T) {
	// 1. Basic block state
	bs1 := ParseBlockState("stone")
	if bs1.Identifier != "minecraft:stone" {
		t.Errorf("expected minecraft:stone, got %s", bs1.Identifier)
	}
	if bs1.String() != "minecraft:stone" {
		t.Errorf("expected string representation minecraft:stone, got %s", bs1.String())
	}
	if bs1.IsAir() {
		t.Errorf("stone should not be air")
	}

	// 2. Block state with properties
	bs2 := ParseBlockState("minecraft:oak_stairs[facing=north,half=bottom,waterlogged=false]")
	if bs2.Identifier != "minecraft:oak_stairs" {
		t.Errorf("expected minecraft:oak_stairs, got %s", bs2.Identifier)
	}
	if bs2.Properties["facing"] != "north" || bs2.Properties["half"] != "bottom" || bs2.Properties["waterlogged"] != "false" {
		t.Errorf("unexpected properties: %+v", bs2.Properties)
	}
	if bs2.BaseItem() != "minecraft:oak_stairs" {
		t.Errorf("expected base item minecraft:oak_stairs, got %s", bs2.BaseItem())
	}
	// Properties should be sorted alphabetically in String()
	expectedStr := "minecraft:oak_stairs[facing=north,half=bottom,waterlogged=false]"
	if bs2.String() != expectedStr {
		t.Errorf("expected %s, got %s", expectedStr, bs2.String())
	}

	// 3. Air states
	for _, airStr := range []string{"minecraft:air", "minecraft:cave_air", "air", ""} {
		air := ParseBlockState(airStr)
		if !air.IsAir() {
			t.Errorf("expected %q to be air", airStr)
		}
	}

	// 4. Equality check
	bs3 := ParseBlockState("minecraft:oak_stairs[half=bottom,facing=north,waterlogged=false]")
	if !bs2.Equals(bs3) {
		t.Errorf("expected bs2 and bs3 to be equal despite property order")
	}
}

func TestVoxelMatrix_OperationsAndBounds(t *testing.T) {
	vm := NewVoxelMatrix(4, 3, 4)

	// Check dimensions
	if vm.Dimensions.Width != 4 || vm.Dimensions.Height != 3 || vm.Dimensions.Length != 4 {
		t.Fatalf("unexpected dimensions: %+v", vm.Dimensions)
	}

	// Default should be air
	st, err := vm.Get(0, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !st.IsAir() {
		t.Errorf("initial state should be air, got %s", st)
	}

	// Set valid voxel
	stairs := ParseBlockState("minecraft:oak_stairs[facing=south]")
	if err := vm.Set(1, 1, 1, stairs); err != nil {
		t.Fatalf("failed setting voxel: %v", err)
	}

	got, err := vm.Get(1, 1, 1)
	if err != nil {
		t.Fatalf("failed getting voxel: %v", err)
	}
	if !got.Equals(stairs) {
		t.Errorf("expected %+v, got %+v", stairs, got)
	}

	// Out of bounds checks
	if err := vm.Set(-1, 0, 0, stairs); err == nil {
		t.Errorf("expected out of bounds error for negative X")
	}
	if err := vm.Set(0, 3, 0, stairs); err == nil {
		t.Errorf("expected out of bounds error for Y == Height")
	}
	if err := vm.Set(0, 0, 4, stairs); err == nil {
		t.Errorf("expected out of bounds error for Z == Length")
	}
	if _, err := vm.Get(4, 0, 0); err == nil {
		t.Errorf("expected out of bounds error on Get")
	}

	// Clone check
	cloned := vm.Clone()
	if cloned == nil {
		t.Fatalf("clone was nil")
	}
	clonedGot, err := cloned.Get(1, 1, 1)
	if err != nil || !clonedGot.Equals(stairs) {
		t.Errorf("cloned matrix did not match original: %+v", clonedGot)
	}
}

func TestVoxelMatrix_BlueprintBidirectionalConversion(t *testing.T) {
	bp := GenerateTemplate("house", "pioneer")
	vm, err := FromBlueprint(bp)
	if err != nil {
		t.Fatalf("FromBlueprint failed: %v", err)
	}

	// Compare BOM of blueprint and voxel matrix
	bpBOM := bp.CalculateBOM()
	vmBOM := vm.CalculateBOM()

	if len(bpBOM) != len(vmBOM) {
		t.Fatalf("BOM length mismatch: bp has %d, vm has %d", len(bpBOM), len(vmBOM))
	}
	for k, count := range bpBOM {
		if vmBOM[k] != count {
			t.Errorf("BOM mismatch for item %s: bp=%d, vm=%d", k, count, vmBOM[k])
		}
	}

	// Convert back to Blueprint
	convertedBp := vm.ToBlueprint("Pioneer House", "house", "pioneer", "tester")
	if err := convertedBp.Validate(); err != nil {
		t.Fatalf("converted blueprint failed validation: %v", err)
	}

	convBOM := convertedBp.CalculateBOM()
	for k, count := range bpBOM {
		if convBOM[k] != count {
			t.Errorf("re-converted blueprint BOM mismatch for item %s: original=%d, re-converted=%d", k, count, convBOM[k])
		}
	}
}

func TestVoxelMatrix_ParseYAML(t *testing.T) {
	// Direct 3D matrix YAML
	yamlContent := `
name: "Small Outpost"
type: "watchtower"
style: "stone"
dimensions:
  width: 3
  height: 2
  length: 3
palette:
  0: "minecraft:air"
  1: "minecraft:cobblestone"
  2: "minecraft:torch"
matrix:
  - - [1, 1, 1]
    - [1, 0, 1]
    - [1, 1, 1]
  - - [2, 0, 2]
    - [0, 0, 0]
    - [2, 0, 2]
`
	vm, err := ParseYAMLMatrix([]byte(yamlContent))
	if err != nil {
		t.Fatalf("ParseYAMLMatrix failed: %v", err)
	}

	if vm.Dimensions.Width != 3 || vm.Dimensions.Height != 2 || vm.Dimensions.Length != 3 {
		t.Fatalf("unexpected dimensions: %+v", vm.Dimensions)
	}

	cobble, _ := vm.Get(0, 0, 0)
	if cobble.Identifier != "minecraft:cobblestone" {
		t.Errorf("expected cobblestone at (0,0,0), got %s", cobble)
	}

	torch, _ := vm.Get(0, 1, 0)
	if torch.Identifier != "minecraft:torch" {
		t.Errorf("expected torch at (0,1,0), got %s", torch)
	}

	air, _ := vm.Get(1, 0, 1)
	if !air.IsAir() {
		t.Errorf("expected air at (1,0,1), got %s", air)
	}

	bom := vm.CalculateBOM()
	if bom["minecraft:cobblestone"] != 8 {
		t.Errorf("expected 8 cobblestone, got %d", bom["minecraft:cobblestone"])
	}
	if bom["minecraft:torch"] != 4 {
		t.Errorf("expected 4 torches, got %d", bom["minecraft:torch"])
	}

	// Test LoadYAMLMatrix from file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "outpost.yaml")
	if err := osWrite(filePath, []byte(yamlContent)); err != nil {
		t.Fatalf("failed to write test YAML: %v", err)
	}

	loadedVM, err := LoadYAMLMatrix(filePath)
	if err != nil {
		t.Fatalf("LoadYAMLMatrix failed: %v", err)
	}
	if loadedVM.Dimensions != vm.Dimensions {
		t.Errorf("loaded dimensions mismatch")
	}
}

func TestSchematic_SerializationAndParsingRoundtrip(t *testing.T) {
	vm := NewVoxelMatrix(3, 3, 3)
	_ = vm.SetStateString(0, 0, 0, "minecraft:stone")
	_ = vm.SetStateString(1, 0, 1, "minecraft:oak_log[axis=y]")
	_ = vm.SetStateString(2, 2, 2, "minecraft:oak_stairs[facing=north,half=bottom]")

	tmpDir := t.TempDir()
	schemPath := filepath.Join(tmpDir, "test.schem")

	if err := SaveSchematic(schemPath, vm); err != nil {
		t.Fatalf("SaveSchematic failed: %v", err)
	}

	loaded, err := LoadSchematic(schemPath)
	if err != nil {
		t.Fatalf("LoadSchematic failed: %v", err)
	}

	if loaded.Dimensions.Width != 3 || loaded.Dimensions.Height != 3 || loaded.Dimensions.Length != 3 {
		t.Fatalf("unexpected schematic dimensions: %+v", loaded.Dimensions)
	}

	s0, _ := loaded.Get(0, 0, 0)
	if s0.Identifier != "minecraft:stone" {
		t.Errorf("expected stone, got %s", s0)
	}

	s1, _ := loaded.Get(1, 0, 1)
	if s1.Identifier != "minecraft:oak_log" || s1.Properties["axis"] != "y" {
		t.Errorf("expected oak_log[axis=y], got %s", s1)
	}

	s2, _ := loaded.Get(2, 2, 2)
	if s2.Identifier != "minecraft:oak_stairs" || s2.Properties["facing"] != "north" || s2.Properties["half"] != "bottom" {
		t.Errorf("expected oak_stairs[facing=north,half=bottom], got %s", s2)
	}
}

func osWrite(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
