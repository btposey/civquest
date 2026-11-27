package system1

// Diagnostics (cq-49c): what happened to an agent and to its work, kept for the logs only.
// Code anywhere in the package notes an event on the agent's state; the FSM hands the notes to
// its event sink each tick. Nothing here feeds back into decisions.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Diagnostic event categories.
const (
	EventDamage        = "DAMAGE"             // health lost, with its source and surroundings
	EventInventory     = "INVENTORY"          // carried or worn items changed
	EventThreats       = "THREATS"            // the set of visible hostiles changed
	EventReceipt       = "RECEIPT"            // a batch's progress report
	EventReceiptFailed = "RECEIPT_FAILED"     // a batch ended without completing
	EventTaskStart     = "TASK_START"         // a task was committed
	EventTaskEnd       = "TASK_END"           // a task completed or failed, and why
	EventTaskDropped   = "TASK_DROPPED"       // a task was abandoned before it ended
	EventDanger        = "DANGER_REGISTERED"  // a new danger spot
	EventUnreachable   = "TARGET_UNREACHABLE" // a place was blacklisted
	EventTreeFailed    = "TREE_FAILED"        // a trunk was put on cooldown
	EventStandGiveUp   = "STAND_GIVE_UP"      // a walk into a cell was abandoned
	EventWalkGivenUp   = "WALK_GIVEN_UP"      // the walking policy withheld a walk

	// maxDiagnostics bounds the notes kept while nobody drains them.
	maxDiagnostics = 256
)

type diagnostic struct {
	category, summary string
	details           map[string]any
}

// noteDiag records a diagnostic event. It takes its own lock, so it may be called with or
// without the state lock held.
func (s *AgentState) noteDiag(category, summary string, details map[string]any) {
	if s == nil {
		return
	}
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	if len(s.diags) < maxDiagnostics {
		s.diags = append(s.diags, diagnostic{category, summary, details})
	}
}

func (s *AgentState) drainDiags() []diagnostic {
	if s == nil {
		return nil
	}
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	out := s.diags
	s.diags = nil
	return out
}

func (fsm *AgentFSM) emitDiagnosticsLocked(ctx *FSMContext, tick int64) {
	if ctx == nil || ctx.State == nil {
		return
	}
	notes := ctx.State.drainDiags()
	if fsm.eventSink == nil {
		return
	}
	for _, n := range notes {
		fsm.eventSink(tick, n.category, n.summary, n.details)
	}
}

// taskDetails describes a task for TASK_ events.
func taskDetails(t *TaskSubroutine, frame *civquestv1.TelemetryFrame) map[string]any {
	d := map[string]any{
		"task": t.Spec.ID, "kind": t.Spec.Kind, "phase": string(t.Phase), "started_tick": t.StartedTick,
		"actions": t.ActionsEmitted, "failures": t.ConsecutiveFailures, "sub_target_id": t.ActiveTargetID,
	}
	if t.FailureReason != "" {
		d["reason"] = t.FailureReason
	}
	if last := t.Metadata["last_failure_reason"]; last != "" {
		d["last_receipt_failure"] = last
	}
	if b := t.ActiveSubTarget; b != nil {
		d["sub_target"] = []int32{b.X, b.Y, b.Z}
	}
	if p := frame.GetStatus().GetPosition(); p != nil {
		d["position"] = []float64{p.X, p.Y, p.Z}
	}
	return d
}

func (fsm *AgentFSM) emitReceiptLocked(tick int64, r *civquestv1.IntentReceipt) {
	if fsm.eventSink == nil || r == nil {
		return
	}
	category := EventReceipt
	switch r.Status {
	case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED,
		civquestv1.IntentReceipt_RECEIPT_STATUS_TIMEOUT:
		category = EventReceiptFailed
	}
	d := map[string]any{
		"batch": r.GetBatchId(), "frame": r.GetFrameId(), "status": r.GetStatus().String(),
		"active_index": r.GetActiveIndex(), "motor_depth": r.GetMotorStackDepth(),
	}
	if r.GetFailureReason() != "" {
		d["reason"] = r.GetFailureReason()
	}
	if f := r.GetFacts(); f != nil {
		d["stuck"], d["enclosed"], d["blocked_by_entity"] = f.Stuck, f.Enclosed, f.BlockedByEntity
		d["progress"] = f.Progress
	}
	fsm.eventSink(tick, category, fmt.Sprintf("batch %d %s", r.GetBatchId(), r.GetStatus()), d)
}

// observeFrameLocked reports damage taken, changes to what is carried and worn, and changes to
// the hostiles in view.
func (fsm *AgentFSM) observeFrameLocked(ctx *FSMContext, tick int64) {
	if fsm.eventSink == nil || ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return
	}
	frame, s := ctx.Frame, ctx.Frame.Status

	hurt := frame.LastDamageTaken
	lost := fsm.seenHealth > 0 && s.Health < fsm.seenHealth
	if hurt != nil || lost {
		d := bodyDetails(frame)
		d["health_before"] = fsm.seenHealth
		summary := "health lost, no damage report"
		if hurt != nil {
			d["source"], d["damage_type"] = hurt.SourceType.String(), hurt.DamageType
			d["amount"], d["attacker"], d["attacker_id"] = hurt.DamageAmount, hurt.AttackerName, hurt.AttackerEntityId
			for _, e := range frame.VisibleEntities {
				if e != nil && e.EntityId == hurt.AttackerEntityId && hurt.AttackerEntityId != 0 {
					d["attacker_type"], d["attacker_distance"] = e.EntityType, e.Distance
				}
			}
			summary = fmt.Sprintf("%.1f from %s %s", hurt.DamageAmount, hurt.DamageType, hurt.AttackerName)
		}
		fsm.eventSink(tick, EventDamage, summary, d)
	}
	fsm.seenHealth = s.Health

	carried, worn := itemList(s.Inventory), itemList(s.Equipment)
	if sig := strings.Join(carried, ",") + "|" + strings.Join(worn, ","); sig != fsm.seenItems {
		fsm.seenItems = sig
		fsm.eventSink(tick, EventInventory, strings.Join(carried, ", "), map[string]any{"carried": carried, "equipped": worn})
	}

	var ids []int32
	var hostiles []map[string]any
	for _, e := range frame.VisibleEntities {
		if e == nil || !e.IsHostile {
			continue
		}
		ids = append(ids, e.EntityId)
		h := map[string]any{"id": e.EntityId, "type": e.EntityType, "distance": e.Distance, "health": e.Health}
		if p := e.Position; p != nil {
			h["position"] = []float64{p.X, p.Y, p.Z}
		}
		hostiles = append(hostiles, h)
	}
	slices.Sort(ids)
	if sig := fmt.Sprint(ids); sig != fsm.seenThreats {
		fsm.seenThreats = sig
		fsm.eventSink(tick, EventThreats, fmt.Sprintf("%d hostiles in view", len(ids)), map[string]any{"hostiles": hostiles})
	}
}

// bodyDetails is where the agent is and what it is standing in.
func bodyDetails(frame *civquestv1.TelemetryFrame) map[string]any {
	s := frame.GetStatus()
	d := map[string]any{
		"health": s.GetHealth(), "feet_block": s.GetFeetBlock(), "head_block": s.GetHeadBlock(),
		"ground_block": s.GetGroundBlock(), "frozen_ticks": s.GetFrozenTicks(), "biome": frame.GetBiomeId(),
		"in_water": s.GetIsInWater(), "on_fire": s.GetIsOnFire(),
	}
	if p := s.GetPosition(); p != nil {
		d["position"] = []float64{p.X, p.Y, p.Z}
	}
	return d
}

// itemList renders stacks as sorted "item x count" totals.
func itemList(stacks []*civquestv1.ItemStackProto) []string {
	totals := map[string]int32{}
	for _, it := range stacks {
		if it != nil && it.Count > 0 {
			totals[strings.TrimPrefix(it.ItemId, "minecraft:")] += it.Count
		}
	}
	out := make([]string, 0, len(totals))
	for id, n := range totals {
		out = append(out, fmt.Sprintf("%s x%d", id, n))
	}
	sort.Strings(out)
	return out
}
