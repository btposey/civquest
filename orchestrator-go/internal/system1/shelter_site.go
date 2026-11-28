package system1

// Where a cabin goes (cq-3ru). Once the agent has a settlement, its permanent cabin stands on a
// plot round it, claimed in the clan's registry so that no two overlap. A cabin once started is
// finished before another is begun, and no new one is started away from the settlement: the
// night is spent there in a temporary shelter (shelter_bivouac.go). Without a settlement the
// cabin goes where the agent first has materials.

import (
	"math"
	"slices"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// homeRange is how far a settlement or a cabin is still walked to (homeRangeSq).
	homeRange = 96.0
	// plotSpacing separates plot centres and plotHalf is the ground a cabin plot keeps to
	// itself either side of its centre: the 5x5 cabin, its doorstep and a way between.
	plotSpacing = int32(10)
	plotHalf    = int32(4)
	plotRings   = 2
	// plotRejects bounds the plots passed over as uneven or unreachable; after that the next
	// free one is built on as it is.
	plotRejects = 6
	plotWalk    = int64(40)
)

// PlotRegistry is the clan-shared plot port, held by the same registry as bed claims.
type PlotRegistry interface {
	ClaimPlot(agentID string, candidates []db.Plot, worldTick int64) (db.Plot, bool, error)
}

func (s *AgentState) plotReg() PlotRegistry {
	r, _ := s.bedReg().(PlotRegistry)
	return r
}

// rejectedPlots are the plots this agent has passed over.
type rejectedPlots struct {
	At [plotRejects]db.Plot
	N  int
}

func (r rejectedPlots) has(p db.Plot) bool { return slices.Contains(r.At[:r.N], p) }

// plotCandidates lists the plots round a settlement, nearest ring first, in an order every
// agent shares. A homestead is its owner's alone, so its centre is a plot too.
func plotCandidates(st Settlement) []db.Plot {
	cx, cz := int32(math.Floor(st.Pos[0])), int32(math.Floor(st.Pos[2]))
	var out []db.Plot
	if st.Kind == SettlementHomestead {
		out = append(out, db.Plot{X: cx, Z: cz, Half: plotHalf})
	}
	for ring := int32(1); ring <= plotRings; ring++ {
		var at [][2]int32
		for z := -ring; z <= ring; z++ {
			for x := -ring; x <= ring; x++ {
				if max(intAbs(int(x)), intAbs(int(z))) == int(ring) {
					at = append(at, [2]int32{x, z})
				}
			}
		}
		slices.SortStableFunc(at, func(a, b [2]int32) int { return int(a[0]*a[0] + a[1]*a[1] - b[0]*b[0] - b[1]*b[1]) })
		for _, c := range at {
			out = append(out, db.Plot{X: cx + c[0]*plotSpacing, Z: cz + c[1]*plotSpacing, Half: plotHalf})
		}
	}
	return out
}

func withinRange(pos *civquestv1.Vec3, x, z, r float64) bool {
	return math.Hypot(pos.X-x, pos.Z-z) <= r
}

func nearBlock(pos *civquestv1.Vec3, b *civquestv1.BlockPosProto, r float64) bool {
	return b != nil && withinRange(pos, float64(b.X)+0.5, float64(b.Z)+0.5, r)
}

// offSite reports a cabin at b that is no part of the settlement the agent now stands by.
func offSite(st Settlement, pos *civquestv1.Vec3, b *civquestv1.BlockPosProto) bool {
	return withinRange(pos, st.Pos[0], st.Pos[2], homeRange) &&
		!nearBlock(&civquestv1.Vec3{X: st.Pos[0], Z: st.Pos[2]}, b, homeRange)
}

// noCabinFromHere reports that there is no cabin work to be had where the agent stands: its
// started cabin is out of range, or it has none and is away from its settlement.
func (s *AgentState) noCabinFromHere(pos *civquestv1.Vec3) bool {
	if pos == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, settled := s.settlementLocked()
	if s.CabinBuilt > 0 && s.ShelterPos != nil {
		return !nearBlock(pos, s.ShelterPos, homeRange) && !(settled && offSite(st, pos, s.ShelterPos))
	}
	return settled && !withinRange(pos, st.Pos[0], st.Pos[2], homeRange)
}

// timberTripLocked reports that a plot's builder is short of blocks with a tree in view, so
// the walk back to the plot waits. state.mu held.
func (s *AgentState) timberTripLocked(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, inv bootstrapInventorySummary) bool {
	return inv.buildingBlockCount < 16 && inv.logCount < 4 && len(usableTrunks(frame, pos, s, nil)) > 0
}

type siteVerdict uint8

const (
	siteBuild siteVerdict = iota // the cabin work goes on from here
	siteWait                     // walking to the plot, or choosing another
	siteNone                     // no cabin work from here
)

// siteCabin settles where the Shelter frame's cabin goes before any work on it.
func (lw *fsmWorld) siteCabin(l *shelterLocal) ([]*civquestv1.AgentIntent, siteVerdict) {
	s, frame, pos := lw.ctx.State, lw.Frame(), lw.pos()
	if s == nil || frame == nil || frame.Status == nil || pos == nil {
		return nil, siteBuild
	}
	st, settled := s.Settlement()
	reg, tick := s.plotReg(), lw.Tick()
	if l.BuildIndex > 0 && l.Pos != nil {
		switch {
		case settled && offSite(st, pos, l.Pos):
			l.BuildIndex, l.Phase, l.Pos = 0, 0, nil // begun too far off ever to go back to
		case !nearBlock(pos, l.Pos, homeRange):
			return nil, siteNone
		default:
			if settled && reg != nil {
				// A cabin begun before the settlement keeps its ground from the others.
				_, _, _ = reg.ClaimPlot(lw.agentID(), []db.Plot{{X: l.Pos.X, Z: l.Pos.Z, Half: plotHalf}}, tick)
			}
			return nil, siteBuild
		}
	}
	if !settled {
		return nil, siteBuild
	}
	if !withinRange(pos, st.Pos[0], st.Pos[2], homeRange) {
		return nil, siteNone
	}
	inv := inspectBootstrapInventory(frame.Status.Inventory)
	if l.BuildIndex == 0 && inv.buildingBlockCount == 0 && inv.logCount == 0 {
		return nil, siteBuild // a plot is taken once there is something to build with
	}
	free := slices.DeleteFunc(plotCandidates(st), l.Rejected.has)
	var plot db.Plot
	ok := len(free) > 0
	if ok && reg == nil {
		plot = free[0]
	} else if ok {
		var err error
		plot, ok, err = reg.ClaimPlot(lw.agentID(), free, tick)
		ok = ok && err == nil // never build on ground that may be another's
	}
	if !ok {
		return nil, siteNone
	}
	if l.Pos == nil || l.Pos.X != plot.X || l.Pos.Z != plot.Z {
		l.Pos = &civquestv1.BlockPosProto{X: plot.X, Y: int32(math.Floor(st.Pos[1])), Z: plot.Z}
		l.WalkTick = 0
	}
	reject := func() ([]*civquestv1.AgentIntent, siteVerdict) {
		if l.Rejected.N == plotRejects {
			return nil, siteBuild
		}
		l.Rejected.At[l.Rejected.N] = plot
		l.Rejected.N++
		l.Pos = nil
		return nil, siteWait
	}
	cx, cz := float64(plot.X)+0.5, float64(plot.Z)+0.5
	if withinRange(pos, cx, cz, 1.5) {
		l.Pos.Y = int32(math.Floor(pos.Y))
		if unevenGround(frame, pos) {
			return reject()
		}
		return nil, siteBuild
	}
	s.mu.Lock()
	givenUp := s.givenUpLocked(cx, float64(l.Pos.Y), cz, tick)
	goNow := inv.buildingBlockCount > 0 && !s.timberTripLocked(frame, pos, inv)
	s.mu.Unlock()
	switch {
	case givenUp:
		return reject()
	case !goNow:
		return nil, siteBuild // fetch what to build with first
	case frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
		l.WalkTick != 0 && tick >= l.WalkTick && tick-l.WalkTick < plotWalk:
		return nil, siteWait
	}
	l.WalkTick = tick
	return []*civquestv1.AgentIntent{{AgentId: lw.agentID(), Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target: &civquestv1.Vec3{X: cx, Y: float64(l.Pos.Y), Z: cz}, SpeedMultiplier: 1.0, ArrivalTolerance: 1,
	}}}}, siteWait
}

// unevenGround reports ground round pos that rises above the feet, or falls away on some sides
// and not on others, as far as the frame shows it.
func unevenGround(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) bool {
	solid, f := frameSolid(frame), ladderCell(pos)
	level := 0
	for _, d := range horizontalDirs {
		beside := f
		beside.X, beside.Y, beside.Z = f.X+int(d[0]), f.Y-1, f.Z+int(d[1])
		if solid(beside) {
			level++
		}
		for dist := 1; dist <= 2; dist++ {
			c := f
			c.X, c.Y, c.Z = f.X+dist*int(d[0]), f.Y+1, f.Z+dist*int(d[1])
			if solid(c) {
				return true
			}
		}
	}
	return level > 0 && level < len(horizontalDirs)
}
