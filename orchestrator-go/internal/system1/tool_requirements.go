package system1

import (
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// ToolClass is the vanilla tool family that mines a block fastest.
type ToolClass string

const (
	ToolClassNone    ToolClass = ""
	ToolClassPickaxe ToolClass = "pickaxe"
	ToolClassAxe     ToolClass = "axe"
	ToolClassShovel  ToolClass = "shovel"
)

// ToolTier orders vanilla mining levels. Gold shares the wooden level for
// drops; netherite shares diamond's.
type ToolTier int

const (
	TierHand ToolTier = iota
	TierWood
	TierStone
	TierIron
	TierDiamond
)

// ToolRequirement says which tool a block wants. Required means the block drops
// nothing (vanilla "requires correct tool for drops") unless a tool of Class and
// at least MinTier is used; otherwise the class is merely faster.
type ToolRequirement struct {
	Class    ToolClass
	MinTier  ToolTier
	Required bool
}

// RequirementForBlock is the pure block-id -> tool requirement model (cq-gpa.10).
// Unknown blocks have no requirement.
func RequirementForBlock(blockID string) ToolRequirement {
	id := bareID(blockID)
	id = strings.TrimPrefix(id, "deepslate_")
	switch {
	case id == "obsidian" || id == "crying_obsidian" || id == "ancient_debris":
		return ToolRequirement{ToolClassPickaxe, TierDiamond, true}
	case strings.HasSuffix(id, "diamond_ore") || strings.HasSuffix(id, "emerald_ore") ||
		strings.HasSuffix(id, "gold_ore") || strings.HasSuffix(id, "redstone_ore") || id == "raw_gold_block":
		return ToolRequirement{ToolClassPickaxe, TierIron, true}
	case strings.HasSuffix(id, "iron_ore") || strings.HasSuffix(id, "copper_ore") || strings.HasSuffix(id, "lapis_ore"):
		return ToolRequirement{ToolClassPickaxe, TierStone, true}
	case strings.HasSuffix(id, "coal_ore"),
		id == "stone", id == "cobblestone", id == "andesite", id == "diorite", id == "granite",
		id == "deepslate", id == "cobbled_deepslate", id == "mossy_cobblestone", id == "sandstone",
		id == "stone_bricks", id == "bricks", id == "furnace", id == "blackstone", id == "netherrack",
		id == "basalt", id == "tuff", id == "calcite":
		return ToolRequirement{ToolClassPickaxe, TierWood, true}
	case isLogBlockID(blockID) || strings.HasSuffix(id, "_planks") || id == "crafting_table" || id == "chest" ||
		strings.HasSuffix(id, "_fence") || strings.HasSuffix(id, "_door"):
		return ToolRequirement{ToolClassAxe, TierHand, false}
	case id == "dirt" || id == "grass_block" || id == "coarse_dirt" || id == "podzol" || id == "mycelium" ||
		id == "sand" || id == "red_sand" || id == "gravel" || id == "clay" || id == "snow" || id == "snow_block" ||
		id == "soul_sand" || id == "farmland" || id == "dirt_path":
		return ToolRequirement{ToolClassShovel, TierHand, false}
	}
	return ToolRequirement{}
}

// ToolOf classifies an item id as a tool class and tier. ok is false for
// non-tools.
func ToolOf(itemID string) (ToolClass, ToolTier, bool) {
	id := bareID(itemID)
	var class ToolClass
	switch {
	case strings.HasSuffix(id, "_pickaxe"):
		class = ToolClassPickaxe
	case strings.HasSuffix(id, "_axe"):
		class = ToolClassAxe
	case strings.HasSuffix(id, "_shovel"):
		class = ToolClassShovel
	default:
		return ToolClassNone, TierHand, false
	}
	switch {
	case strings.HasPrefix(id, "wooden_"), strings.HasPrefix(id, "golden_"):
		return class, TierWood, true
	case strings.HasPrefix(id, "stone_"):
		return class, TierStone, true
	case strings.HasPrefix(id, "iron_"):
		return class, TierIron, true
	case strings.HasPrefix(id, "diamond_"), strings.HasPrefix(id, "netherite_"):
		return class, TierDiamond, true
	}
	return ToolClassNone, TierHand, false
}

// BestToolTier is the highest tier of the given class in the inventory
// (TierHand and false when there is none).
func BestToolTier(inventory []*civquestv1.ItemStackProto, class ToolClass) (ToolTier, bool) {
	best, found := TierHand, false
	for _, it := range inventory {
		if it == nil || it.Count <= 0 {
			continue
		}
		c, t, ok := ToolOf(it.ItemId)
		if !ok || c != class {
			continue
		}
		if !found || t > best {
			best, found = t, true
		}
	}
	return best, found
}

// HasSuitableTool is the harvest precondition: true when the block needs no
// tool, or the inventory holds a tool of the right class and sufficient tier.
func HasSuitableTool(inventory []*civquestv1.ItemStackProto, req ToolRequirement) bool {
	if !req.Required {
		return true
	}
	tier, ok := BestToolTier(inventory, req.Class)
	return ok && tier >= req.MinTier
}

// CraftTargetForRequirement names the cheapest tool item that satisfies req
// ("" when the requirement needs none or cannot be crafted at this stage).
func CraftTargetForRequirement(req ToolRequirement) string {
	if !req.Required || req.Class == ToolClassNone {
		return ""
	}
	switch {
	case req.MinTier <= TierWood:
		return "minecraft:wooden_" + string(req.Class)
	case req.MinTier == TierStone:
		return "minecraft:stone_" + string(req.Class)
	case req.MinTier == TierIron:
		return "minecraft:iron_" + string(req.Class)
	}
	return "minecraft:diamond_" + string(req.Class)
}
