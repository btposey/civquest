package system1

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/protobuf/proto"
)

// Test rig for driving a TaskSubroutine directly, the way a task frame (or a waterfall that
// started it inline) does: the task lives in a walk cursor, never on AgentState.
var (
	rigMu    sync.Mutex
	rigWalks = map[*AgentState]*walkLocal{}
)

// rigWalk returns the walk cursor that owns state's directly driven task.
func rigWalk(state *AgentState) *walkLocal {
	rigMu.Lock()
	defer rigMu.Unlock()
	w := rigWalks[state]
	if w == nil {
		w = &walkLocal{}
		rigWalks[state] = w
	}
	return w
}

func rigStart(state *AgentState, t *TaskSubroutine) {
	state.mu.Lock()
	defer state.mu.Unlock()
	rigWalk(state).startTaskLocked(state, t)
}

func rigStep(state *AgentState, agentID string, frame *civquestv1.TelemetryFrame) TaskStepResult {
	return rigWalk(state).stepTask(agentID, state, frame)
}

func rigDeliver(state *AgentState, rc *civquestv1.IntentReceipt) {
	rigWalk(state).taskReceipt = proto.Clone(rc).(*civquestv1.IntentReceipt)
}

func rigClear(state *AgentState) { rigWalk(state).clearTask(state) }

func rigTask(state *AgentState) *TaskSubroutine { return rigWalk(state).task }

// treeOf returns the trunk column a harvest_tree task is committed to (the frame's existence
// is the lock: there is no separate tree-focus state).
func treeOf(t *TaskSubroutine) (locked bool, x, z int32) {
	if !taskActive(t) || t.Spec.Kind != "harvest_tree" || t.Spec.Target.BlockPos == nil {
		return false, 0, 0
	}
	return true, t.Spec.Target.BlockPos.X, t.Spec.Target.BlockPos.Z
}

// fsmTask returns the committed task on the FSM's stack (framed, or pending in a walk cursor).
func fsmTask(fsm *AgentFSM) *TaskSubroutine {
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return topTaskOf(fsm.kernel.Frames())
}
