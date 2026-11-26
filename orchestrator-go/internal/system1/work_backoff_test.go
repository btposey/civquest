package system1

import (
	"testing"

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

// An agent without a bed and with nothing to make one from pushed the bed plan every tick.
func TestBed_NothingToProgressBacksOff(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	st.SetBedRegistry(newFakeBedRegistry())
	cfg := xdg.DefaultAgentIdentityConfig("bedless")
	cfg.Profession = "FARMER"
	pushes := countPushes(fsm)
	for tick := int64(100); tick < 900; tick += 4 {
		fsm.Tick(&FSMContext{AgentID: "bedless", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(tick, 3000, 20, fullSurvivalKitInv(), nil)})
	}
	limit := int(800/workBackoffTicks) + 1
	if got := pushes[activityKindBed]; got == 0 || got > limit {
		t.Fatalf("bed plan pushed %d times, want 1..%d", got, limit)
	}
}
