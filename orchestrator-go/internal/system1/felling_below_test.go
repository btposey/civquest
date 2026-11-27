package system1

import "testing"

// Live run, play test 8 seed: a trunk far below counted as felled without a swing and was
// picked again 188 times.
func TestFelling_TreeThatYieldsNothingIsLeftAWhile(t *testing.T) {
	var evs []capturedEvent
	below := withBlocks(blk(0, 63, 0, "minecraft:oak_log"), blk(0, 64, 0, "minecraft:oak_log"))
	var steps []replayStep
	for tick := int64(100); tick < 400; tick += 4 {
		steps = append(steps, replayStep{Frame: rf(tick, 3000, withPos(2.5, 71, 0.5), below)})
	}
	runReplay(t, replayScenario{Name: "tree_below", Stage: StageBootstrap, Profession: "EXPLORER",
		Sink: captureSink(&evs), Steps: steps})

	starts := countCat(evs, EventTaskStart)
	if starts == 0 {
		t.Fatal("setup: the tree was never chosen")
	}
	if starts > 1 {
		t.Errorf("the tree was started %d times in 15 seconds", starts)
	}
}
