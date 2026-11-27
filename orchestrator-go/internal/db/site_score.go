package db

// Settlement site scoring (cq-6l3.20). Water weighs most; wood, ore or a cave entrance, and
// livestock each count in a site's favour; open buildable ground helps, while ridges and blocked
// sight lines count against it. Berries are chance finds and no factor.

import (
	"context"
	"fmt"
	"math"
)

// Block ids of the sightings that are not blocks of their own kind.
const (
	CaveEntranceBlockID = "civquest:cave_entrance"
	LivestockBlockID    = "civquest:livestock"
)

// SiteFactorRadius is how far from a site a sighting still speaks for it.
const SiteFactorRadius = 48.0

// SiteFeatures is what is known of a candidate site. The sector counts are horizon-survey
// averages per sweep; Ore, Cave and Livestock are sightings within SiteFactorRadius.
type SiteFeatures struct {
	Forest, Water, Plains, Stone, Structure, Ridge, Occluded float64
	Ore, Cave, Livestock                                     bool
}

// DESIGN BUDGET (cq-6l3.20): weights are first guesses to tune in play.
const (
	siteWaterBonus     = 20.0
	siteWoodBonus      = 8.0
	siteMineralBonus   = 12.0
	siteLivestockBonus = 10.0
)

// ScoreSite rates a candidate settlement site; higher is better.
func ScoreSite(f SiteFeatures) float64 {
	score := math.Min(f.Water, 6)*5 +
		math.Min(f.Forest, 10)*2 +
		math.Min(f.Plains, 12)*1.5 +
		math.Min(f.Stone, 6) +
		math.Min(f.Structure, 4)*1.5 -
		f.Ridge -
		f.Occluded*0.5
	if f.Water >= 1 {
		score += siteWaterBonus
	}
	if f.Forest >= 1.5 {
		score += siteWoodBonus
	}
	if f.Ore || f.Cave {
		score += siteMineralBonus
	}
	if f.Livestock {
		score += siteLivestockBonus
	}
	return math.Round(score*10) / 10
}

type siteSighting struct {
	blockID string
	x, z    float64
}

// siteSightingsLocked returns the clan's shared ore, cave and livestock sightings.
func (c *CivicRegistryDB) siteSightingsLocked(ctx context.Context) ([]siteSighting, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT block_id, x, z FROM settlement_pois
		WHERE is_active = 1 AND category = 'RESOURCE' AND (block_id = ? OR block_id = ? OR block_id LIKE '%!_ore' ESCAPE '!');`,
		CaveEntranceBlockID, LivestockBlockID)
	if err != nil {
		return nil, fmt.Errorf("querying site sightings: %w", err)
	}
	defer rows.Close()
	var out []siteSighting
	for rows.Next() {
		var s siteSighting
		if err := rows.Scan(&s.blockID, &s.x, &s.z); err != nil {
			return nil, fmt.Errorf("scanning site sighting: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
