package crafting

import (
	"sort"
	"strings"
)

// Real recipe catalogues are full of cycles and conversions (any bed can be dyed into any
// other bed, coal <-> coal block, ingot <-> nugget). Searching them blindly is exponential, so
// every planner works from two precomputed facts:
//
//   - a derivation: for each craftable item, the one cheapest recipe that grounds out in
//     things that can be collected. It is acyclic by construction, because a recipe always
//     costs strictly more than each ingredient it is priced on.
//   - reachability: the items an inventory could turn into by crafting alone.

const (
	naturalCost   = 1  // felled, mined or taken from a mob
	scavengedCost = 20 // has no recipe but is not a routine resource (bone, bamboo, ink sac)
)

// Natural reports whether item is a resource the world supplies directly (felled, mined or
// taken from a mob). Planners collect such items instead of deriving them from other items.
func Natural(item string) bool {
	id := strings.TrimPrefix(normalizeItemID(item), "minecraft:")
	for _, suffix := range []string{"_log", "_stem", "_wood", "_wool", "_ore"} {
		if strings.HasSuffix(id, suffix) {
			return true
		}
	}
	switch id {
	case "cobblestone", "cobbled_deepslate", "stone", "blackstone", "coal", "charcoal":
		return true
	}
	return strings.HasPrefix(id, "raw_")
}

type recipeRef struct {
	result string
	index  int
}

type ingredientUse struct {
	recipe recipeRef
	slot   int
}

type recipeIndex struct {
	cost map[string]int
	best map[string]int // item -> index into byResult[item]
	uses map[string][]ingredientUse
}

func (g *RecipeGraph) index() *recipeIndex {
	g.indexOnce.Do(func() { g.idx = buildIndex(g.byResult) })
	return g.idx
}

func buildIndex(byResult map[string][]Recipe) *recipeIndex {
	ix := &recipeIndex{cost: map[string]int{}, best: map[string]int{}, uses: map[string][]ingredientUse{}}
	results := make([]string, 0, len(byResult))
	for item := range byResult {
		results = append(results, item)
	}
	sort.Strings(results)
	for _, item := range results {
		for ri, r := range byResult[item] {
			for si, slot := range r.Slots {
				for _, c := range slot.AcceptedItemIDs {
					ix.uses[c] = append(ix.uses[c], ingredientUse{recipeRef{item, ri}, si})
					switch {
					case Natural(c):
						ix.cost[c] = naturalCost
					case len(byResult[c]) == 0:
						ix.cost[c] = scavengedCost
					}
				}
			}
		}
		if Natural(item) {
			ix.cost[item] = naturalCost
		}
	}
	// Relax until stable. Costs only fall, so this terminates; items reachable only through a
	// cycle never receive one.
	for changed := true; changed; {
		changed = false
		for _, item := range results {
			if Natural(item) {
				continue
			}
		next:
			for ri, r := range byResult[item] {
				total := 1
				for _, slot := range r.Slots {
					cheapest := 0
					for _, c := range slot.AcceptedItemIDs {
						if v, ok := ix.cost[c]; ok && (cheapest == 0 || v < cheapest) {
							cheapest = v
						}
					}
					if cheapest == 0 {
						continue next
					}
					total += cheapest
				}
				cur, ok := ix.cost[item]
				if !ok || total < cur || (total == cur && ri < ix.best[item]) {
					ix.cost[item], ix.best[item] = total, ri
					changed = true
				}
			}
		}
	}
	return ix
}

// Cost ranks how hard item is to obtain from nothing (lower is easier). ok is false when the
// catalogue only produces it through a cycle.
func (g *RecipeGraph) Cost(item string) (int, bool) {
	if g == nil {
		return 0, false
	}
	item = normalizeItemID(item)
	if v, ok := g.index().cost[item]; ok {
		return v, true
	}
	if len(g.byResult[item]) == 0 {
		if Natural(item) {
			return naturalCost, true
		}
		return scavengedCost, true
	}
	return 0, false
}

// Derivation returns the cheapest grounded recipe for item. Following derivations ingredient
// by ingredient always terminates.
func (g *RecipeGraph) Derivation(item string) (Recipe, bool) {
	if g == nil {
		return Recipe{}, false
	}
	item = normalizeItemID(item)
	ri, ok := g.index().best[item]
	if !ok {
		return Recipe{}, false
	}
	return g.byResult[item][ri], true
}

// Reachable returns the items held in inv plus everything crafting could turn them into,
// ignoring quantities: an item outside the set cannot be crafted from inv at all.
func (g *RecipeGraph) Reachable(inv map[string]int) map[string]bool {
	reach := make(map[string]bool, len(inv))
	var queue []string
	add := func(item string) {
		if !reach[item] {
			reach[item] = true
			queue = append(queue, item)
		}
	}
	for item, n := range inv {
		if n > 0 {
			add(item)
		}
	}
	if g == nil {
		return reach
	}
	ix := g.index()
	filled := map[recipeRef]map[int]bool{}
	for len(queue) > 0 {
		item := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, u := range ix.uses[item] {
			if reach[u.recipe.result] {
				continue
			}
			slots := filled[u.recipe]
			if slots == nil {
				slots = map[int]bool{}
				filled[u.recipe] = slots
			}
			slots[u.slot] = true
			if len(slots) == len(g.byResult[u.recipe.result][u.recipe.index].Slots) {
				add(u.recipe.result)
			}
		}
	}
	return reach
}

// preferred lists item's recipes with its derivation first, so a bounded search spends its
// budget on the grounded route before conversions.
func (g *RecipeGraph) preferred(item string) []Recipe {
	list := g.byResult[item]
	ri, ok := g.index().best[item]
	if !ok || ri == 0 {
		return list
	}
	out := make([]Recipe, 0, len(list))
	out = append(out, list[ri])
	out = append(out, list[:ri]...)
	return append(out, list[ri+1:]...)
}
