package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-2sr.21 gap: a task a waterfall started inline lives in the starting frame's walk cursor
// until the next tick frames it. If that frame exits first (here: bootstrap stops being
// eligible because the stage moves on), the task must be handed to a task frame, not dropped.
func TestInlineStartedTaskSurvivesStartingFrameExit(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	tick := func(f *civquestv1.TelemetryFrame) FSMResult {
		return fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
	}

	res := tick(rf(100, 3000, withBlocks(treeColumn...)))
	if !hasInteract(res) || !st.HasActiveTask() {
		t.Fatalf("setup: bootstrap must start a harvest task inline, intents=%v hasTask=%t", res.Intents, st.HasActiveTask())
	}
	if kinds := stackKinds(fsm); len(kinds) != 1 || kinds[0] != activityKindBootstrap {
		t.Fatalf("setup: expected only the starting frame on the stack, got %v", kinds)
	}

	// The survival kit is complete: the stage leaves Bootstrap, so the starting frame exits.
	tick(rf(110, 3000, withInv(survivalKit()...), withBlocks(treeColumn...)))
	if !st.HasActiveTask() {
		t.Fatalf("inline-started task dropped when its starting frame exited; stack=%v", stackKinds(fsm))
	}
	found := false
	for _, k := range stackKinds(fsm) {
		found = found || k == activityKindTaskSubroutine
	}
	if !found {
		t.Fatalf("expected the task to live in a task frame, stack=%v", stackKinds(fsm))
	}
}

func hasInteract(res FSMResult) bool {
	for _, fi := range res.Intents {
		if fi.Intent.GetInteractBlock() != nil {
			return true
		}
	}
	return false
}

func stackKinds(fsm *AgentFSM) []string {
	var ks []string
	for _, f := range fsm.ActivityFrames() {
		ks = append(ks, f.Kind)
	}
	return ks
}
