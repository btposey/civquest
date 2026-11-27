package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
)

// cq-6l3.17: a crafting table that would not go down was sent to the same cell for nine minutes.
func TestPlan_FailingPlacementTriesOtherCellsThenMoves(t *testing.T) {
	st := NewAgentState()
	l := planLocal{}
	cells, moves := map[[3]int32]int{}, 0
	for tick := int64(100); tick < 100+planCraftThrottle*30; tick += planCraftThrottle {
		lw := &fsmWorld{ctx: &FSMContext{AgentID: "placer", State: st, Frame: rf(tick, 4000)}}
		x := planExec{lw: lw, l: &l, tick: tick, facts: plan.Facts{Inventory: map[string]int{"minecraft:crafting_table": 1}}}
		in, working := x.place(plan.Step{Kind: plan.StepPlace, Item: "minecraft:crafting_table"})
		if !working || in == nil {
			t.Fatalf("tick %d: placement must stay in progress, got %v %t", tick, in, working)
		}
		if p := in.GetInteractBlock().GetPos(); p != nil {
			cells[[3]int32{p.X, p.Y, p.Z}]++
		} else if in.GetNavigate() != nil {
			moves++
		}
	}
	if len(cells) < 8 || moves == 0 {
		t.Fatalf("tried %d cells and moved %d times", len(cells), moves)
	}
	for c, n := range cells {
		if n > 3 {
			t.Errorf("cell %v tried %d times", c, n)
		}
	}
}

// cq-6l3.18: an expedition pushed bootstrap while a trunk was in view; it found nothing to do,
// popped, and was pushed again every tick.
func TestBootstrap_IdleHarvestPushBacksOff(t *testing.T) {
	st := NewAgentState()
	fsm := NewAgentFSM(StageBootstrap, nil)
	frame := rf(100, 4000, withBlocks(blk(2, 64, 0, "minecraft:oak_log"), blk(2, 65, 0, "minecraft:oak_log")))
	frame.Status.Position = &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	world := func(tick int64) *fsmWorld {
		return &fsmWorld{fsm: fsm, tick: tick, skipped: map[string]bool{},
			ctx: &FSMContext{AgentID: "harvester", State: st, Frame: frame}}
	}
	push := func(tick int64) bool {
		c, ok := world(tick).duringTravel(activityKindBootstrap, false, true)
		return ok && (c.Kind == activityKindBootstrap || c.Kind == activityKindPlan)
	}
	if !push(100) {
		t.Fatal("setup: a trunk in view must start a harvest")
	}
	step := yieldWalk(world(100), activityKindBootstrap, false, false)
	if step.Op != htpda.OpPop {
		t.Fatalf("an idle frame must pop, got %v", step.Op)
	}
	fsm.applyMemoryLocked(world(100), step.Memory)
	if c, ok := world(104).duringTravel(activityKindBootstrap, false, true); ok && c.Kind == activityKindBootstrap {
		t.Fatal("bootstrap pushed again straight after finding nothing to do")
	}
	if world(100 + workBackoffTicks).backedOff(activityKindBootstrap) {
		t.Fatal("the backoff must end")
	}
	if acted := yieldWalk(world(100), activityKindBootstrap, true, false); len(acted.Memory) != 0 {
		t.Fatal("a frame that did some work is not backed off")
	}
}
