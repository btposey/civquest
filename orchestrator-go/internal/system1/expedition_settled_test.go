package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// cq-6l3.3: once the clan site was chosen and reached, every requested expedition finished in
// the tick it started, thousands of times, while the agent stood idle.
func TestExpedition_SettledAgentDoesNotStartAndFinishEveryTick(t *testing.T) {
	r := newExpRig(t, "settled_scout", 4)
	r.st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	pushes := 0
	r.fsm.SetEventSink(func(_ int64, category, _ string, d map[string]any) {
		if category == EventFrame && d["op"] == "push" && d["kind"] == activityKindExpedition {
			pushes++
		}
	})
	for tick := int64(100); tick < 600; tick += 4 {
		r.step(tick)
	}
	if pushes > 1 {
		t.Fatalf("expedition framed %d times at the settlement site", pushes)
	}
}

// An equipped agent's resource trip waits until it has stood idle: reflection gets the first
// turn. One without its survival kit goes at once (cq-gey.6).
func TestExpedition_SettledTripWaitsForIdleness(t *testing.T) {
	r := newExpRig(t, "settled_waiter", 4)
	r.fsm = NewAgentFSM(StageBootstrap, nil)
	r.fsm.currentRoutine = RoutinePrimaryDuty
	r.st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	kit := withInv(survivalKit()...)
	allowed := func(tick int64, opts ...fo) bool {
		frame := rf(tick, 4000, opts...)
		w := &fsmWorld{fsm: r.fsm, tick: tick, skipped: map[string]bool{},
			ctx: &FSMContext{AgentID: "settled_waiter", State: r.st, Frame: frame}}
		_, ok := w.expeditionStart(activityKindBootstrap)
		return ok
	}
	r.st.UpdateIdleTracker(100, rf(100, 4000).Status.Position, false, false)
	if !allowed(104) {
		t.Fatal("an unequipped agent must go for resources at once")
	}
	if allowed(400, kit) {
		t.Fatal("an equipped agent's trip must not start after 300 idle ticks")
	}
	if !allowed(500, kit) {
		t.Fatal("a trip must be allowed after 400 idle ticks")
	}
}

// A settled agent that needs resources still goes out: before, the trip ended as it began.
func TestExpedition_SettledBootstrapTripScoutsInsteadOfEnding(t *testing.T) {
	r := newExpRig(t, "settled_forager", 4)
	r.st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	r.step(100)
	w := &fsmWorld{fsm: r.fsm, ctx: &FSMContext{AgentID: "settled_forager", State: r.st, Frame: rf(104, 4000)}}
	local, _ := expeditionActivity{}.Init(w, expeditionArgs{Origin: activityKindBootstrap})
	loc, ok := local.(expeditionLocal)
	if !ok || !loc.Forage {
		t.Fatalf("expected a resource trip, got %+v", local)
	}
	if step := (expeditionActivity{}).decide(w, loc); step.Op != htpda.OpPush {
		t.Fatalf("a resource trip at the site must survey or travel, got op %v child %q", step.Op, step.Child)
	}
}
