package blueprints

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// LibraryDirs returns the XDG directories searched for architectural blueprints.
// Precedence order:
// 1. Primary data library: $XDG_DATA_HOME/civquest/blueprints
// 2. User config override: $XDG_CONFIG_HOME/civquest/blueprints
func LibraryDirs() []string {
	return []string{
		filepath.Join(xdg.DataDir(), "blueprints"),
		filepath.Join(xdg.ConfigDir(), "blueprints"),
	}
}

// ListLibraryBlueprints returns all valid blueprints discovered across the XDG library directories.
// Blueprints in the user config directory override data library blueprints with matching IDs.
// Blueprints are returned in deterministic ascending order by ID.
func ListLibraryBlueprints() ([]*Blueprint, error) {
	byID := make(map[string]*Blueprint)

	// Scan in override order: config override first, then primary data library
	dirs := []string{
		filepath.Join(xdg.ConfigDir(), "blueprints"),
		filepath.Join(xdg.DataDir(), "blueprints"),
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // Skip unreadable paths gracefully
			}
			if d.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".json" && ext != ".yaml" && ext != ".yml" {
				return nil
			}

			bp, err := LoadFile(path)
			if err != nil {
				return nil // Skip invalid blueprint files during directory scan
			}

			if _, exists := byID[bp.ID]; !exists {
				byID[bp.ID] = bp
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning blueprint library %s: %w", dir, err)
		}
	}

	result := make([]*Blueprint, 0, len(byID))
	for _, bp := range byID {
		result = append(result, bp)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result, nil
}

// GetLibraryBlueprint searches for a blueprint by ID, slugified ID, or Name (case-insensitive).
// Searches user config override first, then primary data library.
func GetLibraryBlueprint(idOrName string) (*Blueprint, error) {
	target := strings.TrimSpace(idOrName)
	if target == "" {
		return nil, fmt.Errorf("%w: empty query", ErrBlueprintNotFound)
	}

	cleanTarget := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(target), ".json"), ".yaml"), ".yml")
	slug := Slugify(cleanTarget)

	dirs := []string{
		filepath.Join(xdg.ConfigDir(), "blueprints"),
		filepath.Join(xdg.DataDir(), "blueprints"),
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// 1. Direct file candidate check
		candidates := []string{
			filepath.Join(dir, cleanTarget+".json"),
			filepath.Join(dir, cleanTarget+".yaml"),
			filepath.Join(dir, cleanTarget+".yml"),
			filepath.Join(dir, slug+".json"),
			filepath.Join(dir, slug+".yaml"),
			filepath.Join(dir, slug+".yml"),
		}
		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				bp, err := LoadFile(cand)
				if err == nil {
					return bp, nil
				}
			}
		}

		// 2. Full directory scan match
		var matched *Blueprint
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".json" && ext != ".yaml" && ext != ".yml" {
				return nil
			}

			bp, err := LoadFile(path)
			if err != nil {
				return nil
			}

			if strings.EqualFold(bp.ID, target) ||
				strings.EqualFold(bp.ID, cleanTarget) ||
				bp.ID == slug ||
				strings.EqualFold(bp.Name, target) ||
				Slugify(bp.Name) == slug {
				matched = bp
				return filepath.SkipAll
			}
			return nil
		})

		if matched != nil {
			return matched, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrBlueprintNotFound, idOrName)
}

// SaveLibraryBlueprint validates and persists a blueprint into the primary data library.
// ($XDG_DATA_HOME/civquest/blueprints/{id}.json)
func SaveLibraryBlueprint(bp *Blueprint) error {
	if bp == nil {
		return ErrNilBlueprint
	}
	if err := bp.Validate(); err != nil {
		return err
	}

	dir := filepath.Join(xdg.DataDir(), "blueprints")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating blueprint library directory %s: %w", dir, err)
	}

	dest := filepath.Join(dir, bp.ID+".json")
	return bp.SaveJSON(dest)
}

// DeleteLibraryBlueprint removes all matching blueprint files (by ID or Name) from both
// config and data library directories. Returns ErrBlueprintNotFound if no file was deleted.
func DeleteLibraryBlueprint(idOrName string) error {
	target := strings.TrimSpace(idOrName)
	if target == "" {
		return fmt.Errorf("%w: empty query", ErrBlueprintNotFound)
	}

	cleanTarget := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(target), ".json"), ".yaml"), ".yml")
	slug := Slugify(cleanTarget)

	dirs := []string{
		filepath.Join(xdg.ConfigDir(), "blueprints"),
		filepath.Join(xdg.DataDir(), "blueprints"),
	}

	deletedCount := 0

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".json" && ext != ".yaml" && ext != ".yml" {
				return nil
			}

			baseWithoutExt := strings.TrimSuffix(d.Name(), ext)
			matched := strings.EqualFold(baseWithoutExt, target) ||
				strings.EqualFold(baseWithoutExt, cleanTarget) ||
				baseWithoutExt == slug

			if !matched {
				bp, err := LoadFile(path)
				if err == nil {
					matched = strings.EqualFold(bp.ID, target) ||
						strings.EqualFold(bp.ID, cleanTarget) ||
						bp.ID == slug ||
						strings.EqualFold(bp.Name, target) ||
						Slugify(bp.Name) == slug
				}
			}

			if matched {
				if err := os.Remove(path); err == nil {
					deletedCount++
				}
			}
			return nil
		})
	}

	if deletedCount == 0 {
		return fmt.Errorf("%w: %s", ErrBlueprintNotFound, idOrName)
	}
	return nil
}

// EnsureStarterBlueprints populates $XDG_DATA_HOME/civquest/blueprints with starter templates
// if the library directory is empty of blueprint files.
func EnsureStarterBlueprints() error {
	dir := filepath.Join(xdg.DataDir(), "blueprints")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating blueprint library directory %s: %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading blueprint library directory %s: %w", dir, err)
	}

	hasBlueprints := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".json" || ext == ".yaml" || ext == ".yml" {
			hasBlueprints = true
			break
		}
	}

	if hasBlueprints {
		return nil
	}

	for _, bp := range StarterBlueprints() {
		if err := bp.Validate(); err != nil {
			return fmt.Errorf("validating starter blueprint %s: %w", bp.ID, err)
		}
		dest := filepath.Join(dir, bp.ID+".json")
		if err := bp.SaveJSON(dest); err != nil {
			return fmt.Errorf("saving starter blueprint %s: %w", bp.ID, err)
		}
	}

	return nil
}

// StarterBlueprints synthesizes the canonical starter architectural blueprints for CivQuest:
// - pioneer_shelter (Purpose: "SHELTER")
// - timber_hearth_tavern (Purpose: "TAVERN", with basement wine cellar at Y = -1)
// - settlement_armory (Purpose: "ARMORY")
// - frontier_farm (Purpose: "FARM")
// - pioneer_home (Purpose: "HOME")
// - civic_townhall (Purpose: "TOWNHALL")
// - lore_library (Purpose: "LIBRARY")
// - pioneer_storehouse (Purpose: "STOREHOUSE")
func StarterBlueprints() []*Blueprint {
	return []*Blueprint{
		createPioneerShelter(),
		createTimberHearthTavern(),
		createSettlementArmory(),
		createFrontierFarm(),
		createPioneerHome(),
		createCivicTownhall(),
		createLoreLibrary(),
		createPioneerStorehouse(),
	}
}

// 1. Pioneer Shelter (5x4x5, SHELTER)
func createPioneerShelter() *Blueprint {
	w, h, l := 5, 4, 5
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:cobblestone",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:glass_pane",
		5: "minecraft:torch",
		6: "minecraft:oak_door[facing=south,half=lower]",
		7: "minecraft:oak_door[facing=south,half=upper]",
		8: "minecraft:oak_stairs[facing=north,half=bottom]",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					row[x] = 1 // Cobblestone floor
				} else if y == h-1 {
					if z == 0 {
						row[x] = 8 // Roof trim stairs
					} else {
						row[x] = 2 // Oak planks roof
					}
				} else if isCorner {
					row[x] = 3 // Oak log corner pillar
				} else if isEdge {
					if x == 2 && z == 0 {
						if y == 1 {
							row[x] = 6 // Oak door lower
						} else {
							row[x] = 7 // Oak door upper
						}
					} else if (x == 0 && z == 2 || x == 4 && z == 2 || x == 2 && z == 4) && y == 2 {
						row[x] = 4 // Glass pane window
					} else {
						row[x] = 2 // Oak planks wall
					}
				} else {
					if y == 1 && x == 2 && z == 2 {
						row[x] = 5 // Interior torch
					} else {
						row[x] = 0 // Interior air
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "pioneer_shelter",
		Name:        "Pioneer Shelter",
		Version:     1,
		Purpose:     PurposeShelter,
		Type:        "shelter",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Essential 5x4x5 survival shelter with cobblestone base and oak log reinforcement",
		Tags:        []string{"survival", "starter", "shelter"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 2. Timber Hearth Tavern (7x6x7, TAVERN with Y=-1 basement wine cellar)
func createTimberHearthTavern() *Blueprint {
	w, h, l := 7, 6, 7 // Y in [-1, 4]
	minY := -1

	palette := map[int]string{
		0:  "minecraft:air",
		1:  "minecraft:cobblestone",
		2:  "minecraft:oak_planks",
		3:  "minecraft:oak_log[axis=y]",
		4:  "minecraft:glass_pane",
		5:  "minecraft:barrel",
		6:  "minecraft:torch",
		7:  "minecraft:oak_stairs[facing=south,half=bottom]",
		8:  "minecraft:oak_log[axis=x]",
		9:  "minecraft:oak_door[facing=south,half=lower]",
		10: "minecraft:oak_door[facing=south,half=upper]",
	}

	layers := make([]Layer, h)
	for i := 0; i < h; i++ {
		worldY := minY + i
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				switch worldY {
				case -1:
					// Basement cellar level
					if isEdge {
						row[x] = 1 // Cobblestone perimeter foundation walls
					} else {
						// Subterranean wine cellar interior (to be excavated)
						if x == 1 && z == 1 {
							row[x] = 7 // Oak stairs at cellar entrance
						} else if (x == 1 || x == 5) && (z == 1 || z == 2) {
							row[x] = 5 // Wine barrels
						} else if x == 3 && z == 3 {
							row[x] = 6 // Cellar torch
						} else {
							row[x] = 0 // Cellar air
						}
					}
				case 0:
					// Ground floor
					if x == 1 && z == 1 {
						row[x] = 0 // Cellar stairway access
					} else if x == 3 && z == 5 {
						row[x] = 1 // Hearth cobblestone base
					} else {
						row[x] = 2 // Oak planks floor
					}
				case 1:
					// Tavern lower walls
					if isCorner {
						row[x] = 3 // Oak log pillars
					} else if isEdge {
						if x == 3 && z == 0 {
							row[x] = 9 // Oak door lower
						} else {
							row[x] = 2 // Planks wall
						}
					} else {
						if x == 3 && z == 5 {
							row[x] = 6 // Hearth fire/torch
						} else if x == 5 && z == 5 {
							row[x] = 5 // Bar counter barrel
						} else {
							row[x] = 0 // Interior air
						}
					}
				case 2:
					// Tavern upper walls with windows
					if isCorner {
						row[x] = 3 // Oak log pillars
					} else if isEdge {
						if x == 3 && z == 0 {
							row[x] = 10 // Oak door upper
						} else if (x == 1 || x == 5) && z == 6 || (z == 3 && (x == 0 || x == 6)) {
							row[x] = 4 // Glass pane windows
						} else {
							row[x] = 2 // Planks wall
						}
					} else {
						row[x] = 0 // Interior air
					}
				case 3:
					// Ceiling beams
					if isEdge || x == 3 || z == 3 {
						row[x] = 8 // Directional oak log horizontal beams [axis=x]
					} else {
						row[x] = 2 // Plank ceiling
					}
				case 4:
					// Tavern roof
					row[x] = 2 // Oak planks roof
				}
			}
			mat[z] = row
		}
		layers[i] = Layer{Y: worldY, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "timber_hearth_tavern",
		Name:        "Timber Hearth Tavern",
		Version:     1,
		Purpose:     PurposeTavern,
		Type:        "tavern",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Spacious communal tavern featuring a subterranean wine cellar and social hearth",
		Tags:        []string{"social", "tavern", "wine_cellar", "communal"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: minY},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 3. Settlement Armory (7x5x7, ARMORY)
func createSettlementArmory() *Blueprint {
	w, h, l := 7, 5, 7
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:stone_bricks",
		2: "minecraft:iron_bars",
		3: "minecraft:chest",
		4: "minecraft:anvil",
		5: "minecraft:torch",
		6: "minecraft:cobblestone",
		7: "minecraft:stone_slab[type=bottom]",
		8: "minecraft:iron_door[facing=south,half=lower]",
		9: "minecraft:iron_door[facing=south,half=upper]",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					row[x] = 1 // Stone brick foundation
				} else if y == h-1 {
					row[x] = 7 // Stone slab reinforced roof
				} else if isCorner {
					row[x] = 1 // Stone brick pillars
				} else if isEdge {
					if x == 3 && z == 0 {
						if y == 1 {
							row[x] = 8 // Iron door lower
						} else if y == 2 {
							row[x] = 9 // Iron door upper
						} else {
							row[x] = 1 // Stone brick arch
						}
					} else if ((x == 0 || x == 6) && z == 3 || z == 6 && x == 3) && y == 2 {
						row[x] = 2 // Iron bars security slits
					} else {
						row[x] = 6 // Cobblestone walls
					}
				} else {
					if y == 1 {
						if (x == 1 || x == 5) && (z == 1 || z == 2) {
							row[x] = 3 // Munitions chests
						} else if x == 3 && z == 3 {
							row[x] = 4 // Anvil
						} else {
							row[x] = 0
						}
					} else if y == 2 && x == 3 && (z == 1 || z == 5) {
						row[x] = 5 // Wall torches
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "settlement_armory",
		Name:        "Settlement Armory",
		Version:     1,
		Purpose:     PurposeArmory,
		Type:        "armory",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Reinforced armory and munitions store for settlement defense and militia equipment",
		Tags:        []string{"military", "armory", "defense", "militia"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 4. Frontier Farm (7x3x7, FARM)
func createFrontierFarm() *Blueprint {
	w, h, l := 7, 3, 7
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:dirt",
		2: "minecraft:farmland[moisture=7]",
		3: "minecraft:water",
		4: "minecraft:oak_fence",
		5: "minecraft:wheat[age=7]",
		6: "minecraft:torch",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					if isEdge {
						row[x] = 1 // Dirt border
					} else if x == 3 {
						row[x] = 3 // Central irrigation canal
					} else {
						row[x] = 2 // Hydrated farmland
					}
				} else if y == 1 {
					if isEdge {
						if x == 3 && z == 0 {
							row[x] = 0 // Fence gate opening
						} else {
							row[x] = 4 // Protective oak fence
						}
					} else if x == 3 {
						row[x] = 0 // Air above water
					} else {
						row[x] = 5 // Mature wheat crops
					}
				} else if y == 2 {
					if isCorner {
						row[x] = 6 // Perimeter torches on fence corners
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "frontier_farm",
		Name:        "Frontier Farm",
		Version:     1,
		Purpose:     PurposeFarm,
		Type:        "farm",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Irrigated agricultural plots with water irrigation channel and protective perimeter fencing",
		Tags:        []string{"agriculture", "farm", "food", "crops"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 5. Pioneer Home (5x4x5, HOME)
func createPioneerHome() *Blueprint {
	w, h, l := 5, 4, 5
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:cobblestone",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:glass_pane",
		5: "minecraft:chest",
		6: "minecraft:torch",
		7: "minecraft:oak_door[facing=south,half=lower]",
		8: "minecraft:oak_door[facing=south,half=upper]",
		9: "minecraft:oak_stairs[facing=north,half=bottom]",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					row[x] = 1 // Cobblestone floor
				} else if y == h-1 {
					if z == 0 || z == l-1 {
						row[x] = 9 // Oak stairs roof trim
					} else {
						row[x] = 2 // Oak planks ceiling / roof
					}
				} else if isCorner {
					row[x] = 3 // Oak log pillars
				} else if isEdge {
					if x == 2 && z == 0 {
						if y == 1 {
							row[x] = 7 // Oak door lower
						} else {
							row[x] = 8 // Oak door upper
						}
					} else if (x == 0 && z == 2 || x == 4 && z == 2 || x == 2 && z == 4) && y == 2 {
						row[x] = 4 // Glass pane windows
					} else {
						row[x] = 2 // Oak planks wall
					}
				} else {
					if y == 1 {
						if x == 1 && z == 3 {
							row[x] = 5 // Storage chest
						} else if x == 2 && z == 2 {
							row[x] = 6 // Torch
						} else {
							row[x] = 0
						}
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "pioneer_home",
		Name:        "Pioneer Home",
		Version:     1,
		Purpose:     PurposeHome,
		Type:        "home",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Comfortable settler residence with warm wooden interior and glass windows",
		Tags:        []string{"residential", "home", "settler"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 6. Civic Townhall (9x6x9, TOWNHALL)
func createCivicTownhall() *Blueprint {
	w, h, l := 9, 6, 9
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:stone_bricks",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:glass_pane",
		5: "minecraft:bookshelf",
		6: "minecraft:torch",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					if isEdge {
						row[x] = 1 // Stone brick foundation
					} else {
						row[x] = 2 // Polished oak planks hall floor
					}
				} else if y == h-1 {
					row[x] = 2 // Grand pitched oak planks roof
				} else if isCorner {
					row[x] = 1 // Heavy stone brick corner buttresses
				} else if isEdge {
					if (x == 4 && z == 0) && (y >= 1 && y <= 3) {
						row[x] = 0 // Grand entrance portal
					} else if ((x == 0 || x == w-1) && (z == 2 || z == 6) || (z == l-1 && (x == 2 || x == 6))) && (y == 2 || y == 3) {
						row[x] = 4 // Stately glass pane windows
					} else {
						row[x] = 2 // Planks walls
					}
				} else {
					if (x == 2 || x == 6) && (z == 2 || z == 6) && y < 5 {
						row[x] = 3 // Interior hall support colonnade
					} else if y == 1 && z == 7 && (x == 3 || x == 5) {
						row[x] = 5 // Archive bookshelves behind dais
					} else if y == 2 && x == 4 && z == 4 {
						row[x] = 6 // Central hall chandelier torch
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "civic_townhall",
		Name:        "Civic Townhall",
		Version:     1,
		Purpose:     PurposeTownhall,
		Type:        "townhall",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Grand civic hall for governance, council convocations, and dynastic diplomacy",
		Tags:        []string{"civic", "townhall", "government", "council"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 7. Lore Library (7x5x7, LIBRARY)
func createLoreLibrary() *Blueprint {
	w, h, l := 7, 5, 7
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:cobblestone",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:bookshelf",
		5: "minecraft:glass_pane",
		6: "minecraft:torch",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					row[x] = 1 // Cobblestone floor
				} else if y == h-1 {
					row[x] = 2 // Oak planks roof
				} else if isCorner {
					row[x] = 3 // Oak log pillars
				} else if isEdge {
					if (x == 3 && z == 0) && (y == 1 || y == 2) {
						row[x] = 0 // Library entrance
					} else if (z == 3 && (x == 0 || x == 6) || x == 3 && z == 6) && y == 2 {
						row[x] = 5 // Glass pane windows
					} else {
						row[x] = 2 // Planks wall
					}
				} else {
					if y == 1 || y == 2 {
						if (x == 1 || x == 5) && (z >= 2 && z <= 4) || (z == 5 && (x == 2 || x == 4)) {
							row[x] = 4 // Stacks of bookshelves
						} else if y == 1 && x == 3 && z == 3 {
							row[x] = 6 // Reading table torch
						} else {
							row[x] = 0
						}
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "lore_library",
		Name:        "Lore Library",
		Version:     1,
		Purpose:     PurposeLibrary,
		Type:        "library",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Scholarly repository and archive for dynastic chronicles, toponymy maps, and historical codices",
		Tags:        []string{"knowledge", "library", "chronicler", "books"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}

// 8. Pioneer Storehouse (7x5x7, STOREHOUSE)
func createPioneerStorehouse() *Blueprint {
	w, h, l := 7, 5, 7
	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:cobblestone",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:barrel",
		5: "minecraft:chest",
		6: "minecraft:torch",
	}

	layers := make([]Layer, h)
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					row[x] = 1 // Heavy cobblestone storage foundation
				} else if y == h-1 {
					row[x] = 2 // Plank roof
				} else if isCorner {
					row[x] = 3 // Oak log pillars
				} else if isEdge {
					if (x == 3 && z == 0) && (y == 1 || y == 2) {
						row[x] = 0 // Wide loading portal
					} else {
						row[x] = 2 // Oak planks security wall
					}
				} else {
					if y == 1 {
						if (x == 1 || x == 5) && (z >= 1 && z <= 3) {
							row[x] = 4 // Grain and resource barrels
						} else if (z == 5) && (x >= 2 && x <= 4) {
							row[x] = 5 // Bulk material chests
						} else if x == 3 && z == 3 {
							row[x] = 6 // Central work torch
						} else {
							row[x] = 0
						}
					} else if y == 2 {
						if (x == 1 || x == 5) && (z == 1 || z == 2) {
							row[x] = 4 // Stacked barrels
						} else {
							row[x] = 0
						}
					} else {
						row[x] = 0
					}
				}
			}
			mat[z] = row
		}
		layers[y] = Layer{Y: y, Matrix: mat}
	}

	bp := &Blueprint{
		ID:          "pioneer_storehouse",
		Name:        "Pioneer Storehouse",
		Version:     1,
		Purpose:     PurposeStorehouse,
		Type:        "storehouse",
		Style:       "pioneer",
		Author:      "civquest",
		Description: "Secure settlement storehouse and bulk resource storage facility",
		Tags:        []string{"logistics", "storage", "storehouse", "resources"},
		Dimensions:  Dimensions{Width: w, Height: h, Length: l, MinY: 0},
		Palette:     palette,
		Layers:      layers,
	}
	_ = bp.Validate()
	return bp
}
