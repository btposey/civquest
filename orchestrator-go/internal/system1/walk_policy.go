package system1

// The one walking policy (ADR-040 stage 5), applied to every activity's navigate intents where
// they leave the stack: a walk sent again and again while the agent neither moves nor has a path
// under way is going nowhere (cq-6l3.22: one target sent 69 times). The send is withheld and the
// target remembered as unreachable, so the activity chooses another: no sidestep helps a path
// that never starts, unlike a body reporting itself stuck (Recover, ADR-037). Emergency and
// Recover manage their own movement.

import (
	"fmt"
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	// A walk is given up after this many sends over at least this long without headway.
	walkGiveUpSends = 6
	walkGiveUpTicks = int64(80)
	// Closer than this, two targets are the same one and the agent has not moved.
	walkSameDist = 0.5
	// A walk reported under way that has not moved the agent for this long is going nowhere.
	walkStillTicks = int64(200)
	// Further than this above or below, a target is not arrived at by standing under or over it.
	walkArrivalHeight = 4.0
)

// walkWatch follows one frame's current walk.
type walkWatch struct {
	target, from [3]float64
	since        int64
	sends        int
}

func near(a, b [3]float64) bool {
	return math.Abs(a[0]-b[0]) < walkSameDist && math.Abs(a[1]-b[1]) < walkSameDist && math.Abs(a[2]-b[2]) < walkSameDist
}

// steersItself names the kinds whose walks are exact moves out of trouble: they are neither
// bent round a danger nor withheld.
func steersItself(kind htpda.Kind) bool {
	return kind == activityKindEmergency || kind == activityKindRecover || kind == activityKindLadder
}

// routeRoundDanger bends a walk that would cross a registered danger to a waypoint beside it;
// the activity sends its walk again from there. Emergency and Recover steer for themselves.
func routeRoundDanger(w *fsmWorld, kind htpda.Kind, nav *civquestv1.NavigateIntent) {
	pos := w.pos()
	if pos == nil || w.ctx == nil || w.ctx.State == nil || steersItself(kind) {
		return
	}
	to := [3]float64{nav.Target.X, nav.Target.Y, nav.Target.Z}
	if via, ok := w.ctx.State.detour([3]float64{pos.X, pos.Y, pos.Z}, to, w.tick); ok {
		nav.Target = &civquestv1.Vec3{X: via[0], Y: via[1], Z: via[2]}
		nav.IntoCell = false // a waypoint is passed, not stood in
	}
}

// walkAllowedLocked records frame's walk to nav and reports whether it may still be sent.
func (fsm *AgentFSM) walkAllowedLocked(w *fsmWorld, frame htpda.FrameID, kind htpda.Kind, nav *civquestv1.NavigateIntent) bool {
	pos := w.pos()
	if pos == nil || nav.Target == nil || w.ctx == nil || w.ctx.State == nil || steersItself(kind) {
		return true
	}
	target := [3]float64{nav.Target.X, nav.Target.Y, nav.Target.Z}
	here := [3]float64{pos.X, pos.Y, pos.Z}
	pathing := w.ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	watch, ok := fsm.walks[frame]
	if !ok || !near(watch.target, target) || !near(watch.from, here) {
		if fsm.walks == nil {
			fsm.walks = map[htpda.FrameID]walkWatch{}
		}
		fsm.walks[frame] = walkWatch{target: target, from: here, since: w.tick, sends: 1}
		return true
	}
	watch.sends++
	fsm.walks[frame] = watch
	// A body that says it is pathing is given longer, but not for ever: it may be turning on
	// the spot toward a point under its own feet.
	waiting := watch.sends < walkGiveUpSends || w.tick-watch.since < walkGiveUpTicks
	if pathing {
		waiting = w.tick-watch.since < walkStillTicks
	}
	if waiting ||
		(math.Hypot(target[0]-here[0], target[2]-here[2]) <= float64(nav.ArrivalTolerance)+walkSameDist &&
			math.Abs(target[1]-here[1]) <= walkArrivalHeight) {
		return true // standing on the target is arrival, not a failed walk
	}
	delete(fsm.walks, frame)
	w.ctx.State.MarkTargetUnreachable(target[0], target[1], target[2], w.tick, DefaultUnreachableTargetTTL)
	w.ctx.State.wantFreshBearing("a walk was going nowhere")
	fsm.meter.walkGivenUp()
	fsm.attempting(kind, describeWalk(nav)+", going nowhere", w.tick)
	if fsm.eventSink != nil {
		fsm.eventSink(w.tick, EventWalkGivenUp, fmt.Sprintf("%s after %d sends", kind, watch.sends), map[string]any{
			"activity": string(kind), "target": target[:], "position": here[:], "sends": watch.sends, "since_tick": watch.since})
	}
	return false
}
