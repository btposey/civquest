package crafting

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func buildStandardTestNodes() []*civquestv1.RecipeNode {
	planksAccepted := []string{"minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"}
	logsAccepted := []string{"minecraft:birch_log", "minecraft:oak_log", "minecraft:spruce_log"}
	coalAccepted := []string{"minecraft:charcoal", "minecraft:coal"}
	cobbleAccepted := []string{"minecraft:cobbled_deepslate", "minecraft:cobblestone"}

	return []*civquestv1.RecipeNode{
		{
			RecipeId:     "minecraft:oak_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:oak_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:birch_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:birch_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:birch_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:spruce_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:spruce_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:spruce_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:stick",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planksAccepted, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4},
		},
		{
			RecipeId:     "minecraft:crafting_table",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planksAccepted, Count: 4},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:crafting_table", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_axe",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planksAccepted, Count: 3},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_axe", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planksAccepted, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_sword", Count: 1},
		},
		{
			RecipeId:         "minecraft:stone_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: cobbleAccepted, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stone_sword", Count: 1},
		},
		{
			RecipeId:     "minecraft:torch",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: coalAccepted, Count: 1},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:torch", Count: 4},
		},
		{
			RecipeId:         "minecraft:campfire",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 3},
				{AcceptedItemIds: coalAccepted, Count: 1},
				{AcceptedItemIds: logsAccepted, Count: 3},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:campfire", Count: 1},
		},
	}
}

func TestRecipeGraph_MultiStepBirchAxeChain(t *testing.T) {
	g := NewRecipeGraph(buildStandardTestNodes())

	inv := map[string]int{"minecraft:birch_log": 4}

	// Tick 1: birch_log: 4 -> birch_planks
	step1, ok := g.PlanNextStep("minecraft:wooden_axe", inv, true)
	if !ok || !step1.CanExecuteNow || step1.NeedsTable || step1.MissingRaw {
		t.Fatalf("tick 1: unexpected step %+v ok=%v", step1, ok)
	}
	if step1.RecipeID != "minecraft:birch_planks" {
		t.Fatalf("tick 1: expected minecraft:birch_planks, got %s", step1.RecipeID)
	}
	inv["minecraft:birch_log"]--
	inv["minecraft:birch_planks"] += 4

	// Tick 2: birch_log: 3, birch_planks: 4 -> stick
	step2, ok := g.PlanNextStep("minecraft:wooden_axe", inv, true)
	if !ok || !step2.CanExecuteNow || step2.RecipeID != "minecraft:stick" {
		t.Fatalf("tick 2: expected executable minecraft:stick, got %+v ok=%v", step2, ok)
	}
	inv["minecraft:birch_planks"] -= 2
	inv["minecraft:stick"] += 4

	// Tick 3: birch_log: 3, birch_planks: 2, stick: 4 -> birch_planks
	step3, ok := g.PlanNextStep("minecraft:wooden_axe", inv, true)
	if !ok || !step3.CanExecuteNow || step3.RecipeID != "minecraft:birch_planks" {
		t.Fatalf("tick 3: expected executable minecraft:birch_planks, got %+v ok=%v", step3, ok)
	}
	inv["minecraft:birch_log"]--
	inv["minecraft:birch_planks"] += 4

	// Tick 4: birch_log: 2, birch_planks: 6, stick: 4 -> wooden_axe (3x3)
	step4, ok := g.PlanNextStep("minecraft:wooden_axe", inv, true)
	if !ok || !step4.CanExecuteNow || !step4.Requires3x3 || step4.RecipeID != "minecraft:wooden_axe" {
		t.Fatalf("tick 4: expected executable 3x3 minecraft:wooden_axe, got %+v ok=%v", step4, ok)
	}
}

func TestRecipeGraph_ExactVirtualInventoryBudget2Logs(t *testing.T) {
	g := NewRecipeGraph(buildStandardTestNodes())

	// 1 log is NOT enough for wooden_axe (4 planks -> 2 planks + 4 sticks, needs 3 planks)
	if g.CanEventuallyCraft("minecraft:wooden_axe", map[string]int{"minecraft:birch_log": 1}) {
		t.Fatalf("expected 1 birch_log to be insufficient for wooden_axe")
	}
	stepFail, ok := g.PlanNextStep("minecraft:wooden_axe", map[string]int{"minecraft:birch_log": 1}, false)
	if ok || !stepFail.MissingRaw || stepFail.NeedsTable {
		t.Fatalf("expected missing raw with NeedsTable=false for 1 birch_log, got %+v ok=%v", stepFail, ok)
	}

	// 2 logs IS enough (8 planks -> 2 for 4 sticks + 6 planks remaining >= 3 planks)
	if !g.CanEventuallyCraft("minecraft:wooden_axe", map[string]int{"minecraft:birch_log": 2}) {
		t.Fatalf("expected 2 birch_logs to be sufficient for wooden_axe")
	}

	// Without table: executes 2x2 steps first, only sets NeedsTable once materials are ready for 3x3 wooden_axe
	inv := map[string]int{"minecraft:birch_log": 2}
	for i := 0; i < 3; i++ {
		step, ok := g.PlanNextStep("minecraft:wooden_axe", inv, false)
		if !ok || !step.CanExecuteNow || step.NeedsTable || step.Requires3x3 {
			t.Fatalf("pocket step %d: expected executable 2x2 step with NeedsTable=false, got %+v ok=%v", i, step, ok)
		}
		switch step.RecipeID {
		case "minecraft:birch_planks":
			inv["minecraft:birch_log"]--
			inv["minecraft:birch_planks"] += 4
		case "minecraft:stick":
			inv["minecraft:birch_planks"] -= 2
			inv["minecraft:stick"] += 4
		default:
			t.Fatalf("unexpected pocket step %s", step.RecipeID)
		}
	}

	// Now planks=6, stick=4, hasTable=false -> signals NeedsTable=true
	tableStep, ok := g.PlanNextStep("minecraft:wooden_axe", inv, false)
	if !ok || tableStep.CanExecuteNow || !tableStep.NeedsTable || !tableStep.Requires3x3 || tableStep.RecipeID != "minecraft:wooden_axe" {
		t.Fatalf("expected wooden_axe with NeedsTable=true once 2x2 steps complete, got %+v ok=%v", tableStep, ok)
	}
}

func TestRecipeGraph_RejectsImpossibleTargetsAndCycles(t *testing.T) {
	nodes := buildStandardTestNodes()
	// Add a pathological cycle: alpha <-> beta
	nodes = append(nodes,
		&civquestv1.RecipeNode{
			RecipeId: "mod:alpha",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"mod:beta"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "mod:alpha", Count: 1},
		},
		&civquestv1.RecipeNode{
			RecipeId: "mod:beta",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"mod:alpha"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "mod:beta", Count: 1},
		},
	)
	g := NewRecipeGraph(nodes)

	// Campfire without coal/charcoal must fail cleanly
	invNoCoal := map[string]int{"minecraft:oak_log": 10, "minecraft:stick": 10}
	if g.CanEventuallyCraft("minecraft:campfire", invNoCoal) {
		t.Fatalf("campfire without coal/charcoal must not be craftable")
	}
	step, ok := g.PlanNextStep("minecraft:campfire", invNoCoal, true)
	if ok || !step.MissingRaw || step.NeedsTable {
		t.Fatalf("expected MissingRaw=true and ok=false for campfire without coal, got %+v ok=%v", step, ok)
	}

	// Cycle must terminate cleanly without stack overflow
	if g.CanEventuallyCraft("mod:alpha", map[string]int{"minecraft:oak_log": 5}) {
		t.Fatalf("expected cyclic recipe without base input to return false")
	}
	if _, ok := g.PlanNextStep("mod:alpha", map[string]int{}, true); ok {
		t.Fatalf("expected cyclic recipe plan to fail cleanly")
	}
}

func TestRecipeGraph_LegacyIngredientsFallbackAndSuffix(t *testing.T) {
	// Legacy RecipeNode without IngredientSlots (uses Ingredients + WorkstationBlock)
	legacyNodes := []*civquestv1.RecipeNode{
		{
			RecipeId: "minecraft:spruce_planks",
			Ingredients: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:spruce_log", Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:spruce_planks", Count: 4},
		},
		{
			RecipeId:         "minecraft:chest",
			WorkstationBlock: "minecraft:crafting_table",
			Ingredients: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:spruce_planks", Count: 8},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:chest", Count: 1},
		},
	}

	g := NewRecipeGraph(legacyNodes)
	inv := map[string]int{"minecraft:spruce_log": 2}

	step, ok := g.PlanNextStep("minecraft:chest", inv, false)
	if !ok || !step.CanExecuteNow || step.RecipeID != "minecraft:spruce_planks" {
		t.Fatalf("expected legacy fallback to plan spruce_planks first, got %+v ok=%v", step, ok)
	}

	// PlanNextStepForSuffix("_planks") picks spruce_planks
	plankStep, ok := g.PlanNextStepForSuffix("_planks", inv, false)
	if !ok || !plankStep.CanExecuteNow || plankStep.RecipeID != "minecraft:spruce_planks" {
		t.Fatalf("expected suffix _planks to pick spruce_planks, got %+v ok=%v", plankStep, ok)
	}
}
