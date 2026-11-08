package system2

import (
	"context"
	"encoding/json"
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

// DialogueVitals encapsulates an agent's physical and environmental sensory status for prompt context.
type DialogueVitals struct {
	Health           float32 `json:"health"`
	FoodLevel        int32   `json:"food_level"`
	TimeOfDay        int64   `json:"time_of_day,omitempty"`
	DayPhase         string  `json:"day_phase,omitempty"`         // DAWN, MORNING, MIDDAY, AFTERNOON, DUSK, NIGHT
	Weather          string  `json:"weather,omitempty"`           // CLEAR, RAIN, THUNDER
	BiomeID          string  `json:"biome_id,omitempty"`          // e.g. minecraft:plains
	Position         string  `json:"position,omitempty"`          // e.g. "(12, 64, -8)"
	CurrentRoutine   string  `json:"current_routine,omitempty"`   // e.g. PRIMARY_DUTY, SLEEP
	ThreatStatus     string  `json:"threat_status,omitempty"`     // e.g. "SAFE (0 hostiles within 24m)" or "THREAT PRESENT (2 hostiles: zombie, skeleton)"
	CombatConfidence float64 `json:"combat_confidence,omitempty"` // System 1 confidence score
	RecentCombat     string  `json:"recent_combat,omitempty"`     // e.g. "Fought minecraft:zombie during NIGHT (tick 14200)"
	InventorySummary string  `json:"inventory_summary,omitempty"` // e.g. "1x iron_sword, 4x bread"
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

// CounterOfferList is a slice of CounterOfferItem that also tolerates null, "NONE", or single-object JSON outputs from 4B/7B LLMs.
type CounterOfferList []CounterOfferItem

// UnmarshalJSON implements json.Unmarshaler for CounterOfferList.
func (l *CounterOfferList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || strings.HasPrefix(trimmed, "\"") {
		*l = nil
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var single CounterOfferItem
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		if single.ItemID != "" {
			*l = []CounterOfferItem{single}
		} else {
			*l = nil
		}
		return nil
	}
	var items []CounterOfferItem
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	*l = items
	return nil
}

// DelegatedTask represents an optional player task delegation.
type DelegatedTask struct {
	TaskType          string `json:"task_type"` // NONE, ESCORT_PLAYER, GATHER_RESOURCE, SHARE_CROP_PROPOSAL
	TargetBlockOrItem string `json:"target_block_or_item,omitempty"`
	Quota             int32  `json:"quota,omitempty"`
}

// UnmarshalJSON allows DelegatedTask to unmarshal from either a JSON object
// ({"task_type": "NONE"}) or a shorthand string ("NONE", "ESCORT_PLAYER") emitted by 4B/7B LLMs.
func (d *DelegatedTask) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		d.TaskType = "NONE"
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || s == "NULL" {
			s = "NONE"
		}
		d.TaskType = s
		return nil
	}
	type plainDelegatedTask DelegatedTask
	var plain plainDelegatedTask
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	*d = DelegatedTask(plain)
	return nil
}

// DialogueResponse is the structured JSON output conforming to doc/ai/05 Pipeline A.
type DialogueResponse struct {
	SpokenReply       string           `json:"spoken_reply"`
	TradeDecision     string           `json:"trade_decision"` // ACCEPT | COUNTER | REJECT | NONE
	CounterOfferItems CounterOfferList `json:"counter_offer_items"`
	DelegatedTask     *DelegatedTask   `json:"delegated_task,omitempty"`
	PrestigeDelta     float64          `json:"prestige_delta"`
	MemorySummary     string           `json:"memory_summary"`
}

// DialogueResult is the complete evaluated outcome of a dialogue or trade turn.
type DialogueResult struct {
	Response        DialogueResponse
	Disobeyed       bool
	DisobeyReason   string
	ChatIntent      *civquestv1.SendChatIntent
	PacedSegments   []PacedChatSegment
	TradeIntent     *civquestv1.RespondTradeIntent
	UpdatedPrestige float64
}

// DialogueOption provides functional configuration for DialogueService.
type DialogueOption func(*DialogueService)

// WithChatTokensPerSec configures the comfortable human-reading-speed regulator (tokens/sec)
// used to pace multi-utterance chat responses (such as Historian oral storytelling).
func WithChatTokensPerSec(tokPerSec float64) DialogueOption {
	return func(s *DialogueService) {
		if tokPerSec > 0 {
			s.chatTokensPerSec = tokPerSec
		}
	}
}

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
	mu               sync.RWMutex
	client           *Client
	memory           *memory.Pipeline
	reputation       *db.ReputationDB
	agentConfig      *xdg.AgentIdentityConfig
	randFn           func() float64
	chatTokensPerSec float64
	ownsReputation   bool
	isClosed         bool
}

// NewDialogueService constructs a new DialogueService instance.
func NewDialogueService(client *Client, agentConfig *xdg.AgentIdentityConfig, opts ...DialogueOption) (*DialogueService, error) {
	if client == nil {
		return nil, ErrNilConfig
	}
	if agentConfig == nil {
		agentConfig = &xdg.AgentIdentityConfig{
			Name:       "agent",
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
		client:           client,
		agentConfig:      agentConfig,
		randFn:           rand.Float64,
		chatTokensPerSec: DefaultChatTokensPerSec,
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

	// 2. Retrieve episodic memories (combining semantic top-k with recent combat/activity memories)
	var historicalContext string
	if memPipe != nil {
		query := fmt.Sprintf("%s: %s", req.PlayerName, req.Message)
		historicalContext = retrieveCombinedMemories(ctx, memPipe, query, 3)
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

	// 8. Assemble result and compute paced chat schedule
	if resp.SpokenReply == "" {
		resp.SpokenReply = "..."
	}

	s.mu.RLock()
	tokPerSec := s.chatTokensPerSec
	s.mu.RUnlock()

	paced := ComputePacedSchedule(resp.SpokenReply, tokPerSec, DefaultMaxChatUtteranceRunes)

	result := &DialogueResult{
		Response:        resp,
		Disobeyed:       disobeyed,
		DisobeyReason:   disobeyReason,
		UpdatedPrestige: updatedPrestige,
		PacedSegments:   paced,
		ChatIntent: &civquestv1.SendChatIntent{
			Message:       resp.SpokenReply,
			TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
		},
	}

	return result, nil
}

// ProcessTrade handles an incoming barter trade request, negotiating offers based on
// agent traits, clan food surplus, player prestige, and past memories.
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

	// 2. Retrieve episodic memories (combining semantic top-k with recent combat/activity memories)
	var historicalContext string
	if memPipe != nil {
		query := fmt.Sprintf("trade with %s: %s", req.PlayerName, req.SpokenProposal)
		historicalContext = retrieveCombinedMemories(ctx, memPipe, query, 3)
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

	s.mu.RLock()
	tokPerSec := s.chatTokensPerSec
	s.mu.RUnlock()

	paced := ComputePacedSchedule(resp.SpokenReply, tokPerSec, DefaultMaxChatUtteranceRunes)

	result := &DialogueResult{
		Response:        resp,
		Disobeyed:       disobeyed,
		DisobeyReason:   disobeyReason,
		UpdatedPrestige: updatedPrestige,
		PacedSegments:   paced,
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

	sensorySection := formatSensoryEnvironmentSection(req.Vitals, surplus)

	thrift := 1.0 - cfg.Traits.Sociability
	if thrift < 0.0 {
		thrift = 0.0
	}

	cleanPlayerName := sanitizePlayerName(req.PlayerName)
	voiceSection := formatVoiceProfileSection(cfg)
	brevityRule := formatChatBrevityDirective(cfg, req.Message)

	systemPrompt := fmt.Sprintf(`You are %s, an autonomous agent in the settlement simulation CivQuest.
Clan: %s | Profession: %s | Secondary Hobby: %s
Personality Traits:
- Bravery: %.2f (willingness to face danger or fight)
- Sociability: %.2f (friendliness, talkativeness)
- Diligence / Industriousness: %.2f (commitment to tasks and clan economy)
- Thrift: %.2f (reluctance to give away resources easily)
- Aggressiveness: %.2f
- Curiosity: %.2f

%s

%s

Interlocutor:
<player_info>
Player Name: %s
UUID: %s
Hero Prestige Score: %.1f
Title: %s
</player_info>
(Prestige Meanings: HERO >= 50.0 is revered; ALLY >= 20.0 is trusted partner; FRIEND >= 5.0 is welcomed; STRANGER > -5.0 is neutral newcomer; SUSPECT > -20.0 is distrusted; VILLAIN <= -20.0 is clan enemy).

%s

%s

Instructions:
1. Respond to the player in-character reflecting your individualized voice profile, traits, clan surplus state, and the player's prestige standing.
2. Ground your response in <sensory_environment> and <historical_memories>: you already know the current time of day, weather, whether your immediate surroundings are safe or threatened, what tools/items you carry, and whether you fought monsters recently. Never ask the player if the area is safe when <sensory_environment> already tells you.
3. If the player requests dangerous suicide missions or sacrifices, reflect your Bravery trait.
4. If the player requests clan food during DEFICIT, be fiercely protective.
5. Return a JSON object conforming strictly to this JSON schema:
{
  "spoken_reply": "Concise in-game dialogue spoken to the player",
  "trade_decision": "NONE",
  "counter_offer_items": [],
  "delegated_task": {"task_type": "NONE"},
  "prestige_delta": 1.0,
  "memory_summary": "Concise one-sentence episodic summary."
}`,
		cfg.Name, cfg.ClanID, cfg.Profession, cfg.SecondaryHobby,
		cfg.Traits.Bravery, cfg.Traits.Sociability, cfg.Traits.Industriousness, thrift, cfg.Traits.Aggressiveness, cfg.Traits.Curiosity,
		voiceSection,
		sensorySection,
		cleanPlayerName, req.PlayerUUID, prestigeScore, prestigeTitle,
		formatMemorySection(historicalContext),
		brevityRule,
	)

	if cfg.SystemPrompt != "" {
		systemPrompt += "\n\nAdditional Agent Background:\n" + cfg.SystemPrompt
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
	voiceSection := formatVoiceProfileSection(cfg)

	systemPrompt := fmt.Sprintf(`You are %s, an autonomous agent negotiating barter trade in CivQuest.
Clan: %s | Profession: %s | Secondary Hobby: %s
Personality Traits:
- Bravery: %.2f
- Sociability: %.2f
- Diligence / Industriousness: %.2f
- Thrift: %.2f (higher thrift means demanding better barter margins)
- Aggressiveness: %.2f

%s

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

CRITICAL IN-GAME CHAT BREVITY RULE:
Your "spoken_reply" is printed directly into live Minecraft chat. Keep "spoken_reply" concise, natural, and punchy—strictly 1 to 2 short sentences (maximum 140 characters / 25 words). Never output verbose walls of text.

Instructions:
1. Evaluate the player's barter offer and proposal.
2. Choose trade_decision: "ACCEPT", "COUNTER", or "REJECT".
3. If COUNTER, specify counter_offer_items: [{"item_id": "minecraft:emerald", "count": 4}].
4. Return a JSON object conforming strictly to this JSON schema:
{
  "spoken_reply": "Your concise in-character negotiation remark (1-2 short sentences, <= 140 chars)",
  "trade_decision": "ACCEPT",
  "counter_offer_items": [],
  "delegated_task": {"task_type": "NONE"},
  "prestige_delta": 1.5,
  "memory_summary": "Concise one-sentence episodic trade summary."
}`,
		cfg.Name, cfg.ClanID, cfg.Profession, cfg.SecondaryHobby,
		cfg.Traits.Bravery, cfg.Traits.Sociability, cfg.Traits.Industriousness, thrift, cfg.Traits.Aggressiveness,
		voiceSection,
		vitalsInfo, surplus,
		cleanPlayerName, req.PlayerUUID, prestigeScore, prestigeTitle,
		formatMemorySection(historicalContext),
	)

	if cfg.SystemPrompt != "" {
		systemPrompt += "\n\nAdditional Agent Background:\n" + cfg.SystemPrompt
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

func retrieveCombinedMemories(ctx context.Context, memPipe *memory.Pipeline, query string, limit int) string {
	if memPipe == nil {
		return ""
	}
	seen := make(map[int64]bool)
	var combined []db.EpisodicMemoryMatch

	if matches, err := memPipe.Retrieve(ctx, query, limit); err == nil {
		for _, m := range matches {
			seen[m.Memory.ID] = true
			combined = append(combined, m)
		}
	}

	if agentDB := memPipe.AgentDB(); agentDB != nil {
		if recent, err := agentDB.GetRecentMemories(ctx, limit); err == nil {
			for _, m := range recent {
				if !seen[m.ID] {
					seen[m.ID] = true
					combined = append(combined, db.EpisodicMemoryMatch{
						Memory:     m,
						Similarity: 1.0,
					})
				}
			}
		}
	}

	if len(combined) == 0 {
		return ""
	}
	return memPipe.FormatPromptContext(combined)
}

func formatSensoryEnvironmentSection(v *DialogueVitals, surplus SurplusState) string {
	var sb strings.Builder
	sb.WriteString("<sensory_environment>\n")
	if v == nil {
		sb.WriteString("- Vitals: Unknown\n")
		sb.WriteString(fmt.Sprintf("- Clan Granary State: %s\n", surplus))
		sb.WriteString("</sensory_environment>")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("- Vitals: Health %.1f/20.0, Food %d/20\n", v.Health, v.FoodLevel))
	if v.DayPhase != "" {
		sb.WriteString(fmt.Sprintf("- Time of Day: %s (tick %d)\n", v.DayPhase, v.TimeOfDay))
	}
	if v.Weather != "" {
		sb.WriteString(fmt.Sprintf("- Weather: %s\n", v.Weather))
	}
	if v.BiomeID != "" {
		sb.WriteString(fmt.Sprintf("- Biome: %s\n", v.BiomeID))
	}
	if v.Position != "" {
		sb.WriteString(fmt.Sprintf("- Position: %s\n", v.Position))
	}
	if v.CurrentRoutine != "" {
		sb.WriteString(fmt.Sprintf("- Active Daily Routine: %s\n", v.CurrentRoutine))
	}
	if v.ThreatStatus != "" {
		sb.WriteString(fmt.Sprintf("- Immediate Threat Assessment: %s\n", v.ThreatStatus))
	}
	if v.RecentCombat != "" {
		sb.WriteString(fmt.Sprintf("- Recent Combat / Aggro Log: %s\n", v.RecentCombat))
	}
	if v.InventorySummary != "" {
		sb.WriteString(fmt.Sprintf("- Equipped / Carried Items: %s\n", v.InventorySummary))
	}
	sb.WriteString(fmt.Sprintf("- Clan Granary State: %s (DEFICIT = clan is starving/protective; STABLE = normal; SURPLUS = thriving/generous)\n", surplus))
	sb.WriteString("</sensory_environment>")
	return sb.String()
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

var storytellingRegex = regexp.MustCompile(`(?i)\b(story|tale|history|legend|chronicle|recount|lore|what happened|tell me about the)\b`)

func isStorytellingRequest(cfg *xdg.AgentIdentityConfig, message string) bool {
	if cfg == nil {
		return false
	}
	prof := strings.ToUpper(strings.TrimSpace(cfg.Profession))
	hobby := strings.ToUpper(strings.TrimSpace(cfg.SecondaryHobby))
	isLoreRole := prof == "HISTORIAN" || prof == "CHRONICLER" || hobby == "HISTORIAN" || hobby == "CHRONICLER"
	return isLoreRole && storytellingRegex.MatchString(message)
}

func formatVoiceProfileSection(cfg *xdg.AgentIdentityConfig) string {
	v := cfg.EffectiveVoice()
	var sb strings.Builder
	sb.WriteString("<agent_voice_profile>\n")
	sb.WriteString(fmt.Sprintf("- Speech Cadence: %s\n", v.Cadence))
	sb.WriteString(fmt.Sprintf("- Diction & Register: %s\n", v.Diction))
	sb.WriteString(fmt.Sprintf("- Disposition: %s\n", v.Disposition))
	if len(v.SpeechQuirks) > 0 {
		sb.WriteString(fmt.Sprintf("- Speech Quirks: %s\n", strings.Join(v.SpeechQuirks, "; ")))
	}
	if strings.TrimSpace(v.Catchphrase) != "" {
		sb.WriteString(fmt.Sprintf("- Signature Expression (use sparingly): %q\n", v.Catchphrase))
	}
	if strings.TrimSpace(v.Backstory) != "" {
		sb.WriteString(fmt.Sprintf("- Personal Backstory: %s\n", v.Backstory))
	}
	sb.WriteString("</agent_voice_profile>")
	return sb.String()
}

func formatChatBrevityDirective(cfg *xdg.AgentIdentityConfig, message string) string {
	if isStorytellingRequest(cfg, message) {
		return `ORAL STORYTELLING PACING RULE:
You are recounting clan history or lore aloud in chat. Write 2 to 4 vivid, atmospheric sentences separated by clear periods (each sentence <= 120 characters) so the in-game chat pacing regulator can deliver your story beat-by-beat at a comfortable human reading speed.`
	}
	return `CRITICAL IN-GAME CHAT BREVITY RULE:
Your "spoken_reply" is printed directly into live Minecraft chat where human players hate reading walls of text. Keep "spoken_reply" concise, natural, and punchy—strictly 1 to 2 short sentences (maximum 140 characters / 25 words). NEVER output verbose paragraphs, bullet lists, or formal LLM monologues.`
}
