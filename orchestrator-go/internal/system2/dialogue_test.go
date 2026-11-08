package system2

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/memory"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type mockEmbedder struct {
	customVec []float32
}

func (m *mockEmbedder) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if len(m.customVec) > 0 {
		return m.customVec, nil
	}
	vec := make([]float32, db.VectorDimension)
	vec[0] = 1.0
	return vec, nil
}

func setupTestDialogueService(
	t *testing.T,
	handler http.HandlerFunc,
	agentTraits xdg.AgentTraits,
	randVal float64,
) (*DialogueService, *db.ReputationDB, *memory.Pipeline, func()) {
	t.Helper()

	server := httptest.NewServer(handler)

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{
			BaseURL: server.URL,
			APIKey:  "test-api-key",
			Model:   "test-model",
		},
		xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 2,
			RequestTimeoutSeconds:    5,
		},
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed to create system2 client: %v", err)
	}

	tempDir := t.TempDir()
	repDBPath := filepath.Join(tempDir, "clans", "clan_alpha", "reputation.db")
	repDB, err := db.OpenReputationDB(repDBPath, "clan_alpha")
	if err != nil {
		t.Fatalf("failed to open reputation db: %v", err)
	}

	agentDBDir := filepath.Join(tempDir, "agents", "aria")
	agentDB, err := db.OpenAgentDB(agentDBDir)
	if err != nil {
		t.Fatalf("failed to open agent db: %v", err)
	}

	embedder := &mockEmbedder{}
	memPipe := memory.NewPipeline(agentDB, embedder)

	agentCfg := &xdg.AgentIdentityConfig{
		Name:           "aria",
		UUID:           "agent-aria-uuid",
		ClanID:         "clan_alpha",
		Profession:     "FARMER",
		SecondaryHobby: "BOTANY",
		Traits:         agentTraits,
	}

	svc, err := NewDialogueService(
		client,
		agentCfg,
		WithDialogueReputation(repDB),
		WithDialogueMemory(memPipe),
		WithDialogueRand(func() float64 { return randVal }),
	)
	if err != nil {
		t.Fatalf("failed to create dialogue service: %v", err)
	}

	cleanup := func() {
		_ = repDB.Close()
		_ = agentDB.Close()
		_ = client.Close()
		server.Close()
	}

	return svc, repDB, memPipe, cleanup
}

func TestDialogueService_Chat_Success(t *testing.T) {
	var capturedReq ChatCompletionRequest

	mockReply := DialogueResponse{
		SpokenReply:       "Good day, traveler! How can I help you?",
		TradeDecision:     "NONE",
		CounterOfferItems: nil,
		PrestigeDelta:     3.0,
		MemorySummary:     "Pleasant greeting with player Steve.",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)
		respBytes, _ := json.Marshal(mockReply)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   capturedReq.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.8, Sociability: 0.7, Industriousness: 0.6}
	svc, repDB, memPipe, cleanup := setupTestDialogueService(t, handler, traits, 0.1)
	defer cleanup()

	ctx := context.Background()
	req := ChatDialogueRequest{
		PlayerUUID:   "player-steve",
		PlayerName:   "Steve",
		Message:      "Hello Aria, nice weather today!",
		Distance:     2.5,
		WorldTick:    5000,
		SurplusState: SurplusStable,
		Vitals: &DialogueVitals{
			Health:    20.0,
			FoodLevel: 18,
		},
	}

	res, err := svc.ProcessChat(ctx, req)
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}

	if res.Disobeyed {
		t.Errorf("expected disobedience false, got true (%s)", res.DisobeyReason)
	}
	if res.Response.SpokenReply != mockReply.SpokenReply {
		t.Errorf("expected reply %q, got %q", mockReply.SpokenReply, res.Response.SpokenReply)
	}
	if res.ChatIntent == nil || res.ChatIntent.Message != mockReply.SpokenReply {
		t.Errorf("unexpected ChatIntent: %+v", res.ChatIntent)
	}
	if res.ChatIntent.TargetChannel != civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL {
		t.Errorf("expected LOCAL channel, got %v", res.ChatIntent.TargetChannel)
	}
	if res.UpdatedPrestige != 3.0 {
		t.Errorf("expected updated prestige 3.0, got %.1f", res.UpdatedPrestige)
	}

	// Verify prestige.db updated
	hp, err := repDB.GetHeroPrestige(ctx, "player-steve")
	if err != nil || hp == nil {
		t.Fatalf("GetHeroPrestige failed: %v, hp=%+v", err, hp)
	}
	if hp.Score != 3.0 {
		t.Errorf("expected score 3.0 in reputation.db, got %.1f", hp.Score)
	}

	// Verify episodic memory was ingested
	matches, err := memPipe.Retrieve(ctx, "greeting with Steve", 5)
	if err != nil {
		t.Fatalf("Retrieve memory failed: %v", err)
	}
	if len(matches) == 0 {
		t.Error("expected at least 1 ingested episodic memory")
	}
}

func TestDialogueService_Trade_AcceptAndCounter(t *testing.T) {
	var currentMock atomic.Value

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var capturedReq ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		mock := currentMock.Load().(DialogueResponse)
		respBytes, _ := json.Marshal(mock)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-trade",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   capturedReq.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.5, Sociability: 0.5, Industriousness: 0.5}
	svc, repDB, _, cleanup := setupTestDialogueService(t, handler, traits, 0.1)
	defer cleanup()

	ctx := context.Background()

	// 1. ACCEPT Trade
	acceptMock := DialogueResponse{
		SpokenReply:   "A fair bargain! I accept your 3 emeralds for 16 carrots.",
		TradeDecision: "ACCEPT",
		PrestigeDelta: 4.0,
		MemorySummary: "Accepted emeralds for carrots from Steve.",
	}
	currentMock.Store(acceptMock)

	tradeReq := TradeDialogueRequest{
		PlayerEntityID: 101,
		PlayerUUID:     "player-steve",
		PlayerName:     "Steve",
		OfferedItems: []*civquestv1.ItemStackProto{
			{ItemId: "minecraft:emerald", Count: 3},
		},
		SpokenProposal: "Would you trade 16 carrots for these 3 emeralds?",
		WorldTick:      6000,
		SurplusState:   SurplusSurplus,
	}

	res, err := svc.ProcessTrade(ctx, tradeReq)
	if err != nil {
		t.Fatalf("ProcessTrade (ACCEPT) failed: %v", err)
	}
	if res.Disobeyed {
		t.Errorf("expected no disobedience, got %s", res.DisobeyReason)
	}
	if res.TradeIntent == nil || !res.TradeIntent.AcceptDeal {
		t.Errorf("expected AcceptDeal true, got %+v", res.TradeIntent)
	}
	if res.TradeIntent.PlayerEntityId != 101 {
		t.Errorf("expected player entity ID 101, got %d", res.TradeIntent.PlayerEntityId)
	}
	if len(res.TradeIntent.AgentCounterOffer) != 0 {
		t.Errorf("expected empty counter offer on ACCEPT, got %d items", len(res.TradeIntent.AgentCounterOffer))
	}
	if res.UpdatedPrestige != 4.0 {
		t.Errorf("expected updated prestige 4.0, got %.1f", res.UpdatedPrestige)
	}

	// 2. COUNTER Trade
	counterMock := DialogueResponse{
		SpokenReply:   "3 emeralds isn't enough; give me 5 emeralds and we have a deal.",
		TradeDecision: "COUNTER",
		CounterOfferItems: []CounterOfferItem{
			{ItemID: "minecraft:emerald", Count: 5},
		},
		PrestigeDelta: 0.0,
		MemorySummary: "Demanded 5 emeralds instead of 3.",
	}
	currentMock.Store(counterMock)

	res, err = svc.ProcessTrade(ctx, tradeReq)
	if err != nil {
		t.Fatalf("ProcessTrade (COUNTER) failed: %v", err)
	}
	if res.Disobeyed {
		t.Errorf("expected no disobedience, got %s", res.DisobeyReason)
	}
	if res.TradeIntent.AcceptDeal {
		t.Error("expected AcceptDeal false on COUNTER")
	}
	if len(res.TradeIntent.AgentCounterOffer) != 1 {
		t.Fatalf("expected 1 counter offer item, got %d", len(res.TradeIntent.AgentCounterOffer))
	}
	if res.TradeIntent.AgentCounterOffer[0].ItemId != "minecraft:emerald" || res.TradeIntent.AgentCounterOffer[0].Count != 5 {
		t.Errorf("unexpected counter offer item: %+v", res.TradeIntent.AgentCounterOffer[0])
	}

	// Verify prestige score in db remained 4.0
	hp, err := repDB.GetHeroPrestige(ctx, "player-steve")
	if err != nil || hp.Score != 4.0 {
		t.Errorf("expected score 4.0, got %.1f (err: %v)", hp.Score, err)
	}
}

func TestDialogueService_StochasticDisobedience(t *testing.T) {
	mockReply := DialogueResponse{
		SpokenReply:   "Sure, let's do it!",
		TradeDecision: "ACCEPT",
		PrestigeDelta: 1.0,
		MemorySummary: "Agreed to follow player into battle.",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var capturedReq ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		respBytes, _ := json.Marshal(mockReply)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-disobey",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   capturedReq.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.3} // Low bravery: 0.3

	t.Run("DangerousOrder_RollHigherThanBravery_Disobeys", func(t *testing.T) {
		// Rand roll = 0.8 > Bravery 0.3 -> triggers disobedience!
		svc, _, _, cleanup := setupTestDialogueService(t, handler, traits, 0.8)
		defer cleanup()

		ctx := context.Background()
		req := ChatDialogueRequest{
			PlayerUUID:   "player-alex",
			PlayerName:   "Alex",
			Message:      "Come fight the Warden with me in the Deep Dark!",
			WorldTick:    7000,
			SurplusState: SurplusStable,
		}

		res, err := svc.ProcessChat(ctx, req)
		if err != nil {
			t.Fatalf("ProcessChat failed: %v", err)
		}

		if !res.Disobeyed {
			t.Fatal("expected Disobeyed to be true")
		}
		if !strings.Contains(res.Response.SpokenReply, "refuse") && !strings.Contains(res.Response.SpokenReply, "dangerous") {
			t.Errorf("expected refusal text in reply, got: %s", res.Response.SpokenReply)
		}
		if res.ChatIntent.Message != res.Response.SpokenReply {
			t.Errorf("chat intent message %q doesn't match refusal reply %q", res.ChatIntent.Message, res.Response.SpokenReply)
		}
	})

	t.Run("DangerousOrder_RollLowerThanBravery_Obeys", func(t *testing.T) {
		// Rand roll = 0.2 <= Bravery 0.3 -> overcomes fear, obeys!
		svc, _, _, cleanup := setupTestDialogueService(t, handler, traits, 0.2)
		defer cleanup()

		ctx := context.Background()
		req := ChatDialogueRequest{
			PlayerUUID:   "player-alex",
			PlayerName:   "Alex",
			Message:      "Help me fight the creeper horde!",
			Dangerous:    true,
			WorldTick:    7100,
			SurplusState: SurplusStable,
		}

		res, err := svc.ProcessChat(ctx, req)
		if err != nil {
			t.Fatalf("ProcessChat failed: %v", err)
		}

		if res.Disobeyed {
			t.Errorf("expected Disobeyed false, got true (%s)", res.DisobeyReason)
		}
		if res.Response.SpokenReply != mockReply.SpokenReply {
			t.Errorf("expected original reply %q, got %q", mockReply.SpokenReply, res.Response.SpokenReply)
		}
	})

	t.Run("FoodDeficit_TradeRefusal", func(t *testing.T) {
		// Clan has food deficit + roll 0.7 > Bravery 0.3 -> refuses trade
		svc, _, _, cleanup := setupTestDialogueService(t, handler, traits, 0.7)
		defer cleanup()

		ctx := context.Background()
		req := TradeDialogueRequest{
			PlayerEntityID: 202,
			PlayerUUID:     "player-alex",
			PlayerName:     "Alex",
			SpokenProposal: "Sell me all your bread and potatoes.",
			WorldTick:      7200,
			SurplusState:   SurplusDeficit, // DEFICIT
		}

		res, err := svc.ProcessTrade(ctx, req)
		if err != nil {
			t.Fatalf("ProcessTrade failed: %v", err)
		}

		if !res.Disobeyed {
			t.Fatal("expected Disobeyed to be true on deficit")
		}
		if res.TradeIntent.AcceptDeal {
			t.Error("expected AcceptDeal false on deficit refusal")
		}
		if !strings.Contains(res.TradeIntent.SpokenResponse, "deficit") && !strings.Contains(res.TradeIntent.SpokenResponse, "granary") {
			t.Errorf("expected mention of deficit or granary, got %s", res.TradeIntent.SpokenResponse)
		}
	})
}

func TestDialogueService_PromptAssembly_PrestigeAndRAG(t *testing.T) {
	var capturedReq ChatCompletionRequest

	mockReply := DialogueResponse{
		SpokenReply:   "Hail, Clan Hero! It is an honor.",
		TradeDecision: "NONE",
		PrestigeDelta: 0.0,
		MemorySummary: "Welcomed legendary hero Steve.",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		respBytes, _ := json.Marshal(mockReply)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-hero",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   capturedReq.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.9, Sociability: 0.8, Industriousness: 0.7}
	svc, repDB, memPipe, cleanup := setupTestDialogueService(t, handler, traits, 0.1)
	defer cleanup()

	ctx := context.Background()

	// 1. Pre-seed player prestige to 65.0 (HERO)
	_, err := repDB.RecordPrestigeDelta(ctx, db.PrestigeLedgerEntry{
		PlayerUUID: "player-hero",
		PlayerName: "SteveTheHero",
		AgentID:    "aria",
		Delta:      65.0,
		Reason:     "Saved settlement from dragon attack",
		WorldTick:  1000,
	})
	if err != nil {
		t.Fatalf("failed to record prestige delta: %v", err)
	}

	// 2. Pre-seed episodic memory in RAG
	_, err = memPipe.Ingest(ctx, memory.MemoryEvent{
		WorldTick:     2000,
		Category:      memory.CategoryCombat,
		Actors:        []string{"aria", "SteveTheHero"},
		SummaryText:   "Steve fought alongside Aria and saved the granary from zombies.",
		SaliencyScore: memory.SaliencyCombat,
	})
	if err != nil {
		t.Fatalf("failed to ingest memory: %v", err)
	}

	// 3. Process chat
	req := ChatDialogueRequest{
		PlayerUUID:   "player-hero",
		PlayerName:   "SteveTheHero",
		Message:      "Greetings Aria, how is the harvest?",
		WorldTick:    8000,
		SurplusState: SurplusSurplus,
	}

	res, err := svc.ProcessChat(ctx, req)
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}
	if res.Disobeyed {
		t.Errorf("unexpected disobedience: %s", res.DisobeyReason)
	}

	// Verify prompt sent to LLM
	if len(capturedReq.Messages) < 2 {
		t.Fatalf("expected at least 2 prompt messages, got %d", len(capturedReq.Messages))
	}
	sysContent := capturedReq.Messages[0].Content

	// Check Hero Prestige score & Title in prompt
	if !strings.Contains(sysContent, "Hero Prestige Score: 65.0") {
		t.Errorf("expected 'Hero Prestige Score: 65.0' in prompt, got:\n%s", sysContent)
	}
	if !strings.Contains(sysContent, "Title: HERO") {
		t.Errorf("expected 'Title: HERO' in prompt, got:\n%s", sysContent)
	}

	// Check Surplus state in prompt
	if !strings.Contains(sysContent, "Clan Granary State: SURPLUS") {
		t.Errorf("expected SURPLUS state in prompt, got:\n%s", sysContent)
	}

	// Check RAG historical memory in prompt
	if !strings.Contains(sysContent, "saved the granary from zombies") {
		t.Errorf("expected RAG memory in prompt, got:\n%s", sysContent)
	}
}

func TestDialogueService_PrestigeClampingAndNaN(t *testing.T) {
	var currentMock atomic.Value

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock := currentMock.Load().(DialogueResponse)
		respBytes, _ := json.Marshal(mock)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-clamp",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.5, Sociability: 0.5, Industriousness: 0.5}
	svc, repDB, _, cleanup := setupTestDialogueService(t, handler, traits, 0.1)
	defer cleanup()

	ctx := context.Background()

	// 1. ProcessChat clamps excessive positive delta (> 10.0)
	currentMock.Store(DialogueResponse{
		SpokenReply:   "You are amazing!",
		TradeDecision: "NONE",
		PrestigeDelta: 99.9,
	})
	res, err := svc.ProcessChat(ctx, ChatDialogueRequest{
		PlayerUUID: "player-clamp",
		PlayerName: "Clamper",
		Message:    "Hello",
		WorldTick:  100,
	})
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}
	if res.Response.PrestigeDelta != 10.0 || res.UpdatedPrestige != 10.0 {
		t.Errorf("expected prestige clamped to 10.0, got delta %.1f, updated %.1f", res.Response.PrestigeDelta, res.UpdatedPrestige)
	}

	// 2. ProcessChat clamps excessive negative delta (< -10.0)
	currentMock.Store(DialogueResponse{
		SpokenReply:   "You are terrible!",
		TradeDecision: "NONE",
		PrestigeDelta: -50.0,
	})
	res, err = svc.ProcessChat(ctx, ChatDialogueRequest{
		PlayerUUID: "player-clamp",
		PlayerName: "Clamper",
		Message:    "Threatening statement",
		WorldTick:  200,
	})
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}
	if res.Response.PrestigeDelta != -10.0 || res.UpdatedPrestige != 0.0 {
		t.Errorf("expected prestige clamped to -10.0 (new total 0.0), got delta %.1f, updated %.1f", res.Response.PrestigeDelta, res.UpdatedPrestige)
	}

	// 3. ProcessTrade clamps to [-5.0, 5.0]
	currentMock.Store(DialogueResponse{
		SpokenReply:   "Great trade!",
		TradeDecision: "ACCEPT",
		PrestigeDelta: 15.0,
	})
	tradeRes, err := svc.ProcessTrade(ctx, TradeDialogueRequest{
		PlayerEntityID: 200,
		PlayerUUID:     "player-clamp",
		PlayerName:     "Clamper",
		SpokenProposal: "Trade",
		WorldTick:      300,
	})
	if err != nil {
		t.Fatalf("ProcessTrade failed: %v", err)
	}
	if tradeRes.Response.PrestigeDelta != 5.0 || tradeRes.UpdatedPrestige != 5.0 {
		t.Errorf("expected trade prestige clamped to 5.0, got delta %.1f, updated %.1f", tradeRes.Response.PrestigeDelta, tradeRes.UpdatedPrestige)
	}

	// 4. clampPrestigeDelta NaN and Inf handling
	if val := clampPrestigeDelta(math.NaN(), -10.0, 10.0); val != 0.0 {
		t.Errorf("expected NaN to clamp to 0.0, got %f", val)
	}
	if val := clampPrestigeDelta(math.Inf(1), -10.0, 10.0); val != 0.0 {
		t.Errorf("expected +Inf to clamp to 0.0, got %f", val)
	}
	if val := clampPrestigeDelta(math.Inf(-1), -10.0, 10.0); val != 0.0 {
		t.Errorf("expected -Inf to clamp to 0.0, got %f", val)
	}

	_ = repDB
}

func TestDialogueService_CounterOfferFiltering(t *testing.T) {
	var currentMock atomic.Value

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock := currentMock.Load().(DialogueResponse)
		respBytes, _ := json.Marshal(mock)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-counter",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	traits := xdg.AgentTraits{Bravery: 0.5, Sociability: 0.5, Industriousness: 0.5}
	svc, _, _, cleanup := setupTestDialogueService(t, handler, traits, 0.1)
	defer cleanup()

	ctx := context.Background()

	// 1. Clamps counts: count < 1 becomes 1; count > 64 becomes 64; blanks ignored
	currentMock.Store(DialogueResponse{
		SpokenReply:   "Countering offer",
		TradeDecision: "COUNTER",
		CounterOfferItems: []CounterOfferItem{
			{ItemID: "", Count: 10},                     // empty ItemID -> filtered out
			{ItemID: "minecraft:iron_ingot", Count: -5}, // < 1 -> clamped to 1
			{ItemID: "minecraft:bread", Count: 100},     // > 64 -> clamped to 64
		},
	})

	res, err := svc.ProcessTrade(ctx, TradeDialogueRequest{
		PlayerEntityID: 10,
		PlayerUUID:     "trader",
		PlayerName:     "Trader",
		WorldTick:      1000,
	})
	if err != nil {
		t.Fatalf("ProcessTrade failed: %v", err)
	}
	if res.TradeIntent == nil || len(res.TradeIntent.AgentCounterOffer) != 2 {
		t.Fatalf("expected 2 valid counter offer items, got %+v", res.TradeIntent)
	}
	if res.TradeIntent.AgentCounterOffer[0].ItemId != "minecraft:iron_ingot" || res.TradeIntent.AgentCounterOffer[0].Count != 1 {
		t.Errorf("expected 1x iron_ingot, got %+v", res.TradeIntent.AgentCounterOffer[0])
	}
	if res.TradeIntent.AgentCounterOffer[1].ItemId != "minecraft:bread" || res.TradeIntent.AgentCounterOffer[1].Count != 64 {
		t.Errorf("expected 64x bread, got %+v", res.TradeIntent.AgentCounterOffer[1])
	}

	// 2. All items invalid -> fallback to REJECT
	currentMock.Store(DialogueResponse{
		SpokenReply:   "Countering offer",
		TradeDecision: "COUNTER",
		CounterOfferItems: []CounterOfferItem{
			{ItemID: "", Count: 10},
			{ItemID: "   ", Count: 5},
		},
	})
	res, err = svc.ProcessTrade(ctx, TradeDialogueRequest{
		PlayerEntityID: 10,
		PlayerUUID:     "trader",
		PlayerName:     "Trader",
		WorldTick:      2000,
	})
	if err != nil {
		t.Fatalf("ProcessTrade failed: %v", err)
	}
	if res.TradeIntent.AcceptDeal {
		t.Error("expected AcceptDeal false")
	}
	if res.Response.TradeDecision != "REJECT" {
		t.Errorf("expected TradeDecision REJECT, got %s", res.Response.TradeDecision)
	}
	if len(res.TradeIntent.AgentCounterOffer) != 0 {
		t.Errorf("expected 0 counter offers, got %d", len(res.TradeIntent.AgentCounterOffer))
	}
}

func TestDialogueService_GreetingVsDangerousDisobedience(t *testing.T) {
	var currentMock atomic.Value

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock := currentMock.Load().(DialogueResponse)
		respBytes, _ := json.Marshal(mock)
		resp := ChatCompletionResponse{
			ID:      "chatcmpl-disobey",
			Object:  "chat.completion",
			Created: 123456789,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Low bravery (0.1), randFn returns 0.9 (will disobey if triggered)
	traits := xdg.AgentTraits{Bravery: 0.1, Sociability: 0.5, Industriousness: 0.5}
	svc, _, _, cleanup := setupTestDialogueService(t, handler, traits, 0.9)
	defer cleanup()

	ctx := context.Background()

	// 1. Basic greeting under food deficit (SurplusDeficit) MUST NOT be refused
	currentMock.Store(DialogueResponse{
		SpokenReply:   "Hello there, traveler.",
		TradeDecision: "NONE",
		DelegatedTask: nil,
	})
	res, err := svc.ProcessChat(ctx, ChatDialogueRequest{
		PlayerUUID:   "player-greet",
		PlayerName:   "FriendlyPlayer",
		Message:      "Hello! Lovely weather today.",
		SurplusState: SurplusDeficit,
	})
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}
	if res.Disobeyed {
		t.Errorf("expected greeting not to be disobeyed during deficit, got disobedience: %s", res.DisobeyReason)
	}
	if res.Response.SpokenReply != "Hello there, traveler." {
		t.Errorf("unexpected reply: %s", res.Response.SpokenReply)
	}

	// 2. Chat with delegated task under food deficit SHOULD be refused
	currentMock.Store(DialogueResponse{
		SpokenReply:   "I can help gather wheat.",
		TradeDecision: "NONE",
		DelegatedTask: &DelegatedTask{
			TaskType: "GATHER_RESOURCE",
		},
	})
	res, err = svc.ProcessChat(ctx, ChatDialogueRequest{
		PlayerUUID:   "player-task",
		PlayerName:   "TaskMaster",
		Message:      "Can you gather some wood for the clan?",
		SurplusState: SurplusDeficit,
	})
	if err != nil {
		t.Fatalf("ProcessChat failed: %v", err)
	}
	if !res.Disobeyed {
		t.Error("expected delegated task to be disobeyed during food deficit")
	}

	// 3. Word boundary danger test: "skill" is NOT dangerous, "kill" IS dangerous
	if isDangerousText("You have great skill in farming") {
		t.Error("expected 'skill' not to trigger danger detection")
	}
	if !isDangerousText("Go and kill the creeper") {
		t.Error("expected 'kill' and 'creeper' to trigger danger detection")
	}
	if !isDangerousText("attack the enemy") {
		t.Error("expected 'attack' to trigger danger detection")
	}
}

func TestDialogueService_CloseOwnedReputation(t *testing.T) {
	tempDir := t.TempDir()
	repDBPath := filepath.Join(tempDir, "reputation_owned.db")
	repDB, err := db.OpenReputationDB(repDBPath, "clan_owned")
	if err != nil {
		t.Fatalf("failed to open reputation db: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	server := httptest.NewServer(handler)
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1, RequestTimeoutSeconds: 5},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	svc, err := NewDialogueService(
		client,
		&xdg.AgentIdentityConfig{Name: "settler"},
		WithOwnedReputation(repDB),
	)
	if err != nil {
		t.Fatalf("failed to create dialogue service: %v", err)
	}

	// Verify reputation is open
	ctx := context.Background()
	if _, err := svc.ReputationDB().GetHeroPrestige(ctx, "test"); err != nil {
		t.Fatalf("GetHeroPrestige error: %v", err)
	}

	// Close dialogue service -> closes reputation db
	if err := svc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Subsequent call to reputation returns ErrClosed
	if _, err := repDB.GetHeroPrestige(ctx, "test"); err != db.ErrClosed {
		t.Errorf("expected ErrClosed after svc.Close(), got %v", err)
	}
}
