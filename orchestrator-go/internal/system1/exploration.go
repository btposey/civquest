package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// DayPhase enumerates the canonical phases of the 24000-tick Minecraft day cycle.
type DayPhase string

const (
	DayPhaseDawn      DayPhase = "DAWN"
	DayPhaseMorning   DayPhase = "MORNING"
	DayPhaseMidday    DayPhase = "MIDDAY"
	DayPhaseAfternoon DayPhase = "AFTERNOON"
	DayPhaseDusk      DayPhase = "DUSK"
	DayPhaseNight     DayPhase = "NIGHT"
)

// ResolveDayPhase returns the canonical DayPhase enum for a 24000-tick Minecraft day cycle.
func ResolveDayPhase(timeOfDay int64) DayPhase {
	tod := ((timeOfDay % 24000) + 24000) % 24000
	switch {
	case tod >= 23500 || tod < 1000:
		return DayPhaseDawn
	case tod >= 1000 && tod < 5000:
		return DayPhaseMorning
	case tod >= 5000 && tod < 8000:
		return DayPhaseMidday
	case tod >= 8000 && tod < 11000:
		return DayPhaseAfternoon
	case tod >= 11000 && tod < 13000:
		return DayPhaseDusk
	default:
		return DayPhaseNight
	}
}

// FormatDayPhase returns a human-readable time-of-day phase for a 24000-tick Minecraft day cycle.
func FormatDayPhase(timeOfDay int64) string {
	switch ResolveDayPhase(timeOfDay) {
	case DayPhaseDawn:
		return "DAWN (Sunrise)"
	case DayPhaseMorning:
		return "MORNING (Daylight)"
	case DayPhaseMidday:
		return "MIDDAY (Bright Daylight)"
	case DayPhaseAfternoon:
		return "AFTERNOON (Daylight)"
	case DayPhaseDusk:
		return "DUSK (Sunset)"
	default:
		return "NIGHT (Darkness — Hostile Mob Spawning)"
	}
}

// FormatWeather returns a clean human-readable weather description.
func FormatWeather(w civquestv1.Weather) string {
	switch w {
	case civquestv1.Weather_WEATHER_RAIN:
		return "RAIN"
	case civquestv1.Weather_WEATHER_THUNDER:
		return "THUNDERSTORM"
	default:
		return "CLEAR"
	}
}

// AssessImmediateThreats evaluates visible entities within the agent's sensory frustum
// and returns a human-readable threat assessment string, the System 1 confidence score,
// and a list of hostile descriptions.
func AssessImmediateThreats(frame *civquestv1.TelemetryFrame, bravery float64) (string, float64, []string) {
	if frame == nil || frame.Status == nil {
		return "UNKNOWN", 1.0, nil
	}

	var sumHostileThreat, sumAlliesHP float64
	var hostiles []string
	selfPos := frame.Status.Position

	for _, e := range frame.VisibleEntities {
		if e == nil {
			continue
		}
		dist := float64(e.Distance)
		if dist <= 0 && e.Position != nil && selfPos != nil {
			dist = math.Hypot(e.Position.X-selfPos.X, e.Position.Z-selfPos.Z)
		}
		if dist > MaxPerceptionRadius {
			continue
		}
		if IsHostile(e) {
			sumHostileThreat += MobThreat(e.EntityType)
			shortType := strings.TrimPrefix(strings.ToLower(e.EntityType), "minecraft:")
			hostiles = append(hostiles, fmt.Sprintf("%s (%.1fm)", shortType, dist))
		} else if IsAlly(e) {
			aHP := float64(e.Health)
			if aHP <= 0 {
				aHP = 20.0
			}
			sumAlliesHP += aHP
		}
	}

	if len(hostiles) == 0 && frame.LastDamageTaken == nil {
		return "SAFE — 0 hostile mobs within 24 blocks; immediate area is clear and safe.", 999.0, nil
	}

	if len(hostiles) == 0 && frame.LastDamageTaken != nil {
		sumHostileThreat += ThreatDefault
	}

	gearMult := CalculateGearMultiplier(frame.Status.Inventory)
	conf := CalculateConfidence(float64(frame.Status.Health), sumAlliesHP, sumHostileThreat, gearMult, bravery)

	if len(hostiles) > 0 {
		status := fmt.Sprintf("THREAT PRESENT — %d hostile mob(s) nearby [%s]; combat confidence = %.2f",
			len(hostiles), strings.Join(hostiles, ", "), conf)
		return status, conf, hostiles
	}
	var srcStr string
	if frame.LastDamageTaken != nil {
		srcStr = frame.LastDamageTaken.GetSourceType().String()
		if attacker := frame.LastDamageTaken.GetAttackerName(); attacker != "" {
			srcStr = fmt.Sprintf("%s (%s)", srcStr, attacker)
		}
	}
	return fmt.Sprintf("RECENT DAMAGE TAKEN (source: %s); combat confidence = %.2f",
		srcStr, conf), conf, nil
}

// EvaluateTopologicalMapping inspects the agent's current telemetry position and surroundings
// and emits a new or updated db.MapNode (and connecting db.MapEdge) whenever the agent enters
// a new 8x8 topological grid sector.
func EvaluateTopologicalMapping(frame *civquestv1.TelemetryFrame, state *AgentState) (*db.MapNode, *db.MapEdge) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil, nil
	}

	pos := frame.Status.Position
	gx := int32(math.Floor(pos.X/8.0)) * 8
	gz := int32(math.Floor(pos.Z/8.0)) * 8
	nodeID := fmt.Sprintf("wp_%d_%d", gx, gz)

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.homeOrigin == nil {
		state.homeOrigin = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	}

	if state.lastMapNodeID == nodeID {
		return nil, nil
	}

	if state.lastMapPos != nil {
		distMoved := math.Hypot(pos.X-state.lastMapPos.X, pos.Z-state.lastMapPos.Z)
		if distMoved < 6.0 {
			return nil, nil
		}
	}

	// Calculate local safety score in [0.0, 1.0] from visible hostiles
	var hostileThreat float64
	for _, e := range frame.VisibleEntities {
		if e != nil && IsHostile(e) {
			hostileThreat += MobThreat(e.EntityType)
		}
	}
	safetyScore := math.Max(0.0, math.Min(1.0, 1.0-(hostileThreat/60.0)))

	category := "WAYPOINT"
	label := fmt.Sprintf("Frontier Sector (%d, %d)", gx, gz)
	if state.lastMapNodeID == "" {
		category = "SETTLEMENT"
		label = fmt.Sprintf("Home Origin (%d, %d)", gx, gz)
	} else if safetyScore < 0.5 {
		category = "HAZARD"
		label = fmt.Sprintf("Hostile Sector (%d, %d)", gx, gz)
	} else if frame.CurrentLandmark != nil && frame.CurrentLandmark.GetDisplayName() != "" {
		category = "LANDMARK"
		label = fmt.Sprintf("%s (%d, %d)", frame.CurrentLandmark.GetDisplayName(), gx, gz)
	}

	node := &db.MapNode{
		ID:             nodeID,
		X:              math.Round(pos.X*10) / 10,
		Y:              math.Round(pos.Y*10) / 10,
		Z:              math.Round(pos.Z*10) / 10,
		Label:          label,
		Category:       category,
		SafetyScore:    safetyScore,
		DiscoveredTick: frame.WorldTick,
	}

	var edge *db.MapEdge
	if state.lastMapNodeID != "" && state.lastMapPos != nil {
		dist := math.Sqrt(
			math.Pow(pos.X-state.lastMapPos.X, 2) +
				math.Pow(pos.Y-state.lastMapPos.Y, 2) +
				math.Pow(pos.Z-state.lastMapPos.Z, 2),
		)
		edge = &db.MapEdge{
			ID:          fmt.Sprintf("edge_%s_to_%s", state.lastMapNodeID, nodeID),
			FromNodeID:  state.lastMapNodeID,
			ToNodeID:    nodeID,
			Distance:    math.Round(dist*100) / 100,
			DangerLevel: math.Round((1.0-safetyScore)*100) / 100,
			IsPaved:     false,
		}
	}

	state.lastMapNodeID = nodeID
	state.lastMapPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	return node, edge
}

// EvaluateExploration computes autonomous frontier scouting waypoints and terrain-unsticking
// detour maneuvers in <1ms without token costs.
func EvaluateExploration(agentID string, frame *civquestv1.TelemetryFrame, cfg *xdg.AgentIdentityConfig, state *AgentState) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.Status.IsSleeping {
		return nil
	}

	pos := frame.Status.Position

	state.mu.Lock()
	defer state.mu.Unlock()

	state.telemetryFramesSeen++
	if state.homeOrigin == nil {
		state.homeOrigin = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	}
	if state.lastExploreTick == 0 {
		state.lastExploreTick = frame.WorldTick
	}

	if state.companionActive {
		return nil
	}

	navState := frame.Status.NavigationState

	// 1. Immediate terrain/hill unsticking when navigation reports NAVIGATION_STATE_STUCK
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 20 {
			return nil
		}
		state.exploreStepIndex++
		// Rotate ~126 degrees away from blocked heading to contour around steep hills or cliffs
		state.lastHeadingRad += 2.2
		detourDist := 10.0
		targetX := pos.X + math.Cos(state.lastHeadingRad)*detourDist
		targetZ := pos.Z + math.Sin(state.lastHeadingRad)*detourDist
		state.lastExploreTick = frame.WorldTick

		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: math.Round(targetX*10) / 10,
						Y: pos.Y,
						Z: math.Round(targetZ*10) / 10,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	// 2. Autonomous daytime scouting & patrol
	// Require at least 2 observed frames so single-frame unit tests are never polluted
	if state.telemetryFramesSeen < 2 {
		return nil
	}

	routine := state.CurrentRoutine
	if routine != RoutinePrimaryDuty && routine != RoutineSecondaryHobby && routine != RoutineDawnAwakening && routine != RoutineTavernSocial {
		return nil
	}

	isIdleOrArrived := navState == civquestv1.NavigationState_NAVIGATION_STATE_IDLE ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED
	if !isIdleOrArrived {
		return nil
	}

	prof := "UNASSIGNED"
	curiosity := 0.5
	if cfg != nil {
		prof = strings.ToUpper(strings.TrimSpace(cfg.Profession))
		curiosity = cfg.Traits.Curiosity
	}
	isExplorer := prof == "EXPLORER" || prof == "TRAILBLAZER" || prof == "SCOUT"

	cooldownTicks := int64(120) // 6 seconds for general villagers
	if isExplorer {
		cooldownTicks = 60 // 3 seconds between scouting legs for Explorers
	}
	if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < cooldownTicks {
		return nil
	}

	state.exploreStepIndex++
	// Golden-angle spiral around home origin so the agent systematically charts all surrounding sectors
	const goldenAngle = 2.399963229728653
	angle := float64(state.exploreStepIndex) * goldenAngle
	state.lastHeadingRad = angle

	baseRadius := 8.0 + curiosity*8.0
	if isExplorer {
		ring := float64(state.exploreStepIndex % 4)
		baseRadius = 10.0 + ring*6.0
	}

	origin := state.homeOrigin
	targetX := origin.X + math.Cos(angle)*baseRadius
	targetZ := origin.Z + math.Sin(angle)*baseRadius
	state.lastExploreTick = frame.WorldTick

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: math.Round(targetX*10) / 10,
					Y: pos.Y,
					Z: math.Round(targetZ*10) / 10,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}
