package plan

import (
	"math"
	"slices"
	"sort"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
)

const (
	itemTable = "minecraft:crafting_table"
	maxDepth  = 10
	// maxExpansions bounds the search for one goal item. Planning runs inside the agent
	// tick, so an unforeseen catalogue shape must yield "unplannable" rather than run on.
	maxExpansions = 4000
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
	// Shared by every clone of one planning call.
	reach  map[string]bool // craftable from the starting inventory
	path   map[string]bool // items being expanded: a recipe may not consume its own product
	budget *int
}

func newSim(f Facts, inv map[string]int) *sim {
	budget := maxExpansions
	return &sim{g: f.Recipes, f: f, inv: inv, reach: f.Recipes.Reachable(inv), path: map[string]bool{}, budget: &budget}
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
	st := newSim(f, inv)
	var unplannable []string
	for _, goal := range toolProgressionGoals {
		if owned(goal.item, goal.qty, st.inv) {
			continue
		}
		*st.budget = maxExpansions
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
	if f.ReserveOnly {
		return ladderSteps(f, st.inv), nil, nil
	}
	// The ladder reserve needs nothing gathered, so it does not wait behind a gather.
	at := slices.IndexFunc(steps, func(s Step) bool { return s.Kind == StepGather })
	if at < 0 {
		at = len(steps)
	}
	return slices.Insert(steps, at, ladderSteps(f, st.inv)...), bom, unplannable
}

// expand schedules qty of item, consuming held inventory first. It reports
// false (leaving s in an undefined state; callers expand on a clone) when the
// item cannot be produced.
func (s *sim) expand(item string, qty, depth int) bool {
	if depth > maxDepth || *s.budget <= 0 {
		return false
	}
	*s.budget--
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
	// Natural resources are collected, never converted from other items (wool is sheared or
	// dropped, not spun from string; coal is mined, not unpacked from blocks).
	if crafting.Natural(item) {
		return s.gather(item, deficit)
	}
	if len(s.g.RecipesFor(item)) == 0 {
		if _, _, isTool := toolRank(item); isTool || item == itemTable {
			return false // tools and tables are crafted, never gathered
		}
		return s.gather(item, deficit)
	}
	if s.path[item] {
		return false
	}
	s.path[item] = true
	defer delete(s.path, item)
	for _, r := range s.options(item) {
		trial := s.clone()
		if trial.craft(r, deficit, depth) {
			*s = *trial
			return true
		}
	}
	return false
}

// options lists the recipes worth trying for item, in order: those the held items satisfy
// right now, then the grounded derivation, then other routes that start from held items.
// Conversions that would need yet more converting (dyeing one bed into another) are never
// candidates, which keeps the search from exploring every ordering of them.
func (s *sim) options(item string) []crafting.Recipe {
	var now, later []crafting.Recipe
	best, grounded := s.g.Derivation(item)
	for _, r := range s.g.RecipesFor(item) {
		switch {
		case s.holds(r):
			now = append(now, r)
		case grounded && r.RecipeID == best.RecipeID:
		case s.reachable(r):
			later = append(later, r)
		}
	}
	if grounded && !s.holds(best) {
		now = append(now, best)
	}
	return append(now, later...)
}

// holds reports whether every slot of r can be filled from the current inventory.
func (s *sim) holds(r crafting.Recipe) bool {
	for _, slot := range r.Slots {
		have := 0
		for _, c := range slot.AcceptedItemIDs {
			have += s.inv[c]
		}
		if have < slot.Count {
			return false
		}
	}
	return true
}

// reachable reports whether every slot of r has a choice craftable from the starting items.
func (s *sim) reachable(r crafting.Recipe) bool {
	for _, slot := range r.Slots {
		ok := false
		for _, c := range slot.AcceptedItemIDs {
			if s.reach[c] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
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
		if ci, cj := s.cost(cands[i]), s.cost(cands[j]); ci != cj {
			return ci < cj
		}
		return cands[i] < cands[j]
	})
	// Pass 0 accepts only choices that need no new gathering (use what is
	// held, directly or via a craft); pass 1 accepts any producible choice.
	for pass := 0; pass < 2; pass++ {
		for _, c := range cands {
			if pass == 0 && s.inv[c] == 0 && !s.reach[c] {
				continue // cannot be had without gathering
			}
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

// cost ranks a candidate by how hard it is to obtain; cycle-only items rank last.
func (s *sim) cost(item string) int {
	if v, ok := s.g.Cost(item); ok {
		return v
	}
	return math.MaxInt32
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
// otherwise craft a new one and Place it. Under NoNew none is placed or made.
func (s *sim) ensureTable(depth int) bool {
	if s.tableOK {
		return true
	}
	t := s.f.Table
	switch {
	case t.Near:
	case s.inv[itemTable] > 0 && !t.NoNew:
		s.inv[itemTable]--
		s.steps = append(s.steps, Step{Kind: StepPlace, Item: itemTable})
	case t.Known && t.Dist <= MaxTableWalk && !math.IsNaN(t.Dist):
		p := t.Pos
		s.steps = append(s.steps, Step{Kind: StepGoto, Pos: &p})
	default:
		if t.NoNew || !s.expand(itemTable, 1, depth) {
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
