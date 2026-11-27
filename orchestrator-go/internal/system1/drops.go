package system1

// Mined and harvested blocks are only worth something once picked up (play test 7): stone is
// taken only where its drop will lie within pickup reach, and drops in view are walked to.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// The body picks up items 2.5 blocks round it, and no lower than 1.5 under its feet.
	dropReachBelow  = 1.5
	dropReachAbove  = 2.0
	dropCollectDist = 8.0
)

// dropWouldBeInReach reports whether breaking b from pos leaves its drop where it can be
// collected: no more than one block under the feet, and not the block being stood on. Stone
// buried under a layer of soil is left alone.
func dropWouldBeInReach(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto) bool {
	feet := int32(math.Floor(pos.Y + 0.1))
	if b.Y < feet-1 {
		return false
	}
	underfoot := b.X == int32(math.Floor(pos.X)) && b.Z == int32(math.Floor(pos.Z))
	return !(underfoot && b.Y < feet)
}

// nearbyDrop is the closest wanted item lying in view that a few steps would pick up, and that
// is not already within reach.
func nearbyDrop(state *AgentState, frame *civquestv1.TelemetryFrame, wanted func(item string) bool) *civquestv1.VisibleEntity {
	pos := frame.GetStatus().GetPosition()
	if pos == nil {
		return nil
	}
	var best *civquestv1.VisibleEntity
	bestDist := math.MaxFloat64
	for _, e := range frame.VisibleEntities {
		if e == nil || e.Position == nil || !isDroppedItemEntity(e) {
			continue
		}
		dy := e.Position.Y - pos.Y
		d := math.Hypot(e.Position.X-pos.X, e.Position.Z-pos.Z)
		if dy < -dropReachBelow || dy > dropReachAbove || d <= forageDropPickupReach || d > dropCollectDist || d >= bestDist {
			continue
		}
		if _, item := gatherDropGood(e); !wanted(item) ||
			(state != nil && state.IsTargetUnreachableLocked(e.Position.X, e.Position.Y, e.Position.Z, frame.WorldTick)) {
			continue
		}
		best, bestDist = e, d
	}
	return best
}

func walkToDrop(agentID string, drop *civquestv1.VisibleEntity) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target:          &civquestv1.Vec3{X: drop.Position.X, Y: drop.Position.Y, Z: drop.Position.Z},
		SpeedMultiplier: 1.0, ArrivalTolerance: 1,
	}}}
}
