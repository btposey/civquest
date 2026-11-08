package system2

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestCompanionCommandParsing(t *testing.T) {
	tests := []struct {
		input       string
		expectedCmd CompanionCommandType
		expectedOk  bool
	}{
		// Follow commands
		{"follow me", CommandFollow, true},
		{"Follow me!", CommandFollow, true},
		{"Come with me, Steve", CommandFollow, true},
		{"with me", CommandFollow, true},
		{"let's go", CommandFollow, true},
		{"LETS GO!", CommandFollow, true},

		// Wait commands
		{"wait here", CommandWait, true},
		{"Wait here please", CommandWait, true},
		{"stay here", CommandWait, true},
		{"hold position", CommandWait, true},
		{"wait", CommandWait, true},
		{"stay", CommandWait, true},

		// Return home / dismiss
		{"return home", CommandReturnHome, true},
		{"go home now", CommandReturnHome, true},
		{"dismiss", CommandReturnHome, true},
		{"head home", CommandReturnHome, true},
		{"leave me", CommandReturnHome, true},

		// Non-commands (must not be hijacked by companion commands)
		{"hello friend", CommandNone, false},
		{"how are you today?", CommandNone, false},
		{"trade with me", CommandNone, false},
		{"I can't wait to see the build", CommandNone, false},
		{"don't leave me here", CommandNone, false},
		{"", CommandNone, false},
	}

	for _, tc := range tests {
		cmd, ok := ParseCompanionCommand(tc.input)
		if ok != tc.expectedOk {
			t.Errorf("input %q: expected ok=%v, got %v", tc.input, tc.expectedOk, ok)
		}
		if cmd != tc.expectedCmd {
			t.Errorf("input %q: expected cmd=%v, got %v", tc.input, tc.expectedCmd, cmd)
		}
	}
}

func TestComputeBondStage(t *testing.T) {
	tests := []struct {
		disp float64
		fam  float64
		want BondStage
	}{
		{0.0, 0.0, BondStranger},
		{19.9, 0.5, BondStranger},
		{50.0, 0.19, BondStranger},
		{20.0, 0.20, BondQuestor},
		{49.9, 0.49, BondQuestor},
		{60.0, 0.30, BondQuestor},
		{50.0, 0.50, BondCompanion},
		{80.0, 0.90, BondCompanion},
	}

	for _, tc := range tests {
		got := ComputeBondStage(tc.disp, tc.fam)
		if got != tc.want {
			t.Errorf("disp=%.1f, fam=%.2f: expected %s, got %s", tc.disp, tc.fam, tc.want, got)
		}
	}
}

func TestCompanionService_ProgressionAndCommands(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "civquest_companion_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentDB, err := db.OpenAgentDB(tmpDir)
	if err != nil {
		t.Fatalf("failed to open agent DB: %v", err)
	}
	defer agentDB.Close()

	agentCfg := &xdg.AgentIdentityConfig{
		Name:   "Aria",
		ClanID: "pioneers",
	}

	svc := NewCompanionService(agentDB, nil, agentCfg)
	ctx := context.Background()

	playerUUID := "player-steve-123"
	playerName := "Steve"

	// 1. Initially stranger
	stage, err := svc.GetBondStage(ctx, playerUUID)
	if err != nil {
		t.Fatalf("GetBondStage failed: %v", err)
	}
	if stage != BondStranger {
		t.Fatalf("expected initial stage BondStranger, got %s", stage)
	}

	// 2. Command as stranger -> rejected
	res, recognized := svc.ProcessCommand(ctx, playerUUID, playerName, "follow me", 100)
	if !recognized {
		t.Fatal("expected command to be recognized")
	}
	if res.Obeyed {
		t.Fatal("expected stranger command to be rejected")
	}
	if svc.GetState() != CompanionStateNone {
		t.Fatalf("expected state NONE, got %s", svc.GetState())
	}
	if res.ReplyText != "I do not know you well enough to follow your lead." {
		t.Errorf("unexpected reply: %q", res.ReplyText)
	}

	// 3. Dialogue progression: 4 dialogue turns with +5.0 prestige delta each
	// Each turn adds +0.05 familiarity and +5.0 disposition
	for i := 0; i < 4; i++ {
		_, err := svc.RecordDialogueInteraction(ctx, playerUUID, playerName, 5.0, int64(200+i*20))
		if err != nil {
			t.Fatalf("RecordDialogueInteraction failed: %v", err)
		}
	}

	// Now disposition = 20.0, familiarity = 0.20 -> QUESTOR!
	rel, err := svc.GetRelationship(ctx, playerUUID)
	if err != nil || rel == nil {
		t.Fatalf("failed getting relationship: %v", err)
	}
	if rel.DispositionScore != 20.0 {
		t.Errorf("expected disposition 20.0, got %.1f", rel.DispositionScore)
	}
	if math.Abs(rel.Familiarity-0.20) > 1e-6 {
		t.Errorf("expected familiarity 0.20, got %.2f", rel.Familiarity)
	}
	stage, _ = svc.GetBondStage(ctx, playerUUID)
	if stage != BondQuestor {
		t.Fatalf("expected stage BondQuestor, got %s", stage)
	}

	// 4. Command as Questor -> Follow accepted!
	res, recognized = svc.ProcessCommand(ctx, playerUUID, playerName, "follow me", 300)
	if !recognized || !res.Obeyed {
		t.Fatalf("expected Questor follow command to be obeyed, got %+v", res)
	}
	if svc.GetState() != CompanionStateFollow {
		t.Fatalf("expected state FOLLOW, got %s", svc.GetState())
	}
	if !svc.IsActive() {
		t.Fatal("expected IsActive to be true")
	}
	if res.ReplyText != "I am with you!" {
		t.Errorf("unexpected reply: %q", res.ReplyText)
	}

	// 5. Test Wait command
	res, recognized = svc.ProcessCommand(ctx, playerUUID, playerName, "wait here", 350)
	if !recognized || !res.Obeyed {
		t.Fatalf("expected wait command to be obeyed, got %+v", res)
	}
	if svc.GetState() != CompanionStateWait {
		t.Fatalf("expected state WAIT, got %s", svc.GetState())
	}
	if res.ReplyText != "I will wait here." {
		t.Errorf("unexpected reply: %q", res.ReplyText)
	}

	// 6. Test Shared Travel progression
	// Walk 100 blocks together (< 16 blocks distance)
	// Initial agent pos (0, 64, 0), player pos (2, 64, 0)
	agentPos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	playerPos := &civquestv1.Vec3{X: 2, Y: 64, Z: 0}
	// Initial step to anchor lastPlayerPos
	_, _, _ = svc.RecordSharedTravel(ctx, playerUUID, playerName, playerPos, agentPos, 400)

	// Simulate walking 10 steps of 10 blocks each = 100 blocks
	var upgraded bool
	for step := 1; step <= 10; step++ {
		playerPos = &civquestv1.Vec3{X: float64(step*10 + 2), Y: 64, Z: 0}
		agentPos = &civquestv1.Vec3{X: float64(step * 10), Y: 64, Z: 0}
		_, u, err := svc.RecordSharedTravel(ctx, playerUUID, playerName, playerPos, agentPos, int64(400+step*10))
		if err != nil {
			t.Fatalf("RecordSharedTravel step %d failed: %v", step, err)
		}
		if u {
			upgraded = true
		}
	}
	_ = upgraded

	rel, _ = svc.GetRelationship(ctx, playerUUID)
	// Disposition was 20.0 + 2.0 = 22.0, Familiarity was 0.20 + 0.05 = 0.25
	if rel.DispositionScore != 22.0 {
		t.Errorf("expected disposition 22.0 after travel, got %.1f", rel.DispositionScore)
	}
	if math.Abs(rel.Familiarity-0.25) > 1e-6 {
		t.Errorf("expected familiarity 0.25 after travel, got %.2f", rel.Familiarity)
	}

	// 7. Combat Aid progression
	// Calling RecordCombatAid adds +5.0 disposition and +0.08 familiarity
	for i := 0; i < 6; i++ {
		_, err := svc.RecordCombatAid(ctx, playerUUID, playerName, int64(600+i*30))
		if err != nil {
			t.Fatalf("RecordCombatAid %d failed: %v", i, err)
		}
	}
	// 6 combat aids = +30.0 disposition (22.0 + 30.0 = 52.0), +0.48 familiarity (0.25 + 0.48 = 0.73)
	rel, _ = svc.GetRelationship(ctx, playerUUID)
	if rel.DispositionScore < 50.0 {
		t.Errorf("expected disposition >= 50.0, got %.1f", rel.DispositionScore)
	}
	if rel.Familiarity < 0.50 {
		t.Errorf("expected familiarity >= 0.50, got %.2f", rel.Familiarity)
	}

	stage, _ = svc.GetBondStage(ctx, playerUUID)
	if stage != BondCompanion {
		t.Fatalf("expected stage BondCompanion, got %s", stage)
	}

	// 8. Test Return Home command
	res, recognized = svc.ProcessCommand(ctx, playerUUID, playerName, "return home", 800)
	if !recognized || !res.Obeyed {
		t.Fatalf("expected return home command to be obeyed, got %+v", res)
	}
	if svc.GetState() != CompanionStateReturnHome {
		t.Fatalf("expected state RETURN_HOME, got %s", svc.GetState())
	}
	if res.ReplyText != "Returning to our village." {
		t.Errorf("unexpected reply: %q", res.ReplyText)
	}
}

func TestCompanionService_FollowNavigation(t *testing.T) {
	svc := NewCompanionService(nil, nil, nil)
	svc.SetState(CompanionStateFollow)

	agentPos := &civquestv1.Vec3{X: 10, Y: 64, Z: 10}

	// Case 1: Distance <= 4 blocks -> no navigation needed
	playerPosNear := &civquestv1.Vec3{X: 12, Y: 64, Z: 10} // dist = 2
	shouldNav, nav := svc.EvaluateFollowNavigation(agentPos, playerPosNear)
	if shouldNav || nav != nil {
		t.Errorf("expected no navigation when near player, got shouldNav=%v", shouldNav)
	}

	// Case 2: Distance between 4 and 10 blocks -> walk navigation
	playerPosMid := &civquestv1.Vec3{X: 18, Y: 64, Z: 10} // dist = 8
	shouldNav, nav = svc.EvaluateFollowNavigation(agentPos, playerPosMid)
	if !shouldNav || nav == nil {
		t.Fatal("expected navigation when 8 blocks away")
	}
	if nav.Sprint {
		t.Errorf("expected sprint=false for distance 8")
	}
	if nav.SpeedMultiplier != 1.0 {
		t.Errorf("expected speed=1.0, got %v", nav.SpeedMultiplier)
	}

	// Case 3: Distance > 10 blocks (e.g. 20 blocks) -> sprint navigation
	playerPosFar := &civquestv1.Vec3{X: 30, Y: 64, Z: 10} // dist = 20
	shouldNav, nav = svc.EvaluateFollowNavigation(agentPos, playerPosFar)
	if !shouldNav || nav == nil {
		t.Fatal("expected navigation when 20 blocks away")
	}
	if !nav.Sprint {
		t.Errorf("expected sprint=true for distance 20")
	}
	if nav.SpeedMultiplier != 1.25 {
		t.Errorf("expected speed=1.25, got %v", nav.SpeedMultiplier)
	}

	// Case 4: Distance > 48 blocks -> lost track, no navigation
	playerPosTooFar := &civquestv1.Vec3{X: 100, Y: 64, Z: 10} // dist = 90
	shouldNav, nav = svc.EvaluateFollowNavigation(agentPos, playerPosTooFar)
	if shouldNav || nav != nil {
		t.Errorf("expected no navigation when > 48 blocks away")
	}
}
