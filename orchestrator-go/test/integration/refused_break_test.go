package integration

// Play test sb2 (cq-6nz): bert stood in water for 36 minutes asking to break blocks the mod
// would not start (they had water against them), once every 8 ticks, about 3900 times. The
// failed receipt changed nothing in the plan or in Recover.

import (
	"math"
	"slices"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
)

const (
	// A refused block may be asked for a few times in each span it is remembered for: the
	// limit, and one more already on its way when the last report arrives.
	refusedSendsAllowed = 4
	refusedMemorySpan   = int64(600)
)

// worstSpan is the most sends of one cell within any refusedMemorySpan ticks.
func worstSpan(ticks []int64) int {
	worst, first := 0, 0
	for i, t := range ticks {
		for t-ticks[first] >= refusedMemorySpan {
			first++
		}
		worst = max(worst, i-first+1)
	}
	return worst
}

func pickaxeKit() []*civquestv1.ItemStackProto {
	return []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wooden_pickaxe", Count: 1}, {ItemId: "minecraft:wooden_axe", Count: 1},
		{ItemId: "minecraft:wooden_sword", Count: 1}, {ItemId: "minecraft:crafting_table", Count: 1},
		{ItemId: "minecraft:oak_log", Count: 8}, {ItemId: "minecraft:oak_planks", Count: 8}, {ItemId: "minecraft:stick", Count: 8},
	}
}

// The sb2 case in the closed loop: the nearest stone stands in water, dry stone lies further
// off. The agent must leave the refused stone and mine the other, whether the mod says
// beforehand that it will refuse or the agent learns it from the failed receipts.
func TestSandbox2_RefusedStoneInWaterIsLeftForDryStone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unreported bool
	}{
		{"reported by the mod", false},
		{"learnt from receipts", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := groundWorld(40)
			w.Blocks[eval.BlockPosKey{X: 0, Y: 64, Z: 0}] = "minecraft:water"
			for _, k := range []eval.BlockPosKey{{X: 1, Y: 64, Z: 0}, {X: -1, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1}} {
				w.Blocks[k] = "minecraft:stone"
			}
			w.AddStoneOutcrop(7, 7, 3, 2)
			s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
				Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w, TimeOfDay: 2000,
				Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: pickaxeKit(), NoRefusalReport: tc.unreported,
			})
			s.Run(3 * simTicksPerMinute)

			sends := map[eval.BlockPosKey][]int64{}
			for _, h := range s.Harvests {
				if h.Refused {
					sends[h.Cell] = append(sends[h.Cell], h.Tick)
				}
			}
			if !tc.unreported && len(sends) > 0 {
				t.Errorf("asked for blocks the mod had said it would refuse: %v", sends)
			}
			if tc.unreported && len(sends) == 0 {
				t.Fatalf("the scenario never asked for a refused block\n%s", traceTail(s, 0, 40))
			}
			for cell, ticks := range sends {
				if n := worstSpan(ticks); n > refusedSendsAllowed {
					t.Errorf("block %v refused %d times, %d of them within %d ticks", cell, len(ticks), n, refusedMemorySpan)
				}
			}
			if s.ItemCount("minecraft:cobblestone") < 1 && s.CountSuffix("stone_pickaxe") < 1 {
				t.Errorf("no stone from the dry outcrop (inventory: %s)\n%s", s.InventorySummary(), traceTail(s, len(s.Steps)-30, len(s.Steps)))
			}
		})
	}
}

// refusingRig is an agent that cannot move, in a world that refuses every break.
type refusingRig struct {
	stage      system1.Stage
	profession string
	inv        []*civquestv1.ItemStackProto
	food       int32
	timeOfDay  int64
	blocks     []*civquestv1.VisibleBlock
	suffocate  bool // unattributed damage lands every frame
	enclosed   bool
	setup      func(*system1.AgentState)
	// stuckUnder are the activities this agent, which cannot move, is reported standing
	// still under; stuckLater are reported only in the longer run.
	stuckUnder, stuckLater []string
}

type refusedSend struct {
	cell     [3]int32
	activity string
}

// run drives the rig for ticks and returns the ticks of every break asked for.
func (r refusingRig) run(t *testing.T, ticks int64) map[refusedSend][]int64 {
	t.Helper()
	fsm := system1.NewAgentFSMWithRegistry(r.stage, nil, system1.NewDefaultEngineRegistry())
	st := system1.NewAgentState()
	if r.setup != nil {
		r.setup(st)
	}
	cfg := xdg.DefaultAgentIdentityConfig("bert")
	cfg.Profession = r.profession
	if r.food == 0 {
		r.food = 20
	}
	stuck := r.stuckUnder
	if ticks > 3000 {
		stuck = append(slices.Clone(stuck), r.stuckLater...)
	}
	for _, activity := range stuck {
		watchdogtest.Expect(t, fsm, watchdog.NoProgress, activity, "stuck on purpose: the agent cannot move and every break is refused")
	}
	var receipt *civquestv1.IntentReceipt
	var survey *civquestv1.HorizonSurveyReport
	var batch int64
	out := map[refusedSend][]int64{}
	for tick := int64(1000); tick < 1000+ticks; tick += 4 {
		frame := &civquestv1.TelemetryFrame{
			WorldTick: tick, TimeOfDay: (r.timeOfDay + tick - 1000) % 24000, Receipt: receipt, InterestingBlocks: r.blocks,
			Status: &civquestv1.AgentStatus{AgentId: "bert", Health: 20, MaxHealth: 20, FoodLevel: r.food, Inventory: r.inv,
				Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}, NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				NavigationEnclosed: r.enclosed},
		}
		if r.suffocate {
			frame.LastDamageTaken = &civquestv1.DamageEvent{DamageAmount: 1}
		}
		receipt = nil
		if survey != nil {
			st.IngestHorizonSurveyReport(survey, tick)
			frame.HorizonSurvey, survey = survey, nil
		}
		res := fsm.Tick(&system1.FSMContext{AgentID: "bert", State: st, Config: cfg, Frame: frame})
		for _, in := range res.Intents {
			batch++
			switch a := in.Intent.GetAction().(type) {
			case *civquestv1.AgentIntent_SurveyHorizon:
				survey = openPlains(tick)
			case *civquestv1.AgentIntent_InteractBlock:
				if a.InteractBlock.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
					continue
				}
				p := a.InteractBlock.Pos
				k := refusedSend{cell: [3]int32{p.X, p.Y, p.Z}, activity: fsm.ActiveActivityKind()}
				out[k] = append(out[k], tick)
				receipt = &civquestv1.IntentReceipt{BatchId: batch, FrameId: in.Frame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
					FailureReason: "HARVEST_BREAK failed (unbreakable, fluid, or out of reach)"}
			case *civquestv1.AgentIntent_Navigate:
				receipt = &civquestv1.IntentReceipt{BatchId: batch, FrameId: in.Frame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
					FailureReason: "Navigation stuck", Facts: &civquestv1.MotorFacts{Stuck: true, Enclosed: r.enclosed,
						BlockedBearingDeg: float32(math.Mod(float64(tick), 360))}}
			}
		}
	}
	return out
}

func visible(id string, cells ...[3]int32) []*civquestv1.VisibleBlock {
	out := make([]*civquestv1.VisibleBlock, 0, len(cells))
	for _, c := range cells {
		out = append(out, &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: c[0], Y: c[1], Z: c[2]}, BlockId: id})
	}
	return out
}

// walled is the floor under the agent and walls two high on its four sides.
func walled(id string) []*civquestv1.VisibleBlock {
	cells := [][3]int32{{0, 63, 0}}
	for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		cells = append(cells, [3]int32{d[0], 64, d[1]}, [3]int32{d[0], 65, d[1]})
	}
	return visible(id, cells...)
}

type namedRig struct {
	name string
	rig  refusingRig
}

// refusingRigs sets each activity that breaks blocks to work on blocks beside it.
func refusingRigs() []namedRig {
	beside := [][3]int32{{2, 64, 0}, {-2, 64, 0}, {0, 64, 2}}
	rigs := []namedRig{
		{"stone beside an explorer", refusingRig{stage: system1.StageBootstrap, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 2000, blocks: visible("minecraft:stone", beside...),
			stuckUnder: []string{"bootstrap"}, stuckLater: []string{"gather"}}},
		{"directive mines stone", refusingRig{stage: system1.StageSettlement, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 3000, blocks: visible("minecraft:stone", beside...), stuckUnder: []string{"strategic_directive"},
			setup: func(st *system1.AgentState) {
				st.SetStrategicDirective(system1.DirectiveActionMineStone, "stone", "gather cobblestone", 90, 1_000_000)
			}}},
		{"gather takes a find", refusingRig{stage: system1.StageSettlement, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 3000, blocks: visible("minecraft:coal_ore", beside...),
			stuckUnder: []string{"bootstrap"}, stuckLater: []string{"gather"}}},
		{"foraging picks berries", refusingRig{stage: system1.StageSettlement, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 3000, food: 8, blocks: visible("minecraft:sweet_berry_bush", beside...),
			stuckUnder: []string{"foraging"}}},
		{"timber beside the agent", refusingRig{stage: system1.StageBootstrap, profession: "EXPLORER",
			timeOfDay: 2000, blocks: visible("minecraft:oak_log", [3]int32{2, 64, 0}, [3]int32{2, 65, 0}, [3]int32{-2, 64, 0}),
			stuckUnder: []string{"bootstrap"}}},
		{"recover digs out", refusingRig{stage: system1.StageBootstrap, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 11800, enclosed: true, blocks: walled("minecraft:grass_block")}},
		{"emergency digs free", refusingRig{stage: system1.StageBootstrap, profession: "EXPLORER", inv: pickaxeKit(),
			timeOfDay: 3000, suffocate: true, blocks: walled("minecraft:stone")}},
	}
	for i := range rigs {
		if rigs[i].rig.suffocate {
			rigs[i].rig.blocks = append(rigs[i].rig.blocks, visible("minecraft:stone", [3]int32{0, 65, 0})...)
		}
	}
	return rigs
}

// Whatever activity asks for a break, the refusal must change what it does within a few tries.
func TestSandbox2_NoActivityRepeatsARefusedBreak(t *testing.T) {
	for _, tc := range refusingRigs() {
		t.Run(tc.name, func(t *testing.T) {
			sends := tc.rig.run(t, 6000)
			if len(sends) == 0 {
				t.Fatal("the scenario never asked for a break")
			}
			byCell := map[[3]int32][]int64{}
			for k, ticks := range sends {
				t.Logf("%s asked for %v %d times", k.activity, k.cell, len(ticks))
				byCell[k.cell] = append(byCell[k.cell], ticks...)
			}
			for cell, ticks := range byCell {
				slices.Sort(ticks)
				if n := worstSpan(ticks); n > refusedSendsAllowed {
					t.Errorf("block %v asked for %d times, %d of them within %d ticks", cell, len(ticks), n, refusedMemorySpan)
				}
			}
		})
	}
}

// A block the mod reports as refused is not asked for at all, by any activity.
func TestSandbox2_ReportedRefusalsAreNeverAskedFor(t *testing.T) {
	for _, tc := range refusingRigs() {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.rig
			for _, b := range r.blocks {
				b.BreakRefused = true
			}
			for k, ticks := range r.run(t, 3000) {
				t.Errorf("%s asked for the refused block %v %d times", k.activity, k.cell, len(ticks))
			}
		})
	}
}

// openPlains answers a survey as the mod would: open country all round.
func openPlains(tick int64) *civquestv1.HorizonSurveyReport {
	r := &civquestv1.HorizonSurveyReport{OriginX: 0.5, OriginY: 64, OriginZ: 0.5, CompletedTick: tick, MaxRange: 64}
	for i := int32(0); i < 36; i++ {
		rad := float64(i*10) * math.Pi / 180
		r.Sectors = append(r.Sectors, &civquestv1.HorizonSector{SectorIndex: i, BearingDeg: float32(i * 10), HitDistance: 64,
			FeatureType: "OPEN_PLAINS", HitBlockId: "minecraft:air", HitX: -math.Sin(rad) * 64, HitY: 64, HitZ: math.Cos(rad) * 64, OpenHorizon: true})
	}
	return r
}
