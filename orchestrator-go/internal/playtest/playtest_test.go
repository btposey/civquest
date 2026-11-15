package playtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func TestTracerAndDiagnosticLogs(t *testing.T) {
	tempDir := t.TempDir()
	secDir := filepath.Join(tempDir, "secondary")

	tracer, err := NewTracer(tempDir, secDir)
	if err != nil {
		t.Fatalf("Failed creating tracer: %v", err)
	}

	// Emit events in each layer
	pos := &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 20.5}
	tracer.LogPuppetCoordinates("Able", 100, pos, 45.0, 10.0)
	tracer.LogPuppetNavigationState("Able", 105, civquestv1.NavigationState_NAVIGATION_STATE_PATHING, pos, 5.0)

	agentAABB := AABB{MinX: 10.2, MinY: 64.0, MinZ: 20.2, MaxX: 10.8, MaxY: 65.8, MaxZ: 20.8}
	blockAABB := AABB{MinX: 10.0, MinY: 64.0, MinZ: 20.0, MaxX: 11.0, MaxY: 65.0, MaxZ: 21.0}
	blockPos := &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 20}
	tracer.LogPuppetBlockCollision("Able", 110, blockPos, "minecraft:oak_planks", false, "AABB overlap", agentAABB, blockAABB)

	tracer.LogPuppetCraftingProximity("aria", 120, "wooden_pickaxe", blockPos, 2.25, true, true)
	tracer.LogPuppetItemPickup("aria", 130, "minecraft:iron_ingot", 2, "thrower-1", "player-1", false, true, "Thank you!")

	tracer.LogSystem1FSMTransition("Able", 140, system1.StateBootstrap, system1.StateExplore, "morning scout", false, 1)
	tracer.LogSystem1UtilityScores("Able", 145, map[string]float64{"explore": 0.85, "rest": 0.10}, "EXPLORE")
	tracer.LogSystem1RadialScouting("Able", 150, pos, 0.0, 0.05, 0.05, 48.0, 1, false)
	tracer.LogSystem1RoadPaving("boreas", 160, blockPos, 16, 15, 8.5, false, "PAVE_DIRT_PATH")

	tracer.LogSystem2ContemplationTrigger("aria", 13000, 13000, "BED_ENTERED", true)
	tracer.LogSystem2LedgerUpdate("aria", 13050, 1, []string{"g1"}, []string{"g2"}, "Summary of day 1")
	tracer.LogSystem2VectorRAG("aria", 13100, "EPISODIC", "Memory text", 1536)

	if err := tracer.Close(); err != nil {
		t.Fatalf("Failed closing tracer: %v", err)
	}

	// Verify all 4 files exist in tempDir and secDir
	expectedFiles := []string{
		"puppeteering.jsonl",
		"cognitive_system1.jsonl",
		"cognitive_system2.jsonl",
		"playtest_trace.jsonl",
	}

	for _, ef := range expectedFiles {
		p1 := filepath.Join(tempDir, ef)
		fi, err := os.Stat(p1)
		if err != nil || fi.Size() == 0 {
			t.Errorf("Expected non-empty trace file %s, got err=%v", p1, err)
		}

		p2 := filepath.Join(secDir, ef)
		fi2, err := os.Stat(p2)
		if err != nil || fi2.Size() == 0 {
			t.Errorf("Expected replicated trace file %s, got err=%v", p2, err)
		}
	}
}

func TestInvariantSuiteEvaluation(t *testing.T) {
	suite := NewInvariantSuite()

	// Invariant 1: Scout
	origin := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	target48 := &civquestv1.Vec3{X: 48, Y: 64, Z: 0}
	suite.RecordScoutNavState(100, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	suite.RecordScoutAzimuth(100, origin, target48)
	suite.RecordScoutNavState(200, civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)

	// Invariant 2: Road Paving
	campfire := &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}
	suite.SetCampfirePos(campfire)
	suite.RecordRoadPavingPlacement(300, &civquestv1.BlockPosProto{X: 8, Y: 63, Z: 8}, 16)

	// Invariant 3: Shelter Collision Safety
	suite.RecordCollisionCheck(400, false)
	suite.RecordShelterBlockPlacement(410, &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10})
	suite.RecordShelterBlockPlacement(420, &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 10})

	// Invariant 4: Workstation Crafting
	suite.RecordCraftingAttempt(500, "wooden_pickaxe", 36.0, true, false) // distant rejected
	suite.RecordCraftingAttempt(510, "wooden_pickaxe", 4.0, true, true)   // nearby accepted
	suite.RecordCraftingAttempt(520, "oak_planks", 0.0, false, true)      // 2x2 accepted

	// Invariant 5: Drop Gratitude
	suite.RecordItemPickupGratitude(600, "minecraft:wheat_seeds", false, false, false) // natural silent
	suite.RecordItemPickupGratitude(610, "minecraft:iron_ingot", true, true, true)     // player gift thanked

	results := suite.Evaluate()
	if len(results) != 5 {
		t.Fatalf("Expected 5 invariant results, got %d", len(results))
	}

	for _, r := range results {
		if r.Status != StatusPassed {
			t.Errorf("Expected invariant %s to pass, got %s: %v", r.Name, r.Status, r.Failures)
		}
	}

	if !suite.AllPassed() {
		t.Fatalf("Expected suite.AllPassed() to be true")
	}
}

func TestPlaytestRunnerExecution(t *testing.T) {
	tempDir := t.TempDir()
	reportPath := filepath.Join(tempDir, "playtest-report.md")

	runner, err := NewPlaytestRunner(RunnerConfig{
		OutputDir:        tempDir,
		ReportOutputPath: reportPath,
		TotalWorldTicks:  24000,
	})
	if err != nil {
		t.Fatalf("Failed creating playtest runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Playtest runner failed: %v", err)
	}

	if !summary.AllPassed {
		t.Errorf("Expected all playtest invariants to pass")
		for _, r := range summary.Results {
			if r.Status != StatusPassed {
				t.Errorf("  Failed: %s -> %v", r.Name, r.Failures)
			}
		}
	}

	// Verify report file was written and contains key markdown elements
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("Failed reading generated report: %v", err)
	}
	reportStr := string(reportBytes)

	requiredStrings := []string{
		"# CivQuest In-World Playtest Diagnostic Report",
		"Assertion 1: Scout Exploration",
		"Assertion 2: Road Paving",
		"Assertion 3: Shelter Collision Safety",
		"Assertion 4: Workstation Crafting",
		"Assertion 5: Drop Gratitude",
		"puppeteering.jsonl",
		"cognitive_system1.jsonl",
		"cognitive_system2.jsonl",
		"playtest_trace.jsonl",
	}

	for _, req := range requiredStrings {
		if !strings.Contains(reportStr, req) {
			t.Errorf("Report missing expected string: %q", req)
		}
	}
}

func TestPlaytestRunner_SyntheticWorldSeeding(t *testing.T) {
	tempDir := t.TempDir()
	reportPath := filepath.Join(tempDir, "playtest-report.md")

	runner, err := NewPlaytestRunner(RunnerConfig{
		OutputDir:          tempDir,
		ReportOutputPath:   reportPath,
		TotalWorldTicks:    24000,
		SeedSyntheticWorld: true,
	})
	if err != nil {
		t.Fatalf("Failed creating playtest runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Playtest runner failed with synthetic world seeding: %v", err)
	}

	if !summary.AllPassed {
		t.Errorf("Expected all playtest invariants to pass with synthetic world seeding")
	}

	// Verify puppet trace recorded the synthetic world seed event
	puppetTracePath := filepath.Join(tempDir, "puppeteering.jsonl")
	data, err := os.ReadFile(puppetTracePath)
	if err != nil {
		t.Fatalf("Failed reading puppet trace: %v", err)
	}
	if !strings.Contains(string(data), "synthetic_world_seed") {
		t.Errorf("expected puppet trace to contain synthetic_world_seed event")
	}
}
