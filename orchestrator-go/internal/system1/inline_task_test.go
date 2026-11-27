package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-2sr.21, cq-507.4: a task a waterfall starts is its child at once, so it survives the stage
// moving on beneath it.
func TestStartedTaskIsAChildFrameAndSurvivesAStageChange(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	tick := func(f *civquestv1.TelemetryFrame) FSMResult {
		return fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
	}

	res := tick(rf(100, 3000, withBlocks(treeColumn...)))
	if !hasInteract(res) || !st.HasActiveTask() {
		t.Fatalf("setup: bootstrap must start a harvest task, intents=%v hasTask=%t", res.Intents, st.HasActiveTask())
	}
	if kinds := stackKinds(fsm); len(kinds) != 2 || kinds[0] != activityKindBootstrap || kinds[1] != activityKindTaskSubroutine {
		t.Fatalf("setup: expected the task as bootstrap's child in the same tick, got %v", kinds)
	}

	// The survival kit is complete: the stage leaves Bootstrap.
	tick(rf(110, 3000, withInv(survivalKit()...), withBlocks(treeColumn...)))
	if !st.HasActiveTask() {
		t.Fatalf("task dropped when the stage moved on; stack=%v", stackKinds(fsm))
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
