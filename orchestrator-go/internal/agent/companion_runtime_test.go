package agent_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestRuntime_CompanionBondCommandProgression(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	agentID := "lyra"
	agentUUID := "00000000-0000-0000-0000-000000000088"
	clanID := "pioneers"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed to open agent db: %v", err)
	}
	defer agentDB.Close()

	repDB, err := db.OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed to open reputation db: %v", err)
	}
	defer repDB.Close()

	agentCfg := &xdg.AgentIdentityConfig{
		Name:       agentID,
		ClanID:     clanID,
		Profession: "GUARD",
		Traits: xdg.AgentTraits{
			Bravery: 0.8,
		},
	}

	runtime, err := agent.NewRuntime(agent.Config{
		AgentID:         agentID,
		AgentUUID:       agentUUID,
		ClanID:          clanID,
		IdentityConfig:  agentCfg,
		Resolver:        resolver,
		AgentDB:         agentDB,
		ReputationDB:    repDB,
		BridgeClient:    bridgeClient,
		SocketPath:      resolver.AgentSocketPath(agentID),
		IntentQueueSize: 32,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer runtime.Stop()

	// Start runtime loop
	go func() {
		_ = runtime.Start(context.Background())
	}()

	// Wait briefly for status RUNNING
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.Status() == "RUNNING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Wait for handshake intent
	select {
	case handshake := <-mockSvc.intentCh:
		if handshake.SequenceId != 1 {
			t.Errorf("expected handshake sequence_id 1, got %d", handshake.SequenceId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial handshake intent")
	}

	playerUUID := "player-steve-1"
	playerName := "Steve"

	// 1. Stranger sends "follow me" -> Rejected
	frameStranger := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		IncomingChats: []*civquestv1.ChatEvent{
			{
				SenderUuid: playerUUID,
				SenderName: playerName,
				Message:    "follow me",
				Distance:   3.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameStranger)

	// Await response intent
	select {
	case intent := <-mockSvc.intentCh:
		chat := intent.GetChat()
		if chat == nil {
			t.Fatalf("expected chat intent, got %+v", intent)
		}
		if !strings.Contains(chat.Message, "not know you well enough") {
			t.Errorf("expected stranger rejection message, got %q", chat.Message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for stranger command reply")
	}

	compSvc := runtime.CompanionService()
	if compSvc == nil {
		t.Fatal("expected non-nil CompanionService in runtime")
	}
	if compSvc.GetState() != system2.CompanionStateNone {
		t.Errorf("expected state NONE for stranger, got %s", compSvc.GetState())
	}
	if runtime.IsCompanionActive() {
		t.Errorf("expected companionActive=false in system1 state")
	}

	// 2. Upgrade relationship to QUESTOR in AgentDB
	questorRel := db.Relationship{
		EntityUUID:          playerUUID,
		EntityName:          playerName,
		EntityType:          "PLAYER",
		KinshipRole:         "NONE",
		DispositionScore:    25.0,
		Familiarity:         0.25,
		LastInteractionTick: 200,
	}
	if err := agentDB.UpsertRelationship(context.Background(), questorRel); err != nil {
		t.Fatalf("failed upgrading relationship: %v", err)
	}

	// 3. Questor sends "follow me" -> Accepted!
	frameFollow := &civquestv1.TelemetryFrame{
		WorldTick: 250,
		TimeOfDay: 2500,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		IncomingChats: []*civquestv1.ChatEvent{
			{
				SenderUuid: playerUUID,
				SenderName: playerName,
				Message:    "follow me",
				Distance:   3.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameFollow)

	select {
	case intent := <-mockSvc.intentCh:
		chat := intent.GetChat()
		if chat == nil {
			t.Fatalf("expected chat intent, got %+v", intent)
		}
		if chat.Message != "I am with you!" {
			t.Errorf("expected 'I am with you!', got %q", chat.Message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for follow command reply")
	}

	if compSvc.GetState() != system2.CompanionStateFollow {
		t.Errorf("expected state FOLLOW, got %s", compSvc.GetState())
	}
	if !runtime.IsCompanionActive() {
		t.Errorf("expected companionActive=true in system1 state")
	}

	// 4. Questor sends "wait here" -> Accepted!
	frameWait := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		IncomingChats: []*civquestv1.ChatEvent{
			{
				SenderUuid: playerUUID,
				SenderName: playerName,
				Message:    "wait here",
				Distance:   3.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameWait)

	select {
	case intent := <-mockSvc.intentCh:
		chat := intent.GetChat()
		if chat == nil {
			t.Fatalf("expected chat intent, got %+v", intent)
		}
		if chat.Message != "I will wait here." {
			t.Errorf("expected 'I will wait here.', got %q", chat.Message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for wait command reply")
	}

	if compSvc.GetState() != system2.CompanionStateWait {
		t.Errorf("expected state WAIT, got %s", compSvc.GetState())
	}
	if !runtime.IsCompanionActive() {
		t.Errorf("expected companionActive=true in system1 state")
	}

	// 5. Questor sends "return home" -> Accepted!
	frameReturnHome := &civquestv1.TelemetryFrame{
		WorldTick: 400,
		TimeOfDay: 4000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		IncomingChats: []*civquestv1.ChatEvent{
			{
				SenderUuid: playerUUID,
				SenderName: playerName,
				Message:    "return home",
				Distance:   3.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameReturnHome)

	select {
	case intent := <-mockSvc.intentCh:
		chat := intent.GetChat()
		if chat == nil {
			t.Fatalf("expected chat intent, got %+v", intent)
		}
		if chat.Message != "Returning to our village." {
			t.Errorf("expected 'Returning to our village.', got %q", chat.Message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for return home reply")
	}

	if compSvc.GetState() != system2.CompanionStateReturnHome {
		t.Errorf("expected state RETURN_HOME, got %s", compSvc.GetState())
	}
	if runtime.IsCompanionActive() {
		t.Errorf("expected companionActive=false in system1 state after return home")
	}
}

func TestRuntime_CompanionFollowNavigationAndCampingOverride(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	agentID := "lyra"
	agentUUID := "00000000-0000-0000-0000-000000000088"
	clanID := "pioneers"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed to open agent db: %v", err)
	}
	defer agentDB.Close()

	playerUUID := "player-steve-1"
	playerName := "Steve"

	// Seed companion relationship
	companionRel := db.Relationship{
		EntityUUID:          playerUUID,
		EntityName:          playerName,
		EntityType:          "PLAYER",
		KinshipRole:         "NONE",
		DispositionScore:    55.0,
		Familiarity:         0.60,
		LastInteractionTick: 500,
	}
	if err := agentDB.UpsertRelationship(context.Background(), companionRel); err != nil {
		t.Fatalf("failed seeding companion relationship: %v", err)
	}

	runtime, err := agent.NewRuntime(agent.Config{
		AgentID:         agentID,
		AgentUUID:       agentUUID,
		ClanID:          clanID,
		Resolver:        resolver,
		AgentDB:         agentDB,
		BridgeClient:    bridgeClient,
		SocketPath:      resolver.AgentSocketPath(agentID),
		IntentQueueSize: 32,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer runtime.Stop()

	// Start runtime so intent queue flows to bridge client
	go func() {
		_ = runtime.Start(context.Background())
	}()

	// Wait briefly for status RUNNING
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.Status() == "RUNNING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for handshake intent
	select {
	case handshake := <-mockSvc.intentCh:
		if handshake.SequenceId != 1 {
			t.Errorf("expected handshake sequence_id 1, got %d", handshake.SequenceId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial handshake intent")
	}

	// Assign village bed
	bedPos := &civquestv1.BlockPosProto{X: 100, Y: 64, Z: 100}
	runtime.SetAssignedBed(bedPos)

	// Put companion in FOLLOW state
	compSvc := runtime.CompanionService()
	compSvc.SetState(system2.CompanionStateFollow)
	compSvc.SetBondedPlayer(playerUUID, playerName)

	// 1. Player is at distance 12 (> 4 blocks and <= 48 blocks)
	// Agent at (10, 64, 10), Player at (22, 64, 10)
	frameFollow := &civquestv1.TelemetryFrame{
		WorldTick: 600,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    101,
				Uuid:        playerUUID,
				EntityType:  "minecraft:player",
				DisplayName: playerName,
				Position:    &civquestv1.Vec3{X: 22, Y: 64, Z: 10},
				Distance:    12.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameFollow)

	// Verify NavigateIntent to player
	select {
	case intent := <-mockSvc.intentCh:
		nav := intent.GetNavigate()
		if nav == nil || nav.Target == nil {
			t.Fatalf("expected NavigateIntent to player, got %+v", intent)
		}
		if nav.Target.X != 22 || nav.Target.Z != 10 {
			t.Errorf("expected nav target (22, 64, 10), got %+v", nav.Target)
		}
		if !nav.Sprint {
			t.Errorf("expected sprinting for distance 12")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected NavigateIntent to be queued for follow")
	}

	// 2. Evening Tavern (tick 12000) and Night Sleep (tick 16000) override
	// With companion active, tavern return and bed navigation are overridden to RoutineCamp
	frameNightCamp := &civquestv1.TelemetryFrame{
		WorldTick: 700,
		TimeOfDay: 16000, // Night sleep hours
		Status: &civquestv1.AgentStatus{
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    101,
				Uuid:        playerUUID,
				EntityType:  "minecraft:player",
				DisplayName: playerName,
				Position:    &civquestv1.Vec3{X: 12, Y: 64, Z: 10}, // Dist = 2 (close, camping)
				Distance:    2.0,
			},
		},
	}
	runtime.HandleTelemetryFrame(frameNightCamp)

	if runtime.CurrentRoutine() != system1.RoutineCamp {
		t.Errorf("expected RoutineCamp during night, got %s", runtime.CurrentRoutine())
	}

	// Ensure NO bed navigate intent was emitted (village bed is at 100, 64, 100)
	select {
	case intent := <-mockSvc.intentCh:
		if nav := intent.GetNavigate(); nav != nil && nav.Target != nil {
			if nav.Target.X == 100.5 && nav.Target.Z == 100.5 {
				t.Fatalf("agent attempted to navigate to village bed during companion camp!")
			}
		}
	default:
		// Queue is empty -> Correct, companion camps peacefully near player
	}
}

func TestRuntime_CompanionCombatAidAndPermadeathMemorial(t *testing.T) {
	_, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	agentID := "lyra"
	agentUUID := "00000000-0000-0000-0000-000000000088"
	clanID := "pioneers"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed to open agent db: %v", err)
	}
	defer agentDB.Close()

	repDB, err := db.OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed to open reputation db: %v", err)
	}
	defer repDB.Close()

	playerUUID := "player-steve-1"
	playerName := "Steve"

	// Seed initial companion relationship
	companionRel := db.Relationship{
		EntityUUID:          playerUUID,
		EntityName:          playerName,
		EntityType:          "PLAYER",
		KinshipRole:         "NONE",
		DispositionScore:    45.0,
		Familiarity:         0.45,
		LastInteractionTick: 1000,
	}
	if err := agentDB.UpsertRelationship(context.Background(), companionRel); err != nil {
		t.Fatalf("failed seeding relationship: %v", err)
	}

	runtime, err := agent.NewRuntime(agent.Config{
		AgentID:         agentID,
		AgentUUID:       agentUUID,
		ClanID:          clanID,
		Resolver:        resolver,
		AgentDB:         agentDB,
		ReputationDB:    repDB,
		BridgeClient:    bridgeClient,
		SocketPath:      resolver.AgentSocketPath(agentID),
		IntentQueueSize: 32,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer runtime.Stop()

	// 1. Simulate taking damage near player -> Triggers Combat Aid progression
	frameCombat := &civquestv1.TelemetryFrame{
		WorldTick: 1100,
		TimeOfDay: 4000,
		Status: &civquestv1.AgentStatus{
			Health:    14,
			MaxHealth: 20,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    101,
				Uuid:        playerUUID,
				EntityType:  "minecraft:player",
				DisplayName: playerName,
				Position:    &civquestv1.Vec3{X: 12, Y: 64, Z: 10},
				Distance:    2.0,
			},
		},
		LastDamageTaken: &civquestv1.DamageEvent{
			DamageAmount:     6.0,
			SourceType:       civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB,
			AttackerEntityId: 200,
		},
	}
	runtime.HandleTelemetryFrame(frameCombat)

	// Allow async persistence worker to flush to SQLite
	time.Sleep(50 * time.Millisecond)

	// Check that combat aid was recorded
	updatedRel, err := agentDB.GetRelationship(context.Background(), playerUUID)
	if err != nil || updatedRel == nil {
		t.Fatalf("failed getting updated relationship: %v", err)
	}
	// Initial was (45.0, 0.45), combat aid awards +5.0 disposition and +0.08 familiarity
	// Now (50.0, 0.53) -> COMPANION bond stage reached!
	if updatedRel.DispositionScore < 50.0 {
		t.Errorf("expected disposition >= 50.0, got %.1f", updatedRel.DispositionScore)
	}
	if updatedRel.Familiarity < 0.50 {
		t.Errorf("expected familiarity >= 0.50, got %.2f", updatedRel.Familiarity)
	}
	stage := system2.ComputeBondStage(updatedRel.DispositionScore, updatedRel.Familiarity)
	if stage != system2.BondCompanion {
		t.Errorf("expected BondCompanion, got %s", stage)
	}

	// 2. Simulate Companion Perishing in Battle -> Permadeath Memorial Chronicle
	deathEv := &civquestv1.DeathEvent{
		AgentId:           agentID,
		WorldTick:         1200,
		DamageSource:      "minecraft:explosion",
		KillerEntityId:    303,
		KillerDisplayName: "Wither Skeleton",
		DeathLocation: &civquestv1.Vec3{
			X: 10.0,
			Y: 64.0,
			Z: 10.0,
		},
	}
	frameDeath := &civquestv1.TelemetryFrame{
		WorldTick:  1200,
		DeathEvent: deathEv,
	}

	runtime.HandleTelemetryFrame(frameDeath)

	if !runtime.IsDead() {
		t.Fatal("expected runtime to mark agent dead upon DeathEvent")
	}

	res := runtime.PermadeathResult()
	if res == nil {
		t.Fatal("expected non-nil PermadeathResult")
	}

	if !res.CompanionMemorialWritten {
		t.Errorf("expected CompanionMemorialWritten = true")
	}
	if res.CompanionMemorialPath == "" {
		t.Errorf("expected non-empty CompanionMemorialPath")
	} else {
		content, err := os.ReadFile(res.CompanionMemorialPath)
		if err != nil {
			t.Fatalf("failed reading memorial chronicle file %s: %v", res.CompanionMemorialPath, err)
		}
		str := string(content)
		if !strings.Contains(str, "Memorial of lyra") || !strings.Contains(str, playerName) {
			t.Errorf("unexpected memorial chronicle content:\n%s", str)
		}
	}
}
