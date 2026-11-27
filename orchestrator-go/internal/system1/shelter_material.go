package system1

// Cabin placement that survives the world (play test 7): a block is built from any wood held,
// the builder steps within reach first, and a placement that fails is tried again.

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// shelterPlaceReach is just inside the mod's 6-block placing reach.
	shelterPlaceReach = 5.8
	// cabinStand is how far from the cabin's centre the builder stands outside its walls.
	cabinStand = 3.5
	// shelterApproachTicks is the time one walk to a block is given before another counts:
	// the body takes a few frames even to report that it is walking.
	shelterApproachTicks = int64(60)
	// shelterPlaceTries bounds the attempts at one block before the build goes on without it.
	shelterPlaceTries = 3
)

// cabinBlock is what to place for a blueprint state: any wood of the shape that is held, else
// plain filler (cobblestone, dirt). Holding neither, the state is asked for as drawn, and a
// placement that keeps failing is passed over.
func cabinBlock(state string, inventory []*civquestv1.ItemStackProto, fallback string) string {
	var held []string
	for _, it := range inventory {
		if it != nil && it.Count > 0 {
			held = append(held, it.ItemId)
		}
	}
	if placed, ok := blueprints.WoodSubstitute(state, held); ok {
		return placed
	}
	// A door cannot be made of loose blocks, and a workstation or campfire is not a wall.
	if !strings.Contains(state, "_door") && isFillerBlock(fallback) {
		return fallback
	}
	return state
}

func isFillerBlock(id string) bool {
	switch bareID(id) {
	case "cobblestone", "cobbled_deepslate", "stone", "dirt", "andesite", "diorite", "granite":
		return true
	}
	return false
}

func withinPlacingReach(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto) bool {
	dx, dy, dz := float64(b.X)+0.5-pos.X, float64(b.Y)+0.5-pos.Y, float64(b.Z)+0.5-pos.Z
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= shelterPlaceReach
}

// approachUnderWay reports whether the last walk toward the current block still has time.
func (l *shelterLocal) approachUnderWay(tick int64) bool {
	return l.Walks > 0 && l.WalkIndex == l.BuildIndex && tick >= l.WalkTick && tick-l.WalkTick < shelterApproachTicks
}

// approach is where to walk to reach voxel: the closest spot round the outside of the walls
// that is within reach of it. After shelterPlaceTries walks for one block, ok is false and the
// block is sent from wherever the builder stands.
func (l *shelterLocal) approach(pos *civquestv1.Vec3, voxel *civquestv1.BlockPosProto) (*civquestv1.Vec3, bool) {
	if l.WalkIndex != l.BuildIndex {
		l.WalkIndex, l.Walks = l.BuildIndex, 0
	}
	if l.Walks >= shelterPlaceTries {
		return nil, false
	}
	var best *civquestv1.Vec3
	bestDist := math.MaxFloat64
	for _, dx := range []float64{-cabinStand, 0, cabinStand} {
		for _, dz := range []float64{-cabinStand, 0, cabinStand} {
			if dx == 0 && dz == 0 {
				continue
			}
			at := &civquestv1.Vec3{X: float64(l.Pos.X) + 0.5 + dx, Y: float64(l.Pos.Y), Z: float64(l.Pos.Z) + 0.5 + dz}
			if d := math.Hypot(at.X-pos.X, at.Z-pos.Z); d < bestDist && withinPlacingReach(at, voxel) {
				best, bestDist = at, d
			}
		}
	}
	if best == nil {
		return nil, false
	}
	l.Walks++
	return best, true
}

// isPlacementFailure reports a receipt reason that speaks of a block not placed.
func isPlacementFailure(reason string) bool {
	r := strings.ToLower(reason)
	return strings.HasPrefix(r, "place target") || strings.Contains(r, "matching block item") ||
		strings.Contains(r, "solid support") || strings.Contains(r, "interact_block")
}

// onReceipt steps the build back to a block that failed to go in. A cell already occupied
// counts as filled, and a block that keeps failing is passed over.
func (l shelterLocal) onReceipt(rc *civquestv1.IntentReceipt) shelterLocal {
	if l.Pending == 0 || rc.GetStatus() != civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED ||
		!isPlacementFailure(rc.GetFailureReason()) {
		return l
	}
	index := l.Pending - 1
	l.Pending = 0
	if strings.Contains(rc.GetFailureReason(), "occupied") {
		return l
	}
	if l.RetryIndex != index {
		l.RetryIndex, l.Tries = index, 0
	}
	if l.Tries++; l.Tries < shelterPlaceTries {
		l.BuildIndex, l.Phase = index, cabinPhase(index)
	}
	return l
}
