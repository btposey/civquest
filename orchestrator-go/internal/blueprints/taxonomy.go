package blueprints

import (
	"sort"
	"strings"
)

// Standard Minecraft & NeoForge taxonomy tag constants.
const (
	TagPlanks         = "#minecraft:planks"
	TagLogs           = "#minecraft:logs"
	TagWood           = "#minecraft:wood"
	TagStairs         = "#minecraft:stairs"
	TagSlabs          = "#minecraft:slabs"
	TagDoors          = "#minecraft:doors"
	TagTrapdoors      = "#minecraft:trapdoors"
	TagFenceGates     = "#minecraft:fence_gates"
	TagFences         = "#minecraft:fences"
	TagStones         = "#c:stones"
	TagBaseStones     = "#minecraft:base_stone_overworld"
	TagStoneBricks    = "#minecraft:stone_bricks"
	TagCobblestones   = "#c:cobblestones"
	TagWool           = "#minecraft:wool"
	TagBeds           = "#minecraft:beds"
	TagSand           = "#minecraft:sand"
	TagGlassBlocks    = "#c:glass_blocks"
	TagGlassPanes     = "#c:glass_panes"
	TagLightSources   = "#minecraft:light_sources"
)

// knownTaxonomyMap defines default standard catalog mappings of generic taxonomy tags to concrete items.
var knownTaxonomyMap = map[string][]string{
	TagPlanks: {
		"minecraft:oak_planks", "minecraft:spruce_planks", "minecraft:birch_planks",
		"minecraft:jungle_planks", "minecraft:acacia_planks", "minecraft:dark_oak_planks",
		"minecraft:mangrove_planks", "minecraft:cherry_planks", "minecraft:bamboo_planks",
		"minecraft:crimson_planks", "minecraft:warped_planks",
	},
	TagLogs: {
		"minecraft:oak_log", "minecraft:spruce_log", "minecraft:birch_log",
		"minecraft:jungle_log", "minecraft:acacia_log", "minecraft:dark_oak_log",
		"minecraft:mangrove_log", "minecraft:cherry_log", "minecraft:bamboo_block",
		"minecraft:crimson_stem", "minecraft:warped_stem",
	},
	TagStairs: {
		"minecraft:oak_stairs", "minecraft:spruce_stairs", "minecraft:birch_stairs",
		"minecraft:jungle_stairs", "minecraft:acacia_stairs", "minecraft:dark_oak_stairs",
		"minecraft:mangrove_stairs", "minecraft:cherry_stairs", "minecraft:bamboo_stairs",
		"minecraft:cobblestone_stairs", "minecraft:stone_stairs", "minecraft:stone_brick_stairs",
		"minecraft:deepslate_brick_stairs", "minecraft:deepslate_tile_stairs",
		"minecraft:sandstone_stairs", "minecraft:red_sandstone_stairs", "minecraft:brick_stairs",
	},
	TagSlabs: {
		"minecraft:oak_slab", "minecraft:spruce_slab", "minecraft:birch_slab",
		"minecraft:jungle_slab", "minecraft:acacia_slab", "minecraft:dark_oak_slab",
		"minecraft:cobblestone_slab", "minecraft:stone_slab", "minecraft:stone_brick_slab",
		"minecraft:deepslate_brick_slab", "minecraft:sandstone_slab",
	},
	TagDoors: {
		"minecraft:oak_door", "minecraft:spruce_door", "minecraft:birch_door",
		"minecraft:jungle_door", "minecraft:acacia_door", "minecraft:dark_oak_door",
		"minecraft:iron_door", "minecraft:cherry_door", "minecraft:bamboo_door",
	},
	TagTrapdoors: {
		"minecraft:oak_trapdoor", "minecraft:spruce_trapdoor", "minecraft:birch_trapdoor",
		"minecraft:jungle_trapdoor", "minecraft:iron_trapdoor",
	},
	TagFenceGates: {
		"minecraft:oak_fence_gate", "minecraft:spruce_fence_gate", "minecraft:birch_fence_gate",
		"minecraft:jungle_fence_gate", "minecraft:acacia_fence_gate", "minecraft:dark_oak_fence_gate",
	},
	TagFences: {
		"minecraft:oak_fence", "minecraft:spruce_fence", "minecraft:birch_fence",
		"minecraft:jungle_fence", "minecraft:acacia_fence", "minecraft:dark_oak_fence",
		"minecraft:nether_brick_fence",
	},
	TagStones: {
		"minecraft:cobblestone", "minecraft:stone", "minecraft:deepslate", "minecraft:cobbled_deepslate",
		"minecraft:granite", "minecraft:diorite", "minecraft:andesite", "minecraft:tuff",
		"minecraft:sandstone", "minecraft:red_sandstone", "minecraft:blackstone",
	},
	TagBaseStones: {
		"minecraft:stone", "minecraft:deepslate", "minecraft:granite", "minecraft:diorite",
		"minecraft:andesite", "minecraft:tuff",
	},
	TagCobblestones: {
		"minecraft:cobblestone", "minecraft:cobbled_deepslate", "minecraft:blackstone",
	},
	TagStoneBricks: {
		"minecraft:stone_bricks", "minecraft:deepslate_bricks", "minecraft:deepslate_tiles",
		"minecraft:polished_blackstone_bricks", "minecraft:end_stone_bricks",
	},
	TagWool: {
		"minecraft:white_wool", "minecraft:orange_wool", "minecraft:magenta_wool",
		"minecraft:light_blue_wool", "minecraft:yellow_wool", "minecraft:lime_wool",
		"minecraft:pink_wool", "minecraft:gray_wool", "minecraft:light_gray_wool",
		"minecraft:cyan_wool", "minecraft:purple_wool", "minecraft:blue_wool",
		"minecraft:brown_wool", "minecraft:green_wool", "minecraft:red_wool", "minecraft:black_wool",
	},
	TagBeds: {
		"minecraft:red_bed", "minecraft:white_bed", "minecraft:blue_bed", "minecraft:yellow_bed",
		"minecraft:green_bed", "minecraft:black_bed",
	},
	TagSand: {
		"minecraft:sand", "minecraft:red_sand",
	},
	TagGlassBlocks: {
		"minecraft:glass", "minecraft:white_stained_glass", "minecraft:tinted_glass",
	},
	TagGlassPanes: {
		"minecraft:glass_pane", "minecraft:white_stained_glass_pane",
	},
	TagLightSources: {
		"minecraft:torch", "minecraft:lantern", "minecraft:soul_torch", "minecraft:soul_lantern",
		"minecraft:glowstone", "minecraft:shroomlight", "minecraft:sea_lantern",
	},
}

// KnownTaxonomyMap returns a deep copy of the standard catalog taxonomy mappings.
func KnownTaxonomyMap() map[string][]string {
	result := make(map[string][]string, len(knownTaxonomyMap))
	for k, v := range knownTaxonomyMap {
		copied := make([]string, len(v))
		copy(copied, v)
		result[k] = copied
	}
	return result
}

// KnownTaxonomyTags returns a deterministic sorted slice of all known taxonomy tags.
func KnownTaxonomyTags() []string {
	tags := make([]string, 0, len(knownTaxonomyMap))
	for k := range knownTaxonomyMap {
		tags = append(tags, k)
	}
	sort.Strings(tags)
	return tags
}

// IsTaxonomyTag returns true if the given identifier represents a generic taxonomy tag (prefixed with #).
func IsTaxonomyTag(id string) bool {
	clean := strings.TrimSpace(id)
	return strings.HasPrefix(clean, "#")
}

// NormalizeTaxonomyTag ensures a standard format for a taxonomy tag (e.g. "#planks" -> "#minecraft:planks").
func NormalizeTaxonomyTag(raw string) string {
	clean := strings.TrimSpace(raw)
	if !strings.HasPrefix(clean, "#") {
		clean = "#" + clean
	}
	tagBody := clean[1:]
	if !strings.Contains(tagBody, ":") {
		tagBody = "minecraft:" + tagBody
	}
	return "#" + strings.ToLower(tagBody)
}

// MatchesTaxonomy checks whether a concrete block/item ID satisfies a generic taxonomy tag.
func MatchesTaxonomy(taxonomyTag, concreteID string) bool {
	normTag := NormalizeTaxonomyTag(taxonomyTag)
	concreteClean := strings.ToLower(strings.TrimSpace(concreteID))
	if strings.Contains(concreteClean, "[") {
		concreteClean = strings.SplitN(concreteClean, "[", 2)[0]
	}
	if !strings.Contains(concreteClean, ":") {
		concreteClean = "minecraft:" + concreteClean
	}

	// 1. Direct registry lookup
	if members, ok := knownTaxonomyMap[normTag]; ok {
		for _, m := range members {
			if m == concreteClean {
				return true
			}
		}
	}

	// 2. Conventional suffix heuristics for extensible mod compatibility
	tagName := normTag[strings.IndexByte(normTag, ':')+1:]
	switch tagName {
	case "planks":
		return strings.HasSuffix(concreteClean, "_planks")
	case "logs":
		return strings.HasSuffix(concreteClean, "_log") || strings.HasSuffix(concreteClean, "_stem")
	case "wood":
		return strings.HasSuffix(concreteClean, "_wood") || strings.HasSuffix(concreteClean, "_hyphae")
	case "stairs":
		return strings.HasSuffix(concreteClean, "_stairs")
	case "slabs":
		return strings.HasSuffix(concreteClean, "_slab")
	case "doors":
		return strings.HasSuffix(concreteClean, "_door")
	case "trapdoors":
		return strings.HasSuffix(concreteClean, "_trapdoor")
	case "fence_gates":
		return strings.HasSuffix(concreteClean, "_fence_gate")
	case "fences":
		return strings.HasSuffix(concreteClean, "_fence")
	case "stone_bricks":
		return strings.HasSuffix(concreteClean, "_stone_bricks") || concreteClean == "minecraft:stone_bricks"
	case "stones", "cobblestones":
		return strings.Contains(concreteClean, "cobblestone") || strings.Contains(concreteClean, "stone") ||
			strings.Contains(concreteClean, "deepslate") || strings.Contains(concreteClean, "tuff")
	case "wool":
		return strings.HasSuffix(concreteClean, "_wool")
	case "beds":
		return strings.HasSuffix(concreteClean, "_bed")
	case "sand":
		return strings.HasSuffix(concreteClean, "_sand") || concreteClean == "minecraft:sand"
	}

	return false
}

// FindMatchingInventoryItems returns all item IDs and quantities from an inventory that satisfy the taxonomy tag.
func FindMatchingInventoryItems(taxonomyTag string, inventory map[string]int) map[string]int {
	matched := make(map[string]int)
	for itemID, qty := range inventory {
		if qty > 0 && MatchesTaxonomy(taxonomyTag, itemID) {
			matched[itemID] = qty
		}
	}
	return matched
}

// ResolveInSituBlock selects the best concrete blockstate from available in-situ inventory to fulfill a generic or specific target.
// It preserves any directional block state properties present on the target (e.g. facing, axis, half).
func ResolveInSituBlock(targetSpec string, availableInventory map[string]int, fallback string) string {
	clean := strings.TrimSpace(targetSpec)
	if clean == "" {
		return fallback
	}

	// Separate base specification from properties
	baseSpec := clean
	props := ""
	if bracketIdx := strings.IndexByte(clean, '['); bracketIdx != -1 && strings.HasSuffix(clean, "]") {
		baseSpec = clean[:bracketIdx]
		props = clean[bracketIdx:]
	}

	// 1. If concrete and available in inventory, keep it
	if !IsTaxonomyTag(baseSpec) {
		normBase := baseSpec
		if !strings.Contains(normBase, ":") {
			normBase = "minecraft:" + normBase
		}
		if qty := availableInventory[normBase]; qty > 0 {
			return normBase + props
		}
	}

	// 2. Identify taxonomy tag
	tag := baseSpec
	if !IsTaxonomyTag(tag) {
		// Attempt to deduce tag from concrete baseSpec (e.g. "minecraft:oak_stairs" -> "#minecraft:stairs")
		tag = DeduceTaxonomyTag(baseSpec)
	}

	if IsTaxonomyTag(tag) {
		matching := FindMatchingInventoryItems(tag, availableInventory)
		if len(matching) > 0 {
			// Choose the in-situ material with the highest available stock
			type itemStock struct {
				id  string
				qty int
			}
			stocks := make([]itemStock, 0, len(matching))
			for id, qty := range matching {
				stocks = append(stocks, itemStock{id: id, qty: qty})
			}
			sort.Slice(stocks, func(i, j int) bool {
				return stocks[i].qty > stocks[j].qty
			})
			return stocks[0].id + props
		}
	}

	// 3. Fallback
	if fallback != "" {
		if props != "" && !strings.Contains(fallback, "[") {
			return fallback + props
		}
		return fallback
	}

	return clean
}

// DeduceTaxonomyTag infers a generic taxonomy tag from a concrete block or item ID.
func DeduceTaxonomyTag(concreteID string) string {
	id := strings.ToLower(strings.TrimSpace(concreteID))
	if strings.Contains(id, "[") {
		id = strings.SplitN(id, "[", 2)[0]
	}

	switch {
	case strings.HasSuffix(id, "_planks"):
		return TagPlanks
	case strings.HasSuffix(id, "_stairs"):
		return TagStairs
	case strings.HasSuffix(id, "_slab"):
		return TagSlabs
	case strings.HasSuffix(id, "_door"):
		return TagDoors
	case strings.HasSuffix(id, "_trapdoor"):
		return TagTrapdoors
	case strings.HasSuffix(id, "_fence_gate"):
		return TagFenceGates
	case strings.HasSuffix(id, "_fence"):
		return TagFences
	case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_stem"):
		return TagLogs
	case strings.HasSuffix(id, "_wool"):
		return TagWool
	case strings.HasSuffix(id, "_bed"):
		return TagBeds
	case strings.Contains(id, "cobblestone"):
		return TagCobblestones
	case strings.Contains(id, "stone"):
		return TagStones
	default:
		return ""
	}
}

// EvaluateTaxonomyBOM compares a blueprint's Bill of Materials against current settlement inventory,
// resolving generic taxonomy tags and permitting in-situ material substitutions.
func EvaluateTaxonomyBOM(bom map[string]int, inventory map[string]int) (satisfied bool, surplus map[string]int, missing map[string]int) {
	surplus = make(map[string]int)
	missing = make(map[string]int)
	satisfied = true

	// Clone inventory so deductions don't mutate caller state
	stock := make(map[string]int, len(inventory))
	for k, v := range inventory {
		stock[k] = v
	}

	// 1. Process concrete requirements first
	for item, needed := range bom {
		if IsTaxonomyTag(item) {
			continue
		}
		available := stock[item]
		if available >= needed {
			surplus[item] = available - needed
			stock[item] -= needed
		} else {
			satisfied = false
			missing[item] = needed - available
			stock[item] = 0
		}
	}

	// 2. Process generic taxonomy tags using remaining in-situ stock
	for item, needed := range bom {
		if !IsTaxonomyTag(item) {
			continue
		}
		// Count all available in-situ items matching this taxonomy tag
		totalAvailable := 0
		for stockItem, qty := range stock {
			if qty > 0 && MatchesTaxonomy(item, stockItem) {
				totalAvailable += qty
			}
		}

		if totalAvailable >= needed {
			surplus[item] = totalAvailable - needed
			// Deduct needed amount from in-situ stock
			remainingToDeduct := needed
			for stockItem, qty := range stock {
				if qty <= 0 || !MatchesTaxonomy(item, stockItem) {
					continue
				}
				deduct := qty
				if deduct > remainingToDeduct {
					deduct = remainingToDeduct
				}
				stock[stockItem] -= deduct
				remainingToDeduct -= deduct
				if remainingToDeduct <= 0 {
					break
				}
			}
		} else {
			satisfied = false
			missing[item] = needed - totalAvailable
		}
	}

	return satisfied, surplus, missing
}
