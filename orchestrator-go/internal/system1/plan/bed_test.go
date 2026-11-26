package plan

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func bedRecipeGraph() *crafting.RecipeGraph {
	pl := []string{"minecraft:birch_planks", planks}
	nodes := []*civquestv1.RecipeNode{
		node("minecraft:oak_planks", false, 4, planks, sl(1, logs)),
		node("minecraft:crafting_table", false, 1, itemTable, sl(4, pl...)),
		// vanilla also crafts wool from string; the bed plan must still gather wool.
		node("minecraft:white_wool", false, 1, "minecraft:white_wool", sl(4, "minecraft:string")),
	}
	for _, c := range []string{"white", "red", "blue"} {
		nodes = append(nodes, node("minecraft:"+c+"_bed", true, 1, BedItem(c), sl(3, WoolItem(c)), sl(3, pl...)))
	}
	return crafting.NewRecipeGraph(nodes)
}

func bedFacts(inv map[string]int, near bool) Facts {
	return Facts{Tick: 5, Inventory: inv, Table: TableFacts{Near: near}, Recipes: bedRecipeGraph()}
}

func TestBedPlanGathersWoolNotString(t *testing.T) {
	p, ok := New("b", Goal{Kind: GoalBed}, Lease{Owner: "a"}, bedFacts(map[string]int{planks: 6}, true))
	if !ok {
		t.Fatal("GoalBed unknown")
	}
	step, st := Next(p, bedFacts(map[string]int{planks: 6}, true))
	if st != NextReady || step.Kind != StepGather || step.Item != WoolItem("white") || step.Qty != 3 || step.Source != SourceMob {
		t.Fatalf("first step = %+v (%v), want gather 3 white_wool from a mob", step, st)
	}
}

func TestBedPlanUsesHeldColour(t *testing.T) {
	f := bedFacts(map[string]int{planks: 3, WoolItem("red"): 2}, true)
	step, st := Next(Plan{Goal: Goal{Kind: GoalBed}}, f)
	if st != NextReady || step.Item != WoolItem("red") || step.Qty != 1 {
		t.Fatalf("want gather 1 red_wool (finish the held colour), got %+v", step)
	}
}

func TestBedPlanCraftsWhenMaterialsHeld(t *testing.T) {
	f := bedFacts(map[string]int{planks: 3, WoolItem("blue"): 3}, true)
	step, st := Next(Plan{Goal: Goal{Kind: GoalBed}}, f)
	if st != NextReady || step.Kind != StepCraft || step.RecipeID != "minecraft:blue_bed" || !step.Requires3x3 {
		t.Fatalf("want craft blue_bed, got %+v (%v)", step, st)
	}
}

func TestBedPlanLogsThenPlanksThenTable(t *testing.T) {
	f := bedFacts(map[string]int{logs: 1, WoolItem("white"): 3}, false)
	p, _ := New("b", Goal{Kind: GoalBed}, Lease{}, f)
	got := kinds(p.Steps)
	want := []string{"craft:" + planks, "craft:" + itemTable, "place", "craft:" + BedItem("white")}
	if len(got) < 3 || got[0] != want[0] {
		t.Fatalf("steps = %v, want planks crafted first", got)
	}
}

func TestBedPlanCompleteWithBedHeld(t *testing.T) {
	_, st := Next(Plan{Goal: Goal{Kind: GoalBed}}, bedFacts(map[string]int{BedItem("red"): 1}, true))
	if st != NextComplete {
		t.Fatalf("status = %v, want complete", st)
	}
}
