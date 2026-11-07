package db

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

// Relationship models an agent's interpersonal standing and affinity with a player or fellow settler.
// Stored in memory.db in the relationships table.
type Relationship struct {
	EntityUUID          string  `json:"entity_uuid"`
	EntityName          string  `json:"entity_name"`
	EntityType          string  `json:"entity_type"`       // 'PLAYER' or 'SETTLER'
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
