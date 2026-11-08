package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// Prestige title constants per system specifications.
const (
	PrestigeTitleHero     = "HERO"
	PrestigeTitleAlly     = "ALLY"
	PrestigeTitleFriend   = "FRIEND"
	PrestigeTitleStranger = "STRANGER"
	PrestigeTitleSuspect  = "SUSPECT"
	PrestigeTitleVillain  = "VILLAIN"
)

// ComputePrestigeTitle determines a human player's honorary title based on their cumulative prestige score:
// - score >= 50.0: "HERO"
// - score >= 20.0: "ALLY"
// - score >= 5.0:  "FRIEND"
// - score > -5.0:  "STRANGER"
// - score > -20.0: "SUSPECT"
// - score <= -20.0: "VILLAIN"
func ComputePrestigeTitle(score float64) string {
	switch {
	case score >= 50.0:
		return PrestigeTitleHero
	case score >= 20.0:
		return PrestigeTitleAlly
	case score >= 5.0:
		return PrestigeTitleFriend
	case score > -5.0:
		return PrestigeTitleStranger
	case score > -20.0:
		return PrestigeTitleSuspect
	default:
		return PrestigeTitleVillain
	}
}

// ReputationDB manages the SQLite database for clan-level player reputation
// in $XDG_DATA_HOME/civquest/clans/{clan_id}/reputation.db.
// Read operations proceed concurrently with WAL mode, while write transactions
// are serialized in Go user space via writeMu to prevent SQLITE_BUSY contention.
type ReputationDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
}

// OpenReputationDB opens and migrates the reputation.db at the given file path.
func OpenReputationDB(dbPath, clanID string) (*ReputationDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan reputation directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open reputation database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	r := &ReputationDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := r.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating reputation.db: %w", err)
	}

	return r, nil
}

// OpenReputationDBForClan is a convenience constructor that resolves the path
// via the provided XDG resolver (or default resolver if nil).
func OpenReputationDBForClan(resolver *xdg.Resolver, clanID string) (*ReputationDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanReputationDBPath(clanID)
	return OpenReputationDB(dbPath, clanID)
}

func (r *ReputationDB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS hero_prestige (
		player_uuid TEXT PRIMARY KEY,
		player_name TEXT NOT NULL,
		score REAL NOT NULL DEFAULT 0.0,
		title TEXT NOT NULL DEFAULT 'STRANGER',
		last_interaction_tick INTEGER NOT NULL DEFAULT 0,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS prestige_ledger (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		player_uuid TEXT NOT NULL,
		player_name TEXT NOT NULL DEFAULT '',
		agent_id TEXT NOT NULL,
		delta REAL NOT NULL,
		reason TEXT NOT NULL,
		world_tick INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_prestige_ledger_player ON prestige_ledger(player_uuid);
	CREATE INDEX IF NOT EXISTS idx_prestige_ledger_tick ON prestige_ledger(world_tick);
	`
	_, err := r.db.ExecContext(ctx, schema)
	return err
}

// ClanID returns the clan identifier associated with this reputation database.
func (r *ReputationDB) ClanID() string {
	return r.clanID
}

// Path returns the filesystem path of the SQLite database file.
func (r *ReputationDB) Path() string {
	return r.dbPath
}

// DB returns the underlying *sql.DB handle.
func (r *ReputationDB) DB() *sql.DB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.db
}

// Close gracefully closes the database connection.
func (r *ReputationDB) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isClosed {
		return nil
	}
	r.isClosed = true
	return r.db.Close()
}

// GetHeroPrestige retrieves the current prestige standing and honorary title for a player.
// Returns nil, nil if the player has no recorded interactions with the clan yet.
func (r *ReputationDB) GetHeroPrestige(ctx context.Context, playerUUID string) (*HeroPrestige, error) {
	if playerUUID == "" {
		return nil, errors.New("player_uuid cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT player_uuid, player_name, score, title, last_interaction_tick, updated_at
		FROM hero_prestige
		WHERE player_uuid = ?;
	`
	var hp HeroPrestige
	var updatedAtStr string
	err := r.db.QueryRowContext(ctx, query, playerUUID).Scan(
		&hp.PlayerUUID, &hp.PlayerName, &hp.Score, &hp.Title, &hp.LastInteractionTick, &updatedAtStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying hero prestige: %w", err)
	}

	hp.UpdatedAt = parseDBTimestamp(updatedAtStr)
	return &hp, nil
}

// GetHeroPrestigeOrDefault retrieves the prestige standing, returning a default
// 0.0 / "STRANGER" record if no row exists in the database.
func (r *ReputationDB) GetHeroPrestigeOrDefault(ctx context.Context, playerUUID string) (*HeroPrestige, error) {
	hp, err := r.GetHeroPrestige(ctx, playerUUID)
	if err != nil {
		return nil, err
	}
	if hp == nil {
		return &HeroPrestige{
			PlayerUUID: playerUUID,
			PlayerName: playerUUID,
			Score:      0.0,
			Title:      PrestigeTitleStranger,
		}, nil
	}
	return hp, nil
}

// RecordPrestigeDelta atomically modifies a player's hero prestige score and records
// an audit entry into prestige_ledger within a single transaction.
// Returns the newly updated cumulative prestige score.
func (r *ReputationDB) RecordPrestigeDelta(ctx context.Context, entry PrestigeLedgerEntry) (float64, error) {
	if entry.PlayerUUID == "" {
		return 0, errors.New("player_uuid cannot be empty")
	}

	r.mu.RLock()
	if r.isClosed {
		r.mu.RUnlock()
		return 0, ErrClosed
	}
	r.mu.RUnlock()

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning prestige transaction: %w", err)
	}
	defer tx.Rollback()

	// Query existing score and name
	var existingScore float64
	var existingName string
	query := `SELECT score, player_name FROM hero_prestige WHERE player_uuid = ?;`
	err = tx.QueryRowContext(ctx, query, entry.PlayerUUID).Scan(&existingScore, &existingName)

	var newScore float64
	var newTitle string
	playerName := entry.PlayerName

	if errors.Is(err, sql.ErrNoRows) {
		newScore = entry.Delta
		newTitle = ComputePrestigeTitle(newScore)
		if playerName == "" {
			playerName = entry.PlayerUUID
		}
		insertQuery := `
			INSERT INTO hero_prestige (player_uuid, player_name, score, title, last_interaction_tick, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
		`
		if _, err := tx.ExecContext(ctx, insertQuery, entry.PlayerUUID, playerName, newScore, newTitle, entry.WorldTick); err != nil {
			return 0, fmt.Errorf("inserting new hero prestige: %w", err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("querying existing hero prestige: %w", err)
	} else {
		newScore = existingScore + entry.Delta
		newTitle = ComputePrestigeTitle(newScore)
		if playerName == "" {
			playerName = existingName
		}
		updateQuery := `
			UPDATE hero_prestige
			SET score = ?, title = ?, player_name = ?, last_interaction_tick = MAX(last_interaction_tick, ?), updated_at = CURRENT_TIMESTAMP
			WHERE player_uuid = ?;
		`
		if _, err := tx.ExecContext(ctx, updateQuery, newScore, newTitle, playerName, entry.WorldTick, entry.PlayerUUID); err != nil {
			return 0, fmt.Errorf("updating hero prestige: %w", err)
		}
	}

	// Insert into prestige_ledger
	ledgerQuery := `
		INSERT INTO prestige_ledger (player_uuid, player_name, agent_id, delta, reason, world_tick, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`
	if _, err := tx.ExecContext(ctx, ledgerQuery, entry.PlayerUUID, playerName, entry.AgentID, entry.Delta, entry.Reason, entry.WorldTick); err != nil {
		return 0, fmt.Errorf("inserting prestige ledger entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing prestige transaction: %w", err)
	}

	return newScore, nil
}

// GetPrestigeLedger returns audit trail entries for a given playerUUID (or all if empty), up to limit.
func (r *ReputationDB) GetPrestigeLedger(ctx context.Context, playerUUID string, limit int) ([]PrestigeLedgerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isClosed {
		return nil, ErrClosed
	}

	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if playerUUID != "" {
		query := `
			SELECT id, player_uuid, player_name, agent_id, delta, reason, world_tick, created_at
			FROM prestige_ledger
			WHERE player_uuid = ?
			ORDER BY id DESC
			LIMIT ?;
		`
		rows, err = r.db.QueryContext(ctx, query, playerUUID, limit)
	} else {
		query := `
			SELECT id, player_uuid, player_name, agent_id, delta, reason, world_tick, created_at
			FROM prestige_ledger
			ORDER BY id DESC
			LIMIT ?;
		`
		rows, err = r.db.QueryContext(ctx, query, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("querying prestige ledger: %w", err)
	}
	defer rows.Close()

	var entries []PrestigeLedgerEntry
	for rows.Next() {
		var e PrestigeLedgerEntry
		var createdStr string
		if err := rows.Scan(&e.ID, &e.PlayerUUID, &e.PlayerName, &e.AgentID, &e.Delta, &e.Reason, &e.WorldTick, &createdStr); err != nil {
			return nil, fmt.Errorf("scanning prestige ledger row: %w", err)
		}
		e.CreatedAt = parseDBTimestamp(createdStr)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

func parseDBTimestamp(val string) time.Time {
	if val == "" {
		return time.Time{}
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, val); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}
