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

// Project status constants.
const (
	ProjectStatusPlanned     = "PLANNED"
	ProjectStatusSurveying   = "SURVEYING"
	ProjectStatusAwaitingBOM = "AWAITING_BOM"
	ProjectStatusClearing    = "CLEARING"
	ProjectStatusBuilding    = "BUILDING"
	ProjectStatusInProgress  = "IN_PROGRESS"
	ProjectStatusCompleted   = "COMPLETED"
	ProjectStatusHalted      = "HALTED"
	ProjectStatusAbandoned   = "ABANDONED"
)

// IsValidProjectStatus checks whether status is a valid project status.
func IsValidProjectStatus(status string) bool {
	switch status {
	case ProjectStatusPlanned, ProjectStatusSurveying, ProjectStatusAwaitingBOM,
		ProjectStatusClearing, ProjectStatusBuilding, ProjectStatusInProgress,
		ProjectStatusCompleted, ProjectStatusHalted, ProjectStatusAbandoned:
		return true
	default:
		return false
	}
}

// Spatial quadrant constants per ADR-010.
const (
	QuadrantNW  = "NW"
	QuadrantNE  = "NE"
	QuadrantSW  = "SW"
	QuadrantSE  = "SE"
	QuadrantAll = "ALL"
)

// IsValidQuadrant checks whether quadrant is a recognized spatial quadrant.
func IsValidQuadrant(quadrant string) bool {
	switch quadrant {
	case QuadrantNW, QuadrantNE, QuadrantSW, QuadrantSE, QuadrantAll:
		return true
	default:
		return false
	}
}

// Functional zone types.
const (
	ZoneTypeGranary     = "GRANARY"
	ZoneTypeResidential = "RESIDENTIAL"
	ZoneTypeHousing     = "HOUSING"
	ZoneTypeWorkshop    = "WORKSHOP"
	ZoneTypeFarm        = "FARM"
	ZoneTypeFarmland    = "FARMLAND"
	ZoneTypeTavern      = "TAVERN"
	ZoneTypeDefense     = "DEFENSE"
	ZoneTypePasture     = "PASTURE"
	ZoneTypeLibrary     = "LIBRARY"
	ZoneTypeCemetery    = "CEMETERY"
	ZoneTypeMarket      = "MARKET"
)

// IsValidZoneType checks whether zoneType is a recognized functional zone type.
func IsValidZoneType(zoneType string) bool {
	switch zoneType {
	case ZoneTypeGranary, ZoneTypeResidential, ZoneTypeHousing,
		ZoneTypeWorkshop, ZoneTypeFarm, ZoneTypeFarmland,
		ZoneTypeTavern, ZoneTypeDefense, ZoneTypePasture,
		ZoneTypeLibrary, ZoneTypeCemetery, ZoneTypeMarket:
		return true
	default:
		return false
	}
}

// Sentinel errors for civic registry operations.
var (
	ErrInsufficientStock  = errors.New("insufficient granary stock")
	ErrInvalidQuantity    = errors.New("quantity must be greater than zero")
	ErrInvalidCoordinates = errors.New("invalid coordinates: min must be less than or equal to max")
	ErrDuplicateRecord    = errors.New("record already exists")
)

// CivicRegistryDB manages the SQLite database for clan-level civic infrastructure,
// granary reserves, spatial construction projects, and functional zones
// in $XDG_DATA_HOME/civquest/clans/{clan_id}/civic_registry.db.
type CivicRegistryDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
	worldID  string // scopes world-bound rows (bed claims); "" = unnamed world
}

// OpenCivicRegistryDB opens and migrates the civic_registry.db at the given file path.
func OpenCivicRegistryDB(dbPath, clanID string) (*CivicRegistryDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan civic directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open civic registry database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	c := &CivicRegistryDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating civic_registry.db: %w", err)
	}

	return c, nil
}

// OpenCivicRegistryDBForClan resolves the path using the provided XDG resolver.
func OpenCivicRegistryDBForClan(resolver *xdg.Resolver, clanID string) (*CivicRegistryDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanCivicRegistryDBPath(clanID)
	c, err := OpenCivicRegistryDB(dbPath, clanID)
	if err != nil {
		return nil, err
	}
	c.SetWorldID(resolver.ServerID())
	return c, nil
}

func (c *CivicRegistryDB) migrate(ctx context.Context) error {
	return c.initSchema(ctx)
}

// InitSchema initializes all database tables, indices, and constraints in civic_registry.db.
// It is idempotent and can be safely executed multiple times.
func (c *CivicRegistryDB) InitSchema(ctx context.Context) error {
	return c.initSchema(ctx)
}

func (c *CivicRegistryDB) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS settlement_granary (
		item_id TEXT PRIMARY KEY,
		quantity INTEGER NOT NULL DEFAULT 0,
		reserved_quantity INTEGER NOT NULL DEFAULT 0,
		last_updated_tick INTEGER NOT NULL DEFAULT 0,
		CHECK (quantity >= 0),
		CHECK (reserved_quantity >= 0),
		CHECK (reserved_quantity <= quantity)
	);

	CREATE TABLE IF NOT EXISTS construction_projects (
		project_id TEXT PRIMARY KEY,
		blueprint_id TEXT NOT NULL,
		status TEXT NOT NULL,
		anchor_x INTEGER NOT NULL,
		anchor_y INTEGER NOT NULL,
		anchor_z INTEGER NOT NULL,
		quadrant TEXT NOT NULL,
		progress_percent REAL NOT NULL DEFAULT 0.0,
		materials_reserved_json TEXT NOT NULL DEFAULT '{}',
		created_tick INTEGER NOT NULL DEFAULT 0,
		updated_tick INTEGER NOT NULL DEFAULT 0,
		CHECK (progress_percent >= 0.0 AND progress_percent <= 100.0)
	);

	CREATE INDEX IF NOT EXISTS idx_construction_projects_status ON construction_projects(status);
	CREATE INDEX IF NOT EXISTS idx_construction_projects_quadrant ON construction_projects(quadrant);

	CREATE TABLE IF NOT EXISTS functional_zones (
		zone_id TEXT PRIMARY KEY,
		zone_type TEXT NOT NULL,
		min_x INTEGER NOT NULL,
		min_y INTEGER NOT NULL,
		min_z INTEGER NOT NULL,
		max_x INTEGER NOT NULL,
		max_y INTEGER NOT NULL,
		max_z INTEGER NOT NULL,
		owner_agent_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		created_tick INTEGER NOT NULL DEFAULT 0,
		CHECK (min_x <= max_x AND min_y <= max_y AND min_z <= max_z)
	);

	CREATE INDEX IF NOT EXISTS idx_functional_zones_type ON functional_zones(zone_type);
	CREATE INDEX IF NOT EXISTS idx_functional_zones_owner ON functional_zones(owner_agent_id);

	CREATE TABLE IF NOT EXISTS clan_governance (
		clan_id TEXT PRIMARY KEY,
		regime_type TEXT NOT NULL,
		leader_agent_id TEXT NOT NULL DEFAULT '',
		leader_type TEXT NOT NULL DEFAULT 'AGENT' CHECK (leader_type IN ('AGENT', 'PLAYER')),
		mandate_goal TEXT NOT NULL DEFAULT '',
		legitimacy_score REAL NOT NULL DEFAULT 0.0 CHECK (legitimacy_score >= 0.0 AND legitimacy_score <= 100.0),
		term_start_tick INTEGER NOT NULL DEFAULT 0,
		last_election_tick INTEGER NOT NULL DEFAULT 0,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CHECK (regime_type IN ('DEMOCRACY', 'BENEVOLENT_MONARCHY', 'WARLORD', 'OLIGARCHY'))
	);

	CREATE TABLE IF NOT EXISTS civic_assemblies (
		id TEXT PRIMARY KEY,
		clan_id TEXT NOT NULL,
		trigger_axis TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'CONCLUDED', 'ABANDONED')),
		winning_proposal_id TEXT NOT NULL DEFAULT '',
		opened_tick INTEGER NOT NULL DEFAULT 0,
		closed_tick INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_civic_assemblies_clan ON civic_assemblies(clan_id);
	CREATE INDEX IF NOT EXISTS idx_civic_assemblies_status ON civic_assemblies(status);

	CREATE TABLE IF NOT EXISTS civic_votes (
		assembly_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		voter_type TEXT NOT NULL DEFAULT 'AGENT' CHECK (voter_type IN ('AGENT', 'PLAYER')),
		proposal_id TEXT NOT NULL,
		vote_weight REAL NOT NULL DEFAULT 1.0 CHECK (vote_weight >= 0.0),
		rationale TEXT NOT NULL DEFAULT '',
		world_tick INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (assembly_id, agent_id),
		FOREIGN KEY (assembly_id) REFERENCES civic_assemblies(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_civic_votes_assembly ON civic_votes(assembly_id);
	CREATE INDEX IF NOT EXISTS idx_civic_votes_proposal ON civic_votes(proposal_id);

	CREATE TABLE IF NOT EXISTS civic_edicts (
		id TEXT PRIMARY KEY,
		clan_id TEXT NOT NULL,
		leader_agent_id TEXT NOT NULL,
		regime_type TEXT NOT NULL CHECK (regime_type IN ('DEMOCRACY', 'BENEVOLENT_MONARCHY', 'WARLORD', 'OLIGARCHY')),
		edict_title TEXT NOT NULL,
		blueprint_id TEXT NOT NULL DEFAULT '',
		raw_decree TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'COMPLETED', 'RESCINDED')),
		compliance_count INTEGER NOT NULL DEFAULT 0 CHECK (compliance_count >= 0),
		dissent_count INTEGER NOT NULL DEFAULT 0 CHECK (dissent_count >= 0),
		issued_tick INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_civic_edicts_clan ON civic_edicts(clan_id);
	CREATE INDEX IF NOT EXISTS idx_civic_edicts_status ON civic_edicts(status);

	CREATE TABLE IF NOT EXISTS settlement_settlers (
		agent_id TEXT PRIMARY KEY,
		clan_id TEXT NOT NULL,
		profession TEXT NOT NULL DEFAULT '',
		registered_tick INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_settlement_settlers_clan ON settlement_settlers(clan_id);

	CREATE TABLE IF NOT EXISTS settlement_pois (
		settlement_poi_id TEXT PRIMARY KEY,
		settlement_id TEXT NOT NULL,
		category TEXT NOT NULL,
		block_id TEXT NOT NULL,
		x REAL NOT NULL,
		y REAL NOT NULL,
		z REAL NOT NULL,
		access_x REAL NOT NULL,
		access_y REAL NOT NULL,
		access_z REAL NOT NULL,
		elevation_layer TEXT NOT NULL,
		creator_agent_id TEXT NOT NULL,
		registered_tick INTEGER NOT NULL DEFAULT 0,
		last_verified_tick INTEGER NOT NULL DEFAULT 0,
		is_active INTEGER NOT NULL DEFAULT 1
	);
	CREATE INDEX IF NOT EXISTS idx_settlement_pois_settlement ON settlement_pois(settlement_id, category);

	CREATE TABLE IF NOT EXISTS clan_horizon_sectors (
		explorer_id TEXT NOT NULL,
		origin_x REAL NOT NULL,
		origin_y REAL NOT NULL,
		origin_z REAL NOT NULL,
		sector_index INTEGER NOT NULL,
		bearing_deg REAL NOT NULL,
		hit_distance REAL NOT NULL,
		feature_type TEXT NOT NULL,
		hit_block_id TEXT NOT NULL DEFAULT '',
		hit_x REAL NOT NULL,
		hit_y REAL NOT NULL,
		hit_z REAL NOT NULL,
		elevation_delta REAL NOT NULL,
		occluded INTEGER NOT NULL DEFAULT 0,
		open_horizon INTEGER NOT NULL DEFAULT 0,
		surveyed_tick INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (explorer_id, origin_x, origin_y, origin_z, sector_index)
	);
	CREATE INDEX IF NOT EXISTS idx_clan_horizon_sectors_explorer ON clan_horizon_sectors(explorer_id);
	CREATE INDEX IF NOT EXISTS idx_clan_horizon_sectors_tick ON clan_horizon_sectors(surveyed_tick);

	-- Supersedes the unscoped bed_claims table: legacy rows name no world and are ignored.
	CREATE TABLE IF NOT EXISTS world_bed_claims (
		world_id TEXT NOT NULL,
		x INTEGER NOT NULL,
		y INTEGER NOT NULL,
		z INTEGER NOT NULL,
		owner_agent_id TEXT NOT NULL,
		claimed_tick INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (world_id, x, y, z)
	);
	CREATE INDEX IF NOT EXISTS idx_world_bed_claims_owner ON world_bed_claims(world_id, owner_agent_id);
	`
	_, err := c.db.ExecContext(ctx, schema)
	return err
}

// ClanID returns the clan identifier.
func (c *CivicRegistryDB) ClanID() string {
	return c.clanID
}

// Path returns the database filesystem path.
func (c *CivicRegistryDB) Path() string {
	return c.dbPath
}

// DB returns the underlying *sql.DB handle.
func (c *CivicRegistryDB) DB() *sql.DB {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db
}

// Close closes the database connection.
func (c *CivicRegistryDB) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosed {
		return nil
	}
	c.isClosed = true
	return c.db.Close()
}

// -----------------------------------------------------------------------------
// Granary Stock Operations
// -----------------------------------------------------------------------------

// AddGranaryStock increments the stock of itemID by delta.
func (c *CivicRegistryDB) AddGranaryStock(itemID string, delta int, worldTick int64) error {
	return c.AddGranaryStockContext(context.Background(), itemID, delta, worldTick)
}

// AddGranaryStockContext increments the stock of itemID by delta with context.
func (c *CivicRegistryDB) AddGranaryStockContext(ctx context.Context, itemID string, delta int, worldTick int64) error {
	if itemID == "" {
		return errors.New("item_id cannot be empty")
	}
	if delta <= 0 {
		return ErrInvalidQuantity
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO settlement_granary (item_id, quantity, reserved_quantity, last_updated_tick)
		VALUES (?, ?, 0, ?)
		ON CONFLICT(item_id) DO UPDATE SET
			quantity = settlement_granary.quantity + excluded.quantity,
			last_updated_tick = excluded.last_updated_tick;
	`
	_, err := c.db.ExecContext(ctx, query, itemID, delta, worldTick)
	if err != nil {
		return fmt.Errorf("adding granary stock: %w", err)
	}
	return nil
}

// ConsumeGranaryStock decrements available (unreserved) stock by quantity.
func (c *CivicRegistryDB) ConsumeGranaryStock(itemID string, quantity int, worldTick int64) error {
	return c.ConsumeGranaryStockContext(context.Background(), itemID, quantity, worldTick)
}

// ConsumeGranaryStockContext decrements available stock by quantity with context.
func (c *CivicRegistryDB) ConsumeGranaryStockContext(ctx context.Context, itemID string, quantity int, worldTick int64) error {
	if itemID == "" {
		return errors.New("item_id cannot be empty")
	}
	if quantity <= 0 {
		return ErrInvalidQuantity
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning consume transaction: %w", err)
	}
	defer tx.Rollback()

	var currentQty, reservedQty int
	err = tx.QueryRowContext(ctx, `SELECT quantity, reserved_quantity FROM settlement_granary WHERE item_id = ?`, itemID).Scan(&currentQty, &reservedQty)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %q does not exist in granary", ErrInsufficientStock, itemID)
	}
	if err != nil {
		return fmt.Errorf("querying granary stock: %w", err)
	}

	available := currentQty - reservedQty
	if available < quantity {
		return fmt.Errorf("%w: item %q has %d available, %d requested", ErrInsufficientStock, itemID, available, quantity)
	}

	updateQuery := `
		UPDATE settlement_granary
		SET quantity = quantity - ?, last_updated_tick = ?
		WHERE item_id = ?;
	`
	if _, err := tx.ExecContext(ctx, updateQuery, quantity, worldTick, itemID); err != nil {
		return fmt.Errorf("updating granary stock: %w", err)
	}

	return tx.Commit()
}

// ReserveGranaryStock reserves an amount of available stock.
func (c *CivicRegistryDB) ReserveGranaryStock(itemID string, quantity int, worldTick int64) error {
	return c.ReserveGranaryStockContext(context.Background(), itemID, quantity, worldTick)
}

// ReserveGranaryStockContext reserves an amount of available stock with context.
func (c *CivicRegistryDB) ReserveGranaryStockContext(ctx context.Context, itemID string, quantity int, worldTick int64) error {
	if itemID == "" {
		return errors.New("item_id cannot be empty")
	}
	if quantity <= 0 {
		return ErrInvalidQuantity
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning reserve transaction: %w", err)
	}
	defer tx.Rollback()

	var currentQty, reservedQty int
	err = tx.QueryRowContext(ctx, `SELECT quantity, reserved_quantity FROM settlement_granary WHERE item_id = ?`, itemID).Scan(&currentQty, &reservedQty)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %q does not exist in granary", ErrInsufficientStock, itemID)
	}
	if err != nil {
		return fmt.Errorf("querying granary stock: %w", err)
	}

	available := currentQty - reservedQty
	if available < quantity {
		return fmt.Errorf("%w: cannot reserve %d of item %q (%d available)", ErrInsufficientStock, quantity, itemID, available)
	}

	updateQuery := `
		UPDATE settlement_granary
		SET reserved_quantity = reserved_quantity + ?, last_updated_tick = ?
		WHERE item_id = ?;
	`
	if _, err := tx.ExecContext(ctx, updateQuery, quantity, worldTick, itemID); err != nil {
		return fmt.Errorf("reserving granary stock: %w", err)
	}

	return tx.Commit()
}

// ReleaseGranaryStock releases an amount of previously reserved stock.
func (c *CivicRegistryDB) ReleaseGranaryStock(itemID string, quantity int, worldTick int64) error {
	return c.ReleaseGranaryStockContext(context.Background(), itemID, quantity, worldTick)
}

// ReleaseGranaryStockContext releases an amount of previously reserved stock with context.
func (c *CivicRegistryDB) ReleaseGranaryStockContext(ctx context.Context, itemID string, quantity int, worldTick int64) error {
	if itemID == "" {
		return errors.New("item_id cannot be empty")
	}
	if quantity <= 0 {
		return ErrInvalidQuantity
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning release transaction: %w", err)
	}
	defer tx.Rollback()

	var reservedQty int
	err = tx.QueryRowContext(ctx, `SELECT reserved_quantity FROM settlement_granary WHERE item_id = ?`, itemID).Scan(&reservedQty)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %q not in granary", ErrInsufficientStock, itemID)
	}
	if err != nil {
		return fmt.Errorf("querying granary reserved stock: %w", err)
	}

	if reservedQty < quantity {
		return fmt.Errorf("%w: reserved %d < requested %d", ErrInsufficientStock, reservedQty, quantity)
	}

	updateQuery := `
		UPDATE settlement_granary
		SET reserved_quantity = reserved_quantity - ?, last_updated_tick = ?
		WHERE item_id = ?;
	`
	if _, err := tx.ExecContext(ctx, updateQuery, quantity, worldTick, itemID); err != nil {
		return fmt.Errorf("releasing granary reserved stock: %w", err)
	}

	return tx.Commit()
}

// ConsumeReservedGranaryStock consumes previously reserved stock from the granary.
func (c *CivicRegistryDB) ConsumeReservedGranaryStock(itemID string, quantity int, worldTick int64) error {
	return c.ConsumeReservedGranaryStockContext(context.Background(), itemID, quantity, worldTick)
}

// ConsumeReservedGranaryStockContext consumes previously reserved stock from the granary with context.
func (c *CivicRegistryDB) ConsumeReservedGranaryStockContext(ctx context.Context, itemID string, quantity int, worldTick int64) error {
	if itemID == "" {
		return errors.New("item_id cannot be empty")
	}
	if quantity <= 0 {
		return ErrInvalidQuantity
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning consume reserved transaction: %w", err)
	}
	defer tx.Rollback()

	var currentQty, reservedQty int
	err = tx.QueryRowContext(ctx, `SELECT quantity, reserved_quantity FROM settlement_granary WHERE item_id = ?`, itemID).Scan(&currentQty, &reservedQty)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %q does not exist in granary", ErrInsufficientStock, itemID)
	}
	if err != nil {
		return fmt.Errorf("querying granary stock: %w", err)
	}

	if reservedQty < quantity {
		return fmt.Errorf("%w: reserved quantity %d is less than requested %d", ErrInsufficientStock, reservedQty, quantity)
	}

	updateQuery := `
		UPDATE settlement_granary
		SET quantity = quantity - ?, reserved_quantity = reserved_quantity - ?, last_updated_tick = ?
		WHERE item_id = ?;
	`
	if _, err := tx.ExecContext(ctx, updateQuery, quantity, quantity, worldTick, itemID); err != nil {
		return fmt.Errorf("updating consumed reserved stock: %w", err)
	}

	return tx.Commit()
}

// GetGranaryInventory retrieves all stock records from the granary.
func (c *CivicRegistryDB) GetGranaryInventory() ([]*GranaryStock, error) {
	return c.GetGranaryInventoryContext(context.Background())
}

// GetGranaryInventoryContext retrieves all stock records from the granary with context.
func (c *CivicRegistryDB) GetGranaryInventoryContext(ctx context.Context) ([]*GranaryStock, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	rows, err := c.db.QueryContext(ctx, `SELECT item_id, quantity, reserved_quantity, last_updated_tick FROM settlement_granary ORDER BY item_id ASC;`)
	if err != nil {
		return nil, fmt.Errorf("querying granary inventory: %w", err)
	}
	defer rows.Close()

	var result []*GranaryStock
	for rows.Next() {
		var s GranaryStock
		if err := rows.Scan(&s.ItemID, &s.Quantity, &s.ReservedQuantity, &s.LastUpdatedTick); err != nil {
			return nil, fmt.Errorf("scanning granary inventory row: %w", err)
		}
		result = append(result, &s)
	}
	return result, rows.Err()
}

// GetItemStock retrieves the granary stock record for a specific item. Returns nil, nil if not found.
func (c *CivicRegistryDB) GetItemStock(itemID string) (*GranaryStock, error) {
	return c.GetItemStockContext(context.Background(), itemID)
}

// GetItemStockContext retrieves the granary stock record for a specific item with context.
func (c *CivicRegistryDB) GetItemStockContext(ctx context.Context, itemID string) (*GranaryStock, error) {
	if itemID == "" {
		return nil, errors.New("item_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `SELECT item_id, quantity, reserved_quantity, last_updated_tick FROM settlement_granary WHERE item_id = ?;`
	var s GranaryStock
	err := c.db.QueryRowContext(ctx, query, itemID).Scan(&s.ItemID, &s.Quantity, &s.ReservedQuantity, &s.LastUpdatedTick)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying item stock: %w", err)
	}
	return &s, nil
}

// -----------------------------------------------------------------------------
// Construction Project Operations
// -----------------------------------------------------------------------------

// CreateProject registers a new construction project.
func (c *CivicRegistryDB) CreateProject(p *ConstructionProject) error {
	return c.CreateProjectContext(context.Background(), p)
}

// CreateProjectContext registers a new construction project with context.
func (c *CivicRegistryDB) CreateProjectContext(ctx context.Context, p *ConstructionProject) error {
	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	if p == nil {
		return errors.New("construction project cannot be nil")
	}
	if p.ProjectID == "" {
		return errors.New("project_id cannot be empty")
	}
	if p.BlueprintID == "" {
		return errors.New("blueprint_id cannot be empty")
	}
	if p.Status == "" {
		p.Status = ProjectStatusPlanned
	}
	if !IsValidProjectStatus(p.Status) {
		return fmt.Errorf("invalid project status: %q", p.Status)
	}
	if p.Quadrant == "" {
		return errors.New("quadrant cannot be empty")
	}
	if !IsValidQuadrant(p.Quadrant) {
		return fmt.Errorf("invalid quadrant: %q", p.Quadrant)
	}
	if math.IsNaN(p.ProgressPercent) || math.IsInf(p.ProgressPercent, 0) || p.ProgressPercent < 0.0 || p.ProgressPercent > 100.0 {
		return errors.New("progress_percent must be a finite number between 0.0 and 100.0")
	}
	if p.MaterialsReservedJSON == "" {
		p.MaterialsReservedJSON = "{}"
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO construction_projects (
			project_id, blueprint_id, status, anchor_x, anchor_y, anchor_z,
			quadrant, progress_percent, materials_reserved_json, created_tick, updated_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := c.db.ExecContext(ctx, query,
		p.ProjectID, p.BlueprintID, p.Status, p.AnchorX, p.AnchorY, p.AnchorZ,
		p.Quadrant, p.ProgressPercent, p.MaterialsReservedJSON, p.CreatedTick, p.UpdatedTick,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: %v", ErrDuplicateRecord, err)
		}
		return fmt.Errorf("creating construction project: %w", err)
	}
	return nil
}

// UpdateProjectProgress updates the progress percent of an active project.
func (c *CivicRegistryDB) UpdateProjectProgress(projectID string, progressPercent float64, worldTick int64) error {
	return c.UpdateProjectProgressContext(context.Background(), projectID, progressPercent, worldTick)
}

// UpdateProjectProgressContext updates the progress percent of an active project with context.
func (c *CivicRegistryDB) UpdateProjectProgressContext(ctx context.Context, projectID string, progressPercent float64, worldTick int64) error {
	if projectID == "" {
		return errors.New("project_id cannot be empty")
	}
	if math.IsNaN(progressPercent) || math.IsInf(progressPercent, 0) || progressPercent < 0.0 || progressPercent > 100.0 {
		return errors.New("progress_percent must be a finite number between 0.0 and 100.0")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `UPDATE construction_projects SET progress_percent = ?, updated_tick = ? WHERE project_id = ?;`
	res, err := c.db.ExecContext(ctx, query, progressPercent, worldTick, projectID)
	if err != nil {
		return fmt.Errorf("updating project progress: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateProjectStatus updates the status of a project.
func (c *CivicRegistryDB) UpdateProjectStatus(projectID string, status string, worldTick int64) error {
	return c.UpdateProjectStatusContext(context.Background(), projectID, status, worldTick)
}

// UpdateProjectStatusContext updates the status of a project with context.
func (c *CivicRegistryDB) UpdateProjectStatusContext(ctx context.Context, projectID string, status string, worldTick int64) error {
	if projectID == "" {
		return errors.New("project_id cannot be empty")
	}
	if status == "" {
		return errors.New("status cannot be empty")
	}
	if !IsValidProjectStatus(status) {
		return fmt.Errorf("invalid project status: %q", status)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `UPDATE construction_projects SET status = ?, updated_tick = ? WHERE project_id = ?;`
	res, err := c.db.ExecContext(ctx, query, status, worldTick, projectID)
	if err != nil {
		return fmt.Errorf("updating project status: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateProjectMaterialsReserved updates the materials reserved JSON of a project.
func (c *CivicRegistryDB) UpdateProjectMaterialsReserved(projectID string, materialsReservedJSON string, worldTick int64) error {
	return c.UpdateProjectMaterialsReservedContext(context.Background(), projectID, materialsReservedJSON, worldTick)
}

// UpdateProjectMaterialsReservedContext updates the materials reserved JSON of a project with context.
func (c *CivicRegistryDB) UpdateProjectMaterialsReservedContext(ctx context.Context, projectID string, materialsReservedJSON string, worldTick int64) error {
	if projectID == "" {
		return errors.New("project_id cannot be empty")
	}
	if materialsReservedJSON == "" {
		materialsReservedJSON = "{}"
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `UPDATE construction_projects SET materials_reserved_json = ?, updated_tick = ? WHERE project_id = ?;`
	res, err := c.db.ExecContext(ctx, query, materialsReservedJSON, worldTick, projectID)
	if err != nil {
		return fmt.Errorf("updating project materials reserved: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetProject retrieves a construction project by projectID. Returns nil, nil if not found.
func (c *CivicRegistryDB) GetProject(projectID string) (*ConstructionProject, error) {
	return c.GetProjectContext(context.Background(), projectID)
}

// GetProjectContext retrieves a construction project by projectID with context.
func (c *CivicRegistryDB) GetProjectContext(ctx context.Context, projectID string) (*ConstructionProject, error) {
	if projectID == "" {
		return nil, errors.New("project_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT project_id, blueprint_id, status, anchor_x, anchor_y, anchor_z,
		       quadrant, progress_percent, materials_reserved_json, created_tick, updated_tick
		FROM construction_projects
		WHERE project_id = ?;
	`
	var p ConstructionProject
	err := c.db.QueryRowContext(ctx, query, projectID).Scan(
		&p.ProjectID, &p.BlueprintID, &p.Status, &p.AnchorX, &p.AnchorY, &p.AnchorZ,
		&p.Quadrant, &p.ProgressPercent, &p.MaterialsReservedJSON, &p.CreatedTick, &p.UpdatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying construction project: %w", err)
	}
	return &p, nil
}

// ListProjects returns all registered construction projects.
func (c *CivicRegistryDB) ListProjects() ([]*ConstructionProject, error) {
	return c.ListProjectsContext(context.Background())
}

// ListProjectsContext returns all registered construction projects with context.
func (c *CivicRegistryDB) ListProjectsContext(ctx context.Context) ([]*ConstructionProject, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT project_id, blueprint_id, status, anchor_x, anchor_y, anchor_z,
		       quadrant, progress_percent, materials_reserved_json, created_tick, updated_tick
		FROM construction_projects
		ORDER BY created_tick ASC, project_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing construction projects: %w", err)
	}
	defer rows.Close()

	var projects []*ConstructionProject
	for rows.Next() {
		var p ConstructionProject
		if err := rows.Scan(
			&p.ProjectID, &p.BlueprintID, &p.Status, &p.AnchorX, &p.AnchorY, &p.AnchorZ,
			&p.Quadrant, &p.ProgressPercent, &p.MaterialsReservedJSON, &p.CreatedTick, &p.UpdatedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning project row: %w", err)
		}
		projects = append(projects, &p)
	}
	return projects, rows.Err()
}

// -----------------------------------------------------------------------------
// Functional Zone Operations
// -----------------------------------------------------------------------------

// RegisterZone registers a spatial functional zone.
func (c *CivicRegistryDB) RegisterZone(z *FunctionalZone) error {
	return c.RegisterZoneContext(context.Background(), z)
}

// RegisterFunctionalZone registers a spatial functional zone (alias for RegisterZone).
func (c *CivicRegistryDB) RegisterFunctionalZone(z *FunctionalZone) error {
	return c.RegisterZoneContext(context.Background(), z)
}

// RegisterFunctionalZoneContext registers a spatial functional zone with context (alias for RegisterZoneContext).
func (c *CivicRegistryDB) RegisterFunctionalZoneContext(ctx context.Context, z *FunctionalZone) error {
	return c.RegisterZoneContext(ctx, z)
}

// RegisterZoneContext registers a spatial functional zone with context.
func (c *CivicRegistryDB) RegisterZoneContext(ctx context.Context, z *FunctionalZone) error {
	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	if z == nil {
		return errors.New("functional zone cannot be nil")
	}
	if z.ZoneID == "" {
		return errors.New("zone_id cannot be empty")
	}
	if z.ZoneType == "" {
		return errors.New("zone_type cannot be empty")
	}
	if !IsValidZoneType(z.ZoneType) {
		return fmt.Errorf("invalid zone type: %q", z.ZoneType)
	}
	if z.Name == "" {
		return errors.New("name cannot be empty")
	}
	if z.MinX > z.MaxX || z.MinY > z.MaxY || z.MinZ > z.MaxZ {
		return ErrInvalidCoordinates
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO functional_zones (
			zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
			owner_agent_id, name, created_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := c.db.ExecContext(ctx, query,
		z.ZoneID, z.ZoneType, z.MinX, z.MinY, z.MinZ, z.MaxX, z.MaxY, z.MaxZ,
		z.OwnerAgentID, z.Name, z.CreatedTick,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: %v", ErrDuplicateRecord, err)
		}
		return fmt.Errorf("registering functional zone: %w", err)
	}
	return nil
}

// GetZone retrieves a functional zone by zoneID. Returns nil, nil if not found.
func (c *CivicRegistryDB) GetZone(zoneID string) (*FunctionalZone, error) {
	return c.GetZoneContext(context.Background(), zoneID)
}

// GetZoneContext retrieves a functional zone by zoneID with context.
func (c *CivicRegistryDB) GetZoneContext(ctx context.Context, zoneID string) (*FunctionalZone, error) {
	if zoneID == "" {
		return nil, errors.New("zone_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE zone_id = ?;
	`
	var z FunctionalZone
	err := c.db.QueryRowContext(ctx, query, zoneID).Scan(
		&z.ZoneID, &z.ZoneType, &z.MinX, &z.MinY, &z.MinZ, &z.MaxX, &z.MaxY, &z.MaxZ,
		&z.OwnerAgentID, &z.Name, &z.CreatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying functional zone: %w", err)
	}
	return &z, nil
}

// ListZones returns all functional zones.
func (c *CivicRegistryDB) ListZones() ([]*FunctionalZone, error) {
	return c.ListZonesContext(context.Background())
}

// ListZonesContext returns all functional zones with context.
func (c *CivicRegistryDB) ListZonesContext(ctx context.Context) ([]*FunctionalZone, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		ORDER BY created_tick ASC, zone_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing functional zones: %w", err)
	}
	defer rows.Close()

	var zones []*FunctionalZone
	for rows.Next() {
		var z FunctionalZone
		if err := rows.Scan(
			&z.ZoneID, &z.ZoneType, &z.MinX, &z.MinY, &z.MinZ, &z.MaxX, &z.MaxY, &z.MaxZ,
			&z.OwnerAgentID, &z.Name, &z.CreatedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning zone row: %w", err)
		}
		zones = append(zones, &z)
	}
	return zones, rows.Err()
}

// FindZoneAt returns the most specific functional zone containing the given coordinates (x, y, z), or nil if none found.
func (c *CivicRegistryDB) FindZoneAt(x, y, z int) (*FunctionalZone, error) {
	return c.FindZoneAtContext(context.Background(), x, y, z)
}

// FindZoneAtContext returns the most specific functional zone containing coordinates (x, y, z) with context.
// When multiple zones contain (x, y, z), the most specific (smallest volume) zone is returned first.
func (c *CivicRegistryDB) FindZoneAtContext(ctx context.Context, x, y, z int) (*FunctionalZone, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE min_x <= ? AND max_x >= ?
		  AND min_y <= ? AND max_y >= ?
		  AND min_z <= ? AND max_z >= ?
		ORDER BY (max_x - min_x) * (max_y - min_y) * (max_z - min_z) ASC, created_tick DESC, zone_id ASC
		LIMIT 1;
	`
	var zone FunctionalZone
	err := c.db.QueryRowContext(ctx, query, x, x, y, y, z, z).Scan(
		&zone.ZoneID, &zone.ZoneType, &zone.MinX, &zone.MinY, &zone.MinZ, &zone.MaxX, &zone.MaxY, &zone.MaxZ,
		&zone.OwnerAgentID, &zone.Name, &zone.CreatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding zone at coordinates: %w", err)
	}
	return &zone, nil
}

// FindZonesAt returns all functional zones containing the given coordinates (x, y, z), ordered most specific first.
func (c *CivicRegistryDB) FindZonesAt(x, y, z int) ([]*FunctionalZone, error) {
	return c.FindZonesAtContext(context.Background(), x, y, z)
}

// FindZonesAtContext returns all functional zones containing coordinates (x, y, z) with context, ordered most specific first.
func (c *CivicRegistryDB) FindZonesAtContext(ctx context.Context, x, y, z int) ([]*FunctionalZone, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE min_x <= ? AND max_x >= ?
		  AND min_y <= ? AND max_y >= ?
		  AND min_z <= ? AND max_z >= ?
		ORDER BY (max_x - min_x) * (max_y - min_y) * (max_z - min_z) ASC, created_tick DESC, zone_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, x, x, y, y, z, z)
	if err != nil {
		return nil, fmt.Errorf("finding zones at coordinates: %w", err)
	}
	defer rows.Close()

	var zones []*FunctionalZone
	for rows.Next() {
		var zone FunctionalZone
		if err := rows.Scan(
			&zone.ZoneID, &zone.ZoneType, &zone.MinX, &zone.MinY, &zone.MinZ, &zone.MaxX, &zone.MaxY, &zone.MaxZ,
			&zone.OwnerAgentID, &zone.Name, &zone.CreatedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning zone row: %w", err)
		}
		zones = append(zones, &zone)
	}
	return zones, rows.Err()
}

// FindZoneAtByType returns the most specific functional zone of the specified type containing (x, y, z), or nil if none found.
func (c *CivicRegistryDB) FindZoneAtByType(x, y, z int, zoneType string) (*FunctionalZone, error) {
	return c.FindZoneAtByTypeContext(context.Background(), x, y, z, zoneType)
}

// FindZoneAtByTypeContext returns the most specific functional zone of the specified type containing (x, y, z) with context.
func (c *CivicRegistryDB) FindZoneAtByTypeContext(ctx context.Context, x, y, z int, zoneType string) (*FunctionalZone, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE min_x <= ? AND max_x >= ?
		  AND min_y <= ? AND max_y >= ?
		  AND min_z <= ? AND max_z >= ?
		  AND zone_type = ?
		ORDER BY (max_x - min_x) * (max_y - min_y) * (max_z - min_z) ASC, created_tick DESC, zone_id ASC
		LIMIT 1;
	`
	var zone FunctionalZone
	err := c.db.QueryRowContext(ctx, query, x, x, y, y, z, z, zoneType).Scan(
		&zone.ZoneID, &zone.ZoneType, &zone.MinX, &zone.MinY, &zone.MinZ, &zone.MaxX, &zone.MaxY, &zone.MaxZ,
		&zone.OwnerAgentID, &zone.Name, &zone.CreatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding zone at coordinates by type: %w", err)
	}
	return &zone, nil
}

// ListZonesByType returns all functional zones matching the specified zoneType.
func (c *CivicRegistryDB) ListZonesByType(zoneType string) ([]*FunctionalZone, error) {
	return c.ListZonesByTypeContext(context.Background(), zoneType)
}

// ListZonesByTypeContext returns all functional zones matching the specified zoneType with context.
func (c *CivicRegistryDB) ListZonesByTypeContext(ctx context.Context, zoneType string) ([]*FunctionalZone, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z,
		       owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE zone_type = ?
		ORDER BY created_tick ASC, zone_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, zoneType)
	if err != nil {
		return nil, fmt.Errorf("listing functional zones by type: %w", err)
	}
	defer rows.Close()

	var zones []*FunctionalZone
	for rows.Next() {
		var z FunctionalZone
		if err := rows.Scan(
			&z.ZoneID, &z.ZoneType, &z.MinX, &z.MinY, &z.MinZ, &z.MaxX, &z.MaxY, &z.MaxZ,
			&z.OwnerAgentID, &z.Name, &z.CreatedTick,
		); err != nil {
			return nil, fmt.Errorf("scanning zone row: %w", err)
		}
		zones = append(zones, &z)
	}
	return zones, rows.Err()
}

// -----------------------------------------------------------------------------
// Settler Population Operations
// -----------------------------------------------------------------------------

// RegisterSettler registers an agent as an active settler of the settlement clan.
func (c *CivicRegistryDB) RegisterSettler(agentID, profession string, worldTick int64) error {
	return c.RegisterSettlerContext(context.Background(), agentID, profession, worldTick)
}

// RegisterSettlerContext registers an agent as an active settler with context.
func (c *CivicRegistryDB) RegisterSettlerContext(ctx context.Context, agentID, profession string, worldTick int64) error {
	if agentID == "" {
		return errors.New("agent_id cannot be empty")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO settlement_settlers (agent_id, clan_id, profession, registered_tick)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			clan_id = excluded.clan_id,
			profession = excluded.profession,
			registered_tick = excluded.registered_tick;
	`
	_, err := c.db.ExecContext(ctx, query, agentID, c.clanID, profession, worldTick)
	if err != nil {
		return fmt.Errorf("registering settler: %w", err)
	}
	return nil
}

// GetSettlementPopulation retrieves the count of active settlers in the clan.
// It checks settlement_settlers first; if empty, it falls back to distinct agent IDs
// in functional_zones, civic_votes, and clan_governance.
func (c *CivicRegistryDB) GetSettlementPopulation() (int, error) {
	return c.GetSettlementPopulationContext(context.Background())
}

// GetSettlementPopulationContext retrieves the count of active settlers with context.
func (c *CivicRegistryDB) GetSettlementPopulationContext(ctx context.Context) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return 0, ErrClosed
	}

	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_settlers WHERE clan_id = ?;`, c.clanID).Scan(&count)
	if err == nil && count > 0 {
		return count, nil
	}

	query := `
		SELECT COUNT(DISTINCT agent_id) FROM (
			SELECT owner_agent_id AS agent_id FROM functional_zones WHERE owner_agent_id != ''
			UNION
			SELECT agent_id FROM civic_votes WHERE agent_id != ''
			UNION
			SELECT leader_agent_id AS agent_id FROM clan_governance WHERE leader_agent_id != ''
		);
	`
	err = c.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("querying settlement population: %w", err)
	}
	return count, nil
}

// -----------------------------------------------------------------------------
// Collective Clan Spatial Ledger & Horizon Cartography (ADR-024, ADR-032)
// -----------------------------------------------------------------------------

// UpsertSettlementPOIsBatch merges an agent's discovered POIs into the clan's
// settlement_pois ledger (ADR-024).
func (c *CivicRegistryDB) UpsertSettlementPOIsBatch(ctx context.Context, settlementID, creatorAgentID string, pois []AgentPOI) error {
	if len(pois) == 0 {
		return nil
	}
	if strings.TrimSpace(settlementID) == "" {
		settlementID = c.clanID
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning settlement_pois batch tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO settlement_pois (
			settlement_poi_id, settlement_id, category, block_id,
			x, y, z, access_x, access_y, access_z,
			elevation_layer, creator_agent_id, registered_tick, last_verified_tick, is_active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(settlement_poi_id) DO UPDATE SET
			category = excluded.category,
			block_id = excluded.block_id,
			x = excluded.x,
			y = excluded.y,
			z = excluded.z,
			access_x = excluded.access_x,
			access_y = excluded.access_y,
			access_z = excluded.access_z,
			elevation_layer = excluded.elevation_layer,
			last_verified_tick = MAX(settlement_pois.last_verified_tick, excluded.last_verified_tick),
			is_active = excluded.is_active;
	`)
	if err != nil {
		return fmt.Errorf("preparing settlement_pois upsert: %w", err)
	}
	defer stmt.Close()

	for _, poi := range pois {
		if poi.POIID == "" {
			continue
		}
		isActive := 0
		if poi.IsActive {
			isActive = 1
		}
		if _, err := stmt.ExecContext(ctx,
			poi.POIID, settlementID, poi.Category, poi.BlockID,
			poi.X, poi.Y, poi.Z, poi.AccessX, poi.AccessY, poi.AccessZ,
			poi.ElevationLayer, creatorAgentID, poi.DiscoveredTick, poi.LastVerifiedTick, isActive,
		); err != nil {
			return fmt.Errorf("upserting settlement POI %s: %w", poi.POIID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing settlement_pois batch tx: %w", err)
	}
	return nil
}

// ListSettlementPOIs retrieves active communal POIs from settlement_pois for a settlement.
func (c *CivicRegistryDB) ListSettlementPOIs(ctx context.Context, settlementID string) ([]SettlementPOI, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	var rows *sql.Rows
	var err error
	if strings.TrimSpace(settlementID) != "" {
		rows, err = c.db.QueryContext(ctx, `
			SELECT settlement_poi_id, settlement_id, category, block_id,
			       x, y, z, access_x, access_y, access_z,
			       elevation_layer, creator_agent_id, registered_tick, last_verified_tick, is_active
			FROM settlement_pois
			WHERE settlement_id = ? AND is_active = 1
			ORDER BY registered_tick ASC, settlement_poi_id ASC;
		`, settlementID)
	} else {
		rows, err = c.db.QueryContext(ctx, `
			SELECT settlement_poi_id, settlement_id, category, block_id,
			       x, y, z, access_x, access_y, access_z,
			       elevation_layer, creator_agent_id, registered_tick, last_verified_tick, is_active
			FROM settlement_pois
			WHERE is_active = 1
			ORDER BY registered_tick ASC, settlement_poi_id ASC;
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("listing settlement POIs: %w", err)
	}
	defer rows.Close()

	var pois []SettlementPOI
	for rows.Next() {
		var p SettlementPOI
		var isActive int
		if err := rows.Scan(
			&p.SettlementPOIID, &p.SettlementID, &p.Category, &p.BlockID,
			&p.X, &p.Y, &p.Z, &p.AccessX, &p.AccessY, &p.AccessZ,
			&p.ElevationLayer, &p.CreatorAgentID, &p.RegisteredTick, &p.LastVerifiedTick, &isActive,
		); err != nil {
			return nil, fmt.Errorf("scanning settlement POI: %w", err)
		}
		p.IsActive = isActive != 0
		pois = append(pois, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating settlement POIs: %w", err)
	}
	if pois == nil {
		return []SettlementPOI{}, nil
	}
	return pois, nil
}

// MergeExplorerHorizonSurvey merges an explorer agent's surveyed horizon sectors
// into the clan's collective clan_horizon_sectors table (ADR-032).
func (c *CivicRegistryDB) MergeExplorerHorizonSurvey(
	ctx context.Context,
	explorerID string,
	originX, originY, originZ float64,
	surveyedTick int64,
	sectors []HorizonSector,
) error {
	if strings.TrimSpace(explorerID) == "" {
		return errors.New("explorer_id cannot be empty")
	}
	if len(sectors) == 0 {
		return nil
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning clan_horizon_sectors merge tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO clan_horizon_sectors (
			explorer_id, origin_x, origin_y, origin_z, sector_index, bearing_deg,
			hit_distance, feature_type, hit_block_id, hit_x, hit_y, hit_z,
			elevation_delta, occluded, open_horizon, surveyed_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(explorer_id, origin_x, origin_y, origin_z, sector_index) DO UPDATE SET
			bearing_deg = excluded.bearing_deg,
			hit_distance = excluded.hit_distance,
			feature_type = excluded.feature_type,
			hit_block_id = excluded.hit_block_id,
			hit_x = excluded.hit_x,
			hit_y = excluded.hit_y,
			hit_z = excluded.hit_z,
			elevation_delta = excluded.elevation_delta,
			occluded = excluded.occluded,
			open_horizon = excluded.open_horizon,
			surveyed_tick = MAX(clan_horizon_sectors.surveyed_tick, excluded.surveyed_tick);
	`)
	if err != nil {
		return fmt.Errorf("preparing clan_horizon_sectors merge: %w", err)
	}
	defer stmt.Close()

	for _, sec := range sectors {
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
		if _, err := stmt.ExecContext(ctx,
			explorerID, originX, originY, originZ, sec.SectorIndex, sec.BearingDeg,
			sec.HitDistance, sec.FeatureType, sec.HitBlockID, sec.HitX, sec.HitY, sec.HitZ,
			sec.ElevationDelta, occluded, openHorizon, tick,
		); err != nil {
			return fmt.Errorf("merging clan horizon sector %d: %w", sec.SectorIndex, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing clan_horizon_sectors merge tx: %w", err)
	}
	return nil
}

// ScoreCollectiveSettlementCandidates groups surveyed origins/regions across all merged
// clan explorers, scores each region for FOREST_CANOPY, WATER, OPEN_PLAINS, STONE_OUTCROP
// (penalizing RIDGE_OBSTRUCTION and heavy occlusion), and returns the top settlement candidate.
// Returns nil, nil if no horizon surveys have been merged into the clan DB yet.
func (c *CivicRegistryDB) ScoreCollectiveSettlementCandidates(ctx context.Context) (*SettlementCandidate, error) {
	ranked, err := c.RankCollectiveSettlementCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if len(ranked) == 0 {
		return nil, nil
	}
	top := ranked[0]
	return &top, nil
}

// RankCollectiveSettlementCandidates scores and ranks all surveyed clan regions descending by suitability.
func (c *CivicRegistryDB) RankCollectiveSettlementCandidates(ctx context.Context) ([]SettlementCandidate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	rows, err := c.db.QueryContext(ctx, `
		SELECT explorer_id, origin_x, origin_y, origin_z, sector_index,
		       feature_type, occluded, open_horizon, surveyed_tick
		FROM clan_horizon_sectors
		ORDER BY surveyed_tick ASC, explorer_id ASC, origin_x ASC, origin_z ASC, sector_index ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("querying clan_horizon_sectors: %w", err)
	}
	defer rows.Close()

	type sweepKey struct {
		explorerID string
		ox, oy, oz float64
	}
	type sweepData struct {
		key       sweepKey
		forest    int
		water     int
		plains    int
		stone     int
		structure int
		ridge     int
		occluded  int
		total     int
	}

	sweepMap := make(map[sweepKey]*sweepData)
	var sweepOrder []sweepKey

	for rows.Next() {
		var explorerID, featureType string
		var ox, oy, oz float64
		var sectorIdx, occluded, openHorizon int
		var tick int64
		if err := rows.Scan(&explorerID, &ox, &oy, &oz, &sectorIdx, &featureType, &occluded, &openHorizon, &tick); err != nil {
			return nil, fmt.Errorf("scanning clan_horizon_sectors row: %w", err)
		}
		k := sweepKey{explorerID: explorerID, ox: ox, oy: oy, oz: oz}
		sw, exists := sweepMap[k]
		if !exists {
			sw = &sweepData{key: k}
			sweepMap[k] = sw
			sweepOrder = append(sweepOrder, k)
		}
		sw.total++
		if occluded != 0 {
			sw.occluded++
		}
		switch strings.ToUpper(strings.TrimSpace(featureType)) {
		case "FOREST_CANOPY":
			sw.forest++
		case "WATER":
			sw.water++
		case "OPEN_PLAINS":
			sw.plains++
		case "STONE_OUTCROP":
			sw.stone++
		case "STRUCTURE":
			sw.structure++
		case "RIDGE_OBSTRUCTION":
			sw.ridge++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating clan_horizon_sectors: %w", err)
	}
	if len(sweepOrder) == 0 {
		return []SettlementCandidate{}, nil
	}

	type regionCluster struct {
		sumX, sumY, sumZ float64
		centroidX        float64
		centroidY        float64
		centroidZ        float64
		sweeps           []*sweepData
		explorers        map[string]struct{}
	}

	const clusterRadius = 32.0
	var clusters []*regionCluster
	for _, k := range sweepOrder {
		sw := sweepMap[k]
		var target *regionCluster
		bestDist := math.MaxFloat64
		for _, cl := range clusters {
			d := math.Hypot(k.ox-cl.centroidX, k.oz-cl.centroidZ)
			if d <= clusterRadius && d < bestDist {
				bestDist = d
				target = cl
			}
		}
		if target == nil {
			target = &regionCluster{
				explorers: make(map[string]struct{}),
			}
			clusters = append(clusters, target)
		}
		target.sweeps = append(target.sweeps, sw)
		target.explorers[k.explorerID] = struct{}{}
		target.sumX += k.ox
		target.sumY += k.oy
		target.sumZ += k.oz
		n := float64(len(target.sweeps))
		target.centroidX = target.sumX / n
		target.centroidY = target.sumY / n
		target.centroidZ = target.sumZ / n
	}

	candidates := make([]SettlementCandidate, 0, len(clusters))
	for _, cl := range clusters {
		nSweeps := float64(len(cl.sweeps))
		var sumForest, sumWater, sumPlains, sumStone, sumStruct, sumRidge, sumOcc int
		for _, sw := range cl.sweeps {
			sumForest += sw.forest
			sumWater += sw.water
			sumPlains += sw.plains
			sumStone += sw.stone
			sumStruct += sw.structure
			sumRidge += sw.ridge
			sumOcc += sw.occluded
		}
		avgForest := float64(sumForest) / nSweeps
		avgWater := float64(sumWater) / nSweeps
		avgPlains := float64(sumPlains) / nSweeps
		avgStone := float64(sumStone) / nSweeps
		avgStruct := float64(sumStruct) / nSweeps
		avgRidge := float64(sumRidge) / nSweeps
		avgOcc := float64(sumOcc) / nSweeps

		score := math.Min(avgForest, 10.0)*3.0 +
			math.Min(avgWater, 6.0)*3.5 +
			math.Min(avgPlains, 12.0)*2.5 +
			math.Min(avgStone, 6.0)*2.0 +
			math.Min(avgStruct, 4.0)*1.5 -
			avgRidge*2.5 -
			avgOcc*2.0

		if avgForest >= 1.5 && avgPlains >= 2.0 {
			score += 12.0
		}
		if avgWater >= 1.0 {
			score += 8.0
		}
		if avgStone >= 1.0 {
			score += 5.0
		}
		explorerCount := len(cl.explorers)
		if explorerCount > 1 {
			score += float64(explorerCount-1) * 5.0
		}

		cx := math.Round(cl.centroidX*10) / 10
		cy := math.Round(cl.centroidY*10) / 10
		cz := math.Round(cl.centroidZ*10) / 10
		roundedScore := math.Round(score*10) / 10

		summary := fmt.Sprintf(
			"%d explorer(s), forest=%.1f, water=%.1f, plains=%.1f, stone=%.1f, ridges=%.1f, occluded=%.1f (score %.1f)",
			explorerCount, avgForest, avgWater, avgPlains, avgStone, avgRidge, avgOcc, roundedScore,
		)

		candidates = append(candidates, SettlementCandidate{
			X:             cx,
			Y:             cy,
			Z:             cz,
			Score:         roundedScore,
			ExplorerCount: explorerCount,
			Summary:       summary,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].ExplorerCount != candidates[j].ExplorerCount {
			return candidates[i].ExplorerCount > candidates[j].ExplorerCount
		}
		if candidates[i].X != candidates[j].X {
			return candidates[i].X < candidates[j].X
		}
		return candidates[i].Z < candidates[j].Z
	})

	return candidates, nil
}
