package integration

// cq-029: able stood under coal ore four blocks up sending the same walk for 3700 ticks. No walk
// brings the agent nearer to a block it is already under.

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func TestOverheadBlockIsMinedFromBelow(t *testing.T) {
	overhead := eval.BlockPosKey{X: 1, Y: 68, Z: 1}
	pick := &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_pickaxe", Count: 1}
	for _, tc := range []struct {
		name  string
		block string
		cfg   eval.PlaytestSimConfig
	}{
		{"plan", "minecraft:coal_ore", eval.PlaytestSimConfig{Stage: system1.StageBootstrap,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:stone_axe", Count: 1}, {ItemId: "minecraft:stone_sword", Count: 1}, {ItemId: "minecraft:stone_pickaxe", Count: 1},
				{ItemId: "minecraft:stick", Count: 8}}}},
		{"strategic directive", "minecraft:stone", eval.PlaytestSimConfig{Stage: system1.StageSettlement, Inventory: append(simKit(), pick),
			Setup: func(st *system1.AgentState) {
				st.SetStrategicDirective(system1.DirectiveActionMineStone, "", "mine stone", 100, 100_000)
			}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := groundWorld(40)
			w.Blocks[overhead] = tc.block
			cfg := tc.cfg
			cfg.Profession, cfg.World, cfg.TimeOfDay, cfg.Start = "RANCHER", w, 2000, eval.SimVec{X: 0.5, Y: 64, Z: 0.5}
			s := eval.NewPlaytestSim(cfg)
			if !s.RunUntil(func(s *eval.PlaytestSim) bool { return s.BlockAt(overhead) == "" }, simTicksPerMinute) {
				walks := 0
				for _, in := range s.Intents() {
					if to := in.GetNavigate().GetTarget(); to != nil && to.Y == 68 {
						walks++
					}
				}
				t.Errorf("the block overhead still stands after %d walks to it\n%s", walks,
					traceTail(s, len(s.Steps)-20, len(s.Steps)))
			}
		})
	}
}
