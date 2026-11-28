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
	// FirstSeenTick is when this danger was first met, kept across the reports that replace it.
	FirstSeenTick int64
}

// PublishDanger records d, replacing older reports of the same kind within mergeRadius. A
// report replaced within memory ticks of d hands on when the danger was first seen.
func (c *CivicRegistryDB) PublishDanger(ctx context.Context, d DangerSpot, mergeRadius float64, memory int64) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return ErrClosed
	}
	const near = `world_id = ? AND kind = ? AND (x - ?) * (x - ?) + (z - ?) * (z - ?) <= ?`
	args := []any{c.worldID, d.Kind, d.X, d.X, d.Z, d.Z, mergeRadius * mergeRadius}
	first := d.SeenTick
	if err := c.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(CASE WHEN first_seen_tick > 0 THEN first_seen_tick ELSE seen_tick END), ?) FROM danger_spots WHERE `+near+` AND seen_tick > ? AND seen_tick <= ?;`,
		append(append([]any{d.SeenTick}, args...), d.SeenTick-memory, d.SeenTick)...).Scan(&first); err != nil {
		return fmt.Errorf("reading danger history: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM danger_spots WHERE `+near+` AND seen_tick <= ?;`, append(args, d.SeenTick)...); err != nil {
		return fmt.Errorf("replacing danger spot: %w", err)
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO danger_spots (world_id, kind, x, y, z, seen_tick, reported_by, first_seen_tick)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM danger_spots WHERE `+near+`);`,
		append([]any{c.worldID, d.Kind, d.X, d.Y, d.Z, d.SeenTick, d.ReportedBy, first}, args...)...)
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
		SELECT kind, x, y, z, seen_tick, reported_by,
		       CASE WHEN first_seen_tick > 0 THEN first_seen_tick ELSE seen_tick END FROM danger_spots WHERE world_id = ? AND seen_tick >= ?;`,
		c.worldID, sinceTick)
	if err != nil {
		return nil, fmt.Errorf("listing danger spots: %w", err)
	}
	defer rows.Close()
	var out []DangerSpot
	for rows.Next() {
		var d DangerSpot
		if err := rows.Scan(&d.Kind, &d.X, &d.Y, &d.Z, &d.SeenTick, &d.ReportedBy, &d.FirstSeenTick); err != nil {
			return nil, fmt.Errorf("scanning danger spot: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
