package system1

// A destination given up stays given up (play test 8): two thirds of failed walks went to places
// already marked unreachable, because the activity choosing them never asked.

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

// givenUpLocked reports whether (x, y, z) is blacklisted after failed walks. Unlike
// IsTargetUnreachableLocked it says nothing of dangers near the place. s.mu must be held.
func (s *AgentState) givenUpLocked(x, y, z float64, tick int64) bool {
	for _, e := range s.unreachableTargets {
		if e != nil && e.blacklistUntil > tick && unreachableCoordsMatch(e.x, e.y, e.z, x, y, z) {
			return true
		}
	}
	return false
}

// givenUp is givenUpLocked taking the lock.
func (s *AgentState) givenUp(x, y, z float64, tick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.givenUpLocked(x, y, z, tick)
}

// walkGivenUpLocked reports that in is a walk to a place already given up.
func walkGivenUpLocked(s *AgentState, in *civquestv1.AgentIntent, tick int64) bool {
	t := in.GetNavigate().GetTarget()
	return t != nil && s.givenUpLocked(t.X, t.Y, t.Z, tick)
}

// cabinOutOfReach reports that the walk back to the cabin site has been given up and the agent
// is not already at it.
func (s *AgentState) cabinOutOfReach(pos *civquestv1.Vec3, tick int64) bool {
	if s == nil || pos == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ShelterPos == nil {
		return false
	}
	x, y, z := float64(s.ShelterPos.X)+0.5, float64(s.ShelterPos.Y), float64(s.ShelterPos.Z)+0.5
	dx, dz := pos.X-x, pos.Z-z
	return dx*dx+dz*dz > cabinAtSq && s.givenUpLocked(x, y, z, tick)
}

// cabinAtSq is the squared horizontal distance within which the agent counts as at its cabin.
const cabinAtSq = 16.0

