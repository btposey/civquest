package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func shelterFrameLocal(t *testing.T, fsm *AgentFSM) (shelterLocal, uint64) {
	t.Helper()
	for _, f := range fsm.activities() {
		if sl, ok := f.Local.(shelterLocal); ok {
			return sl, uint64(f.ID)
		}
	}
	t.Fatalf("no shelter frame on stack (top=%q depth=%d)", fsm.ActiveActivityKind(), fsm.ActivityDepth())
	return shelterLocal{}, 0
}

// Build progress lives in the Shelter frame Local: it carries across ticks (including ticks that
// emit nothing because of the place throttle) and across suspension by an emergency frame.
func TestShelterFrame_ProgressPersistsAcrossTicksAndSuspend(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	lit := withBlocks(blk(1, 64, 1, "minecraft:torch"))
	inv := withInv(item(0, "minecraft:cobblestone", 64), item(1, "minecraft:torch", 4))
	tick := func(f *civquestv1.TelemetryFrame) FSMResult {
		return fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
	}
	placed := func(r FSMResult) bool {
		for _, fi := range r.Intents {
			if ib := fi.Intent.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
				return true
			}
		}
		return false
	}

	tick(rf(90, 14000, inv)) // lights the site first (torch placement does not advance the cabin)
	if !placed(tick(rf(120, 14000, inv, lit))) {
		t.Fatal("first tick did not place a cabin block")
	}
	l1, id1 := shelterFrameLocal(t, fsm)
	if l1.BuildIndex != 1 || l1.Phase != blueprints.CabinBlockPhase(0) {
		t.Fatalf("after first placement local = %+v", l1)
	}
	// Throttled tick: nothing to emit, frame and progress must survive.
	if placed(tick(rf(130, 14000, inv, lit))) {
		t.Fatal("placed inside the 20-tick throttle")
	}
	if l, id := shelterFrameLocal(t, fsm); id != id1 || l.BuildIndex != 1 {
		t.Fatalf("throttled tick lost progress: id %d->%d local=%+v", id1, id, l)
	}
	if !placed(tick(rf(145, 14000, inv, lit))) {
		t.Fatal("second block not placed")
	}
	if l, _ := shelterFrameLocal(t, fsm); l.BuildIndex != 2 {
		t.Fatalf("local = %+v", l)
	}
	// Flee: emergency suspends the Shelter frame; its Local is untouched.
	creeper := withEntities(creeperAt(7, 3, 0.5, 3))
	tick(rf(150, 14000, inv, lit, withHP(6), creeper))
	if fsm.ActiveActivityKind() != activityKindEmergency {
		t.Fatalf("top = %q, want emergency", fsm.ActiveActivityKind())
	}
	if l, id := shelterFrameLocal(t, fsm); id != id1 || l.BuildIndex != 2 {
		t.Fatalf("suspended local = %+v id=%d", l, id)
	}
	if !placed(tick(rf(220, 14000, inv, lit))) {
		t.Fatal("shelter did not resume placing")
	}
	if l, id := shelterFrameLocal(t, fsm); id != id1 || l.BuildIndex != 3 {
		t.Fatalf("resumed local = %+v id=%d", l, id)
	}
	// Durable knowledge reaches the store only when the frame ends.
	if st.GetCabinBuilt() != 0 {
		t.Fatalf("store written while frame running: %d", st.GetCabinBuilt())
	}
	fsm.cancelActivitiesLocked(&FSMContext{State: st}, "test")
	if st.GetCabinBuilt() != 3 {
		t.Fatalf("banked cabin blocks = %d, want 3", st.GetCabinBuilt())
	}
}

func TestShelterFrame_CompleteIsDerivedAndBankedOnPop(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	st.SetCabinBuilt(blueprints.CabinTotalBlocks - 1)
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	lit := withBlocks(blk(1, 64, 1, "minecraft:torch"))
	inv := withInv(item(0, "minecraft:cobblestone", 64), item(1, "minecraft:torch", 4))
	fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: rf(120, 14000, inv, lit)})
	if l, _ := shelterFrameLocal(t, fsm); !l.complete() {
		t.Fatalf("local not complete: %+v", l)
	}
	if st.GetCabinBuilt() != blueprints.CabinTotalBlocks-1 {
		t.Fatalf("store changed before pop: %d", st.GetCabinBuilt())
	}
	fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: rf(145, 14000, inv, lit)})
	if st.GetCabinBuilt() != blueprints.CabinTotalBlocks {
		t.Fatalf("complete not banked on pop: %d", st.GetCabinBuilt())
	}
}
