package blueprints

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrVoxelOutOfBounds = errors.New("voxel coordinates out of bounds")
	ErrInvalidVoxelData = errors.New("invalid voxel data")
)

// BlockState represents a Minecraft block identifier and its state properties (e.g. facing=north, axis=y).
type BlockState struct {
	Identifier string            `json:"identifier" yaml:"identifier"`
	Properties map[string]string `json:"properties,omitempty" yaml:"properties,omitempty"`
}

// ParseBlockState parses a block state string (e.g. "minecraft:oak_stairs[facing=north,half=bottom]" or "stone").
func ParseBlockState(raw string) BlockState {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return BlockState{
			Identifier: "minecraft:air",
			Properties: make(map[string]string),
		}
	}

	bracketStart := strings.IndexByte(clean, '[')
	bracketEnd := strings.IndexByte(clean, ']')

	var identifier string
	props := make(map[string]string)

	if bracketStart != -1 && bracketEnd > bracketStart {
		identifier = strings.TrimSpace(clean[:bracketStart])
		propContent := strings.TrimSpace(clean[bracketStart+1 : bracketEnd])
		if propContent != "" {
			pairs := strings.Split(propContent, ",")
			for _, pair := range pairs {
				kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
				if len(kv) == 2 {
					key := strings.TrimSpace(kv[0])
					val := strings.TrimSpace(kv[1])
					if key != "" {
						props[key] = val
					}
				}
			}
		}
	} else {
		identifier = clean
	}

	// Normalize namespace
	if !strings.Contains(identifier, ":") {
		identifier = "minecraft:" + identifier
	}

	return BlockState{
		Identifier: identifier,
		Properties: props,
	}
}

// String formats the BlockState back into its canonical Minecraft block state string.
func (b BlockState) String() string {
	id := b.Identifier
	if id == "" {
		id = "minecraft:air"
	} else if !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}

	if len(b.Properties) == 0 {
		return id
	}

	keys := make([]string, 0, len(b.Properties))
	for k := range b.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(id)
	sb.WriteByte('[')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(b.Properties[k])
	}
	sb.WriteByte(']')
	return sb.String()
}

// BaseItem returns the bare namespaced item ID suitable for granary storage and inventory tracking (e.g. "minecraft:oak_stairs").
func (b BlockState) BaseItem() string {
	id := b.Identifier
	if id == "" {
		return "minecraft:air"
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

// IsAir returns true if the block state represents an air block.
func (b BlockState) IsAir() bool {
	id := strings.ToLower(b.BaseItem())
	return id == "minecraft:air" || id == "minecraft:cave_air" || id == "minecraft:void_air"
}

// Equals checks equality of identifier and all properties.
func (b BlockState) Equals(other BlockState) bool {
	if b.BaseItem() != other.BaseItem() {
		return false
	}
	if len(b.Properties) != len(other.Properties) {
		return false
	}
	for k, v := range b.Properties {
		if other.Properties[k] != v {
			return false
		}
	}
	return true
}

// VoxelMatrix represents an explicit 3D bounding box matrix of voxel block states.
type VoxelMatrix struct {
	Dimensions Dimensions
	voxels     [][][]BlockState // [y][z][x]
}

// NewVoxelMatrix creates an empty (air-filled) 3D voxel matrix.
func NewVoxelMatrix(width, height, length int) *VoxelMatrix {
	if width <= 0 || height <= 0 || length <= 0 {
		width = 1
		height = 1
		length = 1
	}

	air := ParseBlockState("minecraft:air")
	voxels := make([][][]BlockState, height)
	for y := 0; y < height; y++ {
		layer := make([][]BlockState, length)
		for z := 0; z < length; z++ {
			row := make([]BlockState, width)
			for x := 0; x < width; x++ {
				row[x] = air
			}
			layer[z] = row
		}
		voxels[y] = layer
	}

	return &VoxelMatrix{
		Dimensions: Dimensions{
			Width:  width,
			Height: height,
			Length: length,
		},
		voxels: voxels,
	}
}

// BoundsCheck returns an error if coordinates fall outside matrix bounds.
func (vm *VoxelMatrix) BoundsCheck(x, y, z int) error {
	if vm == nil {
		return ErrNilBlueprint
	}
	minY := vm.Dimensions.MinY
	maxY := minY + vm.Dimensions.Height - 1
	if x < 0 || x >= vm.Dimensions.Width ||
		y < minY || y > maxY ||
		z < 0 || z >= vm.Dimensions.Length {
		return fmt.Errorf("%w: (%d,%d,%d) outside [0..%d, %d..%d, 0..%d]",
			ErrVoxelOutOfBounds, x, y, z, vm.Dimensions.Width-1, minY, maxY, vm.Dimensions.Length-1)
	}
	return nil
}

// Get returns the BlockState at coordinates (x, y, z).
func (vm *VoxelMatrix) Get(x, y, z int) (BlockState, error) {
	if err := vm.BoundsCheck(x, y, z); err != nil {
		return BlockState{}, err
	}
	arrayY := y - vm.Dimensions.MinY
	return vm.voxels[arrayY][z][x], nil
}

// Set stores a BlockState at coordinates (x, y, z).
func (vm *VoxelMatrix) Set(x, y, z int, state BlockState) error {
	if err := vm.BoundsCheck(x, y, z); err != nil {
		return err
	}
	arrayY := y - vm.Dimensions.MinY
	vm.voxels[arrayY][z][x] = state
	return nil
}

// SetStateString parses raw block state string and stores it at (x, y, z).
func (vm *VoxelMatrix) SetStateString(x, y, z int, raw string) error {
	return vm.Set(x, y, z, ParseBlockState(raw))
}

// CalculateBOM tallies all non-air blocks present in the matrix, normalized by base item ID.
func (vm *VoxelMatrix) CalculateBOM() map[string]int {
	bom := make(map[string]int)
	if vm == nil {
		return bom
	}

	for y := 0; y < vm.Dimensions.Height; y++ {
		for z := 0; z < vm.Dimensions.Length; z++ {
			for x := 0; x < vm.Dimensions.Width; x++ {
				state := vm.voxels[y][z][x]
				if !state.IsAir() {
					bom[state.BaseItem()]++
				}
			}
		}
	}
	return bom
}

// Clone creates a deep copy of the voxel matrix.
func (vm *VoxelMatrix) Clone() *VoxelMatrix {
	if vm == nil {
		return nil
	}
	clone := NewVoxelMatrix(vm.Dimensions.Width, vm.Dimensions.Height, vm.Dimensions.Length)
	clone.Dimensions.MinY = vm.Dimensions.MinY
	for y := 0; y < vm.Dimensions.Height; y++ {
		for z := 0; z < vm.Dimensions.Length; z++ {
			for x := 0; x < vm.Dimensions.Width; x++ {
				props := make(map[string]string)
				for k, v := range vm.voxels[y][z][x].Properties {
					props[k] = v
				}
				clone.voxels[y][z][x] = BlockState{
					Identifier: vm.voxels[y][z][x].Identifier,
					Properties: props,
				}
			}
		}
	}
	return clone
}

// FromBlueprint converts a Blueprint into a 3D VoxelMatrix.
func FromBlueprint(bp *Blueprint) (*VoxelMatrix, error) {
	if bp == nil {
		return nil, ErrNilBlueprint
	}
	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("validating blueprint: %w", err)
	}

	vm := NewVoxelMatrix(bp.Dimensions.Width, bp.Dimensions.Height, bp.Dimensions.Length)
	vm.Dimensions.MinY = bp.Dimensions.MinY

	// 1. Populate from layers & palette
	for _, l := range bp.Layers {
		for z, row := range l.Matrix {
			for x, palIdx := range row {
				if palIdx == 0 {
					continue
				}
				rawState := bp.Palette[palIdx]
				if rawState != "" {
					state := ParseBlockState(rawState)
					if err := vm.Set(x, l.Y, z, state); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	// 2. Populate from explicit blocks
	for _, b := range bp.Blocks {
		state := ParseBlockState(b.State)
		if err := vm.Set(b.X, b.Y, b.Z, state); err != nil {
			return nil, err
		}
	}

	return vm, nil
}

// ToBlueprint converts the 3D VoxelMatrix back into a structured Blueprint.
func (vm *VoxelMatrix) ToBlueprint(name, bpType, style, author string) *Blueprint {
	if vm == nil {
		return nil
	}

	palette := make(map[int]string)
	stateToIdx := make(map[string]int)

	palette[0] = "minecraft:air"
	stateToIdx["minecraft:air"] = 0
	nextIdx := 1

	layers := make([]Layer, vm.Dimensions.Height)
	for y := 0; y < vm.Dimensions.Height; y++ {
		mat := make([][]int, vm.Dimensions.Length)
		for z := 0; z < vm.Dimensions.Length; z++ {
			row := make([]int, vm.Dimensions.Width)
			for x := 0; x < vm.Dimensions.Width; x++ {
				st := vm.voxels[y][z][x]
				if st.IsAir() {
					row[x] = 0
					continue
				}
				stStr := st.String()
				idx, exists := stateToIdx[stStr]
				if !exists {
					idx = nextIdx
					stateToIdx[stStr] = idx
					palette[idx] = stStr
					nextIdx++
				}
				row[x] = idx
			}
			mat[z] = row
		}
		layers[y] = Layer{
			Y:      y + vm.Dimensions.MinY,
			Matrix: mat,
		}
	}

	if name == "" {
		name = fmt.Sprintf("%s %s", strings.Title(style), strings.Title(bpType))
	}

	bp := &Blueprint{
		ID:         Slugify(name),
		Name:       name,
		Version:    1,
		Purpose:    NormalizePurpose(bpType),
		Type:       bpType,
		Style:      style,
		Author:     author,
		Dimensions: vm.Dimensions,
		Palette:    palette,
		Layers:     layers,
	}
	_ = bp.Validate()
	return bp
}

// rawYAMLMatrixFormat is used to parse YAML that defines matrices directly.
type rawYAMLMatrixFormat struct {
	Name       string           `yaml:"name"`
	Type       string           `yaml:"type"`
	Style      string           `yaml:"style"`
	Author     string           `yaml:"author"`
	Dimensions Dimensions       `yaml:"dimensions"`
	Palette    map[int]string   `yaml:"palette"`
	Layers     []Layer          `yaml:"layers"`
	Matrix3D   [][][]int        `yaml:"matrix"` // [y][z][x]
	Blocks     []BlockPlacement `yaml:"blocks"`
	Voxels     []struct {
		X     int    `yaml:"x"`
		Y     int    `yaml:"y"`
		Z     int    `yaml:"z"`
		State string `yaml:"state"`
	} `yaml:"voxels"`
}

// ParseYAMLMatrix parses raw YAML into a 3D VoxelMatrix.
// Supports standard layer blueprints, explicit 3D matrix arrays, or voxel coordinate lists.
func ParseYAMLMatrix(data []byte) (*VoxelMatrix, error) {
	var raw rawYAMLMatrixFormat
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshaling YAML matrix: %w", err)
	}

	if raw.Dimensions.Width <= 0 || raw.Dimensions.Height <= 0 || raw.Dimensions.Length <= 0 {
		return nil, ErrInvalidDimensions
	}

	vm := NewVoxelMatrix(raw.Dimensions.Width, raw.Dimensions.Height, raw.Dimensions.Length)

	// 1. Direct 3D matrix with palette
	if len(raw.Matrix3D) > 0 {
		if raw.Palette == nil {
			raw.Palette = make(map[int]string)
		}
		if _, ok := raw.Palette[0]; !ok {
			raw.Palette[0] = "minecraft:air"
		}

		for y, layer := range raw.Matrix3D {
			if y >= vm.Dimensions.Height {
				break
			}
			for z, row := range layer {
				if z >= vm.Dimensions.Length {
					break
				}
				for x, palIdx := range row {
					if x >= vm.Dimensions.Width {
						break
					}
					if palIdx != 0 {
						stStr := raw.Palette[palIdx]
						if stStr != "" {
							_ = vm.Set(x, y, z, ParseBlockState(stStr))
						}
					}
				}
			}
		}
	}

	// 2. Layers format
	if len(raw.Layers) > 0 {
		bp := &Blueprint{
			Name:       raw.Name,
			Dimensions: raw.Dimensions,
			Palette:    raw.Palette,
			Layers:     raw.Layers,
		}
		if bp.Name == "" {
			bp.Name = "Unnamed"
		}
		fromBp, err := FromBlueprint(bp)
		if err != nil {
			return nil, err
		}
		return fromBp, nil
	}

	// 3. Explicit voxels list
	for _, v := range raw.Voxels {
		if err := vm.Set(v.X, v.Y, v.Z, ParseBlockState(v.State)); err != nil {
			return nil, err
		}
	}

	// 4. Explicit blocks list
	for _, b := range raw.Blocks {
		if err := vm.Set(b.X, b.Y, b.Z, ParseBlockState(b.State)); err != nil {
			return nil, err
		}
	}

	return vm, nil
}

// LoadYAMLMatrix loads and parses a blueprint YAML file into a 3D VoxelMatrix.
func LoadYAMLMatrix(path string) (*VoxelMatrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading YAML matrix file %s: %w", path, err)
	}
	return ParseYAMLMatrix(data)
}
