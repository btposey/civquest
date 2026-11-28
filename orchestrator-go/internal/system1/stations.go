package system1

// Finding a crafting table and the cells to set one down in. A danger shapes where an agent
// chooses to go; it does not take away what the agent can use from where it stands (cq-qdj).
// In play test 11 a drowned nine blocks off hid every table round an agent, which made and set
// down new ones without end.

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// walkableSlope reports that a block is not so far above or below pos, for its distance, that
// walking there is out of the question.
func walkableSlope(b blockCell, pos *civquestv1.Vec3) bool {
	absDy := math.Abs(float64(b[1]) + 0.5 - pos.Y)
	return absDy <= 3.0 || math.Hypot(float64(b[0])+0.5-pos.X, float64(b[2])+0.5-pos.Z) >= absDy*1.5
}

// findNearestCraftingTableLocked locates the crafting table to use: one in reach if there is
// one, else the nearest in view that is worth walking to, else one recalled from the
// SpatialIndex (ADR-022 / ADR-026). A table in reach counts whatever is known of walking to it.
// Caller must hold state.mu if state is non-nil.
func findNearestCraftingTableLocked(
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
) (*civquestv1.VisibleBlock, *db.AgentPOI, float64) {
	var best *civquestv1.VisibleBlock
	bestDistSq := math.MaxFloat64
	if frame == nil || pos == nil {
		return nil, nil, bestDistSq
	}
	bestInReach := false
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !strings.Contains(strings.ToLower(b.BlockId), "crafting_table") {
			continue
		}
		dx := float64(b.Pos.X) + 0.5 - pos.X
		dy := float64(b.Pos.Y) + 0.5 - pos.Y
		dz := float64(b.Pos.Z) + 0.5 - pos.Z
		dSq := dx*dx + dy*dy + dz*dz
		inReach := isCraftingTableWithinReach(b, dSq, pos)
		if !inReach && (!walkableSlope(cellAt(b.Pos), pos) || state != nil &&
			(state.IsWorkstationUnreachableLocked(b.Pos.X, b.Pos.Y, b.Pos.Z, frame.WorldTick) ||
				state.IsDestinationShunnedLocked(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick))) {
			continue
		}
		if inReach && !bestInReach || inReach == bestInReach && dSq < bestDistSq {
			best, bestDistSq, bestInReach = b, dSq, inReach
		}
	}
	if best != nil || state == nil {
		return best, nil, bestDistSq
	}
	return state.recalledCraftingTableLocked(frame, pos)
}

// recalledCraftingTableLocked chooses among remembered tables, nearest first: one in reach, or
// one worth walking to on the agent's own level. Failing that, an agent below ground takes a
// surface table joined to it by a corridor, and one above ground takes any.
func (s *AgentState) recalledCraftingTableLocked(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) (*civquestv1.VisibleBlock, *db.AgentPOI, float64) {
	si := s.SpatialIndexLocked()
	if si == nil {
		return nil, nil, math.MaxFloat64
	}
	layer := ClassifyElevationLayer(pos.Y, 64.0)
	below := layer == ElevationDugout || layer == ElevationUnderground
	originID := ""
	if tracker := s.HabitTrailTrackerLocked(); tracker != nil {
		originID = tracker.CurrentOriginPOIID()
	}
	var other *db.AgentPOI
	otherDistSq := math.MaxFloat64
	found := func(poi *db.AgentPOI, dSq float64) (*civquestv1.VisibleBlock, *db.AgentPOI, float64) {
		return &civquestv1.VisibleBlock{Pos: poiBlockPos(poi), BlockId: poi.BlockID}, poi, dSq
	}
	for _, poi := range si.FindPOIsInRadius(pos, POICategoryWorkstation, 64.0) {
		if !matchesPOIBlockID(poi.BlockID, craftingTableItem) || poi.Reliability < 0.5 {
			continue
		}
		bpos := poiBlockPos(poi)
		dx, dy, dz := poi.X-pos.X, poi.Y+0.5-pos.Y, poi.Z-pos.Z
		dSq := dx*dx + dy*dy + dz*dz
		if isCraftingTableWithinReach(&civquestv1.VisibleBlock{Pos: bpos}, dSq, pos) {
			return found(poi, dSq)
		}
		if !walkableSlope(cellAt(bpos), pos) || s.IsWorkstationUnreachableLocked(bpos.X, bpos.Y, bpos.Z, frame.WorldTick) ||
			s.isPOIShunnedLocked(poi, frame.WorldTick) {
			continue
		}
		if strings.EqualFold(poi.ElevationLayer, layer) {
			return found(poi, dSq)
		}
		if other == nil && (!below || strings.EqualFold(poi.ElevationLayer, ElevationSurface) &&
			originID != "" && si.HasCorridorBetween(originID, poi.POIID)) {
			other, otherDistSq = poi, dSq
		}
	}
	if other != nil {
		return found(other, otherDistSq)
	}
	return nil, nil, math.MaxFloat64
}

// isCraftingTableWithinReach returns true if a crafting table is within 4.5m reach and 2.5m vertical reach.
func isCraftingTableWithinReach(table *civquestv1.VisibleBlock, distSq float64, pos *civquestv1.Vec3) bool {
	if table == nil || table.Pos == nil || pos == nil {
		return false
	}
	dy := math.Abs(float64(table.Pos.Y) + 0.5 - pos.Y)
	return distSq <= 20.25 && dy <= 2.5
}

func isEmptyPlacementBlock(frame *civquestv1.TelemetryFrame, x, y, z int32) bool {
	b := FindVisibleBlockAt(frame, x, y, z)
	if b == nil {
		return true
	}
	id := strings.ToLower(strings.TrimSpace(b.BlockId))
	return id == "" || id == "minecraft:air" || strings.HasSuffix(id, "_air")
}

func selectAdjacentCraftingTablePos(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) *civquestv1.BlockPosProto {
	cx := int32(math.Floor(pos.X))
	cy := int32(math.Floor(pos.Y))
	cz := int32(math.Floor(pos.Z))

	var candidates []*civquestv1.BlockPosProto
	if frame != nil && frame.Status != nil {
		yawRad := float64(frame.Status.Yaw) * math.Pi / 180.0
		fx := -math.Sin(yawRad)
		fz := math.Cos(yawRad)
		frontX := int32(math.Floor(pos.X + fx*1.5))
		frontZ := int32(math.Floor(pos.Z + fz*1.5))
		if frontX != cx || frontZ != cz {
			candidates = append(candidates, &civquestv1.BlockPosProto{X: frontX, Y: cy, Z: frontZ})
		}
	}
	candidates = append(candidates,
		&civquestv1.BlockPosProto{X: cx + 1, Y: cy, Z: cz},
		&civquestv1.BlockPosProto{X: cx - 1, Y: cy, Z: cz},
		&civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz + 1},
		&civquestv1.BlockPosProto{X: cx, Y: cy, Z: cz - 1},
	)

	for _, c := range candidates {
		if isEmptyPlacementBlock(frame, c.X, c.Y, c.Z) {
			return c
		}
	}
	return candidates[0]
}

// placeRelocateDist is how far an agent steps when no cell beside it takes a placement.
const placeRelocateDist = 4.0

// placementCells lists distinct cells to try for a placement beside pos: the preferred one
// first, then the other neighbours at foot height and one up.
func placementCells(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) []*civquestv1.BlockPosProto {
	first := selectAdjacentCraftingTablePos(frame, pos)
	out := []*civquestv1.BlockPosProto{first}
	seen := map[[3]int32]bool{{first.X, first.Y, first.Z}: true}
	cx, cy, cz := int32(math.Floor(pos.X)), int32(math.Floor(pos.Y)), int32(math.Floor(pos.Z))
	for _, d := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {1, 0, 1}, {-1, 0, -1}, {1, 0, -1}, {-1, 0, 1},
		{1, 1, 0}, {-1, 1, 0}, {0, 1, 1}, {0, 1, -1}} {
		c := [3]int32{cx + d[0], cy + d[1], cz + d[2]}
		if !seen[c] {
			seen[c] = true
			out = append(out, &civquestv1.BlockPosProto{X: c[0], Y: c[1], Z: c[2]})
		}
	}
	return out
}

// openCells are the placement cells nothing is seen to occupy (water gives way to a block);
// when every one looks taken, all of them.
func openCells(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) []*civquestv1.BlockPosProto {
	all := placementCells(frame, pos)
	var open []*civquestv1.BlockPosProto
	for _, c := range all {
		if isEmptyPlacementBlock(frame, c.X, c.Y, c.Z) || isWaterAt(frame, c) {
			open = append(open, c)
		}
	}
	if len(open) == 0 {
		return all
	}
	return open
}
