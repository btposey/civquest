package blueprints

import (
	"testing"
)

func TestDirection_GetOrientationCategory(t *testing.T) {
	tests := []struct {
		id       string
		expected OrientationCategory
	}{
		{"minecraft:oak_log", OrientationAxis},
		{"minecraft:stripped_spruce_wood", OrientationAxis},
		{"minecraft:basalt", OrientationAxis},
		{"minecraft:hay_block", OrientationAxis},
		{"minecraft:oak_stairs", OrientationHorizontal},
		{"minecraft:chest", OrientationHorizontal},
		{"minecraft:furnace", OrientationHorizontal},
		{"minecraft:oak_door", OrientationHorizontal},
		{"minecraft:oak_trapdoor", OrientationHorizontal},
		{"minecraft:oak_fence_gate", OrientationHorizontal},
		{"minecraft:red_bed", OrientationHorizontal},
		{"piston", Orientation6Way},
		{"dispenser", Orientation6Way},
		{"barrel", Orientation6Way},
		{"observer", Orientation6Way},
		{"minecraft:oak_slab", OrientationSlab},
		{"stone_slab", OrientationSlab},
		{"minecraft:oak_sign", OrientationRotation},
		{"white_banner", OrientationRotation},
		{"player_head", OrientationRotation},
		{"minecraft:dirt", OrientationNone},
		{"minecraft:glass", OrientationNone},
		{"minecraft:cobblestone", OrientationNone},
	}

	for _, tt := range tests {
		got := GetOrientationCategory(tt.id)
		if got != tt.expected {
			t.Errorf("GetOrientationCategory(%q) = %q, want %q", tt.id, got, tt.expected)
		}
	}
}

func TestDirection_RotateClockwise90(t *testing.T) {
	// 1. Horizontal facing rotation
	stairsNorth := ParseBlockState("minecraft:oak_stairs[facing=north,half=bottom]")
	stairsEast := RotateClockwise90(stairsNorth)
	if stairsEast.Properties["facing"] != "east" || stairsEast.Properties["half"] != "bottom" {
		t.Errorf("expected facing=east, half=bottom, got %s", stairsEast.String())
	}
	stairsSouth := RotateClockwise90(stairsEast)
	if stairsSouth.Properties["facing"] != "south" {
		t.Errorf("expected facing=south, got %s", stairsSouth.String())
	}
	stairsWest := RotateClockwise90(stairsSouth)
	if stairsWest.Properties["facing"] != "west" {
		t.Errorf("expected facing=west, got %s", stairsWest.String())
	}
	stairsCycle := RotateClockwise90(stairsWest)
	if stairsCycle.Properties["facing"] != "north" {
		t.Errorf("expected facing=north, got %s", stairsCycle.String())
	}

	// 2. Axis rotation (x <-> z, y remains unchanged)
	logY := ParseBlockState("minecraft:oak_log[axis=y]")
	logYRot := RotateClockwise90(logY)
	if logYRot.Properties["axis"] != "y" {
		t.Errorf("expected vertical axis=y to remain unchanged, got %s", logYRot.String())
	}

	logX := ParseBlockState("minecraft:oak_log[axis=x]")
	logZ := RotateClockwise90(logX)
	if logZ.Properties["axis"] != "z" {
		t.Errorf("expected axis=z after rotating axis=x, got %s", logZ.String())
	}
	logXAgain := RotateClockwise90(logZ)
	if logXAgain.Properties["axis"] != "x" {
		t.Errorf("expected axis=x after rotating axis=z, got %s", logXAgain.String())
	}

	// 3. Discrete rotation (signs/banners)
	sign0 := ParseBlockState("minecraft:oak_sign[rotation=0]")
	sign4 := RotateClockwise90(sign0)
	if sign4.Properties["rotation"] != "4" {
		t.Errorf("expected rotation=4, got %s", sign4.String())
	}
	sign8 := RotateClockwise90(sign4)
	if sign8.Properties["rotation"] != "8" {
		t.Errorf("expected rotation=8, got %s", sign8.String())
	}
	sign12 := RotateClockwise90(sign8)
	if sign12.Properties["rotation"] != "12" {
		t.Errorf("expected rotation=12, got %s", sign12.String())
	}
	sign16Wrapped := RotateClockwise90(sign12)
	if sign16Wrapped.Properties["rotation"] != "0" {
		t.Errorf("expected rotation=0 (wrapped), got %s", sign16Wrapped.String())
	}

	// 4. 6-Way directional: Up and Down stay fixed
	pistonUp := ParseBlockState("minecraft:piston[facing=up]")
	pistonUpRot := RotateClockwise90(pistonUp)
	if pistonUpRot.Properties["facing"] != "up" {
		t.Errorf("expected facing=up to stay fixed, got %s", pistonUpRot.String())
	}
}

func TestDirection_DirectionGlyph(t *testing.T) {
	stNorth := ParseBlockState("minecraft:oak_stairs[facing=north]")
	if g := DirectionGlyph(stNorth); g != "↑ (N)" {
		t.Errorf("expected '↑ (N)', got %q", g)
	}

	stLog := ParseBlockState("minecraft:oak_log[axis=y]")
	if g := DirectionGlyph(stLog); g != "↕ (Y)" {
		t.Errorf("expected '↕ (Y)', got %q", g)
	}

	stSlab := ParseBlockState("minecraft:oak_slab[type=top]")
	if g := DirectionGlyph(stSlab); g != "▔ (Top)" {
		t.Errorf("expected '▔ (Top)', got %q", g)
	}
}
