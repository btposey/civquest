package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// ExpeditionPhase represents the current phase of the ADR-032 chained 360-degree
// horizon survey and clan expedition state machine.
type ExpeditionPhase string

const (
	ExpeditionIdle             ExpeditionPhase = "IDLE"
	ExpeditionSurveying        ExpeditionPhase = "SURVEYING"
	ExpeditionTraversing       ExpeditionPhase = "TRAVERSING"
	ExpeditionFlanking         ExpeditionPhase = "FLANKING"
	ExpeditionReturning        ExpeditionPhase = "RETURNING"
	ExpeditionHomesteadClaimed ExpeditionPhase = "HOMESTEAD_CLAIMED"
)

const (
	defaultSurveyNumSectors    int32   = 36
	defaultSurveyMaxRange      float32 = 64.0
	defaultSurveyTicksPerSec   int32   = 2
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
	s.RendezvousOrigin = [3]float64{x, y, z}
	s.rendezvousOriginSet = true
}

// GetExpeditionPhase returns the current ExpeditionPhase safely.
func (s *AgentState) GetExpeditionPhase() ExpeditionPhase {
	if s == nil {
		return ExpeditionIdle
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ExpeditionPhase == "" {
		return ExpeditionIdle
	}
	return s.ExpeditionPhase
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

// IsExpeditionActive returns true when the explorer is actively executing a survey,
// outbound traversal, flanking maneuver, or clan rendezvous return leg.
func (s *AgentState) IsExpeditionActive() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsExpeditionActiveLocked()
}

// IsExpeditionActiveLocked returns true when the explorer has an active expedition phase.
// Caller must hold s.mu.
func (s *AgentState) IsExpeditionActiveLocked() bool {
	if s == nil {
		return false
	}
	switch s.ExpeditionPhase {
	case ExpeditionSurveying, ExpeditionTraversing, ExpeditionFlanking, ExpeditionReturning:
		return true
	default:
		return false
	}
}

// IsExpeditionSurveying returns true when a 360-degree horizon survey is actively
// executing on the motor stack and has not yet exceeded the safety timeout.
func (s *AgentState) IsExpeditionSurveying(worldTick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isExpeditionSurveyingLocked(worldTick)
}

func (s *AgentState) isExpeditionSurveyingLocked(worldTick int64) bool {
	if s == nil || s.ExpeditionPhase != ExpeditionSurveying {
		return false
	}
	if s.expeditionSurveyStartTick == 0 || worldTick < s.expeditionSurveyStartTick {
		return true
	}
	return worldTick-s.expeditionSurveyStartTick <= expeditionSurveyTimeout
}

// ClearExpeditionSurveyingIfActive resets an in-flight SURVEYING phase back to IDLE
// (for example when preempted by combat emergency or urgent foraging).
func (s *AgentState) ClearExpeditionSurveyingIfActive() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ExpeditionPhase == ExpeditionSurveying {
		s.ExpeditionPhase = ExpeditionIdle
		s.expeditionSurveyStartTick = 0
	}
}

// ConsumePendingRendezvousSync atomically checks and clears the pending rendezvous sync flag.
func (s *AgentState) ConsumePendingRendezvousSync() (bool, string) {
	if s == nil {
		return false, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.PendingRendezvousSync {
		return false, ""
	}
	summary := s.PendingRendezvousSummary
	s.PendingRendezvousSync = false
	s.PendingRendezvousSummary = ""
	return true, summary
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
	if s.latestSurveySummary != "" {
		return s.latestSurveySummary
	}
	if s.LatestSurvey != nil {
		return FormatHorizonSurveySummary(s.LatestSurvey)
	}
	return ""
}

// IngestHorizonSurveyReport stores a completed 360-degree HorizonSurveyReport, clears
// active surveying state so the next tick selects a bearing, and indexes distant resource
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

	if s.LastSurveyTick > 0 {
		s.expeditionPrevOrigin = s.LastSurveyOrigin
		s.expeditionPrevOriginSet = true
	}

	tick := worldTick
	if tick == 0 {
		tick = cloned.CompletedTick
	}

	s.LatestSurvey = cloned
	s.LastSurveyTick = tick
	s.LastSurveyOrigin = [3]float64{cloned.OriginX, cloned.OriginY, cloned.OriginZ}
	s.latestSurveySummary = ""

	if s.ExpeditionPhase == ExpeditionSurveying {
		s.expeditionSurveyStartTick = 0
		if s.expeditionReturning {
			s.ExpeditionPhase = ExpeditionReturning
		} else {
			s.ExpeditionPhase = ExpeditionIdle
		}
	}

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
	var forest, plains, water, stone, structure, ridge, occluded int
	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		if sec.Occluded {
			occluded++
		}
		switch strings.ToUpper(strings.TrimSpace(sec.FeatureType)) {
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
		}
	}
	score := math.Min(float64(forest), 8.0)*3.0 +
		math.Min(float64(plains), 10.0)*2.5 +
		math.Min(float64(water), 5.0)*3.5 +
		math.Min(float64(stone), 5.0)*2.0 +
		math.Min(float64(structure), 3.0)*2.0 -
		float64(ridge)*2.0 -
		float64(occluded)*2.0

	if forest >= 2 && plains >= 2 {
		score += 10.0
	}
	if water >= 1 {
		score += 6.0
	}
	if stone >= 1 {
		score += 4.0
	}
	return math.Round(score*10) / 10
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
		switch strings.ToUpper(strings.TrimSpace(sec.FeatureType)) {
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

func meetsHomesteadSuitability(report *civquestv1.HorizonSurveyReport) bool {
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
	return ScoreSurveySiteSuitability(report) >= 20.0
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

// EvaluateHorizonExplorer acquires state.mu and runs EvaluateHorizonExplorerLocked.
func EvaluateHorizonExplorer(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return EvaluateHorizonExplorerLocked(frame, state, cfg, agentID)
}

// EvaluateHorizonExplorerLocked executes the deterministic chained 360-degree
// Survey -> Choose Bearing -> Traverse -> Rendezvous / Solo Homestead FSM.
// Caller must hold state.mu.
func EvaluateHorizonExplorerLocked(
	frame *civquestv1.TelemetryFrame,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	agentID string,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.Status.IsSleeping {
		return nil
	}

	pos := frame.Status.Position
	if agentID == "" {
		if frame.Status.AgentId != "" {
			agentID = frame.Status.AgentId
		} else if cfg != nil {
			agentID = cfg.Name
		}
	}

	if state.ExpeditionPhase == "" {
		state.ExpeditionPhase = ExpeditionIdle
	}
	if state.ClanSize < 1 {
		state.ClanSize = 1
	}
	if state.homeOrigin == nil {
		originCopy := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		state.homeOrigin = originCopy
		state.currentOutpost = originCopy
		if len(state.outposts) == 0 {
			state.outposts = append(state.outposts, originCopy)
		}
	}
	if !state.rendezvousOriginSet {
		state.RendezvousOrigin = [3]float64{state.homeOrigin.X, state.homeOrigin.Y, state.homeOrigin.Z}
		state.rendezvousOriginSet = true
	}

	// Solo explorers who have claimed a homestead stay settled there.
	if state.ExpeditionPhase == ExpeditionHomesteadClaimed || state.HomesteadClaimed {
		state.ExpeditionPhase = ExpeditionHomesteadClaimed
		return nil
	}

	// If a collective clan settlement site was chosen, converge toward it when distant.
	if state.CollectiveSiteChosen && state.ClanSize > 1 && state.ExpeditionPhase == ExpeditionIdle {
		distSite := math.Hypot(pos.X-state.CollectiveSitePos[0], pos.Z-state.CollectiveSitePos[2])
		if distSite > 12.0 {
			return emitExpeditionNavigateLocked(agentID, frame, state, state.CollectiveSitePos)
		}
		return nil
	}

	// Hold steady while SURVEY_HORIZON is actively sweeping on the server (up to safety timeout).
	if state.ExpeditionPhase == ExpeditionSurveying {
		if state.isExpeditionSurveyingLocked(frame.WorldTick) {
			return nil
		}
		// Safety timeout expired without receiving report: recover to IDLE (or RETURNING if returning).
		state.expeditionSurveyStartTick = 0
		if state.expeditionReturning {
			state.ExpeditionPhase = ExpeditionReturning
		} else {
			state.ExpeditionPhase = ExpeditionIdle
		}
	}

	navState := frame.Status.NavigationState

	// Handle active return leg to clan rendezvous origin (multi-member clans only).
	if state.ExpeditionPhase == ExpeditionReturning {
		distHome := math.Hypot(pos.X-state.RendezvousOrigin[0], pos.Z-state.RendezvousOrigin[2])
		if distHome <= expeditionRendezvousRadius {
			state.PendingRendezvousSync = true
			state.PendingRendezvousSummary = buildRendezvousReportSummary(state, pos)
			state.ExpeditionHops = 0
			state.ExpeditionFlankCount = 0
			state.ExpeditionNavAttempts = 0
			state.expeditionBlockedBearings = nil
			state.expeditionReturning = false
			state.ExpeditionPhase = ExpeditionIdle
			return nil
		}

		if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
			return handleExpeditionBarrierLocked(agentID, frame, pos, state)
		}
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.ExpeditionNavAttempts > 0 {
			return nil
		}
		if !canDispatchExpeditionNavLocked(state, frame.WorldTick) {
			return nil
		}
		state.ExpeditionBearing = vectorToBearingDeg(state.RendezvousOrigin[0]-pos.X, state.RendezvousOrigin[2]-pos.Z)
		state.ExpeditionTarget = state.RendezvousOrigin
		return emitExpeditionNavigateLocked(agentID, frame, state, state.RendezvousOrigin)
	}

	// Handle active outbound traversal or flanking around an obstruction.
	if state.ExpeditionPhase == ExpeditionTraversing || state.ExpeditionPhase == ExpeditionFlanking {
		dxT := pos.X - state.ExpeditionTarget[0]
		dzT := pos.Z - state.ExpeditionTarget[2]
		if dxT*dxT+dzT*dzT <= expeditionArrivalRadius*expeditionArrivalRadius {
			if state.ExpeditionPhase == ExpeditionFlanking {
				if state.expeditionReturning {
					state.ExpeditionPhase = ExpeditionReturning
					state.ExpeditionNavAttempts = 0
					state.ExpeditionTarget = state.RendezvousOrigin
					state.ExpeditionBearing = vectorToBearingDeg(state.RendezvousOrigin[0]-pos.X, state.RendezvousOrigin[2]-pos.Z)
					return emitExpeditionNavigateLocked(agentID, frame, state, state.RendezvousOrigin)
				}
				state.ExpeditionPhase = ExpeditionTraversing
				state.ExpeditionTarget = state.expeditionPrimaryTarget
				state.ExpeditionBearing = vectorToBearingDeg(state.ExpeditionTarget[0]-pos.X, state.ExpeditionTarget[2]-pos.Z)
				state.ExpeditionNavAttempts = 0
				return emitExpeditionNavigateLocked(agentID, frame, state, state.ExpeditionTarget)
			}

			// Arrived at ExpeditionTarget!
			state.ExpeditionHops++
			state.ExpeditionFlankCount = 0
			state.ExpeditionNavAttempts = 0
			state.expeditionBlockedBearings = nil

			if state.ClanSize <= 1 {
				// Solo explorer: NEVER returns to RendezvousOrigin; claims homestead directly if suitable
				// after chaining at least 2 outbound hops (or surveying locally at the candidate site).
				distFromSurvey := math.Hypot(pos.X-state.LastSurveyOrigin[0], pos.Z-state.LastSurveyOrigin[2])
				if (state.ExpeditionHops >= 2 || distFromSurvey <= expeditionResurveyDist) &&
					meetsHomesteadSuitability(state.LatestSurvey) {
					state.HomesteadClaimed = true
					state.HomesteadPos = [3]float64{pos.X, pos.Y, pos.Z}
					posVec := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
					state.homeOrigin = posVec
					state.currentOutpost = posVec
					state.ShelterPos = &civquestv1.BlockPosProto{
						X: int32(math.Floor(pos.X)),
						Y: int32(math.Floor(pos.Y)),
						Z: int32(math.Floor(pos.Z)),
					}
					state.ExpeditionPhase = ExpeditionHomesteadClaimed
					state.goals.ImmediateGoal = "CLAIM_HOMESTEAD: Stake solo homestead and build shelter at discovered site"
					return nil
				}
				state.ExpeditionPhase = ExpeditionIdle
			} else {
				// Multi-member clan: return to rendezvous after 2+ hops, prime site discovery, or low food.
				if state.ExpeditionHops >= 2 || isPrimeSettlementSite(state.LatestSurvey) || frame.Status.FoodLevel <= 8 {
					state.ExpeditionPhase = ExpeditionReturning
					state.expeditionReturning = true
					state.ExpeditionNavAttempts = 0
					state.ExpeditionTarget = state.RendezvousOrigin
					state.ExpeditionBearing = vectorToBearingDeg(state.RendezvousOrigin[0]-pos.X, state.RendezvousOrigin[2]-pos.Z)
					return emitExpeditionNavigateLocked(agentID, frame, state, state.RendezvousOrigin)
				}
				state.ExpeditionPhase = ExpeditionIdle
			}
		} else if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
			return handleExpeditionBarrierLocked(agentID, frame, pos, state)
		} else {
			if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.ExpeditionNavAttempts > 0 {
				return nil
			}
			if !canDispatchExpeditionNavLocked(state, frame.WorldTick) {
				return nil
			}
			return emitExpeditionNavigateLocked(agentID, frame, state, state.ExpeditionTarget)
		}
	}

	// ExpeditionIdle: either trigger a fresh 360° SurveyHorizonIntent or pick the best bearing from LatestSurvey.
	distFromSurvey := math.Hypot(pos.X-state.LastSurveyOrigin[0], pos.Z-state.LastSurveyOrigin[2])
	surveyExpired := state.LatestSurvey == nil ||
		distFromSurvey > expeditionResurveyDist ||
		(frame.WorldTick >= state.LastSurveyTick && frame.WorldTick-state.LastSurveyTick > expeditionResurveyMaxAge)

	if surveyExpired {
		return startHorizonSurveyLocked(agentID, frame.WorldTick, pos, state,
			"SURVEY_HORIZON: Sweeping 360° horizon to map terrain and resources")
	}

	bearing, target, _ := chooseBestSurveyBearingLocked(agentID, frame, pos, state)
	state.ExpeditionBearing = bearing
	state.ExpeditionTarget = target
	state.expeditionPrimaryBearing = bearing
	state.expeditionPrimaryTarget = target
	state.ExpeditionPhase = ExpeditionTraversing
	state.ExpeditionNavAttempts = 0
	return emitExpeditionNavigateLocked(agentID, frame, state, target)
}

func startHorizonSurveyLocked(agentID string, worldTick int64, pos *civquestv1.Vec3, state *AgentState, goal string) *civquestv1.AgentIntent {
	if state == nil {
		return nil
	}
	if state.isExpeditionSurveyingLocked(worldTick) {
		return nil
	}
	// Duplicate survey storm guard: never emit another SurveyHorizonIntent within 80 ticks
	// of the last emitted survey at the same <6m spot unless the previous survey completed
	// or genuinely timed out (>= 120 ticks).
	if state.lastSurveyEmitTick > 0 && worldTick >= state.lastSurveyEmitTick &&
		worldTick-state.lastSurveyEmitTick < 80 &&
		state.lastSurveyEmitOriginSet && pos != nil &&
		math.Hypot(pos.X-state.lastSurveyEmitOrigin[0], pos.Z-state.lastSurveyEmitOrigin[2]) < expeditionRimResurveyDist {
		prevCompleted := state.LastSurveyTick > 0 && state.LastSurveyTick >= state.lastSurveyEmitTick
		prevTimedOut := worldTick-state.lastSurveyEmitTick >= expeditionSurveyTimeout
		if !prevCompleted && !prevTimedOut {
			return nil
		}
	}

	state.ExpeditionPhase = ExpeditionSurveying
	state.expeditionSurveyStartTick = worldTick
	state.lastSurveyEmitTick = worldTick
	if pos != nil {
		state.lastSurveyEmitOrigin = [3]float64{pos.X, pos.Y, pos.Z}
		state.lastSurveyEmitOriginSet = true
	}
	if goal != "" {
		state.goals.ImmediateGoal = goal
	}
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_SurveyHorizon{
			SurveyHorizon: &civquestv1.SurveyHorizonIntent{
				NumSectors:     defaultSurveyNumSectors,
				MaxRange:       defaultSurveyMaxRange,
				TicksPerSector: defaultSurveyTicksPerSec,
			},
		},
	}
}

// handleExpeditionBarrierLocked implements the rim re-survey ravine protocol:
// 1. Mark blocked forward edge as POICategoryHazard ("civquest:ravine_barrier") & record blocked bearing.
// 2. If not already surveyed within 6.0m of this rim position, pause and immediately trigger a fresh 360° SurveyHorizonIntent.
// 3. If already surveyed within < 6.0m and stuck again, pick a flanking waypoint (left/right 90° preferring clearer side).
func handleExpeditionBarrierLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
) *civquestv1.AgentIntent {
	dx, dz := bearingToUnitVector(state.ExpeditionBearing)
	hx := pos.X + dx*3.0
	hy := pos.Y
	hz := pos.Z + dz*3.0

	if si := state.SpatialIndexLocked(); si != nil {
		bx := int32(math.Floor(hx))
		by := int32(math.Floor(hy))
		bz := int32(math.Floor(hz))
		si.UpsertPOI(db.AgentPOI{
			POIID:            FormatPOIID(POICategoryHazard, bx, by, bz),
			Category:         POICategoryHazard,
			BlockID:          "civquest:ravine_barrier",
			X:                hx,
			Y:                hy,
			Z:                hz,
			AccessX:          pos.X,
			AccessY:          pos.Y,
			AccessZ:          pos.Z,
			ElevationLayer:   ClassifyElevationLayer(hy, pos.Y),
			SafetyScore:      0.1,
			DiscoveredTick:   frame.WorldTick,
			LastVerifiedTick: frame.WorldTick,
			Reliability:      1.0,
			IsActive:         true,
		})
	}

	state.expeditionBlockedBearings = append(state.expeditionBlockedBearings, normalizeBearingDeg(state.ExpeditionBearing))
	if len(state.expeditionBlockedBearings) > 12 {
		state.expeditionBlockedBearings = state.expeditionBlockedBearings[len(state.expeditionBlockedBearings)-12:]
	}

	distFromLastSurvey := math.Hypot(pos.X-state.LastSurveyOrigin[0], pos.Z-state.LastSurveyOrigin[2])
	if state.LastSurveyTick == 0 || distFromLastSurvey >= expeditionRimResurveyDist {
		return startHorizonSurveyLocked(agentID, frame.WorldTick, pos, state,
			"SURVEY_HORIZON: Pausing at terrain rim to sweep 360° and find clear route")
	}

	// Already surveyed within <6.0m of this rim spot and hit stuck again: pick best unblocked flank.
	state.ExpeditionFlankCount++
	if state.ExpeditionFlankCount >= 3 {
		state.ExpeditionFlankCount = 0
		if distFromLastSurvey >= expeditionRimResurveyDist {
			state.expeditionReturning = false
			state.ExpeditionPhase = ExpeditionIdle
			return startHorizonSurveyLocked(agentID, frame.WorldTick, pos, state,
				"SURVEY_HORIZON: Re-surveying after multiple flanked barriers")
		}
		// Already surveyed at this <6m spot: do NOT emit a duplicate same-spot survey.
		// Back out away from the blocked rim toward an unblocked/reverse bearing.
		retreatBearing := pickBestFlankBearingLocked(state.ExpeditionBearing, pos, state, true)
		rdx, rdz := bearingToUnitVector(retreatBearing)
		retreatTarget := [3]float64{
			math.Round((pos.X+rdx*expeditionFlankStepDist)*10) / 10,
			pos.Y,
			math.Round((pos.Z+rdz*expeditionFlankStepDist)*10) / 10,
		}
		state.ExpeditionPhase = ExpeditionFlanking
		state.ExpeditionBearing = retreatBearing
		state.ExpeditionTarget = retreatTarget
		state.ExpeditionNavAttempts = 0
		return emitExpeditionNavigateLocked(agentID, frame, state, retreatTarget)
	}

	flankBearing := pickBestFlankBearingLocked(state.ExpeditionBearing, pos, state, false)
	fdx, fdz := bearingToUnitVector(flankBearing)
	flankTarget := [3]float64{
		math.Round((pos.X+fdx*expeditionFlankStepDist)*10) / 10,
		pos.Y,
		math.Round((pos.Z+fdz*expeditionFlankStepDist)*10) / 10,
	}
	state.ExpeditionPhase = ExpeditionFlanking
	state.ExpeditionBearing = flankBearing
	state.ExpeditionTarget = flankTarget
	state.ExpeditionNavAttempts = 0
	return emitExpeditionNavigateLocked(agentID, frame, state, flankTarget)
}

func pickBestFlankBearingLocked(currentBearing float32, pos *civquestv1.Vec3, state *AgentState, allowReverse bool) float32 {
	candidates := []float32{
		normalizeBearingDeg(currentBearing - 90.0),
		normalizeBearingDeg(currentBearing + 90.0),
	}
	if allowReverse {
		candidates = append(candidates,
			normalizeBearingDeg(currentBearing-135.0),
			normalizeBearingDeg(currentBearing+135.0),
			normalizeBearingDeg(currentBearing+180.0),
		)
	}

	var hazards []*db.AgentPOI
	if pos != nil && state != nil {
		if si := state.SpatialIndexLocked(); si != nil {
			hazards = si.FindPOIsInRadius(pos, POICategoryHazard, 32.0)
		}
	}

	scoreSide := func(b float32) float64 {
		s := 0.0
		if state != nil {
			for _, blocked := range state.expeditionBlockedBearings {
				if angularDiffDeg(b, blocked) <= 40.0 {
					s -= 100.0
				}
			}
			for _, haz := range hazards {
				if haz == nil || pos == nil {
					continue
				}
				hdx := haz.X - pos.X
				hdz := haz.Z - pos.Z
				if math.Hypot(hdx, hdz) <= 0.1 {
					continue
				}
				hBearing := vectorToBearingDeg(hdx, hdz)
				if angularDiffDeg(b, hBearing) <= 40.0 {
					s -= 80.0
				}
			}
			if state.LatestSurvey != nil {
				var bestSec *civquestv1.HorizonSector
				bestAngle := 999.0
				for _, sec := range state.LatestSurvey.Sectors {
					if sec == nil {
						continue
					}
					ang := angularDiffDeg(b, sec.BearingDeg)
					if ang < bestAngle {
						bestAngle = ang
						bestSec = sec
					}
				}
				if bestSec != nil {
					s += float64(bestSec.HitDistance)
					if bestSec.Occluded {
						s -= 35.0
					}
					if bestSec.OpenHorizon {
						s += 15.0
					}
				}
			}
		}
		return s
	}

	bestBearing := candidates[0]
	bestScore := scoreSide(bestBearing)
	for _, cand := range candidates[1:] {
		sc := scoreSide(cand)
		if sc > bestScore {
			bestScore = sc
			bestBearing = cand
		}
	}
	return bestBearing
}

func chooseBestSurveyBearingLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
) (bearing float32, target [3]float64, bestFeature string) {
	report := state.LatestSurvey
	if report == nil || len(report.Sectors) == 0 {
		wedge := float32(fnv1aString(agentID) % 360)
		dx, dz := bearingToUnitVector(wedge)
		return wedge, [3]float64{
			math.Round((pos.X+dx*36.0)*10) / 10,
			pos.Y,
			math.Round((pos.Z+dz*36.0)*10) / 10,
		}, "OPEN_PLAINS"
	}

	inv := inspectBootstrapInventory(frame.Status.Inventory)
	needsTimber := !inv.hasAxe || !inv.hasSword || inv.logCount < 6
	needsStone := !inv.hasStoneGear && inv.cobbleCount < 3

	var hazards []*db.AgentPOI
	if si := state.SpatialIndexLocked(); si != nil {
		hazards = si.FindPOIsInRadius(pos, POICategoryHazard, 64.0)
	}

	hasPrevOrigin := state.expeditionPrevOriginSet &&
		math.Hypot(pos.X-state.expeditionPrevOrigin[0], pos.Z-state.expeditionPrevOrigin[2]) > 4.0
	var backBearing float32
	if hasPrevOrigin {
		backBearing = vectorToBearingDeg(state.expeditionPrevOrigin[0]-pos.X, state.expeditionPrevOrigin[2]-pos.Z)
	}

	agentHash := fnv1aString(agentID)
	wedgeDeg := float32(agentHash % 360)

	var bestSec *civquestv1.HorizonSector
	bestScore := -math.MaxFloat64

	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		b := normalizeBearingDeg(sec.BearingDeg)
		score := float64(sec.HitDistance) * 0.25
		if sec.OpenHorizon {
			score += 10.0
		}
		if sec.Occluded {
			score -= 40.0
		}

		feat := strings.ToUpper(strings.TrimSpace(sec.FeatureType))
		switch feat {
		case "FOREST_CANOPY":
			score += 22.0
			if needsTimber {
				score += 45.0
			}
		case "STONE_OUTCROP":
			score += 12.0
			if needsStone {
				score += 35.0
			}
		case "WATER":
			score += 18.0
		case "OPEN_PLAINS":
			score += 16.0
		case "STRUCTURE":
			score += 20.0
		case "RIDGE_OBSTRUCTION":
			score -= 25.0
		}

		for _, blocked := range state.expeditionBlockedBearings {
			if angularDiffDeg(b, blocked) <= 35.0 {
				score -= 80.0
			}
		}

		for _, haz := range hazards {
			if haz == nil {
				continue
			}
			hdx := haz.X - pos.X
			hdz := haz.Z - pos.Z
			hDist := math.Hypot(hdx, hdz)
			if hDist <= 0.1 {
				continue
			}
			hBearing := vectorToBearingDeg(hdx, hdz)
			if angularDiffDeg(b, hBearing) <= 25.0 && hDist <= float64(sec.HitDistance)+4.0 {
				score -= 60.0
			}
		}

		if hasPrevOrigin && angularDiffDeg(b, backBearing) <= 45.0 {
			score -= 30.0
		}

		if state.ClanSize > 1 && state.ExpeditionHops == 0 {
			diffRad := angularDiffDeg(b, wedgeDeg) * math.Pi / 180.0
			score += math.Cos(diffRad) * 15.0
		}
		if state.ExpeditionHops > 0 {
			// Forward directional momentum along open plains unless a salient feature or blocked ravine dictates turning.
			diffRad := angularDiffDeg(b, state.ExpeditionBearing) * math.Pi / 180.0
			score += math.Cos(diffRad) * 20.0
		}

		// Deterministic zero-alloc tie-breaker so uniform terrain still fans out cleanly.
		jitterHash := (agentHash ^ (uint32(sec.SectorIndex) * 2654435761))
		score += float64(jitterHash%100) * 0.05

		if bestSec == nil || score > bestScore {
			bestScore = score
			bestSec = sec
		}
	}

	chosenBearing := normalizeBearingDeg(bestSec.BearingDeg)
	feat := strings.ToUpper(strings.TrimSpace(bestSec.FeatureType))
	var dist float64
	if (feat == "FOREST_CANOPY" || feat == "STONE_OUTCROP") && bestSec.HitDistance > 0 {
		dist = math.Max(12.0, float64(bestSec.HitDistance)-3.0)
	} else if bestSec.OpenHorizon {
		dist = 48.0
	} else {
		h := agentHash ^ (uint32(state.ExpeditionHops) * 2654435761)
		dist = 32.0 + float64(h%17) // [32, 48] blocks
		if bestSec.HitDistance > 15.0 {
			dist = math.Min(dist, math.Max(12.0, float64(bestSec.HitDistance)-3.0))
		}
	}

	dx, dz := bearingToUnitVector(chosenBearing)
	targetPos := [3]float64{
		math.Round((pos.X+dx*dist)*10) / 10,
		pos.Y,
		math.Round((pos.Z+dz*dist)*10) / 10,
	}
	return chosenBearing, targetPos, feat
}

func canDispatchExpeditionNavLocked(state *AgentState, worldTick int64) bool {
	if state.ExpeditionNavAttempts == 0 || state.expeditionLastNavTick == 0 || worldTick < state.expeditionLastNavTick {
		return true
	}
	return worldTick-state.expeditionLastNavTick >= expeditionNavCooldownTicks
}

func emitExpeditionNavigateLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	state *AgentState,
	target [3]float64,
) *civquestv1.AgentIntent {
	state.ExpeditionNavAttempts++
	state.expeditionLastNavTick = frame.WorldTick
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: target[0],
					Y: target[1],
					Z: target[2],
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}

func buildRendezvousReportSummary(state *AgentState, pos *civquestv1.Vec3) string {
	hops := state.ExpeditionHops
	if hops < 1 {
		hops = 1
	}
	distFromCamp := 0.0
	if state.LastSurveyTick > 0 {
		distFromCamp = math.Hypot(state.LastSurveyOrigin[0]-state.RendezvousOrigin[0], state.LastSurveyOrigin[2]-state.RendezvousOrigin[2])
	} else if pos != nil {
		distFromCamp = math.Hypot(pos.X-state.RendezvousOrigin[0], pos.Z-state.RendezvousOrigin[2])
	}
	dir := bearingToCompassDir(state.ExpeditionBearing)
	surveySummary := ""
	if state.LatestSurvey != nil {
		surveySummary = FormatHorizonSurveySummary(state.LatestSurvey)
	}
	if surveySummary != "" {
		return fmt.Sprintf("Returned from %d-hop %s expedition (~%.0fm out): %s", hops, dir, distFromCamp, surveySummary)
	}
	return fmt.Sprintf("Returned from %d-hop %s expedition (~%.0fm out) to share frontier map.", hops, dir, distFromCamp)
}
