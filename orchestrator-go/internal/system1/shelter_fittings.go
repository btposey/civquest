package system1

// The cabin's door and windows (cq-390.8). When the build comes to one, the Shelter frame
// hangs what it holds, else makes it: it fetches timber, sets down or walks to a crafting
// table and crafts. Every ask is bounded. A fitting that cannot be had is passed over, a
// window being walled up if there is anything to wall it with, so the shelter is finished
// either way. The job is a value on the frame's Local.

import (
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// fittingWait is the time one fitting is given in all.
	fittingWait = int64(2400)
	// fittingPatience is how long nothing at all may be possible for it; a gap longer than
	// fittingIdleGap means something was being done in between.
	fittingPatience = int64(200)
	fittingIdleGap  = int64(40)
	// fittingAsks bounds the crafts sent for one fitting: planks, a table, the item, and
	// each again if it fails.
	fittingAsks = 8
	// fittingRange is how far from the site the frame may go for what a fitting needs.
	fittingRange = 48.0
	// fittingTimber is the planks a table and a door take together.
	fittingTimber = int32(10)
	fittingWalk   = int64(40)

	// EventFittingPassedOver: a cabin was built on without a door or a window.
	EventFittingPassedOver = "FITTING_PASSED_OVER"
)

// fittingJob is the work on one door or window. Index is one more than the blueprint index it
// is about, 0 none.
type fittingJob struct {
	Index            int
	Since            int64 // tick the fitting was first wanted
	IdleSince, Idled int64 // the first and latest tick of the present run with nothing to do
	Walked           int64 // tick of the last walk to a table
	Asks             int
	Walled           bool // a window given up: wall material goes in its place
}

// fittingWalled reports that the block the build is at is a window being walled up.
func (l *shelterLocal) fittingWalled() bool { return l.Fit.Walled && l.Fit.Index == l.BuildIndex+1 }

// fittingView is what one step sees of the fitting the build is at.
type fittingView struct {
	state      string // "" when the next block is no fitting still to be had
	held, near bool
}

// fetching: nothing held will do and the site is near, so the frame may leave it to get what
// will.
func (v fittingView) fetching() bool { return v.state != "" && !v.held && v.near }

// lookAtFittingLocked opens the job on the fitting the build has come to, passing over one
// whose time is spent, however that time went: fetching, walking or crafting. state.mu held.
func (s *AgentState) lookAtFittingLocked(l *shelterLocal, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3,
	inv bootstrapInventorySummary) fittingView {
	for {
		at := blueprints.CabinFittingAt(l.BuildIndex)
		if at == "" || l.fittingWalled() {
			return fittingView{}
		}
		_, held := blueprints.FittingFill(at, heldItemIDs(frame.Status.Inventory))
		v := fittingView{state: at, held: held,
			near: l.Pos != nil && math.Hypot(pos.X-float64(l.Pos.X), pos.Z-float64(l.Pos.Z)) <= fittingRange}
		if !v.fetching() {
			return v
		}
		if l.Fit.Index != l.BuildIndex+1 || frame.WorldTick < l.Fit.Since {
			l.Fit = fittingJob{Index: l.BuildIndex + 1, Since: frame.WorldTick}
		}
		if frame.WorldTick-l.Fit.Since < fittingWait {
			return v
		}
		s.passOverFittingLocked(l, inv)
	}
}

// fittingTableLocked is the crafting table to make a fitting at: none that lies farther from
// the site than the frame may go.
func (s *AgentState) fittingTableLocked(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3,
	site *civquestv1.BlockPosProto) (*civquestv1.VisibleBlock, float64) {
	table, _, distSq := findNearestCraftingTableLocked(frame, pos, s)
	if table == nil || table.Pos == nil || site != nil &&
		math.Hypot(float64(table.Pos.X-site.X), float64(table.Pos.Z-site.Z)) > fittingRange {
		return nil, math.MaxFloat64
	}
	return table, distSq
}

// timberShort reports that the fitting cannot be made from the pack. state.mu held.
func (v fittingView) timberShort(state *AgentState, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3,
	site *civquestv1.BlockPosProto, inv bootstrapInventorySummary) bool {
	graph := state.RecipeGraphLocked()
	if graph.IsEmpty() {
		return false
	}
	held := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	table, _ := state.fittingTableLocked(frame, pos, site)
	if table == nil && held[craftingTableItem] == 0 && inv.logCount*4+inv.planksCount < fittingTimber {
		return true
	}
	for _, item := range blueprints.FittingCrafts(v.state) {
		if graph.CanEventuallyCraft(item, held) {
			return false
		}
	}
	return true
}

// fittingStepLocked is one step toward making the fitting being fetched, and passes it over
// when there has long been nothing to do for it. state.mu held.
func (s *AgentState) fittingStepLocked(agentID, at string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3,
	navState civquestv1.NavigationState, prog *shelterLocal) *civquestv1.AgentIntent {
	tick := frame.WorldTick
	job := prog.Fit
	in, working := s.makeFittingLocked(agentID, at, frame, pos, navState, prog, &job)
	switch {
	case working:
		job.IdleSince = 0
	case job.IdleSince == 0 || tick-job.Idled > fittingIdleGap:
		job.IdleSince, job.Idled = tick, tick
	default:
		job.Idled = tick
	}
	prog.Fit = job
	if job.IdleSince != 0 && tick-job.IdleSince >= fittingPatience {
		s.passOverFittingLocked(prog, inspectBootstrapInventory(frame.Status.Inventory))
		return nil
	}
	return in
}

// makeFittingLocked crafts what the fitting needs, with the table that takes. working is false
// when there is nothing to be done toward it from here.
func (s *AgentState) makeFittingLocked(agentID, at string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3,
	navState civquestv1.NavigationState, prog *shelterLocal, job *fittingJob) (in *civquestv1.AgentIntent, working bool) {
	graph := s.RecipeGraphLocked()
	if graph.IsEmpty() {
		return nil, false
	}
	tick := frame.WorldTick
	held := crafting.InventoryMapFromProtos(frame.Status.Inventory)
	table, tableDistSq := s.fittingTableLocked(frame, pos, prog.Pos)
	inReach := isCraftingTableWithinReach(table, tableDistSq, pos)

	craft := func(step crafting.Step) (*civquestv1.AgentIntent, bool) {
		if prog.Walk.craftHeld(graph, step.RecipeID, held, tick) {
			return nil, true // in flight
		}
		if job.Asks >= fittingAsks {
			return nil, false
		}
		if last := s.mem.lastCraftTick; last != 0 && tick >= last && tick-last < 20 {
			return nil, true
		}
		job.Asks++
		s.mem.lastCraftTick = tick
		prog.Walk.armCraft(graph, step.RecipeID, held, tick)
		c := &civquestv1.CraftItemIntent{RecipeId: step.RecipeID, BatchCount: 1}
		if step.Requires3x3 && inReach {
			c.WorkstationPos = table.Pos
		}
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_CraftItem{CraftItem: c}}, true
	}

	wantsTable := false
	for _, item := range blueprints.FittingCrafts(at) {
		if !graph.CanEventuallyCraft(item, held) {
			continue
		}
		if step, ok := graph.PlanNextStep(item, held, inReach); ok && step.CanExecuteNow {
			return craft(step)
		}
		if step, ok := graph.PlanNextStep(item, held, true); ok && step.CanExecuteNow {
			wantsTable = true
		}
	}
	if !wantsTable || inReach {
		return nil, false
	}
	switch {
	case table != nil:
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
			s.MarkWorkstationUnreachableLocked(table.Pos.X, table.Pos.Y, table.Pos.Z, tick)
			return nil, true
		}
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
			job.Walked != 0 && tick >= job.Walked && tick-job.Walked < fittingWalk {
			return nil, true
		}
		job.Walked = tick
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:          &civquestv1.Vec3{X: float64(table.Pos.X) + 0.5, Y: float64(table.Pos.Y), Z: float64(table.Pos.Z) + 0.5},
			SpeedMultiplier: 1.0, ArrivalTolerance: 2,
		}}}, true
	case held[craftingTableItem] > 0:
		in, refused := s.placeStationLocked(agentID, craftingTableItem, frame, &prog.Walk)
		return in, !refused
	case !s.stationRefusedLocked(craftingTableItem, frame):
		if step, ok := graph.PlanNextStep(craftingTableItem, held, false); ok && step.CanExecuteNow {
			return craft(step)
		}
	}
	return nil, false
}

// passOverFittingLocked ends the work on a fitting that cannot be had: a window is walled up
// if there is a block for it, and otherwise the build goes on with the opening left.
func (s *AgentState) passOverFittingLocked(prog *shelterLocal, inv bootstrapInventorySummary) {
	at := blueprints.CabinFittingAt(prog.BuildIndex)
	walled := blueprints.IsWindow(at) && !prog.fittingWalled() && inv.buildingBlockCount > 0
	how := "left open"
	if walled {
		prog.Fit = fittingJob{Index: prog.BuildIndex + 1, Walled: true}
		how = "walled up"
	} else {
		prog.BuildIndex++
		prog.Phase = cabinPhase(prog.BuildIndex)
	}
	s.noteDiag(EventFittingPassedOver, at+" could not be had: "+how, map[string]any{"fitting": at, "how": how})
}
