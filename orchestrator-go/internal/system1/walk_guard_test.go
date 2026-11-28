package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
)

// Live smoke (cq-6l3.22): a hungry agent was sent to the same food drop 69 times while it stood
// still, because the path never started and nothing counted the attempts.
func TestWalk_FoodDropThatCannotBeReachedIsGivenUp(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	watchdogtest.Expect(t, fsm, watchdog.NoProgress, activityKindForaging, "stuck on purpose: the agent never moves")
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("stranded")
	cfg.Profession = "FARMER"
	drop := &civquestv1.VisibleEntity{EntityId: 7, EntityType: "minecraft:item", DisplayName: "Raw Beef",
		Position: &civquestv1.Vec3{X: 8.5, Y: 62, Z: 6.5}, Distance: 10}
	sent := 0
	for tick := int64(100); tick < 1700; tick += 4 {
		frame := makeArbiterTestFrame(tick, 3000, 4, fullSurvivalKitInv(), nil)
		frame.VisibleEntities = []*civquestv1.VisibleEntity{drop}
		res := fsm.Tick(&FSMContext{AgentID: "stranded", State: st, Config: cfg, Frame: frame})
		for _, in := range res.Intents {
			if n := in.Intent.GetNavigate(); n != nil && n.Target.X == drop.Position.X && n.Target.Z == drop.Position.Z {
				sent++
			}
		}
	}
	// Given up, and left alone until the unreachable mark expires (longer than this run).
	if sent == 0 || sent > 2*walkGiveUpSends+int(walkGiveUpTicks/4) {
		t.Errorf("walk to an unreachable drop sent %d times in 1600 ticks", sent)
	}
}

// A walk that is making headway, however often it is re-sent, is left alone.
func TestWalk_ProgressingWalkIsNotGivenUp(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("strider")
	cfg.Profession = "FARMER"
	drop := &civquestv1.VisibleEntity{EntityId: 7, EntityType: "minecraft:item", DisplayName: "Raw Beef",
		Position: &civquestv1.Vec3{X: 200.5, Y: 64, Z: 0.5}, Distance: 12}
	sent, steps := 0, 0
	for tick := int64(100); tick < 900; tick += 4 {
		frame := makeArbiterTestFrame(tick, 3000, 4, fullSurvivalKitInv(), nil)
		frame.Status.Position.X = 190 + float64(steps)*0.03
		frame.VisibleEntities = []*civquestv1.VisibleEntity{drop}
		res := fsm.Tick(&FSMContext{AgentID: "strider", State: st, Config: cfg, Frame: frame})
		steps++
		for _, in := range res.Intents {
			if in.Intent.GetNavigate() != nil {
				sent++
			}
		}
	}
	if st.IsTargetUnreachable(200.5, 64, 0.5, 900) || sent < 100 {
		t.Errorf("a walk making headway was given up: sent=%d", sent)
	}
}
