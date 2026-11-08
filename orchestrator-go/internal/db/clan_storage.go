package db

import (
	"errors"
	"fmt"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// ClanStorage bundles all SQLite databases belonging to a specific clan:
// civic_registry.db, reputation.db, diplomacy.db, landmarks.db, and contracts.db.
type ClanStorage struct {
	mu            sync.Mutex
	clanID        string
	isClosed      bool
	CivicRegistry *CivicRegistryDB
	Reputation    *ReputationDB
	Diplomacy     *DiplomacyDB
	Landmarks     *LandmarksDB
	Contracts     *ContractsDB
}

// OpenClanStorage initializes directories and opens all 5 clan SQLite databases.
// If any database fails to open or migrate, any previously opened databases are
// immediately closed to prevent descriptor leaks.
func OpenClanStorage(resolver *xdg.Resolver, clanID string) (*ClanStorage, error) {
	if err := xdg.ValidateClanID(clanID); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}

	if err := resolver.InitClanDirectories(clanID); err != nil {
		return nil, fmt.Errorf("initializing clan directories for %s: %w", clanID, err)
	}

	storage := &ClanStorage{
		clanID: clanID,
	}

	// 1. Civic Registry DB
	civic, err := OpenCivicRegistryDBForClan(resolver, clanID)
	if err != nil {
		return nil, fmt.Errorf("opening civic registry DB: %w", err)
	}
	storage.CivicRegistry = civic

	// Helper for cleanup on partial failure
	cleanup := func(cause error) (*ClanStorage, error) {
		return nil, errors.Join(cause, storage.Close())
	}

	// 2. Reputation DB
	rep, err := OpenReputationDBForClan(resolver, clanID)
	if err != nil {
		return cleanup(fmt.Errorf("opening reputation DB: %w", err))
	}
	storage.Reputation = rep

	// 3. Diplomacy DB
	diplo, err := OpenDiplomacyDBForClan(resolver, clanID)
	if err != nil {
		return cleanup(fmt.Errorf("opening diplomacy DB: %w", err))
	}
	storage.Diplomacy = diplo

	// 4. Landmarks DB
	landmarks, err := OpenLandmarksDBForClan(resolver, clanID)
	if err != nil {
		return cleanup(fmt.Errorf("opening landmarks DB: %w", err))
	}
	storage.Landmarks = landmarks

	// 5. Contracts DB
	contracts, err := OpenContractsDBForClan(resolver, clanID)
	if err != nil {
		return cleanup(fmt.Errorf("opening contracts DB: %w", err))
	}
	storage.Contracts = contracts

	return storage, nil
}

// ClanID returns the clan identifier.
func (c *ClanStorage) ClanID() string {
	return c.clanID
}

// Close gracefully closes all clan databases and joins any encountered errors.
func (c *ClanStorage) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return nil
	}
	c.isClosed = true

	var errs []error

	if c.CivicRegistry != nil {
		if err := c.CivicRegistry.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing civic_registry.db: %w", err))
		}
	}
	if c.Reputation != nil {
		if err := c.Reputation.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing reputation.db: %w", err))
		}
	}
	if c.Diplomacy != nil {
		if err := c.Diplomacy.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing diplomacy.db: %w", err))
		}
	}
	if c.Landmarks != nil {
		if err := c.Landmarks.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing landmarks.db: %w", err))
		}
	}
	if c.Contracts != nil {
		if err := c.Contracts.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing contracts.db: %w", err))
		}
	}

	return errors.Join(errs...)
}
