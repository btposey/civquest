package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
		`CREATE TABLE IF NOT EXISTS trade_valuations (
			item_id TEXT PRIMARY KEY,
			base_value REAL NOT NULL CHECK(base_value >= 0.0),
			need_multiplier REAL NOT NULL DEFAULT 1.0 CHECK(need_multiplier >= 0.0),
			min_accept_ratio REAL NOT NULL DEFAULT 0.85 CHECK(min_accept_ratio > 0.0),
			rationale TEXT DEFAULT '',
			updated_tick INTEGER DEFAULT 0
		);`,
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
		`CREATE TABLE IF NOT EXISTS agent_pois (
			poi_id TEXT PRIMARY KEY,
			category TEXT NOT NULL CHECK(category IN ('WORKSTATION', 'CONTAINER', 'HABITATION', 'RESOURCE', 'HAZARD', 'CROP')),
			block_id TEXT NOT NULL,
			x REAL NOT NULL,
			y REAL NOT NULL,
			z REAL NOT NULL,
			access_x REAL NOT NULL,
			access_y REAL NOT NULL,
			access_z REAL NOT NULL,
			elevation_layer TEXT NOT NULL CHECK(elevation_layer IN ('SURFACE', 'DUGOUT', 'UNDERGROUND', 'ELEVATED')),
			safety_score REAL NOT NULL CHECK(safety_score >= 0.0 AND safety_score <= 1.0),
			discovered_tick INTEGER NOT NULL,
			last_verified_tick INTEGER NOT NULL,
			reliability REAL NOT NULL DEFAULT 1.0 CHECK(reliability >= 0.0 AND reliability <= 1.0),
			is_active INTEGER NOT NULL DEFAULT 1
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_pois_cat_pos ON agent_pois(category, x, y, z);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_pois_layer ON agent_pois(elevation_layer);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_pois_active ON agent_pois(is_active);`,
		`CREATE TABLE IF NOT EXISTS trail_corridors (
			id TEXT PRIMARY KEY,
			from_poi_id TEXT NOT NULL,
			to_poi_id TEXT NOT NULL,
			waypoints_json TEXT NOT NULL,
			total_distance REAL NOT NULL CHECK(total_distance >= 0.0),
			is_paved INTEGER NOT NULL DEFAULT 0,
			traverse_count INTEGER NOT NULL DEFAULT 0 CHECK(traverse_count >= 0)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_trail_corridors_from ON trail_corridors(from_poi_id);`,
		`CREATE INDEX IF NOT EXISTS idx_trail_corridors_to ON trail_corridors(to_poi_id);`,
		`CREATE INDEX IF NOT EXISTS idx_trail_corridors_endpoints ON trail_corridors(from_poi_id, to_poi_id);`,
		`CREATE TABLE IF NOT EXISTS horizon_sectors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			origin_x REAL,
			origin_y REAL,
			origin_z REAL,
			sector_index INTEGER,
			bearing_deg REAL,
			hit_distance REAL,
			feature_type TEXT,
			hit_block_id TEXT,
			hit_x REAL,
			hit_y REAL,
			hit_z REAL,
			elevation_delta REAL,
			occluded INTEGER,
			open_horizon INTEGER,
			surveyed_tick INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_horizon_sectors_tick ON horizon_sectors(surveyed_tick);`,
		`CREATE INDEX IF NOT EXISTS idx_horizon_sectors_feature ON horizon_sectors(feature_type);`,
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

// GetRecentMemories retrieves the most recent episodic memories ordered by id DESC (up to limit).
func (a *AgentDB) GetRecentMemories(ctx context.Context, limit int) ([]EpisodicMemory, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := a.memoryDB.QueryContext(ctx, `
		SELECT id, world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		FROM episodic_memories
		ORDER BY id DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent memories: %w", err)
	}
	defer rows.Close()

	var memories []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(
			&m.ID, &m.WorldTick, &m.EventCategory, &m.SaliencyScore,
			&m.ActorsJSON, &m.LocationCoords, &m.SummaryText, &m.Chronicled,
		); err != nil {
			return nil, fmt.Errorf("scanning recent memory row: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent memory rows: %w", err)
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

// ==================== AGENT POIs & TRAIL CORRIDORS (ADR-022) ====================

// AddOrUpdatePOI inserts or updates a persistent Point of Interest in map_graph.db.
func (a *AgentDB) AddOrUpdatePOI(ctx context.Context, poi AgentPOI) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if poi.POIID == "" {
		return errors.New("poi_id cannot be empty")
	}

	isActive := 0
	if poi.IsActive {
		isActive = 1
	}

	query := `
		INSERT INTO agent_pois (
			poi_id, category, block_id, x, y, z,
			access_x, access_y, access_z, elevation_layer,
			safety_score, discovered_tick, last_verified_tick,
			reliability, is_active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(poi_id) DO UPDATE SET
			category = excluded.category,
			block_id = excluded.block_id,
			x = excluded.x,
			y = excluded.y,
			z = excluded.z,
			access_x = excluded.access_x,
			access_y = excluded.access_y,
			access_z = excluded.access_z,
			elevation_layer = excluded.elevation_layer,
			safety_score = excluded.safety_score,
			discovered_tick = excluded.discovered_tick,
			last_verified_tick = excluded.last_verified_tick,
			reliability = excluded.reliability,
			is_active = excluded.is_active;
	`
	_, err := a.mapDB.ExecContext(ctx, query,
		poi.POIID, poi.Category, poi.BlockID,
		poi.X, poi.Y, poi.Z,
		poi.AccessX, poi.AccessY, poi.AccessZ, poi.ElevationLayer,
		poi.SafetyScore, poi.DiscoveredTick, poi.LastVerifiedTick,
		poi.Reliability, isActive,
	)
	if err != nil {
		return fmt.Errorf("upserting agent POI: %w", err)
	}
	return nil
}

// GetPOI retrieves a persistent Point of Interest by its POIID, or returns nil, nil if not found.
func (a *AgentDB) GetPOI(ctx context.Context, poiID string) (*AgentPOI, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT poi_id, category, block_id, x, y, z,
		       access_x, access_y, access_z, elevation_layer,
		       safety_score, discovered_tick, last_verified_tick,
		       reliability, is_active
		FROM agent_pois
		WHERE poi_id = ?;
	`
	var poi AgentPOI
	var isActive int
	err := a.mapDB.QueryRowContext(ctx, query, poiID).Scan(
		&poi.POIID, &poi.Category, &poi.BlockID,
		&poi.X, &poi.Y, &poi.Z,
		&poi.AccessX, &poi.AccessY, &poi.AccessZ, &poi.ElevationLayer,
		&poi.SafetyScore, &poi.DiscoveredTick, &poi.LastVerifiedTick,
		&poi.Reliability, &isActive,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying agent POI: %w", err)
	}
	poi.IsActive = isActive != 0
	return &poi, nil
}

// ListPOIs retrieves Points of Interest filtered optionally by category and active status.
func (a *AgentDB) ListPOIs(ctx context.Context, category string, activeOnly bool) ([]AgentPOI, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	var clauses []string
	var args []any
	if category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, category)
	}
	if activeOnly {
		clauses = append(clauses, "is_active = 1")
	}

	query := `
		SELECT poi_id, category, block_id, x, y, z,
		       access_x, access_y, access_z, elevation_layer,
		       safety_score, discovered_tick, last_verified_tick,
		       reliability, is_active
		FROM agent_pois`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY discovered_tick ASC, poi_id ASC;"

	rows, err := a.mapDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing agent POIs: %w", err)
	}
	defer rows.Close()

	var pois []AgentPOI
	for rows.Next() {
		var poi AgentPOI
		var isActive int
		if err := rows.Scan(
			&poi.POIID, &poi.Category, &poi.BlockID,
			&poi.X, &poi.Y, &poi.Z,
			&poi.AccessX, &poi.AccessY, &poi.AccessZ, &poi.ElevationLayer,
			&poi.SafetyScore, &poi.DiscoveredTick, &poi.LastVerifiedTick,
			&poi.Reliability, &isActive,
		); err != nil {
			return nil, fmt.Errorf("scanning agent POI: %w", err)
		}
		poi.IsActive = isActive != 0
		pois = append(pois, poi)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent POI rows: %w", err)
	}
	if pois == nil {
		return []AgentPOI{}, nil
	}
	return pois, nil
}

// InvalidatePOI marks a Point of Interest as inactive (is_active = 0) in map_graph.db.
// Returns ErrNotFound if no POI matches poiID.
func (a *AgentDB) InvalidatePOI(ctx context.Context, poiID string) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if poiID == "" {
		return errors.New("poi_id cannot be empty")
	}

	res, err := a.mapDB.ExecContext(ctx, "UPDATE agent_pois SET is_active = 0 WHERE poi_id = ?;", poiID)
	if err != nil {
		return fmt.Errorf("invalidating agent POI: %w", err)
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

// DecayPOIReliability reduces a POI's reliability score by delta, clamped to [0.0, 1.0].
// Returns ErrNotFound if no POI matches poiID.
func (a *AgentDB) DecayPOIReliability(ctx context.Context, poiID string, delta float64) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if poiID == "" {
		return errors.New("poi_id cannot be empty")
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta < 0 {
		return errors.New("decay delta must be a finite non-negative number")
	}

	res, err := a.mapDB.ExecContext(ctx,
		"UPDATE agent_pois SET reliability = MAX(0.0, MIN(1.0, reliability - ?)) WHERE poi_id = ?;",
		delta, poiID,
	)
	if err != nil {
		return fmt.Errorf("decaying agent POI reliability: %w", err)
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

// AddTrailCorridor registers or updates a learned breadcrumb waypoint corridor between two POIs.
func (a *AgentDB) AddTrailCorridor(ctx context.Context, corridor TrailCorridor) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	if corridor.ID == "" {
		return errors.New("trail corridor ID cannot be empty")
	}
	if corridor.FromPOIID == "" || corridor.ToPOIID == "" {
		return errors.New("trail corridor from_poi_id and to_poi_id must not be empty")
	}
	if corridor.WaypointsJSON == "" {
		corridor.WaypointsJSON = "[]"
	}

	isPaved := 0
	if corridor.IsPaved {
		isPaved = 1
	}

	query := `
		INSERT INTO trail_corridors (
			id, from_poi_id, to_poi_id, waypoints_json, total_distance, is_paved, traverse_count
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			from_poi_id = excluded.from_poi_id,
			to_poi_id = excluded.to_poi_id,
			waypoints_json = excluded.waypoints_json,
			total_distance = excluded.total_distance,
			is_paved = excluded.is_paved,
			traverse_count = excluded.traverse_count;
	`
	_, err := a.mapDB.ExecContext(ctx, query,
		corridor.ID, corridor.FromPOIID, corridor.ToPOIID,
		corridor.WaypointsJSON, corridor.TotalDistance, isPaved, corridor.TraverseCount,
	)
	if err != nil {
		return fmt.Errorf("upserting trail corridor: %w", err)
	}
	return nil
}

// ListTrailCorridors retrieves trail corridors filtered optionally by fromPOIID and/or toPOIID.
func (a *AgentDB) ListTrailCorridors(ctx context.Context, fromPOIID, toPOIID string) ([]TrailCorridor, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	var clauses []string
	var args []any
	if fromPOIID != "" {
		clauses = append(clauses, "from_poi_id = ?")
		args = append(args, fromPOIID)
	}
	if toPOIID != "" {
		clauses = append(clauses, "to_poi_id = ?")
		args = append(args, toPOIID)
	}

	query := `
		SELECT id, from_poi_id, to_poi_id, waypoints_json, total_distance, is_paved, traverse_count
		FROM trail_corridors`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY traverse_count DESC, total_distance ASC, id ASC;"

	rows, err := a.mapDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing trail corridors: %w", err)
	}
	defer rows.Close()

	var corridors []TrailCorridor
	for rows.Next() {
		var c TrailCorridor
		var isPaved int
		if err := rows.Scan(
			&c.ID, &c.FromPOIID, &c.ToPOIID, &c.WaypointsJSON,
			&c.TotalDistance, &isPaved, &c.TraverseCount,
		); err != nil {
			return nil, fmt.Errorf("scanning trail corridor: %w", err)
		}
		c.IsPaved = isPaved != 0
		corridors = append(corridors, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating trail corridor rows: %w", err)
	}
	if corridors == nil {
		return []TrailCorridor{}, nil
	}
	return corridors, nil
}

// ==================== HORIZON SECTORS (ADR-032) ====================

// InsertHorizonSurveyBatch inserts all azimuth sectors from a completed 360-degree
// peripheral horizon survey into map_graph.db in a single write transaction.
func (a *AgentDB) InsertHorizonSurveyBatch(
	ctx context.Context,
	originX, originY, originZ float64,
	surveyedTick int64,
	sectors []HorizonSector,
) error {
	if len(sectors) == 0 {
		return nil
	}

	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	tx, err := a.mapDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning horizon_sectors insert tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const colsPerRow = 15
	var sb strings.Builder
	sb.WriteString(`INSERT INTO horizon_sectors (
		origin_x, origin_y, origin_z, sector_index, bearing_deg,
		hit_distance, feature_type, hit_block_id, hit_x, hit_y, hit_z,
		elevation_delta, occluded, open_horizon, surveyed_tick
	) VALUES `)
	args := make([]any, 0, len(sectors)*colsPerRow)
	for i, sec := range sectors {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		occluded := 0
		if sec.Occluded {
			occluded = 1
		}
		openHorizon := 0
		if sec.OpenHorizon {
			openHorizon = 1
		}
		tick := sec.SurveyedTick
		if tick == 0 {
			tick = surveyedTick
		}
		args = append(args,
			originX, originY, originZ, sec.SectorIndex, sec.BearingDeg,
			sec.HitDistance, sec.FeatureType, sec.HitBlockID, sec.HitX, sec.HitY, sec.HitZ,
			sec.ElevationDelta, occluded, openHorizon, tick,
		)
	}
	sb.WriteByte(';')

	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("inserting horizon sectors batch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing horizon_sectors insert tx: %w", err)
	}
	return nil
}

// GetRecentHorizonSectors retrieves up to limit most recently surveyed horizon sectors
// from map_graph.db, ordered newest survey first and sector_index ascending within a sweep.
func (a *AgentDB) GetRecentHorizonSectors(ctx context.Context, limit int) ([]HorizonSector, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	if limit <= 0 {
		limit = 36
	}

	rows, err := a.mapDB.QueryContext(ctx, `
		SELECT id, origin_x, origin_y, origin_z, sector_index, bearing_deg,
		       hit_distance, feature_type, hit_block_id, hit_x, hit_y, hit_z,
		       elevation_delta, occluded, open_horizon, surveyed_tick
		FROM horizon_sectors
		ORDER BY surveyed_tick DESC, sector_index ASC, id DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent horizon sectors: %w", err)
	}
	defer rows.Close()

	var sectors []HorizonSector
	for rows.Next() {
		var s HorizonSector
		var occluded, openHorizon int
		if err := rows.Scan(
			&s.ID, &s.OriginX, &s.OriginY, &s.OriginZ, &s.SectorIndex, &s.BearingDeg,
			&s.HitDistance, &s.FeatureType, &s.HitBlockID, &s.HitX, &s.HitY, &s.HitZ,
			&s.ElevationDelta, &occluded, &openHorizon, &s.SurveyedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning horizon sector row: %w", err)
		}
		s.Occluded = occluded != 0
		s.OpenHorizon = openHorizon != 0
		sectors = append(sectors, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating horizon sector rows: %w", err)
	}
	if sectors == nil {
		return []HorizonSector{}, nil
	}
	return sectors, nil
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

// TradeValuation represents a persisted System 1 quid-pro-quo valuation entry for an item.
type TradeValuation struct {
	ItemID         string
	BaseValue      float64
	NeedMultiplier float64
	MinAcceptRatio float64
	Rationale      string
	UpdatedTick    int64
}

// UpsertTradeValuation inserts or updates a single item valuation entry in memory.db.
func (a *AgentDB) UpsertTradeValuation(ctx context.Context, v TradeValuation) error {
	return a.UpsertTradeValuationsBatch(ctx, []TradeValuation{v})
}

// UpsertTradeValuationsBatch inserts or updates multiple item valuation entries in a single write transaction.
func (a *AgentDB) UpsertTradeValuationsBatch(ctx context.Context, valuations []TradeValuation) error {
	if len(valuations) == 0 {
		return nil
	}
	a.memWriteMu.Lock()
	defer a.memWriteMu.Unlock()

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}

	tx, err := a.memoryDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning trade_valuations batch tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO trade_valuations (item_id, base_value, need_multiplier, min_accept_ratio, rationale, updated_tick)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET
			base_value = excluded.base_value,
			need_multiplier = excluded.need_multiplier,
			min_accept_ratio = excluded.min_accept_ratio,
			rationale = excluded.rationale,
			updated_tick = excluded.updated_tick;
	`)
	if err != nil {
		return fmt.Errorf("preparing trade_valuations upsert statement: %w", err)
	}
	defer stmt.Close()

	for _, v := range valuations {
		itemID := strings.ToLower(strings.TrimSpace(v.ItemID))
		if itemID == "" {
			continue
		}
		if !strings.Contains(itemID, ":") {
			itemID = "minecraft:" + itemID
		}
		baseVal := v.BaseValue
		if baseVal < 0 {
			baseVal = 0
		}
		needMult := v.NeedMultiplier
		if needMult <= 0 {
			needMult = 1.0
		}
		minRatio := v.MinAcceptRatio
		if minRatio <= 0 {
			minRatio = 0.85
		}
		if _, err := stmt.ExecContext(ctx, itemID, baseVal, needMult, minRatio, v.Rationale, v.UpdatedTick); err != nil {
			return fmt.Errorf("upserting trade valuation %s: %w", itemID, err)
		}
	}

	return tx.Commit()
}

// GetTradeValuations retrieves all persisted item valuations from memory.db keyed by normalized item_id.
func (a *AgentDB) GetTradeValuations(ctx context.Context) (map[string]TradeValuation, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}

	rows, err := a.memoryDB.QueryContext(ctx, `
		SELECT item_id, base_value, need_multiplier, min_accept_ratio, rationale, updated_tick
		FROM trade_valuations;
	`)
	if err != nil {
		return nil, fmt.Errorf("querying trade_valuations: %w", err)
	}
	defer rows.Close()

	result := make(map[string]TradeValuation)
	for rows.Next() {
		var v TradeValuation
		if err := rows.Scan(&v.ItemID, &v.BaseValue, &v.NeedMultiplier, &v.MinAcceptRatio, &v.Rationale, &v.UpdatedTick); err != nil {
			return nil, fmt.Errorf("scanning trade_valuation row: %w", err)
		}
		result[strings.ToLower(strings.TrimSpace(v.ItemID))] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating trade_valuation rows: %w", err)
	}
	return result, nil
}

// GetRecentMemoriesByCategory retrieves episodic memories of a single category whose
// world_tick is >= minTick, ordered newest first (up to limit).
func (a *AgentDB) GetRecentMemoriesByCategory(ctx context.Context, category string, minTick int64, limit int) ([]EpisodicMemory, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := a.memoryDB.QueryContext(ctx, `
		SELECT id, world_tick, event_category, saliency_score, actors_json, location_coords, summary_text, chronicled
		FROM episodic_memories
		WHERE event_category = ? AND world_tick >= ?
		ORDER BY world_tick DESC, id DESC
		LIMIT ?;
	`, category, minTick, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent memories by category: %w", err)
	}
	defer rows.Close()

	var memories []EpisodicMemory
	for rows.Next() {
		var m EpisodicMemory
		if err := rows.Scan(
			&m.ID, &m.WorldTick, &m.EventCategory, &m.SaliencyScore,
			&m.ActorsJSON, &m.LocationCoords, &m.SummaryText, &m.Chronicled,
		); err != nil {
			return nil, fmt.Errorf("scanning recent category memory row: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent category memory rows: %w", err)
	}
	return memories, nil
}
