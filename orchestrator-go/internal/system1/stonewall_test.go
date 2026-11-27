package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func countEvents(fsm *AgentFSM) map[string]int {
	got := map[string]int{}
	fsm.SetEventSink(func(_ int64, category, _ string, _ map[string]any) { got[category]++ })
	return got
}

// Play test 9: boxed in beside its own cabin site, the shelter asked 7575 times to step out of
// the way of the next block, a walk already given up, and did nothing else.
func TestPlaytest9_StepOutOfTheWayGivenUpIsNotAskedFor(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("dave")
	events := countEvents(fsm)
	inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:oak_planks", Count: 64}}
	asked := 0
	// The agent never moves, whatever it asks for.
	for tick := int64(100); tick < 2400; tick += 4 {
		res := fsm.Tick(&FSMContext{AgentID: "dave", State: st, Config: cfg, Frame: makeArbiterTestFrame(tick, 14000, 20, inv, nil)})
		for _, in := range res.Intents {
			if in.Intent.GetNavigate() != nil {
				asked++
			}
		}
	}
	if asked == 0 {
		t.Fatal("the scenario no longer asks to step out of the way")
	}
	if events[EventWalkGivenUp] > 12 {
		t.Errorf("%d walks asked for after being given up (one report per walk allowed)", events[EventWalkGivenUp])
	}
}

// Whoever asks: an activity that only asks for withheld walks gives way, and says so once.
func TestPlaytest9_ActivityRefusedForLongGivesWay(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	events := countEvents(fsm)
	w := &fsmWorld{fsm: fsm}
	for w.tick = 100; w.tick < 100+refusalPatience; w.tick += 4 {
		fsm.refused(w, activityKindShelter)
	}
	if w.stonewalled(activityKindShelter) {
		t.Fatal("gave way before its patience ran out")
	}
	fsm.refused(w, activityKindShelter)
	if !w.stonewalled(activityKindShelter) || events[EventStandstill] != 1 {
		t.Fatalf("stonewalled=%v events=%v", w.stonewalled(activityKindShelter), events)
	}
	// Something done in between starts the patience again.
	for w.tick = 5000; w.tick < 5000+2*refusalPatience; w.tick += 100 {
		fsm.refused(w, activityKindForaging)
		fsm.served(activityKindForaging)
	}
	if w.stonewalled(activityKindForaging) {
		t.Error("an activity that found other work was made to give way")
	}
}

// An objective that holds the stack through the day without moving or doing anything gives way.
func TestPlaytest9_DaytimeStandstillGivesWay(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("stander")
	cfg.Profession = "EXPLORER"
	events := countEvents(fsm)
	for tick := int64(100); tick < 100+3*standstillTicks; tick += 4 {
		f := makeArbiterTestFrame(tick, 2000, 20, fullSurvivalKitInv(), nil)
		f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING // says it walks, never moves
		fsm.Tick(&FSMContext{AgentID: "stander", State: st, Config: cfg, Frame: f})
	}
	if events[EventStandstill] == 0 {
		t.Errorf("no activity gave way after standing still all morning: %v", events)
	}
}

// A scouting leg whose destination has been given up is dropped for another at once.
func TestPlaytest9_GivenUpScoutingLegIsReplaced(t *testing.T) {
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("scout")
	cfg.Profession = "EXPLORER"
	pos := &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	walk := &walkLocal{}
	frame := makeArbiterTestFrame(1000, 2000, 20, nil, nil)
	first := EvaluateScoutingDirect("scout", frame, pos, st, cfg, walk).GetNavigate()
	if first == nil {
		t.Fatal("no first leg")
	}
	st.RecordTargetNavigationStuck(first.Target.X, first.Target.Y, first.Target.Z, 1004, DefaultUnreachableTargetTTL, 1)
	frame = makeArbiterTestFrame(1008, 2000, 20, nil, nil)
	next := EvaluateScoutingDirect("scout", frame, pos, st, cfg, walk).GetNavigate()
	if next == nil || next.Target.X == first.Target.X && next.Target.Z == first.Target.Z {
		t.Errorf("leg after giving up %v = %v, want a different one", first.Target, next.GetTarget())
	}
}
