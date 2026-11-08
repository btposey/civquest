package system2

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// TradeValuationAdjustment represents a single LLM-reassessed item valuation threshold.
type TradeValuationAdjustment struct {
	ItemID         string  `json:"item_id"`
	BaseValue      float64 `json:"base_value"`
	NeedMultiplier float64 `json:"need_multiplier"`
	MinAcceptRatio float64 `json:"min_accept_ratio"`
	Rationale      string  `json:"rationale"`
}

// TradeReassessmentResponse is the structured JSON output from System 2 periodic trade table reassessment.
type TradeReassessmentResponse struct {
	Adjustments []TradeValuationAdjustment `json:"adjustments"`
	Summary     string                     `json:"summary"`
}

// ReassessTradeValuations invokes System 2 LLM reasoning asynchronously to reassess and update
// the agent's persisted System 1 quid-pro-quo trade valuation table as needs, inventory, and goals evolve.
func (s *DialogueService) ReassessTradeValuations(
	ctx context.Context,
	agentDB *db.AgentDB,
	vitals *DialogueVitals,
	worldTick int64,
) ([]db.TradeValuation, error) {
	if s == nil {
		return nil, fmt.Errorf("dialogue service is nil")
	}

	s.mu.RLock()
	client := s.client
	agentCfg := s.agentConfig
	memPipe := s.memory
	s.mu.RUnlock()

	if client == nil || agentCfg == nil {
		return nil, fmt.Errorf("dialogue client or agentConfig is nil")
	}

	memText := "None recorded."
	if memPipe != nil {
		if retrieved := retrieveCombinedMemories(ctx, memPipe, "trade barter supplies equipment hunger survival", 4); retrieved != "" {
			memText = retrieved
		}
	}

	envSection := formatSensoryEnvironmentSection(vitals, SurplusStable)

	sysPrompt := fmt.Sprintf(`You are the deliberative economic planner (System 2) for %s, an autonomous %s of clan %s.
Review the agent's current vitals, inventory, profession goals, and recent memories, and output updated quid-pro-quo valuation multipliers for System 1 deterministic trade execution.

%s
<recent_memories>
%s
</recent_memories>

Return ONLY a valid JSON object matching this schema:
{
  "adjustments": [
    {
      "item_id": "minecraft:iron_sword",
      "base_value": 14.0,
      "need_multiplier": 1.8,
      "min_accept_ratio": 0.80,
      "rationale": "short reason"
    }
  ],
  "summary": "1-sentence summary of economic priorities"
}`, agentCfg.Name, agentCfg.Profession, agentCfg.ClanID, envSection, memText)

	messages := []ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: "Reassess up to 6 key item valuations (weapons, shield, tools, food, torches, maps, or profession supplies) based on current inventory and survival needs."},
	}

	var parsed TradeReassessmentResponse
	if err := client.CompleteStructured(ctx, messages, &parsed); err != nil {
		return nil, err
	}

	var updated []db.TradeValuation
	for _, adj := range parsed.Adjustments {
		itemID := strings.ToLower(strings.TrimSpace(adj.ItemID))
		if itemID == "" {
			continue
		}
		if !strings.Contains(itemID, ":") {
			itemID = "minecraft:" + itemID
		}
		baseVal := math.Max(0.1, math.Min(100.0, adj.BaseValue))
		needMult := math.Max(0.2, math.Min(4.0, adj.NeedMultiplier))
		minRatio := math.Max(0.5, math.Min(1.5, adj.MinAcceptRatio))
		updated = append(updated, db.TradeValuation{
			ItemID:         itemID,
			BaseValue:      baseVal,
			NeedMultiplier: needMult,
			MinAcceptRatio: minRatio,
			Rationale:      strings.TrimSpace(adj.Rationale),
			UpdatedTick:    worldTick,
		})
	}

	if agentDB != nil && len(updated) > 0 {
		_ = agentDB.UpsertTradeValuationsBatch(ctx, updated)
	}
	return updated, nil
}
