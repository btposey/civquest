package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestShelterRest_DeclaredOnlyWhenNothingIsLeftToDo(t *testing.T) {
	torch := []*civquestv1.ItemStackProto{{ItemId: "minecraft:torch", Count: 4}}
	lit := []*civquestv1.VisibleBlock{{BlockId: "minecraft:torch", Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}}}
	cases := []struct {
		name  string
		rests bool
		edit  func(*fsmWorld, *shelterLocal, *civquestv1.TelemetryFrame)
	}{
		{"at a lit site in the sleep routine", true, func(*fsmWorld, *shelterLocal, *civquestv1.TelemetryFrame) {}},
		{"another routine", false, func(w *fsmWorld, _ *shelterLocal, _ *civquestv1.TelemetryFrame) {
			w.fsm.currentRoutine = RoutinePrimaryDuty
		}},
		{"away from the site", false, func(_ *fsmWorld, l *shelterLocal, _ *civquestv1.TelemetryFrame) { l.Pos.X = 20 }},
		{"no site", false, func(_ *fsmWorld, l *shelterLocal, _ *civquestv1.TelemetryFrame) { l.Pos = nil }},
		{"a walk under way", false, func(_ *fsmWorld, _ *shelterLocal, f *civquestv1.TelemetryFrame) {
			f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
		}},
		{"blocks not being used", false, func(_ *fsmWorld, _ *shelterLocal, f *civquestv1.TelemetryFrame) {
			f.Status.Inventory = append(f.Status.Inventory, &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 8})
		}},
		{"a light that is not up", false, func(_ *fsmWorld, _ *shelterLocal, f *civquestv1.TelemetryFrame) { f.InterestingBlocks = nil }},
	}
	for _, tc := range cases {
		frame := makeArbiterTestFrame(100, 2000, 20, torch, lit)
		frame.Status.Position = &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
		w := &fsmWorld{fsm: &AgentFSM{currentRoutine: RoutineSleep}, ctx: &FSMContext{Frame: frame}}
		l := shelterLocal{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}}
		tc.edit(w, &l, frame)
		if why := w.shelterRest(l); (why != "") != tc.rests {
			t.Errorf("%s: rest %q, want declared=%v", tc.name, why, tc.rests)
		}
	}
}

// A guard that sleeps by day with a light that never appears is not resting: the quiet frame
// declares nothing and is reported. With the light up it rests and is left in peace.
func TestShelterRest_UndeclaredQuietIsStillReported(t *testing.T) {
	for _, lit := range []bool{true, false} {
		name := map[bool]string{true: "lit", false: "light never appears"}[lit]
		t.Run(name, func(t *testing.T) {
			fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
			if !lit {
				watchdogtest.Expect(t, fsm, watchdog.NoProgress, activityKindShelter, "stuck on purpose: the torch never shows")
			}
			st := NewAgentState()
			st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
			st.SetCollectiveSettlementSite(0.5, 64, 0.5)
			st.ObserveFarming(canFarm)
			st.AssignStrategy(StrategyProfession, 0)
			cfg := xdg.DefaultAgentIdentityConfig("guard")
			cfg.Profession = "GUARD"
			got := reports(fsm)
			var blocks []*civquestv1.VisibleBlock
			if lit {
				blocks = []*civquestv1.VisibleBlock{{BlockId: "minecraft:torch", Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}}}
			}
			inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}, {ItemId: "minecraft:wooden_sword", Count: 1}, {ItemId: "minecraft:torch", Count: 4}}
			rested := false
			for tick := int64(100); tick < 3100; tick += 4 {
				frame := makeArbiterTestFrame(tick, 2000+tick, 20, inv, blocks)
				frame.Status.Position = &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
				fsm.Tick(&FSMContext{AgentID: "guard", State: st, Config: cfg, Frame: frame})
				for _, f := range fsm.ActivityFrames() {
					rested = rested || f.Rest != ""
				}
			}
			if rested != lit {
				t.Errorf("rest declared = %v, want %v (stack %v)", rested, lit, fsm.ActivityFrames())
			}
			if lit && len(*got) != 0 {
				t.Errorf("a declared rest was reported: %v", (*got)[0].details)
			}
		})
	}
}
