package system1

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

// TaskPhase represents the execution phase of a generic TaskFSM / TaskSubroutine.
type TaskPhase string

const (
	// TaskPhaseApproach navigates toward the active sub-target until within interaction reach.
	TaskPhaseApproach TaskPhase = "APPROACH"
	// TaskPhasePrepare equips an appropriate tool or item before acting.
	TaskPhasePrepare TaskPhase = "PREPARE"
	// TaskPhaseAct emits the concrete action intent (e.g. HARVEST_BREAK, Attack, UseItem).
	TaskPhaseAct TaskPhase = "ACT"
	// TaskPhaseEvaluate checks sub-target completion and advances to the next sub-target if multi-target.
	TaskPhaseEvaluate TaskPhase = "EVALUATE"
	// TaskPhaseComplete marks the task as achieved and pops it from the PDA stack.
	TaskPhaseComplete TaskPhase = "COMPLETE"
	// TaskPhaseFailed marks the task as unachievable or timed out and pops it from the PDA stack.
	TaskPhaseFailed TaskPhase = "FAILED"
)

// TaskTargetKind classifies the spatial or entity target of a generic TaskDescriptor.
type TaskTargetKind string

const (
	TaskTargetBlock  TaskTargetKind = "BLOCK"
	TaskTargetColumn TaskTargetKind = "COLUMN"
	TaskTargetVolume TaskTargetKind = "VOLUME"
	TaskTargetEntity TaskTargetKind = "ENTITY"
)

// TaskTarget specifies the block, column, bounding volume, or entity target for a task.
type TaskTarget struct {
	Kind     TaskTargetKind
	BlockPos *civquestv1.BlockPosProto
	MinPos   *civquestv1.BlockPosProto
	MaxPos   *civquestv1.BlockPosProto
	EntityID int32
	Position *civquestv1.Vec3
	Label    string
}

// TaskDescriptor parameterizes a generic, preemptible TaskFSM / TaskSubroutine.
type TaskDescriptor struct {
	ID          string
	Kind        string
	Stage       Stage
	Routine     Routine
	Target      TaskTarget
	ReachRadius float64
	// EyeReach measures "within reach" of a block sub-target as vanilla does
	// (eye to nearest point of the block AABB) instead of feet to block centre.
	EyeReach           bool
	ArrivalTolerance   int32
	ActionCooldown     int64
	ProgressTimeout    int64
	MaxReceiptFailures int
	Preemptible        bool
	// Standalone tasks belong to the activity that launched them: no stage waterfall steps,
	// redirects or abandons them.
	Standalone bool

	// SelectSubTarget selects the current/next concrete sub-target and navigation approach point.
	SelectSubTarget func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) (approachPos *civquestv1.Vec3, blockPos *civquestv1.BlockPosProto, targetID string, ok bool)

	// NeedsSubTargetRefresh returns true when the current sub-target should be re-evaluated (e.g. block destroyed).
	NeedsSubTargetRefresh func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) bool

	// PrepareIntent optionally emits a preparation intent (e.g. equip tool) before ACT.
	PrepareIntent func(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) *civquestv1.AgentIntent

	// BuildActIntent constructs the action intent for the active sub-target.
	BuildActIntent func(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) *civquestv1.AgentIntent

	// HasProgressed returns true when forward progress (e.g. block broken, inventory gained) is detected.
	HasProgressed func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) bool

	// IsComplete returns true when the overall task goal is achieved.
	IsComplete func(state *AgentState, frame *civquestv1.TelemetryFrame) bool

	// IsUnachievable returns true when the task cannot be completed.
	IsUnachievable func(state *AgentState, frame *civquestv1.TelemetryFrame) bool

	// OnComplete is invoked when transitioning to TaskPhaseComplete.
	OnComplete func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine)

	// OnFail is invoked when transitioning to TaskPhaseFailed.
	OnFail func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine, reason string)

	// OnStandFailed is told that the cell a "stand:" sub-target names could not be walked into.
	OnStandFailed func()
}

// TaskStepResult represents the output of a single TaskSubroutine evaluation tick.
type TaskStepResult struct {
	Phase  TaskPhase
	Intent *civquestv1.AgentIntent
	Active bool
	Done   bool
	Reason string
}

// TaskSubroutine executes a parameterized TaskDescriptor through APPROACH -> PREPARE -> ACT -> EVALUATE -> COMPLETE/FAILED.
type TaskSubroutine struct {
	Spec                TaskDescriptor
	Phase               TaskPhase
	StartedTick         int64
	LastProgressTick    int64
	LastActionTick      int64
	LastNavigateTick    int64
	ActiveSubTarget     *civquestv1.BlockPosProto
	ActiveTargetID      string
	ActiveApproachPos   *civquestv1.Vec3
	ApproachBestDistSq  float64
	LastActionSubTarget *civquestv1.BlockPosProto
	LastActionTargetID  string
	BaselineCount       int32
	ActionsEmitted      int
	LastReceiptStatus   civquestv1.IntentReceipt_ReceiptStatus
	ConsecutiveFailures int
	FailureReason       string
	Metadata            map[string]string
}

// TaskFSM is an alias for TaskSubroutine representing the generic parameterized task state machine.
type TaskFSM = TaskSubroutine

// NewTaskSubroutine constructs a new TaskSubroutine initialized in TaskPhaseEvaluate.
func NewTaskSubroutine(spec TaskDescriptor, startTick int64) *TaskSubroutine {
	if spec.ReachRadius <= 0 {
		spec.ReachRadius = 4.5
	}
	if spec.ArrivalTolerance <= 0 {
		spec.ArrivalTolerance = 2
	}
	return &TaskSubroutine{
		Spec:             spec,
		Phase:            TaskPhaseEvaluate,
		StartedTick:      startTick,
		LastProgressTick: startTick,
		Metadata:         make(map[string]string),
	}
}

// NewTaskFSM constructs a new TaskFSM initialized from the given descriptor.
func NewTaskFSM(spec TaskDescriptor, startTick int64) *TaskFSM {
	return NewTaskSubroutine(spec, startTick)
}

// CompensateSuspension shifts progress/navigation timers forward by the number of ticks the
// owning frame spent suspended (preempted on the activity stack) so time spent under a
// preemptor never counts toward the progress timeout.
func (t *TaskSubroutine) CompensateSuspension(delta int64) {
	if t == nil || delta <= 0 {
		return
	}
	t.LastProgressTick += delta
	if t.LastNavigateTick > 0 {
		t.LastNavigateTick += delta
	}
}

// Clone returns a copy of the task whose mutable containers (Metadata) are not shared,
// so a frame step works on its own copy of the frame local.
func (t *TaskSubroutine) Clone() *TaskSubroutine {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Metadata != nil {
		cp.Metadata = make(map[string]string, len(t.Metadata))
		for k, v := range t.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

// Tick advances the TaskSubroutine by one sensory frame without a receipt. Receipts reach a
// task only through its activity frame (the kernel routes them by frame ID); a
// TelemetryFrame's receipt field is never read here.
func (t *TaskSubroutine) Tick(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame) TaskStepResult {
	return t.TickWithReceipt(agentID, state, frame, nil)
}

// TickWithReceipt advances the TaskSubroutine by one sensory frame while processing an
// IntentReceipt routed to this task's frame (already validated as fresh and addressed to
// it by the frame-ID seam) for immediate advancement or recovery.
func (t *TaskSubroutine) TickWithReceipt(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, receipt *civquestv1.IntentReceipt) TaskStepResult {
	if t == nil {
		return TaskStepResult{}
	}
	if t.Phase == TaskPhaseComplete || t.Phase == TaskPhaseFailed {
		return TaskStepResult{
			Phase:  t.Phase,
			Active: false,
			Done:   true,
			Reason: t.FailureReason,
		}
	}
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return TaskStepResult{Phase: t.Phase, Active: true}
	}

	if t.StartedTick == 0 {
		t.StartedTick = frame.WorldTick
	}
	if t.LastProgressTick == 0 {
		t.LastProgressTick = frame.WorldTick
	}

	// 0. Process the IntentReceipt routed to this task's frame. Staleness and duplicates are
	// rejected upstream (frame-ID routing + monotonic batch guard), not by per-task counters.
	if receipt != nil {
		t.LastReceiptStatus = receipt.Status

		switch receipt.Status {
		case civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED:
			prevPhase := t.Phase
			prevLastNavTick := t.LastNavigateTick
			if prevPhase == TaskPhaseApproach {
				reach := t.Spec.ReachRadius
				if reach <= 0 {
					reach = 4.5
				}
				tol := t.Spec.ArrivalTolerance
				if tol <= 0 {
					tol = 2
				}
				distSq := distanceSqToSubTarget(frame.Status.Position, t.ActiveApproachPos, t.ActiveSubTarget)
				maxArrival := reach + float64(tol) + 1.0
				withinArrival := (t.ActiveApproachPos == nil && t.ActiveSubTarget == nil) || distSq <= maxArrival*maxArrival
				movedCloser := t.ApproachBestDistSq > 0 && distSq <= t.ApproachBestDistSq-1.0
				if cell := standCell(t); cell != nil {
					withinArrival, movedCloser = standingIn(frame.Status.Position, cell), false
				}
				if prevLastNavTick > 0 && (withinArrival || movedCloser) {
					t.ConsecutiveFailures = 0
					t.LastProgressTick = frame.WorldTick
					t.LastActionTick = 0
					t.LastNavigateTick = 0
					if distSq < t.ApproachBestDistSq || t.ApproachBestDistSq == 0 {
						t.ApproachBestDistSq = distSq
					}
					if frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING && withinArrival {
						t.Phase = TaskPhasePrepare
					}
				}
			} else {
				t.ConsecutiveFailures = 0
				t.LastProgressTick = frame.WorldTick
				t.LastActionTick = 0
				t.LastNavigateTick = 0
			}

		case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED:
			t.LastActionTick = 0
			t.LastNavigateTick = 0
			if standCell(t) != nil {
				t.giveUpStand(state, frame, "walk failed: "+receipt.FailureReason)
				break
			}
			t.ConsecutiveFailures++
			if t.Metadata == nil {
				t.Metadata = make(map[string]string)
			}
			reason := strings.TrimSpace(receipt.FailureReason)
			if reason == "" {
				reason = "intent execution failed"
			}
			t.Metadata["last_failure_reason"] = reason
			t.Metadata["recovery_attempts"] = fmt.Sprintf("%d", t.ConsecutiveFailures)

			maxFailures := t.Spec.MaxReceiptFailures
			if maxFailures <= 0 {
				maxFailures = 2
			}
			if t.ConsecutiveFailures >= maxFailures {
				t.Phase = TaskPhaseFailed
				t.FailureReason = fmt.Sprintf("intent failed (%d attempts): %s", t.ConsecutiveFailures, reason)
				if t.Spec.OnFail != nil {
					t.Spec.OnFail(state, frame, t, t.FailureReason)
				}
				return TaskStepResult{
					Phase:  TaskPhaseFailed,
					Active: false,
					Done:   true,
					Reason: t.FailureReason,
				}
			}
			// Recover by re-evaluating / re-approaching sub-target immediately
			t.Phase = TaskPhaseEvaluate
			t.ActiveApproachPos = nil

		case civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX,
			civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED:
			t.LastProgressTick = frame.WorldTick
			t.LastActionTick = 0
			t.LastNavigateTick = 0
			t.Phase = TaskPhaseEvaluate
			t.ActiveApproachPos = nil
		}
	}

	// 1. Check completion predicate
	if t.Spec.IsComplete != nil && t.Spec.IsComplete(state, frame) {
		t.Phase = TaskPhaseComplete
		if t.Spec.OnComplete != nil {
			t.Spec.OnComplete(state, frame, t)
		}
		return TaskStepResult{
			Phase:  TaskPhaseComplete,
			Active: false,
			Done:   true,
			Reason: "task complete",
		}
	}

	// 2. Check unachievable / abort predicate
	if t.Spec.IsUnachievable != nil && t.Spec.IsUnachievable(state, frame) {
		t.Phase = TaskPhaseFailed
		t.FailureReason = "task unachievable"
		if t.Spec.OnFail != nil {
			t.Spec.OnFail(state, frame, t, t.FailureReason)
		}
		return TaskStepResult{
			Phase:  TaskPhaseFailed,
			Active: false,
			Done:   true,
			Reason: t.FailureReason,
		}
	}

	// 3. Check explicit progress and hard timeout
	if t.Spec.HasProgressed != nil && t.Spec.HasProgressed(state, frame, t) {
		t.LastProgressTick = frame.WorldTick
	}
	if t.Spec.ProgressTimeout > 0 && frame.WorldTick >= t.LastProgressTick &&
		(frame.WorldTick-t.LastProgressTick) >= t.Spec.ProgressTimeout {
		t.Phase = TaskPhaseFailed
		t.FailureReason = "progress timeout"
		if t.Spec.OnFail != nil {
			t.Spec.OnFail(state, frame, t, t.FailureReason)
		}
		return TaskStepResult{
			Phase:  TaskPhaseFailed,
			Active: false,
			Done:   true,
			Reason: t.FailureReason,
		}
	}

	if cell := standCell(t); cell != nil && !standingIn(frame.Status.Position, cell) &&
		frame.WorldTick-t.LastProgressTick >= standGiveUpTicks {
		t.giveUpStand(state, frame, "took too long")
	}

	// 4. EVALUATE: select or advance sub-target
	needsRefresh := t.Phase == "" || t.Phase == TaskPhaseEvaluate || t.ActiveApproachPos == nil
	if !needsRefresh && t.Spec.NeedsSubTargetRefresh != nil {
		needsRefresh = t.Spec.NeedsSubTargetRefresh(state, frame, t)
	}
	if needsRefresh {
		prevBlock := cloneBlockPos(t.ActiveSubTarget)
		prevTargetID := t.ActiveTargetID
		if t.Spec.SelectSubTarget != nil {
			approachPos, blockPos, targetID, ok := t.Spec.SelectSubTarget(state, frame, t)
			if !ok {
				if t.Spec.IsComplete != nil && t.Spec.IsComplete(state, frame) {
					t.Phase = TaskPhaseComplete
					if t.Spec.OnComplete != nil {
						t.Spec.OnComplete(state, frame, t)
					}
					return TaskStepResult{
						Phase:  TaskPhaseComplete,
						Active: false,
						Done:   true,
						Reason: "task complete",
					}
				}
				t.Phase = TaskPhaseFailed
				t.FailureReason = "no valid sub-target"
				if t.Spec.OnFail != nil {
					t.Spec.OnFail(state, frame, t, t.FailureReason)
				}
				return TaskStepResult{
					Phase:  TaskPhaseFailed,
					Active: false,
					Done:   true,
					Reason: t.FailureReason,
				}
			}

			// Advancing from a previous sub-target indicates concrete task progress
			if prevBlock != nil && !sameBlockPos(prevBlock, blockPos) {
				t.LastProgressTick = frame.WorldTick
				t.LastActionTick = 0
				t.LastActionSubTarget = nil
				t.LastActionTargetID = ""
				if t.LastReceiptStatus == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING {
					t.LastReceiptStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED
				}
			} else if prevTargetID != "" && prevTargetID != targetID {
				t.LastProgressTick = frame.WorldTick
				t.LastActionTick = 0
				t.LastActionSubTarget = nil
				t.LastActionTargetID = ""
				if t.LastReceiptStatus == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING {
					t.LastReceiptStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED
				}
			}

			t.ActiveApproachPos = approachPos
			t.ActiveSubTarget = cloneBlockPos(blockPos)
			t.ActiveTargetID = targetID
			t.ApproachBestDistSq = distanceSqToSubTarget(frame.Status.Position, approachPos, blockPos)
			if standCell(t) != nil && prevTargetID != targetID {
				t.LastProgressTick = frame.WorldTick // the walk's own time budget starts now
			}
		}
		t.Phase = TaskPhaseApproach
	}

	// 5. APPROACH: navigate until within interaction reach
	reach := t.Spec.ReachRadius
	if reach <= 0 {
		reach = 4.5
	}
	pos := frame.Status.Position
	distSq := distanceSqToSubTarget(pos, t.ActiveApproachPos, t.ActiveSubTarget)
	if t.ApproachBestDistSq <= 0 {
		t.ApproachBestDistSq = distSq
	}
	inReach := distSq <= reach*reach
	if !inReach && t.Spec.EyeReach && t.ActiveSubTarget != nil {
		inReach = vertical.ReachableAABB(vertical.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}, vertical.DefaultEyeHeight, reach,
			vertical.Cell{X: int(t.ActiveSubTarget.X), Y: int(t.ActiveSubTarget.Y), Z: int(t.ActiveSubTarget.Z)})
	}
	stand := standCell(t)
	if stand != nil {
		inReach = standingIn(pos, stand)
	}
	if t.Phase != TaskPhasePrepare && t.Phase != TaskPhaseAct && !inReach {
		t.Phase = TaskPhaseApproach
		navState := frame.Status.NavigationState
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return TaskStepResult{Phase: TaskPhaseApproach, Active: true}
		}
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK && stand == nil &&
			t.LastNavigateTick > 0 && frame.WorldTick >= t.LastNavigateTick {
			if frame.WorldTick-t.LastNavigateTick < 80 {
				return TaskStepResult{Phase: TaskPhaseApproach, Active: true}
			}
			t.LastNavigateTick = 0
			t.ConsecutiveFailures++
			maxFailures := t.Spec.MaxReceiptFailures
			if maxFailures <= 0 {
				maxFailures = 2
			}
			if t.ConsecutiveFailures >= maxFailures {
				t.Phase = TaskPhaseFailed
				t.FailureReason = fmt.Sprintf("navigation stuck (%d attempts)", t.ConsecutiveFailures)
				if t.Spec.OnFail != nil {
					t.Spec.OnFail(state, frame, t, t.FailureReason)
				}
				return TaskStepResult{
					Phase:  TaskPhaseFailed,
					Active: false,
					Done:   true,
					Reason: t.FailureReason,
				}
			}
		}
		if t.LastNavigateTick > 0 && frame.WorldTick >= t.LastNavigateTick && frame.WorldTick-t.LastNavigateTick < 40 {
			return TaskStepResult{Phase: TaskPhaseApproach, Active: true}
		}
		if t.ActiveApproachPos != nil {
			t.LastNavigateTick = frame.WorldTick
			if distSq < t.ApproachBestDistSq {
				t.ApproachBestDistSq = distSq
			}
			tolerance := t.Spec.ArrivalTolerance
			if tolerance <= 0 {
				tolerance = 2
			}
			if stand != nil {
				tolerance = 1
			}
			navIntent := &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: t.ActiveApproachPos.X,
							Y: t.ActiveApproachPos.Y,
							Z: t.ActiveApproachPos.Z,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: tolerance,
						IntoCell:         stand != nil,
					},
				},
			}
			return TaskStepResult{Phase: TaskPhaseApproach, Intent: navIntent, Active: true}
		}
		return TaskStepResult{Phase: TaskPhaseApproach, Active: true}
	}

	// Within reach -> PREPARE
	if t.Phase == TaskPhaseApproach {
		t.Phase = TaskPhasePrepare
		if t.LastActionTick == 0 && t.LastReceiptStatus == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING {
			// Clear an approach navigation EXECUTING receipt so the initial ACT intent can fire immediately.
			t.LastReceiptStatus = civquestv1.IntentReceipt_RECEIPT_STATUS_UNSPECIFIED
		}
	}

	actExecutingForCurrentTarget := t.LastReceiptStatus == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING &&
		(t.LastActionSubTarget == nil || (sameBlockPos(t.LastActionSubTarget, t.ActiveSubTarget) && t.LastActionTargetID == t.ActiveTargetID))

	// 6. PREPARE: equip tool/item if required
	if t.Phase == TaskPhasePrepare {
		if !actExecutingForCurrentTarget && t.Spec.PrepareIntent != nil {
			if prepIntent := t.Spec.PrepareIntent(agentID, state, frame, t); prepIntent != nil {
				t.Phase = TaskPhaseAct
				return TaskStepResult{Phase: TaskPhasePrepare, Intent: prepIntent, Active: true}
			}
		}
		t.Phase = TaskPhaseAct
	}

	// 7. ACT: wait if active intent is still executing on periphery, otherwise emit concrete action intent
	if actExecutingForCurrentTarget {
		t.Phase = TaskPhaseAct
		return TaskStepResult{Phase: TaskPhaseAct, Active: true}
	}
	if t.Spec.ActionCooldown > 0 && t.LastActionTick > 0 &&
		frame.WorldTick >= t.LastActionTick && (frame.WorldTick-t.LastActionTick) < t.Spec.ActionCooldown {
		return TaskStepResult{Phase: TaskPhaseAct, Active: true}
	}

	if t.Spec.BuildActIntent != nil {
		actIntent := t.Spec.BuildActIntent(agentID, state, frame, t)
		if actIntent != nil {
			t.LastActionTick = frame.WorldTick
			t.LastActionSubTarget = cloneBlockPos(t.ActiveSubTarget)
			t.LastActionTargetID = t.ActiveTargetID
			t.ActionsEmitted++
			t.Phase = TaskPhaseEvaluate
			return TaskStepResult{Phase: TaskPhaseAct, Intent: actIntent, Active: true}
		}
	}

	return TaskStepResult{Phase: TaskPhaseAct, Active: true}
}

func cloneBlockPos(pos *civquestv1.BlockPosProto) *civquestv1.BlockPosProto {
	if pos == nil {
		return nil
	}
	return &civquestv1.BlockPosProto{X: pos.X, Y: pos.Y, Z: pos.Z}
}

func sameBlockPos(a, b *civquestv1.BlockPosProto) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.X == b.X && a.Y == b.Y && a.Z == b.Z
}

func distanceSqToSubTarget(pos *civquestv1.Vec3, approachPos *civquestv1.Vec3, blockPos *civquestv1.BlockPosProto) float64 {
	if pos == nil {
		return math.MaxFloat64
	}
	if blockPos != nil {
		dx := float64(blockPos.X) + 0.5 - pos.X
		dy := float64(blockPos.Y) + 0.5 - pos.Y
		dz := float64(blockPos.Z) + 0.5 - pos.Z
		return dx*dx + dy*dy + dz*dz
	}
	if approachPos != nil {
		dx := approachPos.X - pos.X
		dy := approachPos.Y - pos.Y
		dz := approachPos.Z - pos.Z
		return dx*dx + dy*dy + dz*dz
	}
	return math.MaxFloat64
}

func isLogBlockID(blockID string) bool {
	id := strings.ToLower(strings.TrimSpace(blockID))
	return strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem")
}

func isDroppedItemEntity(e *civquestv1.VisibleEntity) bool {
	if e == nil {
		return false
	}
	et := strings.ToLower(strings.TrimSpace(e.EntityType))
	return et == "minecraft:item" || strings.HasSuffix(et, ":item") || et == "item"
}

func isWoodDropEntity(e *civquestv1.VisibleEntity) bool {
	if !isDroppedItemEntity(e) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(e.DisplayName))
	if name == "" {
		return true
	}
	return strings.Contains(name, "log") ||
		strings.Contains(name, "wood") ||
		strings.Contains(name, "stem") ||
		strings.Contains(name, "plank") ||
		strings.Contains(name, "stick") ||
		strings.Contains(name, "sapling")
}

func woodFamilyOf(blockID string) string {
	id := strings.ToLower(strings.TrimSpace(blockID))
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		id = id[idx+1:]
	}
	for _, suffix := range []string{"_log", "_wood", "_stem"} {
		if strings.HasSuffix(id, suffix) {
			return strings.TrimSuffix(id, suffix)
		}
	}
	return id
}

func isSaplingItemID(rawID string) bool {
	id := strings.ToLower(strings.TrimSpace(rawID))
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		id = id[idx+1:]
	}
	return strings.HasSuffix(id, "_sapling") || id == "mangrove_propagule"
}

func findSaplingInInventory(frame *civquestv1.TelemetryFrame, state *AgentState) string {
	if frame != nil && frame.Status != nil && len(frame.Status.Inventory) > 0 {
		for _, it := range frame.Status.Inventory {
			if it == nil || it.Count <= 0 || it.ItemId == "" {
				continue
			}
			if isSaplingItemID(it.ItemId) {
				return it.ItemId
			}
		}
	}
	return ""
}

func isSoilBlockID(rawID string) bool {
	id := strings.ToLower(strings.TrimSpace(rawID))
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		id = id[idx+1:]
	}
	switch id {
	case "dirt", "grass_block", "coarse_dirt", "podzol", "farmland", "rooted_dirt", "mud", "mycelium":
		return true
	default:
		return false
	}
}

func isAirOrPassableAt(frame *civquestv1.TelemetryFrame, x, y, z int32) bool {
	if frame == nil {
		return true
	}
	b := FindVisibleBlockAt(frame, x, y, z)
	if b == nil {
		return true
	}
	return isPassableBlockID(b.BlockId)
}

func canPlantSaplingAt(frame *civquestv1.TelemetryFrame, tx, ty, tz int32) bool {
	soilY := ty - 1
	if frame != nil {
		soilBlock := FindVisibleBlockAt(frame, tx, soilY, tz)
		if soilBlock != nil && !isSoilBlockID(soilBlock.BlockId) {
			return false
		}
	}
	return isAirOrPassableAt(frame, tx, ty, tz)
}

// scanTreeClusterLogs detects contiguous trunk and branch logs within horizontal distance |dx| <= 2, |dz| <= 2
// around the anchor (tx, tz), across elevation [anchorY-1 ... anchorY+16] per ADR-025.
func scanTreeClusterLogs(
	frame *civquestv1.TelemetryFrame,
	tx, anchorY, tz int32,
	reachRadius float64,
	expectedWood string,
) (totalInCluster int, reachableLogs []*civquestv1.VisibleBlock, nearestLog *civquestv1.VisibleBlock, isTall bool, apexLog *civquestv1.VisibleBlock) {
	if frame == nil {
		return 0, nil, nil, false, nil
	}
	var pos *civquestv1.Vec3
	if frame.Status != nil && frame.Status.Position != nil {
		pos = frame.Status.Position
	} else {
		pos = &civquestv1.Vec3{X: float64(tx) + 0.5, Y: float64(anchorY), Z: float64(tz) + 0.5}
	}

	bestDistSq := math.MaxFloat64
	minY := int32(math.MaxInt32)
	maxY := int32(math.MinInt32)

	anchorWood := expectedWood
	if anchorWood == "" {
		for _, b := range frame.InterestingBlocks {
			if b != nil && b.Pos != nil && isLogBlockID(b.BlockId) && b.Pos.X == tx && b.Pos.Z == tz {
				anchorWood = woodFamilyOf(b.BlockId)
				break
			}
		}
	}

	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !isLogBlockID(b.BlockId) {
			continue
		}
		if anchorWood != "" && woodFamilyOf(b.BlockId) != anchorWood {
			continue
		}
		dx := b.Pos.X - tx
		dz := b.Pos.Z - tz
		if dx < -2 || dx > 2 || dz < -2 || dz > 2 {
			continue
		}
		if b.Pos.Y < anchorY-1 || b.Pos.Y > anchorY+16 {
			continue
		}
		totalInCluster++
		if b.Pos.Y < minY {
			minY = b.Pos.Y
		}
		if b.Pos.Y > maxY {
			maxY = b.Pos.Y
		}

		bx := float64(b.Pos.X) + 0.5 - pos.X
		by := float64(b.Pos.Y) + 0.5 - pos.Y
		bz := float64(b.Pos.Z) + 0.5 - pos.Z
		dSq := bx*bx + by*by + bz*bz
		// Vanilla reach: eye to nearest point of the block AABB (cq-gpa.9), so a
		// trunk worked from directly underneath is harvestable to ~y+6.
		if vertical.ReachableAABB(vertical.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}, vertical.DefaultEyeHeight, reachRadius,
			vertical.Cell{X: int(b.Pos.X), Y: int(b.Pos.Y), Z: int(b.Pos.Z)}) {
			reachableLogs = append(reachableLogs, b)
		}
		if dSq < bestDistSq {
			bestDistSq = dSq
			nearestLog = b
		}
		if apexLog == nil || b.Pos.Y > apexLog.Pos.Y {
			apexLog = b
		} else if b.Pos.Y == apexLog.Pos.Y && dSq < distanceSqToSubTarget(pos, nil, apexLog.Pos) {
			apexLog = b
		}
	}

	if totalInCluster > 0 {
		baseY := anchorY
		if minY < baseY {
			baseY = minY
		}
		height := maxY - baseY + 1
		if height >= 5 || (maxY-anchorY) >= 4 {
			isTall = true
		}
	}

	return totalInCluster, reachableLogs, nearestLog, isTall, apexLog
}

// NewHarvestTreeTask creates a generic TaskSubroutine locked onto a specific tree cluster around (tx, tz).
func NewHarvestTreeTask(tx, ty, tz int32, stage Stage, startTick int64) *TaskSubroutine {
	if stage == "" {
		stage = StageBootstrap
	}
	const treeReach = 4.5
	const harvestTimeoutTicks = int64(150)
	var planted, standGivenUp bool
	var woodFamily string

	spec := TaskDescriptor{
		ID:               fmt.Sprintf("harvest_tree:%d,%d", tx, tz),
		Kind:             "harvest_tree",
		Stage:            stage,
		Routine:          RoutineHarvestTree,
		ReachRadius:      treeReach,
		EyeReach:         true,
		ArrivalTolerance: 2,
		ActionCooldown:   20,
		ProgressTimeout:  harvestTimeoutTicks,
		Preemptible:      true,
		Target: TaskTarget{
			Kind:     TaskTargetVolume,
			BlockPos: &civquestv1.BlockPosProto{X: tx, Y: ty, Z: tz},
			MinPos:   &civquestv1.BlockPosProto{X: tx - 2, Y: ty - 1, Z: tz - 2},
			MaxPos:   &civquestv1.BlockPosProto{X: tx + 2, Y: ty + 16, Z: tz + 2},
			Position: &civquestv1.Vec3{X: float64(tx) + 0.5, Y: float64(ty), Z: float64(tz) + 0.5},
			Label:    "tree_cluster",
		},
		IsComplete: func(state *AgentState, frame *civquestv1.TelemetryFrame) bool {
			if frame == nil || frame.Status == nil || frame.Status.Position == nil {
				return false
			}
			pos := frame.Status.Position
			totalInCluster, reachableLogs, _, _, _ := scanTreeClusterLogs(frame, tx, ty, tz, treeReach, woodFamily)

			// Tree felling only completes when zero reachable logs remain within the tree cluster canopy cylinder.
			if len(reachableLogs) > 0 {
				return false
			}

			// If logs remain in the cluster but none are currently reachable, check if agent is within reach of the tree.
			if totalInCluster > 0 {
				horizDist := math.Hypot(float64(tx)+0.5-pos.X, float64(tz)+0.5-pos.Z)
				if horizDist > 2.5 {
					return false
				}
			}

			if !standGivenUp {
				if _, ok := walkUnderTrunk(frame, tx, ty, tz, treeReach, woodFamily); ok {
					return false
				}
			}

			// All reachable logs have been cleared. Check if autonomous sapling replanting is pending:
			if !planted {
				saplingID := findSaplingInInventory(frame, state)
				if saplingID != "" && canPlantSaplingAt(frame, tx, ty, tz) {
					return false
				}
			}

			return true
		},
		NeedsSubTargetRefresh: func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) bool {
			if current == nil || current.ActiveSubTarget == nil {
				return true
			}
			// Sapling plant target does not need refresh while active
			if strings.HasPrefix(current.ActiveTargetID, "plant:") || isSaplingItemID(current.ActiveTargetID) {
				return false
			}
			if cell := standCell(current); cell != nil {
				return standingIn(frame.GetStatus().GetPosition(), cell)
			}
			// Refresh if active block disappeared (broken!) or if a reachable block exists while active is out of reach
			curBlock := FindVisibleBlockAt(frame, current.ActiveSubTarget.X, current.ActiveSubTarget.Y, current.ActiveSubTarget.Z)
			if curBlock == nil || !isLogBlockID(curBlock.BlockId) {
				return true
			}
			if frame != nil && frame.Status != nil && frame.Status.Position != nil {
				p := frame.Status.Position
				curReach := vertical.ReachableAABB(vertical.Vec3{X: p.X, Y: p.Y, Z: p.Z}, vertical.DefaultEyeHeight, treeReach,
					vertical.Cell{X: int(current.ActiveSubTarget.X), Y: int(current.ActiveSubTarget.Y), Z: int(current.ActiveSubTarget.Z)})
				if !curReach {
					_, reachableLogs, _, _, _ := scanTreeClusterLogs(frame, tx, ty, tz, treeReach, woodFamily)
					if len(reachableLogs) > 0 {
						return true
					}
				}
			}
			return false
		},
		SelectSubTarget: func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) (*civquestv1.Vec3, *civquestv1.BlockPosProto, string, bool) {
			if woodFamily == "" && current != nil && current.Metadata["wood_family"] != "" {
				woodFamily = current.Metadata["wood_family"]
			}
			totalInCluster, reachableLogs, nearestLog, isTall, apexLog := scanTreeClusterLogs(frame, tx, ty, tz, treeReach, woodFamily)
			if woodFamily == "" && frame != nil {
				for _, b := range frame.InterestingBlocks {
					if b != nil && b.Pos != nil && isLogBlockID(b.BlockId) && b.Pos.X == tx && b.Pos.Z == tz {
						woodFamily = woodFamilyOf(b.BlockId)
						if current != nil {
							current.Metadata["wood_family"] = woodFamily
						}
						break
					}
				}
			}
			// Bottom-up from the ground (felling.go); top-down felling is only for an agent
			// already up on a ladder or scaffold (cq-3a0). Logs beyond reach from underneath
			// are left.
			pos := frame.GetStatus().GetPosition()
			elevated := pos != nil && pos.Y > float64(ty)+fellingElevated
			isTallMode := isTall && elevated
			if current != nil && elevated {
				if isTall {
					current.Metadata["is_tall"] = "true"
				} else if current.Metadata["is_tall"] == "true" {
					isTallMode = true
				}
			}
			nearTrunk := pos != nil && math.Hypot(float64(tx)+0.5-pos.X, float64(tz)+0.5-pos.Z) <= 2.5

			var chosen, low *civquestv1.VisibleBlock
			if !elevated {
				low = bottomLog(frame, tx, ty, tz, woodFamily)
				// A bottom log out of reach from beside the trunk is not chased.
				if low != nil && nearTrunk && !slices.Contains(reachableLogs, low) {
					low = nil
				}
				if low == nil && !standGivenUp {
					if cell, ok := walkUnderTrunk(frame, tx, ty, tz, treeReach, woodFamily); ok {
						approach := &civquestv1.Vec3{X: float64(cell.X) + 0.5, Y: float64(cell.Y), Z: float64(cell.Z) + 0.5}
						if current != nil {
							current.Metadata[fellingUnderKey] = "true"
						}
						return approach, cell, underTrunkTarget, true
					}
				}
			}
			if low != nil {
				chosen = low
			} else if len(reachableLogs) > 0 {
				if isTallMode {
					// Tall tree: top-down felling strategy.
					// Select highest reachable log/branch first (descending from apex downward).
					for _, candidate := range reachableLogs {
						if chosen == nil || candidate.Pos.Y > chosen.Pos.Y {
							chosen = candidate
						} else if candidate.Pos.Y == chosen.Pos.Y {
							candDist := distanceSqToSubTarget(frame.Status.Position, nil, candidate.Pos)
							chosenDist := distanceSqToSubTarget(frame.Status.Position, nil, chosen.Pos)
							if candDist < chosenDist {
								chosen = candidate
							}
						}
					}
				} else {
					// Standard tree: bottom-up felling strategy.
					// Select lowest reachable log first.
					for _, candidate := range reachableLogs {
						if chosen == nil || candidate.Pos.Y < chosen.Pos.Y {
							chosen = candidate
						} else if candidate.Pos.Y == chosen.Pos.Y {
							candDist := distanceSqToSubTarget(frame.Status.Position, nil, candidate.Pos)
							chosenDist := distanceSqToSubTarget(frame.Status.Position, nil, chosen.Pos)
							if candDist < chosenDist {
								chosen = candidate
							}
						}
					}
				}
			} else if totalInCluster > 0 {
				switch {
				case !elevated && nearTrunk:
					// Already standing under the trunk: the rest is out of reach; leave it (ladders: cq-3a0).
				case isTallMode && apexLog != nil:
					chosen = apexLog
				default:
					chosen = nearestLog
				}
			}

			if chosen != nil && chosen.Pos != nil {
				approachY := float64(chosen.Pos.Y)
				if !elevated && chosen.Pos.Y > ty {
					// Stand at the trunk base so the eye can reach up the trunk.
					approachY = float64(ty)
				}
				approach := &civquestv1.Vec3{
					X: float64(chosen.Pos.X) + 0.5,
					Y: approachY,
					Z: float64(chosen.Pos.Z) + 0.5,
				}
				return approach, chosen.Pos, chosen.BlockId, true
			}

			// All reachable trunk and branch logs felled (zero reachable logs left).
			// Autonomous Sapling Replanting (ADR-025 Silviculture):
			if !planted && (current == nil || current.Metadata["planted"] != "true") {
				saplingID := findSaplingInInventory(frame, state)
				if saplingID != "" && canPlantSaplingAt(frame, tx, ty, tz) {
					approach := &civquestv1.Vec3{
						X: float64(tx) + 0.5,
						Y: float64(ty),
						Z: float64(tz) + 0.5,
					}
					pos := &civquestv1.BlockPosProto{
						X: tx,
						Y: ty,
						Z: tz,
					}
					return approach, pos, "plant:" + saplingID, true
				}
			}

			return nil, nil, "", false
		},
		BuildActIntent: func(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) *civquestv1.AgentIntent {
			if current == nil || current.ActiveSubTarget == nil || standCell(current) != nil {
				return nil
			}
			if state != nil && frame != nil {
				state.mem.lastHarvestTick = frame.WorldTick
			}

			// Check for autonomous sapling replanting action
			if strings.HasPrefix(current.ActiveTargetID, "plant:") || isSaplingItemID(current.ActiveTargetID) {
				saplingID := strings.TrimPrefix(current.ActiveTargetID, "plant:")
				planted = true
				if current.Metadata != nil {
					current.Metadata["planted"] = "true"
				}
				if state != nil {
					if si := state.SpatialIndexLocked(); si != nil {
						agentY := float64(ty)
						if frame != nil && frame.Status != nil && frame.Status.Position != nil {
							agentY = frame.Status.Position.Y
						}
						poi := db.AgentPOI{
							POIID:            FormatPOIID(POICategoryCrop, tx, ty, tz),
							Category:         POICategoryCrop,
							BlockID:          saplingID,
							X:                float64(tx) + 0.5,
							Y:                float64(ty),
							Z:                float64(tz) + 0.5,
							AccessX:          float64(tx) + 0.5,
							AccessY:          float64(ty),
							AccessZ:          float64(tz) + 0.5,
							ElevationLayer:   ClassifyElevationLayer(float64(ty), agentY),
							SafetyScore:      1.0,
							DiscoveredTick:   frame.WorldTick,
							LastVerifiedTick: frame.WorldTick,
							Reliability:      1.0,
							IsActive:         true,
						}
						si.UpsertPOI(poi)
					}
				}
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_InteractBlock{
						InteractBlock: &civquestv1.InteractBlockIntent{
							Pos:             cloneBlockPos(current.ActiveSubTarget),
							InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
							BlockOrItemId:   saplingID,
						},
					},
				}
			}

			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             cloneBlockPos(current.ActiveSubTarget),
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   current.ActiveTargetID,
					},
				},
			}
		},
		OnStandFailed: func() { standGivenUp = true },
		IsUnachievable: func(state *AgentState, frame *civquestv1.TelemetryFrame) bool {
			if state == nil || frame == nil {
				return false
			}
			return state.IsTargetUnreachableLocked(float64(tx)+0.5, float64(ty), float64(tz)+0.5, frame.WorldTick)
		},
		OnFail: func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine, reason string) {
			if state != nil {
				if frame != nil {
					state.MarkTreeFailedLocked(tx, tz, frame.WorldTick)
					state.MarkTargetUnreachableLocked(float64(tx)+0.5, float64(ty), float64(tz)+0.5, frame.WorldTick, DefaultUnreachableTargetTTL)
				}
			}
		},
	}

	return NewTaskSubroutine(spec, startTick)
}

func findNearestDroppedWoodItem(frame *civquestv1.TelemetryFrame, anchorX, anchorZ int32) *civquestv1.VisibleEntity {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return nil
	}
	pos := frame.Status.Position
	var best *civquestv1.VisibleEntity
	bestDistSq := math.MaxFloat64

	for _, e := range frame.VisibleEntities {
		if !isWoodDropEntity(e) || e.Position == nil {
			continue
		}
		if math.Abs(e.Position.Y-pos.Y) > 3.0 {
			continue
		}
		anchorHoriz := math.Hypot(e.Position.X-(float64(anchorX)+0.5), e.Position.Z-(float64(anchorZ)+0.5))
		if anchorHoriz > 6.0 {
			continue
		}
		dx := e.Position.X - pos.X
		dy := e.Position.Y - pos.Y
		dz := e.Position.Z - pos.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq < bestDistSq {
			bestDistSq = dSq
			best = e
		}
	}
	return best
}

// NewCollectItemsTask creates a generic TaskSubroutine that collects dropped wood items near a felled tree trunk.
func NewCollectItemsTask(anchorX, anchorY, anchorZ int32, stage Stage, startTick int64) *TaskSubroutine {
	if stage == "" {
		stage = StageBootstrap
	}
	const vacuumApproachReach = 1.5

	spec := TaskDescriptor{
		ID:               fmt.Sprintf("collect_items:%d,%d", anchorX, anchorZ),
		Kind:             "collect_items",
		Stage:            stage,
		Routine:          RoutineCollectItems,
		ReachRadius:      vacuumApproachReach,
		ArrivalTolerance: 1,
		ActionCooldown:   20,
		ProgressTimeout:  60,
		Preemptible:      true,
		Target: TaskTarget{
			Kind:     TaskTargetEntity,
			BlockPos: &civquestv1.BlockPosProto{X: anchorX, Y: anchorY, Z: anchorZ},
			Position: &civquestv1.Vec3{X: float64(anchorX) + 0.5, Y: float64(anchorY), Z: float64(anchorZ) + 0.5},
			Label:    "dropped_wood",
		},
		IsComplete: func(state *AgentState, frame *civquestv1.TelemetryFrame) bool {
			return findNearestDroppedWoodItem(frame, anchorX, anchorZ) == nil
		},
		NeedsSubTargetRefresh: func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) bool {
			return true
		},
		SelectSubTarget: func(state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) (*civquestv1.Vec3, *civquestv1.BlockPosProto, string, bool) {
			drop := findNearestDroppedWoodItem(frame, anchorX, anchorZ)
			if drop == nil || drop.Position == nil {
				return nil, nil, "", false
			}
			approach := &civquestv1.Vec3{
				X: drop.Position.X,
				Y: drop.Position.Y,
				Z: drop.Position.Z,
			}
			return approach, nil, fmt.Sprintf("item:%d", drop.EntityId), true
		},
		BuildActIntent: func(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, current *TaskSubroutine) *civquestv1.AgentIntent {
			// Within 1.5m vacuum pickup range; server AgentEntity.tryPickupNearbyItems collects automatically.
			return nil
		},
	}

	return NewTaskSubroutine(spec, startTick)
}
