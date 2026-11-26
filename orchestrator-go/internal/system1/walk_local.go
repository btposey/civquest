package system1

// walkLocal is the typed value-type cursor of the "walk" family of activities (bootstrap
// timber/craft gathering, exploration, patrol, frontier scouting, the shelter night-outpost
// search) — ADR-036 §1 (FL), bead cq-2sr.9. Each of those frames owns one in its Local; it
// lives exactly as long as the frame and is discarded on pop. None of it lives on AgentState.
//
// Evaluators receive *walkLocal and mutate it in place; the activity Step copies the Local in
// and the mutated copy becomes the next Local. A nil *walkLocal means "no frame bound" (a
// direct call): evaluators use a fresh cursor (see orFreshWalk).

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

type walkLocal struct {
	lastExploreTick         int64
	lastBootstrapNavTick    int64
	navigatingToPOI         bool
	exploreStepIndex        int
	lastHeadingRad          float64
	frontierAzimuthRad      float64
	frontierAzimuthSet      bool
	frontierRadialLeg       int
	currentExploreTarget    *civquestv1.Vec3 // immutable once set; replaced, never mutated
	lastExploreProgressDist float64
	lastExploreProgressTick int64

	// wantExpedition is the walk's request to push an expedition over its frame; the activity
	// Step consumes it (it is a step result, not progress).
	wantExpedition bool

	// task is the committed task this cursor's frame owns: the task frame's bound Local while
	// it steps, or a task a waterfall started inline that the kernel frames next tick.
	// taskReceipt is the receipt routed to it, consumed by its next step.
	task        *TaskSubroutine
	taskReceipt *civquestv1.IntentReceipt

	// craftLatch holds the in-flight craft until the inventory reflects it (cq-dcx.2).
	craftLatch craftLatch
}

func orFreshWalk(w *walkLocal) *walkLocal {
	if w == nil {
		return &walkLocal{}
	}
	return w
}

// frontierAzimuth returns the locked frontier sector azimuth and whether it is set.
func (w *walkLocal) frontierAzimuth() (float64, bool) {
	return w.frontierAzimuthRad, w.frontierAzimuthSet
}
