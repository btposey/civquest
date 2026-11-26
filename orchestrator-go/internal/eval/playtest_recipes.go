package eval

import (
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// VanillaRecipeNodes returns the small vanilla recipe subset the bootstrap tool progression
// uses (planks, sticks, table, wooden and stone tools, torch). In production the recipe graph
// arrives from the mod; scenarios install this one on AgentState via SetRecipeGraph.
func VanillaRecipeNodes() []*civquestv1.RecipeNode {
	planks := []string{"minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"}
	cobble := []string{"minecraft:cobbled_deepslate", "minecraft:cobblestone"}
	stick := []string{"minecraft:stick"}
	res := func(id string, n int32) *civquestv1.ItemStackProto {
		return &civquestv1.ItemStackProto{ItemId: id, Count: n}
	}
	slot := func(n int32, ids ...string) *civquestv1.RecipeIngredientSlot {
		return &civquestv1.RecipeIngredientSlot{AcceptedItemIds: ids, Count: n}
	}
	tool := func(name string, mat []string, matN, stickN int32) *civquestv1.RecipeNode {
		return &civquestv1.RecipeNode{
			RecipeId: "minecraft:" + name, RecipeType: "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table", Requires_3X3: true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(matN, mat...), slot(stickN, stick...)},
			Result:          res("minecraft:"+name, 1),
		}
	}
	nodes := []*civquestv1.RecipeNode{}
	for _, w := range []string{"oak", "birch", "spruce"} {
		nodes = append(nodes, &civquestv1.RecipeNode{
			RecipeId: "minecraft:" + w + "_planks", RecipeType: "minecraft:crafting_shapeless",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(1, "minecraft:"+w+"_log")},
			Result:          res("minecraft:"+w+"_planks", 4),
		})
	}
	nodes = append(nodes,
		&civquestv1.RecipeNode{RecipeId: "minecraft:stick", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(2, planks...)}, Result: res("minecraft:stick", 4)},
		&civquestv1.RecipeNode{RecipeId: "minecraft:crafting_table", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(4, planks...)}, Result: res("minecraft:crafting_table", 1)},
		tool("wooden_axe", planks, 3, 2), tool("wooden_sword", planks, 2, 1), tool("wooden_pickaxe", planks, 3, 2),
		tool("wooden_shovel", planks, 1, 2),
		tool("stone_sword", cobble, 2, 1), tool("stone_pickaxe", cobble, 3, 2), tool("stone_axe", cobble, 3, 2),
		&civquestv1.RecipeNode{RecipeId: "minecraft:torch", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(1, "minecraft:charcoal", "minecraft:coal"), slot(1, stick...)},
			Result:          res("minecraft:torch", 4)},
	)
	return nodes
}

// VanillaRecipeGraph builds the planner graph over VanillaRecipeNodes.
func VanillaRecipeGraph() *crafting.RecipeGraph { return crafting.NewRecipeGraph(VanillaRecipeNodes()) }
