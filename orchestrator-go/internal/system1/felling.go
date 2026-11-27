package system1

// Felling a tree (user direction, 2026-11-27) is bottom to top: the two lowest trunk logs are
// cleared from beside the tree, the agent walks into the space they left, and takes the rest
// looking straight up, where reach goes furthest and the drops fall onto it. Leaves are broken
// only by that walk, and only the ones in its way (the mod's navigator).

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// A sub-target with this ID prefix is a cell to walk into, not a block to act on.
	standTargetPrefix = "stand:"
	underTrunkTarget  = standTargetPrefix + "under_trunk"
	fellingUnderKey   = "felling_under" // task metadata: the walk under the trunk was begun

	// standCentre is how far from the cell's centre still counts as standing in it. It is a
	// little wider than the mod's arrival tolerance (0.3).
	standCentre = 0.35
	standYSlack = 1.2
	// standGiveUpTicks bounds the walk into a cell before the task does without it.
	standGiveUpTicks = int64(100)

	// Higher above the trunk's base than this, the agent is up on something (a ladder, cq-3a0).
	fellingElevated = 2.5
)

// standCell is the cell t's current sub-target requires the agent to walk into, or nil.
func standCell(t *TaskSubroutine) *civquestv1.BlockPosProto {
	if t == nil || !strings.HasPrefix(t.ActiveTargetID, standTargetPrefix) {
		return nil
	}
	return t.ActiveSubTarget
}

func standingIn(pos *civquestv1.Vec3, cell *civquestv1.BlockPosProto) bool {
	return pos != nil && cell != nil &&
		math.Hypot(pos.X-(float64(cell.X)+0.5), pos.Z-(float64(cell.Z)+0.5)) <= standCentre &&
		math.Abs(pos.Y-float64(cell.Y)) <= standYSlack
}

// fellingFromUnder reports whether t is taking its trunk from underneath: on the way there or
// standing there, with logs still up.
func fellingFromUnder(t *TaskSubroutine, frame *civquestv1.TelemetryFrame) bool {
	pos := frame.GetStatus().GetPosition()
	if t == nil || pos == nil || t.Spec.Target.BlockPos == nil {
		return false
	}
	at := t.Spec.Target.BlockPos
	if t.Metadata[fellingUnderKey] != "true" && !standingIn(pos, cellUnderTrunk(pos, at.X, at.Y, at.Z)) {
		return false
	}
	total, _, _, _, _ := scanTreeClusterLogs(frame, at.X, at.Y, at.Z, t.Spec.ReachRadius, t.Metadata["wood_family"])
	return total > 0
}

// walkingIntoCell reports whether local is a task frame whose walk is into a stand cell.
func walkingIntoCell(local any) bool {
	tl, ok := local.(taskLocal)
	return ok && standCell(tl.Task) != nil
}

// giveUpStand drops a cell that could not be walked into; the task picks another sub-target.
func (t *TaskSubroutine) giveUpStand(state *AgentState, frame *civquestv1.TelemetryFrame, why string) {
	tick := frame.GetWorldTick()
	d := taskDetails(t, frame)
	d["why"] = why
	state.noteDiag(EventStandGiveUp, t.Spec.ID+": "+why, d)
	if t.Spec.OnStandFailed != nil {
		t.Spec.OnStandFailed()
	}
	t.Phase = TaskPhaseEvaluate
	t.ActiveSubTarget, t.ActiveApproachPos, t.ActiveTargetID = nil, nil, ""
	t.LastNavigateTick = 0
	t.LastProgressTick = tick
}

// bottomLog is the lowest log still standing among the two lowest of the trunk based at
// (tx, ty, tz), or nil once both are gone.
func bottomLog(frame *civquestv1.TelemetryFrame, tx, ty, tz int32, family string) *civquestv1.VisibleBlock {
	var low *civquestv1.VisibleBlock
	for _, b := range frame.GetInterestingBlocks() {
		if b == nil || b.Pos == nil || !isLogBlockID(b.BlockId) || b.Pos.X != tx || b.Pos.Z != tz ||
			b.Pos.Y < ty || b.Pos.Y > ty+1 || (family != "" && woodFamilyOf(b.BlockId) != family) {
			continue
		}
		if low == nil || b.Pos.Y < low.Pos.Y {
			low = b
		}
	}
	return low
}

// cellUnderTrunk is where to stand under the trunk based at (tx, ty, tz). A trunk hanging two
// or more blocks over the agent (its bottom already gone) is stood under at the agent's level.
func cellUnderTrunk(pos *civquestv1.Vec3, tx, ty, tz int32) *civquestv1.BlockPosProto {
	y := ty
	if feet := int32(math.Floor(pos.Y + 0.1)); ty-feet >= 2 {
		y = feet
	}
	return &civquestv1.BlockPosProto{X: tx, Y: y, Z: tz}
}

// walkUnderTrunk returns the cell to walk into next: the bottom of the trunk is cleared, the
// agent is on the ground beside it, and logs remain that can be reached from underneath.
func walkUnderTrunk(frame *civquestv1.TelemetryFrame, tx, ty, tz int32, reach float64, family string) (*civquestv1.BlockPosProto, bool) {
	pos := frame.GetStatus().GetPosition()
	if pos == nil || pos.Y > float64(ty)+fellingElevated || bottomLog(frame, tx, ty, tz, family) != nil {
		return nil, false
	}
	cell := cellUnderTrunk(pos, tx, ty, tz)
	if standingIn(pos, cell) {
		return nil, false
	}
	under := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{Position: &civquestv1.Vec3{
			X: float64(cell.X) + 0.5, Y: float64(cell.Y), Z: float64(cell.Z) + 0.5}},
		InterestingBlocks: frame.InterestingBlocks,
	}
	_, reachable, _, _, _ := scanTreeClusterLogs(under, tx, ty, tz, reach, family)
	return cell, len(reachable) > 0
}
