// Package craftingtest supplies recipe catalogues shaped like the live server's, for tests
// of anything that plans crafting. Hand-picked fixtures without cycles let an exponential
// planner pass every test and freeze every agent in play (cq-yn1.1).
package craftingtest

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Colours are the vanilla dye colours.
var Colours = []string{
	"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black",
}

// Woods are the wood species whose logs make planks.
var Woods = []string{
	"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "crimson", "warped",
}

type slot struct {
	n   int32
	ids []string
}

func mc(name string) string { return "minecraft:" + name }

func one(n int32, names ...string) slot {
	s := slot{n: n}
	for _, name := range names {
		s.ids = append(s.ids, mc(name))
	}
	return s
}

type builder struct{ nodes []*civquestv1.RecipeNode }

func (b *builder) add(id string, big bool, count int32, result string, slots ...slot) {
	n := &civquestv1.RecipeNode{
		RecipeId: mc(id), RecipeType: "minecraft:crafting_shaped", Requires_3X3: big,
		Result: &civquestv1.ItemStackProto{ItemId: mc(result), Count: count},
	}
	if big {
		n.WorkstationBlock = "minecraft:crafting_table"
	}
	for _, s := range slots {
		n.IngredientSlots = append(n.IngredientSlots, &civquestv1.RecipeIngredientSlot{AcceptedItemIds: s.ids, Count: s.n})
	}
	b.nodes = append(b.nodes, n)
}

// Nodes returns a catalogue with the structure that matters in vanilla Minecraft: tag-wide
// ingredient slots, block/item compression cycles and the recolouring recipes that turn any
// wool or bed into any other.
func Nodes() []*civquestv1.RecipeNode {
	b := &builder{}
	var planks, logs []string
	for _, w := range Woods {
		variants := []string{w + "_log", w + "_wood", "stripped_" + w + "_log", "stripped_" + w + "_wood"}
		logs = append(logs, variants...)
		planks = append(planks, w+"_planks")
		b.add(w+"_planks", false, 4, w+"_planks", one(1, variants...))
		b.add(w+"_wood", false, 3, w+"_wood", one(4, w+"_log"))
		b.add("stripped_"+w+"_wood", false, 3, "stripped_"+w+"_wood", one(4, "stripped_"+w+"_log"))
	}
	stone := []string{"cobblestone", "cobbled_deepslate", "blackstone"}
	fuel := []string{"coal", "charcoal"}

	b.add("stick", false, 4, "stick", one(2, planks...))
	b.add("stick_from_bamboo_item", false, 1, "stick", one(2, "bamboo"))
	b.add("crafting_table", false, 1, "crafting_table", one(4, planks...))
	for tier, mat := range map[string][]string{"wooden": planks, "stone": stone} {
		b.add(tier+"_axe", true, 1, tier+"_axe", one(3, mat...), one(2, "stick"))
		b.add(tier+"_sword", true, 1, tier+"_sword", one(2, mat...), one(1, "stick"))
		b.add(tier+"_pickaxe", true, 1, tier+"_pickaxe", one(3, mat...), one(2, "stick"))
	}
	b.add("wooden_shovel", true, 1, "wooden_shovel", one(1, planks...), one(2, "stick"))
	b.add("torch", false, 4, "torch", one(1, fuel...), one(1, "stick"))
	b.add("campfire", true, 1, "campfire", one(3, "stick"), one(1, fuel...), one(3, logs...))
	b.add("chest", true, 1, "chest", one(8, planks...))
	b.add("furnace", true, 1, "furnace", one(8, stone...))
	for _, w := range Woods {
		b.add(w+"_door", true, 3, w+"_door", one(6, w+"_planks"))
		b.add(w+"_trapdoor", true, 2, w+"_trapdoor", one(6, w+"_planks"))
		b.add(w+"_fence", true, 3, w+"_fence", one(4, w+"_planks"), one(2, "stick"))
	}
	b.add("ladder", true, 3, "ladder", one(7, "stick"))
	b.add("fishing_rod", true, 1, "fishing_rod", one(3, "stick"), one(2, "string"))
	b.add("shears", false, 1, "shears", one(2, "iron_ingot"))

	// Compression and conversion cycles.
	for item, block := range map[string]string{"coal": "coal_block", "iron_ingot": "iron_block", "wheat": "hay_block", "bone_meal": "bone_block"} {
		b.add(block, true, 1, block, one(9, item))
		b.add(item+"_from_"+block, false, 9, item, one(1, block))
	}
	b.add("iron_nugget", false, 9, "iron_nugget", one(1, "iron_ingot"))
	b.add("iron_ingot_from_nuggets", true, 1, "iron_ingot", one(9, "iron_nugget"))
	b.add("bone_meal", false, 3, "bone_meal", one(1, "bone"))

	b.add("white_wool_from_string", false, 1, "white_wool", one(4, "string"))
	b.add("white_dye", false, 1, "white_dye", one(1, "bone_meal"))
	for _, c := range Colours {
		if c != "white" {
			b.add(c+"_dye", false, 1, c+"_dye", one(1, c+"_dye_source"))
		}
		var otherWool, otherBed []string
		for _, o := range Colours {
			if o != c {
				otherWool = append(otherWool, o+"_wool")
				otherBed = append(otherBed, o+"_bed")
			}
		}
		b.add("dye_"+c+"_wool", false, 1, c+"_wool", one(1, c+"_dye"), one(1, otherWool...))
		b.add(c+"_bed", true, 1, c+"_bed", one(3, c+"_wool"), one(3, planks...))
		b.add("dye_"+c+"_bed", false, 1, c+"_bed", one(1, c+"_dye"), one(1, otherBed...))
	}
	return b.nodes
}
