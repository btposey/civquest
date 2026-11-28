package system1

import (
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
)

// An equipped agent with wood to spare by a table makes ladders in preparation (cq-htv.4).
func TestReplayPlan_MakesTheLadderReserveFromSpareWood(t *testing.T) {
	pack := withInv(item(0, "minecraft:stone_axe", 1), item(1, "minecraft:stone_sword", 1), item(2, "minecraft:stone_pickaxe", 1),
		item(3, "minecraft:oak_log", 10), item(4, "minecraft:stick", 8), item(5, "minecraft:torch", 4))
	table := withBlocks(blk(1, 64, 0, "minecraft:crafting_table"))
	tr := runReplay(t, replayScenario{
		Name: "plan_ladder_reserve", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{{Frame: rf(100, 3000, pack, table), Setup: func(st *AgentState) {
			st.SetRecipeGraph(crafting.NewRecipeGraph(craftingtest.Nodes()))
		}}},
	})
	if got := strings.Join(tr[0].Intents, "; "); !strings.Contains(got, "Craft(minecraft:") {
		t.Fatalf("expected a craft towards ladders, got %q (%s)", got, tr[0])
	}
}
