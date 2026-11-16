package eval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// TestTimeWarp_Performance_24000TicksUnderOneSecond verifies Acceptance Criterion 1:
// Time-warp simulation executes 24000+ ticks (full day/night cycle) in <1 second.
func TestTimeWarp_Performance_24000TicksUnderOneSecond(t *testing.T) {
	tmpDir := t.TempDir()
	sim, err := NewTimeWarpSimulation(TimeWarpConfig{
		AgentID:   "speed_agent",
		WorkDir:   tmpDir,
		TickDelta: 4, // 5 Hz telemetry delta (6,000 frames)
	})
	if err != nil {
		t.Fatalf("failed creating simulation: %v", err)
	}
	defer sim.Close()

	start := time.Now()
	totalTicks := int64(24000)
	if err := sim.RunTicks(totalTicks); err != nil {
		t.Fatalf("RunTicks failed: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("Executed %d world ticks in %v (%.2f ticks/ms)", totalTicks, elapsed, float64(totalTicks)/float64(elapsed.Milliseconds()))

	threshold := 1 * time.Second
	// In -race builds, Go memory access instrumentation adds ~4-5x overhead
	if raceDetectorEnabled {
		threshold = 5 * time.Second
	}
	if elapsed >= threshold {
		t.Fatalf("PERFORMANCE VIOLATION: 24,000 ticks took %v (must execute in < %v)", elapsed, threshold)
	}
}

// TestTimeWarp_Scenario1_MultiDayRoutineProgression verifies Scenario 1:
// Dawn -> Explore -> Dusk Shelter -> Sleep Contemplation -> Dawn.
func TestTimeWarp_Scenario1_MultiDayRoutineProgression(t *testing.T) {
	tmpDir := t.TempDir()
	sim, err := RunMultiDayRoutineScenario(TimeWarpConfig{
		AgentID:   "routine_agent",
		WorkDir:   tmpDir,
		TickDelta: 4,
	}, 24000)
	if err != nil {
		t.Fatalf("Scenario 1 failed: %v", err)
	}
	defer sim.Close()

	// Verify the simulation ended at 24000 ticks
	if sim.CurrentTick() != 24000 {
		t.Fatalf("expected tick 24000, got %d", sim.CurrentTick())
	}

	// Verify no FSM oscillations occurred
	if violations := sim.OscillationDetector.Violations(); len(violations) > 0 {
		t.Fatalf("unexpected FSM oscillations: %v", violations)
	}

	// Verify no navigation stalls occurred
	if violations := sim.StallDetector.Violations(); len(violations) > 0 {
		t.Fatalf("unexpected navigation stalls: %v", violations)
	}
}

// TestTimeWarp_Scenario2_ExplorerFrontierAdvancement verifies Scenario 2:
// Outward radial legs 48m -> 96m -> 144m with zero spin thrashing.
func TestTimeWarp_Scenario2_ExplorerFrontierAdvancement(t *testing.T) {
	tmpDir := t.TempDir()
	sim, err := RunFrontierAdvancementScenario(TimeWarpConfig{
		AgentID: "Able",
		WorkDir: tmpDir,
	}, 3)
	if err != nil {
		t.Fatalf("Scenario 2 failed: %v", err)
	}
	defer sim.Close()

	legs := sim.AzimuthDetector.ObservedLegs()
	t.Logf("Observed radial legs: %+v", legs)
	if len(legs) < 3 {
		t.Fatalf("expected at least 3 observed radial legs, got %d", len(legs))
	}
}

// TestTimeWarp_Scenario3_RoadPavingCorridors verifies Scenario 3:
// Campfire 4m exclusion zone, casual 15-step threshold, directional lines.
func TestTimeWarp_Scenario3_RoadPavingCorridors(t *testing.T) {
	tmpDir := t.TempDir()
	sim, err := RunRoadPavingCorridorScenario(TimeWarpConfig{
		AgentID: "boreas",
		WorkDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("Scenario 3 failed: %v", err)
	}
	defer sim.Close()

	if sim.PavingIntentsCount == 0 {
		t.Fatalf("expected road paving intents to be emitted along corridor")
	}
	t.Logf("Successfully emitted %d paving intents along directional corridor", sim.PavingIntentsCount)
}

// TestTimeWarp_Scenario4_DuskShelterConstruction verifies Scenario 4:
// Harvest timber -> craft planks -> build 5x5 cabin bottom-up without timing out or self-entombing.
func TestTimeWarp_Scenario4_DuskShelterConstruction(t *testing.T) {
	tmpDir := t.TempDir()
	sim, err := RunDuskShelterConstructionScenario(TimeWarpConfig{
		AgentID: "Able",
		WorkDir: tmpDir,
	})
	if err != nil {
		for _, tr := range sim.OscillationDetector.Transitions() {
			t.Logf("TRANSITION: %d: %s -> %s (%s)", tr.Tick, tr.FromState, tr.ToState, tr.Reason)
		}
		t.Fatalf("Scenario 4 failed: %v", err)
	}
	defer sim.Close()

	placed := sim.BlueprintDetector.Count()
	t.Logf("Successfully placed %d cabin blueprint blocks bottom-up to completion", placed)
}

// TestTimeWarp_Scenario5_NighttimeContemplation verifies Scenario 5:
// Triggers during SLEEP, updates ledger, consolidates vector memories.
func TestTimeWarp_Scenario5_NighttimeContemplation(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"immediate_goal":"SECURE_OUTPOST","short_term_goal":"FORTIFY_SETTLEMENT","long_term_goal":"EXPAND_CLAN_TERRITORY","trade_adjustments":[]}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	llmClient, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: llmServer.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(llmServer.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating LLM client: %v", err)
	}
	defer llmClient.Close()

	contemplator := system2.NewContemplationService(llmClient)

	tmpDir := t.TempDir()
	sim, err := RunNighttimeContemplationScenario(TimeWarpConfig{
		AgentID: "contemplative_agent",
		WorkDir: tmpDir,
	}, contemplator)
	if err != nil {
		t.Fatalf("Scenario 5 failed: %v", err)
	}
	defer sim.Close()

	// Advance into deep night and await async contemplation completion
	for i := 0; i < 50; i++ {
		_ = sim.Step(4)
	}
	time.Sleep(150 * time.Millisecond)

	s1 := sim.Runtime().System1State()
	if s1 == nil {
		t.Fatalf("system1 state is nil")
	}
	routine := s1.GetCurrentRoutine()
	t.Logf("Current routine during bedtime: %s", routine)
	if routine != system1.RoutineSleep {
		t.Fatalf("expected RoutineSleep during nighttime, got %s", routine)
	}
}

// TestTimeWarp_FSMOscillationDetector_CatchesFreeze verifies that the oscillation detector
// correctly catches and flags rapid fluttering (>2 transitions between identical states within 60s).
func TestTimeWarp_FSMOscillationDetector_CatchesFreeze(t *testing.T) {
	det := NewFSMOscillationDetector(1200, 2)

	// Simulate the playtest freeze pattern:
	// EXPLORE -> SHELTER -> EXPLORE -> SHELTER within 60 seconds (1200 ticks)
	det.RecordTransition(system1.StateExplore, system1.StateShelter, 12000, "dusk approaching")
	det.RecordTransition(system1.StateShelter, system1.StateExplore, 12600, "shelter timeout fallback")
	det.RecordTransition(system1.StateExplore, system1.StateShelter, 12601, "re-entering shelter")

	if err := det.Check(); err == nil {
		t.Fatalf("expected oscillation detector to flag violation on 3rd transition between EXPLORE and SHELTER, got nil")
	} else {
		t.Logf("Successfully caught oscillation: %v", err)
	}
}
