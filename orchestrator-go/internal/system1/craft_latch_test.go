package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestCraftLatch(t *testing.T) {
	g := newTestRecipeGraph()
	const id = "minecraft:oak_planks"
	inv := map[string]int{"minecraft:oak_log": 6}
	w := &walkLocal{}

	if w.craftHeld(g, id, inv, 100) {
		t.Fatal("unarmed latch must not hold")
	}
	w.armCraft(g, id, inv, 100)
	if !w.craftHeld(g, id, inv, 120) {
		t.Fatal("same inventory within timeout must hold")
	}
	if w.craftHeld(g, "minecraft:stick", inv, 120) {
		t.Fatal("a different recipe is not held")
	}
	if !w.craftHeld(g, id, inv, 120) {
		t.Fatal("a probe of another recipe must not release the latch")
	}
	// Unrelated item changes do not release; a recipe input/output change does.
	if !w.craftHeld(g, id, map[string]int{"minecraft:oak_log": 6, "minecraft:dirt": 9}, 130) {
		t.Fatal("unrelated inventory change must not release")
	}
	if w.craftHeld(g, id, map[string]int{"minecraft:oak_log": 5, "minecraft:oak_planks": 4}, 140) {
		t.Fatal("inventory delta must release")
	}
	if w.craftLatch.armed() {
		t.Fatal("release must clear the latch")
	}

	w.armCraft(g, id, inv, 100)
	if w.craftHeld(g, id, inv, 100+craftLatchTimeoutTicks) {
		t.Fatal("timeout must release")
	}
	w.armCraft(g, id, inv, 100)
	if w.craftHeld(g, id, inv, 50) {
		t.Fatal("clock going backwards must release")
	}
}

func TestReleaseCraftOnReceipt(t *testing.T) {
	g := newTestRecipeGraph()
	inv := map[string]int{"minecraft:oak_log": 6}
	armed := func() any {
		b := bootstrapLocal{}
		b.Walk.armCraft(g, "minecraft:oak_planks", inv, 10)
		return b
	}
	for _, st := range []struct {
		name    string
		rc      civquestv1.IntentReceipt_ReceiptStatus
		cleared bool
	}{{"failed", rcFailed, true}, {"aborted", rcAborted, true}, {"completed", rcCompleted, false}, {"executing", rcExecuting, false}} {
		got := releaseCraftOnReceipt(armed(), receipt(1, st.rc, "")).(bootstrapLocal)
		if got.Walk.craftLatch.armed() == st.cleared {
			t.Errorf("%s: latch armed=%t, want cleared=%t", st.name, got.Walk.craftLatch.armed(), st.cleared)
		}
	}
}
