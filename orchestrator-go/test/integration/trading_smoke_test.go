package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Capability smoke tests for trading end to end (cq-71l.5): feat.trading, intent.trade_response,
// s1.evaluator.trade, s2.trade_negotiation. Two agents drive the real runtime / dialogue
// service; the inventory exchange is applied by a tiny in-test "world" that mirrors what the
// Java TradeHandler does with a RespondTradeIntent.

func newTradeRuntime(t *testing.T, id, profession string) *agent.AgentRuntime {
	t.Helper()
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	if err := r.InitAgentDirectories(id); err != nil {
		t.Fatalf("init dirs: %v", err)
	}
	cfg := xdg.DefaultAgentIdentityConfig(id)
	cfg.ClanID = "pioneers"
	cfg.Profession = profession
	// The dispatcher is gated on a dialogue service being attached; deterministic
	// [SYSTEM1_TRADE] offers never reach this (unreachable) LLM endpoint.
	client, err := system2.NewClientWithConfig(xdg.LLMGatewayConfig{BaseURL: "http://127.0.0.1:1"},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1, RequestTimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	dsvc, err := system2.NewDialogueService(client, &xdg.AgentIdentityConfig{Name: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dsvc.Close() })
	rt, err := agent.NewRuntime(agent.Config{AgentID: id, ClanID: "pioneers", IdentityConfig: cfg, Resolver: r, IntentQueueSize: 16, DialogueService: dsvc})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	return rt
}

type tradeInv map[string]int32

func (inv tradeInv) apply(give, take []*civquestv1.ItemStackProto, to tradeInv) bool {
	for _, s := range give {
		if inv[s.ItemId] < s.Count {
			return false
		}
	}
	for _, s := range give {
		inv[s.ItemId] -= s.Count
		to[s.ItemId] += s.Count
	}
	return true
}

func (inv tradeInv) stacks() []*civquestv1.ItemStackProto {
	var out []*civquestv1.ItemStackProto
	i := int32(0)
	for id, n := range inv {
		if n > 0 {
			out = append(out, &civquestv1.ItemStackProto{ItemId: id, Count: n, Slot: i})
			i++
		}
	}
	return out
}

func tradeIntentFrom(t *testing.T, rt *agent.AgentRuntime) *civquestv1.RespondTradeIntent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case in := <-rt.IntentQueue():
			if tr := in.GetTradeResponse(); tr != nil {
				return tr
			}
		case <-deadline:
			t.Fatal("no TradeResponse intent emitted")
		}
	}
}

func TestIntegration_Trading_System1MerchantAcceptsFairBarterAndItemsSwap(t *testing.T) {
	merchant := newTradeRuntime(t, "merchant_agent", "FARMER")
	invM := tradeInv{"minecraft:bread": 32, "minecraft:iron_sword": 1}
	invP := tradeInv{"minecraft:emerald": 8}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		Status: &civquestv1.AgentStatus{Health: 20, MaxHealth: 20, FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Inventory: invM.stacks()},
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 7, PlayerUuid: "player-b", PlayerName: "Bea",
			PlayerOfferedItems: []*civquestv1.ItemStackProto{{ItemId: "minecraft:emerald", Count: 2}},
			SpokenProposal:     "[SYSTEM1_TRADE] two emeralds for bread",
		},
	}
	merchant.HandleTelemetryFrame(frame)
	resp := tradeIntentFrom(t, merchant)

	if !resp.AcceptDeal {
		t.Fatalf("merchant should accept a fair offer: %+v", resp)
	}
	if resp.PlayerEntityId != 7 {
		t.Fatalf("response must address the requesting entity, got %d", resp.PlayerEntityId)
	}
	if len(resp.AgentCounterOffer) == 0 {
		t.Fatalf("expected the merchant to put goods on the table: %+v", resp)
	}

	// Exchange (mirrors Java TradeHandler): player gives offered, merchant gives counter-offer.
	if !invP.apply(frame.PendingTrade.PlayerOfferedItems, nil, invM) {
		t.Fatal("player could not afford its own offer")
	}
	if !invM.apply(resp.AgentCounterOffer, nil, invP) {
		t.Fatalf("merchant could not cover its counter-offer %+v from %v", resp.AgentCounterOffer, invM)
	}
	if invM["minecraft:emerald"] != 2 || invP["minecraft:emerald"] != 6 {
		t.Fatalf("emeralds did not move to the merchant: M=%v P=%v", invM, invP)
	}
	got := int32(0)
	for _, s := range resp.AgentCounterOffer {
		got += invP[s.ItemId]
	}
	if got == 0 {
		t.Fatalf("player received nothing in exchange: P=%v", invP)
	}
}

func TestIntegration_Trading_System1RejectsEmptyOffer(t *testing.T) {
	merchant := newTradeRuntime(t, "merchant_empty", "FARMER")
	merchant.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 1000,
		Status:    &civquestv1.AgentStatus{Health: 20, MaxHealth: 20, FoodLevel: 20, Position: &civquestv1.Vec3{Y: 64}},
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 9, PlayerUuid: "player-c", PlayerName: "Cy",
			SpokenProposal: "[SYSTEM1_TRADE] nothing for something",
		},
	})
	if resp := tradeIntentFrom(t, merchant); resp.AcceptDeal || len(resp.AgentCounterOffer) != 0 {
		t.Fatalf("empty offer must be rejected without goods: %+v", resp)
	}
}

// Negotiation through the System 2 dialogue service with a stubbed LLM: ACCEPT then COUNTER,
// each decision turned into a TradeResponse and (for ACCEPT) an actual swap.
func TestIntegration_Trading_System2NegotiationAcceptThenCounter(t *testing.T) {
	var decision = `{"spoken_reply":"Deal!","trade_decision":"ACCEPT","counter_offer_items":[],"prestige_delta":2.0}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{Choices: []system2.ChatCompletionChoice{
			{Message: system2.ChatMessage{Role: system2.RoleAssistant, Content: decision}},
		}})
	}))
	defer srv.Close()
	client, err := system2.NewClientWithConfig(xdg.LLMGatewayConfig{BaseURL: srv.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 2, RequestTimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	svc, err := system2.NewDialogueService(client, &xdg.AgentIdentityConfig{Name: "negotiator"})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	req := system2.TradeDialogueRequest{
		PlayerEntityID: 5, PlayerUUID: "p", PlayerName: "Pat",
		OfferedItems:   []*civquestv1.ItemStackProto{{ItemId: "minecraft:emerald", Count: 3}},
		SpokenProposal: "3 emeralds for carrots?", WorldTick: 6000, SurplusState: system2.SurplusStable,
	}
	res, err := svc.ProcessTrade(context.Background(), req)
	if err != nil || res.TradeIntent == nil || !res.TradeIntent.AcceptDeal {
		t.Fatalf("expected ACCEPT intent, got %+v err=%v", res, err)
	}
	invN := tradeInv{"minecraft:carrot": 16}
	invP := tradeInv{"minecraft:emerald": 3}
	invP.apply(req.OfferedItems, nil, invN)
	if invN["minecraft:emerald"] != 3 || invP["minecraft:emerald"] != 0 {
		t.Fatalf("accepted deal must move the offered items: N=%v P=%v", invN, invP)
	}

	decision = `{"spoken_reply":"Need more.","trade_decision":"COUNTER","counter_offer_items":[{"item_id":"minecraft:emerald","count":5}],"prestige_delta":0}`
	req.WorldTick = 7000
	res, err = svc.ProcessTrade(context.Background(), req)
	if err != nil || res.TradeIntent == nil {
		t.Fatalf("expected COUNTER intent, got %+v err=%v", res, err)
	}
	if res.TradeIntent.AcceptDeal || len(res.TradeIntent.AgentCounterOffer) != 1 || res.TradeIntent.AgentCounterOffer[0].Count != 5 {
		t.Fatalf("counter must demand 5 emeralds without accepting: %+v", res.TradeIntent)
	}
}
