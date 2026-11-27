package smoke

// cq-6l3.15 gathering smoke tests: the real System 1 kernel in the closed-loop eval.PlaytestSim.

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func travellerKit() []*civquestv1.ItemStackProto {
	return append(survivalKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:stone_pickaxe", Count: 1},
		&civquestv1.ItemStackProto{ItemId: "minecraft:fishing_rod", Count: 1})
}

// finds lays an exposed ore, a berry bush, a stone outcrop and a pond around the start.
func findsWorld() *eval.SyntheticStartingWorld {
	w := harvestWorld()
	w.Blocks[eval.BlockPosKey{X: 6, Y: 64, Z: 2}] = "minecraft:iron_ore"
	w.Blocks[eval.BlockPosKey{X: -5, Y: 64, Z: -3}] = "minecraft:sweet_berry_bush"
	for x := int32(-8); x <= -6; x++ {
		w.Blocks[eval.BlockPosKey{X: x, Y: 64, Z: 5}] = "minecraft:stone"
	}
	w.AddPond(0, 14, 4)
	return w
}

func TestSmoke_ExplorerGathersAlongTheWay(t *testing.T) {
	s := harvestSim(findsWorld(), travellerKit(), 18)
	done := func(s *eval.PlaytestSim) bool {
		return s.ItemCount("minecraft:raw_iron") > 0 && s.ItemCount("minecraft:sweet_berries") > 0 &&
			s.ItemCount("minecraft:cobblestone") >= 3 && fishCarried(s) > 0
	}
	if !s.RunUntil(done, 6*harvestSimMinute) {
		t.Fatalf("explorer did not gather ore, fish, stone and berries (casts=%d) inventory=%s", s.Fauna().Casts, s.InventorySummary())
	}
	// The rare find comes first, the chance berries after the rest.
	order := map[string]int{}
	for i, h := range s.Harvests {
		if _, ok := order[h.BlockID]; !ok && h.Existed {
			order[h.BlockID] = i
		}
	}
	if order["minecraft:iron_ore"] > order["minecraft:sweet_berry_bush"] {
		t.Errorf("berries were taken before the ore: %v", order)
	}
	assertStayedDry(t, s)
}

func TestSmoke_SettledAgentDoesNotStockpileInPassing(t *testing.T) {
	s := harvestSim(findsWorld(), travellerKit(), 18)
	s.State.SetCollectiveSettlementSite(0.5, 64, 0.5)
	s.Run(2 * harvestSimMinute)
	if n := s.ItemCount("minecraft:raw_iron") + s.ItemCount("minecraft:sweet_berries"); n != 0 || s.Fauna().Casts != 0 {
		t.Errorf("a settled agent gathered in passing: casts=%d inventory=%s", s.Fauna().Casts, s.InventorySummary())
	}
}

// A find that cannot be taken is given up instead of being worked on for ever.
func TestSmoke_GatherGivesUpOnAnUnbreakableFind(t *testing.T) {
	w := harvestWorld()
	ore := eval.BlockPosKey{X: 3, Y: 64, Z: 0}
	w.Blocks[ore] = "minecraft:iron_ore"
	s := harvestSim(w, travellerKit(), 18)
	hits := 0
	for s.Tick < 3*harvestSimMinute {
		s.Step()
		w.Blocks[ore] = "minecraft:iron_ore" // it never goes away
	}
	for _, in := range s.Intents() {
		if ib := in.GetInteractBlock(); ib != nil && ib.Pos.X == ore.X && ib.Pos.Z == ore.Z {
			hits++
		}
	}
	if hits == 0 || hits > 15 {
		t.Errorf("break attempts on an unbreakable ore = %d, want 1..15", hits)
	}
}

// An agent still short of its first tools, out looking for timber, passes a find: gathering must
// not launch the bootstrap activity again on top of itself (seen live: 600 pushes in 10 minutes).
func TestSmoke_GatherDuringBootstrapTripDoesNotChurn(t *testing.T) {
	w := eval.NewBlankWorld()
	w.FillGround(-60, -60, 60, 60, "")
	for x := int32(-48); x <= 48; x += 8 {
		for z := int32(-48); z <= 48; z += 8 {
			w.Blocks[eval.BlockPosKey{X: x + 3, Y: 64, Z: z + 3}] = "minecraft:sweet_berry_bush"
		}
	}
	s := harvestSim(w, nil, 20)
	pushes, nested := 0, 0
	s.FSM.SetEventSink(func(_ int64, category, _ string, d map[string]any) {
		if category != system1.EventFrame || d["op"] != "push" || d["kind"] != "bootstrap" {
			return
		}
		pushes++
		if d["parent"] != nil {
			nested++
		}
	})
	s.Run(3 * harvestSimMinute)
	t.Logf("pushes=%d nested=%d", pushes, nested)
	if s.ItemCount("minecraft:sweet_berries") == 0 {
		t.Fatalf("scenario did not gather anything on the trip: %s", s.InventorySummary())
	}
	if pushes > 40 {
		t.Errorf("bootstrap pushed %d times (%d on top of another activity) in 3 minutes", pushes, nested)
	}
}

// Stocking up on wood during the bootstrap stage, with the tools already made: the tree is
// felled, and a felling that ends early is not restarted over and over (seen live: 800 times).
func TestSmoke_GatherFellsForStockDuringBootstrapWithoutChurn(t *testing.T) {
	for _, logs := range []int32{0, 3, 8, 16} {
		w := eval.NewBlankWorld()
		w.FillGround(-60, -60, 60, 60, "")
		for x := int32(-48); x <= 48; x += 12 {
			for z := int32(-48); z <= 48; z += 12 {
				w.AddTrunk(x+4, z+4, 4, "")
			}
		}
		inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_axe", Count: 1}}
		if logs > 0 {
			inv = append(inv, &civquestv1.ItemStackProto{ItemId: "minecraft:oak_log", Count: logs})
		}
		s := harvestSim(w, inv, 20)
		tasks := 0
		s.FSM.SetEventSink(func(_ int64, category, _ string, d map[string]any) {
			if category == system1.EventFrame && d["op"] == "push" && d["kind"] == "task_subroutine" {
				tasks++
			}
		})
		s.Run(3 * harvestSimMinute)
		t.Logf("start logs=%d: tasks=%d inventory=%s", logs, tasks, s.InventorySummary())
		if tasks > 40 {
			t.Errorf("start logs=%d: task pushed %d times in 3 minutes", logs, tasks)
		}
	}
}
