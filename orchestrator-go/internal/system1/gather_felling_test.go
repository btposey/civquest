package system1

import (
	"testing"

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
