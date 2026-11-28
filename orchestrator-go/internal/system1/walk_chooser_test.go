package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
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

// Play test 9: with a finished cabin elsewhere, agents stood out the night wherever work ended.
func TestPlaytest9_FinishedCabinIsWalkedHomeToAtNight(t *testing.T) {
	for _, stage := range []State{StageBootstrap, StageSettlement} {
		fsm := NewAgentFSMWithRegistry(stage, nil, NewDefaultEngineRegistry())
		st := NewAgentState()
		cfg := xdg.DefaultAgentIdentityConfig("homer")
		st.SetShelterPos(&civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0})
		st.SetCabinBuilt(blueprints.CabinTotalBlocks)
		home := false
		for tick := int64(100); tick < 400 && !home; tick += 4 {
			res := fsm.Tick(&FSMContext{AgentID: "homer", State: st, Config: cfg,
				Frame: makeArbiterTestFrame(tick, 14000, 20, fullSurvivalKitInv(), nil)})
			for _, in := range res.Intents {
				if n := in.Intent.GetNavigate(); n != nil && n.Target.X > 19 && n.Target.X < 22 {
					home = true
				}
			}
		}
		if !home {
			t.Errorf("stage %s: no walk home to the cabin at night", stage)
		}
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

// A directive's scouting waypoint that has been given up is dropped for another, and the
// forager does not choose a heading it has given up: both asked for it for ever once nothing
// took them off the stack (cq-3sa.1).
func TestGivenUpWaypointsAreReplaced(t *testing.T) {
	pos := &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	frame := makeArbiterTestFrame(1000, 2000, 20, nil, nil)
	st := NewAgentState()
	loc := &directiveLocal{}
	first := evaluateDirectiveScoutOutwardLocked("scout", frame, pos, 0, st, loc, 100).GetNavigate()
	st.RecordTargetNavigationStuck(first.Target.X, first.Target.Y, first.Target.Z, 1004, DefaultUnreachableTargetTTL, 1)
	frame = makeArbiterTestFrame(1200, 2000, 20, nil, nil)
	next := evaluateDirectiveScoutOutwardLocked("scout", frame, pos, 0, st, loc, 100).GetNavigate()
	if next == nil || next.Target.X == first.Target.X && next.Target.Z == first.Target.Z {
		t.Errorf("directive waypoint after giving up %v = %v, want a different one", first.Target, next.GetTarget())
	}

	st, mem := NewAgentState(), &forageLocal{}
	frame = makeArbiterTestFrame(1000, 2000, 4, nil, nil)
	walk := EvaluateForaging("forager", frame, st, mem).GetNavigate()
	st.RecordTargetNavigationStuck(walk.Target.X, walk.Target.Y, walk.Target.Z, 1004, DefaultUnreachableTargetTTL, 1)
	// The same spot and the same memory of ground covered, so the same first choice.
	frame = makeArbiterTestFrame(1000+forageExploreCooldownTicks*2, 2000, 4, nil, nil)
	again := EvaluateForaging("forager", frame, st, &forageLocal{}).GetNavigate()
	if again == nil || st.givenUp(again.Target.X, again.Target.Y, again.Target.Z, frame.WorldTick) {
		t.Errorf("forager chose %v again after giving up %v", again.GetTarget(), walk.Target)
	}
}
