package domain_test

import (
	"encoding/json"
	"fmt"
	"net"
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

	// Create a real active unix socket listener for aria
	if err := r.InitAgentDirectories("aria"); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}
	sockPath := r.AgentSocketPath("aria")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen mock socket failed: %v", err)
	}
	defer func() {
		_ = l.Close()
		_ = os.Remove(sockPath)
	}()

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
		t.Errorf("expected aria.Active == true because socket listener is active")
	}
}

func TestDetectAgents_DeceasedInGraveyard(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories failed: %v", err)
	}

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

	ariaCfg := xdg.DefaultAgentIdentityConfig("aria")
	ariaCfg.ClanID = "pioneers"
	ariaCfg.Profession = "FARMER"
	if err := r.SaveAgentConfig(ariaCfg); err != nil {
		t.Fatalf("SaveAgentConfig(aria) failed: %v", err)
	}

	// Create graveyard memorial archive for aria
	graveDir, err := r.GraveyardAgentDir("aria", "00000000-0000-0000-0000-000000000011", 1000)
	if err != nil {
		t.Fatalf("GraveyardAgentDir failed: %v", err)
	}
	if err := os.MkdirAll(graveDir, 0750); err != nil {
		t.Fatalf("mkdir graveDir failed: %v", err)
	}

	agentsStatus, err := domain.DetectAgents(r)
	if err != nil {
		t.Fatalf("DetectAgents failed: %v", err)
	}

	if len(agentsStatus.ConfiguredAgents) != 1 {
		t.Fatalf("expected 1 configured agent, got %d", len(agentsStatus.ConfiguredAgents))
	}
	summary := agentsStatus.ConfiguredAgents[0]
	if !summary.Deceased {
		t.Errorf("expected summary.Deceased == true")
	}
	if summary.Active {
		t.Errorf("expected summary.Active == false for deceased agent")
	}
}

func TestDetectAgents_StaleSocket(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories failed: %v", err)
	}
	if err := r.InitAgentDirectories("aria"); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	ariaCfg := xdg.DefaultAgentIdentityConfig("aria")
	ariaCfg.ClanID = "pioneers"
	ariaCfg.Profession = "FARMER"
	if err := r.SaveAgentConfig(ariaCfg); err != nil {
		t.Fatalf("SaveAgentConfig(aria) failed: %v", err)
	}

	// Create a socket, then immediately close it so it becomes stale
	sockPath := r.AgentSocketPath("aria")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	_ = l.Close() // Socket file remains on disk, but nothing listening

	agentsStatus, err := domain.DetectAgents(r)
	if err != nil {
		t.Fatalf("DetectAgents failed: %v", err)
	}
	if len(agentsStatus.ConfiguredAgents) != 1 {
		t.Fatalf("expected 1 configured agent, got %d", len(agentsStatus.ConfiguredAgents))
	}
	if agentsStatus.ConfiguredAgents[0].Active {
		t.Errorf("expected aria.Active == false for stale closed socket")
	}
	// Verify stale socket file was cleaned up
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("expected stale socket file to be cleaned up")
	}
}

func TestDetectServer_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Create empty server dir (no manifest, no run.sh, no .jar)
	if err := os.MkdirAll(r.ServerDir(), 0755); err != nil {
		t.Fatalf("mkdir server dir failed: %v", err)
	}

	status, err := domain.DetectServer(r)
	if err != nil {
		t.Fatalf("DetectServer failed: %v", err)
	}
	if status.Installed {
		t.Errorf("expected status.Installed == false for empty server directory, got true")
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
