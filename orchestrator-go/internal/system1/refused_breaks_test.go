package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func breakOf(x int32) []*civquestv1.AgentIntent {
	return []*civquestv1.AgentIntent{{Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
		Pos: &civquestv1.BlockPosProto{X: x, Y: 64}, InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
	}}}}
}

func report(status civquestv1.IntentReceipt_ReceiptStatus) *civquestv1.IntentReceipt {
	return &civquestv1.IntentReceipt{Status: status, FailureReason: "HARVEST_BREAK failed"}
}

func TestBreakRecord_GivesABlockUpAtTheLimit(t *testing.T) {
	failed := report(civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED)
	var r breakRecord
	// Two blocks asked for in turn are each counted on their own.
	for try := 1; try <= breakFailLimit; try++ {
		for x := int32(1); x <= 2; x++ {
			r = r.sent(breakOf(x))
			before := r
			next, memory := r.reported(failed, int64(try))
			r = next
			if try < breakFailLimit && len(memory) != 0 {
				t.Fatalf("block %d given up after %d failures", x, try)
			}
			if try == breakFailLimit && (len(memory) != 1 || memory[0].(blockRefused).At != blockCell{x, 64, 0}) {
				t.Fatalf("block %d not given up after %d failures: %v", x, try, memory)
			}
			// A record is a value: the one reported on is unchanged.
			for _, f := range before.Failed {
				if f.At == (blockCell{x, 64, 0}) && f.N != try-1 {
					t.Fatalf("earlier record changed in place: %+v", before.Failed)
				}
			}
		}
	}
	if len(r.Failed) != 0 {
		t.Fatalf("blocks handed to memory stay on record: %+v", r.Failed)
	}
}

func TestBreakRecord_CountsOnlyItsOwnBreaksFailing(t *testing.T) {
	failed := report(civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED)
	because := func(reason string) *civquestv1.IntentReceipt {
		return &civquestv1.IntentReceipt{Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: reason}
	}
	walk := []*civquestv1.AgentIntent{{Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{}}}}
	asked := breakRecord{}.sent(breakOf(1))
	for name, tc := range map[string]struct {
		r  breakRecord
		rc *civquestv1.IntentReceipt
	}{
		"nothing asked":      {breakRecord{}, failed},
		"a stuck walk":       {asked, because("Navigation stuck")},
		"a stopped walk":     {asked, because("Navigation stopped before reaching destination")},
		"a signpost":         {asked, because("Primitive at index 0 (PLACE_SIGNPOST) failed")},
		"preempted":          {asked, report(civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX)},
		"still being broken": {asked, report(civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING)},
	} {
		if r, memory := tc.r.reported(tc.rc, 1); len(r.Failed) != 0 || len(memory) != 0 {
			t.Errorf("%s: counted a failure: %+v %v", name, r.Failed, memory)
		}
	}
	// Every reason the mod gives for a break counts, even when a walk has been sent since.
	for _, reason := range []string{"HARVEST_BREAK failed (unbreakable, fluid, or out of reach)",
		"Block breaking cancelled or target unreachable", "Failed to resume block breaking at BlockPos"} {
		if r, _ := asked.sent(walk).reported(because(reason), 1); len(r.Failed) != 1 {
			t.Errorf("%q not counted: %+v", reason, r.Failed)
		}
	}
	// The completion of a walk sent since says nothing of the block.
	once, _ := asked.reported(failed, 1)
	if r, _ := once.sent(walk).reported(report(civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED), 2); len(r.Failed) != 1 {
		t.Errorf("a walk's completion cleared the block's failures: %+v", r.Failed)
	}

	r, _ := breakRecord{}.sent(breakOf(1)).reported(failed, 1)
	if got := r.closing(7); len(got) != 1 || got[0].(blockRefused) != (blockRefused{At: blockCell{1, 64, 0}, Tick: 7}) {
		t.Fatalf("a closing frame must hand over its refusals, got %v", got)
	}
	r, _ = r.sent(breakOf(1)).reported(report(civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED), 2)
	if len(r.Failed) != 0 {
		t.Fatalf("a block that broke is forgotten: %+v", r.Failed)
	}
}

// Every activity that can break a block keeps a record, or a failed break changes nothing.
func TestEveryActivityKeepsABreakRecord(t *testing.T) {
	var locals []any
	for _, a := range newManagedActivities() {
		l, _ := a.Init(&fsmWorld{}, nil)
		locals = append(locals, l)
	}
	for _, l := range append(locals, recoverLocal{}, emergencyLocal{}) {
		if _, put, ok := breakRecordOf(l); !ok {
			t.Errorf("%T keeps no break record", l)
		} else if r, _, _ := breakRecordOf(put(breakRecord{Asked: blockCell{1, 2, 3}})); r.Asked != (blockCell{1, 2, 3}) {
			t.Errorf("%T does not hold the record put in it", l)
		}
	}
}

// Every chooser passes over a block the mod reports as refused, and one given up in Memory.
func TestChoosersPassOverBlocksThatWillNotBreak(t *testing.T) {
	pos := &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:stone_pickaxe", Count: 1}}
	near := &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}
	frameOf := func(id string, refused bool) *civquestv1.TelemetryFrame {
		return &civquestv1.TelemetryFrame{WorldTick: 100,
			Status:            &civquestv1.AgentStatus{AgentId: "a", Position: pos, Inventory: inv, FoodLevel: 20},
			InterestingBlocks: []*civquestv1.VisibleBlock{{Pos: near, BlockId: id, BreakRefused: refused}}}
	}
	choosers := map[string]struct {
		block  string
		choose func(*civquestv1.TelemetryFrame, *AgentState) *civquestv1.BlockPosProto
	}{
		"mine stone": {"minecraft:stone", func(f *civquestv1.TelemetryFrame, st *AgentState) *civquestv1.BlockPosProto {
			b, _ := findNearestMineableStoneBlock(f, pos, inv, func(b *civquestv1.VisibleBlock) bool { return st.unbreakable(b, 100) })
			return b.GetPos()
		}},
		"trunk": {"minecraft:oak_log", func(f *civquestv1.TelemetryFrame, st *AgentState) *civquestv1.BlockPosProto {
			b, _ := findNearestTreeTrunk("a", f, pos, st)
			return b.GetPos()
		}},
		"timber for a bed": {"minecraft:oak_log", func(f *civquestv1.TelemetryFrame, st *AgentState) *civquestv1.BlockPosProto {
			in, _ := logGatherIntent("a", f, pos, st, &bedLocal{})
			return in.GetInteractBlock().GetPos()
		}},
		"find along the way": {"minecraft:coal_ore", func(f *civquestv1.TelemetryFrame, st *AgentState) *civquestv1.BlockPosProto {
			find := bestGatherFind("a", f, st, func(string) bool { return false })
			if find == nil {
				return nil
			}
			return find.Block.GetPos()
		}},
	}
	for name, tc := range choosers {
		t.Run(name, func(t *testing.T) {
			if got := tc.choose(frameOf(tc.block, false), NewAgentState()); !sameBlockPos(got, near) {
				t.Fatalf("control: expected the block to be chosen, got %v", got)
			}
			if got := tc.choose(frameOf(tc.block, true), NewAgentState()); sameBlockPos(got, near) {
				t.Errorf("chose a block the mod will refuse")
			}
			st := NewAgentState()
			st.giveUpBlock(cellAt(near), 100)
			if got := tc.choose(frameOf(tc.block, false), st); sameBlockPos(got, near) {
				t.Errorf("chose a block that was given up")
			}
		})
	}
}

// A blueprint cell that cannot be cleared is passed over rather than dug at for ever.
func TestConstructionPassesOverACellThatWillNotBreak(t *testing.T) {
	voxel := &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 0}
	frame := &civquestv1.TelemetryFrame{
		Status:            &civquestv1.AgentStatus{Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}},
		InterestingBlocks: []*civquestv1.VisibleBlock{{Pos: voxel, BlockId: "minecraft:stone", BreakRefused: true}},
	}
	work := &civquestv1.BuildBlockIntent{TargetVoxel: voxel, BlockStateString: "minecraft:oak_planks"}
	st := NewAgentState()
	intents, advance := constructionWorkStep(frame, blueprints.QuadrantNW, work,
		func(b *civquestv1.VisibleBlock) bool { return st.unbreakable(b, 100) })
	if len(intents) != 0 || !advance {
		t.Fatalf("expected the cell to be passed over, got %v advance=%v", intents, advance)
	}
}

// A suffocating agent that cannot dig itself free tries to walk out, one side after another.
func TestSuffocationWalksOutWhenTheDigIsRefused(t *testing.T) {
	ctx := &FSMContext{State: NewAgentState(), Frame: &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}}}}
	h := &blockHazard{Kind: blockHazardSuffocation, Dig: &civquestv1.BlockPosProto{Y: 65}}
	if got := h.walkOutIfRefused(ctx, 100); got.Dig == nil {
		t.Fatalf("a dig that has not failed is kept, got %+v", got)
	}
	ctx.State.giveUpBlock(cellAt(h.Dig), 100)
	sides := map[[2]float64]bool{}
	for tick := int64(100); tick < 100+4*refusedSideTicks; tick += refusedSideTicks {
		got := h.walkOutIfRefused(ctx, tick)
		if got.Dig != nil || got.Target == nil {
			t.Fatalf("expected a walk, got %+v", got)
		}
		sides[[2]float64{got.Target.X, got.Target.Z}] = true
	}
	if len(sides) != 4 {
		t.Errorf("tried %d sides, want 4: %v", len(sides), sides)
	}
}
