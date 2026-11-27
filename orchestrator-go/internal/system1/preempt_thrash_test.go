package system1

import (
	"math"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func gatherKit() []*civquestv1.ItemStackProto {
	return append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{ItemId: "minecraft:stone_pickaxe", Count: 1})
}

// travelRun drives an explorer that never gets anywhere, with a coal seam in view whenever
// seam says so, and returns the ticks at which gathering was pushed.
func travelRun(ticks int64, seam func(fsm *AgentFSM) bool) (pushes []int64) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("fred")
	cfg.Profession = "EXPLORER"
	fsm.SetEventSink(func(tick int64, category, summary string, _ map[string]any) {
		if category == "FSM_FRAME" && summary == "push gather" {
			pushes = append(pushes, tick)
		}
	})
	var survey *civquestv1.HorizonSurveyReport
	for tick := int64(100); tick < 100+ticks; tick += 4 {
		var blocks []*civquestv1.VisibleBlock
		if seam(fsm) {
			blocks = []*civquestv1.VisibleBlock{blk(10, 64, 0, "minecraft:coal_ore")}
		}
		inv := gatherKit()
		f := makeArbiterTestFrame(tick, 2000, 20, inv, blocks)
		f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
		if survey != nil {
			st.IngestHorizonSurveyReport(survey, tick)
			f.HorizonSurvey, survey = survey, nil
		}
		for _, in := range fsm.Tick(&FSMContext{AgentID: "fred", State: st, Config: cfg, Frame: f}).Intents {
			if in.Intent.GetSurveyHorizon() != nil {
				survey = clearSurvey(tick)
			}
		}
	}
	return pushes
}

func clearSurvey(tick int64) *civquestv1.HorizonSurveyReport {
	r := &civquestv1.HorizonSurveyReport{OriginX: 0.5, OriginY: 64, OriginZ: 0.5, CompletedTick: tick, MaxRange: 64}
	for i := int32(0); i < 36; i++ {
		rad := float64(i*10) * math.Pi / 180
		r.Sectors = append(r.Sectors, &civquestv1.HorizonSector{SectorIndex: i, BearingDeg: float32(i * 10), HitDistance: 64,
			FeatureType: "OPEN_PLAINS", HitBlockId: "minecraft:air", HitX: -math.Sin(rad) * 64, HitY: 64, HitZ: math.Cos(rad) * 64, OpenHorizon: true})
	}
	return r
}

func within(ticks []int64, from, to int64) (n int) {
	for _, t := range ticks {
		if t >= from && t < to {
			n++
		}
	}
	return n
}

// Play test 10: gathering was made to give way, and the travelling agent was offered it again
// every frame, to be pushed and ended at once: it stood for minutes.
func TestPlaytest10_NothingGivingWayIsOfferedDuringTravel(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	ctx := &FSMContext{AgentID: "fred", State: NewAgentState(), Config: xdg.DefaultAgentIdentityConfig("fred"),
		Frame: makeArbiterTestFrame(1000, 2000, 20, gatherKit(), []*civquestv1.VisibleBlock{blk(10, 64, 0, "minecraft:coal_ore")})}
	reg := NewDefaultEngineRegistry()
	offered := func() (string, bool) {
		w := newFSMWorld(fsm, ctx, reg, &SkillContext{AgentID: "fred", Frame: ctx.Frame, Config: ctx.Config, State: ctx.State})
		c, ok := w.duringTravel(activityKindExploration, false, true)
		return string(c.Kind), ok
	}
	if kind, ok := offered(); !ok || kind != activityKindGather {
		t.Fatalf("on the way the seam offers %q (%v), want gather", kind, ok)
	}
	fsm.stonewall(&fsmWorld{fsm: fsm, ctx: ctx, tick: 1000}, activityKindGather, "test")
	if kind, ok := offered(); ok && kind == activityKindGather {
		t.Error("gather is offered while it is giving way")
	}
}

// Play test 10, second form: a find that is gone as soon as gathering looks ends the frame at
// once; it must not be offered again every frame.
func TestPlaytest10_FindThatVanishesDoesNotThrash(t *testing.T) {
	pushes := travelRun(800, func(fsm *AgentFSM) bool { return fsm.ActiveActivityKind() != activityKindGather })
	if len(pushes) == 0 {
		t.Fatal("gathering was never offered: the scenario no longer reaches the case")
	}
	for from := int64(100); from < 900; from += workBackoffTicks {
		// The first push is ordinary work, not an interruption, and is not counted against it.
		if n := within(pushes, from, from+workBackoffTicks); n > thrashLimit+1 {
			t.Errorf("gather pushed %d times in %d ticks from %d", n, workBackoffTicks, from)
		}
	}
}

// An emergency does not wind back the standstill watch: a threat flickering every few seconds
// kept it from ever firing.
func TestPlaytest10_FlickeringEmergencyDoesNotHideAStandstill(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	w := &fsmWorld{fsm: fsm, ctx: &FSMContext{State: NewAgentState()}}
	for w.tick = 100; w.tick < 100+standstillTicks; w.tick += 4 {
		w.ctx.Frame = makeArbiterTestFrame(w.tick, 2000, 20, nil, nil)
		fsm.currentStage = StageSettlement
		if w.tick%60 < 8 {
			fsm.currentStage = StageEmergency
		}
		fsm.watchStandstill(w, nil)
	}
	if fsm.mark.since != 100 {
		t.Errorf("the watch was wound back to %d by the emergencies", fsm.mark.since)
	}
}
