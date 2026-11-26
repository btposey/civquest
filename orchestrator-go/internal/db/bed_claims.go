package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Bed ownership (cq-390.1/.2): one row per claimed bed block, keyed by world and position, so
// the first agent to claim a bed owns it and every other agent sees the owner and goes to
// build or craft its own. Claims never cross worlds (cq-390.5): coordinates only identify a
// block within the world the handle is scoped to.

// SetWorldID scopes bed claims to one world (the server instance identifier). Call it before
// the handle is shared.
func (c *CivicRegistryDB) SetWorldID(worldID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.worldID = worldID
}

// WorldID returns the world the handle's bed claims are scoped to.
func (c *CivicRegistryDB) WorldID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.worldID
}

// ClaimBed atomically claims the bed at (x,y,z) for agentID unless another agent already
// owns it. It returns the owner after the call: agentID on success (or when already held by
// agentID), otherwise the existing owner.
func (c *CivicRegistryDB) ClaimBed(x, y, z int32, agentID string, worldTick int64) (string, error) {
	if agentID == "" {
		return "", errors.New("agent_id cannot be empty")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return "", ErrClosed
	}
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO world_bed_claims (world_id, x, y, z, owner_agent_id, claimed_tick) VALUES (?, ?, ?, ?, ?, ?)`,
		c.WorldID(), x, y, z, agentID, worldTick); err != nil {
		return "", fmt.Errorf("claiming bed: %w", err)
	}
	return c.bedOwnerLocked(ctx, x, y, z)
}

// BedOwner returns the owner of the bed at (x,y,z), or "" when unclaimed.
func (c *CivicRegistryDB) BedOwner(x, y, z int32) (string, error) {
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return "", ErrClosed
	}
	return c.bedOwnerLocked(context.Background(), x, y, z)
}

func (c *CivicRegistryDB) bedOwnerLocked(ctx context.Context, x, y, z int32) (string, error) {
	var owner string
	err := c.db.QueryRowContext(ctx,
		`SELECT owner_agent_id FROM world_bed_claims WHERE world_id = ? AND x = ? AND y = ? AND z = ?`,
		c.WorldID(), x, y, z).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading bed owner: %w", err)
	}
	return owner, nil
}

// ReleaseBed drops agentID's claim on the bed at (x,y,z) (no-op for another owner's bed).
func (c *CivicRegistryDB) ReleaseBed(x, y, z int32, agentID string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	_, err := c.db.ExecContext(context.Background(),
		`DELETE FROM world_bed_claims WHERE world_id = ? AND x = ? AND y = ? AND z = ? AND owner_agent_id = ?`,
		c.WorldID(), x, y, z, agentID)
	return err
}

// ReleaseBedsOwnedBy drops every claim agentID holds in this world (permadeath).
func (c *CivicRegistryDB) ReleaseBedsOwnedBy(agentID string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	_, err := c.db.ExecContext(context.Background(),
		`DELETE FROM world_bed_claims WHERE world_id = ? AND owner_agent_id = ?`, c.WorldID(), agentID)
	return err
}

// BedOwnedBy returns the bed positions owned by agentID.
func (c *CivicRegistryDB) BedOwnedBy(agentID string) ([][3]int32, error) {
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}
	rows, err := c.db.QueryContext(context.Background(),
		`SELECT x, y, z FROM world_bed_claims WHERE world_id = ? AND owner_agent_id = ? ORDER BY claimed_tick, x, y, z`,
		c.WorldID(), agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][3]int32
	for rows.Next() {
		var p [3]int32
		if err := rows.Scan(&p[0], &p[1], &p[2]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
