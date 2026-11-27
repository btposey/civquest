package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Live smoke: a torch that never appeared was placed on the same cell 212 times.
func TestShelter_LightThatNeverAppearsIsTriedElsewhereThenLeft(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("lamplighter")
	inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}, {ItemId: "minecraft:torch", Count: 4},
		{ItemId: "minecraft:oak_planks", Count: 20}}
	cells := map[[3]int32]int{}
	total := 0
	for tick := int64(100); tick < 2500; tick += 4 {
		res := fsm.Tick(&FSMContext{AgentID: "lamplighter", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(tick, 14000, 20, inv, nil)})
		for _, in := range res.Intents {
			if ib := in.Intent.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK &&
				ib.BlockOrItemId == "minecraft:torch" {
				cells[[3]int32{ib.Pos.X, ib.Pos.Y, ib.Pos.Z}]++
				total++
			}
		}
	}
	if total == 0 || total > shelterLightTries || len(cells) < min(total, 3) {
		t.Errorf("torch placed %d times on %d cells, want 1..%d over several cells", total, len(cells), shelterLightTries)
	}
}
