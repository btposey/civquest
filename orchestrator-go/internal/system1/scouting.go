package system1

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluateScouting computes autonomous frontier scouting waypoints, radial sector azimuth expansion,
// forward outpost creation, and terrain unsticking / contouring maneuvers for Explorers, Scouts, and Trailblazers.
func EvaluateScouting(
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

	if state.companionActive {
		return nil
	}

	navState := frame.Status.NavigationState

	// 1. Immediate terrain unsticking / dig-out or contouring detour when STUCK or ENCLOSED
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
		if unstuckIntent := EvaluateUnstuck(agentID, frame, pos, state); unstuckIntent != nil {
			return unstuckIntent
		}
		if locked, _, _, _, _, _ := state.GetUnstuckLockLocked(); locked {
			return nil // Waiting on excavation sequence, do NOT rotate heading
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

	// Require at least 2 observed frames so single-frame unit tests are not prematurely triggered
	if state.telemetryFramesSeen < 2 {
		return nil
	}

	// 2. Terrain-Aware Exploration Strategies (Valleys, Ridges, Fords, Rims, Flatlands, Canopy Trailblazing)
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

	// 3. Radial sector azimuth frontier scouting
	return evaluateScoutingLocked(agentID, frame, pos, state, cfg)
}

// EvaluateFrontierScouting is an alias for EvaluateScouting.
func EvaluateFrontierScouting(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
) *civquestv1.AgentIntent {
	return EvaluateScouting(agentID, frame, cfg, state)
}

// EvaluateScoutingDirect evaluates scouting with explicit position under locked state.
func EvaluateScoutingDirect(
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
	return evaluateScoutingLocked(agentID, frame, pos, state, cfg)
}

func evaluateScoutingLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}

	navState := frame.Status.NavigationState

	// 1. Initialize sector azimuth if not yet set
	if !state.frontierAzimuthSet {
		var azimuth float64
		if state.lastHeadingRad != 0 {
			azimuth = state.lastHeadingRad
		} else {
			h := fnv1aString(agentID)
			azimuth = float64(h%360) * (math.Pi / 180.0)
		}
		azimuth = math.Atan2(math.Sin(azimuth), math.Cos(azimuth))
		state.frontierAzimuthRad = azimuth
		state.frontierAzimuthSet = true
		state.lastHeadingRad = azimuth
		state.frontierRadialLeg = 0
		state.currentExploreTarget = nil
	}

	origin := state.currentOutpost
	if origin == nil {
		origin = state.homeOrigin
	}
	if origin == nil {
		origin = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	}

	cooldownTicks := int64(60) // 3 seconds between scouting legs for Explorers
	legStep := 48.0            // Progressive 48m radial legs (48m -> 96m -> 144m -> 192m)

	// 3. Evaluate arrival, stuck, and timeout conditions
	hasActiveTarget := state.currentExploreTarget != nil
	if !hasActiveTarget {
		if state.lastExploreTick > 0 && frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < cooldownTicks {
			return nil
		}
	}
	var distToTarget float64
	if hasActiveTarget {
		distToTarget = math.Hypot(pos.X-state.currentExploreTarget.X, pos.Z-state.currentExploreTarget.Z)
	}

	arrived := hasActiveTarget && (distToTarget <= 2.0 || navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	stuck := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK

	timedOut := false
	if hasActiveTarget && !arrived && !stuck {
		if distToTarget < state.lastExploreProgressDist-1.0 {
			// Forward progress was made toward waypoint
			state.lastExploreProgressDist = distToTarget
			state.lastExploreProgressTick = frame.WorldTick
		} else if state.lastExploreProgressTick > 0 && frame.WorldTick-state.lastExploreProgressTick >= 600 {
			// 30 seconds (600 ticks) of zero forward progress
			timedOut = true
		}
	}

	// If actively navigating toward current waypoint and not arrived/stuck/timed out, DO NOT re-issue!
	isActivelyNavigating := navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	if hasActiveTarget && !arrived && !stuck && !timedOut {
		if isActivelyNavigating {
			return nil
		}
		// If idle/unspecified and cooldown hasn't passed, do not re-issue
		if frame.WorldTick >= state.lastExploreTick && frame.WorldTick-state.lastExploreTick < 40 {
			return nil
		}
	}

	// 4. Advance leg or pivot azimuth
	if arrived {
		// Advance to next radial leg along the SAME sector azimuth
		state.frontierRadialLeg++
		state.exploreStepIndex++
	} else if stuck || timedOut {
		// Rotate to a new sector azimuth and reset to leg 0
		newAz := math.Atan2(math.Sin(state.frontierAzimuthRad+1.2), math.Cos(state.frontierAzimuthRad+1.2))
		state.frontierAzimuthRad = newAz
		state.lastHeadingRad = state.frontierAzimuthRad
		state.frontierRadialLeg = 0
		state.exploreStepIndex++
	} else if hasActiveTarget && !isActivelyNavigating {
		// Baritone is idle but agent hasn't arrived/stuck/timedOut and cooldown expired;
		// re-issue navigation to the SAME target
		state.lastExploreTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: state.currentExploreTarget.X,
						Y: pos.Y,
						Z: state.currentExploreTarget.Z,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	// 5. Generate target for current/new leg
	legDist := float64(state.frontierRadialLeg+1) * legStep
	targetX := origin.X + math.Cos(state.frontierAzimuthRad)*legDist
	targetZ := origin.Z + math.Sin(state.frontierAzimuthRad)*legDist
	newTarget := &civquestv1.Vec3{
		X: math.Round(targetX*10) / 10,
		Y: pos.Y,
		Z: math.Round(targetZ*10) / 10,
	}

	state.currentExploreTarget = newTarget
	state.lastExploreProgressDist = math.Hypot(pos.X-newTarget.X, pos.Z-newTarget.Z)
	state.lastExploreProgressTick = frame.WorldTick
	state.lastExploreTick = frame.WorldTick

	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:           newTarget,
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}

func findObstructingBlockToDig(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) *civquestv1.VisibleBlock {
	if frame == nil || pos == nil {
		return nil
	}
	bx := int32(math.Floor(pos.X))
	by := int32(math.Floor(pos.Y))
	bz := int32(math.Floor(pos.Z))

	var bestHead, bestFeet *civquestv1.VisibleBlock
	bestHeadDist := math.MaxFloat64
	bestFeetDist := math.MaxFloat64

	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		if b.Pos.X == bx && b.Pos.Z == bz {
			continue
		}
		if b.Pos.Y != by && b.Pos.Y != by+1 {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(b.BlockId))
		if !isDiggableObstruction(id) {
			continue
		}
		dx := float64(b.Pos.X) + 0.5 - pos.X
		dz := float64(b.Pos.Z) + 0.5 - pos.Z
		hDist := math.Hypot(dx, dz)
		if hDist > 2.5 {
			continue
		}
		if b.Pos.Y == by+1 {
			if hDist < bestHeadDist {
				bestHeadDist = hDist
				bestHead = b
			}
		} else {
			if hDist < bestFeetDist {
				bestFeetDist = hDist
				bestFeet = b
			}
		}
	}
	if bestHead != nil {
		return bestHead
	}
	return bestFeet
}

func isDiggableObstruction(id string) bool {
	return strings.Contains(id, "dirt") ||
		strings.Contains(id, "grass_block") ||
		strings.HasSuffix(id, ":stone") || id == "stone" ||
		strings.Contains(id, "cobblestone") ||
		strings.Contains(id, "cobbled_deepslate") ||
		strings.Contains(id, "sand") ||
		strings.Contains(id, "gravel") ||
		strings.HasSuffix(id, "_log") ||
		strings.HasSuffix(id, "_wood") ||
		strings.HasSuffix(id, "_stem") ||
		strings.HasSuffix(id, "_planks") ||
		strings.Contains(id, "leaves")
}
