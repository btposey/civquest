package db

// Settlement records (cq-6l3.14). An agent remembers the settlement it belongs to in its own
// map database; a clan records the one site it selected in its civic registry. Both live under
// the world's data directory, so a reset world starts without them.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SettlementClaim is the settlement an agent belongs to.
type SettlementClaim struct {
	Kind    string // "HOMESTEAD" (a lone agent's own) or "CLAN_SITE" (chosen by its clan)
	X, Y, Z float64
}

// SaveSettlementClaim stores the agent's settlement, replacing any earlier one.
func (a *AgentDB) SaveSettlementClaim(ctx context.Context, c SettlementClaim) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}
	_, err := a.mapDB.ExecContext(ctx, `
		INSERT INTO settlement_claim (id, kind, x, y, z) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, x = excluded.x, y = excluded.y, z = excluded.z;`,
		c.Kind, c.X, c.Y, c.Z)
	if err != nil {
		return fmt.Errorf("saving settlement claim: %w", err)
	}
	return nil
}

// LoadSettlementClaim returns the stored settlement, or nil when there is none.
func (a *AgentDB) LoadSettlementClaim(ctx context.Context) (*SettlementClaim, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}
	var c SettlementClaim
	err := a.mapDB.QueryRowContext(ctx, `SELECT kind, x, y, z FROM settlement_claim WHERE id = 1;`).
		Scan(&c.Kind, &c.X, &c.Y, &c.Z)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading settlement claim: %w", err)
	}
	return &c, nil
}

// StrategyRow is the strategic machine an agent was in.
type StrategyRow struct {
	Kind, Resume string
	Since        int64
	Chosen       bool
}

// SaveStrategy stores the agent's strategy, replacing the earlier one.
func (a *AgentDB) SaveStrategy(ctx context.Context, s StrategyRow) error {
	a.mapWriteMu.Lock()
	defer a.mapWriteMu.Unlock()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return ErrClosed
	}
	_, err := a.mapDB.ExecContext(ctx, `
		INSERT INTO strategy_state (id, kind, resume, since, chosen) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, resume = excluded.resume, since = excluded.since, chosen = excluded.chosen;`,
		s.Kind, s.Resume, s.Since, s.Chosen)
	if err != nil {
		return fmt.Errorf("saving strategy: %w", err)
	}
	return nil
}

// LoadStrategy returns the stored strategy, or nil when there is none.
func (a *AgentDB) LoadStrategy(ctx context.Context) (*StrategyRow, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isClosed {
		return nil, ErrClosed
	}
	var s StrategyRow
	err := a.mapDB.QueryRowContext(ctx, `SELECT kind, resume, since, chosen FROM strategy_state WHERE id = 1;`).
		Scan(&s.Kind, &s.Resume, &s.Since, &s.Chosen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading strategy: %w", err)
	}
	return &s, nil
}

// ClanSite is a site a clan selected for a settlement.
type ClanSite struct {
	X, Y, Z    float64
	Score      float64
	ChosenBy   string
	ChosenTick int64
}

// ClaimClanSite records site as the clan's home unless it has one, and returns the
// selection that stands: the first claim wins, so every member settles in the same place.
// created reports whether this call made it.
func (c *CivicRegistryDB) ClaimClanSite(ctx context.Context, clanID string, site ClanSite) (standing ClanSite, created bool, err error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return ClanSite{}, false, ErrClosed
	}
	res, err := c.db.ExecContext(ctx, `
		INSERT INTO clan_sites (world_id, clan_id, site_no, x, y, z, score, chosen_by, chosen_tick) VALUES (?, ?, 0, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(world_id, clan_id, site_no) DO NOTHING;`,
		c.worldID, clanID, site.X, site.Y, site.Z, site.Score, site.ChosenBy, site.ChosenTick)
	if err != nil {
		return ClanSite{}, false, fmt.Errorf("claiming clan site: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ClanSite{}, false, fmt.Errorf("claiming clan site: %w", err)
	}
	got, err := c.clanSiteLocked(ctx, clanID)
	if err != nil {
		return ClanSite{}, false, err
	}
	if got == nil {
		return ClanSite{}, false, fmt.Errorf("claiming clan site: no row for clan %q", clanID)
	}
	return *got, n > 0, nil
}

// GetClanSite returns the clan's home site, or nil when it has not chosen one.
func (c *CivicRegistryDB) GetClanSite(ctx context.Context, clanID string) (*ClanSite, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}
	return c.clanSiteLocked(ctx, clanID)
}

func (c *CivicRegistryDB) clanSiteLocked(ctx context.Context, clanID string) (*ClanSite, error) {
	var s ClanSite
	err := c.db.QueryRowContext(ctx,
		`SELECT x, y, z, score, chosen_by, chosen_tick FROM clan_sites WHERE world_id = ? AND clan_id = ? AND site_no = 0;`, c.worldID, clanID).
		Scan(&s.X, &s.Y, &s.Z, &s.Score, &s.ChosenBy, &s.ChosenTick)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading clan site: %w", err)
	}
	return &s, nil
}
