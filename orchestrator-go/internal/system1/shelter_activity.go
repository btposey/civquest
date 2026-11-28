package system1

// Shelter and Construction as frame-local activities (ADR-036, bead cq-2sr.8).
//
// Build progress is the Shelter frame's typed value Local; it survives ticks and
// suspend/resume (an emergency frame pushed over Shelter leaves the Local untouched).
// Durable knowledge (ShelterPos, banked cabin block count) reaches the store only
// through the Store effect of the frame's pop/cancel/expiry. "Shelter complete" is
// derived: Local while the frame runs, the banked store count afterwards.

import (
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// shelterLocal is the Shelter frame Local: the frontier-cabin build progress.
// shelterLightTries bounds light placements that never show up; a light seen for
// shelterLightStays ticks has shown up.
const (
	shelterLightTries = 6
	shelterLightStays = int64(200)
)

type shelterLocal struct {
	Breaks     breakRecord // refused_breaks.go
	BuildIndex int         // next cabin blueprint block to place
	Phase      int         // construction phase of the last placed block
	Pos        *civquestv1.BlockPosProto
	// Walk is the night-outpost search/exploration cursor used while sourcing materials.
	Walk walkLocal
	// LightTries counts light placements since a light was last in view.
	LightTries int
	LightSeen  int64 // tick a light came into view (0 none in view)
	// Pending is one more than the blueprint index of the last block sent for placing (0 none);
	// RetryIndex and Tries count the failed attempts at one block (shelter_material.go).
	Pending, RetryIndex, Tries int
	// WalkIndex and Walks count the walks made to get within reach of one block.
	WalkIndex, Walks int
	WalkTick         int64
	// Rest is why the frame is resting this step, "" when it is not (rest.go); QuietSince is
	// the tick the frame last fell quiet (0 while it is asking for things).
	Rest       string
	QuietSince int64
	// TreeLocked/TreeX/TreeZ: the trunk this frame's timber sourcing is focused on (private to
	// the Shelter frame; the frame's existence is the lock).
	TreeLocked   bool
	TreeX, TreeZ int32
}

func (l shelterLocal) rest() string { return l.Rest }

// targetTree returns the focused trunk (nil-safe: no frame bound means no focus).
func (l *shelterLocal) targetTree() (locked bool, x, z int32) {
	if l == nil {
		return false, 0, 0
	}
	return l.TreeLocked, l.TreeX, l.TreeZ
}

func (l *shelterLocal) focusTree(x, z int32) {
	if l != nil {
		l.TreeLocked, l.TreeX, l.TreeZ = true, x, z
	}
}

func (l *shelterLocal) clearTreeFocus() {
	if l != nil {
		l.TreeLocked, l.TreeX, l.TreeZ = false, 0, 0
	}
}

// shelterWalk is the walk cursor of a Shelter frame Local (a fresh one when no frame is bound).
func shelterWalk(prog *shelterLocal) *walkLocal {
	if prog == nil {
		return &walkLocal{}
	}
	return &prog.Walk
}

// complete reports whether every cabin block has been placed.
func (l shelterLocal) complete() bool { return l.BuildIndex >= blueprints.CabinTotalBlocks }

// cabinPhase is the phase of the last placed block (0 before any placement).
func cabinPhase(built int) int {
	if built <= 0 {
		return 0
	}
	return blueprints.CabinBlockPhase(built - 1)
}

// shelterStore is the Store effect a Shelter frame emits when it ends.
type shelterStore struct {
	Pos    *civquestv1.BlockPosProto
	Built  int
	Lights shelterLights
}

// shelterLights counts the light placements that have not shown up at the cabin site At. It
// outlives the frame, or each new frame places the same torches all over again.
type shelterLights struct {
	At    lightPos3
	Tries int
}

type lightPos3 struct {
	X, Y, Z int32
	Set     bool
}

func lightSite(p *civquestv1.BlockPosProto) lightPos3 {
	if p == nil {
		return lightPos3{}
	}
	return lightPos3{X: p.X, Y: p.Y, Z: p.Z, Set: true}
}

// newShelterLocal seeds a Local from the durable store (call without s.mu held).
func newShelterLocal(s *AgentState) shelterLocal {
	if s == nil {
		return shelterLocal{}
	}
	built := s.GetCabinBuilt()
	l := shelterLocal{BuildIndex: built, Phase: cabinPhase(built), Pos: s.GetShelterPos()}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return l.withLights(s.construction.lights)
}

// seedShelterLocalLocked is newShelterLocal for callers already holding s.mu.
func seedShelterLocalLocked(s *AgentState) shelterLocal {
	l := shelterLocal{BuildIndex: s.CabinBuilt, Phase: cabinPhase(s.CabinBuilt)}
	if s.ShelterPos != nil {
		l.Pos = &civquestv1.BlockPosProto{X: s.ShelterPos.X, Y: s.ShelterPos.Y, Z: s.ShelterPos.Z}
	}
	return l.withLights(s.construction.lights)
}

func (l shelterLocal) withLights(v shelterLights) shelterLocal {
	if v.At == lightSite(l.Pos) {
		l.LightTries = v.Tries
	}
	return l
}

func (l shelterLocal) storeWrite() htpda.StoreWrite {
	return shelterStore{Pos: l.Pos, Built: l.BuildIndex, Lights: shelterLights{At: lightSite(l.Pos), Tries: l.LightTries}}
}

// applyShelterStore applies a Shelter frame's Store effect to the durable store.
func (s *AgentState) applyShelterStore(w shelterStore) {
	if w.Pos != nil {
		s.SetShelterPos(w.Pos)
	}
	s.SetCabinBuilt(w.Built)
	s.mu.Lock()
	s.construction.lights = w.Lights
	s.mu.Unlock()
}

// applyStoreWrites applies kernel Store effects (applied only on frame exit).
func (s *AgentState) applyStoreWrites(ws []htpda.StoreWrite) {
	if s == nil {
		return
	}
	for _, w := range ws {
		if v, ok := w.(shelterStore); ok {
			s.applyShelterStore(v)
		}
		if v, ok := w.(constructionStore); ok {
			s.applyConstructionStore(v)
		}
	}
}

// shelterActivity owns its eligibility (Propose) and waterfall; its weight is priorityShelter.
type shelterActivity struct{ frameBase }

func (a shelterActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a shelterActivity) Init(w htpda.World, _ any) (any, htpda.Lease) {
	if lw := asFSMWorld(w); lw != nil && lw.ctx != nil {
		return newShelterLocal(lw.ctx.State), htpda.Lease{}
	}
	return shelterLocal{}, htpda.Lease{}
}
func (a shelterActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (shelterActivity) fallThrough(lw *fsmWorld) bool { return lw.fsm.currentStage != StageColonize }

// cancel banks the Local into the store.
func (shelterActivity) cancel(local any) htpda.Step {
	sl, _ := local.(shelterLocal)
	return htpda.Step{Op: htpda.OpPop, Store: []htpda.StoreWrite{sl.storeWrite()}}
}

// exit ends the frame, banking the Local into the store.
func (a shelterActivity) exit(lw *fsmWorld, local any, intents []*civquestv1.AgentIntent, halt bool) htpda.Step {
	sl, _ := local.(shelterLocal)
	st := popFrame(lw, a.kind, intents, halt)
	st.Store = []htpda.StoreWrite{sl.storeWrite()}
	return st
}

// eligible: awake, no conversation/trade; a BUILD_SHELTER directive biases toward Shelter
// (which places the blocks); otherwise only without a bed/finished shelter, at dusk/night
// (bootstrap defers while a harvest still needs the agent's materials).
func (shelterActivity) eligible(lw *fsmWorld) bool {
	ctx, skillCtx := lw.ctx, lw.skillCtx
	stage, routine := lw.fsm.currentStage, lw.fsm.currentRoutine
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if directiveBuildsShelter(stage, routine, ctx) && !skillIsShelterComplete(skillCtx, ctx.Facts) && !skillIsShelterOnCooldown(skillCtx, ctx.Facts) {
		return true // a BUILD_SHELTER directive biases toward Shelter, which places the blocks
	}
	if skillIsShelterComplete(skillCtx, ctx.Facts) && lw.homeBound() {
		return true // walk home: standing out the night wherever work ended helps nothing
	}
	if skillHasAssignedBed(skillCtx, ctx.Facts) || skillIsShelterComplete(skillCtx, ctx.Facts) || skillIsShelterOnCooldown(skillCtx, ctx.Facts) {
		return false
	}
	if ctx.State.cabinOutOfReach(ctx.Frame.Status.Position, ctx.Frame.WorldTick) {
		return false // other work until a way back may be tried again
	}
	if stage == StageBootstrap {
		if !skillIsDuskOrNight(skillCtx, ctx.Facts) {
			return false
		}
		return !shouldDeferShelterForHarvest(ctx)
	}
	return skillIsDuskOrNight(skillCtx, ctx.Facts) || routine == RoutineSleep
}

func shouldDeferShelterForHarvest(ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if !ctx.State.HasActiveTask() {
		return false
	}
	_, kind, _, _ := ctx.State.ActiveTaskInfo()
	if kind != "harvest_tree" && kind != "collect_items" {
		return false
	}
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	return inv.buildingBlockCount == 0 && !inv.hasLight
}

// hasShelterWorkOrMaterials reports whether a (not yet running) Shelter frame has anything to
// do: banked cabin progress in the store or build materials in reach.
func hasShelterWorkOrMaterials(ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if ctx.State != nil && ctx.State.GetCabinBuilt() > 0 {
		return true
	}
	return hasShelterMaterials(ctx)
}

// hasShelterWork is hasShelterWorkOrMaterials for a running frame: its Local holds the progress.
func hasShelterWork(ctx *FSMContext, l shelterLocal) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	return l.BuildIndex > 0 || hasShelterMaterials(ctx)
}

func hasShelterMaterials(ctx *FSMContext) bool {
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	if inv.logCount > 0 || inv.planksCount > 0 || inv.buildingBlockCount > 0 || inv.hasLight {
		return true
	}
	for _, b := range ctx.Frame.InterestingBlocks {
		if b == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem") {
			return true
		}
	}
	return false
}

// tick: the bootstrap shelter skill, then shelter_construction, then the goal evaluator.
func (shelterActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	ctx, skillCtx, reg := lw.ctx, lw.skillCtx, lw.reg
	var si []*civquestv1.AgentIntent
	if lw.fsm.currentStage == StageBootstrap {
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "shelter"); ok && len(skillIntents) > 0 {
			si = appendNonNilIntents(nil, skillIntents)
		}
	}
	if len(si) == 0 {
		if skill, ok := reg.GetSkill("shelter_construction"); ok && skill != nil && skill.Preconditions(ctx.Facts) {
			if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
				si = appendNonNilIntents(nil, out)
			}
		}
	}
	if len(si) == 0 {
		if intent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Shelter); intent != nil {
			si = []*civquestv1.AgentIntent{intent}
		}
	}
	return wrapOrDone(si)
}

// run is one step of the Shelter frame. The waterfall runs with the frame's Local bound to the
// skill context and the updated Local becomes the next Local.
func (a shelterActivity) run(lw *fsmWorld, local any) htpda.Step {
	sl, _ := local.(shelterLocal)
	sl.Rest = "" // declared afresh by each step that still means it
	if sl.complete() {
		if home := lw.walkHome(&sl); home != nil {
			return htpda.Step{Op: htpda.OpStay, Intents: home, Next: sl}
		}
		return a.exit(lw, sl, nil, true)
	}
	prog := sl
	lw.skillCtx.Shelter = &prog
	fi, done := a.tick(lw)
	lw.skillCtx.Shelter = nil
	intents := unwrapFSMIntents(fi)
	if !done {
		prog.QuietSince = 0
		return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: prog}
	}
	// Nothing to emit this tick. With progress or materials in hand the frame is held instead
	// of dropping progress: the build is waiting (place/craft throttle, pathing), or, with
	// nothing left to do here, the agent rests and says so. Any other quiet is a fault the
	// watchdog reports.
	if hasShelterWork(lw.ctx, prog) {
		if tick := lw.Tick(); prog.QuietSince == 0 || tick < prog.QuietSince {
			prog.QuietSince = tick
		} else if tick-prog.QuietSince >= shelterRestAfter {
			prog.Rest = lw.shelterRest(prog)
		}
		return htpda.Step{Op: htpda.OpStay, Next: prog}
	}
	return a.exit(lw, prog, intents, len(intents) > 0 || !a.fallThrough(lw))
}

// shelterRestAfter is how long the frame must have asked for nothing before the quiet can be
// a rest: longer than the pacing of any send, so work being tried again and again never is.
const shelterRestAfter = int64(100)

// shelterRestReach is how near the shelter site counts as being there: the distance within
// which no walk to it is asked for.
const shelterRestReach = 4.0

// shelterRest says why a Shelter frame with nothing to ask for is resting, or "". It is a rest
// only in the SLEEP routine (the routine ending ends it), at the shelter site, standing on dry
// ground with no walk under way, with nothing held to build with and no light waiting to go up.
// Materials that do not get used, a light that will not stay and a site not reached are faults.
func (lw *fsmWorld) shelterRest(l shelterLocal) string {
	ctx := lw.ctx
	if lw.fsm.currentRoutine != RoutineSleep || ctx == nil || ctx.Frame == nil || l.Pos == nil {
		return ""
	}
	s := ctx.Frame.Status
	pos := s.GetPosition()
	if pos == nil || s.GetIsInWater() || s.GetNavigationEnclosed() ||
		s.GetNavigationState() == civquestv1.NavigationState_NAVIGATION_STATE_PATHING ||
		s.GetNavigationState() == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
		return ""
	}
	if math.Hypot(pos.X-float64(l.Pos.X)-0.5, pos.Z-float64(l.Pos.Z)-0.5) > shelterRestReach ||
		math.Abs(pos.Y-float64(l.Pos.Y)) > shelterRestReach {
		return ""
	}
	inv := inspectBootstrapInventory(s.Inventory)
	if !l.complete() && (inv.logCount > 0 || inv.buildingBlockCount > 0) {
		return ""
	}
	if inv.hasLight && !lightNearby(ctx.Frame, pos) {
		return ""
	}
	return "SLEEP routine at the shelter site with nothing left to build or light"
}

// Construction progress (blueprint quadrant block index) is a value-type Local owned by whoever
// drives ConstructionSkill; the skill itself holds no per-agent state.
type ConstructionLocal struct{ Index int }

// homeBound: it is night, there is no bed to go to, and the cabin is elsewhere and reachable.
func (lw *fsmWorld) homeBound() bool {
	ctx := lw.ctx
	return ctx != nil && ctx.State != nil && ctx.Frame != nil && ctx.Frame.Status != nil &&
		!skillHasAssignedBed(lw.skillCtx, ctx.Facts) &&
		(skillIsDuskOrNight(lw.skillCtx, ctx.Facts) || lw.fsm.currentRoutine == RoutineSleep) &&
		ctx.State.awayFromCabin(ctx.Frame.Status.Position, ctx.Frame.WorldTick)
}

// walkHome is the walk to a finished cabin, sent at most every homeWalkEvery ticks while the
// agent is home bound; nil otherwise.
func (lw *fsmWorld) walkHome(l *shelterLocal) []*civquestv1.AgentIntent {
	if !lw.homeBound() {
		return nil
	}
	tick := lw.Tick()
	if l.WalkTick != 0 && tick >= l.WalkTick && tick-l.WalkTick < homeWalkEvery {
		return []*civquestv1.AgentIntent{} // under way
	}
	at := lw.ctx.State.GetShelterPos()
	if at == nil {
		return nil
	}
	l.WalkTick = tick
	return []*civquestv1.AgentIntent{{AgentId: lw.agentID(), Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target:          &civquestv1.Vec3{X: float64(at.X) + 0.5, Y: float64(at.Y), Z: float64(at.Z) + 0.5},
		SpeedMultiplier: 1.0, ArrivalTolerance: 2,
	}}}}
}

const homeWalkEvery = int64(40)
