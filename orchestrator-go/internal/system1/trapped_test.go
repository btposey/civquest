package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// Live (cq-6l3.23): agents stayed on one spot for most of a run, sidestepping, giving up and
// starting again. After giving up twice from the same spot an agent treats itself as boxed in.
func TestRecover_RepeatedGiveUpsOnOneSpotEscalateToDigging(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	world := func(tick int64, opts ...fo) *fsmWorld {
		return newFSMWorld(fsm, &FSMContext{AgentID: "trapped", State: st, Frame: rf(tick, 3000, opts...)}, nil, nil)
	}
	ask := func(tick int64, opts ...fo) recoverArgs {
		rc := terrainStuck(1)
		w := world(tick, opts...)
		w.receipt = rc
		a, ok := w.recoverArgsFor(htpda.Receipt{Frame: 1, Status: rc.GetStatus(), Facts: motorFacts(rc.GetFacts())}, 0)
		if !ok {
			t.Fatal("a stuck receipt must ask for recovery")
		}
		return a
	}
	here := withPos(10.5, 64, 10.5)
	for i, tick := range []int64{100, 300} {
		if a := ask(tick, here); a.Dig || a.Cause != CauseTerrain {
			t.Fatalf("after %d give-ups: %+v", i, a)
		}
		fsm.applyMemoryLocked(world(tick, here), spotGiveUp(world(tick, here).pos(), tick))
	}
	if a := ask(500, here); !a.Dig || a.Cause != CauseEnclosed {
		t.Errorf("third time on the same spot must dig out, got %+v", a)
	}
	if a := ask(500, withPos(30.5, 64, 10.5)); a.Dig {
		t.Errorf("elsewhere the agent is not boxed in, got %+v", a)
	}
	if a := ask(300+trapMemoryTicks+1, here); a.Dig {
		t.Errorf("old give-ups must be forgotten, got %+v", a)
	}
}
