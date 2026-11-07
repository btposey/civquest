package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// Common sentinel errors for database operations.
var (
	ErrNotFound            = errors.New("record not found")
	ErrEmptyQueryEmbedding = errors.New("query embedding cannot be empty")
	ErrClosed              = errors.New("database connection is closed")
)

// AgentDB manages SQLite connections and schemas for an autonomous agent's
// memory.db and map_graph.db in $XDG_DATA_HOME/civquest/agents/<agent_name>/.
type AgentDB struct {
	mu       sync.RWMutex
	dataDir  string
	memoryDB *sql.DB
	mapDB    *sql.DB
	isClosed bool
	hasVec0  bool
}

// OpenAgentDB opens and automatically runs schema migrations on memory.db and map_graph.db
// inside the specified agentDataDir. The directory is created if it does not exist.
func OpenAgentDB(agentDataDir string) (*AgentDB, error) {
	if err := os.MkdirAll(agentDataDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create agent data directory %s: %w", agentDataDir, err)
	}

	memPath := filepath.Join(agentDataDir, "memory.db")
	mapPath := filepath.Join(agentDataDir, "map_graph.db")

	memDB, err := openAndConfigureDB(memPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open memory database %s: %w", memPath, err)
	}

	mapDB, err := openAndConfigureDB(mapPath)
	if err != nil {
		_ = memDB.Close()
		return nil, fmt.Errorf("failed to open map database %s: %w", mapPath, err)
	}

	agentDB := &AgentDB{
		dataDir:  agentDataDir,
		memoryDB: memDB,
		mapDB:    mapDB,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agentDB.migrateMemoryDB(ctx); err != nil {
		_ = agentDB.Close()
		return nil, fmt.Errorf("migrating memory.db: %w", err)
	}

	if err := agentDB.migrateMapDB(ctx); err != nil {
		_ = agentDB.Close()
		return nil, fmt.Errorf("migrating map_graph.db: %w", err)
	}

	return agentDB, nil
}

// OpenAgentDBForAgent is a convenience constructor resolving the agent data directory
// via the provided XDG resolver (or default resolver if nil).
func OpenAgentDBForAgent(resolver *xdg.Resolver, agentName string) (*AgentDB, error) {
	if resolver == nil {
		resolver = xdg.NewResolver()
	}
	agentDataDir := resolver.AgentDataDir(agentName)
	return OpenAgentDB(agentDataDir)
}

func openAndConfigureDB(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// SQLite connection pooling for WAL mode
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	// Apply WAL, busy_timeout, synchronous, and foreign keys PRAGMAs
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	return db, nil
}

func (a *AgentDB) migrateMemoryDB(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS episodic_memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			world_tick INTEGER NOT NULL,
			event_category TEXT NOT NULL,
			saliency_score REAL NOT NULL,
			actors_json TEXT NOT NULL,
			location_coords TEXT NOT NULL,
			summary_text TEXT NOT NULL,
			chronicled BOOLEAN DEFAULT FALSE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_episodic_memories_tick ON episodic_memories(world_tick);`,
		`CREATE INDEX IF NOT EXISTS idx_episodic_memories_category ON episodic_memories(event_category);`,
		`CREATE INDEX IF NOT EXISTS idx_episodic_memories_saliency ON episodic_memories(saliency_score);`,
		`CREATE TABLE IF NOT EXISTS relationships (
			entity_uuid TEXT PRIMARY KEY,
			entity_name TEXT NOT NULL,
			entity_type TEXT CHECK(entity_type IN ('PLAYER', 'SETTLER')),
			disposition_score REAL DEFAULT 0.0,
			familiarity REAL DEFAULT 0.0,
			kinship_role TEXT DEFAULT 'NONE',
			last_interaction_tick INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_relationships_type ON relationships(entity_type);`,
		`CREATE TABLE IF NOT EXISTS agent_skills (
			skill_name TEXT PRIMARY KEY,
			category TEXT CHECK(category IN ('PRIMARY', 'SECONDARY_HOBBY')),
			xp INTEGER DEFAULT 0,
			level INTEGER DEFAULT 1,
			workstation_pos TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_skills_cat ON agent_skills(category);`,
	}

	for _, q := range queries {
		if _, err := a.memoryDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("executing memory migration query: %w", err)
		}
	}

	// Migrate episodic_memories_vec: Check if table already exists
	var tableName string
	err := a.memoryDB.QueryRowContext(ctx, "SELECT name FROM sqlite_schema WHERE name = 'episodic_memories_vec'").Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		// Attempt virtual table vec0 first if sqlite-vec is available
		_, vErr := a.memoryDB.ExecContext(ctx, `CREATE VIRTUAL TABLE episodic_memories_vec USING vec0(
			memory_id INTEGER PRIMARY KEY,
			embedding FLOAT[768]
		);`)
		if vErr == nil {
			a.hasVec0 = true
		} else {
			// Fallback to relational table with IEEE-754 binary BLOB embedding
			fallbackQuery := `CREATE TABLE episodic_memories_vec (
				memory_id INTEGER PRIMARY KEY,
				embedding BLOB NOT NULL,
				FOREIGN KEY (memory_id) REFERENCES episodic_memories(id) ON DELETE CASCADE
			);`
			if _, rErr := a.memoryDB.ExecContext(ctx, fallbackQuery); rErr != nil {
				return fmt.Errorf("creating episodic_memories_vec fallback table: %w", rErr)
			}
		}
	} else if err != nil {
		return fmt.Errorf("checking episodic_memories_vec table: %w", err)
	}

	return nil
}

func (a *AgentDB) migrateMapDB(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS map_nodes (
			id TEXT PRIMARY KEY,
			x REAL NOT NULL,
			y REAL NOT NULL,
			z REAL NOT NULL,
			label TEXT NOT NULL,
			category TEXT NOT NULL,
			safety_score REAL DEFAULT 1.0,
			discovered_tick INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_map_nodes_category ON map_nodes(category);`,
		`CREATE TABLE IF NOT EXISTS map_edges (
			id TEXT PRIMARY KEY,
			from_node_id TEXT NOT NULL,
			to_node_id TEXT NOT NULL,
			distance REAL NOT NULL,
			danger_level REAL DEFAULT 0.0,
			is_paved BOOLEAN DEFAULT FALSE,
			FOREIGN KEY (from_node_id) REFERENCES map_nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (to_node_id) REFERENCES map_nodes(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_map_edges_from ON map_edges(from_node_id);`,
		`CREATE INDEX IF NOT EXISTS idx_map_edges_to ON map_edges(to_node_id);`,
	}

	for _, q := range queries {
		if _, err := a.mapDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("executing map migration query: %w", err)
		}
	}

	return nil
}

// DataDir returns the path to the agent's data directory.
func (a *AgentDB) DataDir() string {
	return a.dataDir
}

// MemoryDB returns the raw *sql.DB handle for memory.db.
func (a *AgentDB) MemoryDB() *sql.DB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.memoryDB
}

// MapDB returns the raw *sql.DB handle for map_graph.db.
func (a *AgentDB) MapDB() *sql.DB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mapDB
}

// HasVec0 reports whether sqlite-vec's vec0 virtual table is active.
func (a *AgentDB) HasVec0() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hasVec0
}

// ==================== EPISODIC MEMORIES ====================

// InsertEpisodicMemory inserts an episodic memory and its corresponding vector embedding
// atomically in memory.db. Returns the generated memory ID.
func (a *AgentDB) InsertEpisodicMemory(ctx context.Context, memory EpisodicMemory, embedding []float32) (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return 0, ErrClosed
	}

	tx, err := a.memoryDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning memory insert tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO episodic_memories (
			world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		) VALUES (?, ?, ?, ?, ?, ?, ?);
	`, memory.WorldTick, memory.EventCategory, memory.SaliencyScore, memory.ActorsJSON, memory.LocationCoords, memory.SummaryText, memory.Chronicled)
	if err != nil {
		return 0, fmt.Errorf("inserting into episodic_memories: %w", err)
	}

	memID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("retrieving memory last_insert_id: %w", err)
	}

	if len(embedding) > 0 {
		embBytes := Float32SliceToBytes(embedding)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO episodic_memories_vec (memory_id, embedding) VALUES (?, ?);
		`, memID, embBytes)
		if err != nil {
			return 0, fmt.Errorf("inserting into episodic_memories_vec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing memory insert tx: %w", err)
	}

	return memID, nil
}

// SearchEpisodicMemories calculates cosine similarity between the query embedding
// and stored episodic memories using the SIMD/unrolled CPU kernel (ADR-013),
// returning the top-K highest ranking matches.
func (a *AgentDB) SearchEpisodicMemories(ctx context.Context, queryEmbedding []float32, limit int) ([]EpisodicMemoryMatch, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	if len(queryEmbedding) == 0 {
		return nil, ErrEmptyQueryEmbedding
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := a.memoryDB.QueryContext(ctx, `
		SELECT 
			m.id, m.world_tick, m.event_category, m.saliency_score,
			m.actors_json, m.location_coords, m.summary_text, m.chronicled,
			v.embedding
		FROM episodic_memories m
		INNER JOIN episodic_memories_vec v ON m.id = v.memory_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying episodic memories with embeddings: %w", err)
	}
	defer rows.Close()

	var matches []EpisodicMemoryMatch

	for rows.Next() {
		var mem EpisodicMemory
		var embBytes []byte

		if err := rows.Scan(
			&mem.ID, &mem.WorldTick, &mem.EventCategory, &mem.SaliencyScore,
			&mem.ActorsJSON, &mem.LocationCoords, &mem.SummaryText, &mem.Chronicled,
			&embBytes,
		); err != nil {
			return nil, fmt.Errorf("scanning episodic memory row: %w", err)
		}

		targetVec := BytesToFloat32Slice(embBytes)
		if len(targetVec) == 0 {
			continue
		}

		sim := CosineSimilarity(queryEmbedding, targetVec)
		matches = append(matches, EpisodicMemoryMatch{
			Memory:     mem,
			Similarity: sim,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating memory rows: %w", err)
	}

	if len(matches) == 0 {
		return []EpisodicMemoryMatch{}, nil
	}

	// Sort descending by cosine similarity score
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches, nil
}

// ==================== RELATIONSHIPS ====================

// UpsertRelationship creates or updates an agent's relationship entry with a player or settler.
func (a *AgentDB) UpsertRelationship(ctx context.Context, rel Relationship) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if rel.EntityUUID == "" {
		return errors.New("entity_uuid cannot be empty")
	}
	if rel.EntityType != "PLAYER" && rel.EntityType != "SETTLER" {
		return fmt.Errorf("invalid entity_type %q: must be 'PLAYER' or 'SETTLER'", rel.EntityType)
	}
	if rel.KinshipRole == "" {
		rel.KinshipRole = "NONE"
	}

	query := `
		INSERT INTO relationships (
			entity_uuid, entity_name, entity_type, disposition_score, familiarity, kinship_role, last_interaction_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_uuid) DO UPDATE SET
			entity_name = excluded.entity_name,
			entity_type = excluded.entity_type,
			disposition_score = excluded.disposition_score,
			familiarity = excluded.familiarity,
			kinship_role = excluded.kinship_role,
			last_interaction_tick = excluded.last_interaction_tick;
	`
	_, err := a.memoryDB.ExecContext(ctx, query,
		rel.EntityUUID, rel.EntityName, rel.EntityType,
		rel.DispositionScore, rel.Familiarity, rel.KinshipRole, rel.LastInteractionTick,
	)
	if err != nil {
		return fmt.Errorf("upserting relationship: %w", err)
	}
	return nil
}

// GetRelationship retrieves a relationship by entity UUID, or returns nil, nil if not found.
func (a *AgentDB) GetRelationship(ctx context.Context, entityUUID string) (*Relationship, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT entity_uuid, entity_name, entity_type, disposition_score, familiarity, kinship_role, last_interaction_tick
		FROM relationships
		WHERE entity_uuid = ?;
	`
	var rel Relationship
	err := a.memoryDB.QueryRowContext(ctx, query, entityUUID).Scan(
		&rel.EntityUUID, &rel.EntityName, &rel.EntityType,
		&rel.DispositionScore, &rel.Familiarity, &rel.KinshipRole, &rel.LastInteractionTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying relationship: %w", err)
	}
	return &rel, nil
}

// ListRelationships lists all relationships ordered by most recently interacted first.
func (a *AgentDB) ListRelationships(ctx context.Context) ([]Relationship, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT entity_uuid, entity_name, entity_type, disposition_score, familiarity, kinship_role, last_interaction_tick
		FROM relationships
		ORDER BY last_interaction_tick DESC, entity_name ASC;
	`
	rows, err := a.memoryDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var rel Relationship
		if err := rows.Scan(
			&rel.EntityUUID, &rel.EntityName, &rel.EntityType,
			&rel.DispositionScore, &rel.Familiarity, &rel.KinshipRole, &rel.LastInteractionTick,
		); err != nil {
			return nil, fmt.Errorf("scanning relationship: %w", err)
		}
		rels = append(rels, rel)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating relationship rows: %w", err)
	}
	if rels == nil {
		return []Relationship{}, nil
	}
	return rels, nil
}

// DeleteRelationship removes a relationship entry by UUID.
func (a *AgentDB) DeleteRelationship(ctx context.Context, entityUUID string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	_, err := a.memoryDB.ExecContext(ctx, "DELETE FROM relationships WHERE entity_uuid = ?", entityUUID)
	if err != nil {
		return fmt.Errorf("deleting relationship: %w", err)
	}
	return nil
}

// ==================== AGENT SKILLS ====================

// UpsertSkill inserts or updates an agent skill level and XP.
func (a *AgentDB) UpsertSkill(ctx context.Context, skill Skill) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if skill.SkillName == "" {
		return errors.New("skill_name cannot be empty")
	}
	if skill.Category != "PRIMARY" && skill.Category != "SECONDARY_HOBBY" {
		return fmt.Errorf("invalid skill category %q: must be 'PRIMARY' or 'SECONDARY_HOBBY'", skill.Category)
	}
	if skill.Level < 1 {
		skill.Level = 1
	}

	query := `
		INSERT INTO agent_skills (
			skill_name, category, xp, level, workstation_pos
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET
			category = excluded.category,
			xp = excluded.xp,
			level = excluded.level,
			workstation_pos = excluded.workstation_pos;
	`
	_, err := a.memoryDB.ExecContext(ctx, query,
		skill.SkillName, skill.Category, skill.XP, skill.Level, skill.WorkstationPos,
	)
	if err != nil {
		return fmt.Errorf("upserting skill: %w", err)
	}
	return nil
}

// GetSkill retrieves a skill by its unique name, or returns nil, nil if not found.
func (a *AgentDB) GetSkill(ctx context.Context, skillName string) (*Skill, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT skill_name, category, xp, level, workstation_pos
		FROM agent_skills
		WHERE skill_name = ?;
	`
	var skill Skill
	err := a.memoryDB.QueryRowContext(ctx, query, skillName).Scan(
		&skill.SkillName, &skill.Category, &skill.XP, &skill.Level, &skill.WorkstationPos,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying skill: %w", err)
	}
	return &skill, nil
}

// ListSkills returns all skills registered for this agent.
func (a *AgentDB) ListSkills(ctx context.Context) ([]Skill, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT skill_name, category, xp, level, workstation_pos
		FROM agent_skills
		ORDER BY category ASC, level DESC, xp DESC;
	`
	rows, err := a.memoryDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.SkillName, &s.Category, &s.XP, &s.Level, &s.WorkstationPos); err != nil {
			return nil, fmt.Errorf("scanning skill: %w", err)
		}
		skills = append(skills, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating skill rows: %w", err)
	}
	if skills == nil {
		return []Skill{}, nil
	}
	return skills, nil
}

// ==================== MAP GRAPH (NODES & EDGES) ====================

// AddMapNode registers or updates a topological waypoint node.
func (a *AgentDB) AddMapNode(ctx context.Context, node MapNode) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if node.ID == "" {
		return errors.New("map node ID cannot be empty")
	}

	query := `
		INSERT INTO map_nodes (
			id, x, y, z, label, category, safety_score, discovered_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			x = excluded.x,
			y = excluded.y,
			z = excluded.z,
			label = excluded.label,
			category = excluded.category,
			safety_score = excluded.safety_score,
			discovered_tick = excluded.discovered_tick;
	`
	_, err := a.mapDB.ExecContext(ctx, query,
		node.ID, node.X, node.Y, node.Z, node.Label, node.Category, node.SafetyScore, node.DiscoveredTick,
	)
	if err != nil {
		return fmt.Errorf("adding map node: %w", err)
	}
	return nil
}

// GetMapNode retrieves a map node by ID, or returns nil, nil if not found.
func (a *AgentDB) GetMapNode(ctx context.Context, id string) (*MapNode, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, x, y, z, label, category, safety_score, discovered_tick
		FROM map_nodes
		WHERE id = ?;
	`
	var node MapNode
	err := a.mapDB.QueryRowContext(ctx, query, id).Scan(
		&node.ID, &node.X, &node.Y, &node.Z, &node.Label, &node.Category, &node.SafetyScore, &node.DiscoveredTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying map node: %w", err)
	}
	return &node, nil
}

// AddMapEdge registers or updates a traversable edge between two topological nodes.
func (a *AgentDB) AddMapEdge(ctx context.Context, edge MapEdge) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if edge.ID == "" {
		return errors.New("map edge ID cannot be empty")
	}
	if edge.FromNodeID == "" || edge.ToNodeID == "" {
		return errors.New("map edge from_node_id and to_node_id must not be empty")
	}

	query := `
		INSERT INTO map_edges (
			id, from_node_id, to_node_id, distance, danger_level, is_paved
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			from_node_id = excluded.from_node_id,
			to_node_id = excluded.to_node_id,
			distance = excluded.distance,
			danger_level = excluded.danger_level,
			is_paved = excluded.is_paved;
	`
	_, err := a.mapDB.ExecContext(ctx, query,
		edge.ID, edge.FromNodeID, edge.ToNodeID, edge.Distance, edge.DangerLevel, edge.IsPaved,
	)
	if err != nil {
		return fmt.Errorf("adding map edge: %w", err)
	}
	return nil
}

// GetMapGraph retrieves the complete topological map graph consisting of all nodes and edges.
func (a *AgentDB) GetMapGraph(ctx context.Context) ([]MapNode, []MapEdge, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, nil, ErrClosed
	}

	// Fetch all nodes
	nodeRows, err := a.mapDB.QueryContext(ctx, `
		SELECT id, x, y, z, label, category, safety_score, discovered_tick
		FROM map_nodes
		ORDER BY id ASC;
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching map nodes: %w", err)
	}
	defer nodeRows.Close()

	var nodes []MapNode
	for nodeRows.Next() {
		var n MapNode
		if err := nodeRows.Scan(&n.ID, &n.X, &n.Y, &n.Z, &n.Label, &n.Category, &n.SafetyScore, &n.DiscoveredTick); err != nil {
			return nil, nil, fmt.Errorf("scanning map node: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating map node rows: %w", err)
	}

	// Fetch all edges
	edgeRows, err := a.mapDB.QueryContext(ctx, `
		SELECT id, from_node_id, to_node_id, distance, danger_level, is_paved
		FROM map_edges
		ORDER BY id ASC;
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching map edges: %w", err)
	}
	defer edgeRows.Close()

	var edges []MapEdge
	for edgeRows.Next() {
		var e MapEdge
		if err := edgeRows.Scan(&e.ID, &e.FromNodeID, &e.ToNodeID, &e.Distance, &e.DangerLevel, &e.IsPaved); err != nil {
			return nil, nil, fmt.Errorf("scanning map edge: %w", err)
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating map edge rows: %w", err)
	}

	if nodes == nil {
		nodes = []MapNode{}
	}
	if edges == nil {
		edges = []MapEdge{}
	}

	return nodes, edges, nil
}

// Close cleanly terminates both memory.db and map_graph.db connections.
func (a *AgentDB) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isClosed {
		return nil
	}
	a.isClosed = true

	var errs []error
	if a.memoryDB != nil {
		if err := a.memoryDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing memory.db: %w", err))
		}
		a.memoryDB = nil
	}
	if a.mapDB != nil {
		if err := a.mapDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing map_graph.db: %w", err))
		}
		a.mapDB = nil
	}

	return errors.Join(errs...)
}
