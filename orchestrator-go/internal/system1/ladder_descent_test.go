package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// A walk to a place below fails at the top of a three-block drop: the agent steps off, hangs a
// ladder for every block back to where it stood, and Recover ends below.
func TestRecover_DescendsAndHangsTheWayBack(t *testing.T) {
	r := newRecoverRig(t, [3]float64{20.5, 61, 0.5})
	ledge := []*civquestv1.VisibleBlock{blk(0, 63, 0, "minecraft:stone"), blk(0, 62, 0, "minecraft:stone"),
		blk(0, 61, 0, "minecraft:stone"), blk(1, 60, 0, "minecraft:stone")}
	reg, err := htpda.NewRegistry(r.parent, recoverActivity{}, ladderActivity{})
	if err != nil {
		t.Fatal(err)
	}
	r.k = htpda.NewKernel(reg, htpda.PriorityTable{htpda.AnyStage: {"stub": 1}}, htpda.Options{})
	r.dispatch(100, nil, htpda.Tick{})
	pack := withInv(item(0, "minecraft:ladder", 3))
	in := r.stuck(110, terrainStuck(2), withBlocks(ledge...), pack).Intents()
	nav := func() *civquestv1.NavigateIntent {
		if len(in) != 1 {
			return nil
		}
		return in[0].GetNavigate()
	}()
	if r.topKind() != activityKindLadder || nav == nil || !nav.IntoCell || nav.Target.X != 1.5 || nav.Target.Y != 61 {
		t.Fatalf("expected a step off the drop, top=%s intents=%v", r.topKind(), in)
	}
	below := withPos(1.5, 61, 0.5)
	in = r.dispatch(130, nil, htpda.Tick{}, withBlocks(ledge...), pack, below).Intents()
	for n, y := 0, int32(61); y <= 63; n, y = n+1, y+1 {
		if len(in) != 1 || in[0].GetBuildBlock() == nil || in[0].GetBuildBlock().TargetVoxel.Y != y ||
			in[0].GetBuildBlock().BlockStateString != "minecraft:ladder[facing=east]" {
			t.Fatalf("rung %d: got %v", y, in)
		}
		ledge = append(ledge, blk(1, y, 0, "minecraft:ladder"))
		top, _ := r.k.Top()
		rc := receipt(int64(10+n), rcCompleted, "")
		rc.FrameId = uint64(top.ID)
		in = r.dispatch(int64(140+10*n), rc, htpda.Receipt{Frame: top.ID, Status: rcCompleted}, withBlocks(ledge...), pack, below).Intents()
	}
	if _, ok := r.result.(recovered); !ok {
		t.Fatalf("expected Recover to end below, top=%s result=%#v", r.topKind(), r.result)
	}
}

func TestSafeDrop_TakesOnlyWhatIsSeenToBeSafe(t *testing.T) {
	wall := []*civquestv1.VisibleBlock{blk(0, 63, 0, "minecraft:stone"), blk(0, 62, 0, "minecraft:stone"), blk(0, 61, 0, "minecraft:stone")}
	for name, tc := range map[string]struct {
		blocks []*civquestv1.VisibleBlock
		drop   int
	}{
		"three onto rock":    {append(wall[:3:3], blk(1, 60, 0, "minecraft:stone")), 3},
		"two onto rock":      {append(wall[:3:3], blk(1, 61, 0, "minecraft:stone")), 2},
		"a step":             {append(wall[:3:3], blk(1, 62, 0, "minecraft:stone")), 0},
		"four":               {append(wall[:3:3], blk(1, 59, 0, "minecraft:stone")), 0},
		"bottom unseen":      {wall, 0},
		"lava":               {append(wall[:3:3], blk(1, 61, 0, "minecraft:lava"), blk(1, 60, 0, "minecraft:stone")), 0},
		"water":              {append(wall[:3:3], blk(1, 60, 0, "minecraft:water")), 0},
		"magma":              {append(wall[:3:3], blk(1, 60, 0, "minecraft:magma_block")), 0},
		"nothing to hang on": {[]*civquestv1.VisibleBlock{wall[0], blk(1, 60, 0, "minecraft:stone")}, 0},
	} {
		got, ok := safeDrop(rf(100, 3000, withBlocks(tc.blocks...)), ladderCell(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}), 1, 0)
		if got != tc.drop || ok != (tc.drop > 0) {
			t.Errorf("%s: drop %d, %v; want %d", name, got, ok, tc.drop)
		}
	}
}
