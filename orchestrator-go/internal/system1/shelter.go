package system1

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluateShelterGoals directly executes dusk/night forward outpost shelter construction and placement.
func EvaluateShelterGoals(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
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

	navState := frame.Status.NavigationState
	return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state)
}

// EvaluateShelterGoalsDirect evaluates dusk/night outpost shelter goals with explicit pos and navState.
func EvaluateShelterGoalsDirect(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
) *civquestv1.AgentIntent {
	if frame == nil || pos == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return evaluateDuskNightOutpostShelterLocked(agentID, frame, pos, navState, state)
}

func evaluateDuskNightOutpostShelterLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}

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
		state.frontierRadialLeg = 0
		state.currentExploreTarget = nil
		state.lastExploreProgressDist = 0
		state.lastExploreProgressTick = frame.WorldTick
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
	isStuckOrEnclosed := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed()
	if distToShelter > 4.0 && !isStuckOrEnclosed {
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

	inv := inspectBootstrapInventory(frame.Status.Inventory)
	canCraft := state.lastCraftTick == 0 || frame.WorldTick < state.lastCraftTick || frame.WorldTick-state.lastCraftTick >= 20
	canPlace := state.lastShelterPlaceTick == 0 || frame.WorldTick < state.lastShelterPlaceTick || frame.WorldTick-state.lastShelterPlaceTick >= 20

	cx := int32(math.Floor(pos.X))
	cy := int32(math.Floor(pos.Y))
	cz := int32(math.Floor(pos.Z))

	if state.ShelterPos == nil {
		state.ShelterPos = &civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz}
	}
	centerVec := &civquestv1.Vec3{
		X: float64(state.ShelterPos.X),
		Y: float64(state.ShelterPos.Y),
		Z: float64(state.ShelterPos.Z),
	}
	cabinIntents := blueprints.FrontierCabinIntents(centerVec)

	// Check if all cabin blueprint blocks have been completed
	if state.ShelterBuildIndex >= len(cabinIntents) {
		state.shelterBlocksPlaced = len(cabinIntents)
		distToCenter := math.Hypot(pos.X-float64(state.ShelterPos.X)-0.5, pos.Z-float64(state.ShelterPos.Z)-0.5)
		if distToCenter > 1.5 && navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: float64(state.ShelterPos.X) + 0.5,
							Y: float64(state.ShelterPos.Y),
							Z: float64(state.ShelterPos.Z) + 0.5,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 1,
					},
				},
			}
		}
		return nil
	}

	// 1. Convert logs in inventory into oak_planks if building blocks are needed
	if inv.logCount >= 1 && (inv.nonLogBuildingCount < 16 || state.ShelterBuildIndex < len(cabinIntents)) && canCraft {
		state.lastCraftTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_CraftItem{
				CraftItem: &civquestv1.CraftItemIntent{
					RecipeId:   "minecraft:oak_planks",
					BatchCount: 1,
				},
			},
		}
	}

	// 2. Harvest timber when building blocks are depleted
	if inv.buildingBlockCount == 0 && inv.logCount == 0 && state.ShelterBuildIndex < len(cabinIntents) {
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

			if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
				if state.lastExploreTick == 0 || frame.WorldTick < state.lastExploreTick || frame.WorldTick-state.lastExploreTick >= 40 {
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
	}

	// 2. Check if a campfire, torch, or lantern is nearby within 8.0 blocks
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

	if !hasNearbyLight {
		if inv.hasLight && canPlace {
			state.lastShelterPlaceTick = frame.WorldTick
			lightPos := &civquestv1.BlockPosProto{
				X: cx + 1,
				Y: cy,
				Z: cz + 1,
			}
			if state.ShelterPos == nil {
				state.ShelterPos = &civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz}
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
		if !inv.hasLight && (inv.logCount >= 1 || inv.planksCount >= 1 || inv.stickCount >= 1) && canCraft {
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

	// 3. Progressively build bottom-up 5x5 Frontier Cabin structure per ADR-010
	if inv.buildingBlockCount > 0 && state.ShelterBuildIndex < len(cabinIntents) && canPlace {
		target := cabinIntents[state.ShelterBuildIndex]

		// Ensure the agent is positioned outside the target block voxel before placing.
		// If pos is within 1.0 block horizontally and vertically of target.GetTargetVoxel(),
		// emit a micro-step NavigateIntent to step back outside the construction boundary.
		if targetVoxel := target.GetTargetVoxel(); targetVoxel != nil && blueprints.IsCollidingWithVoxel(pos, targetVoxel) {
			stepBackPos := blueprints.CalculateCabinStepBack(pos, targetVoxel, state.ShelterPos)
			state.lastExploreTick = frame.WorldTick
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

		state.ShelterBuildPhase = blueprints.CabinBlockPhase(state.ShelterBuildIndex)
		state.ShelterBuildIndex++
		state.shelterBlocksPlaced = state.ShelterBuildIndex
		state.lastShelterPlaceTick = frame.WorldTick

		blockID := target.GetBlockStateString()
		if blockID == "" {
			blockID = inv.placeableBlockID
		}

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
