package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupTestAgent(t *testing.T, r *xdg.Resolver, agentName string) {
	t.Helper()
	if err := os.MkdirAll(r.AgentsConfigDir(), 0755); err != nil {
		t.Fatalf("failed creating agents config dir: %v", err)
	}

	cfg := &xdg.AgentIdentityConfig{
		Name:       agentName,
		ClanID:     "pioneers",
		Profession: "EXPLORER",
		Skin: xdg.AgentSkinConfig{
			Model: "default",
		},
	}
	if err := r.SaveAgentConfig(cfg); err != nil {
		t.Fatalf("failed saving test agent config: %v", err)
	}
}

func TestWatchdog_RestartOnCleanExitWithoutDeath(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "aria")

	t.Setenv("CIVQUEST_IN_TEST", "1")
	t.Setenv("CIVQUEST_WATCHDOG_TEST_FAST", "1")

	runCountFile := filepath.Join(tmpDir, "run_count")

	// Mock agent script: exit 0 on first run, stay alive on second run
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgentScript := filepath.Join(binDir, "civquest-agent")
	mockContent := fmt.Sprintf(`#!/usr/bin/env bash
COUNT_FILE=%q
COUNT=0
if [ -f "$COUNT_FILE" ]; then
    COUNT=$(cat "$COUNT_FILE")
fi
COUNT=$((COUNT + 1))
echo "$COUNT" > "$COUNT_FILE"

if [ "$COUNT" -eq 1 ]; then
    # First run: exit 0 unexpectedly without death
    sleep 0.05
    exit 0
else
    # Subsequent runs: stay alive
    trap "exit 0" SIGTERM
    while true; do sleep 0.05; done
fi
`, runCountFile)

	if err := os.WriteFile(mockAgentScript, []byte(mockContent), 0755); err != nil {
		t.Fatalf("failed writing mock agent script: %v", err)
	}
	t.Setenv("CIVQUEST_AGENT_BIN", mockAgentScript)

	// Start agent with --restart-on-exit
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "aria", "--restart-on-exit",
	)
	if err != nil {
		t.Fatalf("agent start failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "started with supervisor watchdog") {
		t.Errorf("expected watchdog start confirmation, got: %s", out)
	}

	// Verify that the process exited once and was restarted by the watchdog
	restarted := false
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(runCountFile)
		if err == nil {
			count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			if count >= 2 {
				restarted = true
				break
			}
		}
	}
	if !restarted {
		t.Fatal("expected watchdog to restart agent after unexpected clean exit (code 0)")
	}

	// Verify PID file exists and represents the restarted process
	pidPath := r.AgentPIDPath("aria")
	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("expected active PID file at %s after restart", pidPath)
	}

	// Stop agent gracefully
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "stop", "aria",
	)
	if err != nil {
		t.Fatalf("agent stop failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "stopped successfully") {
		t.Errorf("expected stop confirmation, got: %s", out)
	}
}

func TestWatchdog_NoRestartOnPermadeath(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "boris")

	t.Setenv("CIVQUEST_IN_TEST", "1")
	t.Setenv("CIVQUEST_WATCHDOG_TEST_FAST", "1")

	runCountFile := filepath.Join(tmpDir, "boris_run_count")

	// Mock agent script: logs Permadeath complete and exits 0
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgentScript := filepath.Join(binDir, "civquest-agent")
	mockContent := fmt.Sprintf(`#!/usr/bin/env bash
COUNT_FILE=%q
COUNT=0
if [ -f "$COUNT_FILE" ]; then
    COUNT=$(cat "$COUNT_FILE")
fi
COUNT=$((COUNT + 1))
echo "$COUNT" > "$COUNT_FILE"

echo "[civquest-agent] Permadeath complete. Memorial archive created. Clean permadeath exit code 0."
exit 0
`, runCountFile)

	if err := os.WriteFile(mockAgentScript, []byte(mockContent), 0755); err != nil {
		t.Fatalf("failed writing mock agent script: %v", err)
	}
	t.Setenv("CIVQUEST_AGENT_BIN", mockAgentScript)

	// Start agent with --restart-on-exit
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "boris", "--restart-on-exit",
	)
	if err != nil {
		t.Fatalf("agent start failed: %v\nOutput: %s", err, out)
	}

	// Wait for process to exit and watchdog to evaluate permadeath
	time.Sleep(300 * time.Millisecond)

	// Verify it ran exactly ONCE and did NOT restart
	data, err := os.ReadFile(runCountFile)
	if err != nil {
		t.Fatalf("failed reading run count file: %v", err)
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if count != 1 {
		t.Errorf("expected agent to run exactly once on permadeath, ran %d times", count)
	}

	// Verify PID file is cleaned up
	pidPath := r.AgentPIDPath("boris")
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("expected PID file to be removed after permadeath, but it exists at %s", pidPath)
	}
}

func TestWatchdog_ExponentialBackoffAndMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	cfg := &xdg.AgentIdentityConfig{
		Name:       "cedric",
		ClanID:     "pioneers",
		Profession: "FARMER",
	}

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockScript := filepath.Join(binDir, "civquest-agent")
	mockContent := `#!/usr/bin/env bash
exit 1
`
	if err := os.WriteFile(mockScript, []byte(mockContent), 0755); err != nil {
		t.Fatalf("failed writing mock script: %v", err)
	}

	var mu sync.Mutex
	restarts := 0
	var recordedDelays []time.Duration
	maxRetriesHit := false

	opts := WatchdogOptions{
		CleanExitBackoff: 10 * time.Millisecond,
		CrashBackoffs:    []time.Duration{15 * time.Millisecond, 30 * time.Millisecond, 60 * time.Millisecond},
		MaxRetries:       3,
		CheckDeath: func() bool {
			return false
		},
		OnRestart: func(attempt int, delay time.Duration, isCrash bool) {
			mu.Lock()
			defer mu.Unlock()
			restarts++
			recordedDelays = append(recordedDelays, delay)
		},
		OnMaxRetries: func(exitCode int) {
			mu.Lock()
			defer mu.Unlock()
			maxRetriesHit = true
		},
	}

	err := runAgentWatchdog(
		context.Background(),
		r,
		cfg,
		mockScript,
		[]string{"--name", "cedric"},
		"",
		nil,
		opts,
		nil,
	)

	if err == nil {
		t.Fatal("expected error after max restart retries exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "max restart retries (3) exceeded") {
		t.Errorf("expected error message to mention max retries exceeded, got: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if restarts != 3 {
		t.Errorf("expected 3 restart attempts before giving up, got %d", restarts)
	}
	if !maxRetriesHit {
		t.Error("expected OnMaxRetries callback to be called")
	}
	if len(recordedDelays) != 3 {
		t.Fatalf("expected 3 recorded delays, got %d", len(recordedDelays))
	}
	if recordedDelays[0] != 15*time.Millisecond || recordedDelays[1] != 30*time.Millisecond || recordedDelays[2] != 60*time.Millisecond {
		t.Errorf("expected backoff delays [15ms, 30ms, 60ms], got %v", recordedDelays)
	}
}

func TestWatchdog_GracefulStopDoesNotRestart(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "diana")

	t.Setenv("CIVQUEST_IN_TEST", "1")
	t.Setenv("CIVQUEST_WATCHDOG_TEST_FAST", "1")

	runCountFile := filepath.Join(tmpDir, "diana_run_count")

	// Mock agent script: loops cleanly until SIGTERM
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgentScript := filepath.Join(binDir, "civquest-agent")
	mockContent := fmt.Sprintf(`#!/usr/bin/env bash
COUNT_FILE=%q
COUNT=0
if [ -f "$COUNT_FILE" ]; then
    COUNT=$(cat "$COUNT_FILE")
fi
COUNT=$((COUNT + 1))
echo "$COUNT" > "$COUNT_FILE"

trap "exit 0" SIGTERM
while true; do sleep 0.05; done
`, runCountFile)

	if err := os.WriteFile(mockAgentScript, []byte(mockContent), 0755); err != nil {
		t.Fatalf("failed writing mock agent script: %v", err)
	}
	t.Setenv("CIVQUEST_AGENT_BIN", mockAgentScript)

	// Start agent with --restart-on-exit
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "diana", "--restart-on-exit",
	)
	if err != nil {
		t.Fatalf("agent start failed: %v\nOutput: %s", err, out)
	}

	pidPath := r.AgentPIDPath("diana")
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("expected PID file at %s", pidPath)
	}

	// Wait until mock script has started running and written initial count
	for i := 0; i < 50; i++ {
		if data, err := os.ReadFile(runCountFile); err == nil {
			if c, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && c >= 1 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Stop agent via cq stop
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "stop", "diana",
	)
	if err != nil {
		t.Fatalf("agent stop failed: %v\nOutput: %s", err, out)
	}

	// Sleep to ensure watchdog does NOT restart the stopped agent
	time.Sleep(200 * time.Millisecond)

	data, err := os.ReadFile(runCountFile)
	if err != nil {
		t.Fatalf("failed reading run count: %v", err)
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if count != 1 {
		t.Errorf("expected agent to run only once and not be restarted after stop, count = %d", count)
	}
}

func TestWatchdog_CannotStartDeceasedAgentInGraveyard(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	setupTestAgent(t, r, "elena")

	// Create graveyard memorial archive for elena
	graveDir := r.GraveyardDir()
	_ = os.MkdirAll(filepath.Join(graveDir, "elena_testuuid_tick100"), 0750)

	_, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "elena", "--restart-on-exit",
	)
	if err == nil {
		t.Fatal("expected error starting deceased agent in graveyard, got nil")
	}
	if !strings.Contains(err.Error(), "deceased") {
		t.Errorf("expected error message to mention deceased agent, got: %v", err)
	}
}
