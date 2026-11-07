package domain_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestDetectTopology_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	topology, err := domain.DetectTopology(r)
	if err != nil {
		t.Fatalf("DetectTopology failed: %v", err)
	}

	if topology.Server.Installed {
		t.Errorf("expected Server.Installed == false, got true")
	}
	if topology.Server.Running {
		t.Errorf("expected Server.Running == false, got true")
	}
	if topology.Agents.Installed {
		t.Errorf("expected Agents.Installed == false, got true")
	}
	if len(topology.Agents.ConfiguredAgents) != 0 {
		t.Errorf("expected 0 configured agents, got %d", len(topology.Agents.ConfiguredAgents))
	}
	if topology.Clans.Initialized {
		t.Errorf("expected Clans.Initialized == false, got true")
	}
}

func TestDetectTopology_ServerInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Create server directory and manifest
	if err := os.MkdirAll(r.ServerModsDir(), 0755); err != nil {
		t.Fatalf("mkdir server mods failed: %v", err)
	}

	manifest := domain.ServerManifest{
		Version:          "0.1.0-SNAPSHOT",
		MinecraftVersion: "1.21.1",
		NeoForgeVersion:  "21.1.265",
		InstalledAt:      "2026-11-07T12:00:00Z",
		ServerDir:        r.ServerDir(),
		ModsDir:          r.ServerModsDir(),
		ModJar:           "mod-neoforge-0.1.0-SNAPSHOT.jar",
		JavaOptions:      []string{"-Xms2G", "-Xmx4G"},
		RunnerScript:     r.ServerRunScriptPath(),
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(r.ServerManifestPath(), manifestData, 0644); err != nil {
		t.Fatalf("write server manifest failed: %v", err)
	}

	serverStatus, err := domain.DetectServer(r)
	if err != nil {
		t.Fatalf("DetectServer failed: %v", err)
	}

	if !serverStatus.Installed {
		t.Errorf("expected Server.Installed == true, got false")
	}
	if serverStatus.Running {
		t.Errorf("expected Server.Running == false without PID file")
	}
	if serverStatus.Manifest == nil || serverStatus.Manifest.MinecraftVersion != "1.21.1" {
		t.Errorf("expected manifest with MC 1.21.1, got %+v", serverStatus.Manifest)
	}

	// Test with current running process PID (our own test process)
	myPID := os.Getpid()
	if err := os.MkdirAll(r.StateDir(), 0755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	if err := os.WriteFile(r.ServerPIDPath(), []byte(fmt.Sprintf("%d\n", myPID)), 0644); err != nil {
		t.Fatalf("write PID failed: %v", err)
	}

	serverStatus, err = domain.DetectServer(r)
	if err != nil {
		t.Fatalf("DetectServer with PID failed: %v", err)
	}
	if !serverStatus.Running {
		t.Errorf("expected Server.Running == true for live PID %d", myPID)
	}
	if serverStatus.PID != myPID {
		t.Errorf("expected Server.PID == %d, got %d", myPID, serverStatus.PID)
	}

	// Test stale PID (e.g. 9999999)
	if err := os.WriteFile(r.ServerPIDPath(), []byte("9999999\n"), 0644); err != nil {
		t.Fatalf("write stale PID failed: %v", err)
	}
	serverStatus, err = domain.DetectServer(r)
	if err != nil {
		t.Fatalf("DetectServer with stale PID failed: %v", err)
	}
	if serverStatus.Running {
		t.Errorf("expected Server.Running == false for stale PID 9999999")
	}
}

func TestDetectTopology_AgentsInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Create directories
	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories failed: %v", err)
	}

	// Write agent manifest
	manifest := domain.AgentsManifest{
		Version:     "0.1.0",
		InstalledAt: "2026-11-07T12:00:00Z",
		BinDir:      filepath.Join(tmpDir, "bin"),
		Binaries:    []string{"civctl", "civquest-agent"},
		ConfigDir:   r.ConfigDir(),
		DataDir:     r.DataDir(),
		StateDir:    r.StateDir(),
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(r.AgentsManifestPath(), manifestData, 0644); err != nil {
		t.Fatalf("write agent manifest failed: %v", err)
	}

	// Write agent configs
	ariaCfg := xdg.DefaultAgentIdentityConfig("aria")
	ariaCfg.ClanID = "pioneers"
	ariaCfg.Profession = "FARMER"
	if err := r.SaveAgentConfig(ariaCfg); err != nil {
		t.Fatalf("SaveAgentConfig(aria) failed: %v", err)
	}

	// Create a mock socket for aria to test active flag
	if err := r.InitAgentDirectories("aria"); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}
	if err := os.WriteFile(r.AgentSocketPath("aria"), []byte("mock-socket"), 0600); err != nil {
		t.Fatalf("write mock socket failed: %v", err)
	}

	agentsStatus, err := domain.DetectAgents(r)
	if err != nil {
		t.Fatalf("DetectAgents failed: %v", err)
	}

	if !agentsStatus.Installed {
		t.Errorf("expected agents installed == true")
	}
	if len(agentsStatus.ConfiguredAgents) != 1 {
		t.Fatalf("expected 1 configured agent, got %d", len(agentsStatus.ConfiguredAgents))
	}
	ariaSummary := agentsStatus.ConfiguredAgents[0]
	if ariaSummary.Name != "aria" || ariaSummary.Profession != "FARMER" {
		t.Errorf("unexpected agent summary: %+v", ariaSummary)
	}
	if !ariaSummary.Active {
		t.Errorf("expected aria.Active == true because socket exists")
	}
}

func TestDetectTopology_Clans(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Create clan directories
	if err := r.InitClanDirectories("pioneers"); err != nil {
		t.Fatalf("InitClanDirectories failed: %v", err)
	}
	if err := r.InitClanDirectories("iron_order"); err != nil {
		t.Fatalf("InitClanDirectories failed: %v", err)
	}

	clansStatus, err := domain.DetectClans(r)
	if err != nil {
		t.Fatalf("DetectClans failed: %v", err)
	}

	if !clansStatus.Initialized {
		t.Errorf("expected Clans.Initialized == true")
	}
	if clansStatus.ClanCount != 2 {
		t.Errorf("expected 2 clans, got %d", clansStatus.ClanCount)
	}
}
