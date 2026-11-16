package blueprints

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupTestXDG(t *testing.T) (string, string) {
	t.Helper()
	tmpRoot := t.TempDir()
	configDir := filepath.Join(tmpRoot, "config")
	dataDir := filepath.Join(tmpRoot, "data")
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	return configDir, dataDir
}

func TestLibrary_LibraryDirs(t *testing.T) {
	setupTestXDG(t)

	dirs := LibraryDirs()
	if len(dirs) != 2 {
		t.Fatalf("expected 2 library dirs, got %d: %+v", len(dirs), dirs)
	}

	expectedData := filepath.Join(xdg.DataDir(), "blueprints")
	expectedConfig := filepath.Join(xdg.ConfigDir(), "blueprints")

	if dirs[0] != expectedData {
		t.Errorf("expected primary data dir %q, got %q", expectedData, dirs[0])
	}
	if dirs[1] != expectedConfig {
		t.Errorf("expected config override dir %q, got %q", expectedConfig, dirs[1])
	}
}

func TestLibrary_EnsureStarterBlueprintsAndList(t *testing.T) {
	_, dataDir := setupTestXDG(t)

	// Verify initially empty
	initialList, err := ListLibraryBlueprints()
	if err != nil {
		t.Fatalf("initial ListLibraryBlueprints failed: %v", err)
	}
	if len(initialList) != 0 {
		t.Errorf("expected 0 blueprints initially, got %d", len(initialList))
	}

	// Run EnsureStarterBlueprints
	if err := EnsureStarterBlueprints(); err != nil {
		t.Fatalf("EnsureStarterBlueprints failed: %v", err)
	}

	// Verify files in data directory
	libraryDir := filepath.Join(dataDir, "civquest", "blueprints")
	expectedStarters := []struct {
		id      string
		purpose string
	}{
		{"pioneer_shelter", PurposeShelter},
		{"timber_hearth_tavern", PurposeTavern},
		{"settlement_armory", PurposeArmory},
		{"frontier_farm", PurposeFarm},
		{"pioneer_home", PurposeHome},
		{"civic_townhall", PurposeTownhall},
		{"lore_library", PurposeLibrary},
		{"pioneer_storehouse", PurposeStorehouse},
	}

	for _, s := range expectedStarters {
		path := filepath.Join(libraryDir, s.id+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected starter blueprint file %s does not exist", path)
		}

		bp, err := LoadJSON(path)
		if err != nil {
			t.Errorf("failed loading starter blueprint %s: %v", s.id, err)
			continue
		}
		if bp.Purpose != s.purpose {
			t.Errorf("starter %s: expected purpose %q, got %q", s.id, s.purpose, bp.Purpose)
		}
		if len(bp.BOM) == 0 {
			t.Errorf("starter %s: expected non-empty BOM", s.id)
		}
	}

	// Verify Timber Hearth Tavern subterranean cellar and excavation
	tavern, err := GetLibraryBlueprint("timber_hearth_tavern")
	if err != nil {
		t.Fatalf("failed retrieving timber_hearth_tavern: %v", err)
	}
	if tavern.Dimensions.MinY != -1 {
		t.Errorf("expected tavern MinY = -1, got %d", tavern.Dimensions.MinY)
	}
	excavation := tavern.ComputeExcavation()
	if excavation <= 0 {
		t.Errorf("expected tavern excavation volume > 0, got %d", excavation)
	}

	// Verify directional block aggregation in BOM for tavern
	if tavern.BOM["minecraft:oak_log"] <= 0 {
		t.Errorf("expected oak_log in tavern BOM, got %d", tavern.BOM["minecraft:oak_log"])
	}
	if tavern.BOM["minecraft:oak_stairs"] <= 0 {
		t.Errorf("expected oak_stairs in tavern BOM, got %d", tavern.BOM["minecraft:oak_stairs"])
	}
	if tavern.BOM["minecraft:oak_door"] <= 0 {
		t.Errorf("expected oak_door in tavern BOM, got %d", tavern.BOM["minecraft:oak_door"])
	}

	// Verify ListLibraryBlueprints returns all starters in ascending ID order
	all, err := ListLibraryBlueprints()
	if err != nil {
		t.Fatalf("ListLibraryBlueprints failed: %v", err)
	}
	if len(all) < len(expectedStarters) {
		t.Fatalf("expected at least %d blueprints, got %d", len(expectedStarters), len(all))
	}

	// Verify alphabetical sort
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Errorf("blueprints not sorted by ID: %s >= %s", all[i-1].ID, all[i].ID)
		}
	}

	// Idempotency check: EnsureStarterBlueprints second run should be a no-op
	if err := EnsureStarterBlueprints(); err != nil {
		t.Fatalf("second EnsureStarterBlueprints call failed: %v", err)
	}
}

func TestLibrary_GetLibraryBlueprintQueries(t *testing.T) {
	setupTestXDG(t)
	if err := EnsureStarterBlueprints(); err != nil {
		t.Fatalf("EnsureStarterBlueprints failed: %v", err)
	}

	// 1. Exact ID
	bp1, err := GetLibraryBlueprint("settlement_armory")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint by exact ID failed: %v", err)
	}
	if bp1.ID != "settlement_armory" {
		t.Errorf("expected ID 'settlement_armory', got %q", bp1.ID)
	}

	// 2. Case-insensitive ID
	bp2, err := GetLibraryBlueprint("SETTLEMENT_ARMORY")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint case-insensitive ID failed: %v", err)
	}
	if bp2.ID != "settlement_armory" {
		t.Errorf("expected ID 'settlement_armory', got %q", bp2.ID)
	}

	// 3. Exact Name
	bp3, err := GetLibraryBlueprint("Settlement Armory")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint by Name failed: %v", err)
	}
	if bp3.ID != "settlement_armory" {
		t.Errorf("expected ID 'settlement_armory', got %q", bp3.ID)
	}

	// 4. File extension suffix
	bp4, err := GetLibraryBlueprint("settlement_armory.json")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint with .json extension failed: %v", err)
	}
	if bp4.ID != "settlement_armory" {
		t.Errorf("expected ID 'settlement_armory', got %q", bp4.ID)
	}

	// 5. Unknown query returns ErrBlueprintNotFound
	_, err = GetLibraryBlueprint("non_existent_castle_v99")
	if !errors.Is(err, ErrBlueprintNotFound) {
		t.Errorf("expected ErrBlueprintNotFound, got %v", err)
	}
}

func TestLibrary_ConfigOverridePrecedence(t *testing.T) {
	configDir, _ := setupTestXDG(t)
	if err := EnsureStarterBlueprints(); err != nil {
		t.Fatalf("EnsureStarterBlueprints failed: %v", err)
	}

	// Retrieve original pioneer_home
	orig, err := GetLibraryBlueprint("pioneer_home")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint failed: %v", err)
	}
	if orig.Author != "civquest" {
		t.Errorf("expected original author 'civquest', got %q", orig.Author)
	}

	// Create user override in $XDG_CONFIG_HOME/civquest/blueprints/pioneer_home.json
	cfgBlueprintDir := filepath.Join(configDir, "civquest", "blueprints")
	if err := os.MkdirAll(cfgBlueprintDir, 0755); err != nil {
		t.Fatalf("failed creating config dir: %v", err)
	}

	overridden := *orig
	overridden.Author = "player_custom"
	overridden.Description = "Customized player edition of pioneer home"
	if err := overridden.SaveJSON(filepath.Join(cfgBlueprintDir, "pioneer_home.json")); err != nil {
		t.Fatalf("failed saving override: %v", err)
	}

	// Retrieve again: must return the config override version!
	fetched, err := GetLibraryBlueprint("pioneer_home")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint failed: %v", err)
	}
	if fetched.Author != "player_custom" {
		t.Errorf("expected overridden author 'player_custom', got %q", fetched.Author)
	}
	if fetched.Description != "Customized player edition of pioneer home" {
		t.Errorf("expected overridden description, got %q", fetched.Description)
	}

	// Verify ListLibraryBlueprints still returns a single pioneer_home (deduplicated)
	list, err := ListLibraryBlueprints()
	if err != nil {
		t.Fatalf("ListLibraryBlueprints failed: %v", err)
	}
	count := 0
	for _, b := range list {
		if b.ID == "pioneer_home" {
			count++
			if b.Author != "player_custom" {
				t.Errorf("list returned non-override version: author = %q", b.Author)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 pioneer_home in list, got %d", count)
	}
}

func TestLibrary_SaveAndDeleteLibraryBlueprint(t *testing.T) {
	setupTestXDG(t)

	custom := &Blueprint{
		ID:          "nordic_watchtower",
		Name:        "Nordic Watchtower",
		Purpose:     PurposeBarracks,
		Type:        "watchtower",
		Style:       "nordic",
		Author:      "viking_guild",
		Description: "High-elevation defensive lookout",
		Tags:        []string{"defense", "tower"},
		Dimensions:  Dimensions{Width: 3, Height: 4, Length: 3, MinY: 0},
		Palette: map[int]string{
			0: "minecraft:air",
			1: "minecraft:cobblestone",
			2: "minecraft:spruce_planks",
		},
		Layers: []Layer{
			{Y: 0, Matrix: [][]int{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
			{Y: 1, Matrix: [][]int{{1, 1, 1}, {1, 0, 1}, {1, 1, 1}}},
			{Y: 2, Matrix: [][]int{{1, 1, 1}, {1, 0, 1}, {1, 1, 1}}},
			{Y: 3, Matrix: [][]int{{2, 2, 2}, {2, 2, 2}, {2, 2, 2}}},
		},
	}

	// Save to library
	if err := SaveLibraryBlueprint(custom); err != nil {
		t.Fatalf("SaveLibraryBlueprint failed: %v", err)
	}

	// Get from library
	got, err := GetLibraryBlueprint("nordic_watchtower")
	if err != nil {
		t.Fatalf("GetLibraryBlueprint failed: %v", err)
	}
	if got.Author != "viking_guild" {
		t.Errorf("expected author 'viking_guild', got %q", got.Author)
	}

	// Delete from library
	if err := DeleteLibraryBlueprint("nordic_watchtower"); err != nil {
		t.Fatalf("DeleteLibraryBlueprint failed: %v", err)
	}

	// Verify it is gone
	_, err = GetLibraryBlueprint("nordic_watchtower")
	if !errors.Is(err, ErrBlueprintNotFound) {
		t.Errorf("expected ErrBlueprintNotFound after deletion, got %v", err)
	}

	// Delete again should return ErrBlueprintNotFound
	if err := DeleteLibraryBlueprint("nordic_watchtower"); !errors.Is(err, ErrBlueprintNotFound) {
		t.Errorf("expected ErrBlueprintNotFound deleting non-existent blueprint, got %v", err)
	}
}

func TestLibrary_DirectionalBlocksBOMAggregation(t *testing.T) {
	// Tests that directional stairs and directional logs aggregate into base item IDs
	bp := &Blueprint{
		ID:      "stair_showcase",
		Name:    "Stair Showcase",
		Purpose: PurposeHome,
		Dimensions: Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			0: "minecraft:air",
			1: "minecraft:oak_stairs[facing=north,half=bottom]",
			2: "minecraft:oak_stairs[facing=south,half=bottom]",
			3: "minecraft:oak_stairs[facing=east,half=bottom]",
			4: "minecraft:oak_stairs[facing=west,half=top]",
			5: "minecraft:oak_log[axis=y]",
			6: "minecraft:oak_log[axis=x]",
		},
		Layers: []Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 2, 3},
					{4, 0, 0},
					{0, 0, 0},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{5, 6, 0},
					{0, 0, 0},
					{0, 0, 0},
				},
			},
		},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("Validate failed on directional blocks: %v", err)
	}

	// 4 stairs placed (different directions) should aggregate to 4 "minecraft:oak_stairs"
	if bp.BOM["minecraft:oak_stairs"] != 4 {
		t.Errorf("expected 4 aggregated oak_stairs, got %d", bp.BOM["minecraft:oak_stairs"])
	}

	// 2 logs placed (different axes) should aggregate to 2 "minecraft:oak_log"
	if bp.BOM["minecraft:oak_log"] != 2 {
		t.Errorf("expected 2 aggregated oak_log, got %d", bp.BOM["minecraft:oak_log"])
	}
}
