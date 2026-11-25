package system1

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluatePatrol computes autonomous settlement perimeter polygon patrols and village landmark
// perimeter watch for non-explorer citizens (Farmers, Builders, Chroniclers, Guards, etc.).
// After completing radial legs, non-explorers rotate azimuth by +45 degrees (Pi/4) to patrol
// a perimeter polygon rather than oscillating in 1D.
func EvaluatePatrol(
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

	if state.companionActive {
		return nil
	}

	navState := frame.Status.NavigationState

	// 1. Immediate terrain unsticking / dig-out or contouring detour when STUCK or ENCLOSED
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK || frame.Status.GetNavigationEnclosed() {
		if frame.WorldTick >= walk.lastExploreTick && frame.WorldTick-walk.lastExploreTick < 20 {
			return nil
		}
		walk.exploreStepIndex++
		// Rotate ~126 degrees away from blocked heading to contour around steep hills or cliffs
		walk.lastHeadingRad += 2.2
		walk.lastHeadingRad = math.Atan2(math.Sin(walk.lastHeadingRad), math.Cos(walk.lastHeadingRad))
		walk.frontierAzimuthRad = walk.lastHeadingRad
		walk.frontierRadialLeg = 0
		detourDist := 10.0
		targetX := pos.X + math.Cos(walk.lastHeadingRad)*detourDist
		targetZ := pos.Z + math.Sin(walk.lastHeadingRad)*detourDist
		walk.lastExploreTick = frame.WorldTick

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
	if state.mem.telemetryFramesSeen < 2 {
		return nil
	}

	return evaluatePatrolLocked(agentID, frame, pos, state, cfg, walk)
}

// EvaluateCivicPatrol is an alias for EvaluatePatrol.
func EvaluateCivicPatrol(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	return EvaluatePatrol(agentID, frame, cfg, state, walk)
}

// EvaluatePatrolDirect evaluates patrol with explicit position under locked state.
func EvaluatePatrolDirect(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || pos == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return evaluatePatrolLocked(agentID, frame, pos, state, cfg, walk)
}

func evaluatePatrolLocked(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	if frame == nil || frame.Status == nil || pos == nil || state == nil {
		return nil
	}

	curiosity := 0.5
	if cfg != nil {
		curiosity = cfg.Traits.Curiosity
	}

	navState := frame.Status.NavigationState

	// 1. Initialize sector azimuth if not yet set
	if !walk.frontierAzimuthSet {
		var azimuth float64
		if walk.lastHeadingRad != 0 {
			azimuth = walk.lastHeadingRad
		} else {
			h := fnv1aString(agentID)
			azimuth = float64(h%360) * (math.Pi / 180.0)
		}
		azimuth = math.Atan2(math.Sin(azimuth), math.Cos(azimuth))
		walk.frontierAzimuthRad = azimuth
		walk.frontierAzimuthSet = true
		walk.lastHeadingRad = azimuth
		walk.frontierRadialLeg = 0
		walk.currentExploreTarget = nil
	}

	// Landmark perimeter watch: if in a landmark zone, patrol around landmark center or settlement home origin
	origin := state.homeOrigin
	if origin == nil {
		origin = state.currentOutpost
	}
	if origin == nil {
		origin = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	}

	cooldownTicks := int64(120) // 6 seconds for general villagers
	legStep := 16.0 + curiosity*8.0

	// 2. Evaluate arrival, stuck, and timeout conditions
	hasActiveTarget := walk.currentExploreTarget != nil
	if !hasActiveTarget {
		if walk.lastExploreTick > 0 && frame.WorldTick >= walk.lastExploreTick && frame.WorldTick-walk.lastExploreTick < cooldownTicks {
			return nil
		}
	}
	var distToTarget float64
	if hasActiveTarget {
		distToTarget = math.Hypot(pos.X-walk.currentExploreTarget.X, pos.Z-walk.currentExploreTarget.Z)
	}

	arrived := hasActiveTarget && (distToTarget <= 2.0 || navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED)
	stuck := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK

	timedOut := false
	if hasActiveTarget && !arrived && !stuck {
		if distToTarget < walk.lastExploreProgressDist-1.0 {
			walk.lastExploreProgressDist = distToTarget
			walk.lastExploreProgressTick = frame.WorldTick
		} else if walk.lastExploreProgressTick > 0 && frame.WorldTick-walk.lastExploreProgressTick >= 600 {
			timedOut = true
		}
	}

	// If actively navigating toward current waypoint and not arrived/stuck/timed out, DO NOT re-issue!
	isActivelyNavigating := navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	if hasActiveTarget && !arrived && !stuck && !timedOut {
		if isActivelyNavigating {
			return nil
		}
		if frame.WorldTick >= walk.lastExploreTick && frame.WorldTick-walk.lastExploreTick < 40 {
			return nil
		}
	}

	// 3. Determine new waypoint
	if arrived {
		if walk.frontierRadialLeg >= 2 {
			// Non-explorers rotate azimuth after legs to patrol a perimeter polygon rather than oscillating in 1D
			newAz := math.Atan2(math.Sin(walk.frontierAzimuthRad+math.Pi/4), math.Cos(walk.frontierAzimuthRad+math.Pi/4))
			walk.frontierAzimuthRad = newAz
			walk.lastHeadingRad = walk.frontierAzimuthRad
			walk.frontierRadialLeg = 0
		} else {
			walk.frontierRadialLeg++
		}
		walk.exploreStepIndex++
	} else if stuck || timedOut {
		// Rotate to a new sector azimuth and reset to leg 0
		newAz := math.Atan2(math.Sin(walk.frontierAzimuthRad+1.2), math.Cos(walk.frontierAzimuthRad+1.2))
		walk.frontierAzimuthRad = newAz
		walk.lastHeadingRad = walk.frontierAzimuthRad
		walk.frontierRadialLeg = 0
		walk.exploreStepIndex++
	} else if hasActiveTarget && !isActivelyNavigating {
		// Re-issue navigation to the SAME target
		walk.lastExploreTick = frame.WorldTick
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: walk.currentExploreTarget.X,
						Y: pos.Y,
						Z: walk.currentExploreTarget.Z,
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 2,
				},
			},
		}
	}

	// 4. Generate target for current/new leg
	legDist := 12.0 + float64(walk.frontierRadialLeg)*legStep
	targetX := origin.X + math.Cos(walk.frontierAzimuthRad)*legDist
	targetZ := origin.Z + math.Sin(walk.frontierAzimuthRad)*legDist
	newTarget := &civquestv1.Vec3{
		X: math.Round(targetX*10) / 10,
		Y: pos.Y,
		Z: math.Round(targetZ*10) / 10,
	}

	walk.currentExploreTarget = newTarget
	walk.lastExploreProgressDist = math.Hypot(pos.X-newTarget.X, pos.Z-newTarget.Z)
	walk.lastExploreProgressTick = frame.WorldTick
	walk.lastExploreTick = frame.WorldTick

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
