package tavern

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Role & Secondary Hobby constants.
const (
	ProfessionTavernkeeper = "TAVERNKEEPER"

	HobbyNone        = "NONE"
	HobbyApiarist    = "APIARIST"
	HobbyJamMaker    = "JAM_MAKER"
	HobbyCiderBrewer = "CIDER_BREWER"
	HobbyChandler    = "CHANDLER"
	HobbyMycologist  = "MYCOLOGIST"
)

// SupportedHobbies lists all recognized emergent secondary hobbies.
var SupportedHobbies = []string{
	HobbyApiarist,
	HobbyJamMaker,
	HobbyCiderBrewer,
	HobbyChandler,
	HobbyMycologist,
}

// Time-of-day bracket boundaries (24000 tick Minecraft day-night cycle).
const (
	TickDawnStart    int64 = 0
	TickWorkStart    int64 = 1000
	TickHobbyStart   int64 = 8000
	TickEveningStart int64 = 11000
	TickBedtimeStart int64 = 13000
	TickNightEnd     int64 = 23500
	TickCycleLength  int64 = 24000
)

// Gathering constants.
const (
	SocialGatheringRadiusBlocks = 3.0
	TavernArrivalTolerance      = 2
)

// IsTavernEveningHours checks if the given world time-of-day falls into the evening tavern gathering window
// (Ticks 11000-24000).
func IsTavernEveningHours(timeOfDay int64) bool {
	tod := ((timeOfDay % TickCycleLength) + TickCycleLength) % TickCycleLength
	return tod >= TickEveningStart && tod < TickCycleLength
}

// IsSecondaryHobbyHours checks if time-of-day falls into the afternoon leisure hobby bracket
// (Ticks 8000-11000).
func IsSecondaryHobbyHours(timeOfDay int64) bool {
	tod := ((timeOfDay % TickCycleLength) + TickCycleLength) % TickCycleLength
	return tod >= TickHobbyStart && tod < TickEveningStart
}

// IsOffShift checks if an agent is outside their primary daytime working hours (1000-8000).
func IsOffShift(timeOfDay int64) bool {
	tod := ((timeOfDay % TickCycleLength) + TickCycleLength) % TickCycleLength
	return tod < TickWorkStart || tod >= TickHobbyStart
}

// ==================== EMERGENT HOBBY UNLOCKING ====================

// Feedstock block and item identifiers for secondary hobbies.
var (
	apiaristBlocks = []string{"minecraft:beehive", "minecraft:bee_nest", "beehive", "bee_nest"}
	apiaristItems  = []string{"minecraft:honeycomb", "minecraft:honey_bottle", "honeycomb", "honey_bottle"}

	jamMakerBlocks = []string{"minecraft:sweet_berry_bush", "minecraft:cave_vines", "minecraft:cave_vines_plant", "sweet_berry_bush"}
	jamMakerItems  = []string{"minecraft:sweet_berries", "minecraft:glow_berries", "minecraft:sugar", "sweet_berries", "glow_berries", "sugar"}

	ciderBrewerBlocks = []string{"minecraft:barrel", "minecraft:oak_leaves", "minecraft:dark_oak_leaves", "barrel"}
	ciderBrewerItems  = []string{"minecraft:apple", "minecraft:potion", "minecraft:glass_bottle", "apple", "glass_bottle"}

	chandlerBlocks = []string{"minecraft:candle", "minecraft:candle_cake", "candle"}
	chandlerItems  = []string{"minecraft:honeycomb", "minecraft:string", "minecraft:candle", "honeycomb", "string", "candle"}

	mycologistBlocks = []string{"minecraft:red_mushroom", "minecraft:brown_mushroom", "minecraft:red_mushroom_block", "minecraft:brown_mushroom_block", "minecraft:mushroom_stem", "red_mushroom", "brown_mushroom"}
	mycologistItems  = []string{"minecraft:red_mushroom", "minecraft:brown_mushroom", "minecraft:mushroom_stew", "red_mushroom", "brown_mushroom", "mushroom_stew"}
)

func matchesAny(name string, patterns []string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, p := range patterns {
		if lower == p || strings.HasSuffix(lower, ":"+p) || strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// CountFeedstocks scans visible interesting blocks and agent inventory to calculate local feedstock abundance per hobby.
func CountFeedstocks(blocks []*civquestv1.VisibleBlock, items []*civquestv1.ItemStackProto) map[string]int {
	abundance := make(map[string]int)
	for _, h := range SupportedHobbies {
		abundance[h] = 0
	}

	for _, b := range blocks {
		if b == nil {
			continue
		}
		name := b.BlockId
		if matchesAny(name, apiaristBlocks) {
			abundance[HobbyApiarist]++
		}
		if matchesAny(name, jamMakerBlocks) {
			abundance[HobbyJamMaker]++
		}
		if matchesAny(name, ciderBrewerBlocks) {
			abundance[HobbyCiderBrewer]++
		}
		if matchesAny(name, chandlerBlocks) {
			abundance[HobbyChandler]++
		}
		if matchesAny(name, mycologistBlocks) {
			abundance[HobbyMycologist]++
		}
	}

	for _, item := range items {
		if item == nil {
			continue
		}
		name := item.ItemId
		qty := int(item.Count)
		if qty <= 0 {
			qty = 1
		}
		if matchesAny(name, apiaristItems) {
			abundance[HobbyApiarist] += qty
		}
		if matchesAny(name, jamMakerItems) {
			abundance[HobbyJamMaker] += qty
		}
		if matchesAny(name, ciderBrewerItems) {
			abundance[HobbyCiderBrewer] += qty
		}
		if matchesAny(name, chandlerItems) {
			abundance[HobbyChandler] += qty
		}
		if matchesAny(name, mycologistItems) {
			abundance[HobbyMycologist] += qty
		}
	}

	return abundance
}

// ScoreHobby calculates an affinity score combining feedstock abundance and personality quirks.
// High curiosity favors natural discovery (APIARIST, MYCOLOGIST).
// High industriousness favors processing and craft (CHANDLER, JAM_MAKER, CIDER_BREWER).
func ScoreHobby(hobby string, abundance int, traits xdg.AgentTraits) float64 {
	if abundance <= 0 {
		return 0.0
	}

	rawAbundance := float64(abundance)
	var traitMultiplier float64

	switch hobby {
	case HobbyApiarist:
		// Curiosity for observing bees + industriousness for honeycomb cultivation
		traitMultiplier = 1.0 + (0.6 * traits.Curiosity) + (0.4 * traits.Industriousness)
	case HobbyJamMaker:
		// Industriousness for berry harvesting + sociability for sharing food
		traitMultiplier = 1.0 + (0.5 * traits.Industriousness) + (0.5 * traits.Sociability)
	case HobbyCiderBrewer:
		// Sociability for tavern hospitality + industriousness for barrel aging
		traitMultiplier = 1.0 + (0.6 * traits.Sociability) + (0.4 * traits.Industriousness)
	case HobbyChandler:
		// High industriousness for careful candle-making + curiosity for lighting
		traitMultiplier = 1.0 + (0.7 * traits.Industriousness) + (0.3 * traits.Curiosity)
	case HobbyMycologist:
		// High curiosity for mushroom spore hunting + bravery for exploring dark caverns
		traitMultiplier = 1.0 + (0.7 * traits.Curiosity) + (0.3 * traits.Bravery)
	default:
		traitMultiplier = 1.0
	}

	return rawAbundance * traitMultiplier
}

// EvaluateEmergentHobby selects the best-fit secondary hobby given visible feedstocks and agent traits.
func EvaluateEmergentHobby(blocks []*civquestv1.VisibleBlock, items []*civquestv1.ItemStackProto, traits xdg.AgentTraits) (string, float64) {
	abundance := CountFeedstocks(blocks, items)

	bestHobby := HobbyNone
	bestScore := 0.0

	for _, h := range SupportedHobbies {
		score := ScoreHobby(h, abundance[h], traits)
		if score > bestScore {
			bestScore = score
			bestHobby = h
		}
	}

	return bestHobby, bestScore
}

// UnlockEmergentHobby dynamically unlocks an unassigned secondary hobby for the agent based on local feedstock
// abundance and personality quirks, persisting the new skill into agent_skills.
func UnlockEmergentHobby(ctx context.Context, agentDB *db.AgentDB, agentCfg *xdg.AgentIdentityConfig, blocks []*civquestv1.VisibleBlock, items []*civquestv1.ItemStackProto) (string, bool, error) {
	if agentCfg == nil {
		return HobbyNone, false, errors.New("agent configuration cannot be nil")
	}

	// If the agent already has a designated secondary hobby, preserve it
	currentHobby := strings.ToUpper(strings.TrimSpace(agentCfg.SecondaryHobby))
	if currentHobby != "" && currentHobby != HobbyNone && currentHobby != "UNASSIGNED" {
		return currentHobby, false, nil
	}

	chosenHobby, score := EvaluateEmergentHobby(blocks, items, agentCfg.Traits)
	if score <= 0 || chosenHobby == HobbyNone {
		return HobbyNone, false, nil
	}

	agentCfg.SecondaryHobby = chosenHobby

	if agentDB != nil {
		skill := db.Skill{
			SkillName: chosenHobby,
			Category:  "SECONDARY_HOBBY",
			Level:     1,
			XP:        0,
		}
		if err := agentDB.UpsertSkill(ctx, skill); err != nil {
			return chosenHobby, false, fmt.Errorf("persisting unlocked secondary hobby %s: %w", chosenHobby, err)
		}
	}

	return chosenHobby, true, nil
}

// ==================== CARTOGRAPHY MAP EDGE MERGING ====================

// CartographySyncReport details the outcome of bidirectional map graph synchronization.
type CartographySyncReport struct {
	NodesAddedToA int `json:"nodes_added_to_a"`
	NodesAddedToB int `json:"nodes_added_to_b"`
	NodesUpdatedA int `json:"nodes_updated_a"`
	NodesUpdatedB int `json:"nodes_updated_b"`
	EdgesAddedToA int `json:"edges_added_to_a"`
	EdgesAddedToB int `json:"edges_added_to_b"`
	EdgesUpdatedA int `json:"edges_updated_a"`
	EdgesUpdatedB int `json:"edges_updated_b"`
}

// TotalExchanged returns total mutations applied during cartography sync.
func (r *CartographySyncReport) TotalExchanged() int {
	if r == nil {
		return 0
	}
	return r.NodesAddedToA + r.NodesAddedToB + r.NodesUpdatedA + r.NodesUpdatedB +
		r.EdgesAddedToA + r.EdgesAddedToB + r.EdgesUpdatedA + r.EdgesUpdatedB
}

// SyncCartography merges topological map nodes and edges bidirectionally between two settlers' map databases.
// To preserve foreign key constraints, all nodes are synchronized before edges are added.
func SyncCartography(ctx context.Context, dbA, dbB *db.AgentDB) (*CartographySyncReport, error) {
	if dbA == nil || dbB == nil {
		return nil, errors.New("agent databases cannot be nil")
	}

	nodesA, edgesA, err := dbA.GetMapGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching map graph from agent A: %w", err)
	}

	nodesB, edgesB, err := dbB.GetMapGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching map graph from agent B: %w", err)
	}

	report := &CartographySyncReport{}

	mapA := make(map[string]db.MapNode)
	for _, n := range nodesA {
		mapA[n.ID] = n
	}
	mapB := make(map[string]db.MapNode)
	for _, n := range nodesB {
		mapB[n.ID] = n
	}

	// 1. Sync Nodes: B -> A
	for _, nodeB := range nodesB {
		nodeA, exists := mapA[nodeB.ID]
		if !exists {
			if err := dbA.AddMapNode(ctx, nodeB); err != nil {
				return nil, fmt.Errorf("adding node %s to agent A: %w", nodeB.ID, err)
			}
			report.NodesAddedToA++
		} else {
			// Update if B has newer discovery or safer score
			if nodeB.SafetyScore > nodeA.SafetyScore || nodeB.DiscoveredTick > nodeA.DiscoveredTick {
				merged := nodeA
				if nodeB.SafetyScore > nodeA.SafetyScore {
					merged.SafetyScore = nodeB.SafetyScore
				}
				if nodeB.DiscoveredTick > nodeA.DiscoveredTick {
					merged.DiscoveredTick = nodeB.DiscoveredTick
				}
				if err := dbA.AddMapNode(ctx, merged); err != nil {
					return nil, fmt.Errorf("updating node %s in agent A: %w", merged.ID, err)
				}
				report.NodesUpdatedA++
			}
		}
	}

	// 2. Sync Nodes: A -> B
	for _, nodeA := range nodesA {
		nodeB, exists := mapB[nodeA.ID]
		if !exists {
			if err := dbB.AddMapNode(ctx, nodeA); err != nil {
				return nil, fmt.Errorf("adding node %s to agent B: %w", nodeA.ID, err)
			}
			report.NodesAddedToB++
		} else {
			if nodeA.SafetyScore > nodeB.SafetyScore || nodeA.DiscoveredTick > nodeB.DiscoveredTick {
				merged := nodeB
				if nodeA.SafetyScore > nodeB.SafetyScore {
					merged.SafetyScore = nodeA.SafetyScore
				}
				if nodeA.DiscoveredTick > nodeB.DiscoveredTick {
					merged.DiscoveredTick = nodeA.DiscoveredTick
				}
				if err := dbB.AddMapNode(ctx, merged); err != nil {
					return nil, fmt.Errorf("updating node %s in agent B: %w", merged.ID, err)
				}
				report.NodesUpdatedB++
			}
		}
	}

	// 3. Sync Edges: B -> A
	edgeMapA := make(map[string]db.MapEdge)
	for _, e := range edgesA {
		edgeMapA[e.ID] = e
	}
	for _, edgeB := range edgesB {
		edgeA, exists := edgeMapA[edgeB.ID]
		if !exists {
			if err := dbA.AddMapEdge(ctx, edgeB); err != nil {
				return nil, fmt.Errorf("adding edge %s to agent A: %w", edgeB.ID, err)
			}
			report.EdgesAddedToA++
		} else {
			// Update if road paved or danger level changed
			if (!edgeA.IsPaved && edgeB.IsPaved) || edgeB.DangerLevel < edgeA.DangerLevel {
				merged := edgeA
				if edgeB.IsPaved {
					merged.IsPaved = true
				}
				if edgeB.DangerLevel < edgeA.DangerLevel {
					merged.DangerLevel = edgeB.DangerLevel
				}
				if err := dbA.AddMapEdge(ctx, merged); err != nil {
					return nil, fmt.Errorf("updating edge %s in agent A: %w", merged.ID, err)
				}
				report.EdgesUpdatedA++
			}
		}
	}

	// 4. Sync Edges: A -> B
	edgeMapB := make(map[string]db.MapEdge)
	for _, e := range edgesB {
		edgeMapB[e.ID] = e
	}
	for _, edgeA := range edgesA {
		edgeB, exists := edgeMapB[edgeA.ID]
		if !exists {
			if err := dbB.AddMapEdge(ctx, edgeA); err != nil {
				return nil, fmt.Errorf("adding edge %s to agent B: %w", edgeA.ID, err)
			}
			report.EdgesAddedToB++
		} else {
			if (!edgeB.IsPaved && edgeA.IsPaved) || edgeA.DangerLevel < edgeB.DangerLevel {
				merged := edgeB
				if edgeA.IsPaved {
					merged.IsPaved = true
				}
				if edgeA.DangerLevel < edgeB.DangerLevel {
					merged.DangerLevel = edgeA.DangerLevel
				}
				if err := dbB.AddMapEdge(ctx, merged); err != nil {
					return nil, fmt.Errorf("updating edge %s in agent B: %w", merged.ID, err)
				}
				report.EdgesUpdatedB++
			}
		}
	}

	return report, nil
}

// ==================== RUMOR DIFFUSION ====================

// DiffuseRumors shares undiffused rumors from the clan rumor store to a listener agent,
// recording diffusion entries and incrementing spread counts.
func DiffuseRumors(ctx context.Context, rumorsDB *db.RumorsDB, speakerID, listenerID string, currentTick int64, maxRumors int) ([]*db.Rumor, error) {
	if rumorsDB == nil {
		return nil, errors.New("rumors database cannot be nil")
	}
	if speakerID == "" || listenerID == "" {
		return nil, errors.New("speaker and listener IDs cannot be empty")
	}

	undiffused, err := rumorsDB.GetUndiffusedRumorsForAgent(ctx, listenerID, maxRumors)
	if err != nil {
		return nil, fmt.Errorf("querying undiffused rumors for %s: %w", listenerID, err)
	}

	var diffused []*db.Rumor
	for _, r := range undiffused {
		record := db.RumorDiffusionRecord{
			RumorID:         r.RumorID,
			SpeakerAgentID:  speakerID,
			ListenerAgentID: listenerID,
			WorldTick:       currentTick,
		}
		if err := rumorsDB.RecordDiffusion(ctx, record); err != nil {
			return diffused, fmt.Errorf("recording rumor diffusion for %s: %w", r.RumorID, err)
		}
		r.SpreadCount++
		diffused = append(diffused, r)
	}

	return diffused, nil
}

// CreateHazardRumor generates a hazard intelligence report when an agent spots a dangerous entity.
func CreateHazardRumor(ctx context.Context, rumorsDB *db.RumorsDB, sourceAgentID, sourceAgentName string, entity *civquestv1.VisibleEntity, currentTick int64) (*db.Rumor, error) {
	if rumorsDB == nil || entity == nil {
		return nil, errors.New("invalid arguments for hazard rumor")
	}

	entityType := strings.ToLower(entity.EntityType)
	isHostile := strings.Contains(entityType, "creeper") ||
		strings.Contains(entityType, "zombie") ||
		strings.Contains(entityType, "skeleton") ||
		strings.Contains(entityType, "spider") ||
		strings.Contains(entityType, "witch") ||
		strings.Contains(entityType, "warden") ||
		strings.Contains(entityType, "pillager")

	if !isHostile {
		return nil, nil
	}

	rumorID := fmt.Sprintf("hazard-%s-%d", sourceAgentID, currentTick)
	content := fmt.Sprintf("Hostile %s spotted at (%.1f, %.1f, %.1f)", entityType, entity.Position.X, entity.Position.Y, entity.Position.Z)

	rumor := &db.Rumor{
		RumorID:         rumorID,
		Category:        db.RumorCategoryHazard,
		Content:         content,
		SourceAgentID:   sourceAgentID,
		SourceAgentName: sourceAgentName,
		LocationX:       entity.Position.X,
		LocationY:       entity.Position.Y,
		LocationZ:       entity.Position.Z,
		LocationName:    fmt.Sprintf("Near (%d, %d)", int(entity.Position.X), int(entity.Position.Z)),
		Credibility:     0.90,
		OriginTick:      currentTick,
		CreatedTick:     currentTick,
		ExpiresTick:     currentTick + 24000, // Expires after 1 Minecraft day
	}

	if err := rumorsDB.AddRumor(ctx, rumor); err != nil {
		return nil, err
	}
	return rumor, nil
}

// CreateResourceRumor generates a resource discovery report when high-value blocks are scanned.
func CreateResourceRumor(ctx context.Context, rumorsDB *db.RumorsDB, sourceAgentID, sourceAgentName string, block *civquestv1.VisibleBlock, currentTick int64) (*db.Rumor, error) {
	if rumorsDB == nil || block == nil || block.Pos == nil {
		return nil, errors.New("invalid arguments for resource rumor")
	}

	blockID := strings.ToLower(block.BlockId)
	isResource := strings.Contains(blockID, "diamond") ||
		strings.Contains(blockID, "ancient_debris") ||
		strings.Contains(blockID, "beehive") ||
		strings.Contains(blockID, "bee_nest") ||
		strings.Contains(blockID, "emerald") ||
		strings.Contains(blockID, "gold_ore")

	if !isResource {
		return nil, nil
	}

	rumorID := fmt.Sprintf("resource-%s-%d", sourceAgentID, currentTick)
	content := fmt.Sprintf("Resource deposit %s discovered at (%d, %d, %d)", blockID, block.Pos.X, block.Pos.Y, block.Pos.Z)

	rumor := &db.Rumor{
		RumorID:         rumorID,
		Category:        db.RumorCategoryResource,
		Content:         content,
		SourceAgentID:   sourceAgentID,
		SourceAgentName: sourceAgentName,
		LocationX:       float64(block.Pos.X),
		LocationY:       float64(block.Pos.Y),
		LocationZ:       float64(block.Pos.Z),
		LocationName:    fmt.Sprintf("Resource Site (%d, %d)", block.Pos.X, block.Pos.Z),
		Credibility:     0.95,
		OriginTick:      currentTick,
		CreatedTick:     currentTick,
		ExpiresTick:     0, // Permanent resource landmark
	}

	if err := rumorsDB.AddRumor(ctx, rumor); err != nil {
		return nil, err
	}
	return rumor, nil
}

// ==================== TAVERN MANAGER & EVENING GATHERING ====================

// TavernExchangeReport summarizes the intel and cartography swapped between settlers at the tavern.
type TavernExchangeReport struct {
	Cartography *CartographySyncReport `json:"cartography"`
	RumorsToA   []*db.Rumor            `json:"rumors_to_a"`
	RumorsToB   []*db.Rumor            `json:"rumors_to_b"`
}

// TavernManager coordinates tavern functional zones, tavernkeeper positions, patrons,
// evening social gatherings, and rumor diffusion.
type TavernManager struct {
	mu            sync.RWMutex
	civicDB       *db.CivicRegistryDB
	rumorsDB      *db.RumorsDB
	tavernkeepers map[string]*civquestv1.Vec3 // agentID -> position
	patrons       map[string]*civquestv1.Vec3 // agentID -> position
}

// NewTavernManager constructs a TavernManager backed by clan storage.
func NewTavernManager(storage *db.ClanStorage) *TavernManager {
	if storage == nil {
		return NewTavernManagerWithDBs(nil, nil)
	}
	return NewTavernManagerWithDBs(storage.CivicRegistry, storage.Rumors)
}

// NewTavernManagerWithDBs constructs a TavernManager with explicit database handles.
func NewTavernManagerWithDBs(civicDB *db.CivicRegistryDB, rumorsDB *db.RumorsDB) *TavernManager {
	return &TavernManager{
		civicDB:       civicDB,
		rumorsDB:      rumorsDB,
		tavernkeepers: make(map[string]*civquestv1.Vec3),
		patrons:       make(map[string]*civquestv1.Vec3),
	}
}

// RegisterTavernkeeper records or updates a tavernkeeper's active position in the settlement.
func (m *TavernManager) RegisterTavernkeeper(agentID string, pos *civquestv1.Vec3) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pos == nil {
		delete(m.tavernkeepers, agentID)
		return
	}
	m.tavernkeepers[agentID] = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
}

// RegisterPatron records an off-duty settler present in or near the tavern.
func (m *TavernManager) RegisterPatron(agentID string, pos *civquestv1.Vec3) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pos == nil {
		delete(m.patrons, agentID)
		return
	}
	m.patrons[agentID] = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
}

// GetTavernkeeperPos returns the position of an active tavernkeeper, or nil if none registered.
func (m *TavernManager) GetTavernkeeperPos() *civquestv1.Vec3 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, pos := range m.tavernkeepers {
		return &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	}
	return nil
}

// FindTavernZone searches for a registered functional zone of type TAVERN in the civic registry.
func (m *TavernManager) FindTavernZone(ctx context.Context) (*db.FunctionalZone, error) {
	if m.civicDB == nil {
		return nil, errors.New("civic registry database is not configured")
	}

	zones, err := m.civicDB.ListZonesByTypeContext(ctx, db.ZoneTypeTavern)
	if err != nil {
		return nil, fmt.Errorf("querying tavern zones: %w", err)
	}
	if len(zones) == 0 {
		return nil, nil
	}
	return zones[0], nil
}

// EvaluateGatheringIntent produces navigation and social intents for an agent during evening hours (11000-24000).
// Off-duty agents outside the tavern navigate to the Tavern zone.
// Once inside, agents gather around the Tavernkeeper (or tavern center if no tavernkeeper is present).
func (m *TavernManager) EvaluateGatheringIntent(ctx context.Context, agentID string, profession string, currentPos *civquestv1.Vec3, zone *db.FunctionalZone) (*civquestv1.AgentIntent, error) {
	if zone == nil {
		return nil, nil
	}
	if currentPos == nil {
		return nil, errors.New("current agent position cannot be nil")
	}

	centerX := float64(zone.MinX+zone.MaxX) / 2.0
	centerY := float64(zone.MinY)
	centerZ := float64(zone.MinZ+zone.MaxZ) / 2.0

	isInside := zone.Contains(int(math.Floor(currentPos.X)), int(math.Floor(currentPos.Y)), int(math.Floor(currentPos.Z)))
	isTavernkeeper := strings.EqualFold(strings.TrimSpace(profession), ProfessionTavernkeeper)

	// 1. Outside the tavern: navigate to tavern functional zone
	if !isInside {
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: centerX,
						Y: centerY,
						Z: centerZ,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: TavernArrivalTolerance,
				},
			},
		}, nil
	}

	// 2. Inside the tavern: Tavernkeeper stands at station/counter
	if isTavernkeeper {
		m.RegisterTavernkeeper(agentID, currentPos)
		// Tavernkeeper is at post; no forced navigation needed
		return nil, nil
	}

	// 3. Inside the tavern: Off-duty patron gathers around the Tavernkeeper
	m.RegisterPatron(agentID, currentPos)
	keeperPos := m.GetTavernkeeperPos()

	var target *civquestv1.Vec3
	if keeperPos != nil {
		target = keeperPos
	} else {
		target = &civquestv1.Vec3{X: centerX, Y: centerY, Z: centerZ}
	}

	dx := currentPos.X - target.X
	dz := currentPos.Z - target.Z
	dist := math.Sqrt(dx*dx + dz*dz)

	// If farther than social circle radius (3 blocks), walk closer to Tavernkeeper
	if dist > SocialGatheringRadiusBlocks {
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target:           target,
					SpeedMultiplier:  0.8, // relaxed social stroll
					ArrivalTolerance: TavernArrivalTolerance,
				},
			},
		}, nil
	}

	// Within social circle radius: agent is gathered at the social hub
	return nil, nil
}

// ExchangeTavernIntel coordinates rumor sharing and cartography synchronization between two settlers gathered at the tavern.
func (m *TavernManager) ExchangeTavernIntel(ctx context.Context, agentA, agentB string, dbA, dbB *db.AgentDB, tick int64) (*TavernExchangeReport, error) {
	report := &TavernExchangeReport{}

	// 1. Merge cartography edges and nodes
	if dbA != nil && dbB != nil {
		syncRep, err := SyncCartography(ctx, dbA, dbB)
		if err != nil {
			return nil, fmt.Errorf("syncing cartography between %s and %s: %w", agentA, agentB, err)
		}
		report.Cartography = syncRep
	}

	// 2. Diffuse rumors bidirectionally via RumorsDB
	if m.rumorsDB != nil {
		// A tells B
		diffusedToB, err := DiffuseRumors(ctx, m.rumorsDB, agentA, agentB, tick, 5)
		if err != nil {
			return nil, fmt.Errorf("diffusing rumors from %s to %s: %w", agentA, agentB, err)
		}
		report.RumorsToB = diffusedToB

		// B tells A
		diffusedToA, err := DiffuseRumors(ctx, m.rumorsDB, agentB, agentA, tick, 5)
		if err != nil {
			return nil, fmt.Errorf("diffusing rumors from %s to %s: %w", agentB, agentA, err)
		}
		report.RumorsToA = diffusedToA
	}

	return report, nil
}

// HandleOffShiftTransition evaluates time-of-day and duty blockers to transition an agent to their secondary hobby
// or evening tavern social routine.
func HandleOffShiftTransition(agentCfg *xdg.AgentIdentityConfig, state *system1.AgentState, timeOfDay int64, isPrimaryBlocked bool) system1.RoutineState {
	if IsTavernEveningHours(timeOfDay) {
		if state != nil {
			state.SetCurrentRoutine(system1.RoutineTavernSocial)
		}
		return system1.RoutineTavernSocial
	}

	if IsSecondaryHobbyHours(timeOfDay) || isPrimaryBlocked {
		if state != nil {
			state.SetCurrentRoutine(system1.RoutineSecondaryHobby)
		}
		return system1.RoutineSecondaryHobby
	}

	if state != nil {
		state.SetCurrentRoutine(system1.RoutinePrimaryDuty)
	}
	return system1.RoutinePrimaryDuty
}
