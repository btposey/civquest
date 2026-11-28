package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// cq-8jf: a sealed cabin is no trap. Recovery in or into it breaks the doorway's temporary
// blocks and nothing else; walls are never dug while the doorway stands.
func TestRecover_LeavesOwnCabinByTheDoorway(t *testing.T) {
	st := NewAgentState()
	st.SetCabinBuilt(blueprints.CabinTotalBlocks)
	st.SetShelterPos(&civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0})
	lw := &fsmWorld{ctx: &FSMContext{AgentID: "a", State: st}}
	walls := []*civquestv1.VisibleBlock{
		blk(2, 65, 0, "minecraft:cobblestone"), blk(-2, 65, 0, "minecraft:cobblestone"), blk(0, 65, -2, "minecraft:cobblestone"),
		blk(0, 65, 2, "minecraft:cobblestone"), blk(0, 66, 2, "minecraft:cobblestone"),
	}
	frame := rf(100, 1000, withBlocks(walls...))
	for _, tc := range []struct {
		name   string
		pos    *civquestv1.Vec3
		target [3]float64
	}{
		{"enclosed inside", &civquestv1.Vec3{X: 0.5, Y: 65, Z: 0.5}, [3]float64{20, 64, 0}},
		{"coming home to the doorstep", &civquestv1.Vec3{X: 0.5, Y: 64, Z: 3.5}, [3]float64{0.5, 64, 0.5}},
	} {
		loc := recoverLocal{recoverArgs: recoverArgs{Cause: CauseEnclosed, Target: tc.target, HasTarget: true}, Climb: true}
		in, handled := loc.doorwayStep(lw, frame, tc.pos)
		got := in.GetInteractBlock()
		if !handled || got.GetPos().GetX() != 0 || got.GetPos().GetY() != 66 || got.GetPos().GetZ() != 2 {
			t.Errorf("%s: handled=%v intent=%v, want the upper doorway block broken", tc.name, handled, in)
		}
	}
	// Away from the cabin, ordinary recovery digs as before.
	loc := recoverLocal{recoverArgs: recoverArgs{Cause: CauseEnclosed}}
	if _, handled := loc.doorwayStep(lw, frame, &civquestv1.Vec3{X: 40, Y: 64, Z: 40}); handled {
		t.Error("handled a recovery away from the cabin")
	}
}
