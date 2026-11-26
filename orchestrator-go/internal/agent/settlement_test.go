package agent_test

import (
	"context"
	"path/filepath"
	"testing"

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
	s.tick += 50
	s.rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: s.tick, TimeOfDay: 3000, HorizonSurvey: survey,
		Status: &civquestv1.AgentStatus{AgentId: s.name, Health: 20, MaxHealth: 20, FoodLevel: 20,
			Position: &civquestv1.Vec3{X: x, Y: 64, Z: z}},
	})
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
	chosen, ok := first.rt.System1State().Settlement()
	if !ok || chosen.Kind != system1.SettlementClanSite {
		t.Fatalf("no clan site after a good survey: %+v %t", chosen, ok)
	}
	if got := first.rt.Profession(); got != "FARMER" {
		t.Errorf("after settling: %q", got)
	}

	// A clanmate who found somewhere even better still joins the standing site.
	second := newSettler(t, res, civic, "bert")
	second.returnWith(-300, -300, 10, 6)
	if got, _ := second.rt.System1State().Settlement(); got != chosen {
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
}
