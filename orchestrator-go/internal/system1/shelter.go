package system1

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluateShelterGoals directly executes dusk/night forward outpost shelter construction and placement.
func EvaluateShelterGoals(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	prog *shelterLocal,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.Status.IsSleeping {
		return nil
	}

	pos := frame.Status.Position
	state.mu.Lock()
	defer state.mu.Unlock()
	walk := shelterWalk(prog)

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

	navState := frame.Status.NavigationState
	return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state, prog, walk)
}

// EvaluateShelterGoalsDirect evaluates dusk/night outpost shelter goals with explicit pos and navState.
func EvaluateShelterGoalsDirect(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	prog *shelterLocal,
) *civquestv1.AgentIntent {
	if frame == nil || pos == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state, prog, shelterWalk(prog))
}

func evaluateDuskNightOutpostShelterLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
	prog *shelterLocal,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}
	walk.navigatingToPOI = false

	// Build progress belongs to the Shelter frame's Local. Callers outside a Shelter frame
	// (prog == nil) evaluate against a read-only snapshot of the store: they may navigate,
	// harvest and craft, but never place cabin blocks.
	owned := prog != nil
	if prog == nil {
		scratch := seedShelterLocalLocked(state)
		prog = &scratch
	}

	outpost := state.currentOutpost
	if outpost == nil {
		outpost = state.homeOrigin
	}

	isStuckOrEnclosed := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed()
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	hasBuildMaterials := inv.logCount > 0 || inv.planksCount > 0 || inv.buildingBlockCount > 0
	hasShelterMaterials := hasBuildMaterials || inv.hasLight
	isShelterComplete := prog.complete()

	// ADR-022: Recall known bed or campfire from SpatialIndex before establishing a duplicate
	// forward outpost or placing duplicate habitation blocks.
	recalledHabPOI := findNearestReliableHabitationPOI(pos, state, "", frame.WorldTick)
	if recalledHabPOI != nil {
		dx := recalledHabPOI.X - pos.X
		dy := recalledHabPOI.Y + 0.5 - pos.Y
		dz := recalledHabPOI.Z - pos.Z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= 12.25 && !hasVisibleHabitationAt(frame, poiBlockPos(recalledHabPOI)) {
			if si := state.SpatialIndexLocked(); si != nil {
				si.RemovePOI(recalledHabPOI.POIID)
			}
			recalledHabPOI = nil
		}
	}

	// What the cabin's next block needs, when it is a door or a window (shelter_fittings.go).
	var fitting fittingView
	if owned {
		fitting = state.lookAtFittingLocked(prog, frame, pos, inv)
		isShelterComplete = prog.complete()
	}

	// If the Explorer has traveled > 32 blocks from the current outpost along their path of travel,
	// carries shelter/light materials, and no known habitation POI exists within 64 blocks,
	// establish a new Forward Outpost.
	if hasShelterMaterials && !fitting.fetching() && recalledHabPOI == nil && outpost != nil && math.Hypot(pos.X-outpost.X, pos.Z-outpost.Z) > 32.0 {
		newOutpost := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		state.currentOutpost = newOutpost
		state.homeOrigin = newOutpost
		state.outposts = append(state.outposts, newOutpost)
		prog.Pos = &civquestv1.BlockPosProto{
			X: int32(math.Floor(pos.X)),
			Y: int32(math.Floor(pos.Y)),
			Z: int32(math.Floor(pos.Z)),
		}
		// A new outpost starts a new cabin.
		prog.BuildIndex, prog.Phase = 0, 0
		isShelterComplete = false
		// Reset lastMapNodeID so EvaluateTopologicalMapping records this sector as OUTPOST
		state.lastMapNodeID = ""
		walk.frontierRadialLeg = 0
		walk.currentExploreTarget = nil
		walk.lastExploreProgressDist = 0
		walk.lastExploreProgressTick = frame.WorldTick
		outpost = newOutpost
	}

	targetX := pos.X
	targetY := pos.Y
	targetZ := pos.Z
	if recalledHabPOI != nil && !fitting.fetching() && (hasShelterMaterials || isShelterComplete || strings.Contains(strings.ToLower(recalledHabPOI.BlockID), "bed")) &&
		(prog.Pos == nil || math.Hypot(pos.X-float64(prog.Pos.X), pos.Z-float64(prog.Pos.Z)) > 32.0) {
		accessTarget := poiAccessTarget(recalledHabPOI)
		if math.Hypot(pos.X-recalledHabPOI.X, pos.Z-recalledHabPOI.Z) > 4.0 && !poiWalkOver(pos, recalledHabPOI) && !isStuckOrEnclosed {
			if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
				return nil
			}
			if walk.lastBootstrapNavTick == 0 || frame.WorldTick < walk.lastBootstrapNavTick || frame.WorldTick-walk.lastBootstrapNavTick >= 40 {
				walk.lastBootstrapNavTick = frame.WorldTick
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_Navigate{
						Navigate: &civquestv1.NavigateIntent{
							Target:           accessTarget,
							SpeedMultiplier:  1.0,
							ArrivalTolerance: 2,
						},
					},
				}
			}
			return nil
		}
	}
	if prog.Pos != nil && (isShelterComplete || math.Hypot(pos.X-float64(prog.Pos.X), pos.Z-float64(prog.Pos.Z)) <= 32.0) {
		targetX = float64(prog.Pos.X) + 0.5
		targetY = float64(prog.Pos.Y)
		targetZ = float64(prog.Pos.Z) + 0.5
	} else if outpost != nil {
		targetX = outpost.X
		targetY = outpost.Y
		targetZ = outpost.Z
	}

	// Only pull toward homeOrigin / ShelterPos when carrying materials to build/light shelter,
	// or when shelter is already complete and the agent is seeking refuge for the night.
	distToShelter := math.Hypot(pos.X-targetX, pos.Z-targetZ)
	if distToShelter > 4.0 && state.givenUpLocked(targetX, targetY, targetZ, frame.WorldTick) {
		return nil // no way back was found: neither walk there nor build at it from here
	}
	if (hasShelterMaterials || isShelterComplete) && !fitting.fetching() && distToShelter > 4.0 && !isStuckOrEnclosed {
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return nil
		}
		if frame.WorldTick >= walk.lastExploreTick && frame.WorldTick-walk.lastExploreTick < 40 {
			return nil
		}
		walk.lastExploreTick = frame.WorldTick
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

	invMap := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	recipeGraph := state.RecipeGraphLocked()
	canCraft := state.mem.lastCraftTick == 0 || frame.WorldTick < state.mem.lastCraftTick || frame.WorldTick-state.mem.lastCraftTick >= 20
	canPlace := state.mem.lastShelterPlaceTick == 0 || frame.WorldTick < state.mem.lastShelterPlaceTick || frame.WorldTick-state.mem.lastShelterPlaceTick >= 20

	cx := int32(math.Floor(pos.X))
	cy := int32(math.Floor(pos.Y))
	cz := int32(math.Floor(pos.Z))

	if prog.Pos == nil && (hasShelterMaterials || isShelterComplete) {
		prog.Pos = &civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz}
	}
	centerVec := &civquestv1.Vec3{
		X: float64(cx),
		Y: float64(cy),
		Z: float64(cz),
	}
	if prog.Pos != nil {
		centerVec = &civquestv1.Vec3{
			X: float64(prog.Pos.X),
			Y: float64(prog.Pos.Y),
			Z: float64(prog.Pos.Z),
		}
	}
	cabinIntents := blueprints.FrontierCabinIntents(centerVec)

	// Check if all cabin blueprint blocks have been completed
	if prog.BuildIndex >= len(cabinIntents) {
		distToCenter := math.Hypot(pos.X-centerVec.X-0.5, pos.Z-centerVec.Z-0.5)
		if distToCenter > 1.5 && navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: centerVec.X + 0.5,
							Y: centerVec.Y,
							Z: centerVec.Z + 0.5,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 1,
					},
				},
			}
		}
		return nil
	}

	// 1. Convert logs in inventory into planks via dynamic RecipeGraph if building blocks are needed
	remainingBlocks := len(cabinIntents) - prog.BuildIndex
	if remainingBlocks < 0 {
		remainingBlocks = 0
	}
	if inv.logCount >= 1 && inv.nonLogBuildingCount < 16 && inv.nonLogBuildingCount < int32(remainingBlocks) &&
		prog.BuildIndex < len(cabinIntents) && canCraft && !recipeGraph.IsEmpty() {
		if plankStep, ok := recipeGraph.PlanNextStepForSuffix("_planks", invMap, false); ok && plankStep.CanExecuteNow {
			state.mem.lastCraftTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_CraftItem{
					CraftItem: &civquestv1.CraftItemIntent{
						RecipeId:   plankStep.RecipeID,
						BatchCount: 1,
					},
				},
			}
		}
	}

	// 2. Pick up wood that was cut and left lying, then harvest more when stock is low.
	if prog.BuildIndex < len(cabinIntents) && navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		if drop := nearbyDrop(state, frame, isLogBlockID); drop != nil &&
			(walk.lastExploreTick == 0 || frame.WorldTick < walk.lastExploreTick || frame.WorldTick-walk.lastExploreTick >= 40) {
			walk.lastExploreTick = frame.WorldTick
			return walkToDrop(agentID, drop)
		}
	}
	lowStock := inv.buildingBlockCount < 16 && inv.logCount < 4
	switch {
	case fitting.fetching():
		lowStock = fitting.timberShort(state, frame, pos, prog.Pos, inv)
	case fitting.held:
		lowStock = false // hang it before going for more
	}
	if lowStock && prog.BuildIndex < len(cabinIntents) {
		var nearestLog *civquestv1.VisibleBlock
		bestDistSq := math.MaxFloat64

		treeLocked, tx, tz := prog.targetTree()
		var focusedLog *civquestv1.VisibleBlock
		focusedDistSq := math.MaxFloat64

		// A tree once begun is finished; otherwise the nearest log of a tree worth felling
		// now (tree_choice.go).
		trunks := usableTrunks(frame, pos, state, nil)
		squared := func(b *civquestv1.VisibleBlock) float64 {
			dx, dy, dz := float64(b.Pos.X)+0.5-pos.X, float64(b.Pos.Y)+0.5-pos.Y, float64(b.Pos.Z)+0.5-pos.Z
			return dx*dx + dy*dy + dz*dz
		}
		for _, t := range preferredTrunks(trunks) {
			for _, b := range t.logs {
				if d := squared(b); d < bestDistSq {
					nearestLog, bestDistSq = b, d
				}
			}
		}
		for _, t := range trunks {
			if !treeLocked || t.base.Pos.X != tx || t.base.Pos.Z != tz {
				continue
			}
			for _, b := range t.logs {
				if d := squared(b); d < focusedDistSq {
					focusedLog, focusedDistSq = b, d
				}
			}
		}

		targetLog := nearestLog
		targetDistSq := bestDistSq
		if focusedLog != nil {
			targetLog = focusedLog
			targetDistSq = focusedDistSq
		}

		if targetLog != nil {
			prog.focusTree(targetLog.Pos.X, targetLog.Pos.Z)
			now, spent := breakOrWalk(pos, targetLog.Pos, targetDistSq <= 20.25) // Within 4.5 blocks melee/harvest reach
			if spent {
				state.MarkWorkstationUnreachableLocked(targetLog.Pos.X, targetLog.Pos.Y, targetLog.Pos.Z, frame.WorldTick)
				return nil
			}
			if now {
				if state.mem.lastHarvestTick == 0 || frame.WorldTick < state.mem.lastHarvestTick || frame.WorldTick-state.mem.lastHarvestTick >= 20 {
					state.mem.lastHarvestTick = frame.WorldTick
					return &civquestv1.AgentIntent{
						AgentId: agentID,
						Action: &civquestv1.AgentIntent_InteractBlock{
							InteractBlock: &civquestv1.InteractBlockIntent{
								Pos:             targetLog.Pos,
								InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
								BlockOrItemId:   targetLog.BlockId,
							},
						},
					}
				}
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
									X: float64(targetLog.Pos.X) + 0.5,
									Y: float64(targetLog.Pos.Y),
									Z: float64(targetLog.Pos.Z) + 0.5,
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

		// ADR-022: No logs in local 16m frustum; query SpatialIndex for remembered tree stands.
		if treeLocked {
			prog.clearTreeFocus()
		}
		if _, recalledTreePOI := findNearestTreeTrunk(agentID, frame, pos, state); recalledTreePOI != nil {
			if navIntent, handled := navigateToRecalledTreePOILocked(agentID, frame, pos, navState, state, recalledTreePOI, walk); handled {
				return navIntent
			}
		}
	}

	// 2. Check if a campfire, torch, or lantern is nearby within 8.0 blocks
	hasNearbyLight := lightNearby(frame, pos)

	// Only a light that stays resets the count: one placed in water shows for a moment and is
	// washed away (play test 8: placed 625 times in a night).
	switch {
	case !hasNearbyLight:
		prog.LightSeen = 0
	case prog.LightSeen == 0 || frame.WorldTick < prog.LightSeen:
		prog.LightSeen = frame.WorldTick
	case frame.WorldTick-prog.LightSeen >= shelterLightStays:
		prog.LightTries = 0
	}
	if !hasNearbyLight {
		// Query SpatialIndex for a known campfire before placing/crafting a duplicate campfire,
		// only when holding shelter materials or returning to a completed shelter.
		if hasShelterMaterials || isShelterComplete {
			if recalledLight := findNearestReliableHabitationPOI(pos, state, "campfire", frame.WorldTick); recalledLight != nil {
				if math.Hypot(pos.X-recalledLight.X, pos.Z-recalledLight.Z) > 4.0 && !poiWalkOver(pos, recalledLight) && !isStuckOrEnclosed {
					if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
						return nil
					}
					if walk.lastBootstrapNavTick == 0 || frame.WorldTick < walk.lastBootstrapNavTick || frame.WorldTick-walk.lastBootstrapNavTick >= 40 {
						walk.lastBootstrapNavTick = frame.WorldTick
						return &civquestv1.AgentIntent{
							AgentId: agentID,
							Action: &civquestv1.AgentIntent_Navigate{
								Navigate: &civquestv1.NavigateIntent{
									Target:           poiAccessTarget(recalledLight),
									SpeedMultiplier:  1.0,
									ArrivalTolerance: 2,
								},
							},
						}
					}
					return nil
				}
			}
		}
		// A light that never appears is tried on other cells, then left: seen live, one
		// torch was placed on the same cell 212 times.
		if inv.hasLight && canPlace && prog.LightTries < shelterLightTries && !frame.Status.IsInWater {
			state.mem.lastShelterPlaceTick = frame.WorldTick
			lightPos := &civquestv1.BlockPosProto{
				X: cx + 1,
				Y: cy,
				Z: cz + 1,
			}
			if prog.LightTries > 0 {
				cells := placementCells(frame, pos)
				lightPos = cells[(prog.LightTries-1)%len(cells)]
			}
			prog.LightTries++
			if isWaterAt(frame, lightPos) {
				return nil // try the next cell
			}
			if prog.Pos == nil {
				prog.Pos = &civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz}
			}
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             lightPos,
						InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
						BlockOrItemId:   inv.lightItemID,
					},
				},
			}
		}
		if !inv.hasLight && canCraft && !recipeGraph.IsEmpty() {
			nearestTable, _, bestTableDistSq := findNearestCraftingTableLocked(frame, pos, state)
			if craftIntent := craftLightSourceIntentLocked(agentID, frame.WorldTick, invMap, nearestTable, bestTableDistSq, pos, state, nil); craftIntent != nil {
				return craftIntent
			}
		}
	}

	// 3. Build the 5x5 Frontier Cabin (ADR-010): the structure bottom-up, then door and windows
	if owned && prog.BuildIndex < len(cabinIntents) {
		// The block is asked for as drawn, from what is held; a door or a window from its fill.
		fill := ""
		if at := blueprints.CabinFittingAt(prog.BuildIndex); at != "" && !prog.fittingWalled() {
			var held bool
			if fill, held = blueprints.FittingFill(at, heldItemIDs(frame.Status.Inventory)); !held {
				if !fitting.fetching() {
					return nil // the site is far: the walk back comes first
				}
				return state.fittingStepLocked(agentID, at, frame, pos, navState, prog)
			}
		} else if inv.buildingBlockCount <= 0 {
			if prog.fittingWalled() {
				state.passOverFittingLocked(prog, inv)
			}
			return nil
		}
		if !canPlace {
			return nil
		}
		target := cabinIntents[prog.BuildIndex]

		// Ensure the agent is positioned outside the target block voxel before placing.
		// If pos is within 1.0 block horizontally and vertically of target.GetTargetVoxel(),
		// emit a micro-step NavigateIntent to step back outside the construction boundary.
		if targetVoxel := target.GetTargetVoxel(); targetVoxel != nil && blueprints.IsCollidingWithVoxel(pos, targetVoxel) {
			if frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
				return nil // already stepping back
			}
			stepBackPos := blueprints.CalculateCabinStepBack(pos, targetVoxel, prog.Pos)
			if state.givenUpLocked(stepBackPos.X, pos.Y, stepBackPos.Z, frame.WorldTick) {
				// There is no getting out of this block's way: pass it over (play test 9).
				prog.BuildIndex++
				prog.Phase = cabinPhase(prog.BuildIndex)
				return nil
			}
			walk.lastExploreTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: stepBackPos.X,
							Y: pos.Y,
							Z: stepBackPos.Z,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 1,
					},
				},
			}
		}

		if voxel := target.GetTargetVoxel(); voxel != nil && prog.Pos != nil && !withinPlacingReach(pos, voxel) {
			if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING || prog.approachUnderWay(frame.WorldTick) {
				return nil
			}
			if stand, ok := prog.approach(pos, voxel); ok && !state.givenUpLocked(stand.X, stand.Y, stand.Z, frame.WorldTick) {
				prog.WalkTick = frame.WorldTick
				walk.lastExploreTick = frame.WorldTick
				return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
					Target: stand, SpeedMultiplier: 1.0, ArrivalTolerance: 1,
				}}}
			}
		}

		blockID := fill
		if blockID == "" {
			drawn := target.GetBlockStateString()
			if prog.fittingWalled() {
				drawn = blueprints.BlockOakPlanks
			} else if drawn == "" {
				drawn = inv.placeableBlockID
			}
			blockID = cabinBlock(drawn, frame.Status.Inventory, inv.placeableBlockID)
		}

		prog.Phase = blueprints.CabinBlockPhase(prog.BuildIndex)
		prog.BuildIndex++
		prog.Pending = prog.BuildIndex
		state.mem.lastShelterPlaceTick = frame.WorldTick

		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos:             target.GetTargetVoxel(),
					InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
					BlockOrItemId:   blockID,
				},
			},
		}
	}

	return nil
}

// lightNearby reports a campfire, torch or lantern in view within 8 blocks.
func lightNearby(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) bool {
	for _, b := range frame.GetInterestingBlocks() {
		if b == nil || b.Pos == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if (strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern")) &&
			math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z)) <= 8.0 {
			return true
		}
	}
	return false
}

func findNearestReliableHabitationPOI(pos *civquestv1.Vec3, state *AgentState, preferredBlock string, worldTick int64) *db.AgentPOI {
	if pos == nil || state == nil {
		return nil
	}
	agentLayer := ClassifyElevationLayer(pos.Y, 64.0)
	if preferredBlock != "" {
		if poi := state.findNearestReachablePOILocked(pos, POICategoryHabitation, preferredBlock, agentLayer, 64.0, 0.5, worldTick); poi != nil {
			return poi
		}
		return state.findNearestReachablePOILocked(pos, POICategoryHabitation, preferredBlock, "", 64.0, 0.5, worldTick)
	}
	bedPOI := state.findNearestReachablePOILocked(pos, POICategoryHabitation, "bed", agentLayer, 64.0, 0.5, worldTick)
	if bedPOI == nil {
		bedPOI = state.findNearestReachablePOILocked(pos, POICategoryHabitation, "bed", "", 64.0, 0.5, worldTick)
	}
	campPOI := state.findNearestReachablePOILocked(pos, POICategoryHabitation, "campfire", agentLayer, 64.0, 0.5, worldTick)
	if campPOI == nil {
		campPOI = state.findNearestReachablePOILocked(pos, POICategoryHabitation, "campfire", "", 64.0, 0.5, worldTick)
	}
	if bedPOI == nil {
		return campPOI
	}
	if campPOI == nil {
		return bedPOI
	}
	bedDistSq := (bedPOI.X-pos.X)*(bedPOI.X-pos.X) + (bedPOI.Y-pos.Y)*(bedPOI.Y-pos.Y) + (bedPOI.Z-pos.Z)*(bedPOI.Z-pos.Z)
	campDistSq := (campPOI.X-pos.X)*(campPOI.X-pos.X) + (campPOI.Y-pos.Y)*(campPOI.Y-pos.Y) + (campPOI.Z-pos.Z)*(campPOI.Z-pos.Z)
	if bedDistSq <= campDistSq {
		return bedPOI
	}
	return campPOI
}

func hasVisibleHabitationAt(frame *civquestv1.TelemetryFrame, pos *civquestv1.BlockPosProto) bool {
	if frame == nil || pos == nil {
		return false
	}
	b := FindVisibleBlockAt(frame, pos.X, pos.Y, pos.Z)
	if b == nil {
		return false
	}
	id := strings.ToLower(b.BlockId)
	return strings.Contains(id, "bed") || strings.Contains(id, "campfire")
}
