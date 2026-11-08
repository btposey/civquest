package system2

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/memory"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// SurplusState describes the clan's granary and food supply status.
type SurplusState string

const (
	SurplusDeficit SurplusState = "DEFICIT"
	SurplusStable  SurplusState = "STABLE"
	SurplusSurplus SurplusState = "SURPLUS"
)

// DialogueVitals encapsulates an agent's physical status for prompt context.
type DialogueVitals struct {
	Health    float32 `json:"health"`
	FoodLevel int32   `json:"food_level"`
}

// ChatDialogueRequest represents an incoming verbal chat event from a player.
type ChatDialogueRequest struct {
	PlayerUUID   string          `json:"player_uuid"`
	PlayerName   string          `json:"player_name"`
	Message      string          `json:"message"`
	Distance     float32         `json:"distance"`
	WorldTick    int64           `json:"world_tick"`
	Vitals       *DialogueVitals `json:"vitals,omitempty"`
	SurplusState SurplusState    `json:"surplus_state,omitempty"`
	Dangerous    bool            `json:"dangerous,omitempty"`
}

// TradeDialogueRequest represents an active barter trade negotiation initiated by a player.
type TradeDialogueRequest struct {
	PlayerEntityID int32                        `json:"player_entity_id"`
	PlayerUUID     string                       `json:"player_uuid"`
	PlayerName     string                       `json:"player_name"`
	OfferedItems   []*civquestv1.ItemStackProto `json:"offered_items,omitempty"`
	SpokenProposal string                       `json:"spoken_proposal"`
	WorldTick      int64                        `json:"world_tick"`
	Vitals         *DialogueVitals              `json:"vitals,omitempty"`
	SurplusState   SurplusState                 `json:"surplus_state,omitempty"`
	Dangerous      bool                         `json:"dangerous,omitempty"`
}

// CounterOfferItem represents an item offered or demanded in barter trade.
type CounterOfferItem struct {
	ItemID string `json:"item_id"`
	Count  int32  `json:"count"`
}

// DelegatedTask represents an optional player task delegation.
type DelegatedTask struct {
	TaskType          string `json:"task_type"` // NONE, ESCORT_PLAYER, GATHER_RESOURCE, SHARE_CROP_PROPOSAL
	TargetBlockOrItem string `json:"target_block_or_item,omitempty"`
	Quota             int32  `json:"quota,omitempty"`
}

// DialogueResponse is the structured JSON output conforming to doc/ai/05 Pipeline A.
type DialogueResponse struct {
	SpokenReply       string             `json:"spoken_reply"`
	TradeDecision     string             `json:"trade_decision"` // ACCEPT | COUNTER | REJECT | NONE
	CounterOfferItems []CounterOfferItem `json:"counter_offer_items"`
	DelegatedTask     *DelegatedTask     `json:"delegated_task,omitempty"`
	PrestigeDelta     float64            `json:"prestige_delta"`
	MemorySummary     string             `json:"memory_summary"`
}

// DialogueResult is the complete evaluated outcome of a dialogue or trade turn.
type DialogueResult struct {
	Response        DialogueResponse
	Disobeyed       bool
	DisobeyReason   string
	ChatIntent      *civquestv1.SendChatIntent
	TradeIntent     *civquestv1.RespondTradeIntent
	UpdatedPrestige float64
}

// DialogueOption provides functional configuration for DialogueService.
type DialogueOption func(*DialogueService)

// WithDialogueMemory assigns an episodic vector RAG memory pipeline.
func WithDialogueMemory(mem *memory.Pipeline) DialogueOption {
	return func(s *DialogueService) {
		s.memory = mem
	}
}

// WithDialogueReputation assigns the clan reputation database for Hero Prestige.
func WithDialogueReputation(rep *db.ReputationDB) DialogueOption {
	return func(s *DialogueService) {
		s.reputation = rep
	}
}

// WithOwnedReputation assigns the clan reputation database and marks it as owned,
// ensuring DialogueService.Close() closes the underlying database.
func WithOwnedReputation(rep *db.ReputationDB) DialogueOption {
	return func(s *DialogueService) {
		s.reputation = rep
		s.ownsReputation = true
	}
}

// WithDialogueRand overrides the random number generator function for stochastic checks.
func WithDialogueRand(randFn func() float64) DialogueOption {
	return func(s *DialogueService) {
		if randFn != nil {
			s.randFn = randFn
		}
	}
}

// DialogueService orchestrates Pipeline A: conversational dialogue, barter haggling,
// Hero Prestige progression, and stochastic disobedience evaluation.
type DialogueService struct {
	mu             sync.RWMutex
	client         *Client
	memory         *memory.Pipeline
	reputation     *db.ReputationDB
	agentConfig    *xdg.AgentIdentityConfig
	randFn         func() float64
	ownsReputation bool
	isClosed       bool
}

// NewDialogueService constructs a new DialogueService instance.
func NewDialogueService(client *Client, agentConfig *xdg.AgentIdentityConfig, opts ...DialogueOption) (*DialogueService, error) {
	if client == nil {
		return nil, ErrNilConfig
	}
	if agentConfig == nil {
		agentConfig = &xdg.AgentIdentityConfig{
			Name:       "settler",
			ClanID:     "clan_default",
			Profession: "VILLAGER",
			Traits: xdg.AgentTraits{
				Bravery:         0.5,
				Industriousness: 0.5,
				Sociability:     0.5,
				Curiosity:       0.5,
				Aggressiveness:  0.2,
			},
		}
	}

	svc := &DialogueService{
		client:      client,
		agentConfig: agentConfig,
		randFn:      rand.Float64,
	}

	for _, opt := range opts {
		opt(svc)
	}

	return svc, nil
}

// Client returns the underlying LLM gateway client.
func (s *DialogueService) Client() *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// ReputationDB returns the clan reputation database handle.
func (s *DialogueService) ReputationDB() *db.ReputationDB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reputation
}

// MemoryPipeline returns the episodic memory pipeline handle.
func (s *DialogueService) MemoryPipeline() *memory.Pipeline {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memory
}

// Close gracefully closes any underlying resources owned by DialogueService (such as reputation.db).
func (s *DialogueService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		return nil
	}
	s.isClosed = true
	if s.ownsReputation && s.reputation != nil {
		return s.reputation.Close()
	}
	return nil
}

// ProcessChat handles an incoming chat message, querying historical context,
// generating structured response via LLM, evaluating disobedience, updating prestige,
// and ingesting episodic memory.
func (s *DialogueService) ProcessChat(ctx context.Context, req ChatDialogueRequest) (*DialogueResult, error) {
	s.mu.RLock()
	client := s.client
	agentCfg := s.agentConfig
	repDB := s.reputation
	memPipe := s.memory
	randFn := s.randFn
	s.mu.RUnlock()

	// 1. Resolve player Hero Prestige
	var currentPrestige float64
	prestigeTitle := db.PrestigeTitleStranger
	if repDB != nil && req.PlayerUUID != "" {
		if hp, err := repDB.GetHeroPrestigeOrDefault(ctx, req.PlayerUUID); err == nil && hp != nil {
			currentPrestige = hp.Score
			prestigeTitle = hp.Title
		}
	}

	// 2. Retrieve episodic memories
	var historicalContext string
	if memPipe != nil {
		query := fmt.Sprintf("%s: %s", req.PlayerName, req.Message)
		if matches, err := memPipe.Retrieve(ctx, query, 3); err == nil && len(matches) > 0 {
			historicalContext = memPipe.FormatPromptContext(matches)
		}
	}

	// 3. Assemble prompt
	messages := s.buildChatPrompt(req, agentCfg, currentPrestige, prestigeTitle, historicalContext)

	// 4. Execute LLM structured completion with creative temperature (T=0.85)
	var resp DialogueResponse
	creativeTemp := client.CreativeTemperature()
	if creativeTemp <= 0 {
		creativeTemp = DefaultCreativeTemperature
	}
	if err := client.CompleteStructuredWithTemp(ctx, messages, &resp, creativeTemp); err != nil {
		return nil, fmt.Errorf("llm dialogue completion: %w", err)
	}

	// 5. Stochastic disobedience check
	// Disobedience on chat only applies if the player delegates a task or the request is dangerous.
	// Basic greetings are never refused.
	hasTaskDelegation := resp.DelegatedTask != nil && resp.DelegatedTask.TaskType != "" && resp.DelegatedTask.TaskType != "NONE"
	isDangerous := req.Dangerous || isDangerousText(req.Message)
	isDeficit := req.SurplusState == SurplusDeficit
	disobeyed := false
	var disobeyReason string

	if isDangerous || (isDeficit && hasTaskDelegation) {
		roll := randFn()
		bravery := agentCfg.Traits.Bravery
		if roll > bravery {
			disobeyed = true
			if isDeficit && isDangerous {
				disobeyReason = "Refused dangerous request under clan food deficit"
				resp.SpokenReply = "I cannot do that! Our granaries are empty, and risking my life right now would be madness."
			} else if isDeficit {
				disobeyReason = "Refused delegated task due to clan food deficit"
				resp.SpokenReply = "I must refuse that task. Our clan's food stores are dangerously depleted; we cannot spare time or resources."
			} else {
				disobeyReason = "Refused dangerous order due to self-preservation"
				resp.SpokenReply = "I refuse! That sounds far too dangerous. I am not throwing my life away."
			}
			if resp.DelegatedTask != nil {
				resp.DelegatedTask.TaskType = "NONE"
			}
		}
	}

	// 6. Clamp prestige delta [-10.0, 10.0] and update Hero Prestige if delta is non-zero
	resp.PrestigeDelta = clampPrestigeDelta(resp.PrestigeDelta, -10.0, 10.0)
	updatedPrestige := currentPrestige
	if repDB != nil && req.PlayerUUID != "" && resp.PrestigeDelta != 0.0 {
		cleanName := sanitizePlayerName(req.PlayerName)
		entry := db.PrestigeLedgerEntry{
			PlayerUUID: req.PlayerUUID,
			PlayerName: cleanName,
			AgentID:    agentCfg.Name,
			Delta:      resp.PrestigeDelta,
			Reason:     resp.MemorySummary,
			WorldTick:  req.WorldTick,
		}
		if entry.Reason == "" {
			entry.Reason = fmt.Sprintf("Chat interaction with %s", cleanName)
		}
		if newScore, err := repDB.RecordPrestigeDelta(ctx, entry); err == nil {
			updatedPrestige = newScore
		}
	}

	// 7. Ingest episodic memory
	if memPipe != nil {
		summary := resp.MemorySummary
		if summary == "" {
			summary = fmt.Sprintf("Spoke with %s: %s", req.PlayerName, resp.SpokenReply)
		}
		_, _ = memPipe.Ingest(ctx, memory.MemoryEvent{
			WorldTick:     req.WorldTick,
			Category:      memory.CategoryChat,
			Actors:        []string{agentCfg.Name, req.PlayerName},
			SummaryText:   summary,
			SaliencyScore: memory.SaliencyChat,
		})
	}

	// 8. Assemble result
	if resp.SpokenReply == "" {
		resp.SpokenReply = "..."
	}

	result := &DialogueResult{
		Response:        resp,
		Disobeyed:       disobeyed,
		DisobeyReason:   disobeyReason,
		UpdatedPrestige: updatedPrestige,
		ChatIntent: &civquestv1.SendChatIntent{
			Message:       resp.SpokenReply,
			TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
		},
	}

	return result, nil
}

// ProcessTrade handles an incoming barter trade request, negotiating offers based on
// settler traits, clan food surplus, player prestige, and past memories.
func (s *DialogueService) ProcessTrade(ctx context.Context, req TradeDialogueRequest) (*DialogueResult, error) {
	s.mu.RLock()
	client := s.client
	agentCfg := s.agentConfig
	repDB := s.reputation
	memPipe := s.memory
	randFn := s.randFn
	s.mu.RUnlock()

	// 1. Resolve player Hero Prestige
	var currentPrestige float64
	prestigeTitle := db.PrestigeTitleStranger
	if repDB != nil && req.PlayerUUID != "" {
		if hp, err := repDB.GetHeroPrestigeOrDefault(ctx, req.PlayerUUID); err == nil && hp != nil {
			currentPrestige = hp.Score
			prestigeTitle = hp.Title
		}
	}

	// 2. Retrieve episodic memories
	var historicalContext string
	if memPipe != nil {
		query := fmt.Sprintf("trade with %s: %s", req.PlayerName, req.SpokenProposal)
		if matches, err := memPipe.Retrieve(ctx, query, 3); err == nil && len(matches) > 0 {
			historicalContext = memPipe.FormatPromptContext(matches)
		}
	}

	// 3. Assemble trade prompt
	messages := s.buildTradePrompt(req, agentCfg, currentPrestige, prestigeTitle, historicalContext)

	// 4. Execute LLM structured completion with creative temperature (T=0.85)
	var resp DialogueResponse
	creativeTemp := client.CreativeTemperature()
	if creativeTemp <= 0 {
		creativeTemp = DefaultCreativeTemperature
	}
	if err := client.CompleteStructuredWithTemp(ctx, messages, &resp, creativeTemp); err != nil {
		return nil, fmt.Errorf("llm trade completion: %w", err)
	}

	// 5. Stochastic disobedience / refusal check
	isDangerous := req.Dangerous || isDangerousText(req.SpokenProposal)
	isDeficit := req.SurplusState == SurplusDeficit
	disobeyed := false
	var disobeyReason string

	if isDangerous || isDeficit {
		roll := randFn()
		bravery := agentCfg.Traits.Bravery
		if roll > bravery {
			disobeyed = true
			if isDeficit {
				disobeyReason = "Refused trade due to clan food deficit"
				resp.TradeDecision = "REJECT"
				resp.CounterOfferItems = nil
				resp.SpokenReply = "Our clan granary is in deficit; we cannot spare our food or resources right now."
			} else {
				disobeyReason = "Refused dangerous barter proposal"
				resp.TradeDecision = "REJECT"
				resp.CounterOfferItems = nil
				resp.SpokenReply = "That trade demands far too high a risk. Deal is off."
			}
			if resp.DelegatedTask != nil {
				resp.DelegatedTask.TaskType = "NONE"
			}
		}
	}

	// 6. Clamp prestige delta [-5.0, 5.0] and update Hero Prestige if delta is non-zero
	resp.PrestigeDelta = clampPrestigeDelta(resp.PrestigeDelta, -5.0, 5.0)
	updatedPrestige := currentPrestige
	if repDB != nil && req.PlayerUUID != "" && resp.PrestigeDelta != 0.0 {
		cleanName := sanitizePlayerName(req.PlayerName)
		entry := db.PrestigeLedgerEntry{
			PlayerUUID: req.PlayerUUID,
			PlayerName: cleanName,
			AgentID:    agentCfg.Name,
			Delta:      resp.PrestigeDelta,
			Reason:     resp.MemorySummary,
			WorldTick:  req.WorldTick,
		}
		if entry.Reason == "" {
			entry.Reason = fmt.Sprintf("Barter trade with %s (%s)", cleanName, resp.TradeDecision)
		}
		if newScore, err := repDB.RecordPrestigeDelta(ctx, entry); err == nil {
			updatedPrestige = newScore
		}
	}

	// 7. Ingest episodic memory
	if memPipe != nil {
		summary := resp.MemorySummary
		if summary == "" {
			summary = fmt.Sprintf("Barter with %s: decision %s, reply: %s", req.PlayerName, resp.TradeDecision, resp.SpokenReply)
		}
		_, _ = memPipe.Ingest(ctx, memory.MemoryEvent{
			WorldTick:     req.WorldTick,
			Category:      memory.CategoryTrade,
			Actors:        []string{agentCfg.Name, req.PlayerName},
			SummaryText:   summary,
			SaliencyScore: memory.SaliencyTrade,
		})
	}

	// 8. Validate counter-offers and assemble result
	var counterOffers []*civquestv1.ItemStackProto
	if !disobeyed && strings.EqualFold(resp.TradeDecision, "COUNTER") {
		for _, item := range resp.CounterOfferItems {
			itemID := strings.TrimSpace(item.ItemID)
			if itemID == "" {
				continue
			}
			count := item.Count
			if count < 1 {
				count = 1
			} else if count > 64 {
				count = 64
			}
			counterOffers = append(counterOffers, &civquestv1.ItemStackProto{
				ItemId: itemID,
				Count:  count,
			})
		}
		if len(counterOffers) == 0 {
			resp.TradeDecision = "REJECT"
			if resp.SpokenReply == "" || strings.EqualFold(resp.SpokenReply, "Trade decision: COUNTER") {
				resp.SpokenReply = "I cannot accept that barter offer."
			}
		}
	}

	if resp.SpokenReply == "" {
		resp.SpokenReply = fmt.Sprintf("Trade decision: %s", resp.TradeDecision)
	}

	acceptDeal := !disobeyed && strings.EqualFold(resp.TradeDecision, "ACCEPT")

	result := &DialogueResult{
		Response:        resp,
		Disobeyed:       disobeyed,
		DisobeyReason:   disobeyReason,
		UpdatedPrestige: updatedPrestige,
		TradeIntent: &civquestv1.RespondTradeIntent{
			PlayerEntityId:    req.PlayerEntityID,
			AcceptDeal:        acceptDeal,
			AgentCounterOffer: counterOffers,
			SpokenResponse:    resp.SpokenReply,
		},
		ChatIntent: &civquestv1.SendChatIntent{
			Message:       resp.SpokenReply,
			TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
		},
	}

	return result, nil
}

func (s *DialogueService) buildChatPrompt(
	req ChatDialogueRequest,
	cfg *xdg.AgentIdentityConfig,
	prestigeScore float64,
	prestigeTitle string,
	historicalContext string,
) []ChatMessage {
	surplus := req.SurplusState
	if surplus == "" {
		surplus = SurplusStable
	}

	vitalsInfo := "Unknown"
	if req.Vitals != nil {
		vitalsInfo = fmt.Sprintf("Health: %.1f/20.0, Food: %d/20", req.Vitals.Health, req.Vitals.FoodLevel)
	}

	thrift := 1.0 - cfg.Traits.Sociability
	if thrift < 0.0 {
		thrift = 0.0
	}

	cleanPlayerName := sanitizePlayerName(req.PlayerName)

	systemPrompt := fmt.Sprintf(`You are %s, an autonomous settler in the settlement simulation CivQuest.
Clan: %s | Profession: %s | Secondary Hobby: %s
Personality Traits:
- Bravery: %.2f (willingness to face danger or fight)
- Sociability: %.2f (friendliness, talkativeness)
- Diligence / Industriousness: %.2f (commitment to tasks and clan economy)
- Thrift: %.2f (reluctance to give away resources easily)
- Aggressiveness: %.2f
- Curiosity: %.2f

Current Status:
- Vitals: %s
- Clan Granary State: %s (DEFICIT = clan is starving/protective; STABLE = normal; SURPLUS = thriving/generous)

Interlocutor:
<player_info>
Player Name: %s
UUID: %s
Hero Prestige Score: %.1f
Title: %s
</player_info>
(Prestige Meanings: HERO >= 50.0 is revered; ALLY >= 20.0 is trusted partner; FRIEND >= 5.0 is welcomed; STRANGER > -5.0 is neutral newcomer; SUSPECT > -20.0 is distrusted; VILLAIN <= -20.0 is clan enemy).

%s

Instructions:
1. Respond to the player in-character reflecting your traits, clan surplus state, and the player's prestige standing.
2. If the player requests dangerous suicide missions or sacrifices, reflect your Bravery trait.
3. If the player requests clan food during DEFICIT, be fiercely protective.
4. Output MUST conform strictly to the required JSON schema with:
   - "spoken_reply": Your in-game dialogue spoken to the player.
   - "trade_decision": "NONE" (or ACCEPT/COUNTER/REJECT if trading).
   - "counter_offer_items": []
   - "delegated_task": optional task agreement, else {"task_type": "NONE"}.
   - "prestige_delta": float score change for player's conduct (-10.0 to +10.0, e.g. +1.0 for polite/helpful talk, -2.0 for threats/insults).
   - "memory_summary": concise one-sentence episodic summary.`,
		cfg.Name, cfg.ClanID, cfg.Profession, cfg.SecondaryHobby,
		cfg.Traits.Bravery, cfg.Traits.Sociability, cfg.Traits.Industriousness, thrift, cfg.Traits.Aggressiveness, cfg.Traits.Curiosity,
		vitalsInfo, surplus,
		cleanPlayerName, req.PlayerUUID, prestigeScore, prestigeTitle,
		formatMemorySection(historicalContext),
	)

	if cfg.SystemPrompt != "" {
		systemPrompt += "\n\nAdditional Settler Background:\n" + cfg.SystemPrompt
	}

	userMsg := fmt.Sprintf("<player_chat player=%q distance=%.1f>\n%s\n</player_chat>", cleanPlayerName, req.Distance, req.Message)

	return []ChatMessage{
		{Role: RoleSystem, Content: systemPrompt},
		{Role: RoleUser, Content: userMsg},
	}
}

func (s *DialogueService) buildTradePrompt(
	req TradeDialogueRequest,
	cfg *xdg.AgentIdentityConfig,
	prestigeScore float64,
	prestigeTitle string,
	historicalContext string,
) []ChatMessage {
	surplus := req.SurplusState
	if surplus == "" {
		surplus = SurplusStable
	}

	vitalsInfo := "Unknown"
	if req.Vitals != nil {
		vitalsInfo = fmt.Sprintf("Health: %.1f/20.0, Food: %d/20", req.Vitals.Health, req.Vitals.FoodLevel)
	}

	thrift := 1.0 - cfg.Traits.Sociability
	if thrift < 0.0 {
		thrift = 0.0
	}

	var itemsSummary strings.Builder
	if len(req.OfferedItems) == 0 {
		itemsSummary.WriteString("Nothing offered yet")
	} else {
		for i, item := range req.OfferedItems {
			if i > 0 {
				itemsSummary.WriteString(", ")
			}
			itemsSummary.WriteString(fmt.Sprintf("%dx %s", item.Count, item.ItemId))
		}
	}

	cleanPlayerName := sanitizePlayerName(req.PlayerName)

	systemPrompt := fmt.Sprintf(`You are %s, an autonomous settler negotiating barter trade in CivQuest.
Clan: %s | Profession: %s | Secondary Hobby: %s
Personality Traits:
- Bravery: %.2f
- Sociability: %.2f
- Diligence / Industriousness: %.2f
- Thrift: %.2f (higher thrift means demanding better barter margins)
- Aggressiveness: %.2f

Status & Economy:
- Vitals: %s
- Clan Granary State: %s (DEFICIT = protect food and high-value supplies; SURPLUS = generous trades)

Trading Partner:
<player_info>
Player Name: %s
UUID: %s
Hero Prestige Score: %.1f
Title: %s
</player_info>
(HERO/ALLY receive generous barter discounts; STRANGER receives fair market rates; SUSPECT/VILLAIN are charged exorbitant premiums or refused outright).

%s

Instructions:
1. Evaluate the player's barter offer and proposal.
2. Choose trade_decision: "ACCEPT", "COUNTER", or "REJECT".
3. If COUNTER, specify counter_offer_items: [{"item_id": "...", "count": N}].
4. Output MUST conform strictly to the required JSON schema with:
   - "spoken_reply": Your in-character negotiation remark.
   - "trade_decision": "ACCEPT" | "COUNTER" | "REJECT" | "NONE".
   - "counter_offer_items": list of counter-offered items.
   - "delegated_task": {"task_type": "NONE"}.
   - "prestige_delta": float score change for player's generosity or greed (-5.0 to +5.0).
   - "memory_summary": concise one-sentence episodic trade summary.`,
		cfg.Name, cfg.ClanID, cfg.Profession, cfg.SecondaryHobby,
		cfg.Traits.Bravery, cfg.Traits.Sociability, cfg.Traits.Industriousness, thrift, cfg.Traits.Aggressiveness,
		vitalsInfo, surplus,
		cleanPlayerName, req.PlayerUUID, prestigeScore, prestigeTitle,
		formatMemorySection(historicalContext),
	)

	if cfg.SystemPrompt != "" {
		systemPrompt += "\n\nAdditional Settler Background:\n" + cfg.SystemPrompt
	}

	userMsg := fmt.Sprintf("<player_trade player=%q>\nOffered items: [%s]\nProposal: %s\n</player_trade>", cleanPlayerName, itemsSummary.String(), req.SpokenProposal)

	return []ChatMessage{
		{Role: RoleSystem, Content: systemPrompt},
		{Role: RoleUser, Content: userMsg},
	}
}

func formatMemorySection(historicalContext string) string {
	trimmed := strings.TrimSpace(historicalContext)
	if trimmed == "" {
		return "<historical_memories>\nNo previous recorded memories with this interlocutor.\n</historical_memories>"
	}
	return fmt.Sprintf("<historical_memories>\n%s\n</historical_memories>", trimmed)
}

func sanitizePlayerName(name string) string {
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", " ")
	return strings.TrimSpace(name)
}

func clampPrestigeDelta(val float64, minVal, maxVal float64) float64 {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0.0
	}
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

var dangerRegex = regexp.MustCompile(`(?i)\b(kill|attack|die|sacrifice|warden|wither|creeper|lava|suicide|dangerous|assassinate|murder)\b`)

func isDangerousText(text string) bool {
	return dangerRegex.MatchString(text)
}
