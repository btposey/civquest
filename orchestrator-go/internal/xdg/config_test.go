package xdg_test

import (
	"errors"
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
			cfg := xdg.DefaultAgentIdentityConfig("settler_1")
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
