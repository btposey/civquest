package system1

import (
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// hutBlueprint is a 2x1x2 plank hut: 4 blocks, one per quadrant.
func hutBlueprint() *blueprints.Blueprint {
	return &blueprints.Blueprint{
		ID: "test_hut", Name: "Test Hut", Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{Width: 2, Height: 1, Length: 2},
		Palette:    map[int]string{1: "minecraft:oak_planks"},
		Layers:     []blueprints.Layer{{Y: 0, Matrix: [][]int{{1, 1}, {1, 1}}}},
	}
}

func hutRegistry() *EngineRegistry {
	reg := NewDefaultEngineRegistry()
	reg.RegisterEvaluator(NewBlueprintEvaluator(hutBlueprint()))
	return reg
}

func settlementKit() []*civquestv1.ItemStackProto {
	return append(survivalKit(), item(3, "minecraft:oak_planks", 16))
}

func countPlaces(tr []replayRecord) int {
	n := 0
	for _, r := range tr {
		for _, s := range r.Intents {
			if strings.HasPrefix(s, "Interact(PLACE_BLOCK") {
				n++
			}
		}
	}
	return n
}

// cq-gpa.6 regression: a SETTLEMENT agent with an affordable recommended blueprint and a home
// must build it (navigate to the site, then PLACE_BLOCK every block, then stop). Before the
// construction activity existed, nothing drove ConstructionSkill and the trace never held a
// single placement.
func scenarioSettlementBuildsRecommendedBlueprint() replayScenario {
	home := func(st *AgentState) { st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}) }
	at := func(tick int64) *civquestv1.TelemetryFrame {
		// Standing on the site (anchor = home + 8 east) so every block is within reach.
		return rf(tick, 3000, withInv(settlementKit()...), withPos(9, 64, 1))
	}
	steps := []replayStep{{Frame: at(100), Setup: home, Annotation: "settlement, recommended blueprint affordable: construction starts"}}
	for i := int64(1); i <= 8; i++ {
		steps = append(steps, replayStep{Frame: at(100 + i*6), Annotation: "next block"})
	}
	return replayScenario{
		Name: "settlement_builds_recommended_blueprint", Stage: StageSettlement, Profession: "BUILDER",
		Registry: hutRegistry(),
		Steps:    steps,
		Check: func(t *testing.T, tr []replayRecord) {
			if got := countPlaces(tr); got != 4 {
				t.Fatalf("want exactly 4 PLACE_BLOCK intents (one per hut block), got %d:\n%s", got, renderTrace(tr))
			}
			if !hasIntentPrefix(tr[0], "Interact(PLACE_BLOCK 8,64,0") && !hasIntentPrefix(tr[0], "Navigate(") {
				t.Fatalf("first step must start the build: %s", tr[0])
			}
		},
	}
}

func TestConstructionBanksProgressAndFinishedBlueprintIsNotRebuilt(t *testing.T) {
	sc := scenarioSettlementBuildsRecommendedBlueprint()
	runReplay(t, sc)
	// A second pass over a fresh state must not matter; here the property is about the state:
	// replay a longer script and assert no placement after completion.
	long := sc
	long.Name = "long"
	for i := int64(9); i <= 20; i++ {
		long.Steps = append(long.Steps, replayStep{Frame: rf(100+i*6, 3000, withInv(settlementKit()...), withPos(9, 64, 1))})
	}
	tr := runReplay(t, long)
	if got := countPlaces(tr); got != 4 {
		t.Fatalf("a finished blueprint must not be rebuilt; places=%d\n%s", got, renderTrace(tr))
	}
}

func TestConstructionNotEligibleAtNight(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage State
		tod   int64
	}{{"settlement night", StageSettlement, 16000}, {"settlement dusk", StageSettlement, 12500}} {
		sc := scenarioSettlementBuildsRecommendedBlueprint()
		sc.Stage = tc.stage
		sc.Steps = nil
		home := func(st *AgentState) { st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}) }
		for i := int64(0); i < 6; i++ {
			sc.Steps = append(sc.Steps, replayStep{Frame: rf(100+i*6, tc.tod, withInv(settlementKit()...), withPos(9, 64, 1)), Setup: home})
		}
		if n := countPlaces(runReplay(t, sc)); n != 0 {
			t.Fatalf("%s: construction must not place blocks, got %d", tc.name, n)
		}
	}
}
