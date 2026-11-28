package plan

// The ladder reserve (cq-htv.4): ladders are made in preparation, from wood that is to spare once
// the tool progression has had what it needs. Nothing is gathered for them, so a shortage of wood
// holds nothing up.

import (
	"maps"
	"slices"
	"strings"
)

const (
	itemLadder = "minecraft:ladder"
	// LadderReserve is how many ladders an agent aims to carry.
	LadderReserve = 12
	// ladderBatch is what one craft yields (from 7 sticks).
	ladderBatch = 3
	// ladderLow: without a table in reach, one is walked to or set down only below this.
	ladderLow = 6
	// woodKept is the wood, in planks, never spent on ladders: the six logs below which more
	// timber is gathered.
	woodKept = 24
)

// ladderSteps crafts as many batches towards the reserve as the spare inventory allows: what is
// carried and left over by the goals before it (left), less the wood kept.
func ladderSteps(f Facts, left map[string]int) []Step {
	have := f.Inventory[itemLadder]
	if have+ladderBatch > LadderReserve || !f.Table.Near && have >= ladderLow || len(f.Recipes.RecipesFor(itemLadder)) == 0 {
		return nil
	}
	spare := map[string]int{}
	for item, n := range f.Inventory {
		if n = min(n, left[item]); n > 0 && item != itemLadder {
			spare[item] = n
		}
	}
	keepWood(spare)
	for n := (LadderReserve - have) / ladderBatch; n > 0; n-- {
		s := newSim(f, maps.Clone(spare))
		if s.expand(itemLadder, n*ladderBatch, 0) && countGathers(s.steps) == 0 {
			return s.steps
		}
	}
	return nil
}

// keepWood takes the kept wood out of inv, whole logs first.
func keepWood(inv map[string]int) {
	kept := woodKept
	for _, kind := range []struct {
		planks   int
		suffixes []string
	}{{4, []string{"_log", "_wood", "_stem"}}, {1, []string{"_planks"}}} {
		for _, item := range slices.Sorted(maps.Keys(inv)) {
			if !slices.ContainsFunc(kind.suffixes, func(s string) bool { return strings.HasSuffix(item, s) }) {
				continue
			}
			n := min(inv[item], (kept+kind.planks-1)/kind.planks)
			inv[item] -= n
			kept = max(0, kept-n*kind.planks)
		}
	}
}
