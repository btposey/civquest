package blueprints

import (
	"strconv"
	"strings"
)

// OrientationCategory categorizes how a Minecraft block defines its spatial orientation.
type OrientationCategory string

const (
	OrientationNone       OrientationCategory = "none"       // Cubical / isotropic (dirt, cobblestone, glass)
	OrientationAxis       OrientationCategory = "axis"       // 3-axis alignment (logs, pillars, basalt, hay bales)
	OrientationHorizontal OrientationCategory = "horizontal" // 4-way horizontal facing (stairs, chests, furnaces, beds, doors)
	Orientation6Way       OrientationCategory = "6way"       // 6-way omnidirectional (pistons, dispensers, observers, barrels)
	OrientationSlab       OrientationCategory = "slab"       // Vertical slab half (bottom, top, double)
	OrientationRotation   OrientationCategory = "rotation"   // 16-step discrete yaw (standing signs, banners, skulls)
)

// Minecraft Cardinal Directions and Axes
const (
	FacingNorth = "north"
	FacingEast  = "east"
	FacingSouth = "south"
	FacingWest  = "west"
	FacingUp    = "up"
	FacingDown  = "down"

	AxisY = "y" // Vertical
	AxisX = "x" // East-West
	AxisZ = "z" // North-South

	HalfBottom = "bottom"
	HalfTop    = "top"
	HalfLower  = "lower" // Doors
	HalfUpper  = "upper" // Doors

	SlabBottom = "bottom"
	SlabTop    = "top"
	SlabDouble = "double"
)

// GetOrientationCategory determines the orientation semantic for a given Minecraft block ID.
func GetOrientationCategory(blockID string) OrientationCategory {
	id := strings.ToLower(strings.TrimSpace(blockID))
	if strings.Contains(id, "[") {
		id = strings.SplitN(id, "[", 2)[0]
	}
	if strings.HasPrefix(id, "minecraft:") {
		id = strings.TrimPrefix(id, "minecraft:")
	}

	// 1. Slabs
	if strings.HasSuffix(id, "_slab") {
		return OrientationSlab
	}

	// 2. 3-Axis blocks (Logs, Wood, Basalt, Hay, Pillars, Bone)
	if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") ||
		strings.HasSuffix(id, "_stem") || strings.HasSuffix(id, "_hyphae") ||
		strings.HasSuffix(id, "_pillar") || id == "basalt" || id == "polished_basalt" ||
		id == "hay_block" || id == "bone_block" || id == "deepslate" || id == "chain" {
		return OrientationAxis
	}

	// 3. 6-Way Directional blocks
	if id == "piston" || id == "sticky_piston" || id == "dispenser" || id == "dropper" ||
		id == "observer" || id == "barrel" || id == "end_rod" || id == "lightning_rod" ||
		id == "shulker_box" || strings.HasSuffix(id, "_shulker_box") || id == "hopper" {
		return Orientation6Way
	}

	// 4. Discrete 16-step Rotation (standing signs, banners, heads)
	if (strings.HasSuffix(id, "_sign") && !strings.Contains(id, "wall")) ||
		(strings.HasSuffix(id, "_banner") && !strings.Contains(id, "wall")) ||
		id == "player_head" || id == "creeper_head" || id == "zombie_head" || id == "skeleton_skull" {
		return OrientationRotation
	}

	// 5. Horizontal Facing (Stairs, Chests, Furnaces, Doors, Trapdoors, Beds, Gates, etc.)
	if strings.HasSuffix(id, "_stairs") || strings.HasSuffix(id, "_door") ||
		strings.HasSuffix(id, "_trapdoor") || strings.HasSuffix(id, "_fence_gate") ||
		strings.HasSuffix(id, "_bed") || strings.HasSuffix(id, "_glazed_terracotta") ||
		id == "chest" || id == "trapped_chest" || id == "ender_chest" ||
		id == "furnace" || id == "blast_furnace" || id == "smoker" ||
		id == "stonecutter" || id == "loom" || id == "carved_pumpkin" || id == "jack_o_lantern" ||
		id == "repeater" || id == "comparator" || id == "ladder" ||
		strings.Contains(id, "wall_sign") || strings.Contains(id, "wall_torch") ||
		id == "beehive" || id == "bee_nest" || id == "campfire" || id == "soul_campfire" {
		return OrientationHorizontal
	}

	return OrientationNone
}

// CanonicalDefaultProperties returns the canonical default Minecraft properties for directional blocks.
func CanonicalDefaultProperties(blockID string) map[string]string {
	category := GetOrientationCategory(blockID)
	id := strings.ToLower(blockID)
	if strings.HasPrefix(id, "minecraft:") {
		id = strings.TrimPrefix(id, "minecraft:")
	}

	switch category {
	case OrientationAxis:
		return map[string]string{"axis": AxisY}
	case OrientationHorizontal:
		props := map[string]string{"facing": FacingNorth}
		if strings.HasSuffix(id, "_stairs") {
			props["half"] = HalfBottom
			props["shape"] = "straight"
		} else if strings.HasSuffix(id, "_door") {
			props["half"] = HalfLower
			props["hinge"] = "left"
			props["open"] = "false"
		} else if strings.HasSuffix(id, "_trapdoor") {
			props["half"] = HalfBottom
			props["open"] = "false"
		} else if strings.HasSuffix(id, "_fence_gate") {
			props["in_wall"] = "false"
			props["open"] = "false"
		}
		return props
	case Orientation6Way:
		return map[string]string{"facing": FacingNorth}
	case OrientationSlab:
		return map[string]string{"type": SlabBottom}
	case OrientationRotation:
		return map[string]string{"rotation": "0"}
	default:
		return nil
	}
}

// RotateClockwise90 rotates a BlockState 90 degrees clockwise around the vertical Y axis.
func RotateClockwise90(state BlockState) BlockState {
	category := GetOrientationCategory(state.BaseItem())
	if category == OrientationNone {
		return state
	}

	clonedProps := make(map[string]string, len(state.Properties))
	for k, v := range state.Properties {
		clonedProps[k] = v
	}

	switch category {
	case OrientationAxis:
		currentAxis := clonedProps["axis"]
		switch currentAxis {
		case AxisX:
			clonedProps["axis"] = AxisZ
		case AxisZ:
			clonedProps["axis"] = AxisX
		default:
			// AxisY remains vertical under Y-axis rotation
		}

	case OrientationHorizontal, Orientation6Way:
		currentFacing := clonedProps["facing"]
		switch currentFacing {
		case FacingNorth:
			clonedProps["facing"] = FacingEast
		case FacingEast:
			clonedProps["facing"] = FacingSouth
		case FacingSouth:
			clonedProps["facing"] = FacingWest
		case FacingWest:
			clonedProps["facing"] = FacingNorth
		// Up and Down for 6-way stay fixed
		default:
			if currentFacing == "" {
				clonedProps["facing"] = FacingEast
			}
		}

	case OrientationRotation:
		if rotStr, ok := clonedProps["rotation"]; ok {
			if rot, err := strconv.Atoi(rotStr); err == nil {
				// 16 directions, 90 degrees = 4 steps
				newRot := (rot + 4) % 16
				clonedProps["rotation"] = strconv.Itoa(newRot)
			}
		} else {
			clonedProps["rotation"] = "4"
		}
	}

	return BlockState{
		Identifier: state.Identifier,
		Properties: clonedProps,
	}
}

// DirectionGlyph returns a compact Unicode glyph representing the block's current orientation.
func DirectionGlyph(state BlockState) string {
	category := GetOrientationCategory(state.BaseItem())
	switch category {
	case OrientationAxis:
		axis := state.Properties["axis"]
		switch axis {
		case AxisX:
			return "↔ (E-W)"
		case AxisZ:
			return "⤢ (N-S)"
		default:
			return "↕ (Y)"
		}
	case OrientationHorizontal, Orientation6Way:
		facing := state.Properties["facing"]
		switch facing {
		case FacingNorth:
			return "↑ (N)"
		case FacingEast:
			return "→ (E)"
		case FacingSouth:
			return "↓ (S)"
		case FacingWest:
			return "← (W)"
		case FacingUp:
			return "▲ (Up)"
		case FacingDown:
			return "▼ (Down)"
		default:
			return "•"
		}
	case OrientationSlab:
		t := state.Properties["type"]
		switch t {
		case SlabTop:
			return "▔ (Top)"
		case SlabDouble:
			return "█ (Double)"
		default:
			return "  (Bottom)"
		}
	case OrientationRotation:
		rotStr := state.Properties["rotation"]
		return "↺ " + rotStr
	default:
		return ""
	}
}
