package system1

// Progress, as the watchdog counts it (ADR-041 section 4): movement, or something the game
// confirms. A confirmation is a change in what the pack holds, a completed receipt for an
// action, or a block break that has got further than before. Sending, a refusal and a failure
// are not progress: counting each send hid a 36 minute loop of breaks that failed (cq-6nz).
// An action already confirmed where the agent stands counts once: the mod completes a break of
// a block that is gone, and an agent could repeat that for ever.

import (
	"fmt"
	"math"
	"slices"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// askedKept bounds the actions remembered while waiting for the game's report.
const askedKept = 32

// unwatchedTicks without a frame and the watch starts afresh: what was not seen is not counted
// as standing still. A brain that stops being fed is the stall watchdog's business.
const unwatchedTicks = int64(200)

type progressWatch struct {
	at      [3]float64
	pack    string
	since   int64
	seen    int64
	started bool
	// waiting is the last action each frame sent that the game has not yet reported on.
	waiting map[uint64]askedAction
	// confirmed and broken are the actions already confirmed, and how far each break got,
	// since the agent last moved or its pack changed.
	confirmed map[string]bool
	broken    map[string]float64
}

type askedAction struct {
	what   string
	breaks bool
}

// asked notes an intent that does something where the agent stands. Walks, surveys and chat
// are not such: a walk counts when the agent has moved.
func (p *progressWatch) asked(frame uint64, in *civquestv1.AgentIntent) {
	switch in.GetAction().(type) {
	case *civquestv1.AgentIntent_Navigate, *civquestv1.AgentIntent_SurveyHorizon, *civquestv1.AgentIntent_Chat, nil:
		return
	}
	if p.waiting == nil || len(p.waiting) >= askedKept {
		p.waiting = map[uint64]askedAction{}
	}
	p.waiting[frame] = askedAction{what: describeIntent(in),
		breaks: in.GetInteractBlock().GetInteractionType() == civquestv1.InteractBlockIntent_HARVEST_BREAK}
}

// packOf is what the pack holds and how worn it is, wherever in the pack each thing lies.
func packOf(inv []*civquestv1.ItemStackProto) string {
	items := make([]string, 0, len(inv))
	for _, it := range inv {
		if it.GetCount() > 0 {
			items = append(items, fmt.Sprintf("%s*%d/%d", it.GetItemId(), it.GetCount(), it.GetDurability()))
		}
	}
	slices.Sort(items)
	return strings.Join(items, " ")
}

// restart begins the watch afresh from here.
func (p *progressWatch) restart(pos *civquestv1.Vec3, tick int64) {
	p.at, p.since = [3]float64{pos.X, pos.Y, pos.Z}, tick
}

// elsewhere forgets what was confirmed where the agent stood before.
func (p *progressWatch) elsewhere() { p.confirmed, p.broken = nil, nil }

// progressed ends the standstill and every run of refusals.
func (fsm *AgentFSM) progressed(w *fsmWorld) {
	if pos := w.pos(); pos != nil {
		fsm.watch.progress.restart(pos, w.tick)
	}
	clear(fsm.watch.refusals)
}

// observeProgress looks, before the stack is ticked, for movement and a changed pack.
func (fsm *AgentFSM) observeProgress(w *fsmWorld) {
	pos := w.pos()
	if pos == nil || w.ctx == nil || w.ctx.Frame == nil || w.ctx.Frame.Status == nil {
		return
	}
	p := &fsm.watch.progress
	pack := packOf(w.ctx.Frame.Status.Inventory)
	unwatched := !p.started || w.tick < p.seen || w.tick-p.seen > unwatchedTicks
	p.seen = w.tick
	if unwatched {
		p.restart(pos, w.tick)
		p.pack, p.started = pack, true
		p.elsewhere()
		return
	}
	if fsm.currentStage == StageEmergency {
		p.pack = pack // wear and eating in a fight are not the work getting on
		return
	}
	moved := math.Hypot(pos.X-p.at[0], pos.Z-p.at[2]) >= standstillMove || math.Abs(pos.Y-p.at[1]) >= standstillMove
	if moved || pack != p.pack {
		p.pack = pack
		p.elsewhere()
		fsm.progressed(w)
	}
}

// observeReceipt weighs a receipt not seen before (admitReceiptLocked).
func (fsm *AgentFSM) observeReceipt(w *fsmWorld, r *civquestv1.IntentReceipt) {
	p := &fsm.watch.progress
	a, ok := p.waiting[r.GetFrameId()]
	if !ok {
		return
	}
	switch r.GetStatus() {
	case civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED:
		delete(p.waiting, r.GetFrameId())
		if p.confirmed[a.what] || fsm.currentStage == StageEmergency {
			return
		}
		if p.confirmed == nil {
			p.confirmed = map[string]bool{}
		}
		p.confirmed[a.what] = true
		fsm.progressed(w)
	case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, civquestv1.IntentReceipt_RECEIPT_STATUS_TIMEOUT,
		civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED:
		delete(p.waiting, r.GetFrameId())
	}
}

// breakProgress is how far a break under way has got, as a fraction. A walk reports the same
// field (the distance still to go), but only along with being stuck, enclosed or still.
func breakProgress(r *civquestv1.IntentReceipt) float64 {
	f := r.GetFacts()
	if r.GetStatus() != civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING || f.GetStuck() || f.GetEnclosed() ||
		f.GetStuckTicks() != 0 || f.GetProgress() > 1 {
		return 0
	}
	return f.GetProgress()
}

// observeBreaking counts a break under way that has got further than it ever did here, so that
// slow mining is not taken for standing still. It reads every report, repeats included.
func (fsm *AgentFSM) observeBreaking(w *fsmWorld, r *civquestv1.IntentReceipt) {
	p := &fsm.watch.progress
	a, ok := p.waiting[r.GetFrameId()]
	done := breakProgress(r)
	if !ok || !a.breaks || done <= p.broken[a.what] || fsm.currentStage == StageEmergency {
		return
	}
	if p.broken == nil {
		p.broken = map[string]float64{}
	}
	p.broken[a.what] = done
	fsm.progressed(w)
}
