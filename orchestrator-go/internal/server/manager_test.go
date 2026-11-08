package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
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

func TestManager_ConcurrencyLock(t *testing.T) {
	r, mgr := setupMockServer(t)

	// Pre-acquire lock on server.lock
	if err := os.MkdirAll(r.StateDir(), 0755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	lockPath := r.StateDir() + "/server.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("open lock file failed: %v", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock failed: %v", err)
	}

	// Attempting Start while lock is held must fail
	_, startErr := mgr.Start()
	if startErr == nil {
		t.Fatalf("expected error starting when lock is held, got nil")
	}
	if !strings.Contains(startErr.Error(), "holds lock") {
		t.Errorf("unexpected error message: %v", startErr)
	}

	// Release lock
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	// Now Start should succeed
	pid, startErr := mgr.Start()
	if startErr != nil {
		t.Fatalf("start failed after lock release: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = mgr.Stop(ctx, 1*time.Second)
	if pid <= 0 {
		t.Errorf("expected positive pid, got %d", pid)
	}
}

func TestManager_RunnerScriptTraversal(t *testing.T) {
	r, mgr := setupMockServer(t)

	// Create script outside server directory
	outsideScript := r.DataDir() + "/outside.sh"
	if err := os.WriteFile(outsideScript, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write outside script failed: %v", err)
	}

	// Point manifest to script outside server directory
	manifestData, err := os.ReadFile(r.ServerManifestPath())
	if err != nil {
		t.Fatalf("read manifest failed: %v", err)
	}
	var manifest domain.ServerManifest
	_ = json.Unmarshal(manifestData, &manifest)
	manifest.RunnerScript = outsideScript
	data, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(r.ServerManifestPath(), data, 0644)

	_, err = mgr.Start()
	if err == nil {
		t.Fatalf("expected error on runner script traversal, got nil")
	}
	if !strings.Contains(err.Error(), "security violation") {
		t.Errorf("expected security violation error, got: %v", err)
	}
}

func TestManager_StopContextCancellation(t *testing.T) {
	r, mgr := setupMockServer(t)

	// Use a mock script with realistic graceful shutdown delay (sleep 0.5s) upon SIGTERM,
	// so the process remains running during immediate context cancellation inspection.
	mockSlowScript := `#!/usr/bin/env bash
trap "echo [mock-server] caught SIGTERM >> server.log; sleep 0.5; exit 0" SIGTERM
echo "[mock-server] Initializing Minecraft 1.21.1 NeoForge server..."
while true; do
    sleep 0.05
done
`
	if err := os.WriteFile(r.ServerRunScriptPath(), []byte(mockSlowScript), 0755); err != nil {
		t.Fatalf("write slow mock script failed: %v", err)
	}

	pid, err := mgr.Start()
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before or during Stop

	err = mgr.Stop(ctx, 3*time.Second)
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected cancelled error, got %v", err)
	}

	// Verify PID file was NOT deleted
	if _, statErr := os.Stat(r.ServerPIDPath()); os.IsNotExist(statErr) {
		t.Errorf("server.pid should NOT have been removed when context cancelled")
	}

	// Verify process is still alive (not killed with SIGKILL)
	if !domain.IsProcessRunning(pid) {
		t.Errorf("server process %d should still be alive after context cancellation", pid)
	}

	// Clean shutdown with fresh context
	ctxClean, cancelClean := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClean()
	if err := mgr.Stop(ctxClean, 2*time.Second); err != nil {
		t.Fatalf("clean stop failed: %v", err)
	}
}

func TestManager_LogsLargeLine(t *testing.T) {
	r, mgr := setupMockServer(t)

	// Write log file with a 128KB single line
	if err := os.MkdirAll(r.StateDir(), 0755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	largePayload := strings.Repeat("CIVQUEST_LOG_ENTRY_", 8*1024) // ~150KB
	logContent := "line 1\n" + largePayload + "\nline 3\n"
	if err := os.WriteFile(r.ServerLogPath(), []byte(logContent), 0644); err != nil {
		t.Fatalf("write large log failed: %v", err)
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := mgr.Logs(ctx, 10, false, &buf); err != nil {
		t.Fatalf("Logs failed on large line: %v", err)
	}
	if !strings.Contains(buf.String(), "CIVQUEST_LOG_ENTRY_") {
		t.Errorf("expected large line to be read correctly")
	}
}

func TestManager_Setup(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	mgr := server.NewManager(r)

	opts := server.DefaultSetupOptions()
	opts.Port = 25565
	opts.OnlineMode = false

	if err := mgr.Setup(opts); err != nil {
		t.Fatalf("mgr.Setup failed: %v", err)
	}

	// 1. Verify eula.txt exists and contains eula=true
	eulaBytes, err := os.ReadFile(r.ServerEULAPath())
	if err != nil {
		t.Fatalf("failed reading eula.txt: %v", err)
	}
	if !strings.Contains(string(eulaBytes), "eula=true") {
		t.Errorf("expected eula.txt to contain eula=true, got: %s", string(eulaBytes))
	}

	// 2. Verify server.properties exists and contains expected configurations
	propsBytes, err := os.ReadFile(r.ServerPropertiesPath())
	if err != nil {
		t.Fatalf("failed reading server.properties: %v", err)
	}
	propsStr := string(propsBytes)
	if !strings.Contains(propsStr, "server-port=25565") {
		t.Errorf("expected server-port=25565 in server.properties, got: %s", propsStr)
	}
	if !strings.Contains(propsStr, "online-mode=false") {
		t.Errorf("expected online-mode=false in server.properties, got: %s", propsStr)
	}

	// 3. Verify runner script exists and is executable
	info, err := os.Stat(r.ServerRunScriptPath())
	if err != nil {
		t.Fatalf("failed stat runner script: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("runner script should be executable, mode: %v", info.Mode())
	}

	// 4. Verify manifest was written
	if _, err := os.Stat(r.ServerManifestPath()); err != nil {
		t.Errorf("expected server manifest to exist at %s: %v", r.ServerManifestPath(), err)
	}

	// 5. Verify status detects installation
	status, err := mgr.Status()
	if err != nil {
		t.Fatalf("mgr.Status failed: %v", err)
	}
	if !status.Installed {
		t.Errorf("expected server status to be installed after Setup")
	}

	// 6. Test Force overwrite with different port
	opts.Port = 25570
	opts.Force = true
	if err := mgr.Setup(opts); err != nil {
		t.Fatalf("mgr.Setup with Force failed: %v", err)
	}
	updatedProps, _ := os.ReadFile(r.ServerPropertiesPath())
	if !strings.Contains(string(updatedProps), "server-port=25570") {
		t.Errorf("expected updated server-port=25570 after Force setup, got: %s", string(updatedProps))
	}
}
