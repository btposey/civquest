package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func fellingFrame(tick int64, x, y, z float64, logs []*civquestv1.VisibleBlock) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: tick,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: x, Y: y, Z: z},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: logs,
	}
}

func startFelling(t *testing.T) *AgentState {
	t.Helper()
	state := NewAgentState()
	rigStart(state, NewHarvestTreeTask(0, 64, 0, StageBootstrap, 100))
	return state
}

// The two lowest logs come out from beside the tree, whatever else is in reach; then the agent
// walks into their place and nowhere else.
func TestFelling_BottomTwoThenIntoTheirPlace(t *testing.T) {
	state := startFelling(t)
	logs := nineLogTrunk(0, 0, 64, 5)
	for i, want := range []int32{64, 65} {
		res := rigStep(state, "agent", fellingFrame(100+int64(i)*21, 1.5, 64, 0.5, logs[i:]))
		if got := res.Intent.GetInteractBlock(); got == nil || got.Pos.Y != want {
			t.Fatalf("step %d: want the log at y=%d, got %+v", i, want, res.Intent)
		}
	}
	res := rigStep(state, "agent", fellingFrame(142, 1.5, 64, 0.5, logs[2:]))
	nav := res.Intent.GetNavigate()
	if nav == nil || !nav.IntoCell || nav.Target.X != 0.5 || nav.Target.Y != 64 || nav.Target.Z != 0.5 {
		t.Fatalf("want a walk into the trunk's own cell, got %+v", res.Intent)
	}
	// Close is not underneath: from the edge of the cell the walk is still under way.
	res = rigStep(state, "agent", fellingFrame(162, 1.1, 64, 0.5, logs[2:]))
	if res.Intent.GetInteractBlock() != nil {
		t.Fatalf("broke a log before standing under the trunk: %+v", res.Intent)
	}
	res = rigStep(state, "agent", fellingFrame(183, 0.55, 64, 0.5, logs[2:]))
	if got := res.Intent.GetInteractBlock(); got == nil || got.Pos.Y != 66 {
		t.Fatalf("underneath: want the log at y=66, got %+v", res.Intent)
	}
}

// A trunk that cannot be walked under is worked from outside instead of being given up.
func TestFelling_NoWayUnderStillHarvests(t *testing.T) {
	for name, fail := range map[string]func(state *AgentState) int64{
		"walk takes too long": func(*AgentState) int64 { return 150 + standGiveUpTicks },
		"walk fails": func(state *AgentState) int64 {
			rigDeliver(state, &civquestv1.IntentReceipt{Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: "Navigation stuck"})
			return 150
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := startFelling(t)
			logs := nineLogTrunk(0, 0, 64, 5)[2:]
			res := rigStep(state, "agent", fellingFrame(140, 1.5, 64, 0.5, logs))
			if !res.Intent.GetNavigate().GetIntoCell() {
				t.Fatalf("want the walk under the trunk first, got %+v", res.Intent)
			}
			res = rigStep(state, "agent", fellingFrame(fail(state), 1.5, 64, 0.5, logs))
			if got := res.Intent.GetInteractBlock(); got == nil || got.Pos.Y != 66 {
				t.Fatalf("want y=66 broken from beside the trunk, got %+v (active=%v)", res, state.HasActiveTask())
			}
		})
	}
}

// A trunk whose bottom is already gone is stood under on the agent's own level.
func TestFelling_HangingTrunkIsStoodUnderOnTheGround(t *testing.T) {
	cell := cellUnderTrunk(&civquestv1.Vec3{X: 1.5, Y: 64, Z: 0.5}, 0, 66, 0)
	if cell.Y != 64 {
		t.Fatalf("want the ground cell y=64, got %d", cell.Y)
	}
}

// Nothing reachable from underneath: no walk for its own sake.
func TestFelling_NoWalkWhenNothingIsInReach(t *testing.T) {
	frame := fellingFrame(100, 1.5, 64, 0.5, nineLogTrunk(0, 0, 71, 2))
	if cell, ok := walkUnderTrunk(frame, 0, 64, 0, 4.5, "oak"); ok {
		t.Fatalf("want no walk, got %+v", cell)
	}
}
