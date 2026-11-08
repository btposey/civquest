package blueprints

import (
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
	ErrInvalidDimensions      = errors.New("blueprint dimensions must be positive integers")
	ErrInvalidPalette         = errors.New("palette contains invalid block state")
	ErrOutOfBounds            = errors.New("block coordinate out of blueprint dimensions bounds")
	ErrLayerDimensionMismatch = errors.New("layer matrix dimension mismatch with width/length")
)

// Dimensions represents the 3D bounding box for a structure.
type Dimensions struct {
	Width  int `yaml:"width" json:"width"`   // X size
	Height int `yaml:"height" json:"height"` // Y size
	Length int `yaml:"length" json:"length"` // Z size
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
	Name       string           `yaml:"name" json:"name"`
	Version    int              `yaml:"version" json:"version"`
	Type       string           `yaml:"type" json:"type"`   // e.g. "tavern", "granary", "house", "watchtower"
	Style      string           `yaml:"style" json:"style"` // e.g. "pioneer", "nordic", "stone"
	Author     string           `yaml:"author,omitempty" json:"author,omitempty"`
	Dimensions Dimensions       `yaml:"dimensions" json:"dimensions"`
	Palette    map[int]string   `yaml:"palette,omitempty" json:"palette,omitempty"`
	Layers     []Layer          `yaml:"layers,omitempty" json:"layers,omitempty"`
	Blocks     []BlockPlacement `yaml:"blocks,omitempty" json:"blocks,omitempty"`
}

// Validate checks the structural integrity, bounds, and palette validity of the blueprint.
func (b *Blueprint) Validate() error {
	if b == nil {
		return ErrNilBlueprint
	}
	if strings.TrimSpace(b.Name) == "" {
		return ErrEmptyName
	}
	if b.Dimensions.Width <= 0 || b.Dimensions.Height <= 0 || b.Dimensions.Length <= 0 {
		return fmt.Errorf("%w: width=%d, height=%d, length=%d",
			ErrInvalidDimensions, b.Dimensions.Width, b.Dimensions.Height, b.Dimensions.Length)
	}

	// Validate palette entries if present
	for idx, state := range b.Palette {
		if strings.TrimSpace(state) == "" {
			return fmt.Errorf("%w: palette index %d has empty state", ErrInvalidPalette, idx)
		}
		if !strings.Contains(state, ":") {
			return fmt.Errorf("%w: palette index %d state %q missing namespace prefix", ErrInvalidPalette, idx, state)
		}
	}

	// Validate layers
	for _, l := range b.Layers {
		if l.Y < 0 || l.Y >= b.Dimensions.Height {
			return fmt.Errorf("%w: layer y=%d out of height %d", ErrOutOfBounds, l.Y, b.Dimensions.Height)
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
			bp.Y < 0 || bp.Y >= b.Dimensions.Height ||
			bp.Z < 0 || bp.Z >= b.Dimensions.Length {
			return fmt.Errorf("%w: block at (%d,%d,%d) exceeds bounds (%d,%d,%d)",
				ErrOutOfBounds, bp.X, bp.Y, bp.Z, b.Dimensions.Width, b.Dimensions.Height, b.Dimensions.Length)
		}
		if strings.TrimSpace(bp.State) == "" {
			return fmt.Errorf("%w: block at (%d,%d,%d) has empty state", ErrInvalidPalette, bp.X, bp.Y, bp.Z)
		}
	}

	return nil
}

// CalculateBOM computes the total Bill of Materials (non-air block counts) required to construct the blueprint.
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
				if state != "" && !isAir(state) {
					bom[state]++
				}
			}
		}
	}

	// 2. Tally from explicit blocks
	for _, bp := range b.Blocks {
		if bp.State != "" && !isAir(bp.State) {
			bom[bp.State]++
		}
	}

	return bom
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

	name := fmt.Sprintf("%s %s", strings.Title(cleanStyle), strings.Title(cleanType))
	w, h, l := 5, 4, 5

	if cleanType == "tavern" || cleanType == "granary" {
		w, h, l = 7, 5, 7
	} else if cleanType == "watchtower" {
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

	return &Blueprint{
		Name:    name,
		Version: 1,
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
