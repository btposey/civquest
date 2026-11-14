package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestAgentStatus_LiveSocketFormattedOutput(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "solas")

	sockPath := r.AgentSocketPath("solas")
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatalf("failed creating agent state dir: %v", err)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed listening on unix socket: %v", err)
	}
	defer listener.Close()

	mockResp := agent.SocketStatusResponse{
		Agent:           "solas",
		Clan:            "pioneers",
		Status:          "RUNNING",
		PID:             12345,
		WorldTick:       2500,
		Health:          19.5,
		FoodLevel:       14,
		UptimeSeconds:   42.5,
		MemoryRSS:       67371008,
		MemoryRSSMB:     64.25,
		FrameLatencyMs:  1.85,
		ActiveLLMRequests: 2,
		NavigationState: "NAVIGATION_STATE_PATHING",
		Position: &agent.SocketPosition{
			X: 12.0,
			Y: 65.0,
			Z: -40.0,
		},
		NavigationDestination: &agent.SocketPosition{
			X: 100.0,
			Y: 64.0,
			Z: 200.0,
		},
		UtilityScores: &agent.System1UtilityScores{
			Hunger:      0.30,
			Threat:      0.00,
			Confidence:  1.50,
			Exploration: 0.85,
			SleepNeed:   0.10,
		},
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			data, err := json.Marshal(mockResp)
			if err == nil {
				data = append(data, '\n')
				_, _ = conn.Write(data)
			}
			_ = conn.Close()
		}
	}()

	// 1. Human-readable status output
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "status", "solas",
	)
	if err != nil {
		t.Fatalf("agent status failed: %v\nOutput: %s", err, out)
	}

	expectedSubstrings := []string{
		"CivQuest Agent Status: solas",
		"Active (PID 12345, Live Socket)",
		"Uptime:          42.5s",
		"Memory RSS:      64.25 MB",
		"Health:          19.5",
		"Food Level:      14 / 20",
		"Navigation:      NAVIGATION_STATE_PATHING",
		"Position:        (12.0, 65.0, -40.0)",
		"Nav Destination: (100.0, 64.0, 200.0)",
		"Frame Latency:   1.85 ms",
		"Active LLM Reqs: 2",
		"System 1 Utility Scores:",
		"• Hunger:        0.30",
		"• Threat:        0.00",
		"• Confidence:    1.50",
		"• Exploration:   0.85",
		"• Sleep Need:    0.10",
	}

	for _, substr := range expectedSubstrings {
		if !strings.Contains(out, substr) {
			t.Errorf("expected output to contain %q, but got:\n%s", substr, out)
		}
	}

	// 2. JSON status output
	jsonOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "status", "solas", "--json",
	)
	if err != nil {
		t.Fatalf("agent status --json failed: %v\nOutput: %s", err, jsonOut)
	}

	var jsonMap map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &jsonMap); err != nil {
		t.Fatalf("failed decoding json output: %v\nOutput: %s", err, jsonOut)
	}

	if active, ok := jsonMap["active"].(bool); !ok || !active {
		t.Errorf("expected active=true in JSON output, got %+v", jsonMap["active"])
	}

	telemetryRaw, ok := jsonMap["telemetry"].(map[string]any)
	if !ok || telemetryRaw == nil {
		t.Fatalf("expected 'telemetry' object in JSON output, got: %s", jsonOut)
	}

	if telemetryRaw["agent"] != "solas" {
		t.Errorf("expected telemetry.agent='solas', got %v", telemetryRaw["agent"])
	}
	if telemetryRaw["memory_rss_mb"] != 64.25 {
		t.Errorf("expected memory_rss_mb=64.25, got %v", telemetryRaw["memory_rss_mb"])
	}
	if telemetryRaw["frame_latency_ms"] != 1.85 {
		t.Errorf("expected frame_latency_ms=1.85, got %v", telemetryRaw["frame_latency_ms"])
	}
	if float64(telemetryRaw["active_llm_requests"].(float64)) != 2 {
		t.Errorf("expected active_llm_requests=2, got %v", telemetryRaw["active_llm_requests"])
	}

	utilScores, ok := telemetryRaw["utility_scores"].(map[string]any)
	if !ok || utilScores == nil {
		t.Fatalf("expected utility_scores map in telemetry, got %+v", telemetryRaw["utility_scores"])
	}
	if utilScores["hunger"] != 0.30 {
		t.Errorf("expected hunger 0.30, got %v", utilScores["hunger"])
	}
	if utilScores["confidence"] != 1.50 {
		t.Errorf("expected confidence 1.50, got %v", utilScores["confidence"])
	}
}
