package blueprints

import (
	"strings"
	"testing"
)

func TestPrompt_FormatASCIISlices_Basic(t *testing.T) {
	bp := &Blueprint{
		ID:      "test_cabin",
		Name:    "Test Cabin",
		Purpose: PurposeShelter,
		Dimensions: Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "#minecraft:logs[axis=y]",
			2: "#minecraft:planks",
		},
		Layers: []Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1, 1},
					{1, 0, 1},
					{1, 1, 1},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{2, 2, 2},
					{2, 0, 2},
					{2, 2, 2},
				},
			},
		},
	}

	formatted := FormatASCIISlices(bp)
	if formatted == "" {
		t.Fatalf("expected non-empty formatted ASCII slices")
	}

	// Verify header, palette, and layer output
	if !strings.Contains(formatted, "# Blueprint: test_cabin (Test Cabin)") {
		t.Errorf("missing header in output:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Palette:") {
		t.Errorf("missing Palette section in output:\n%s", formatted)
	}
	if !strings.Contains(formatted, ". = air") {
		t.Errorf("missing air entry in palette:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Layer Y=0:") || !strings.Contains(formatted, "Layer Y=1:") {
		t.Errorf("missing layer headers in output:\n%s", formatted)
	}

	// Token efficiency: text length must be < 1000 characters (<250 tokens)
	if len(formatted) > 1000 {
		t.Errorf("formatted text too long: %d characters", len(formatted))
	}
}

func TestPrompt_ParseASCIISlices_Roundtrip(t *testing.T) {
	bpOriginal := &Blueprint{
		ID:      "frontier_outpost",
		Name:    "Frontier Outpost",
		Purpose: PurposeShelter,
		Dimensions: Dimensions{
			Width:  4,
			Height: 3,
			Length: 4,
			MinY:   -1, // basement at Y=-1
		},
		Palette: map[int]string{
			1: "minecraft:cobblestone",
			2: "#minecraft:logs[axis=y]",
			3: "#minecraft:planks",
		},
		Layers: []Layer{
			{
				Y: -1, // Basement
				Matrix: [][]int{
					{1, 1, 1, 1},
					{1, 0, 0, 1},
					{1, 0, 0, 1},
					{1, 1, 1, 1},
				},
			},
			{
				Y: 0, // Ground
				Matrix: [][]int{
					{2, 3, 3, 2},
					{3, 0, 0, 3},
					{3, 0, 0, 3},
					{2, 3, 3, 2},
				},
			},
			{
				Y: 1, // Roof
				Matrix: [][]int{
					{3, 3, 3, 3},
					{3, 3, 3, 3},
					{3, 3, 3, 3},
					{3, 3, 3, 3},
				},
			},
		},
	}

	if err := bpOriginal.Validate(); err != nil {
		t.Fatalf("bpOriginal validation failed: %v", err)
	}

	// Format to ASCII
	formatted := FormatASCIISlices(bpOriginal)
	t.Logf("Formatted output:\n%s", formatted)

	// Parse back
	parsedBP, err := ParseASCIISlices("Frontier Outpost", PurposeShelter, formatted, nil)
	if err != nil {
		t.Fatalf("ParseASCIISlices failed: %v\nInput was:\n%s", err, formatted)
	}

	// Assert dimensions and properties
	if parsedBP.Dimensions.Width != 4 || parsedBP.Dimensions.Length != 4 || parsedBP.Dimensions.Height != 3 {
		t.Errorf("dimension mismatch: got %+v, want Width=4, Length=4, Height=3", parsedBP.Dimensions)
	}
	if parsedBP.Dimensions.MinY != -1 {
		t.Errorf("MinY mismatch: got %d, want -1", parsedBP.Dimensions.MinY)
	}

	// Verify subterranean excavation was computed
	if parsedBP.Excavation <= 0 {
		t.Errorf("expected positive excavation for subterranean level, got %d", parsedBP.Excavation)
	}

	// Verify BOM computation
	if len(parsedBP.BOM) == 0 {
		t.Errorf("expected populated BOM, got empty")
	}
}

func TestPrompt_ParseASCIISlices_WithExternalPalette(t *testing.T) {
	raw := `
Layer Y=0:
LLLL
L..L
L..L
LLLL

Layer Y=1:
PPPP
P..P
P..P
PPPP
`
	palette := map[string]string{
		"L": "#minecraft:logs[axis=y]",
		"P": "#minecraft:planks",
	}

	bp, err := ParseASCIISlices("Tavern Annex", "TAVERN", raw, palette)
	if err != nil {
		t.Fatalf("ParseASCIISlices failed: %v", err)
	}

	if bp.Dimensions.Width != 4 || bp.Dimensions.Length != 4 || bp.Dimensions.Height != 2 {
		t.Errorf("unexpected dimensions: %+v", bp.Dimensions)
	}
	if bp.Purpose != PurposeTavern {
		t.Errorf("unexpected purpose: %s, want %s", bp.Purpose, PurposeTavern)
	}
}

func TestPrompt_ParseASCIISlices_Errors(t *testing.T) {
	// 1. Empty name
	_, err := ParseASCIISlices("", "SHELTER", "Layer Y=0:\nLL\nLL", map[string]string{"L": "minecraft:stone"})
	if err == nil {
		t.Errorf("expected error on empty name")
	}

	// 2. No layers found
	_, err = ParseASCIISlices("Test", "SHELTER", "No layers here", nil)
	if err == nil {
		t.Errorf("expected error when no layers found")
	}

	// 3. Unmapped character
	rawUnknown := `
Layer Y=0:
LL
L?
`
	_, err = ParseASCIISlices("Test", "SHELTER", rawUnknown, map[string]string{"L": "minecraft:stone"})
	if err == nil {
		t.Errorf("expected error for unmapped character '?'")
	}

	// 4. Inconsistent row dimensions
	rawMismatch := `
Layer Y=0:
LLL
LL
`
	_, err = ParseASCIISlices("Test", "SHELTER", rawMismatch, map[string]string{"L": "minecraft:stone"})
	if err == nil {
		t.Errorf("expected error for mismatched row lengths")
	}
}
