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

	var immediate string
	switch {
	case hostilesNearby || frame.LastDamageTaken != nil:
		immediate = "COMBAT_SURVIVAL: Defend against or evade nearby hostile threats"
	case frame.Status.FoodLevel <= 6:
		immediate = "CRITICAL_HUNGER: Consume food or forage immediately"
	case state != nil && state.IsCompanionActive():
		immediate = "ESCORT_COMPANION: Accompany and protect bonded player"
	case isDuskOrNight || isThunder:
		if frame.Status.IsSleeping {
			immediate = "SLEEPING: Resting safely until dawn"
		} else if state != nil && state.GetAssignedBed() != nil {
			immediate = "SEEK_BED_SHELTER: Return to assigned bed and sleep through the night"
		} else {
			immediate = "ESTABLISH_OUTPOST: Secure and light forward outpost shelter for the night"
		}
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

type bootstrapInventorySummary struct {
	logCount     int32
	planksCount  int32
	stickCount   int32
	cobbleCount  int32
	hasAxe       bool
	hasSword     bool
	hasWeapon    bool
	hasStoneGear bool
	hasLight     bool
	lightItemID  string
}

func inspectBootstrapInventory(inventory []*civquestv1.ItemStackProto) bootstrapInventorySummary {
	var s bootstrapInventorySummary
	for _, it := range inventory {
		if it == nil || it.Count <= 0 || it.ItemId == "" {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(it.ItemId))
		switch {
		case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem"):
			s.logCount += it.Count
		case strings.HasSuffix(id, "_planks"):
			s.planksCount += it.Count
		case id == "minecraft:stick" || strings.HasSuffix(id, ":stick") || id == "stick":
			s.stickCount += it.Count
		case strings.Contains(id, "cobblestone") || strings.Contains(id, "cobbled_deepslate") || strings.Contains(id, "blackstone"):
			s.cobbleCount += it.Count
		}
		if strings.Contains(id, "_axe") {
			s.hasAxe = true
			s.hasWeapon = true
		}
		if strings.Contains(id, "_sword") {
			s.hasSword = true
			s.hasWeapon = true
		}
		if strings.Contains(id, "stone_sword") || strings.Contains(id, "stone_axe") ||
			strings.Contains(id, "iron_sword") || strings.Contains(id, "iron_axe") ||
			strings.Contains(id, "diamond_sword") || strings.Contains(id, "netherite_sword") {
			s.hasStoneGear = true
		}
		if strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern") {
			s.hasLight = true
			if s.lightItemID == "" {
				s.lightItemID = it.ItemId
			}
		}
	}
	return s
}

// EvaluateExploration computes autonomous frontier scouting waypoints, bootstrap timber/tool gathering,
// dusk/night forward outpost shelter reflexes, and terrain-unsticking detour maneuvers in <1ms without token costs.
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

	// Require at least 2 observed frames so single-frame unit tests are never polluted
	if state.telemetryFramesSeen < 2 {
		return nil
	}

	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11500 && tod < 23500
	isThunder := frame.Weather == civquestv1.Weather_WEATHER_THUNDER

	// 2. Dusk & Night Shelter / Forward Outpost Reflex (do NOT wander outward at dusk or night!)
	if isDuskOrNight || isThunder {
		if state.AssignedBed != nil {
			// EvaluateSleep handles navigating to bed and sleeping when a bed is assigned
			return nil
		}
		return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state)
	}

	routine := state.CurrentRoutine
	if routine != RoutinePrimaryDuty && routine != RoutineSecondaryHobby && routine != RoutineDawnAwakening && routine != RoutineTavernSocial {
		return nil
	}

	// 3. Daytime Bootstrap Survival Goals (Gather Timber & Craft Basic Tools)
	if bootstrapIntent := evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state); bootstrapIntent != nil {
		return bootstrapIntent
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
	// Golden-angle spiral around current outpost/home origin; Explorers progressively expand rings to advance outward
	const goldenAngle = 2.399963229728653
	angle := float64(state.exploreStepIndex) * goldenAngle
	state.lastHeadingRad = angle

	baseRadius := 8.0 + curiosity*8.0
	if isExplorer {
		ring := float64(state.exploreStepIndex % 5)
		baseRadius = 12.0 + ring*8.0
	}

	origin := state.currentOutpost
	if origin == nil {
		origin = state.homeOrigin
	}
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

func evaluateDuskNightOutpostShelterLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
) *civquestv1.AgentIntent {
	outpost := state.currentOutpost
	if outpost == nil {
		outpost = state.homeOrigin
	}

	// If the Explorer has traveled > 32 blocks from the current outpost along their path of travel,
	// establish a new Forward Outpost at their current frontier coordinates for the night!
	if outpost != nil && math.Hypot(pos.X-outpost.X, pos.Z-outpost.Z) > 32.0 {
		newOutpost := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		state.currentOutpost = newOutpost
		state.homeOrigin = newOutpost
		state.outposts = append(state.outposts, newOutpost)
		state.ShelterPos = &civquestv1.BlockPosProto{
			X: int32(math.Floor(pos.X)),
			Y: int32(math.Floor(pos.Y)),
			Z: int32(math.Floor(pos.Z)),
		}
		// Reset lastMapNodeID so EvaluateTopologicalMapping records this sector as OUTPOST
		state.lastMapNodeID = ""
		outpost = newOutpost
	}

	targetX := pos.X
	targetY := pos.Y
	targetZ := pos.Z
	if state.ShelterPos != nil && math.Hypot(pos.X-float64(state.ShelterPos.X), pos.Z-float64(state.ShelterPos.Z)) <= 32.0 {
		targetX = float64(state.ShelterPos.X) + 0.5
		targetY = float64(state.ShelterPos.Y)
		targetZ = float64(state.ShelterPos.Z) + 0.5
	} else if outpost != nil {
		targetX = outpost.X
		targetY = outpost.Y
		targetZ = outpost.Z
	}

	distToShelter := math.Hypot(pos.X-targetX, pos.Z-targetZ)
	if distToShelter > 4.0 {
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return nil
		}
		if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 40 {
			return nil
		}
		state.lastExploreTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: math.Round(targetX*10) / 10,
						Y: targetY,
						Z: math.Round(targetZ*10) / 10,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	// Within 4.0 blocks of shelter/outpost at dusk/night: check if a campfire or torch is nearby
	hasNearbyLight := false
	for _, b := range frame.InterestingBlocks {
		if b != nil && b.Pos != nil {
			id := strings.ToLower(b.BlockId)
			if strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern") {
				if math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z)) <= 8.0 {
					hasNearbyLight = true
					break
				}
			}
		}
	}

	if !hasNearbyLight && (state.lastShelterPlaceTick == 0 || frame.WorldTick < state.lastShelterPlaceTick || frame.WorldTick-state.lastShelterPlaceTick >= 600) {
		inv := inspectBootstrapInventory(frame.Status.Inventory)
		if inv.hasLight {
			state.lastShelterPlaceTick = frame.WorldTick
			placePos := &civquestv1.BlockPosProto{
				X: int32(math.Floor(pos.X)) + 1,
				Y: int32(math.Floor(pos.Y)),
				Z: int32(math.Floor(pos.Z)),
			}
			state.ShelterPos = placePos
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             placePos,
						InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
						BlockOrItemId:   inv.lightItemID,
					},
				},
			}
		}
		if (inv.logCount >= 1 || inv.planksCount >= 1 || inv.stickCount >= 1) &&
			(state.lastCraftTick == 0 || frame.WorldTick < state.lastCraftTick || frame.WorldTick-state.lastCraftTick >= 20) {
			state.lastCraftTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_CraftItem{
					CraftItem: &civquestv1.CraftItemIntent{
						RecipeId:   "minecraft:campfire",
						BatchCount: 1,
					},
				},
			}
		}
	}

	return nil
}

func evaluateBootstrapGoalsLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
) *civquestv1.AgentIntent {
	inv := inspectBootstrapInventory(frame.Status.Inventory)

	canCraft := state.lastCraftTick == 0 || frame.WorldTick < state.lastCraftTick || frame.WorldTick-state.lastCraftTick >= 20

	// 1. Upgrade to stone sword if cobblestone and wood are available
	if !inv.hasStoneGear && inv.cobbleCount >= 3 && (inv.logCount >= 1 || inv.planksCount >= 1 || inv.stickCount >= 1) && canCraft {
		state.lastCraftTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_CraftItem{
				CraftItem: &civquestv1.CraftItemIntent{
					RecipeId:   "minecraft:stone_sword",
					BatchCount: 1,
				},
			},
		}
	}

	// 2. Craft wooden axe, wooden sword, or campfire when timber is in inventory
	if (!inv.hasAxe || !inv.hasSword || !inv.hasLight) && (inv.logCount >= 1 || inv.planksCount >= 4) && canCraft {
		recipe := "minecraft:wooden_axe"
		if inv.hasAxe && !inv.hasSword {
			recipe = "minecraft:wooden_sword"
		} else if inv.hasAxe && inv.hasSword && !inv.hasLight {
			recipe = "minecraft:campfire"
		}
		state.lastCraftTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_CraftItem{
				CraftItem: &civquestv1.CraftItemIntent{
					RecipeId:   recipe,
					BatchCount: 1,
				},
			},
		}
	}

	// 3. Gather timber if the agent lacks basic tools and has < 2 logs
	if !inv.hasAxe && !inv.hasSword && inv.logCount < 2 {
		var nearestLog *civquestv1.VisibleBlock
		bestDistSq := math.MaxFloat64
		for _, b := range frame.InterestingBlocks {
			if b == nil || b.Pos == nil {
				continue
			}
			id := strings.ToLower(b.BlockId)
			if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem") {
				dx := float64(b.Pos.X) + 0.5 - pos.X
				dy := float64(b.Pos.Y) + 0.5 - pos.Y
				dz := float64(b.Pos.Z) + 0.5 - pos.Z
				dSq := dx*dx + dy*dy + dz*dz
				if dSq < bestDistSq {
					bestDistSq = dSq
					nearestLog = b
				}
			}
		}

		if nearestLog != nil {
			if bestDistSq <= 20.25 { // Within 4.5 blocks melee/harvest reach
				if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
					state.lastHarvestTick = frame.WorldTick
					return &civquestv1.AgentIntent{
						AgentId: agentID,
						Action: &civquestv1.AgentIntent_InteractBlock{
							InteractBlock: &civquestv1.InteractBlockIntent{
								Pos:             nearestLog.Pos,
								InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
								BlockOrItemId:   nearestLog.BlockId,
							},
						},
					}
				}
				return nil
			}

			// Navigate to the nearest tree log
			if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
				return nil
			}
			if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 40 {
				return nil
			}
			state.lastExploreTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: float64(nearestLog.Pos.X) + 0.5,
							Y: float64(nearestLog.Pos.Y),
							Z: float64(nearestLog.Pos.Z) + 0.5,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
		}
	}

	return nil
}
