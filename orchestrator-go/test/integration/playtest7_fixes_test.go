package integration

// Play test 7 fixes (cq-d23): walking circuits, stone dug where its drop is lost, drops left
// lying, and cabins that demanded oak. Each scenario drives the real System 1 kernel through
// the closed-loop synthetic world and asserts an outcome.

import (
	"math"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// UNFIXED BEHAVIOUR: bert walked for nine minutes between a cabin and a remembered tree
// sighting that hung in mid-air, and agents spun in place over points under their own feet.
func TestPlaytest7_01_ImpossibleSightingIsDroppedAndTheAgentLooksAgain(t *testing.T) {
	w := groundWorld(40)
	const sx, sy, sz = 18.5, 80.0, 0.5
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 2000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: nil,
		Setup: func(st *system1.AgentState) {
			st.SpatialIndex().UpsertPOI(db.AgentPOI{
				POIID: "sighting", Category: system1.POICategoryResource, BlockID: "minecraft:oak_log",
				X: sx, Y: sy, Z: sz, AccessX: sx, AccessY: sy, AccessZ: sz,
				ElevationLayer: system1.ElevationSurface, Reliability: 0.9, IsActive: true,
			})
		},
	})
	s.Run(3 * simTicksPerMinute)

	toSighting, lateToSighting, surveys := 0, 0, 0
	for _, step := range s.Steps {
		for _, in := range step.Intents {
			if in.GetSurveyHorizon() != nil {
				surveys++
			}
			if n := in.GetNavigate(); n != nil && math.Hypot(n.Target.X-sx, n.Target.Z-sz) < 0.6 {
				toSighting++
				if step.Tick > 90+simTicksPerMinute {
					lateToSighting++
				}
			}
		}
	}
	if toSighting == 0 {
		t.Fatalf("setup: the agent never tried the remembered sighting\n%s", traceTail(s, 0, 30))
	}
	if lateToSighting > 0 {
		t.Errorf("still walking to the impossible point after a minute (%d orders, %d in all)", lateToSighting, toSighting)
	}
	if surveys == 0 {
		t.Errorf("never looked round again for a new bearing")
	}
}

// UNFIXED BEHAVIOUR: agents dug cobblestone from under a layer of dirt and left it in the hole.
func TestPlaytest7_02_StoneIsNotDugWhereItsDropIsLost(t *testing.T) {
	w := groundWorld(30)
	// Stone two below the feet, bared by a one-block hole; an outcrop a short walk off.
	for _, x := range []int32{-1, 1} { // beside the feet, where the sensors look downward
		delete(w.Blocks, eval.BlockPosKey{X: x, Y: 63, Z: 0})
		w.Blocks[eval.BlockPosKey{X: x, Y: 62, Z: 0}] = "minecraft:stone"
	}
	w.AddStoneOutcrop(-6, -1, 3, 3)
	inv := append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_pickaxe", Count: 1})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageSettlement, World: w, TimeOfDay: 3000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
		Setup: func(st *system1.AgentState) {
			st.SetStrategicDirective(system1.DirectiveActionMineStone, "stone", "gather cobblestone", 90, 1_000_000)
		},
	})
	s.Run(2 * simTicksPerMinute)

	for _, h := range s.Harvests {
		if strings.Contains(h.BlockID, "stone") && h.Cell.Y < 63 {
			t.Errorf("dug %s at %v, two blocks under the feet", h.BlockID, h.Cell)
		}
	}
	if s.ItemCount("minecraft:cobblestone") < 1 {
		t.Errorf("no cobblestone from the outcrop (inventory: %s)", s.InventorySummary())
	}
}

// UNFIXED BEHAVIOUR: cabins asked for oak slabs in acacia country and were left unroofed.
func TestPlaytest7_03_CabinIsBuiltFromWhateverWoodIsHeld(t *testing.T) {
	w := groundWorld(30)
	inv := append(simKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:acacia_planks", Count: 96})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 11800,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
	})
	s.Run(5 * simTicksPerMinute)

	roof := 0
	for _, k := range s.Placed {
		id := w.Blocks[k]
		if strings.Contains(id, "oak_") {
			t.Errorf("placed %s at %v with no oak held", id, k)
		}
		if k.Y == 68 { // the 3x3 roof, four above the floor
			roof++
		}
	}
	// The simulator places by plain item ID, so the door and torches are not counted.
	if len(s.Placed) < 70 || roof != 9 {
		t.Errorf("placed %d blocks, %d of them roof; want most of an acacia cabin with a roof", len(s.Placed), roof)
	}
}
