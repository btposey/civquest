package integration

import (
	"math"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Play test 11: an agent fled into a ravine and stayed at its bottom. A trapped agent with wood
// crafts ladders, hangs them up a wall and climbs out; the ladders stay. The pit is
// pocketSim's, with crafting, table placement, ladder survival and climbing as the mod does
// them: a ladder needs a full block behind it, holds a body in its cell, and is climbed a cell
// at a time.
func TestPlaytest11_AgentClimbsOutOfADeepPitByLadder(t *testing.T) {
	for _, tc := range []struct {
		name        string
		half, depth int32
		logs        int32
		carried     int // ladders already in the pack
		rock        string
		refuse      bool // the server refuses every ladder
		ladders     bool // the way out is by ladder
	}{
		// Rock that cannot be cut leaves the ladder as the only way up.
		{"narrow shaft, eight deep", 1, 8, 8, 0, "minecraft:bedrock", false, true},
		{"ravine floor, ten deep", 3, 10, 8, 0, "minecraft:bedrock", false, true},
		{"ladders carried, no wood", 1, 8, 0, 12, "minecraft:bedrock", false, true},
		{"too little wood: steps are cut", 2, 5, 1, 0, "minecraft:stone", false, false},
		{"ladders refused: steps are cut", 2, 5, 8, 0, "minecraft:stone", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pocketSim{air: map[pocketCell]bool{}, top: 64 + tc.depth, rock: tc.rock}
			for x := -tc.half; x <= tc.half; x++ {
				for z := -tc.half; z <= tc.half; z++ {
					for y := int32(64); y < p.top; y++ {
						p.air[pocketCell{x, y, z}] = true
					}
				}
			}
			p.moveTo(pocketCell{0, 64, 0})
			fsm := system1.NewAgentFSMWithRegistry(system1.StageSettlement, nil, system1.NewDefaultEngineRegistry())
			st := system1.NewAgentState()
			graph := crafting.NewRecipeGraph(craftingtest.Nodes())
			st.SetRecipeGraph(graph)
			cfg := xdg.DefaultAgentIdentityConfig("fred")
			cfg.Profession = "EXPLORER"
			inv := map[string]int{"minecraft:stone_pickaxe": 1, "minecraft:stone_axe": 1, "minecraft:stone_sword": 1,
				"minecraft:oak_log": int(tc.logs), "minecraft:ladder": tc.carried}

			ladders, tables := map[pocketCell]bool{}, map[pocketCell]bool{}
			// hold reports whether a body stays in cell c.
			hold := func(c pocketCell) bool {
				return p.standable(c) || ladders[c] || ladders[pocketCell{c[0], c[1] - 1, c[2]}] && p.clear(c)
			}
			full := func(c pocketCell) bool { return !p.clear(c) }

			var receipt *civquestv1.IntentReceipt
			var survey *civquestv1.HorizonSurveyReport
			var batch int64
			breaks, hung, asked := 0, 0, 0
			for tick := int64(1000); tick < 9000; tick += 4 {
				reach := p.reachable()
				if survey != nil {
					st.IngestHorizonSurveyReport(survey, tick)
				}
				blocks := p.frameBlocks()
				for _, set := range []struct {
					id    string
					cells map[pocketCell]bool
				}{{"minecraft:ladder", ladders}, {"minecraft:crafting_table", tables}} {
					for c := range set.cells {
						blocks = append(blocks, &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: c[0], Y: c[1], Z: c[2]}, BlockId: set.id})
					}
				}
				var items []*civquestv1.ItemStackProto
				for id, n := range inv {
					if n > 0 {
						items = append(items, &civquestv1.ItemStackProto{ItemId: id, Count: int32(n)})
					}
				}
				res := fsm.Tick(&system1.FSMContext{AgentID: "fred", State: st, Config: cfg, Frame: &civquestv1.TelemetryFrame{
					WorldTick: tick, TimeOfDay: tick % 24000, Receipt: receipt, HorizonSurvey: survey, InterestingBlocks: blocks,
					Status: &civquestv1.AgentStatus{AgentId: "fred", Health: 20, MaxHealth: 20, FoodLevel: 20, Inventory: items,
						Position:           &civquestv1.Vec3{X: p.pos.X, Y: p.pos.Y, Z: p.pos.Z},
						NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
						NavigationEnclosed: len(reach) <= 60},
				}})
				receipt, survey = nil, nil
				for _, in := range res.Intents {
					status, facts, reason := civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED, (*civquestv1.MotorFacts)(nil), ""
					fail := func(why string) { status, reason = civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, why }
					switch {
					case in.Intent.GetNavigate() != nil:
						nav := in.Intent.GetNavigate()
						goal := pocketCell{int32(math.Floor(nav.Target.X)), int32(math.Floor(nav.Target.Y + 0.1)), int32(math.Floor(nav.Target.Z))}
						f := p.feet
						dx, dz := abs32(goal[0]-f[0]), abs32(goal[2]-f[2])
						onRungs := ladders[f] || ladders[pocketCell{f[0], f[1] - 1, f[2]}]
						climbed := false
						switch {
						case onRungs && dx+dz == 0: // up or down the column, as far as it holds
							climbed = true
							for y := f[1] + 1; y <= goal[1] && hold(pocketCell{f[0], y, f[2]}) && p.clear(pocketCell{f[0], y + 1, f[2]}); y++ {
								p.moveTo(pocketCell{f[0], y, f[2]})
							}
						case onRungs && dx+dz == 1 && goal[1]-f[1] >= 0 && goal[1]-f[1] <= 1 && p.standable(goal):
							climbed = true // off the top of the ladder onto the ledge
							p.moveTo(goal)
						}
						if climbed {
							break
						}
						best, found := pocketCell{}, false
						for c := range reach {
							near := math.Hypot(float64(c[0])+0.5-nav.Target.X, float64(c[2])+0.5-nav.Target.Z) <= math.Max(1, float64(nav.ArrivalTolerance)) &&
								math.Abs(float64(c[1])-nav.Target.Y) <= 2
							if nav.IntoCell {
								near = c == goal
							}
							if near && (!found || c == goal) {
								best, found = c, true
							}
						}
						if found {
							p.moveTo(best)
						} else {
							fail("Navigation stuck")
							facts = &civquestv1.MotorFacts{Stuck: true, Enclosed: len(reach) <= 60}
						}
					case in.Intent.GetInteractBlock() != nil && in.Intent.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK:
						b := in.Intent.GetInteractBlock().Pos
						c := pocketCell{b.X, b.Y, b.Z}
						if p.block(c) != "" && math.Sqrt(sqf(float64(b.X)+0.5-p.pos.X)+sqf(float64(b.Y)+0.5-p.pos.Y-1.6)+sqf(float64(b.Z)+0.5-p.pos.Z)) <= 5 {
							p.air[c] = true
							breaks++
							for !hold(p.feet) && p.clear(pocketCell{p.feet[0], p.feet[1] - 1, p.feet[2]}) {
								p.moveTo(pocketCell{p.feet[0], p.feet[1] - 1, p.feet[2]})
							}
						}
					case in.Intent.GetInteractBlock() != nil && in.Intent.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK:
						b := in.Intent.GetInteractBlock()
						c := pocketCell{b.Pos.X, b.Pos.Y, b.Pos.Z}
						if b.BlockOrItemId == "minecraft:crafting_table" && inv[b.BlockOrItemId] > 0 && p.clear(c) && c != p.feet {
							inv[b.BlockOrItemId]--
							tables[c] = true
						} else {
							fail("cannot place there")
						}
					case in.Intent.GetBuildBlock() != nil:
						b := in.Intent.GetBuildBlock()
						c := pocketCell{b.TargetVoxel.X, b.TargetVoxel.Y, b.TargetVoxel.Z}
						asked++
						behind := map[string][2]int32{"north": {0, 1}, "south": {0, -1}, "east": {-1, 0}, "west": {1, 0}}
						facing := strings.TrimSuffix(strings.TrimPrefix(b.BlockStateString, "minecraft:ladder[facing="), "]")
						d, ok := behind[facing]
						if tc.refuse || !ok || inv["minecraft:ladder"] == 0 || !p.clear(c) || ladders[c] || !full(pocketCell{c[0] + d[0], c[1], c[2] + d[1]}) {
							fail("placement refused")
							break
						}
						inv["minecraft:ladder"]--
						ladders[c] = true
						hung++
					case in.Intent.GetCraftItem() != nil:
						r, ok := graph.RecipeByID(in.Intent.GetCraftItem().RecipeId)
						near := false
						for c := range tables {
							near = near || math.Hypot(float64(c[0])+0.5-p.pos.X, float64(c[2])+0.5-p.pos.Z) <= 4
						}
						if !ok || !graph.CanCraftNow(r.RecipeID, inv) || r.Requires3x3 && !near {
							fail("cannot craft")
							break
						}
						for _, slot := range r.Slots {
							need := int(slot.Count)
							for _, id := range slot.AcceptedItemIDs {
								n := min(need, inv[id])
								inv[id] -= n
								need -= n
							}
						}
						inv[r.ResultItemID] += int(r.ResultCount)
					case in.Intent.GetSurveyHorizon() != nil:
						survey = openSurvey(p.pos.X, p.pos.Y, p.pos.Z, tick)
					default:
						continue
					}
					batch++
					receipt = &civquestv1.IntentReceipt{BatchId: batch, Status: status, FailureReason: reason, Facts: facts, FrameId: in.Frame}
				}
				if p.feet[1] >= p.top {
					t.Logf("out after %d ticks: %d ladders hung (%d asked), %d blocks broken", tick-1000, hung, asked, breaks)
					switch {
					case tc.ladders && hung < int(tc.depth)-1:
						t.Errorf("left the pit with %d ladders hung, want a column of at least %d", hung, tc.depth-1)
					case tc.carried > 0 && (len(tables) > 0 || tick-1000 > 400):
						t.Errorf("carried ladders: %d tables set down, out after %d ticks", len(tables), tick-1000)
					case !tc.ladders && (hung > 0 || breaks == 0):
						t.Errorf("want steps cut: %d ladders hung, %d blocks broken", hung, breaks)
					case tc.refuse && asked > 4:
						t.Errorf("a refused ladder was asked for %d times, once a side at most", asked)
					}
					return
				}
			}
			t.Errorf("still in the pit after 8000 ticks at %v (%d ladders hung of %d asked, %d blocks broken, inventory %v)",
				p.feet, hung, asked, breaks, inv)
		})
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
