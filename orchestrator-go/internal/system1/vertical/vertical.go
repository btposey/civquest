// Package vertical is a pure, value-type planner for vertical access:
// deciding whether logs (or any target cell) can be worked from the ground and,
// if not, planning a ladder column and a top-down felling order.
//
// It has no AgentState access and performs no I/O. It is intended to be called
// from a frame-local activity (ADR-036; bead cq-3a0.3) whose Local carries the
// resulting plan by value.
//
// User rules encoded here:
//   - Bottom-up harvesting only when every log is reachable from the ground.
//   - Otherwise ladders and top-down felling.
//   - The plan never places any non-ladder block (no pillar/column material).
//   - Ladders are carried in inventory and picked up after use (CleanupOrder).
//
// Coordinate conventions: Minecraft block coordinates (X east, Y up, Z south),
// matching the facing strings in internal/blueprints (north = -Z, south = +Z,
// east = +X, west = -X). Positions of the agent are feet positions in world
// (float) coordinates; a block cell (x,y,z) spans [x,x+1) etc.
//
// Ladder facing: vanilla "facing" is the direction the ladder faces, i.e.
// AWAY from its supporting block. A ladder supported by the block to its south
// (z+1) has facing=north.
package vertical

import (
	"math"
	"sort"
)

// Defaults.
const (
	DefaultReach     = 4.5
	DefaultEyeHeight = 1.62
)

// Facing strings match internal/blueprints (FacingNorth etc.).
const (
	FacingNorth = "north"
	FacingEast  = "east"
	FacingSouth = "south"
	FacingWest  = "west"
)

// Failure reasons.
const (
	FailNone         = ""
	FailNoTargets    = "no_targets"
	FailNoGround     = "no_ground_at_ladder_base"
	FailNoAttachFace = "no_valid_attach_face"
	FailUnreachable  = "target_unreachable_from_rungs"
)

// Cell is an integer block position.
type Cell struct{ X, Y, Z int }

// Add returns c offset by (dx,dy,dz).
func (c Cell) Add(dx, dy, dz int) Cell { return Cell{c.X + dx, c.Y + dy, c.Z + dz} }

// Vec3 is a world-space point (feet position for agents).
type Vec3 struct{ X, Y, Z float64 }

// SolidFunc reports whether a cell is a solid, full block.
type SolidFunc func(Cell) bool

// FeetAt returns the standing position at the centre of cell c's floor.
func FeetAt(c Cell) Vec3 { return Vec3{float64(c.X) + 0.5, float64(c.Y), float64(c.Z) + 0.5} }

// Reachable reports whether the centre of block target is within reach of an
// agent standing at feet with the given eye height. Distance is inclusive
// (exactly == reach is reachable).
func Reachable(feet Vec3, eyeHeight, reach float64, target Cell) bool {
	dx := float64(target.X) + 0.5 - feet.X
	dy := float64(target.Y) + 0.5 - (feet.Y + eyeHeight)
	dz := float64(target.Z) + 0.5 - feet.Z
	return dx*dx+dy*dy+dz*dz <= reach*reach+1e-9
}

// ReachInput configures ReachDecision.
type ReachInput struct {
	Targets   []Cell  // logs to harvest
	Agent     Vec3    // feet position the agent will work from
	EyeHeight float64 // default DefaultEyeHeight
	Reach     float64 // default DefaultReach
}

func (in ReachInput) eye() float64 {
	if in.EyeHeight <= 0 {
		return DefaultEyeHeight
	}
	return in.EyeHeight
}
func (in ReachInput) reach() float64 {
	if in.Reach <= 0 {
		return DefaultReach
	}
	return in.Reach
}

// ReachDecision says whether bottom-up harvesting from the ground is allowed.
type ReachDecision struct {
	FromGround  bool   // true only if EVERY target is reachable from Agent
	Unreachable []Cell // targets beyond reach, sorted top-to-bottom
}

// DecideReach applies the user rule: bottom-up only if every log is reachable.
func DecideReach(in ReachInput) ReachDecision {
	var un []Cell
	for _, t := range in.Targets {
		if !Reachable(in.Agent, in.eye(), in.reach(), t) {
			un = append(un, t)
		}
	}
	sortTopDown(un)
	return ReachDecision{FromGround: len(un) == 0, Unreachable: un}
}

// AttachFace describes where one ladder is mounted.
type AttachFace struct {
	Ladder  Cell   // the ladder cell
	Support Cell   // the solid block it rests against
	Facing  string // blockstate facing (away from Support)
	OnTrunk bool   // Support is one of the target logs
}

// FellStep is one log to break and the rung to break it from.
type FellStep struct {
	Log   Cell
	Stand Cell // rung (or ground) cell the agent stands in
}

// ClimbInput configures PlanClimb.
type ClimbInput struct {
	Targets     []Cell // trunk logs (all columns/heights to fell)
	Agent       Vec3   // agent feet position (ground level defines the ladder base)
	Solid       SolidFunc
	Breakable   SolidFunc // optional: solid cells that may be cleared (leaves); never used as support
	HaveLadders int       // ladders currently in inventory
	EyeHeight   float64
	Reach       float64
}

// ClimbPlan is the output of PlanClimb. All slices are fresh copies.
type ClimbPlan struct {
	FromGround     bool         // true: no ladders needed, harvest bottom-up
	Failure        string       // non-empty: no valid plan
	LadderCells    []Cell       // bottom-to-top placement order
	AttachFaces    []AttachFace // parallel to LadderCells
	RungStandCells []Cell       // cells the agent stands in (== LadderCells; base is ground-level)
	ClearCells     []Cell       // breakable cells (leaves) to clear before placing
	LaddersNeeded  int
	LaddersToCraft int        // max(0, LaddersNeeded-HaveLadders)
	FellOrder      []FellStep // top-to-bottom
	CleanupOrder   []Cell     // ladders to pick up, top-to-bottom (descent order)
}

// OK reports whether the plan is usable.
func (p ClimbPlan) OK() bool { return p.Failure == FailNone }

// PlacesOnlyLadders is a structural guarantee: the plan only ever places
// ladders (LadderCells); there is no other placement list. Returns the number
// of placements, which always equals LaddersNeeded.
func (p ClimbPlan) Placements() int { return len(p.LadderCells) }

var horiz = [4]struct {
	dx, dz int
	facing string // facing of a ladder whose support lies in (dx,dz) direction
}{
	{0, -1, FacingSouth}, // support to the north -> faces south
	{1, 0, FacingWest},   // support to the east  -> faces west
	{0, 1, FacingNorth},  // support to the south -> faces north
	{-1, 0, FacingEast},  // support to the west  -> faces east
}

// PlanClimb computes a ladder climb for the targets. If everything is within
// reach from the ground, it returns FromGround=true with no ladders.
func PlanClimb(in ClimbInput) ClimbPlan {
	if len(in.Targets) == 0 {
		return ClimbPlan{Failure: FailNoTargets}
	}
	eye, reach := in.EyeHeight, in.Reach
	if eye <= 0 {
		eye = DefaultEyeHeight
	}
	if reach <= 0 {
		reach = DefaultReach
	}
	rd := DecideReach(ReachInput{Targets: in.Targets, Agent: in.Agent, EyeHeight: eye, Reach: reach})
	if rd.FromGround {
		return ClimbPlan{FromGround: true}
	}

	logs := append([]Cell(nil), in.Targets...)
	sortTopDown(logs)
	trunk := make(map[Cell]bool, len(logs))
	for _, l := range logs {
		trunk[l] = true
	}
	solid := func(c Cell) bool {
		if trunk[c] {
			return true
		}
		return in.Solid != nil && in.Solid(c)
	}
	breakable := func(c Cell) bool {
		return !trunk[c] && in.Breakable != nil && in.Breakable(c)
	}
	support := func(c Cell) bool { return solid(c) && !breakable(c) }

	baseY := int(math.Floor(in.Agent.Y + 1e-9))
	topLog := logs[0]

	// Highest rung needed: lowest rung s such that the top log is reachable from
	// it while standing strictly below the log (so breaking it never removes the
	// ladder under the agent) -- or the ground when the log is at ground level.
	// Horizontal offset is one block (ladder column adjacent to the trunk).
	topRung := baseY
	for s := baseY; ; s++ {
		if s >= topLog.Y || Reachable(Vec3{0.5, float64(s), 0.5}, eye, reach, Cell{1, topLog.Y, 0}) {
			topRung = s
			break
		}
	}
	if topRung >= topLog.Y {
		topRung = maxInt(baseY, topLog.Y-1)
	}

	// Candidate ladder columns: 4-neighbours of trunk footprint, outside it.
	foot := map[[2]int]bool{}
	for _, l := range logs {
		foot[[2]int{l.X, l.Z}] = true
	}
	var cols [][2]int
	seen := map[[2]int]bool{}
	for f := range foot {
		for _, h := range horiz {
			c := [2]int{f[0] + h.dx, f[1] + h.dz}
			if !foot[c] && !seen[c] {
				seen[c] = true
				cols = append(cols, c)
			}
		}
	}
	sort.Slice(cols, func(i, j int) bool {
		if cols[i][0] != cols[j][0] {
			return cols[i][0] < cols[j][0]
		}
		return cols[i][1] < cols[j][1]
	})

	type cand struct {
		faces   []AttachFace
		clear   []Cell
		onTrunk int
		dist    float64
	}
	var best *cand
	failure := FailNoAttachFace
	groundOK := false
	for _, col := range cols {
		base := Cell{col[0], baseY, col[1]}
		if !support(base.Add(0, -1, 0)) {
			continue
		}
		groundOK = true
		c := cand{}
		ok := true
		for y := baseY; y <= topRung && ok; y++ {
			cell := Cell{col[0], y, col[1]}
			if solid(cell) {
				if !breakable(cell) {
					ok = false
					break
				}
				c.clear = append(c.clear, cell)
			}
			face, found := pickAttach(cell, trunk, support)
			if !found {
				ok = false
				break
			}
			if face.OnTrunk {
				c.onTrunk++
			}
			c.faces = append(c.faces, face)
		}
		if ok {
			// head clearance above the top rung
			head := Cell{col[0], topRung + 1, col[1]}
			if solid(head) {
				if !breakable(head) {
					ok = false
				} else {
					c.clear = append(c.clear, head)
				}
			}
		}
		if !ok {
			continue
		}
		dx, dz := float64(col[0])+0.5-in.Agent.X, float64(col[1])+0.5-in.Agent.Z
		c.dist = dx*dx + dz*dz
		if best == nil || c.onTrunk > best.onTrunk || (c.onTrunk == best.onTrunk && c.dist < best.dist) {
			cc := c
			best = &cc
		}
	}
	if best == nil {
		if !groundOK {
			failure = FailNoGround
		}
		return ClimbPlan{Failure: failure}
	}

	plan := ClimbPlan{AttachFaces: best.faces, ClearCells: best.clear}
	for _, f := range best.faces {
		plan.LadderCells = append(plan.LadderCells, f.Ladder)
	}
	plan.RungStandCells = append([]Cell(nil), plan.LadderCells...)
	plan.LaddersNeeded = len(plan.LadderCells)
	if n := plan.LaddersNeeded - in.HaveLadders; n > 0 {
		plan.LaddersToCraft = n
	}
	for i := len(plan.LadderCells) - 1; i >= 0; i-- {
		plan.CleanupOrder = append(plan.CleanupOrder, plan.LadderCells[i])
	}

	// Top-down felling: each log from the highest rung that is strictly below
	// it (or the ground rung) and within reach.
	for _, l := range logs {
		idx := minInt(len(plan.RungStandCells)-1, maxInt(0, l.Y-1-baseY))
		stand := plan.RungStandCells[idx]
		if !Reachable(FeetAt(stand), eye, reach, l) {
			// try lower rungs (never higher: would risk popping own support)
			found := false
			for j := idx - 1; j >= 0; j-- {
				if Reachable(FeetAt(plan.RungStandCells[j]), eye, reach, l) {
					stand, found = plan.RungStandCells[j], true
					break
				}
			}
			if !found {
				return ClimbPlan{Failure: FailUnreachable}
			}
		}
		plan.FellOrder = append(plan.FellOrder, FellStep{Log: l, Stand: stand})
	}
	return plan
}

// pickAttach chooses the support for a ladder cell: a trunk neighbour first,
// otherwise any other solid non-breakable neighbour; deterministic N,E,S,W.
func pickAttach(cell Cell, trunk map[Cell]bool, support SolidFunc) (AttachFace, bool) {
	var fallback *AttachFace
	for _, h := range horiz {
		s := cell.Add(h.dx, 0, h.dz)
		if trunk[s] {
			return AttachFace{Ladder: cell, Support: s, Facing: h.facing, OnTrunk: true}, true
		}
		if fallback == nil && support(s) {
			f := AttachFace{Ladder: cell, Support: s, Facing: h.facing}
			fallback = &f
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return AttachFace{}, false
}

// AccessInput configures the generic VerticalAccess helper.
type AccessInput struct {
	Target      Cell // block to work on (roof cell, cliff block, ...)
	Agent       Vec3
	Solid       SolidFunc
	Breakable   SolidFunc
	HaveLadders int
	EyeHeight   float64
	Reach       float64
}

// VerticalAccess answers "how do I work on this one cell?" for activities
// other than tree felling (shelter roof, cliff). It returns the same plan type;
// FellOrder holds the single target (read it as the work order).
func VerticalAccess(in AccessInput) ClimbPlan {
	return PlanClimb(ClimbInput{
		Targets: []Cell{in.Target}, Agent: in.Agent, Solid: in.Solid, Breakable: in.Breakable,
		HaveLadders: in.HaveLadders, EyeHeight: in.EyeHeight, Reach: in.Reach,
	})
}

func sortTopDown(c []Cell) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Y != c[j].Y {
			return c[i].Y > c[j].Y
		}
		if c[i].X != c[j].X {
			return c[i].X < c[j].X
		}
		return c[i].Z < c[j].Z
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
