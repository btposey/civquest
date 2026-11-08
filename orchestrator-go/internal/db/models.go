package db

import "time"

// EpisodicMemory represents a single experiential event or observation recorded by an agent.
// Stored in memory.db in the episodic_memories table.
type EpisodicMemory struct {
	ID             int64   `json:"id"`
	WorldTick      int64   `json:"world_tick"`
	EventCategory  string  `json:"event_category"` // 'COMBAT', 'TRADE', 'CHAT', 'DEATH', 'DISCOVERY', 'BETRAYAL'
	SaliencyScore  float64 `json:"saliency_score"` // [0.0 - 1.0]
	ActorsJSON     string  `json:"actors_json"`
	LocationCoords string  `json:"location_coords"`
	SummaryText    string  `json:"summary_text"`
	Chronicled     bool    `json:"chronicled"`
}

// EpisodicMemoryMatch pairs an EpisodicMemory with its calculated cosine similarity score.
type EpisodicMemoryMatch struct {
	Memory     EpisodicMemory `json:"memory"`
	Similarity float32        `json:"similarity"`
}

// Relationship models an agent's interpersonal standing and affinity with a player or fellow agent.
// Stored in memory.db in the relationships table.
type Relationship struct {
	EntityUUID          string  `json:"entity_uuid"`
	EntityName          string  `json:"entity_name"`
	EntityType          string  `json:"entity_type"`       // 'PLAYER' or 'AGENT'
	DispositionScore    float64 `json:"disposition_score"` // [-100.0 to +100.0]
	Familiarity         float64 `json:"familiarity"`       // [0.0 to 1.0]
	KinshipRole         string  `json:"kinship_role"`      // 'NONE', 'SPOUSE', 'PARENT', 'CHILD', 'WIDOWED'
	LastInteractionTick int64   `json:"last_interaction_tick"`
}

// Skill models an agent's vocational career progression or emergent hobby mastery.
// Stored in memory.db in the agent_skills table.
type Skill struct {
	SkillName      string `json:"skill_name"`
	Category       string `json:"category"` // 'PRIMARY' or 'SECONDARY_HOBBY'
	XP             int64  `json:"xp"`
	Level          int    `json:"level"`
	WorkstationPos string `json:"workstation_pos"`
}

// MapNode represents a discovered topological waypoint or landmark.
// Stored in map_graph.db in the map_nodes table.
type MapNode struct {
	ID             string  `json:"id"`
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	Z              float64 `json:"z"`
	Label          string  `json:"label"`
	Category       string  `json:"category"`
	SafetyScore    float64 `json:"safety_score"` // [0.0 - 1.0]
	DiscoveredTick int64   `json:"discovered_tick"`
}

// MapEdge represents a traversable path connecting two topological waypoints.
// Stored in map_graph.db in the map_edges table.
type MapEdge struct {
	ID          string  `json:"id"`
	FromNodeID  string  `json:"from_node_id"`
	ToNodeID    string  `json:"to_node_id"`
	Distance    float64 `json:"distance"`
	DangerLevel float64 `json:"danger_level"`
	IsPaved     bool    `json:"is_paved"`
}

// HeroPrestige tracks human player reputation and honorary title within a clan.
// Stored in reputation.db in the hero_prestige table.
type HeroPrestige struct {
	PlayerUUID          string    `json:"player_uuid"`
	PlayerName          string    `json:"player_name"`
	Score               float64   `json:"score"`
	Title               string    `json:"title"`
	LastInteractionTick int64     `json:"last_interaction_tick"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PrestigeLedgerEntry records an individual delta change to a player's hero prestige score.
// Stored in reputation.db in the prestige_ledger table.
type PrestigeLedgerEntry struct {
	ID         int64     `json:"id,omitempty"`
	PlayerUUID string    `json:"player_uuid"`
	PlayerName string    `json:"player_name,omitempty"`
	AgentID    string    `json:"agent_id"`
	Delta      float64   `json:"delta"`
	Reason     string    `json:"reason"`
	WorldTick  int64     `json:"world_tick"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

// GranaryStock tracks stockpiled resources and food rations in a settlement's granary.
// Stored in civic_registry.db in the settlement_granary table.
type GranaryStock struct {
	ItemID           string `json:"item_id"`
	Quantity         int    `json:"quantity"`
	ReservedQuantity int    `json:"reserved_quantity"`
	LastUpdatedTick  int64  `json:"last_updated_tick"`
}

// Available returns unreserved quantity.
func (g *GranaryStock) Available() int {
	if g == nil {
		return 0
	}
	avail := g.Quantity - g.ReservedQuantity
	if avail < 0 {
		return 0
	}
	return avail
}

// ConstructionProject represents an active or planned architectural structure under spatial construction.
// Stored in civic_registry.db in the construction_projects table.
type ConstructionProject struct {
	ProjectID             string  `json:"project_id"`
	BlueprintID           string  `json:"blueprint_id"`
	Status                string  `json:"status"` // 'PLANNED', 'IN_PROGRESS', 'COMPLETED', 'HALTED', 'ABANDONED'
	AnchorX               int     `json:"anchor_x"`
	AnchorY               int     `json:"anchor_y"`
	AnchorZ               int     `json:"anchor_z"`
	Quadrant              string  `json:"quadrant"`         // 'NW', 'NE', 'SW', 'SE'
	ProgressPercent       float64 `json:"progress_percent"` // [0.0 - 100.0]
	MaterialsReservedJSON string  `json:"materials_reserved_json"`
	CreatedTick           int64   `json:"created_tick"`
	UpdatedTick           int64   `json:"updated_tick"`
}

// FunctionalZone designates a spatial boundary for clan activities (farming, residential, defense, storage).
// Stored in civic_registry.db in the functional_zones table.
type FunctionalZone struct {
	ZoneID       string `json:"zone_id"`
	ZoneType     string `json:"zone_type"` // 'GRANARY', 'RESIDENTIAL', 'WORKSHOP', 'FARM', 'TAVERN', 'DEFENSE', etc.
	MinX         int    `json:"min_x"`
	MinY         int    `json:"min_y"`
	MinZ         int    `json:"min_z"`
	MaxX         int    `json:"max_x"`
	MaxY         int    `json:"max_y"`
	MaxZ         int    `json:"max_z"`
	OwnerAgentID string `json:"owner_agent_id,omitempty"`
	Name         string `json:"name"`
	CreatedTick  int64  `json:"created_tick"`
}

// Contains checks whether a given 3D coordinate falls inside the functional zone bounds.
func (z *FunctionalZone) Contains(x, y, zCoord int) bool {
	if z == nil {
		return false
	}
	return x >= z.MinX && x <= z.MaxX &&
		y >= z.MinY && y <= z.MaxY &&
		zCoord >= z.MinZ && zCoord <= z.MaxZ
}

// ClanAffinity models inter-clan diplomatic relations, treaties, and caravan trade volume.
// Stored in diplomacy.db in the clan_affinities table.
type ClanAffinity struct {
	TargetClanID     string  `json:"target_clan_id"`
	AffinityScore    float64 `json:"affinity_score"` // [-100.0 to +100.0]
	Status           string  `json:"status"`         // 'ALLIED', 'FRIENDLY', 'NEUTRAL', 'SUSPICIOUS', 'HOSTILE', 'WAR'
	TradeVolume      int     `json:"trade_volume"`
	ActiveTreaty     string  `json:"active_treaty"` // 'NONE', 'NON_AGGRESSION', 'FREE_TRADE', 'DEFENSIVE_PACT', 'MUTUAL_DEFENSE'
	TreatyExpiryTick int64   `json:"treaty_expiry_tick"`
	LastUpdatedTick  int64   `json:"last_updated_tick"`
}

// IsTreatyActive checks if an active treaty is established and not expired.
// A TreatyExpiryTick of 0 denotes an indefinite or permanent treaty.
func (a *ClanAffinity) IsTreatyActive(worldTick int64) bool {
	if a == nil || a.ActiveTreaty == "" || a.ActiveTreaty == "NONE" {
		return false
	}
	return a.TreatyExpiryTick == 0 || worldTick < a.TreatyExpiryTick
}

// Landmark records a named geographic waypoint, cultural site, or natural formation with historical lore.
// Stored in landmarks.db in the landmarks table.
type Landmark struct {
	LandmarkID          string  `json:"landmark_id"`
	Name                string  `json:"name"`
	Category            string  `json:"category"` // 'NATURAL', 'SETTLEMENT', 'RUINS', 'RESOURCE', 'MEMORIAL', 'MONUMENT'
	X                   float64 `json:"x"`
	Y                   float64 `json:"y"`
	Z                   float64 `json:"z"`
	SignpostLine1       string  `json:"signpost_line1"`
	SignpostLine2       string  `json:"signpost_line2"`
	LoreText            string  `json:"lore_text"`
	DiscoveredByAgentID string  `json:"discovered_by_agent_id"`
	DiscoveredTick      int64   `json:"discovered_tick"`
}

// SharecroppingContract defines an agricultural tenancy agreement between a human player and clan agents.
// Stored in contracts.db in the sharecropping_contracts table.
type SharecroppingContract struct {
	ContractID         string  `json:"contract_id"`
	PlayerUUID         string  `json:"player_uuid"`
	PlayerName         string  `json:"player_name"`
	AgentID            string  `json:"agent_id"`
	PlotMinX           int     `json:"plot_min_x"`
	PlotMinY           int     `json:"plot_min_y"`
	PlotMinZ           int     `json:"plot_min_z"`
	PlotMaxX           int     `json:"plot_max_x"`
	PlotMaxY           int     `json:"plot_max_y"`
	PlotMaxZ           int     `json:"plot_max_z"`
	ChestX             int     `json:"chest_x"`
	ChestY             int     `json:"chest_y"`
	ChestZ             int     `json:"chest_z"`
	SplitPlayerPercent float64 `json:"split_player_percent"` // e.g. 60.0 (player gets 60%)
	SplitClanPercent   float64 `json:"split_clan_percent"`   // e.g. 40.0 (clan granary gets 40%)
	CropType           string  `json:"crop_type"`            // e.g. 'minecraft:wheat'
	Status             string  `json:"status"`               // 'ACTIVE', 'FULFILLED', 'TERMINATED', 'BREACHED', 'EXPIRED'
	CreatedTick        int64   `json:"created_tick"`
	ExpiresTick        int64   `json:"expires_tick"`
}
