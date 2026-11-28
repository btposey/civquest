package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func logsAt(cells ...[3]int32) []*civquestv1.VisibleBlock {
	var out []*civquestv1.VisibleBlock
	for _, c := range cells {
		out = append(out, &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: c[0], Y: c[1], Z: c[2]}, BlockId: "minecraft:oak_log"})
	}
	return out
}

func column(x, z, height int32) []*civquestv1.VisibleBlock {
	var cells [][3]int32
	for y := range height {
		cells = append(cells, [3]int32{x, 64 + y, z})
	}
	return logsAt(cells...)
}

func TestTreeChoice_LargeShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		logs  []*civquestv1.VisibleBlock
		large bool
	}{
		{"seven logs, all in reach from underneath", column(2, 0, 7), false},
		{"top out of reach from underneath", column(2, 0, 8), true},
		{"thick trunk", append(column(2, 0, 4), column(3, 0, 4)...), true},
		{"diagonal branch", append(column(2, 0, 4), logsAt([3]int32{3, 68, 1})...), true},
	} {
		frame := fellingFrame(100, 0.5, 64, 0.5, tc.logs)
		if got := largeTreeColumns(frame)[[2]int32{2, 0}]; got != tc.large {
			t.Errorf("%s: large=%v, want %v", tc.name, got, tc.large)
		}
	}
}

// Every chooser takes the farther small tree over the branching one beside the agent, and the
// branching one when it is all there is.
func TestTreeChoice_EveryChooserPrefersTheSmallTree(t *testing.T) {
	branching := append(column(2, 0, 4), logsAt([3]int32{3, 67, 0})...)
	choosers := map[string]func(*civquestv1.TelemetryFrame, *AgentState) int32{
		"trunk": func(f *civquestv1.TelemetryFrame, st *AgentState) int32 {
			b, _ := findNearestTreeTrunk("a", f, f.Status.Position, st)
			return b.GetPos().GetX()
		},
		"bed": func(f *civquestv1.TelemetryFrame, st *AgentState) int32 {
			return bedLogLocked(f, f.Status.Position, st).GetPos().GetX()
		},
		"plan": func(f *civquestv1.TelemetryFrame, st *AgentState) int32 {
			x := &planExec{lw: &fsmWorld{ctx: &FSMContext{AgentID: "a", Frame: f, State: st}}, l: &planLocal{}, tick: f.WorldTick}
			target, _ := x.commitTrunk()
			return target.Pos[0]
		},
	}
	for name, choose := range choosers {
		both := fellingFrame(100, 0.5, 64, 0.5, append(column(9, 0, 4), branching...))
		if got := choose(both, NewAgentState()); got != 9 {
			t.Errorf("%s: chose x=%d, want the small tree at 9", name, got)
		}
		if got := choose(fellingFrame(100, 0.5, 64, 0.5, branching), NewAgentState()); got != 2 && got != 3 {
			t.Errorf("%s: chose x=%d with only the branching tree, want one of its columns", name, got)
		}
	}
}
