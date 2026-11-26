package plan

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	planks = "minecraft:oak_planks"
	logs   = "minecraft:oak_log"
	stick  = "minecraft:stick"
	cobble = "minecraft:cobblestone"
	coal   = "minecraft:coal"
)

func node(id string, three bool, count int32, result string, slots ...*civquestv1.RecipeIngredientSlot) *civquestv1.RecipeNode {
	n := &civquestv1.RecipeNode{
		RecipeId: id, RecipeType: "minecraft:crafting_shaped", Requires_3X3: three,
		IngredientSlots: slots, Result: &civquestv1.ItemStackProto{ItemId: result, Count: count},
	}
	if three {
		n.WorkstationBlock = "minecraft:crafting_table"
	}
	return n
}

func sl(n int32, ids ...string) *civquestv1.RecipeIngredientSlot {
	return &civquestv1.RecipeIngredientSlot{AcceptedItemIds: ids, Count: n}
}

func fullGraph(skip ...string) *crafting.RecipeGraph {
	pl := []string{"minecraft:birch_planks", planks}
	co := []string{"minecraft:cobbled_deepslate", cobble}
	nodes := []*civquestv1.RecipeNode{
		node("minecraft:oak_planks", false, 4, planks, sl(1, logs)),
		node("minecraft:birch_planks", false, 4, "minecraft:birch_planks", sl(1, "minecraft:birch_log")),
		node("minecraft:stick", false, 4, stick, sl(2, pl...)),
		node("minecraft:crafting_table", false, 1, itemTable, sl(4, pl...)),
		node("minecraft:wooden_axe", true, 1, "minecraft:wooden_axe", sl(3, pl...), sl(2, stick)),
		node("minecraft:wooden_sword", true, 1, "minecraft:wooden_sword", sl(2, pl...), sl(1, stick)),
		node("minecraft:wooden_pickaxe", true, 1, "minecraft:wooden_pickaxe", sl(3, pl...), sl(2, stick)),
		node("minecraft:stone_pickaxe", true, 1, "minecraft:stone_pickaxe", sl(3, co...), sl(2, stick)),
		node("minecraft:stone_axe", true, 1, "minecraft:stone_axe", sl(3, co...), sl(2, stick)),
		node("minecraft:stone_sword", true, 1, "minecraft:stone_sword", sl(2, co...), sl(1, stick)),
		node("minecraft:torch", false, 4, "minecraft:torch", sl(1, "minecraft:charcoal", coal), sl(1, stick)),
	}
	var out []*civquestv1.RecipeNode
	for _, n := range nodes {
		skipIt := false
		for _, s := range skip {
			if n.RecipeId == s {
				skipIt = true
			}
		}
		if !skipIt {
			out = append(out, n)
		}
	}
	return crafting.NewRecipeGraph(out)
}

func facts(inv map[string]int, table TableFacts, g *crafting.RecipeGraph) Facts {
	return Facts{Tick: 10, Inventory: inv, Table: table, Recipes: g}
}

func kinds(steps []Step) []string {
	var out []string
	for _, s := range steps {
		switch s.Kind {
		case StepGather:
			out = append(out, "gather:"+s.Item)
		case StepCraft:
			out = append(out, "craft:"+s.Item)
		default:
			out = append(out, string(s.Kind))
		}
	}
	return out
}

func idx(steps []Step, kind StepKind, item string) int {
	for i, s := range steps {
		if s.Kind == kind && s.Item == item {
			return i
		}
	}
	return -1
}

func TestEmptyInventoryFullProgression(t *testing.T) {
	p := NewToolProgression("p1", Lease{Owner: "plan"}, facts(nil, TableFacts{}, fullGraph()))
	if len(p.Unplannable) != 0 {
		t.Fatalf("unplannable: %v", p.Unplannable)
	}
	order := []string{itemTable, "minecraft:wooden_axe", "minecraft:wooden_sword",
		"minecraft:wooden_pickaxe", "minecraft:stone_pickaxe", "minecraft:stone_axe", "minecraft:stone_sword", "minecraft:torch"}
	prev := -1
	for _, it := range order {
		i := idx(p.Steps, StepCraft, it)
		if i < 0 || i <= prev {
			t.Fatalf("%s out of order (%d after %d): %v", it, i, prev, kinds(p.Steps))
		}
		prev = i
	}
	pl := idx(p.Steps, StepPlace, itemTable)
	if pl < idx(p.Steps, StepCraft, itemTable) || pl > idx(p.Steps, StepCraft, "minecraft:wooden_axe") {
		t.Fatalf("table not placed between craft and axe: %v", kinds(p.Steps))
	}
	pick := idx(p.Steps, StepCraft, "minecraft:wooden_pickaxe")
	for _, it := range []string{cobble, coal} {
		if g := idx(p.Steps, StepGather, it); g < pick {
			t.Fatalf("%s gathered before pickaxe: %v", it, kinds(p.Steps))
		}
	}
	seen := map[string]int{}
	for _, s := range p.Steps {
		if s.Kind == StepGather {
			seen[s.Item]++
		}
	}
	for item, n := range seen {
		if n != 1 {
			t.Fatalf("%s gathered %d times", item, n)
		}
	}
	bom := map[string]int{}
	for _, b := range p.BOM {
		bom[b.Item] = b.Qty
	}
	if bom[cobble] != 8 || bom[coal] != 1 || bom[logs] < 3 || len(bom) != 3 {
		t.Fatalf("bom: %+v", p.BOM)
	}
	if p.ID != "p1" || p.CreatedTick != 10 || p.Goal.Kind != GoalToolProgression || p.Lease.Owner != "plan" {
		t.Fatalf("header: %+v", p)
	}
	q := NewToolProgression("p1", Lease{Owner: "plan"}, facts(nil, TableFacts{}, fullGraph()))
	if len(q.Steps) != len(p.Steps) {
		t.Fatal("nondeterministic length")
	}
	for i := range p.Steps {
		if p.Steps[i].Kind != q.Steps[i].Kind || p.Steps[i].Item != q.Steps[i].Item || p.Steps[i].Qty != q.Steps[i].Qty {
			t.Fatalf("nondeterministic step %d", i)
		}
	}
}

func TestNextFromEmptyIsGatherLogs(t *testing.T) {
	f := facts(nil, TableFacts{}, fullGraph())
	p := NewToolProgression("p", Lease{}, f)
	s, st := Next(p, f)
	if st != NextReady || s.Kind != StepGather || s.Item != logs || s.Source != SourceTree || s.Qty < 3 {
		t.Fatalf("got %+v %v", s, st)
	}
}

func TestMidProgressInventories(t *testing.T) {
	g := fullGraph()
	near := TableFacts{Near: true}
	stoneTools := map[string]int{"minecraft:stone_axe": 1, "minecraft:stone_sword": 1, "minecraft:stone_pickaxe": 1, stick: 2}
	withCoal := map[string]int{"minecraft:stone_axe": 1, "minecraft:stone_sword": 1, "minecraft:stone_pickaxe": 1, stick: 2, coal: 1}
	cases := []struct {
		name     string
		inv      map[string]int
		table    TableFacts
		wantKind StepKind
		wantItem string
	}{
		{"logs held -> craft planks", map[string]int{logs: 6}, TableFacts{}, StepCraft, planks},
		{"planks held -> sticks", map[string]int{planks: 8}, TableFacts{}, StepCraft, stick},
		{"planks+sticks, no table -> craft table", map[string]int{planks: 8, stick: 4}, TableFacts{}, StepCraft, itemTable},
		{"table in inventory -> place", map[string]int{itemTable: 1, planks: 8, stick: 4}, TableFacts{}, StepPlace, itemTable},
		{"table near, materials -> wooden axe", map[string]int{planks: 8, stick: 4}, near, StepCraft, "minecraft:wooden_axe"},
		{"has wood tools -> gather cobble", map[string]int{"minecraft:wooden_axe": 1, "minecraft:wooden_sword": 1, "minecraft:wooden_pickaxe": 1, stick: 8}, near, StepGather, cobble},
		{"stone tools -> gather coal", stoneTools, near, StepGather, coal},
		{"coal+sticks -> torch", withCoal, near, StepCraft, "minecraft:torch"},
	}
	for _, c := range cases {
		f := facts(c.inv, c.table, g)
		s, st := Next(NewToolProgression("x", Lease{}, f), f)
		if st != NextReady || s.Kind != c.wantKind || s.Item != c.wantItem {
			t.Errorf("%s: got %+v status %v", c.name, s, st)
		}
	}
}

func TestCompleteWhenEverythingOwned(t *testing.T) {
	inv := map[string]int{"minecraft:iron_axe": 1, "minecraft:stone_sword": 1, "minecraft:stone_pickaxe": 1, "minecraft:torch": 4}
	f := facts(inv, TableFacts{}, fullGraph())
	if _, st := Next(NewToolProgression("x", Lease{}, f), f); st != NextComplete {
		t.Fatalf("status %v", st)
	}
}

func TestTableFarNear(t *testing.T) {
	g := fullGraph()
	inv := map[string]int{planks: 8, stick: 4}
	cases := []struct {
		name  string
		table TableFacts
		want  StepKind
		item  string
	}{
		{"near", TableFacts{Near: true}, StepCraft, "minecraft:wooden_axe"},
		{"far known close enough -> goto", TableFacts{Known: true, Pos: Pos{5, 64, 5}, Dist: 20}, StepGoto, ""},
		{"far known too far -> craft new", TableFacts{Known: true, Pos: Pos{500, 64, 5}, Dist: 400}, StepCraft, itemTable},
		{"unknown -> craft new", TableFacts{}, StepCraft, itemTable},
	}
	for _, c := range cases {
		f := facts(inv, c.table, g)
		p := NewToolProgression("x", Lease{}, f)
		s, _ := Next(p, f)
		if s.Kind != c.want || s.Item != c.item {
			t.Errorf("%s: got %+v", c.name, s)
		}
		if c.want == StepGoto && (s.Pos == nil || *s.Pos != (Pos{5, 64, 5}) || !p.Site.Valid) {
			t.Errorf("%s: goto pos/site wrong: %+v %+v", c.name, s, p.Site)
		}
	}
}

func TestMissingRecipes(t *testing.T) {
	f := facts(nil, TableFacts{}, fullGraph("minecraft:wooden_pickaxe"))
	p := NewToolProgression("x", Lease{}, f)
	want := map[string]bool{"minecraft:wooden_pickaxe": true, "minecraft:stone_pickaxe": true,
		"minecraft:stone_axe": true, "minecraft:stone_sword": true, "minecraft:torch": true}
	if len(p.Unplannable) != len(want) {
		t.Fatalf("unplannable: %v", p.Unplannable)
	}
	for _, u := range p.Unplannable {
		if !want[u] {
			t.Fatalf("unexpected %s", u)
		}
	}
	if idx(p.Steps, StepCraft, "minecraft:wooden_axe") < 0 {
		t.Fatal("wooden axe should still be planned")
	}
	f = facts(nil, TableFacts{}, crafting.NewRecipeGraph(nil))
	p = NewToolProgression("x", Lease{}, f)
	if _, st := Next(p, f); st != NextBlocked {
		t.Fatalf("status %v", st)
	}
	f = facts(nil, TableFacts{}, fullGraph("minecraft:crafting_table"))
	p = NewToolProgression("x", Lease{}, f)
	if idx(p.Steps, StepCraft, "minecraft:wooden_axe") >= 0 {
		t.Fatal("axe needs a table")
	}
}

func TestInvalidationAndReplan(t *testing.T) {
	g := fullGraph()
	f0 := facts(nil, TableFacts{}, g)
	p := NewToolProgression("keep", Lease{Owner: "o", ExpiresTick: 100}, f0)
	if r := Check(p, f0); r != InvalidNone {
		t.Fatalf("fresh plan invalid: %s", r)
	}
	f1 := facts(map[string]int{logs: 20}, TableFacts{}, g)
	if r := Check(p, f1); r != InvalidNone {
		t.Fatalf("progress invalidated: %s", r)
	}
	f2 := f0
	f2.Tick = 100
	if r := Check(p, f2); r != InvalidLeaseExpired {
		t.Fatalf("got %s", r)
	}
	f3 := facts(nil, TableFacts{}, fullGraph("minecraft:wooden_axe"))
	if r := Check(p, f3); r != InvalidRecipeMissing {
		t.Fatalf("got %s", r)
	}
	fHave := facts(map[string]int{itemTable: 1}, TableFacts{}, g)
	pn := NewToolProgression("n", Lease{}, fHave)
	if r := Check(pn, f0); r != InvalidBOMDrift {
		t.Fatalf("got %s", r)
	}
	np := Replan(pn, f0)
	if np.ID != "n" || np.Goal != pn.Goal || idx(np.Steps, StepCraft, itemTable) < 0 {
		t.Fatalf("replan: %v", kinds(np.Steps))
	}
	if r := Check(np, f0); r != InvalidNone {
		t.Fatalf("replanned still invalid: %s", r)
	}
	inv := map[string]int{planks: 8, stick: 4}
	pg := NewToolProgression("g", Lease{}, facts(inv, TableFacts{Known: true, Pos: Pos{1, 2, 3}, Dist: 10}, g))
	if r := Check(pg, facts(inv, TableFacts{}, g)); r != InvalidSiteLost {
		t.Fatalf("got %s", r)
	}
}

func TestMixedLogsPrefersHeld(t *testing.T) {
	f := facts(map[string]int{"minecraft:birch_log": 10}, TableFacts{}, fullGraph())
	p := NewToolProgression("x", Lease{}, f)
	for _, b := range p.BOM {
		if b.Item == logs {
			t.Fatalf("gathers oak despite held birch: %+v", p.BOM)
		}
	}
	if s := p.Steps[0]; s.Kind != StepCraft || s.Item != "minecraft:birch_planks" {
		t.Fatalf("first step %+v", s)
	}
}

func TestUnknownGoal(t *testing.T) {
	if _, ok := New("x", Goal{Kind: "nope"}, Lease{}, Facts{}); ok {
		t.Fatal("expected !ok")
	}
}
