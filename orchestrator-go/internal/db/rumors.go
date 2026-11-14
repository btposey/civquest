package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// Rumor categories.
const (
	RumorCategoryHazard    = "HAZARD"
	RumorCategoryDiscovery = "DISCOVERY"
	RumorCategoryResource  = "RESOURCE"
	RumorCategoryTrade     = "TRADE"
	RumorCategoryLore      = "LORE"
	RumorCategoryGossip    = "GOSSIP"
)

// IsValidRumorCategory returns true if cat is one of the recognized rumor categories.
func IsValidRumorCategory(cat string) bool {
	switch strings.ToUpper(strings.TrimSpace(cat)) {
	case RumorCategoryHazard, RumorCategoryDiscovery, RumorCategoryResource,
		RumorCategoryTrade, RumorCategoryLore, RumorCategoryGossip:
		return true
	default:
		return false
	}
}

// RumorsDB manages the SQLite database for tavern intelligence, environmental hazard reports,
// resource deposits, and inter-settler rumor diffusion in
// $XDG_DATA_HOME/civquest/clans/{clan_id}/rumors.db.
type RumorsDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
}

// OpenRumorsDB opens and migrates rumors.db at dbPath.
func OpenRumorsDB(dbPath, clanID string) (*RumorsDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan rumors directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open rumors database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	r := &RumorsDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := r.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating rumors.db: %w", err)
	}

	return r, nil
}

// OpenRumorsDBForClan resolves the path using the provided XDG resolver.
func OpenRumorsDBForClan(resolver *xdg.Resolver, clanID string) (*RumorsDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanRumorsDBPath(clanID)
	return OpenRumorsDB(dbPath, clanID)
}

func (r *RumorsDB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS rumors (
		rumor_id TEXT PRIMARY KEY,
		category TEXT NOT NULL CHECK(category IN ('HAZARD', 'DISCOVERY', 'RESOURCE', 'TRADE', 'LORE', 'GOSSIP')),
		content TEXT NOT NULL,
		source_agent_id TEXT NOT NULL,
		source_agent_name TEXT NOT NULL DEFAULT '',
		location_x REAL NOT NULL DEFAULT 0.0,
		location_y REAL NOT NULL DEFAULT 0.0,
		location_z REAL NOT NULL DEFAULT 0.0,
		location_name TEXT NOT NULL DEFAULT '',
		credibility REAL NOT NULL DEFAULT 1.0 CHECK(credibility >= 0.0 AND credibility <= 1.0),
		spread_count INTEGER NOT NULL DEFAULT 0 CHECK(spread_count >= 0),
		origin_tick INTEGER NOT NULL DEFAULT 0,
		created_tick INTEGER NOT NULL DEFAULT 0,
		expires_tick INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_rumors_cat ON rumors(category);
	CREATE INDEX IF NOT EXISTS idx_rumors_created_tick ON rumors(created_tick);
	CREATE INDEX IF NOT EXISTS idx_rumors_expires_tick ON rumors(expires_tick);

	CREATE TABLE IF NOT EXISTS rumor_diffusion (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		rumor_id TEXT NOT NULL,
		speaker_agent_id TEXT NOT NULL,
		listener_agent_id TEXT NOT NULL,
		world_tick INTEGER NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (rumor_id) REFERENCES rumors(rumor_id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_rumor_diffusion_rumor ON rumor_diffusion(rumor_id);
	CREATE INDEX IF NOT EXISTS idx_rumor_diffusion_listener ON rumor_diffusion(listener_agent_id);
	`
	_, err := r.db.ExecContext(ctx, schema)
	return err
}

// AddRumor records or updates a rumor in rumors.db.
func (r *RumorsDB) AddRumor(ctx context.Context, rumor *Rumor) error {
	if rumor == nil {
		return errors.New("rumor cannot be nil")
	}
	if strings.TrimSpace(rumor.RumorID) == "" {
		return errors.New("rumor_id cannot be empty")
	}
	if strings.TrimSpace(rumor.Content) == "" {
		return errors.New("content cannot be empty")
	}
	if strings.TrimSpace(rumor.SourceAgentID) == "" {
		return errors.New("source_agent_id cannot be empty")
	}
	cat := strings.ToUpper(strings.TrimSpace(rumor.Category))
	if !IsValidRumorCategory(cat) {
		return fmt.Errorf("invalid rumor category: %q", rumor.Category)
	}
	rumor.Category = cat

	if rumor.Credibility < 0.0 {
		rumor.Credibility = 0.0
	} else if rumor.Credibility > 1.0 {
		rumor.Credibility = 1.0
	}

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return ErrClosed
	}

	query := `
		INSERT INTO rumors (
			rumor_id, category, content, source_agent_id, source_agent_name,
			location_x, location_y, location_z, location_name,
			credibility, spread_count, origin_tick, created_tick, expires_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rumor_id) DO UPDATE SET
			category = excluded.category,
			content = excluded.content,
			source_agent_id = excluded.source_agent_id,
			source_agent_name = excluded.source_agent_name,
			location_x = excluded.location_x,
			location_y = excluded.location_y,
			location_z = excluded.location_z,
			location_name = excluded.location_name,
			credibility = excluded.credibility,
			spread_count = excluded.spread_count,
			origin_tick = excluded.origin_tick,
			created_tick = excluded.created_tick,
			expires_tick = excluded.expires_tick;
	`
	_, err := r.db.ExecContext(ctx, query,
		rumor.RumorID, rumor.Category, rumor.Content, rumor.SourceAgentID, rumor.SourceAgentName,
		rumor.LocationX, rumor.LocationY, rumor.LocationZ, rumor.LocationName,
		rumor.Credibility, rumor.SpreadCount, rumor.OriginTick, rumor.CreatedTick, rumor.ExpiresTick,
	)
	if err != nil {
		return fmt.Errorf("adding rumor: %w", err)
	}
	return nil
}

// GetRumor retrieves a rumor by its unique identifier.
func (r *RumorsDB) GetRumor(ctx context.Context, rumorID string) (*Rumor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT rumor_id, category, content, source_agent_id, source_agent_name,
		       location_x, location_y, location_z, location_name,
		       credibility, spread_count, origin_tick, created_tick, expires_tick, created_at
		FROM rumors
		WHERE rumor_id = ?;
	`
	var rum Rumor
	err := r.db.QueryRowContext(ctx, query, rumorID).Scan(
		&rum.RumorID, &rum.Category, &rum.Content, &rum.SourceAgentID, &rum.SourceAgentName,
		&rum.LocationX, &rum.LocationY, &rum.LocationZ, &rum.LocationName,
		&rum.Credibility, &rum.SpreadCount, &rum.OriginTick, &rum.CreatedTick, &rum.ExpiresTick, &rum.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying rumor: %w", err)
	}
	return &rum, nil
}

// ListRumors returns all recorded rumors up to limit (or all if limit <= 0), ordered newest first.
func (r *RumorsDB) ListRumors(ctx context.Context, limit int) ([]*Rumor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT rumor_id, category, content, source_agent_id, source_agent_name,
		       location_x, location_y, location_z, location_name,
		       credibility, spread_count, origin_tick, created_tick, expires_tick, created_at
		FROM rumors
		ORDER BY created_tick DESC, rumor_id ASC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing rumors: %w", err)
	}
	defer rows.Close()

	return scanRumors(rows)
}

// ListActiveRumors returns all rumors that have not expired relative to currentTick.
func (r *RumorsDB) ListActiveRumors(ctx context.Context, currentTick int64, limit int) ([]*Rumor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT rumor_id, category, content, source_agent_id, source_agent_name,
		       location_x, location_y, location_z, location_name,
		       credibility, spread_count, origin_tick, created_tick, expires_tick, created_at
		FROM rumors
		WHERE expires_tick = 0 OR expires_tick > ?
		ORDER BY created_tick DESC, rumor_id ASC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.db.QueryContext(ctx, query, currentTick)
	if err != nil {
		return nil, fmt.Errorf("listing active rumors: %w", err)
	}
	defer rows.Close()

	return scanRumors(rows)
}

// ListRumorsByCategory returns rumors belonging to the given category.
func (r *RumorsDB) ListRumorsByCategory(ctx context.Context, category string, limit int) ([]*Rumor, error) {
	cat := strings.ToUpper(strings.TrimSpace(category))
	if !IsValidRumorCategory(cat) {
		return nil, fmt.Errorf("invalid rumor category: %q", category)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT rumor_id, category, content, source_agent_id, source_agent_name,
		       location_x, location_y, location_z, location_name,
		       credibility, spread_count, origin_tick, created_tick, expires_tick, created_at
		FROM rumors
		WHERE category = ?
		ORDER BY created_tick DESC, rumor_id ASC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.db.QueryContext(ctx, query, cat)
	if err != nil {
		return nil, fmt.Errorf("listing rumors by category: %w", err)
	}
	defer rows.Close()

	return scanRumors(rows)
}

// RecordDiffusion records a rumor diffusion event from speaker to listener and increments spread_count.
func (r *RumorsDB) RecordDiffusion(ctx context.Context, record RumorDiffusionRecord) error {
	if strings.TrimSpace(record.RumorID) == "" {
		return errors.New("rumor_id cannot be empty")
	}
	if strings.TrimSpace(record.SpeakerAgentID) == "" {
		return errors.New("speaker_agent_id cannot be empty")
	}
	if strings.TrimSpace(record.ListenerAgentID) == "" {
		return errors.New("listener_agent_id cannot be empty")
	}

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return ErrClosed
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting diffusion tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insertQuery := `
		INSERT INTO rumor_diffusion (rumor_id, speaker_agent_id, listener_agent_id, world_tick)
		VALUES (?, ?, ?, ?);
	`
	if _, err := tx.ExecContext(ctx, insertQuery, record.RumorID, record.SpeakerAgentID, record.ListenerAgentID, record.WorldTick); err != nil {
		return fmt.Errorf("inserting rumor diffusion record: %w", err)
	}

	updateQuery := `
		UPDATE rumors
		SET spread_count = spread_count + 1
		WHERE rumor_id = ?;
	`
	if _, err := tx.ExecContext(ctx, updateQuery, record.RumorID); err != nil {
		return fmt.Errorf("updating rumor spread count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing diffusion tx: %w", err)
	}
	return nil
}

// GetDiffusionHistory returns all diffusion events logged for a specific rumor.
func (r *RumorsDB) GetDiffusionHistory(ctx context.Context, rumorID string) ([]RumorDiffusionRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, rumor_id, speaker_agent_id, listener_agent_id, world_tick, created_at
		FROM rumor_diffusion
		WHERE rumor_id = ?
		ORDER BY id ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, rumorID)
	if err != nil {
		return nil, fmt.Errorf("querying diffusion history: %w", err)
	}
	defer rows.Close()

	var records []RumorDiffusionRecord
	for rows.Next() {
		var rec RumorDiffusionRecord
		if err := rows.Scan(&rec.ID, &rec.RumorID, &rec.SpeakerAgentID, &rec.ListenerAgentID, &rec.WorldTick, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning diffusion record: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// GetUndiffusedRumorsForAgent returns rumors that the specified agent has not yet heard (not received as listener)
// and did not originate.
func (r *RumorsDB) GetUndiffusedRumorsForAgent(ctx context.Context, agentID string, limit int) ([]*Rumor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT r.rumor_id, r.category, r.content, r.source_agent_id, r.source_agent_name,
		       r.location_x, r.location_y, r.location_z, r.location_name,
		       r.credibility, r.spread_count, r.origin_tick, r.created_tick, r.expires_tick, r.created_at
		FROM rumors r
		WHERE r.source_agent_id != ?
		  AND r.rumor_id NOT IN (
			SELECT d.rumor_id FROM rumor_diffusion d WHERE d.listener_agent_id = ?
		  )
		ORDER BY r.credibility DESC, r.created_tick DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.db.QueryContext(ctx, query, agentID, agentID)
	if err != nil {
		return nil, fmt.Errorf("querying undiffused rumors: %w", err)
	}
	defer rows.Close()

	return scanRumors(rows)
}

// IncrementSpreadCount manually increments the diffusion spread count of a rumor.
func (r *RumorsDB) IncrementSpreadCount(ctx context.Context, rumorID string) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return ErrClosed
	}

	res, err := r.db.ExecContext(ctx, "UPDATE rumors SET spread_count = spread_count + 1 WHERE rumor_id = ?;", rumorID)
	if err != nil {
		return fmt.Errorf("incrementing spread count: %w", err)
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

// ClanID returns the clan identifier.
func (r *RumorsDB) ClanID() string {
	return r.clanID
}

// Path returns the database file path.
func (r *RumorsDB) Path() string {
	return r.dbPath
}

// DB returns the underlying *sql.DB handle.
func (r *RumorsDB) DB() *sql.DB {
	return r.db
}

// Close closes the database connection.
func (r *RumorsDB) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isClosed {
		return nil
	}
	r.isClosed = true
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func scanRumors(rows *sql.Rows) ([]*Rumor, error) {
	var rumors []*Rumor
	for rows.Next() {
		var rum Rumor
		if err := rows.Scan(
			&rum.RumorID, &rum.Category, &rum.Content, &rum.SourceAgentID, &rum.SourceAgentName,
			&rum.LocationX, &rum.LocationY, &rum.LocationZ, &rum.LocationName,
			&rum.Credibility, &rum.SpreadCount, &rum.OriginTick, &rum.CreatedTick, &rum.ExpiresTick, &rum.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning rumor row: %w", err)
		}
		rumors = append(rumors, &rum)
	}
	return rumors, rows.Err()
}
