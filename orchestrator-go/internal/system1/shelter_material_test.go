package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// A walk toward a block is given time before another is counted: seen live, three were spent
// before the body moved and the block was placed from beyond reach.
func TestShelterApproachWaitsForEachWalk(t *testing.T) {
	l := &shelterLocal{BuildIndex: 5, Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}}
	far := &civquestv1.Vec3{X: 20, Y: 64, Z: 0}
	if _, ok := l.approach(far, &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}); !ok {
		t.Fatal("expected a walk toward the block")
	}
	l.WalkTick = 100
	if !l.approachUnderWay(104) {
		t.Error("a walk just sent must be waited for")
	}
	if l.approachUnderWay(100 + shelterApproachTicks) {
		t.Error("a walk that has had its time may be followed by another")
	}
	l.BuildIndex = 6
	if l.approachUnderWay(104) {
		t.Error("the next block starts its own walks")
	}
}
