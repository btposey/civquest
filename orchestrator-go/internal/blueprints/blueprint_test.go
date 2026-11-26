package blueprints

import (
	"math"
	"os"
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

	if bp.Purpose != PurposeTavern {
		t.Errorf("expected purpose %q, got %q", PurposeTavern, bp.Purpose)
	}

	if bp.ID != "pioneer_tavern" {
		t.Errorf("expected ID %q, got %q", "pioneer_tavern", bp.ID)
	}

	bom := bp.CalculateBOM()
	if len(bom) == 0 {
		t.Errorf("expected non-empty BOM")
	}

	if bom["minecraft:cobblestone"] <= 0 {
		t.Errorf("expected cobblestone in BOM, got %d", bom["minecraft:cobblestone"])
	}

	// Test Save and Load YAML
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "tavern.yaml")
	if err := bp.SaveYAML(path); err != nil {
		t.Fatalf("failed saving blueprint YAML: %v", err)
	}

	loaded, err := LoadYAML(path)
	if err != nil {
		t.Fatalf("failed loading blueprint YAML: %v", err)
	}

	if loaded.Name != bp.Name {
		t.Errorf("expected name %q, got %q", bp.Name, loaded.Name)
	}
	if loaded.ID != bp.ID {
		t.Errorf("expected ID %q, got %q", bp.ID, loaded.ID)
	}
	if loaded.Purpose != bp.Purpose {
		t.Errorf("expected Purpose %q, got %q", bp.Purpose, loaded.Purpose)
	}
}

func TestBlueprint_JSONSerializationRoundtrip(t *testing.T) {
	bp := GenerateTemplate("storehouse", "pioneer")
	bp.Description = "Pioneer settlement storehouse"
	bp.Tags = []string{"storage", "pioneer"}

	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "storehouse.json")

	// Test SaveJSON
	if err := bp.SaveJSON(jsonPath); err != nil {
		t.Fatalf("SaveJSON failed: %v", err)
	}

	// Test LoadJSON
	loaded, err := LoadJSON(jsonPath)
	if err != nil {
		t.Fatalf("LoadJSON failed: %v", err)
	}

	if loaded.ID != bp.ID {
		t.Errorf("expected ID %q, got %q", bp.ID, loaded.ID)
	}
	if loaded.Purpose != PurposeStorehouse {
		t.Errorf("expected Purpose %q, got %q", PurposeStorehouse, loaded.Purpose)
	}
	if loaded.Description != bp.Description {
		t.Errorf("expected Description %q, got %q", bp.Description, loaded.Description)
	}
	if len(loaded.Tags) != len(bp.Tags) {
		t.Errorf("expected %d tags, got %d", len(bp.Tags), len(loaded.Tags))
	}
	if len(loaded.BOM) == 0 {
		t.Errorf("expected populated BOM after LoadJSON")
	}

	// Test LoadFile auto-detection
	loadFileLoaded, err := LoadFile(jsonPath)
	if err != nil {
		t.Fatalf("LoadFile on JSON failed: %v", err)
	}
	if loadFileLoaded.Name != bp.Name {
		t.Errorf("expected name %q, got %q", bp.Name, loadFileLoaded.Name)
	}

	// Test SaveFile auto-detection
	yamlPath := filepath.Join(tmpDir, "storehouse.yaml")
	if err := bp.SaveFile(yamlPath); err != nil {
		t.Fatalf("SaveFile on YAML failed: %v", err)
	}

	yamlLoaded, err := LoadFile(yamlPath)
	if err != nil {
		t.Fatalf("LoadFile on YAML failed: %v", err)
	}
	if yamlLoaded.ID != bp.ID {
		t.Errorf("expected ID %q, got %q", bp.ID, yamlLoaded.ID)
	}
}

func TestBlueprint_ValidationDefaultsAndBOM(t *testing.T) {
	// 1. Defaults ID to slugified Name and Purpose to HOME
	bp := &Blueprint{
		Name: "Rustic Cozy Home",
		Dimensions: Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
		},
		Palette: map[int]string{
			1: "minecraft:oak_planks",
		},
		Layers: []Layer{
			{Y: 0, Matrix: [][]int{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
			{Y: 1, Matrix: [][]int{{1, 0, 1}, {0, 0, 0}, {1, 0, 1}}},
		},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	if bp.ID != "rustic_cozy_home" {
		t.Errorf("expected slugified ID 'rustic_cozy_home', got %q", bp.ID)
	}
	if bp.Purpose != PurposeHome {
		t.Errorf("expected default Purpose %q, got %q", PurposeHome, bp.Purpose)
	}
	if bp.Type != "home" {
		t.Errorf("expected Type synced to 'home', got %q", bp.Type)
	}

	// Verify BOM was populated automatically
	if len(bp.BOM) == 0 {
		t.Fatalf("expected BOM to be populated automatically on Validate()")
	}
	// 9 blocks on layer 0 + 4 blocks on layer 1 = 13 oak_planks
	if bp.BOM["minecraft:oak_planks"] != 13 {
		t.Errorf("expected 13 oak_planks in BOM, got %d", bp.BOM["minecraft:oak_planks"])
	}
}

func TestBlueprint_GranaryAliasNormalization(t *testing.T) {
	bp := &Blueprint{
		Name:    "Valley Granary",
		Purpose: "GRANARY",
		Dimensions: Dimensions{
			Width:  3,
			Height: 1,
			Length: 3,
		},
		Palette: map[int]string{1: "minecraft:cobblestone"},
		Layers: []Layer{
			{Y: 0, Matrix: [][]int{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
		},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	if bp.Purpose != PurposeStorehouse {
		t.Errorf("expected GRANARY to normalize to STOREHOUSE, got %q", bp.Purpose)
	}
}

func TestBlueprint_SubterraneanBasementAndExcavation(t *testing.T) {
	// A 5x4x5 blueprint with MinY = -1 (so Y = -1, 0, 1, 2)
	// Y = -1 has a 5x5 perimeter of cobblestone with a 3x3 interior of air + 1 chest.
	// Interior within perimeter bounds = 3x3 = 9 voxels to be excavated.
	bp := &Blueprint{
		ID:      "cellar_lodge",
		Name:    "Cellar Lodge",
		Purpose: PurposeTavern,
		Dimensions: Dimensions{
			Width:  5,
			Height: 4,
			Length: 5,
			MinY:   -1,
		},
		Palette: map[int]string{
			0: "minecraft:air",
			1: "minecraft:cobblestone",
			2: "minecraft:oak_planks",
			3: "minecraft:barrel",
		},
		Layers: []Layer{
			{
				Y: -1, // Cellar basement
				Matrix: [][]int{
					{1, 1, 1, 1, 1},
					{1, 3, 0, 0, 1}, // (1,1) barrel
					{1, 0, 0, 0, 1},
					{1, 0, 0, 3, 1}, // (3,3) barrel
					{1, 1, 1, 1, 1},
				},
			},
			{
				Y: 0, // Ground floor
				Matrix: [][]int{
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
				},
			},
			{
				Y: 1, // Walls
				Matrix: [][]int{
					{2, 2, 2, 2, 2},
					{2, 0, 0, 0, 2},
					{2, 0, 0, 0, 2},
					{2, 0, 0, 0, 2},
					{2, 2, 2, 2, 2},
				},
			},
			{
				Y: 2, // Roof
				Matrix: [][]int{
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
					{2, 2, 2, 2, 2},
				},
			},
		},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("failed validating blueprint with negative Y cellar: %v", err)
	}

	// Verify excavation volume
	excavation := bp.ComputeExcavation()
	// The 3x3 interior bounded by perimeter is 9 blocks
	if excavation != 9 {
		t.Errorf("expected excavation volume of 9 blocks, got %d", excavation)
	}
	if bp.Excavation != 9 {
		t.Errorf("expected bp.Excavation field to be 9, got %d", bp.Excavation)
	}

	// Verify VoxelMatrix roundtrip with negative Y
	vm, err := bp.ToVoxelMatrix()
	if err != nil {
		t.Fatalf("ToVoxelMatrix failed: %v", err)
	}

	st, err := vm.Get(1, -1, 1)
	if err != nil {
		t.Fatalf("vm.Get(1, -1, 1) failed: %v", err)
	}
	if st.BaseItem() != "minecraft:barrel" {
		t.Errorf("expected barrel at (1, -1, 1), got %s", st.String())
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

	// 6. Invalid purpose
	bp.Layers = []Layer{
		{Y: 0, Matrix: [][]int{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
	}
	bp.Purpose = "ASTRONOMY_OBSERVATORY_NONEXISTENT"
	if err := bp.Validate(); err == nil {
		t.Errorf("expected error for invalid purpose")
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

func TestBlueprint_MultiBlockBOMAccounting(t *testing.T) {
	bp := &Blueprint{
		ID:      "test_multiblock",
		Name:    "Test Multiblock",
		Purpose: PurposeHome,
		Dimensions: Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "minecraft:oak_door[facing=south,half=lower]",
			2: "minecraft:oak_door[facing=south,half=upper]",
			3: "minecraft:red_bed[facing=north,part=foot]",
			4: "minecraft:red_bed[facing=north,part=head]",
			5: "minecraft:oak_trapdoor[facing=north]",
		},
		Layers: []Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 3, 5},
					{0, 4, 0},
					{0, 0, 0},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{2, 0, 0},
					{0, 0, 0},
					{0, 0, 0},
				},
			},
		},
	}

	bom := bp.CalculateBOM()

	// 2 door voxels (lower + upper) must register as 1 door item in BOM
	if bom["minecraft:oak_door"] != 1 {
		t.Errorf("expected 1 oak_door item in BOM for paired lower+upper voxels, got %d", bom["minecraft:oak_door"])
	}

	// 2 bed voxels (foot + head) must register as 1 bed item in BOM
	if bom["minecraft:red_bed"] != 1 {
		t.Errorf("expected 1 red_bed item in BOM for paired foot+head voxels, got %d", bom["minecraft:red_bed"])
	}

	// 1 trapdoor must register as 1 item in BOM (not halved)
	if bom["minecraft:oak_trapdoor"] != 1 {
		t.Errorf("expected 1 oak_trapdoor item in BOM, got %d", bom["minecraft:oak_trapdoor"])
	}
}

// Silence unused import in test if needed
var _ = os.PathSeparator

// cq-6l3.1: the navigator treats a target within the arrival tolerance (1) as reached, so a
// shorter step back left the builder re-sending it for minutes.
func TestCalculateCabinStepBackAlwaysMovesTheAgent(t *testing.T) {
	center := &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	voxel := &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}
	for _, x := range []float64{1.6, 2.5, 2.9, 3.4, 3.5} {
		pos := &civquestv1.Vec3{X: x, Y: 64, Z: 0.5}
		to := CalculateCabinStepBack(pos, voxel, center)
		if d := math.Hypot(to.X-pos.X, to.Z-pos.Z); d < CabinStepBackMinDist-0.1 {
			t.Errorf("from x=%v: step of %.2f blocks to (%v,%v)", x, d, to.X, to.Z)
		}
		// Even stopping one block short of the target leaves the voxel clear.
		d := math.Hypot(to.X-pos.X, to.Z-pos.Z)
		stop := &civquestv1.Vec3{X: to.X - (to.X-pos.X)/d, Y: 64, Z: to.Z - (to.Z-pos.Z)/d}
		if IsCollidingWithVoxel(stop, voxel) {
			t.Errorf("from x=%v: still colliding at (%v,%v)", x, stop.X, stop.Z)
		}
	}
}
