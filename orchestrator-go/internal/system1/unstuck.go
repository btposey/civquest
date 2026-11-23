package system1

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// CardinalDirs provides standard cardinal offsets: North (0, -1), South (0, 1), East (1, 0), West (-1, 0).
var CardinalDirs = [][2]int32{
	{0, -1}, // North
	{1, 0},  // East
	{0, 1},  // South
	{-1, 0}, // West
}

// IsDiggableObstruction checks if a block identifier corresponds to a mineable terrain or foliage block.
func IsDiggableObstruction(id string) bool {
	s := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.Index(s, ":"); idx != -1 {
		s = s[idx+1:]
	}
	return strings.Contains(s, "dirt") ||
		strings.Contains(s, "grass") ||
		strings.Contains(s, "stone") ||
		strings.Contains(s, "cobblestone") ||
		strings.Contains(s, "deepslate") ||
		strings.Contains(s, "sand") ||
		strings.Contains(s, "gravel") ||
		strings.Contains(s, "clay") ||
		strings.Contains(s, "mud") ||
		strings.Contains(s, "log") ||
		strings.Contains(s, "wood") ||
		strings.Contains(s, "stem") ||
		strings.Contains(s, "planks") ||
		strings.Contains(s, "leaves") ||
		strings.Contains(s, "foliage")
}

// FindVisibleBlockAt locates a block from InterestingBlocks at the exact integer coordinates (x, y, z).
func FindVisibleBlockAt(frame *civquestv1.TelemetryFrame, x, y, z int32) *civquestv1.VisibleBlock {
	if frame == nil {
		return nil
	}
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		if b.Pos.X == x && b.Pos.Y == y && b.Pos.Z == z {
			return b
		}
	}
	return nil
}

// EvaluateUnstuck performs deterministic pit/trench escape navigation, staircase excavation,
// and heading-locked obstacle clearing when the agent is STUCK or ENCLOSED.
func EvaluateUnstuck(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}

	navState := frame.Status.NavigationState
	isStuck := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK
	isEnclosed := frame.Status.GetNavigationEnclosed()

	locked, dirX, dirZ, startTick, startX, startZ := state.GetUnstuckLockLocked()

	// If neither stuck nor enclosed, check if we can unlock and return nil
	if !isStuck && !isEnclosed {
		if locked {
			dx := pos.X - startX
			dz := pos.Z - startZ
			if dx*dx+dz*dz >= 1.0 || (frame.WorldTick-startTick >= 40) {
				state.ClearUnstuckLockLocked()
			}
		}
		return nil
	}

	bx := int32(math.Floor(pos.X))
	by := int32(math.Floor(pos.Y))
	bz := int32(math.Floor(pos.Z))

	// Pick or renew locked escape direction
	if !locked || (frame.WorldTick-startTick > 120) {
		// Primary direction from heading:
		chosenDirX := int32(0)
		chosenDirZ := int32(1)

		cosH := math.Cos(state.lastHeadingRad)
		sinH := math.Sin(state.lastHeadingRad)
		if math.Abs(cosH) > math.Abs(sinH) {
			if cosH > 0 {
				chosenDirX = 1
				chosenDirZ = 0
			} else {
				chosenDirX = -1
				chosenDirZ = 0
			}
		} else {
			if sinH > 0 {
				chosenDirX = 0
				chosenDirZ = 1
			} else {
				chosenDirX = 0
				chosenDirZ = -1
			}
		}

		// Check if there is an obstruction in the heading direction:
		headingHasObstruction := false
		for dy := int32(0); dy <= 2; dy++ {
			if b := FindVisibleBlockAt(frame, bx+chosenDirX, by+dy, bz+chosenDirZ); b != nil && IsDiggableObstruction(b.BlockId) {
				headingHasObstruction = true
				break
			}
		}

		if !headingHasObstruction {
			// If heading is unobstructed, check which cardinal direction has an obstruction we can clear to escape
			foundObstruction := false
			for _, dir := range CardinalDirs {
				for dy := int32(0); dy <= 2; dy++ {
					if b := FindVisibleBlockAt(frame, bx+dir[0], by+dy, bz+dir[1]); b != nil && IsDiggableObstruction(b.BlockId) {
						chosenDirX = dir[0]
						chosenDirZ = dir[1]
						foundObstruction = true
						break
					}
				}
				if foundObstruction {
					break
				}
			}
		}

		dirX = chosenDirX
		dirZ = chosenDirZ
		locked = true
		startTick = frame.WorldTick
		startX = pos.X
		startZ = pos.Z
		state.SetUnstuckLockLocked(true, dirX, dirZ, startTick, startX, startZ)
	}

	// 1. Check overhead clearance directly above the agent: (bx, by+2, bz)
	// Agent needs 2 blocks of standing room + 1 block jump headroom to step up
	if overhead := FindVisibleBlockAt(frame, bx, by+2, bz); overhead != nil && IsDiggableObstruction(overhead.BlockId) {
		if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
			state.lastHarvestTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             overhead.Pos,
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   overhead.BlockId,
					},
				},
			}
		}
		return nil // Wait for block harvest to register, do NOT rotate heading
	}

	step1X := bx + dirX
	step1Z := bz + dirZ

	// 2. Staircase excavation at Distance 1 (step1X, step1Z):
	// Break Y=by+1 (eye level) first to form a 1-block step-up onto Y=by
	if obs := FindVisibleBlockAt(frame, step1X, by+1, step1Z); obs != nil && IsDiggableObstruction(obs.BlockId) {
		if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
			state.lastHarvestTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             obs.Pos,
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   obs.BlockId,
					},
				},
			}
		}
		return nil
	}

	// Check Y=by+2 (overhead clearance on step 1)
	if obs := FindVisibleBlockAt(frame, step1X, by+2, step1Z); obs != nil && IsDiggableObstruction(obs.BlockId) {
		if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
			state.lastHarvestTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             obs.Pos,
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   obs.BlockId,
					},
				},
			}
		}
		return nil
	}

	// Step 1 eye and headroom are clear!
	// Check if step 1 has a solid block at Y=by (foot level).
	step1Foot := FindVisibleBlockAt(frame, step1X, by, step1Z)
	step1FootSolid := step1Foot != nil && IsDiggableObstruction(step1Foot.BlockId)

	// Check Distance 2 for continuing staircase: (bx+2*dirX, bz+2*dirZ)
	step2X := bx + 2*dirX
	step2Z := bz + 2*dirZ

	if step1FootSolid {
		// Step 1 is a 1-block elevated step (Y=by).
		// For the agent to jump onto it, ensure step 2 headroom (Y=by+2 and Y=by+3) is clear
		if obs := FindVisibleBlockAt(frame, step2X, by+2, step2Z); obs != nil && IsDiggableObstruction(obs.BlockId) {
			if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
				state.lastHarvestTick = frame.WorldTick
				return &civquestv1.AgentIntent{
					AgentId: agentID,
					Action: &civquestv1.AgentIntent_InteractBlock{
						InteractBlock: &civquestv1.InteractBlockIntent{
							Pos:             obs.Pos,
							InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
							BlockOrItemId:   obs.BlockId,
						},
					},
				}
			}
			return nil
		}

		// Step 1 is clear to jump onto! Emit navigation intent stepping up onto step 1
		targetX := float64(step1X) + 0.5
		targetY := float64(by) + 1.0
		targetZ := float64(step1Z) + 0.5
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: targetX,
						Y: targetY,
						Z: targetZ,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 1,
				},
			},
		}
	}

	// If step1Foot is NOT solid (flat opening), check distance 2
	if obs := FindVisibleBlockAt(frame, step2X, by+1, step2Z); obs != nil && IsDiggableObstruction(obs.BlockId) {
		if state.lastHarvestTick == 0 || frame.WorldTick < state.lastHarvestTick || frame.WorldTick-state.lastHarvestTick >= 20 {
			state.lastHarvestTick = frame.WorldTick
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_InteractBlock{
					InteractBlock: &civquestv1.InteractBlockIntent{
						Pos:             obs.Pos,
						InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
						BlockOrItemId:   obs.BlockId,
					},
				},
			}
		}
		return nil
	}

	// Flat walkway is open! Emit navigate forward
	targetX := float64(step1X) + 0.5
	targetY := float64(by)
	targetZ := float64(step1Z) + 0.5
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: targetX,
					Y: targetY,
					Z: targetZ,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 1,
			},
		},
	}
}
