package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func countPushes(fsm *AgentFSM) map[string]int {
	n := map[string]int{}
	fsm.SetEventSink(func(_ int64, category, _ string, d map[string]any) {
		if category == EventFrame && d["op"] == "push" {
			n[d["kind"].(string)]++
		}
	})
	return n
}

// A well-fed agent told to forage, with nothing in reach, pushed foraging every tick.
func TestForaging_DirectiveWithNothingToFindBacksOff(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	st.SetStrategicDirective(DirectiveActionForageFood, "food", "FORAGE_FOOD: gather", 100, 60000)
	cfg := xdg.DefaultAgentIdentityConfig("directed_forager")
	cfg.Profession = "FARMER"
	pushes := countPushes(fsm)
	for tick := int64(100); tick < 900; tick += 4 {
		fsm.Tick(&FSMContext{AgentID: "directed_forager", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(tick, 3000, 20, fullSurvivalKitInv(), nil)})
	}
	limit := int(800/workBackoffTicks) + 1
	if got := pushes[activityKindForaging]; got == 0 || got > limit {
		t.Fatalf("foraging pushed %d times, want 1..%d", got, limit)
	}
}

// An agent without a bed and with nothing to make one from pushed the bed plan, which popped in
// the same tick, again and again (cq-3sa.2): it is offered only when its step can progress.
func TestBed_OfferedOnlyWhenTheStepCanProgress(t *testing.T) {
	for name, tc := range map[string]struct {
		blocks []*civquestv1.VisibleBlock
		pushed bool
	}{
		"nothing to make one from": {},
		"a log in view": {blocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 3, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		}, pushed: true},
	} {
		t.Run(name, func(t *testing.T) {
			fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
			st := NewAgentState()
			st.SetBedRegistry(newFakeBedRegistry())
			st.SetRecipeGraph(crafting.NewRecipeGraph(craftingtest.Nodes()))
			cfg := xdg.DefaultAgentIdentityConfig("bedless")
			cfg.Profession = "FARMER"
			pushes := countPushes(fsm)
			for tick := int64(100); tick < 900; tick += 4 {
				f := makeArbiterTestFrame(tick, 3000, 20, fullSurvivalKitInv(), nil)
				f.InterestingBlocks = append(f.InterestingBlocks, tc.blocks...)
				fsm.Tick(&FSMContext{AgentID: "bedless", State: st, Config: cfg, Frame: f})
			}
			if got := pushes[activityKindBed]; (got > 0) != tc.pushed {
				t.Fatalf("bed plan pushed %d times, want pushed=%v", got, tc.pushed)
			}
		})
	}
}

// Live: an activity that popped for want of work was pushed over the running one again on the
// next tick, hundreds of times.
func TestWorkBackoff_NoInterruptionByAnActivityWithNothingToDo(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	fsm.currentRoutine = RoutinePrimaryDuty
	preemptor := func() string {
		w := &fsmWorld{fsm: fsm, tick: 100, skipped: map[string]bool{},
			ctx: &FSMContext{AgentID: "bert", State: NewAgentState(), Frame: rf(100, 4000), Facts: NewFactGraph()}}
		kind, _ := w.preemptorAbove(priorityGather, "")
		return kind
	}
	if got := preemptor(); got != activityKindBootstrap {
		t.Fatalf("setup: expected bootstrap to outrank gathering, got %q", got)
	}
	fsm.workBackoffUntil = map[htpda.Kind]int64{activityKindBootstrap: 300}
	if got := preemptor(); got == activityKindBootstrap {
		t.Fatal("bootstrap interrupted again while backed off")
	}
}
