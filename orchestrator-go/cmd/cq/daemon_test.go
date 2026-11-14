package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/daemon"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestDaemonCLI_StartStatusStop(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	t.Setenv("CIVQUEST_IN_TEST", "1")

	setupTestAgent(t, r, "aria")

	mockScriptPath := filepath.Join(tmpDir, "bin", "civquest-agent")
	mockScriptContent := `#!/usr/bin/env bash
echo "[civquest-agent] Running agent worker"
>&2 echo "[civquest-agent] Debug warning stream"
trap "exit 0" SIGTERM SIGINT
while true; do
    sleep 0.05
done
`
	if err := os.MkdirAll(filepath.Dir(mockScriptPath), 0o755); err != nil {
		t.Fatalf("failed creating bin dir: %v", err)
	}
	if err := os.WriteFile(mockScriptPath, []byte(mockScriptContent), 0o755); err != nil {
		t.Fatalf("failed writing mock agent: %v", err)
	}
	t.Setenv("CIVQUEST_AGENT_BIN", mockScriptPath)

	baseArgs := []string{
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
	}

	// 1. Initial status: should report inactive
	out, err := executeCommand(append(baseArgs, "daemon", "status")...)
	if err != nil {
		t.Fatalf("daemon status failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "CivQuest supervisor daemon is inactive") {
		t.Errorf("expected inactive status, got: %s", out)
	}

	// 2. Start daemon
	out, err = executeCommand(append(baseArgs, "daemon", "start", "--agent-bin", mockScriptPath)...)
	if err != nil {
		t.Fatalf("daemon start failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "started in background") {
		t.Errorf("expected background start confirmation, got: %s", out)
	}

	// 3. Status human output
	time.Sleep(300 * time.Millisecond)
	out, err = executeCommand(append(baseArgs, "daemon", "status")...)
	if err != nil {
		t.Fatalf("daemon status failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Supervisor Daemon Status") {
		t.Errorf("expected supervisor daemon status header, got: %s", out)
	}
	if !strings.Contains(out, "aria") {
		t.Errorf("expected aria in agent worker list, got: %s", out)
	}

	// 4. Status JSON output
	out, err = executeCommand(append(baseArgs, "--json", "daemon", "status")...)
	if err != nil {
		t.Fatalf("daemon status --json failed: %v\nOutput: %s", err, out)
	}
	var st daemon.DaemonStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("failed unmarshaling daemon status JSON: %v\nOutput: %s", err, out)
	}
	if !st.Running {
		t.Errorf("expected Running=true in status JSON")
	}
	if st.PID <= 0 {
		t.Errorf("expected valid PID in status JSON, got %d", st.PID)
	}

	// 5. Verify JSONL log was captured
	logPath := r.AgentHumanLogPath("aria")
	entries, err := daemon.ReadJSONLLogs(logPath)
	if err != nil {
		t.Fatalf("failed reading jsonl logs from %s: %v", logPath, err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected non-empty JSONL log entries for aria")
	}

	// 6. Stop daemon
	out, err = executeCommand(append(baseArgs, "daemon", "stop")...)
	if err != nil {
		t.Fatalf("daemon stop failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected stopped confirmation, got: %s", out)
	}

	// 7. Status after stop: should report inactive
	out, err = executeCommand(append(baseArgs, "daemon", "status")...)
	if err != nil {
		t.Fatalf("daemon status failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "CivQuest supervisor daemon is inactive") {
		t.Errorf("expected inactive status after stop, got: %s", out)
	}
}
