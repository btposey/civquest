package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type settler struct {
	t    *testing.T
	rt   *agent.AgentRuntime
	name string
	tick int64
}

func newSettler(t *testing.T, res *xdg.Resolver, civic *db.CivicRegistryDB, name string) *settler {
	t.Helper()
	agentDB, err := db.OpenAgentDB(res.AgentDataDir(name))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	t.Cleanup(func() { _ = agentDB.Close() })
	cfg := xdg.DefaultAgentIdentityConfig(name)
	cfg.ClanID, cfg.Profession = "pioneers", "FARMER"
	rt, err := agent.NewRuntime(agent.Config{
		AgentID: name, ClanID: "pioneers", IdentityConfig: cfg, Resolver: res,
		AgentDB: agentDB, CivicRegistryDB: civic,
		SocketPath: filepath.Join(t.TempDir(), name+".sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.System1State().SetClanSize(3)
	return &settler{t: t, rt: rt, name: name, tick: 100}
}

func (s *settler) frame(x, z float64, survey *civquestv1.HorizonSurveyReport) {
	s.see(x, z, func(f *civquestv1.TelemetryFrame) { f.HorizonSurvey = survey })
}

func (s *settler) see(x, z float64, add func(*civquestv1.TelemetryFrame)) {
	s.tick += 50
	f := &civquestv1.TelemetryFrame{
		WorldTick: s.tick, TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{AgentId: s.name, Health: 20, MaxHealth: 20, FoodLevel: 20,
			Position: &civquestv1.Vec3{X: x, Y: 64, Z: z}},
	}
	add(f)
	s.rt.HandleTelemetryFrame(f)
	s.rt.DrainIntents()
}

// returnWith surveys a region and then reports it at the rendezvous.
func (s *settler) returnWith(x, z float64, forest, water int) {
	s.frame(x, z, makeRuntimeSurveyReport(x, 64, z, s.tick+50, forest, water))
	s.rt.AgentFSM().QueueRendezvousReport("Returned from the frontier.")
	s.frame(1, 1, nil)
}

// cq-6l3.14: the clan's selection is one shared decision, and it ends the explorer phase.
func TestSettlement_ClanSelectsOneSiteAndMembersRememberIt(t *testing.T) {
	res := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	civic, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civic.Close()

	first := newSettler(t, res, civic, "aria")
	if got := first.rt.Profession(); got != "EXPLORER" {
		t.Fatalf("before settling: %q", got)
	}
	first.returnWith(200, 200, 8, 4)
	// One member alone does not commit the clan (cq-gey.3).
	if st, ok := first.rt.System1State().Settlement(); ok {
		t.Fatalf("one member settled the clan: %+v", st)
	}
	second := newSettler(t, res, civic, "bert")
	second.returnWith(200, 200, 8, 4)
	chosen, ok := second.rt.System1State().Settlement()
	if !ok || chosen.Kind != system1.SettlementClanSite {
		t.Fatalf("no clan site after two members agreed: %+v %t", chosen, ok)
	}
	first.frame(1, 1, nil)
	first.rt.AgentFSM().QueueRendezvousReport("Back at camp.")
	first.frame(1, 1, nil)
	if got := first.rt.Profession(); got != "SETTLER" {
		t.Errorf("a new member: %q", got)
	}

	// A clanmate who found somewhere even better still joins the standing site.
	third := newSettler(t, res, civic, "carl")
	third.returnWith(-300, -300, 10, 6)
	if got, _ := third.rt.System1State().Settlement(); got != chosen {
		t.Errorf("clanmate settled %+v, clan chose %+v", got, chosen)
	}

	// A restarted agent remembers without returning to the rendezvous again.
	first.rt.Stop()
	restarted := newSettler(t, res, civic, "aria")
	if got, _ := restarted.rt.System1State().Settlement(); got != chosen {
		t.Errorf("after restart %+v, want %+v", got, chosen)
	}
}

// cq-6l3.14: ground that is mostly ridges and blocked sight lines is not selected.
func TestSettlement_PoorGroundIsNotSelected(t *testing.T) {
	res := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	civic, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civic.Close()

	ridges := make([]db.HorizonSector, 36)
	for i := range ridges {
		ridges[i] = db.HorizonSector{SectorIndex: i, BearingDeg: float64(i * 10), HitDistance: 6,
			FeatureType: "RIDGE_OBSTRUCTION", Occluded: true, SurveyedTick: 90}
	}
	if err := civic.MergeExplorerHorizonSurvey(context.Background(), "bran", 40, 64, 40, 90, ridges); err != nil {
		t.Fatalf("merging peer survey: %v", err)
	}
	s := newSettler(t, res, civic, "aria")
	report := makeRuntimeSurveyReport(42, 64, 42, 150, 0, 0)
	for _, sec := range report.Sectors {
		sec.FeatureType, sec.Occluded, sec.OpenHorizon = "RIDGE_OBSTRUCTION", true, false
	}
	s.frame(42, 42, report)
	s.rt.AgentFSM().QueueRendezvousReport("Nothing but cliffs.")
	s.frame(1, 1, nil)

	if st, ok := s.rt.System1State().Settlement(); ok {
		t.Errorf("settled on ridges: %+v", st)
	}
	if site, err := civic.GetClanSite(context.Background(), "pioneers"); err != nil || site != nil {
		t.Errorf("clan site recorded: %+v %v", site, err)
	}
	expectNoVote(t, res, "aria", `"why":"below_score"`)
}

// expectNoVote requires a recorded reason for a report that endorsed nothing (cq-ix1).
func expectNoVote(t *testing.T, res *xdg.Resolver, name string, want ...string) {
	t.Helper()
	out, err := os.ReadFile(res.AgentDebugJSONLPath(name))
	if err != nil {
		t.Fatalf("reading the debug log: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, `"category":"SETTLEMENT_NO_VOTE"`) {
			continue
		}
		found := true
		for _, w := range want {
			found = found && strings.Contains(line, w)
		}
		if found {
			return
		}
	}
	t.Errorf("no SETTLEMENT_NO_VOTE event with %v in:\n%s", want, out)
}

// cq-6l3.20: wooded flat ground alone is not enough; water, ore or a cave, or livestock tips it.
func TestSettlement_SightingsDecideWhetherWoodedGroundIsChosen(t *testing.T) {
	cases := []struct {
		name  string
		water int
		see   func(f *civquestv1.TelemetryFrame)
		want  bool
	}{
		{name: "wood alone", want: false},
		{name: "water", water: 3, want: true},
		{name: "livestock", want: true, see: func(f *civquestv1.TelemetryFrame) {
			f.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 7, EntityType: "minecraft:cow", Position: &civquestv1.Vec3{X: 208, Y: 64, Z: 206}}}
		}},
		{name: "cave entrance", want: true, see: func(f *civquestv1.TelemetryFrame) {
			f.InterestingBlocks = []*civquestv1.VisibleBlock{{BlockId: db.CaveEntranceBlockID, Pos: &civquestv1.BlockPosProto{X: 210, Y: 65, Z: 212}}}
		}},
		{name: "exposed ore", want: true, see: func(f *civquestv1.TelemetryFrame) {
			f.InterestingBlocks = []*civquestv1.VisibleBlock{{BlockId: "minecraft:iron_ore", Pos: &civquestv1.BlockPosProto{X: 190, Y: 64, Z: 196}}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
			civic, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
			if err != nil {
				t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
			}
			defer civic.Close()
			// A clanmate has surveyed the same wooded ground.
			peer := makeRuntimeSurveyReport(204, 64, 204, 90, 6, c.water)
			sectors := make([]db.HorizonSector, 0, len(peer.Sectors))
			for _, sec := range peer.Sectors {
				sectors = append(sectors, db.HorizonSector{SectorIndex: int(sec.SectorIndex), FeatureType: sec.FeatureType, SurveyedTick: 90})
			}
			// Ridges make the ground plain: wood and open sectors alone score below the bar.
			for i := 24; i < 36; i++ {
				sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
				peer.Sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
			}
			if err := civic.MergeExplorerHorizonSurvey(context.Background(), "bran", 204, 64, 204, 90, sectors); err != nil {
				t.Fatalf("merging peer survey: %v", err)
			}
			endorse(t, civic, "bran", 204, 204)
			s := newSettler(t, res, civic, "aria")
			survey := makeRuntimeSurveyReport(200, 64, 200, 150, 6, c.water)
			for i := 24; i < 36; i++ {
				survey.Sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
			}
			s.frame(200, 200, survey)
			if c.see != nil {
				s.see(200, 200, c.see)
			}
			s.rt.AgentFSM().QueueRendezvousReport("Back from the woods.")
			s.frame(1, 1, nil)
			deadline := time.Now().Add(2 * time.Second)
			for c.want && !s.rt.System1State().SettlementEstablished() && time.Now().Before(deadline) {
				// Sightings are written to the agent database in the background.
				time.Sleep(20 * time.Millisecond)
				s.rt.AgentFSM().QueueRendezvousReport("Back again.")
				s.frame(1, 1, nil)
			}
			got := s.rt.System1State().SettlementEstablished()
			if got != c.want {
				ranked, _ := civic.RankCollectiveSettlementCandidates(context.Background())
				t.Errorf("settled=%t, want %t; candidates %+v", got, c.want, ranked)
			}
		})
	}
}

// endorse makes a living clanmate who has voted for the site at (x, z).
func endorse(t *testing.T, civic *db.CivicRegistryDB, name string, x, z float64) {
	t.Helper()
	ctx := context.Background()
	if err := civic.MemberHeartbeat(ctx, "pioneers", name, 100); err != nil {
		t.Fatalf("MemberHeartbeat: %v", err)
	}
	if err := civic.VoteForSite(ctx, "pioneers", name, db.ClanSite{X: x, Z: z, ChosenTick: 100}); err != nil {
		t.Fatalf("VoteForSite: %v", err)
	}
}

// cq-gey.3: the clan does not settle beside a registered danger, however good the ground.
func TestSettlement_NoSiteBesideADanger(t *testing.T) {
	res := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	civic, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civic.Close()
	if err := civic.PublishDanger(context.Background(), db.DangerSpot{Kind: "drowned", X: 230, Y: 62, Z: 200, SeenTick: 100}, 16); err != nil {
		t.Fatalf("PublishDanger: %v", err)
	}
	first, second := newSettler(t, res, civic, "aria"), newSettler(t, res, civic, "bert")
	first.returnWith(200, 200, 10, 6)
	second.returnWith(200, 200, 10, 6)
	if st, ok := second.rt.System1State().Settlement(); ok {
		t.Fatalf("settled beside a Drowned: %+v", st)
	}
	expectNoVote(t, res, "bert", `"why":"danger"`, `"kind":"drowned"`)
}

func TestSettlement_Quorum(t *testing.T) {
	for _, c := range []struct{ living, clan, want int }{{6, 6, 4}, {3, 6, 2}, {2, 6, 2}, {1, 6, 2}, {1, 1, 1}} {
		if got := agent.SiteQuorum(c.living, c.clan); got != c.want {
			t.Errorf("%d living of %d: need %d, want %d", c.living, c.clan, got, c.want)
		}
	}
}
