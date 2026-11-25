package system1

import (
	"fmt"
	"math"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// expRig drives an AgentFSM through the expedition frames: a SETTLEMENT explorer on PRIMARY_DUTY
// whose scout-frontier directive starts the expedition (the replay "expedition_flank" setup).
type expRig struct {
	t   *testing.T
	fsm *AgentFSM
	st  *AgentState
	cfg *xdg.AgentIdentityConfig
}

func newExpRig(t *testing.T, id string, clan int) *expRig {
	t.Helper()
	st := NewAgentState()
	st.SetClanSize(clan)
	cfg := xdg.DefaultAgentIdentityConfig(id)
	cfg.Profession = "EXPLORER"
	st.SetStrategicDirective(DirectiveActionScoutFrontier, "south", "SCOUT_FRONTIER: scout", 100, 60000)
	return &expRig{t: t, fsm: NewAgentFSM(StageSettlement, nil), st: st, cfg: cfg}
}

func (r *expRig) step(tick int64, opts ...fo) FSMResult {
	r.t.Helper()
	f := rf(tick, 4000, append([]fo{withInv(survivalKit()...)}, opts...)...)
	f.Status.AgentId = r.cfg.Name
	if sv := f.GetHorizonSurvey(); sv != nil {
		r.st.IngestHorizonSurveyReport(sv, f.WorldTick)
	}
	return r.fsm.Tick(&FSMContext{AgentID: r.cfg.Name, State: r.st, Config: r.cfg, Frame: f})
}

// stepR steps with a receipt addressed to the frame currently on top of the stack.
func (r *expRig) stepR(tick int64, rc *civquestv1.IntentReceipt, opts ...fo) FSMResult {
	r.t.Helper()
	if fr := r.fsm.ActivityFrames(); len(fr) > 0 {
		rc.FrameId = fr[len(fr)-1].ID
	}
	return r.step(tick, append(opts, func(f *civquestv1.TelemetryFrame) { f.Receipt = rc })...)
}

func (r *expRig) stack() string {
	var ks []string
	for _, f := range r.fsm.ActivityFrames() {
		ks = append(ks, f.Kind)
	}
	return strings.Join(ks, ">")
}

func firstIntent(res FSMResult) string {
	if len(res.Intents) == 0 {
		return "none"
	}
	return summarizeIntent(res.Intents[0].Intent)
}

func navTarget(t *testing.T, res FSMResult) (x, z float64) {
	t.Helper()
	for _, fi := range res.Intents {
		if n := fi.Intent.GetNavigate(); n != nil {
			return n.Target.X, n.Target.Z
		}
	}
	t.Fatalf("expected a Navigate intent, got %v", res.Intents)
	return 0, 0
}

func lushSurvey(ox, oz float64, tick int64) *civquestv1.HorizonSurveyReport {
	r := uniformSurvey(ox, oz, tick)
	for i := 0; i < 6; i++ {
		r.Sectors[i].FeatureType = "FOREST_CANOPY"
		r.Sectors[i].HitBlockId = "minecraft:oak_leaves"
		r.Sectors[i].HitDistance = 30
		r.Sectors[i].OpenHorizon = false
	}
	for i := 6; i < 9; i++ {
		r.Sectors[i].FeatureType = "WATER"
		r.Sectors[i].HitBlockId = "minecraft:water"
		r.Sectors[i].HitDistance = 32
		r.Sectors[i].OpenHorizon = false
	}
	return r
}

func TestExpedition_SurveyThenTraverseAsFrames(t *testing.T) {
	r := newExpRig(t, "pioneer", 1)

	res := r.step(110)
	if !strings.HasPrefix(firstIntent(res), "Survey(") || r.stack() != "expedition>survey" {
		t.Fatalf("expected survey frame over expedition, got %s %s", firstIntent(res), r.stack())
	}
	// Holding steady while the sweep runs: no intents, survey keeps the top.
	if res = r.step(130); len(res.Intents) != 0 || r.stack() != "expedition>survey" {
		t.Fatalf("survey must hold steady, got %v %s", res.Intents, r.stack())
	}
	if !r.st.IsExpeditionSurveying(130) || !r.st.IsExpeditionActive() {
		t.Fatal("derived accessors must reflect the frames")
	}

	res = r.step(172, withSurvey(uniformSurvey(0, 0, 172)))
	if !strings.HasPrefix(firstIntent(res), "Navigate(") || r.stack() != "expedition>navigate" {
		t.Fatalf("survey report must pop into a navigate frame, got %s %s", firstIntent(res), r.stack())
	}
	if active, hops, flanks, _ := r.fsm.ExpeditionStatus(); !active || hops != 0 || flanks != 0 {
		t.Fatalf("status = %v %d %d", active, hops, flanks)
	}
}

func TestExpedition_SurveyTimeoutResurveysAfterGuard(t *testing.T) {
	r := newExpRig(t, "pioneer", 1)
	if res := r.step(110); !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected initial survey, got %s", firstIntent(res))
	}
	// Lease (120 ticks) expires without a report: Timeout pops the survey and the expedition re-sweeps.
	res := r.step(231)
	if !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected recovery survey after the survey lease timed out, got %s (%s)", firstIntent(res), r.stack())
	}
}

func TestExpedition_FlankIsReplaceWithExtendedBlockedList(t *testing.T) {
	r := newExpRig(t, "scout", 2)
	ravine := uniformSurvey(0, 20, 250)
	for i := 0; i < 36; i++ {
		if i <= 13 || i >= 32 {
			ravine.Sectors[i].Occluded = true
			ravine.Sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
			ravine.Sectors[i].HitDistance = 6.0
			ravine.Sectors[i].OpenHorizon = false
		}
	}
	initial := uniformSurvey(0, 0, 120)
	initial.Sectors[0].FeatureType = "FOREST_CANOPY"
	initial.Sectors[0].HitBlockId = "minecraft:oak_leaves"
	initial.Sectors[0].HitDistance = 48
	initial.Sectors[0].OpenHorizon = false

	r.step(110)
	res := r.step(125, withSurvey(initial))
	_, tz := navTarget(t, res)
	if tz < 20 {
		t.Fatalf("expected traverse south (+Z) toward forest, got z=%.1f", tz)
	}

	// Stuck receipt mid-traverse at the rim: the Navigate frame pushes a one-attempt Recover
	// (sidestep); telemetry stuck alone never does.
	res = r.step(170, withPos(0, 64, 20), withNav(navStuck))
	if r.stack() != "expedition>navigate" {
		t.Fatalf("telemetry stuck must not push Recover, stack=%s", r.stack())
	}
	res = r.stepR(180, terrainStuck(2), withPos(0, 64, 20))
	if !strings.HasPrefix(firstIntent(res), "Navigate(") || r.stack() != "expedition>navigate>recover" {
		t.Fatalf("expected Recover sidestep, got %s %s", firstIntent(res), r.stack())
	}
	// The single attempt times out: Recover gives up, Navigate pops Blocked: hazard POI +
	// fresh survey, blocked list = 1.
	res = r.step(205, withPos(0, 64, 20))
	if !strings.HasPrefix(firstIntent(res), "Survey(") || r.stack() != "expedition>survey" {
		t.Fatalf("expected rim survey, got %s %s", firstIntent(res), r.stack())
	}
	if hz := r.st.SpatialIndex().FindPOIsInRadius(&civquestv1.Vec3{X: 0, Y: 64, Z: 23}, POICategoryHazard, 4.0); len(hz) == 0 || hz[0].BlockID != "civquest:ravine_barrier" {
		t.Fatalf("expected ravine barrier hazard POI ahead of the rim, got %+v", hz)
	}
	if _, _, flanks, _ := r.fsm.ExpeditionStatus(); flanks != 1 {
		t.Fatalf("FlankCount == len(Blocked) == 1 after the rim hit, got %d", flanks)
	}

	// Rim survey completes: detour away from blocked south.
	res = r.step(255, withPos(0, 64, 20), withSurvey(ravine))
	tx, _ := navTarget(t, res)
	if tx <= 5 {
		t.Fatalf("expected eastward detour (+X), got x=%.1f", tx)
	}

	// Stuck again at the same rim: flank = Replace of the expedition with an extended Blocked list.
	before := r.fsm.ActivityFrames()
	r.stepR(280, terrainStuck(3), withPos(1, 64, 20.5))
	res = r.step(305, withPos(1, 64, 20.5))
	if !strings.HasPrefix(firstIntent(res), "Navigate(") || r.stack() != "expedition>navigate" {
		t.Fatalf("expected flank navigate, got %s %s", firstIntent(res), r.stack())
	}
	if _, _, flanks, _ := r.fsm.ExpeditionStatus(); flanks != 2 {
		t.Fatalf("expected len(Blocked) == 2 after the flank, got %d", flanks)
	}
	after := r.fsm.ActivityFrames()
	if before[1].ID == after[1].ID {
		t.Fatal("a flank must replace the expedition frame (new frame ID)")
	}
	if before[0].Parent != after[0].Parent {
		t.Fatal("OpReplace keeps the parent")
	}
}

func TestExpedition_SoloNeverReturnsAndClaimsHomestead(t *testing.T) {
	r := newExpRig(t, "nomad", 1)
	r.st.SetRendezvousOrigin(0, 64, 0)

	r.step(110)
	x1, z1 := navTarget(t, r.step(125, withSurvey(uniformSurvey(0, 0, 125))))

	// Arrival: hop 1 ends the scout directive; the expedition continues on its own and re-surveys.
	res := r.step(200, withPos(x1, 64, z1))
	if !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected chained survey after hop 1, got %s (%s)", firstIntent(res), r.stack())
	}
	if _, active := r.st.ActiveStrategicDirective(200); active {
		t.Fatal("scout-frontier directive must end after the first hop")
	}
	if r.stack() != "expedition>survey" {
		t.Fatalf("hand-off keeps the expedition frame, got %s", r.stack())
	}
	if _, hops, _, _ := r.fsm.ExpeditionStatus(); hops != 1 {
		t.Fatalf("hops = %d, want 1", hops)
	}

	x2, z2 := navTarget(t, r.step(270, withPos(x1, 64, z1), withSurvey(uniformSurvey(x1, z1, 270))))
	res = r.step(375, withPos(x2, 64, z2))
	if _, _, _, returning := r.fsm.ExpeditionStatus(); returning {
		t.Fatal("solo explorer (ClanSize=1) must NEVER return to the rendezvous origin")
	}
	if !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected solo explorer to keep chaining surveys, got %s", firstIntent(res))
	}

	x3, z3 := navTarget(t, r.step(455, withPos(x2, 64, z2), withSurvey(lushSurvey(x2, z2, 450))))
	r.step(560, withPos(x3, 64, z3))
	if !r.st.HomesteadClaimed {
		t.Fatalf("expected solo homestead claim at the third hop (stack %s)", r.stack())
	}
	if math.Hypot(r.st.HomesteadPos[0]-x3, r.st.HomesteadPos[2]-z3) > 0.1 {
		t.Fatalf("homestead at %+v, want (%.1f,%.1f)", r.st.HomesteadPos, x3, z3)
	}
	if active, _, _, _ := r.fsm.ExpeditionStatus(); active {
		t.Fatal("the expedition frame must have exited on the claim")
	}
	if rep := r.fsm.DrainRendezvousReports(); len(rep) != 0 {
		t.Fatalf("solo explorer must never emit a rendezvous report, got %v", rep)
	}
}

func TestExpedition_ClanReturnEmitsRendezvousReportEvent(t *testing.T) {
	r := newExpRig(t, "aria", 6)
	r.st.SetRendezvousOrigin(0, 64, 0)

	r.step(110)
	x1, z1 := navTarget(t, r.step(125, withSurvey(uniformSurvey(0, 0, 125))))
	r.step(200, withPos(x1, 64, z1)) // hop 1 -> survey
	x2, z2 := navTarget(t, r.step(270, withPos(x1, 64, z1), withSurvey(uniformSurvey(x1, z1, 270))))

	// Hop 2 arrival: multi-member clans return to the rendezvous origin.
	res := r.step(400, withPos(x2, 64, z2))
	hx, hz := navTarget(t, res)
	if hx != 0 || hz != 0 {
		t.Fatalf("expected return navigate to the rendezvous origin, got (%.1f,%.1f)", hx, hz)
	}
	if _, _, _, returning := r.fsm.ExpeditionStatus(); !returning {
		t.Fatal("expected a returning expedition after 2 hops")
	}

	// Within 20m of camp: the expedition exits and a RendezvousReport event is emitted.
	r.step(550, withPos(12, 64, -10))
	reps := r.fsm.DrainRendezvousReports()
	if len(reps) != 1 || !strings.Contains(reps[0].Summary, "expedition") {
		t.Fatalf("expected one rendezvous report, got %+v", reps)
	}
	if active, _, _, _ := r.fsm.ExpeditionStatus(); active {
		t.Fatal("expedition must exit at the rendezvous origin")
	}
	if len(r.fsm.DrainRendezvousReports()) != 0 {
		t.Fatal("reports are drained exactly once")
	}
}

func TestExpedition_ClanExplorersFanOutOnHopZero(t *testing.T) {
	k := expeditionKnowledge{Survey: uniformSurvey(0, 0, 100), ClanSize: 6}
	frame := rf(110, 3000)
	pos := frame.Status.Position
	seen := map[int]bool{}
	for _, id := range []string{"aria", "bran", "cora", "dorn", "elena", "finn"} {
		b, _ := chooseBestSurveyBearing(id, frame, pos, k, expeditionLocal{})
		seen[int(b/10)] = true
	}
	if len(seen) < 3 {
		t.Fatalf("expected clan explorers to fan out, got %v", seen)
	}
}

func TestExpedition_EmergencyAbortsSurveyAndResweeps(t *testing.T) {
	r := newExpRig(t, "pioneer", 1)
	if res := r.step(110); !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected survey, got %s", firstIntent(res))
	}
	res := r.step(115, withHP(6), withEntities(creeperAt(99, 3, 0.5, 4)))
	if res.Stage != StageEmergency {
		t.Fatalf("expected emergency, got %s", res.Stage)
	}
	if got := r.stack(); !strings.HasSuffix(got, ">emergency") || !strings.Contains(got, "survey") {
		t.Fatalf("survey is suspended under the emergency frame, got %s", got)
	}
	// Threat gone well after the duplicate-survey window: the aborted sweep is redone.
	res = r.step(300)
	if !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected a fresh survey after the emergency, got %s (%s)", firstIntent(res), r.stack())
	}
}

func TestExpedition_ForagingPreemptsSurveyingNavigation(t *testing.T) {
	r := newExpRig(t, "forager", 1)
	r.step(110)
	_ = r.step(125, withSurvey(uniformSurvey(0, 0, 125))) // traverse
	res := r.step(150, withFood(6), withBlocks(&civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush", BlockStateString: "age=3"}))
	found := false
	for _, fi := range res.Intents {
		if fi.Intent.GetInteractBlock() != nil {
			found = true
		}
	}
	if !found || !strings.Contains(r.stack(), fmt.Sprintf(">%s", activityKindForaging)) {
		t.Fatalf("expected foraging to push over the navigate frame and harvest, got %v (%s)", res.Intents, r.stack())
	}
}

func TestExpedition_BootstrapStartsSurveyWhenNoTimberVisible(t *testing.T) {
	st := NewAgentState()
	st.SetClanSize(1)
	cfg := xdg.DefaultAgentIdentityConfig("pioneer")
	cfg.Profession = "EXPLORER"
	fsm := NewAgentFSM(StageBootstrap, nil)
	tick := func(n int64) FSMResult {
		f := rf(n, 3000)
		f.Status.AgentId = "pioneer"
		if sv := f.GetHorizonSurvey(); sv != nil {
			st.IngestHorizonSurveyReport(sv, n)
		}
		return fsm.Tick(&FSMContext{AgentID: "pioneer", State: st, Config: cfg, Frame: f})
	}
	tick(50)
	res := tick(100)
	if !strings.HasPrefix(firstIntent(res), "Survey(") {
		t.Fatalf("expected survey on an empty horizon, got %s", firstIntent(res))
	}
	if got := fsm.ActiveActivityKind(); got != activityKindSurvey {
		t.Fatalf("top frame = %q, want survey", got)
	}

	// Forest spotted east: survey pops, expedition navigates toward it and the POIs are indexed.
	report := uniformSurvey(0, 0, 172)
	dxE, dzE := bearingToUnitVector(270)
	report.Sectors[27].FeatureType = "FOREST_CANOPY"
	report.Sectors[27].HitBlockId = "minecraft:oak_leaves"
	report.Sectors[27].HitDistance = 42
	report.Sectors[27].HitX, report.Sectors[27].HitY, report.Sectors[27].HitZ = dxE*42, 70, dzE*42
	report.Sectors[27].OpenHorizon = false
	f := rf(175, 3000, withSurvey(report))
	f.Status.AgentId = "pioneer"
	st.IngestHorizonSurveyReport(report, 175)
	res = fsm.Tick(&FSMContext{AgentID: "pioneer", State: st, Config: cfg, Frame: f})
	x, z := navTarget(t, res)
	if x < 35 || math.Abs(z) > 2 {
		t.Fatalf("expected a target east along +X ~39m, got (%.1f,%.1f)", x, z)
	}
	if _, tree := findNearestTreeTrunk("pioneer", f, f.Status.Position, st); tree == nil {
		t.Fatal("expected the distant canopy POI to be recalled from the spatial index")
	}
}
