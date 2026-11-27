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
	loc *directiveLocal,
) *civquestv1.AgentIntent {
	agentID := ""
	if frame != nil && frame.Status != nil && frame.Status.AgentId != "" {
		agentID = frame.Status.AgentId
	} else if cfg != nil {
		agentID = cfg.Name
	}
	return EvaluateStrategicDirectiveWithID(agentID, frame, state, cfg, loc)
}

// EvaluateStrategicDirectiveWithID executes the active System 2 strategic directive with an explicit agentID.
func EvaluateStrategicDirectiveWithID(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	loc *directiveLocal,
) *civquestv1.AgentIntent {
	dir, ok := runnableDirective(frame, state)
	if !ok {
		return nil
	}
	if loc == nil {
		// No frame Local bound (direct call): a stateless evaluation with a fresh cursor.
		loc = &directiveLocal{}
	}

	if dir.Action != DirectiveActionSocialize && state.IsExpeditionSurveying(frame.WorldTick) {
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

	// Let active horizon expeditions handle terrain barriers (ravine rim hazard + re-survey + flank)
	// (stuck recovery itself is the Recover frame, ADR-037).
	if (navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed()) &&
		state.IsExpeditionActive() && !state.HasActiveTask() {
		return nil
	}

	switch dir.Action {
	case DirectiveActionGatherTimber:
		return evaluateDirectiveGatherTimber(agentID, frame, pos, navState, state, dir, loc)
	case DirectiveActionMineStone:
		return evaluateDirectiveMineStone(agentID, frame, pos, navState, state, dir, loc)
	case DirectiveActionForageFood:
		// A bid source, not an executor: the directive makes the Foraging activity eligible
		// (directiveForcesForaging) and the Foraging frame does the work.
		return nil
	case DirectiveActionBuildShelter:
		// Without materials the directive gathers timber; with them it bids for the Shelter
		// activity (directiveBuildsShelter), which owns block placement.
		inv := inspectBootstrapInventory(frame.Status.Inventory)
		if inv.buildingBlockCount == 0 {
			return evaluateDirectiveGatherTimber(agentID, frame, pos, navState, state, dir, loc)
		}
		return nil
	case DirectiveActionSocialize:
		return evaluateDirectiveSocialize(agentID, frame, pos, navState, state, cfg, loc)
	default:
		return nil
	}
}

func hasEmergencyPreemption(frame *civquestv1.TelemetryFrame) bool {
	if frame == nil || frame.Status == nil {
		return true
	}
	for _, e := range frame.VisibleEntities {
		if IsImmediateHostileThreat(frame, e) {
			return true
		}
	}
	if frame.LastDamageTaken != nil && !IsPassiveOrEnvironmentalDamage(frame.LastDamageTaken) {
		return true
	}
	if isDrowningDamage(frame) || frame.Status.IsOnFire {
		return true
	}
	maxHP := float64(frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	if float64(frame.Status.Health) <= LowHPThresholdRatio*maxHP {
		return true
	}
	if frame.Status.FoodLevel <= 6 {
		if _, _, hasFood := FindOptimalFood(frame.Status.Inventory); hasFood {
			return true
		}
	}
	return false
}

func hasSurvivalPreemption(frame *civquestv1.TelemetryFrame) bool {
	if frame == nil || frame.Status == nil {
		return true
	}
	if hasEmergencyPreemption(frame) {
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
	loc *directiveLocal,
) *civquestv1.AgentIntent {
	state.mu.Lock()
	defer state.mu.Unlock()

	walk := &loc.Walk
	if walk.hasTask() {
		kind := walk.task.Spec.Kind
		if kind == "harvest_tree" || kind == "collect_items" {
			stepRes := walk.stepTaskLocked(agentID, state, frame)
			if stepRes.Active {
				return stepRes.Intent
			}
		}
	}

	targetLog, recalledTreePOI := findNearestTreeTrunk(agentID, frame, pos, state)
	if targetLog != nil {
		treeTask := NewHarvestTreeTask(targetLog.Pos.X, targetLog.Pos.Y, targetLog.Pos.Z, state.stage, frame.WorldTick)
		stepRes := walk.launchTaskLocked(agentID, state, frame, treeTask)
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
			canDispatchDirectiveNavLocked(loc, frame.WorldTick, "poi:"+recalledTreePOI.POIID) {
			loc.LastActionTick = frame.WorldTick
			loc.LastNavKey = "poi:" + recalledTreePOI.POIID
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

	return evaluateDirectiveScoutOutwardLocked(agentID, frame, pos, navState, loc, dir.IssuedTick)
}

func evaluateDirectiveMineStone(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	dir StrategicDirectiveState,
	loc *directiveLocal,
) *civquestv1.AgentIntent {
	state.mu.Lock()
	defer state.mu.Unlock()

	var inventory []*civquestv1.ItemStackProto
	if frame.Status != nil {
		inventory = frame.Status.Inventory
	}
	// Precondition (cq-gpa.10): never issue a stone/ore break without a tool that yields drops.
	// Only blocks the current tools can harvest are considered; with no pickaxe at all, plan to
	// craft one (wooden first) instead of mining by hand or with an unrelated tool.
	bestBlock, bestDistSq := findNearestMineableStoneBlock(frame, pos, inventory, func(b *civquestv1.BlockPosProto) bool {
		return state.givenUpLocked(float64(b.X)+0.5, float64(b.Y), float64(b.Z)+0.5, frame.WorldTick)
	})
	if bestBlock == nil {
		if _, havePick := BestToolTier(inventory, ToolClassPickaxe); !havePick {
			return craftToolIntentLocked(agentID, frame, state, pos, "minecraft:wooden_pickaxe", &loc.Walk)
		}
	}
	if bestBlock != nil {
		dy := math.Abs(float64(bestBlock.Pos.Y) + 0.5 - pos.Y)
		if bestDistSq <= directiveReachRadius*directiveReachRadius && dy <= 3.0 {
			if loc.LastActionTick > 0 && frame.WorldTick >= loc.LastActionTick &&
				frame.WorldTick-loc.LastActionTick < directiveActionCooldownTicks {
				return nil
			}
			loc.LastActionTick = frame.WorldTick
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
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && loc.LastNavKey == navKey {
			return nil
		}
		if canDispatchDirectiveNavLocked(loc, frame.WorldTick, navKey) {
			loc.LastActionTick = frame.WorldTick
			loc.LastNavKey = navKey
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
				canDispatchDirectiveNavLocked(loc, frame.WorldTick, "poi:"+minePOI.POIID) {
				loc.LastActionTick = frame.WorldTick
				loc.LastNavKey = "poi:" + minePOI.POIID
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

	return evaluateDirectiveScoutOutwardLocked(agentID, frame, pos, navState, loc, dir.IssuedTick)
}

func evaluateDirectiveSocialize(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	loc *directiveLocal,
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
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && loc.LastNavKey == navKey {
		return nil
	}
	if !canDispatchDirectiveNavLocked(loc, frame.WorldTick, navKey) {
		return nil
	}
	loc.LastActionTick = frame.WorldTick
	loc.LastNavKey = navKey

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
	loc *directiveLocal,
	issuedTick int64,
) *civquestv1.AgentIntent {
	if loc.ScoutTarget == nil ||
		math.Hypot(pos.X-loc.ScoutTarget.X, pos.Z-loc.ScoutTarget.Z) <= 4.0 ||
		navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
		loc.ScoutTarget = computeDirectiveFrontierWaypoint(agentID, issuedTick+int64(loc.ScoutStep), pos)
		loc.ScoutStep++
	}
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && loc.LastNavKey == "scout_outward" {
		return nil
	}
	if !canDispatchDirectiveNavLocked(loc, frame.WorldTick, "scout_outward") {
		return nil
	}
	loc.LastActionTick = frame.WorldTick
	loc.LastNavKey = "scout_outward"
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: loc.ScoutTarget.X,
					Y: loc.ScoutTarget.Y,
					Z: loc.ScoutTarget.Z,
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

func canDispatchDirectiveNavLocked(loc *directiveLocal, worldTick int64, navKey string) bool {
	if loc.LastNavKey != navKey || loc.LastActionTick == 0 || worldTick < loc.LastActionTick {
		return true
	}
	return worldTick-loc.LastActionTick >= directiveNavCooldownTicks
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

// craftToolIntentLocked returns a craft intent for targetID when the recipe graph can
// execute the next step now (with a table in reach for 3x3 steps), else nil so the bootstrap
// progression (timber, table) supplies the materials. state.mu must be held.
func craftToolIntentLocked(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState, pos *civquestv1.Vec3, targetID string, walk *walkLocal) *civquestv1.AgentIntent {
	graph := state.RecipeGraphLocked()
	if graph.IsEmpty() {
		return nil
	}
	if last := state.mem.lastCraftTick; last != 0 && frame.WorldTick >= last && frame.WorldTick-last < 20 {
		return nil
	}
	table, _, tableDistSq := findNearestCraftingTableLocked(frame, pos, state)
	inReach := isCraftingTableWithinReach(table, tableDistSq, pos)
	step, ok := graph.PlanNextStep(targetID, crafting.InventoryMapFromProtos(frame.Status.Inventory), inReach)
	if !ok || !step.CanExecuteNow {
		return nil
	}
	invMap := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	if walk.craftHeld(graph, step.RecipeID, invMap, frame.WorldTick) {
		return nil
	}
	state.mem.lastCraftTick = frame.WorldTick
	walk.armCraft(graph, step.RecipeID, invMap, frame.WorldTick)
	craft := &civquestv1.CraftItemIntent{RecipeId: step.RecipeID, BatchCount: 1}
	if step.Requires3x3 && table != nil {
		craft.WorkstationPos = table.Pos
	}
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_CraftItem{CraftItem: craft}}
}

func findNearestMineableStoneBlock(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, inventory []*civquestv1.ItemStackProto, skip func(*civquestv1.BlockPosProto) bool) (*civquestv1.VisibleBlock, float64) {
	if frame == nil || pos == nil {
		return nil, math.MaxFloat64
	}
	var best *civquestv1.VisibleBlock
	bestDistSq := math.MaxFloat64
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !isMineableStoneBlockID(b.BlockId) {
			continue
		}
		if !HasSuitableTool(inventory, RequirementForBlock(b.BlockId)) {
			continue
		}
		if !dropWouldBeInReach(pos, b.Pos) {
			continue // buried, or underfoot: its drop would be left in the hole
		}
		if skip != nil && skip(b.Pos) {
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
	state.mu.RLock()
	defer state.mu.RUnlock()
	return canExecuteImmediateBootstrapCraftLocked(frame, state)
}

func canExecuteImmediateBootstrapCraftLocked(frame *civquestv1.TelemetryFrame, state *AgentState) bool {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return false
	}
	pos := frame.Status.Position
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	invMap := crafting.InventoryMapFromProtos(frame.Status.Inventory)

	recipeGraph := state.RecipeGraphLocked()
	lastCraftTick := state.mem.lastCraftTick
	lastPlaceTick := state.mem.lastShelterPlaceTick

	if recipeGraph.IsEmpty() {
		return false
	}
	canCraft := lastCraftTick == 0 || frame.WorldTick < lastCraftTick || frame.WorldTick-lastCraftTick >= 20
	if !canCraft {
		return false
	}

	nearestTable, _, bestTableDistSq := findNearestCraftingTableLocked(frame, pos, state)
	tableInReach := isCraftingTableWithinReach(nearestTable, bestTableDistSq, pos)

	toolTargets := bootstrapToolTargets(inv)

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
