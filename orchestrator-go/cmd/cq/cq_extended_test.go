package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestCivctl_Agent_Create_Start_Stop(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	if err := r.InitDirectories(); err != nil {
		t.Fatalf("InitDirectories failed: %v", err)
	}

	// 1. Agent Create: should write valid agent YAML and 64x64 skin asset
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "create",
		"--name", "brynn",
		"--clan", "pioneers",
		"--role", "BUILDER",
		"--bravery", "0.75",
	)
	if err != nil {
		t.Fatalf("agent create failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "created successfully") {
		t.Errorf("expected success message, got: %s", out)
	}

	// Verify YAML written and valid
	cfg, err := r.LoadAgentConfig("brynn")
	if err != nil {
		t.Fatalf("LoadAgentConfig failed: %v", err)
	}
	if cfg.Name != "brynn" || cfg.ClanID != "pioneers" || cfg.Profession != "BUILDER" {
		t.Errorf("unexpected agent config: %+v", cfg)
	}
	if cfg.Traits.Bravery != 0.75 {
		t.Errorf("expected bravery 0.75, got %.2f", cfg.Traits.Bravery)
	}

	// Verify skin file exists
	skinPath := filepath.Join(r.AgentsDataDir(), "brynn", "skin.png")
	fi, err := os.Stat(skinPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty skin asset at %s, err: %v", skinPath, err)
	}

	// 2. Prepare mock civquest-agent binary
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	mockAgentScript := filepath.Join(binDir, "civquest-agent")
	mockContent := `#!/usr/bin/env bash
trap "exit 0" SIGTERM
while true; do sleep 0.05; done
`
	if err := os.WriteFile(mockAgentScript, []byte(mockContent), 0755); err != nil {
		t.Fatalf("failed writing mock agent script: %v", err)
	}
	t.Setenv("CIVQUEST_AGENT_BIN", mockAgentScript)

	// 3. Agent Start: should launch process and write PID
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "brynn",
	)
	if err != nil {
		t.Fatalf("agent start failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "started in background") {
		t.Errorf("expected start confirmation, got: %s", out)
	}

	pidPath := r.AgentPIDPath("brynn")
	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("expected PID file at %s", pidPath)
	}

	// Starting again should error (already running)
	_, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "start", "brynn",
	)
	if err == nil {
		t.Errorf("expected error starting already running agent")
	}

	// 4. Agent Stop: should terminate process and remove PID
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"agent", "stop", "brynn",
	)
	if err != nil {
		t.Fatalf("agent stop failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "stopped successfully") {
		t.Errorf("expected stop confirmation, got: %s", out)
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("expected PID file to be removed after stop")
	}
}

func TestCivctl_Clan_Inspect_Prestige_Diplomacy(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	clanID := "pioneers"

	storage, err := db.OpenClanStorage(r, clanID)
	if err != nil {
		t.Fatalf("OpenClanStorage failed: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	// Seed Granary stock
	_ = storage.CivicRegistry.AddGranaryStockContext(ctx, "minecraft:oak_log", 128, 100)
	_ = storage.CivicRegistry.AddGranaryStockContext(ctx, "minecraft:bread", 64, 100)
	_ = storage.CivicRegistry.ReserveGranaryStockContext(ctx, "minecraft:bread", 14, 100)

	// Seed Functional Zone
	if err := storage.CivicRegistry.RegisterZoneContext(ctx, &db.FunctionalZone{
		ZoneID:      "zone-market-1",
		ZoneType:    "TAVERN",
		Name:        "Pioneer Agora",
		MinX:        10,
		MinY:        64,
		MinZ:        10,
		MaxX:        30,
		MaxY:        80,
		MaxZ:        30,
		CreatedTick: 100,
	}); err != nil {
		t.Fatalf("seed zone failed: %v", err)
	}

	// Seed Hero Prestige
	_, _ = storage.Reputation.AddPrestigeContext(ctx, "player-steve-1", "Steve", 150.0, "Slew Ender Dragon", 100)

	// Seed Diplomacy
	_ = storage.Diplomacy.SetAffinityContext(ctx, "nomads", 45.0, 100)
	_ = storage.Diplomacy.SetDiplomaticStatusContext(ctx, "nomads", "FRIENDLY", 100)
	_ = storage.Diplomacy.SetTreatyContext(ctx, "nomads", "FREE_TRADE", 24000, 100)
	_ = storage.Diplomacy.RecordTradeVolumeContext(ctx, "nomads", 120, 100)

	// 1. Clan Inspect
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"clan", "inspect", clanID,
	)
	if err != nil {
		t.Fatalf("clan inspect failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "minecraft:oak_log") || !strings.Contains(out, "Pioneer Agora") {
		t.Errorf("expected granary item and zone in output, got: %s", out)
	}

	// 2. Clan Prestige
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"clan", "prestige", clanID,
	)
	if err != nil {
		t.Fatalf("clan prestige failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Steve") || !strings.Contains(out, "150.0") {
		t.Errorf("expected Steve with 150.0 prestige in output, got: %s", out)
	}

	// 3. Clan Diplomacy
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"clan", "diplomacy", clanID,
	)
	if err != nil {
		t.Fatalf("clan diplomacy failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "nomads") || !strings.Contains(out, "FREE_TRADE") {
		t.Errorf("expected nomads and FREE_TRADE in output, got: %s", out)
	}
}

func TestCivctl_Blueprint_Commands(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = r.InitDirectories()

	// 1. Generate blueprint
	out, err := executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"blueprint", "generate",
		"--type", "granary",
		"--style", "pioneer",
	)
	if err != nil {
		t.Fatalf("blueprint generate failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "generated successfully") {
		t.Errorf("expected success message, got: %s", out)
	}

	// 2. List blueprints
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"blueprint", "list",
	)
	if err != nil {
		t.Fatalf("blueprint list failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Pioneer Granary") {
		t.Errorf("expected Pioneer Granary in list output, got: %s", out)
	}

	// 3. Validate blueprint
	bpPath := filepath.Join(r.LLMGeneratedBlueprintsDir(), "pioneer_granary.yaml")
	out, err = executeCommand(
		"--config-dir", r.ConfigDir(),
		"--data-dir", r.DataDir(),
		"--state-dir", r.StateDir(),
		"blueprint", "validate", bpPath,
	)
	if err != nil {
		t.Fatalf("blueprint validate failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Blueprint is valid!") || !strings.Contains(out, "Bill of Materials") {
		t.Errorf("expected valid and BOM header, got: %s", out)
	}
}

func TestCivctl_Gateway_Test(t *testing.T) {
	// Spin up mock OpenAI-compatible LLM endpoint
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.URL.Path, "/chat/completions") {
			resp := map[string]any{
				"id":      "chatcmpl-test-1",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "mock-model",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": "PONG",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     8,
					"completion_tokens": 2,
					"total_tokens":      10,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(req.URL.Path, "/embeddings") {
			resp := map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"object":    "embedding",
						"index":     0,
						"embedding": []float32{0.1, 0.2, 0.3, 0.4},
					},
				},
				"model": "mock-embedding-model",
				"usage": map[string]int{
					"prompt_tokens": 3,
					"total_tokens":  3,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, req)
	}))
	defer mockServer.Close()

	out, err := executeCommand(
		"gateway", "test",
		"--endpoint", mockServer.URL,
		"--model", "mock-model",
	)
	if err != nil {
		t.Fatalf("gateway test failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "ONLINE (HTTP 200 OK)") || !strings.Contains(out, "PONG") {
		t.Errorf("expected ONLINE and PONG in output, got: %s", out)
	}
	if !strings.Contains(out, "Token Throughput:") {
		t.Errorf("expected Token Throughput in output, got: %s", out)
	}
}

// Synthetic bridge server for testing AdminClient and export-recipes
type syntheticAdminServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer
}

func (s *syntheticAdminServer) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	return &civquestv1.SpawnAgentResponse{
		Success:           true,
		MinecraftEntityId: 42,
		OfflineUuid:       "00000000-0000-0000-0000-000000000042",
	}, nil
}

func (s *syntheticAdminServer) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest) (*civquestv1.DespawnAgentResponse, error) {
	return &civquestv1.DespawnAgentResponse{
		Success: true,
	}, nil
}

func (s *syntheticAdminServer) ExportRecipeGraph(ctx context.Context, req *civquestv1.ExportRecipeRequest) (*civquestv1.ExportRecipeResponse, error) {
	return &civquestv1.ExportRecipeResponse{
		Recipes: []*civquestv1.RecipeNode{
			{
				RecipeId:         "minecraft:crafting_table",
				RecipeType:       "minecraft:crafting_shaped",
				WorkstationBlock: "minecraft:crafting_table",
				Result: &civquestv1.ItemStackProto{
					ItemId: "minecraft:crafting_table",
					Count:  1,
				},
			},
		},
	}, nil
}

func TestCivctl_AdminClient_And_ExportRecipes(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(srv, &syntheticAdminServer{})
	go func() {
		_ = srv.Serve(lis)
	}()
	defer srv.Stop()

	target := lis.Addr().String()

	// 1. Direct AdminClient API
	admin, err := NewAdminClient(target, "")
	if err != nil {
		t.Fatalf("NewAdminClient failed: %v", err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	spawnResp, err := admin.SpawnAgentEntity(ctx, &civquestv1.SpawnAgentRequest{
		AgentId: "lyra",
	})
	if err != nil || !spawnResp.GetSuccess() {
		t.Fatalf("SpawnAgentEntity failed: %v, resp: %+v", err, spawnResp)
	}

	despawnResp, err := admin.DespawnAgentEntity(ctx, &civquestv1.DespawnAgentRequest{
		AgentId: "lyra",
	})
	if err != nil || !despawnResp.GetSuccess() {
		t.Fatalf("DespawnAgentEntity failed: %v, resp: %+v", err, despawnResp)
	}

	recipeResp, err := admin.ExportRecipeGraph(ctx, &civquestv1.ExportRecipeRequest{})
	if err != nil || len(recipeResp.GetRecipes()) == 0 {
		t.Fatalf("ExportRecipeGraph failed: %v, resp: %+v", err, recipeResp)
	}

	// 2. CLI server export-recipes
	out, err := executeCommand(
		"--target", target,
		"server", "export-recipes",
	)
	if err != nil {
		t.Fatalf("server export-recipes failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "minecraft:crafting_table") {
		t.Errorf("expected crafting_table in recipe export output, got: %s", out)
	}
}
