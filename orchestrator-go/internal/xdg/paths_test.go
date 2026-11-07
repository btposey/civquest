package xdg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestDefaultResolution(t *testing.T) {
	mockHome := "/home/mockuser"
	r := xdg.NewResolver(
		xdg.WithHomeDir(mockHome),
		xdg.WithEnv(map[string]string{}),
	)

	expectedConfig := filepath.Join(mockHome, ".config", "civquest")
	expectedData := filepath.Join(mockHome, ".local", "share", "civquest")
	expectedState := filepath.Join(mockHome, ".local", "state", "civquest")
	expectedCache := filepath.Join(mockHome, ".cache", "civquest")
	expectedRuntime := filepath.Join(expectedState, "run")

	if got := r.ConfigDir(); got != expectedConfig {
		t.Errorf("ConfigDir() = %q, want %q", got, expectedConfig)
	}
	if got := r.DataDir(); got != expectedData {
		t.Errorf("DataDir() = %q, want %q", got, expectedData)
	}
	if got := r.StateDir(); got != expectedState {
		t.Errorf("StateDir() = %q, want %q", got, expectedState)
	}
	if got := r.CacheDir(); got != expectedCache {
		t.Errorf("CacheDir() = %q, want %q", got, expectedCache)
	}
	if got := r.RuntimeDir(); got != expectedRuntime {
		t.Errorf("RuntimeDir() fallback = %q, want %q", got, expectedRuntime)
	}

	// Verify path helpers
	if got := r.GlobalConfigPath(); got != filepath.Join(expectedConfig, "global.yaml") {
		t.Errorf("GlobalConfigPath() = %q", got)
	}
	if got := r.AgentsConfigDir(); got != filepath.Join(expectedConfig, "agents") {
		t.Errorf("AgentsConfigDir() = %q", got)
	}
	if got := r.AgentConfigPath("aria"); got != filepath.Join(expectedConfig, "agents", "aria.yaml") {
		t.Errorf("AgentConfigPath() = %q", got)
	}
	if got := r.BlueprintsDir(); got != filepath.Join(expectedConfig, "blueprints") {
		t.Errorf("BlueprintsDir() = %q", got)
	}
	if got := r.OfficialBlueprintsDir(); got != filepath.Join(expectedConfig, "blueprints", "official") {
		t.Errorf("OfficialBlueprintsDir() = %q", got)
	}
	if got := r.PlayerImportedBlueprintsDir(); got != filepath.Join(expectedConfig, "blueprints", "player_imported") {
		t.Errorf("PlayerImportedBlueprintsDir() = %q", got)
	}
	if got := r.LLMGeneratedBlueprintsDir(); got != filepath.Join(expectedConfig, "blueprints", "llm_generated") {
		t.Errorf("LLMGeneratedBlueprintsDir() = %q", got)
	}

	// Data helpers
	if got := r.AgentsDataDir(); got != filepath.Join(expectedData, "agents") {
		t.Errorf("AgentsDataDir() = %q", got)
	}
	if got := r.AgentDataDir("aria"); got != filepath.Join(expectedData, "agents", "aria") {
		t.Errorf("AgentDataDir() = %q", got)
	}
	if got := r.AgentMemoryDBPath("aria"); got != filepath.Join(expectedData, "agents", "aria", "memory.db") {
		t.Errorf("AgentMemoryDBPath() = %q", got)
	}
	if got := r.AgentMapGraphDBPath("aria"); got != filepath.Join(expectedData, "agents", "aria", "map_graph.db") {
		t.Errorf("AgentMapGraphDBPath() = %q", got)
	}
	if got := r.AgentInventoryCachePath("aria"); got != filepath.Join(expectedData, "agents", "aria", "inventory_cache.json") {
		t.Errorf("AgentInventoryCachePath() = %q", got)
	}
	if got := r.AgentTexturesDir("aria"); got != filepath.Join(expectedData, "agents", "aria", "textures") {
		t.Errorf("AgentTexturesDir() = %q", got)
	}
	if got := r.AgentSkinPath("aria"); got != filepath.Join(expectedData, "agents", "aria", "textures", "skin.png") {
		t.Errorf("AgentSkinPath() = %q", got)
	}

	// Clan helpers
	if got := r.ClansDataDir(); got != filepath.Join(expectedData, "clans") {
		t.Errorf("ClansDataDir() = %q", got)
	}
	if got := r.ClanDataDir("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron") {
		t.Errorf("ClanDataDir() = %q", got)
	}
	if got := r.ClanCivicRegistryDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "civic_registry.db") {
		t.Errorf("ClanCivicRegistryDBPath() = %q", got)
	}
	if got := r.ClanReputationDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "reputation.db") {
		t.Errorf("ClanReputationDBPath() = %q", got)
	}
	if got := r.ClanDiplomacyDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "diplomacy.db") {
		t.Errorf("ClanDiplomacyDBPath() = %q", got)
	}
	if got := r.ClanLandmarksDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "landmarks.db") {
		t.Errorf("ClanLandmarksDBPath() = %q", got)
	}
	if got := r.ClanRumorsDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "rumors.db") {
		t.Errorf("ClanRumorsDBPath() = %q", got)
	}
	if got := r.ClanContractsDBPath("clan_iron"); got != filepath.Join(expectedData, "clans", "clan_iron", "contracts.db") {
		t.Errorf("ClanContractsDBPath() = %q", got)
	}

	// Graveyard helpers
	if got := r.GraveyardDir(); got != filepath.Join(expectedData, "graveyard") {
		t.Errorf("GraveyardDir() = %q", got)
	}
	expectedGrave := filepath.Join(expectedData, "graveyard", "aria_uuid-42_tick12000")
	if got := r.GraveyardAgentDir("aria", "uuid-42", 12000); got != expectedGrave {
		t.Errorf("GraveyardAgentDir() = %q", got)
	}
	if got := r.GraveyardMemoryDBPath("aria", "uuid-42", 12000); got != filepath.Join(expectedGrave, "memory.db") {
		t.Errorf("GraveyardMemoryDBPath() = %q", got)
	}
	if got := r.GraveyardDeathCertificatePath("aria", "uuid-42", 12000); got != filepath.Join(expectedGrave, "death_certificate.json") {
		t.Errorf("GraveyardDeathCertificatePath() = %q", got)
	}
	if got := r.GraveyardEpitaphPath("aria", "uuid-42", 12000); got != filepath.Join(expectedGrave, "epitaph.txt") {
		t.Errorf("GraveyardEpitaphPath() = %q", got)
	}

	// State helpers
	if got := r.AgentsStateDir(); got != filepath.Join(expectedState, "agents") {
		t.Errorf("AgentsStateDir() = %q", got)
	}
	if got := r.AgentStateDir("aria"); got != filepath.Join(expectedState, "agents", "aria") {
		t.Errorf("AgentStateDir() = %q", got)
	}
	if got := r.AgentLogPath("aria"); got != filepath.Join(expectedState, "agents", "aria", "aria.log") {
		t.Errorf("AgentLogPath() = %q", got)
	}
	if got := r.AgentSocketPath("aria"); got != filepath.Join(expectedState, "agents", "aria", "aria.sock") {
		t.Errorf("AgentSocketPath() = %q", got)
	}
	if got := r.SupervisorPIDPath(); got != filepath.Join(expectedState, "supervisor.pid") {
		t.Errorf("SupervisorPIDPath() = %q", got)
	}
}

func TestEnvOverrides(t *testing.T) {
	env := map[string]string{
		"XDG_CONFIG_HOME": "/custom/config_root",
		"XDG_DATA_HOME":   "/custom/data_root",
		"XDG_STATE_HOME":  "/custom/state_root",
		"XDG_CACHE_HOME":  "/custom/cache_root",
		"XDG_RUNTIME_DIR": "/custom/runtime_root",
	}

	r := xdg.NewResolver(xdg.WithGetenv(func(k string) string {
		return env[k]
	}))

	if got := r.ConfigDir(); got != "/custom/config_root/civquest" {
		t.Errorf("ConfigDir() = %q, want %q", got, "/custom/config_root/civquest")
	}
	if got := r.DataDir(); got != "/custom/data_root/civquest" {
		t.Errorf("DataDir() = %q, want %q", got, "/custom/data_root/civquest")
	}
	if got := r.StateDir(); got != "/custom/state_root/civquest" {
		t.Errorf("StateDir() = %q, want %q", got, "/custom/state_root/civquest")
	}
	if got := r.CacheDir(); got != "/custom/cache_root/civquest" {
		t.Errorf("CacheDir() = %q, want %q", got, "/custom/cache_root/civquest")
	}
	if got := r.RuntimeDir(); got != "/custom/runtime_root/civquest" {
		t.Errorf("RuntimeDir() = %q, want %q", got, "/custom/runtime_root/civquest")
	}
}

func TestExplicitHomeOverrides(t *testing.T) {
	r := xdg.NewResolver(
		xdg.WithConfigHome("/explicit/config"),
		xdg.WithDataHome("/explicit/data"),
		xdg.WithStateHome("/explicit/state"),
		xdg.WithCacheHome("/explicit/cache"),
		xdg.WithRuntimeDir("/explicit/run"),
	)

	if got := r.ConfigDir(); got != "/explicit/config/civquest" {
		t.Errorf("ConfigDir() = %q", got)
	}
	if got := r.DataDir(); got != "/explicit/data/civquest" {
		t.Errorf("DataDir() = %q", got)
	}
	if got := r.StateDir(); got != "/explicit/state/civquest" {
		t.Errorf("StateDir() = %q", got)
	}
	if got := r.CacheDir(); got != "/explicit/cache/civquest" {
		t.Errorf("CacheDir() = %q", got)
	}
	if got := r.RuntimeDir(); got != "/explicit/run/civquest" {
		t.Errorf("RuntimeDir() = %q", got)
	}
}

func TestCustomRoot(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if got := r.ConfigDir(); got != filepath.Join(tmpDir, "config", "civquest") {
		t.Errorf("ConfigDir() = %q", got)
	}
	if got := r.DataDir(); got != filepath.Join(tmpDir, "data", "civquest") {
		t.Errorf("DataDir() = %q", got)
	}
	if got := r.StateDir(); got != filepath.Join(tmpDir, "state", "civquest") {
		t.Errorf("StateDir() = %q", got)
	}
	if got := r.CacheDir(); got != filepath.Join(tmpDir, "cache", "civquest") {
		t.Errorf("CacheDir() = %q", got)
	}
	if got := r.RuntimeDir(); got != filepath.Join(tmpDir, "run", "civquest") {
		t.Errorf("RuntimeDir() = %q", got)
	}
}

func TestInitDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories() failed: %v", err)
	}

	expectedDirs := []string{
		r.ConfigDir(),
		r.AgentsConfigDir(),
		r.BlueprintsDir(),
		r.OfficialBlueprintsDir(),
		r.PlayerImportedBlueprintsDir(),
		r.LLMGeneratedBlueprintsDir(),
		r.DataDir(),
		r.AgentsDataDir(),
		r.ClansDataDir(),
		r.GraveyardDir(),
		r.StateDir(),
		r.AgentsStateDir(),
		r.CacheDir(),
		r.RuntimeDir(),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("expected directory %s to exist, stat error: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", dir)
		}
		perm := info.Mode().Perm()
		if perm != 0750 {
			t.Errorf("directory %s permission = %04o, want 0750", dir, perm)
		}
	}

	// Test agent and clan directory initialization
	if err := r.InitAgentDirectories("settler_alpha"); err != nil {
		t.Fatalf("InitAgentDirectories() failed: %v", err)
	}
	for _, dir := range []string{
		r.AgentDataDir("settler_alpha"),
		r.AgentTexturesDir("settler_alpha"),
		r.AgentStateDir("settler_alpha"),
	} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("expected agent dir %s to exist", dir)
		}
	}

	if err := r.InitClanDirectories("clan_ironwood"); err != nil {
		t.Fatalf("InitClanDirectories() failed: %v", err)
	}
	if info, err := os.Stat(r.ClanDataDir("clan_ironwood")); err != nil || !info.IsDir() {
		t.Errorf("expected clan dir %s to exist", r.ClanDataDir("clan_ironwood"))
	}

	// Test invalid names
	if err := r.InitAgentDirectories("../escape"); err == nil {
		t.Errorf("expected error for path traversal agent name, got nil")
	}
	if err := r.InitClanDirectories("invalid/name"); err == nil {
		t.Errorf("expected error for slash in clan id, got nil")
	}
}

func TestPackageLevelConvenience(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "cfg")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")
	cacheDir := filepath.Join(tmpDir, "cache")
	runDir := filepath.Join(tmpDir, "run")

	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	t.Setenv("XDG_RUNTIME_DIR", runDir)

	if xdg.DefaultResolver() == nil {
		t.Fatal("DefaultResolver() must not be nil")
	}

	// Config paths
	if got := xdg.ConfigDir(); got != filepath.Join(cfgDir, "civquest") {
		t.Errorf("package ConfigDir() = %q", got)
	}
	if got := xdg.GlobalConfigPath(); got != filepath.Join(cfgDir, "civquest", "global.yaml") {
		t.Errorf("package GlobalConfigPath() = %q", got)
	}
	if got := xdg.AgentsConfigDir(); got != filepath.Join(cfgDir, "civquest", "agents") {
		t.Errorf("package AgentsConfigDir() = %q", got)
	}
	if got := xdg.AgentConfigPath("aria"); got != filepath.Join(cfgDir, "civquest", "agents", "aria.yaml") {
		t.Errorf("package AgentConfigPath() = %q", got)
	}
	if got := xdg.BlueprintsDir(); got != filepath.Join(cfgDir, "civquest", "blueprints") {
		t.Errorf("package BlueprintsDir() = %q", got)
	}
	if got := xdg.OfficialBlueprintsDir(); got != filepath.Join(cfgDir, "civquest", "blueprints", "official") {
		t.Errorf("package OfficialBlueprintsDir() = %q", got)
	}
	if got := xdg.PlayerImportedBlueprintsDir(); got != filepath.Join(cfgDir, "civquest", "blueprints", "player_imported") {
		t.Errorf("package PlayerImportedBlueprintsDir() = %q", got)
	}
	if got := xdg.LLMGeneratedBlueprintsDir(); got != filepath.Join(cfgDir, "civquest", "blueprints", "llm_generated") {
		t.Errorf("package LLMGeneratedBlueprintsDir() = %q", got)
	}

	// Data paths
	if got := xdg.DataDir(); got != filepath.Join(dataDir, "civquest") {
		t.Errorf("package DataDir() = %q", got)
	}
	if got := xdg.AgentDataDir("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria") {
		t.Errorf("package AgentDataDir() = %q", got)
	}
	if got := xdg.AgentMemoryDBPath("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria", "memory.db") {
		t.Errorf("package AgentMemoryDBPath() = %q", got)
	}
	if got := xdg.AgentMapGraphDBPath("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria", "map_graph.db") {
		t.Errorf("package AgentMapGraphDBPath() = %q", got)
	}
	if got := xdg.AgentInventoryCachePath("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria", "inventory_cache.json") {
		t.Errorf("package AgentInventoryCachePath() = %q", got)
	}
	if got := xdg.AgentTexturesDir("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria", "textures") {
		t.Errorf("package AgentTexturesDir() = %q", got)
	}
	if got := xdg.AgentSkinPath("aria"); got != filepath.Join(dataDir, "civquest", "agents", "aria", "textures", "skin.png") {
		t.Errorf("package AgentSkinPath() = %q", got)
	}
	if got := xdg.ClanDataDir("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1") {
		t.Errorf("package ClanDataDir() = %q", got)
	}
	if got := xdg.ClanCivicRegistryDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "civic_registry.db") {
		t.Errorf("package ClanCivicRegistryDBPath() = %q", got)
	}
	if got := xdg.ClanReputationDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "reputation.db") {
		t.Errorf("package ClanReputationDBPath() = %q", got)
	}
	if got := xdg.ClanDiplomacyDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "diplomacy.db") {
		t.Errorf("package ClanDiplomacyDBPath() = %q", got)
	}
	if got := xdg.ClanLandmarksDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "landmarks.db") {
		t.Errorf("package ClanLandmarksDBPath() = %q", got)
	}
	if got := xdg.ClanRumorsDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "rumors.db") {
		t.Errorf("package ClanRumorsDBPath() = %q", got)
	}
	if got := xdg.ClanContractsDBPath("clan1"); got != filepath.Join(dataDir, "civquest", "clans", "clan1", "contracts.db") {
		t.Errorf("package ClanContractsDBPath() = %q", got)
	}
	if got := xdg.GraveyardDir(); got != filepath.Join(dataDir, "civquest", "graveyard") {
		t.Errorf("package GraveyardDir() = %q", got)
	}
	if got := xdg.GraveyardAgentDir("aria", "u-1", 100); got != filepath.Join(dataDir, "civquest", "graveyard", "aria_u-1_tick100") {
		t.Errorf("package GraveyardAgentDir() = %q", got)
	}

	// State paths
	if got := xdg.StateDir(); got != filepath.Join(stateDir, "civquest") {
		t.Errorf("package StateDir() = %q", got)
	}
	if got := xdg.AgentStateDir("aria"); got != filepath.Join(stateDir, "civquest", "agents", "aria") {
		t.Errorf("package AgentStateDir() = %q", got)
	}
	if got := xdg.AgentLogPath("aria"); got != filepath.Join(stateDir, "civquest", "agents", "aria", "aria.log") {
		t.Errorf("package AgentLogPath() = %q", got)
	}
	if got := xdg.AgentSocketPath("aria"); got != filepath.Join(stateDir, "civquest", "agents", "aria", "aria.sock") {
		t.Errorf("package AgentSocketPath() = %q", got)
	}
	if got := xdg.SupervisorPIDPath(); got != filepath.Join(stateDir, "civquest", "supervisor.pid") {
		t.Errorf("package SupervisorPIDPath() = %q", got)
	}

	// Cache & runtime
	if got := xdg.CacheDir(); got != filepath.Join(cacheDir, "civquest") {
		t.Errorf("package CacheDir() = %q", got)
	}
	if got := xdg.RuntimeDir(); got != filepath.Join(runDir, "civquest") {
		t.Errorf("package RuntimeDir() = %q", got)
	}

	// Package directory initialization
	if err := xdg.InitDirectories(); err != nil {
		t.Fatalf("package InitDirectories() error: %v", err)
	}
	if err := xdg.InitAgentDirectories("aria"); err != nil {
		t.Fatalf("package InitAgentDirectories() error: %v", err)
	}
	if err := xdg.InitClanDirectories("clan1"); err != nil {
		t.Fatalf("package InitClanDirectories() error: %v", err)
	}
}
