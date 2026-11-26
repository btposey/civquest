package system1

// Playtest observability (bead cq-gpa.5): the FSM reports frame lifecycle (push, pop,
// expire, replace), Recover decisions, stuck/enclosed receipts and periodic vitals through an
// injected EventSink. The sink is write-only; nothing here feeds back into decisions.

import (
	"fmt"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// EventSink receives one structured diagnostic event (category, summary, details).
type EventSink func(tick int64, category, summary string, details map[string]any)

// Playtest event categories emitted by the FSM.
const (
	EventFrame         = "FSM_FRAME"        // frame push/pop/expire/replace/cancel
	EventRecover       = "RECOVER_DECISION" // Recover frame lifecycle and its verdict
	EventMotorStuck    = "MOTOR_STUCK"      // receipt MotorFacts stuck/enclosed
	EventVitals        = "VITALS"           // periodic health/food/air/hazard snapshot
	vitalsIntervalTick = int64(20)
)

// SetEventSink installs (or, with nil, removes) the playtest event sink.
func (fsm *AgentFSM) SetEventSink(s EventSink) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	fsm.eventSink = s
	fsm.mu.Unlock()
}

func (fsm *AgentFSM) emitLifecycleLocked(tick int64, evs []htpda.FrameEvent) {
	if fsm.eventSink == nil {
		return
	}
	for _, e := range evs {
		cat := EventFrame
		if e.Kind == activityKindRecover || e.From == activityKindRecover {
			cat = EventRecover
		}
		d := map[string]any{
			"op":    e.Op,
			"frame": uint64(e.Frame),
			"kind":  string(e.Kind),
			"depth": e.Depth,
		}
		if e.Parent != 0 {
			d["parent"] = uint64(e.Parent)
		}
		if e.From != "" {
			d["from_kind"] = string(e.From)
		}
		if e.Result != nil {
			d["result"] = describeResult(e.Result)
		}
		if f := fsm.kernel.Frames(); len(f) > 0 {
			d["top"] = string(f[len(f)-1].Kind())
		}
		sum := fmt.Sprintf("%s %s", e.Op, e.Kind)
		if e.Op == htpda.LifeReplace {
			sum = fmt.Sprintf("replace %s -> %s", e.From, e.Kind)
		}
		fsm.eventSink(tick, cat, sum, d)
	}
}

// describeResult renders a pop Result compactly (Recover verdicts keep their cause).
func describeResult(r any) string {
	switch v := r.(type) {
	case recovered:
		return "recovered"
	case recoverBlocked:
		return "blocked:" + v.Cause.String()
	case htpda.Timeout:
		return "timeout"
	case htpda.Rejected:
		return "rejected:" + v.Reason
	case legacyResult:
		if v.Halt {
			return "halt"
		}
		return "fallthrough"
	}
	return fmt.Sprintf("%T", r)
}

func (fsm *AgentFSM) emitStuckReceiptLocked(tick int64, r *civquestv1.IntentReceipt, f htpda.MotorFacts) {
	if fsm.eventSink == nil || !(f.Stuck || f.Enclosed) {
		return
	}
	fsm.eventSink(tick, EventMotorStuck, fmt.Sprintf("receipt stuck=%v enclosed=%v", f.Stuck, f.Enclosed), map[string]any{
		"frame":             r.GetFrameId(),
		"batch":             r.GetBatchId(),
		"status":            r.GetStatus().String(),
		"stuck":             f.Stuck,
		"enclosed":          f.Enclosed,
		"blocked_by_entity": f.BlockedByEntity,
		"bearing_deg":       f.BlockedBearingDeg,
		"stuck_ticks":       f.StuckTicks,
		"progress":          f.Progress,
		"reason":            r.GetFailureReason(),
	})
}

// emitVitalsLocked emits a VITALS snapshot at most once per vitalsIntervalTick world ticks.
func (fsm *AgentFSM) emitVitalsLocked(ctx *FSMContext, tick int64) {
	if fsm.eventSink == nil || ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return
	}
	if fsm.vitalsEmitted && tick >= fsm.lastVitalsTick && tick-fsm.lastVitalsTick < vitalsIntervalTick {
		return
	}
	fsm.vitalsEmitted, fsm.lastVitalsTick = true, tick
	s := ctx.Frame.Status
	d := map[string]any{
		"health":     s.Health,
		"max_health": s.MaxHealth,
		"food":       s.FoodLevel,
		"air":        s.AirSupply,
		"in_water":   s.IsInWater,
		"submerged":  s.Submerged,
		"on_fire":    s.IsOnFire,
		"stage":      string(fsm.currentStage),
	}
	if p := s.Position; p != nil {
		d["position"] = []float64{p.X, p.Y, p.Z}
	}
	if top, ok := fsm.kernel.Top(); ok {
		d["top"] = string(top.Kind())
	}
	fsm.eventSink(tick, EventVitals, fmt.Sprintf("hp=%.1f food=%d air=%d", s.Health, s.FoodLevel, s.AirSupply), d)
}
