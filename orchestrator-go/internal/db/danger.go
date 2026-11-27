package db

// Mob dangers shared by a clan (cq-gey.2).

import (
	"context"
	"fmt"
)

// DangerSpot is a place where a clan member met a dangerous mob.
type DangerSpot struct {
	Kind       string
	X, Y, Z    float64
	SeenTick   int64
	ReportedBy string
}

// PublishDanger records d, replacing older reports of the same kind within mergeRadius.
func (c *CivicRegistryDB) PublishDanger(ctx context.Context, d DangerSpot, mergeRadius float64) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return ErrClosed
	}
	const near = `world_id = ? AND kind = ? AND (x - ?) * (x - ?) + (z - ?) * (z - ?) <= ?`
	args := []any{c.worldID, d.Kind, d.X, d.X, d.Z, d.Z, mergeRadius * mergeRadius}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM danger_spots WHERE `+near+` AND seen_tick <= ?;`, append(args, d.SeenTick)...); err != nil {
		return fmt.Errorf("replacing danger spot: %w", err)
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO danger_spots (world_id, kind, x, y, z, seen_tick, reported_by)
		SELECT ?, ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM danger_spots WHERE `+near+`);`,
		append([]any{c.worldID, d.Kind, d.X, d.Y, d.Z, d.SeenTick, d.ReportedBy}, args...)...)
	if err != nil {
		return fmt.Errorf("recording danger spot: %w", err)
	}
	return nil
}

// ListDangers returns the clan's dangers seen at or after sinceTick.
func (c *CivicRegistryDB) ListDangers(ctx context.Context, sinceTick int64) ([]DangerSpot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT kind, x, y, z, seen_tick, reported_by FROM danger_spots WHERE world_id = ? AND seen_tick >= ?;`,
		c.worldID, sinceTick)
	if err != nil {
		return nil, fmt.Errorf("listing danger spots: %w", err)
	}
	defer rows.Close()
	var out []DangerSpot
	for rows.Next() {
		var d DangerSpot
		if err := rows.Scan(&d.Kind, &d.X, &d.Y, &d.Z, &d.SeenTick, &d.ReportedBy); err != nil {
			return nil, fmt.Errorf("scanning danger spot: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
