package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// Read operations proceed concurrently with up to 10 connections in WAL mode,
// while write transactions are serialized in Go user space via dedicated write mutexes
// (memWriteMu and mapWriteMu) to completely prevent SQLITE_BUSY contention.
type AgentDB struct {
	mu         sync.RWMutex
	memWriteMu sync.Mutex
	mapWriteMu sync.Mutex
	dataDir    string
	memoryDB   *sql.DB
	mapDB      *sql.DB
	isClosed   bool
	hasVec0    bool
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
// Validates agentName to prevent directory traversal and injection attacks.
func OpenAgentDBForAgent(resolver *xdg.Resolver, agentName string) (*AgentDB, error) {
	if err := xdg.ValidateAgentName(agentName); err != nil {
		return nil, err
	}
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

	// SQLite connection pooling for WAL mode: concurrent reads with up to 10 connections
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
	tx, err := a.memoryDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning memory migration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	queries := []string{
		`CREATE TABLE IF NOT EXISTS episodic_memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			world_tick INTEGER NOT NULL,
			event_category TEXT NOT NULL CHECK(event_category IN ('COMBAT', 'TRADE', 'CHAT', 'DEATH', 'DISCOVERY', 'BETRAYAL')),
			saliency_score REAL NOT NULL CHECK(saliency_score >= 0.0 AND saliency_score <= 1.0),
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
			entity_type TEXT CHECK(entity_type IN ('PLAYER', 'AGENT')),
			disposition_score REAL DEFAULT 0.0 CHECK(disposition_score >= -100.0 AND disposition_score <= 100.0),
			familiarity REAL DEFAULT 0.0 CHECK(familiarity >= 0.0 AND familiarity <= 1.0),
			kinship_role TEXT DEFAULT 'NONE' CHECK(kinship_role IN ('NONE', 'SPOUSE', 'PARENT', 'CHILD', 'WIDOWED')),
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
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("executing memory migration query: %w", err)
		}
	}

	// Check if episodic_memories_vec table already exists and whether it uses vec0
	var schemaSQL string
	err = tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE name = 'episodic_memories_vec'").Scan(&schemaSQL)
	if err == nil {
		if strings.Contains(strings.ToLower(schemaSQL), "vec0") {
			a.hasVec0 = true
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		// Attempt virtual table vec0 first if sqlite-vec module is registered
		var modName string
		mErr := tx.QueryRowContext(ctx, "SELECT name FROM pragma_module_list WHERE name = 'vec0'").Scan(&modName)
		if mErr == nil && modName == "vec0" {
			_, vErr := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE episodic_memories_vec USING vec0(
				memory_id INTEGER PRIMARY KEY,
				embedding FLOAT[768]
			);`)
			if vErr == nil {
				a.hasVec0 = true
			}
		}
		if !a.hasVec0 {
			// Fallback to relational table with IEEE-754 binary BLOB embedding
			fallbackQuery := `CREATE TABLE episodic_memories_vec (
				memory_id INTEGER PRIMARY KEY,
				embedding BLOB NOT NULL,
				FOREIGN KEY (memory_id) REFERENCES episodic_memories(id) ON DELETE CASCADE
			);`
			if _, rErr := tx.ExecContext(ctx, fallbackQuery); rErr != nil {
				return fmt.Errorf("creating episodic_memories_vec fallback table: %w", rErr)
			}
		}
	} else {
		return fmt.Errorf("checking episodic_memories_vec table: %w", err)
	}

	return tx.Commit()
}

func (a *AgentDB) migrateMapDB(ctx context.Context) error {
	tx, err := a.mapDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning map migration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	queries := []string{
		`CREATE TABLE IF NOT EXISTS map_nodes (
			id TEXT PRIMARY KEY,
			x REAL NOT NULL,
			y REAL NOT NULL,
			z REAL NOT NULL,
			label TEXT NOT NULL,
			category TEXT NOT NULL,
			safety_score REAL DEFAULT 1.0 CHECK(safety_score >= 0.0 AND safety_score <= 1.0),
			discovered_tick INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_map_nodes_category ON map_nodes(category);`,
		`CREATE TABLE IF NOT EXISTS map_edges (
			id TEXT PRIMARY KEY,
			from_node_id TEXT NOT NULL,
			to_node_id TEXT NOT NULL,
			distance REAL NOT NULL CHECK(distance >= 0.0),
			danger_level REAL DEFAULT 0.0 CHECK(danger_level >= 0.0),
			is_paved BOOLEAN DEFAULT FALSE,
			FOREIGN KEY (from_node_id) REFERENCES map_nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (to_node_id) REFERENCES map_nodes(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_map_edges_from ON map_edges(from_node_id);`,
		`CREATE INDEX IF NOT EXISTS idx_map_edges_to ON map_edges(to_node_id);`,
	}

	for _, q := range queries {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("executing map migration query: %w", err)
		}
	}

	return tx.Commit()
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
// atomically in memory.db. Validates that non-empty embeddings have exactly 768 finite dimensions.
// Returns the generated memory ID.
func (a *AgentDB) InsertEpisodicMemory(ctx context.Context, memory EpisodicMemory, embedding []float32) (int64, error) {
	if len(embedding) > 0 {
		if err := ValidateVector(embedding); err != nil {
			return 0, err
		}
	}

	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

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

// GetEpisodicMemory retrieves an episodic memory by its ID, or returns nil, nil if not found.
func (a *AgentDB) GetEpisodicMemory(ctx context.Context, id int64) (*EpisodicMemory, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		FROM episodic_memories
		WHERE id = ?;
	`
	var mem EpisodicMemory
	err := a.memoryDB.QueryRowContext(ctx, query, id).Scan(
		&mem.ID, &mem.WorldTick, &mem.EventCategory, &mem.SaliencyScore,
		&mem.ActorsJSON, &mem.LocationCoords, &mem.SummaryText, &mem.Chronicled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying episodic memory: %w", err)
	}
	return &mem, nil
}

// MarkMemoryChronicled updates the chronicled flag to true for an episodic memory.
// Returns ErrNotFound if the specified memory ID does not exist.
func (a *AgentDB) MarkMemoryChronicled(ctx context.Context, id int64) error {
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	res, err := a.memoryDB.ExecContext(ctx, "UPDATE episodic_memories SET chronicled = TRUE WHERE id = ?;", id)
	if err != nil {
		return fmt.Errorf("marking memory chronicled: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUnchronicledMemories retrieves unchronicled memories meeting either the minimum saliency score
// or possessing the 'DEATH' event category, ordered chronologically by world_tick.
func (a *AgentDB) GetUnchronicledMemories(ctx context.Context, minSaliency float64, limit int) ([]EpisodicMemory, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT id, world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		FROM episodic_memories
		WHERE chronicled = FALSE AND (saliency_score >= ? OR event_category = 'DEATH')
		ORDER BY world_tick ASC
		LIMIT ?;
	`

	rows, err := a.memoryDB.QueryContext(ctx, query, minSaliency, limit)
	if err != nil {
		return nil, fmt.Errorf("querying unchronicled memories: %w", err)
	}
	defer rows.Close()

	var memories []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(
			&m.ID, &m.WorldTick, &m.EventCategory, &m.SaliencyScore,
			&m.ActorsJSON, &m.LocationCoords, &m.SummaryText, &m.Chronicled,
		); err != nil {
			return nil, fmt.Errorf("scanning unchronicled memory row: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating unchronicled memory rows: %w", err)
	}

	return memories, nil
}

// MarkMemoriesChronicledBatch marks multiple episodic memories as chronicled in a single write lock.
func (a *AgentDB) MarkMemoriesChronicledBatch(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("UPDATE episodic_memories SET chronicled = TRUE WHERE id IN (%s);", strings.Join(placeholders, ", "))
	if _, err := a.memoryDB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("batch marking memories chronicled: %w", err)
	}
	return nil
}

// SearchEpisodicMemories retrieves the top-K highest ranking episodic memories.
// If vec0 virtual tables are available, it executes an indexed sqlite-vec MATCH query.
// Otherwise, it streams stored vector embeddings and evaluates cosine similarity using
// the host CPU superscalar unrolled kernel (ADR-013), maintaining a bounded top-K collection
// and subsequently hydrating winning memory attributes.
func (a *AgentDB) SearchEpisodicMemories(ctx context.Context, queryEmbedding []float32, limit int) ([]EpisodicMemoryMatch, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	if len(queryEmbedding) == 0 {
		return nil, ErrEmptyQueryEmbedding
	}
	if err := ValidateVector(queryEmbedding); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	// 1. Dual-mode vec0 virtual table query path
	if a.hasVec0 {
		queryBytes := Float32SliceToBytes(queryEmbedding)
		rows, err := a.memoryDB.QueryContext(ctx, `
			SELECT 
				m.id, m.world_tick, m.event_category, m.saliency_score,
				m.actors_json, m.location_coords, m.summary_text, m.chronicled,
				v.distance
			FROM episodic_memories_vec v
			JOIN episodic_memories m ON m.id = v.memory_id
			WHERE v.embedding MATCH ? AND k = ?
			ORDER BY v.distance ASC;
		`, queryBytes, limit)
		if err == nil {
			defer rows.Close()
			var matches []EpisodicMemoryMatch
			for rows.Next() {
				var mem EpisodicMemory
				var dist float64
				if err := rows.Scan(
					&mem.ID, &mem.WorldTick, &mem.EventCategory, &mem.SaliencyScore,
					&mem.ActorsJSON, &mem.LocationCoords, &mem.SummaryText, &mem.Chronicled,
					&dist,
				); err != nil {
					return nil, fmt.Errorf("scanning vec0 match row: %w", err)
				}
				sim := float32(1.0 - dist)
				if sim < -1.0 {
					sim = -1.0
				} else if sim > 1.0 {
					sim = 1.0
				}
				matches = append(matches, EpisodicMemoryMatch{
					Memory:     mem,
					Similarity: sim,
				})
			}
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("iterating vec0 match rows: %w", err)
			}
			if matches == nil {
				return []EpisodicMemoryMatch{}, nil
			}
			return matches, nil
		}
		// If vec0 execution encountered an error, fall through to CPU search
	}

	// 2. Relational fallback mode with bounded CPU cosine similarity scan
	rows, err := a.memoryDB.QueryContext(ctx, "SELECT memory_id, embedding FROM episodic_memories_vec;")
	if err != nil {
		return nil, fmt.Errorf("querying episodic memory embeddings: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id  int64
		sim float32
	}
	var candidates []candidate

	for rows.Next() {
		var id int64
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			return nil, fmt.Errorf("scanning memory vector: %w", err)
		}
		targetVec := BytesToFloat32Slice(embBytes)
		if len(targetVec) == 0 {
			continue
		}
		sim := CosineSimilarity(queryEmbedding, targetVec)
		candidates = append(candidates, candidate{id: id, sim: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating vector rows: %w", err)
	}

	if len(candidates) == 0 {
		return []EpisodicMemoryMatch{}, nil
	}

	// Sort candidates descending by similarity score, breaking ties deterministically by ID ascending
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sim != candidates[j].sim {
			return candidates[i].sim > candidates[j].sim
		}
		return candidates[i].id < candidates[j].id
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	// Hydrate full memory attributes only for winning top-K IDs
	idList := make([]any, len(candidates))
	placeholders := make([]string, len(candidates))
	for i, c := range candidates {
		idList[i] = c.id
		placeholders[i] = "?"
	}

	fetchQuery := fmt.Sprintf(`
		SELECT id, world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		FROM episodic_memories
		WHERE id IN (%s);
	`, strings.Join(placeholders, ", "))

	memRows, err := a.memoryDB.QueryContext(ctx, fetchQuery, idList...)
	if err != nil {
		return nil, fmt.Errorf("fetching winning memories: %w", err)
	}
	defer memRows.Close()

	memMap := make(map[int64]EpisodicMemory, len(candidates))
	for memRows.Next() {
		var mem EpisodicMemory
		if err := memRows.Scan(
			&mem.ID, &mem.WorldTick, &mem.EventCategory, &mem.SaliencyScore,
			&mem.ActorsJSON, &mem.LocationCoords, &mem.SummaryText, &mem.Chronicled,
		); err != nil {
			return nil, fmt.Errorf("scanning winning memory row: %w", err)
		}
		memMap[mem.ID] = mem
	}
	if err := memRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating winning memory rows: %w", err)
	}

	// Reassemble matches in the exact order of candidates
	matches := make([]EpisodicMemoryMatch, 0, len(candidates))
	for _, c := range candidates {
		if mem, ok := memMap[c.id]; ok {
			matches = append(matches, EpisodicMemoryMatch{
				Memory:     mem,
				Similarity: c.sim,
			})
		}
	}

	return matches, nil
}

// ==================== RELATIONSHIPS ====================

// UpsertRelationship creates or updates an agent's relationship entry with a player or agent.
func (a *AgentDB) UpsertRelationship(ctx context.Context, rel Relationship) error {
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if rel.EntityUUID == "" {
		return errors.New("entity_uuid cannot be empty")
	}
	if rel.EntityType != "PLAYER" && rel.EntityType != "AGENT" {
		return fmt.Errorf("invalid entity_type %q: must be 'PLAYER' or 'AGENT'", rel.EntityType)
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
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

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
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

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
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

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

// DeleteMapNode removes a map node by ID. Cascade deletion automatically removes incident edges.
// Returns ErrNotFound if the node does not exist.
func (a *AgentDB) DeleteMapNode(ctx context.Context, id string) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if id == "" {
		return errors.New("node ID cannot be empty")
	}

	res, err := a.mapDB.ExecContext(ctx, "DELETE FROM map_nodes WHERE id = ?;", id)
	if err != nil {
		return fmt.Errorf("deleting map node: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMapEdge registers or updates a traversable edge between two topological nodes.
func (a *AgentDB) AddMapEdge(ctx context.Context, edge MapEdge) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

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

// GetMapEdge retrieves a map edge by ID, or returns nil, nil if not found.
func (a *AgentDB) GetMapEdge(ctx context.Context, id string) (*MapEdge, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, from_node_id, to_node_id, distance, danger_level, is_paved
		FROM map_edges
		WHERE id = ?;
	`
	var edge MapEdge
	err := a.mapDB.QueryRowContext(ctx, query, id).Scan(
		&edge.ID, &edge.FromNodeID, &edge.ToNodeID, &edge.Distance, &edge.DangerLevel, &edge.IsPaved,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying map edge: %w", err)
	}
	return &edge, nil
}

// DeleteMapEdge removes a map edge by ID.
// Returns ErrNotFound if the edge does not exist.
func (a *AgentDB) DeleteMapEdge(ctx context.Context, id string) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if id == "" {
		return errors.New("edge ID cannot be empty")
	}

	res, err := a.mapDB.ExecContext(ctx, "DELETE FROM map_edges WHERE id = ?;", id)
	if err != nil {
		return fmt.Errorf("deleting map edge: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMapGraph retrieves the complete topological map graph consisting of all nodes and edges
// within a read-only snapshot transaction to prevent torn snapshots.
func (a *AgentDB) GetMapGraph(ctx context.Context) ([]MapNode, []MapEdge, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, nil, ErrClosed
	}

	tx, err := a.mapDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("beginning read-only map graph tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Fetch all nodes
	nodeRows, err := tx.QueryContext(ctx, `
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
	edgeRows, err := tx.QueryContext(ctx, `
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

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("committing map graph read tx: %w", err)
	}

	if nodes == nil {
		nodes = []MapNode{}
	}
	if edges == nil {
		edges = []MapEdge{}
	}

	return nodes, edges, nil
}

// Close cleanly terminates both memory.db and map_graph.db connections,
// first acquiring write mutexes to ensure in-flight write transactions drain safely.
// Checkpoint executes a WAL checkpoint (TRUNCATE) on both memory.db and map_graph.db,
// flushing write-ahead logs to the main database files and truncating wal files.
func (a *AgentDB) Checkpoint(ctx context.Context) error {
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	var errs []error
	if a.memoryDB != nil {
		if _, err := a.memoryDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
			errs = append(errs, fmt.Errorf("checkpointing memory.db: %w", err))
		}
	}
	if a.mapDB != nil {
		if _, err := a.mapDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
			errs = append(errs, fmt.Errorf("checkpointing map_graph.db: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (a *AgentDB) Close() error {
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isClosed {
		return nil
	}
	a.isClosed = true

	var errs []error
	if a.memoryDB != nil {
		_, _ = a.memoryDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		if err := a.memoryDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing memory.db: %w", err))
		}
		a.memoryDB = nil
	}
	if a.mapDB != nil {
		_, _ = a.mapDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		if err := a.mapDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing map_graph.db: %w", err))
		}
		a.mapDB = nil
	}

	return errors.Join(errs...)
}
