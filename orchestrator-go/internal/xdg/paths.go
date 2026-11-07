package xdg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Sentinel errors for path operations.
var (
	ErrInvalidAgentName  = errors.New("invalid agent name")
	ErrInvalidClanID     = errors.New("invalid clan ID")
	ErrInvalidUUID       = errors.New("invalid UUID")
	ErrInvalidIdentifier = errors.New("invalid identifier")
)

var validIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

// Resolver resolves filesystem paths in compliance with the XDG Base Directory specification.
type Resolver struct {
	homeDir           string
	configHome        string
	dataHome          string
	stateHome         string
	cacheHome         string
	runtimeDir        string
	customRoot        string
	explicitConfigDir string
	explicitDataDir   string
	explicitStateDir  string
	getenv            func(string) string
}

// Option configures a Resolver instance.
type Option func(*Resolver)

// WithHomeDir overrides the home directory.
func WithHomeDir(homeDir string) Option {
	return func(r *Resolver) {
		r.homeDir = homeDir
	}
}

// WithConfigHome overrides the base config home directory (parent of civquest).
func WithConfigHome(configHome string) Option {
	return func(r *Resolver) {
		r.configHome = configHome
	}
}

// WithDataHome overrides the base data home directory (parent of civquest).
func WithDataHome(dataHome string) Option {
	return func(r *Resolver) {
		r.dataHome = dataHome
	}
}

// WithStateHome overrides the base state home directory (parent of civquest).
func WithStateHome(stateHome string) Option {
	return func(r *Resolver) {
		r.stateHome = stateHome
	}
}

// WithCacheHome overrides the base cache home directory (parent of civquest).
func WithCacheHome(cacheHome string) Option {
	return func(r *Resolver) {
		r.cacheHome = cacheHome
	}
}

// WithRuntimeDir overrides the base runtime directory (parent of civquest).
func WithRuntimeDir(runtimeDir string) Option {
	return func(r *Resolver) {
		r.runtimeDir = runtimeDir
	}
}

// WithCustomRoot configures an isolated root directory containing config, data, state,
// cache, and run subdirectories. Ideal for testing and sandbox isolation.
func WithCustomRoot(root string) Option {
	return func(r *Resolver) {
		r.customRoot = root
	}
}

// WithExplicitDirs configures explicit directories for config, data, and state.
// Any empty string argument is ignored, falling back to standard resolution.
func WithExplicitDirs(configDir, dataDir, stateDir string) Option {
	return func(r *Resolver) {
		r.explicitConfigDir = configDir
		r.explicitDataDir = dataDir
		r.explicitStateDir = stateDir
	}
}

// WithEnv configures environment variable lookup from a fixed map.
func WithEnv(env map[string]string) Option {
	return func(r *Resolver) {
		r.getenv = func(key string) string {
			return env[key]
		}
	}
}

// WithGetenv overrides the environment variable getter function.
func WithGetenv(getenv func(string) string) Option {
	return func(r *Resolver) {
		r.getenv = getenv
	}
}

// NewResolver creates a new Resolver with optional custom configuration.
func NewResolver(opts ...Option) *Resolver {
	r := &Resolver{
		getenv: os.Getenv,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Resolver) getHomeDir() string {
	if r.homeDir != "" {
		return r.homeDir
	}
	if env := r.getenv("HOME"); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return os.TempDir()
}

// ConfigDir returns the base CivQuest configuration directory ($XDG_CONFIG_HOME/civquest).
func (r *Resolver) ConfigDir() string {
	if r.explicitConfigDir != "" {
		return r.explicitConfigDir
	}
	if r.configHome != "" {
		return filepath.Join(r.configHome, "civquest")
	}
	if r.customRoot != "" {
		return filepath.Join(r.customRoot, "config", "civquest")
	}
	if val := r.getenv("XDG_CONFIG_HOME"); val != "" && filepath.IsAbs(val) {
		return filepath.Join(val, "civquest")
	}
	return filepath.Join(r.getHomeDir(), ".config", "civquest")
}

// DataDir returns the base CivQuest data directory ($XDG_DATA_HOME/civquest).
func (r *Resolver) DataDir() string {
	if r.explicitDataDir != "" {
		return r.explicitDataDir
	}
	if r.dataHome != "" {
		return filepath.Join(r.dataHome, "civquest")
	}
	if r.customRoot != "" {
		return filepath.Join(r.customRoot, "data", "civquest")
	}
	if val := r.getenv("XDG_DATA_HOME"); val != "" && filepath.IsAbs(val) {
		return filepath.Join(val, "civquest")
	}
	return filepath.Join(r.getHomeDir(), ".local", "share", "civquest")
}

// StateDir returns the base CivQuest state directory ($XDG_STATE_HOME/civquest).
func (r *Resolver) StateDir() string {
	if r.explicitStateDir != "" {
		return r.explicitStateDir
	}
	if r.stateHome != "" {
		return filepath.Join(r.stateHome, "civquest")
	}
	if r.customRoot != "" {
		return filepath.Join(r.customRoot, "state", "civquest")
	}
	if val := r.getenv("XDG_STATE_HOME"); val != "" && filepath.IsAbs(val) {
		return filepath.Join(val, "civquest")
	}
	return filepath.Join(r.getHomeDir(), ".local", "state", "civquest")
}

// CacheDir returns the base CivQuest cache directory ($XDG_CACHE_HOME/civquest).
func (r *Resolver) CacheDir() string {
	if r.cacheHome != "" {
		return filepath.Join(r.cacheHome, "civquest")
	}
	if r.customRoot != "" {
		return filepath.Join(r.customRoot, "cache", "civquest")
	}
	if val := r.getenv("XDG_CACHE_HOME"); val != "" && filepath.IsAbs(val) {
		return filepath.Join(val, "civquest")
	}
	return filepath.Join(r.getHomeDir(), ".cache", "civquest")
}

// RuntimeDir returns the base CivQuest runtime directory ($XDG_RUNTIME_DIR/civquest).
// If unset or relative, falls back to $XDG_STATE_HOME/civquest/run.
func (r *Resolver) RuntimeDir() string {
	if r.runtimeDir != "" {
		return filepath.Join(r.runtimeDir, "civquest")
	}
	if r.customRoot != "" {
		return filepath.Join(r.customRoot, "run", "civquest")
	}
	if val := r.getenv("XDG_RUNTIME_DIR"); val != "" && filepath.IsAbs(val) {
		return filepath.Join(val, "civquest")
	}
	return filepath.Join(r.StateDir(), "run")
}

// ==================== CONFIG PATHS ====================

// GlobalConfigPath returns $XDG_CONFIG_HOME/civquest/global.yaml.
func (r *Resolver) GlobalConfigPath() string {
	return filepath.Join(r.ConfigDir(), "global.yaml")
}

// AgentsConfigDir returns $XDG_CONFIG_HOME/civquest/agents.
func (r *Resolver) AgentsConfigDir() string {
	return filepath.Join(r.ConfigDir(), "agents")
}

// AgentConfigPath returns $XDG_CONFIG_HOME/civquest/agents/{agent_name}.yaml.
func (r *Resolver) AgentConfigPath(agentName string) string {
	return filepath.Join(r.AgentsConfigDir(), fmt.Sprintf("%s.yaml", agentName))
}

// BlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints.
func (r *Resolver) BlueprintsDir() string {
	return filepath.Join(r.ConfigDir(), "blueprints")
}

// OfficialBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/official.
func (r *Resolver) OfficialBlueprintsDir() string {
	return filepath.Join(r.BlueprintsDir(), "official")
}

// PlayerImportedBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/player_imported.
func (r *Resolver) PlayerImportedBlueprintsDir() string {
	return filepath.Join(r.BlueprintsDir(), "player_imported")
}

// LLMGeneratedBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/llm_generated.
func (r *Resolver) LLMGeneratedBlueprintsDir() string {
	return filepath.Join(r.BlueprintsDir(), "llm_generated")
}

// ==================== DATA PATHS ====================

// AgentsDataDir returns $XDG_DATA_HOME/civquest/agents.
func (r *Resolver) AgentsDataDir() string {
	return filepath.Join(r.DataDir(), "agents")
}

// AgentDataDir returns $XDG_DATA_HOME/civquest/agents/{agent_name}.
func (r *Resolver) AgentDataDir(agentName string) string {
	return filepath.Join(r.AgentsDataDir(), agentName)
}

// AgentMemoryDBPath returns $XDG_DATA_HOME/civquest/agents/{agent_name}/memory.db.
func (r *Resolver) AgentMemoryDBPath(agentName string) string {
	return filepath.Join(r.AgentDataDir(agentName), "memory.db")
}

// AgentMapGraphDBPath returns $XDG_DATA_HOME/civquest/agents/{agent_name}/map_graph.db.
func (r *Resolver) AgentMapGraphDBPath(agentName string) string {
	return filepath.Join(r.AgentDataDir(agentName), "map_graph.db")
}

// AgentInventoryCachePath returns $XDG_DATA_HOME/civquest/agents/{agent_name}/inventory_cache.json.
func (r *Resolver) AgentInventoryCachePath(agentName string) string {
	return filepath.Join(r.AgentDataDir(agentName), "inventory_cache.json")
}

// AgentTexturesDir returns $XDG_DATA_HOME/civquest/agents/{agent_name}/textures.
func (r *Resolver) AgentTexturesDir(agentName string) string {
	return filepath.Join(r.AgentDataDir(agentName), "textures")
}

// AgentSkinPath returns $XDG_DATA_HOME/civquest/agents/{agent_name}/textures/skin.png.
func (r *Resolver) AgentSkinPath(agentName string) string {
	return filepath.Join(r.AgentTexturesDir(agentName), "skin.png")
}

// ClansDataDir returns $XDG_DATA_HOME/civquest/clans.
func (r *Resolver) ClansDataDir() string {
	return filepath.Join(r.DataDir(), "clans")
}

// ClanDataDir returns $XDG_DATA_HOME/civquest/clans/{clan_id}.
func (r *Resolver) ClanDataDir(clanID string) string {
	return filepath.Join(r.ClansDataDir(), clanID)
}

// ClanCivicRegistryDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/civic_registry.db.
func (r *Resolver) ClanCivicRegistryDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "civic_registry.db")
}

// ClanReputationDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/reputation.db.
func (r *Resolver) ClanReputationDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "reputation.db")
}

// ClanDiplomacyDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/diplomacy.db.
func (r *Resolver) ClanDiplomacyDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "diplomacy.db")
}

// ClanLandmarksDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/landmarks.db.
func (r *Resolver) ClanLandmarksDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "landmarks.db")
}

// ClanRumorsDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/rumors.db.
func (r *Resolver) ClanRumorsDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "rumors.db")
}

// ClanContractsDBPath returns $XDG_DATA_HOME/civquest/clans/{clan_id}/contracts.db.
func (r *Resolver) ClanContractsDBPath(clanID string) string {
	return filepath.Join(r.ClanDataDir(clanID), "contracts.db")
}

// GraveyardDir returns $XDG_DATA_HOME/civquest/graveyard.
func (r *Resolver) GraveyardDir() string {
	return filepath.Join(r.DataDir(), "graveyard")
}

// BinDir returns $XDG_DATA_HOME/civquest/bin.
func (r *Resolver) BinDir() string {
	return filepath.Join(r.DataDir(), "bin")
}

// AgentsManifestPath returns $XDG_DATA_HOME/civquest/civquest.agents.json.
func (r *Resolver) AgentsManifestPath() string {
	return filepath.Join(r.DataDir(), "civquest.agents.json")
}

// ServerDir returns $XDG_DATA_HOME/civquest/server.
func (r *Resolver) ServerDir() string {
	return filepath.Join(r.DataDir(), "server")
}

// ServerModsDir returns $XDG_DATA_HOME/civquest/server/mods.
func (r *Resolver) ServerModsDir() string {
	return filepath.Join(r.ServerDir(), "mods")
}

// ServerManifestPath returns $XDG_DATA_HOME/civquest/server/civquest.server.json.
func (r *Resolver) ServerManifestPath() string {
	return filepath.Join(r.ServerDir(), "civquest.server.json")
}

// ServerRunScriptPath returns $XDG_DATA_HOME/civquest/server/run.sh.
func (r *Resolver) ServerRunScriptPath() string {
	return filepath.Join(r.ServerDir(), "run.sh")
}

// ServerPropertiesPath returns $XDG_DATA_HOME/civquest/server/server.properties.
func (r *Resolver) ServerPropertiesPath() string {
	return filepath.Join(r.ServerDir(), "server.properties")
}

// ServerEULAPath returns $XDG_DATA_HOME/civquest/server/eula.txt.
func (r *Resolver) ServerEULAPath() string {
	return filepath.Join(r.ServerDir(), "eula.txt")
}

// GraveyardAgentDir returns $XDG_DATA_HOME/civquest/graveyard/{agent_name}_{uuid}_tick{tick}.
func (r *Resolver) GraveyardAgentDir(agentName, uuid string, tick int64) (string, error) {
	if err := validateIdentifier(agentName); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidAgentName, err)
	}
	if err := validateGraveyardUUID(uuid); err != nil {
		return "", err
	}
	dirName := fmt.Sprintf("%s_%s_tick%d", agentName, uuid, tick)
	return filepath.Join(r.GraveyardDir(), dirName), nil
}

// GraveyardMemoryDBPath returns the archived memory database path for a deceased agent.
func (r *Resolver) GraveyardMemoryDBPath(agentName, uuid string, tick int64) (string, error) {
	dir, err := r.GraveyardAgentDir(agentName, uuid, tick)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "memory.db"), nil
}

// GraveyardDeathCertificatePath returns the death certificate JSON path for a deceased agent.
func (r *Resolver) GraveyardDeathCertificatePath(agentName, uuid string, tick int64) (string, error) {
	dir, err := r.GraveyardAgentDir(agentName, uuid, tick)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "death_certificate.json"), nil
}

// GraveyardEpitaphPath returns the epitaph text path for a deceased agent.
func (r *Resolver) GraveyardEpitaphPath(agentName, uuid string, tick int64) (string, error) {
	dir, err := r.GraveyardAgentDir(agentName, uuid, tick)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "epitaph.txt"), nil
}

// ==================== STATE PATHS ====================

// AgentsStateDir returns $XDG_STATE_HOME/civquest/agents.
func (r *Resolver) AgentsStateDir() string {
	return filepath.Join(r.StateDir(), "agents")
}

// AgentStateDir returns $XDG_STATE_HOME/civquest/agents/{agent_name}.
func (r *Resolver) AgentStateDir(agentName string) string {
	return filepath.Join(r.AgentsStateDir(), agentName)
}

// AgentLogPath returns $XDG_STATE_HOME/civquest/agents/{agent_name}/{agent_name}.log.
func (r *Resolver) AgentLogPath(agentName string) string {
	return filepath.Join(r.AgentStateDir(agentName), fmt.Sprintf("%s.log", agentName))
}

// AgentSocketPath returns $XDG_STATE_HOME/civquest/agents/{agent_name}.sock.
func (r *Resolver) AgentSocketPath(agentName string) string {
	return filepath.Join(r.AgentsStateDir(), fmt.Sprintf("%s.sock", agentName))
}

// SupervisorPIDPath returns $XDG_STATE_HOME/civquest/supervisor.pid.
func (r *Resolver) SupervisorPIDPath() string {
	return filepath.Join(r.StateDir(), "supervisor.pid")
}

// ServerPIDPath returns $XDG_STATE_HOME/civquest/server.pid.
func (r *Resolver) ServerPIDPath() string {
	return filepath.Join(r.StateDir(), "server.pid")
}

// ServerLogPath returns $XDG_STATE_HOME/civquest/server.log.
func (r *Resolver) ServerLogPath() string {
	return filepath.Join(r.StateDir(), "server.log")
}

// ==================== DIRECTORY INITIALIZATION ====================

// InitDirectories creates the standard CivQuest directory skeleton with 0750 permissions,
// and the runtime directory with strict 0700 permissions per XDG specification.
func (r *Resolver) InitDirectories() error {
	dirs := []string{
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

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// RuntimeDir must have mode 0700 per XDG specification
	if err := os.MkdirAll(r.RuntimeDir(), 0700); err != nil {
		return fmt.Errorf("failed to create runtime directory %s: %w", r.RuntimeDir(), err)
	}
	if err := os.Chmod(r.RuntimeDir(), 0700); err != nil {
		return fmt.Errorf("failed to chmod runtime directory %s: %w", r.RuntimeDir(), err)
	}

	return nil
}

// InitAgentDirectories creates data, texture, and state directories for a specific agent.
func (r *Resolver) InitAgentDirectories(agentName string) error {
	if err := validateIdentifier(agentName); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidAgentName, err)
	}
	dirs := []string{
		r.AgentDataDir(agentName),
		r.AgentTexturesDir(agentName),
		r.AgentStateDir(agentName),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create agent directory %s: %w", dir, err)
		}
	}
	return nil
}

// InitServerDirectories creates server and mods directories with 0750 permissions.
func (r *Resolver) InitServerDirectories() error {
	dirs := []string{
		r.ServerDir(),
		r.ServerModsDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create server directory %s: %w", dir, err)
		}
	}
	return nil
}

// InitClanDirectories creates data directories for a specific clan.
func (r *Resolver) InitClanDirectories(clanID string) error {
	if err := validateIdentifier(clanID); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidClanID, err)
	}
	dir := r.ClanDataDir(clanID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create clan directory %s: %w", dir, err)
	}
	return nil
}

func validateIdentifier(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("identifier cannot be empty")
	}
	if trimmed == "." || trimmed == ".." {
		return errors.New("identifier cannot be '.' or '..'")
	}
	if len(id) > 32 {
		return fmt.Errorf("identifier %q exceeds maximum length of 32 characters", id)
	}
	if !validIdentifierRegex.MatchString(id) {
		return fmt.Errorf("identifier %q contains invalid characters or does not match allowlist", id)
	}
	return nil
}

func validateGraveyardUUID(uuid string) error {
	trimmed := strings.TrimSpace(uuid)
	if trimmed == "" {
		return fmt.Errorf("%w: uuid cannot be empty", ErrInvalidUUID)
	}
	if trimmed == "." || trimmed == ".." {
		return fmt.Errorf("%w: uuid cannot be '.' or '..'", ErrInvalidUUID)
	}
	if strings.ContainsAny(trimmed, `/\:*?"<>|`) || strings.Contains(trimmed, "..") {
		return fmt.Errorf("%w: uuid %q contains invalid characters or path traversal elements", ErrInvalidUUID, uuid)
	}
	return nil
}

// ==================== DEFAULT RESOLVER & CONVENIENCE FUNCTIONS ====================

var defaultResolver = NewResolver()

// DefaultResolver returns the package-level default Resolver.
func DefaultResolver() *Resolver {
	return defaultResolver
}

// ConfigDir returns $XDG_CONFIG_HOME/civquest via the default resolver.
func ConfigDir() string { return defaultResolver.ConfigDir() }

// DataDir returns $XDG_DATA_HOME/civquest via the default resolver.
func DataDir() string { return defaultResolver.DataDir() }

// StateDir returns $XDG_STATE_HOME/civquest via the default resolver.
func StateDir() string { return defaultResolver.StateDir() }

// CacheDir returns $XDG_CACHE_HOME/civquest via the default resolver.
func CacheDir() string { return defaultResolver.CacheDir() }

// RuntimeDir returns $XDG_RUNTIME_DIR/civquest via the default resolver.
func RuntimeDir() string { return defaultResolver.RuntimeDir() }

// GlobalConfigPath returns $XDG_CONFIG_HOME/civquest/global.yaml via the default resolver.
func GlobalConfigPath() string { return defaultResolver.GlobalConfigPath() }

// AgentsConfigDir returns $XDG_CONFIG_HOME/civquest/agents via the default resolver.
func AgentsConfigDir() string { return defaultResolver.AgentsConfigDir() }

// AgentConfigPath returns $XDG_CONFIG_HOME/civquest/agents/{agent_name}.yaml via the default resolver.
func AgentConfigPath(agentName string) string { return defaultResolver.AgentConfigPath(agentName) }

// BlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints via the default resolver.
func BlueprintsDir() string { return defaultResolver.BlueprintsDir() }

// OfficialBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/official via the default resolver.
func OfficialBlueprintsDir() string { return defaultResolver.OfficialBlueprintsDir() }

// PlayerImportedBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/player_imported via the default resolver.
func PlayerImportedBlueprintsDir() string { return defaultResolver.PlayerImportedBlueprintsDir() }

// LLMGeneratedBlueprintsDir returns $XDG_CONFIG_HOME/civquest/blueprints/llm_generated via the default resolver.
func LLMGeneratedBlueprintsDir() string { return defaultResolver.LLMGeneratedBlueprintsDir() }

// AgentDataDir returns $XDG_DATA_HOME/civquest/agents/{agent_name} via the default resolver.
func AgentDataDir(agentName string) string { return defaultResolver.AgentDataDir(agentName) }

// AgentMemoryDBPath returns memory.db path for agentName via the default resolver.
func AgentMemoryDBPath(agentName string) string { return defaultResolver.AgentMemoryDBPath(agentName) }

// AgentMapGraphDBPath returns map_graph.db path for agentName via the default resolver.
func AgentMapGraphDBPath(agentName string) string {
	return defaultResolver.AgentMapGraphDBPath(agentName)
}

// AgentInventoryCachePath returns inventory_cache.json path for agentName via the default resolver.
func AgentInventoryCachePath(agentName string) string {
	return defaultResolver.AgentInventoryCachePath(agentName)
}

// AgentTexturesDir returns textures dir for agentName via the default resolver.
func AgentTexturesDir(agentName string) string { return defaultResolver.AgentTexturesDir(agentName) }

// AgentSkinPath returns skin.png path for agentName via the default resolver.
func AgentSkinPath(agentName string) string { return defaultResolver.AgentSkinPath(agentName) }

// ClanDataDir returns $XDG_DATA_HOME/civquest/clans/{clan_id} via the default resolver.
func ClanDataDir(clanID string) string { return defaultResolver.ClanDataDir(clanID) }

// ClanCivicRegistryDBPath returns civic_registry.db path for clanID via the default resolver.
func ClanCivicRegistryDBPath(clanID string) string {
	return defaultResolver.ClanCivicRegistryDBPath(clanID)
}

// ClanReputationDBPath returns reputation.db path for clanID via the default resolver.
func ClanReputationDBPath(clanID string) string {
	return defaultResolver.ClanReputationDBPath(clanID)
}

// ClanDiplomacyDBPath returns diplomacy.db path for clanID via the default resolver.
func ClanDiplomacyDBPath(clanID string) string {
	return defaultResolver.ClanDiplomacyDBPath(clanID)
}

// ClanLandmarksDBPath returns landmarks.db path for clanID via the default resolver.
func ClanLandmarksDBPath(clanID string) string {
	return defaultResolver.ClanLandmarksDBPath(clanID)
}

// ClanRumorsDBPath returns rumors.db path for clanID via the default resolver.
func ClanRumorsDBPath(clanID string) string {
	return defaultResolver.ClanRumorsDBPath(clanID)
}

// ClanContractsDBPath returns contracts.db path for clanID via the default resolver.
func ClanContractsDBPath(clanID string) string {
	return defaultResolver.ClanContractsDBPath(clanID)
}

// GraveyardDir returns $XDG_DATA_HOME/civquest/graveyard via the default resolver.
func GraveyardDir() string { return defaultResolver.GraveyardDir() }

// GraveyardAgentDir returns graveyard folder for agent via the default resolver.
func GraveyardAgentDir(agentName, uuid string, tick int64) (string, error) {
	return defaultResolver.GraveyardAgentDir(agentName, uuid, tick)
}

// GraveyardMemoryDBPath returns memory.db path for deceased agent via the default resolver.
func GraveyardMemoryDBPath(agentName, uuid string, tick int64) (string, error) {
	return defaultResolver.GraveyardMemoryDBPath(agentName, uuid, tick)
}

// GraveyardDeathCertificatePath returns death certificate path for deceased agent via the default resolver.
func GraveyardDeathCertificatePath(agentName, uuid string, tick int64) (string, error) {
	return defaultResolver.GraveyardDeathCertificatePath(agentName, uuid, tick)
}

// GraveyardEpitaphPath returns epitaph path for deceased agent via the default resolver.
func GraveyardEpitaphPath(agentName, uuid string, tick int64) (string, error) {
	return defaultResolver.GraveyardEpitaphPath(agentName, uuid, tick)
}

// AgentStateDir returns $XDG_STATE_HOME/civquest/agents/{agent_name} via the default resolver.
func AgentStateDir(agentName string) string { return defaultResolver.AgentStateDir(agentName) }

// AgentLogPath returns {agent_name}.log path via the default resolver.
func AgentLogPath(agentName string) string { return defaultResolver.AgentLogPath(agentName) }

// AgentSocketPath returns {agent_name}.sock path via the default resolver.
func AgentSocketPath(agentName string) string { return defaultResolver.AgentSocketPath(agentName) }

// SupervisorPIDPath returns supervisor.pid path via the default resolver.
func SupervisorPIDPath() string { return defaultResolver.SupervisorPIDPath() }

// BinDir returns $XDG_DATA_HOME/civquest/bin via the default resolver.
func BinDir() string { return defaultResolver.BinDir() }

// AgentsManifestPath returns civquest.agents.json path via the default resolver.
func AgentsManifestPath() string { return defaultResolver.AgentsManifestPath() }

// ServerDir returns $XDG_DATA_HOME/civquest/server via the default resolver.
func ServerDir() string { return defaultResolver.ServerDir() }

// ServerModsDir returns $XDG_DATA_HOME/civquest/server/mods via the default resolver.
func ServerModsDir() string { return defaultResolver.ServerModsDir() }

// ServerManifestPath returns civquest.server.json path via the default resolver.
func ServerManifestPath() string { return defaultResolver.ServerManifestPath() }

// ServerRunScriptPath returns run.sh path via the default resolver.
func ServerRunScriptPath() string { return defaultResolver.ServerRunScriptPath() }

// ServerPropertiesPath returns server.properties path via the default resolver.
func ServerPropertiesPath() string { return defaultResolver.ServerPropertiesPath() }

// ServerEULAPath returns eula.txt path via the default resolver.
func ServerEULAPath() string { return defaultResolver.ServerEULAPath() }

// ServerPIDPath returns server.pid path via the default resolver.
func ServerPIDPath() string { return defaultResolver.ServerPIDPath() }

// ServerLogPath returns server.log path via the default resolver.
func ServerLogPath() string { return defaultResolver.ServerLogPath() }

// InitDirectories initializes the standard directory skeleton via the default resolver.
func InitDirectories() error { return defaultResolver.InitDirectories() }

// InitServerDirectories initializes server directories via the default resolver.
func InitServerDirectories() error { return defaultResolver.InitServerDirectories() }

// InitAgentDirectories initializes directories for an agent via the default resolver.
func InitAgentDirectories(agentName string) error {
	return defaultResolver.InitAgentDirectories(agentName)
}

// InitClanDirectories initializes directories for a clan via the default resolver.
func InitClanDirectories(clanID string) error { return defaultResolver.InitClanDirectories(clanID) }
