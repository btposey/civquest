package system1

// Climbing a wall by ladder (cq-htv.1): a step-level activity (ADR-040) that hangs ladders up the
// agent's own column and climbs onto the ledge above. It uses the ladders carried (the reserve of
// plan/ladders.go) and crafts from carried wood only when it has none (cq-htv.4). Recover
// pushes it for a trapped agent; travel and felling may push it with the wall they choose. The
// ladders stay where they are hung (user, 2026-11-27).
//
// It pops ladderResult, and every way it can fail is bounded: an ask that is refused or not
// answered, climbing that gets no higher, or the lease, which only a new rung or a new height
// renews. What to do instead is the owner's choice.

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

const (
	activityKindLadder = "ladder"

	ladderItem = "minecraft:ladder"

	// EventLadderClimbed and EventLadderFailed report how a climb ended.
	EventLadderClimbed = "LADDER_CLIMBED"
	EventLadderFailed  = "LADDER_FAILED"
)

// ladderTunables is the single home of the climb's numbers (initial values).
var ladderTunables = struct {
	// Stock ladders are made when none are carried, if the wood lasts: the wall's height is unseen.
	Stock int
	// StockTicks bounds the making of them.
	StockTicks int64
	// LeaseTicks passes at most between one rung or height and the next.
	LeaseTicks int64
	// AskTicks bounds one placement or climbing leg.
	AskTicks int64
	// IdleWalks is how many climbing legs may end no higher before the climb has failed.
	IdleWalks int
}{Stock: plan.LadderReserve, StockTicks: 400, LeaseTicks: 600, AskTicks: 200, IdleWalks: 3}

// ladderArgs names the wall: the side of the agent's cell it is on. With a Drop it names instead
// the side of a descent that far (ladder_descent.go).
type ladderArgs struct {
	DirX, DirZ int32
	Drop       int
}

// ladderResult is the pop Result.
type ladderResult struct {
	ladderArgs
	Climbed bool
	Reason  string // why not
}

// ladderAsk is the placement or climbing leg in flight.
type ladderAsk struct {
	Op   vertical.ClimbOp
	Y    int
	Tick int64
}

type ladderLocal struct {
	ladderArgs
	Column  vertical.Cell // the base of the ladder column
	Walk    walkLocal     // craft latch and station asks
	Started int64         // when the making of ladders began
	Making  bool          // ladders are being made
	Made    bool          // the one making of this climb is over
	Top     int           // highest rung hung
	Best    int           // highest cell reached
	Idle    int           // climbing legs since Best rose
	Ask     ladderAsk
	Waiting bool // Ask is out
}

type ladderActivity struct{}

var _ htpda.Activity = ladderActivity{}

func (ladderActivity) Kind() htpda.Kind              { return activityKindLadder }
func (ladderActivity) Scope() htpda.Scope            { return htpda.ScopeMotor }
func (ladderActivity) Propose(htpda.World) htpda.Bid { return htpda.Bid{} }

func (ladderActivity) Init(w htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(ladderArgs)
	loc := ladderLocal{ladderArgs: a}
	if lw := asFSMWorld(w); lw != nil {
		if p := lw.pos(); p != nil {
			loc.Column = ladderCell(p)
			if a.Drop > 0 {
				loc.Column = loc.Column.Add(int(a.DirX), -a.Drop, int(a.DirZ)) // the landing
			}
			loc.Top, loc.Best = loc.Column.Y-1, loc.Column.Y
		}
	}
	return loc, htpda.Lease{StartTick: w.Tick(), DeadlineTick: w.Tick() + ladderTunables.LeaseTicks}
}

// ladderCell is the cell a body is in; on a ladder it counts as on the rung it has all but
// reached.
func ladderCell(p *civquestv1.Vec3) vertical.Cell {
	return vertical.Cell{X: int(math.Floor(p.X)), Y: int(math.Floor(p.Y + 0.4)), Z: int(math.Floor(p.Z))}
}

// frameSolid reports full blocks among those the frame lists.
func frameSolid(f *civquestv1.TelemetryFrame) vertical.SolidFunc {
	return func(c vertical.Cell) bool {
		b := FindVisibleBlockAt(f, int32(c.X), int32(c.Y), int32(c.Z))
		return b != nil && isSuffocatingBlock(b.BlockId)
	}
}

// canLadder reports whether ladders are carried, or the wood to make some: 7 sticks, and a
// crafting table unless one is carried or in reach.
func canLadder(inv map[string]int, tableInReach bool) bool {
	if inv[ladderItem] > 0 {
		return true
	}
	planks := 0
	for id, n := range inv {
		switch {
		case strings.HasSuffix(id, "_planks"):
			planks += n
		case strings.HasSuffix(id, "_log"), strings.HasSuffix(id, "_wood"), strings.HasSuffix(id, "_stem"):
			planks += 4 * n
		}
	}
	need := 0
	if short := 7 - inv["minecraft:stick"]; short > 0 {
		need = (short + 3) / 4 * 2
	}
	if !tableInReach && inv[craftingTableItem] == 0 {
		need += 4
	}
	return planks >= need
}

func (a ladderActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stationStep(w, local, func(local any) htpda.Step { return a.step(w, htpda.LocalAs[ladderLocal](local), ev) })
}

func (a ladderActivity) step(w htpda.World, loc ladderLocal, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	if lw == nil || lw.ctx == nil || lw.ctx.State == nil {
		return htpda.Step{Op: htpda.OpPop, Result: ladderResult{ladderArgs: loc.ladderArgs, Reason: "no world"}}
	}
	switch e := ev.(type) {
	case htpda.Cancel:
		return htpda.Step{Op: htpda.OpPop}
	case htpda.Expired:
		return a.fail(lw, loc, "took too long")
	case htpda.Resumed, htpda.ChildDone:
		loc.Waiting = false // whatever held the motor ended the ask
	case htpda.Receipt:
		switch e.Status {
		case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED:
			if loc.Waiting {
				reason := lw.receipt.GetFailureReason()
				return a.fail(lw, loc, "refused: "+reason)
			}
			if loc.Walk.craftLatch.armed() { // a refused craft: climb with what there is
				loc.Walk.craftLatch, loc.Making, loc.Made = craftLatch{}, false, true
			}
		case civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED:
			if loc.Waiting && loc.Ask.Op == vertical.ClimbHang {
				loc.Top = loc.Ask.Y
				st := a.next(lw, loc.answered())
				st.Lease = a.renew(lw)
				return st
			}
			loc = loc.answered()
		case civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED, civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX:
			loc.Waiting = false
		}
	}
	return a.next(lw, loc)
}

// next is the move of the climb or descent under way.
func (a ladderActivity) next(lw *fsmWorld, loc ladderLocal) htpda.Step {
	if loc.Drop > 0 && lw.Frame().GetStatus() != nil && lw.pos() != nil {
		return a.descend(lw, loc)
	}
	return a.advance(lw, loc)
}

func (l ladderLocal) answered() ladderLocal {
	l.Waiting = false
	return l
}

// made ends the making of ladders when done.
func (l ladderLocal) made(done bool) ladderLocal {
	if done {
		l.Making, l.Made = false, true
	}
	return l
}

func (ladderActivity) renew(lw *fsmWorld) *htpda.Lease {
	return &htpda.Lease{StartTick: lw.Tick(), DeadlineTick: lw.Tick() + ladderTunables.LeaseTicks}
}

func (ladderActivity) succeed(lw *fsmWorld, loc ladderLocal, event, summary string, height int) htpda.Step {
	lw.ctx.State.noteDiag(event, summary, map[string]any{
		"column": []int{loc.Column.X, loc.Column.Y, loc.Column.Z}, "height": height})
	return htpda.Step{Op: htpda.OpPop, Result: ladderResult{ladderArgs: loc.ladderArgs, Climbed: true}}
}

func (ladderActivity) fail(lw *fsmWorld, loc ladderLocal, reason string) htpda.Step {
	lw.ctx.State.noteDiag(EventLadderFailed, "ladder climb failed: "+reason, map[string]any{
		"reason": reason, "column": []int{loc.Column.X, loc.Column.Y, loc.Column.Z}, "height": loc.Best - loc.Column.Y})
	return htpda.Step{Op: htpda.OpPop, Result: ladderResult{ladderArgs: loc.ladderArgs, Reason: reason}}
}

func (a ladderActivity) advance(lw *fsmWorld, loc ladderLocal) htpda.Step {
	f, pos, tick := lw.Frame(), lw.pos(), lw.Tick()
	if f == nil || f.Status == nil || pos == nil {
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	}
	at := ladderCell(pos)
	inColumn := at.X == loc.Column.X && at.Z == loc.Column.Z
	onLedge := at.X == loc.Column.X+int(loc.DirX) && at.Z == loc.Column.Z+int(loc.DirZ) && at.Y >= loc.Column.Y+2
	switch {
	case onLedge:
		return a.succeed(lw, loc, EventLadderClimbed, fmt.Sprintf("climbed %d blocks by ladder", at.Y-loc.Column.Y), at.Y-loc.Column.Y)
	case loc.Waiting:
		if tick-loc.Ask.Tick >= ladderTunables.AskTicks {
			return a.fail(lw, loc, "no answer")
		}
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	case !inColumn:
		return a.fail(lw, loc, "no longer at the wall")
	}
	var lease *htpda.Lease
	if at.Y > loc.Best {
		loc.Best, loc.Idle, lease = at.Y, 0, a.renew(lw)
	}
	// Rungs already there (an earlier climb's) are climbed, not hung again.
	for {
		b := FindVisibleBlockAt(f, int32(loc.Column.X), int32(loc.Top+1), int32(loc.Column.Z))
		if b == nil || b.BlockId != ladderItem {
			break
		}
		loc.Top++
	}
	carried := crafting.InventoryMapFromProtos(f.Status.Inventory)[ladderItem]
	move := vertical.NextClimb(vertical.ClimbState{At: at, DX: int(loc.DirX), DZ: int(loc.DirZ), BaseY: loc.Column.Y,
		Top: loc.Top, Ladders: carried}, frameSolid(f))
	if move.Op == vertical.ClimbStuck && carried == 0 && !loc.Made && !loc.Making {
		loc.Making, loc.Started = true, tick
	}
	if loc.Making {
		in, next := a.stock(lw, loc)
		if !next.Making {
			st := a.advance(lw, next)
			if st.Lease == nil {
				st.Lease = lease
			}
			return st
		}
		st := htpda.Step{Op: htpda.OpStay, Next: next, Lease: lease}
		if in != nil {
			st.Intents = []*civquestv1.AgentIntent{in}
		}
		return st
	}
	var intent *civquestv1.AgentIntent
	switch move.Op {
	case vertical.ClimbHang:
		intent = hangLadder(lw.agentID(), move)
	case vertical.ClimbRung, vertical.ClimbStepOff:
		if loc.Idle >= ladderTunables.IdleWalks {
			return a.fail(lw, loc, "climbing got no higher")
		}
		loc.Idle++
		intent = recoverStep(lw.agentID(), int32(move.Cell.X), int32(move.Cell.Y), int32(move.Cell.Z))
	default:
		reason := "no way up the wall"
		if carried == 0 {
			reason = "out of ladders"
		}
		return a.fail(lw, loc, reason)
	}
	loc.Ask, loc.Waiting = ladderAsk{Op: move.Op, Y: move.Cell.Y, Tick: tick}, true
	return htpda.Step{Op: htpda.OpStay, Next: loc, Lease: lease, Intents: []*civquestv1.AgentIntent{intent}}
}

func hangLadder(agentID string, move vertical.ClimbMove) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_BuildBlock{BuildBlock: &civquestv1.BuildBlockIntent{
		TargetVoxel:      &civquestv1.BlockPosProto{X: int32(move.Cell.X), Y: int32(move.Cell.Y), Z: int32(move.Cell.Z)},
		BlockStateString: ladderItem + "[facing=" + move.Facing + "]",
	}}}
}

// stock makes ladders at the wall: a table in reach (set down, or made first), then batches until
// there are enough or no more can be made, which clears Making. A nil intent while Making waits.
func (ladderActivity) stock(lw *fsmWorld, loc ladderLocal) (*civquestv1.AgentIntent, ladderLocal) {
	st, f, pos := lw.ctx.State, lw.Frame(), lw.pos()
	inv := crafting.InventoryMapFromProtos(f.Status.Inventory)
	if inv[ladderItem] >= ladderTunables.Stock || lw.Tick()-loc.Started >= ladderTunables.StockTicks {
		return nil, loc.made(true)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	graph := st.RecipeGraphLocked()
	table, _, distSq := findNearestCraftingTableLocked(f, pos, st)
	inReach := isCraftingTableWithinReach(table, distSq, pos)
	want := ladderItem
	if !inReach {
		want = craftingTableItem
		if inv[craftingTableItem] > 0 {
			in, refused := st.placeStationLocked(lw.agentID(), craftingTableItem, f, &loc.Walk)
			return in, loc.made(refused)
		}
		if st.stationRefusedLocked(craftingTableItem, f) {
			// Just set down and not yet listed, or given up: wait, unless there is nothing to wait for.
			return nil, loc.made(inv[ladderItem] > 0)
		}
	}
	if step, ok := graph.PlanNextStep(want, inv, inReach); !ok || !step.CanExecuteNow {
		return nil, loc.made(true)
	}
	return craftToolIntentLocked(lw.agentID(), f, st, pos, want, &loc.Walk), loc
}
