package system1

// Pending-craft latch (cq-dcx.2). A craft intent is issued, but the inventory telemetry lags
// the server by several ticks; the 20-tick craft throttle then expired and the same craft was
// re-issued 3-4 times. The latch lives in the issuing frame's walkLocal (ADR-036: frame-local,
// no AgentState field). While latched the same craft is not re-issued; it is released by an
// inventory delta over the recipe's inputs/outputs, by a failed receipt (frame-ID routed), or
// by the timeout.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// craftLatchTimeoutTicks is a design budget (tag: design-budget): ~10 s, long enough for any
// lagging inventory sync, short enough that a craft the server silently dropped is retried.
const craftLatchTimeoutTicks int64 = 200

type craftLatch struct {
	RecipeID    string
	Fingerprint string
	IssuedTick  int64
}

func (l craftLatch) armed() bool { return l.RecipeID != "" }

// craftFingerprint renders the counts of every item the recipe consumes or produces.
func craftFingerprint(r crafting.Recipe, inv map[string]int) string {
	ids := map[string]bool{r.ResultItemID: true}
	for _, s := range r.Slots {
		for _, id := range s.AcceptedItemIDs {
			ids[id] = true
		}
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%d;", k, inv[k])
	}
	return sb.String()
}

// craftHeld reports whether recipeID's craft is still pending. A stale latch (inventory delta,
// timeout, other recipe) is released.
func (w *walkLocal) craftHeld(graph *crafting.RecipeGraph, recipeID string, inv map[string]int, tick int64) bool {
	if w == nil || !w.craftLatch.armed() {
		return false
	}
	l := w.craftLatch
	if l.RecipeID != recipeID {
		return false // a different craft: the latch is replaced when that one is armed
	}
	if tick < l.IssuedTick || tick-l.IssuedTick >= craftLatchTimeoutTicks {
		w.craftLatch = craftLatch{}
		return false
	}
	if r, ok := graph.RecipeByID(recipeID); ok && craftFingerprint(r, inv) != l.Fingerprint {
		w.craftLatch = craftLatch{}
		return false
	}
	return true
}

// armCraft latches a freshly issued craft.
func (w *walkLocal) armCraft(graph *crafting.RecipeGraph, recipeID string, inv map[string]int, tick int64) {
	if w == nil {
		return
	}
	r, ok := graph.RecipeByID(recipeID)
	if !ok {
		return
	}
	w.craftLatch = craftLatch{RecipeID: recipeID, Fingerprint: craftFingerprint(r, inv), IssuedTick: tick}
}

// withCraftLatch attaches a popping frame's armed latch to its pop result, so the frame
// beneath keeps holding the in-flight craft (still frame-local: it moves, it is not shared).
func withCraftLatch(step htpda.Step, l craftLatch) htpda.Step {
	if r, ok := step.Result.(legacyResult); ok && l.armed() {
		r.Craft = l
		step.Result = r
	}
	return step
}

// adoptCraftLatch moves a popped child's craft latch into the resumed frame's cursor.
func adoptCraftLatch(local, result any) any {
	r, ok := result.(legacyResult)
	if !ok || !r.Craft.armed() {
		return local
	}
	return setCraftLatch(local, r.Craft)
}

// inheritCraftLatch copies the in-flight craft of the nearest frame below into a newly pushed
// frame, so a preemptor does not re-issue a craft a suspended frame is still waiting for.
func (w *fsmWorld) inheritCraftLatch(local any) any {
	if w == nil || w.fsm == nil || w.fsm.kernel == nil {
		return local
	}
	frames := w.activitiesInStep()
	for i := len(frames) - 1; i >= 0; i-- {
		if l := craftLatchOf(frames[i].Local); l.armed() {
			return setCraftLatch(local, l)
		}
	}
	return local
}

func craftLatchOf(local any) craftLatch {
	if tl, ok := local.(taskLocal); ok {
		return tl.Walk.craftLatch
	}
	w, _, _ := walkCarrier(local)
	return w.craftLatch
}

func setCraftLatch(local any, l craftLatch) any {
	if tl, ok := local.(taskLocal); ok {
		tl.Walk.craftLatch = l
		return tl
	}
	if w, rebuild, ok := walkCarrier(local); ok {
		w.craftLatch = l
		return rebuild(w)
	}
	return local
}

// releaseCraftOnReceipt: a failed/aborted/preempted receipt routed to the frame releases the
// latch so the craft can be retried (after the ordinary throttle). A completed receipt keeps
// it: the inventory delta (or timeout) is the truth that the result landed.
func releaseCraftOnReceipt(local any, rc *civquestv1.IntentReceipt) any {
	if rc == nil {
		return local
	}
	switch rc.GetStatus() {
	case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED,
		civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX:
	default:
		return local
	}
	if w, rebuild, ok := walkCarrier(local); ok && w.craftLatch.armed() {
		w.craftLatch = craftLatch{}
		return rebuild(w)
	}
	return local
}
