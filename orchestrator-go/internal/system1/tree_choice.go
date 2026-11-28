package system1

// Tree choice (user direction, 2026-11-27, cq-d6p): every activity that picks a tree picks it
// here. A small tree, one the felling method (felling.go) takes whole from underneath, comes
// before any large or branching one, which is chosen only when no small one will do. Whether
// wood is wanted at all is the asking activity's decision.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

// treeTrunk is one column of logs in view that the agent may break.
type treeTrunk struct {
	base   *civquestv1.VisibleBlock   // its lowest usable log
	logs   []*civquestv1.VisibleBlock // all its usable logs, in frame order
	distSq float64                    // to the nearest of them
	large  bool                       // part of a tree that cannot be taken whole from underneath
}

// usableTrunks returns the trunk columns in view, in frame order, leaving out logs that are
// out of reach in a canopy, will not break, belong to a recently failed tree or lie where the
// agent will not travel, and those extra (which may be nil) refuses.
func usableTrunks(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, state *AgentState,
	extra func(*civquestv1.VisibleBlock) bool) []treeTrunk {
	if frame == nil || pos == nil {
		return nil
	}
	large := largeTreeColumns(frame)
	index := map[[2]int32]int{}
	var trunks []treeTrunk
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !isLogBlockID(b.BlockId) || isElevatedCanopyOnlyLog(frame, pos, b) ||
			state.unbreakableLocked(b, frame.WorldTick) || extra != nil && !extra(b) {
			continue
		}
		if state != nil && (state.IsTreeOnCooldownLocked(b.Pos.X, b.Pos.Z, frame.WorldTick) ||
			state.IsDestinationShunnedLocked(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick)) {
			continue
		}
		col := [2]int32{b.Pos.X, b.Pos.Z}
		dx, dy, dz := float64(b.Pos.X)+0.5-pos.X, float64(b.Pos.Y)+0.5-pos.Y, float64(b.Pos.Z)+0.5-pos.Z
		dSq := dx*dx + dy*dy + dz*dz
		i, ok := index[col]
		if !ok {
			index[col] = len(trunks)
			trunks = append(trunks, treeTrunk{base: b, logs: []*civquestv1.VisibleBlock{b}, distSq: dSq, large: large[col]})
			continue
		}
		t := &trunks[i]
		t.logs = append(t.logs, b)
		t.distSq = math.Min(t.distSq, dSq)
		if b.Pos.Y < t.base.Pos.Y {
			t.base = b
		}
	}
	return trunks
}

// preferredTrunks returns the small trees' trunks, or every trunk when there is no small one.
func preferredTrunks(trunks []treeTrunk) []treeTrunk {
	var small []treeTrunk
	for _, t := range trunks {
		if !t.large {
			small = append(small, t)
		}
	}
	if len(small) > 0 {
		return small
	}
	return trunks
}

// largeTreeColumns marks the columns of the trees in view that cannot be taken whole from
// underneath. Logs of one wood that touch (corners included) are one tree; it is large when it
// stands in more than one column (a thick trunk, or branches) or its top is out of reach from
// the foot of the trunk. Only the logs in the frame are judged.
func largeTreeColumns(frame *civquestv1.TelemetryFrame) map[[2]int32]bool {
	logs := map[[3]int32]string{}
	for _, b := range frame.GetInterestingBlocks() {
		if b != nil && b.Pos != nil && isLogBlockID(b.BlockId) {
			logs[[3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}] = woodFamilyOf(b.BlockId)
		}
	}
	large := map[[2]int32]bool{}
	seen := map[[3]int32]bool{}
	for start, family := range logs {
		if seen[start] {
			continue
		}
		seen[start] = true
		tree := [][3]int32{start}
		foot, top, spread := start, start, false
		for i := 0; i < len(tree); i++ {
			p := tree[i]
			if p[1] < foot[1] {
				foot = p
			}
			if p[1] > top[1] {
				top = p
			}
			spread = spread || p[0] != start[0] || p[2] != start[2]
			for dx := int32(-1); dx <= 1; dx++ {
				for dy := int32(-1); dy <= 1; dy++ {
					for dz := int32(-1); dz <= 1; dz++ {
						n := [3]int32{p[0] + dx, p[1] + dy, p[2] + dz}
						if f, ok := logs[n]; ok && f == family && !seen[n] {
							seen[n] = true
							tree = append(tree, n)
						}
					}
				}
			}
		}
		under := vertical.Vec3{X: float64(foot[0]) + 0.5, Y: float64(foot[1]), Z: float64(foot[2]) + 0.5}
		if spread || !vertical.ReachableAABB(under, vertical.DefaultEyeHeight, treeReach,
			vertical.Cell{X: int(top[0]), Y: int(top[1]), Z: int(top[2])}) {
			for _, p := range tree {
				large[[2]int32{p[0], p[2]}] = true
			}
		}
	}
	return large
}

// isElevatedCanopyOnlyLog returns true when a visible log block is > 4 blocks above the agent
// and no reachable trunk base log (<= 4 blocks above pos.Y) exists in the same or adjacent column.
func isElevatedCanopyOnlyLog(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, b *civquestv1.VisibleBlock) bool {
	if frame == nil || pos == nil || b == nil || b.Pos == nil {
		return false
	}
	if float64(b.Pos.Y)-pos.Y <= 4.0 {
		return false
	}
	for _, other := range frame.InterestingBlocks {
		if other == nil || other.Pos == nil || !isLogBlockID(other.BlockId) {
			continue
		}
		dx := other.Pos.X - b.Pos.X
		dz := other.Pos.Z - b.Pos.Z
		if dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1 && float64(other.Pos.Y)-pos.Y <= 4.0 {
			return false
		}
	}
	return true
}
