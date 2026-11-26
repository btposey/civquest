package system1

// Shared walk-frame stepper of Bootstrap and Exploration (ADR-036, bead cq-2sr.9/.10). Each
// frame's Local carries its walk cursor (walkLocal); nothing of it lives on AgentState. An
// expedition request from the waterfall is a step result (walkLocal.wantExpedition -> OpPush),
// not a mailbox on shared state.

import (
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

func runWalkFrame(a waterfallActivity, lw *fsmWorld, walk walkLocal, wrap func(walkLocal) any) htpda.Step {
	prog := walk
	prog.wantExpedition = false
	lw.skillCtx.Walk = &prog
	fi, done := a.tick(lw)
	lw.skillCtx.Walk = nil
	intents := unwrapFSMIntents(fi)
	want := prog.wantExpedition
	prog.wantExpedition = false
	if want {
		if args, ok := lw.expeditionStart(string(a.Kind())); ok {
			return htpda.Step{Op: htpda.OpPush, Child: activityKindExpedition, Args: args, Next: wrap(prog), Intents: intents}
		}
	}
	if done && len(intents) == 0 && lw.craftPending(&prog) {
		// Popping would discard the latch and a fresh frame would re-issue the in-flight craft
		// (cq-dcx.2); hold until the inventory reflects it or the latch times out.
		return htpda.Step{Op: htpda.OpStay, Next: wrap(prog)}
	}
	if done && len(intents) == 0 && !lw.holds() {
		// A preemptor over another frame (or a fall-through activity) with nothing to do yields.
		return popFrame(lw, a.Kind(), nil, !a.fallThrough(lw))
	}
	if done && len(intents) == 0 && a.fallThrough(lw) {
		return popFrame(lw, a.Kind(), nil, false)
	}
	// Hold: the cursor (explore target, throttles) lives in this frame, so waiting for a
	// cooldown or a pathing leg must not pop it.
	return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: wrap(prog)}
}

// craftPending reports whether the walk cursor still latches an in-flight craft.
func (w *fsmWorld) craftPending(walk *walkLocal) bool {
	if !walk.craftLatch.armed() || w.ctx == nil || w.ctx.State == nil || w.ctx.Frame == nil || w.ctx.Frame.Status == nil {
		return false
	}
	return walk.craftHeld(w.ctx.State.RecipeGraph(), walk.craftLatch.RecipeID,
		crafting.InventoryMapFromProtos(w.ctx.Frame.Status.Inventory), w.ctx.Frame.WorldTick)
}

// holds reports whether the stepping frame is the stack base: only a base frame holds (stays)
// when it has nothing to emit; a frame pushed over another one yields so the frame beneath it
// is not starved.
func (w *fsmWorld) holds() bool { return w.fsm.kernel.DepthInStep() <= 1 }
