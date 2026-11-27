package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Play test 8: one cell was water, where the torch showed for a moment and was washed away, and
// the other refused it. Each sighting reset the count and the torch was placed 625 times.
func TestShelter_LightThatWashesAwayIsStillGivenUp(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("lamplighter")
	inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}, {ItemId: "minecraft:torch", Count: 4},
		{ItemId: "minecraft:oak_planks", Count: 4}}
	total, glimpse := 0, false
	for tick := int64(100); tick < 4000; tick += 4 {
		var blocks []*civquestv1.VisibleBlock
		if glimpse {
			blocks, glimpse = []*civquestv1.VisibleBlock{blk(1, 64, 1, "minecraft:torch")}, false
		}
		res := fsm.Tick(&FSMContext{AgentID: "lamplighter", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(tick, 14000, 20, inv, blocks)})
		for _, in := range res.Intents {
			if ib := in.Intent.GetInteractBlock(); ib != nil && ib.BlockOrItemId == "minecraft:torch" {
				total++
				glimpse = total%2 == 1 // every other placement shows, briefly
			}
		}
	}
	if total == 0 || total > shelterLightTries {
		t.Errorf("torch placed %d times, want 1..%d", total, shelterLightTries)
	}
}

// A torch is not placed while the agent stands in water.
func TestShelter_NoLightFromTheWater(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("lamplighter")
	inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:torch", Count: 4}, {ItemId: "minecraft:oak_planks", Count: 4}}
	for tick := int64(100); tick < 1000; tick += 4 {
		f := makeArbiterTestFrame(tick, 14000, 20, inv, nil)
		f.Status.IsInWater = true
		res := fsm.Tick(&FSMContext{AgentID: "lamplighter", State: st, Config: cfg, Frame: f})
		for _, in := range res.Intents {
			if ib := in.Intent.GetInteractBlock(); ib != nil && ib.BlockOrItemId == "minecraft:torch" {
				t.Fatalf("placed a torch while in water at tick %d", tick)
			}
		}
	}
}
