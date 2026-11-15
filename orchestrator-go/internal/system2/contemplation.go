package system2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// StrategicContemplationSamplingTemperature is the deliberate inference temperature (T=0.30)
// for System 2 nighttime strategic contemplation and goal synthesis.
const StrategicContemplationSamplingTemperature = 0.30

// CartographySummary captures regional exploration, frontier distance, and road infrastructure.
type CartographySummary struct {
	UnexploredDirections []string `json:"unexplored_directions"`
	DistanceToFrontier   float64  `json:"distance_to_frontier"`
	UnpavedRoadCount     int      `json:"unpaved_road_count"`
	DangerSpots          []string `json:"danger_spots"`
}

// GranarySummary summarizes settlement food reserves and key stockpiles.
type GranarySummary struct {
	FoodSurplusDays float64        `json:"food_surplus_days"`
	StockStatus     string         `json:"stock_status"` // "DEFICIT", "STABLE", "SURPLUS"
	KeyItemCounts   map[string]int `json:"key_item_counts"`
}

// TavernRumorEntry represents an individual rumor or intelligence report.
type TavernRumorEntry struct {
	RumorID     string  `json:"rumor_id,omitempty"`
	Category    string  `json:"category"` // HAZARD, RESOURCE, DISCOVERY, TRADE
	Content     string  `json:"content"`
	Credibility float64 `json:"credibility"`
	Location    string  `json:"location,omitempty"`
	LocationX   float64 `json:"location_x,omitempty"`
	LocationY   float64 `json:"location_y,omitempty"`
	LocationZ   float64 `json:"location_z,omitempty"`
}

// TavernRumorsSummary lists active intelligence, hazard reports, and economic rumors.
type TavernRumorsSummary struct {
	ActiveRumors []TavernRumorEntry `json:"active_rumors"`
	Rumors       []TavernRumorEntry `json:"rumors,omitempty"`
}

// DiplomaticSummary encapsulates inter-clan relations, active treaties, and human player reputation.
type DiplomaticSummary struct {
	ClanAffinities      map[string]float64 `json:"clan_affinities"`
	PlayerPrestigeScore float64            `json:"player_prestige_score"`
	RecentTreaties      []string           `json:"recent_treaties"`
}

// PersonalVitals represents an agent's physical and spatial condition.
type PersonalVitals struct {
	Health    float32 `json:"health"`
	MaxHealth float32 `json:"max_health,omitempty"`
	FoodLevel int32   `json:"food_level"`
	Position  string  `json:"position,omitempty"`
}

// PersonalProfile captures the agent's identity, personality, condition, and inventory.
type PersonalProfile struct {
	AgentID           string             `json:"agent_id"`
	ClanID            string             `json:"clan_id"`
	Profession        string             `json:"profession"`
	SecondaryHobby    string             `json:"secondary_hobby,omitempty"`
	SleepSystemPrompt string             `json:"sleep_system_prompt,omitempty"`
	Traits            map[string]float64 `json:"traits,omitempty"`
	Vitals            PersonalVitals     `json:"vitals"`
	InventoryItems    []string           `json:"inventory_items"`
}

// StrategicLedger aggregates cartography, granary stock, tavern rumors, diplomacy,
// and personal status for nighttime contemplation.
type StrategicLedger struct {
	AgentID             string              `json:"agent_id"`
	ClanID              string              `json:"clan_id"`
	WorldTick           int64               `json:"world_tick"`
	CartographySummary  CartographySummary  `json:"cartography_summary"`
	GranarySummary      GranarySummary      `json:"granary_summary"`
	TavernRumorsSummary TavernRumorsSummary `json:"tavern_rumors_summary"`
	DiplomaticSummary   DiplomaticSummary   `json:"diplomatic_summary"`
	PersonalProfile     PersonalProfile     `json:"personal_profile"`
}

// Cartography returns the CartographySummary.
func (l *StrategicLedger) Cartography() CartographySummary {
	if l == nil {
		return CartographySummary{}
	}
	return l.CartographySummary
}

// Granary returns the GranarySummary.
func (l *StrategicLedger) Granary() GranarySummary {
	if l == nil {
		return GranarySummary{}
	}
	return l.GranarySummary
}

// TavernRumors returns the TavernRumorsSummary.
func (l *StrategicLedger) TavernRumors() TavernRumorsSummary {
	if l == nil {
		return TavernRumorsSummary{}
	}
	return l.TavernRumorsSummary
}

// Diplomacy returns the DiplomaticSummary.
func (l *StrategicLedger) Diplomacy() DiplomaticSummary {
	if l == nil {
		return DiplomaticSummary{}
	}
	return l.DiplomaticSummary
}

// Personal returns the PersonalProfile.
func (l *StrategicLedger) Personal() PersonalProfile {
	if l == nil {
		return PersonalProfile{}
	}
	return l.PersonalProfile
}

// StrategicContemplationResponse is the structured LLM JSON output from nighttime strategic reflection.
type StrategicContemplationResponse struct {
	ImmediateGoal    string                     `json:"immediate_goal"`
	ShortTermGoal    string                     `json:"short_term_goal"`
	LongTermGoal     string                     `json:"long_term_goal"`
	ReflectiveMemory string                     `json:"reflective_memory"`
	TradeAdjustments []TradeValuationAdjustment `json:"trade_adjustments"`
	CivicProposals   []string                   `json:"civic_proposals"`
	Rationale        string                     `json:"rationale"`
}

// AssembleStrategicLedger queries and aggregates sensory, economic, intelligence, and diplomatic state
// across AgentDB, CivicRegistryDB, RumorsDB, DiplomacyDB, and ReputationDB.
func AssembleStrategicLedger(
	ctx context.Context,
	agentDB *db.AgentDB,
	civicDB *db.CivicRegistryDB,
	rumorsDB *db.RumorsDB,
	diplomacyDB *db.DiplomacyDB,
	reputationDB *db.ReputationDB,
	profile PersonalProfile,
	worldTick int64,
) (*StrategicLedger, error) {
	// 1. Cartography Summary from AgentDB map graph
	carto := CartographySummary{
		UnexploredDirections: []string{"North", "South", "East", "West"},
		DangerSpots:          []string{},
	}
	if agentDB != nil {
		nodes, edges, err := agentDB.GetMapGraph(ctx)
		if err == nil {
			unpaved := 0
			for _, edge := range edges {
				if !edge.IsPaved {
					unpaved++
				}
				if edge.DangerLevel > 0.5 {
					carto.DangerSpots = append(carto.DangerSpots,
						fmt.Sprintf("edge %s->%s (danger %.2f)", edge.FromNodeID, edge.ToNodeID, edge.DangerLevel))
				}
			}
			carto.UnpavedRoadCount = unpaved

			maxDist := 0.0
			hasN, hasS, hasE, hasW := false, false, false, false
			for _, node := range nodes {
				dist := math.Hypot(node.X, node.Z)
				if dist > maxDist {
					maxDist = dist
				}
				if node.SafetyScore < 0.5 {
					carto.DangerSpots = append(carto.DangerSpots,
						fmt.Sprintf("node %s (%.0f, %.0f safety %.2f)", node.Label, node.X, node.Z, node.SafetyScore))
				}
				if node.Z < -10 {
					hasN = true
				}
				if node.Z > 10 {
					hasS = true
				}
				if node.X > 10 {
					hasE = true
				}
				if node.X < -10 {
					hasW = true
				}
			}
			carto.DistanceToFrontier = maxDist

			if len(nodes) > 0 {
				var unexplored []string
				if !hasN {
					unexplored = append(unexplored, "North")
				}
				if !hasS {
					unexplored = append(unexplored, "South")
				}
				if !hasE {
					unexplored = append(unexplored, "East")
				}
				if !hasW {
					unexplored = append(unexplored, "West")
				}
				carto.UnexploredDirections = unexplored
			}
		}
	}

	// 2. Granary Summary from CivicRegistryDB
	granary := GranarySummary{
		StockStatus:   string(SurplusStable),
		KeyItemCounts: make(map[string]int),
	}
	if civicDB != nil {
		stocks, err := civicDB.GetGranaryInventoryContext(ctx)
		if err == nil {
			totalFood := 0
			for _, s := range stocks {
				if s == nil {
					continue
				}
				avail := s.Available()
				granary.KeyItemCounts[s.ItemID] = avail
				if isFoodItemIdentifier(s.ItemID) {
					totalFood += avail
				}
			}
			// Assume a standard clan baseline consumption of ~4 units per day
			granary.FoodSurplusDays = float64(totalFood) / 4.0
			if granary.FoodSurplusDays < 3.0 {
				granary.StockStatus = string(SurplusDeficit)
			} else if granary.FoodSurplusDays > 7.0 {
				granary.StockStatus = string(SurplusSurplus)
			} else {
				granary.StockStatus = string(SurplusStable)
			}
		}
	}

	// 3. Tavern Rumors Summary from RumorsDB
	rumorsSummary := TavernRumorsSummary{
		ActiveRumors: []TavernRumorEntry{},
	}
	if rumorsDB != nil {
		active, err := rumorsDB.ListActiveRumors(ctx, worldTick, 25)
		if err == nil {
			for _, r := range active {
				if r == nil {
					continue
				}
				cat := strings.ToUpper(strings.TrimSpace(r.Category))
				if cat == db.RumorCategoryHazard || cat == db.RumorCategoryResource ||
					cat == db.RumorCategoryDiscovery || cat == db.RumorCategoryTrade {
					entry := TavernRumorEntry{
						RumorID:     r.RumorID,
						Category:    cat,
						Content:     r.Content,
						Credibility: r.Credibility,
						Location:    fmt.Sprintf("(%.0f, %.0f, %.0f)", r.LocationX, r.LocationY, r.LocationZ),
						LocationX:   r.LocationX,
						LocationY:   r.LocationY,
						LocationZ:   r.LocationZ,
					}
					rumorsSummary.ActiveRumors = append(rumorsSummary.ActiveRumors, entry)
				}
			}
		}
	}
	rumorsSummary.Rumors = rumorsSummary.ActiveRumors

	// 4. Diplomatic Summary from DiplomacyDB and ReputationDB
	diplo := DiplomaticSummary{
		ClanAffinities: make(map[string]float64),
		RecentTreaties: []string{},
	}
	if diplomacyDB != nil {
		affinities, err := diplomacyDB.ListAffinitiesContext(ctx)
		if err == nil {
			for _, a := range affinities {
				if a == nil {
					continue
				}
				diplo.ClanAffinities[a.TargetClanID] = a.AffinityScore
				if a.IsTreatyActive(worldTick) {
					diplo.RecentTreaties = append(diplo.RecentTreaties,
						fmt.Sprintf("%s: %s (expiry tick %d)", a.TargetClanID, a.ActiveTreaty, a.TreatyExpiryTick))
				}
			}
		}
	}
	if reputationDB != nil {
		heroes, err := reputationDB.ListHeroPrestigeContext(ctx, 5)
		if err == nil && len(heroes) > 0 && heroes[0] != nil {
			diplo.PlayerPrestigeScore = heroes[0].Score
		}
	}

	return &StrategicLedger{
		AgentID:             profile.AgentID,
		ClanID:              profile.ClanID,
		WorldTick:           worldTick,
		CartographySummary:  carto,
		GranarySummary:      granary,
		TavernRumorsSummary: rumorsSummary,
		DiplomaticSummary:   diplo,
		PersonalProfile:     profile,
	}, nil
}

// isFoodItemIdentifier returns true if itemID corresponds to standard nourishment.
func isFoodItemIdentifier(itemID string) bool {
	lower := strings.ToLower(itemID)
	for _, term := range []string{
		"wheat", "bread", "stew", "soup", "pie", "carrot", "potato",
		"apple", "beef", "porkchop", "mutton", "chicken", "fish",
		"salmon", "cod", "berries", "cookie", "melon",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// DefaultContemplationPrompt returns the default sleep-state system prompt for an agent.
func DefaultContemplationPrompt(agentName, profession, clanID string) string {
	if profession == "" {
		profession = "SETTLER"
	}
	if agentName == "" {
		agentName = "Agent"
	}
	if clanID == "" {
		clanID = "Settlement"
	}
	return fmt.Sprintf("You are the deliberative strategic mind (System 2) of %s, an autonomous %s of clan %s resting during nighttime sleep hours.\nSynthesize the agent's overnight reflection, morning priorities, and long-term settlement strategy.", agentName, profession, clanID)
}

// ContemplationService manages System 2 nighttime strategic contemplation.
type ContemplationService struct {
	client             *Client
	CustomSystemPrompt string
}

// NewContemplationService constructs a ContemplationService with the given LLM client.
func NewContemplationService(client *Client) *ContemplationService {
	return &ContemplationService{client: client}
}

// SetCustomSystemPrompt configures a custom system prompt override for strategic contemplation.
func (s *ContemplationService) SetCustomSystemPrompt(prompt string) {
	if s != nil {
		s.CustomSystemPrompt = prompt
	}
}

// Contemplate executes deliberative LLM reasoning (T=0.30) across the StrategicLedger
// to produce a validated StrategicContemplationResponse.
func (s *ContemplationService) Contemplate(ctx context.Context, ledger *StrategicLedger) (*StrategicContemplationResponse, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("contemplation service or LLM client is nil")
	}
	if ledger == nil {
		return nil, errors.New("strategic ledger cannot be nil")
	}

	ledgerJSON, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling strategic ledger: %w", err)
	}

	prof := ledger.PersonalProfile.Profession
	if prof == "" {
		prof = "SETTLER"
	}
	agentName := ledger.PersonalProfile.AgentID
	if agentName == "" {
		agentName = ledger.AgentID
	}
	clanID := ledger.PersonalProfile.ClanID
	if clanID == "" {
		clanID = ledger.ClanID
	}

	customPrompt := strings.TrimSpace(ledger.PersonalProfile.SleepSystemPrompt)
	if customPrompt == "" && s != nil {
		customPrompt = strings.TrimSpace(s.CustomSystemPrompt)
	}

	var sysPrompt string
	if customPrompt != "" {
		base := customPrompt
		if !strings.Contains(base, "<strategic_ledger>") {
			base = base + "\n\n<strategic_ledger>\n" + string(ledgerJSON) + "\n</strategic_ledger>"
		}
		if !strings.Contains(base, "Output ONLY a valid JSON object") && !strings.Contains(base, `"immediate_goal"`) {
			base = base + `

Output ONLY a valid JSON object matching this exact schema:
{
  "immediate_goal": "morning priority when awakening at dawn (e.g. SCOUT_FRONTIER, PAVE_ROAD, HARVEST_CROPS, CRAFT_TOOLS)",
  "short_term_goal": "next 1-3 days operational objective",
  "long_term_goal": "seasonal aspirational milestone for self and clan",
  "reflective_memory": "consolidated dream reflection or episodic synthesis from today's events, rumors, and environment to commit to memory",
  "trade_adjustments": [
    {
      "item_id": "minecraft:bread",
      "base_value": 4.0,
      "need_multiplier": 1.5,
      "min_accept_ratio": 0.85,
      "rationale": "food reserve deficit requires higher valuation"
    }
  ],
  "civic_proposals": [
    "watchtower",
    "granary"
  ],
  "rationale": "concise reasoning for strategic agenda"
}`
		}
		sysPrompt = base
	} else {
		sysPrompt = fmt.Sprintf(`%s

<strategic_ledger>
%s
</strategic_ledger>

Output ONLY a valid JSON object matching this exact schema:
{
  "immediate_goal": "morning priority when awakening at dawn (e.g. SCOUT_FRONTIER, PAVE_ROAD, HARVEST_CROPS, CRAFT_TOOLS)",
  "short_term_goal": "next 1-3 days operational objective",
  "long_term_goal": "seasonal aspirational milestone for self and clan",
  "reflective_memory": "consolidated dream reflection or episodic synthesis from today's events, rumors, and environment to commit to memory",
  "trade_adjustments": [
    {
      "item_id": "minecraft:bread",
      "base_value": 4.0,
      "need_multiplier": 1.5,
      "min_accept_ratio": 0.85,
      "rationale": "food reserve deficit requires higher valuation"
    }
  ],
  "civic_proposals": [
    "watchtower",
    "granary"
  ],
  "rationale": "concise reasoning for strategic agenda"
}`, DefaultContemplationPrompt(agentName, prof, clanID), string(ledgerJSON))
	}

	messages := []ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: "Synthesize overnight strategic contemplation and morning agenda from the strategic ledger."},
	}

	var resp StrategicContemplationResponse
	if err := s.client.CompleteStructuredWithTemp(ctx, messages, &resp, StrategicContemplationSamplingTemperature); err != nil {
		// Fallback to default structured JSON if explicit temp fails
		if fallbackErr := s.client.CompleteStructuredJSON(ctx, messages, &resp); fallbackErr != nil {
			return nil, fmt.Errorf("strategic contemplation inference failed: %w", err)
		}
	}

	sanitizeContemplationResponse(&resp, ledger)
	return &resp, nil
}

// sanitizeContemplationResponse normalizes and validates goals, trade adjustments, and memories.
func sanitizeContemplationResponse(resp *StrategicContemplationResponse, ledger *StrategicLedger) {
	if resp == nil {
		return
	}

	prof := "SETTLER"
	if ledger != nil && ledger.PersonalProfile.Profession != "" {
		prof = strings.ToUpper(strings.TrimSpace(ledger.PersonalProfile.Profession))
	}

	if strings.TrimSpace(resp.ImmediateGoal) == "" {
		switch prof {
		case "BUILDER", "ARCHITECT":
			resp.ImmediateGoal = "PAVE_ROAD: Lay stone slabs connecting forward outpost to settlement network"
		case "FARMER", "AGRARIAN":
			resp.ImmediateGoal = "HARVEST_CROPS: Tend farmland, harvest mature crops, and resupply granary"
		case "EXPLORER", "SCOUT":
			resp.ImmediateGoal = "SCOUT_FRONTIER: Chart unexplored sectors beyond current perimeter"
		case "CHRONICLER":
			resp.ImmediateGoal = "INSCRIBE_CHRONICLES: Record fallen hero lore and landmark names at library desk"
		default:
			resp.ImmediateGoal = "GATHER_RESOURCES: Stockpile essential tools and timber for settlement tasks"
		}
	}

	if strings.TrimSpace(resp.ShortTermGoal) == "" {
		resp.ShortTermGoal = "EXPAND_INFRASTRUCTURE: Secure perimeter and fortify forward outpost network"
	}
	if strings.TrimSpace(resp.LongTermGoal) == "" {
		resp.LongTermGoal = "BUILD_PROSPEROUS_CLAN: Advance dynastic growth, granary surplus, and inter-clan trade"
	}
	if strings.TrimSpace(resp.ReflectiveMemory) == "" {
		resp.ReflectiveMemory = fmt.Sprintf("Reflected upon clan priorities: %s; rationale: %s",
			resp.ImmediateGoal, resp.Rationale)
	}

	var sanitizedAdjustments []TradeValuationAdjustment
	for _, adj := range resp.TradeAdjustments {
		itemID := strings.ToLower(strings.TrimSpace(adj.ItemID))
		if itemID == "" {
			continue
		}
		if !strings.Contains(itemID, ":") {
			itemID = "minecraft:" + itemID
		}
		baseVal := math.Max(0.1, math.Min(100.0, adj.BaseValue))
		if baseVal == 0 {
			baseVal = 1.0
		}
		needMult := math.Max(0.2, math.Min(4.0, adj.NeedMultiplier))
		if needMult == 0 {
			needMult = 1.0
		}
		minRatio := math.Max(0.5, math.Min(1.5, adj.MinAcceptRatio))
		if minRatio == 0 {
			minRatio = 0.85
		}

		sanitizedAdjustments = append(sanitizedAdjustments, TradeValuationAdjustment{
			ItemID:         itemID,
			BaseValue:      baseVal,
			NeedMultiplier: needMult,
			MinAcceptRatio: minRatio,
			Rationale:      strings.TrimSpace(adj.Rationale),
		})
	}
	resp.TradeAdjustments = sanitizedAdjustments
}

// Contemplate is a package-level helper that invokes strategic contemplation with the provided LLM client.
func Contemplate(ctx context.Context, client *Client, ledger *StrategicLedger) (*StrategicContemplationResponse, error) {
	if client == nil {
		return nil, errors.New("client cannot be nil")
	}
	svc := NewContemplationService(client)
	return svc.Contemplate(ctx, ledger)
}

// Contemplate invokes strategic contemplation via the DialogueService's underlying client.
func (s *DialogueService) Contemplate(ctx context.Context, ledger *StrategicLedger) (*StrategicContemplationResponse, error) {
	if s == nil {
		return nil, errors.New("dialogue service is nil")
	}
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	if client == nil {
		return nil, errors.New("dialogue service client is nil")
	}
	return Contemplate(ctx, client, ledger)
}
