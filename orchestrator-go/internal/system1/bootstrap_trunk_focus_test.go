package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Port of the baseline TestEvaluateBootstrapTimber_TreeTrunkFocus (cq-aik.30): bootstrap keeps
// harvesting the SAME trunk after its bottom log falls. The focus is frame-local: it is the
// committed harvest_tree task owned by the bootstrap walk cursor / task frame Local, not a
// field on AgentState.
func TestBootstrapTimber_TreeTrunkFocusSurvivesBottomLogFelled(t *testing.T) {
	state := NewAgentState()
	walk := &walkLocal{}
	pos := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Position:        pos,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 12, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 15, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
		},
	}
	intent := EvaluateBootstrapGoalsDirect("able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, walk)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected harvest intent on nearest log, got %+v", intent)
	}
	if locked, x, z := treeOf(walk.task); !locked || x != 12 || z != 10 {
		t.Fatalf("expected trunk focus (12,10) in the walk's task Local, got locked=%v (%d,%d)", locked, x, z)
	}

	// Bottom log felled; the upper log of the focused trunk remains, a decoy tree is now
	// strictly NEARER than the remaining trunk log.
	frame.WorldTick = 225
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 12, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 11}, BlockId: "minecraft:birch_log"},
	}
	intent2 := EvaluateBootstrapGoalsDirect("able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state, walk)
	if intent2 == nil || intent2.GetInteractBlock() == nil {
		t.Fatalf("expected harvest intent on remaining trunk log, got %+v", intent2)
	}
	if p := intent2.GetInteractBlock().Pos; p.X != 12 || p.Y != 65 || p.Z != 10 {
		t.Errorf("expected focus on the same trunk (12,65,10), got (%d,%d,%d)", p.X, p.Y, p.Z)
	}
	if locked, x, z := treeOf(walk.task); !locked || x != 12 || z != 10 {
		t.Errorf("focus must stay on (12,10), got locked=%v (%d,%d)", locked, x, z)
	}
}
