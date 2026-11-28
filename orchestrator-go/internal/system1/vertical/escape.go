package vertical

// Climbing a wall whose height cannot be seen: one decision at a time from the cells beside the
// agent. Rungs go up the agent's own column against the wall, as far as it can reach, until the
// wall ahead ends and there is a ledge to step onto.

// ClimbReach is how many cells above the one the agent is in it hangs rungs.
const ClimbReach = 2

// ClimbState is a climb under way.
type ClimbState struct {
	At      Cell // the cell the agent is in, on the ground or on a rung of the column
	DX, DZ  int  // from the column towards the wall
	BaseY   int  // where the column starts
	Top     int  // highest rung hung; BaseY-1 before the first
	Ladders int  // ladders carried
}

// ClimbOp is what to do next.
type ClimbOp uint8

const (
	ClimbStuck   ClimbOp = iota // no way up: out of ladders, an overhang or a gap in the wall
	ClimbHang                   // hang a ladder in Cell, facing Facing
	ClimbRung                   // climb to the rung Cell
	ClimbStepOff                // step onto the ledge at Cell
)

// ClimbMove is one decision of a climb.
type ClimbMove struct {
	Op     ClimbOp
	Cell   Cell
	Facing string // ClimbHang
}

// NextClimb decides the next move of a climb. solid reports full blocks, and must be right for
// the column and the wall from the agent's cell to ClimbReach above it.
func NextClimb(s ClimbState, solid SolidFunc) ClimbMove {
	ahead := s.At.Add(s.DX, 0, s.DZ)
	if s.At.Y > s.BaseY && solid(ahead) && !solid(ahead.Add(0, 1, 0)) && !solid(ahead.Add(0, 2, 0)) {
		return ClimbMove{Op: ClimbStepOff, Cell: ahead.Add(0, 1, 0)}
	}
	next := Cell{s.At.X, s.Top + 1, s.At.Z}
	if s.Ladders > 0 && next.Y <= s.At.Y+ClimbReach && !solid(next) && solid(next.Add(s.DX, 0, s.DZ)) {
		return ClimbMove{Op: ClimbHang, Cell: next, Facing: FacingFrom(s.DX, s.DZ)}
	}
	if s.Top > s.At.Y {
		return ClimbMove{Op: ClimbRung, Cell: Cell{s.At.X, s.Top, s.At.Z}}
	}
	return ClimbMove{Op: ClimbStuck}
}

// Climbable reports whether the side (dx,dz) of the cell at is a wall too high to step up, with
// room above at to climb it.
func Climbable(at Cell, dx, dz int, solid SolidFunc) bool {
	wall := at.Add(dx, 0, dz)
	return solid(wall) && solid(wall.Add(0, 1, 0)) && !solid(at.Add(0, 1, 0)) && !solid(at.Add(0, 2, 0))
}

// FacingFrom is the facing of a ladder whose support lies (dx,dz) from it.
func FacingFrom(dx, dz int) string {
	for _, h := range horiz {
		if h.dx == dx && h.dz == dz {
			return h.facing
		}
	}
	return ""
}
