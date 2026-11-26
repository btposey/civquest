package smoke

// cq-390.3 wool smoke tests: the REAL System 1 kernel driven through the closed-loop
// eval.PlaytestSim, asserting outcomes (wool in the inventory, shearing preferred over killing,
// kill as the fallback) rather than single frames.

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const harvestSimMinute = 20 * 60

func harvestWorld() *eval.SyntheticStartingWorld {
	w := eval.NewBlankWorld()
	w.FillGround(-30, -30, 30, 30, "")
	return w
}

func harvestSim(w *eval.SyntheticStartingWorld, inv []*civquestv1.ItemStackProto, food int32) *eval.PlaytestSim {
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "FARMER", Stage: system1.StageBootstrap, World: w,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
	})
	s.AddRecipe(eval.FishingRodRecipe())
	s.Food = food
	return s
}

func countIntents(s *eval.PlaytestSim, pred func(*civquestv1.AgentIntent) bool) int {
	n := 0
	for _, in := range s.Intents() {
		if pred(in) {
			n++
		}
	}
	return n
}

func isShear(in *civquestv1.AgentIntent) bool {
	return in.GetUseItem().GetMode() == civquestv1.UseItemIntent_SHEAR_ENTITY
}

// cq-390.3: with shears carried the agent shears sheep (the flock survives) instead of killing
// them and collects the wool drops. Any colour counts; a sheared sheep is not re-shorn and a
// baby is never touched.
func TestSmoke_WoolShearingPreferredOverKilling(t *testing.T) {
	s := harvestSim(harvestWorld(), append(survivalKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:shears", Count: 1}), 20)
	a := s.AddSheep(8, 2, "black")
	b := s.AddSheep(-6, 5, "black")
	baby := s.AddSheep(4, -6, "black")
	baby.Baby = true

	ok := s.RunUntil(func(s *eval.PlaytestSim) bool { return s.ItemCount("minecraft:black_wool") >= 3 }, 4*harvestSimMinute)
	f := s.Fauna()
	if !ok {
		t.Fatalf("agent never gathered 3 wool; shears=%d kills=%d inventory=%s", f.Shears, f.SheepKilled, s.InventorySummary())
	}
	if f.Shears < 2 {
		t.Errorf("expected both adult sheep to be shorn, got %d shears", f.Shears)
	}
	if f.SheepKilled != 0 || a.Dead || b.Dead {
		t.Errorf("shears were carried but %d sheep were killed (shearing must be preferred)", f.SheepKilled)
	}
	if baby.Sheared || baby.Dead {
		t.Errorf("baby sheep was shorn or killed")
	}
	if f.ShearsDeny != 0 {
		t.Errorf("%d shear intents targeted a sheared, baby or out-of-reach sheep", f.ShearsDeny)
	}
	if n := countIntents(s, func(in *civquestv1.AgentIntent) bool { return in.GetAttack() != nil }); n != 0 {
		t.Errorf("agent attacked %d times while it carried shears", n)
	}
}

// cq-390.3: without shears (they need 2 iron ingots) the agent kills sheep and collects the wool
// drops, and stops once it holds a bed's worth: the rest of the flock survives.
func TestSmoke_WoolKillFallbackWithoutShears(t *testing.T) {
	s := harvestSim(harvestWorld(), survivalKit(), 20)
	for _, p := range [][2]float64{{6, 3}, {-7, 4}, {3, -8}, {-5, -6}} {
		s.AddSheep(p[0], p[1], "white")
	}

	ok := s.RunUntil(func(s *eval.PlaytestSim) bool { return s.ItemCount("minecraft:white_wool") >= 3 }, 6*harvestSimMinute)
	f := s.Fauna()
	if !ok {
		t.Fatalf("agent never gathered 3 wool by killing (kills=%d) inventory=%s", f.SheepKilled, s.InventorySummary())
	}
	if f.SheepKilled != 3 {
		t.Errorf("wool without shears needs one kill per fleece, kills=%d", f.SheepKilled)
	}
	if countIntents(s, isShear) != 0 {
		t.Errorf("agent tried to shear without shears")
	}
	s.Run(harvestSimMinute)
	if f.SheepKilled != 3 {
		t.Errorf("agent kept killing sheep after it had enough wool, kills=%d", f.SheepKilled)
	}
}

// soloBedRegistry is a single-clan in-memory bed ownership registry.
type soloBedRegistry map[[3]int32]string

func (r soloBedRegistry) ClaimBed(x, y, z int32, agentID string, _ int64) (string, error) {
	k := [3]int32{x, y, z}
	if r[k] == "" {
		r[k] = agentID
	}
	return r[k], nil
}
func (r soloBedRegistry) BedOwner(x, y, z int32) (string, error) { return r[[3]int32{x, y, z}], nil }
func (r soloBedRegistry) BedOwnedBy(agentID string) (out [][3]int32, _ error) {
	for k, o := range r {
		if o == agentID {
			out = append(out, k)
		}
	}
	return out, nil
}
func (r soloBedRegistry) ReleaseBed(x, y, z int32, agentID string) error {
	if k := [3]int32{x, y, z}; r[k] == agentID {
		delete(r, k)
	}
	return nil
}

// cq-390.3 + cq-390.2: the bed plan and the wool activity close the loop: a bed-less agent shears
// the flock for the wool, then crafts its bed at the table.
func TestSmoke_WoolFeedsTheBedPlan(t *testing.T) {
	w := harvestWorld()
	w.Blocks[eval.BlockPosKey{X: 2, Y: 64, Z: 0}] = "minecraft:crafting_table"
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "FARMER", Stage: system1.StageBootstrap, World: w, Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5},
		Inventory: append(survivalKit(),
			&civquestv1.ItemStackProto{ItemId: "minecraft:shears", Count: 1},
			&civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 3}),
		Setup: func(st *system1.AgentState) { st.SetBedRegistry(soloBedRegistry{}) },
	})
	s.AddSheep(7, 3, "white")
	s.AddSheep(-6, 4, "white")

	crafted := func(s *eval.PlaytestSim) bool {
		for _, c := range s.Crafts {
			if c.OK && c.Recipe == "white_bed" {
				return true
			}
		}
		return false
	}
	if !s.RunUntil(crafted, 4*harvestSimMinute) {
		t.Fatalf("no bed crafted: shears=%d crafts=%v inventory=%s", s.Fauna().Shears, s.Crafts, s.InventorySummary())
	}
	if s.Fauna().Shears < 2 || s.Fauna().SheepKilled != 0 {
		t.Errorf("bed wool should come from shearing: shears=%d kills=%d", s.Fauna().Shears, s.Fauna().SheepKilled)
	}
}
