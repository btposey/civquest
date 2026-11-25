package vertical

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

// world builds a solid lookup: ground plane at y<=0 (agent feet at y=1),
// plus extra solid cells.
func world(extra ...Cell) SolidFunc {
	set := map[Cell]bool{}
	for _, c := range extra {
		set[c] = true
	}
	return func(c Cell) bool { return c.Y <= 0 || set[c] }
}

func trunk(x, z, y0, h int) []Cell {
	var out []Cell
	for y := y0; y < y0+h; y++ {
		out = append(out, Cell{x, y, z})
	}
	return out
}

var agent = Vec3{5.5, 1, 0.5} // feet on ground (cell y=1), next to trunk at (5,*,... )

func TestReachBoundary(t *testing.T) {
	// feet y=1, eye=2.62. Target straight above: centre y = ty+0.5; dy = ty-2.12.
	// dy == 4.5 exactly when ty = 6.62 -> use custom eye so it is exact.
	feet := Vec3{0.5, 0, 0.5}
	eye := 1.5 // eye y 1.5
	// target centre y = ty+0.5; dy = ty-1.0; ty=5 => dy=4.0; need exactly 4.5: reach=4.0
	if !Reachable(feet, eye, 4.0, Cell{0, 5, 0}) {
		t.Fatal("exactly at reach must be reachable")
	}
	if Reachable(feet, eye, 4.0-1e-6, Cell{0, 5, 0}) {
		t.Fatal("just beyond reach must be unreachable")
	}
	// default reach 4.5, default eye 1.62: ty=5 -> dy=5.5-2.62=2.88 ok ; ty=7 -> dy=4.88 no
	d := DecideReach(ReachInput{Targets: []Cell{{0, 1 + 4, 0}}, Agent: Vec3{0.5, 1, 0.5}})
	if !d.FromGround {
		t.Fatal("log 4 above feet should be reachable")
	}
	d = DecideReach(ReachInput{Targets: []Cell{{0, 1 + 6, 0}}, Agent: Vec3{0.5, 1, 0.5}})
	if d.FromGround || len(d.Unreachable) != 1 {
		t.Fatalf("log 6 above feet should be unreachable: %+v", d)
	}
}

func TestReachDecisionAllOrNothing(t *testing.T) {
	logs := trunk(0, 0, 1, 8)
	d := DecideReach(ReachInput{Targets: logs, Agent: Vec3{0.5, 1, 1.5}})
	if d.FromGround {
		t.Fatal("any unreachable log forces ladders")
	}
	if len(d.Unreachable) == 0 || len(d.Unreachable) == len(logs) {
		t.Fatalf("expected a partial unreachable set, got %d", len(d.Unreachable))
	}
	for i := 1; i < len(d.Unreachable); i++ {
		if d.Unreachable[i].Y > d.Unreachable[i-1].Y {
			t.Fatal("unreachable must be top-down")
		}
	}
}

func TestTrunkHeights(t *testing.T) {
	for h := 1; h <= 14; h++ {
		t.Run(fmt.Sprintf("h%d", h), func(t *testing.T) {
			logs := trunk(0, 0, 1, h)
			ag := Vec3{1.5, 1, 0.5}
			in := ClimbInput{Targets: logs, Agent: ag, Solid: world(), HaveLadders: 2}
			p := PlanClimb(in)
			rd := DecideReach(ReachInput{Targets: logs, Agent: ag})
			if rd.FromGround != p.FromGround {
				t.Fatalf("FromGround mismatch rd=%v plan=%v", rd.FromGround, p.FromGround)
			}
			if p.FromGround {
				if p.LaddersNeeded != 0 || len(p.LadderCells) != 0 {
					t.Fatal("no ladders when from ground")
				}
				return
			}
			if !p.OK() {
				t.Fatalf("failure %s", p.Failure)
			}
			if p.LaddersNeeded != len(p.LadderCells) || p.Placements() != p.LaddersNeeded {
				t.Fatal("ladder count mismatch")
			}
			if p.LaddersToCraft != max0(p.LaddersNeeded-2) {
				t.Fatalf("craft %d", p.LaddersToCraft)
			}
			// contiguous column from ground
			for i, c := range p.LadderCells {
				if c.Y != 1+i || c.X != p.LadderCells[0].X || c.Z != p.LadderCells[0].Z {
					t.Fatalf("ladder cells not a contiguous column: %v", p.LadderCells)
				}
			}
			// fell order top-down, covers all logs, reachable, stand below log or ground
			if len(p.FellOrder) != h {
				t.Fatalf("fell order len %d", len(p.FellOrder))
			}
			for i, s := range p.FellOrder {
				if i > 0 && s.Log.Y > p.FellOrder[i-1].Log.Y {
					t.Fatal("not top-down")
				}
				if !Reachable(FeetAt(s.Stand), DefaultEyeHeight, DefaultReach, s.Log) {
					t.Fatalf("log %v not reachable from %v", s.Log, s.Stand)
				}
				if s.Stand.Y >= s.Log.Y && s.Stand.Y != 1 {
					t.Fatalf("standing at/above own log %v from %v", s.Log, s.Stand)
				}
			}
			// cleanup is top-down reversal of ladders
			for i, c := range p.CleanupOrder {
				if c != p.LadderCells[len(p.LadderCells)-1-i] {
					t.Fatal("cleanup order")
				}
			}
			// minimality: top rung one lower would not reach the top log
			top := p.LadderCells[len(p.LadderCells)-1]
			if top.Y >= 2 {
				if Reachable(Vec3{0.5, float64(top.Y - 1), 0.5}, DefaultEyeHeight, DefaultReach, Cell{1, h, 0}) && top.Y-1 < h {
					t.Fatalf("too many ladders: %d for h=%d", p.LaddersNeeded, h)
				}
			}
		})
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func TestLadderCountsKnown(t *testing.T) {
	// feet y=1; top log at y=h. Reach to center of log from rung s horizontal 1:
	// need |h+0.5-(s+1.62)| <= sqrt(4.5^2-1)=4.387 -> s >= h-5.5 -> s>=h-5.
	for _, tc := range []struct{ h, ladders int }{{8, 3 + 1 - 1 + 0}, {10, 5}, {14, 9}} {
		logs := trunk(0, 0, 1, tc.h)
		p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
		// ladders occupy y=1..topRung, topRung = h-5
		want := tc.h - 5
		if p.LaddersNeeded != want {
			t.Errorf("h=%d got %d want %d", tc.h, p.LaddersNeeded, want)
		}
	}
}

func TestAttachPrefersTrunk(t *testing.T) {
	logs := trunk(0, 0, 1, 12)
	// add a neighbouring wall at (-1,*,-1)... irrelevant; also solid next to ladder column.
	var wall []Cell
	for y := 1; y <= 12; y++ {
		wall = append(wall, Cell{2, y, 0}) // east of ladder column x=1
	}
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: world(wall...)})
	if !p.OK() {
		t.Fatal(p.Failure)
	}
	for _, f := range p.AttachFaces {
		if !f.OnTrunk {
			t.Fatalf("expected trunk attach, got %+v", f)
		}
		// ladder at x=1? support at x=0 means facing east
		if f.Ladder.X == 1 && f.Support.X == 0 && f.Facing != FacingEast {
			t.Fatalf("support west => facing east, got %s", f.Facing)
		}
	}
}

func TestFacingConvention(t *testing.T) {
	cases := []struct {
		ladder, support Cell
		facing          string
	}{
		{Cell{0, 5, 0}, Cell{0, 5, 1}, FacingNorth}, // support south -> faces north
		{Cell{0, 5, 0}, Cell{0, 5, -1}, FacingSouth},
		{Cell{0, 5, 0}, Cell{1, 5, 0}, FacingWest},
		{Cell{0, 5, 0}, Cell{-1, 5, 0}, FacingEast},
	}
	for _, c := range cases {
		trunkSet := map[Cell]bool{c.support: true}
		f, ok := pickAttach(c.ladder, trunkSet, func(Cell) bool { return false })
		if !ok || f.Facing != c.facing || f.Support != c.support {
			t.Errorf("%+v got %+v", c, f)
		}
	}
}

func TestAttachFallsBackToAdjacentSolid(t *testing.T) {
	// trunk with a gap at y=4..5: ladder cells there must use a neighbouring solid.
	var logs []Cell
	for y := 1; y <= 12; y++ {
		if y == 4 || y == 5 {
			continue
		}
		logs = append(logs, Cell{0, y, 0})
	}
	// Only column x=1 has a side wall at (1,y,1) -> (z+1) for y in 4..5
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: world(Cell{1, 4, 1}, Cell{1, 5, 1})})
	if !p.OK() {
		t.Fatalf("failure %s", p.Failure)
	}
	sawFallback := false
	for _, f := range p.AttachFaces {
		if f.Ladder.Y == 4 || f.Ladder.Y == 5 {
			if f.OnTrunk || f.Support != (Cell{f.Ladder.X, f.Ladder.Y, f.Ladder.Z + 1}) {
				// another column may have been chosen; accept any solid support
				if f.OnTrunk {
					t.Fatalf("gap cell cannot attach to trunk: %+v", f)
				}
			}
			sawFallback = true
		}
	}
	if !sawFallback {
		t.Fatal("no gap rung seen")
	}
}

func TestNoAttachFailure(t *testing.T) {
	// trunk with a gap and no solid neighbours anywhere around the gap -> failure.
	var logs []Cell
	for y := 1; y <= 12; y++ {
		if y == 4 {
			continue
		}
		logs = append(logs, Cell{0, y, 0})
	}
	// The only candidate columns are the 4 neighbours; at y=4 each has no solid
	// neighbour except the (gone) trunk. Diagonal-adjacent columns would only
	// touch trunk logs above/below, not same-level.
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
	if p.OK() || p.Failure != FailNoAttachFace {
		t.Fatalf("want %s got %+v", FailNoAttachFace, p)
	}
	if len(p.LadderCells) != 0 || p.LaddersNeeded != 0 {
		t.Fatal("failed plan must be empty")
	}
}

func TestNoGroundFailure(t *testing.T) {
	logs := trunk(0, 0, 1, 12)
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: func(Cell) bool { return false }})
	if p.Failure != FailNoGround {
		t.Fatalf("got %q", p.Failure)
	}
}

func TestNoTargets(t *testing.T) {
	if p := PlanClimb(ClimbInput{}); p.Failure != FailNoTargets {
		t.Fatal(p.Failure)
	}
}

func TestSingleTrunkNoNeighbourUsesTrunkFace(t *testing.T) {
	logs := trunk(0, 0, 1, 10)
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
	if !p.OK() || p.LaddersNeeded == 0 {
		t.Fatalf("%+v", p)
	}
	for _, f := range p.AttachFaces {
		if !f.OnTrunk {
			t.Fatal("isolated trunk must use trunk face")
		}
	}
}

func TestBreakableClearedAndNotSupport(t *testing.T) {
	logs := trunk(0, 0, 1, 10)
	leaf := Cell{1, 3, 0}
	solid := world(leaf)
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: solid,
		Breakable: func(c Cell) bool { return c == leaf }})
	if !p.OK() {
		t.Fatal(p.Failure)
	}
	// either avoided the leaf column or listed it for clearing
	for _, l := range p.LadderCells {
		if l == leaf {
			found := false
			for _, c := range p.ClearCells {
				found = found || c == leaf
			}
			if !found {
				t.Fatal("leaf in ladder column must be in ClearCells")
			}
		}
	}
	// without Breakable the leaf column is rejected
	p2 := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{1.5, 1, 0.5}, Solid: solid})
	for _, l := range p2.LadderCells {
		if l == leaf {
			t.Fatal("solid unbreakable cell used as ladder cell")
		}
	}
}

func TestZeroNonLadderPlacements(t *testing.T) {
	// ClimbPlan exposes exactly one placement list; guard against new ones.
	typ := reflect.TypeOf(ClimbPlan{})
	allowed := map[string]bool{"LadderCells": true, "AttachFaces": true, "RungStandCells": true, "ClearCells": true,
		"FellOrder": true, "CleanupOrder": true, "FromGround": true, "Failure": true, "LaddersNeeded": true, "LaddersToCraft": true}
	for i := 0; i < typ.NumField(); i++ {
		if !allowed[typ.Field(i).Name] {
			t.Errorf("unreviewed ClimbPlan field %s: must not introduce non-ladder placements", typ.Field(i).Name)
		}
	}
	for h := 6; h <= 14; h++ {
		p := PlanClimb(ClimbInput{Targets: trunk(0, 0, 1, h), Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
		if p.Placements() != p.LaddersNeeded || len(p.AttachFaces) != len(p.LadderCells) {
			t.Fatalf("h=%d placements beyond ladders", h)
		}
		trunkSet := map[Cell]bool{}
		for _, c := range trunk(0, 0, 1, h) {
			trunkSet[c] = true
		}
		for _, l := range p.LadderCells {
			if trunkSet[l] {
				t.Fatal("ladder placed inside trunk")
			}
		}
	}
}

func TestTwoByTwoTrunk(t *testing.T) {
	var logs []Cell
	for _, x := range []int{0, 1} {
		for _, z := range []int{0, 1} {
			logs = append(logs, trunk(x, z, 1, 12)...)
		}
	}
	p := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{2.5, 1, 0.5}, Solid: world()})
	if !p.OK() || len(p.FellOrder) != len(logs) {
		t.Fatalf("%+v", p.Failure)
	}
	for i := 1; i < len(p.FellOrder); i++ {
		if p.FellOrder[i].Log.Y > p.FellOrder[i-1].Log.Y {
			t.Fatal("not top-down")
		}
	}
	for _, s := range p.FellOrder {
		if !Reachable(FeetAt(s.Stand), DefaultEyeHeight, DefaultReach, s.Log) {
			t.Fatalf("unreachable %v from %v", s.Log, s.Stand)
		}
	}
}

func TestDeterminism(t *testing.T) {
	var logs []Cell
	for _, x := range []int{0, 1} {
		for _, z := range []int{0, 1} {
			logs = append(logs, trunk(x, z, 1, 11)...)
		}
	}
	first := PlanClimb(ClimbInput{Targets: logs, Agent: Vec3{2.5, 1, 0.5}, Solid: world()})
	for i := 0; i < 50; i++ {
		// shuffle input order
		sh := append([]Cell(nil), logs...)
		for j := range sh {
			k := (j*7 + i) % len(sh)
			sh[j], sh[k] = sh[k], sh[j]
		}
		got := PlanClimb(ClimbInput{Targets: sh, Agent: Vec3{2.5, 1, 0.5}, Solid: world()})
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("non-deterministic at iter %d", i)
		}
	}
}

func TestInputNotMutated(t *testing.T) {
	logs := trunk(0, 0, 1, 12)
	cp := append([]Cell(nil), logs...)
	for i, j := 0, len(cp)-1; i < j; i, j = i+1, j-1 {
		cp[i], cp[j] = cp[j], cp[i]
	}
	in := append([]Cell(nil), cp...)
	PlanClimb(ClimbInput{Targets: cp, Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
	if !reflect.DeepEqual(cp, in) {
		t.Fatal("input mutated")
	}
}

func TestVerticalAccess(t *testing.T) {
	// roof cell 8 above ground next to a wall
	var wall []Cell
	for y := 1; y <= 9; y++ {
		wall = append(wall, Cell{3, y, 0})
	}
	target := Cell{3, 9, 0}
	p := VerticalAccess(AccessInput{Target: target, Agent: Vec3{1.5, 1, 0.5}, Solid: world(wall...)})
	if !p.OK() || p.FromGround || p.LaddersNeeded == 0 {
		t.Fatalf("%+v", p)
	}
	if len(p.FellOrder) != 1 || p.FellOrder[0].Log != target {
		t.Fatal("work order should be the single target")
	}
	low := VerticalAccess(AccessInput{Target: Cell{3, 3, 0}, Agent: Vec3{1.5, 1, 0.5}, Solid: world()})
	if !low.FromGround || low.LaddersNeeded != 0 {
		t.Fatal("low target is reachable from ground")
	}
	_ = math.Pi
}
