package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func inv(items ...string) []*civquestv1.ItemStackProto {
	var out []*civquestv1.ItemStackProto
	for i, id := range items {
		out = append(out, &civquestv1.ItemStackProto{Slot: int32(i), ItemId: id, Count: 1})
	}
	return out
}

func TestRequirementForBlock(t *testing.T) {
	cases := []struct {
		block string
		class ToolClass
		tier  ToolTier
		req   bool
	}{
		{"minecraft:stone", ToolClassPickaxe, TierWood, true},
		{"minecraft:cobblestone", ToolClassPickaxe, TierWood, true},
		{"minecraft:coal_ore", ToolClassPickaxe, TierWood, true},
		{"minecraft:deepslate_coal_ore", ToolClassPickaxe, TierWood, true},
		{"minecraft:iron_ore", ToolClassPickaxe, TierStone, true},
		{"minecraft:deepslate_copper_ore", ToolClassPickaxe, TierStone, true},
		{"minecraft:diamond_ore", ToolClassPickaxe, TierIron, true},
		{"minecraft:obsidian", ToolClassPickaxe, TierDiamond, true},
		{"minecraft:oak_log", ToolClassAxe, TierHand, false},
		{"minecraft:dirt", ToolClassShovel, TierHand, false},
		{"minecraft:poppy", ToolClassNone, TierHand, false},
	}
	for _, c := range cases {
		got := RequirementForBlock(c.block)
		if got.Class != c.class || got.MinTier != c.tier || got.Required != c.req {
			t.Errorf("%s: got %+v want class=%q tier=%d required=%v", c.block, got, c.class, c.tier, c.req)
		}
	}
}

func TestHasSuitableTool(t *testing.T) {
	stone := RequirementForBlock("minecraft:stone")
	iron := RequirementForBlock("minecraft:iron_ore")
	log := RequirementForBlock("minecraft:oak_log")
	if HasSuitableTool(inv("minecraft:wooden_axe", "minecraft:wooden_sword"), stone) {
		t.Fatal("an axe must not count as a tool for stone")
	}
	if !HasSuitableTool(inv("minecraft:wooden_pickaxe"), stone) {
		t.Fatal("wooden pickaxe mines stone")
	}
	if HasSuitableTool(inv("minecraft:wooden_pickaxe"), iron) {
		t.Fatal("wooden pickaxe must not count for iron ore")
	}
	if !HasSuitableTool(inv("minecraft:stone_pickaxe"), iron) {
		t.Fatal("stone pickaxe mines iron ore")
	}
	if !HasSuitableTool(nil, log) {
		t.Fatal("logs need no tool for drops")
	}
	if CraftTargetForRequirement(stone) != "minecraft:wooden_pickaxe" || CraftTargetForRequirement(iron) != "minecraft:stone_pickaxe" {
		t.Fatal("craft targets wrong")
	}
}

func toolTestGraph() *crafting.RecipeGraph {
	planks := []string{"minecraft:oak_planks"}
	return crafting.NewRecipeGraph([]*civquestv1.RecipeNode{
		{RecipeId: "minecraft:stick", RecipeType: "minecraft:crafting_shaped", IngredientSlots: []*civquestv1.RecipeIngredientSlot{{AcceptedItemIds: planks, Count: 2}}, Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4}},
		{RecipeId: "minecraft:wooden_pickaxe", RecipeType: "minecraft:crafting_shaped", WorkstationBlock: "minecraft:crafting_table", Requires_3X3: true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{{AcceptedItemIds: planks, Count: 3}, {AcceptedItemIds: []string{"minecraft:stick"}, Count: 2}},
			Result:          &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_pickaxe", Count: 1}},
		{RecipeId: "minecraft:stone_pickaxe", RecipeType: "minecraft:crafting_shaped", WorkstationBlock: "minecraft:crafting_table", Requires_3X3: true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{{AcceptedItemIds: []string{"minecraft:cobblestone"}, Count: 3}, {AcceptedItemIds: []string{"minecraft:stick"}, Count: 2}},
			Result:          &civquestv1.ItemStackProto{ItemId: "minecraft:stone_pickaxe", Count: 1}},
	})
}

func TestBootstrapToolTargets_PickaxeProgression(t *testing.T) {
	has := func(targets []string, id string) bool {
		for _, x := range targets {
			if x == id {
				return true
			}
		}
		return false
	}
	bare := bootstrapToolTargets(inspectBootstrapInventory(nil))
	if !has(bare, "minecraft:wooden_pickaxe") || !has(bare, "minecraft:stone_pickaxe") {
		t.Fatalf("empty inventory targets %v must include both pickaxes", bare)
	}
	wood := bootstrapToolTargets(inspectBootstrapInventory(inv("minecraft:wooden_pickaxe")))
	if has(wood, "minecraft:wooden_pickaxe") || !has(wood, "minecraft:stone_pickaxe") {
		t.Fatalf("wooden pickaxe held: want only stone_pickaxe upgrade, got %v", wood)
	}
	stone := bootstrapToolTargets(inspectBootstrapInventory(inv("minecraft:stone_pickaxe")))
	if has(stone, "minecraft:wooden_pickaxe") || has(stone, "minecraft:stone_pickaxe") {
		t.Fatalf("stone pickaxe held: no pickaxe targets expected, got %v", stone)
	}
}

func bootstrapToolFrame(items []*civquestv1.ItemStackProto) *civquestv1.TelemetryFrame {
	f := newDirectiveTestFrame("aria", 400, 4000, 0.5, 64, 0.5)
	f.Status.Inventory = items
	f.InterestingBlocks = []*civquestv1.VisibleBlock{{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"}}
	return f
}

func TestBootstrap_CraftsWoodenThenStonePickaxe(t *testing.T) {
	cfg := xdg.DefaultAgentIdentityConfig("aria")
	base := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
		{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
		{Slot: 2, ItemId: "minecraft:torch", Count: 4},
		{Slot: 3, ItemId: "minecraft:oak_planks", Count: 6},
		{Slot: 4, ItemId: "minecraft:stick", Count: 4},
	}
	state := NewAgentState()
	state.SetRecipeGraph(toolTestGraph())
	intent := EvaluateBootstrapGoals("Logger", bootstrapToolFrame(base), cfg, state, rigWalk(state))
	if intent == nil || intent.GetCraftItem() == nil || intent.GetCraftItem().RecipeId != "minecraft:wooden_pickaxe" {
		t.Fatalf("expected wooden_pickaxe craft, got %+v", intent)
	}

	// With a wooden pickaxe and cobblestone the progression upgrades to stone.
	withCobble := append(append([]*civquestv1.ItemStackProto{}, base...),
		&civquestv1.ItemStackProto{Slot: 5, ItemId: "minecraft:wooden_pickaxe", Count: 1},
		&civquestv1.ItemStackProto{Slot: 6, ItemId: "minecraft:cobblestone", Count: 5})
	state2 := NewAgentState()
	state2.SetRecipeGraph(toolTestGraph())
	intent = EvaluateBootstrapGoals("Logger", bootstrapToolFrame(withCobble), cfg, state2, rigWalk(state2))
	if intent == nil || intent.GetCraftItem() == nil || intent.GetCraftItem().RecipeId != "minecraft:stone_pickaxe" {
		t.Fatalf("expected stone_pickaxe craft, got %+v", intent)
	}
}

func mineDirectiveState(tick int64) *AgentState {
	st := NewAgentState()
	st.SetStrategicDirective(DirectiveActionMineStone, "stone", "MINE_STONE: Gather stone", tick-10, tick+6000)
	return st
}

func TestDirectiveMineStone_RequiresPickaxe(t *testing.T) {
	cfg := xdg.DefaultAgentIdentityConfig("aria")
	stoneNear := []*civquestv1.VisibleBlock{{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:stone"}}

	// Axe only: must not issue HARVEST_BREAK on stone (playtest 2 regression).
	f := newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	f.Status.Inventory = inv("minecraft:wooden_axe", "minecraft:wooden_sword")
	f.InterestingBlocks = stoneNear
	if in := EvaluateStrategicDirective(f, mineDirectiveState(200), cfg, nil); in != nil && in.GetInteractBlock() != nil {
		t.Fatalf("stone break issued without a pickaxe: %+v", in)
	}

	// Planks + sticks + table in reach: plan to craft the pickaxe instead.
	st := mineDirectiveState(200)
	st.SetRecipeGraph(toolTestGraph())
	f = newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5)
	f.Status.Inventory = []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
		{Slot: 1, ItemId: "minecraft:oak_planks", Count: 3},
		{Slot: 2, ItemId: "minecraft:stick", Count: 2},
	}
	f.InterestingBlocks = append([]*civquestv1.VisibleBlock{{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:crafting_table"}}, stoneNear...)
	in := EvaluateStrategicDirective(f, st, cfg, nil)
	if in == nil || in.GetCraftItem() == nil || in.GetCraftItem().RecipeId != "minecraft:wooden_pickaxe" {
		t.Fatalf("expected wooden_pickaxe craft instead of mining, got %+v", in)
	}

	// Wooden pickaxe: stone is mined; iron ore (needs stone tier) is skipped in favour of stone.
	f = withWoodenPickaxe(newDirectiveTestFrame("aria", 200, 4000, 0.5, 64, 0.5))
	f.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}, BlockId: "minecraft:iron_ore"},
		{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:stone"},
	}
	in = EvaluateStrategicDirective(f, mineDirectiveState(200), cfg, nil)
	if in == nil || in.GetInteractBlock() == nil || in.GetInteractBlock().Pos.X != 3 {
		t.Fatalf("expected stone (3,64,0) mined, iron skipped, got %+v", in)
	}
}
