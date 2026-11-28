package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Cabin plots (cq-3ru): one rectangle of ground per agent in a world, never overlapping
// another agent's. A plot carries its own size, so cabins of other footprints fit the same
// table.

// Plot is a square of ground: its centre and the distance from it to each side.
type Plot struct{ X, Z, Half int32 }

// Overlaps reports that the two squares share ground.
func (a Plot) Overlaps(b Plot) bool {
	apart := func(p, q int32) bool { d := p - q; return d > a.Half+b.Half || -d > a.Half+b.Half }
	return !apart(a.X, b.X) && !apart(a.Z, b.Z)
}

// ClaimPlot gives agentID a plot out of candidates, which are in order of preference: the one
// it holds if that is among them, else the first that overlaps no other agent's, replacing what
// it held. ok is false when none is free; the agent then keeps what it had.
func (c *CivicRegistryDB) ClaimPlot(agentID string, candidates []Plot, worldTick int64) (Plot, bool, error) {
	if agentID == "" {
		return Plot{}, false, errors.New("agent_id cannot be empty")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	closed := c.isClosed
	c.mu.RUnlock()
	if closed {
		return Plot{}, false, ErrClosed
	}
	ctx := context.Background()
	rows, err := c.db.QueryContext(ctx,
		`SELECT owner_agent_id, x, z, half FROM world_plot_claims WHERE world_id = ?`, c.WorldID())
	if err != nil {
		return Plot{}, false, fmt.Errorf("reading plots: %w", err)
	}
	var others []Plot
	for rows.Next() {
		var owner string
		var p Plot
		if err := rows.Scan(&owner, &p.X, &p.Z, &p.Half); err != nil {
			rows.Close()
			return Plot{}, false, fmt.Errorf("reading plots: %w", err)
		}
		if owner != agentID {
			others = append(others, p)
		} else if slices.Contains(candidates, p) {
			rows.Close()
			return p, true, nil
		}
	}
	if err := rows.Close(); err != nil {
		return Plot{}, false, fmt.Errorf("reading plots: %w", err)
	}
	for _, p := range candidates {
		if slices.ContainsFunc(others, p.Overlaps) {
			continue
		}
		// The insert checks again in the same statement, so two processes cannot both take it.
		res, err := c.db.ExecContext(ctx, `
			INSERT OR REPLACE INTO world_plot_claims (world_id, owner_agent_id, x, z, half, claimed_tick)
			SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM world_plot_claims WHERE world_id = ? AND owner_agent_id <> ?
					AND ABS(x - ?) <= half + ? AND ABS(z - ?) <= half + ?)`,
			c.WorldID(), agentID, p.X, p.Z, p.Half, worldTick,
			c.WorldID(), agentID, p.X, p.Half, p.Z, p.Half)
		if err != nil {
			return Plot{}, false, fmt.Errorf("claiming plot: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			return p, true, nil
		}
	}
	return Plot{}, false, nil
}
