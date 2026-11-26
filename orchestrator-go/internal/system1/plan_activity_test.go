package system1

import (
	"math"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/spatialmem"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// ---- unit tests ----

func TestPlanToolGate_AdaptsBlockToolModel(t *testing.T) {
	cases := map[string]string{
		"minecraft:oak_log":     "",
		"minecraft:cobblestone": "minecraft:wooden_pickaxe",
		"minecraft:coal":        "minecraft:wooden_pickaxe",
		"minecraft:raw_iron":    "minecraft:stone_pickaxe",
		"minecraft:diamond":     "minecraft:iron_pickaxe",
		"minecraft:stick":       "",
	}
	for item, want := range cases {
		if got := planToolGate(item); got != want {
			t.Errorf("planToolGate(%s) = %q, want %q", item, got, want)
		}
	}
}

func TestPlanMineable_MatchesGatheredItem(t *testing.T) {
	if !planMineable("minecraft:stone", "minecraft:cobblestone") || planMineable("minecraft:coal_ore", "minecraft:cobblestone") {
		t.Fatal("cobblestone comes from stone, not ore")
	}
	if !planMineable("minecraft:deepslate_coal_ore", "minecraft:coal") || planMineable("minecraft:stone", "minecraft:coal") {
		t.Fatal("coal comes from coal ore only")
	}
	if planMineable("minecraft:oak_log", "minecraft:cobblestone") {
		t.Fatal("logs are not mined")
	}
}

func TestPlanningMemory_VisitOncePerCellEntryAndDepleted(t *testing.T) {
	var m planningMemory
	for tick := int64(0); tick < 40; tick += 4 {
		m.applyMemory(spatialVisit{X: 1, Z: 1, Tick: tick}) // dwell in one cell
	}
	c := m.Spatial.CellOf(1, 1)
	if st, _ := m.Spatial.Stat(c); st.Visits != 1 {
		t.Fatalf("dwelling in one cell must count one visit, got %d", st.Visits)
	}
	m.applyMemory(spatialVisit{X: 20, Z: 1, Tick: 44})
	m.applyMemory(spatialVisit{X: 1, Z: 1, Tick: 48})
	if st, _ := m.Spatial.Stat(c); st.Visits != 2 {
		t.Fatalf("re-entering the cell is a second visit, got %d", st.Visits)
	}
	m.applyMemory(spatialDepleted{Pos: spatialmem.Pos{X: 3, Z: 4}, Kind: spatialmem.KindTrunk, Tick: 50})
	if !m.Spatial.Depleted(spatialmem.Pos{X: 3, Z: 4}, spatialmem.KindTrunk, 60) {
		t.Fatal("felled trunk mark missing")
	}
	if m.applyMemory(surveyBackoff{}) {
		t.Fatal("non-planning writes must fall through")
	}
}

func TestPlanningMemory_BankStoreAndDone(t *testing.T) {
	var m planningMemory
	p := plan.Plan{ID: "p", Lease: plan.Lease{ExpiresTick: 1000}}
	m.applyStore([]htpda.StoreWrite{planBank{Plan: p, Target: planTarget{Kind: "trunk"}}})
	if !m.Bank.live(500) || m.Bank.live(1000) {
		t.Fatal("bank must be live until the plan lease expires")
	}
	m.applyStore([]htpda.StoreWrite{planBank{Plan: p, Done: true}})
	if m.Bank.Plan.ID != "" {
		t.Fatal("a completed plan clears the bank")
	}
}

func TestNoveltyHeading_DefaultWithEmptyMemoryAndSteersOffVisitedGround(t *testing.T) {
	var empty spatialmem.Snapshot
	if h := noveltyHeading(empty, 0, 0, 1.0, 24, 100, forageHeadingOffsets); h != 1.9 {
		t.Fatalf("empty memory must keep the default +0.9 turn, got %.3f", h)
	}
	// Walk the default heading's leg so it is covered; the steer must pick another.
	m := empty
	for d := 0.0; d <= 24; d += 4 {
		m = m.VisitAt(math.Cos(1.9)*d, math.Sin(1.9)*d, 90)
	}
	if h := noveltyHeading(m, 0, 0, 1.0, 24, 100, forageHeadingOffsets); h == 1.9 {
		t.Fatal("a covered default leg must be replaced by a novel heading")
	}
}

// ---- closed-loop FSM tests ----

type planRig struct {
	t   *testing.T
	fsm *AgentFSM
	st  *AgentState
	cfg *xdg.AgentIdentityConfig
}

func newPlanRig(t *testing.T, stage Stage) *planRig {
	st := NewAgentState()
	st.SetRecipeGraph(newTestRecipeGraph())
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	return &planRig{t: t, fsm: NewAgentFSMWithRegistry(stage, nil, nil), st: st, cfg: cfg}
}

func (r *planRig) tick(f *civquestv1.TelemetryFrame) []*civquestv1.AgentIntent {
	res := r.fsm.Tick(&FSMContext{AgentID: "replay", State: r.st, Config: r.cfg, Frame: f})
	var out []*civquestv1.AgentIntent
	for _, fi := range res.Intents {
		out = append(out, fi.Intent)
	}
	return out
}

func (r *planRig) planLocal() (planLocal, bool) {
	for _, f := range r.fsm.kernel.Frames() {
		if l, ok := f.Local.(planLocal); ok {
			return l, true
		}
	}
	return planLocal{}, false
}

func planNavTarget(in []*civquestv1.AgentIntent) (*civquestv1.Vec3, bool) {
	for _, i := range in {
		if n := i.GetNavigate(); n != nil && n.Target != nil {
			return n.Target, true
		}
	}
	return nil, false
}

func stoneKit() fo {
	return withInv(item(0, "minecraft:wooden_axe", 1), item(1, "minecraft:wooden_sword", 1), item(2, "minecraft:wooden_pickaxe", 1),
		item(3, "minecraft:oak_planks", 8), item(4, "minecraft:stick", 8))
}

// Commitment + hysteresis: a committed stone target is kept when a somewhat nearer one comes
// into view, and replaced only by a candidate more than 2x cheaper.
func TestPlanActivity_CommitmentHysteresis(t *testing.T) {
	r := newPlanRig(t, StageBootstrap)
	a := blk(8, 64, 0, "minecraft:stone")
	in := r.tick(rf(100, 3000, stoneKit(), withBlocks(a)))
	tg, ok := planNavTarget(in)
	if !ok || math.Floor(tg.X) != 8 {
		t.Fatalf("expected a navigate to the stone at x=8, got %v", in)
	}
	l, ok := r.planLocal()
	if !ok || l.Target.Kind != "mine" || l.Target.Pos[0] != 8 {
		t.Fatalf("plan must commit the stone target, local=%+v", l)
	}
	// Nearer (dist ~6 vs 7.5) but not 2x cheaper: hold the commitment.
	b := blk(0, 64, 6, "minecraft:stone")
	r.tick(rf(140, 3000, stoneKit(), withBlocks(a, b)))
	if l, _ := r.planLocal(); l.Target.Pos != [3]int32{8, 64, 0} {
		t.Fatalf("a slightly nearer stone must not preempt the committed one, target=%v", l.Target.Pos)
	}
	// Far cheaper (dist ~2.5): switch.
	c := blk(2, 64, 1, "minecraft:stone")
	r.tick(rf(180, 3000, stoneKit(), withBlocks(a, b, c)))
	if l, _ := r.planLocal(); l.Target.Pos != [3]int32{2, 64, 1} {
		t.Fatalf("a candidate past the hysteresis ratio must win, target=%v", l.Target.Pos)
	}
}

// Replan on invalidation: the plan walks to a known table; when the table is lost the plan
// is re-derived (site lost) and crafts its own table instead.
func TestPlanActivity_ReplansWhenSiteLost(t *testing.T) {
	r := newPlanRig(t, StageBootstrap)
	inv := withInv(item(0, "minecraft:oak_planks", 8), item(1, "minecraft:stick", 4))
	in := r.tick(rf(100, 3000, inv, withBlocks(blk(10, 64, 0, "minecraft:crafting_table"))))
	if tg, ok := planNavTarget(in); !ok || math.Floor(tg.X) != 10 {
		t.Fatalf("expected the plan to walk to the known table, got %v", in)
	}
	in = r.tick(rf(140, 3000, inv))
	l, _ := r.planLocal()
	if l.Replans != 1 || l.Invalid != plan.InvalidSiteLost {
		t.Fatalf("expected one site-lost replan, got replans=%d invalid=%q", l.Replans, l.Invalid)
	}
	if len(in) == 0 || in[0].GetCraftItem() == nil || !strings.Contains(in[0].GetCraftItem().RecipeId, "crafting_table") {
		t.Fatalf("after the replan the plan crafts its own table, got %v", in)
	}
}

// Resume after emergency: the plan frame (with its committed target) is suspended under the
// emergency frame and resumes with the same plan and target; a banked plan resumes after a pop.
func TestPlanActivity_ResumesAfterEmergencyAndBanksOnExit(t *testing.T) {
	r := newPlanRig(t, StageBootstrap)
	a := blk(8, 64, 0, "minecraft:stone")
	r.tick(rf(100, 3000, stoneKit(), withBlocks(a)))
	before, ok := r.planLocal()
	if !ok {
		t.Fatal("plan frame missing")
	}
	r.tick(rf(110, 3000, stoneKit(), withBlocks(a), withHP(6), withEntities(creeperAt(9, 2.5, 0.5, 2.5))))
	if top := r.fsm.ActiveActivityKind(); top != activityKindEmergency {
		t.Fatalf("expected emergency on top, got %s", top)
	}
	in := r.tick(rf(160, 3000, stoneKit(), withBlocks(a, blk(0, 64, 6, "minecraft:stone"))))
	after, ok := r.planLocal()
	if !ok || after.Plan.ID != before.Plan.ID || after.Target.Pos != before.Target.Pos {
		t.Fatalf("plan/target must survive the emergency: before=%+v after=%+v", before.Target, after.Target)
	}
	if tg, ok := planNavTarget(in); ok && math.Floor(tg.X) != 8 {
		t.Fatalf("resumed plan must keep walking to the committed stone, got %v", tg)
	}
	// The stone leaves view: nothing actionable, the plan frame pops and banks its progress.
	r.tick(rf(200, 3000, stoneKit()))
	if _, ok := r.planLocal(); ok {
		t.Fatal("plan frame with nothing actionable must yield")
	}
	p, ok := r.fsm.BankedPlan()
	if !ok || p.ID != before.Plan.ID {
		t.Fatalf("exit must bank the plan, got ok=%v id=%q", ok, p.ID)
	}
	r.tick(rf(240, 3000, stoneKit(), withBlocks(a)))
	if l, ok := r.planLocal(); !ok || l.Plan.ID != before.Plan.ID {
		t.Fatalf("the next plan frame must resume the banked plan, got %+v", l.Plan.ID)
	}
}

// No retread (closed loop): a hungry forager with no food in sight walks each wander leg to
// its end; with spatial memory consecutive legs do not fold back onto already-covered cells.
func TestPlanSpatialMemory_ForageWanderDoesNotRetread(t *testing.T) {
	r := newPlanRig(t, StageSettlement)
	pos := civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
	nav := civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	type cell struct{ x, z int32 }
	seen := map[cell]bool{}
	legs, revisits := 0, 0
	for tick := int64(100); tick < 100+40*200 && legs < 14; tick += 20 {
		in := r.tick(rf(tick, 3000, withInv(survivalKit()...), withFood(4), withPos(pos.X, pos.Y, pos.Z), withNav(nav)))
		tg, ok := planNavTarget(in)
		if !ok {
			nav = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
			continue
		}
		// walk the leg (teleport along it so the visits land in memory), then arrive
		for f := 0.25; f <= 1.0; f += 0.25 {
			x, z := pos.X+(tg.X-pos.X)*f, pos.Z+(tg.Z-pos.Z)*f
			r.tick(rf(tick+int64(f*16), 3000, withInv(survivalKit()...), withFood(4), withPos(x, pos.Y, z), withNav(civquestv1.NavigationState_NAVIGATION_STATE_PATHING)))
		}
		pos = civquestv1.Vec3{X: tg.X, Y: pos.Y, Z: tg.Z}
		nav = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
		c := cell{int32(math.Floor(tg.X / 8)), int32(math.Floor(tg.Z / 8))}
		if seen[c] {
			revisits++
		}
		seen[c] = true
		legs++
	}
	if legs < 10 {
		t.Fatalf("expected >= 10 wander legs, got %d", legs)
	}
	ratio := float64(revisits) / float64(legs)
	t.Logf("forage wander: %d legs, leg-end cell revisit ratio %.2f", legs, ratio)
	if ratio > 0.1 {
		t.Fatalf("leg ends revisit covered cells (ratio %.2f): the wander retreads", ratio)
	}
}
