package blueprints

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrNoLayersFound     = errors.New("no layers found in ASCII slice input")
	ErrEmptyGridRow      = errors.New("empty grid row encountered in layer")
	ErrUnknownChar       = errors.New("unmapped character encountered in ASCII grid")
	ErrDimensionMismatch = errors.New("inconsistent dimensions in ASCII grid layers")
)

// Standard mnemonic characters for common Minecraft architectural block families.
var mnemonicChars = []struct {
	keyword string
	char    byte
}{
	{"log", 'L'},
	{"plank", 'P'},
	{"stair", 'S'},
	{"slab", 'B'},
	{"door", 'D'},
	{"glass", 'G'},
	{"fence", 'F'},
	{"wall", 'W'},
	{"cobble", 'C'},
	{"stone", 'C'},
	{"brick", 'R'},
	{"wool", 'O'},
	{"chest", 'X'},
	{"barrel", 'X'},
	{"bed", 'E'},
	{"lantern", 'T'},
	{"torch", 'T'},
	{"table", 'K'},
	{"furnace", 'U'},
	{"iron", 'I'},
	{"gold", 'M'},
	{"water", 'H'},
}

const charPool = "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456789#+*@abcdefghijklmnopqrstuvwxyz"

// FormatASCIISlices serializes a Blueprint's vertical layers into a token-efficient
// ASCII character slice representation (<400 tokens per building) complete with a character palette legend.
func FormatASCIISlices(bp *Blueprint) string {
	if bp == nil {
		return ""
	}

	// If bp has blocks but no layers, attempt reconstruction
	workingBP := bp
	if len(workingBP.Layers) == 0 && len(workingBP.Blocks) > 0 {
		if vm, err := FromBlueprint(workingBP); err == nil {
			workingBP = vm.ToBlueprint(bp.Name, bp.Purpose, bp.Style, bp.Author)
		}
	}

	if len(workingBP.Layers) == 0 {
		return fmt.Sprintf("# Blueprint: %s (%s)\n# Purpose: %s\n# Empty layers", bp.ID, bp.Name, bp.Purpose)
	}

	// 1. Assign single ASCII characters to each unique palette block state
	intToChar := make(map[int]byte)
	charToState := make(map[byte]string)
	usedChars := make(map[byte]bool)

	// Air is always '.'
	intToChar[0] = '.'
	charToState['.'] = "air"
	usedChars['.'] = true

	// Sort palette indices for deterministic assignment
	var palIndices []int
	for idx, state := range workingBP.Palette {
		if idx == 0 || isAir(state) {
			continue
		}
		palIndices = append(palIndices, idx)
	}
	sort.Ints(palIndices)

	for _, idx := range palIndices {
		state := workingBP.Palette[idx]
		assigned := false
		lowerState := strings.ToLower(state)

		// Try mnemonic first
		for _, m := range mnemonicChars {
			if strings.Contains(lowerState, m.keyword) && !usedChars[m.char] {
				intToChar[idx] = m.char
				charToState[m.char] = state
				usedChars[m.char] = true
				assigned = true
				break
			}
		}

		// Fallback to pool
		if !assigned {
			for i := 0; i < len(charPool); i++ {
				ch := charPool[i]
				if !usedChars[ch] {
					intToChar[idx] = ch
					charToState[ch] = state
					usedChars[ch] = true
					assigned = true
					break
				}
			}
		}

		if !assigned {
			// Last resort
			intToChar[idx] = '?'
			charToState['?'] = state
		}
	}

	// 2. Sort layers ascending by Y
	layers := make([]Layer, len(workingBP.Layers))
	copy(layers, workingBP.Layers)
	sort.Slice(layers, func(i, j int) bool {
		return layers[i].Y < layers[j].Y
	})

	var sb strings.Builder

	// Header metadata
	sb.WriteString(fmt.Sprintf("# Blueprint: %s (%s)\n", bp.ID, bp.Name))
	sb.WriteString(fmt.Sprintf("# Purpose: %s\n", bp.Purpose))
	sb.WriteString(fmt.Sprintf("# Dimensions: Width=%d, Height=%d, Length=%d, MinY=%d\n\n",
		bp.Dimensions.Width, bp.Dimensions.Height, bp.Dimensions.Length, bp.Dimensions.MinY))

	// Palette section
	sb.WriteString("Palette:\n")
	sb.WriteString(". = air\n")

	var sortedChars []byte
	for ch := range charToState {
		if ch == '.' {
			continue
		}
		sortedChars = append(sortedChars, ch)
	}
	sort.Slice(sortedChars, func(i, j int) bool {
		return sortedChars[i] < sortedChars[j]
	})

	for _, ch := range sortedChars {
		sb.WriteString(fmt.Sprintf("%c = %s\n", ch, charToState[ch]))
	}

	// Layer slices
	for _, l := range layers {
		sb.WriteString(fmt.Sprintf("\nLayer Y=%d:\n", l.Y))
		for _, row := range l.Matrix {
			for _, palIdx := range row {
				ch, ok := intToChar[palIdx]
				if !ok {
					ch = '.'
				}
				sb.WriteByte(ch)
			}
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}

// ParseASCIISlices parses raw ASCII layer slices back into a fully validated Blueprint
// with computed Bill of Materials (BOM) and subterranean excavation volume.
func ParseASCIISlices(name, purpose string, raw string, palette map[string]string) (*Blueprint, error) {
	if strings.TrimSpace(name) == "" {
		return nil, ErrEmptyName
	}

	// Character to state mapping
	charToState := make(map[byte]string)
	charToState['.'] = "minecraft:air"

	// 1. Populate from passed palette argument
	for k, v := range palette {
		trimmedK := strings.TrimSpace(k)
		trimmedV := strings.TrimSpace(v)
		if len(trimmedK) == 1 && trimmedV != "" {
			charToState[trimmedK[0]] = trimmedV
		}
	}

	// 2. Parse lines from raw string
	lines := strings.Split(raw, "\n")

	type parsedLayer struct {
		y    int
		rows []string
	}

	var parsedLayers []parsedLayer
	var currentLayer *parsedLayer
	inPaletteSection := false

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r\n")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		// Comment check
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Section headers
		lowerTrimmed := strings.ToLower(trimmed)
		if strings.HasPrefix(lowerTrimmed, "palette:") {
			inPaletteSection = true
			continue
		}

		if strings.HasPrefix(lowerTrimmed, "layer") {
			inPaletteSection = false
		}

		if inPaletteSection {
			if strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				chKey := strings.TrimSpace(parts[0])
				stateVal := strings.TrimSpace(parts[1])
				if len(chKey) == 1 && stateVal != "" {
					charToState[chKey[0]] = stateVal
				}
				continue
			}
			// If we hit something without '=' in palette section, palette is done
			inPaletteSection = false
		}

		// Check for Layer header: e.g. "Layer Y=0:" or "Layer Y = 0:" or "Layer 0:"
		if strings.HasPrefix(lowerTrimmed, "layer") {
			inPaletteSection = false
			colonIdx := strings.Index(trimmed, ":")
			headerContent := trimmed
			if colonIdx != -1 {
				headerContent = trimmed[:colonIdx]
			}
			// Extract Y value
			yVal := 0
			yIdx := strings.Index(strings.ToUpper(headerContent), "Y")
			if yIdx != -1 {
				eqIdx := strings.Index(headerContent[yIdx:], "=")
				if eqIdx != -1 {
					numStr := strings.TrimSpace(headerContent[yIdx+eqIdx+1:])
					if val, err := strconv.Atoi(numStr); err == nil {
						yVal = val
					}
				}
			} else {
				// Try parsing last token as number: e.g. "Layer 0"
				tokens := strings.Fields(headerContent)
				if len(tokens) >= 2 {
					if val, err := strconv.Atoi(tokens[len(tokens)-1]); err == nil {
						yVal = val
					}
				}
			}

			if currentLayer != nil {
				parsedLayers = append(parsedLayers, *currentLayer)
			}
			currentLayer = &parsedLayer{y: yVal}
			continue
		}

		// Grid row line
		if currentLayer != nil {
			currentLayer.rows = append(currentLayer.rows, trimmed)
		}
	}

	if currentLayer != nil {
		parsedLayers = append(parsedLayers, *currentLayer)
	}

	if len(parsedLayers) == 0 {
		return nil, ErrNoLayersFound
	}

	// Sort layers by Y ascending
	sort.Slice(parsedLayers, func(i, j int) bool {
		return parsedLayers[i].y < parsedLayers[j].y
	})

	// Dimensions verification
	length := len(parsedLayers[0].rows)
	if length == 0 {
		return nil, ErrEmptyGridRow
	}
	width := len(parsedLayers[0].rows[0])
	if width == 0 {
		return nil, ErrEmptyGridRow
	}

	for _, l := range parsedLayers {
		if len(l.rows) != length {
			return nil, fmt.Errorf("%w: layer Y=%d has %d rows, expected length=%d",
				ErrDimensionMismatch, l.y, len(l.rows), length)
		}
		for rIdx, r := range l.rows {
			if len(r) != width {
				return nil, fmt.Errorf("%w: layer Y=%d row %d has %d cols, expected width=%d",
					ErrDimensionMismatch, l.y, rIdx, len(r), width)
			}
		}
	}

	// 3. Build unique integer palette
	stateToInt := make(map[string]int)
	intToState := make(map[int]string)
	nextPalIdx := 1

	var layers []Layer
	for _, pl := range parsedLayers {
		mat := make([][]int, length)
		for z := 0; z < length; z++ {
			mat[z] = make([]int, width)
			rowStr := pl.rows[z]
			for x := 0; x < width; x++ {
				ch := rowStr[x]
				state, ok := charToState[ch]
				if !ok {
					return nil, fmt.Errorf("%w: character %q at Y=%d, Z=%d, X=%d", ErrUnknownChar, ch, pl.y, z, x)
				}
				if ch == '.' || isAir(state) {
					mat[z][x] = 0
				} else {
					idx, exists := stateToInt[state]
					if !exists {
						idx = nextPalIdx
						nextPalIdx++
						stateToInt[state] = idx
						intToState[idx] = state
					}
					mat[z][x] = idx
				}
			}
		}
		layers = append(layers, Layer{
			Y:      pl.y,
			Matrix: mat,
		})
	}

	minY := parsedLayers[0].y
	height := len(parsedLayers)

	bp := &Blueprint{
		ID:      Slugify(name),
		Name:    strings.TrimSpace(name),
		Purpose: NormalizePurpose(purpose),
		Type:    strings.ToLower(NormalizePurpose(purpose)),
		Dimensions: Dimensions{
			Width:  width,
			Height: height,
			Length: length,
			MinY:   minY,
		},
		Palette: intToState,
		Layers:  layers,
	}

	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("validating parsed blueprint: %w", err)
	}

	return bp, nil
}
