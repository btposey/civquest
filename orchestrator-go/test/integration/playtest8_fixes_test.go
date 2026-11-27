package integration

// Play test 8 fixes (cq-j0e): places given up were walked to again and again.

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const givenUpForTest = int64(1_000_000)

func ordersNear(s *eval.PlaytestSim, x, z, within float64) (walks, breaks int) {
	for _, step := range s.Steps {
		for _, in := range step.Intents {
			if n := in.GetNavigate(); n != nil && math.Hypot(n.Target.X-x, n.Target.Z-z) <= within {
				walks++
			}
			if b := in.GetInteractBlock(); b != nil && b.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK &&
				math.Hypot(float64(b.Pos.X)+0.5-x, float64(b.Pos.Z)+0.5-z) <= within {
				breaks++
			}
		}
	}
	return walks, breaks
}

// UNFIXED BEHAVIOUR: fred sent one walk to an ore 115 times, with 57 notes that it could not be
// reached.
func TestPlaytest8_01_OreGivenUpIsLeftForAnother(t *testing.T) {
	w := groundWorld(40)
	w.AddStoneOutcrop(5, -1, 1, 1) // the nearer one is given up
	w.AddStoneOutcrop(-11, -1, 3, 3)
	inv := append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_pickaxe", Count: 1})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageSettlement, World: w, TimeOfDay: 3000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
		Setup: func(st *system1.AgentState) {
			st.MarkTargetUnreachable(5.5, 64, -0.5, 90, givenUpForTest)
			st.SetStrategicDirective(system1.DirectiveActionMineStone, "stone", "gather cobblestone", 90, givenUpForTest)
		},
	})
	s.Run(2 * simTicksPerMinute)

	if walks, breaks := ordersNear(s, 5.5, -0.5, 0.6); walks+breaks > 0 {
		t.Errorf("went after the ore that was given up: %d walks, %d breaks", walks, breaks)
	}
	if s.ItemCount("minecraft:cobblestone") < 1 {
		t.Errorf("no cobblestone from the other outcrop (inventory: %s)", s.InventorySummary())
	}
}

// UNFIXED BEHAVIOUR: carl, walled in below the cabin, was sent back to it 96 times.
func TestPlaytest8_02_CabinGivenUpIsNotWalkedTo(t *testing.T) {
	w := groundWorld(40)
	inv := append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 32})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 11800,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
		Setup: func(st *system1.AgentState) {
			st.SetShelterPos(&civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0})
			st.SetCabinBuilt(10)
			st.MarkTargetUnreachable(20.5, 64, 0.5, 90, givenUpForTest)
		},
	})
	s.Run(2 * simTicksPerMinute)

	if walks, _ := ordersNear(s, 20.5, 0.5, 4); walks > 0 {
		t.Errorf("sent back to the cabin that was given up %d times", walks)
	}
}
