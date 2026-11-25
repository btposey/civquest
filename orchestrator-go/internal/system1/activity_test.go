package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func makeActivityTestCtx(st *AgentState, tick int64, health float32, hostile bool) *FSMContext {
	f := &civquestv1.TelemetryFrame{
		WorldTick: tick,
		TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{
			AgentId:   "activity_agent",
			Health:    health,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 4},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}
	if hostile {
		f.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 7, EntityType: "minecraft:creeper", IsHostile: true, Distance: 2.5, Position: &civquestv1.Vec3{X: 2.5, Y: 64, Z: 0.5}}}
	}
	return &FSMContext{AgentID: "activity_agent", State: st, Frame: f}
}

// stackTask returns the topmost task frame's task (nil when no task frame is on the stack).
func stackTask(fsm *AgentFSM) *TaskSubroutine {
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	frames := fsm.kernel.Frames()
	for i := len(frames) - 1; i >= 0; i-- {
		if tl, ok := frames[i].Local.(taskLocal); ok {
			return tl.Task
		}
	}
	return nil
}

func topTaskLocal(t *testing.T, fsm *AgentFSM) (uint64, *TaskSubroutine) {
	t.Helper()
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	frames := fsm.kernel.Frames()
	for i := len(frames) - 1; i >= 0; i-- {
		if tl, ok := frames[i].Local.(taskLocal); ok {
			return uint64(frames[i].ID), tl.Task
		}
	}
	return 0, nil
}

// ADR-036: one activity stack. Emergency preemption is a push over the task frame; clearance
// pops it and the SAME task frame resumes with its Local (task progress) intact.
func TestActivityStack_EmergencyIsAPushAndResumeKeepsFrameLocal(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	fsm.StartTask(st, NewHarvestTreeTask(2, 64, 0, StageSettlement, 100))

	fsm.Tick(makeActivityTestCtx(st, 100, 20, false))
	id0, task0 := topTaskLocal(t, fsm)
	if fsm.ActiveActivityKind() != activityKindTaskSubroutine || task0 == nil || task0.ActionsEmitted != 1 {
		t.Fatalf("expected task frame on top with one action emitted, top=%q task=%+v", fsm.ActiveActivityKind(), task0)
	}
	if !st.HasActiveTask() {
		t.Fatalf("expected AgentState task view to project the task frame")
	}

	res := fsm.Tick(makeActivityTestCtx(st, 110, 6, true))
	if res.Stage != StageEmergency || fsm.ActiveActivityKind() != activityKindEmergency || fsm.ActivityDepth() != 2 {
		t.Fatalf("expected emergency frame pushed over the task, stage=%s top=%q depth=%d", res.Stage, fsm.ActiveActivityKind(), fsm.ActivityDepth())
	}
	if !st.HasActiveTask() {
		t.Fatalf("suspended task must remain committed")
	}

	res = fsm.Tick(makeActivityTestCtx(st, 300, 20, false))
	id1, task1 := topTaskLocal(t, fsm)
	if res.Stage != StageSettlement || fsm.ActiveActivityKind() != activityKindTaskSubroutine || id1 != id0 {
		t.Fatalf("expected the same task frame (%d) resumed, got stage=%s top=%q frame=%d", id0, res.Stage, fsm.ActiveActivityKind(), id1)
	}
	if task1 == nil || task1.ActionsEmitted < task0.ActionsEmitted || task1.Phase == TaskPhaseFailed {
		t.Fatalf("expected task progress preserved across the emergency (no false timeout), got %+v", task1)
	}

	res = fsm.Tick(makeActivityTestCtx(st, 310, 0, false))
	if res.Stage != StageDead || fsm.ActivityDepth() != 1 || fsm.ActiveActivityKind() != activityKindDead || st.HasActiveTask() {
		t.Fatalf("expected death to unwind the stack to the lone dead frame, got stage=%s depth=%d task=%v owner=%q", res.Stage, fsm.ActivityDepth(), st.HasActiveTask(), st.ActiveActivityKind())
	}
}
