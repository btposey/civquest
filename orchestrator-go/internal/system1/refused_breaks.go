package system1

// A break the mod will not do changes what its owner does (cq-6nz). In sandbox play test sb2 an
// agent in water asked for the same blocks 3900 times: the mod then broke nothing with fluid
// against it (now only lava, cq-vl2), and the failed receipt was read by nobody.
//
//   - Choosers pass over blocks the mod reports as refused (VisibleBlock.break_refused).
//   - Every frame that breaks blocks keeps a record in its own Local and counts the failure
//     reports it gets. After breakFailLimit the block is handed to Memory (ADR-036 §1), where
//     every chooser sees it, so the frame has to do something else. A frame that ends with
//     refusals still on record hands those over too: the next frame must not start from nought.

import (
	"slices"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

const (
	// breakFailLimit failed breaks of one block and the frame gives it up. More than one,
	// because a break under way can be cancelled by something that does not last.
	breakFailLimit = 3

	// EventBlockGivenUp: breaking a block failed and it is left alone for a while.
	EventBlockGivenUp = "BLOCK_GIVEN_UP"
)

type blockCell [3]int32

func cellAt(p *civquestv1.BlockPosProto) blockCell { return blockCell{p.GetX(), p.GetY(), p.GetZ()} }

// breakRecord is what one frame knows of its own breaking. It is a value: Failed is never
// changed in place.
type breakRecord struct {
	// Asked is the block last asked to be broken, if Any has been.
	Asked blockCell
	Any   bool
	// Latest: nothing else has been sent to the body since, so a completion report is about
	// that break.
	Latest bool
	Failed []failedBreak
}

type failedBreak struct {
	At blockCell
	N  int
}

// blockRefused is the Memory write for a block given up.
type blockRefused struct {
	At   blockCell
	Tick int64
}

// sent is the record after the frame sent intents.
func (r breakRecord) sent(intents []*civquestv1.AgentIntent) breakRecord {
	for _, in := range intents {
		if b := in.GetInteractBlock(); b.GetInteractionType() == civquestv1.InteractBlockIntent_HARVEST_BREAK && b.GetPos() != nil {
			r.Asked, r.Any, r.Latest = cellAt(b.Pos), true, true
		} else {
			r.Latest = false
		}
	}
	return r
}

// isBreakFailure reports a receipt reason that speaks of a block not broken (the mod's Mine
// motor). Other things fail under the same frame, a walk or a signpost, and say nothing of it.
func isBreakFailure(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "break")
}

// reported is the record after a receipt for this frame, and the Memory write if the block it
// was breaking has now failed too often.
func (r breakRecord) reported(rc *civquestv1.IntentReceipt, tick int64) (breakRecord, []htpda.MemoryWrite) {
	if !r.Any {
		return r, nil
	}
	i := slices.IndexFunc(r.Failed, func(f failedBreak) bool { return f.At == r.Asked })
	switch rc.GetStatus() {
	case civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED:
		if r.Latest && i >= 0 {
			r.Failed = slices.Delete(slices.Clone(r.Failed), i, i+1)
		}
	case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED:
		if !isBreakFailure(rc.GetFailureReason()) {
			return r, nil
		}
		r.Failed = slices.Clone(r.Failed)
		if i < 0 {
			i, r.Failed = len(r.Failed), append(r.Failed, failedBreak{At: r.Asked})
		}
		if r.Failed[i].N++; r.Failed[i].N >= breakFailLimit {
			r.Failed = slices.Delete(r.Failed, i, i+1)
			return r, []htpda.MemoryWrite{blockRefused{At: r.Asked, Tick: tick}}
		}
	}
	return r, nil
}

// closing is the Memory writes of a frame that ends with refusals on record.
func (r breakRecord) closing(tick int64) []htpda.MemoryWrite {
	var out []htpda.MemoryWrite
	for _, f := range r.Failed {
		out = append(out, blockRefused{At: f.At, Tick: tick})
	}
	return out
}

// breakRecordOf returns the record a Local holds and how to put one back. Every activity that
// can break a block is listed.
func breakRecordOf(local any) (breakRecord, func(breakRecord) any, bool) {
	switch l := local.(type) {
	case planLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case gatherLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case bedLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case forageLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case shelterLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case directiveLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case constructionLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case bootstrapLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case explorationLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case taskLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case woolLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case recoverLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	case emergencyLocal:
		return l.Breaks, func(r breakRecord) any { l.Breaks = r; return l }, true
	}
	return breakRecord{}, nil, false
}

// breakingStep runs a frame's step and keeps its record of breaking around it, so that no
// activity has to remember to.
func breakingStep(w htpda.World, local any, ev htpda.Event, step func(local any) htpda.Step) htpda.Step {
	var memory []htpda.MemoryWrite
	if _, ok := ev.(htpda.Receipt); ok {
		if lw := asFSMWorld(w); lw != nil && lw.receipt != nil {
			if r, put, ok := breakRecordOf(local); ok {
				r, memory = r.reported(lw.receipt, lw.Tick())
				local = put(r)
			}
		}
	}
	st := step(local)
	if before, _, ok := breakRecordOf(local); ok {
		switch st.Op {
		case htpda.OpPop, htpda.OpReplace:
			if len(before.Failed) > 0 {
				memory = append(memory, before.closing(w.Tick())...)
			}
		default:
			// The step may have built its Local afresh: the record carries over.
			if _, put, ok := breakRecordOf(st.Next); ok {
				st.Next = put(before.sent(st.Intents))
			}
		}
	}
	st.Memory = append(st.Memory, memory...)
	return st
}

// ---- what choosers ask ----

// giveUpBlock records a block that could not be broken, in the record of blocks that could not
// be used (it also holds workstations that could not be reached).
func (s *AgentState) giveUpBlock(at blockCell, tick int64) {
	s.MarkWorkstationUnreachable(at[0], at[1], at[2], tick)
	s.noteDiag(EventBlockGivenUp, "breaking it keeps failing", map[string]any{"block": at[:]})
}

// unbreakableLocked reports a block not to be chosen for breaking: the mod says it will refuse,
// or breaking it has failed lately. s may be nil; otherwise s.mu is held.
func (s *AgentState) unbreakableLocked(b *civquestv1.VisibleBlock, tick int64) bool {
	return b.GetBreakRefused() ||
		s != nil && s.IsWorkstationUnreachableLocked(b.GetPos().GetX(), b.GetPos().GetY(), b.GetPos().GetZ(), tick)
}

// unbreakable is unbreakableLocked taking the lock.
func (s *AgentState) unbreakable(b *civquestv1.VisibleBlock, tick int64) bool {
	if s == nil {
		return b.GetBreakRefused()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unbreakableLocked(b, tick)
}

// withinBreakReach is the mod's reach rule for starting a break (cq-gpa.9).
func withinBreakReach(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto) bool {
	return vertical.ReachableAABB(vertical.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}, vertical.DefaultEyeHeight, vertical.DefaultReach,
		vertical.Cell{X: int(b.X), Y: int(b.Y), Z: int(b.Z)})
}
