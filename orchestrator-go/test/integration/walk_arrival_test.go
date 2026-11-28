package integration

// Play test 11 (cq-kne): able and bert stood still for minutes sending walks the mod reported
// done at once: the activity asked for a place the agent was already at, learnt nothing from
// getting there, and asked again.

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// A remembered tree is far from where it was seen from, and the agent is already at that spot
// with no tree to fell: the walk there is over, so the sighting is dropped and something else done.
func TestPlaytest11_RememberedTreeWalkThatIsOverIsNotRepeated(t *testing.T) {
	start := eval.SimVec{X: 0.5, Y: 64, Z: 0.5}
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "RANCHER", Stage: system1.StageSettlement, World: groundWorld(60), TimeOfDay: 2000,
		Start: start, Inventory: simKit(),
		Setup: func(st *system1.AgentState) {
			st.SpatialIndex().UpsertPOI(db.AgentPOI{
				POIID: "poi_resource_12_73_0", Category: system1.POICategoryResource, BlockID: "minecraft:oak_log",
				X: 12.5, Y: 73, Z: 0.5, AccessX: 1, AccessY: 65, AccessZ: 0.5,
				ElevationLayer: system1.ElevationSurface, SafetyScore: 1, Reliability: 1, IsActive: true,
			})
			st.SetStrategicDirective(system1.DirectiveActionGatherTimber, "", "gather timber", 100, 100_000)
		},
	})
	s.Run(simTicksPerMinute)

	asked := 0
	for _, step := range s.Steps {
		for _, in := range step.Intents {
			if to := in.GetNavigate().GetTarget(); to != nil && to.X == 1 && to.Y == 65 {
				asked++
			}
		}
	}
	if asked > 2 {
		t.Errorf("asked %d times to walk to a spot it was already at\n%s", asked, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	if d := math.Hypot(s.Pos.X-start.X, s.Pos.Z-start.Z); d < 6 {
		t.Errorf("still %.1f blocks from the start after a minute\n%s", d, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
}

// Terrain exploration picks a notch between cliffs. A notch a step away is nothing to walk to:
// taken each time it is reached, the agent shuffles on the spot for ever.
func TestPlaytest11_TerrainFeatureAtTheFeetIsNotWalkedToForEver(t *testing.T) {
	w := groundWorld(60)
	for _, c := range [][2]int32{{7, 0}, {-7, 0}, {0, 7}, {0, -7}, {5, 5}, {-5, -5}, {5, -5}, {-5, 5}} {
		for y := int32(64); y <= 68; y++ {
			w.Blocks[eval.BlockPosKey{X: c[0], Y: y, Z: c[1]}] = "minecraft:stone"
		}
	}
	start := eval.SimVec{X: 0.5, Y: 64, Z: 0.5}
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageSettlement, World: w, TimeOfDay: 2000,
		Start: start, Inventory: simKit(),
	})
	short, walks := 0, 0
	for tick := int64(0); tick < simTicksPerMinute; tick += 4 {
		step := s.Step()
		for _, in := range step.Intents {
			if to := in.GetNavigate().GetTarget(); to != nil {
				walks++
				if math.Hypot(to.X-step.Pos.X, to.Z-step.Pos.Z) <= 2.5 {
					short++
				}
			}
		}
	}
	if walks == 0 {
		t.Fatalf("the scenario never walked\n%s", traceTail(s, 0, 40))
	}
	if short > 2 {
		t.Errorf("%d of %d walks asked for a place within arrival of the agent\n%s", short, walks, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
}
