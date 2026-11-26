package plan

import (
	"sort"
	"strings"
)

// Bed planner (cq-390.2): one bed = 3 wool of ONE colour (any colour) + 3 planks. The colour
// is the one the agent already holds most of (so partial wool is never wasted), white by
// default; wool is always a Gather from a mob (shear preferred, kill as fallback, cq-390.3).

// bedColours are the vanilla bed colours; "white" first as the default.
var bedColours = []string{
	"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black",
}

// WoolItem is the wool item id of colour.
func WoolItem(colour string) string { return "minecraft:" + colour + "_wool" }

// BedItem is the bed item id of colour.
func BedItem(colour string) string { return "minecraft:" + colour + "_bed" }

// HasBed reports whether inv holds a bed of any colour.
func HasBed(inv map[string]int) bool {
	for id, n := range inv {
		if n > 0 && strings.HasSuffix(id, "_bed") {
			return true
		}
	}
	return false
}

// bedColourOrder ranks colours by held wool (most first), then catalogue order.
func bedColourOrder(inv map[string]int) []string {
	out := append([]string(nil), bedColours...)
	sort.SliceStable(out, func(i, j int) bool { return inv[WoolItem(out[i])] > inv[WoolItem(out[j])] })
	return out
}

func planBed(f Facts) ([]Step, []BOMLine, []string) {
	inv := map[string]int{}
	for k, v := range f.Inventory {
		if v > 0 {
			inv[k] = v
		}
	}
	if HasBed(inv) {
		return nil, nil, nil
	}
	var unplannable []string
	for _, c := range bedColourOrder(inv) {
		bed := BedItem(c)
		if len(f.Recipes.RecipesFor(bed)) == 0 {
			continue
		}
		st := newSim(f, copyInv(inv))
		if st.expand(bed, 1, 0) {
			steps, bom := mergeGathers(st.steps)
			return steps, bom, nil
		}
	}
	unplannable = append(unplannable, BedItem("white"))
	return nil, nil, unplannable
}

func copyInv(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
