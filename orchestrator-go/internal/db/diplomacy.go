package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// Diplomatic status constants.
const (
	DiplomacyStatusAllied     = "ALLIED"
	DiplomacyStatusFriendly   = "FRIENDLY"
	DiplomacyStatusNeutral    = "NEUTRAL"
	DiplomacyStatusSuspicious = "SUSPICIOUS"
	DiplomacyStatusHostile    = "HOSTILE"
	DiplomacyStatusWar        = "WAR"
)

// Treaty type constants.
const (
	TreatyNone          = "NONE"
	TreatyNonAggression = "NON_AGGRESSION"
	TreatyFreeTrade     = "FREE_TRADE"
	TreatyDefensivePact = "DEFENSIVE_PACT"
	TreatyMutualDefense = "MUTUAL_DEFENSE"
)

// IsValidDiplomaticStatus returns true if status is a recognized diplomatic status.
func IsValidDiplomaticStatus(s string) bool {
	switch s {
	case DiplomacyStatusAllied, DiplomacyStatusFriendly, DiplomacyStatusNeutral,
		DiplomacyStatusSuspicious, DiplomacyStatusHostile, DiplomacyStatusWar:
		return true
	default:
		return false
	}
}

// IsValidTreaty returns true if treaty is a recognized treaty type.
func IsValidTreaty(t string) bool {
	switch t {
	case TreatyNone, TreatyNonAggression, TreatyFreeTrade, TreatyDefensivePact, TreatyMutualDefense:
		return true
	default:
		return false
	}
}

// ComputeAffinityStatus returns diplomatic status based on affinity score [-100.0, 100.0].
func ComputeAffinityStatus(score float64) string {
	switch {
	case score >= 60.0:
		return DiplomacyStatusAllied
	case score >= 20.0:
		return DiplomacyStatusFriendly
	case score >= -20.0:
		return DiplomacyStatusNeutral
	case score >= -60.0:
		return DiplomacyStatusSuspicious
	case score > -90.0:
		return DiplomacyStatusHostile
	default:
		return DiplomacyStatusWar
	}
}

// DiplomacyDB manages the SQLite database for inter-clan diplomatic affinities,
// treaties, and trade volumes in $XDG_DATA_HOME/civquest/clans/{clan_id}/diplomacy.db.
type DiplomacyDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
}

// OpenDiplomacyDB opens and migrates diplomacy.db at the given file path.
func OpenDiplomacyDB(dbPath, clanID string) (*DiplomacyDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan diplomacy directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open diplomacy database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	d := &DiplomacyDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating diplomacy.db: %w", err)
	}

	return d, nil
}

// OpenDiplomacyDBForClan resolves the path using the provided XDG resolver.
func OpenDiplomacyDBForClan(resolver *xdg.Resolver, clanID string) (*DiplomacyDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanDiplomacyDBPath(clanID)
	return OpenDiplomacyDB(dbPath, clanID)
}

func (d *DiplomacyDB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS clan_affinities (
		target_clan_id TEXT PRIMARY KEY,
		affinity_score REAL NOT NULL DEFAULT 0.0,
		status TEXT NOT NULL DEFAULT 'NEUTRAL',
		trade_volume INTEGER NOT NULL DEFAULT 0,
		active_treaty TEXT NOT NULL DEFAULT 'NONE',
		treaty_expiry_tick INTEGER NOT NULL DEFAULT 0,
		last_updated_tick INTEGER NOT NULL DEFAULT 0,
		CHECK (affinity_score >= -100.0 AND affinity_score <= 100.0),
		CHECK (trade_volume >= 0)
	);

	CREATE INDEX IF NOT EXISTS idx_clan_affinities_status ON clan_affinities(status);
	`
	_, err := d.db.ExecContext(ctx, schema)
	return err
}

// ClanID returns the clan identifier.
func (d *DiplomacyDB) ClanID() string {
	return d.clanID
}

// Path returns the database filesystem path.
func (d *DiplomacyDB) Path() string {
	return d.dbPath
}

// DB returns the underlying *sql.DB handle.
func (d *DiplomacyDB) DB() *sql.DB {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.db
}

// Close closes the database connection.
func (d *DiplomacyDB) Close() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isClosed {
		return nil
	}
	d.isClosed = true
	return d.db.Close()
}

// SetAffinity modifies the affinity score with a target clan by delta and updates status.
func (d *DiplomacyDB) SetAffinity(targetClanID string, delta float64, worldTick int64) error {
	return d.SetAffinityContext(context.Background(), targetClanID, delta, worldTick)
}

// SetAffinityContext modifies the affinity score with targetClanID by delta with context.
func (d *DiplomacyDB) SetAffinityContext(ctx context.Context, targetClanID string, delta float64, worldTick int64) error {
	if targetClanID == "" {
		return errors.New("target_clan_id cannot be empty")
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return errors.New("affinity delta must be a finite number")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	d.mu.RLock()
	if d.isClosed {
		d.mu.RUnlock()
		return ErrClosed
	}
	d.mu.RUnlock()

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning set affinity transaction: %w", err)
	}
	defer tx.Rollback()

	// Query existing score if any
	var currentScore float64
	var currentStatus, currentTreaty string
	err = tx.QueryRowContext(ctx, `SELECT affinity_score, status, active_treaty FROM clan_affinities WHERE target_clan_id = ?;`, targetClanID).Scan(&currentScore, &currentStatus, &currentTreaty)
	if errors.Is(err, sql.ErrNoRows) {
		currentScore = 0.0
		currentStatus = DiplomacyStatusNeutral
		currentTreaty = TreatyNone
	} else if err != nil {
		return fmt.Errorf("querying current affinity: %w", err)
	}

	newScore := currentScore + delta
	if newScore > 100.0 {
		newScore = 100.0
	} else if newScore < -100.0 {
		newScore = -100.0
	}

	newStatus := ComputeAffinityStatus(newScore)

	// If current status is WAR, preserve WAR unless the new affinity is non-negative (>= 0.0) or an explicit treaty is formed.
	if currentStatus == DiplomacyStatusWar {
		hasActiveTreaty := currentTreaty != "" && currentTreaty != TreatyNone
		if newScore < 0.0 && !hasActiveTreaty {
			newStatus = DiplomacyStatusWar
		}
	}

	upsertQuery := `
		INSERT INTO clan_affinities (target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick)
		VALUES (?, ?, ?, 0, 'NONE', 0, ?)
		ON CONFLICT(target_clan_id) DO UPDATE SET
			affinity_score = excluded.affinity_score,
			status = excluded.status,
			last_updated_tick = excluded.last_updated_tick;
	`
	if _, err := tx.ExecContext(ctx, upsertQuery, targetClanID, newScore, newStatus, worldTick); err != nil {
		return fmt.Errorf("upserting affinity: %w", err)
	}

	return tx.Commit()
}

// SetDiplomaticStatus explicitly sets diplomatic status with targetClanID.
func (d *DiplomacyDB) SetDiplomaticStatus(targetClanID string, status string, worldTick int64) error {
	return d.SetDiplomaticStatusContext(context.Background(), targetClanID, status, worldTick)
}

// SetDiplomaticStatusContext explicitly sets diplomatic status with targetClanID with context.
func (d *DiplomacyDB) SetDiplomaticStatusContext(ctx context.Context, targetClanID string, status string, worldTick int64) error {
	if targetClanID == "" {
		return errors.New("target_clan_id cannot be empty")
	}
	if !IsValidDiplomaticStatus(status) {
		return fmt.Errorf("invalid diplomatic status: %q", status)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	d.mu.RLock()
	if d.isClosed {
		d.mu.RUnlock()
		return ErrClosed
	}
	d.mu.RUnlock()

	query := `
		INSERT INTO clan_affinities (target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick)
		VALUES (?, 0.0, ?, 0, 'NONE', 0, ?)
		ON CONFLICT(target_clan_id) DO UPDATE SET
			status = excluded.status,
			last_updated_tick = excluded.last_updated_tick;
	`
	_, err := d.db.ExecContext(ctx, query, targetClanID, status, worldTick)
	if err != nil {
		return fmt.Errorf("setting diplomatic status: %w", err)
	}
	return nil
}

// SetTreaty establishes or clears an active treaty with targetClanID for durationTicks.
func (d *DiplomacyDB) SetTreaty(targetClanID string, treaty string, durationTicks int64, worldTick int64) error {
	return d.SetTreatyContext(context.Background(), targetClanID, treaty, durationTicks, worldTick)
}

// SetTreatyContext establishes or clears an active treaty with targetClanID with context.
func (d *DiplomacyDB) SetTreatyContext(ctx context.Context, targetClanID string, treaty string, durationTicks int64, worldTick int64) error {
	if targetClanID == "" {
		return errors.New("target_clan_id cannot be empty")
	}
	if durationTicks < 0 {
		return errors.New("treaty duration must be non-negative")
	}
	if treaty == "" {
		treaty = TreatyNone
	}
	if !IsValidTreaty(treaty) {
		return fmt.Errorf("invalid treaty type: %q", treaty)
	}

	var expiryTick int64
	if treaty == TreatyNone || durationTicks == 0 {
		treaty = TreatyNone
		expiryTick = 0
	} else {
		expiryTick = worldTick + durationTicks
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	d.mu.RLock()
	if d.isClosed {
		d.mu.RUnlock()
		return ErrClosed
	}
	d.mu.RUnlock()

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning set treaty transaction: %w", err)
	}
	defer tx.Rollback()

	var currentScore float64
	var currentStatus string
	err = tx.QueryRowContext(ctx, `SELECT affinity_score, status FROM clan_affinities WHERE target_clan_id = ?;`, targetClanID).Scan(&currentScore, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		currentScore = 0.0
		currentStatus = DiplomacyStatusNeutral
	} else if err != nil {
		return fmt.Errorf("querying clan affinity before treaty: %w", err)
	}

	newStatus := currentStatus
	if treaty != TreatyNone && currentStatus == DiplomacyStatusWar {
		newStatus = ComputeAffinityStatus(currentScore)
		if newStatus == DiplomacyStatusWar {
			newStatus = DiplomacyStatusHostile
		}
	}

	query := `
		INSERT INTO clan_affinities (target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick)
		VALUES (?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(target_clan_id) DO UPDATE SET
			status = ?,
			active_treaty = excluded.active_treaty,
			treaty_expiry_tick = excluded.treaty_expiry_tick,
			last_updated_tick = excluded.last_updated_tick;
	`
	_, err = tx.ExecContext(ctx, query, targetClanID, currentScore, newStatus, treaty, expiryTick, worldTick, newStatus)
	if err != nil {
		return fmt.Errorf("setting treaty: %w", err)
	}

	return tx.Commit()
}

// RecordTradeVolume increments the recorded trade volume with targetClanID.
func (d *DiplomacyDB) RecordTradeVolume(targetClanID string, volume int, worldTick int64) error {
	return d.RecordTradeVolumeContext(context.Background(), targetClanID, volume, worldTick)
}

// RecordTradeVolumeContext increments the recorded trade volume with targetClanID with context.
func (d *DiplomacyDB) RecordTradeVolumeContext(ctx context.Context, targetClanID string, volume int, worldTick int64) error {
	if targetClanID == "" {
		return errors.New("target_clan_id cannot be empty")
	}
	if volume <= 0 {
		return errors.New("trade volume must be positive")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	d.mu.RLock()
	if d.isClosed {
		d.mu.RUnlock()
		return ErrClosed
	}
	d.mu.RUnlock()

	query := `
		INSERT INTO clan_affinities (target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick)
		VALUES (?, 0.0, 'NEUTRAL', ?, 'NONE', 0, ?)
		ON CONFLICT(target_clan_id) DO UPDATE SET
			trade_volume = clan_affinities.trade_volume + excluded.trade_volume,
			last_updated_tick = excluded.last_updated_tick;
	`
	_, err := d.db.ExecContext(ctx, query, targetClanID, volume, worldTick)
	if err != nil {
		return fmt.Errorf("recording trade volume: %w", err)
	}
	return nil
}

// GetAffinity retrieves the affinity record for a specific target clan. Returns nil, nil if not found.
func (d *DiplomacyDB) GetAffinity(targetClanID string) (*ClanAffinity, error) {
	return d.GetAffinityContext(context.Background(), targetClanID)
}

// GetAffinityContext retrieves the affinity record for a specific target clan with context.
func (d *DiplomacyDB) GetAffinityContext(ctx context.Context, targetClanID string) (*ClanAffinity, error) {
	if targetClanID == "" {
		return nil, errors.New("target_clan_id cannot be empty")
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick
		FROM clan_affinities
		WHERE target_clan_id = ?;
	`
	var a ClanAffinity
	err := d.db.QueryRowContext(ctx, query, targetClanID).Scan(
		&a.TargetClanID, &a.AffinityScore, &a.Status, &a.TradeVolume, &a.ActiveTreaty, &a.TreatyExpiryTick, &a.LastUpdatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying clan affinity: %w", err)
	}
	return &a, nil
}

// ListAffinities returns all clan affinity records.
func (d *DiplomacyDB) ListAffinities() ([]*ClanAffinity, error) {
	return d.ListAffinitiesContext(context.Background())
}

// ListAffinitiesContext returns all clan affinity records with context.
func (d *DiplomacyDB) ListAffinitiesContext(ctx context.Context) ([]*ClanAffinity, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT target_clan_id, affinity_score, status, trade_volume, active_treaty, treaty_expiry_tick, last_updated_tick
		FROM clan_affinities
		ORDER BY target_clan_id ASC;
	`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing clan affinities: %w", err)
	}
	defer rows.Close()

	var affinities []*ClanAffinity
	for rows.Next() {
		var a ClanAffinity
		if err := rows.Scan(
			&a.TargetClanID, &a.AffinityScore, &a.Status, &a.TradeVolume, &a.ActiveTreaty, &a.TreatyExpiryTick, &a.LastUpdatedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning affinity row: %w", err)
		}
		affinities = append(affinities, &a)
	}
	return affinities, rows.Err()
}
