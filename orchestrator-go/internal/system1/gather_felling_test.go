package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// A felling that ends at once (live: the task was dropped as soon as it was framed, 800 times
// in ten minutes) is not started again for the same trunk.
func TestGather_FellingThatEndsAtOnceIsNotRestarted(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("feller")
	pushes := countPushes(fsm)
	var blocks []*civquestv1.VisibleBlock
	for y := int32(64); y < 68; y++ {
		blocks = append(blocks, &civquestv1.VisibleBlock{BlockId: "minecraft:oak_log", Pos: &civquestv1.BlockPosProto{X: 6, Y: y, Z: 0}})
	}
	for tick := int64(100); tick < 1300; tick += 4 {
		fsm.Tick(&FSMContext{AgentID: "feller", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(tick, 3000, 20, fullSurvivalKitInv(), blocks)})
		// The task is lost as soon as it exists, whatever the reason.
		for _, f := range fsm.activities() {
			if tl, ok := f.Local.(taskLocal); ok && tl.Task != nil {
				tl.Task.Phase = TaskPhaseFailed
			}
		}
	}
	if got := pushes[activityKindTaskSubroutine]; got > 12 {
		t.Errorf("task pushed %d times in 1200 ticks (all pushes %v)", got, pushes)
	}
}

// Live smoke: a stocking task was dropped by the bootstrap waterfall (which wanted to craft from
// the logs carried) and relaunched within the same tick, 691 times. Such a task runs on its own.
func TestGather_StockingTaskIsNotAbandonedByTheBootstrapWaterfall(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, NewDefaultEngineRegistry())
		st := NewAgentState()
		st.SetRecipeGraph(crafting.NewRecipeGraph(craftingtest.Nodes()))
		cfg := xdg.DefaultAgentIdentityConfig("stocker")
		inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}, {ItemId: "minecraft:oak_log", Count: 8}}
		var blocks []*civquestv1.VisibleBlock
		for y := int32(64); y < 68; y++ {
			blocks = append(blocks, &civquestv1.VisibleBlock{BlockId: "minecraft:oak_log", Pos: &civquestv1.BlockPosProto{X: 3, Y: y, Z: 0}})
		}
		task := NewHarvestTreeTask(3, 64, 0, StageBootstrap, 100)
		task.Spec.Standalone = standalone
		fsm.StartTask(st, task)
		chops := 0
		for tick := int64(100); tick < 200; tick += 4 {
			res := fsm.Tick(&FSMContext{AgentID: "stocker", State: st, Config: cfg,
				Frame: makeArbiterTestFrame(tick, 3000, 20, inv, blocks)})
			for _, in := range res.Intents {
				if ib := in.Intent.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK {
					chops++
				}
			}
		}
		if standalone && chops == 0 {
			t.Error("the stocking task never chopped: something abandoned it")
		}
		t.Logf("standalone=%t chops=%d", standalone, chops)
	}
}
