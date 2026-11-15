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
		Agent:             "solas",
		Clan:              "pioneers",
		Status:            "RUNNING",
		PID:               12345,
		WorldTick:         2500,
		Health:            19.5,
		FoodLevel:         14,
		UptimeSeconds:     42.5,
		MemoryRSS:         67371008,
		MemoryRSSMB:       64.25,
		FrameLatencyMs:    1.85,
		ActiveLLMRequests: 2,
		NavigationState:   "NAVIGATION_STATE_PATHING",
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

func TestAgentSkill_ImportAndExport_SingleAgent(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "solas")

	// 1. Export default prompt before any custom prompt is imported
	defaultExportPath := filepath.Join(tmpDir, "solas_default.md")
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "solas", defaultExportPath,
	)
	if err != nil {
		t.Fatalf("export default failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Successfully exported default") {
		t.Errorf("expected output to mention default export, got: %s", out)
	}
	defaultData, err := os.ReadFile(defaultExportPath)
	if err != nil {
		t.Fatalf("failed to read exported default prompt: %v", err)
	}
	if !strings.Contains(string(defaultData), "solas") {
		t.Errorf("expected exported prompt to mention solas, got: %s", string(defaultData))
	}

	// 2. Export to stdout using '-'
	stdoutOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "solas", "-",
	)
	if err != nil {
		t.Fatalf("export to stdout failed: %v\nOutput: %s", err, stdoutOut)
	}
	if !strings.Contains(stdoutOut, "deliberative strategic mind") {
		t.Errorf("expected stdout export to contain prompt text, got: %s", stdoutOut)
	}

	// 3. Create a custom prompt file and import it
	customSkillText := "You are a master stonemason dedicated to fortifying the settlement perimeter."
	importPath := filepath.Join(tmpDir, "custom.skill")
	if err := os.WriteFile(importPath, []byte(customSkillText), 0644); err != nil {
		t.Fatalf("failed writing custom skill file: %v", err)
	}

	importOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "solas", importPath,
	)
	if err != nil {
		t.Fatalf("import failed: %v\nOutput: %s", err, importOut)
	}
	if !strings.Contains(importOut, "Successfully imported") {
		t.Errorf("expected success message, got: %s", importOut)
	}

	// Verify loaded from resolver
	loaded, err := r.LoadAgentSkill("solas")
	if err != nil {
		t.Fatalf("LoadAgentSkill failed: %v", err)
	}
	if loaded != customSkillText {
		t.Errorf("expected %q, got %q", customSkillText, loaded)
	}

	// 4. Export custom prompt and verify content
	customExportPath := filepath.Join(tmpDir, "solas_custom.md")
	exportCustomOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "solas", customExportPath,
	)
	if err != nil {
		t.Fatalf("export custom failed: %v\nOutput: %s", err, exportCustomOut)
	}
	if !strings.Contains(exportCustomOut, "Successfully exported custom") {
		t.Errorf("expected output to mention custom export, got: %s", exportCustomOut)
	}
	customData, err := os.ReadFile(customExportPath)
	if err != nil {
		t.Fatalf("failed reading exported custom prompt: %v", err)
	}
	if strings.TrimSpace(string(customData)) != customSkillText {
		t.Errorf("expected exported custom prompt %q, got %q", customSkillText, string(customData))
	}

	// 5. JSON output verification for import and export
	jsonImportOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "solas", importPath, "--json",
	)
	if err != nil {
		t.Fatalf("import --json failed: %v\nOutput: %s", err, jsonImportOut)
	}
	var importResp map[string]any
	if err := json.Unmarshal([]byte(jsonImportOut), &importResp); err != nil {
		t.Fatalf("failed parsing JSON import response: %v\nOutput: %s", err, jsonImportOut)
	}
	if importResp["status"] != "ok" || importResp["agent"] != "solas" {
		t.Errorf("unexpected json import response: %+v", importResp)
	}

	jsonExportOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "solas", customExportPath, "--json",
	)
	if err != nil {
		t.Fatalf("export --json failed: %v\nOutput: %s", err, jsonExportOut)
	}
	var exportResp map[string]any
	if err := json.Unmarshal([]byte(jsonExportOut), &exportResp); err != nil {
		t.Fatalf("failed parsing JSON export response: %v\nOutput: %s", err, jsonExportOut)
	}
	if exportResp["status"] != "ok" || exportResp["agent"] != "solas" || exportResp["is_custom"] != true {
		t.Errorf("unexpected json export response: %+v", exportResp)
	}
}

func TestAgentSkill_ImportAndExport_AllAgents(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "solas")
	setupTestAgent(t, r, "lyra")

	// 1. Batch import with -all
	sharedPromptText := "COLLECTIVE_DIRECTIVE: All agents shall prioritize granary reserves and perimeter road paving."
	promptFile := filepath.Join(tmpDir, "shared.skill")
	if err := os.WriteFile(promptFile, []byte(sharedPromptText), 0644); err != nil {
		t.Fatalf("failed writing prompt file: %v", err)
	}

	importOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "-all", promptFile,
	)
	if err != nil {
		t.Fatalf("import -all failed: %v\nOutput: %s", err, importOut)
	}
	if !strings.Contains(importOut, "Successfully imported") || !strings.Contains(importOut, "2 agents") {
		t.Errorf("expected 2 agents imported in output, got: %s", importOut)
	}

	// Verify both agents have the prompt
	for _, name := range []string{"solas", "lyra"} {
		loaded, err := r.LoadAgentSkill(name)
		if err != nil {
			t.Fatalf("LoadAgentSkill(%q) failed: %v", name, err)
		}
		if loaded != sharedPromptText {
			t.Errorf("agent %s expected %q, got %q", name, sharedPromptText, loaded)
		}
	}

	// 2. Batch export with -all to directory
	exportDir := filepath.Join(tmpDir, "exported_skills")
	exportOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "-all", exportDir,
	)
	if err != nil {
		t.Fatalf("export -all failed: %v\nOutput: %s", err, exportOut)
	}
	if !strings.Contains(exportOut, "Successfully exported") {
		t.Errorf("expected success message, got: %s", exportOut)
	}

	// Verify per-agent files exist in exportDir
	solasFile := filepath.Join(exportDir, "solas.skill.md")
	lyraFile := filepath.Join(exportDir, "lyra.skill.md")

	solasData, err := os.ReadFile(solasFile)
	if err != nil {
		t.Fatalf("solas.skill.md not found in exportDir: %v", err)
	}
	if strings.TrimSpace(string(solasData)) != sharedPromptText {
		t.Errorf("expected solas prompt %q, got %q", sharedPromptText, string(solasData))
	}

	lyraData, err := os.ReadFile(lyraFile)
	if err != nil {
		t.Fatalf("lyra.skill.md not found in exportDir: %v", err)
	}
	if strings.TrimSpace(string(lyraData)) != sharedPromptText {
		t.Errorf("expected lyra prompt %q, got %q", sharedPromptText, string(lyraData))
	}

	// 3. Batch import with --all flag
	updatePromptText := "UPDATED_DIRECTIVE: Fortify north watchtower."
	updatePromptFile := filepath.Join(tmpDir, "update.skill")
	if err := os.WriteFile(updatePromptFile, []byte(updatePromptText), 0644); err != nil {
		t.Fatalf("failed writing update prompt file: %v", err)
	}

	importAllFlagOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "--all", updatePromptFile,
	)
	if err != nil {
		t.Fatalf("import --all failed: %v\nOutput: %s", err, importAllFlagOut)
	}
	if !strings.Contains(importAllFlagOut, "Successfully imported") {
		t.Errorf("expected success message with --all, got: %s", importAllFlagOut)
	}

	solasUpdated, _ := r.LoadAgentSkill("solas")
	if solasUpdated != updatePromptText {
		t.Errorf("expected %q, got %q", updatePromptText, solasUpdated)
	}

	// 4. Batch export with --all and --json
	exportDir2 := filepath.Join(tmpDir, "exported_skills_all_flag")
	exportAllJsonOut, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "--all", exportDir2, "--json",
	)
	if err != nil {
		t.Fatalf("export --all --json failed: %v\nOutput: %s", err, exportAllJsonOut)
	}
	var exportAllMap map[string]any
	if err := json.Unmarshal([]byte(exportAllJsonOut), &exportAllMap); err != nil {
		t.Fatalf("failed decoding json export output: %v", err)
	}
	if exportAllMap["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", exportAllMap["status"])
	}
	exportedArr, ok := exportAllMap["exported"].([]any)
	if !ok || len(exportedArr) != 2 {
		t.Errorf("expected 2 exported agents in JSON output, got %d", len(exportedArr))
	}
}

func TestAgentSkill_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "solas")

	// 1. Missing arguments
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import",
	)
	if err == nil {
		t.Errorf("expected error for missing arguments, got nil\nOutput: %s", out)
	}

	// 2. Non-existent prompt file
	_, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "solas", filepath.Join(tmpDir, "nonexistent.prompt"),
	)
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}

	// 3. Empty prompt file
	emptyFile := filepath.Join(tmpDir, "empty.prompt")
	_ = os.WriteFile(emptyFile, []byte("   \n  "), 0644)
	_, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "solas", emptyFile,
	)
	if err == nil {
		t.Errorf("expected error for empty skill file, got nil")
	}

	// 4. Non-existent agent
	validFile := filepath.Join(tmpDir, "valid.prompt")
	_ = os.WriteFile(validFile, []byte("Valid prompt text"), 0644)
	_, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "import", "ghost_agent", validFile,
	)
	if err == nil {
		t.Errorf("expected error importing for non-existent agent, got nil")
	}

	// 5. Non-existent agent export
	_, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "skill", "export", "ghost_agent", filepath.Join(tmpDir, "ghost.prompt"),
	)
	if err == nil {
		t.Errorf("expected error exporting for non-existent agent, got nil")
	}
}
