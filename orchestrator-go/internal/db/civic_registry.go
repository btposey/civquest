package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// Project status constants.
const (
	ProjectStatusPlanned    = "PLANNED"
	ProjectStatusInProgress = "IN_PROGRESS"
	ProjectStatusCompleted  = "COMPLETED"
	ProjectStatusHalted     = "HALTED"
	ProjectStatusAbandoned  = "ABANDONED"
)

// IsValidProjectStatus checks whether status is a valid project status.
func IsValidProjectStatus(status string) bool {
	switch status {
	case ProjectStatusPlanned, ProjectStatusInProgress, ProjectStatusCompleted,
		ProjectStatusHalted, ProjectStatusAbandoned:
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
)

// IsValidZoneType checks whether zoneType is a recognized functional zone type.
func IsValidZoneType(zoneType string) bool {
	switch zoneType {
	case ZoneTypeGranary, ZoneTypeResidential, ZoneTypeHousing,
		ZoneTypeWorkshop, ZoneTypeFarm, ZoneTypeFarmland,
		ZoneTypeTavern, ZoneTypeDefense, ZoneTypePasture,
		ZoneTypeLibrary, ZoneTypeCemetery:
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
	return OpenCivicRegistryDB(dbPath, clanID)
}

func (c *CivicRegistryDB) migrate(ctx context.Context) error {
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
