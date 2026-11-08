package xdg

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sentinel errors for config operations.
var (
	ErrNilConfig          = errors.New("configuration cannot be nil")
	ErrTraitOutOfBounds   = errors.New("personality trait out of bounds [0.0, 1.0]")
	ErrInvalidSkinModel   = errors.New("skin model must be 'default' or 'slim'")
	ErrInvalidSkinTexture = errors.New("invalid skin texture path")
	ErrInvalidGRPCAddress = errors.New("invalid gRPC host or port")
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ==================== GLOBAL CONFIG ====================

// GlobalConfig specifies system-wide settings for LLM inference, gRPC networking, and supervisor policies.
type GlobalConfig struct {
	InferenceGovernor InferenceGovernorConfig `yaml:"inference_governor"`
	LLMGateway        LLMGatewayConfig        `yaml:"llm_gateway"`
	GRPC              GRPCConfig              `yaml:"grpc"`
	Supervisor        SupervisorConfig        `yaml:"supervisor"`
}

// InferenceGovernorConfig controls concurrency and timeouts for System 2 LLM inference pipelines.
type InferenceGovernorConfig struct {
	MaxConcurrentLLMRequests int `yaml:"max_concurrent_llm_requests"`
	RequestTimeoutSeconds    int `yaml:"request_timeout_seconds"`
}

// LLMGatewayConfig specifies the OpenAI-compatible endpoint, model names, and dual temperatures.
type LLMGatewayConfig struct {
	BaseURL               string  `yaml:"base_url"`
	APIKey                string  `yaml:"api_key,omitempty"`
	Model                 string  `yaml:"model"`
	EmbeddingModel        string  `yaml:"embedding_model"`
	StructuredTemperature float64 `yaml:"structured_temperature"`
	CreativeTemperature   float64 `yaml:"creative_temperature"`
}

// GRPCConfig holds connection parameters for the Netty gRPC server hosted in the NeoForge mod.
type GRPCConfig struct {
	Host                  string `yaml:"host"`
	Port                  int    `yaml:"port"`
	ConnectTimeoutSeconds int    `yaml:"connect_timeout_seconds"`
}

// Address returns the formatted host:port string.
func (g GRPCConfig) Address() string {
	return fmt.Sprintf("%s:%d", g.Host, g.Port)
}

// SupervisorConfig contains lifecycle and dynastic reproduction policies for cq.
type SupervisorConfig struct {
	MaxActiveAgents    int  `yaml:"max_active_agents"`
	MinFoodSurplusDays int  `yaml:"min_food_surplus_days"`
	EnableAutoRestart  bool `yaml:"enable_auto_restart"`
}

// DefaultGlobalConfig returns standard default configurations per ADR specifications.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		InferenceGovernor: InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 4,
			RequestTimeoutSeconds:    30,
		},
		LLMGateway: LLMGatewayConfig{
			BaseURL:               "http://127.0.0.1:11434/v1",
			APIKey:                "",
			Model:                 "llama3.1",
			EmbeddingModel:        "nomic-embed-text",
			StructuredTemperature: 0.20,
			CreativeTemperature:   0.85,
		},
		GRPC: GRPCConfig{
			Host:                  "127.0.0.1",
			Port:                  50051,
			ConnectTimeoutSeconds: 10,
		},
		Supervisor: SupervisorConfig{
			MaxActiveAgents:    16,
			MinFoodSurplusDays: 7,
			EnableAutoRestart:  true,
		},
	}
}

// Validate checks that all required GlobalConfig fields are populated and within valid ranges.
func (c *GlobalConfig) Validate() error {
	if c == nil {
		return ErrNilConfig
	}
	if c.InferenceGovernor.MaxConcurrentLLMRequests <= 0 {
		return errors.New("inference_governor.max_concurrent_llm_requests must be positive")
	}
	if c.InferenceGovernor.RequestTimeoutSeconds <= 0 {
		return errors.New("inference_governor.request_timeout_seconds must be positive")
	}
	if strings.TrimSpace(c.LLMGateway.BaseURL) == "" {
		return errors.New("llm_gateway.base_url must not be empty")
	}
	if strings.TrimSpace(c.LLMGateway.Model) == "" {
		return errors.New("llm_gateway.model must not be empty")
	}
	if strings.TrimSpace(c.LLMGateway.EmbeddingModel) == "" {
		return errors.New("llm_gateway.embedding_model must not be empty")
	}
	if math.IsNaN(c.LLMGateway.StructuredTemperature) || c.LLMGateway.StructuredTemperature < 0.0 || c.LLMGateway.StructuredTemperature > 2.0 {
		return fmt.Errorf("llm_gateway.structured_temperature %f out of range [0.0, 2.0]", c.LLMGateway.StructuredTemperature)
	}
	if math.IsNaN(c.LLMGateway.CreativeTemperature) || c.LLMGateway.CreativeTemperature < 0.0 || c.LLMGateway.CreativeTemperature > 2.0 {
		return fmt.Errorf("llm_gateway.creative_temperature %f out of range [0.0, 2.0]", c.LLMGateway.CreativeTemperature)
	}
	if strings.TrimSpace(c.GRPC.Host) == "" {
		return errors.New("grpc.host must not be empty")
	}
	if c.GRPC.Port <= 0 || c.GRPC.Port > 65535 {
		return fmt.Errorf("%w: port %d outside [1, 65535]", ErrInvalidGRPCAddress, c.GRPC.Port)
	}
	if c.GRPC.ConnectTimeoutSeconds <= 0 {
		return errors.New("grpc.connect_timeout_seconds must be positive")
	}
	if c.Supervisor.MaxActiveAgents <= 0 {
		return errors.New("supervisor.max_active_agents must be positive")
	}
	if c.Supervisor.MinFoodSurplusDays < 0 {
		return errors.New("supervisor.min_food_surplus_days cannot be negative")
	}
	return nil
}

// ==================== AGENT IDENTITY CONFIG ====================

// AgentIdentityConfig holds the identity, personality, and skin configuration for an agent.
type AgentIdentityConfig struct {
	Name           string          `yaml:"name"`
	UUID           string          `yaml:"uuid"`
	ClanID         string          `yaml:"clan_id"`
	Profession     string          `yaml:"profession"`
	SecondaryHobby string          `yaml:"secondary_hobby,omitempty"`
	SystemPrompt   string          `yaml:"system_prompt,omitempty"`
	Traits         AgentTraits     `yaml:"traits"`
	Skin           AgentSkinConfig `yaml:"skin"`
}

// AgentTraits contains the 5 continuous personality dimensions bounded in [0.0, 1.0].
type AgentTraits struct {
	Bravery         float64 `yaml:"bravery"`
	Industriousness float64 `yaml:"industriousness"`
	Sociability     float64 `yaml:"sociability"`
	Curiosity       float64 `yaml:"curiosity"`
	Aggressiveness  float64 `yaml:"aggressiveness"`
}

// Validate checks that each trait falls within the [0.0, 1.0] interval.
func (t *AgentTraits) Validate() error {
	check := func(name string, val float64) error {
		if math.IsNaN(val) || val < 0.0 || val > 1.0 {
			return fmt.Errorf("%w: %s has value %f", ErrTraitOutOfBounds, name, val)
		}
		return nil
	}
	if err := check("bravery", t.Bravery); err != nil {
		return err
	}
	if err := check("industriousness", t.Industriousness); err != nil {
		return err
	}
	if err := check("sociability", t.Sociability); err != nil {
		return err
	}
	if err := check("curiosity", t.Curiosity); err != nil {
		return err
	}
	if err := check("aggressiveness", t.Aggressiveness); err != nil {
		return err
	}
	return nil
}

// AgentSkinConfig specifies the skin texture path and avatar model variant.
type AgentSkinConfig struct {
	TexturePath string `yaml:"texture_path,omitempty"`
	Model       string `yaml:"model,omitempty"` // "default" (Steve) or "slim" (Alex)
}

// DefaultAgentIdentityConfig returns a sensible default identity configuration for an agent.
func DefaultAgentIdentityConfig(name string) *AgentIdentityConfig {
	return &AgentIdentityConfig{
		Name:           name,
		UUID:           "",
		ClanID:         "pioneers",
		Profession:     "UNASSIGNED",
		SecondaryHobby: "NONE",
		SystemPrompt:   "",
		Traits: AgentTraits{
			Bravery:         0.5,
			Industriousness: 0.5,
			Sociability:     0.5,
			Curiosity:       0.5,
			Aggressiveness:  0.5,
		},
		Skin: AgentSkinConfig{
			Model: "default",
		},
	}
}

// Validate checks that all required AgentIdentityConfig fields are populated and valid.
func (c *AgentIdentityConfig) Validate() error {
	if c == nil {
		return ErrNilConfig
	}
	if err := validateIdentifier(c.Name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidAgentName, err)
	}
	if err := validateIdentifier(c.ClanID); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidClanID, err)
	}
	if c.UUID != "" {
		if !uuidRegex.MatchString(c.UUID) {
			return fmt.Errorf("%w: %q does not match canonical UUID format", ErrInvalidUUID, c.UUID)
		}
	}
	if err := c.Traits.Validate(); err != nil {
		return fmt.Errorf("traits validation failed for agent %q: %w", c.Name, err)
	}

	// Validate skin texture path if provided
	if c.Skin.TexturePath != "" {
		if strings.Contains(c.Skin.TexturePath, "..") {
			return fmt.Errorf("%w: path traversal sequence detected in %q", ErrInvalidSkinTexture, c.Skin.TexturePath)
		}
		if !strings.HasSuffix(strings.ToLower(c.Skin.TexturePath), ".png") {
			return fmt.Errorf("%w: texture must have .png extension: %q", ErrInvalidSkinTexture, c.Skin.TexturePath)
		}
	}

	// Normalize skin model
	model := strings.ToLower(strings.TrimSpace(c.Skin.Model))
	switch model {
	case "", "default", "steve":
		c.Skin.Model = "default"
	case "slim", "alex":
		c.Skin.Model = "slim"
	default:
		return fmt.Errorf("%w: got %q", ErrInvalidSkinModel, c.Skin.Model)
	}
	return nil
}

// ==================== LOADERS & WRITERS ====================

// LoadGlobalConfig loads and validates a GlobalConfig from the specified file path.
// If path is empty, it uses the package default GlobalConfigPath().
// Unspecified fields retain defaults from DefaultGlobalConfig().
func LoadGlobalConfig(path string) (*GlobalConfig, error) {
	if path == "" {
		path = GlobalConfigPath()
	}
	cfg := DefaultGlobalConfig()
	if err := readYAMLFile(path, cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("global config validation error: %w", err)
	}
	return cfg, nil
}

// SaveGlobalConfig validates and writes a GlobalConfig to the specified file path in YAML format.
// If path is empty, it uses the package default GlobalConfigPath().
func SaveGlobalConfig(cfg *GlobalConfig, path string) error {
	if cfg == nil {
		return ErrNilConfig
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("global config validation error: %w", err)
	}
	if path == "" {
		path = GlobalConfigPath()
	}
	return writeYAMLFile(path, cfg)
}

// LoadAgentConfig loads and validates an AgentIdentityConfig from the specified file path.
func LoadAgentConfig(path string) (*AgentIdentityConfig, error) {
	if path == "" {
		return nil, errors.New("agent config path cannot be empty")
	}
	cfg := &AgentIdentityConfig{}
	if err := readYAMLFile(path, cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("agent config validation error: %w", err)
	}
	return cfg, nil
}

// SaveAgentConfig validates and writes an AgentIdentityConfig to the specified file path in YAML format.
// If path is empty, it uses the default AgentConfigPath(cfg.Name).
func SaveAgentConfig(cfg *AgentIdentityConfig, path string) error {
	if cfg == nil {
		return ErrNilConfig
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("agent config validation error: %w", err)
	}
	if path == "" {
		path = AgentConfigPath(cfg.Name)
	}
	return writeYAMLFile(path, cfg)
}

// LoadAgentConfigByName loads an agent config by name from the standard config directory.
func LoadAgentConfigByName(agentName string) (*AgentIdentityConfig, error) {
	if err := validateIdentifier(agentName); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAgentName, err)
	}
	return LoadAgentConfig(AgentConfigPath(agentName))
}

// SaveAgentConfigByName saves an agent config by name to the standard config directory.
func SaveAgentConfigByName(cfg *AgentIdentityConfig) error {
	if cfg == nil {
		return ErrNilConfig
	}
	return SaveAgentConfig(cfg, AgentConfigPath(cfg.Name))
}

// ==================== RESOLVER CONFIG METHODS ====================

// LoadGlobalConfig loads global config using the resolver's configured paths.
func (r *Resolver) LoadGlobalConfig() (*GlobalConfig, error) {
	return LoadGlobalConfig(r.GlobalConfigPath())
}

// SaveGlobalConfig saves global config using the resolver's configured paths.
func (r *Resolver) SaveGlobalConfig(cfg *GlobalConfig) error {
	return SaveGlobalConfig(cfg, r.GlobalConfigPath())
}

// LoadAgentConfig loads agent identity config using the resolver's configured paths.
func (r *Resolver) LoadAgentConfig(agentName string) (*AgentIdentityConfig, error) {
	if err := validateIdentifier(agentName); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAgentName, err)
	}
	return LoadAgentConfig(r.AgentConfigPath(agentName))
}

// SaveAgentConfig saves agent identity config using the resolver's configured paths.
func (r *Resolver) SaveAgentConfig(cfg *AgentIdentityConfig) error {
	if cfg == nil {
		return ErrNilConfig
	}
	return SaveAgentConfig(cfg, r.AgentConfigPath(cfg.Name))
}

// ==================== INTERNAL I/O HELPERS ====================

func readYAMLFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to unmarshal yaml from %s: %w", path, err)
	}
	return nil
}

func writeYAMLFile(path string, data any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal yaml: %w", err)
	}

	// Write to a temporary file in the same directory then rename atomically
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.Write(out); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to replace destination file %s: %w", path, err)
	}

	return nil
}
