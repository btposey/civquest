package system1

// A destination given up stays given up (play test 8): two thirds of failed walks went to places
// already marked unreachable, because the activity choosing them never asked.

import (
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// givenUpLocked reports whether (x, y, z) is blacklisted after failed walks. It says nothing
// of dangers near the place. s.mu must be held.
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

// IsDestinationShunned reports a place the agent should not choose to go to: a walk to it was
// given up, or it lies by a registered danger. It says nothing of what can be used from where
// the agent already stands (cq-qdj).
func (s *AgentState) IsDestinationShunned(x, y, z float64, tick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsDestinationShunnedLocked(x, y, z, tick)
}

// IsDestinationShunnedLocked is IsDestinationShunned while s.mu is held.
func (s *AgentState) IsDestinationShunnedLocked(x, y, z float64, tick int64) bool {
	return s != nil && (s.endangeredLocked(x, z, tick) || s.givenUpLocked(x, y, z, tick))
}

// isPOIShunnedLocked reports that a POI's block or its access target is shunned.
func (s *AgentState) isPOIShunnedLocked(poi *db.AgentPOI, tick int64) bool {
	if s == nil || poi == nil {
		return false
	}
	if s.IsDestinationShunnedLocked(poi.X, poi.Y, poi.Z, tick) {
		return true
	}
	acc := poiAccessTarget(poi)
	return acc != nil && s.IsDestinationShunnedLocked(acc.X, acc.Y, acc.Z, tick)
}

// useRefused reports a block not to be taken up from from. A walk to it that was given up
// counts anywhere; a danger by it counts only when it is beyond reach, so that what the agent
// stands beside does not stop existing.
func (s *AgentState) useRefused(from *civquestv1.Vec3, b *civquestv1.BlockPosProto, reach float64, tick int64) bool {
	if s == nil || b == nil {
		return false
	}
	x, y, z := float64(b.X)+0.5, float64(b.Y), float64(b.Z)+0.5
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.givenUpLocked(x, y, z, tick) {
		return true
	}
	if from != nil && math.Sqrt((x-from.X)*(x-from.X)+(y+0.5-from.Y)*(y+0.5-from.Y)+(z-from.Z)*(z-from.Z)) <= reach {
		return false
	}
	return s.endangeredLocked(x, z, tick)
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

// awayFromCabin reports a known cabin near enough to walk back to at night, that the agent is
// not at and has not given up on.
func (s *AgentState) awayFromCabin(pos *civquestv1.Vec3, tick int64) bool {
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
	d := dx*dx + dz*dz
	return d > cabinAtSq && d <= homeRangeSq && !s.givenUpLocked(x, y, z, tick)
}

// cabinAtSq is the squared horizontal distance within which the agent counts as at its cabin.
const cabinAtSq = 16.0

// homeRangeSq is the squared distance (96 blocks) beyond which the night is not spent walking.
const homeRangeSq = 96.0 * 96.0
