package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// homesteadScore is what a lone agent asks of a site before claiming it (db.ScoreSite).
	homesteadScore           float64 = 35.0
	defaultSurveyNumSectors  int32   = 36
	defaultSurveyMaxRange    float32 = 64.0
	defaultSurveyTicksPerSec int32   = 2
	// expeditionSurveyTimeout is the survey frame's lease length in ticks.
	expeditionSurveyTimeout    int64   = 120
	expeditionNavCooldownTicks int64   = 40
	expeditionArrivalRadius    float64 = 6.0
	expeditionRimResurveyDist  float64 = 6.0
	expeditionFlankStepDist    float64 = 22.0
	expeditionRendezvousRadius float64 = 20.0
	expeditionResurveyDist     float64 = 24.0
	expeditionResurveyMaxAge   int64   = 2400
)

// normalizeBearingDeg normalizes a Minecraft yaw bearing into [0, 360).
func normalizeBearingDeg(bearing float32) float32 {
	mod := math.Mod(float64(bearing), 360.0)
	if mod < 0 {
		mod += 360.0
	}
	if mod >= 360.0 {
		mod = 0
	}
	return float32(mod)
}

// bearingToUnitVector converts a Minecraft native yaw bearing in [0, 360) to horizontal
// unit vector components: dx = -sin(rad(bearing)), dz = cos(rad(bearing)).
// Convention: 0° = +Z South, 90° = -X West, 180° = -Z North, 270° = +X East.
func bearingToUnitVector(bearingDeg float32) (dx, dz float64) {
	rad := float64(normalizeBearingDeg(bearingDeg)) * math.Pi / 180.0
	return -math.Sin(rad), math.Cos(rad)
}

// vectorToBearingDeg computes the Minecraft native yaw bearing in [0, 360) for vector (dx, dz).
func vectorToBearingDeg(dx, dz float64) float32 {
	if dx == 0 && dz == 0 {
		return 0
	}
	deg := math.Atan2(-dx, dz) * 180.0 / math.Pi
	return normalizeBearingDeg(float32(deg))
}

// angularDiffDeg returns the shortest absolute angular distance in degrees [0, 180].
func angularDiffDeg(a, b float32) float64 {
	diff := math.Abs(float64(normalizeBearingDeg(a)) - float64(normalizeBearingDeg(b)))
	if diff > 180.0 {
		diff = 360.0 - diff
	}
	return diff
}

// bearingToCompassDir maps Minecraft native yaw in [0, 360) to an 8-point compass label.
func bearingToCompassDir(bearingDeg float32) string {
	b := float64(normalizeBearingDeg(bearingDeg))
	switch {
	case b >= 337.5 || b < 22.5:
		return "S"
	case b < 67.5:
		return "SW"
	case b < 112.5:
		return "W"
	case b < 157.5:
		return "NW"
	case b < 202.5:
		return "N"
	case b < 247.5:
		return "NE"
	case b < 292.5:
		return "E"
	default:
		return "SE"
	}
}

// SetClanSize configures the known clan population size for rendezvous vs. solo homestead logic.
// Marks the clan size as explicitly configured so runtime auto-detection will not override it.
func (s *AgentState) SetClanSize(n int) {
	if s == nil {
		return
	}
	if n < 1 {
		n = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClanSize = n
	s.clanSizeExplicit = true
}

// KnownClanSize returns the clan's size as last set or discovered (0 if never).
func (s *AgentState) KnownClanSize() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ClanSize
}

// SetResolvedClanSize updates ClanSize from environment/DB discovery unless explicitly overridden.
func (s *AgentState) SetResolvedClanSize(n int) {
	if s == nil {
		return
	}
	if n < 1 {
		n = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clanSizeExplicit {
		return
	}
	s.ClanSize = n
}

// GetClanSize returns the effective clan size (minimum 1).
func (s *AgentState) GetClanSize() int {
	if s == nil {
		return 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ClanSize < 1 {
		return 1
	}
	return s.ClanSize
}

// SetRendezvousOrigin explicitly configures the clan rendezvous camp coordinate.
func (s *AgentState) SetRendezvousOrigin(x, y, z float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RendezvousOrigin = &[3]float64{x, y, z}
}

// GetLatestSurvey returns the latest ingested HorizonSurveyReport safely.
func (s *AgentState) GetLatestSurvey() *civquestv1.HorizonSurveyReport {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LatestSurvey
}

// IsExpeditionActive reports whether an expedition frame is on the activity stack.
// Derived from the stack projection; there is no expedition phase to read.
func (s *AgentState) IsExpeditionActive() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsExpeditionActiveLocked()
}

// IsExpeditionActiveLocked is IsExpeditionActive for callers holding s.mu. A pending request
// to start one (made earlier in this tick's waterfall) counts: the frame is about to exist.
func (s *AgentState) IsExpeditionActiveLocked() bool {
	return s != nil && (s.hasActivityKindLocked(activityKindExpedition))
}

// IsExpeditionSurveying reports whether a horizon survey frame is on the stack (a survey's
// timeout is its frame lease, so there is no tick to compare against).
func (s *AgentState) IsExpeditionSurveying(_ int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isExpeditionSurveyingLocked(0)
}

func (s *AgentState) isExpeditionSurveyingLocked(_ int64) bool {
	return s != nil && s.hasActivityKindLocked(activityKindSurvey)
}

// SetCollectiveSettlementSite records the chosen collective clan settlement site.
func (s *AgentState) SetCollectiveSettlementSite(x, y, z float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CollectiveSiteChosen = true
	s.CollectiveSitePos = [3]float64{x, y, z}
}

// LatestSurveySummary returns the compact compass summary of the latest horizon survey.
func (s *AgentState) LatestSurveySummary() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.LatestSurvey != nil {
		return FormatHorizonSurveySummary(s.LatestSurvey)
	}
	return ""
}

// IngestHorizonSurveyReport stores a completed 360-degree HorizonSurveyReport (the survey
// frame observes it and pops with a SurveyReport) and indexes distant resource
// landmarks (FOREST_CANOPY, WATER, STONE_OUTCROP, STRUCTURE) into SpatialIndex.
func (s *AgentState) IngestHorizonSurveyReport(report *civquestv1.HorizonSurveyReport, worldTick int64) {
	if s == nil || report == nil {
		return
	}
	sectorsCopy := make([]*civquestv1.HorizonSector, len(report.Sectors))
	copy(sectorsCopy, report.Sectors)
	cloned := &civquestv1.HorizonSurveyReport{
		OriginX:       report.OriginX,
		OriginY:       report.OriginY,
		OriginZ:       report.OriginZ,
		CompletedTick: report.CompletedTick,
		MaxRange:      report.MaxRange,
		Sectors:       sectorsCopy,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tick := worldTick
	if tick == 0 {
		tick = cloned.CompletedTick
	}

	s.LatestSurvey = cloned
	s.LastSurveyTick = tick
	s.LastSurveyOrigin = [3]float64{cloned.OriginX, cloned.OriginY, cloned.OriginZ}

	si := s.SpatialIndexLocked()
	if si == nil {
		return
	}

	for _, sec := range cloned.Sectors {
		if sec == nil || sec.HitDistance <= 0 {
			continue
		}
		if sec.FeatureType == "OPEN_PLAINS" || sec.FeatureType == "RIDGE_OBSTRUCTION" {
			continue
		}
		feat := strings.ToUpper(strings.TrimSpace(sec.FeatureType))
		var category, defaultBlockID string
		switch feat {
		case "FOREST_CANOPY":
			category = POICategoryResource
			defaultBlockID = "minecraft:oak_leaves"
		case "WATER":
			category = POICategoryResource
			defaultBlockID = "minecraft:water"
		case "STONE_OUTCROP":
			category = POICategoryResource
			defaultBlockID = "minecraft:stone"
		case "STRUCTURE":
			category = POICategoryHabitation
			defaultBlockID = "minecraft:oak_planks"
		default:
			continue
		}

		blockID := strings.TrimSpace(sec.HitBlockId)
		if blockID == "" || strings.EqualFold(blockID, "minecraft:air") {
			blockID = defaultBlockID
		}

		bx := int32(math.Floor(sec.HitX))
		by := int32(math.Floor(sec.HitY))
		bz := int32(math.Floor(sec.HitZ))
		poiID := FormatPOIID(category, bx, by, bz)

		// Preserve verified close-range high-confidence POIs over distant raycast sightings.
		if existing, ok := si.GetPOI(poiID); ok && existing != nil && existing.Reliability >= 0.9 {
			continue
		}

		accessY := sec.HitY
		if feat == "FOREST_CANOPY" && sec.HitY > cloned.OriginY+2.0 {
			accessY = cloned.OriginY
		}

		layer := ClassifyElevationLayer(sec.HitY, cloned.OriginY)
		if feat == "FOREST_CANOPY" && math.Abs(float64(sec.ElevationDelta)) < 14.0 {
			layer = ElevationSurface
		}

		si.UpsertPOI(db.AgentPOI{
			POIID:            poiID,
			Category:         category,
			BlockID:          blockID,
			X:                sec.HitX,
			Y:                sec.HitY,
			Z:                sec.HitZ,
			AccessX:          sec.HitX,
			AccessY:          accessY,
			AccessZ:          sec.HitZ,
			ElevationLayer:   layer,
			SafetyScore:      0.85,
			DiscoveredTick:   tick,
			LastVerifiedTick: tick,
			Reliability:      0.65,
			IsActive:         true,
		})
	}
}

// FormatHorizonSurveySummary renders a compact compass summary of a HorizonSurveyReport,
// e.g. "N: oak forest canopy at 42m; E: open plains 64m+; S: ridge obstruction at 8m; W: water at 38m".
func FormatHorizonSurveySummary(report *civquestv1.HorizonSurveyReport) string {
	if report == nil || len(report.Sectors) == 0 {
		return ""
	}
	maxRange := float64(report.MaxRange)
	if maxRange <= 0 {
		maxRange = float64(defaultSurveyMaxRange)
	}

	type dirPick struct {
		priority int
		desc     string
	}
	bestByCard := make(map[string]dirPick)

	featurePriority := func(feat string) int {
		switch strings.ToUpper(strings.TrimSpace(feat)) {
		case "STRUCTURE":
			return 6
		case "FOREST_CANOPY":
			return 5
		case "WATER":
			return 4
		case "STONE_OUTCROP":
			return 3
		case "RIDGE_OBSTRUCTION":
			return 2
		case "OPEN_PLAINS":
			return 1
		default:
			return 0
		}
	}

	formatFeatureDesc := func(sec *civquestv1.HorizonSector) string {
		feat := strings.ToUpper(strings.TrimSpace(sec.FeatureType))
		dist := int(math.Round(float64(sec.HitDistance)))
		switch feat {
		case "FOREST_CANOPY":
			species := extractTreeSpecies(sec.HitBlockId)
			if species != "" {
				return fmt.Sprintf("%s forest canopy at %dm", species, dist)
			}
			return fmt.Sprintf("forest canopy at %dm", dist)
		case "WATER":
			return fmt.Sprintf("water at %dm", dist)
		case "STONE_OUTCROP":
			return fmt.Sprintf("stone outcrop at %dm", dist)
		case "STRUCTURE":
			return fmt.Sprintf("structure at %dm", dist)
		case "RIDGE_OBSTRUCTION":
			return fmt.Sprintf("ridge obstruction at %dm", dist)
		case "OPEN_PLAINS":
			if sec.OpenHorizon || float64(sec.HitDistance) >= maxRange*0.75 {
				return fmt.Sprintf("open plains %dm+", int(math.Round(maxRange)))
			}
			return fmt.Sprintf("open plains %dm", dist)
		default:
			return fmt.Sprintf("%s at %dm", strings.ToLower(feat), dist)
		}
	}

	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		card := primaryCardinalForBearing(sec.BearingDeg)
		prio := featurePriority(sec.FeatureType)
		existing, ok := bestByCard[card]
		if !ok || prio > existing.priority {
			bestByCard[card] = dirPick{
				priority: prio,
				desc:     formatFeatureDesc(sec),
			}
		}
	}

	var parts []string
	for _, card := range []string{"N", "E", "S", "W"} {
		if pick, ok := bestByCard[card]; ok {
			parts = append(parts, fmt.Sprintf("%s: %s", card, pick.desc))
		}
	}
	return strings.Join(parts, "; ")
}

func primaryCardinalForBearing(bearingDeg float32) string {
	b := float64(normalizeBearingDeg(bearingDeg))
	switch {
	case b >= 315.0 || b < 45.0:
		return "S"
	case b < 135.0:
		return "W"
	case b < 225.0:
		return "N"
	default:
		return "E"
	}
}

func extractTreeSpecies(blockID string) string {
	id := bareID(blockID)
	for _, suffix := range []string{"_leaves", "_log", "_wood", "_stem"} {
		if strings.HasSuffix(id, suffix) {
			return strings.TrimSuffix(id, suffix)
		}
	}
	return ""
}

// ScoreSurveySiteSuitability computes a deterministic local settlement suitability score
// from a 360-degree HorizonSurveyReport.
func ScoreSurveySiteSuitability(report *civquestv1.HorizonSurveyReport) float64 {
	if report == nil || len(report.Sectors) == 0 {
		return 0
	}
	var forest, plains, water, stone, structure, ridge, occluded, ice int
	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		if sec.Occluded {
			occluded++
		}
		switch db.SiteFeatureOf(sec.FeatureType, report.Frozen) {
		case "FOREST_CANOPY":
			forest++
		case "OPEN_PLAINS":
			plains++
		case "WATER":
			water++
		case "STONE_OUTCROP":
			stone++
		case "STRUCTURE":
			structure++
		case "RIDGE_OBSTRUCTION":
			ridge++
		case db.FeatureIce:
			ice++
		}
	}
	return db.ScoreSite(db.SiteFeatures{Forest: float64(forest), Water: float64(water), Plains: float64(plains),
		Stone: float64(stone), Structure: float64(structure), Ridge: float64(ridge), Occluded: float64(occluded), Ice: float64(ice)})
}

func countSurveyFeatures(report *civquestv1.HorizonSurveyReport) (forest, plains, water, stone, occluded, total int) {
	if report == nil {
		return
	}
	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		total++
		if sec.Occluded {
			occluded++
		}
		switch db.SiteFeatureOf(sec.FeatureType, report.Frozen) {
		case "FOREST_CANOPY":
			forest++
		case "OPEN_PLAINS":
			plains++
		case "WATER":
			water++
		case "STONE_OUTCROP":
			stone++
		}
	}
	return
}

func meetsHomesteadSuitability(report *civquestv1.HorizonSurveyReport, nearby db.SiteFeatures) bool {
	if report == nil || len(report.Sectors) == 0 {
		return false
	}
	forest, plains, water, stone, occluded, total := countSurveyFeatures(report)
	if total == 0 || occluded*2 >= total {
		return false
	}
	if forest < 2 {
		return false
	}
	if plains < 4 && water == 0 && stone == 0 {
		return false
	}
	score := ScoreSurveySiteSuitability(report)
	// What has been seen near here counts as it does for a clan's choice.
	score += db.ScoreSite(db.SiteFeatures{Ore: nearby.Ore, Cave: nearby.Cave, Livestock: nearby.Livestock})
	return score >= homesteadScore
}

func isPrimeSettlementSite(report *civquestv1.HorizonSurveyReport) bool {
	if report == nil || len(report.Sectors) == 0 {
		return false
	}
	forest, plains, water, stone, occluded, total := countSurveyFeatures(report)
	if total == 0 || occluded*2 >= total {
		return false
	}
	return forest >= 3 && plains >= 4 && (water >= 1 || stone >= 1) && ScoreSurveySiteSuitability(report) >= 35.0
}
