package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestEvaluateTradeQuidProQuo(t *testing.T) {
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 2000,
		Status: &civquestv1.AgentStatus{
			Health:    20,
			FoodLevel: 12,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 1, ItemId: "minecraft:oak_log", Count: 8},
			},
		},
	}
	req := &civquestv1.TradeRequest{
		PlayerEntityId: 7,
		PlayerUuid:     "player-1",
		PlayerName:     "Brian",
		SpokenProposal: "[SYSTEM1_TRADE] iron sword",
		PlayerOfferedItems: []*civquestv1.ItemStackProto{
			{Slot: 0, ItemId: "minecraft:iron_sword", Count: 1},
		},
	}

	dec := EvaluateTradeQuidProQuo(req, frame, "EXPLORER", 10.0, nil)
	if !dec.AcceptDeal || dec.Decision != "ACCEPT" {
		t.Fatalf("expected unarmed explorer to accept iron_sword offer, got %+v", dec)
	}
	if dec.TradeIntent == nil || !dec.TradeIntent.AcceptDeal {
		t.Fatalf("expected RespondTradeIntent with AcceptDeal=true, got %+v", dec.TradeIntent)
	}
	if len(dec.CounterOffer) == 0 {
		t.Fatalf("expected counter-offer from surplus oak_log, got empty counter offer")
	}
}
