package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Canonical System 2 -> System 1 directive action names (matching system2.Directive*, ADR-031).
const (
	DirectiveActionGatherTimber     = "GATHER_TIMBER"
	DirectiveActionMineStone        = "MINE_STONE"
	DirectiveActionForageFood       = "FORAGE_FOOD"
	DirectiveActionCraftSurvivalKit = "CRAFT_SURVIVAL_KIT"
	DirectiveActionBuildShelter     = "BUILD_SHELTER"
	DirectiveActionScoutFrontier    = "SCOUT_FRONTIER"
	DirectiveActionSocialize        = "SOCIALIZE"
)

const (
	directiveActionCooldownTicks = int64(20)
	directiveNavCooldownTicks    = int64(40)
	directiveReachRadius         = 4.5
)

// DirectiveEvaluator publishes active System 2 strategic directives into the System 1 FactGraph (ADR-031).
type DirectiveEvaluator struct{}

// NewDirectiveEvaluator creates a DirectiveEvaluator instance.
func NewDirectiveEvaluator() *DirectiveEvaluator {
	return &DirectiveEvaluator{}
}

func (e *DirectiveEvaluator) Name() string {
	return "directive"
}

func (e *DirectiveEvaluator) Category() string {
	return "system2"
}

func (e *DirectiveEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	var worldTick int64
	if ctx != nil && ctx.Frame != nil {
		worldTick = ctx.Frame.WorldTick
	}

	var dir StrategicDirectiveState
	var active bool
	if ctx != nil && ctx.State != nil {
		dir, active = ctx.State.ActiveStrategicDirective(worldTick)
	}

	if !active {
		graph.SetBool("system2.directive_active", "system2", false, "No active System 2 directive")
		graph.SetString("system2.directive_action", "system2", "", "No directive action")
		graph.SetString("system2.directive_target", "system2", "", "No directive target")
		graph.SetString("system2.immediate_goal", "system2", "", "No System 2 immediate goal")
		return nil
	}

	goalLabel := dir.ImmediateGoal
	if goalLabel == "" {
		if dir.Target != "" {
			goalLabel = dir.Action + ": " + dir.Target
		} else {
			goalLabel = dir.Action
		}
	}

	graph.Set(Fact{
		Key:         "system2.directive_active",
		Category:    "system2",
		Value:       FactValue{Bool: true},
		Confidence:  1.0,
		Evidence:    fmt.Sprintf("Active directive %s (expires tick %d)", dir.Action, dir.ExpiresTick),
		ExpiresTick: dir.ExpiresTick,
	})
	graph.Set(Fact{
		Key:         "system2.directive_action",
		Category:    "system2",
		Value:       FactValue{String: dir.Action},
		Confidence:  1.0,
		Evidence:    fmt.Sprintf("Directive action: %s", dir.Action),
		ExpiresTick: dir.ExpiresTick,
	})
	graph.Set(Fact{
		Key:         "system2.directive_target",
		Category:    "system2",
		Value:       FactValue{String: dir.Target},
		Confidence:  1.0,
		Evidence:    fmt.Sprintf("Directive target: %s", dir.Target),
		ExpiresTick: dir.ExpiresTick,
	})
	graph.Set(Fact{
		Key:         "system2.immediate_goal",
		Category:    "system2",
		Value:       FactValue{String: goalLabel},
		Confidence:  1.0,
		Evidence:    fmt.Sprintf("System 2 immediate goal: %s", goalLabel),
		ExpiresTick: dir.ExpiresTick,
	})
	return nil
}

// EvaluateStrategicDirective executes the active System 2 strategic directive deterministically
// in <5ms with zero token cost, yielding immediately to survival, emergency, combat, hunger, or sleep reflexes.
func EvaluateStrategicDirective(
	frame *civquestv1.TelemetryFrame,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	agentID := ""
	if frame != nil && frame.Status != nil && frame.Status.AgentId != "" {
		agentID = frame.Status.AgentId
	} else if cfg != nil {
		agentID = cfg.Name
	}
	return EvaluateStrategicDirectiveWithID(agentID, frame, state, cfg)
}

// EvaluateStrategicDirectiveWithID executes the active System 2 strategic directive with an explicit agentID.
func EvaluateStrategicDirectiveWithID(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.DeathEvent != nil || frame.Status.Health <= 0 || frame.Status.IsSleeping {
		return nil
	}
	if len(frame.IncomingChats) > 0 || frame.PendingTrade != nil {
		return nil
	}
	if state.IsCompanionActive() {
		return nil
	}

	// Survival reflexes ALWAYS take priority over strategic directives.
	if hasSurvivalPreemption(frame) {
		return nil
	}

	tod := ((frame.TimeOfDay % 24000) + 24000) % 24000
	isDuskOrNight := tod >= 11500 && tod < 23500
	if isDuskOrNight || frame.Weather == civquestv1.Weather_WEATHER_THUNDER || state.Routine() == RoutineSleep {
		return nil
	}

	dir, active := state.ActiveStrategicDirective(frame.WorldTick)
	if !active {
		return nil
	}

	if agentID == "" {
		if frame.Status.AgentId != "" {
			agentID = frame.Status.AgentId
		} else if cfg != nil {
			agentID = cfg.Name
		}
	}

	pos := frame.Status.Position
	navState := frame.Status.NavigationState

	// Terrain unsticking takes precedence if stuck/enclosed while executing a directive.
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
		if unstuckIntent := EvaluateUnstuck(agentID, frame, pos, state); unstuckIntent != nil {
			return unstuckIntent
		}
	}

	switch dir.Action {
	case DirectiveActionGatherTimber:
		return evaluateDirectiveGatherTimber(agentID, frame, pos, navState, state, dir)
	case DirectiveActionMineStone:
		return evaluateDirectiveMineStone(agentID, frame, pos, navState, state, dir)
	case DirectiveActionForageFood:
		return EvaluateForagingDirective(agentID, frame, state)
	case DirectiveActionCraftSurvivalKit:
		return EvaluateBootstrapGoals(agentID, frame, cfg, state)
	case DirectiveActionBuildShelter:
		inv := inspectBootstrapInventory(frame.Status.Inventory)
		if inv.buildingBlockCount == 0 {
			return evaluateDirectiveGatherTimber(agentID, frame, pos, navState, state, dir)
		}
		return EvaluateShelterGoals(agentID, frame, cfg, state)
	case DirectiveActionScoutFrontier:
		return evaluateDirectiveScoutFrontier(agentID, frame, pos, navState, state, dir)
	case DirectiveActionSocialize:
		return evaluateDirectiveSocialize(agentID, frame, pos, navState, state, cfg)
	default:
		return nil
	}
}

func hasSurvivalPreemption(frame *civquestv1.TelemetryFrame) bool {
	if frame == nil || frame.Status == nil {
		return true
	}
	for _, e := range frame.VisibleEntities {
		if e != nil && IsHostile(e) {
			return true
		}
	}
	if frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
		return true
	}
	if isDrowningDamage(frame) {
		return true
	}
	if frame.Status.Health <= 6.0 {
		return true
	}
	if frame.Status.FoodLevel <= 6 {
		return true
	}
	if NeedsForaging(frame) {
		return true
	}
	return false
}

func evaluateDirectiveGatherTimber(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	dir StrategicDirectiveState,
) *civquestv1.AgentIntent {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.activeTask != nil {
		kind := state.activeTask.Spec.Kind
		if kind == "harvest_tree" || kind == "collect_items" {
			stepRes := state.StepActiveTaskLocked(agentID, frame)
			if stepRes.Active {
				return stepRes.Intent
			}
		}
	}

	targetLog, recalledTreePOI := findNearestTreeTrunk(agentID, frame, pos, state)
	if targetLog != nil {
		treeTask := NewHarvestTreeTask(targetLog.Pos.X, targetLog.Pos.Y, targetLog.Pos.Z, state.stage, frame.WorldTick)
		state.StartTaskLocked(treeTask)
		stepRes := state.StepActiveTaskLocked(agentID, frame)
		if stepRes.Active {
			return stepRes.Intent
		}
		return nil
	}
	if recalledTreePOI != nil {
		bpos := poiBlockPos(recalledTreePOI)
		dx := recalledTreePOI.X - pos.X
		dy := recalledTreePOI.Y + 0.5 - pos.Y
		dz := recalledTreePOI.Z - pos.Z
		if dx*dx+dy*dy+dz*dz <= 12.25 {
			if si := state.SpatialIndexLocked(); si != nil {
				si.RemovePOI(recalledTreePOI.POIID)
			}
		} else if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING &&
			canDispatchDirectiveNavLocked(state, frame.WorldTick, "poi:"+recalledTreePOI.POIID) {
			state.lastDirectiveActionTick = frame.WorldTick
			state.lastDirectiveNavKey = "poi:" + recalledTreePOI.POIID
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target:           poiAccessTarget(recalledTreePOI),
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
		} else if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
			state.MarkTreeFailedLocked(bpos.X, bpos.Z, frame.WorldTick)
			return nil
		}
		return nil
	}

	return evaluateDirectiveScoutOutwardLocked(agentID, frame, pos, navState, state, dir.IssuedTick)
}

func evaluateDirectiveMineStone(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	dir StrategicDirectiveState,
) *civquestv1.AgentIntent {
	state.mu.Lock()
	defer state.mu.Unlock()

	bestBlock, bestDistSq := findNearestMineableStoneBlock(frame, pos)
	if bestBlock != nil {
		dy := math.Abs(float64(bestBlock.Pos.Y) + 0.5 - pos.Y)
		if bestDistSq <= directiveReachRadius*directiveReachRadius && dy <= 3.0 {
			if state.lastDirectiveActionTick > 0 && frame.WorldTick >= state.lastDirectiveActionTick &&
				frame.WorldTick-state.lastDirectiveActionTick < directiveActionCooldownTicks {
				return nil
			}
			state.lastDirectiveActionTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             cloneBlockPos(bestBlock.Pos),
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   bestBlock.BlockId,
					},
				},
			}
		}

		navKey := fmt.Sprintf("mine:%d,%d,%d", bestBlock.Pos.X, bestBlock.Pos.Y, bestBlock.Pos.Z)
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.lastDirectiveNavKey == navKey {
			return nil
		}
		if canDispatchDirectiveNavLocked(state, frame.WorldTick, navKey) {
			state.lastDirectiveActionTick = frame.WorldTick
			state.lastDirectiveNavKey = navKey
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: float64(bestBlock.Pos.X) + 0.5,
							Y: float64(bestBlock.Pos.Y),
							Z: float64(bestBlock.Pos.Z) + 0.5,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
		}
		return nil
	}

	if si := state.SpatialIndexLocked(); si != nil {
		if minePOI := findNearestMineResourcePOI(si, pos); minePOI != nil {
			dx := minePOI.X - pos.X
			dy := minePOI.Y + 0.5 - pos.Y
			dz := minePOI.Z - pos.Z
			if dx*dx+dy*dy+dz*dz <= 12.25 {
				si.RemovePOI(minePOI.POIID)
			} else if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING &&
				canDispatchDirectiveNavLocked(state, frame.WorldTick, "poi:"+minePOI.POIID) {
				state.lastDirectiveActionTick = frame.WorldTick
				state.lastDirectiveNavKey = "poi:" + minePOI.POIID
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_Navigate{
						Navigate: &civquestv1.NavigateIntent{
							Target:           poiAccessTarget(minePOI),
							SpeedMultiplier:  1.0,
							ArrivalTolerance: 2,
						},
					},
				}
			} else {
				return nil
			}
		}
	}

	return evaluateDirectiveScoutOutwardLocked(agentID, frame, pos, navState, state, dir.IssuedTick)
}

func evaluateDirectiveScoutFrontier(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	dir StrategicDirectiveState,
) *civquestv1.AgentIntent {
	state.mu.Lock()
	if state.directiveScoutTarget == nil {
		state.directiveScoutTarget = computeDirectiveFrontierWaypoint(agentID, dir.IssuedTick, pos)
	}
	target := &civquestv1.Vec3{
		X: state.directiveScoutTarget.X,
		Y: state.directiveScoutTarget.Y,
		Z: state.directiveScoutTarget.Z,
	}
	dist := math.Hypot(pos.X-target.X, pos.Z-target.Z)
	if dist <= 4.0 || (navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED && dist <= 6.0) {
		state.strategicDirective = nil
		state.directiveScoutTarget = nil
		state.lastDirectiveActionTick = 0
		state.lastDirectiveNavKey = ""
		state.mu.Unlock()
		return nil
	}

	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.lastDirectiveNavKey == "scout_frontier" {
		state.mu.Unlock()
		return nil
	}
	if !canDispatchDirectiveNavLocked(state, frame.WorldTick, "scout_frontier") {
		state.mu.Unlock()
		return nil
	}
	state.lastDirectiveActionTick = frame.WorldTick
	state.lastDirectiveNavKey = "scout_frontier"
	state.mu.Unlock()

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:           target,
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}

func evaluateDirectiveSocialize(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	selfName := agentID
	if cfg != nil && cfg.Name != "" {
		selfName = cfg.Name
	}

	var bestPeer *civquestv1.VisibleEntity
	bestPeerDist := math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if e == nil || e.Position == nil || IsHostile(e) || e.IsTamed {
			continue
		}
		if (agentID != "" && strings.EqualFold(e.DisplayName, agentID)) ||
			(selfName != "" && strings.EqualFold(e.DisplayName, selfName)) {
			continue
		}
		et := strings.ToLower(e.EntityType)
		if !strings.Contains(et, "player") && !strings.Contains(et, "agent") {
			continue
		}
		d := math.Hypot(e.Position.X-pos.X, e.Position.Z-pos.Z)
		if d < bestPeerDist {
			bestPeerDist = d
			bestPeer = e
		}
	}

	var anchor *civquestv1.Vec3
	var distToAnchor float64
	var navKey string

	if bestPeer != nil {
		anchor = bestPeer.Position
		distToAnchor = bestPeerDist
		navKey = fmt.Sprintf("social_peer:%d", bestPeer.EntityId)
	} else {
		// Fallback to visible campfire or SpatialIndex campfire/habitation POI
		for _, b := range frame.InterestingBlocks {
			if b != nil && b.Pos != nil && strings.Contains(strings.ToLower(b.BlockId), "campfire") {
				d := math.Hypot(float64(b.Pos.X)+0.5-pos.X, float64(b.Pos.Z)+0.5-pos.Z)
				if anchor == nil || d < distToAnchor {
					anchor = &civquestv1.Vec3{
						X: float64(b.Pos.X) + 0.5,
						Y: float64(b.Pos.Y),
						Z: float64(b.Pos.Z) + 0.5,
					}
					distToAnchor = d
					navKey = fmt.Sprintf("social_fire:%d,%d,%d", b.Pos.X, b.Pos.Y, b.Pos.Z)
				}
			}
		}
		if anchor == nil {
			if si := state.SpatialIndex(); si != nil {
				if poi := si.FindNearestPOI(pos, POICategoryHabitation, "campfire", 64.0, ""); poi != nil {
					anchor = &civquestv1.Vec3{X: poi.X, Y: poi.Y, Z: poi.Z}
					distToAnchor = math.Hypot(poi.X-pos.X, poi.Z-pos.Z)
					navKey = "social_poi:" + poi.POIID
				}
			}
		}
	}

	if anchor == nil {
		return nil
	}
	// Already within 4-6m social distance
	if distToAnchor <= 6.0 {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.lastDirectiveNavKey == navKey {
		return nil
	}
	if !canDispatchDirectiveNavLocked(state, frame.WorldTick, navKey) {
		return nil
	}
	state.lastDirectiveActionTick = frame.WorldTick
	state.lastDirectiveNavKey = navKey

	// Stand 4.5m from peer/campfire along the approach line so we stop within 4-6m.
	standoff := 4.5
	ux := (pos.X - anchor.X) / distToAnchor
	uz := (pos.Z - anchor.Z) / distToAnchor
	targetX := math.Round((anchor.X+ux*standoff)*10) / 10
	targetZ := math.Round((anchor.Z+uz*standoff)*10) / 10

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: targetX,
					Y: anchor.Y,
					Z: targetZ,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}

func evaluateDirectiveScoutOutwardLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	issuedTick int64,
) *civquestv1.AgentIntent {
	if state.directiveScoutTarget == nil ||
		math.Hypot(pos.X-state.directiveScoutTarget.X, pos.Z-state.directiveScoutTarget.Z) <= 4.0 ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
		state.directiveScoutTarget = computeDirectiveFrontierWaypoint(agentID, issuedTick+int64(state.exploreStepIndex), pos)
		state.exploreStepIndex++
	}
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && state.lastDirectiveNavKey == "scout_outward" {
		return nil
	}
	if !canDispatchDirectiveNavLocked(state, frame.WorldTick, "scout_outward") {
		return nil
	}
	state.lastDirectiveActionTick = frame.WorldTick
	state.lastDirectiveNavKey = "scout_outward"
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: state.directiveScoutTarget.X,
					Y: state.directiveScoutTarget.Y,
					Z: state.directiveScoutTarget.Z,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}

func computeDirectiveFrontierWaypoint(agentID string, seedTick int64, pos *civquestv1.Vec3) *civquestv1.Vec3 {
	h := fnv1aString(fmt.Sprintf("%s:%d", agentID, seedTick))
	azimuth := float64(h%360) * (math.Pi / 180.0)
	dist := 24.0 + float64((h>>8)%13) // [24.0, 36.0] blocks
	return &civquestv1.Vec3{
		X: math.Round((pos.X+math.Cos(azimuth)*dist)*10) / 10,
		Y: pos.Y,
		Z: math.Round((pos.Z+math.Sin(azimuth)*dist)*10) / 10,
	}
}

func canDispatchDirectiveNavLocked(state *AgentState, worldTick int64, navKey string) bool {
	if state.lastDirectiveNavKey != navKey || state.lastDirectiveActionTick == 0 || worldTick < state.lastDirectiveActionTick {
		return true
	}
	return worldTick-state.lastDirectiveActionTick >= directiveNavCooldownTicks
}

func isMineableStoneBlockID(raw string) bool {
	id := bareID(raw)
	switch id {
	case "coal_ore", "deepslate_coal_ore",
		"iron_ore", "deepslate_iron_ore",
		"copper_ore", "deepslate_copper_ore",
		"stone", "cobblestone", "andesite", "diorite", "granite",
		"deepslate", "cobbled_deepslate":
		return true
	default:
		return false
	}
}

func findNearestMineableStoneBlock(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) (*civquestv1.VisibleBlock, float64) {
	if frame == nil || pos == nil {
		return nil, math.MaxFloat64
	}
	footX := int32(math.Floor(pos.X))
	footY := int32(math.Floor(pos.Y))
	footZ := int32(math.Floor(pos.Z))

	var best *civquestv1.VisibleBlock
	bestDistSq := math.MaxFloat64
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !isMineableStoneBlockID(b.BlockId) {
			continue
		}
		// Never mine directly underneath own feet
		if b.Pos.X == footX && b.Pos.Z == footZ && b.Pos.Y <= footY-1 {
			continue
		}
		dx := float64(b.Pos.X) + 0.5 - pos.X
		dy := float64(b.Pos.Y) + 0.5 - pos.Y
		dz := float64(b.Pos.Z) + 0.5 - pos.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq < bestDistSq {
			bestDistSq = dSq
			best = b
		}
	}
	return best, bestDistSq
}

func findNearestMineResourcePOI(si *SpatialIndex, pos *civquestv1.Vec3) *db.AgentPOI {
	if si == nil || pos == nil {
		return nil
	}
	for _, sub := range []string{"coal_ore", "iron_ore", "stone", "ore", "cobblestone"} {
		if poi := si.FindNearestPOI(pos, POICategoryResource, sub, 64.0, ""); poi != nil && poi.Reliability >= 0.5 {
			return poi
		}
	}
	return nil
}

// canExecuteImmediateBootstrapCraft returns true if the agent can immediately execute a Recipe Graph
// craft step (or place a carried crafting table) without harvesting or wandering.
func canExecuteImmediateBootstrapCraft(frame *civquestv1.TelemetryFrame, state *AgentState) bool {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return false
	}
	pos := frame.Status.Position
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	invMap := crafting.InventoryMapFromProtos(frame.Status.Inventory)

	state.mu.RLock()
	recipeGraph := state.RecipeGraphLocked()
	lastCraftTick := state.lastCraftTick
	lastPlaceTick := state.lastShelterPlaceTick
	state.mu.RUnlock()

	if recipeGraph.IsEmpty() {
		return false
	}
	canCraft := lastCraftTick == 0 || frame.WorldTick < lastCraftTick || frame.WorldTick-lastCraftTick >= 20
	if !canCraft {
		return false
	}

	state.mu.RLock()
	nearestTable, _, bestTableDistSq := findNearestCraftingTableLocked(frame, pos, state)
	state.mu.RUnlock()
	tableInReach := isCraftingTableWithinReach(nearestTable, bestTableDistSq, pos)

	var toolTargets []string
	if !inv.hasStoneGear {
		toolTargets = append(toolTargets, "minecraft:stone_sword")
	}
	if !inv.hasAxe {
		toolTargets = append(toolTargets, "minecraft:wooden_axe")
	} else if !inv.hasSword {
		toolTargets = append(toolTargets, "minecraft:wooden_sword")
	}

	for _, targetID := range toolTargets {
		step, ok := recipeGraph.PlanNextStep(targetID, invMap, tableInReach)
		if !ok {
			continue
		}
		if step.CanExecuteNow {
			return true
		}
		if step.NeedsTable {
			canPlace := lastPlaceTick == 0 || frame.WorldTick < lastPlaceTick || frame.WorldTick-lastPlaceTick >= 20
			if inv.hasCraftingTable && canPlace {
				return true
			}
			if tableStep, tableOK := recipeGraph.PlanNextStep("minecraft:crafting_table", invMap, false); tableOK && tableStep.CanExecuteNow {
				return true
			}
		}
		break
	}

	if !inv.hasLight {
		if tableInReach {
			if step, ok := recipeGraph.PlanNextStep("minecraft:campfire", invMap, true); ok && step.CanExecuteNow {
				return true
			}
		}
		if step, ok := recipeGraph.PlanNextStep("minecraft:torch", invMap, tableInReach); ok && step.CanExecuteNow {
			return true
		}
	}
	return false
}
