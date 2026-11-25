package agent_test

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func makeRuntimeSurveyReport(ox, oy, oz float64, tick int64, forestSectors, waterSectors int) *civquestv1.HorizonSurveyReport {
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  bearing,
			HitDistance: 64.0,
			FeatureType: "OPEN_PLAINS",
			HitBlockId:  "minecraft:air",
			HitX:        ox + dx*64.0,
			HitY:        oy,
			HitZ:        oz + dz*64.0,
			OpenHorizon: true,
		}
	}
	for i := 0; i < forestSectors && i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i].FeatureType = "FOREST_CANOPY"
		sectors[i].HitBlockId = "minecraft:oak_leaves"
		sectors[i].HitDistance = 42.0
		sectors[i].HitX = ox + dx*42.0
		sectors[i].HitY = oy + 6.0
		sectors[i].HitZ = oz + dz*42.0
		sectors[i].ElevationDelta = 6.0
		sectors[i].OpenHorizon = false
	}
	for i := forestSectors; i < forestSectors+waterSectors && i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i].FeatureType = "WATER"
		sectors[i].HitBlockId = "minecraft:water"
		sectors[i].HitDistance = 36.0
		sectors[i].HitX = ox + dx*36.0
		sectors[i].HitY = oy - 1.0
		sectors[i].HitZ = oz + dz*36.0
		sectors[i].ElevationDelta = -1.0
		sectors[i].OpenHorizon = false
	}
	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: tick,
		MaxRange:      64.0,
		Sectors:       sectors,
	}
}

func TestRuntime_HorizonSurveyPersistAndLedgerSummary(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Save 3 clanmate configs -> resolveClanSize should detect ClanSize == 3
	for _, name := range []string{"aria", "bran", "cora"} {
		cfg := xdg.DefaultAgentIdentityConfig(name)
		cfg.ClanID = "pioneers"
		if err := res.SaveAgentConfig(cfg); err != nil {
			t.Fatalf("SaveAgentConfig(%s): %v", name, err)
		}
	}

	agentDB, err := db.OpenAgentDB(res.AgentDataDir("aria"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	ariaCfg, err := res.LoadAgentConfig("aria")
	if err != nil {
		t.Fatalf("LoadAgentConfig: %v", err)
	}
	rt, err := agent.NewRuntime(agent.Config{
		AgentID:        "aria",
		ClanID:         "pioneers",
		IdentityConfig: ariaCfg,
		Resolver:       res,
		AgentDB:        agentDB,
		SocketPath:     filepath.Join(tmpDir, "aria.sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	// Frame 1 with HorizonSurveyReport -> persists 36 sectors to map_graph.db, resolves ClanSize=3,
	// updates StrategicLedger summary, and immediately lets System 1 select a bearing!
	report := makeRuntimeSurveyReport(0, 64, 0, 100, 4, 2)
	frame := &civquestv1.TelemetryFrame{
		WorldTick:     100,
		TimeOfDay:     3000,
		HorizonSurvey: report,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	rt.HandleTelemetryFrame(frame)

	if gotSize := rt.System1State().GetClanSize(); gotSize != 3 {
		t.Fatalf("expected resolved ClanSize=3 from XDG configs, got %d", gotSize)
	}

	var recent []db.HorizonSector
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		recent, err = agentDB.GetRecentHorizonSectors(ctx, 36)
		if err == nil && len(recent) == 36 {
			break
		}
	}
	if err != nil {
		t.Fatalf("GetRecentHorizonSectors: %v", err)
	}
	if len(recent) != 36 {
		t.Fatalf("expected 36 persisted horizon_sectors in agentDB, got %d", len(recent))
	}

	summary := rt.System1State().LatestSurveySummary()
	if !strings.Contains(summary, "forest canopy") {
		t.Fatalf("expected non-empty LatestSurveySummary mentioning forest canopy, got %q", summary)
	}

	// Verify StrategicLedger picks up HorizonSurveySummary
	ledger, err := system2.AssembleStrategicLedger(ctx, agentDB, nil, nil, nil, nil, system2.PersonalProfile{
		AgentID: "aria",
		ClanID:  "pioneers",
	}, 14000)
	if err != nil {
		t.Fatalf("AssembleStrategicLedger: %v", err)
	}
	if ledger.HorizonSurveySummary == "" {
		t.Fatal("expected StrategicLedger.HorizonSurveySummary to be populated")
	}
}

func TestRuntime_RendezvousSyncMergesClanDBScoresSiteAndChats(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	agentDB, err := db.OpenAgentDB(res.AgentDataDir("aria"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	civicDB, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civicDB.Close()

	// Pre-merge peer explorer "bran" survey at (80, 64, 80) so collective scoring hits explorerCount >= 2
	peerSectors := make([]db.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		peerSectors[i] = db.HorizonSector{
			SectorIndex:  i,
			BearingDeg:   float64(i * 10),
			HitDistance:  64.0,
			FeatureType:  "OPEN_PLAINS",
			OpenHorizon:  true,
			SurveyedTick: 150,
		}
	}
	for i := 0; i < 6; i++ {
		peerSectors[i].FeatureType = "FOREST_CANOPY"
		peerSectors[i].HitBlockID = "minecraft:oak_leaves"
		peerSectors[i].HitDistance = 35.0
		peerSectors[i].OpenHorizon = false
	}
	for i := 6; i < 9; i++ {
		peerSectors[i].FeatureType = "WATER"
		peerSectors[i].HitBlockID = "minecraft:water"
		peerSectors[i].HitDistance = 30.0
		peerSectors[i].OpenHorizon = false
	}
	if err := civicDB.MergeExplorerHorizonSurvey(ctx, "bran", 80, 64, 80, 150, peerSectors); err != nil {
		t.Fatalf("MergeExplorerHorizonSurvey(bran): %v", err)
	}

	cfg := xdg.DefaultAgentIdentityConfig("aria")
	cfg.ClanID = "pioneers"
	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "aria",
		ClanID:          "pioneers",
		IdentityConfig:  cfg,
		Resolver:        res,
		AgentDB:         agentDB,
		CivicRegistryDB: civicDB,
		SocketPath:      filepath.Join(tmpDir, "aria.sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	st := rt.System1State()
	st.SetClanSize(4)
	st.SetRendezvousOrigin(0, 64, 0)

	// Aria completes a survey near (85, 64, 82), persists it, and then enters ExpeditionReturning
	ariaReport := makeRuntimeSurveyReport(85, 64, 82, 200, 6, 3)
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick:     200,
		TimeOfDay:     3000,
		HorizonSurvey: ariaReport,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 85, Y: 64, Z: 82},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})
	_ = rt.DrainIntents() // drain initial nav intent

	// An expedition frame returning within <= 20m of RendezvousOrigin (0, 64, 0) emits a
	// RendezvousReport event (covered by TestExpedition_* in system1); the runtime drains it
	// after the FSM tick.
	rt.AgentFSM().QueueRendezvousReport("Returned from 2-hop S expedition (~90m out) to share frontier map.")
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 4000,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 305,
		TimeOfDay: 4005,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})

	// 1. Verify SendChatIntent on CHAT_CHANNEL_LOCAL was emitted
	intents := rt.DrainIntents()
	var chatMsg string
	for _, in := range intents {
		if ch := in.GetChat(); ch != nil && ch.TargetChannel == civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL {
			chatMsg = ch.Message
			break
		}
	}
	if chatMsg == "" {
		t.Fatalf("expected local SendChatIntent upon rendezvous return, got intents %+v", intents)
	}
	if !strings.Contains(chatMsg, "expedition") {
		t.Fatalf("expected expedition report in local chat message, got %q", chatMsg)
	}

	// 2. Verify clan_horizon_sectors now includes both bran and aria, and CollectiveSiteChosen is set
	top, err := civicDB.ScoreCollectiveSettlementCandidates(ctx)
	if err != nil {
		t.Fatalf("ScoreCollectiveSettlementCandidates: %v", err)
	}
	if top == nil || top.ExplorerCount != 2 {
		t.Fatalf("expected merged candidate with ExplorerCount=2, got %+v", top)
	}
	if !st.CollectiveSiteChosen {
		t.Fatal("expected CollectiveSiteChosen=true after collective scoring at rendezvous")
	}

	// 3. Verify settlement_pois synced Aria's discovered POIs
	clanPOIs, err := civicDB.ListSettlementPOIs(ctx, "pioneers")
	if err != nil {
		t.Fatalf("ListSettlementPOIs: %v", err)
	}
	if len(clanPOIs) == 0 {
		t.Fatal("expected Aria's discovered POIs synced into settlement_pois on rendezvous")
	}
}
