package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
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

	if len(hostiles) == 0 && isDrowningDamage(frame) {
		return "DROWNING — agent is drowning in water; immediate surfacing required.", 999.0, nil
	}

	if len(hostiles) == 0 && (frame.LastDamageTaken == nil || IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken)) {
		return "SAFE — 0 hostile mobs within 24 blocks; immediate area is clear and safe.", 999.0, nil
	}

	if len(hostiles) == 0 && frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
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
		originCopy := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		state.homeOrigin = originCopy
		state.currentOutpost = originCopy
		if len(state.outposts) == 0 {
			state.outposts = append(state.outposts, originCopy)
		}
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

	isForwardOutpost := false
	outpostIdx := 0
	for idx, op := range state.outposts {
		if idx > 0 && op != nil && math.Hypot(pos.X-op.X, pos.Z-op.Z) <= 8.0 {
			isForwardOutpost = true
			outpostIdx = idx + 1
			break
		}
	}

	category := "WAYPOINT"
	label := fmt.Sprintf("Frontier Sector (%d, %d)", gx, gz)
	if isForwardOutpost {
		category = "OUTPOST"
		label = fmt.Sprintf("Forward Outpost #%d (%d, %d)", outpostIdx, gx, gz)
		safetyScore = math.Max(safetyScore, 0.90)
	} else if state.lastMapNodeID == "" {
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

// EvaluateGoals computes the 3-horizon GoalHierarchy (IMMEDIATE, SHORT_TERM, LONG_TERM)
// deterministically from the agent's current sensory frame, inventory, and profession.
func EvaluateGoals(frame *civquestv1.TelemetryFrame, cfg *xdg.AgentIdentityConfig, state *AgentState) GoalHierarchy {
	prof := "EXPLORER"
	if cfg != nil && strings.TrimSpace(cfg.Profession) != "" {
		prof = strings.ToUpper(strings.TrimSpace(cfg.Profession))
	}

	var longTerm string
	switch prof {
	case "EXPLORER", "TRAILBLAZER", "SCOUT":
		longTerm = "CHART_REGIONAL_ATLAS: Map biome sectors, consecrate landmarks, and link forward outposts for Clan Cartographer"
	case "BUILDER", "ARCHITECT":
		longTerm = "CONSTRUCT_SETTLEMENT_QUADRANTS: Build bottom-up clan halls, workshops, and fortifications"
	case "FARMER":
		longTerm = "CULTIVATE_CLAN_GRANARY: Expand irrigated farmland and maintain clan food surplus"
	case "CHRONICLER", "HISTORIAN":
		longTerm = "ARCHIVE_CLAN_HISTORIES: Author written chronicles and preserve dynastic lore"
	default:
		longTerm = "BUILD_PROSPEROUS_CLAN: Gather resources, fortify shelter, and support settlement growth"
	}

	if frame == nil || frame.Status == nil {
		g := GoalHierarchy{
			ImmediateGoal: "GATHER_TIMBER: Harvest nearby tree logs to craft basic survival tools",
			ShortTermGoal: "BOOTSTRAP_SURVIVAL_KIT: Gather timber, craft wooden axe & sword, and secure initial night shelter",
			LongTermGoal:  longTerm,
		}
		if state != nil {
			state.SetGoals(g)
		}
		return g
	}

	inv := inspectBootstrapInventory(frame.Status.Inventory)
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11500 && tod < 23500
	isThunder := frame.Weather == civquestv1.Weather_WEATHER_THUNDER

	var shortTerm string
	if !inv.hasWeapon && !inv.hasAxe {
		shortTerm = "BOOTSTRAP_SURVIVAL_KIT: Gather timber, craft wooden axe & sword, and secure initial night shelter"
	} else if !inv.hasStoneGear {
		shortTerm = "UPGRADE_TO_STONE_GEAR: Acquire cobblestone, upgrade to stone tools, and stockpile torches/campfire"
	} else {
		shortTerm = "ESTABLISH_OUTPOST_CHAIN: Advance frontier and establish safe day-night outposts with tools & light"
	}

	hostilesNearby := false
	for _, e := range frame.VisibleEntities {
		if IsHostile(e) {
			hostilesNearby = true
			break
		}
	}

	_, _, hasEdibleFood := FindOptimalFood(frame.Status.Inventory)
	var immediate string
	switch {
	case hostilesNearby || (frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken)):
		immediate = "COMBAT_SURVIVAL: Defend against or evade nearby hostile threats"
	case frame.Status.FoodLevel <= 12 && !hasEdibleFood && !frame.Status.IsSleeping:
		immediate = ForagingGoalText
	case frame.Status.FoodLevel <= 6:
		immediate = "CRITICAL_HUNGER: Consume food or forage immediately"
	case state != nil && state.IsCompanionActive():
		immediate = "ESCORT_COMPANION: Accompany and protect bonded player"
	case isDuskOrNight || isThunder:
		if frame.Status.IsSleeping {
			immediate = "SLEEPING: Resting safely until dawn"
		} else if state != nil && state.GetAssignedBed() != nil {
			immediate = "SEEK_BED_SHELTER: Return to assigned bed and sleep through the night"
		} else if inv.buildingBlockCount > 0 && (state == nil || state.GetShelterBuildIndex() < len(blueprints.FrontierCabinBlueprint())) {
			immediate = "BUILD_SHELTER: Construct protective night shelter and light source from carried blocks"
		} else {
			immediate = "ESTABLISH_OUTPOST: Secure and light forward outpost shelter for the night"
		}
	case state != nil && func() bool {
		_, active := state.ActiveStrategicDirective(frame.WorldTick)
		return active
	}():
		dir, _ := state.ActiveStrategicDirective(frame.WorldTick)
		switch {
		case dir.ImmediateGoal != "":
			immediate = dir.ImmediateGoal
		case dir.Target != "":
			immediate = dir.Action + ": " + dir.Target
		default:
			immediate = dir.Action
		}
	case state != nil && func() bool {
		active, kind, _, _ := state.ActiveTaskInfo()
		return active && (kind == "harvest_tree" || kind == "collect_items")
	}():
		immediate = "GATHER_TIMBER: Harvest committed tree trunk logs and collect dropped wood"
	case !inv.hasWeapon && !inv.hasAxe:
		if inv.logCount < 2 && inv.planksCount < 4 {
			immediate = "GATHER_TIMBER: Harvest nearby tree logs to craft basic survival tools"
		} else {
			immediate = "CRAFT_BASIC_TOOLS: Craft wooden axe, wooden sword, and campfire from gathered timber"
		}
	default:
		immediate = "SCOUT_FRONTIER: Chart unexplored terrain sectors and advance toward next forward outpost"
	}

	g := GoalHierarchy{
		ImmediateGoal: immediate,
		ShortTermGoal: shortTerm,
		LongTermGoal:  longTerm,
	}
	if state != nil {
		state.SetGoals(g)
	}
	return g
}

// EvaluateExploration computes autonomous frontier scouting waypoints, bootstrap timber/tool gathering,
// dusk/night forward outpost shelter reflexes, and terrain-unsticking detour maneuvers in <1ms without token costs.
// It serves as a unified coordinator and backward-compatible entry point for System 1 exploration.
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
		originCopy := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		state.homeOrigin = originCopy
		state.currentOutpost = originCopy
		if len(state.outposts) == 0 {
			state.outposts = append(state.outposts, originCopy)
		}
	}
	if state.currentOutpost == nil {
		state.currentOutpost = &civquestv1.Vec3{X: state.homeOrigin.X, Y: state.homeOrigin.Y, Z: state.homeOrigin.Z}
	}
	if state.lastExploreTick == 0 {
		state.lastExploreTick = frame.WorldTick
	}

	if state.companionActive {
		return nil
	}

	// Hold steady while a 360-degree horizon survey is executing on the motor stack (ADR-032).
	if state.isExpeditionSurveyingLocked(frame.WorldTick) {
		return nil
	}

	navState := frame.Status.NavigationState
	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11500 && tod < 23500
	isThunder := frame.Weather == civquestv1.Weather_WEATHER_THUNDER
	if !isDuskOrNight && !isThunder {
		state.shelterBlocksPlaced = 0
		state.ShelterBuildPhase = 0
		state.ShelterBuildIndex = 0
	}

	// 1. Immediate terrain/hill unsticking or tool-aware dig-out when navigation reports NAVIGATION_STATE_STUCK or ENCLOSED
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
		if unstuckIntent := EvaluateUnstuck(agentID, frame, pos, state); unstuckIntent != nil {
			return unstuckIntent
		}
		if locked, _, _, _, _, _ := state.GetUnstuckLockLocked(); locked {
			return nil // Waiting on excavation sequence, do NOT rotate heading
		}
		if (isDuskOrNight || isThunder) && state.AssignedBed == nil {
			if shelterIntent := evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state); shelterIntent != nil {
				return shelterIntent
			}
		}
		if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 20 {
			return nil
		}
		state.exploreStepIndex++
		// Rotate ~126 degrees away from blocked heading to contour around steep hills or cliffs
		state.lastHeadingRad += 2.2
		state.lastHeadingRad = math.Atan2(math.Sin(state.lastHeadingRad), math.Cos(state.lastHeadingRad))
		state.frontierAzimuthRad = state.lastHeadingRad
		state.frontierRadialLeg = 0
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

	// Require at least 2 observed frames so single-frame unit tests are never polluted
	if state.telemetryFramesSeen < 2 {
		return nil
	}

	// 2. Dusk & Night Shelter / Forward Outpost Reflex (do NOT wander outward at dusk or night!)
	if isDuskOrNight || isThunder {
		if state.AssignedBed != nil {
			// EvaluateSleep handles navigating to bed and sleeping when a bed is assigned
			return nil
		}
		if state.lastShelterTimeoutTick > 0 && (frame.WorldTick-state.lastShelterTimeoutTick) < 600 {
			if bootstrapIntent := evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state); bootstrapIntent != nil {
				return bootstrapIntent
			}
			return nil
		}
		return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state)
	}

	routine := state.CurrentRoutine
	if !state.HasActiveTaskLocked() &&
		routine != RoutinePrimaryDuty && routine != RoutineSecondaryHobby &&
		routine != RoutineDawnAwakening && routine != RoutineTavernSocial &&
		routine != RoutineHarvestTree && routine != RoutineCollectItems {
		return nil
	}

	// 3. Daytime Bootstrap Survival Goals (Gather Timber & Craft Basic Tools)
	if bootstrapIntent := evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state); bootstrapIntent != nil {
		return bootstrapIntent
	}
	if state.HasActiveTaskLocked() || state.navigatingToPOI {
		// Suppress terrain and frontier exploration while a committed TaskSubroutine or POI recall navigation is active
		return nil
	}

	// 4. Terrain-Aware Exploration Strategies (Valleys, Ridges, Fords, Rims, Flatlands, Canopy Trailblazing)
	if state.currentExploreTarget == nil || navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED ||
		math.Hypot(pos.X-state.currentExploreTarget.X, pos.Z-state.currentExploreTarget.Z) <= 2.0 {
		if terrainIntent := EvaluateTerrainExploration(agentID, frame, pos, state, cfg); terrainIntent != nil {
			if nav := terrainIntent.GetNavigate(); nav != nil && nav.Target != nil {
				state.currentExploreTarget = nav.Target
				state.lastExploreProgressDist = math.Hypot(pos.X-nav.Target.X, pos.Z-nav.Target.Z)
				state.lastExploreProgressTick = frame.WorldTick
			}
			return terrainIntent
		}
	}

	// 5. Sector-locked radial frontier scouting or patrol
	return evaluateGeneralExplorationLocked(agentID, frame, pos, state, cfg)
}

// EvaluateGeneralExploration computes sector-locked radial frontier scouting or settlement perimeter patrol.
// Thin backward-compatible wrapper dispatching to scouting or patrol depending on agent profession.
func EvaluateGeneralExploration(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	if frame == nil || pos == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return evaluateGeneralExplorationLocked(agentID, frame, pos, state, cfg)
}

func evaluateGeneralExplorationLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	prof := "UNASSIGNED"
	if cfg != nil {
		prof = strings.ToUpper(strings.TrimSpace(cfg.Profession))
	}
	isExplorer := prof == "EXPLORER" || prof == "TRAILBLAZER" || prof == "SCOUT"
	if isExplorer {
		if state.telemetryFramesSeen < 2 {
			return nil
		}
		if state.ExpeditionPhase == ExpeditionIdle &&
			(frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
				(state.lastExploreTick > 0 && frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 40)) {
			return nil
		}
		return EvaluateHorizonExplorerLocked(frame, state, cfg, agentID)
	}
	return evaluatePatrolLocked(agentID, frame, pos, state, cfg)
}

func fnv1aString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
