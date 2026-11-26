package smoke

// cq-390.4 fishing smoke tests: the REAL System 1 kernel in the closed-loop eval.PlaytestSim.
// Telemetry fidelity follows the mod: surface water and fish are surfaced although the agent
// otherwise only sees blocks within 2 cells plus interesting blocks within 16.

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func fishCarried(s *eval.PlaytestSim) int32 {
	return s.ItemCount("minecraft:cod") + s.ItemCount("minecraft:salmon")
}

// assertNeverDrowned: wading in after a floating catch is allowed, suffering for it is not.
func assertNeverDrowned(t *testing.T, s *eval.PlaytestSim) {
	t.Helper()
	for _, st := range s.Steps {
		if st.HP < 20 || st.Air < 200 {
			t.Fatalf("agent drowning while fishing at t=%d hp=%.0f air=%d", st.Tick, st.HP, st.Air)
		}
	}
}

func assertStayedDry(t *testing.T, s *eval.PlaytestSim) {
	t.Helper()
	for _, st := range s.Steps {
		if st.Submerged {
			t.Fatalf("agent submerged while fishing at t=%d pos=%+v (must stay on the shore)", st.Tick, st.Pos)
		}
	}
}

// cq-390.4 (a): hungry, no string: melee a fish from the shore and pick up the drop.
func TestSmoke_FishingMeleeWithoutString(t *testing.T) {
	w := harvestWorld()
	w.AddPond(0, 14, 4)
	s := harvestSim(w, survivalKit(), 9)
	s.AddFish("cod", 0.5, 64.0, 11.0)
	s.AddFish("salmon", 1.5, 64.0, 12.5)

	ok := s.RunUntil(func(s *eval.PlaytestSim) bool { return fishCarried(s) > 0 }, 5*harvestSimMinute)
	if !ok {
		t.Fatalf("hungry agent never landed a fish by hand (kills=%d) inventory=%s", s.Fauna().FishKilled, s.InventorySummary())
	}
	if s.Fauna().FishKilled == 0 || s.Fauna().Casts != 0 {
		t.Errorf("want a melee kill and no casts: kills=%d casts=%d", s.Fauna().FishKilled, s.Fauna().Casts)
	}
	assertNeverDrowned(t, s)
}

// cq-390.4 (b): string and sticks beside a crafting table: craft the rod, cast at the pond, wait
// through the bobber, reel on the bite and land a cod.
func TestSmoke_FishingRodCraftCastReel(t *testing.T) {
	w := harvestWorld()
	w.AddPond(0, 14, 4)
	w.Blocks[eval.BlockPosKey{X: 2, Y: 64, Z: 0}] = "minecraft:crafting_table"
	inv := append(survivalKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:string", Count: 2},
		&civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 3})
	s := harvestSim(w, inv, 9)

	ok := s.RunUntil(func(s *eval.PlaytestSim) bool { return fishCarried(s) > 0 }, 6*harvestSimMinute)
	f := s.Fauna()
	if !ok {
		t.Fatalf("agent never caught a fish with a rod (casts=%d reels=%d crafts=%v) inventory=%s",
			f.Casts, f.Reels, s.Crafts, s.InventorySummary())
	}
	if s.ItemCount("minecraft:fishing_rod") != 1 {
		t.Errorf("rod was not crafted, crafts=%v", s.Crafts)
	}
	if f.Casts < 1 || f.Catches < 1 {
		t.Errorf("expected cast and catch, got casts=%d reels=%d catches=%d", f.Casts, f.Reels, f.Catches)
	}
	if f.Reels != f.Catches {
		t.Errorf("reeled %d times for %d catches: the agent reeled before the bite", f.Reels, f.Catches)
	}
	assertStayedDry(t, s)
}

// cq-390.4: fishing bids against other food by utility: livestock beside the agent beats a pond
// 14 blocks away.
func TestSmoke_FishingLosesToCloserLandFood(t *testing.T) {
	w := harvestWorld()
	w.AddPond(0, 14, 4)
	for z := int32(-2); z <= -2; z++ {
		w.Blocks[eval.BlockPosKey{X: 1, Y: 64, Z: z}] = "minecraft:carrots"
	}
	inv := append(survivalKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:fishing_rod", Count: 1})
	s := harvestSim(w, inv, 9)

	s.RunUntil(func(s *eval.PlaytestSim) bool { return s.ItemCount("minecraft:carrots") > 0 }, harvestSimMinute)
	if s.ItemCount("minecraft:carrots") == 0 {
		t.Fatalf("adjacent crop not harvested; inventory=%s", s.InventorySummary())
	}
	if s.Fauna().Casts != 0 {
		t.Errorf("walked off to fish although food was next to the agent (casts=%d)", s.Fauna().Casts)
	}
}
