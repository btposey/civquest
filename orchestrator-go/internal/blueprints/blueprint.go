package blueprints

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrNilBlueprint           = errors.New("blueprint cannot be nil")
	ErrEmptyName              = errors.New("blueprint name cannot be empty")
	ErrInvalidID              = errors.New("invalid blueprint id")
	ErrInvalidPurpose         = errors.New("invalid blueprint purpose")
	ErrInvalidDimensions      = errors.New("blueprint dimensions must be positive integers")
	ErrInvalidPalette         = errors.New("palette contains invalid block state")
	ErrOutOfBounds            = errors.New("block coordinate out of blueprint dimensions bounds")
	ErrLayerDimensionMismatch = errors.New("layer matrix dimension mismatch with width/length")
	ErrBlueprintNotFound      = errors.New("blueprint not found")
)

// Standard architectural civic purpose constants.
const (
	PurposeShelter    = "SHELTER"
	PurposeHome       = "HOME"
	PurposeLibrary    = "LIBRARY"
	PurposeTownhall   = "TOWNHALL"
	PurposeTavern     = "TAVERN"
	PurposeArmory     = "ARMORY"
	PurposeFarm       = "FARM"
	PurposeStorehouse = "STOREHOUSE"
	PurposeGranary    = "GRANARY" // Alias for STOREHOUSE
	PurposeBarracks   = "BARRACKS"
	PurposeWorkshop   = "WORKSHOP"
)

// Dimensions represents the 3D bounding box for a structure.
type Dimensions struct {
	Width  int `yaml:"width" json:"width"`               // X size
	Height int `yaml:"height" json:"height"`             // Y size
	Length int `yaml:"length" json:"length"`             // Z size
	MinY   int `yaml:"min_y,omitempty" json:"min_y,omitempty"` // Lowest elevation (<=0 for subterranean basements)
}

// Layer represents a 2D horizontal slice of a blueprint at elevation Y.
type Layer struct {
	Y      int     `yaml:"y" json:"y"`
	Matrix [][]int `yaml:"matrix" json:"matrix"` // Row is Z (length), Column is X (width)
}

// BlockPlacement represents an explicit voxel placement.
type BlockPlacement struct {
	X     int    `yaml:"x" json:"x"`
	Y     int    `yaml:"y" json:"y"`
	Z     int    `yaml:"z" json:"z"`
	State string `yaml:"state" json:"state"`
}

// Blueprint specifies an architectural construction plan.
type Blueprint struct {
	ID          string           `yaml:"id" json:"id"`
	Name        string           `yaml:"name" json:"name"`
	Version     int              `yaml:"version" json:"version"`
	Purpose     string           `yaml:"purpose" json:"purpose"` // SHELTER, HOME, LIBRARY, TOWNHALL, TAVERN, ARMORY, FARM, STOREHOUSE, BARRACKS, WORKSHOP
	Type        string           `yaml:"type" json:"type"`       // Legacy category/type, synced with Purpose
	Style       string           `yaml:"style" json:"style"`     // e.g. "pioneer", "nordic", "stone"
	Author      string           `yaml:"author,omitempty" json:"author,omitempty"`
	Description string           `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string         `yaml:"tags,omitempty" json:"tags,omitempty"`
	Dimensions  Dimensions       `yaml:"dimensions" json:"dimensions"`
	BOM         map[string]int   `yaml:"bom,omitempty" json:"bom,omitempty"`
	Excavation  int              `yaml:"excavation,omitempty" json:"excavation,omitempty"`
	Palette     map[int]string   `yaml:"palette,omitempty" json:"palette,omitempty"`
	Layers      []Layer          `yaml:"layers,omitempty" json:"layers,omitempty"`
	Blocks      []BlockPlacement `yaml:"blocks,omitempty" json:"blocks,omitempty"`
}

// Slugify converts an arbitrary string into a normalized lowercase identifier [a-z0-9_].
func Slugify(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var sb strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore && sb.Len() > 0 {
			sb.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(sb.String(), "_")
}

// isValidID checks if an identifier is valid (non-empty lowercase alphanumeric, hyphen, underscore).
func isValidID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i, r := range id {
		if i == 0 && !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// IsValidPurpose checks whether the given civic purpose identifier is valid.
func IsValidPurpose(purpose string) bool {
	switch strings.ToUpper(strings.TrimSpace(purpose)) {
	case PurposeShelter, PurposeHome, PurposeLibrary, PurposeTownhall, PurposeTavern,
		PurposeArmory, PurposeFarm, PurposeStorehouse, PurposeGranary, PurposeBarracks, PurposeWorkshop:
		return true
	default:
		return false
	}
}

// NormalizePurpose converts a raw purpose or type string into a canonical uppercase purpose.
func NormalizePurpose(raw string) string {
	p := strings.ToUpper(strings.TrimSpace(raw))
	switch p {
	case "HOUSE", "RESIDENCE":
		return PurposeHome
	case "GRANARY":
		return PurposeStorehouse
	case "WATCHTOWER":
		return PurposeBarracks
	case PurposeShelter, PurposeHome, PurposeLibrary, PurposeTownhall, PurposeTavern,
		PurposeArmory, PurposeFarm, PurposeStorehouse, PurposeBarracks, PurposeWorkshop:
		return p
	default:
		return p
	}
}

// Validate checks the structural integrity, bounds, ID, purpose, and palette validity of the blueprint.
// It also automatically computes and populates the BOM if empty.
func (b *Blueprint) Validate() error {
	if b == nil {
		return ErrNilBlueprint
	}
	if strings.TrimSpace(b.Name) == "" {
		return ErrEmptyName
	}

	// 1. Validate / default ID
	b.ID = strings.ToLower(strings.TrimSpace(b.ID))
	if b.ID == "" {
		b.ID = Slugify(b.Name)
	}
	if b.ID == "" || !isValidID(b.ID) {
		return fmt.Errorf("%w: %q", ErrInvalidID, b.ID)
	}

	// 2. Validate / default Purpose
	b.Purpose = NormalizePurpose(b.Purpose)
	if b.Purpose == "" {
		if b.Type != "" {
			b.Purpose = NormalizePurpose(b.Type)
		}
		if b.Purpose == "" || !IsValidPurpose(b.Purpose) {
			b.Purpose = PurposeHome
		}
	} else if !IsValidPurpose(b.Purpose) {
		return fmt.Errorf("%w: %q", ErrInvalidPurpose, b.Purpose)
	}

	// Sync Type for backward compatibility if empty
	if strings.TrimSpace(b.Type) == "" {
		b.Type = strings.ToLower(b.Purpose)
	}

	// 3. Dimensions validation
	if b.Dimensions.Width <= 0 || b.Dimensions.Height <= 0 || b.Dimensions.Length <= 0 {
		return fmt.Errorf("%w: width=%d, height=%d, length=%d",
			ErrInvalidDimensions, b.Dimensions.Width, b.Dimensions.Height, b.Dimensions.Length)
	}

	// Default MinY if negative Y layers or blocks exist and MinY was not set
	minY := b.Dimensions.MinY
	for _, l := range b.Layers {
		if l.Y < minY {
			minY = l.Y
		}
	}
	for _, bp := range b.Blocks {
		if bp.Y < minY {
			minY = bp.Y
		}
	}
	if b.Dimensions.MinY == 0 && minY < 0 {
		b.Dimensions.MinY = minY
	}

	maxY := b.Dimensions.MinY + b.Dimensions.Height - 1

	// Validate palette entries if present
	for idx, state := range b.Palette {
		cleanState := strings.TrimSpace(state)
		if cleanState == "" {
			return fmt.Errorf("%w: palette index %d has empty state", ErrInvalidPalette, idx)
		}
		base := NormalizeItemID(cleanState)
		if !strings.Contains(base, ":") {
			return fmt.Errorf("%w: palette index %d state %q missing namespace prefix", ErrInvalidPalette, idx, state)
		}
		// Validate bracketed property syntax if present
		bStart := strings.IndexByte(cleanState, '[')
		bEnd := strings.IndexByte(cleanState, ']')
		if (bStart != -1 && bEnd == -1) || (bStart == -1 && bEnd != -1) || (bStart > bEnd) {
			return fmt.Errorf("%w: palette index %d state %q has malformed bracketed properties", ErrInvalidPalette, idx, state)
		}
	}

	// Validate layers
	for _, l := range b.Layers {
		if l.Y < b.Dimensions.MinY || l.Y > maxY {
			return fmt.Errorf("%w: layer y=%d out of bounds [%d, %d]", ErrOutOfBounds, l.Y, b.Dimensions.MinY, maxY)
		}
		if len(l.Matrix) > 0 {
			if len(l.Matrix) != b.Dimensions.Length {
				return fmt.Errorf("%w: layer y=%d rows=%d expected length=%d",
					ErrLayerDimensionMismatch, l.Y, len(l.Matrix), b.Dimensions.Length)
			}
			for z, row := range l.Matrix {
				if len(row) != b.Dimensions.Width {
					return fmt.Errorf("%w: layer y=%d row z=%d cols=%d expected width=%d",
						ErrLayerDimensionMismatch, l.Y, z, len(row), b.Dimensions.Width)
				}
				for x, palIdx := range row {
					if palIdx != 0 && b.Palette != nil {
						if _, ok := b.Palette[palIdx]; !ok {
							return fmt.Errorf("%w: layer y=%d (x=%d, z=%d) references unmapped palette index %d",
								ErrInvalidPalette, l.Y, x, z, palIdx)
						}
					}
				}
			}
		}
	}

	// Validate explicit block placements
	for _, bp := range b.Blocks {
		if bp.X < 0 || bp.X >= b.Dimensions.Width ||
			bp.Y < b.Dimensions.MinY || bp.Y > maxY ||
			bp.Z < 0 || bp.Z >= b.Dimensions.Length {
			return fmt.Errorf("%w: block at (%d,%d,%d) exceeds bounds [0..%d, %d..%d, 0..%d]",
				ErrOutOfBounds, bp.X, bp.Y, bp.Z, b.Dimensions.Width-1, b.Dimensions.MinY, maxY, b.Dimensions.Length-1)
		}
		if strings.TrimSpace(bp.State) == "" {
			return fmt.Errorf("%w: block at (%d,%d,%d) has empty state", ErrInvalidPalette, bp.X, bp.Y, bp.Z)
		}
	}

	// 4. Calculate and populate BOM automatically if empty
	if len(b.BOM) == 0 {
		b.CalculateBOM()
	}

	// 5. Track subterranean excavation volume if subterranean levels exist
	b.Excavation = b.ComputeExcavation()

	return nil
}

// CalculateBOM computes the total Bill of Materials (non-air block counts) required to construct the blueprint.
// Directional block state properties (e.g. axis=y, facing=north) are normalized so all variants aggregate
// into the canonical base item ID (e.g. oak_stairs[facing=north] -> oak_stairs).
// It also updates and populates b.BOM.
func (b *Blueprint) CalculateBOM() map[string]int {
	bom := make(map[string]int)
	if b == nil {
		return bom
	}

	// 1. Tally from layers and palette
	for _, l := range b.Layers {
		for _, row := range l.Matrix {
			for _, palIdx := range row {
				if palIdx == 0 {
					continue // Air
				}
				state := b.Palette[palIdx]
				if state != "" {
					baseItem := NormalizeItemID(state)
					if !isAir(baseItem) {
						bom[baseItem]++
					}
				}
			}
		}
	}

	// 2. Tally from explicit blocks
	for _, bp := range b.Blocks {
		if bp.State != "" {
			baseItem := NormalizeItemID(bp.State)
			if !isAir(baseItem) {
				bom[baseItem]++
			}
		}
	}

	// 3. Multi-block items accounting:
	// In Minecraft, a bed occupies 2 horizontal voxels (head + foot) and a door occupies 2 vertical voxels (lower + upper).
	// A paired 2-block bed or 2-block door registers as 1 item in the BOM, not 2.
	for item, count := range bom {
		if (strings.HasSuffix(item, "_door") && !strings.HasSuffix(item, "_trapdoor")) ||
			item == "minecraft:iron_door" || strings.Contains(item, ":doors") ||
			strings.HasSuffix(item, "_bed") || strings.Contains(item, ":beds") {
			bom[item] = (count + 1) / 2
		}
	}

	b.BOM = bom
	return bom
}

// ComputeExcavation calculates the total subterranean excavation volume (in cubic blocks)
// required for negative Y layers (Y < 0). Any interior voxel within the perimeter bounds
// that is air or to-be-excavated is tracked as excavation volume.
func (b *Blueprint) ComputeExcavation() int {
	if b == nil {
		return 0
	}

	totalExcavation := 0

	// 1. Calculate from layers
	for _, l := range b.Layers {
		if l.Y >= 0 || len(l.Matrix) == 0 {
			continue
		}

		minX, maxX := -1, -1
		minZ, maxZ := -1, -1

		// Find non-air bounding box
		for z, row := range l.Matrix {
			for x, palIdx := range row {
				if palIdx != 0 && b.Palette != nil {
					state := b.Palette[palIdx]
					if state != "" && !isAir(state) {
						if minX == -1 || x < minX {
							minX = x
						}
						if maxX == -1 || x > maxX {
							maxX = x
						}
						if minZ == -1 || z < minZ {
							minZ = z
						}
						if maxZ == -1 || z > maxZ {
							maxZ = z
						}
					}
				}
			}
		}

		// Fallback to layer dimensions if no placed blocks
		if minX == -1 {
			minX = 0
			maxX = b.Dimensions.Width - 1
			minZ = 0
			maxZ = b.Dimensions.Length - 1
		}

		for z := minZ; z <= maxZ; z++ {
			if z < 0 || z >= len(l.Matrix) {
				continue
			}
			row := l.Matrix[z]
			for x := minX; x <= maxX; x++ {
				if x < 0 || x >= len(row) {
					continue
				}
				isInterior := x > minX && x < maxX && z > minZ && z < maxZ
				palIdx := row[x]
				isAirVoxel := palIdx == 0 || (b.Palette != nil && isAir(b.Palette[palIdx]))
				if isInterior || isAirVoxel {
					totalExcavation++
				}
			}
		}
	}

	// 2. Calculate from explicit blocks if no layers present
	if len(b.Layers) == 0 && len(b.Blocks) > 0 {
		byY := make(map[int][]BlockPlacement)
		for _, blk := range b.Blocks {
			if blk.Y < 0 {
				byY[blk.Y] = append(byY[blk.Y], blk)
			}
		}

		for _, blks := range byY {
			minX, maxX := -1, -1
			minZ, maxZ := -1, -1
			blockMap := make(map[[2]int]string)
			for _, blk := range blks {
				if !isAir(blk.State) {
					if minX == -1 || blk.X < minX {
						minX = blk.X
					}
					if maxX == -1 || blk.X > maxX {
						maxX = blk.X
					}
					if minZ == -1 || blk.Z < minZ {
						minZ = blk.Z
					}
					if maxZ == -1 || blk.Z > maxZ {
						maxZ = blk.Z
					}
				}
				blockMap[[2]int{blk.X, blk.Z}] = blk.State
			}
			if minX != -1 {
				for z := minZ; z <= maxZ; z++ {
					for x := minX; x <= maxX; x++ {
						isInterior := x > minX && x < maxX && z > minZ && z < maxZ
						st := blockMap[[2]int{x, z}]
						isAirVoxel := st == "" || isAir(st)
						if isInterior || isAirVoxel {
							totalExcavation++
						}
					}
				}
			}
		}
	}

	return totalExcavation
}

func isAir(state string) bool {
	clean := strings.ToLower(strings.TrimSpace(state))
	return clean == "minecraft:air" || clean == "minecraft:cave_air" || clean == "minecraft:void_air" || clean == "air"
}

// GenerateTemplate synthesizes a valid structured architectural blueprint based on type and style.
func GenerateTemplate(bType, style string) *Blueprint {
	cleanType := strings.ToLower(strings.TrimSpace(bType))
	if cleanType == "" {
		cleanType = "house"
	}
	cleanStyle := strings.ToLower(strings.TrimSpace(style))
	if cleanStyle == "" {
		cleanStyle = "pioneer"
	}

	purpose := NormalizePurpose(cleanType)
	name := fmt.Sprintf("%s %s", strings.Title(cleanStyle), strings.Title(cleanType))
	w, h, l := 5, 4, 5

	if cleanType == "tavern" || cleanType == "granary" || cleanType == "storehouse" {
		w, h, l = 7, 5, 7
	} else if cleanType == "watchtower" || cleanType == "barracks" {
		w, h, l = 5, 9, 5
	}

	palette := map[int]string{
		0: "minecraft:air",
		1: "minecraft:cobblestone",
		2: "minecraft:oak_planks",
		3: "minecraft:oak_log[axis=y]",
		4: "minecraft:glass_pane",
		5: "minecraft:torch",
	}

	var layers []Layer
	for y := 0; y < h; y++ {
		mat := make([][]int, l)
		for z := 0; z < l; z++ {
			row := make([]int, w)
			for x := 0; x < w; x++ {
				isCorner := (x == 0 || x == w-1) && (z == 0 || z == l-1)
				isEdge := (x == 0 || x == w-1 || z == 0 || z == l-1)

				if y == 0 {
					// Foundation: Cobblestone
					row[x] = 1
				} else if y == h-1 {
					// Roof / Ceiling: Oak Planks
					row[x] = 2
				} else if isCorner {
					// Corner pillars: Oak Log
					row[x] = 3
				} else if isEdge {
					// Walls: Oak Planks, windows in middle
					if (x == w/2 || z == l/2) && y == 1 {
						row[x] = 4 // Glass pane
					} else {
						row[x] = 2 // Planks
					}
				} else {
					row[x] = 0 // Interior air
				}
			}
			mat[z] = row
		}
		layers = append(layers, Layer{Y: y, Matrix: mat})
	}

	bp := &Blueprint{
		ID:      Slugify(name),
		Name:    name,
		Version: 1,
		Purpose: purpose,
		Type:    cleanType,
		Style:   cleanStyle,
		Author:  "cq",
		Dimensions: Dimensions{
			Width:  w,
			Height: h,
			Length: l,
		},
		Palette: palette,
		Layers:  layers,
	}
	_ = bp.Validate()
	return bp
}

// LoadYAML reads and parses a blueprint YAML file.
func LoadYAML(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint file %s: %w", path, err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("parsing blueprint YAML %s: %w", path, err)
	}

	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("invalid blueprint %s: %w", path, err)
	}

	return &bp, nil
}

// SaveYAML saves the blueprint to disk in YAML format.
func SaveYAML(path string, bp *Blueprint) error {
	if bp == nil {
		return ErrNilBlueprint
	}
	return bp.SaveYAML(path)
}

// SaveYAML saves the blueprint to disk in YAML format.
func (b *Blueprint) SaveYAML(path string) error {
	if err := b.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(b)
	if err != nil {
		return fmt.Errorf("marshaling blueprint YAML: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing blueprint file %s: %w", path, err)
	}

	return nil
}

// LoadJSON reads and parses a blueprint JSON file.
func LoadJSON(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint file %s: %w", path, err)
	}

	var bp Blueprint
	if err := json.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("parsing blueprint JSON %s: %w", path, err)
	}

	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("invalid blueprint %s: %w", path, err)
	}

	return &bp, nil
}

// SaveJSON saves the blueprint to disk in JSON format.
func SaveJSON(path string, bp *Blueprint) error {
	if bp == nil {
		return ErrNilBlueprint
	}
	return bp.SaveJSON(path)
}

// SaveJSON saves the blueprint to disk in JSON format.
func (b *Blueprint) SaveJSON(path string) error {
	if err := b.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling blueprint JSON: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing blueprint file %s: %w", path, err)
	}

	return nil
}

// LoadFile reads and parses a blueprint file, auto-detecting format from extension (.json vs .yaml/.yml).
func LoadFile(path string) (*Blueprint, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return LoadJSON(path)
	case ".yaml", ".yml":
		return LoadYAML(path)
	default:
		// Attempt JSON first, then YAML fallback
		bp, err := LoadJSON(path)
		if err == nil {
			return bp, nil
		}
		return LoadYAML(path)
	}
}

// SaveFile saves the blueprint to disk, choosing JSON or YAML based on file extension.
func SaveFile(path string, bp *Blueprint) error {
	if bp == nil {
		return ErrNilBlueprint
	}
	return bp.SaveFile(path)
}

// SaveFile saves the blueprint to disk, choosing JSON or YAML based on file extension.
func (b *Blueprint) SaveFile(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		return b.SaveJSON(path)
	}
	return b.SaveYAML(path)
}

// ToVoxelMatrix converts the blueprint into an explicit 3D VoxelMatrix.
func (b *Blueprint) ToVoxelMatrix() (*VoxelMatrix, error) {
	return FromBlueprint(b)
}

// FromVoxelMatrix repopulates the blueprint from a 3D VoxelMatrix.
func (b *Blueprint) FromVoxelMatrix(vm *VoxelMatrix) {
	if vm == nil {
		return
	}
	newBp := vm.ToBlueprint(b.Name, b.Type, b.Style, b.Author)
	b.Dimensions = newBp.Dimensions
	b.Palette = newBp.Palette
	b.Layers = newBp.Layers
	b.Blocks = nil
	b.CalculateBOM()
}
