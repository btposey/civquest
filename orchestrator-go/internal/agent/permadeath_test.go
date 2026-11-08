package agent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"
)

func TestPermadeath_FullLifecycle_Murder(t *testing.T) {
	tmpDir := t.TempDir()
	serverID := "srv-alpha"
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir), xdg.WithServerID(serverID))

	agentID := "aria"
	clanID := "pioneers"
	agentUUID := "00000000-0000-0000-0000-000000000042"
	killerUUID := "player-griefer-uuid"
	killerName := "GrieferDan"
	deathTick := int64(5432)

	// 1. Initialize agent directories
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("failed initializing agent directories: %v", err)
	}

	// 2. Open AgentDB and seed initial relationships
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}

	ctx := context.Background()

	// Seed spouse relationship
	spouseRel := db.Relationship{
		EntityUUID:          "spouse-boran-uuid",
		EntityName:          "Boran",
		EntityType:          "AGENT",
		KinshipRole:         "SPOUSE",
		DispositionScore:    85.0,
		Familiarity:         0.9,
		LastInteractionTick: 5000,
	}
	if err := agentDB.UpsertRelationship(ctx, spouseRel); err != nil {
		t.Fatalf("failed seeding spouse relationship: %v", err)
	}

	// 3. Open ReputationDB and seed killer initial prestige
	repDB, err := db.OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed opening reputation DB: %v", err)
	}
	defer repDB.Close()

	if _, err := repDB.RecordPrestigeDelta(ctx, db.PrestigeLedgerEntry{
		PlayerUUID: killerUUID,
		PlayerName: killerName,
		Delta:      20.0,
		Reason:     "Initial standing",
		WorldTick:  1000,
	}); err != nil {
		t.Fatalf("failed seeding initial prestige: %v", err)
	}

	// 4. Construct death event
	deathEv := &civquestv1.DeathEvent{
		AgentId:           agentID,
		WorldTick:         deathTick,
		DamageSource:      "player",
		KillerEntityId:    101,
		KillerUuid:        killerUUID,
		KillerDisplayName: killerName,
		DeathLocation: &civquestv1.Vec3{
			X: 123.4,
			Y: 65.0,
			Z: -567.8,
		},
		DroppedLoot: []*civquestv1.ItemStackProto{
			{
				ItemId:     "minecraft:diamond_sword",
				Count:      1,
				Slot:       0,
				Durability: 1561,
			},
		},
	}

	req := agent.PermadeathRequest{
		AgentID:      agentID,
		AgentUUID:    agentUUID,
		ClanID:       clanID,
		DeathEvent:   deathEv,
		Resolver:     resolver,
		AgentDB:      agentDB,
		ReputationDB: repDB,
	}

	// 5. Execute permadeath
	res, err := agent.ExecutePermadeath(ctx, req)
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	// -------------------------------------------------------------------
	// Verification 1: Saliency 1.0 Memorial Record in AgentDB
	// -------------------------------------------------------------------
	if res.MemorialMemoryID <= 0 {
		t.Errorf("expected positive MemorialMemoryID, got %d", res.MemorialMemoryID)
	}

	// -------------------------------------------------------------------
	// Verification 2: Spouse Status Transition to WIDOWED
	// -------------------------------------------------------------------
	if !res.SpouseUpdated {
		t.Errorf("expected SpouseUpdated to be true")
	}

	// -------------------------------------------------------------------
	// Verification 3: Player Prestige Penalty (-100.0 for Murder)
	// -------------------------------------------------------------------
	if res.PrestigeDeducted != -100.0 {
		t.Errorf("expected -100.0 prestige deducted, got %.1f", res.PrestigeDeducted)
	}
	if res.PrestigeTargetPlayer != killerUUID {
		t.Errorf("expected target player %s, got %s", killerUUID, res.PrestigeTargetPlayer)
	}

	heroPrestige, err := repDB.GetHeroPrestige(ctx, killerUUID)
	if err != nil {
		t.Fatalf("failed retrieving updated hero prestige: %v", err)
	}
	// Initial was 20.0, murder is -100.0 -> cumulative is -80.0 ("VILLAIN")
	if heroPrestige.Score != -80.0 {
		t.Errorf("expected cumulative score -80.0, got %.1f", heroPrestige.Score)
	}
	if heroPrestige.Title != db.PrestigeTitleVillain {
		t.Errorf("expected title %s, got %s", db.PrestigeTitleVillain, heroPrestige.Title)
	}

	ledger, err := repDB.GetPrestigeLedger(ctx, killerUUID, 5)
	if err != nil || len(ledger) < 1 {
		t.Fatalf("failed retrieving prestige ledger: %v", err)
	}
	latestLedger := ledger[0]
	if latestLedger.Delta != -100.0 {
		t.Errorf("expected latest ledger delta -100.0, got %.1f", latestLedger.Delta)
	}
	expectedReason := fmt.Sprintf("Outlaw: Murdered agent %s", agentID)
	if latestLedger.Reason != expectedReason {
		t.Errorf("expected ledger reason %q, got %q", expectedReason, latestLedger.Reason)
	}

	// -------------------------------------------------------------------
	// Verification 4: Atomic Directory Relocation to Graveyard
	// -------------------------------------------------------------------
	sourceDir := resolver.AgentDataDir(agentID)
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Errorf("expected source directory %s to have been relocated and not exist", sourceDir)
	}

	expectedGraveyardDir, err := resolver.GraveyardAgentDir(agentID, agentUUID, deathTick)
	if err != nil {
		t.Fatalf("failed calculating expected graveyard dir: %v", err)
	}
	if res.GraveyardDir != expectedGraveyardDir {
		t.Errorf("expected graveyard dir %s, got %s", expectedGraveyardDir, res.GraveyardDir)
	}

	// Verify graveyard path incorporates server_id namespacing
	expectedSubpath := filepath.Join("servers", serverID, "graveyard", fmt.Sprintf("%s_%s_tick%d", agentID, agentUUID, deathTick))
	if !strings.Contains(res.GraveyardDir, expectedSubpath) {
		t.Errorf("expected graveyard dir to contain %q, got %s", expectedSubpath, res.GraveyardDir)
	}

	// -------------------------------------------------------------------
	// Verification 5: death_certificate.json and epitaph.txt Contents
	// -------------------------------------------------------------------
	certData, err := os.ReadFile(res.DeathCertificatePath)
	if err != nil {
		t.Fatalf("failed reading death certificate at %s: %v", res.DeathCertificatePath, err)
	}
	var cert agent.DeathCertificate
	if err := json.Unmarshal(certData, &cert); err != nil {
		t.Fatalf("failed unmarshaling death certificate: %v", err)
	}

	if cert.AgentID != agentID {
		t.Errorf("expected certificate agent_id %s, got %s", agentID, cert.AgentID)
	}
	if cert.OfflineUUID != agentUUID {
		t.Errorf("expected certificate offline_uuid %s, got %s", agentUUID, cert.OfflineUUID)
	}
	if cert.DeathTick != deathTick {
		t.Errorf("expected certificate death_tick %d, got %d", deathTick, cert.DeathTick)
	}
	if cert.DamageSource != "player" {
		t.Errorf("expected damage_source 'player', got %s", cert.DamageSource)
	}
	if cert.KillerUUID != killerUUID {
		t.Errorf("expected killer_uuid %s, got %s", killerUUID, cert.KillerUUID)
	}
	if cert.KillerDisplayName != killerName {
		t.Errorf("expected killer_display_name %s, got %s", killerName, cert.KillerDisplayName)
	}
	if cert.DeathLocation.X != 123.4 || cert.DeathLocation.Y != 65.0 || cert.DeathLocation.Z != -567.8 {
		t.Errorf("unexpected death coords in certificate: %+v", cert.DeathLocation)
	}
	if len(cert.DroppedLoot) != 1 || cert.DroppedLoot[0].ItemID != "minecraft:diamond_sword" {
		t.Errorf("unexpected dropped loot in certificate: %+v", cert.DroppedLoot)
	}
	if cert.ArchivedAt.IsZero() {
		t.Errorf("expected non-zero archived_at in death certificate")
	}

	epitaphData, err := os.ReadFile(res.EpitaphPath)
	if err != nil {
		t.Fatalf("failed reading epitaph at %s: %v", res.EpitaphPath, err)
	}
	expectedEpitaph := fmt.Sprintf(
		"Here lies %s of clan %s.\nPerished at world tick %d by %s.\nMay their deeds be remembered in the clan chronicles.\n",
		agentID, clanID, deathTick, "player",
	)
	if string(epitaphData) != expectedEpitaph {
		t.Errorf("epitaph mismatch:\nexpected:\n%q\ngot:\n%q", expectedEpitaph, string(epitaphData))
	}

	// -------------------------------------------------------------------
	// Verification 6: Read-Only Permissions on Archived memory.db
	// -------------------------------------------------------------------
	fi, err := os.Stat(res.MemoryDBPath)
	if err != nil {
		t.Fatalf("failed to stat archived memory.db at %s: %v", res.MemoryDBPath, err)
	}
	if fi.Mode().Perm() != 0400 {
		t.Errorf("expected memory.db permissions 0400, got %#o", fi.Mode().Perm())
	}

	// Verify archived database contents by opening read-only
	readOnlyDSN := fmt.Sprintf("file:%s?_pragma=query_only(1)&mode=ro", res.MemoryDBPath)
	roDB, err := sql.Open("sqlite", readOnlyDSN)
	if err != nil {
		t.Fatalf("failed opening read-only archived memory.db: %v", err)
	}
	defer roDB.Close()

	var count int
	var saliency float64
	var category string
	var summaryText string
	row := roDB.QueryRow(`
		SELECT count(*), saliency_score, event_category, summary_text
		FROM episodic_memories
		WHERE event_category = 'DEATH';
	`)
	if err := row.Scan(&count, &saliency, &category, &summaryText); err != nil {
		t.Fatalf("failed scanning memorial row in archived memory.db: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 death memory, got %d", count)
	}
	if saliency != 1.0 {
		t.Errorf("expected saliency 1.0, got %.2f", saliency)
	}
	if category != "DEATH" {
		t.Errorf("expected category 'DEATH', got %s", category)
	}
	expectedSummary := fmt.Sprintf("%s perished at tick %d by player (killer: %s)", agentID, deathTick, killerName)
	if summaryText != expectedSummary {
		t.Errorf("expected summary %q, got %q", expectedSummary, summaryText)
	}

	// Verify spouse transition to WIDOWED in archived database
	var spouseRole string
	var spouseDisp float64
	var spouseTick int64
	relRow := roDB.QueryRow(`
		SELECT kinship_role, disposition_score, last_interaction_tick
		FROM relationships
		WHERE entity_uuid = 'spouse-boran-uuid';
	`)
	if err := relRow.Scan(&spouseRole, &spouseDisp, &spouseTick); err != nil {
		t.Fatalf("failed scanning updated spouse relationship: %v", err)
	}
	if spouseRole != "WIDOWED" {
		t.Errorf("expected spouse kinship_role 'WIDOWED', got %s", spouseRole)
	}
	if spouseDisp != -10.0 {
		t.Errorf("expected spouse disposition_score -10.0, got %.1f", spouseDisp)
	}
	if spouseTick != deathTick {
		t.Errorf("expected spouse last_interaction_tick %d, got %d", deathTick, spouseTick)
	}
}

func TestPermadeath_CompanionProtectionPenalty(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	agentID := "lyra"
	clanID := "sentinels"
	agentUUID := "00000000-0000-0000-0000-000000000099"
	companionUUID := "player-companion-steve"
	companionName := "Steve"
	deathTick := int64(7890)

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}

	ctx := context.Background()

	// Seed companion relationship
	companionRel := db.Relationship{
		EntityUUID:          companionUUID,
		EntityName:          companionName,
		EntityType:          "PLAYER",
		KinshipRole:         "NONE",
		DispositionScore:    80.0,
		Familiarity:         0.85,
		LastInteractionTick: 7000,
	}
	if err := agentDB.UpsertRelationship(ctx, companionRel); err != nil {
		t.Fatalf("failed seeding companion relationship: %v", err)
	}

	repDB, err := db.OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed opening reputation DB: %v", err)
	}
	defer repDB.Close()

	// Initial score is 50.0 (HERO)
	if _, err := repDB.RecordPrestigeDelta(ctx, db.PrestigeLedgerEntry{
		PlayerUUID: companionUUID,
		PlayerName: companionName,
		Delta:      50.0,
		Reason:     "Heroic quests",
		WorldTick:  5000,
	}); err != nil {
		t.Fatalf("failed seeding initial prestige: %v", err)
	}

	// Mob death event (Creeper explosion)
	deathEv := &civquestv1.DeathEvent{
		AgentId:           agentID,
		WorldTick:         deathTick,
		DamageSource:      "minecraft:creeper",
		KillerEntityId:    202,
		KillerDisplayName: "Creeper",
		DeathLocation: &civquestv1.Vec3{
			X: 10.0,
			Y: 64.0,
			Z: 10.0,
		},
	}

	res, err := agent.ExecutePermadeath(ctx, agent.PermadeathRequest{
		AgentID:      agentID,
		AgentUUID:    agentUUID,
		ClanID:       clanID,
		DeathEvent:   deathEv,
		Resolver:     resolver,
		AgentDB:      agentDB,
		ReputationDB: repDB,
	})
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	// Verification: -50.0 prestige deducted for companion death
	if res.PrestigeDeducted != -50.0 {
		t.Errorf("expected -50.0 prestige deduction, got %.1f", res.PrestigeDeducted)
	}
	if res.PrestigeTargetPlayer != companionUUID {
		t.Errorf("expected target player %s, got %s", companionUUID, res.PrestigeTargetPlayer)
	}

	heroPrestige, err := repDB.GetHeroPrestige(ctx, companionUUID)
	if err != nil {
		t.Fatalf("failed getting hero prestige: %v", err)
	}
	// Initial was 50.0, penalty is -50.0 -> updated is 0.0 ("STRANGER")
	if heroPrestige.Score != 0.0 {
		t.Errorf("expected score 0.0, got %.1f", heroPrestige.Score)
	}
	if heroPrestige.Title != db.PrestigeTitleStranger {
		t.Errorf("expected title %s, got %s", db.PrestigeTitleStranger, heroPrestige.Title)
	}

	ledger, err := repDB.GetPrestigeLedger(ctx, companionUUID, 5)
	if err != nil || len(ledger) < 1 {
		t.Fatalf("failed retrieving ledger: %v", err)
	}
	if ledger[0].Reason != "Companion perished under player protection" {
		t.Errorf("expected reason 'Companion perished under player protection', got %q", ledger[0].Reason)
	}

	// Verification of Companion Memorial Chronicle recorded in battle
	if !res.CompanionMemorialWritten {
		t.Errorf("expected CompanionMemorialWritten = true")
	}
	if res.CompanionMemorialPath == "" {
		t.Errorf("expected non-empty CompanionMemorialPath")
	} else {
		content, readErr := os.ReadFile(res.CompanionMemorialPath)
		if readErr != nil {
			t.Fatalf("failed reading companion memorial file: %v", readErr)
		}
		if !strings.Contains(string(content), "Steve") || !strings.Contains(string(content), "Memorial of lyra") {
			t.Errorf("unexpected memorial chronicle content:\n%s", string(content))
		}
	}
	if res.CompanionMemorialMemoryID == 0 {
		t.Errorf("expected non-zero CompanionMemorialMemoryID in AgentDB")
	}
}

// =========================================================================
// Verification 7: Clean Runtime Exit With No Goroutine Leaks or Socket Remnants
// =========================================================================

type permadeathMockBridgeServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer

	mu          sync.Mutex
	stream      civquestv1.CivQuestBridgeService_ConnectAgentServer
	streamReady chan struct{}
	intentCh    chan *civquestv1.AgentIntent
}

func (s *permadeathMockBridgeServer) ConnectAgent(stream civquestv1.CivQuestBridgeService_ConnectAgentServer) error {
	s.mu.Lock()
	s.stream = stream
	select {
	case s.streamReady <- struct{}{}:
	default:
	}
	s.mu.Unlock()

	for {
		intent, err := stream.Recv()
		if err != nil {
			return err
		}
		select {
		case s.intentCh <- intent:
		default:
		}
	}
}

func (s *permadeathMockBridgeServer) SendFrame(frame *civquestv1.TelemetryFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream == nil {
		return fmt.Errorf("no stream connected")
	}
	return s.stream.Send(frame)
}

func setupPermadeathMockBridge(t *testing.T) (*permadeathMockBridgeServer, *bridge.Client, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on tcp: %v", err)
	}

	mockSvc := &permadeathMockBridgeServer{
		streamReady: make(chan struct{}, 1),
		intentCh:    make(chan *civquestv1.AgentIntent, 64),
	}

	server := grpc.NewServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(server, mockSvc)

	go func() {
		_ = server.Serve(lis)
	}()

	client, err := bridge.NewClient(lis.Addr().String(), "")
	if err != nil {
		server.Stop()
		_ = lis.Close()
		t.Fatalf("failed creating bridge client: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		server.Stop()
		_ = lis.Close()
	}

	return mockSvc, client, cleanup
}

func TestPermadeath_RuntimeIntegration_CleanExitAndSocketTeardown(t *testing.T) {
	mockSvc, client, cleanupBridge := setupPermadeathMockBridge(t)
	defer cleanupBridge()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "runtime_victim"
	agentUUID := "00000000-0000-0000-0000-000000000077"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}

	deathCallbackCalled := make(chan struct{}, 1)

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      agentID,
		AgentUUID:    agentUUID,
		ClanID:       "pioneers",
		Resolver:     resolver,
		AgentDB:      agentDB,
		BridgeClient: client,
		OnDeath: func(e *civquestv1.DeathEvent) {
			select {
			case deathCallbackCalled <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("failed creating agent runtime: %v", err)
	}

	ctx := context.Background()
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	// Await bridge stream readiness
	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge stream ready")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Verify UNIX domain diagnostic socket exists while running
	sockPath := rt.SocketPath()
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("expected active socket file at %s, got: %v", sockPath, err)
	}

	// Deliver fatal DeathEvent
	deathEv := &civquestv1.DeathEvent{
		AgentId:           agentID,
		WorldTick:         12000,
		DamageSource:      "minecraft:fall",
		KillerDisplayName: "Gravity",
		DeathLocation: &civquestv1.Vec3{
			X: 50.0,
			Y: 4.0,
			Z: 50.0,
		},
	}

	if err := mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick:  12000,
		DeathEvent: deathEv,
	}); err != nil {
		t.Fatalf("failed sending death frame: %v", err)
	}

	// 1. Verify onDeath callback was triggered
	select {
	case <-deathCallbackCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onDeath callback")
	}

	// 2. Verify runtime cleanly terminates with nil error (clean exit code 0)
	select {
	case runErr := <-runErrCh:
		if runErr != nil {
			t.Fatalf("expected nil error on permadeath exit, got: %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for rt.Start() to return on death")
	}

	// 3. Verify terminal status
	if !rt.IsDead() {
		t.Errorf("expected rt.IsDead() to be true")
	}
	if rt.Status() != "DEAD" {
		t.Errorf("expected rt.Status() to be 'DEAD', got %s", rt.Status())
	}

	// 4. Verify UNIX domain socket file was cleanly cleaned up (no remnants)
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("expected socket file %s to be deleted upon permadeath, but it still exists", sockPath)
	}

	// 5. Verify outbound intents are strictly suppressed
	dummyIntent := &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Chat{
			Chat: &civquestv1.SendChatIntent{
				Message: "I am a ghost",
			},
		},
	}
	if err := rt.QueueIntent(dummyIntent); err != agent.ErrAgentDead {
		t.Errorf("expected ErrAgentDead on QueueIntent, got: %v", err)
	}
	if err := rt.QueuePriorityIntent(dummyIntent); err != agent.ErrAgentDead {
		t.Errorf("expected ErrAgentDead on QueuePriorityIntent, got: %v", err)
	}

	// 6. Verify PermadeathResult is recorded and accessible
	res := rt.PermadeathResult()
	if res == nil {
		t.Fatalf("expected non-nil PermadeathResult on runtime")
	}
	if res.GraveyardDir == "" {
		t.Errorf("expected non-empty GraveyardDir in PermadeathResult")
	}
	if _, err := os.Stat(res.DeathCertificatePath); err != nil {
		t.Errorf("expected death certificate to exist at %s: %v", res.DeathCertificatePath, err)
	}
	if _, err := os.Stat(res.EpitaphPath); err != nil {
		t.Errorf("expected epitaph to exist at %s: %v", res.EpitaphPath, err)
	}
	if _, err := os.Stat(res.MemoryDBPath); err != nil {
		t.Errorf("expected memory.db to exist in graveyard at %s: %v", res.MemoryDBPath, err)
	}
}

func TestPermadeath_DirectoryArchiving(t *testing.T) {
	// Perform full permadeath where source dir contains files
	tmpRoot := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpRoot))
	agentID := "copy_agent"
	agentUUID := "00000000-0000-0000-0000-000000000088"

	_ = r.InitAgentDirectories(agentID)
	// Write dummy files into data dir
	dummyFile := filepath.Join(r.AgentDataDir(agentID), "test.txt")
	_ = os.WriteFile(dummyFile, []byte("persisted"), 0644)

	res, err := agent.ExecutePermadeath(context.Background(), agent.PermadeathRequest{
		AgentID:   agentID,
		AgentUUID: agentUUID,
		DeathEvent: &civquestv1.DeathEvent{
			AgentId:      agentID,
			WorldTick:    100,
			DamageSource: "void",
		},
		Resolver: r,
	})
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	archivedDummy := filepath.Join(res.GraveyardDir, "test.txt")
	if data, err := os.ReadFile(archivedDummy); err != nil || string(data) != "persisted" {
		t.Errorf("expected archived dummy file to contain 'persisted', got err=%v data=%q", err, string(data))
	}
}

func TestPermadeath_MobKill_WithUUID_NotPlayerMurder(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "scout"
	agentUUID := "00000000-0000-0000-0000-000000000066"
	clanID := "pioneers"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}

	repDB, err := db.OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed opening reputation DB: %v", err)
	}
	defer repDB.Close()

	ctx := context.Background()

	// Mob kill (Skeleton with UUID, damage source mob/arrow - NOT a player)
	mobUUID := "mob-skeleton-uuid-999"
	deathEv := &civquestv1.DeathEvent{
		AgentId:           agentID,
		WorldTick:         3000,
		DamageSource:      "mob",
		KillerEntityId:    505,
		KillerUuid:        mobUUID,
		KillerDisplayName: "Skeleton",
		DeathLocation: &civquestv1.Vec3{
			X: 10.0,
			Y: 64.0,
			Z: 10.0,
		},
	}

	res, err := agent.ExecutePermadeath(ctx, agent.PermadeathRequest{
		AgentID:      agentID,
		AgentUUID:    agentUUID,
		ClanID:       clanID,
		DeathEvent:   deathEv,
		Resolver:     resolver,
		AgentDB:      agentDB,
		ReputationDB: repDB,
	})
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	// Mob kill should NOT be treated as player murder
	if res.PrestigeDeducted == -100.0 {
		t.Errorf("mob kill was erroneously treated as player murder with -100.0 prestige deduction")
	}
	if res.PrestigeTargetPlayer == mobUUID {
		t.Errorf("mob UUID %s was erroneously awarded outlaw prestige score", mobUUID)
	}
}

func TestPermadeath_SurvivingSpouseAgentUpdated(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	ctx := context.Background()

	deceasedAgent := "aria"
	deceasedUUID := "00000000-0000-0000-0000-000000000011"
	survivingSpouse := "boran"
	spouseUUID := "00000000-0000-0000-0000-000000000022"

	// 1. Initialize directories for both agents
	_ = resolver.InitAgentDirectories(deceasedAgent)
	_ = resolver.InitAgentDirectories(survivingSpouse)

	// 2. Open deceased agent DB and seed spouse relationship
	deceasedDB, err := db.OpenAgentDBForAgent(resolver, deceasedAgent)
	if err != nil {
		t.Fatalf("failed opening deceased DB: %v", err)
	}
	if err := deceasedDB.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       spouseUUID,
		EntityName:       survivingSpouse,
		EntityType:       "AGENT",
		KinshipRole:      "SPOUSE",
		DispositionScore: 80.0,
		Familiarity:      0.9,
	}); err != nil {
		t.Fatalf("failed seeding deceased spouse relationship: %v", err)
	}

	// 3. Open surviving spouse DB and seed reciprocal spouse relationship
	survivingDB, err := db.OpenAgentDBForAgent(resolver, survivingSpouse)
	if err != nil {
		t.Fatalf("failed opening surviving spouse DB: %v", err)
	}
	if err := survivingDB.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       deceasedUUID,
		EntityName:       deceasedAgent,
		EntityType:       "AGENT",
		KinshipRole:      "SPOUSE",
		DispositionScore: 85.0,
		Familiarity:      0.9,
	}); err != nil {
		t.Fatalf("failed seeding surviving spouse relationship: %v", err)
	}
	// Close surviving spouse DB connection so ExecutePermadeath can update it without locking contention
	if err := survivingDB.Close(); err != nil {
		t.Fatalf("failed closing surviving DB: %v", err)
	}

	// 4. Execute permadeath on deceased agent
	deathEv := &civquestv1.DeathEvent{
		AgentId:      deceasedAgent,
		WorldTick:    6000,
		DamageSource: "mob",
	}

	res, err := agent.ExecutePermadeath(ctx, agent.PermadeathRequest{
		AgentID:    deceasedAgent,
		AgentUUID:  deceasedUUID,
		ClanID:     "pioneers",
		DeathEvent: deathEv,
		Resolver:   resolver,
		AgentDB:    deceasedDB,
	})
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	if !res.SpouseUpdated {
		t.Errorf("expected res.SpouseUpdated to be true")
	}

	// 5. Verify surviving spouse's database was updated to WIDOWED with disposition lowered
	spouseDBPath := resolver.AgentMemoryDBPath(survivingSpouse)
	sdb, err := sql.Open("sqlite", spouseDBPath)
	if err != nil {
		t.Fatalf("failed opening surviving spouse DB: %v", err)
	}
	defer sdb.Close()

	var kinshipRole string
	var dispScore float64
	var lastTick int64
	row := sdb.QueryRow(`
		SELECT kinship_role, disposition_score, last_interaction_tick
		FROM relationships
		WHERE entity_name = ?;
	`, deceasedAgent)
	if err := row.Scan(&kinshipRole, &dispScore, &lastTick); err != nil {
		t.Fatalf("failed scanning surviving spouse relationship: %v", err)
	}

	if kinshipRole != "WIDOWED" {
		t.Errorf("expected surviving spouse kinship_role 'WIDOWED', got %s", kinshipRole)
	}
	// Initial was 85.0, minus 10 grief penalty -> 75.0
	if dispScore != 75.0 {
		t.Errorf("expected surviving spouse disposition_score 75.0, got %.1f", dispScore)
	}
	if lastTick != 6000 {
		t.Errorf("expected surviving spouse last_interaction_tick 6000, got %d", lastTick)
	}
}

func TestPermadeath_GraveyardPermissionsAndCleanliness(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	ctx := context.Background()

	agentID := "clean_agent"
	agentUUID := "00000000-0000-0000-0000-000000000055"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}

	// Create dummy map node so map_graph.db is non-empty
	err = agentDB.AddMapNode(ctx, db.MapNode{
		ID:             "node-1",
		X:              10.0,
		Y:              64.0,
		Z:              20.0,
		Label:          "base",
		Category:       "TRANSIT",
		SafetyScore:    1.0,
		DiscoveredTick: 50,
	})
	if err != nil {
		t.Fatalf("failed recording map node: %v", err)
	}

	// Write an inventory cache in data dir and state dir
	cacheData := []byte(`{"agent_id":"clean_agent","items":[]}`)
	_ = os.WriteFile(resolver.AgentInventoryCachePath(agentID), cacheData, 0644)
	_ = os.WriteFile(filepath.Join(resolver.AgentStateDir(agentID), "inventory_cache.json"), cacheData, 0644)
	_ = os.WriteFile(resolver.AgentSocketPath(agentID), []byte("dummy"), 0644)

	res, err := agent.ExecutePermadeath(ctx, agent.PermadeathRequest{
		AgentID:    agentID,
		AgentUUID:  agentUUID,
		ClanID:     "pioneers",
		DeathEvent: &civquestv1.DeathEvent{AgentId: agentID, WorldTick: 100, DamageSource: "void"},
		Resolver:   resolver,
		AgentDB:    agentDB,
	})
	if err != nil {
		t.Fatalf("ExecutePermadeath failed: %v", err)
	}

	// Verify memory.db is 0400
	memStat, err := os.Stat(filepath.Join(res.GraveyardDir, "memory.db"))
	if err != nil {
		t.Fatalf("failed to stat memory.db: %v", err)
	}
	if memStat.Mode().Perm() != 0400 {
		t.Errorf("expected memory.db perm 0400, got %#o", memStat.Mode().Perm())
	}

	// Verify map_graph.db is 0400
	mapStat, err := os.Stat(filepath.Join(res.GraveyardDir, "map_graph.db"))
	if err != nil {
		t.Fatalf("failed to stat map_graph.db: %v", err)
	}
	if mapStat.Mode().Perm() != 0400 {
		t.Errorf("expected map_graph.db perm 0400, got %#o", mapStat.Mode().Perm())
	}

	// Verify no -wal or -shm files remain
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(res.GraveyardDir, "memory.db"+suffix)); !os.IsNotExist(err) {
			t.Errorf("expected memory.db%s to be removed", suffix)
		}
		if _, err := os.Stat(filepath.Join(res.GraveyardDir, "map_graph.db"+suffix)); !os.IsNotExist(err) {
			t.Errorf("expected map_graph.db%s to be removed", suffix)
		}
	}

	// Verify inventory_cache.json is removed from graveyard and state
	if _, err := os.Stat(filepath.Join(res.GraveyardDir, "inventory_cache.json")); !os.IsNotExist(err) {
		t.Errorf("expected inventory_cache.json to be deleted from graveyard")
	}
	if _, err := os.Stat(resolver.AgentSocketPath(agentID)); !os.IsNotExist(err) {
		t.Errorf("expected socket file to be cleaned up")
	}
}
