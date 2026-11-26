package plan

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
)

// Planning runs inside the agent tick. The budget is generous so a loaded machine does not
// flake; the failure it guards against ran for minutes.
const (
	planDeadline = 500 * time.Millisecond
	hangAfter    = 10 * time.Second
)

// catalogs returns the cycle-bearing synthetic catalogue and, when a server is installed
// locally, the real one.
func catalogs(t *testing.T) map[string]*crafting.RecipeGraph {
	t.Helper()
	out := map[string]*crafting.RecipeGraph{"synthetic": crafting.NewRecipeGraph(craftingtest.Nodes())}
	if jar := craftingtest.FindServerJar(); jar != "" {
		nodes, err := craftingtest.FromServerJar(jar)
		if err != nil {
			t.Fatalf("reading %s: %v", jar, err)
		}
		if len(nodes) < 500 {
			t.Fatalf("only %d crafting recipes read from %s", len(nodes), jar)
		}
		out["server"] = crafting.NewRecipeGraph(nodes)
	} else {
		t.Log("no local server jar: real catalogue not covered")
	}
	return out
}

func within(t *testing.T, what string, f func()) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(hangAfter):
		t.Fatalf("%s did not return within %v", what, hangAfter)
	}
	d := time.Since(start)
	if d > planDeadline {
		t.Errorf("%s took %v, budget %v", what, d, planDeadline)
	}
	return d
}

func TestPlannersBoundedOnCyclicCatalog(t *testing.T) {
	items := []string{
		"oak_log", "birch_log", "oak_planks", "birch_planks", "stick", "crafting_table", "wooden_axe",
		"wooden_sword", "wooden_pickaxe", "stone_pickaxe", "cobblestone", "coal", "charcoal", "torch",
		"white_wool", "black_wool", "white_dye", "black_dye", "bone", "bone_meal", "string", "bamboo",
		"coal_block", "iron_ingot", "iron_nugget", "campfire",
	}
	targets := []string{
		"wooden_axe", "wooden_sword", "campfire", "torch", "crafting_table", "stick", "white_bed",
		"black_bed", "white_wool", "ladder", "fishing_rod", "shears", "oak_planks", "chest", "furnace",
		"iron_ingot", "coal_block",
	}
	for name, g := range catalogs(t) {
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(1))
			var worst time.Duration
			var worstWhat string
			track := func(what string, f func()) {
				if d := within(t, what, f); d > worst {
					worst, worstWhat = d, what
				}
			}
			for i := 0; i < 150 && !t.Failed(); i++ {
				inv := map[string]int{}
				if i > 0 { // the empty inventory first
					for _, it := range items {
						if rng.Intn(3) == 0 {
							inv["minecraft:"+it] = 1 + rng.Intn(9)
						}
					}
				}
				f := Facts{Inventory: inv, Recipes: g}
				track(fmt.Sprintf("tool plan %v", inv), func() { NewToolProgression("p", Lease{}, f) })
				track(fmt.Sprintf("bed plan %v", inv), func() { New("b", Goal{Kind: GoalBed}, Lease{}, f) })
				for _, target := range targets {
					target := "minecraft:" + target
					track(fmt.Sprintf("next step %s %v", target, inv), func() { g.PlanNextStep(target, inv, true) })
				}
				track(fmt.Sprintf("any bed %v", inv), func() { g.PlanNextStepForSuffix("_bed", inv, true) })
				track(fmt.Sprintf("any planks %v", inv), func() { g.PlanNextStepForSuffix("_planks", inv, true) })
			}
			t.Logf("worst %v: %.120s", worst, worstWhat)
		})
	}
}

// A bed must be planned from wool and planks, never by recolouring other beds.
func TestBedPlanOnCyclicCatalogUsesWoolAndPlanks(t *testing.T) {
	for name, g := range catalogs(t) {
		for _, inv := range []map[string]int{
			{},
			{"minecraft:wooden_axe": 1},
			{"minecraft:black_wool": 3, "minecraft:oak_log": 2},
			{"minecraft:string": 12, "minecraft:bone": 3, "minecraft:black_dye": 2},
		} {
			var p Plan
			var ok bool
			within(t, fmt.Sprintf("%s bed plan %v", name, inv), func() {
				p, ok = New("b", Goal{Kind: GoalBed}, Lease{}, Facts{Inventory: inv, Recipes: g})
			})
			if !ok || len(p.Unplannable) != 0 {
				t.Fatalf("%s %v: unplannable %v", name, inv, p.Unplannable)
			}
			for _, s := range p.Steps {
				if strings.HasPrefix(s.RecipeID, "minecraft:dye_") {
					t.Errorf("%s %v: recolouring step %s in %v", name, inv, s.RecipeID, kinds(p.Steps))
				}
				if s.Kind == StepGather && s.Source == SourceWorld {
					t.Errorf("%s %v: gathers %s: %v", name, inv, s.Item, kinds(p.Steps))
				}
			}
			last := p.Steps[len(p.Steps)-1]
			if last.Kind != StepCraft || !strings.HasSuffix(last.Item, "_bed") {
				t.Errorf("%s %v: plan does not end in a bed: %v", name, inv, kinds(p.Steps))
			}
		}
	}
}

// The tool progression must gather only routine resources.
func TestToolPlanOnCyclicCatalogGathersRoutineResources(t *testing.T) {
	for name, g := range catalogs(t) {
		p := NewToolProgression("p", Lease{}, Facts{Inventory: map[string]int{}, Recipes: g})
		if len(p.Unplannable) != 0 {
			t.Fatalf("%s: unplannable %v", name, p.Unplannable)
		}
		for _, b := range p.BOM {
			switch b.Source {
			case SourceTree, SourceStone, SourceOre:
			default:
				t.Errorf("%s: gathers %s (%s): %v", name, b.Item, b.Source, kinds(p.Steps))
			}
		}
		if i := idx(p.Steps, StepCraft, "minecraft:torch"); i < 0 {
			t.Errorf("%s: no torches: %v", name, kinds(p.Steps))
		}
	}
}
