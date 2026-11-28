package integration

// cq-3ru: with a settlement, cabins go on spaced plots round it, a started cabin is finished,
// and far from home the night is spent in a temporary shelter.

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func settledBuilder(t *testing.T, id string, reg *db.CivicRegistryDB, start eval.SimVec, tod int64) (*eval.PlaytestSim, *system1.AgentState) {
	t.Helper()
	var state *system1.AgentState
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		AgentID: id, Profession: "EXPLORER", Stage: system1.StageBootstrap, World: groundWorld(48), TimeOfDay: tod,
		Start: start, Inventory: append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 100}),
		Setup: func(st *system1.AgentState) {
			state = st
			st.SetBedRegistry(reg)
			st.SetCollectiveSettlementSite(0.5, 64, 0.5)
		},
	})
	return s, state
}

func TestSettlement_CabinsStandOnSeparatePlotsRoundTheSite(t *testing.T) {
	reg, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic.db"), "clan")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	var cabins []*civquestv1.BlockPosProto
	// Both start on the same spot, away from the site.
	for _, id := range []string{"able", "bert", "carl"} {
		s, state := settledBuilder(t, id, reg, eval.SimVec{X: 30.5, Y: 64, Z: 30.5}, 11800)
		if !s.RunUntil(func(*eval.PlaytestSim) bool { return state.GetCabinBuilt() >= blueprints.CabinTotalBlocks }, 8*simTicksPerMinute) {
			t.Fatalf("%s: cabin not finished: %d blocks, site %v, pos %+v\n%s", id, state.GetCabinBuilt(), state.GetShelterPos(), s.Pos,
				traceTail(s, len(s.Steps)-30, len(s.Steps)))
		}
		cabins = append(cabins, state.GetShelterPos())
	}
	for i, c := range cabins {
		if d := math.Hypot(float64(c.X), float64(c.Z)); d < 5 || d > 30 {
			t.Errorf("cabin %d at %v is %.0f blocks from the site", i, c, d)
		}
		for _, o := range cabins[:i] {
			if max(math.Abs(float64(c.X-o.X)), math.Abs(float64(c.Z-o.Z))) < 9 {
				t.Errorf("cabins at %v and %v crowd each other", o, c)
			}
		}
	}
}

func TestSettlement_FarFromHomeShelterIsTemporaryAndLeftInTheMorning(t *testing.T) {
	reg, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic.db"), "clan")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	// 140 blocks from the site, at night.
	var state *system1.AgentState
	w := eval.NewBlankWorld()
	w.FillGround(110, -30, 170, 30, "")
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 14000,
		Start: eval.SimVec{X: 140.5, Y: 64, Z: 0.5}, Inventory: append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 40}),
		Setup: func(st *system1.AgentState) {
			state = st
			st.SetBedRegistry(reg)
			st.SetCollectiveSettlementSite(0.5, 64, 0.5)
		},
	})
	door := []eval.BlockPosKey{{X: 140, Y: 64, Z: 1}, {X: 140, Y: 65, Z: 1}}
	closed := func(s *eval.PlaytestSim) bool { return s.BlockAt(door[0]) != "" && s.BlockAt(door[1]) != "" }
	if !s.RunUntil(closed, 2*simTicksPerMinute) {
		t.Fatalf("no temporary shelter (placed %v)\n%s", s.Placed, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	if len(s.Placed) != 9 || state.GetCabinBuilt() != 0 {
		t.Errorf("placed %d blocks and %d of a cabin, want the nine of a temporary shelter", len(s.Placed), state.GetCabinBuilt())
	}
	s.RunUntil(func(s *eval.PlaytestSim) bool { return s.TOD%24000 >= 22000 }, 8*simTicksPerMinute)
	if !closed(s) || len(s.Harvests) != 0 || math.Floor(s.Pos.X) != 140 || math.Floor(s.Pos.Z) != 0 {
		t.Fatalf("night not spent inside: closed=%v breaks=%d pos=%+v", closed(s), len(s.Harvests), s.Pos)
	}
	if !s.RunUntil(func(s *eval.PlaytestSim) bool { return s.BlockAt(door[0]) == "" && s.BlockAt(door[1]) == "" }, 5*simTicksPerMinute) {
		t.Fatalf("not left in the morning (tod %d)\n%s", s.TOD%24000, traceTail(s, len(s.Steps)-30, len(s.Steps)))
	}
	for _, h := range s.Harvests {
		if h.Cell != door[0] && h.Cell != door[1] {
			t.Errorf("broke %v, outside the doorway", h.Cell)
		}
	}
	if state.GetCabinBuilt() != 0 {
		t.Errorf("a cabin was started %d blocks out", 140)
	}
}
