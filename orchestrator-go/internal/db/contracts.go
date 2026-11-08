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

// Contract status constants.
const (
	ContractStatusProposed   = "PROPOSED"
	ContractStatusActive     = "ACTIVE"
	ContractStatusFulfilled  = "FULFILLED"
	ContractStatusTerminated = "TERMINATED"
	ContractStatusBreached   = "BREACHED"
	ContractStatusExpired    = "EXPIRED"
)

// IsValidContractStatus checks whether status is a valid lifecycle status.
func IsValidContractStatus(status string) bool {
	switch status {
	case ContractStatusProposed, ContractStatusActive, ContractStatusFulfilled,
		ContractStatusTerminated, ContractStatusBreached, ContractStatusExpired:
		return true
	default:
		return false
	}
}

// Sentinel errors for contracts.
var (
	ErrInvalidSplitPercent = errors.New("split percentages must be non-negative and sum to 100%")
)

// ContractsDB manages the SQLite database for player-agent agrarian sharecropping
// agreements in $XDG_DATA_HOME/civquest/clans/{clan_id}/contracts.db.
type ContractsDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
}

// OpenContractsDB opens and migrates contracts.db at dbPath.
func OpenContractsDB(dbPath, clanID string) (*ContractsDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan contracts directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open contracts database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	c := &ContractsDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating contracts.db: %w", err)
	}

	return c, nil
}

// OpenContractsDBForClan resolves the path using the provided XDG resolver.
func OpenContractsDBForClan(resolver *xdg.Resolver, clanID string) (*ContractsDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanContractsDBPath(clanID)
	return OpenContractsDB(dbPath, clanID)
}

func (c *ContractsDB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sharecropping_contracts (
		contract_id TEXT PRIMARY KEY,
		player_uuid TEXT NOT NULL,
		player_name TEXT NOT NULL,
		settler_agent_id TEXT NOT NULL,
		plot_min_x INTEGER NOT NULL,
		plot_min_y INTEGER NOT NULL,
		plot_min_z INTEGER NOT NULL,
		plot_max_x INTEGER NOT NULL,
		plot_max_y INTEGER NOT NULL,
		plot_max_z INTEGER NOT NULL,
		chest_x INTEGER NOT NULL,
		chest_y INTEGER NOT NULL,
		chest_z INTEGER NOT NULL,
		split_player_percent REAL NOT NULL,
		split_clan_percent REAL NOT NULL,
		crop_type TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ACTIVE',
		created_tick INTEGER NOT NULL DEFAULT 0,
		expires_tick INTEGER NOT NULL DEFAULT 0,
		CHECK (plot_min_x <= plot_max_x AND plot_min_y <= plot_max_y AND plot_min_z <= plot_max_z),
		CHECK (split_player_percent >= 0.0 AND split_clan_percent >= 0.0),
		CHECK (abs((split_player_percent + split_clan_percent) - 100.0) < 0.01)
	);

	CREATE INDEX IF NOT EXISTS idx_contracts_player ON sharecropping_contracts(player_uuid);
	CREATE INDEX IF NOT EXISTS idx_contracts_agent ON sharecropping_contracts(settler_agent_id);
	CREATE INDEX IF NOT EXISTS idx_contracts_status ON sharecropping_contracts(status);
	`
	_, err := c.db.ExecContext(ctx, schema)
	return err
}

// ClanID returns the clan identifier.
func (c *ContractsDB) ClanID() string {
	return c.clanID
}

// Path returns the database filesystem path.
func (c *ContractsDB) Path() string {
	return c.dbPath
}

// DB returns the underlying *sql.DB handle.
func (c *ContractsDB) DB() *sql.DB {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db
}

// Close closes the database connection.
func (c *ContractsDB) Close() error {
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

// CreateContract records a new sharecropping contract.
func (c *ContractsDB) CreateContract(contract *SharecroppingContract) error {
	return c.CreateContractContext(context.Background(), contract)
}

// CreateContractContext records a new sharecropping contract with context.
func (c *ContractsDB) CreateContractContext(ctx context.Context, contract *SharecroppingContract) error {
	if contract == nil {
		return errors.New("sharecropping contract cannot be nil")
	}
	if contract.ContractID == "" {
		return errors.New("contract_id cannot be empty")
	}
	if contract.PlayerUUID == "" {
		return errors.New("player_uuid cannot be empty")
	}
	if contract.SettlerAgentID == "" {
		return errors.New("settler_agent_id cannot be empty")
	}
	if contract.CropType == "" {
		return errors.New("crop_type cannot be empty")
	}
	if contract.PlayerName == "" {
		contract.PlayerName = contract.PlayerUUID
	}
	if contract.Status == "" {
		contract.Status = ContractStatusActive
	}
	if !IsValidContractStatus(contract.Status) {
		return fmt.Errorf("invalid contract status: %q", contract.Status)
	}
	if contract.PlotMinX > contract.PlotMaxX ||
		contract.PlotMinY > contract.PlotMaxY ||
		contract.PlotMinZ > contract.PlotMaxZ {
		return ErrInvalidCoordinates
	}

	if math.IsNaN(contract.SplitPlayerPercent) || math.IsInf(contract.SplitPlayerPercent, 0) ||
		math.IsNaN(contract.SplitClanPercent) || math.IsInf(contract.SplitClanPercent, 0) ||
		contract.SplitPlayerPercent < 0.0 || contract.SplitClanPercent < 0.0 ||
		math.Abs((contract.SplitPlayerPercent+contract.SplitClanPercent)-100.0) >= 0.01 {
		return ErrInvalidSplitPercent
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
		INSERT INTO sharecropping_contracts (
			contract_id, player_uuid, player_name, settler_agent_id,
			plot_min_x, plot_min_y, plot_min_z,
			plot_max_x, plot_max_y, plot_max_z,
			chest_x, chest_y, chest_z,
			split_player_percent, split_clan_percent,
			crop_type, status, created_tick, expires_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := c.db.ExecContext(ctx, query,
		contract.ContractID, contract.PlayerUUID, contract.PlayerName, contract.SettlerAgentID,
		contract.PlotMinX, contract.PlotMinY, contract.PlotMinZ,
		contract.PlotMaxX, contract.PlotMaxY, contract.PlotMaxZ,
		contract.ChestX, contract.ChestY, contract.ChestZ,
		contract.SplitPlayerPercent, contract.SplitClanPercent,
		contract.CropType, contract.Status, contract.CreatedTick, contract.ExpiresTick,
	)
	if err != nil {
		return fmt.Errorf("creating sharecropping contract: %w", err)
	}
	return nil
}

// UpdateContractStatus updates the lifecycle status of a contract.
func (c *ContractsDB) UpdateContractStatus(contractID string, status string) error {
	return c.UpdateContractStatusContext(context.Background(), contractID, status)
}

// UpdateContractStatusContext updates the lifecycle status of a contract with context.
func (c *ContractsDB) UpdateContractStatusContext(ctx context.Context, contractID string, status string) error {
	if contractID == "" {
		return errors.New("contract_id cannot be empty")
	}
	if status == "" {
		return errors.New("status cannot be empty")
	}
	if !IsValidContractStatus(status) {
		return fmt.Errorf("invalid contract status: %q", status)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `UPDATE sharecropping_contracts SET status = ? WHERE contract_id = ?;`
	res, err := c.db.ExecContext(ctx, query, status, contractID)
	if err != nil {
		return fmt.Errorf("updating contract status: %w", err)
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

// ReassignContractSettler reassigns an active contract to a new settler agent upon permadeath handoff.
func (c *ContractsDB) ReassignContractSettler(contractID string, newAgentID string) error {
	return c.ReassignContractSettlerContext(context.Background(), contractID, newAgentID)
}

// ReassignContractSettlerContext reassigns a contract to newAgentID with context.
func (c *ContractsDB) ReassignContractSettlerContext(ctx context.Context, contractID string, newAgentID string) error {
	if contractID == "" {
		return errors.New("contract_id cannot be empty")
	}
	if err := xdg.ValidateAgentName(newAgentID); err != nil {
		return fmt.Errorf("invalid agent name for reassignment: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `UPDATE sharecropping_contracts SET settler_agent_id = ? WHERE contract_id = ?;`
	res, err := c.db.ExecContext(ctx, query, newAgentID, contractID)
	if err != nil {
		return fmt.Errorf("reassigning contract settler: %w", err)
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

// GetContract retrieves a sharecropping contract by contractID. Returns nil, nil if not found.
func (c *ContractsDB) GetContract(contractID string) (*SharecroppingContract, error) {
	return c.GetContractContext(context.Background(), contractID)
}

// GetContractContext retrieves a sharecropping contract by contractID with context.
func (c *ContractsDB) GetContractContext(ctx context.Context, contractID string) (*SharecroppingContract, error) {
	if contractID == "" {
		return nil, errors.New("contract_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		WHERE contract_id = ?;
	`
	var sc SharecroppingContract
	err := c.db.QueryRowContext(ctx, query, contractID).Scan(
		&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
		&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
		&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
		&sc.ChestX, &sc.ChestY, &sc.ChestZ,
		&sc.SplitPlayerPercent, &sc.SplitClanPercent,
		&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying contract: %w", err)
	}
	return &sc, nil
}

// ListContractsForPlayer returns all contracts involving playerUUID, ordered newest first.
func (c *ContractsDB) ListContractsForPlayer(playerUUID string) ([]*SharecroppingContract, error) {
	return c.ListContractsForPlayerContext(context.Background(), playerUUID)
}

// ListContractsForPlayerContext returns all contracts for playerUUID with context.
func (c *ContractsDB) ListContractsForPlayerContext(ctx context.Context, playerUUID string) ([]*SharecroppingContract, error) {
	if playerUUID == "" {
		return nil, errors.New("player_uuid cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		WHERE player_uuid = ?
		ORDER BY created_tick DESC, contract_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, playerUUID)
	if err != nil {
		return nil, fmt.Errorf("querying contracts for player: %w", err)
	}
	defer rows.Close()

	var contracts []*SharecroppingContract
	for rows.Next() {
		var sc SharecroppingContract
		if err := rows.Scan(
			&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
			&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
			&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
			&sc.ChestX, &sc.ChestY, &sc.ChestZ,
			&sc.SplitPlayerPercent, &sc.SplitClanPercent,
			&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
		); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		contracts = append(contracts, &sc)
	}
	return contracts, rows.Err()
}

// ListContractsForAgent returns all contracts managed by settlerAgentID, ordered newest first.
func (c *ContractsDB) ListContractsForAgent(agentID string) ([]*SharecroppingContract, error) {
	return c.ListContractsForAgentContext(context.Background(), agentID)
}

// ListContractsForAgentContext returns all contracts managed by settlerAgentID with context.
func (c *ContractsDB) ListContractsForAgentContext(ctx context.Context, agentID string) ([]*SharecroppingContract, error) {
	if agentID == "" {
		return nil, errors.New("agent_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		WHERE settler_agent_id = ?
		ORDER BY created_tick DESC, contract_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, fmt.Errorf("querying contracts for agent: %w", err)
	}
	defer rows.Close()

	var contracts []*SharecroppingContract
	for rows.Next() {
		var sc SharecroppingContract
		if err := rows.Scan(
			&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
			&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
			&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
			&sc.ChestX, &sc.ChestY, &sc.ChestZ,
			&sc.SplitPlayerPercent, &sc.SplitClanPercent,
			&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
		); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		contracts = append(contracts, &sc)
	}
	return contracts, rows.Err()
}

// ListContracts returns all registered contracts.
func (c *ContractsDB) ListContracts() ([]*SharecroppingContract, error) {
	return c.ListContractsContext(context.Background())
}

// ListContractsByStatus returns all contracts matching status, ordered newest first.
func (c *ContractsDB) ListContractsByStatus(status string) ([]*SharecroppingContract, error) {
	return c.ListContractsByStatusContext(context.Background(), status)
}

// ListContractsByStatusContext returns all contracts matching status with context.
func (c *ContractsDB) ListContractsByStatusContext(ctx context.Context, status string) ([]*SharecroppingContract, error) {
	if status == "" {
		return nil, errors.New("status cannot be empty")
	}
	if !IsValidContractStatus(status) {
		return nil, fmt.Errorf("invalid contract status: %q", status)
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		WHERE status = ?
		ORDER BY created_tick DESC, contract_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, status)
	if err != nil {
		return nil, fmt.Errorf("querying contracts by status: %w", err)
	}
	defer rows.Close()

	var contracts []*SharecroppingContract
	for rows.Next() {
		var sc SharecroppingContract
		if err := rows.Scan(
			&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
			&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
			&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
			&sc.ChestX, &sc.ChestY, &sc.ChestZ,
			&sc.SplitPlayerPercent, &sc.SplitClanPercent,
			&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
		); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		contracts = append(contracts, &sc)
	}
	return contracts, rows.Err()
}

// ListActiveContractsForAgent returns all active contracts for agentID, ordered newest first.
func (c *ContractsDB) ListActiveContractsForAgent(agentID string) ([]*SharecroppingContract, error) {
	return c.ListActiveContractsForAgentContext(context.Background(), agentID)
}

// ListActiveContractsForAgentContext returns all active contracts for agentID with context.
func (c *ContractsDB) ListActiveContractsForAgentContext(ctx context.Context, agentID string) ([]*SharecroppingContract, error) {
	if agentID == "" {
		return nil, errors.New("agent_id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		WHERE settler_agent_id = ? AND status = ?
		ORDER BY created_tick DESC, contract_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query, agentID, ContractStatusActive)
	if err != nil {
		return nil, fmt.Errorf("querying active contracts for agent: %w", err)
	}
	defer rows.Close()

	var contracts []*SharecroppingContract
	for rows.Next() {
		var sc SharecroppingContract
		if err := rows.Scan(
			&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
			&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
			&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
			&sc.ChestX, &sc.ChestY, &sc.ChestZ,
			&sc.SplitPlayerPercent, &sc.SplitClanPercent,
			&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
		); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		contracts = append(contracts, &sc)
	}
	return contracts, rows.Err()
}

// ListContractsContext returns all registered contracts with context.
func (c *ContractsDB) ListContractsContext(ctx context.Context) ([]*SharecroppingContract, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT contract_id, player_uuid, player_name, settler_agent_id,
		       plot_min_x, plot_min_y, plot_min_z,
		       plot_max_x, plot_max_y, plot_max_z,
		       chest_x, chest_y, chest_z,
		       split_player_percent, split_clan_percent,
		       crop_type, status, created_tick, expires_tick
		FROM sharecropping_contracts
		ORDER BY created_tick DESC, contract_id ASC;
	`
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing contracts: %w", err)
	}
	defer rows.Close()

	var contracts []*SharecroppingContract
	for rows.Next() {
		var sc SharecroppingContract
		if err := rows.Scan(
			&sc.ContractID, &sc.PlayerUUID, &sc.PlayerName, &sc.SettlerAgentID,
			&sc.PlotMinX, &sc.PlotMinY, &sc.PlotMinZ,
			&sc.PlotMaxX, &sc.PlotMaxY, &sc.PlotMaxZ,
			&sc.ChestX, &sc.ChestY, &sc.ChestZ,
			&sc.SplitPlayerPercent, &sc.SplitClanPercent,
			&sc.CropType, &sc.Status, &sc.CreatedTick, &sc.ExpiresTick,
		); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		contracts = append(contracts, &sc)
	}
	return contracts, rows.Err()
}
