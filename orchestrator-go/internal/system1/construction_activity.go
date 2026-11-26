package system1

// Blueprint construction as a frame-local activity (ADR-036, bead cq-gpa.6).
//
// Root cause of "no BuildBlock/place intents in SETTLEMENT": BlueprintEvaluator publishes
// blueprint.recommended and ConstructionSkill (category "construction") knows how to walk a
// blueprint, but nothing ever drove it: no activity selected the "construction" category and
// no ConstructionCoordinator was ever built outside tests. SETTLEMENT therefore only ran
// patrol/armory/foraging/shelter/sleep, whatever the blueprint facts said.
//
// This activity is that missing driver. Its Local is the project cursor (blueprint, anchor,
// quadrant, block index); the partitioned work list is derived once in Init and treated as
// read-only. Progress is banked to AgentState (constructionStore) when the frame exits, so a
// dusk/sleep interruption resumes where it stopped and a finished blueprint is not rebuilt.

import (
	"math"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const activityKindConstruction = "construction"

// constructionActionTicks throttles intent emission (one navigate/dig/place per interval).
const constructionActionTicks = int64(5)

// constructionSiteOffset places the site east of the home origin, clear of the outpost cabin.
const constructionSiteOffset = 8

// ConstructionProgress is the banked project cursor (Store: written by the construction frame
// on exit, read by the next one's args).
type ConstructionProgress struct {
	BlueprintID    string
	AX, AY, AZ     int
	Quadrant, Item int
}

// constructionState is the AgentState slice for construction (progress + finished projects).
type constructionState struct {
	mu       sync.Mutex
	progress *ConstructionProgress
	built    map[string]bool
}

// constructionStore is the Store write a construction frame emits when it exits.
type constructionStore struct {
	Progress ConstructionProgress
	Done     bool
}

// ConstructionProgressOf returns the banked in-flight project, if any.
func (s *AgentState) ConstructionProgressOf() (ConstructionProgress, bool) {
	if s == nil {
		return ConstructionProgress{}, false
	}
	s.construction.mu.Lock()
	defer s.construction.mu.Unlock()
	if s.construction.progress == nil {
		return ConstructionProgress{}, false
	}
	return *s.construction.progress, true
}

// IsBlueprintBuilt reports whether this agent finished building the blueprint.
func (s *AgentState) IsBlueprintBuilt(id string) bool {
	if s == nil {
		return false
	}
	s.construction.mu.Lock()
	defer s.construction.mu.Unlock()
	return s.construction.built[id]
}

func (s *AgentState) applyConstructionStore(w constructionStore) {
	s.construction.mu.Lock()
	defer s.construction.mu.Unlock()
	if w.Done {
		if s.construction.built == nil {
			s.construction.built = map[string]bool{}
		}
		s.construction.built[w.Progress.BlueprintID] = true
		s.construction.progress = nil
		return
	}
	p := w.Progress
	s.construction.progress = &p
}

// constructionArgs starts (or resumes) a project.
type constructionArgs struct {
	Blueprint *blueprints.Blueprint
	Anchor    [3]int
	Quadrant  int
	Item      int
}

// constructionLocal is the construction frame Local.
type constructionLocal struct {
	BlueprintID    string
	Anchor         [3]int
	Work           map[blueprints.Quadrant][]*civquestv1.BuildBlockIntent // read-only after Init
	Quadrant, Item int
	LastAction     int64
	Done           bool
}

func (l constructionLocal) progress() ConstructionProgress {
	return ConstructionProgress{BlueprintID: l.BlueprintID, AX: l.Anchor[0], AY: l.Anchor[1], AZ: l.Anchor[2], Quadrant: l.Quadrant, Item: l.Item}
}

// current returns the quadrant and work item the cursor points at, advancing past empty
// or finished quadrants; ok=false means the project is complete.
func (l *constructionLocal) current() (blueprints.Quadrant, *civquestv1.BuildBlockIntent, bool) {
	quads := blueprints.AllQuadrants()
	for l.Quadrant < len(quads) {
		w := l.Work[quads[l.Quadrant]]
		if l.Item < len(w) {
			return quads[l.Quadrant], w[l.Item], true
		}
		l.Quadrant++
		l.Item = 0
	}
	return "", nil, false
}

type constructionActivity struct{ frameBase }

func (a constructionActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a constructionActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (constructionActivity) fallThrough(*fsmWorld) bool { return true }

func (constructionActivity) Init(_ htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(constructionArgs)
	l := constructionLocal{Anchor: a.Anchor, Quadrant: a.Quadrant, Item: a.Item}
	if a.Blueprint == nil {
		l.Done = true
		return l, htpda.Lease{}
	}
	l.BlueprintID = a.Blueprint.ID
	vm, err := a.Blueprint.ToVoxelMatrix()
	if err != nil {
		l.Done = true
		return l, htpda.Lease{}
	}
	l.Work = blueprints.PartitionBlueprintWithAnchor(vm, a.Anchor[0], a.Anchor[1], a.Anchor[2])
	return l, htpda.Lease{}
}

// project resolves what to build: the banked in-flight project, else the recommended
// blueprint (not yet built) at a site beside the home origin.
func (constructionActivity) project(lw *fsmWorld) (constructionArgs, bool) {
	ctx := lw.ctx
	if ctx == nil || ctx.State == nil || lw.reg == nil {
		return constructionArgs{}, false
	}
	ev, ok := lw.reg.GetEvaluator("blueprint")
	if !ok {
		return constructionArgs{}, false
	}
	be, ok := ev.(*BlueprintEvaluator)
	if !ok {
		return constructionArgs{}, false
	}
	if p, ok := ctx.State.ConstructionProgressOf(); ok {
		if bp := be.blueprintByID(p.BlueprintID); bp != nil {
			return constructionArgs{Blueprint: bp, Anchor: [3]int{p.AX, p.AY, p.AZ}, Quadrant: p.Quadrant, Item: p.Item}, true
		}
	}
	if ctx.Facts == nil {
		return constructionArgs{}, false
	}
	rec := ctx.Facts.GetString("blueprint.recommended")
	home := ctx.State.GetHomeOrigin()
	if rec == "" || home == nil || ctx.State.IsBlueprintBuilt(rec) {
		return constructionArgs{}, false
	}
	bp := be.blueprintByID(rec)
	if bp == nil {
		return constructionArgs{}, false
	}
	return constructionArgs{Blueprint: bp, Anchor: [3]int{int(math.Floor(home.X)) + constructionSiteOffset, int(math.Floor(home.Y)), int(math.Floor(home.Z))}}, true
}

func (a constructionActivity) args(lw *fsmWorld) (any, bool) {
	p, ok := a.project(lw)
	if !ok {
		return nil, false
	}
	return p, true
}

// eligible: SETTLEMENT, awake, daytime working routine, no conversation, a project to build.
func (a constructionActivity) eligible(lw *fsmWorld) bool {
	ctx := lw.ctx
	if lw.fsm.currentStage != StageSettlement || ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	f := ctx.Frame
	if len(f.IncomingChats) > 0 || f.PendingTrade != nil || f.Status.IsSleeping || ctx.IsDuskOrNight() || hasSurvivalPreemption(f) {
		return false
	}
	switch lw.fsm.currentRoutine {
	case RoutinePrimaryDuty, RoutineSecondaryHobby:
	default:
		return false
	}
	_, ok := a.project(lw)
	return ok
}

func (constructionActivity) ended(local any) bool {
	l, _ := local.(constructionLocal)
	return l.Done
}

func (a constructionActivity) store(local any) htpda.StoreWrite {
	l, _ := local.(constructionLocal)
	return constructionStore{Progress: l.progress(), Done: l.Done && l.BlueprintID != ""}
}

func (a constructionActivity) cancel(local any) htpda.Step {
	return htpda.Step{Op: htpda.OpPop, Store: []htpda.StoreWrite{a.store(local)}}
}

func (a constructionActivity) exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	st := popFrame(lw, a.kind, intents, halt)
	st.Store = []htpda.StoreWrite{a.store(local)}
	return st
}

// run is one step: hold while pathing or inside the action throttle, else emit the next
// navigate/dig/place for the cursor's work item.
func (a constructionActivity) run(lw *fsmWorld, local any) htpda.Step {
	l, _ := local.(constructionLocal)
	f := lw.ctx.Frame
	q, work, ok := l.current()
	if !ok {
		l.Done = true
		return htpda.Step{Op: htpda.OpStay, Next: l}
	}
	if f.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
		(l.LastAction > 0 && f.WorldTick >= l.LastAction && f.WorldTick-l.LastAction < constructionActionTicks) {
		return htpda.Step{Op: htpda.OpStay, Next: l}
	}
	intents, advance := constructionWorkStep(f, q, work)
	if advance {
		l.Item++
	}
	if len(intents) > 0 {
		l.LastAction = f.WorldTick
	}
	return htpda.Step{Op: htpda.OpStay, Next: l, Intents: intents}
}

// tick is unused (the frame drives run directly) but required by waterfallActivity.
func (constructionActivity) tick(*fsmWorld) ([]FSMIntent, bool) { return nil, true }

// blueprintByID returns the library blueprint with id (nil when unknown).
func (e *BlueprintEvaluator) blueprintByID(id string) *blueprints.Blueprint {
	for _, bp := range e.getLibraryBlueprints() {
		if bp != nil && bp.ID == id {
			return bp
		}
	}
	return nil
}
