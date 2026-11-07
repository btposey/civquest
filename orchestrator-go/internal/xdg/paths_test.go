package xdg_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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
	gotGrave, err := r.GraveyardAgentDir("aria", "uuid-42", 12000)
	if err != nil {
		t.Fatalf("GraveyardAgentDir() error: %v", err)
	}
	if gotGrave != expectedGrave {
		t.Errorf("GraveyardAgentDir() = %q, want %q", gotGrave, expectedGrave)
	}
	gotMem, err := r.GraveyardMemoryDBPath("aria", "uuid-42", 12000)
	if err != nil {
		t.Fatalf("GraveyardMemoryDBPath() error: %v", err)
	}
	if gotMem != filepath.Join(expectedGrave, "memory.db") {
		t.Errorf("GraveyardMemoryDBPath() = %q", gotMem)
	}
	gotDeath, err := r.GraveyardDeathCertificatePath("aria", "uuid-42", 12000)
	if err != nil {
		t.Fatalf("GraveyardDeathCertificatePath() error: %v", err)
	}
	if gotDeath != filepath.Join(expectedGrave, "death_certificate.json") {
		t.Errorf("GraveyardDeathCertificatePath() = %q", gotDeath)
	}
	gotEpi, err := r.GraveyardEpitaphPath("aria", "uuid-42", 12000)
	if err != nil {
		t.Fatalf("GraveyardEpitaphPath() error: %v", err)
	}
	if gotEpi != filepath.Join(expectedGrave, "epitaph.txt") {
		t.Errorf("GraveyardEpitaphPath() = %q", gotEpi)
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
	if got := r.AgentSocketPath("aria"); got != filepath.Join(expectedState, "agents", "aria.sock") {
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

	standardDirs := []string{
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
	}

	for _, dir := range standardDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("expected directory %s to exist, stat error: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", dir)
		}
		perm := info.Mode().Perm()
		if perm&0700 != 0700 {
			t.Errorf("directory %s missing owner permissions: %04o", dir, perm)
		}
		if perm&0027 != 0 {
			t.Errorf("directory %s has unauthorized permissions (group write or other access): %04o", dir, perm)
		}
	}

	// Verify runtime dir permissions specifically (must be 0700 per XDG specification)
	runtimeInfo, err := os.Stat(r.RuntimeDir())
	if err != nil {
		t.Fatalf("expected runtime directory %s to exist: %v", r.RuntimeDir(), err)
	}
	if !runtimeInfo.IsDir() {
		t.Errorf("expected %s to be a directory", r.RuntimeDir())
	}
	if perm := runtimeInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("runtime directory %s permission = %04o, want 0700", r.RuntimeDir(), perm)
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
	gotGravePkg, err := xdg.GraveyardAgentDir("aria", "u-1", 100)
	if err != nil {
		t.Fatalf("package GraveyardAgentDir() error: %v", err)
	}
	if gotGravePkg != filepath.Join(dataDir, "civquest", "graveyard", "aria_u-1_tick100") {
		t.Errorf("package GraveyardAgentDir() = %q", gotGravePkg)
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
	if got := xdg.AgentSocketPath("aria"); got != filepath.Join(stateDir, "civquest", "agents", "aria.sock") {
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

func TestGraveyardPathTraversal(t *testing.T) {
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))

	invalidUUIDs := []string{
		"../../escape",
		"/absolute/path",
		"foo/bar",
		"foo\\bar",
		"..",
		".",
		"",
		"uuid:colon",
		"uuid*star",
	}

	for _, badUUID := range invalidUUIDs {
		t.Run("uuid_"+badUUID, func(t *testing.T) {
			_, err := r.GraveyardAgentDir("aria", badUUID, 100)
			if err == nil {
				t.Errorf("expected error for invalid UUID %q, got nil", badUUID)
			}
			if !errors.Is(err, xdg.ErrInvalidUUID) {
				t.Errorf("expected error to wrap ErrInvalidUUID, got: %v", err)
			}

			if _, err := r.GraveyardMemoryDBPath("aria", badUUID, 100); err == nil {
				t.Errorf("expected error from GraveyardMemoryDBPath for %q", badUUID)
			}
			if _, err := r.GraveyardDeathCertificatePath("aria", badUUID, 100); err == nil {
				t.Errorf("expected error from GraveyardDeathCertificatePath for %q", badUUID)
			}
			if _, err := r.GraveyardEpitaphPath("aria", badUUID, 100); err == nil {
				t.Errorf("expected error from GraveyardEpitaphPath for %q", badUUID)
			}
		})
	}

	invalidAgentNames := []string{
		"../aria",
		"aria/sub",
		"aria\\sub",
		".",
		"..",
		"",
	}

	for _, badName := range invalidAgentNames {
		t.Run("agent_"+badName, func(t *testing.T) {
			_, err := r.GraveyardAgentDir(badName, "valid-uuid-1", 100)
			if err == nil {
				t.Errorf("expected error for invalid agentName %q in graveyard, got nil", badName)
			}
			if !errors.Is(err, xdg.ErrInvalidAgentName) {
				t.Errorf("expected error to wrap ErrInvalidAgentName, got: %v", err)
			}
		})
	}
}

func TestIdentifierValidation(t *testing.T) {
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))

	invalidIdentifiers := []struct {
		name string
		id   string
	}{
		{"single dot", "."},
		{"double dot", ".."},
		{"leading dot", ".hidden"},
		{"dot in middle", "aria.name"},
		{"trailing dot", "aria."},
		{"empty string", ""},
		{"whitespace only", "   "},
		{"leading whitespace", " aria"},
		{"trailing whitespace", "aria "},
		{"slash", "aria/path"},
		{"backslash", "aria\\path"},
		{"colon", "aria:path"},
		{"at symbol", "aria@path"},
		{"dollar", "aria$path"},
		{"exceeds 32 chars", "a12345678901234567890123456789012"}, // 33 chars
		{"leading hyphen", "-aria"},
		{"leading underscore", "_aria"},
	}

	for _, tc := range invalidIdentifiers {
		t.Run(tc.name, func(t *testing.T) {
			errAgent := r.InitAgentDirectories(tc.id)
			if errAgent == nil {
				t.Errorf("InitAgentDirectories(%q) expected error, got nil", tc.id)
			}
			if !errors.Is(errAgent, xdg.ErrInvalidAgentName) {
				t.Errorf("InitAgentDirectories(%q) error should wrap ErrInvalidAgentName, got: %v", tc.id, errAgent)
			}

			errClan := r.InitClanDirectories(tc.id)
			if errClan == nil {
				t.Errorf("InitClanDirectories(%q) expected error, got nil", tc.id)
			}
			if !errors.Is(errClan, xdg.ErrInvalidClanID) {
				t.Errorf("InitClanDirectories(%q) error should wrap ErrInvalidClanID, got: %v", tc.id, errClan)
			}
		})
	}

	validIdentifiers := []string{
		"a",
		"aria",
		"settler_1",
		"settler-alpha",
		"Clan_42-Iron",
		"a1234567890123456789012345678901", // exactly 32 chars
	}

	for _, valid := range validIdentifiers {
		t.Run("valid_"+valid, func(t *testing.T) {
			if err := r.InitAgentDirectories(valid); err != nil {
				t.Errorf("InitAgentDirectories(%q) unexpected error: %v", valid, err)
			}
			if err := r.InitClanDirectories(valid); err != nil {
				t.Errorf("InitClanDirectories(%q) unexpected error: %v", valid, err)
			}
		})
	}
}

func TestRelativeXDGEnvironmentVariables(t *testing.T) {
	mockHome := "/home/mockuser"
	env := map[string]string{
		"XDG_CONFIG_HOME": "relative/config",
		"XDG_DATA_HOME":   "relative/data",
		"XDG_STATE_HOME":  "relative/state",
		"XDG_CACHE_HOME":  "relative/cache",
		"XDG_RUNTIME_DIR": "relative/run",
	}

	r := xdg.NewResolver(
		xdg.WithHomeDir(mockHome),
		xdg.WithGetenv(func(k string) string { return env[k] }),
	)

	expectedConfig := filepath.Join(mockHome, ".config", "civquest")
	expectedData := filepath.Join(mockHome, ".local", "share", "civquest")
	expectedState := filepath.Join(mockHome, ".local", "state", "civquest")
	expectedCache := filepath.Join(mockHome, ".cache", "civquest")
	expectedRuntime := filepath.Join(expectedState, "run")

	if got := r.ConfigDir(); got != expectedConfig {
		t.Errorf("ConfigDir() with relative env = %q, want %q", got, expectedConfig)
	}
	if got := r.DataDir(); got != expectedData {
		t.Errorf("DataDir() with relative env = %q, want %q", got, expectedData)
	}
	if got := r.StateDir(); got != expectedState {
		t.Errorf("StateDir() with relative env = %q, want %q", got, expectedState)
	}
	if got := r.CacheDir(); got != expectedCache {
		t.Errorf("CacheDir() with relative env = %q, want %q", got, expectedCache)
	}
	if got := r.RuntimeDir(); got != expectedRuntime {
		t.Errorf("RuntimeDir() with relative env = %q, want %q", got, expectedRuntime)
	}
}

func TestAgentSocketPathLength(t *testing.T) {
	r := xdg.NewResolver(xdg.WithHomeDir("/home/mockuser"))
	// Maximum valid 32-character agent identifier
	maxLenAgentName := "a1234567890123456789012345678901"
	socketPath := r.AgentSocketPath(maxLenAgentName)

	expected := filepath.Join("/home/mockuser", ".local", "state", "civquest", "agents", maxLenAgentName+".sock")
	if socketPath != expected {
		t.Errorf("AgentSocketPath = %q, want %q", socketPath, expected)
	}

	// Linux sockaddr_un sun_path is 108 bytes; ADR-004 requires staying safely under 104 bytes
	if len(socketPath) >= 104 {
		t.Errorf("AgentSocketPath length %d exceeds safe limit of 104 bytes: %q", len(socketPath), socketPath)
	}
}

func TestInitDirectoriesStrictUmask(t *testing.T) {
	oldMask := syscall.Umask(0077)
	defer syscall.Umask(oldMask)

	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories under strict umask 0077 failed: %v", err)
	}

	// Check runtime directory has 0700
	runtimeInfo, err := os.Stat(r.RuntimeDir())
	if err != nil {
		t.Fatalf("stat runtime dir failed: %v", err)
	}
	if perm := runtimeInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("runtime directory permission under umask 0077 = %04o, want 0700", perm)
	}

	// Check config dir has owner permissions and no unauthorized bits
	cfgInfo, err := os.Stat(r.ConfigDir())
	if err != nil {
		t.Fatalf("stat config dir failed: %v", err)
	}
	cfgPerm := cfgInfo.Mode().Perm()
	if cfgPerm&0700 != 0700 {
		t.Errorf("config dir under umask 0077 missing owner permissions: %04o", cfgPerm)
	}
	if cfgPerm&0027 != 0 {
		t.Errorf("config dir under umask 0077 has unauthorized bits: %04o", cfgPerm)
	}
}
