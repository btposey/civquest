package system1

import (
	"math"

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
	firstEver := state.homeOrigin == nil // the agent's very first evaluation: spawn warm-up applies
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
	// Only the agent's very first evaluation waits out the spawn warm-up cooldown. A fresh
	// walk of a newly pushed exploration frame has no previous leg, so no cooldown is
	// pending: lastExploreTick stays 0 ("never") and the first leg is issued at once instead
	// of idling a cooldown on every frame entry (cq-aik.31).
	if walk.lastExploreTick == 0 && firstEver {
		walk.lastExploreTick = frame.WorldTick
	}

	if state.companionActive {
		return nil
	}

	navState := frame.Status.NavigationState

	// 1. Terrain barriers met by an active expedition are handled by its Navigate frame (flank / re-survey).
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

	// 2. Terrain-Aware Exploration Strategies (Valleys, Ridges, Fords, Rims, Flatlands, Canopy Trailblazing)
	if walk.currentExploreTarget == nil || navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED ||
		math.Hypot(pos.X-walk.currentExploreTarget.X, pos.Z-walk.currentExploreTarget.Z) <= 2.0 {
		if terrainIntent := EvaluateTerrainExploration(agentID, frame, pos, state, cfg, walk); terrainIntent != nil &&
			!walkGivenUpLocked(state, terrainIntent, frame.WorldTick) {
			if nav := terrainIntent.GetNavigate(); nav != nil && nav.Target != nil {
				walk.currentExploreTarget = nav.Target
				walk.lastExploreProgressDist = math.Hypot(pos.X-nav.Target.X, pos.Z-nav.Target.Z)
				walk.lastExploreProgressTick = frame.WorldTick
			}
			return terrainIntent
		}
	}

	// 3. Radial sector azimuth frontier scouting
	return evaluateScoutingLocked(agentID, frame, pos, state, cfg, walk)
}

// EvaluateFrontierScouting is an alias for EvaluateScouting.
func EvaluateFrontierScouting(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	walk = orFreshWalk(walk)
	return EvaluateScouting(agentID, frame, cfg, state, walk)
}

// EvaluateScoutingDirect evaluates scouting with explicit position under locked state.
func EvaluateScoutingDirect(
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
	return evaluateScoutingLocked(agentID, frame, pos, state, cfg, walk)
}

func evaluateScoutingLocked(
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

	// Hold steady while a 360-degree horizon survey is executing on the motor stack (ADR-032).
	if state.isExpeditionSurveyingLocked(frame.WorldTick) {
		return nil
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
	stuck := navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK ||
		hasActiveTarget && state.givenUpLocked(walk.currentExploreTarget.X, walk.currentExploreTarget.Y, walk.currentExploreTarget.Z, frame.WorldTick)

	timedOut := false
	if hasActiveTarget && !arrived && !stuck {
		if distToTarget < walk.lastExploreProgressDist-1.0 {
			// Forward progress was made toward waypoint
			walk.lastExploreProgressDist = distToTarget
			walk.lastExploreProgressTick = frame.WorldTick
		} else if walk.lastExploreProgressTick > 0 && frame.WorldTick-walk.lastExploreProgressTick >= 600 {
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
		if frame.WorldTick >= walk.lastExploreTick && frame.WorldTick-walk.lastExploreTick < 40 {
			return nil
		}
	}

	// 4. Advance leg or pivot azimuth
	if arrived {
		// Advance to next radial leg along the SAME sector azimuth
		walk.frontierRadialLeg++
		walk.exploreStepIndex++
	} else if stuck || timedOut {
		// Rotate to a new sector azimuth and reset to leg 0
		// Default +1.2 rad pivot, steered toward the least-covered sector (cq-dcx.4).
		pivot := noveltyHeading(walk.spatial, origin.X, origin.Z, walk.frontierAzimuthRad, legStep, frame.WorldTick, scoutPivotOffsets)
		newAz := math.Atan2(math.Sin(pivot), math.Cos(pivot))
		walk.frontierAzimuthRad = newAz
		walk.lastHeadingRad = walk.frontierAzimuthRad
		walk.frontierRadialLeg = 0
		walk.exploreStepIndex++
	} else if hasActiveTarget && !isActivelyNavigating {
		// Baritone is idle but agent hasn't arrived/stuck/timedOut and cooldown expired;
		// re-issue navigation to the SAME target
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

	// 5. Generate target for current/new leg
	legDist := float64(walk.frontierRadialLeg+1) * legStep
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
