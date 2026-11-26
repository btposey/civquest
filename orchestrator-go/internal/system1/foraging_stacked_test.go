package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-6l3.2: a foraging frame stacked above another one popped on its first quiet tick, forgot
// what it had visited and walked back to the same chest forever.
func TestForaging_StackedFrameKeepsItsCursorThroughQuietTicks(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("stacked_forager")
	cfg.Profession = "FARMER"
	tick := func(at int64, food int32, nav civquestv1.NavigationState) FSMResult {
		f := makeArbiterTestFrame(at, 3000, food, fullSurvivalKitInv(), nil)
		f.Status.NavigationState = nav
		return fsm.Tick(&FSMContext{AgentID: "stacked_forager", State: st, Config: cfg, Frame: f})
	}
	idle := civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	walking := civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	tick(100, 20, idle)
	tick(250, 20, idle)
	if res := tick(270, 8, idle); fsm.ActivityDepth() != 2 || fsm.ActiveActivityKind() != activityKindForaging || len(res.Intents) == 0 {
		t.Fatalf("expected foraging walking above exploration, depth=%d kind=%s intents=%v", fsm.ActivityDepth(), fsm.ActiveActivityKind(), res.Intents)
	}
	frame := fsm.ActivityFrames()[1].ID
	for i := int64(1); i <= 20; i++ {
		res := tick(270+i*4, 8, walking)
		if fsm.ActivityDepth() != 2 || fsm.ActivityFrames()[1].ID != frame {
			t.Fatalf("quiet tick %d replaced the foraging frame: depth=%d", i, fsm.ActivityDepth())
		}
		if len(res.Intents) != 0 {
			t.Fatalf("quiet tick %d emitted %v while walking", i, res.Intents)
		}
	}
	// Fed again: eligibility still ends the frame.
	tick(400, 20, idle)
	if fsm.ActiveActivityKind() == activityKindForaging {
		t.Fatal("a fed agent must leave foraging")
	}
}
