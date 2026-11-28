package system1

// Task ownership (ADR-036, bead cq-2sr.21). AgentState no longer holds a task: a committed
// task's progress is the Local of its task_subroutine frame, or, for the tick between a
// waterfall starting it inline and the kernel framing it, the starting frame's walk cursor
// (walkLocal.task: the task is a step result handed to the child push as Init args, never a
// mailbox on AgentState). AgentState keeps only taskView, a value projection for readers
// outside the owning frame.

import (
	"fmt"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// taskInfo is the value snapshot of the committed task published on AgentState.
type taskInfo struct {
	Active  bool
	Kind    string
	Phase   TaskPhase
	Routine Routine
}

func taskInfoOf(t *TaskSubroutine) *taskInfo {
	if !taskActive(t) {
		return &taskInfo{}
	}
	return &taskInfo{Active: true, Kind: t.Spec.Kind, Phase: t.Phase, Routine: t.Spec.Routine}
}

// publishTask republishes the projection of t.
func (s *AgentState) publishTask(t *TaskSubroutine) {
	if s != nil {
		s.taskView.Store(taskInfoOf(t))
	}
}

// taskStart is the Init args of a task frame: the committed task and any receipt routed to
// it while it was a pending (inline-started) task.
type taskStart struct {
	Task    *TaskSubroutine
	Receipt *civquestv1.IntentReceipt
}

// hasTask reports whether the walk cursor owns an active task.
func (w *walkLocal) hasTask() bool { return w != nil && taskActive(w.task) }

// startTaskLocked commits t to the walk cursor (state.mu held). The first step runs inline.
func (w *walkLocal) startTaskLocked(state *AgentState, t *TaskSubroutine) {
	if t != nil {
		state.noteDiag(EventTaskStart, t.Spec.ID, taskDetails(t, nil))
	}
	if state == nil {
		return
	}
	w.task = t
	w.taskReceipt = nil
	state.publishTask(t)
}

// launchTaskLocked commits a new task and, unless the stepping frame hands its tasks to
// children, runs its first step at once. A deferred task runs in its own frame this same tick,
// so its intents and receipts belong to the frame that owns it.
func (w *walkLocal) launchTaskLocked(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame, t *TaskSubroutine) TaskStepResult {
	w.startTaskLocked(state, t)
	if w.deferTask {
		return TaskStepResult{Active: true}
	}
	return w.stepTaskLocked(agentID, state, frame)
}

// stepTaskLocked advances the owned task one tick (state.mu held), dropping it when done.
func (w *walkLocal) stepTaskLocked(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame) TaskStepResult {
	if w == nil || w.task == nil {
		return TaskStepResult{}
	}
	receipt := w.taskReceipt
	w.taskReceipt = nil
	res := w.task.TickWithReceipt(agentID, state, frame, receipt)
	if res.Done {
		state.noteDiag(EventTaskEnd, fmt.Sprintf("%s %s: %s", w.task.Spec.ID, res.Phase, res.Reason), taskDetails(w.task, frame))
		w.task = nil
	}
	state.publishTask(w.task)
	return res
}

// stepTask is stepTaskLocked for callers that do not hold state.mu.
func (w *walkLocal) stepTask(agentID string, state *AgentState, frame *civquestv1.TelemetryFrame) TaskStepResult {
	if w == nil || state == nil {
		return TaskStepResult{}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return w.stepTaskLocked(agentID, state, frame)
}

// clearTaskLocked drops the owned task (state.mu not required: the view is atomic).
func (w *walkLocal) clearTask(state *AgentState) {
	if w != nil {
		if w.task != nil {
			state.noteDiag(EventTaskDropped, w.task.Spec.ID, taskDetails(w.task, nil))
		}
		w.task, w.taskReceipt = nil, nil
	}
	state.publishTask(nil)
}

// walkCarrier returns the walk cursor of a Local that can start tasks inline, and a rebuild
// func that returns the Local with a replaced cursor.
func walkCarrier(local any) (walkLocal, func(walkLocal) any, bool) {
	switch l := local.(type) {
	case bootstrapLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case explorationLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case directiveLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case planLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case bedLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	case ladderLocal:
		return l.Walk, func(w walkLocal) any { l.Walk = w; return l }, true
	}
	return walkLocal{}, nil, false
}

// pendingTaskOf returns the inline-started, not yet framed task a Local holds.
func pendingTaskOf(local any) *taskStart {
	if w, _, ok := walkCarrier(local); ok && taskActive(w.task) {
		return &taskStart{Task: w.task, Receipt: w.taskReceipt}
	}
	return nil
}

// withoutPendingTask returns the Local after its pending task was handed to a task frame.
func withoutPendingTask(local any) any {
	if w, rebuild, ok := walkCarrier(local); ok {
		w.task, w.taskReceipt = nil, nil
		return rebuild(w)
	}
	return local
}

// routePendingReceipt stores a receipt for the pending task a Local holds (the task's first
// intents were emitted under this frame; the receipt is routed by that frame ID).
func routePendingReceipt(local any, rc *civquestv1.IntentReceipt) (any, bool) {
	if w, rebuild, ok := walkCarrier(local); ok && taskActive(w.task) {
		w.taskReceipt = rc
		return rebuild(w), true
	}
	return local, false
}

// topTaskOf is the task the view projects: the topmost active task frame's task, else a
// pending start held by a frame's walk cursor.
func topTaskOf(frames []htpda.Frame) *TaskSubroutine {
	for i := len(frames) - 1; i >= 0; i-- {
		if tl, ok := frames[i].Local.(taskLocal); ok && taskActive(tl.Task) {
			return tl.Task
		}
		if p := pendingTaskOf(frames[i].Local); p != nil {
			return p.Task
		}
	}
	return nil
}

// handOffPendingTask keeps the task an exiting frame started inline (cq-2sr.21): the frame's
// walk cursor is its only home until the next tick frames it, so a frame that exits first
// parks the task (and the receipt routed to it) where the task activity's Propose finds it,
// instead of dropping a committed task with its Local.
func (w *fsmWorld) handOffPendingTask(local any) {
	if p := pendingTaskOf(local); p != nil && w.fsm != nil && w.fsm.startedTask == nil {
		w.fsm.startedTask, w.fsm.startedReceipt = p.Task, p.Receipt
	}
}
