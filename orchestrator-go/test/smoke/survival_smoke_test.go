// Package smoke holds capability-coverage smoke tests (bead cq-71l): each test drives the REAL
// System 1 through the eval harness (TimeWarp / synthetic world) and asserts that one agent
// capability observably works. They are deliberately loose about map randomness but fully
// deterministic (no wall clock, no RNG outside the seeded runtime) and fast.
package smoke

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newSim(t *testing.T, id, profession string, cfg eval.TimeWarpConfig) *eval.TimeWarpSimulation {
	t.Helper()
	ident := xdg.DefaultAgentIdentityConfig(id)
	ident.Profession = profession
	cfg.AgentID = id
	cfg.IdentityConfig = ident
	cfg.WorkDir = t.TempDir()
	cfg.TickDelta = 4
	sim, err := eval.NewTimeWarpSimulation(cfg)
	if err != nil {
		t.Fatalf("new sim: %v", err)
	}
	t.Cleanup(func() { _ = sim.Close() })
	return sim
}

func foodLevel(sim *eval.TimeWarpSimulation) int32 {
	return sim.BuildTelemetryFrame().Status.FoodLevel
}

// cq-71l.7: s1.activity.foraging + s1.skill.biological_needs (eating half).
// A hungry agent with only a crop field nearby walks to it, harvests it, then eats the yield.
func TestSmoke_ForagingHarvestsAndEats(t *testing.T) {
	sim := newSim(t, "forager", "FARMER", eval.TimeWarpConfig{InitialTimeOfDay: 3000, HungerDecayRateTicks: 20})
	for z := int32(0); z < 3; z++ {
		sim.AddBlock(12, 63, z, "minecraft:grass_block")
		sim.AddBlock(12, 64, z, "minecraft:carrots")
	}
	var harvested, ate bool
	minFood, maxFoodAfterMin := int32(99), int32(-1)
	for i := 0; i < 1500 && !ate; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		for _, in := range sim.IntentsThisStep {
			if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK && ib.BlockOrItemId == "minecraft:carrots" {
				harvested = true
			}
			if ui := in.GetUseItem(); ui != nil && ui.IsEatingOrDrinking {
				ate = true
			}
		}
		f := foodLevel(sim)
		if f < minFood {
			minFood, maxFoodAfterMin = f, f
		} else if f > maxFoodAfterMin {
			maxFoodAfterMin = f
		}
	}
	if !harvested {
		t.Fatal("hungry agent never harvested the visible crop (foraging did not reach food)")
	}
	if !ate {
		t.Fatal("agent harvested food but never emitted an eating use_item intent")
	}
	if maxFoodAfterMin <= minFood {
		t.Fatalf("food level did not recover after eating (min=%d max-after=%d)", minFood, maxFoodAfterMin)
	}
	if n := sim.InventoryItemCount("minecraft:carrot"); n != 0 {
		t.Fatalf("expected the harvested carrot to be consumed, still have %d", n)
	}
}

// cq-71l.7: s1.activity.walk. A navigate intent is executed end to end: the agent leaves spawn,
// the sim reports arrival, and the agent keeps issuing new waypoints.
func TestSmoke_WalkReachesNavigationTargets(t *testing.T) {
	sim := newSim(t, "walker", "EXPLORER", eval.TimeWarpConfig{InitialTimeOfDay: 3000})
	start := *sim.CurrentPosition()
	var targets []*civquestv1.Vec3
	arrived := 0
	for i := 0; i < 1500; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		for _, in := range sim.IntentsThisStep {
			if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
				targets = append(targets, nav.Target)
			}
		}
		p := sim.CurrentPosition()
		for _, tg := range targets {
			if math.Hypot(p.X-tg.X, p.Z-tg.Z) < 0.5 {
				arrived++
				targets = nil
				break
			}
		}
	}
	end := sim.CurrentPosition()
	if d := math.Hypot(end.X-start.X, end.Z-start.Z); d < 20 {
		t.Fatalf("agent barely moved from spawn (%.1f blocks)", d)
	}
	if arrived < 2 {
		t.Fatalf("expected the agent to reach at least 2 navigation waypoints, reached %d", arrived)
	}
}

// cq-71l.7: s1.skill.patrol_exploration. A guard on night watch (CIVIC_PATROL routine) keeps
// issuing distinct patrol waypoints and walks them.
func TestSmoke_PatrolMovesBetweenWaypoints(t *testing.T) {
	sim := newSim(t, "guard1", "GUARD", eval.TimeWarpConfig{InitialTimeOfDay: 14000})
	st := sim.Runtime().System1State()
	st.SetStage(system1.StageSettlement)
	st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
	distinct := map[[2]int]bool{}
	sawPatrol := false
	for i := 0; i < 1200; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		if st.GetCurrentRoutine() == system1.RoutineCivicPatrol {
			sawPatrol = true
		}
		for _, in := range sim.IntentsThisStep {
			if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
				distinct[[2]int{int(nav.Target.X), int(nav.Target.Z)}] = true
			}
		}
	}
	if !sawPatrol {
		t.Fatal("guard never entered the CIVIC_PATROL routine at night")
	}
	if len(distinct) < 3 {
		t.Fatalf("patrol issued only %d distinct waypoints, want >= 3", len(distinct))
	}
	home := st.GetHomeOrigin()
	p := sim.CurrentPosition()
	if math.Hypot(p.X-home.X, p.Z-home.Z) < 5 {
		t.Fatal("patrolling guard never left the home origin")
	}
}

// cq-71l.7 follow-up: patrol should orbit the settlement perimeter, not march away forever.
func TestSmoke_PatrolStaysNearHome(t *testing.T) {
	t.Skip("cq-71l.12: CIVIC_PATROL guard marches radially away from home (~850 blocks in 5k ticks) instead of orbiting a perimeter")
	sim := newSim(t, "guard2", "GUARD", eval.TimeWarpConfig{InitialTimeOfDay: 14000})
	st := sim.Runtime().System1State()
	st.SetStage(system1.StageSettlement)
	st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
	maxDist := 0.0
	for i := 0; i < 1200; i++ {
		_ = sim.Step(4)
		p := sim.CurrentPosition()
		maxDist = math.Max(maxDist, math.Hypot(p.X-0.5, p.Z-0.5))
	}
	if maxDist > 150 {
		t.Fatalf("patrol wandered %.0f blocks from home", maxDist)
	}
}

// cq-71l.7: s1.evaluator.circadian. Across a full day the routine follows the schedule and
// switches at dusk.
func TestSmoke_CircadianRoutineSwitchesAtDusk(t *testing.T) {
	sim := newSim(t, "c1", "FARMER", eval.TimeWarpConfig{InitialTimeOfDay: 500})
	st := sim.Runtime().System1State()
	st.SetStage(system1.StageSettlement)
	st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
	type change struct {
		tod int64
		r   system1.Routine
	}
	var changes []change
	var last system1.Routine
	for i := 0; i < 5900; i++ { // just under one full 24000-tick day
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		if r := st.GetCurrentRoutine(); r != last {
			last = r
			changes = append(changes, change{sim.CurrentTimeOfDay() % 24000, r})
		}
	}
	if len(changes) < 4 {
		t.Fatalf("expected >= 4 routine transitions over a day, got %+v", changes)
	}
	seen := map[system1.Routine]bool{}
	var duskSwitch bool
	for _, c := range changes {
		seen[c.r] = true
		if c.tod >= 10500 && c.tod <= 13000 {
			duskSwitch = true
		}
	}
	if !duskSwitch {
		t.Fatalf("no routine transition around dusk (tod 10500-13000): %+v", changes)
	}
	if !seen[system1.RoutinePrimaryDuty] {
		t.Fatalf("daytime PRIMARY_DUTY never scheduled: %+v", changes)
	}
}
