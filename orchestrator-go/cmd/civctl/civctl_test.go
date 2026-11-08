package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func executeCommand(args ...string) (string, error) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

func TestCivctl_Status_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")

	out, err := executeCommand(
		"--config-dir", cfgDir,
		"--data-dir", dataDir,
		"--state-dir", stateDir,
		"status",
	)
	if err != nil {
		t.Fatalf("civctl status failed: %v\nOutput: %s", err, out)
	}

	if !strings.Contains(out, "CivQuest Host Installation Topology") {
		t.Errorf("expected header in output, got: %s", out)
	}
	if !strings.Contains(out, "Installed: No") {
		t.Errorf("expected 'Installed: No' in output, got: %s", out)
	}
}

func TestCivctl_Status_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")

	out, err := executeCommand(
		"--config-dir", cfgDir,
		"--data-dir", dataDir,
		"--state-dir", stateDir,
		"--json",
		"info",
	)
	if err != nil {
		t.Fatalf("civctl info --json failed: %v\nOutput: %s", err, out)
	}

	var top domain.Topology
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("failed unmarshaling json output: %v\nOutput: %s", err, out)
	}

	if top.Server.Installed {
		t.Errorf("expected server not installed")
	}
	if top.Agents.Installed {
		t.Errorf("expected agents not installed")
	}
}

func TestCivctl_Server_Commands(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// 1. Check server status before installation
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"server", "status",
	)
	if err == nil {
		t.Fatalf("expected error when checking status on uninstalled server, got nil")
	}
	if !strings.Contains(err.Error(), "run './install.sh server'") {
		t.Errorf("expected install hint in error, got: %v", err)
	}

	// 2. Prepare server directory & mock runner script
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
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(r.ServerManifestPath(), manifestData, 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}

	mockScript := `#!/usr/bin/env bash
trap "exit 0" SIGTERM
while true; do sleep 0.05; done
`
	if err := os.WriteFile(r.ServerRunScriptPath(), []byte(mockScript), 0755); err != nil {
		t.Fatalf("write mock run.sh failed: %v", err)
	}

	// 3. Status should now report STOPPED
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"server", "status",
	)
	if err != nil {
		t.Fatalf("server status failed: %v", err)
	}
	if !strings.Contains(out, "STOPPED") {
		t.Errorf("expected STOPPED in output, got: %s", out)
	}

	// 4. Start server
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"server", "start",
	)
	if err != nil {
		t.Fatalf("server start failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "started in background") {
		t.Errorf("expected start success message, got: %s", out)
	}

	time.Sleep(100 * time.Millisecond)

	// 5. Status should now report RUNNING
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"server", "status",
	)
	if err != nil {
		t.Fatalf("server status after start failed: %v", err)
	}
	if !strings.Contains(out, "RUNNING") {
		t.Errorf("expected RUNNING in output, got: %s", out)
	}

	// 6. Stop server
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"server", "stop",
	)
	if err != nil {
		t.Fatalf("server stop failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "stopped gracefully") {
		t.Errorf("expected stop success message, got: %s", out)
	}
}

func TestCivctl_Agent_Commands(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// 1. Agent list before installation
	_, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "list",
	)
	if err == nil {
		t.Fatalf("expected error listing agents when not installed")
	}

	// 2. Initialize directories and agent config
	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories failed: %v", err)
	}

	aria := xdg.DefaultAgentIdentityConfig("aria")
	aria.ClanID = "pioneers"
	aria.Profession = "FARMER"
	if err := r.SaveAgentConfig(aria); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	// 3. Agent list after configuration
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "list",
	)
	if err != nil {
		t.Fatalf("agent list failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "aria") || !strings.Contains(out, "FARMER") {
		t.Errorf("expected aria / FARMER in output, got: %s", out)
	}

	// 4. Agent status aria
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "status", "aria",
	)
	if err != nil {
		t.Fatalf("agent status aria failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "CivQuest Agent Status: aria") {
		t.Errorf("expected aria status header, got: %s", out)
	}
}

func TestCivctl_ServerSetup(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")

	// Test server setup command
	out, err := executeCommand(
		"--config-dir", cfgDir,
		"--data-dir", dataDir,
		"--state-dir", stateDir,
		"server", "setup", "--port", "25565",
	)
	if err != nil {
		t.Fatalf("server setup failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "server environment prepared successfully") {
		t.Errorf("expected success message in output, got: %s", out)
	}

	// Verify server status is now installed (STOPPED)
	out, err = executeCommand(
		"--config-dir", cfgDir,
		"--data-dir", dataDir,
		"--state-dir", stateDir,
		"server", "status",
	)
	if err != nil {
		t.Fatalf("server status failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Status:    STOPPED") {
		t.Errorf("expected STOPPED status after setup, got: %s", out)
	}
}
