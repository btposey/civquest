package caravan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Role constants.
const (
	RoleCaravanMaster = "CARAVAN_MASTER"

	// MinSurplusDurationTicks is 10 in-game Minecraft days (24,000 ticks/day).
	MinSurplusDurationTicks int64 = 240000

	// MinAffinityScore is the minimum diplomatic affinity score (-20.0) required to initiate trade.
	// Scores below -20.0 are Suspicious, Hostile, or War.
	MinAffinityScore float64 = -20.0

	// DefaultAffinityTradeDelta is the default affinity increase awarded upon successful caravan trade.
	DefaultAffinityTradeDelta float64 = 10.0

	// ArrivalToleranceBlocks is the block distance tolerance to consider a waypoint reached.
	ArrivalToleranceBlocks float64 = 2.0
)

// Sentinel errors.
var (
	ErrIneligibleEconomicConditions = errors.New("economic conditions not met for caravan expedition")
	ErrInsufficientSurplusDuration  = errors.New("commodity surplus has not been sustained for >10 days")
	ErrPartnerAffinityTooLow        = errors.New("partner clan affinity is below minimum threshold (-20.0)")
	ErrNoPavedPath                  = errors.New("no continuous paved road path found between waypoints")
	ErrNoForeignTradeZone           = errors.New("no foreign Tavern or Market functional zone found")
	ErrExpeditionNotFound           = errors.New("expedition not found")
	ErrInvalidExpeditionState       = errors.New("invalid expedition state for operation")
	ErrNilCivicDB                   = errors.New("civic registry DB cannot be nil")
	ErrNilDiplomacyDB               = errors.New("diplomacy DB cannot be nil")
)

// IsPackBeast returns true if the entity type represents a recognized pack beast (mule, donkey, llama).
func IsPackBeast(entityType string) bool {
	norm := strings.ToLower(strings.TrimSpace(entityType))
	norm = strings.TrimPrefix(norm, "minecraft:")
	return norm == "mule" || norm == "donkey" || norm == "llama" || norm == "trader_llama"
}

// ExpeditionStatus represents the lifecycle phase of an inter-clan trade expedition.
type ExpeditionStatus string

const (
	ExpeditionStatusPlanned   ExpeditionStatus = "PLANNED"
	ExpeditionStatusForming   ExpeditionStatus = "FORMING"
	ExpeditionStatusInTransit ExpeditionStatus = "IN_TRANSIT"
	ExpeditionStatusTrading   ExpeditionStatus = "TRADING"
	ExpeditionStatusReturning ExpeditionStatus = "RETURNING"
	ExpeditionStatusCompleted ExpeditionStatus = "COMPLETED"
	ExpeditionStatusAborted   ExpeditionStatus = "ABORTED"
)

// PackBeastInfo tracks a leashed beast assigned to the caravan.
type PackBeastInfo struct {
	EntityID    int32  `json:"entity_id"`
	EntityType  string `json:"entity_type"`
	IsLeashed   bool   `json:"is_leashed"`
	ChestLoaded bool   `json:"chest_loaded"`
}

// EconomicCheckResult encapsulates the result of evaluating surplus and partner deficit.
type EconomicCheckResult struct {
	Eligible        bool    `json:"eligible"`
	SourceClanID    string  `json:"source_clan_id"`
	TargetClanID    string  `json:"target_clan_id"`
	CommodityID     string  `json:"commodity_id"`
	AvailableStock  int     `json:"available_stock"`
	SurplusDuration int64   `json:"surplus_duration_ticks"`
	PartnerDeficit  int     `json:"partner_deficit"`
	AffinityScore   float64 `json:"affinity_score"`
	Reason          string  `json:"reason"`
}

// TradeDialogueRequest contains information needed for System 2 trade dialogue at the foreign market.
type TradeDialogueRequest struct {
	ExpeditionID   string                       `json:"expedition_id"`
	SourceClanID   string                       `json:"source_clan_id"`
	TargetClanID   string                       `json:"target_clan_id"`
	OfferedItems   []*civquestv1.ItemStackProto `json:"offered_items"`
	LocalRumors    []string                     `json:"local_rumors"`
	WorldTick      int64                        `json:"world_tick"`
	TargetZoneName string                       `json:"target_zone_name"`
}

// TradeDialogueOutcome contains the result of trade negotiation with foreign settlement.
type TradeDialogueOutcome struct {
	Accepted       bool                         `json:"accepted"`
	GoodsReceived  []*civquestv1.ItemStackProto `json:"goods_received"`
	RumorsReceived []string                     `json:"rumors_received"`
	SpokenResponse string                       `json:"spoken_response"`
	AffinityDelta  float64                      `json:"affinity_delta"`
	TradeVolume    int                          `json:"trade_volume"`
}

// TradeExecutor is an interface for conducting trade exchanges (e.g. via System 2 LLM or deterministic barter).
type TradeExecutor interface {
	ExecuteTradeDialogue(ctx context.Context, req TradeDialogueRequest) (*TradeDialogueOutcome, error)
}

// DefaultTradeExecutor provides a deterministic barter exchange implementation.
type DefaultTradeExecutor struct {
	DefaultExchangeRate float64
	RumorPool           []string
}

// NewDefaultTradeExecutor creates a default trade executor.
func NewDefaultTradeExecutor() *DefaultTradeExecutor {
	return &DefaultTradeExecutor{
		DefaultExchangeRate: 1.0,
		RumorPool: []string{
			"Rumors speak of rich copper veins in the eastern foothills.",
			"A band of illagers was sighted past the northern river bend.",
			"The harvest here has been bountiful thanks to the new irrigation canal.",
		},
	}
}

// ExecuteTradeDialogue provides a realistic barter exchange trade response.
func (e *DefaultTradeExecutor) ExecuteTradeDialogue(ctx context.Context, req TradeDialogueRequest) (*TradeDialogueOutcome, error) {
	totalOffered := 0
	for _, it := range req.OfferedItems {
		totalOffered += int(it.Count)
	}
	if totalOffered == 0 {
		return &TradeDialogueOutcome{
			Accepted:       false,
			SpokenResponse: "You bring no goods to trade with us.",
		}, nil
	}

	// Barter trade: exchange surplus items for currency / goods (emeralds, iron ingots, or specialty goods)
	receivedEmeralds := int32(math.Max(1, float64(totalOffered)/4.0))
	goods := []*civquestv1.ItemStackProto{
		{
			ItemId: "minecraft:emerald",
			Count:  receivedEmeralds,
		},
	}

	// Pick a rumor
	rumors := []string{"We welcome friendly traders from your clan."}
	if len(e.RumorPool) > 0 {
		rumors = append(rumors, e.RumorPool[req.WorldTick%int64(len(e.RumorPool))])
	}

	return &TradeDialogueOutcome{
		Accepted:       true,
		GoodsReceived:  goods,
		RumorsReceived: rumors,
		SpokenResponse: fmt.Sprintf("A fair trade! We accept your goods and offer %d emeralds in return.", receivedEmeralds),
		AffinityDelta:  DefaultAffinityTradeDelta,
		TradeVolume:    totalOffered,
	}, nil
}

// ExpeditionState tracks the full lifecycle of an active caravan expedition.
type ExpeditionState struct {
	mu                   sync.RWMutex
	ID                   string                       `json:"id"`
	SourceClanID         string                       `json:"source_clan_id"`
	TargetClanID         string                       `json:"target_clan_id"`
	CaravanMasterAgentID string                       `json:"caravan_master_agent_id"`
	GuardAgentIDs        []string                     `json:"guard_agent_ids"`
	PackBeasts           []PackBeastInfo              `json:"pack_beasts"`
	Cargo                []*civquestv1.ItemStackProto `json:"cargo"`
	GoodsReceived        []*civquestv1.ItemStackProto `json:"goods_received"`
	RumorsShared         []string                     `json:"rumors_shared"`
	RumorsReceived       []string                     `json:"rumors_received"`
	Status               ExpeditionStatus             `json:"status"`
	PavedWaypoints       []*db.MapNode                `json:"paved_waypoints"`
	CurrentWaypointIndex int                          `json:"current_waypoint_index"`
	TargetZone           *db.FunctionalZone           `json:"target_zone"`
	AffinityDelta        float64                      `json:"affinity_delta"`
	TradeVolume          int                          `json:"trade_volume"`
	DepartureTick        int64                        `json:"departure_tick"`
	ReturnTick           int64                        `json:"return_tick"`
}

// Clone creates a thread-safe snapshot of ExpeditionState.
func (e *ExpeditionState) Clone() *ExpeditionState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	guards := make([]string, len(e.GuardAgentIDs))
	copy(guards, e.GuardAgentIDs)

	beasts := make([]PackBeastInfo, len(e.PackBeasts))
	copy(beasts, e.PackBeasts)

	cargo := make([]*civquestv1.ItemStackProto, len(e.Cargo))
	for i, c := range e.Cargo {
		cargo[i] = &civquestv1.ItemStackProto{
			ItemId: c.ItemId,
			Count:  c.Count,
		}
	}

	goods := make([]*civquestv1.ItemStackProto, len(e.GoodsReceived))
	for i, g := range e.GoodsReceived {
		goods[i] = &civquestv1.ItemStackProto{
			ItemId: g.ItemId,
			Count:  g.Count,
		}
	}

	rumorsShared := make([]string, len(e.RumorsShared))
	copy(rumorsShared, e.RumorsShared)

	rumorsRecv := make([]string, len(e.RumorsReceived))
	copy(rumorsRecv, e.RumorsReceived)

	waypoints := make([]*db.MapNode, len(e.PavedWaypoints))
	copy(waypoints, e.PavedWaypoints)

	var targetZone *db.FunctionalZone
	if e.TargetZone != nil {
		tz := *e.TargetZone
		targetZone = &tz
	}

	return &ExpeditionState{
		ID:                   e.ID,
		SourceClanID:         e.SourceClanID,
		TargetClanID:         e.TargetClanID,
		CaravanMasterAgentID: e.CaravanMasterAgentID,
		GuardAgentIDs:        guards,
		PackBeasts:           beasts,
		Cargo:                cargo,
		GoodsReceived:        goods,
		RumorsShared:         rumorsShared,
		RumorsReceived:       rumorsRecv,
		Status:               e.Status,
		PavedWaypoints:       waypoints,
		CurrentWaypointIndex: e.CurrentWaypointIndex,
		TargetZone:           targetZone,
		AffinityDelta:        e.AffinityDelta,
		TradeVolume:          e.TradeVolume,
		DepartureTick:        e.DepartureTick,
		ReturnTick:           e.ReturnTick,
	}
}

// SurplusTracker tracks timestamps when commodities entered surplus in clan granaries.
type SurplusTracker struct {
	mu           sync.RWMutex
	surplusStart map[string]int64 // key: clanID + ":" + itemID -> start worldTick
}

// NewSurplusTracker creates a new surplus duration tracker.
func NewSurplusTracker() *SurplusTracker {
	return &SurplusTracker{
		surplusStart: make(map[string]int64),
	}
}

// RecordSurplus records or updates surplus status for an item in a clan granary.
func (st *SurplusTracker) RecordSurplus(clanID, itemID string, hasSurplus bool, currentTick int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	key := clanID + ":" + itemID
	if hasSurplus {
		if _, exists := st.surplusStart[key]; !exists {
			st.surplusStart[key] = currentTick
		}
	} else {
		delete(st.surplusStart, key)
	}
}

// GetSurplusDuration returns how many ticks an item has continuously been in surplus.
func (st *SurplusTracker) GetSurplusDuration(clanID, itemID string, currentTick int64) int64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	key := clanID + ":" + itemID
	startTick, exists := st.surplusStart[key]
	if !exists {
		return 0
	}
	if currentTick < startTick {
		return 0
	}
	return currentTick - startTick
}

// SetSurplusStartTime manually configures the surplus start time (useful for bootstrapping or testing).
func (st *SurplusTracker) SetSurplusStartTime(clanID, itemID string, startTick int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	key := clanID + ":" + itemID
	st.surplusStart[key] = startTick
}

// CaravanManagerConfig holds dependencies and configuration for CaravanManager.
type CaravanManagerConfig struct {
	SourceClanID         string
	CaravanMasterAgentID string
	SourceCivicDB        *db.CivicRegistryDB
	SourceDiplomacyDB    *db.DiplomacyDB
	PartnerCivicDB       *db.CivicRegistryDB // Optional: for local multi-clan environments
	PartnerDiplomacyDB   *db.DiplomacyDB     // Optional: for reciprocal affinity updates
	SurplusTracker       *SurplusTracker
	TradeExecutor        TradeExecutor
	SurplusThreshold     int   // Available quantity above which item is surplus (default 32)
	DeficitThreshold     int   // Partner stock below which item is in deficit (default 10)
	MinSurplusDuration   int64 // Required duration ticks (default MinSurplusDurationTicks)
}

// CaravanManager orchestrates the Caravan Master role, pack-beast leashing,
// economic surplus/deficit checks, paved route traversal, and inter-clan trade expeditions.
type CaravanManager struct {
	mu                   sync.RWMutex
	sourceClanID         string
	caravanMasterAgentID string
	sourceCivicDB        *db.CivicRegistryDB
	sourceDiplomacyDB    *db.DiplomacyDB
	partnerCivicDB       *db.CivicRegistryDB
	partnerDiplomacyDB   *db.DiplomacyDB
	surplusTracker       *SurplusTracker
	tradeExecutor        TradeExecutor
	surplusThreshold     int
	deficitThreshold     int
	minSurplusDuration   int64

	expeditions        map[string]*ExpeditionState
	activeExpeditionID string
}

// NewCaravanManager initializes a new CaravanManager.
func NewCaravanManager(cfg CaravanManagerConfig) (*CaravanManager, error) {
	if cfg.SourceClanID == "" {
		return nil, errors.New("source clan ID cannot be empty")
	}
	if cfg.CaravanMasterAgentID == "" {
		return nil, errors.New("caravan master agent ID cannot be empty")
	}
	if cfg.SourceCivicDB == nil {
		return nil, ErrNilCivicDB
	}
	if cfg.SourceDiplomacyDB == nil {
		return nil, ErrNilDiplomacyDB
	}

	st := cfg.SurplusTracker
	if st == nil {
		st = NewSurplusTracker()
	}

	te := cfg.TradeExecutor
	if te == nil {
		te = NewDefaultTradeExecutor()
	}

	surplusThresh := cfg.SurplusThreshold
	if surplusThresh <= 0 {
		surplusThresh = 32
	}

	deficitThresh := cfg.DeficitThreshold
	if deficitThresh <= 0 {
		deficitThresh = 10
	}

	minDuration := cfg.MinSurplusDuration
	if minDuration <= 0 {
		minDuration = MinSurplusDurationTicks
	}

	return &CaravanManager{
		sourceClanID:         cfg.SourceClanID,
		caravanMasterAgentID: cfg.CaravanMasterAgentID,
		sourceCivicDB:        cfg.SourceCivicDB,
		sourceDiplomacyDB:    cfg.SourceDiplomacyDB,
		partnerCivicDB:       cfg.PartnerCivicDB,
		partnerDiplomacyDB:   cfg.PartnerDiplomacyDB,
		surplusTracker:       st,
		tradeExecutor:        te,
		surplusThreshold:     surplusThresh,
		deficitThreshold:     deficitThresh,
		minSurplusDuration:   minDuration,
		expeditions:          make(map[string]*ExpeditionState),
	}, nil
}

// SetPartnerDatabases configures partner clan databases dynamically.
func (m *CaravanManager) SetPartnerDatabases(civic *db.CivicRegistryDB, diplo *db.DiplomacyDB) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.partnerCivicDB = civic
	m.partnerDiplomacyDB = diplo
}

// SurplusTracker returns the underlying surplus tracker.
func (m *CaravanManager) SurplusTracker() *SurplusTracker {
	return m.surplusTracker
}

// CheckEconomicConditions evaluates whether source clan has a sustained commodity surplus (>10 days)
// and target clan has a deficit with affinity score >= -20.0.
func (m *CaravanManager) CheckEconomicConditions(
	ctx context.Context,
	targetClanID string,
	commodityID string,
	currentTick int64,
) (*EconomicCheckResult, error) {
	if targetClanID == "" {
		return nil, errors.New("target clan ID cannot be empty")
	}
	if commodityID == "" {
		return nil, errors.New("commodity ID cannot be empty")
	}

	// 1. Check diplomatic affinity with target clan (must be >= -20.0)
	aff, err := m.sourceDiplomacyDB.GetAffinityContext(ctx, targetClanID)
	if err != nil {
		return nil, fmt.Errorf("checking affinity for %s: %w", targetClanID, err)
	}
	currentAffinity := 0.0
	if aff != nil {
		currentAffinity = aff.AffinityScore
	}
	if currentAffinity < MinAffinityScore {
		return &EconomicCheckResult{
			Eligible:      false,
			SourceClanID:  m.sourceClanID,
			TargetClanID:  targetClanID,
			CommodityID:   commodityID,
			AffinityScore: currentAffinity,
			Reason:        fmt.Sprintf("target clan affinity (%.1f) is below minimum threshold (%.1f)", currentAffinity, MinAffinityScore),
		}, nil
	}

	// 2. Check source clan granary for commodity stock
	stock, err := m.sourceCivicDB.GetItemStockContext(ctx, commodityID)
	if err != nil {
		return nil, fmt.Errorf("checking source granary stock for %s: %w", commodityID, err)
	}
	availableStock := 0
	if stock != nil {
		availableStock = stock.Available()
	}

	// Check if available stock exceeds surplus threshold
	hasSurplus := availableStock > m.surplusThreshold
	m.surplusTracker.RecordSurplus(m.sourceClanID, commodityID, hasSurplus, currentTick)

	surplusDuration := m.surplusTracker.GetSurplusDuration(m.sourceClanID, commodityID, currentTick)
	if !hasSurplus || surplusDuration < m.minSurplusDuration {
		return &EconomicCheckResult{
			Eligible:        false,
			SourceClanID:    m.sourceClanID,
			TargetClanID:    targetClanID,
			CommodityID:     commodityID,
			AvailableStock:  availableStock,
			SurplusDuration: surplusDuration,
			AffinityScore:   currentAffinity,
			Reason:          fmt.Sprintf("commodity surplus not sustained (>10 days required, current duration: %d ticks, available: %d)", surplusDuration, availableStock),
		}, nil
	}

	// 3. Check partner clan deficit
	partnerDeficit := 0
	if m.partnerCivicDB != nil {
		pStock, err := m.partnerCivicDB.GetItemStockContext(ctx, commodityID)
		if err != nil {
			return nil, fmt.Errorf("checking partner granary stock for %s: %w", commodityID, err)
		}
		pAvailable := 0
		if pStock != nil {
			pAvailable = pStock.Available()
		}
		if pAvailable >= m.deficitThreshold {
			return &EconomicCheckResult{
				Eligible:        false,
				SourceClanID:    m.sourceClanID,
				TargetClanID:    targetClanID,
				CommodityID:     commodityID,
				AvailableStock:  availableStock,
				SurplusDuration: surplusDuration,
				PartnerDeficit:  0,
				AffinityScore:   currentAffinity,
				Reason:          fmt.Sprintf("partner clan has sufficient stock (%d >= threshold %d)", pAvailable, m.deficitThreshold),
			}, nil
		}
		partnerDeficit = m.deficitThreshold - pAvailable
	} else {
		// If partner civic DB not attached directly, assume requested deficit based on trade invitation/demand
		partnerDeficit = m.deficitThreshold
	}

	return &EconomicCheckResult{
		Eligible:        true,
		SourceClanID:    m.sourceClanID,
		TargetClanID:    targetClanID,
		CommodityID:     commodityID,
		AvailableStock:  availableStock,
		SurplusDuration: surplusDuration,
		PartnerDeficit:  partnerDeficit,
		AffinityScore:   currentAffinity,
		Reason:          "sustained commodity surplus (>10 days) and partner deficit met with eligible affinity",
	}, nil
}

// FormExpeditionRequest contains parameters to assemble an expedition.
type FormExpeditionRequest struct {
	ExpeditionID   string
	TargetClanID   string
	CargoCommodity string
	CargoQuantity  int
	GuardAgentIDs  []string
	PackBeasts     []*civquestv1.VisibleEntity
	GraphNodes     []db.MapNode
	GraphEdges     []db.MapEdge
	StartNodeID    string
	TargetNodeID   string
	DepartureTick  int64
	LocalRumors    []string
}

// FormExpedition verifies economic conditions, recruits guards, leashes pack beasts,
// loads chest inventory with surplus items, plans the paved route, and emits initialization intents.
func (m *CaravanManager) FormExpedition(
	ctx context.Context,
	req FormExpeditionRequest,
) (*ExpeditionState, []*civquestv1.AgentIntent, error) {
	if req.ExpeditionID == "" {
		req.ExpeditionID = fmt.Sprintf("exp-%s-%s-%d", m.sourceClanID, req.TargetClanID, req.DepartureTick)
	}
	if req.TargetClanID == "" {
		return nil, nil, errors.New("target clan ID cannot be empty")
	}
	if req.CargoCommodity == "" || req.CargoQuantity <= 0 {
		return nil, nil, errors.New("valid cargo commodity and positive quantity required")
	}

	// 1. Verify economic conditions
	econ, err := m.CheckEconomicConditions(ctx, req.TargetClanID, req.CargoCommodity, req.DepartureTick)
	if err != nil {
		return nil, nil, fmt.Errorf("evaluating economic conditions: %w", err)
	}
	if !econ.Eligible {
		return nil, nil, fmt.Errorf("%w: %s", ErrIneligibleEconomicConditions, econ.Reason)
	}

	// 2. Reserve / consume commodity from source granary
	if err := m.sourceCivicDB.ConsumeGranaryStockContext(ctx, req.CargoCommodity, req.CargoQuantity, req.DepartureTick); err != nil {
		return nil, nil, fmt.Errorf("consuming surplus cargo from granary: %w", err)
	}

	// 3. Find foreign Tavern or Market zone if partner civic DB is available
	var targetZone *db.FunctionalZone
	if m.partnerCivicDB != nil {
		zones, err := m.partnerCivicDB.ListZonesContext(ctx)
		if err == nil {
			for _, z := range zones {
				if z.ZoneType == db.ZoneTypeMarket || z.ZoneType == db.ZoneTypeTavern {
					targetZone = z
					break
				}
			}
		}
	}
	if targetZone == nil {
		// Provide default foreign market zone fallback if not configured in DB
		targetZone = &db.FunctionalZone{
			ZoneID:   "foreign-market-default",
			ZoneType: db.ZoneTypeMarket,
			Name:     "Foreign Clan Market Square",
			MinX:     100, MaxX: 120,
			MinY: 64, MaxY: 72,
			MinZ: 100, MaxZ: 120,
		}
	}

	// 4. Plan paved route between start node and target node
	var pavedWaypoints []*db.MapNode
	if len(req.GraphNodes) > 0 && len(req.GraphEdges) > 0 && req.StartNodeID != "" && req.TargetNodeID != "" {
		route, err := PlanPavedRoute(req.GraphNodes, req.GraphEdges, req.StartNodeID, req.TargetNodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("planning paved route: %w", err)
		}
		pavedWaypoints = route
	}

	// 5. Identify pack beasts and generate leash & load chest intents
	var intents []*civquestv1.AgentIntent
	var beastInfos []PackBeastInfo

	cargoItem := &civquestv1.ItemStackProto{
		ItemId: req.CargoCommodity,
		Count:  int32(req.CargoQuantity),
	}

	for _, ent := range req.PackBeasts {
		if !IsPackBeast(ent.EntityType) {
			continue
		}

		beastInfo := PackBeastInfo{
			EntityID:    ent.EntityId,
			EntityType:  ent.EntityType,
			IsLeashed:   true,
			ChestLoaded: true,
		}
		beastInfos = append(beastInfos, beastInfo)

		// 5a. CaravanIntent: LEASH_BEAST
		leashIntent := &civquestv1.AgentIntent{
			AgentId: m.caravanMasterAgentID,
			Action: &civquestv1.AgentIntent_Caravan{
				Caravan: &civquestv1.CaravanIntent{
					Action:        civquestv1.CaravanIntent_LEASH_BEAST,
					BeastEntityId: ent.EntityId,
				},
			},
		}
		intents = append(intents, leashIntent)

		// 5b. CaravanIntent: LOAD_BEAST_CHEST
		loadIntent := &civquestv1.AgentIntent{
			AgentId: m.caravanMasterAgentID,
			Action: &civquestv1.AgentIntent_Caravan{
				Caravan: &civquestv1.CaravanIntent{
					Action:        civquestv1.CaravanIntent_LOAD_BEAST_CHEST,
					BeastEntityId: ent.EntityId,
					CargoItems:    []*civquestv1.ItemStackProto{cargoItem},
				},
			},
		}
		intents = append(intents, loadIntent)
	}

	// 6. Assemble expedition state
	guards := make([]string, len(req.GuardAgentIDs))
	copy(guards, req.GuardAgentIDs)

	expedition := &ExpeditionState{
		ID:                   req.ExpeditionID,
		SourceClanID:         m.sourceClanID,
		TargetClanID:         req.TargetClanID,
		CaravanMasterAgentID: m.caravanMasterAgentID,
		GuardAgentIDs:        guards,
		PackBeasts:           beastInfos,
		Cargo:                []*civquestv1.ItemStackProto{cargoItem},
		Status:               ExpeditionStatusInTransit,
		PavedWaypoints:       pavedWaypoints,
		CurrentWaypointIndex: 0,
		TargetZone:           targetZone,
		DepartureTick:        req.DepartureTick,
		RumorsShared:         req.LocalRumors,
	}

	// 7. Record expedition in clan diplomacy caravan_logs
	cargoJSON, _ := json.Marshal(expedition.Cargo)
	logEntry := &db.CaravanLogEntry{
		ExpeditionID:   expedition.ID,
		LeaderAgentID:  expedition.CaravanMasterAgentID,
		SourceClanID:   expedition.SourceClanID,
		TargetClanID:   expedition.TargetClanID,
		Status:         string(expedition.Status),
		CargoItemsJSON: string(cargoJSON),
		DepartureTick:  expedition.DepartureTick,
	}
	if err := m.sourceDiplomacyDB.LogExpeditionContext(ctx, logEntry); err != nil {
		return nil, nil, fmt.Errorf("logging expedition: %w", err)
	}

	m.mu.Lock()
	m.expeditions[expedition.ID] = expedition
	m.activeExpeditionID = expedition.ID
	m.mu.Unlock()

	return expedition.Clone(), intents, nil
}

// PlanPavedRoute computes the shortest sequence of MapNodes along paved road edges (IsPaved == true).
// Returns ErrNoPavedPath if no unbroken paved path connects startNodeID to targetNodeID.
func PlanPavedRoute(nodes []db.MapNode, edges []db.MapEdge, startNodeID, targetNodeID string) ([]*db.MapNode, error) {
	if startNodeID == "" || targetNodeID == "" {
		return nil, errors.New("start and target node IDs cannot be empty")
	}

	nodeMap := make(map[string]*db.MapNode, len(nodes))
	for i := range nodes {
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	if _, exists := nodeMap[startNodeID]; !exists {
		return nil, fmt.Errorf("start node %s not found in graph", startNodeID)
	}
	if _, exists := nodeMap[targetNodeID]; !exists {
		return nil, fmt.Errorf("target node %s not found in graph", targetNodeID)
	}
	if startNodeID == targetNodeID {
		return []*db.MapNode{nodeMap[startNodeID]}, nil
	}

	type edgeNeighbor struct {
		toID     string
		distance float64
	}

	adj := make(map[string][]edgeNeighbor)
	for _, e := range edges {
		// Only traverse paved road edges
		if !e.IsPaved {
			continue
		}
		dist := e.Distance
		if dist <= 0 {
			// Calculate Euclidean distance if not stored
			if n1, ok1 := nodeMap[e.FromNodeID]; ok1 {
				if n2, ok2 := nodeMap[e.ToNodeID]; ok2 {
					dx := n1.X - n2.X
					dy := n1.Y - n2.Y
					dz := n1.Z - n2.Z
					dist = math.Sqrt(dx*dx + dy*dy + dz*dz)
				}
			}
			if dist <= 0 {
				dist = 1.0
			}
		}

		adj[e.FromNodeID] = append(adj[e.FromNodeID], edgeNeighbor{toID: e.ToNodeID, distance: dist})
		adj[e.ToNodeID] = append(adj[e.ToNodeID], edgeNeighbor{toID: e.FromNodeID, distance: dist})
	}

	// Dijkstra's algorithm
	dist := make(map[string]float64)
	prev := make(map[string]string)
	visited := make(map[string]bool)

	for id := range nodeMap {
		dist[id] = math.Inf(1)
	}
	dist[startNodeID] = 0.0

	for len(visited) < len(nodeMap) {
		// Find unvisited node with lowest distance
		curr := ""
		minD := math.Inf(1)
		for id, d := range dist {
			if !visited[id] && d < minD {
				minD = d
				curr = id
			}
		}

		if curr == "" || math.IsInf(minD, 1) {
			break
		}
		if curr == targetNodeID {
			break
		}

		visited[curr] = true

		for _, neighbor := range adj[curr] {
			if visited[neighbor.toID] {
				continue
			}
			alt := dist[curr] + neighbor.distance
			if alt < dist[neighbor.toID] {
				dist[neighbor.toID] = alt
				prev[neighbor.toID] = curr
			}
		}
	}

	if math.IsInf(dist[targetNodeID], 1) {
		return nil, ErrNoPavedPath
	}

	// Reconstruct path
	var pathRev []*db.MapNode
	u := targetNodeID
	for u != "" {
		pathRev = append(pathRev, nodeMap[u])
		u = prev[u]
	}

	// Reverse to start -> target
	route := make([]*db.MapNode, len(pathRev))
	for i := range pathRev {
		route[i] = pathRev[len(pathRev)-1-i]
	}

	return route, nil
}

// NavigateStep evaluates the caravan master's current coordinate against the planned paved waypoints.
// Emits NavigateIntent along paved road edges, advancing waypoints as arrival tolerance is met.
func (m *CaravanManager) NavigateStep(
	expeditionID string,
	currentPos *civquestv1.Vec3,
) (*civquestv1.AgentIntent, error) {
	m.mu.RLock()
	exp, exists := m.expeditions[expeditionID]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrExpeditionNotFound
	}

	exp.mu.Lock()
	defer exp.mu.Unlock()

	if exp.Status != ExpeditionStatusInTransit && exp.Status != ExpeditionStatusReturning {
		return nil, nil
	}

	if len(exp.PavedWaypoints) == 0 {
		return nil, nil
	}

	targetNode := exp.PavedWaypoints[exp.CurrentWaypointIndex]
	if currentPos != nil {
		dx := currentPos.X - targetNode.X
		dy := currentPos.Y - targetNode.Y
		dz := currentPos.Z - targetNode.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

		if dist <= ArrivalToleranceBlocks {
			// Advance to next waypoint
			if exp.CurrentWaypointIndex < len(exp.PavedWaypoints)-1 {
				exp.CurrentWaypointIndex++
				targetNode = exp.PavedWaypoints[exp.CurrentWaypointIndex]
			} else {
				// Reached final waypoint
				if exp.Status == ExpeditionStatusInTransit {
					exp.Status = ExpeditionStatusTrading
					return nil, nil
				} else if exp.Status == ExpeditionStatusReturning {
					// Caravan reached home
					return nil, nil
				}
			}
		}
	}

	// Emit NavigateIntent towards target paved waypoint
	navIntent := &civquestv1.AgentIntent{
		AgentId: m.caravanMasterAgentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: targetNode.X,
					Y: targetNode.Y,
					Z: targetNode.Z,
				},
				SpeedMultiplier:       1.0,
				Sprint:                false,
				ArrivalTolerance:      int32(ArrivalToleranceBlocks),
				AllowBreakBlocks:      false,
				AllowPlaceScaffolding: false,
			},
		},
	}
	return navIntent, nil
}

// ExecuteTrade negotiates barter exchange via System 2 trade dialogue at the foreign Tavern/Market,
// exchanges rumors, updates clan_affinities, logs the exchange, and prepares return waypoints.
func (m *CaravanManager) ExecuteTrade(
	ctx context.Context,
	expeditionID string,
	worldTick int64,
) (*TradeDialogueOutcome, error) {
	m.mu.RLock()
	exp, exists := m.expeditions[expeditionID]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrExpeditionNotFound
	}

	exp.mu.Lock()
	defer exp.mu.Unlock()

	if exp.Status != ExpeditionStatusTrading {
		return nil, fmt.Errorf("%w: current status is %s, expected %s", ErrInvalidExpeditionState, exp.Status, ExpeditionStatusTrading)
	}

	zoneName := "Market Square"
	if exp.TargetZone != nil && exp.TargetZone.Name != "" {
		zoneName = exp.TargetZone.Name
	}

	dialogueReq := TradeDialogueRequest{
		ExpeditionID:   exp.ID,
		SourceClanID:   exp.SourceClanID,
		TargetClanID:   exp.TargetClanID,
		OfferedItems:   exp.Cargo,
		LocalRumors:    exp.RumorsShared,
		WorldTick:      worldTick,
		TargetZoneName: zoneName,
	}

	outcome, err := m.tradeExecutor.ExecuteTradeDialogue(ctx, dialogueReq)
	if err != nil {
		return nil, fmt.Errorf("executing trade dialogue: %w", err)
	}

	if !outcome.Accepted {
		exp.Status = ExpeditionStatusReturning
		return outcome, nil
	}

	exp.GoodsReceived = outcome.GoodsReceived
	exp.RumorsReceived = outcome.RumorsReceived
	exp.AffinityDelta = outcome.AffinityDelta
	exp.TradeVolume = outcome.TradeVolume

	// 1. Update source clan diplomacy DB
	if err := m.sourceDiplomacyDB.RecordCaravanTradeContext(ctx, exp.TargetClanID, outcome.TradeVolume, outcome.AffinityDelta, worldTick); err != nil {
		return nil, fmt.Errorf("updating source clan affinity: %w", err)
	}

	// 2. Update partner clan diplomacy DB if available
	if m.partnerDiplomacyDB != nil {
		_ = m.partnerDiplomacyDB.RecordCaravanTradeContext(ctx, exp.SourceClanID, outcome.TradeVolume, outcome.AffinityDelta, worldTick)
	}

	// 3. Reverse paved waypoints for return journey
	if len(exp.PavedWaypoints) > 1 {
		revWaypoints := make([]*db.MapNode, len(exp.PavedWaypoints))
		for i := range exp.PavedWaypoints {
			revWaypoints[i] = exp.PavedWaypoints[len(exp.PavedWaypoints)-1-i]
		}
		exp.PavedWaypoints = revWaypoints
		exp.CurrentWaypointIndex = 0
	}

	exp.Status = ExpeditionStatusReturning

	// 4. Update caravan log in source clan diplomacy
	goodsJSON, _ := json.Marshal(outcome.GoodsReceived)
	_ = m.sourceDiplomacyDB.UpdateExpeditionLogContext(ctx, exp.ID, string(exp.Status), 0, string(goodsJSON), outcome.AffinityDelta)

	return outcome, nil
}

// ReturnAndUnload completes the return journey, deposits goods into home settlement granary,
// emits unleash / hitch intents for pack beasts, and closes the expedition log.
func (m *CaravanManager) ReturnAndUnload(
	ctx context.Context,
	expeditionID string,
	worldTick int64,
) ([]*civquestv1.AgentIntent, error) {
	m.mu.RLock()
	exp, exists := m.expeditions[expeditionID]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrExpeditionNotFound
	}

	exp.mu.Lock()
	defer exp.mu.Unlock()

	if exp.Status != ExpeditionStatusReturning && exp.Status != ExpeditionStatusInTransit {
		return nil, fmt.Errorf("%w: status is %s", ErrInvalidExpeditionState, exp.Status)
	}

	// 1. Deposit received goods into home settlement granary
	for _, item := range exp.GoodsReceived {
		if item.Count > 0 && item.ItemId != "" {
			if err := m.sourceCivicDB.AddGranaryStockContext(ctx, item.ItemId, int(item.Count), worldTick); err != nil {
				return nil, fmt.Errorf("depositing returned trade goods into granary: %w", err)
			}
		}
	}

	// 2. Generate UNLEASH_BEAST intents for each pack beast
	var intents []*civquestv1.AgentIntent
	for i := range exp.PackBeasts {
		exp.PackBeasts[i].IsLeashed = false
		exp.PackBeasts[i].ChestLoaded = false

		unleashIntent := &civquestv1.AgentIntent{
			AgentId: m.caravanMasterAgentID,
			Action: &civquestv1.AgentIntent_Caravan{
				Caravan: &civquestv1.CaravanIntent{
					Action:        civquestv1.CaravanIntent_UNLEASH_BEAST,
					BeastEntityId: exp.PackBeasts[i].EntityID,
				},
			},
		}
		intents = append(intents, unleashIntent)
	}

	exp.Status = ExpeditionStatusCompleted
	exp.ReturnTick = worldTick

	// 3. Update expedition log
	goodsJSON, _ := json.Marshal(exp.GoodsReceived)
	_ = m.sourceDiplomacyDB.UpdateExpeditionLogContext(ctx, exp.ID, string(exp.Status), exp.ReturnTick, string(goodsJSON), exp.AffinityDelta)

	return intents, nil
}

// GetExpedition retrieves an expedition state by ID.
func (m *CaravanManager) GetExpedition(expeditionID string) (*ExpeditionState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	exp, exists := m.expeditions[expeditionID]
	if !exists {
		return nil, ErrExpeditionNotFound
	}
	return exp.Clone(), nil
}

// ActiveExpedition returns the currently active expedition if any.
func (m *CaravanManager) ActiveExpedition() *ExpeditionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeExpeditionID == "" {
		return nil
	}
	if exp, exists := m.expeditions[m.activeExpeditionID]; exists {
		return exp.Clone()
	}
	return nil
}

// Step evaluates an in-flight expedition against a telemetry frame.
// During IN_TRANSIT, it guides the caravan along paved waypoints and automatically initiates trade upon arrival.
// During RETURNING, it guides the caravan back home and unloads goods upon arrival.
func (m *CaravanManager) Step(ctx context.Context, frame *civquestv1.TelemetryFrame) ([]*civquestv1.AgentIntent, error) {
	if frame == nil || frame.Status == nil {
		return nil, nil
	}

	m.mu.RLock()
	activeID := m.activeExpeditionID
	m.mu.RUnlock()

	if activeID == "" {
		return nil, nil
	}

	exp, err := m.GetExpedition(activeID)
	if err != nil || exp == nil {
		return nil, nil
	}

	pos := frame.Status.Position

	switch exp.Status {
	case ExpeditionStatusInTransit:
		intent, err := m.NavigateStep(exp.ID, pos)
		if err != nil {
			return nil, err
		}
		// Check if arrival triggered TRADING state
		updated, _ := m.GetExpedition(exp.ID)
		if updated != nil && updated.Status == ExpeditionStatusTrading {
			_, _ = m.ExecuteTrade(ctx, exp.ID, frame.WorldTick)
		}
		if intent != nil {
			return []*civquestv1.AgentIntent{intent}, nil
		}
		return nil, nil

	case ExpeditionStatusTrading:
		_, err := m.ExecuteTrade(ctx, exp.ID, frame.WorldTick)
		return nil, err

	case ExpeditionStatusReturning:
		intent, err := m.NavigateStep(exp.ID, pos)
		if err != nil {
			return nil, err
		}
		// Check if we arrived back at home
		expState, _ := m.GetExpedition(exp.ID)
		if expState != nil && len(expState.PavedWaypoints) > 0 && expState.CurrentWaypointIndex >= len(expState.PavedWaypoints)-1 {
			if pos != nil {
				homeNode := expState.PavedWaypoints[len(expState.PavedWaypoints)-1]
				dx := pos.X - homeNode.X
				dy := pos.Y - homeNode.Y
				dz := pos.Z - homeNode.Z
				if math.Sqrt(dx*dx+dy*dy+dz*dz) <= ArrivalToleranceBlocks {
					// Home reached, unload and unleash
					return m.ReturnAndUnload(ctx, exp.ID, frame.WorldTick)
				}
			}
		}
		if intent != nil {
			return []*civquestv1.AgentIntent{intent}, nil
		}
		return nil, nil

	case ExpeditionStatusCompleted, ExpeditionStatusAborted:
		m.mu.Lock()
		if m.activeExpeditionID == exp.ID {
			m.activeExpeditionID = ""
		}
		m.mu.Unlock()
		return nil, nil
	}

	return nil, nil
}
