package system1

// The temporary shelter (cq-3ru): far from home at night an agent walls itself into one cell
// rather than found another cabin. Nine blocks, the two of the doorway last. Like the cabin's
// openings it is read off the world against its template, so only its site is remembered; the
// doorway is broken to leave, and recovery leaves no other way.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const bivouacBlocks = 9

// bivouacShape is the template, as offsets from the sheltered cell, in the order built: walls,
// roof, then the doorway (south), lower cell first.
var bivouacShape = [bivouacBlocks][3]int32{
	{0, 0, -1}, {1, 0, 0}, {-1, 0, 0}, {0, 1, -1}, {1, 1, 0}, {-1, 1, 0}, {0, 2, 0}, {0, 0, 1}, {0, 1, 1},
}

// bivouacDoor indexes the doorway's cells in bivouacShape.
const bivouacDoor = 7

func bivouacCell(site lightPos3, i int) *civquestv1.BlockPosProto {
	d := bivouacShape[i]
	return &civquestv1.BlockPosProto{X: site.X + d[0], Y: site.Y + d[1], Z: site.Z + d[2]}
}

func inBivouac(pos *civquestv1.Vec3, site lightPos3) bool {
	if !site.Set || pos == nil {
		return false
	}
	c := ladderCell(pos)
	return c.X == int(site.X) && c.Z == int(site.Z) && c.Y >= int(site.Y) && c.Y <= int(site.Y)+1
}

// farFromHomeLocked reports that no cabin, bed or settlement serves the night from here, nor is
// a cabin to be built here. state.mu held.
func (s *AgentState) farFromHomeLocked(pos *civquestv1.Vec3) bool {
	if nearBlock(pos, s.AssignedBed, homeRange) || s.CabinBuilt > 0 && nearBlock(pos, s.ShelterPos, homeRange) {
		return false
	}
	if st, settled := s.settlementLocked(); settled {
		return !withinRange(pos, st.Pos[0], st.Pos[2], homeRange) || s.cabinCompleteLocked()
	}
	return s.CabinBuilt > 0 && s.ShelterPos != nil
}

// bivouacMoveLocked is the next thing to do about a temporary shelter; ok is false when none is
// called for here. state.mu held.
func (lw *fsmWorld) bivouacMoveLocked(t cabinTend, sheltering bool) (tendMove, bool) {
	s, frame, pos := lw.ctx.State, lw.Frame(), lw.pos()
	site := s.construction.site.Bivouac
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	here := site.Set && math.Hypot(pos.X-float64(site.X)-0.5, pos.Z-float64(site.Z)-0.5) <= tendRange &&
		math.Abs(pos.Y-float64(site.Y)) <= 4
	if !here {
		if !sheltering || frame.Status.GetIsInWater() || inv.buildingBlockCount < bivouacBlocks || !s.farFromHomeLocked(pos) {
			return tendMove{}, false
		}
		c := ladderCell(pos)
		return tendMove{Pitch: lightPos3{X: int32(c.X), Y: int32(c.Y), Z: int32(c.Z), Set: true}}, true
	}
	solid := frameSolid(frame)
	stands := func(i int) *civquestv1.VisibleBlock {
		at := bivouacCell(site, i)
		if b := FindVisibleBlockAt(frame, at.X, at.Y, at.Z); b != nil && solid(ladderCell(&civquestv1.Vec3{
			X: float64(at.X), Y: float64(at.Y), Z: float64(at.Z)})) {
			return b
		}
		return nil
	}
	centre := &civquestv1.Vec3{X: float64(site.X) + 0.5, Y: float64(site.Y), Z: float64(site.Z) + 0.5}
	move := tendMove{Centre: centre, Door: true, Rest: "sheltering in a temporary shelter until morning"}
	if !sheltering {
		for i := bivouacBlocks - 1; i >= bivouacDoor; i-- {
			if b := stands(i); b != nil {
				move.Break, move.Block = b.Pos, b.BlockId
				return move, true
			}
		}
		return tendMove{Forget: true}, true
	}
	if !inBivouac(pos, site) {
		if t.Walks >= tendAsks {
			return tendMove{Forget: true}, true // no way back into it
		}
		move.Walk, move.Mark = centre, bivouacCell(site, bivouacDoor)
		return move, true
	}
	if inv.buildingBlockCount > 0 {
		for i := range bivouacShape {
			at := bivouacCell(site, i)
			if stands(i) != nil || t.Failed.Cell == lightSite(at) && t.Failed.Sheltering == sheltering {
				continue
			}
			move.Place, move.Door = at, i >= bivouacDoor
			move.Block = cabinBlock(inv.placeableBlockID, frame.Status.Inventory, inv.placeableBlockID)
			return move, true
		}
	}
	move.Hold = true
	return move, true
}
