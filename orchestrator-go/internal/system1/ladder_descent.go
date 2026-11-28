package system1

// Deliberate descent (cq-htv.4): an agent bound for a place below, whose walk there failed, steps
// off a drop that does no harm and at once hangs ladders from the landing up to where it stood, so
// that the way back is there. Nothing unseen is trusted: the mod lists every block of the four
// neighbouring columns and of the agent's own to depth 4, so a cell it does not list there is
// open, and a drop is taken only onto a listed full block with a listed wall to hang each rung on.

import (
	"fmt"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

const (
	// A drop of one is a step back up; one of more than three hurts, and the mod's path planner
	// does not take it.
	minDescent = 2
	maxDescent = 3

	// EventLadderDescended reports a descent whose way back is hung.
	EventLadderDescended = "LADDER_DESCENDED"
)

// safeDrop is how far down the drop on side (dx,dz) of the cell at goes, if it may be taken.
func safeDrop(f *civquestv1.TelemetryFrame, at vertical.Cell, dx, dz int) (int, bool) {
	listed := func(c vertical.Cell) bool { return FindVisibleBlockAt(f, int32(c.X), int32(c.Y), int32(c.Z)) != nil }
	solid := frameSolid(f)
	edge := at.Add(dx, 0, dz)
	if listed(edge) || listed(edge.Add(0, 1, 0)) {
		return 0, false
	}
	for ground := 1; ground <= maxDescent+1; ground++ {
		below := edge.Add(0, -ground, 0)
		if !listed(below) {
			continue
		}
		drop := ground - 1
		if drop < minDescent || !solid(below) || FindVisibleBlockAt(f, int32(below.X), int32(below.Y), int32(below.Z)).BlockId == "minecraft:magma_block" {
			return 0, false // a step, water, lava or other poor footing
		}
		for k := 1; k <= drop; k++ {
			if !solid(at.Add(0, -k, 0)) {
				return 0, false
			}
		}
		return drop, true
	}
	return 0, false // the bottom is not seen
}

// ladderDescent picks the drop a Recover takes towards a target below: once an episode, on the
// side most nearly towards it, with a ladder carried for every block of the way back.
func (loc recoverLocal) ladderDescent(f *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) (ladderArgs, bool) {
	if loc.Cause != CauseTerrain || !loc.HasTarget || loc.Descended || f == nil || f.Status == nil || pos == nil {
		return ladderArgs{}, false
	}
	ladders := crafting.InventoryMapFromProtos(f.Status.Inventory)[ladderItem]
	at := ladderCell(pos)
	var best ladderArgs
	bestToward := 0.0
	for _, d := range CardinalDirs {
		toward := float64(d[0])*(loc.Target[0]-pos.X) + float64(d[1])*(loc.Target[2]-pos.Z)
		drop, ok := safeDrop(f, at, int(d[0]), int(d[1]))
		if ok && toward > bestToward && drop <= ladders && loc.Target[1] <= float64(at.Y-drop)+0.5 {
			best, bestToward = ladderArgs{DirX: d[0], DirZ: d[1], Drop: drop}, toward
		}
	}
	return best, best.Drop > 0
}

// descend walks off the edge and hangs the way back from the landing, all of it within reach.
func (a ladderActivity) descend(lw *fsmWorld, loc ladderLocal) htpda.Step {
	f, at, tick := lw.Frame(), ladderCell(lw.pos()), lw.Tick()
	if loc.Waiting {
		if at != loc.Column || loc.Ask.Op == vertical.ClimbHang {
			if tick-loc.Ask.Tick >= ladderTunables.AskTicks {
				return a.fail(lw, loc, "no answer")
			}
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		loc.Waiting = false // landed
	}
	var intent *civquestv1.AgentIntent
	if at != loc.Column {
		if loc.Idle >= ladderTunables.IdleWalks {
			return a.fail(lw, loc, "did not get down")
		}
		loc.Idle++
		loc.Ask = ladderAsk{Op: vertical.ClimbStepOff, Tick: tick}
		intent = recoverStep(lw.agentID(), int32(loc.Column.X), int32(loc.Column.Y), int32(loc.Column.Z))
	} else {
		for b := FindVisibleBlockAt(f, int32(at.X), int32(loc.Top+1), int32(at.Z)); b != nil && b.BlockId == ladderItem; b = FindVisibleBlockAt(f, int32(at.X), int32(loc.Top+1), int32(at.Z)) {
			loc.Top++
		}
		if loc.Top >= loc.Column.Y+loc.Drop-1 {
			return a.succeed(lw, loc, EventLadderDescended, fmt.Sprintf("dropped %d blocks and hung the way back", loc.Drop), loc.Drop)
		}
		move := vertical.NextClimb(vertical.ClimbState{At: at, DX: -int(loc.DirX), DZ: -int(loc.DirZ), BaseY: loc.Column.Y,
			Top: loc.Top, Ladders: crafting.InventoryMapFromProtos(f.Status.Inventory)[ladderItem]}, frameSolid(f))
		if move.Op != vertical.ClimbHang {
			return a.fail(lw, loc, "cannot hang the way back")
		}
		loc.Ask = ladderAsk{Op: move.Op, Y: move.Cell.Y, Tick: tick}
		intent = hangLadder(lw.agentID(), move)
	}
	loc.Waiting = true
	return htpda.Step{Op: htpda.OpStay, Next: loc, Lease: a.renew(lw), Intents: []*civquestv1.AgentIntent{intent}}
}
