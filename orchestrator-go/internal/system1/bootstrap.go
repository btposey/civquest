package system1

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type bootstrapInventorySummary struct {
	logCount            int32
	planksCount         int32
	stickCount          int32
	cobbleCount         int32
	nonLogBuildingCount int32
	buildingBlockCount  int32
	placeableBlockID    string
	hasAxe              bool
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

// EvaluateBootstrapGoals directly executes bootstrap timber harvesting, basic tool crafting,
// and local scouting for timber stands while maintaining the agent in StateBootstrap.
func EvaluateBootstrapGoals(
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

	// 1. Unsticking if STUCK
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		if obstruct := findObstructingBlockToDig(frame, pos); obstruct != nil {
			if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
				state.lastHarvestTick = frame.WorldTick
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_InteractBlock{
						InteractBlock: &civquestv1.InteractBlockIntent{
							Pos:             obstruct.Pos,
							InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
							BlockOrItemId:   obstruct.BlockId,
						},
					},
				}
			}
		}
	}

	// 2. Direct bootstrap timber gathering & tool crafting
	if bootstrapIntent := evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state); bootstrapIntent != nil {
		return bootstrapIntent
	}

	// 3. If no immediate trees or crafting table in frustum, scout locally for timber stands
	return evaluateGeneralExplorationLocked(agentID, frame, pos, state, cfg)
}

// EvaluateBootstrapGoalsDirect evaluates bootstrap goals directly given explicit pos and navState.
func EvaluateBootstrapGoalsDirect(
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
	return evaluateBootstrapGoalsLocked(agentID, frame, pos, navState, state)
}

func evaluateBootstrapGoalsLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	navState civquestv1.NavigationState,
	state *AgentState,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}

	inv := inspectBootstrapInventory(frame.Status.Inventory)

	canCraft := state.lastCraftTick == 0 || frame.WorldTick < state.lastCraftTick || frame.WorldTick-state.lastCraftTick >= 20
	canPlace := state.lastShelterPlaceTick == 0 || frame.WorldTick < state.lastShelterPlaceTick || frame.WorldTick-state.lastShelterPlaceTick >= 20

	needsStoneUpgrade := !inv.hasStoneGear && inv.cobbleCount >= 3 && (inv.logCount >= 1 || inv.planksCount >= 1 || inv.stickCount >= 1)
	needsWoodenTools := (!inv.hasAxe || !inv.hasSword) && (inv.logCount >= 1 || inv.planksCount >= 4)
	needsTools := needsStoneUpgrade || needsWoodenTools

	var nearestCraftingTable *civquestv1.VisibleBlock
	bestTableDistSq := math.MaxFloat64
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if strings.Contains(id, "crafting_table") {
			dx := float64(b.Pos.X) + 0.5 - pos.X
			dy := float64(b.Pos.Y) + 0.5 - pos.Y
			dz := float64(b.Pos.Z) + 0.5 - pos.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq < bestTableDistSq {
				bestTableDistSq = dSq
				nearestCraftingTable = b
			}
		}
	}

	if needsTools {
		if nearestCraftingTable == nil {
			if inv.hasCraftingTable {
				if canPlace {
					state.lastShelterPlaceTick = frame.WorldTick
					cx := int32(math.Floor(pos.X))
					cy := int32(math.Floor(pos.Y))
					cz := int32(math.Floor(pos.Z))
					return &civquestv1.AgentIntent{
						AgentId: agentID,
						Action: &civquestv1.AgentIntent_InteractBlock{
							InteractBlock: &civquestv1.InteractBlockIntent{
								Pos: &civquestv1.BlockPosProto{
									X: cx + 1,
									Y: cy,
									Z: cz,
								},
								InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
								BlockOrItemId:   "minecraft:crafting_table",
							},
						},
					}
				}
				return nil
			}

			if (inv.logCount >= 1 || inv.planksCount >= 4) && canCraft {
				state.lastCraftTick = frame.WorldTick
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_CraftItem{
						CraftItem: &civquestv1.CraftItemIntent{
							RecipeId:   "minecraft:crafting_table",
							BatchCount: 1,
						},
					},
				}
			}
		} else {
			if bestTableDistSq <= 20.25 {
				if needsStoneUpgrade && canCraft {
					state.lastCraftTick = frame.WorldTick
					return &civquestv1.AgentIntent{
						AgentId: agentID,
						Action: &civquestv1.AgentIntent_CraftItem{
							CraftItem: &civquestv1.CraftItemIntent{
								RecipeId:       "minecraft:stone_sword",
								WorkstationPos: nearestCraftingTable.Pos,
								BatchCount:     1,
							},
						},
					}
				}
				if needsWoodenTools && canCraft {
					recipe := "minecraft:wooden_axe"
					if inv.hasAxe && !inv.hasSword {
						recipe = "minecraft:wooden_sword"
					}
					state.lastCraftTick = frame.WorldTick
					return &civquestv1.AgentIntent{
						AgentId: agentID,
						Action: &civquestv1.AgentIntent_CraftItem{
							CraftItem: &civquestv1.CraftItemIntent{
								RecipeId:       recipe,
								WorkstationPos: nearestCraftingTable.Pos,
								BatchCount:     1,
							},
						},
					}
				}
			} else {
				if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
					if state.lastExploreTick == 0 || frame.WorldTick < state.lastExploreTick || frame.WorldTick-state.lastExploreTick >= 40 {
						state.lastExploreTick = frame.WorldTick
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
			}
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
