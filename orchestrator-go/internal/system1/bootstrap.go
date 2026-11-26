package system1

import (
	"hash/crc32"
	"math"
	"sort"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type bootstrapInventorySummary struct {
	logCount            int32
	planksCount         int32
	stickCount          int32
	cobbleCount         int32
	coalCount           int32
	nonLogBuildingCount int32
	buildingBlockCount  int32
	placeableBlockID    string
	hasAxe              bool
	hasPickaxe          bool
	pickaxeTier         ToolTier
	hasSword            bool
	hasWeapon           bool
	hasStoneGear        bool
	hasLight            bool
	lightItemID         string
	craftingTableCount  int32
	hasCraftingTable    bool
}

func inspectBootstrapInventory(inventory []*civquestv1.ItemStackProto) bootstrapInventorySummary {
	var s bootstrapInventorySummary
	var firstPlankID, firstSolidID, firstLogID string
	for _, it := range inventory {
		if it == nil || it.Count <= 0 || it.ItemId == "" {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(it.ItemId))
		switch {
		case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem"):
			s.logCount += it.Count
			s.buildingBlockCount += it.Count
			if firstLogID == "" {
				firstLogID = it.ItemId
			}
		case strings.HasSuffix(id, "_planks"):
			s.planksCount += it.Count
			s.nonLogBuildingCount += it.Count
			s.buildingBlockCount += it.Count
			if firstPlankID == "" {
				firstPlankID = it.ItemId
			}
		case id == "minecraft:stick" || strings.HasSuffix(id, ":stick") || id == "stick":
			s.stickCount += it.Count
		case id == "minecraft:coal" || id == "minecraft:charcoal" ||
			strings.HasSuffix(id, ":coal") || strings.HasSuffix(id, ":charcoal") ||
			id == "coal" || id == "charcoal":
			s.coalCount += it.Count
		case strings.Contains(id, "cobblestone") || strings.Contains(id, "cobbled_deepslate") || strings.Contains(id, "blackstone"):
			s.cobbleCount += it.Count
			s.nonLogBuildingCount += it.Count
			s.buildingBlockCount += it.Count
			if firstSolidID == "" {
				firstSolidID = it.ItemId
			}
		case id == "minecraft:dirt" || strings.HasSuffix(id, ":dirt") || id == "dirt" ||
			id == "minecraft:stone" || strings.HasSuffix(id, ":stone") || id == "stone":
			s.nonLogBuildingCount += it.Count
			s.buildingBlockCount += it.Count
			if firstSolidID == "" {
				firstSolidID = it.ItemId
			}
		}
		if strings.Contains(id, "_axe") {
			s.hasAxe = true
			s.hasWeapon = true
		}
		if class, tier, ok := ToolOf(id); ok && class == ToolClassPickaxe {
			if !s.hasPickaxe || tier > s.pickaxeTier {
				s.pickaxeTier = tier
			}
			s.hasPickaxe = true
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
		if strings.Contains(id, "crafting_table") {
			s.hasCraftingTable = true
			s.craftingTableCount += it.Count
		}
	}
	switch {
	case firstPlankID != "":
		s.placeableBlockID = firstPlankID
	case firstSolidID != "":
		s.placeableBlockID = firstSolidID
	case firstLogID != "":
		s.placeableBlockID = firstLogID
	}
	return s
}

// HasSurvivalKit returns true if the inventory contains basic survival kit gear:
// an axe, a sword/weapon, and a light source (campfire/torch).
func HasSurvivalKit(inventory []*civquestv1.ItemStackProto) bool {
	inv := inspectBootstrapInventory(inventory)
	return inv.hasAxe && (inv.hasSword || inv.hasWeapon) && inv.hasLight
}

// IsPioneerNucleationLocked returns true if specialized profession duties and career transitions
// are locked due to the absence of a verified Settlement POI (or Habitation hearth) in SpatialIndex (ADR-027).
func IsPioneerNucleationLocked(si *SpatialIndex, profession string) bool {
	if si == nil || !si.HasSettlementPOI(nil, 0) {
		return IsSpecializedProfession(profession)
	}
	return false
}

// EffectivePioneerProfession returns the effective profession for utility and behavior evaluation (ADR-027).
// If no verified Settlement POI exists in SpatialIndex, specialized professions fall back to EXPLORER.
func EffectivePioneerProfession(si *SpatialIndex, profession string) string {
	if IsPioneerNucleationLocked(si, profession) {
		return "EXPLORER"
	}
	if strings.TrimSpace(profession) == "" {
		return "EXPLORER"
	}
	return strings.ToUpper(strings.TrimSpace(profession))
}

// EvaluateBootstrapGoals directly executes bootstrap timber harvesting, basic tool crafting,
// and local scouting for timber stands while maintaining the agent in StateBootstrap.
func EvaluateBootstrapGoals(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.Status.IsSleeping {
		return nil
	}

	pos := frame.Status.Position
	state.mu.Lock()
	defer state.mu.Unlock()

	state.mem.telemetryFramesSeen++
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
	if walk.lastExploreTick == 0 {
		walk.lastExploreTick = frame.WorldTick
	}

	navState := frame.Status.NavigationState

	// 2. Direct bootstrap timber gathering & tool crafting (with strict TaskSubroutine commitment)
	if bootstrapIntent := evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state, cfg, walk); bootstrapIntent != nil {
		return bootstrapIntent
	}
	if walk.hasTask() || walk.navigatingToPOI || state.isExpeditionSurveyingLocked(frame.WorldTick) {
		// Committed task subroutine, POI recall navigation, or active 360-degree horizon survey:
		// suppress exploration/wandering until complete, failed, or preempted.
		return nil
	}

	// 3. If no immediate trees or crafting table in frustum, scout locally for timber stands
	return evaluateGeneralExplorationLocked(agentID, frame, pos, state, cfg, walk)
}

// EvaluateBootstrapGoalsDirect evaluates bootstrap goals directly given explicit pos and navState.
func EvaluateBootstrapGoalsDirect(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || pos == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state, nil, walk)
}

func evaluateBootstrapGoalsLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}
	walk.navigatingToPOI = false

	// 0. Execute any committed TaskSubroutine (e.g. harvest_tree, collect_items) before evaluating other goals.
	// While committed, the agent MUST NOT switch tasks, craft prematurely, re-target another tree, or wander away,
	// EXCEPT when already carrying enough timber/materials to immediately craft missing survival tools.
	justFinishedHarvest := false
	if walk.hasTask() {
		taskKind := walk.task.Spec.Kind
		preemptForCraft := false
		if taskKind == "harvest_tree" {
			invCheck := inspectBootstrapInventory(frame.Status.Inventory)
			hasTimberForKit := invCheck.logCount >= 6 ||
				(!invCheck.hasAxe && (invCheck.planksCount >= 3 || invCheck.stickCount >= 2 || invCheck.hasCraftingTable)) ||
				(walk.task.Phase == TaskPhaseApproach && (invCheck.logCount >= 1 || invCheck.planksCount >= 1 || invCheck.stickCount >= 1) && (!invCheck.hasAxe || !invCheck.hasSword))
			if hasTimberForKit && canExecuteImmediateBootstrapCraftLocked(frame, state) {
				preemptForCraft = true
				walk.clearTask(state)
			}
		}
		if !preemptForCraft {
			targetPos := cloneBlockPos(walk.task.Spec.Target.BlockPos)
			stepRes := walk.stepTaskLocked(agentID, state, frame)
			if stepRes.Active {
				return stepRes.Intent
			}
			// Task reached COMPLETE or FAILED: transition cleanly
			if stepRes.Done && taskKind == "harvest_tree" && stepRes.Phase == TaskPhaseComplete {
				justFinishedHarvest = true
				if targetPos != nil {
					if drop := findNearestDroppedWoodItem(frame, targetPos.X, targetPos.Z); drop != nil {
						collectTask := NewCollectItemsTask(targetPos.X, targetPos.Y, targetPos.Z, state.stage, frame.WorldTick)
						walk.startTaskLocked(state, collectTask)
						collectRes := walk.stepTaskLocked(agentID, state, frame)
						if collectRes.Active {
							return collectRes.Intent
						}
					}
				}
			}
		}
	}

	inv := inspectBootstrapInventory(frame.Status.Inventory)
	invMap := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	recipeGraph := state.RecipeGraphLocked()

	canCraft := state.mem.lastCraftTick == 0 || frame.WorldTick < state.mem.lastCraftTick || frame.WorldTick-state.mem.lastCraftTick >= 20
	canPlace := state.mem.lastShelterPlaceTick == 0 || frame.WorldTick < state.mem.lastShelterPlaceTick || frame.WorldTick-state.mem.lastShelterPlaceTick >= 20

	var nearestCraftingTable *civquestv1.VisibleBlock
	var recalledTablePOI *db.AgentPOI
	bestTableDistSq := math.MaxFloat64
	if !recipeGraph.IsEmpty() && (!inv.hasStoneGear || !inv.hasAxe || !inv.hasSword || !inv.hasLight) {
		nearestCraftingTable, recalledTablePOI, bestTableDistSq = findNearestCraftingTableLocked(frame, pos, state)
	}
	tableInReach := isCraftingTableWithinReach(nearestCraftingTable, bestTableDistSq, pos)

	if !recipeGraph.IsEmpty() {
		toolTargets := bootstrapToolTargets(inv)

		for _, targetID := range toolTargets {
			step, ok := recipeGraph.PlanNextStep(targetID, invMap, tableInReach)
			if !ok {
				continue
			}
			if step.CanExecuteNow {
				if !canCraft {
					return nil
				}
				state.mem.lastCraftTick = frame.WorldTick
				craftIntent := &civquestv1.CraftItemIntent{
					RecipeId:   step.RecipeID,
					BatchCount: 1,
				}
				if step.Requires3x3 && nearestCraftingTable != nil {
					craftIntent.WorkstationPos = nearestCraftingTable.Pos
				}
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_CraftItem{
						CraftItem: craftIntent,
					},
				}
			}
			if step.NeedsTable {
				if recalledTablePOI != nil {
					if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
						state.MarkWorkstationUnreachableLocked(nearestCraftingTable.Pos.X, nearestCraftingTable.Pos.Y, nearestCraftingTable.Pos.Z, frame.WorldTick)
						if si := state.SpatialIndexLocked(); si != nil {
							si.DecayPOIReliability(recalledTablePOI.POIID, 0.3)
						}
						return nil
					}
					walk.navigatingToPOI = true
					if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
						if walk.lastBootstrapNavTick == 0 || frame.WorldTick < walk.lastBootstrapNavTick || frame.WorldTick-walk.lastBootstrapNavTick >= 40 {
							walk.lastBootstrapNavTick = frame.WorldTick
							return &civquestv1.AgentIntent{
								AgentId: agentID,
								Action: &civquestv1.AgentIntent_Navigate{
									Navigate: &civquestv1.NavigateIntent{
										Target:           poiAccessTarget(recalledTablePOI),
										SpeedMultiplier:  1.0,
										ArrivalTolerance: 2,
									},
								},
							}
						}
					}
					return nil
				} else if inv.hasCraftingTable {
					// Self-placement: when carrying a crafting table and none is within reach,
					// place it immediately in front of the agent instead of trekking to a distant visible table.
					if canPlace {
						state.mem.lastShelterPlaceTick = frame.WorldTick
						placePos := selectAdjacentCraftingTablePos(frame, pos)
						return &civquestv1.AgentIntent{
							AgentId: agentID,
							Action: &civquestv1.AgentIntent_InteractBlock{
								InteractBlock: &civquestv1.InteractBlockIntent{
									Pos:             placePos,
									InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
									BlockOrItemId:   "minecraft:crafting_table",
								},
							},
						}
					}
					return nil
				} else if nearestCraftingTable != nil {
					if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
						// We became stuck trying to reach this table! Mark it unreachable so we don't repeat this futile attempt.
						state.MarkWorkstationUnreachableLocked(nearestCraftingTable.Pos.X, nearestCraftingTable.Pos.Y, nearestCraftingTable.Pos.Z, frame.WorldTick)
						return nil
					}
					if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
						if walk.lastExploreTick == 0 || frame.WorldTick < walk.lastExploreTick || frame.WorldTick-walk.lastExploreTick >= 40 {
							walk.lastExploreTick = frame.WorldTick
							return &civquestv1.AgentIntent{
								AgentId: agentID,
								Action: &civquestv1.AgentIntent_Navigate{
									Navigate: &civquestv1.NavigateIntent{
										Target: &civquestv1.Vec3{
											X: float64(nearestCraftingTable.Pos.X) + 0.5,
											Y: float64(nearestCraftingTable.Pos.Y),
											Z: float64(nearestCraftingTable.Pos.Z) + 0.5,
										},
										SpeedMultiplier:  1.0,
										ArrivalTolerance: 2,
									},
								},
							}
						}
					}
					return nil
				} else if tableStep, tableOK := recipeGraph.PlanNextStep("minecraft:crafting_table", invMap, false); tableOK && tableStep.CanExecuteNow {
					// Autonomous self-reliance: craft our own table in 2x2 pocket crafting
					// if no reachable crafting table exists.
					if canCraft {
						state.mem.lastCraftTick = frame.WorldTick
						return &civquestv1.AgentIntent{
							AgentId: agentID,
							Action: &civquestv1.AgentIntent_CraftItem{
								CraftItem: &civquestv1.CraftItemIntent{
									RecipeId:   tableStep.RecipeID,
									BatchCount: 1,
								},
							},
						}
					}
					return nil
				}
			}
			break
		}
	}

	if !inv.hasLight && canCraft && !recipeGraph.IsEmpty() {
		if craftIntent := craftLightSourceIntentLocked(agentID, frame.WorldTick, invMap, nearestCraftingTable, bestTableDistSq, pos, state); craftIntent != nil {
			return craftIntent
		}
	}

	// 3. Gather timber if the agent lacks basic tools or has < 6 logs.
	// If a harvest_tree task just completed on this tick, allow the HFSM PDA stack to pop cleanly
	// before starting another harvest_tree task on the next tick.
	// When returning to clan rendezvous, complete the return leg instead of stopping to chop trees.
	needsTimber := !inv.hasAxe || !inv.hasSword || inv.logCount < 6
	if needsTimber && !justFinishedHarvest {
		targetLog, recalledTreePOI := findNearestTreeTrunk(agentID, frame, pos, state)
		if targetLog != nil {
			treeTask := NewHarvestTreeTask(targetLog.Pos.X, targetLog.Pos.Y, targetLog.Pos.Z, state.stage, frame.WorldTick)
			walk.startTaskLocked(state, treeTask)
			stepRes := walk.stepTaskLocked(agentID, state, frame)
			if stepRes.Active {
				return stepRes.Intent
			}
		} else if recalledTreePOI != nil &&
			(!isLeavesBlockID(recalledTreePOI.BlockID) || recalledTreePOI.Reliability >= 0.9 || cfg == nil || !isExplorerProfession(cfg.Profession)) &&
			!state.IsExpeditionActiveLocked() {
			if navIntent, handled := navigateToRecalledTreePOILocked(agentID, frame, pos, navState, state, recalledTreePOI, walk); handled {
				return navIntent
			}
			return nil
		} else {
			// No visible logs within 16m and no verified close-range tree POI in SpatialIndex:
			// run the 360-degree horizon survey explorer loop to locate distant forests (ADR-032).
			// Require >=2 observed frames and non-pathing/cooldown clearance on initial idle entry
			// so single-frame unit tests and brief pathing segments are not prematurely interrupted.
			canInitiateSurvey := state.mem.telemetryFramesSeen >= 2 &&
				navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING &&
				(walk.lastExploreTick == 0 || frame.WorldTick < walk.lastExploreTick || frame.WorldTick-walk.lastExploreTick >= 40)
			if canInitiateSurvey {
				// The expedition runs as a frame pushed over the bootstrap activity.
				walk.wantExpedition = true
			}
		}
	}

	return nil
}

// navigateToRecalledTreePOILocked handles navigation, stale-POI pruning, stuck decay, and ADR-026
// corridor traversal for a recalled tree resource POI. Caller must hold state.mu.
func navigateToRecalledTreePOILocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	recalledTreePOI *db.AgentPOI,
	walk *walkLocal,
) (*civquestv1.AgentIntent, bool) {
	walk = orFreshWalk(walk)
	if frame == nil || pos == nil || state == nil || recalledTreePOI == nil {
		return nil, false
	}
	bpos := poiBlockPos(recalledTreePOI)
	dx := recalledTreePOI.X - pos.X
	dy := recalledTreePOI.Y + 0.5 - pos.Y
	dz := recalledTreePOI.Z - pos.Z
	if dx*dx+dy*dy+dz*dz <= 12.25 || (dx*dx+dz*dz <= 12.25 && math.Abs(recalledTreePOI.AccessY-pos.Y) <= 4.0) {
		// Arrived within line-of-sight of remembered tree stand with no visible logs: prune stale POI.
		if si := state.SpatialIndexLocked(); si != nil {
			si.RemovePOI(recalledTreePOI.POIID)
		}
		return nil, false
	}
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		state.MarkTreeFailedLocked(bpos.X, bpos.Z, frame.WorldTick)
		if si := state.SpatialIndexLocked(); si != nil {
			si.DecayPOIReliability(recalledTreePOI.POIID, 0.3)
		}
		return nil, false
	}
	walk.navigatingToPOI = true
	if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		// ADR-026: Hierarchical Trail-Intersection Pathfinding:
		// If a corridor exists connecting the origin/trail to the destination POI,
		// traverse sequential corridor waypoints instead of direct Euclidean line-of-sight.
		if tracker := state.HabitTrailTrackerLocked(); tracker != nil {
			originID := tracker.CurrentOriginPOIID()
			si := state.SpatialIndexLocked()
			if originID != "" && si != nil {
				if corridor, ok := si.GetCorridor(originID, recalledTreePOI.POIID); ok && corridor != nil {
					if !tracker.IsNavigatingCorridor() {
						intent, err := tracker.StartCorridorNavigation(agentID, corridor, poiAccessTarget(recalledTreePOI))
						if err == nil && intent != nil {
							return intent, true
						}
					}
				}
			}
			if tracker.IsNavigatingCorridor() {
				if intent, stepped := tracker.UpdateNavigation(pos, navState); stepped && intent != nil {
					return intent, true
				}
			}
		}

		if walk.lastBootstrapNavTick == 0 || frame.WorldTick < walk.lastBootstrapNavTick || frame.WorldTick-walk.lastBootstrapNavTick >= 40 {
			walk.lastBootstrapNavTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target:           poiAccessTarget(recalledTreePOI),
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}, true
		}
	}
	return nil, true
}

// findNearestCraftingTableLocked locates the nearest reachable crafting table from visible blocks
// or SpatialIndex POIs (ADR-022 / ADR-026). Caller must hold state.mu if state is non-nil.
func findNearestCraftingTableLocked(
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
) (*civquestv1.VisibleBlock, *db.AgentPOI, float64) {
	var nearestCraftingTable *civquestv1.VisibleBlock
	var recalledTablePOI *db.AgentPOI
	bestTableDistSq := math.MaxFloat64
	if frame == nil || pos == nil {
		return nil, nil, bestTableDistSq
	}

	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if !strings.Contains(id, "crafting_table") {
			continue
		}
		dx := float64(b.Pos.X) + 0.5 - pos.X
		dy := float64(b.Pos.Y) + 0.5 - pos.Y
		dz := float64(b.Pos.Z) + 0.5 - pos.Z
		// Reject tables vertically separated by > 3 blocks unless there is a gradual horizontal slope,
		// or tables recently marked unreachable.
		hDist := math.Hypot(dx, dz)
		absDy := math.Abs(dy)
		if absDy > 3.0 && hDist < absDy*1.5 {
			continue
		}
		if state != nil && (state.IsWorkstationUnreachableLocked(b.Pos.X, b.Pos.Y, b.Pos.Z, frame.WorldTick) ||
			state.IsTargetUnreachableLocked(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick)) {
			continue
		}
		dSq := dx*dx + dy*dy + dz*dz
		if dSq < bestTableDistSq {
			bestTableDistSq = dSq
			nearestCraftingTable = b
		}
	}

	// ADR-022 / ADR-026: If no reachable crafting table is visible in the frustum, query SpatialIndex.
	if nearestCraftingTable == nil && state != nil {
		if si := state.SpatialIndexLocked(); si != nil {
			agentLayer := ClassifyElevationLayer(pos.Y, 64.0)
			poi := si.FindNearestPOI(pos, POICategoryWorkstation, "minecraft:crafting_table", 64.0, agentLayer)
			if poi == nil && (agentLayer == ElevationDugout || agentLayer == ElevationUnderground) {
				// If underground without a local table, only target surface workstation if connected by a corridor.
				originID := ""
				if tracker := state.HabitTrailTrackerLocked(); tracker != nil {
					originID = tracker.CurrentOriginPOIID()
				}
				if surfacePOI := si.FindNearestPOI(pos, POICategoryWorkstation, "minecraft:crafting_table", 64.0, ElevationSurface); surfacePOI != nil {
					if originID != "" && si.HasCorridorBetween(originID, surfacePOI.POIID) {
						poi = surfacePOI
					}
				}
			} else if poi == nil {
				poi = si.FindNearestPOI(pos, POICategoryWorkstation, "minecraft:crafting_table", 64.0, "")
			}
			if poi != nil && poi.Reliability >= 0.5 {
				bpos := poiBlockPos(poi)
				if !state.IsWorkstationUnreachableLocked(bpos.X, bpos.Y, bpos.Z, frame.WorldTick) &&
					!state.isPOIUnreachableLocked(poi, frame.WorldTick) {
					dx := poi.X - pos.X
					dy := poi.Y + 0.5 - pos.Y
					dz := poi.Z - pos.Z
					hDist := math.Hypot(dx, dz)
					absDy := math.Abs(dy)
					if absDy <= 3.0 || hDist >= absDy*1.5 {
						recalledTablePOI = poi
						bestTableDistSq = dx*dx + dy*dy + dz*dz
						nearestCraftingTable = &civquestv1.VisibleBlock{
							Pos:     bpos,
							BlockId: poi.BlockID,
						}
					}
				}
			}
		}
	}

	return nearestCraftingTable, recalledTablePOI, bestTableDistSq
}

// isCraftingTableWithinReach returns true if a crafting table is within 4.5m reach and 2.5m vertical reach.
func isCraftingTableWithinReach(table *civquestv1.VisibleBlock, distSq float64, pos *civquestv1.Vec3) bool {
	if table == nil || table.Pos == nil || pos == nil {
		return false
	}
	dy := math.Abs(float64(table.Pos.Y) + 0.5 - pos.Y)
	return distSq <= 20.25 && dy <= 2.5
}

// craftLightSourceIntentLocked crafts a campfire (3x3 at reachable crafting table) or
// pocket-crafts a torch (2x2) by querying the dynamic RecipeGraph (ADR-029).
// Returns nil without emitting bogus craft intents when required materials or loaded recipes are absent.
func craftLightSourceIntentLocked(
	agentID string,
	worldTick int64,
	invMap map[string]int,
	nearestCraftingTable *civquestv1.VisibleBlock,
	bestTableDistSq float64,
	pos *civquestv1.Vec3,
	state *AgentState,
) *civquestv1.AgentIntent {
	if state == nil {
		return nil
	}
	recipeGraph := state.RecipeGraphLocked()
	if recipeGraph.IsEmpty() {
		return nil
	}

	tableInReach := isCraftingTableWithinReach(nearestCraftingTable, bestTableDistSq, pos)
	if tableInReach {
		if step, ok := recipeGraph.PlanNextStep("minecraft:campfire", invMap, true); ok && step.CanExecuteNow {
			state.mem.lastCraftTick = worldTick
			craftIntent := &civquestv1.CraftItemIntent{
				RecipeId:   step.RecipeID,
				BatchCount: 1,
			}
			if step.Requires3x3 && nearestCraftingTable != nil {
				craftIntent.WorkstationPos = nearestCraftingTable.Pos
			}
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_CraftItem{
					CraftItem: craftIntent,
				},
			}
		}
	}

	if step, ok := recipeGraph.PlanNextStep("minecraft:torch", invMap, tableInReach); ok && step.CanExecuteNow {
		state.mem.lastCraftTick = worldTick
		craftIntent := &civquestv1.CraftItemIntent{
			RecipeId:   step.RecipeID,
			BatchCount: 1,
		}
		if step.Requires3x3 && tableInReach && nearestCraftingTable != nil {
			craftIntent.WorkstationPos = nearestCraftingTable.Pos
		}
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_CraftItem{
				CraftItem: craftIntent,
			},
		}
	}

	return nil
}

func isEmptyPlacementBlock(frame *civquestv1.TelemetryFrame, x, y, z int32) bool {
	b := FindVisibleBlockAt(frame, x, y, z)
	if b == nil {
		return true
	}
	id := strings.ToLower(strings.TrimSpace(b.BlockId))
	return id == "" || id == "minecraft:air" || strings.HasSuffix(id, "_air")
}

func selectAdjacentCraftingTablePos(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) *civquestv1.BlockPosProto {
	cx := int32(math.Floor(pos.X))
	cy := int32(math.Floor(pos.Y))
	cz := int32(math.Floor(pos.Z))

	var candidates []*civquestv1.BlockPosProto
	if frame != nil && frame.Status != nil {
		yawRad := float64(frame.Status.Yaw) * math.Pi / 180.0
		fx := -math.Sin(yawRad)
		fz := math.Cos(yawRad)
		frontX := int32(math.Floor(pos.X + fx*1.5))
		frontZ := int32(math.Floor(pos.Z + fz*1.5))
		if frontX != cx || frontZ != cz {
			candidates = append(candidates, &civquestv1.BlockPosProto{X: frontX, Y: cy, Z: frontZ})
		}
	}
	candidates = append(candidates,
		&civquestv1.BlockPosProto{X: cx + 1, Y: cy, Z: cz},
		&civquestv1.BlockPosProto{X: cx - 1, Y: cy, Z: cz},
		&civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz + 1},
		&civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz - 1},
	)

	for _, c := range candidates {
		if isEmptyPlacementBlock(frame, c.X, c.Y, c.Z) {
			return c
		}
	}
	return candidates[0]
}

type trunkCandidate struct {
	tx     int32
	tz     int32
	distSq float64
	anchor *civquestv1.VisibleBlock
}

// isElevatedCanopyOnlyLog returns true when a visible log block is > 4 blocks above the agent
// and no reachable trunk base log (<= 4 blocks above pos.Y) exists in the same or adjacent column.
func isElevatedCanopyOnlyLog(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, b *civquestv1.VisibleBlock) bool {
	if frame == nil || pos == nil || b == nil || b.Pos == nil {
		return false
	}
	if float64(b.Pos.Y)-pos.Y <= 4.0 {
		return false
	}
	for _, other := range frame.InterestingBlocks {
		if other == nil || other.Pos == nil || !isLogBlockID(other.BlockId) {
			continue
		}
		dx := other.Pos.X - b.Pos.X
		dz := other.Pos.Z - b.Pos.Z
		if dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1 && float64(other.Pos.Y)-pos.Y <= 4.0 {
			return false
		}
	}
	return true
}

// findNearestTreeTrunk selects a tree trunk column from visible logs, sorting unique
// nearby trunks by distance and hashing agentID so multiple agents distribute across trees.
// If no logs are visible in the frustum, it queries SpatialIndex for a recalled timber stand.
func findNearestTreeTrunk(agentID string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, state *AgentState) (*civquestv1.VisibleBlock, *db.AgentPOI) {
	if frame == nil || pos == nil {
		return nil, nil
	}

	type trunkKey struct {
		x int32
		z int32
	}
	byColumn := make(map[trunkKey]*trunkCandidate)
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !isLogBlockID(b.BlockId) {
			continue
		}
		if isElevatedCanopyOnlyLog(frame, pos, b) {
			continue
		}
		if state != nil && (state.IsTreeOnCooldownLocked(b.Pos.X, b.Pos.Z, frame.WorldTick) ||
			state.IsTargetUnreachableLocked(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick)) {
			continue
		}
		dx := float64(b.Pos.X) + 0.5 - pos.X
		dy := float64(b.Pos.Y) + 0.5 - pos.Y
		dz := float64(b.Pos.Z) + 0.5 - pos.Z
		dSq := dx*dx + dy*dy + dz*dz

		k := trunkKey{x: b.Pos.X, z: b.Pos.Z}
		existing, ok := byColumn[k]
		if !ok {
			byColumn[k] = &trunkCandidate{
				tx:     b.Pos.X,
				tz:     b.Pos.Z,
				distSq: dSq,
				anchor: b,
			}
		} else {
			if dSq < existing.distSq {
				existing.distSq = dSq
			}
			if b.Pos.Y < existing.anchor.Pos.Y {
				existing.anchor = b
			}
		}
	}
	if len(byColumn) == 0 {
		if state != nil {
			if si := state.SpatialIndexLocked(); si != nil {
				// ADR-026: Prohibit agents from pathfinding toward distant surface targets while underground.
				// If the agent is inside a dugout or underground, only query for a recalled timber stand if:
				// 1. The agent is on the surface (pos.Y >= 64.0 and layer == ElevationSurface), OR
				// 2. A trail corridor connects the current origin to the target.
				agentLayer := ClassifyElevationLayer(pos.Y, 64.0)
				isUnderground := agentLayer == ElevationDugout || agentLayer == ElevationUnderground

				for _, poi := range si.FindPOIsInRadius(pos, POICategoryResource, 64.0) {
					if poi == nil || !poi.IsActive || poi.Reliability < 0.5 {
						continue
					}
					if !strings.EqualFold(poi.ElevationLayer, ElevationSurface) || !matchesPOIBlockID(poi.BlockID, "log") {
						continue
					}
					bpos := poiBlockPos(poi)
					if state.IsTreeOnCooldownLocked(bpos.X, bpos.Z, frame.WorldTick) || state.isPOIUnreachableLocked(poi, frame.WorldTick) {
						continue
					}
					if isUnderground {
						originID := ""
						if tracker := state.HabitTrailTrackerLocked(); tracker != nil {
							originID = tracker.CurrentOriginPOIID()
						}
						hasCorridor := false
						if originID != "" {
							hasCorridor = si.HasCorridorBetween(originID, poi.POIID)
						}
						if !hasCorridor {
							return nil, nil
						}
					}
					return nil, poi
				}
			}
		}
		return nil, nil
	}

	const candidateRadiusSq = 32.0 * 32.0
	var nearby []*trunkCandidate
	var all []*trunkCandidate
	for _, c := range byColumn {
		all = append(all, c)
		if c.distSq <= candidateRadiusSq {
			nearby = append(nearby, c)
		}
	}
	candidates := nearby
	if len(candidates) == 0 {
		candidates = all
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distSq != candidates[j].distSq {
			return candidates[i].distSq < candidates[j].distSq
		}
		if candidates[i].tx != candidates[j].tx {
			return candidates[i].tx < candidates[j].tx
		}
		return candidates[i].tz < candidates[j].tz
	})

	// Deprioritize trunks where a visible peer AgentEntity is already standing within 2.5m horizontally,
	// spreading clustered agents across distinct trees whenever unoccupied trunks exist.
	if len(candidates) > 1 && len(frame.VisibleEntities) > 0 {
		var unoccupied []*trunkCandidate
		for _, c := range candidates {
			cx := float64(c.tx) + 0.5
			cz := float64(c.tz) + 0.5
			occupied := false
			for _, e := range frame.VisibleEntities {
				if e == nil || e.Position == nil || IsHostile(e) || e.IsTamed {
					continue
				}
				if agentID != "" && strings.EqualFold(e.DisplayName, agentID) {
					continue
				}
				et := strings.ToLower(e.EntityType)
				if !strings.Contains(et, "player") && !strings.Contains(et, "agent") {
					continue
				}
				if math.Hypot(e.Position.X-cx, e.Position.Z-cz) <= 2.5 {
					occupied = true
					break
				}
			}
			if !occupied {
				unoccupied = append(unoccupied, c)
			}
		}
		if len(unoccupied) > 0 {
			candidates = unoccupied
		}
	}

	idx := 0
	if len(candidates) > 1 && agentID != "" {
		idx = int(crc32.ChecksumIEEE([]byte(agentID))) % len(candidates)
	}
	return candidates[idx].anchor, nil
}

// bootstrapToolTargets is the ordered recipe-graph progression of tools the
// bootstrap stage crafts as materials allow: stone gear (sword, then a stone
// pickaxe) when cobblestone is on hand, then the wooden axe/sword, then the
// wooden pickaxe (cq-gpa.10). A target whose raw materials are missing is
// skipped by the planner, so listing stone first costs nothing without cobble.
func bootstrapToolTargets(inv bootstrapInventorySummary) []string {
	var targets []string
	if !inv.hasStoneGear {
		targets = append(targets, "minecraft:stone_sword")
	}
	if inv.pickaxeTier < TierStone {
		targets = append(targets, "minecraft:stone_pickaxe")
	}
	if !inv.hasAxe {
		targets = append(targets, "minecraft:wooden_axe")
	} else if !inv.hasSword {
		targets = append(targets, "minecraft:wooden_sword")
	}
	if !inv.hasPickaxe {
		targets = append(targets, "minecraft:wooden_pickaxe")
	}
	return targets
}
