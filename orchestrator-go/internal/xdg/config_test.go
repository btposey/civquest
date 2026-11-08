package xdg_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestDefaultGlobalConfig(t *testing.T) {
	cfg := xdg.DefaultGlobalConfig()
	if cfg == nil {
		t.Fatal("DefaultGlobalConfig() must not be nil")
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultGlobalConfig() failed validation: %v", err)
	}

	if cfg.InferenceGovernor.MaxConcurrentLLMRequests != 4 {
		t.Errorf("MaxConcurrentLLMRequests = %d, want 4", cfg.InferenceGovernor.MaxConcurrentLLMRequests)
	}
	if cfg.InferenceGovernor.RequestTimeoutSeconds != 30 {
		t.Errorf("RequestTimeoutSeconds = %d, want 30", cfg.InferenceGovernor.RequestTimeoutSeconds)
	}
	if cfg.LLMGateway.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("BaseURL = %q, want http://127.0.0.1:11434/v1", cfg.LLMGateway.BaseURL)
	}
	if cfg.LLMGateway.Model != "llama3.1" {
		t.Errorf("Model = %q, want llama3.1", cfg.LLMGateway.Model)
	}
	if cfg.LLMGateway.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("EmbeddingModel = %q, want nomic-embed-text", cfg.LLMGateway.EmbeddingModel)
	}
	if cfg.LLMGateway.StructuredTemperature != 0.20 {
		t.Errorf("StructuredTemperature = %f, want 0.20", cfg.LLMGateway.StructuredTemperature)
	}
	if cfg.LLMGateway.CreativeTemperature != 0.85 {
		t.Errorf("CreativeTemperature = %f, want 0.85", cfg.LLMGateway.CreativeTemperature)
	}
	if cfg.GRPC.Host != "127.0.0.1" || cfg.GRPC.Port != 50051 {
		t.Errorf("GRPC host:port = %s:%d, want 127.0.0.1:50051", cfg.GRPC.Host, cfg.GRPC.Port)
	}
	if got := cfg.GRPC.Address(); got != "127.0.0.1:50051" {
		t.Errorf("Address() = %q, want 127.0.0.1:50051", got)
	}
	if cfg.Supervisor.MaxActiveAgents != 16 {
		t.Errorf("MaxActiveAgents = %d, want 16", cfg.Supervisor.MaxActiveAgents)
	}
	if cfg.Supervisor.MinFoodSurplusDays != 7 {
		t.Errorf("MinFoodSurplusDays = %d, want 7", cfg.Supervisor.MinFoodSurplusDays)
	}
	if !cfg.Supervisor.EnableAutoRestart {
		t.Errorf("EnableAutoRestart = false, want true")
	}
}

func TestGlobalConfigRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "custom_global.yaml")

	original := &xdg.GlobalConfig{
		InferenceGovernor: xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 8,
			RequestTimeoutSeconds:    45,
		},
		LLMGateway: xdg.LLMGatewayConfig{
			BaseURL:               "http://localhost:8000/v1",
			APIKey:                "secret-key",
			Model:                 "mistral-nemo",
			EmbeddingModel:        "bge-large-en-v1.5",
			StructuredTemperature: 0.15,
			CreativeTemperature:   0.90,
		},
		GRPC: xdg.GRPCConfig{
			Host:                  "10.0.0.5",
			Port:                  50055,
			ConnectTimeoutSeconds: 15,
		},
		Supervisor: xdg.SupervisorConfig{
			MaxActiveAgents:    32,
			MinFoodSurplusDays: 14,
			EnableAutoRestart:  false,
		},
	}

	if err := xdg.SaveGlobalConfig(original, filePath); err != nil {
		t.Fatalf("SaveGlobalConfig failed: %v", err)
	}

	loaded, err := xdg.LoadGlobalConfig(filePath)
	if err != nil {
		t.Fatalf("LoadGlobalConfig failed: %v", err)
	}

	if loaded.InferenceGovernor != original.InferenceGovernor {
		t.Errorf("InferenceGovernor mismatch: got %+v, want %+v", loaded.InferenceGovernor, original.InferenceGovernor)
	}
	if loaded.LLMGateway != original.LLMGateway {
		t.Errorf("LLMGateway mismatch: got %+v, want %+v", loaded.LLMGateway, original.LLMGateway)
	}
	if loaded.GRPC != original.GRPC {
		t.Errorf("GRPC mismatch: got %+v, want %+v", loaded.GRPC, original.GRPC)
	}
	if loaded.Supervisor != original.Supervisor {
		t.Errorf("Supervisor mismatch: got %+v, want %+v", loaded.Supervisor, original.Supervisor)
	}
}

func TestGlobalConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*xdg.GlobalConfig)
		wantErr bool
	}{
		{
			name:    "valid default",
			modify:  func(c *xdg.GlobalConfig) {},
			wantErr: false,
		},
		{
			name: "zero max concurrent",
			modify: func(c *xdg.GlobalConfig) {
				c.InferenceGovernor.MaxConcurrentLLMRequests = 0
			},
			wantErr: true,
		},
		{
			name: "negative timeout",
			modify: func(c *xdg.GlobalConfig) {
				c.InferenceGovernor.RequestTimeoutSeconds = -5
			},
			wantErr: true,
		},
		{
			name: "empty base url",
			modify: func(c *xdg.GlobalConfig) {
				c.LLMGateway.BaseURL = "  "
			},
			wantErr: true,
		},
		{
			name: "empty model",
			modify: func(c *xdg.GlobalConfig) {
				c.LLMGateway.Model = ""
			},
			wantErr: true,
		},
		{
			name: "empty embedding model",
			modify: func(c *xdg.GlobalConfig) {
				c.LLMGateway.EmbeddingModel = ""
			},
			wantErr: true,
		},
		{
			name: "invalid structured temperature high",
			modify: func(c *xdg.GlobalConfig) {
				c.LLMGateway.StructuredTemperature = 2.5
			},
			wantErr: true,
		},
		{
			name: "invalid creative temperature low",
			modify: func(c *xdg.GlobalConfig) {
				c.LLMGateway.CreativeTemperature = -0.1
			},
			wantErr: true,
		},
		{
			name: "empty grpc host",
			modify: func(c *xdg.GlobalConfig) {
				c.GRPC.Host = ""
			},
			wantErr: true,
		},
		{
			name: "invalid grpc port",
			modify: func(c *xdg.GlobalConfig) {
				c.GRPC.Port = 70000
			},
			wantErr: true,
		},
		{
			name: "negative connect timeout",
			modify: func(c *xdg.GlobalConfig) {
				c.GRPC.ConnectTimeoutSeconds = 0
			},
			wantErr: true,
		},
		{
			name: "zero max active agents",
			modify: func(c *xdg.GlobalConfig) {
				c.Supervisor.MaxActiveAgents = 0
			},
			wantErr: true,
		},
		{
			name: "negative min food surplus days",
			modify: func(c *xdg.GlobalConfig) {
				c.Supervisor.MinFoodSurplusDays = -1
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := xdg.DefaultGlobalConfig()
			tc.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}

	var nilCfg *xdg.GlobalConfig
	if err := nilCfg.Validate(); err == nil {
		t.Errorf("expected error when validating nil config, got nil")
	}
}

func TestAgentIdentityConfigRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "aria.yaml")

	agent := &xdg.AgentIdentityConfig{
		Name:           "Aria",
		UUID:           "d9d06b5a-19fb-4395-9ff7-320c2423aef5",
		ClanID:         "clan_ironwood",
		Profession:     "ARCHITECT",
		SecondaryHobby: "APIARIST",
		SystemPrompt:   "You are Aria, master builder of Ironwood.",
		Traits: xdg.AgentTraits{
			Bravery:         0.85,
			Industriousness: 0.95,
			Sociability:     0.70,
			Curiosity:       0.80,
			Aggressiveness:  0.20,
		},
		Skin: xdg.AgentSkinConfig{
			TexturePath: "textures/aria.png",
			Model:       "slim",
		},
	}

	if err := xdg.SaveAgentConfig(agent, filePath); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	loaded, err := xdg.LoadAgentConfig(filePath)
	if err != nil {
		t.Fatalf("LoadAgentConfig failed: %v", err)
	}

	if loaded.Name != agent.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, agent.Name)
	}
	if loaded.UUID != agent.UUID {
		t.Errorf("UUID = %q, want %q", loaded.UUID, agent.UUID)
	}
	if loaded.ClanID != agent.ClanID {
		t.Errorf("ClanID = %q, want %q", loaded.ClanID, agent.ClanID)
	}
	if loaded.Profession != agent.Profession {
		t.Errorf("Profession = %q, want %q", loaded.Profession, agent.Profession)
	}
	if loaded.SecondaryHobby != agent.SecondaryHobby {
		t.Errorf("SecondaryHobby = %q, want %q", loaded.SecondaryHobby, agent.SecondaryHobby)
	}
	if loaded.SystemPrompt != agent.SystemPrompt {
		t.Errorf("SystemPrompt = %q, want %q", loaded.SystemPrompt, agent.SystemPrompt)
	}
	if loaded.Traits != agent.Traits {
		t.Errorf("Traits = %+v, want %+v", loaded.Traits, agent.Traits)
	}
	if loaded.Skin != agent.Skin {
		t.Errorf("Skin = %+v, want %+v", loaded.Skin, agent.Skin)
	}
}

func TestAgentTraitsValidation(t *testing.T) {
	tests := []struct {
		name    string
		traits  xdg.AgentTraits
		wantErr bool
	}{
		{
			name: "all valid middle",
			traits: xdg.AgentTraits{
				Bravery:         0.5,
				Industriousness: 0.5,
				Sociability:     0.5,
				Curiosity:       0.5,
				Aggressiveness:  0.5,
			},
			wantErr: false,
		},
		{
			name: "boundary zeros and ones",
			traits: xdg.AgentTraits{
				Bravery:         0.0,
				Industriousness: 1.0,
				Sociability:     0.0,
				Curiosity:       1.0,
				Aggressiveness:  0.0,
			},
			wantErr: false,
		},
		{
			name: "bravery negative",
			traits: xdg.AgentTraits{
				Bravery: -0.01,
			},
			wantErr: true,
		},
		{
			name: "bravery too high",
			traits: xdg.AgentTraits{
				Bravery: 1.01,
			},
			wantErr: true,
		},
		{
			name: "industriousness negative",
			traits: xdg.AgentTraits{
				Industriousness: -0.1,
			},
			wantErr: true,
		},
		{
			name: "industriousness too high",
			traits: xdg.AgentTraits{
				Industriousness: 1.5,
			},
			wantErr: true,
		},
		{
			name: "sociability negative",
			traits: xdg.AgentTraits{
				Sociability: -0.001,
			},
			wantErr: true,
		},
		{
			name: "curiosity too high",
			traits: xdg.AgentTraits{
				Curiosity: 2.0,
			},
			wantErr: true,
		},
		{
			name: "aggressiveness negative",
			traits: xdg.AgentTraits{
				Aggressiveness: -0.5,
			},
			wantErr: true,
		},
		{
			name: "aggressiveness too high",
			traits: xdg.AgentTraits{
				Aggressiveness: 1.05,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.traits.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestAgentIdentityValidation(t *testing.T) {
	tests := []struct {
		name          string
		modify        func(*xdg.AgentIdentityConfig)
		wantErr       bool
		expectedModel string
	}{
		{
			name:          "valid default",
			modify:        func(c *xdg.AgentIdentityConfig) {},
			wantErr:       false,
			expectedModel: "default",
		},
		{
			name: "empty name",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.Name = ""
			},
			wantErr: true,
		},
		{
			name: "path traversal in name",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.Name = "../bad_name"
			},
			wantErr: true,
		},
		{
			name: "empty clan_id",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.ClanID = ""
			},
			wantErr: true,
		},
		{
			name: "path traversal in clan_id",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.ClanID = "clans/../../escape"
			},
			wantErr: true,
		},
		{
			name: "steve model normalized to default",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.Skin.Model = "steve"
			},
			wantErr:       false,
			expectedModel: "default",
		},
		{
			name: "alex model normalized to slim",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.Skin.Model = "alex"
			},
			wantErr:       false,
			expectedModel: "slim",
		},
		{
			name: "invalid skin model",
			modify: func(c *xdg.AgentIdentityConfig) {
				c.Skin.Model = "giant_robot"
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := xdg.DefaultAgentIdentityConfig("agent_1")
			tc.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr && tc.expectedModel != "" {
				if cfg.Skin.Model != tc.expectedModel {
					t.Errorf("Skin.Model = %q, want %q", cfg.Skin.Model, tc.expectedModel)
				}
			}
		})
	}

	var nilAgent *xdg.AgentIdentityConfig
	if err := nilAgent.Validate(); err == nil {
		t.Errorf("expected error when validating nil AgentIdentityConfig")
	}
}

func TestResolverConfigMethods(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories() failed: %v", err)
	}

	// Save and load GlobalConfig through resolver
	globalCfg := xdg.DefaultGlobalConfig()
	globalCfg.InferenceGovernor.MaxConcurrentLLMRequests = 6
	if err := r.SaveGlobalConfig(globalCfg); err != nil {
		t.Fatalf("r.SaveGlobalConfig() failed: %v", err)
	}

	// Check file is at expected path
	expectedGlobalPath := filepath.Join(tmpDir, "config", "civquest", "global.yaml")
	if _, err := os.Stat(expectedGlobalPath); err != nil {
		t.Errorf("expected global config at %s, got error: %v", expectedGlobalPath, err)
	}

	loadedGlobal, err := r.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("r.LoadGlobalConfig() failed: %v", err)
	}
	if loadedGlobal.InferenceGovernor.MaxConcurrentLLMRequests != 6 {
		t.Errorf("loaded MaxConcurrentLLMRequests = %d, want 6", loadedGlobal.InferenceGovernor.MaxConcurrentLLMRequests)
	}

	// Save and load AgentConfig through resolver
	agentCfg := xdg.DefaultAgentIdentityConfig("brian")
	agentCfg.Profession = "FARMER"
	agentCfg.SecondaryHobby = "JAM_MAKER"
	if err := r.SaveAgentConfig(agentCfg); err != nil {
		t.Fatalf("r.SaveAgentConfig() failed: %v", err)
	}

	expectedAgentPath := filepath.Join(tmpDir, "config", "civquest", "agents", "brian.yaml")
	if _, err := os.Stat(expectedAgentPath); err != nil {
		t.Errorf("expected agent config at %s, got error: %v", expectedAgentPath, err)
	}

	loadedAgent, err := r.LoadAgentConfig("brian")
	if err != nil {
		t.Fatalf("r.LoadAgentConfig() failed: %v", err)
	}
	if loadedAgent.Profession != "FARMER" || loadedAgent.SecondaryHobby != "JAM_MAKER" {
		t.Errorf("loaded agent mismatch: %+v", loadedAgent)
	}

	// Test nil saves
	if err := r.SaveAgentConfig(nil); err == nil {
		t.Errorf("expected error saving nil agent config")
	}
}

func TestPackageLevelConfigHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	globalCfg := xdg.DefaultGlobalConfig()
	if err := xdg.SaveGlobalConfig(globalCfg, ""); err != nil {
		t.Fatalf("SaveGlobalConfig() with empty path failed: %v", err)
	}

	loadedGlobal, err := xdg.LoadGlobalConfig("")
	if err != nil {
		t.Fatalf("LoadGlobalConfig() with empty path failed: %v", err)
	}
	if loadedGlobal.InferenceGovernor.MaxConcurrentLLMRequests != globalCfg.InferenceGovernor.MaxConcurrentLLMRequests {
		t.Errorf("loaded global config mismatch")
	}

	agentCfg := xdg.DefaultAgentIdentityConfig("hugo")
	if err := xdg.SaveAgentConfigByName(agentCfg); err != nil {
		t.Fatalf("SaveAgentConfigByName() failed: %v", err)
	}

	loadedAgent, err := xdg.LoadAgentConfigByName("hugo")
	if err != nil {
		t.Fatalf("LoadAgentConfigByName() failed: %v", err)
	}
	if loadedAgent.Name != "hugo" {
		t.Errorf("loaded agent name = %q, want hugo", loadedAgent.Name)
	}

	if err := xdg.SaveAgentConfigByName(nil); err == nil {
		t.Errorf("expected error saving nil agent by name")
	}
}

func TestLoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentGlobal := filepath.Join(tmpDir, "does_not_exist.yaml")
	_, err := xdg.LoadGlobalConfig(nonExistentGlobal)
	if err == nil {
		t.Fatal("expected error loading non-existent file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected error to wrap os.ErrNotExist, got: %v", err)
	}

	nonExistentAgent := filepath.Join(tmpDir, "no_agent.yaml")
	_, err = xdg.LoadAgentConfig(nonExistentAgent)
	if err == nil {
		t.Fatal("expected error loading non-existent file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected error to wrap os.ErrNotExist, got: %v", err)
	}

	if _, err := xdg.LoadAgentConfig(""); err == nil {
		t.Errorf("expected error loading with empty path")
	}
	if err := xdg.SaveGlobalConfig(nil, ""); err == nil {
		t.Errorf("expected error saving nil GlobalConfig")
	}
	if err := xdg.SaveAgentConfig(nil, ""); err == nil {
		t.Errorf("expected error saving nil AgentIdentityConfig")
	}
}

func TestConfigFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "global.yaml")
	agentPath := filepath.Join(tmpDir, "agent.yaml")

	globalCfg := xdg.DefaultGlobalConfig()
	if err := xdg.SaveGlobalConfig(globalCfg, globalPath); err != nil {
		t.Fatalf("SaveGlobalConfig failed: %v", err)
	}

	info, err := os.Stat(globalPath)
	if err != nil {
		t.Fatalf("stat globalPath failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("global config file permissions = %04o, want 0600", perm)
	}

	agentCfg := xdg.DefaultAgentIdentityConfig("agent_sec")
	if err := xdg.SaveAgentConfig(agentCfg, agentPath); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	agentInfo, err := os.Stat(agentPath)
	if err != nil {
		t.Fatalf("stat agentPath failed: %v", err)
	}
	if perm := agentInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("agent config file permissions = %04o, want 0600", perm)
	}
}

func TestNaNValidation(t *testing.T) {
	t.Run("traits_nan", func(t *testing.T) {
		traitFields := []struct {
			name   string
			modify func(*xdg.AgentTraits)
		}{
			{"bravery", func(tr *xdg.AgentTraits) { tr.Bravery = math.NaN() }},
			{"industriousness", func(tr *xdg.AgentTraits) { tr.Industriousness = math.NaN() }},
			{"sociability", func(tr *xdg.AgentTraits) { tr.Sociability = math.NaN() }},
			{"curiosity", func(tr *xdg.AgentTraits) { tr.Curiosity = math.NaN() }},
			{"aggressiveness", func(tr *xdg.AgentTraits) { tr.Aggressiveness = math.NaN() }},
		}

		for _, tc := range traitFields {
			t.Run(tc.name, func(t *testing.T) {
				traits := xdg.AgentTraits{
					Bravery:         0.5,
					Industriousness: 0.5,
					Sociability:     0.5,
					Curiosity:       0.5,
					Aggressiveness:  0.5,
				}
				tc.modify(&traits)
				err := traits.Validate()
				if err == nil {
					t.Errorf("expected error for NaN trait %s, got nil", tc.name)
				}
				if !errors.Is(err, xdg.ErrTraitOutOfBounds) {
					t.Errorf("expected ErrTraitOutOfBounds, got: %v", err)
				}
			})
		}
	})

	t.Run("global_config_nan", func(t *testing.T) {
		cfgStructured := xdg.DefaultGlobalConfig()
		cfgStructured.LLMGateway.StructuredTemperature = math.NaN()
		if err := cfgStructured.Validate(); err == nil {
			t.Errorf("expected error for NaN StructuredTemperature, got nil")
		}

		cfgCreative := xdg.DefaultGlobalConfig()
		cfgCreative.LLMGateway.CreativeTemperature = math.NaN()
		if err := cfgCreative.Validate(); err == nil {
			t.Errorf("expected error for NaN CreativeTemperature, got nil")
		}
	})

	t.Run("yaml_nan_parsing", func(t *testing.T) {
		tmpDir := t.TempDir()
		nanYamlPath := filepath.Join(tmpDir, "nan_global.yaml")
		nanContent := `
llm_gateway:
  base_url: "http://127.0.0.1:11434/v1"
  model: "llama3.1"
  embedding_model: "nomic-embed-text"
  structured_temperature: .nan
  creative_temperature: 0.85
`
		if err := os.WriteFile(nanYamlPath, []byte(nanContent), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		_, err := xdg.LoadGlobalConfig(nanYamlPath)
		if err == nil {
			t.Errorf("expected error loading global config with .nan temperature, got nil")
		}
	})
}

func TestPartialGlobalConfigLoad(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "partial_override.yaml")
	overrideContent := `
llm_gateway:
  model: "custom-model-42"
supervisor:
  max_active_agents: 32
`
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	loaded, err := xdg.LoadGlobalConfig(overridePath)
	if err != nil {
		t.Fatalf("LoadGlobalConfig failed on partial override: %v", err)
	}

	// Verify overrides took effect
	if loaded.LLMGateway.Model != "custom-model-42" {
		t.Errorf("Model = %q, want custom-model-42", loaded.LLMGateway.Model)
	}
	if loaded.Supervisor.MaxActiveAgents != 32 {
		t.Errorf("MaxActiveAgents = %d, want 32", loaded.Supervisor.MaxActiveAgents)
	}

	// Verify unmentioned defaults were preserved
	defaults := xdg.DefaultGlobalConfig()
	if loaded.LLMGateway.BaseURL != defaults.LLMGateway.BaseURL {
		t.Errorf("BaseURL = %q, want default %q", loaded.LLMGateway.BaseURL, defaults.LLMGateway.BaseURL)
	}
	if loaded.LLMGateway.EmbeddingModel != defaults.LLMGateway.EmbeddingModel {
		t.Errorf("EmbeddingModel = %q, want default %q", loaded.LLMGateway.EmbeddingModel, defaults.LLMGateway.EmbeddingModel)
	}
	if loaded.LLMGateway.StructuredTemperature != defaults.LLMGateway.StructuredTemperature {
		t.Errorf("StructuredTemperature = %f, want default %f", loaded.LLMGateway.StructuredTemperature, defaults.LLMGateway.StructuredTemperature)
	}
	if loaded.InferenceGovernor.MaxConcurrentLLMRequests != defaults.InferenceGovernor.MaxConcurrentLLMRequests {
		t.Errorf("MaxConcurrentLLMRequests = %d, want default %d", loaded.InferenceGovernor.MaxConcurrentLLMRequests, defaults.InferenceGovernor.MaxConcurrentLLMRequests)
	}
	if loaded.GRPC.Host != defaults.GRPC.Host || loaded.GRPC.Port != defaults.GRPC.Port {
		t.Errorf("GRPC = %s:%d, want default %s:%d", loaded.GRPC.Host, loaded.GRPC.Port, defaults.GRPC.Host, defaults.GRPC.Port)
	}
	if loaded.Supervisor.MinFoodSurplusDays != defaults.Supervisor.MinFoodSurplusDays {
		t.Errorf("MinFoodSurplusDays = %d, want default %d", loaded.Supervisor.MinFoodSurplusDays, defaults.Supervisor.MinFoodSurplusDays)
	}
}

func TestLoadAgentConfigByNameValidation(t *testing.T) {
	invalidNames := []string{
		"../traversal",
		"../../etc/passwd",
		".",
		"..",
		"bad/name",
		"bad\\name",
		"a12345678901234567890123456789012", // 33 chars
	}

	for _, badName := range invalidNames {
		t.Run(badName, func(t *testing.T) {
			_, err := xdg.LoadAgentConfigByName(badName)
			if err == nil {
				t.Errorf("LoadAgentConfigByName(%q) expected error, got nil", badName)
			}
			if !errors.Is(err, xdg.ErrInvalidAgentName) {
				t.Errorf("LoadAgentConfigByName(%q) error should wrap ErrInvalidAgentName, got: %v", badName, err)
			}
		})
	}

	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	for _, badName := range invalidNames {
		t.Run("resolver_"+badName, func(t *testing.T) {
			_, err := r.LoadAgentConfig(badName)
			if err == nil {
				t.Errorf("r.LoadAgentConfig(%q) expected error, got nil", badName)
			}
			if !errors.Is(err, xdg.ErrInvalidAgentName) {
				t.Errorf("r.LoadAgentConfig(%q) error should wrap ErrInvalidAgentName, got: %v", badName, err)
			}
		})
	}
}

func TestAgentUUIDValidation(t *testing.T) {
	t.Run("valid_canonical_uuid", func(t *testing.T) {
		cfg := xdg.DefaultAgentIdentityConfig("agent_uuid")
		cfg.UUID = "d9d06b5a-19fb-4395-9ff7-320c2423aef5"
		if err := cfg.Validate(); err != nil {
			t.Errorf("valid UUID failed validation: %v", err)
		}
	})

	t.Run("empty_uuid_allowed", func(t *testing.T) {
		cfg := xdg.DefaultAgentIdentityConfig("agent_uuid")
		cfg.UUID = ""
		if err := cfg.Validate(); err != nil {
			t.Errorf("empty UUID should be allowed: %v", err)
		}
	})

	invalidUUIDs := []string{
		"not-a-uuid",
		"../../escape",
		"d9d06b5a-19fb-4395-9ff7-320c2423aef",   // 35 chars
		"d9d06b5a-19fb-4395-9ff7-320c2423aef55", // 37 chars
		"d9d06b5a_19fb_4395_9ff7_320c2423aef5",  // underscores instead of hyphens
		"d9d06b5a-19fb-4395-9ff7/320c2423aef5",  // slash
	}

	for _, badUUID := range invalidUUIDs {
		t.Run(badUUID, func(t *testing.T) {
			cfg := xdg.DefaultAgentIdentityConfig("agent_uuid")
			cfg.UUID = badUUID
			err := cfg.Validate()
			if err == nil {
				t.Errorf("expected error for invalid UUID %q, got nil", badUUID)
			}
			if !errors.Is(err, xdg.ErrInvalidUUID) {
				t.Errorf("expected error to wrap ErrInvalidUUID, got: %v", err)
			}
		})
	}
}

func TestAgentSkinTextureValidation(t *testing.T) {
	validTextures := []string{
		"",
		"textures/aria.png",
		"aria.PNG",
		"custom/skin_model.png",
	}

	for _, tex := range validTextures {
		t.Run("valid_"+tex, func(t *testing.T) {
			cfg := xdg.DefaultAgentIdentityConfig("agent_tex")
			cfg.Skin.TexturePath = tex
			if err := cfg.Validate(); err != nil {
				t.Errorf("valid texture path %q failed: %v", tex, err)
			}
		})
	}

	invalidTextures := []string{
		"../textures/aria.png",
		"textures/../../secret.png",
		"textures/aria.jpg",
		"aria.jpeg",
		"aria.png.exe",
		"aria.png/sub",
		"aria.txt",
	}

	for _, tex := range invalidTextures {
		t.Run("invalid_"+tex, func(t *testing.T) {
			cfg := xdg.DefaultAgentIdentityConfig("agent_tex")
			cfg.Skin.TexturePath = tex
			err := cfg.Validate()
			if err == nil {
				t.Errorf("expected error for invalid texture path %q, got nil", tex)
			}
			if !errors.Is(err, xdg.ErrInvalidSkinTexture) {
				t.Errorf("expected error to wrap ErrInvalidSkinTexture, got: %v", err)
			}
		})
	}
}
