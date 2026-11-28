package smoke

// cq-d6p: tree choice in the real System 1 kernel in the closed-loop eval.PlaytestSim.

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// A branching tree stands next to the agent and two small ones farther off: the small ones are
// felled first, and the branching one once they are gone and wood is still wanted.
func TestSmoke_SmallTreesAreFelledBeforeABranchingOne(t *testing.T) {
	w := harvestWorld()
	w.AddTrunk(4, 0, 5, "")
	for _, p := range []eval.BlockPosKey{{X: 5, Y: 67, Z: 0}, {X: 6, Y: 68, Z: 0}, {X: 3, Y: 67, Z: 1}} {
		w.Blocks[p] = "minecraft:oak_log"
	}
	w.AddTrunk(-9, 3, 4, "")
	w.AddTrunk(-2, -11, 4, "")
	s := harvestSim(w, []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}}, 20)
	small, large := 0, 0
	felled := func(s *eval.PlaytestSim) bool {
		small, large = 0, 0
		for _, h := range s.Harvests {
			if !h.Existed || h.BlockID != "minecraft:oak_log" {
				continue
			}
			if h.Cell.X == -9 || h.Cell.X == -2 {
				small++
				if large > 0 {
					return true
				}
			} else {
				large++
			}
		}
		return large > 0
	}
	if !s.RunUntil(felled, 4*harvestSimMinute) {
		t.Fatalf("the branching tree was never felled: small logs=%d inventory=%s", small, s.InventorySummary())
	}
	if small != 8 {
		t.Fatalf("branching tree cut with %d of 8 small-tree logs taken: %+v", small, s.Harvests)
	}
}
