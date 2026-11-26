package integration

// Playtest-2 fix scenarios (bead cq-gpa.13).
//
// Each scenario drives the REAL System 1 kernel (system1.AgentFSM) through the closed-loop
// eval.PlaytestSim synthetic world for simulated minutes and asserts an OUTCOME, so a
// regression in any cq-gpa fix flips a named pass/fail instead of hiding behind a unit test.
// Every test carries an "UNFIXED BEHAVIOUR" note: what the 2026-11-25 playtest observed.
//
// Scenarios whose fix has not landed in git yet are skipped via pendingFix with the bead id;
// set PLAYTEST2_RUN_PENDING=1 to run them anyway (used to prove they pass against a working
// tree that carries the fix and fail without it). scripts/test-playtest-fixes.sh prints the
// pass/fail/skip table.

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const simTicksPerMinute = 20 * 60

// pendingFix skips a scenario whose fix is not in git history yet. Remove the call (and the
// bead from the report's skip list) in the commit that lands the fix.
func pendingFix(t *testing.T, bead, why string) {
	t.Helper()
	if os.Getenv("PLAYTEST2_RUN_PENDING") == "" {
		t.Skipf("pending %s: %s (PLAYTEST2_RUN_PENDING=1 to run anyway)", bead, why)
	}
}

func groundWorld(half int32) *eval.SyntheticStartingWorld {
	w := eval.NewBlankWorld()
	w.FillGround(-half, -half, half, half, "")
	return w
}

func simKit() []*civquestv1.ItemStackProto {
	return []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wooden_axe", Count: 1}, {ItemId: "minecraft:wooden_sword", Count: 1}, {ItemId: "minecraft:torch", Count: 8},
	}
}

// traceTail renders steps [from,to) for failure messages.
func traceTail(s *eval.PlaytestSim, from, to int) string {
	var sb strings.Builder
	if to > len(s.Steps) {
		to = len(s.Steps)
	}
	if from < 0 {
		from = 0
	}
	for i := from; i < to; i++ {
		st := s.Steps[i]
		fmt.Fprintf(&sb, "  t=%d %s/%s pos=(%.1f,%.1f,%.1f) hp=%.0f air=%d inWater=%v sub=%v fire=%v intents=%d\n",
			st.Tick, st.Stage, st.Top, st.Pos.X, st.Pos.Y, st.Pos.Z, st.HP, st.Air, st.InWater, st.Submerged, st.OnFire, len(st.Intents))
	}
	return sb.String()
}

// emergenciesInland counts EMERGENCY entries away from the rim of a groundWorld(half). The rim
// is a real drop that a wandering agent reaches, steps back from and may reach again.
func emergenciesInland(s *eval.PlaytestSim, half float64) int {
	n, in := 0, false
	for _, st := range s.Steps {
		now := st.Stage == system1.StageEmergency
		if now && !in && st.Pos.X > 2-half && st.Pos.X < half-2 && st.Pos.Z > 2-half && st.Pos.Z < half-2 {
			n++
		}
		in = now
	}
	return n
}

// (1) Drowning (cq-gpa.1 telemetry + cq-gpa.2 emergency hold).
//
// UNFIXED BEHAVIOUR: bert and dave drowned. EMERGENCY was entered and cleared every ~20 ticks
// (the drown-damage cadence) because the trigger was the damage edge, not "eyes under water,
// air draining"; the routine resumed ~2 ticks later and swam back in.
func TestPlaytest2_01_DrowningHeldUntilOnLandWithAir(t *testing.T) {
	w := groundWorld(40)
	w.AddPond(0, 0, 3)
	w.AddTrunk(12, 0, 5, "")
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w,
		Start: eval.SimVec{X: 0.5, Y: 63, Z: 0.5}, Inventory: simKit(), Air: 60,
	})
	s.Run(3 * simTicksPerMinute)

	if s.Dead {
		t.Fatalf("agent drowned (died at hp 0)\n%s", traceTail(s, 0, 40))
	}
	last := s.Steps[len(s.Steps)-1]
	if last.InWater || last.Submerged {
		t.Errorf("agent still in the water after 3 simulated minutes\n%s", traceTail(s, len(s.Steps)-10, len(s.Steps)))
	}
	if s.Air < 240 {
		t.Errorf("air not restored: %d/300 (want >= 240)", s.Air)
	}
	if n := emergenciesInland(s, 40); n > 2 {
		t.Errorf("EMERGENCY entered %d times (want <= 2): the frame is not held (flapping)\n%s", n, traceTail(s, 0, 60))
	}
	// Held: while the agent is still wet after first entering EMERGENCY, the stage must stay EMERGENCY.
	entered := false
	for i, st := range s.Steps {
		if st.Stage == system1.StageEmergency {
			entered = true
		}
		if entered && (st.InWater || st.Submerged) && st.Stage != system1.StageEmergency {
			t.Fatalf("emergency dropped while still in water at step %d (t=%d): flapping\n%s", i, st.Tick, traceTail(s, i-5, i+5))
		}
	}
	if !entered {
		t.Fatalf("a submerged agent with draining air never entered EMERGENCY\n%s", traceTail(s, 0, 20))
	}
}

// (2) Burning (cq-gpa.2 hold; campfire navigation hazard itself is cq-gpa.11, Java-side).
//
// UNFIXED BEHAVIOUR: able died at a campfire ("went up in flames"). Fire damage lands once per
// second; the damage-edge trigger popped the emergency after ~2 ticks and the routine resumed
// next to / inside the fire.
func TestPlaytest2_02_BurningLeavesCampfireAndStaysOut(t *testing.T) {
	w := groundWorld(40)
	w.AddLitCampfire(0, 0)
	w.AddTrunk(12, 0, 5, "")
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: simKit(),
	})
	s.Run(3 * simTicksPerMinute)

	if s.Dead {
		t.Fatalf("agent burned to death\n%s", traceTail(s, 0, 40))
	}
	last := s.Steps[len(s.Steps)-1]
	if last.OnFire {
		t.Errorf("agent still burning after 3 simulated minutes\n%s", traceTail(s, len(s.Steps)-8, len(s.Steps)))
	}
	onCampfire := func(st eval.SimStep) bool {
		return int32(st.Pos.X) == 0 && int32(st.Pos.Z) == 0 && st.Pos.X >= 0 && st.Pos.Z >= 0 && st.Pos.X < 1 && st.Pos.Z < 1
	}
	left := -1
	for i, st := range s.Steps {
		if !onCampfire(st) {
			left = i
			break
		}
	}
	if left < 0 {
		t.Fatalf("agent never left the campfire cell\n%s", traceTail(s, 0, 30))
	}
	for i := left; i < len(s.Steps); i++ {
		if onCampfire(s.Steps[i]) {
			t.Errorf("re-entered the lit campfire at t=%d after leaving at t=%d\n%s", s.Steps[i].Tick, s.Steps[left].Tick, traceTail(s, i-4, i+4))
			break
		}
	}
	if n := emergenciesInland(s, 40); n > 2 {
		t.Errorf("EMERGENCY entered %d times (want <= 2): not held\n%s", n, traceTail(s, 0, 60))
	}
}

// (3) 9-log trunk (cq-gpa.9, landed).
//
// UNFIXED BEHAVIOUR: the top 3-4 logs of tall trunks were left standing because reach was
// measured from the feet centre (4.5) instead of eye-to-AABB, so only logs to y+4 qualified.
func TestPlaytest2_03_NineLogTrunkFelledToEyeReach(t *testing.T) {
	w := groundWorld(30)
	w.AddTrunk(6, 0, 9, "")
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5},
		// No recipe graph: isolates tree reach from the planner deciding it has "enough wood".
		NoRecipeGraph: true,
	})
	s.Run(3 * simTicksPerMinute)

	var standing []int32
	for y := int32(64); y < 73; y++ {
		if _, ok := w.Blocks[eval.BlockPosKey{X: 6, Y: y, Z: 0}]; ok {
			standing = append(standing, y)
		}
	}
	got := 9 - len(standing)
	if got < 7 {
		t.Errorf("felled %d of 9 logs, want >= 7 (every log within eye reach); still standing y=%v; logs in inventory=%d",
			got, standing, s.ItemCount("minecraft:oak_log"))
	}
	if n := s.ItemCount("minecraft:oak_log"); n < 7 {
		t.Errorf("only %d logs collected (want >= 7)", n)
	}
	for _, h := range s.Harvests {
		if h.Existed && !h.InReach {
			t.Errorf("issued HARVEST_BREAK on %v which was out of reach (mod would reject it)", h.Cell)
		}
	}
}

// (4) Stone needs a pickaxe (cq-gpa.10).
//
// UNFIXED BEHAVIOUR: able and dave issued HARVEST_BREAK on minecraft:stone holding only an
// axe (no drops); the bootstrap tool ladder had no pickaxe and zero pickaxe crafts happened.
func TestPlaytest2_04_StoneNeedsPickaxeCraftedFirst(t *testing.T) {
	w := groundWorld(30)
	w.AddStoneOutcrop(2, -1, 3, 3)
	w.Blocks[eval.BlockPosKey{X: -1, Y: 64, Z: 0}] = "minecraft:crafting_table"
	inv := append(simKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 8},
		&civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageSettlement, World: w, TimeOfDay: 3000,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: inv,
		Setup: func(st *system1.AgentState) {
			st.SetStrategicDirective(system1.DirectiveActionMineStone, "stone", "gather cobblestone", 90, 1_000_000)
		},
	})
	s.Run(2 * simTicksPerMinute)

	firstStone := int64(-1)
	for _, h := range s.Harvests {
		if !strings.Contains(h.BlockID, "stone") {
			continue
		}
		if !h.HadTool {
			t.Errorf("HARVEST_BREAK on %s at t=%d with no pickaxe (inventory: %s): the break yields nothing", h.BlockID, h.Tick, h.Inv)
		}
		if firstStone < 0 {
			firstStone = h.Tick
		}
	}
	crafted := int64(-1)
	for _, c := range s.Crafts {
		if c.Recipe == "wooden_pickaxe" && c.OK {
			crafted = c.Tick
			break
		}
	}
	if crafted < 0 {
		t.Errorf("never crafted a wooden_pickaxe (crafts: %+v)", s.Crafts)
	}
	if firstStone >= 0 && crafted >= 0 && crafted > firstStone {
		t.Errorf("pickaxe crafted at t=%d, after the first stone break at t=%d", crafted, firstStone)
	}
	if s.ItemCount("minecraft:cobblestone") < 1 {
		t.Errorf("no cobblestone collected after 2 minutes of MINE_STONE (inventory: %s)", s.InventorySummary())
	}
}

// (5) Zombie engagement (cq-gpa.3).
//
// UNFIXED BEHAVIOUR: fred died to a mob. Fight-vs-flee was re-decided every frame from a
// moving confidence/HP estimate, so Attack.flee_immediately alternated true/false and the
// agent neither fought nor escaped.
func TestPlaytest2_05_ZombieEngagementDecidedOnce(t *testing.T) {
	run := func(t *testing.T, hp, zhp, zdmg float32) *eval.PlaytestSim {
		w := groundWorld(60)
		s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
			Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w,
			Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: simKit(), Health: hp,
		})
		s.Zombies = []*eval.SimZombie{{ID: 31, Pos: eval.SimVec{X: 8.5, Y: 64, Z: 0.5}, HP: zhp, Speed: 0.17, Damage: zdmg}}
		s.Run(20 * 20) // 20 simulated seconds: engagement plus escape, before the arena edge
		return s
	}
	flips := func(s *eval.PlaytestSim) (toFlee, toFight int) {
		for i := 1; i < len(s.AttackLog); i++ {
			a, b := s.AttackLog[i-1], s.AttackLog[i]
			if b.Tick-a.Tick > 40 { // a new engagement (>2s gap) may decide afresh
				continue
			}
			if !a.Flee && b.Flee {
				toFlee++
			}
			if a.Flee && !b.Flee {
				toFight++
			}
		}
		return
	}

	t.Run("winnable_fight_is_held_to_the_end", func(t *testing.T) {
		s := run(t, 20, 20, 2)
		if len(s.AttackLog) == 0 {
			t.Fatal("no Attack intents against an approaching zombie")
		}
		toFlee, toFight := flips(s)
		if toFlee+toFight != 0 {
			t.Errorf("fight/flee flipped (%d to-flee, %d to-fight) in a winnable fight; attacks=%+v", toFlee, toFight, s.AttackLog)
		}
		if !s.Zombies[0].Dead || s.Dead {
			t.Errorf("expected to win: zombieDead=%v agentDead=%v hp=%.0f", s.Zombies[0].Dead, s.Dead, s.HP)
		}
	})
	t.Run("losing_fight_escalates_to_flee_once", func(t *testing.T) {
		s := run(t, 20, 80, 6)
		toFlee, toFight := flips(s)
		if toFight != 0 || toFlee > 1 {
			t.Errorf("flee flag alternated: %d fight->flee, %d flee->fight (want <=1 and 0); attacks=%+v", toFlee, toFight, s.AttackLog)
		}
		if s.Dead {
			t.Errorf("agent died fighting a zombie it should have fled (hp=0)")
		}
	})
}

func hutRegistry() *system1.EngineRegistry {
	bp := &blueprints.Blueprint{
		ID: "test_hut", Name: "Test Hut", Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{Width: 2, Height: 1, Length: 2},
		Palette:    map[int]string{1: "minecraft:oak_planks"},
		Layers:     []blueprints.Layer{{Y: 0, Matrix: [][]int{{1, 1}, {1, 1}}}},
	}
	reg := system1.NewDefaultEngineRegistry()
	reg.RegisterEvaluator(system1.NewBlueprintEvaluator(bp))
	return reg
}

// (6) Settlement builds (cq-gpa.6, landed).
//
// UNFIXED BEHAVIOUR: zero BuildBlock/Place intents in any agent; the SETTLEMENT stage only
// looped GATHER_TIMBER / SLEEP even with a recommended blueprint and materials in the pack.
func TestPlaytest2_06_SettlementPlacesRecommendedBlueprintByDay(t *testing.T) {
	w := groundWorld(30)
	inv := append(simKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 16})
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "BUILDER", Stage: system1.StageSettlement, World: w, Registry: hutRegistry(),
		Start: eval.SimVec{X: 3.5, Y: 64, Z: 3.5}, Inventory: inv, TimeOfDay: 1000,
		Setup: func(st *system1.AgentState) { st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}) },
	})
	s.Run(2 * simTicksPerMinute)

	if len(s.Placed) != 4 {
		t.Errorf("placed %d blocks, want exactly 4 (the 2x2 hut, then stop): %v\n%s", len(s.Placed), s.Placed, traceTail(s, 0, 40))
	}
	for _, k := range s.Placed {
		if w.Blocks[k] != "minecraft:oak_planks" {
			t.Errorf("block at %v is %q, want oak_planks", k, w.Blocks[k])
		}
	}
	if left := s.ItemCount("minecraft:oak_planks"); left != 12 {
		t.Errorf("planks left %d, want 12 (16 - 4 placed)", left)
	}
}

// (7) Craft latch (cq-dcx.2).
//
// UNFIXED BEHAVIOUR (craft spam seen in the playtest logs): the kernel re-emits the same
// CraftItem every frame until the inventory update lands, so one planks craft became several.
func TestPlaytest2_07_CraftNotReissuedWhilePending(t *testing.T) {
	const latency = int64(60) // ticks between intent and inventory change (mod server tick + receipt)
	w := groundWorld(30)
	w.AddTrunk(4, 0, 6, "")
	w.AddTrunk(4, 3, 6, "")
	s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
		Profession: "EXPLORER", Stage: system1.StageBootstrap, World: w,
		Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, CraftLatency: latency,
	})
	s.Run(3 * simTicksPerMinute)

	if len(s.CraftIssued) == 0 {
		t.Fatalf("no craft intents at all after 3 simulated minutes (bootstrap never crafted)")
	}
	last := map[string]int64{}
	for _, c := range s.CraftIssued {
		if prev, ok := last[c.Recipe]; ok && c.Tick < prev+latency {
			failed := false
			for _, ex := range s.Crafts {
				if ex.Recipe == c.Recipe && !ex.OK && ex.Tick >= prev && ex.Tick <= c.Tick {
					failed = true
				}
			}
			if !failed {
				t.Errorf("craft %q re-issued at t=%d only %d ticks after t=%d, before the first could land and with no failed receipt",
					c.Recipe, c.Tick, c.Tick-prev, prev)
			}
		}
		last[c.Recipe] = c.Tick
	}
}

// noRetreadBudget is the DESIGN BUDGET for the revisit ratio (fraction of 2x2-block cell
// entries that re-enter an already-visited cell) over a 5-minute exploration; not a measurement.
const noRetreadBudget = 0.25

// (8) No retread (cq-dcx.4 planning, cq-dcx.6 spatial memory use).
//
// UNFIXED BEHAVIOUR: agents retraced the same corridors repeatedly (positions clustered at
// 1-2 blocks; same targets re-picked) because nothing remembered where they had already been.
//
// Attractors: the agent is hungry in a world with no food, so the Foraging frame's outward
// wander drives five minutes of exploration (its old fixed +0.9 rad turn per 24-block leg closed
// a ~7-leg loop around the spawn and re-walked it: revisit ratio 0.756 before cq-dcx.4); a
// crafting table and a timber grid around the spawn are the familiar POIs that pull an agent
// without spatial memory back over the same ground. A sated explorer in the bounded arena is the
// control (frontier scouting legs; it must stay within budget too).
func TestPlaytest2_08_ExplorationDoesNotRetread(t *testing.T) {
	run := func(t *testing.T, half int32, food int32) float64 {
		w := groundWorld(half)
		for gx := int32(-40); gx <= 40; gx += 20 {
			for gz := int32(-40); gz <= 40; gz += 20 {
				w.AddTrunk(gx, gz, 5, "")
			}
		}
		w.Blocks[eval.BlockPosKey{X: 3, Y: 64, Z: 3}] = "minecraft:crafting_table"
		s := eval.NewPlaytestSim(eval.PlaytestSimConfig{
			Profession: "EXPLORER", Stage: system1.StageSettlement, World: w,
			Start: eval.SimVec{X: 0.5, Y: 64, Z: 0.5}, Inventory: simKit(), TimeOfDay: 1000,
		})
		if food > 0 {
			s.Food = food
		}
		s.Run(5 * simTicksPerMinute)
		walked := 0.0
		path := s.Path()
		for i := 1; i < len(path); i++ {
			walked += math.Hypot(path[i].X-path[i-1].X, path[i].Z-path[i-1].Z)
		}
		ratio := eval.RevisitRatio(path)
		t.Logf("revisit ratio over 5 simulated minutes = %.3f over %.0f blocks walked (design budget < %.2f)", ratio, walked, noRetreadBudget)
		if walked < 200 {
			t.Errorf("walked only %.0f blocks in 5 minutes: the scenario no longer exercises exploration", walked)
		}
		return ratio
	}
	t.Run("hungry_forager_open_world", func(t *testing.T) {
		if r := run(t, 100, 4); r >= noRetreadBudget {
			t.Errorf("revisit ratio %.3f >= design budget %.2f: the forager is retracing its own path", r, noRetreadBudget)
		}
	})
	t.Run("explorer_bounded_arena_control", func(t *testing.T) {
		if r := run(t, 50, 0); r >= noRetreadBudget {
			t.Errorf("revisit ratio %.3f >= design budget %.2f: the explorer is retracing its own path", r, noRetreadBudget)
		}
	})
}

// Metric sanity (runs always): the helper itself must separate a retrace from a straight line.
func TestPlaytest2_RevisitRatioMetric(t *testing.T) {
	var line, loop []eval.SimVec
	for i := 0; i < 100; i++ {
		line = append(line, eval.SimVec{X: float64(i), Z: 0})
	}
	for i := 0; i < 100; i++ {
		loop = append(loop, eval.SimVec{X: float64(i % 10), Z: 0})
	}
	if r := eval.RevisitRatio(line); r != 0 {
		t.Errorf("straight line revisit ratio = %.3f, want 0", r)
	}
	if r := eval.RevisitRatio(loop); r < 0.5 {
		t.Errorf("10-block corridor walked 10 times revisit ratio = %.3f, want >= 0.5", r)
	}
	if r := eval.RevisitRatio(nil); r != 0 {
		t.Errorf("empty path ratio = %.3f", r)
	}
}
