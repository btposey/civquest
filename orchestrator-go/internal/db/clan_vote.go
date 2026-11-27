package db

// The clan commits to its settlement together (cq-gey.3): members report in while they live,
// each endorses the site it judges best, and the site is claimed once a majority agree.

import (
	"context"
	"fmt"
)

// MemberHeartbeat records that agentID of clanID was alive at tick.
func (c *CivicRegistryDB) MemberHeartbeat(ctx context.Context, clanID, agentID string, tick int64) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return ErrClosed
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO clan_members (world_id, clan_id, agent_id, last_seen_tick) VALUES (?, ?, ?, ?)
		ON CONFLICT(world_id, clan_id, agent_id) DO UPDATE SET last_seen_tick = excluded.last_seen_tick;`,
		c.worldID, clanID, agentID, tick)
	if err != nil {
		return fmt.Errorf("recording clan member: %w", err)
	}
	return nil
}

// VoteForSite records the site voter endorses now, replacing its earlier choice.
func (c *CivicRegistryDB) VoteForSite(ctx context.Context, clanID, voter string, site ClanSite) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return ErrClosed
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO clan_site_votes (world_id, clan_id, agent_id, x, z, voted_tick) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(world_id, clan_id, agent_id) DO UPDATE SET x = excluded.x, z = excluded.z, voted_tick = excluded.voted_tick;`,
		c.worldID, clanID, voter, site.X, site.Z, site.ChosenTick)
	if err != nil {
		return fmt.Errorf("recording site vote: %w", err)
	}
	return nil
}

// SiteSupport counts the clan's living members (seen at or after sinceTick) and how many of
// them endorse a site within radius of (x, z).
func (c *CivicRegistryDB) SiteSupport(ctx context.Context, clanID string, x, z, radius float64, sinceTick int64) (supporters, living int, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return 0, 0, ErrClosed
	}
	err = c.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN v.agent_id IS NOT NULL
		       AND (v.x - ?) * (v.x - ?) + (v.z - ?) * (v.z - ?) <= ? THEN 1 ELSE 0 END), 0)
		FROM clan_members m
		LEFT JOIN clan_site_votes v ON v.world_id = m.world_id AND v.clan_id = m.clan_id AND v.agent_id = m.agent_id
		WHERE m.world_id = ? AND m.clan_id = ? AND m.last_seen_tick >= ?;`,
		x, x, z, z, radius*radius, c.worldID, clanID, sinceTick).Scan(&living, &supporters)
	if err != nil {
		return 0, 0, fmt.Errorf("counting site support: %w", err)
	}
	return supporters, living, nil
}
