package plan

import (
	"math"
	"sort"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
)

const (
	itemTable = "minecraft:crafting_table"
	maxDepth  = 10
)

type goalItem struct {
	item string
	qty  int
}

// toolProgressionGoals is the ordered progression:
// wooden axe, sword, pickaxe, then stone pickaxe, axe, sword, then torches.
// The crafting table is pulled in on demand by the first 3x3 recipe.
var toolProgressionGoals = []goalItem{
	{"minecraft:wooden_axe", 1},
	{"minecraft:wooden_sword", 1},
	{"minecraft:wooden_pickaxe", 1},
	{"minecraft:stone_pickaxe", 1},
	{"minecraft:stone_axe", 1},
	{"minecraft:stone_sword", 1},
	{"minecraft:torch", TorchQty},
}

// slotPreference orders interchangeable ingredients; unlisted items rank last
// and ties break by id, keeping choices deterministic.
var slotPreference = map[string]int{
	"minecraft:oak_planks":  0,
	"minecraft:oak_log":     0,
	"minecraft:coal":        0,
	"minecraft:cobblestone": 0,
}

type sim struct {
	g       *crafting.RecipeGraph
	f       Facts
	inv     map[string]int
	steps   []Step
	tableOK bool
	hasTool func(string) bool
}

func (s *sim) clone() *sim {
	c := *s
	c.inv = make(map[string]int, len(s.inv))
	for k, v := range s.inv {
		c.inv[k] = v
	}
	c.steps = append([]Step(nil), s.steps...)
	return &c
}

func (s *sim) gate(item string) string {
	if s.f.ToolGate != nil {
		return s.f.ToolGate(item)
	}
	return DefaultToolGate(item)
}

func planToolProgression(f Facts) ([]Step, []BOMLine, []string) {
	inv := map[string]int{}
	for k, v := range f.Inventory {
		if v > 0 {
			inv[k] = v
		}
	}
	st := &sim{g: f.Recipes, f: f, inv: inv}
	var unplannable []string
	for _, goal := range toolProgressionGoals {
		if owned(goal.item, goal.qty, st.inv) {
			continue
		}
		trial := st.clone()
		if len(f.Recipes.RecipesFor(goal.item)) == 0 || !trial.expand(goal.item, goal.qty, 0) {
			unplannable = append(unplannable, goal.item)
			continue
		}
		*st = *trial
		st.inv[goal.item] += goal.qty // the goal item is now held for later gating
	}
	sort.Strings(unplannable)
	steps, bom := mergeGathers(st.steps)
	return steps, bom, unplannable
}

// expand schedules qty of item, consuming held inventory first. It reports
// false (leaving s in an undefined state; callers expand on a clone) when the
// item cannot be produced.
func (s *sim) expand(item string, qty, depth int) bool {
	if depth > maxDepth {
		return false
	}
	if _, _, isTool := toolRank(item); isTool && owned(item, qty, s.inv) {
		return true
	}
	take := s.inv[item]
	if take > qty {
		take = qty
	}
	s.inv[item] -= take
	deficit := qty - take
	if deficit == 0 {
		return true
	}
	recipes := s.g.RecipesFor(item)
	if len(recipes) == 0 {
		if _, _, isTool := toolRank(item); isTool || item == itemTable {
			return false // tools and tables are crafted, never gathered
		}
		return s.gather(item, deficit)
	}
	for _, r := range recipes {
		trial := s.clone()
		if trial.craft(r, deficit, depth) {
			*s = *trial
			return true
		}
	}
	return false
}

func (s *sim) gather(item string, qty int) bool {
	tool := s.gate(item)
	if tool != "" && !owned(tool, 1, s.inv) {
		return false
	}
	s.steps = append(s.steps, Step{
		Kind: StepGather, Item: item, Qty: qty, Source: sourceOf(item), RequiresTool: tool,
	})
	return true
}

func (s *sim) craft(r crafting.Recipe, deficit, depth int) bool {
	rc := r.ResultCount
	if rc <= 0 {
		rc = 1
	}
	times := (deficit + rc - 1) / rc
	for _, slot := range r.Slots {
		if !s.fillSlot(slot, slot.Count*times, depth+1) {
			return false
		}
	}
	if r.Requires3x3 && !s.ensureTable(depth+1) {
		return false
	}
	s.steps = append(s.steps, Step{
		Kind: StepCraft, Item: r.ResultItemID, RecipeID: r.RecipeID, Times: times, Requires3x3: r.Requires3x3,
	})
	s.inv[r.ResultItemID] += times*rc - deficit
	return true
}

func (s *sim) fillSlot(slot crafting.IngredientSlot, need, depth int) bool {
	cands := append([]string(nil), slot.AcceptedItemIDs...)
	sort.Slice(cands, func(i, j int) bool {
		hi, hj := s.inv[cands[i]] > 0, s.inv[cands[j]] > 0
		if hi != hj {
			return hi
		}
		if hi && s.inv[cands[i]] != s.inv[cands[j]] {
			return s.inv[cands[i]] > s.inv[cands[j]]
		}
		ri, rj := rank(cands[i]), rank(cands[j])
		if ri != rj {
			return ri < rj
		}
		return cands[i] < cands[j]
	})
	// Pass 0 accepts only choices that need no new gathering (use what is
	// held, directly or via a craft); pass 1 accepts any producible choice.
	for pass := 0; pass < 2; pass++ {
		for _, c := range cands {
			trial := s.clone()
			if !trial.expand(c, need, depth) {
				continue
			}
			if pass == 0 && countGathers(trial.steps) > countGathers(s.steps) {
				continue
			}
			*s = *trial
			return true
		}
	}
	return false
}

func countGathers(steps []Step) int {
	n := 0
	for _, st := range steps {
		if st.Kind == StepGather {
			n++
		}
	}
	return n
}

func rank(item string) int {
	if r, ok := slotPreference[item]; ok {
		return r
	}
	return 1
}

// ensureTable makes a crafting table available for a 3x3 craft: nothing when
// one is in reach, Place when carried, Goto when a known one is close enough,
// otherwise craft a new one and Place it.
func (s *sim) ensureTable(depth int) bool {
	if s.tableOK {
		return true
	}
	t := s.f.Table
	switch {
	case t.Near:
	case s.inv[itemTable] > 0:
		s.inv[itemTable]--
		s.steps = append(s.steps, Step{Kind: StepPlace, Item: itemTable})
	case t.Known && t.Dist <= MaxTableWalk && !math.IsNaN(t.Dist):
		p := t.Pos
		s.steps = append(s.steps, Step{Kind: StepGoto, Pos: &p})
	default:
		if !s.expand(itemTable, 1, depth) {
			return false
		}
		s.steps = append(s.steps, Step{Kind: StepPlace, Item: itemTable})
	}
	s.tableOK = true
	return true
}

// mergeGathers folds every Gather of the same item into its first occurrence
// (one trip per resource) and returns the BOM of those totals. Tool gating is
// unaffected: the first occurrence was already validated after its tool.
func mergeGathers(in []Step) ([]Step, []BOMLine) {
	first := map[string]int{}
	totals := map[string]int{}
	var out []Step
	for _, st := range in {
		if st.Kind != StepGather {
			out = append(out, st)
			continue
		}
		totals[st.Item] += st.Qty
		if i, ok := first[st.Item]; ok {
			out[i].Qty += st.Qty
			continue
		}
		first[st.Item] = len(out)
		out = append(out, st)
	}
	return out, sortedBOM(totals)
}
