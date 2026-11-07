package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/server"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupMockServer(t *testing.T) (*xdg.Resolver, *server.Manager) {
	t.Helper()
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := os.MkdirAll(r.ServerModsDir(), 0755); err != nil {
		t.Fatalf("mkdir server mods failed: %v", err)
	}

	manifest := domain.ServerManifest{
		Version:          "0.1.0-SNAPSHOT",
		MinecraftVersion: "1.21.1",
		NeoForgeVersion:  "21.1.265",
		InstalledAt:      time.Now().Format(time.RFC3339),
		ServerDir:        r.ServerDir(),
		ModsDir:          r.ServerModsDir(),
		ModJar:           "mod-neoforge-0.1.0-SNAPSHOT.jar",
		JavaOptions:      []string{"-Xms2G", "-Xmx4G"},
		RunnerScript:     r.ServerRunScriptPath(),
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(r.ServerManifestPath(), data, 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}

	// Create mock run.sh
	mockScript := `#!/usr/bin/env bash
trap "echo [mock-server] caught SIGTERM >> server.log; exit 0" SIGTERM
echo "[mock-server] Initializing Minecraft 1.21.1 NeoForge server..."
while true; do
    sleep 0.05
done
`
	if err := os.WriteFile(r.ServerRunScriptPath(), []byte(mockScript), 0755); err != nil {
		t.Fatalf("write mock run.sh failed: %v", err)
	}

	mgr := server.NewManager(r)
	return r, mgr
}

func TestManager_NotInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	mgr := server.NewManager(r)

	_, err := mgr.Start()
	if err == nil {
		t.Fatalf("expected error when server is not installed, got nil")
	}
	if !strings.Contains(err.Error(), "server domain is not installed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestManager_Lifecycle(t *testing.T) {
	_, mgr := setupMockServer(t)

	// Initial status should be installed, not running
	status, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !status.Installed {
		t.Errorf("expected installed == true")
	}
	if status.Running {
		t.Errorf("expected running == false")
	}

	// Start server
	pid, err := mgr.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if pid <= 0 {
		t.Errorf("expected positive pid, got %d", pid)
	}

	// Give process a moment to spin up
	time.Sleep(150 * time.Millisecond)

	// Status should now be running
	status, err = mgr.Status()
	if err != nil {
		t.Fatalf("Status after start failed: %v", err)
	}
	if !status.Running {
		t.Errorf("expected running == true after Start")
	}
	if status.PID != pid {
		t.Errorf("expected status.PID == %d, got %d", pid, status.PID)
	}

	// Starting again should error
	_, err = mgr.Start()
	if err == nil {
		t.Errorf("expected error on duplicate start, got nil")
	}

	// Read logs
	var logBuf bytes.Buffer
	ctxLogs, cancelLogs := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelLogs()
	if err := mgr.Logs(ctxLogs, 20, false, &logBuf); err != nil {
		t.Fatalf("Logs failed: %v", err)
	}
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "Starting CivQuest NeoForge Server") {
		t.Errorf("expected start banner in logs, got %q", logOutput)
	}

	// Stop server gracefully
	ctxStop, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err := mgr.Stop(ctxStop, 2*time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Status should be stopped
	status, err = mgr.Status()
	if err != nil {
		t.Fatalf("Status after stop failed: %v", err)
	}
	if status.Running {
		t.Errorf("expected running == false after Stop")
	}

	// Stopping again should return ErrServerNotRunning
	err = mgr.Stop(ctxStop, 1*time.Second)
	if err == nil {
		t.Errorf("expected error on duplicate stop, got nil")
	}
}

func TestManager_StalePIDCleanup(t *testing.T) {
	r, mgr := setupMockServer(t)

	// Write stale PID
	if err := os.MkdirAll(r.StateDir(), 0755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	if err := os.WriteFile(r.ServerPIDPath(), []byte("9999999\n"), 0644); err != nil {
		t.Fatalf("write stale pid failed: %v", err)
	}

	// Stop should detect stale PID and clean it up
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := mgr.Stop(ctx, 500*time.Millisecond)
	if err == nil {
		t.Errorf("expected error stopping with stale PID")
	}

	// Verify PID file was removed
	if _, err := os.Stat(r.ServerPIDPath()); !os.IsNotExist(err) {
		t.Errorf("expected stale PID file to be removed")
	}
}
