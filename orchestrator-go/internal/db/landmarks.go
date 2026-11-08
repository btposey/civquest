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

// Landmark category constants.
const (
	LandmarkCategoryNatural    = "NATURAL"
	LandmarkCategorySettlement = "SETTLEMENT"
	LandmarkCategoryRuins      = "RUINS"
	LandmarkCategoryResource   = "RESOURCE"
	LandmarkCategoryMemorial   = "MEMORIAL"
	LandmarkCategoryMonument   = "MONUMENT"
)

// IsValidLandmarkCategory returns true if category is one of the recognized landmark categories.
func IsValidLandmarkCategory(cat string) bool {
	switch cat {
	case LandmarkCategoryNatural, LandmarkCategorySettlement, LandmarkCategoryRuins,
		LandmarkCategoryResource, LandmarkCategoryMemorial, LandmarkCategoryMonument:
		return true
	default:
		return false
	}
}

// LandmarksDB manages the SQLite database for cultural toponyms, geographical
// coordinates, signpost engravings, and historical lore in
// $XDG_DATA_HOME/civquest/clans/{clan_id}/landmarks.db.
type LandmarksDB struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	clanID   string
	dbPath   string
	db       *sql.DB
	isClosed bool
}

// OpenLandmarksDB opens and migrates landmarks.db at dbPath.
func OpenLandmarksDB(dbPath, clanID string) (*LandmarksDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create clan landmarks directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open landmarks database %s: %w", dbPath, err)
	}

	dbConn.SetMaxOpenConns(10)
	dbConn.SetMaxIdleConns(5)

	l := &LandmarksDB{
		clanID: clanID,
		dbPath: dbPath,
		db:     dbConn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := l.migrate(ctx); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("migrating landmarks.db: %w", err)
	}

	return l, nil
}

// OpenLandmarksDBForClan resolves the path using the provided XDG resolver.
func OpenLandmarksDBForClan(resolver *xdg.Resolver, clanID string) (*LandmarksDB, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}
	dbPath := resolver.ClanLandmarksDBPath(clanID)
	return OpenLandmarksDB(dbPath, clanID)
}

func (l *LandmarksDB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS landmarks (
		landmark_id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		x REAL NOT NULL,
		y REAL NOT NULL,
		z REAL NOT NULL,
		signpost_line1 TEXT NOT NULL DEFAULT '',
		signpost_line2 TEXT NOT NULL DEFAULT '',
		lore_text TEXT NOT NULL DEFAULT '',
		discovered_by_agent_id TEXT NOT NULL,
		discovered_tick INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_landmarks_category ON landmarks(category);
	CREATE INDEX IF NOT EXISTS idx_landmarks_coords ON landmarks(x, z);
	`
	_, err := l.db.ExecContext(ctx, schema)
	return err
}

// ClanID returns the clan identifier.
func (l *LandmarksDB) ClanID() string {
	return l.clanID
}

// Path returns the database filesystem path.
func (l *LandmarksDB) Path() string {
	return l.dbPath
}

// DB returns the underlying *sql.DB handle.
func (l *LandmarksDB) DB() *sql.DB {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.db
}

// Close closes the database connection.
func (l *LandmarksDB) Close() error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.isClosed {
		return nil
	}
	l.isClosed = true
	return l.db.Close()
}

// RegisterLandmark registers a new landmark.
func (l *LandmarksDB) RegisterLandmark(landmark *Landmark) error {
	return l.RegisterLandmarkContext(context.Background(), landmark)
}

// RegisterLandmarkContext registers a new landmark with context.
func (l *LandmarksDB) RegisterLandmarkContext(ctx context.Context, landmark *Landmark) error {
	l.mu.RLock()
	if l.isClosed {
		l.mu.RUnlock()
		return ErrClosed
	}
	l.mu.RUnlock()

	if landmark == nil {
		return errors.New("landmark cannot be nil")
	}
	if landmark.LandmarkID == "" {
		return errors.New("landmark_id cannot be empty")
	}
	if landmark.Name == "" {
		return errors.New("name cannot be empty")
	}
	if landmark.Category == "" {
		return errors.New("category cannot be empty")
	}
	if !IsValidLandmarkCategory(landmark.Category) {
		return fmt.Errorf("invalid landmark category: %q", landmark.Category)
	}
	if landmark.DiscoveredByAgentID == "" {
		return errors.New("discovered_by_agent_id cannot be empty")
	}
	if math.IsNaN(landmark.X) || math.IsInf(landmark.X, 0) ||
		math.IsNaN(landmark.Y) || math.IsInf(landmark.Y, 0) ||
		math.IsNaN(landmark.Z) || math.IsInf(landmark.Z, 0) {
		return errors.New("coordinates must be finite numbers")
	}

	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	l.mu.RLock()
	if l.isClosed {
		l.mu.RUnlock()
		return ErrClosed
	}
	l.mu.RUnlock()

	query := `
		INSERT INTO landmarks (
			landmark_id, name, category, x, y, z,
			signpost_line1, signpost_line2, lore_text,
			discovered_by_agent_id, discovered_tick
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := l.db.ExecContext(ctx, query,
		landmark.LandmarkID, landmark.Name, landmark.Category,
		landmark.X, landmark.Y, landmark.Z,
		landmark.SignpostLine1, landmark.SignpostLine2, landmark.LoreText,
		landmark.DiscoveredByAgentID, landmark.DiscoveredTick,
	)
	if err != nil {
		return fmt.Errorf("registering landmark: %w", err)
	}
	return nil
}

// GetLandmark retrieves a landmark by landmarkID. Returns nil, nil if not found.
func (l *LandmarksDB) GetLandmark(landmarkID string) (*Landmark, error) {
	return l.GetLandmarkContext(context.Background(), landmarkID)
}

// GetLandmarkContext retrieves a landmark by landmarkID with context.
func (l *LandmarksDB) GetLandmarkContext(ctx context.Context, landmarkID string) (*Landmark, error) {
	if landmarkID == "" {
		return nil, errors.New("landmark_id cannot be empty")
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT landmark_id, name, category, x, y, z,
		       signpost_line1, signpost_line2, lore_text,
		       discovered_by_agent_id, discovered_tick
		FROM landmarks
		WHERE landmark_id = ?;
	`
	var lm Landmark
	err := l.db.QueryRowContext(ctx, query, landmarkID).Scan(
		&lm.LandmarkID, &lm.Name, &lm.Category,
		&lm.X, &lm.Y, &lm.Z,
		&lm.SignpostLine1, &lm.SignpostLine2, &lm.LoreText,
		&lm.DiscoveredByAgentID, &lm.DiscoveredTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying landmark: %w", err)
	}
	return &lm, nil
}

// FindNearestLandmark finds the landmark closest to coordinates (x, y, z), optionally filtered by category.
// If category is empty (""), all categories are considered. Returns nil, nil if no landmarks match.
func (l *LandmarksDB) FindNearestLandmark(x, y, z float64, category string) (*Landmark, error) {
	return l.FindNearestLandmarkContext(context.Background(), x, y, z, category)
}

// FindNearestLandmarkContext finds the closest landmark with context.
func (l *LandmarksDB) FindNearestLandmarkContext(ctx context.Context, x, y, z float64, category string) (*Landmark, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) ||
		math.IsNaN(y) || math.IsInf(y, 0) ||
		math.IsNaN(z) || math.IsInf(z, 0) {
		return nil, errors.New("coordinates must be finite numbers")
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.isClosed {
		return nil, ErrClosed
	}

	var row *sql.Row
	if category != "" {
		query := `
			SELECT landmark_id, name, category, x, y, z,
			       signpost_line1, signpost_line2, lore_text,
			       discovered_by_agent_id, discovered_tick,
			       ((x - ?) * (x - ?) + (y - ?) * (y - ?) + (z - ?) * (z - ?)) AS dist_sq
			FROM landmarks
			WHERE category = ?
			ORDER BY dist_sq ASC
			LIMIT 1;
		`
		row = l.db.QueryRowContext(ctx, query, x, x, y, y, z, z, category)
	} else {
		query := `
			SELECT landmark_id, name, category, x, y, z,
			       signpost_line1, signpost_line2, lore_text,
			       discovered_by_agent_id, discovered_tick,
			       ((x - ?) * (x - ?) + (y - ?) * (y - ?) + (z - ?) * (z - ?)) AS dist_sq
			FROM landmarks
			ORDER BY dist_sq ASC
			LIMIT 1;
		`
		row = l.db.QueryRowContext(ctx, query, x, x, y, y, z, z)
	}

	var lm Landmark
	var distSq float64
	err := row.Scan(
		&lm.LandmarkID, &lm.Name, &lm.Category,
		&lm.X, &lm.Y, &lm.Z,
		&lm.SignpostLine1, &lm.SignpostLine2, &lm.LoreText,
		&lm.DiscoveredByAgentID, &lm.DiscoveredTick,
		&distSq,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding nearest landmark: %w", err)
	}
	return &lm, nil
}

// ListLandmarks returns all registered landmarks ordered alphabetically by name.
func (l *LandmarksDB) ListLandmarks() ([]*Landmark, error) {
	return l.ListLandmarksContext(context.Background())
}

// ListLandmarksContext returns all registered landmarks with context.
func (l *LandmarksDB) ListLandmarksContext(ctx context.Context) ([]*Landmark, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT landmark_id, name, category, x, y, z,
		       signpost_line1, signpost_line2, lore_text,
		       discovered_by_agent_id, discovered_tick
		FROM landmarks
		ORDER BY name ASC, landmark_id ASC;
	`
	rows, err := l.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing landmarks: %w", err)
	}
	defer rows.Close()

	var landmarks []*Landmark
	for rows.Next() {
		var lm Landmark
		if err := rows.Scan(
			&lm.LandmarkID, &lm.Name, &lm.Category,
			&lm.X, &lm.Y, &lm.Z,
			&lm.SignpostLine1, &lm.SignpostLine2, &lm.LoreText,
			&lm.DiscoveredByAgentID, &lm.DiscoveredTick,
		); err != nil {
			return nil, fmt.Errorf("scanning landmark row: %w", err)
		}
		landmarks = append(landmarks, &lm)
	}
	return landmarks, rows.Err()
}
