package plan

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
)

// craftAll runs the plan's craft and place steps against inv until none is left.
func craftAll(t *testing.T, f Facts) map[string]int {
	t.Helper()
	for i := 0; i < 40; i++ {
		step, status := Next(Plan{Goal: Goal{Kind: GoalToolProgression}}, f)
		if status != NextReady || step.Kind == StepGather {
			return f.Inventory
		}
		if step.Kind == StepPlace {
			f.Inventory[step.Item]--
			f.Table.Near = true
			continue
		}
		ps, ok := f.Recipes.PlanNextStep(step.Item, f.Inventory, f.Table.Near)
		if !ok || !ps.CanExecuteNow {
			t.Fatalf("step %+v cannot be executed from %v", step, f.Inventory)
		}
		for _, r := range f.Recipes.RecipesFor(ps.ResultItemID) {
			if r.RecipeID != ps.RecipeID {
				continue
			}
			for _, slot := range r.Slots {
				need := slot.Count
				for _, id := range slot.AcceptedItemIDs {
					n := min(need, f.Inventory[id])
					f.Inventory[id] -= n
					need -= n
				}
			}
			f.Inventory[r.ResultItemID] += r.ResultCount
		}
	}
	t.Fatalf("plan did not settle: %v", f.Inventory)
	return nil
}

func TestLadderReserveFromSpareWoodOnly(t *testing.T) {
	kit := func(logs int) Facts {
		f := Facts{Recipes: crafting.NewRecipeGraph(craftingtest.Nodes()), Inventory: map[string]int{
			"minecraft:stone_axe": 1, "minecraft:stone_sword": 1, "minecraft:stone_pickaxe": 1,
			"minecraft:oak_log": logs,
		}}
		return f
	}
	got := craftAll(t, kit(14))
	if got[itemLadder] != LadderReserve || 4*got["minecraft:oak_log"]+got["minecraft:oak_planks"] < woodKept {
		t.Fatalf("rich in wood: %v", got)
	}
	if got := craftAll(t, kit(6)); got[itemLadder] != 0 || got["minecraft:oak_log"] != 6 {
		t.Fatalf("the kept wood was spent: %v", got)
	}
	// With the reserve nearly full and no table in reach, none is set down for a top-up.
	f := kit(14)
	f.Inventory[itemLadder] = ladderLow
	if got := craftAll(t, f); got[itemLadder] != ladderLow {
		t.Fatalf("topped up without a table: %v", got)
	}
}
