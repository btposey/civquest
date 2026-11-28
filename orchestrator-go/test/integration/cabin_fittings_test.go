package integration

// cq-390.8 (user, play test 11): cabins need doors to keep the agents safe at night, and
// windows filled with a pane, else an upright trapdoor or a fence. A fitting that cannot be
// had never holds the shelter up.

import (
	"math"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// buildCabin runs an agent at dusk with the given pack until its cabin is finished.
func buildCabin(t *testing.T, extra ...*civquestv1.ItemStackProto) (*eval.PlaytestSim, *system1.AgentState) {
	t.Helper()
	return buildCabinIn(t, groundWorld(30), extra...)
}

func buildCabinIn(t *testing.T, w *eval.SyntheticStartingWorld, extra ...*civquestv1.ItemStackProto) (*eval.PlaytestSim, *system1.AgentState) {
	t.Helper()
	var state *system1.AgentState
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 11800,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: append(simKit(), extra...),
		Setup: func(st *system1.AgentState) { state = st },
	})
	if !s.RunUntil(func(*eval.PlaytestSim) bool { return state.GetCabinBuilt() >= blueprints.CabinTotalBlocks }, 8*simTicksPerMinute) {
		t.Fatalf("cabin not finished: %d of %d blocks (inventory: %s)\n%s", state.GetCabinBuilt(),
			blueprints.CabinTotalBlocks, s.InventorySummary(), traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	return s, state
}

// placedIn lists the blocks the agent placed whose ID ends in suffix.
func placedIn(s *eval.PlaytestSim, suffix string) []eval.BlockPosKey {
	var out []eval.BlockPosKey
	for _, k := range s.Placed {
		if strings.HasSuffix(s.World.Blocks[k], suffix) {
			out = append(out, k)
		}
	}
	return out
}

func TestCabin_DoorIsCraftedAndHungAndWindowsFilled(t *testing.T) {
	// Wood and nothing made of it: no table, door or window fill in the pack.
	s, _ := buildCabin(t, &civquestv1.ItemStackProto{ItemId: "minecraft:spruce_planks", Count: 100})

	doors := placedIn(s, "_door")
	if len(doors) != 1 {
		t.Fatalf("doors hung = %d, want 1 (inventory: %s)", len(doors), s.InventorySummary())
	}
	d := doors[0]
	if up := s.World.Blocks[eval.BlockPosKey{X: d.X, Y: d.Y + 1, Z: d.Z}]; up != "minecraft:spruce_door" {
		t.Errorf("upper half is %q", up)
	}
	windows := placedIn(s, "_trapdoor")
	if len(windows) != 2 {
		t.Fatalf("windows filled = %d, want 2 (inventory: %s)", len(windows), s.InventorySummary())
	}
	for _, w := range windows {
		if state := s.World.BlockStates[w]; !strings.Contains(state, "open=true") || w.Y != d.Y+1 {
			t.Errorf("window at %v is %q, want an upright trapdoor level with the door's upper half", w, state)
		}
	}
}

func TestCabin_HeldPanesAndDoorAreUsedAsTheyAre(t *testing.T) {
	s, _ := buildCabin(t, &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 100},
		&civquestv1.ItemStackProto{ItemId: "minecraft:birch_door", Count: 1},
		&civquestv1.ItemStackProto{ItemId: "minecraft:oak_fence", Count: 2},
		&civquestv1.ItemStackProto{ItemId: "minecraft:glass_pane", Count: 1})

	if len(placedIn(s, "birch_door")) != 1 || len(placedIn(s, "glass_pane")) != 1 || len(placedIn(s, "oak_fence")) != 1 {
		t.Errorf("want the held door, the pane first and then a fence (inventory: %s)", s.InventorySummary())
	}
	for _, c := range s.CraftIssued {
		if strings.HasSuffix(c.Recipe, "door") {
			t.Errorf("crafted %s with a door and window fills in the pack", c.Recipe)
		}
	}
}

func TestCabin_FinishedWithoutWoodForItsFittings(t *testing.T) {
	// Cobblestone and no tree anywhere: the walls go up, the door cannot be made.
	s, _ := buildCabin(t, &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 100})

	if n := len(placedIn(s, "_door")) + len(placedIn(s, "_trapdoor")); n != 0 {
		t.Errorf("%d fittings from no wood", n)
	}
	// 79 blocks of structure less the torch, and both windows walled up.
	if n := len(placedIn(s, "cobblestone")); n < 78 {
		t.Errorf("placed %d cobblestone blocks, want the structure and both windows", n)
	}
}

func TestCabin_TimberIsFetchedForTheDoor(t *testing.T) {
	// The walls are cobblestone; the wood for a table, a door and the windows is still in
	// trees 14 blocks north of the site.
	w := groundWorld(30)
	for x := int32(-1); x <= 1; x++ {
		for y := int32(64); y <= 67; y++ {
			w.Blocks[eval.BlockPosKey{X: x, Y: y, Z: -14}] = "minecraft:oak_log"
		}
	}
	s, _ := buildCabinIn(t, w, &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 100})

	if len(placedIn(s, "oak_door")) != 1 {
		t.Errorf("no door hung with trees to hand (inventory: %s)\n%s", s.InventorySummary(),
			traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
}

// cq-8jf (user: favour protection): an opening with no fitting is blocked with a temporary
// block, the doorway from inside for the night; the walls are never touched, the doorway is
// opened in the morning, and fittings held later replace the temporary blocks.
func TestCabin_SealedForTheNightOpenedInTheMorningAndFittedLater(t *testing.T) {
	s, state := buildCabinIn(t, groundWorld(30), &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 100})
	site := state.GetShelterPos()
	at := func(dx, dy, dz int32) eval.BlockPosKey {
		return eval.BlockPosKey{X: site.X + dx, Y: site.Y + dy, Z: site.Z + dz}
	}
	door := []eval.BlockPosKey{at(0, 1, 2), at(0, 2, 2)}
	windows := []eval.BlockPosKey{at(-2, 2, 0), at(2, 2, 0)}
	inside := func(s *eval.PlaytestSim) bool {
		return math.Abs(s.Pos.X-float64(site.X)-0.5) < 1.5 && math.Abs(s.Pos.Z-float64(site.Z)-0.5) < 1.5
	}
	sealed := func(s *eval.PlaytestSim) bool {
		return s.BlockAt(door[0]) == "minecraft:cobblestone" && s.BlockAt(door[1]) == "minecraft:cobblestone"
	}
	harvested := len(s.Harvests)
	// The sim walks in straight lines: put the builder on the doorway's side.
	s.Pos = eval.SimVec{X: float64(site.X) + 0.5, Y: float64(site.Y), Z: float64(site.Z) + 3.5}

	if !s.RunUntil(func(s *eval.PlaytestSim) bool { return sealed(s) && inside(s) }, 2*simTicksPerMinute) {
		t.Fatalf("doorway not sealed from inside: door %q/%q pos %+v\n%s", s.BlockAt(door[0]), s.BlockAt(door[1]), s.Pos,
			traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	for _, w := range windows {
		if s.BlockAt(w) != "minecraft:cobblestone" {
			t.Errorf("window %v is %q, want blocked", w, s.BlockAt(w))
		}
	}
	s.RunUntil(func(s *eval.PlaytestSim) bool { return s.TOD%24000 >= 22000 }, 12*simTicksPerMinute)
	if !sealed(s) || !inside(s) || len(s.Harvests) != harvested {
		t.Fatalf("night not spent sealed in: sealed=%v inside=%v breaks=%d", sealed(s), inside(s), len(s.Harvests)-harvested)
	}

	if !s.RunUntil(func(s *eval.PlaytestSim) bool { return s.BlockAt(door[0]) == "" && s.BlockAt(door[1]) == "" }, 5*simTicksPerMinute) {
		t.Fatalf("doorway not opened in the morning (tod %d)\n%s", s.TOD%24000, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	for _, h := range s.Harvests[harvested:] {
		if h.Cell != door[0] && h.Cell != door[1] {
			t.Errorf("broke %v (%s), outside the doorway", h.Cell, h.BlockID)
		}
	}

	s.AddItem("minecraft:oak_door", 1)
	s.AddItem("minecraft:glass_pane", 2)
	s.Pos = eval.SimVec{X: float64(site.X) + 0.5, Y: float64(site.Y + 1), Z: float64(site.Z) + 0.5}
	if !s.RunUntil(func(s *eval.PlaytestSim) bool {
		return s.BlockAt(door[0]) == "minecraft:oak_door" && s.BlockAt(windows[0]) == "minecraft:glass_pane" &&
			s.BlockAt(windows[1]) == "minecraft:glass_pane"
	}, 3*simTicksPerMinute) {
		t.Fatalf("fittings not put in: %q %q %q (%s)\n%s", s.BlockAt(door[0]), s.BlockAt(windows[0]), s.BlockAt(windows[1]),
			s.InventorySummary(), traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
}
