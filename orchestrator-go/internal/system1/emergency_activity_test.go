package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func emergencyLocalOf(t *testing.T, fsm *AgentFSM) (uint64, emergencyLocal) {
	t.Helper()
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	top, ok := fsm.kernel.Top()
	if !ok || top.Kind() != activityKindEmergency {
		t.Fatalf("expected emergency frame on top, got %+v", top)
	}
	return uint64(top.ID), top.Local.(emergencyLocal)
}

// The emergency frame owns its private state (mode, flee target) and receives Java reflex
// notices as Reflex events routed by preempted_frame_id.
func TestEmergencyActivity_LocalAndReflexEvents(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	fsm.StartTask(st, NewHarvestTreeTask(2, 64, 0, StageSettlement, 100))
	fsm.Tick(makeActivityTestCtx(st, 100, 20, false))

	fsm.Tick(makeActivityTestCtx(st, 110, 6, true))
	id, el := emergencyLocalOf(t, fsm)
	if el.Mode != emergencyCombat || el.EnteredTick != 110 || el.LastFleeTarget == nil || el.LastFleeTick != 110 {
		t.Fatalf("unexpected emergency local after entry: %+v", el)
	}

	ctx := makeActivityTestCtx(st, 120, 6, true)
	ctx.Frame.ReflexNotices = []*civquestv1.ReflexNotice{
		{ReflexType: civquestv1.ReflexNotice_REFLEX_TYPE_SHIELD_DEFENSE, PreemptedFrameId: id},
		{ReflexType: civquestv1.ReflexNotice_REFLEX_TYPE_FLEE, PreemptedFrameId: 9999}, // stale: dropped
		{ReflexType: civquestv1.ReflexNotice_REFLEX_TYPE_DODGE},                        // unknown id: top frame
	}
	fsm.Tick(ctx)
	_, el = emergencyLocalOf(t, fsm)
	if el.Reflexes != 2 || el.LastReflex != civquestv1.ReflexNotice_REFLEX_TYPE_DODGE {
		t.Fatalf("expected 2 routed reflex events, got %+v", el)
	}
}

func TestEmergencyActivity_CriticalHungerMode(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	ctx := makeActivityTestCtx(st, 100, 20, false)
	ctx.Frame.Status.FoodLevel = 4
	ctx.Frame.Status.Inventory = append(ctx.Frame.Status.Inventory, &civquestv1.ItemStackProto{ItemId: "minecraft:cooked_beef", Count: 3})
	res := fsm.Tick(ctx)
	if res.Stage != StageEmergency || fsm.ActiveActivityKind() != activityKindEmergency {
		t.Fatalf("expected emergency, got stage=%s top=%q", res.Stage, fsm.ActiveActivityKind())
	}
	if _, el := emergencyLocalOf(t, fsm); el.Mode != emergencyHunger {
		t.Fatalf("expected hunger mode, got %+v", el)
	}
}
