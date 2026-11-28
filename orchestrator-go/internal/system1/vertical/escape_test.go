package vertical

import "testing"

// A wall five high to the east of an agent at the origin: rungs go up as far as it can reach,
// it climbs them, and steps off onto the ledge.
func TestNextClimb(t *testing.T) {
	wall := func(c Cell) bool { return c.Y < 0 || c.X == 1 && c.Y < 5 }
	if !Climbable(Cell{}, 1, 0, wall) || Climbable(Cell{}, -1, 0, wall) {
		t.Fatal("only the wall to the east is one to climb")
	}
	at := func(y, top, ladders int) ClimbMove {
		return NextClimb(ClimbState{At: Cell{0, y, 0}, DX: 1, Top: top, Ladders: ladders}, wall)
	}
	for _, tc := range []struct {
		name      string
		got, want ClimbMove
	}{
		{"first rung", at(0, -1, 9), ClimbMove{ClimbHang, Cell{0, 0, 0}, FacingWest}},
		{"highest in reach", at(0, 1, 9), ClimbMove{ClimbHang, Cell{0, 2, 0}, FacingWest}},
		{"then climb", at(0, 2, 9), ClimbMove{Op: ClimbRung, Cell: Cell{0, 2, 0}}},
		{"top of the wall", at(3, 3, 9), ClimbMove{ClimbHang, Cell{0, 4, 0}, FacingWest}},
		{"ledge", at(4, 4, 9), ClimbMove{Op: ClimbStepOff, Cell: Cell{1, 5, 0}}},
		{"out of ladders", at(2, 2, 0), ClimbMove{Op: ClimbStuck}},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, tc.got, tc.want)
		}
	}
}
