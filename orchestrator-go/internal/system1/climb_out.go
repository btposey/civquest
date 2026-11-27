package system1

// Climbing out (play test 9): an agent in a pocket whose walls were not beside it found nothing
// to dig, and walked at random into them for fifty minutes. It now works a cell at a time along
// one heading until it meets a wall, and cuts steps up it (recover_activity.go).

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// climbLockTTL keeps a climb's heading across the steps and the work between them.
const climbLockTTL = int64(1200)

// climbClearDist walked from the start of an episode means the pocket is behind.
const climbClearDist = 6.0

const (
	// pocketEpisodes enclosed recoveries within pocketRadius mean that stepping about is not
	// getting the agent out, and it starts to climb.
	pocketEpisodes = 3
	pocketRadius   = 6.0
)

// notePocket records an enclosed recovery at pos and reports whether to climb.
func (fsm *AgentFSM) notePocket(pos *civquestv1.Vec3, tick int64) bool {
	if pos == nil {
		return false
	}
	kept, near := fsm.pockets[:0], 0
	for _, p := range fsm.pockets {
		if tick >= p.Tick && tick-p.Tick <= trapMemoryTicks {
			kept = append(kept, p)
			if math.Hypot(pos.X-p.X, pos.Z-p.Z) <= pocketRadius {
				near++
			}
		}
	}
	fsm.pockets = append(kept, gaveUpHere{X: pos.X, Z: pos.Z, Tick: tick})
	return near+1 >= pocketEpisodes
}

func (l recoverEscapeLock) ttl() int64 {
	if l.Climb {
		return climbLockTTL
	}
	return escapeLockTTL
}

// climbDone ends a climbing episode: up a step, or well away from where it began. A single
// pace across the floor of a pocket is neither, and ending there let other work take over
// (and mine the very steps being cut).
func (l recoverLocal) climbDone(pos *civquestv1.Vec3) bool {
	return l.Climb && l.HasStart && pos != nil &&
		(pos.Y-l.StartPos[1] >= 0.5 || math.Hypot(pos.X-l.StartPos[0], pos.Z-l.StartPos[2]) >= climbClearDist)
}

// towardsWall walks into the open cell ahead when there is ground under it, and otherwise turns:
// going down a hole is no way out.
func (loc recoverLocal) towardsWall(agentID string, frame *civquestv1.TelemetryFrame, x, by, z int32, out *recoverLocal) (*civquestv1.AgentIntent, bool) {
	if FindVisibleBlockAt(frame, x, by-1, z) == nil {
		out.DirX, out.DirZ = -out.DirZ, out.DirX
		return nil, false
	}
	return recoverStep(agentID, x, by, z), true
}

// recoverStep walks into exactly the cell (x, y, z).
func recoverStep(agentID string, x, y, z int32) *civquestv1.AgentIntent {
	in := recoverNav(agentID, float64(x)+0.5, float64(y), float64(z)+0.5, 1)
	in.GetNavigate().IntoCell = true
	return in
}
