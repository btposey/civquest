package integration

// Play test 11 (cq-qdj): able, in water within 20 blocks of a registered drowned, crafted
// crafting tables and set them down for 13 minutes (193 sends, 12 new tables in one spot). A
// table by a danger did not exist to the planner, and nothing remembered having placed one.

import (
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const (
	simTable = "minecraft:crafting_table"
	// stationAsksAllowed is the most placements tried from one spot: two rounds of cells.
	stationAsksAllowed = 16
)

func timberKit(extra ...*civquestv1.ItemStackProto) []*civquestv1.ItemStackProto {
	return append([]*civquestv1.ItemStackProto{
		{ItemId: "minecraft:oak_log", Count: 12}, {ItemId: "minecraft:oak_planks", Count: 12},
		{ItemId: "minecraft:stick", Count: 8},
	}, extra...)
}

// tableOrders counts the tables crafted and the placements of one asked for.
func tableOrders(s *eval.PlaytestSim) (crafts, places int) {
	for _, step := range s.Steps {
		for _, in := range step.Intents {
			if c := in.GetCraftItem(); c != nil && strings.HasSuffix(c.RecipeId, "crafting_table") {
				crafts++
			}
			if b := in.GetInteractBlock(); b != nil && b.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK &&
				b.BlockOrItemId == simTable {
				places++
			}
		}
	}
	return crafts, places
}

// A drowned stands registered 9 blocks off and a crafting table is beside the agent: the table
// is used, and no other is made or set down.
func TestPlaytest11_TableBesideTheAgentIsUsedDespiteADanger(t *testing.T) {
	for _, tc := range []struct {
		name string
		kit  []*civquestv1.ItemStackProto
	}{
		{"empty handed", timberKit()},
		{"carrying a table", timberKit(&civquestv1.ItemStackProto{ItemId: simTable, Count: 1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := groundWorld(40)
			w.Blocks[eval.BlockPosKey{X: 2, Y: 64, Z: 0}] = simTable
			s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
				Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 2000,
				Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: tc.kit,
				Setup: func(st *system1.AgentState) {
					st.NoteDanger(system1.Danger{Kind: "drowned", X: -8.5, Y: 63, Z: 0.5, Seen: 90})
				},
			})
			s.RunUntil(func(s *eval.PlaytestSim) bool { return s.CountSuffix("_axe") > 0 }, simTicksPerMinute)

			if crafts, places := tableOrders(s); crafts+places > 0 {
				t.Errorf("made %d tables and asked to set %d down beside a table in reach (inventory: %s)",
					crafts, places, s.InventorySummary())
			}
			if s.CountSuffix("_axe") < 1 {
				t.Errorf("no tool crafted at the table beside the agent (inventory: %s)\n%s",
					s.InventorySummary(), traceTail(s, len(s.Steps)-30, len(s.Steps)))
			}
		})
	}
}

// A table that will not go down is asked for a bounded number of times, and no second one is
// made meanwhile.
func TestPlaytest11_PlacementThatNeverLandsIsGivenUp(t *testing.T) {
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: groundWorld(40), TimeOfDay: 2000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, RefusePlacing: true,
		Inventory: timberKit(&civquestv1.ItemStackProto{ItemId: simTable, Count: 1}),
	})
	s.Run(40 * 20)

	crafts, places := tableOrders(s)
	if places == 0 {
		t.Fatalf("the scenario never asked to set the table down\n%s", traceTail(s, 0, 40))
	}
	if places > stationAsksAllowed || crafts > 0 {
		t.Errorf("asked to set a table down %d times (%d allowed) and made %d more", places, stationAsksAllowed, crafts)
	}
}

// A table that went down but is never shown to the agent is not made and set down again.
func TestPlaytest11_TableThatCannotBeSeenIsNotMultiplied(t *testing.T) {
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: groundWorld(40), TimeOfDay: 2000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: timberKit(), HidePlaced: true,
	})
	s.Run(40 * 20)

	crafts, places := tableOrders(s)
	if places == 0 {
		t.Fatalf("the scenario never set a table down (inventory: %s)\n%s", s.InventorySummary(), traceTail(s, 0, 40))
	}
	if crafts > 1 || len(s.Placed) > 1 {
		t.Errorf("made %d tables and set down %d at %v", crafts, len(s.Placed), s.Placed)
	}
}
