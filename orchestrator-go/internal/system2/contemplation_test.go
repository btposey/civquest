package system2_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestAssembleStrategicLedger_NilDatabases(t *testing.T) {
	ctx := context.Background()
	prof := system2.PersonalProfile{
		AgentID:        "aria",
		ClanID:         "clan_pioneers",
		Profession:     "EXPLORER",
		SecondaryHobby: "CARPENTER",
		Traits: map[string]float64{
			"bravery":         0.8,
			"industriousness": 0.9,
		},
		Vitals: system2.PersonalVitals{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 18,
			Position:  "(10, 64, 20)",
		},
		InventoryItems: []string{"1x stone_sword", "4x bread"},
	}

	ledger, err := system2.AssembleStrategicLedger(ctx, nil, nil, nil, nil, nil, prof, 14000)
	if err != nil {
		t.Fatalf("unexpected error assembling ledger with nil dbs: %v", err)
	}

	if ledger == nil {
		t.Fatal("expected non-nil ledger")
	}
	if ledger.AgentID != "aria" {
		t.Errorf("expected AgentID aria, got %s", ledger.AgentID)
	}
	if ledger.ClanID != "clan_pioneers" {
		t.Errorf("expected ClanID clan_pioneers, got %s", ledger.ClanID)
	}
	if ledger.WorldTick != 14000 {
		t.Errorf("expected WorldTick 14000, got %d", ledger.WorldTick)
	}
	if len(ledger.CartographySummary.UnexploredDirections) != 4 {
		t.Errorf("expected 4 unexplored directions with nil db, got %d", len(ledger.CartographySummary.UnexploredDirections))
	}
	if ledger.GranarySummary.StockStatus != string(system2.SurplusStable) {
		t.Errorf("expected STABLE stock status, got %s", ledger.GranarySummary.StockStatus)
	}
	if len(ledger.TavernRumorsSummary.ActiveRumors) != 0 {
		t.Errorf("expected 0 rumors, got %d", len(ledger.TavernRumorsSummary.ActiveRumors))
	}
	if len(ledger.DiplomaticSummary.ClanAffinities) != 0 {
		t.Errorf("expected 0 affinities, got %d", len(ledger.DiplomaticSummary.ClanAffinities))
	}
}

func TestAssembleStrategicLedger_PopulatedDatabases(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. AgentDB
	agentDB, err := db.OpenAgentDB(filepath.Join(tmpDir, "agent"))
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	_ = agentDB.AddMapNode(ctx, db.MapNode{
		ID:             "node-home",
		X:              0,
		Y:              64,
		Z:              0,
		Label:          "Home Base",
		Category:       "BASE",
		SafetyScore:    1.0,
		DiscoveredTick: 100,
	})
	_ = agentDB.AddMapNode(ctx, db.MapNode{
		ID:             "node-north",
		X:              0,
		Y:              64,
		Z:              -50,
		Label:          "North Watch",
		Category:       "OUTPOST",
		SafetyScore:    0.3, // Danger spot (<0.5)
		DiscoveredTick: 200,
	})
	_ = agentDB.AddMapEdge(ctx, db.MapEdge{
		ID:          "edge-1",
		FromNodeID:  "node-home",
		ToNodeID:    "node-north",
		Distance:    50.0,
		DangerLevel: 0.7, // Danger spot (>0.5)
		IsPaved:     false,
	})

	// 2. CivicRegistryDB
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "civic.db"), "clan_pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	_ = civicDB.AddGranaryStockContext(ctx, "minecraft:bread", 20, 1000)
	_ = civicDB.AddGranaryStockContext(ctx, "minecraft:cobblestone", 64, 1000)

	// 3. RumorsDB
	rumorsDB, err := db.OpenRumorsDB(filepath.Join(tmpDir, "rumors.db"), "clan_pioneers")
	if err != nil {
		t.Fatalf("OpenRumorsDB failed: %v", err)
	}
	defer rumorsDB.Close()

	_ = rumorsDB.AddRumor(ctx, &db.Rumor{
		RumorID:       "rumor-iron",
		Category:      db.RumorCategoryResource,
		Content:       "Rich iron vein exposed in northern ravine",
		SourceAgentID: "miner_scout",
		LocationX:     15,
		LocationY:     55,
		LocationZ:     -40,
		Credibility:   0.9,
		OriginTick:    12000,
		CreatedTick:   12000,
		ExpiresTick:   50000,
	})
	_ = rumorsDB.AddRumor(ctx, &db.Rumor{
		RumorID:       "rumor-creeper",
		Category:      db.RumorCategoryHazard,
		Content:       "Creeper cluster spotted near southern ridge",
		SourceAgentID: "guard_bob",
		LocationX:     0,
		LocationY:     64,
		LocationZ:     30,
		Credibility:   0.8,
		OriginTick:    12500,
		CreatedTick:   12500,
		ExpiresTick:   50000,
	})

	// 4. DiplomacyDB
	diploDB, err := db.OpenDiplomacyDB(filepath.Join(tmpDir, "diplomacy.db"), "clan_pioneers")
	if err != nil {
		t.Fatalf("OpenDiplomacyDB failed: %v", err)
	}
	defer diploDB.Close()

	_ = diploDB.SetAffinityContext(ctx, "clan_ironborn", 25.0, 1000)
	_ = diploDB.SetTreatyContext(ctx, "clan_ironborn", "NON_AGGRESSION", 100000, 1000)

	// 5. ReputationDB
	repDB, err := db.OpenReputationDB(filepath.Join(tmpDir, "reputation.db"), "clan_pioneers")
	if err != nil {
		t.Fatalf("OpenReputationDB failed: %v", err)
	}
	defer repDB.Close()

	_, _ = repDB.AddPrestigeContext(ctx, "player-uuid-1", "HeroSteve", 45.0, "Saved settlement from raid", 1000)

	prof := system2.PersonalProfile{
		AgentID:        "aria",
		ClanID:         "clan_pioneers",
		Profession:     "EXPLORER",
		SecondaryHobby: "ARCHITECT",
		Vitals: system2.PersonalVitals{
			Health:    19.0,
			FoodLevel: 15,
			Position:  "(0, 64, 0)",
		},
		InventoryItems: []string{"1x compass", "2x bread"},
	}

	ledger, err := system2.AssembleStrategicLedger(ctx, agentDB, civicDB, rumorsDB, diploDB, repDB, prof, 14000)
	if err != nil {
		t.Fatalf("AssembleStrategicLedger failed: %v", err)
	}

	// Verify Cartography
	if ledger.Cartography().UnpavedRoadCount != 1 {
		t.Errorf("expected 1 unpaved road, got %d", ledger.Cartography().UnpavedRoadCount)
	}
	if ledger.Cartography().DistanceToFrontier != 50.0 {
		t.Errorf("expected frontier distance 50.0, got %f", ledger.Cartography().DistanceToFrontier)
	}
	if len(ledger.Cartography().DangerSpots) < 2 {
		t.Errorf("expected at least 2 danger spots, got %d", len(ledger.Cartography().DangerSpots))
	}

	// Verify Granary
	if ledger.Granary().KeyItemCounts["minecraft:bread"] != 20 {
		t.Errorf("expected 20 bread, got %d", ledger.Granary().KeyItemCounts["minecraft:bread"])
	}
	if ledger.Granary().FoodSurplusDays < 4.0 {
		t.Errorf("expected >= 4.0 food surplus days, got %f", ledger.Granary().FoodSurplusDays)
	}
	if ledger.Granary().StockStatus != string(system2.SurplusStable) {
		t.Errorf("expected STABLE stock status, got %s", ledger.Granary().StockStatus)
	}

	// Verify Rumors
	if len(ledger.TavernRumors().ActiveRumors) != 2 {
		t.Errorf("expected 2 active rumors, got %d", len(ledger.TavernRumors().ActiveRumors))
	}

	// Verify Diplomacy
	if ledger.Diplomacy().ClanAffinities["clan_ironborn"] != 25.0 {
		t.Errorf("expected affinity 25.0 for clan_ironborn, got %f", ledger.Diplomacy().ClanAffinities["clan_ironborn"])
	}
	if len(ledger.Diplomacy().RecentTreaties) != 1 {
		t.Errorf("expected 1 active treaty, got %d", len(ledger.Diplomacy().RecentTreaties))
	}
	if ledger.Diplomacy().PlayerPrestigeScore != 45.0 {
		t.Errorf("expected player prestige 45.0, got %f", ledger.Diplomacy().PlayerPrestigeScore)
	}
}

func TestContemplate_SuccessfulInference(t *testing.T) {
	expectedResp := system2.StrategicContemplationResponse{
		ImmediateGoal:    "SCOUT_FRONTIER: Explore iron vein in northern ravine at (15, 55, -40)",
		ShortTermGoal:    "PAVE_HIGHWAY: Lay cobblestone path to northern watchtower",
		LongTermGoal:     "SECURE_BORDER_TREATIES: Form mutual defense pact with clan_ironborn",
		ReflectiveMemory: "Night fell as I considered the northern iron deposit and the danger of southern creepers.",
		TradeAdjustments: []system2.TradeValuationAdjustment{
			{
				ItemID:         "iron_pickaxe",
				BaseValue:      12.0,
				NeedMultiplier: 2.0,
				MinAcceptRatio: 0.9,
				Rationale:      "Need mining tools to extract northern iron",
			},
		},
		CivicProposals: []string{"watchtower", "cobblestone_road"},
		Rationale:      "Sensory rumors indicate resources in the north, but infrastructure is lacking.",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Temperature != 0.30 {
			t.Errorf("expected sampling temperature 0.30, got %f", req.Temperature)
		}

		payload, _ := json.Marshal(expectedResp)
		resp := system2.ChatCompletionResponse{
			ID: "contemplate-test-1",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    "assistant",
						Content: string(payload),
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	svc := system2.NewContemplationService(client)
	ledger := &system2.StrategicLedger{
		AgentID:   "aria",
		ClanID:    "clan_pioneers",
		WorldTick: 14000,
		PersonalProfile: system2.PersonalProfile{
			AgentID:    "aria",
			ClanID:     "clan_pioneers",
			Profession: "EXPLORER",
		},
	}

	resp, err := svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate failed: %v", err)
	}

	if resp.ImmediateGoal != expectedResp.ImmediateGoal {
		t.Errorf("expected ImmediateGoal %q, got %q", expectedResp.ImmediateGoal, resp.ImmediateGoal)
	}
	if resp.ShortTermGoal != expectedResp.ShortTermGoal {
		t.Errorf("expected ShortTermGoal %q, got %q", expectedResp.ShortTermGoal, resp.ShortTermGoal)
	}
	if resp.LongTermGoal != expectedResp.LongTermGoal {
		t.Errorf("expected LongTermGoal %q, got %q", expectedResp.LongTermGoal, resp.LongTermGoal)
	}
	if len(resp.TradeAdjustments) != 1 {
		t.Fatalf("expected 1 trade adjustment, got %d", len(resp.TradeAdjustments))
	}
	if resp.TradeAdjustments[0].ItemID != "minecraft:iron_pickaxe" {
		t.Errorf("expected itemID minecraft:iron_pickaxe, got %s", resp.TradeAdjustments[0].ItemID)
	}
	if len(resp.CivicProposals) != 2 {
		t.Errorf("expected 2 civic proposals, got %d", len(resp.CivicProposals))
	}
}

func TestContemplate_DialogueServiceIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal(map[string]any{
			"immediate_goal":    "HARVEST_WHEAT",
			"short_term_goal":   "EXPAND_FARM",
			"long_term_goal":    "FEED_CLAN",
			"reflective_memory": "Quiet night in the fields.",
			"civic_proposals":   []string{"granary_extension"},
			"rationale":         "Wheat is ready to harvest.",
		})
		resp := system2.ChatCompletionResponse{
			ID: "dialogue-contemplate-1",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    "assistant",
						Content: string(payload),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	diagSvc, err := system2.NewDialogueService(client, &xdg.AgentIdentityConfig{
		Name:       "farmer_bob",
		ClanID:     "clan_wheat",
		Profession: "FARMER",
	})
	if err != nil {
		t.Fatalf("failed creating dialogue service: %v", err)
	}
	defer diagSvc.Close()

	ledger := &system2.StrategicLedger{
		AgentID: "farmer_bob",
		ClanID:  "clan_wheat",
		PersonalProfile: system2.PersonalProfile{
			AgentID:    "farmer_bob",
			ClanID:     "clan_wheat",
			Profession: "FARMER",
		},
	}

	resp, err := diagSvc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("diagSvc.Contemplate failed: %v", err)
	}
	if resp.ImmediateGoal != "HARVEST_WHEAT" {
		t.Errorf("expected HARVEST_WHEAT, got %s", resp.ImmediateGoal)
	}
}

func TestContemplate_ValidationAndSanitization(t *testing.T) {
	// Return empty goals to test default fallback generation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal(map[string]any{
			"immediate_goal":    "",
			"short_term_goal":   "",
			"long_term_goal":    "",
			"reflective_memory": "",
			"trade_adjustments": []map[string]any{
				{
					"item_id":          "bread",
					"base_value":       150.0, // should be clamped to 100.0
					"need_multiplier":  10.0,  // should be clamped to 4.0
					"min_accept_ratio": 0.1,   // should be clamped to 0.5
					"rationale":        "super expensive bread",
				},
			},
		})
		resp := system2.ChatCompletionResponse{
			ID: "contemplate-sanitize-1",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    "assistant",
						Content: string(payload),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	ledger := &system2.StrategicLedger{
		PersonalProfile: system2.PersonalProfile{
			AgentID:    "bob",
			Profession: "BUILDER",
		},
	}

	resp, err := system2.Contemplate(context.Background(), client, ledger)
	if err != nil {
		t.Fatalf("Contemplate failed: %v", err)
	}

	if resp.ImmediateGoal == "" {
		t.Error("expected default ImmediateGoal for BUILDER, got empty")
	}
	if resp.ShortTermGoal == "" {
		t.Error("expected default ShortTermGoal, got empty")
	}
	if resp.LongTermGoal == "" {
		t.Error("expected default LongTermGoal, got empty")
	}
	if resp.ReflectiveMemory == "" {
		t.Error("expected default ReflectiveMemory, got empty")
	}
	if len(resp.TradeAdjustments) != 1 {
		t.Fatalf("expected 1 adjustment, got %d", len(resp.TradeAdjustments))
	}

	adj := resp.TradeAdjustments[0]
	if adj.ItemID != "minecraft:bread" {
		t.Errorf("expected minecraft:bread, got %s", adj.ItemID)
	}
	if adj.BaseValue > 100.0 {
		t.Errorf("expected base_value <= 100.0, got %f", adj.BaseValue)
	}
	if adj.NeedMultiplier > 4.0 {
		t.Errorf("expected need_multiplier <= 4.0, got %f", adj.NeedMultiplier)
	}
	if adj.MinAcceptRatio < 0.5 {
		t.Errorf("expected min_accept_ratio >= 0.5, got %f", adj.MinAcceptRatio)
	}
}

func TestContemplate_CustomSystemPrompt(t *testing.T) {
	var capturedSystemPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, msg := range req.Messages {
			if msg.Role == "system" {
				capturedSystemPrompt = msg.Content
			}
		}

		payload, _ := json.Marshal(map[string]any{
			"immediate_goal":    "BUILD_DEFENSE",
			"short_term_goal":   "FORTIFY_WALLS",
			"long_term_goal":    "IMPREGNABLE_CASTLE",
			"reflective_memory": "Custom dreams of stone.",
			"civic_proposals":   []string{"stone_wall"},
			"rationale":         "Strategic directives.",
		})
		resp := system2.ChatCompletionResponse{
			ID: "custom-contemplate-1",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    "assistant",
						Content: string(payload),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	svc := system2.NewContemplationService(client)

	// 1. Profile-level custom prompt
	customProfilePrompt := "CUSTOM_SKILL_PROMPT: You are a fortress architect dedicated to stone."
	ledger := &system2.StrategicLedger{
		AgentID: "solas",
		PersonalProfile: system2.PersonalProfile{
			AgentID:           "solas",
			Profession:        "BUILDER",
			ClanID:            "clan_iron",
			SleepSystemPrompt: customProfilePrompt,
		},
	}

	resp, err := svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate with custom profile prompt failed: %v", err)
	}
	if resp.ImmediateGoal != "BUILD_DEFENSE" {
		t.Errorf("expected BUILD_DEFENSE, got %s", resp.ImmediateGoal)
	}
	if !strings.Contains(capturedSystemPrompt, customProfilePrompt) {
		t.Errorf("expected captured system prompt to contain custom prompt, got: %s", capturedSystemPrompt)
	}

	// 2. Service-level CustomSystemPrompt fallback when profile prompt is empty
	svc.SetCustomSystemPrompt("SERVICE_LEVEL_PROMPT: Global sleep contemplation directive.")
	ledger.PersonalProfile.SleepSystemPrompt = ""
	_, err = svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate with service custom prompt failed: %v", err)
	}
	if !strings.Contains(capturedSystemPrompt, "SERVICE_LEVEL_PROMPT: Global sleep contemplation directive.") {
		t.Errorf("expected captured system prompt to contain service level prompt, got: %s", capturedSystemPrompt)
	}

	// 3. Fallback to default when neither is present
	svc.SetCustomSystemPrompt("")
	ledger.PersonalProfile.SleepSystemPrompt = ""
	_, err = svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate with default prompt failed: %v", err)
	}
	expectedDefault := system2.DefaultContemplationPrompt("solas", "BUILDER", "clan_iron")
	if !strings.Contains(capturedSystemPrompt, expectedDefault) {
		t.Errorf("expected captured system prompt to contain default contemplation prompt, got: %s", capturedSystemPrompt)
	}
}

func TestContemplate_VerifiedFactsInStrategicLedger(t *testing.T) {
	graph := system1.NewFactGraph()
	graph.SetBool("colonize.is_establishing", "colonize", true, "Establishing new settlement node")
	graph.SetBool("threat.is_emergency", "threat", false, "Threat nominal")

	ledger := &system2.StrategicLedger{
		AgentID:   "aria",
		ClanID:    "clan_pioneers",
		WorldTick: 15000,
		PersonalProfile: system2.PersonalProfile{
			AgentID:    "aria",
			ClanID:     "clan_pioneers",
			Profession: "EXPLORER",
		},
	}
	ledger.WithFactGraph(graph)

	if !strings.Contains(ledger.VerifiedFacts, "Establishing new settlement node") {
		t.Fatalf("expected ledger.VerifiedFacts to contain fact evidence, got:\n%s", ledger.VerifiedFacts)
	}

	var capturedSystemPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) > 0 {
			capturedSystemPrompt = req.Messages[0].Content
		}

		resp := system2.ChatCompletionResponse{
			ID: "contemplate-facts-test",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    "assistant",
						Content: `{"immediate_goal":"SCOUT_FRONTIER","short_term_goal":"MAP_SECTOR","long_term_goal":"FOUND_COLONY","reflective_memory":"Reflected on verified facts","trade_adjustments":[],"civic_proposals":[],"rationale":"Facts verified"}`,
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	svc := system2.NewContemplationService(client)
	resp, err := svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate failed: %v", err)
	}
	if resp.ImmediateGoal != "SCOUT_FRONTIER" {
		t.Errorf("expected SCOUT_FRONTIER, got %s", resp.ImmediateGoal)
	}

	if !strings.Contains(capturedSystemPrompt, "verified_facts") {
		t.Errorf("expected capturedSystemPrompt to contain 'verified_facts' key in strategic ledger, got:\n%s", capturedSystemPrompt)
	}
	if !strings.Contains(capturedSystemPrompt, "Establishing new settlement node") {
		t.Errorf("expected capturedSystemPrompt to contain fact evidence, got:\n%s", capturedSystemPrompt)
	}
}

func TestContemplate_InjectsCommunityDialogueSection(t *testing.T) {
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			captured += m.Content + "\n"
		}
		payload, _ := json.Marshal(map[string]any{
			"immediate_goal": "SCOUT_FRONTIER", "short_term_goal": "a", "long_term_goal": "b",
			"reflective_memory": "c", "rationale": "d",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "x",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: server.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	svc := system2.NewContemplationService(client)
	ledger := &system2.StrategicLedger{
		AgentID:           "aria",
		CommunityDialogue: "[COMMUNITY DIALOGUE & RECENT PEER STATEMENTS]\n- Bran (5m away, 3s ago): \"need logs\"",
	}
	if _, err := svc.Contemplate(context.Background(), ledger); err != nil {
		t.Fatalf("Contemplate: %v", err)
	}
	if !strings.Contains(captured, "[COMMUNITY DIALOGUE & RECENT PEER STATEMENTS]") || !strings.Contains(captured, "need logs") {
		t.Errorf("community dialogue not injected into prompt: %s", captured)
	}
}

func TestContemplate_StrategicDirectiveParsingAndPrompt(t *testing.T) {
	var capturedPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, m := range req.Messages {
			if m.Role == "system" {
				capturedPrompt = m.Content
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"immediate_goal":    "MINE_STONE: Gather coal and cobblestone from northern ridge",
			"short_term_goal":   "UPGRADE_TOOLS",
			"long_term_goal":    "BUILD_FORGE",
			"reflective_memory": "Bran is already chopping timber, so I will mine stone.",
			"directive": map[string]any{
				"action": "mine stone",
				"target": "northern ridge",
				"reason": "avoid duplicating Bran's timber run",
			},
			"rationale": "Coordinate with peers",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "directive-test-1",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: server.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	svc := system2.NewContemplationService(client)
	ledger := &system2.StrategicLedger{
		AgentID:           "aria",
		ClanID:            "clan_pioneers",
		WorldTick:         4000,
		CommunityDialogue: "[COMMUNITY DIALOGUE & RECENT PEER STATEMENTS]\n- Bran (4m away, 5s ago): \"I am gathering timber\"",
		PersonalProfile: system2.PersonalProfile{
			AgentID:    "aria",
			Profession: "EXPLORER",
		},
	}

	resp, err := svc.Contemplate(context.Background(), ledger)
	if err != nil {
		t.Fatalf("Contemplate: %v", err)
	}

	for _, action := range []string{
		system2.DirectiveGatherTimber,
		system2.DirectiveMineStone,
		system2.DirectiveForageFood,
		system2.DirectiveCraftSurvivalKit,
		system2.DirectiveBuildShelter,
		system2.DirectiveScoutFrontier,
		system2.DirectiveSocialize,
	} {
		if !strings.Contains(capturedPrompt, action) {
			t.Errorf("expected prompt to include canonical directive action %s", action)
		}
	}
	if !strings.Contains(capturedPrompt, "avoid duplicating peers") {
		t.Errorf("expected prompt to mention avoiding duplicating peers")
	}

	if resp.Directive == nil {
		t.Fatal("expected normalized non-nil Directive")
	}
	if resp.Directive.Action != system2.DirectiveMineStone {
		t.Errorf("expected MINE_STONE, got %q", resp.Directive.Action)
	}
	if resp.Directive.Target != "northern ridge" {
		t.Errorf("expected target 'northern ridge', got %q", resp.Directive.Target)
	}
	if resp.Directive.Reason != "avoid duplicating Bran's timber run" {
		t.Errorf("unexpected reason %q", resp.Directive.Reason)
	}
}

func TestNormalizeDirective_FallbackInference(t *testing.T) {
	cases := []struct {
		name          string
		directive     *system2.StrategicDirective
		immediateGoal string
		role          string
		want          string
	}{
		{
			name:          "canonical exact",
			directive:     &system2.StrategicDirective{Action: "SOCIALIZE"},
			immediateGoal: "GATHER_TIMBER",
			want:          system2.DirectiveSocialize,
		},
		{
			name:          "lowercase hyphen variant",
			directive:     &system2.StrategicDirective{Action: "craft-survival-kit"},
			immediateGoal: "",
			want:          system2.DirectiveCraftSurvivalKit,
		},
		{
			name:          "nil directive infers scout",
			directive:     nil,
			immediateGoal: "SCOUT_FRONTIER: Map eastern valley",
			want:          system2.DirectiveScoutFrontier,
		},
		{
			name:          "unknown directive infers forage from harvest crops",
			directive:     &system2.StrategicDirective{Action: "DO_SOMETHING"},
			immediateGoal: "HARVEST_CROPS: Tend wheat and berries",
			want:          system2.DirectiveForageFood,
		},
		{
			name:          "infers mine stone",
			directive:     nil,
			immediateGoal: "MINE_COAL: Extract coal ore",
			want:          system2.DirectiveMineStone,
		},
		{
			name:          "infers build shelter from pave road",
			directive:     nil,
			immediateGoal: "PAVE_ROAD: Lay stone slabs along trail",
			want:          system2.DirectiveBuildShelter,
		},
		{
			name:          "infers craft survival kit",
			directive:     nil,
			immediateGoal: "CRAFT_TOOLS: Craft wooden axe and sword",
			want:          system2.DirectiveCraftSurvivalKit,
		},
		{
			name:          "infers socialize from tavern",
			directive:     nil,
			immediateGoal: "TAVERN_MEET: Catch up with neighbors",
			want:          system2.DirectiveSocialize,
		},
		{
			name:          "generic goal falls back to role",
			directive:     nil,
			immediateGoal: "DO_WORK",
			role:          "FARMER",
			want:          system2.DirectiveForageFood,
		},
		{
			name:          "default fallback is GATHER_TIMBER",
			directive:     nil,
			immediateGoal: "GATHER_RESOURCES",
			role:          "SETTLER",
			want:          system2.DirectiveGatherTimber,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := system2.NormalizeDirective(tc.directive, tc.immediateGoal, tc.role)
			if got == nil {
				t.Fatal("NormalizeDirective returned nil")
			}
			if got.Action != tc.want {
				t.Errorf("got action %q, want %q", got.Action, tc.want)
			}
		})
	}
}
