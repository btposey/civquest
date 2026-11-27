package db

// Settlement site scoring (cq-6l3.20, cq-gey.3). Water weighs most; wood, ore or a cave
// entrance, and livestock each count in a site's favour; open buildable ground helps, while
// ridges and blocked sight lines count against it. Ice is harsh country: it counts heavily
// against a site, and water beside it counts for nothing. Berries are chance finds and no factor.

import (
	"context"
	"fmt"
	"math"
	"strings"
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
	Ice                                                      float64
	Ore, Cave, Livestock                                     bool
}

// DESIGN BUDGET (cq-6l3.20): weights are first guesses to tune in play.
const (
	siteWaterBonus     = 20.0
	siteWoodBonus      = 8.0
	siteMineralBonus   = 12.0
	siteLivestockBonus = 10.0
	siteIcePenalty     = 40.0
	siteIceWeight      = 2.0
)

// FeatureIce marks a survey sector of ice or frozen ground.
const FeatureIce = "ICE"

// SiteFeatureOf is the feature a survey sector counts as when judging a site: in frozen country
// the open ground and water are ice.
func SiteFeatureOf(feature string, frozen bool) string {
	f := strings.ToUpper(strings.TrimSpace(feature))
	if frozen && (f == "WATER" || f == "OPEN_PLAINS") {
		return FeatureIce
	}
	return f
}

// ScoreSite rates a candidate settlement site; higher is better.
func ScoreSite(f SiteFeatures) float64 {
	icy := f.Ice >= 1
	score := math.Min(f.Forest, 10)*2 +
		math.Min(f.Plains, 12)*1.5 +
		math.Min(f.Stone, 6) +
		math.Min(f.Structure, 4)*1.5 -
		f.Ridge -
		f.Occluded*0.5
	switch {
	case icy:
		score -= siteIcePenalty + math.Min(f.Ice, 12)*siteIceWeight
	case f.Water >= 1:
		score += math.Min(f.Water, 6)*5 + siteWaterBonus
	default:
		score += f.Water * 5
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
