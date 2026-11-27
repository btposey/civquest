package system1

// Expedition, Survey and Navigate as frames (ADR-036, bead cq-2sr.7).
//
// An expedition is the ADR-032 chained "survey -> choose bearing -> traverse -> rendezvous /
// solo homestead" behaviour. Its progress is the typed value-type Local of the expedition frame
// (target, hops, bearing, blocked bearings, returning); which child frame is on top IS the old
// phase enum:
//
//	Survey   pushed by Expedition, pops with SurveyReport (or Timeout via its lease)
//	Navigate pushed by Expedition, pops with navReached or navBlocked{bearing}
//	flank    = Expedition OpReplace with an extended Blocked list (FlankCount == len(Blocked))
//
// Durable knowledge (LatestSurvey, HomesteadPos, RendezvousOrigin, ClanSize, CollectiveSite*)
// stays in AgentState (the store). Steps read it through an immutable expeditionKnowledge
// snapshot and write it only through Step.Store effects on exit, which the adapter applies
// (applyStoreWrites). The rendezvous report that used to be a PendingRendezvousSync mailbox is
// a RendezvousReport event drained from the FSM.

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/spatialmem"
)

const (
	activityKindExpedition = "expedition"
	activityKindSurvey     = "survey"
	activityKindNavigate   = "navigate"

	// expeditionPriority is the legacy arbitration priority of an expedition (the old
	// survey_explorer spec): self-push preemptors must out-rank it.
	expeditionPriority = 0.65

	maxBlockedBearings = 12
	// expeditionFlankBudget is len(Blocked) at which flanking gives up and retreats: the rim
	// hit that triggered the re-survey plus three flanks (the old FlankCount >= 3 rule).
	expeditionFlankBudget = 4
)

// ---- results (frame -> parent) ----

// SurveyReport is the Result of a completed survey frame.
type SurveyReport struct {
	Report *civquestv1.HorizonSurveyReport
	Tick   int64
}

type surveyAborted struct{}
type navReached struct{ Pos [3]float64 }
type navBlocked struct {
	Bearing float32
	Pos     [3]float64
}

// expeditionIneligible is the pop Result of a child whose expedition may no longer run.
type expeditionIneligible struct{}

// expeditionDone is the pop Result of an expedition that finished (homestead, rendezvous, ...).
type expeditionDone struct{}

// ---- store writes (applied by the adapter) ----

type homesteadClaim struct{ Pos [3]float64 }
type hazardMark struct{ POI db.AgentPOI }

// scoutHopReached ends the System 2 scout-frontier directive that started the expedition.
type scoutHopReached struct{}

// RendezvousReport is the event emitted when an expedition returns to the clan rendezvous
// origin; the runtime drains it (AgentFSM.DrainRendezvousReports) to sync with the clan.
type RendezvousReport struct{ Summary string }

// ---- locals ----

type expeditionArgs struct {
	// Origin is the activity kind that started this expedition (its eligibility rules apply
	// and it is never re-pushed over the expedition).
	Origin string
	// Seed continues an expedition across an OpReplace (flank, directive hand-off).
	Seed *expeditionLocal
}

type expeditionLocal struct {
	Origin string
	// Leg is true while there is an active destination (Goal) the expedition is travelling to.
	Leg bool
	// Goal is the leg destination (outbound target, or Home while Returning); Target is the
	// current waypoint (== Goal unless flanking an obstruction).
	Goal      [3]float64
	Target    [3]float64
	Bearing   float32
	Hops      int
	Blocked   []float32 // immutable; extend by copy
	Returning bool
	Home      [3]float64
	// Forage marks a resource trip started from a settled agent: it scouts out and comes back
	// instead of ending (homestead) or converging on the clan site at once (cq-6l3.3).
	Forage bool

	LastOrigin    [3]float64 // origin of the newest survey this expedition has seen
	HasLastOrigin bool
	PrevOrigin    [3]float64 // origin of the survey before it (avoid doubling back)
	HasPrevOrigin bool

	EmitTick   int64 // duplicate-survey guard
	EmitOrigin [3]float64
	HasEmit    bool

	// SurveyTimeouts counts consecutive surveys that ended on their lease without a report
	// (the sweep never completed); a report resets it. At expeditionMaxSurveyTimeouts the
	// expedition gives up and backs off (cq-aik.31: the survey wait is bounded).
	SurveyTimeouts int
}

// FlankCount is len(Blocked) (ADR-036: FlankCount == len(Blocked)).
func (l expeditionLocal) FlankCount() int { return len(l.Blocked) }

func (l expeditionLocal) flanking() bool { return l.Leg && l.Target != l.Goal }

type surveyArgs struct {
	Origin    string
	Returning bool
}
type surveyLocal struct {
	surveyArgs
	StartTick int64
	BaseTick  int64 // LastSurveyTick when the survey began
	LastStep  int64 // tick of the last step that pushed a preemptor over this frame
}

type navigateArgs struct {
	Origin    string
	Returning bool
	Target    [3]float64
	Bearing   float32
	Radius    float64
}
type navigateLocal struct {
	navigateArgs
	Attempts int
	// YieldRetries counts congestion give-ups already retried on this leg.
	YieldRetries int
	LastEmit     int64
	LastStep     int64 // tick of the last step that pushed a preemptor over this frame
}

// ---- knowledge snapshot ----

// expeditionKnowledge is an immutable snapshot of the durable store an expedition reads.
type expeditionKnowledge struct {
	Survey       *civquestv1.HorizonSurveyReport
	SurveyTick   int64
	SurveyOrigin [3]float64
	Home         [3]float64
	ClanSize     int
	Homesteaded  bool
	Site         *[3]float64
	Hazards      []*db.AgentPOI
	Spatial      spatialmem.Snapshot // the agent's spatial memory (ADR-039), read-only
}

func (s *AgentState) expeditionKnowledge(pos *civquestv1.Vec3) expeditionKnowledge {
	var k expeditionKnowledge
	if s == nil {
		k.ClanSize = 1
		return k
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.homeOrigin == nil && pos != nil {
		originCopy := &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		s.homeOrigin = originCopy
		s.currentOutpost = originCopy
		if len(s.outposts) == 0 {
			s.outposts = append(s.outposts, originCopy)
		}
	}
	k.Survey, k.SurveyTick, k.SurveyOrigin = s.LatestSurvey, s.LastSurveyTick, s.LastSurveyOrigin
	k.ClanSize = s.ClanSize
	if k.ClanSize < 1 {
		k.ClanSize = 1
	}
	k.Homesteaded = s.HomesteadClaimed
	if s.CollectiveSiteChosen {
		site := s.CollectiveSitePos
		k.Site = &site
	}
	switch {
	case s.RendezvousOrigin != nil:
		k.Home = *s.RendezvousOrigin
	case s.homeOrigin != nil:
		k.Home = [3]float64{s.homeOrigin.X, s.homeOrigin.Y, s.homeOrigin.Z}
	case pos != nil:
		k.Home = [3]float64{pos.X, pos.Y, pos.Z}
	}
	if si := s.SpatialIndexLocked(); si != nil && pos != nil {
		k.Hazards = si.FindPOIsInRadius(pos, POICategoryHazard, 64.0)
	}
	return k
}

// settledSiteRadius: within it of the clan site, an agent has converged.
const settledSiteRadius = 12.0

// settledAt reports whether settlement scouting has nothing left to do at pos: the homestead is
// claimed, or the clan site is chosen and reached.
func (k expeditionKnowledge) settledAt(pos *civquestv1.Vec3) bool {
	if k.Homesteaded {
		return true
	}
	return pos != nil && k.Site != nil && k.ClanSize > 1 && math.Hypot(pos.X-k.Site[0], pos.Z-k.Site[2]) <= settledSiteRadius
}

// settledTripIdleTicks: how long an equipped settled agent stands idle before a bootstrap walk
// may send it out for resources. It is just past the daytime-idle contemplation threshold, so
// System 2 gets to give it something to do first. An agent still without its survival kit does
// not wait at all (cq-gey.6): the camp may have nothing to make one from.
const settledTripIdleTicks = 400

// idleSince reports whether the agent has been idle for at least ticks.
func (s *AgentState) idleSince(tick, ticks int64) bool {
	start, ok, _, _, _ := s.IdleState()
	return ok && start > 0 && tick-start >= ticks
}

// settledAt is the read-only form of expeditionKnowledge.settledAt for eligibility checks:
// reading expeditionKnowledge also records the home origin.
func (s *AgentState) settledAt(pos *civquestv1.Vec3) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := expeditionKnowledge{ClanSize: s.ClanSize, Homesteaded: s.HomesteadClaimed}
	if s.CollectiveSiteChosen {
		site := s.CollectiveSitePos
		k.Site = &site
	}
	return k.settledAt(pos)
}

func (w *fsmWorld) knowledge() expeditionKnowledge {
	var pos *civquestv1.Vec3
	if f := w.Frame(); f != nil && f.Status != nil {
		pos = f.Status.Position
	}
	if w.ctx == nil {
		return expeditionKnowledge{ClanSize: 1}
	}
	k := w.ctx.State.expeditionKnowledge(pos)
	k.Spatial = w.spatial()
	return k
}

func (w *fsmWorld) agentID() string {
	if w.ctx != nil && w.ctx.AgentID != "" {
		return w.ctx.AgentID
	}
	if f := w.Frame(); f != nil && f.Status != nil && f.Status.AgentId != "" {
		return f.Status.AgentId
	}
	if w.ctx != nil && w.ctx.Config != nil {
		return w.ctx.Config.Name
	}
	return ""
}

func (w *fsmWorld) pos() *civquestv1.Vec3 {
	if f := w.Frame(); f != nil && f.Status != nil {
		return f.Status.Position
	}
	return nil
}

// ---- eligibility and preemption ----

// equipped reports that the agent holds its survival kit.
func (w *fsmWorld) equipped() bool {
	f := w.Frame()
	return f != nil && f.Status != nil && HasSurvivalKit(f.Status.Inventory)
}

// surveyBackoff is the Memory write of an expedition that gave up after repeated survey
// timeouts: no expedition starts until Until.
type surveyBackoff struct {
	Until    int64
	Timeouts int
}

const (
	// expeditionMaxSurveyTimeouts consecutive report-less survey leases end an expedition.
	expeditionMaxSurveyTimeouts = 2
	// expeditionSurveyBackoffTicks is how long expeditions stay suppressed afterwards (30 s).
	expeditionSurveyBackoffTicks int64 = 600
)

// surveyBackedOff reports whether expeditions are suppressed after repeated survey timeouts.
func (w *fsmWorld) surveyBackedOff() bool {
	return w.fsm != nil && w.fsm.surveyBackoffUntil > 0 && w.Tick() < w.fsm.surveyBackoffUntil
}

// SurveyBackoffUntil is the tick until which expeditions are suppressed because surveys kept
// timing out without a report (0: not backing off). It makes the give-up observable.
func (fsm *AgentFSM) SurveyBackoffUntil() int64 {
	if fsm == nil {
		return 0
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.surveyBackoffUntil
}

// expeditionEligible reports whether an expedition started by origin may keep running.
func (w *fsmWorld) expeditionEligible(origin string) bool {
	if w.surveyBackedOff() {
		return false
	}
	ctx := w.ctx
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	f := ctx.Frame
	if len(f.IncomingChats) > 0 || f.PendingTrade != nil || f.Status.IsSleeping {
		return false
	}
	stage, routine := w.fsm.currentStage, w.fsm.currentRoutine
	switch origin {
	case activityKindStrategicDirective:
		return isStrategicDirectiveEligible(stage, routine, ctx)
	case activityKindBootstrap:
		return isBootstrapActivityEligible(stage, routine, ctx)
	default:
		return isExpeditionExplorerEligible(stage, routine, ctx)
	}
}

// isExpeditionExplorerEligible gates an expedition started by the exploration activity.
func isExpeditionExplorerEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if stage == StageBootstrap {
		return false
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		return HasSurvivalKit(ctx.Frame.Status.Inventory)
	}
	if routine == RoutineSleep || routine == RoutineArmoryMaintenance {
		return false
	}
	return routine == RoutinePrimaryDuty
}

// bootstrapHarvestAvailable mirrors the bootstrap waterfall: while timber is needed and a
// trunk is within reach, harvesting wins and a bootstrap-started expedition ends.
func bootstrapHarvestAvailable(ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Frame.Status.Position == nil {
		return false
	}
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	if inv.hasAxe && inv.hasSword && inv.logCount >= 6 {
		return false
	}
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()
	trunk, _ := findNearestTreeTrunk(ctx.AgentID, ctx.Frame, ctx.Frame.Status.Position, ctx.State)
	return trunk != nil
}

// ineligiblePop ends a frame whose expedition may no longer run: the exposed parent re-runs.
func (w *fsmWorld) ineligiblePop(child bool) htpda.Step {
	if child {
		return htpda.Step{Op: htpda.OpPop, Result: expeditionIneligible{}}
	}
	w.skipped[activityKindExpedition] = true
	w.refill = true
	return htpda.Step{Op: htpda.OpPop, Result: legacyResult{Halt: false}}
}

// ---- Expedition ----

type expeditionActivity struct{}

var _ htpda.Activity = expeditionActivity{}

func (expeditionActivity) Kind() htpda.Kind   { return activityKindExpedition }
func (expeditionActivity) Scope() htpda.Scope { return htpda.ScopeMotor }

// Propose abstains: the goal frame launches expeditions, a SCOUT_FRONTIER directive's included.
func (expeditionActivity) Propose(htpda.World) htpda.Bid { return htpda.Bid{} }

func (expeditionActivity) Init(w htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(expeditionArgs)
	if a.Seed != nil {
		return *a.Seed, htpda.Lease{}
	}
	loc := expeditionLocal{Origin: a.Origin}
	if lw := asFSMWorld(w); lw != nil {
		k := lw.knowledge()
		loc.Home = k.Home
		if pos := lw.pos(); a.Origin == activityKindBootstrap && k.settledAt(pos) {
			loc.Forage, loc.Home = true, [3]float64{pos.X, pos.Y, pos.Z}
		}
		if k.SurveyTick > 0 {
			loc.LastOrigin, loc.HasLastOrigin = k.SurveyOrigin, true
		}
	}
	return loc, htpda.Lease{}
}

func (a expeditionActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	loc := htpda.LocalAs[expeditionLocal](local)
	if lw == nil || lw.ctx == nil || lw.ctx.State == nil {
		return htpda.Step{Op: htpda.OpPop}
	}
	switch e := ev.(type) {
	case htpda.Cancel, htpda.Expired:
		return htpda.Step{Op: htpda.OpPop}
	case htpda.Started:
		if !lw.expeditionEligible(loc.Origin) {
			return lw.ineligiblePop(false)
		}
		if loc.Leg {
			return a.pushNavigate(lw, loc)
		}
		return a.decide(lw, loc)
	case htpda.ChildDone:
		return a.onChild(lw, loc, e.Result)
	case htpda.Resumed, htpda.Tick:
		if !lw.expeditionEligible(loc.Origin) {
			return lw.ineligiblePop(false)
		}
		if !loc.Leg {
			return a.decide(lw, loc)
		}
		return a.pushNavigate(lw, loc)
	}
	return htpda.Step{Op: htpda.OpStay, Next: loc}
}

func (a expeditionActivity) onChild(lw *fsmWorld, loc expeditionLocal, result any) htpda.Step {
	switch r := result.(type) {
	case expeditionIneligible:
		return lw.ineligiblePop(false)
	case htpda.Rejected:
		lw.skipped[activityKindExpedition] = true
		return htpda.Step{Op: htpda.OpPop, Result: r}
	case legacyResult:
		if r.Halt {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
	}
	if !lw.expeditionEligible(loc.Origin) {
		return lw.ineligiblePop(false)
	}
	pos := lw.pos()
	if pos == nil {
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	}
	switch r := result.(type) {
	case htpda.Timeout:
		if r.Kind == activityKindSurvey {
			loc.SurveyTimeouts++
			if loc.SurveyTimeouts >= expeditionMaxSurveyTimeouts {
				// No report after repeated sweeps: stop holding the motor for surveys that
				// never complete. The expedition ends and its origin (or the activities
				// below) carry on; surveying resumes after the backoff.
				st := a.done(lw)
				st.Memory = []htpda.MemoryWrite{surveyBackoff{Until: lw.Tick() + expeditionSurveyBackoffTicks, Timeouts: loc.SurveyTimeouts}}
				return st
			}
		}
		loc.Leg = false
		return a.decide(lw, loc)
	case SurveyReport:
		loc.SurveyTimeouts = 0
		if loc.HasLastOrigin {
			loc.PrevOrigin, loc.HasPrevOrigin = loc.LastOrigin, true
		}
		if r.Report != nil {
			loc.LastOrigin, loc.HasLastOrigin = [3]float64{r.Report.OriginX, r.Report.OriginY, r.Report.OriginZ}, true
		}
		return a.decide(lw, loc)
	case navReached:
		return a.onArrived(lw, loc, pos, r)
	case navBlocked:
		return a.onBarrier(lw, loc, pos, r)
	}
	// surveyAborted, htpda.Timeout and anything else: re-decide from the current state.
	loc.Leg = false
	return a.decide(lw, loc)
}

func (a expeditionActivity) done(lw *fsmWorld, store ...htpda.StoreWrite) htpda.Step {
	lw.skipped[activityKindExpedition] = true
	return htpda.Step{Op: htpda.OpPop, Result: expeditionDone{}, Store: store}
}

// decide chooses the next leg when none is active: claim/converge, survey, or traverse.
func (a expeditionActivity) decide(lw *fsmWorld, loc expeditionLocal) htpda.Step {
	f := lw.Frame()
	pos := lw.pos()
	if f == nil || pos == nil {
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	}
	k := lw.knowledge()
	loc.Home = firstHome(loc.Home, k.Home)

	// Solo explorers who claimed a homestead stay settled there.
	if k.Homesteaded && !loc.Forage {
		return a.done(lw)
	}

	if loc.Returning {
		loc.Goal, loc.Target = loc.Home, loc.Home
		loc.Bearing = vectorToBearingDeg(loc.Home[0]-pos.X, loc.Home[2]-pos.Z)
		loc.Leg = true
		return a.pushNavigate(lw, loc)
	}

	// If a collective clan settlement site was chosen, converge toward it when distant.
	if k.Site != nil && k.ClanSize > 1 && !loc.Forage {
		if !k.settledAt(pos) {
			loc.Goal, loc.Target, loc.Leg = *k.Site, *k.Site, true
			loc.Bearing = vectorToBearingDeg(k.Site[0]-pos.X, k.Site[2]-pos.Z)
			return a.pushNavigate(lw, loc)
		}
		return a.done(lw)
	}

	distFromSurvey := math.Hypot(pos.X-k.SurveyOrigin[0], pos.Z-k.SurveyOrigin[2])
	surveyExpired := k.Survey == nil ||
		distFromSurvey > expeditionResurveyDist ||
		(f.WorldTick >= k.SurveyTick && f.WorldTick-k.SurveyTick > expeditionResurveyMaxAge)
	if surveyExpired {
		return a.pushSurvey(lw, loc, k, pos)
	}

	bearing, target := chooseBestSurveyBearing(lw.agentID(), f, pos, k, loc)
	loc.Bearing, loc.Target, loc.Goal, loc.Leg = bearing, target, target, true
	return a.pushNavigate(lw, loc)
}

func firstHome(cur, fallback [3]float64) [3]float64 {
	if cur != ([3]float64{}) {
		return cur
	}
	return fallback
}

// pushSurvey pushes a Survey unless the duplicate-survey storm guard suppresses it.
func (a expeditionActivity) pushSurvey(lw *fsmWorld, loc expeditionLocal, k expeditionKnowledge, pos *civquestv1.Vec3) htpda.Step {
	tick := lw.Tick()
	// Never emit another survey within 80 ticks of the last at the same <6m spot unless the
	// previous completed or genuinely timed out.
	if loc.EmitTick > 0 && tick >= loc.EmitTick && tick-loc.EmitTick < 80 && loc.HasEmit &&
		math.Hypot(pos.X-loc.EmitOrigin[0], pos.Z-loc.EmitOrigin[2]) < expeditionRimResurveyDist {
		prevCompleted := k.SurveyTick > 0 && k.SurveyTick >= loc.EmitTick
		prevTimedOut := tick-loc.EmitTick >= expeditionSurveyTimeout
		if !prevCompleted && !prevTimedOut {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
	}
	loc.EmitTick, loc.EmitOrigin, loc.HasEmit = tick, [3]float64{pos.X, pos.Y, pos.Z}, true
	return htpda.Step{Op: htpda.OpPush, Child: activityKindSurvey, Args: surveyArgs{Origin: loc.Origin, Returning: loc.Returning}, Next: loc}
}

func (a expeditionActivity) pushNavigate(lw *fsmWorld, loc expeditionLocal) htpda.Step {
	radius := expeditionArrivalRadius
	if loc.Returning && !loc.flanking() {
		radius = expeditionRendezvousRadius
	}
	return htpda.Step{Op: htpda.OpPush, Child: activityKindNavigate, Next: loc,
		Args: navigateArgs{Origin: loc.Origin, Returning: loc.Returning, Target: loc.Target, Bearing: loc.Bearing, Radius: radius}}
}

func (a expeditionActivity) onArrived(lw *fsmWorld, loc expeditionLocal, pos *civquestv1.Vec3, r navReached) htpda.Step {
	f := lw.Frame()
	k := lw.knowledge()

	if loc.Returning && !loc.flanking() {
		// Back at the clan rendezvous origin: report and end the expedition.
		summary := buildRendezvousReportSummary(loc, k, pos)
		return a.done(lw, RendezvousReport{Summary: summary})
	}
	if loc.flanking() {
		// Flank waypoint reached: resume toward the leg destination.
		loc.Target = loc.Goal
		loc.Bearing = vectorToBearingDeg(loc.Goal[0]-pos.X, loc.Goal[2]-pos.Z)
		return a.pushNavigate(lw, loc)
	}

	// Arrived at the leg destination.
	loc.Hops++
	loc.Blocked = nil
	loc.Leg = false

	if k.ClanSize <= 1 && !loc.Forage {
		// Solo explorer: NEVER returns to the rendezvous origin; claims a homestead directly
		// after chaining at least 2 outbound hops (or surveying locally at the candidate site).
		distFromSurvey := math.Hypot(pos.X-k.SurveyOrigin[0], pos.Z-k.SurveyOrigin[2])
		if (loc.Hops >= 2 || distFromSurvey <= expeditionResurveyDist) && meetsHomesteadSuitability(k.Survey, lw.ctx.State.siteSightings(pos)) {
			return a.done(lw, homesteadClaim{Pos: [3]float64{pos.X, pos.Y, pos.Z}})
		}
	} else if loc.Hops >= 2 || isPrimeSettlementSite(k.Survey) || (f != nil && f.Status != nil && f.Status.FoodLevel <= 8) {
		loc.Returning = true
		loc.Goal, loc.Target, loc.Leg = loc.Home, loc.Home, true
		loc.Bearing = vectorToBearingDeg(loc.Home[0]-pos.X, loc.Home[2]-pos.Z)
	}

	if loc.Origin == activityKindStrategicDirective {
		// A scout-frontier directive is satisfied by one hop: end it and let the expedition
		// continue on its own (an in-flight return leg survives the directive).
		next := loc
		next.Origin = activityKindExploration
		return htpda.Step{Op: htpda.OpReplace, Child: activityKindExpedition,
			Args: expeditionArgs{Origin: next.Origin, Seed: &next}, Store: []htpda.StoreWrite{scoutHopReached{}}}
	}
	if loc.Leg {
		return a.pushNavigate(lw, loc)
	}
	return a.decide(lw, loc)
}

// onBarrier implements the rim re-survey ravine protocol: record the blocked bearing, sweep a
// fresh survey unless one was taken within 6m of this rim, otherwise flank (Replace with an
// extended Blocked list).
func (a expeditionActivity) onBarrier(lw *fsmWorld, loc expeditionLocal, pos *civquestv1.Vec3, r navBlocked) htpda.Step {
	k := lw.knowledge()

	blocked := make([]float32, 0, len(loc.Blocked)+1)
	blocked = append(blocked, loc.Blocked...)
	blocked = append(blocked, normalizeBearingDeg(r.Bearing))
	if len(blocked) > maxBlockedBearings {
		blocked = blocked[len(blocked)-maxBlockedBearings:]
	}
	loc.Blocked = blocked

	distFromLastSurvey := math.Hypot(pos.X-k.SurveyOrigin[0], pos.Z-k.SurveyOrigin[2])
	if k.SurveyTick == 0 || distFromLastSurvey >= expeditionRimResurveyDist {
		// Pause at the rim and sweep a fresh 360 degrees; the navigate leg is re-decided after.
		loc.Leg = false
		return a.pushSurvey(lw, loc, k, pos)
	}

	allowReverse := false
	stepBearing := loc.Bearing
	if len(loc.Blocked) >= expeditionFlankBudget {
		// Flank budget spent at an already-surveyed spot: back out toward an unblocked or
		// reverse bearing instead of emitting a duplicate same-spot survey.
		allowReverse = true
		loc.Blocked = loc.Blocked[len(loc.Blocked)-1:]
	}
	flank := pickBestFlankBearing(stepBearing, pos, loc, k, allowReverse)
	dx, dz := bearingToUnitVector(flank)
	loc.Bearing = flank
	loc.Target = [3]float64{
		math.Round((pos.X+dx*expeditionFlankStepDist)*10) / 10,
		pos.Y,
		math.Round((pos.Z+dz*expeditionFlankStepDist)*10) / 10,
	}
	loc.Leg = true
	if loc.Goal == ([3]float64{}) {
		loc.Goal = loc.Target
	}
	return htpda.Step{Op: htpda.OpReplace, Child: activityKindExpedition,
		Args: expeditionArgs{Origin: loc.Origin, Seed: &loc}}
}

// ---- Survey ----

type surveyActivity struct{}

var _ htpda.Activity = surveyActivity{}

func (surveyActivity) Kind() htpda.Kind              { return activityKindSurvey }
func (surveyActivity) Scope() htpda.Scope            { return htpda.ScopeMotor }
func (surveyActivity) Propose(htpda.World) htpda.Bid { return htpda.Bid{} }
func (surveyActivity) surveyIntent(id string) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{
		AgentId: id,
		Action: &civquestv1.AgentIntent_SurveyHorizon{
			SurveyHorizon: &civquestv1.SurveyHorizonIntent{
				NumSectors:     defaultSurveyNumSectors,
				MaxRange:       defaultSurveyMaxRange,
				TicksPerSector: defaultSurveyTicksPerSec,
			},
		},
	}
}

func (surveyActivity) Init(w htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(surveyArgs)
	loc := surveyLocal{surveyArgs: a, StartTick: w.Tick()}
	if lw := asFSMWorld(w); lw != nil {
		loc.BaseTick = lw.knowledge().SurveyTick
	}
	return loc, htpda.Lease{StartTick: w.Tick(), DeadlineTick: w.Tick() + expeditionSurveyTimeout}
}

func (a surveyActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	loc := htpda.LocalAs[surveyLocal](local)
	if lw == nil || lw.ctx == nil || lw.ctx.State == nil {
		return htpda.Step{Op: htpda.OpPop, Result: surveyAborted{}}
	}
	switch e := ev.(type) {
	case htpda.Cancel:
		return htpda.Step{Op: htpda.OpPop, Result: surveyAborted{}}
	case htpda.Expired:
		return htpda.Step{Op: htpda.OpPop}
	case htpda.Started:
		return htpda.Step{Op: htpda.OpStay, Next: loc, Intents: []*civquestv1.AgentIntent{a.surveyIntent(lw.agentID())}}
	case htpda.ChildDone, htpda.Resumed:
		if cd, ok := e.(htpda.ChildDone); ok && halted(cd.Result) {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		if lw.tick != loc.LastStep {
			// A preemptor (foraging, shelter, emergency) held the motor across ticks and
			// interrupted the sweep: abandon it and re-decide.
			return htpda.Step{Op: htpda.OpPop, Result: surveyAborted{}}
		}
		// The preemptor fell through within the same tick: the sweep carries on.
		return a.Step(w, loc, htpda.Tick{})
	case htpda.Tick:
		if !lw.expeditionEligible(loc.Origin) {
			return lw.ineligiblePop(true)
		}
		k := lw.knowledge()
		if k.SurveyTick > loc.BaseTick && k.SurveyTick >= loc.StartTick {
			return htpda.Step{Op: htpda.OpPop, Result: SurveyReport{Report: k.Survey, Tick: k.SurveyTick}}
		}
		loc.LastStep = lw.tick
	}
	return htpda.Step{Op: htpda.OpStay, Next: loc}
}

// ---- Navigate ----

type navigateActivity struct{}

var _ htpda.Activity = navigateActivity{}

func (navigateActivity) Kind() htpda.Kind              { return activityKindNavigate }
func (navigateActivity) Scope() htpda.Scope            { return htpda.ScopeMotor }
func (navigateActivity) Propose(htpda.World) htpda.Bid { return htpda.Bid{} }

func (navigateActivity) Init(_ htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(navigateArgs)
	return navigateLocal{navigateArgs: a}, htpda.Lease{}
}

func (a navigateActivity) emit(lw *fsmWorld, loc navigateLocal) htpda.Step {
	loc.Attempts++
	loc.LastEmit = lw.Tick()
	return htpda.Step{Op: htpda.OpStay, Next: loc, Intents: []*civquestv1.AgentIntent{{
		AgentId: lw.agentID(),
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:           &civquestv1.Vec3{X: loc.Target[0], Y: loc.Target[1], Z: loc.Target[2]},
			SpeedMultiplier:  1.0,
			ArrivalTolerance: 2,
		}},
	}}}
}

func (a navigateActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	loc := htpda.LocalAs[navigateLocal](local)
	if lw == nil || lw.ctx == nil || lw.ctx.State == nil {
		return htpda.Step{Op: htpda.OpPop, Result: surveyAborted{}}
	}
	switch e := ev.(type) {
	case htpda.Cancel, htpda.Expired:
		return htpda.Step{Op: htpda.OpPop}
	case htpda.Started:
		return a.emit(lw, loc)
	case htpda.Receipt:
		// This frame owns its Navigate's recovery; a flank (not a long recovery) is the
		// expedition's answer to a barrier, so the Recover budget is one attempt.
		if args, ok := lw.recoverArgsFor(e, recoverTunables.ExpeditionAttempts); ok {
			return htpda.Step{Op: htpda.OpPush, Child: activityKindRecover, Args: args, Next: loc}
		}
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	case htpda.ChildDone, htpda.Resumed:
		if cd, ok := e.(htpda.ChildDone); ok {
			if halted(cd.Result) {
				return htpda.Step{Op: htpda.OpStay, Next: loc}
			}
			switch r := cd.Result.(type) {
			case recovered:
				loc.Attempts = 0
				return a.emit(lw, loc)
			case recoverBlocked:
				if r.Cause == CauseCongestion && loc.YieldRetries < recoverTunables.CongestionRetries {
					// A peer is in the way: retry later, the target is not invalid.
					loc.YieldRetries++
					loc.Attempts = 0
					return a.emit(lw, loc)
				}
				return htpda.Step{
					Op:     htpda.OpPop,
					Result: navBlocked{Bearing: loc.Bearing, Pos: r.Pos},
					Store:  []htpda.StoreWrite{barrierHazard(loc.Bearing, &civquestv1.Vec3{X: r.Pos[0], Y: r.Pos[1], Z: r.Pos[2]}, lw.tick)},
				}
			}
		}
		if lw.tick == loc.LastStep {
			// The preemptor fell through within the same tick: the leg carries on.
			return a.Step(w, loc, htpda.Tick{})
		}
		// A preemptor held the motor across ticks: re-issue the navigation.
		loc.Attempts = 0
		return a.emit(lw, loc)
	case htpda.Tick:
		if !lw.expeditionEligible(loc.Origin) {
			return lw.ineligiblePop(true)
		}
		f := lw.Frame()
		pos := lw.pos()
		if f == nil || f.Status == nil || pos == nil {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		loc.LastStep = lw.tick
		dx, dz := pos.X-loc.Target[0], pos.Z-loc.Target[2]
		if dx*dx+dz*dz <= loc.Radius*loc.Radius {
			return htpda.Step{Op: htpda.OpPop, Result: navReached{Pos: [3]float64{pos.X, pos.Y, pos.Z}}}
		}
		if loc.Attempts > 0 && lw.ctx.State.IsTargetUnreachable(loc.Target[0], loc.Target[1], loc.Target[2], f.WorldTick) {
			// The walking policy gave this leg up: treat it as a barrier and go around.
			return htpda.Step{Op: htpda.OpPop, Result: navBlocked{Bearing: loc.Bearing, Pos: [3]float64{pos.X, pos.Y, pos.Z}}}
		}
		navState := f.Status.NavigationState
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && loc.Attempts > 0 {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		if loc.Attempts > 0 && loc.LastEmit != 0 && f.WorldTick >= loc.LastEmit && f.WorldTick-loc.LastEmit < expeditionNavCooldownTicks {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		return a.emit(lw, loc)
	}
	return htpda.Step{Op: htpda.OpStay, Next: loc}
}

// barrierHazard marks the blocked forward edge as a ravine-barrier hazard POI.
func barrierHazard(bearing float32, pos *civquestv1.Vec3, tick int64) hazardMark {
	dx, dz := bearingToUnitVector(bearing)
	hx, hy, hz := pos.X+dx*3.0, pos.Y, pos.Z+dz*3.0
	return hazardMark{POI: db.AgentPOI{
		POIID:            FormatPOIID(POICategoryHazard, int32(math.Floor(hx)), int32(math.Floor(hy)), int32(math.Floor(hz))),
		Category:         POICategoryHazard,
		BlockID:          "civquest:ravine_barrier",
		X:                hx,
		Y:                hy,
		Z:                hz,
		AccessX:          pos.X,
		AccessY:          pos.Y,
		AccessZ:          pos.Z,
		ElevationLayer:   ClassifyElevationLayer(hy, pos.Y),
		SafetyScore:      0.1,
		DiscoveredTick:   tick,
		LastVerifiedTick: tick,
		Reliability:      1.0,
		IsActive:         true,
	}}
}

// ---- pure decision helpers (ported from the old waterfall evaluator) ----

func pickBestFlankBearing(currentBearing float32, pos *civquestv1.Vec3, loc expeditionLocal, k expeditionKnowledge, allowReverse bool) float32 {
	candidates := []float32{
		normalizeBearingDeg(currentBearing - 90.0),
		normalizeBearingDeg(currentBearing + 90.0),
	}
	if allowReverse {
		candidates = append(candidates,
			normalizeBearingDeg(currentBearing-135.0),
			normalizeBearingDeg(currentBearing+135.0),
			normalizeBearingDeg(currentBearing+180.0),
		)
	}
	var hazards []*db.AgentPOI
	for _, h := range k.Hazards {
		if h != nil && math.Hypot(h.X-pos.X, h.Z-pos.Z) <= 32.0 {
			hazards = append(hazards, h)
		}
	}
	scoreSide := func(b float32) float64 {
		s := 0.0
		for _, blocked := range loc.Blocked {
			if angularDiffDeg(b, blocked) <= 40.0 {
				s -= 100.0
			}
		}
		for _, haz := range hazards {
			hdx, hdz := haz.X-pos.X, haz.Z-pos.Z
			if math.Hypot(hdx, hdz) <= 0.1 {
				continue
			}
			if angularDiffDeg(b, vectorToBearingDeg(hdx, hdz)) <= 40.0 {
				s -= 80.0
			}
		}
		if k.Survey != nil {
			var bestSec *civquestv1.HorizonSector
			bestAngle := 999.0
			for _, sec := range k.Survey.Sectors {
				if sec == nil {
					continue
				}
				if ang := angularDiffDeg(b, sec.BearingDeg); ang < bestAngle {
					bestAngle, bestSec = ang, sec
				}
			}
			if bestSec != nil {
				s += float64(bestSec.HitDistance)
				if bestSec.Occluded {
					s -= 35.0
				}
				if bestSec.OpenHorizon {
					s += 15.0
				}
			}
		}
		return s
	}
	best, bestScore := candidates[0], scoreSide(candidates[0])
	for _, cand := range candidates[1:] {
		if sc := scoreSide(cand); sc > bestScore {
			best, bestScore = cand, sc
		}
	}
	return best
}

func chooseBestSurveyBearing(agentID string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, k expeditionKnowledge, loc expeditionLocal) (float32, [3]float64) {
	report := k.Survey
	if report == nil || len(report.Sectors) == 0 {
		wedge := float32(fnv1aString(agentID) % 360)
		dx, dz := bearingToUnitVector(wedge)
		return wedge, [3]float64{
			math.Round((pos.X+dx*36.0)*10) / 10,
			pos.Y,
			math.Round((pos.Z+dz*36.0)*10) / 10,
		}
	}

	inv := inspectBootstrapInventory(frame.Status.Inventory)
	needsTimber := !inv.hasAxe || !inv.hasSword || inv.logCount < 6
	needsStone := !inv.hasStoneGear && inv.cobbleCount < 3

	hasPrevOrigin := loc.HasPrevOrigin && math.Hypot(pos.X-loc.PrevOrigin[0], pos.Z-loc.PrevOrigin[2]) > 4.0
	var backBearing float32
	if hasPrevOrigin {
		backBearing = vectorToBearingDeg(loc.PrevOrigin[0]-pos.X, loc.PrevOrigin[2]-pos.Z)
	}

	agentHash := fnv1aString(agentID)
	wedgeDeg := float32(agentHash % 360)

	var bestSec *civquestv1.HorizonSector
	bestScore := -math.MaxFloat64
	for _, sec := range report.Sectors {
		if sec == nil {
			continue
		}
		b := normalizeBearingDeg(sec.BearingDeg)
		score := float64(sec.HitDistance) * 0.25
		if sec.OpenHorizon {
			score += 10.0
		}
		if sec.Occluded {
			score -= 40.0
		}
		switch strings.ToUpper(strings.TrimSpace(sec.FeatureType)) {
		case "FOREST_CANOPY":
			score += 22.0
			if needsTimber {
				score += 45.0
			}
		case "STONE_OUTCROP":
			score += 12.0
			if needsStone {
				score += 35.0
			}
		case "WATER":
			score += 18.0
		case "OPEN_PLAINS":
			score += 16.0
		case "STRUCTURE":
			score += 20.0
		case "RIDGE_OBSTRUCTION":
			score -= 25.0
		}
		for _, blocked := range loc.Blocked {
			if angularDiffDeg(b, blocked) <= 35.0 {
				score -= 80.0
			}
		}
		for _, haz := range k.Hazards {
			if haz == nil {
				continue
			}
			hdx, hdz := haz.X-pos.X, haz.Z-pos.Z
			hDist := math.Hypot(hdx, hdz)
			if hDist <= 0.1 {
				continue
			}
			if angularDiffDeg(b, vectorToBearingDeg(hdx, hdz)) <= 25.0 && hDist <= float64(sec.HitDistance)+4.0 {
				score -= 60.0
			}
		}
		if hasPrevOrigin && angularDiffDeg(b, backBearing) <= 45.0 {
			score -= 30.0
		}
		if k.ClanSize > 1 && loc.Hops == 0 {
			diffRad := angularDiffDeg(b, wedgeDeg) * math.Pi / 180.0
			score += math.Cos(diffRad) * 15.0
		}
		if loc.Hops > 0 {
			// Forward directional momentum along open plains unless a salient feature or blocked ravine dictates turning.
			diffRad := angularDiffDeg(b, loc.Bearing) * math.Pi / 180.0
			score += math.Cos(diffRad) * 20.0
		}
		// Spatial memory (cq-dcx.4): prefer legs over ground the agent has not covered yet.
		if bx, bz := bearingToUnitVector(b); k.Spatial.Len() > 0 {
			nov := 0.0
			for _, f := range [...]float64{16, 32} {
				nov += spatialNovelty(k.Spatial, pos.X+bx*f, pos.Z+bz*f, frame.WorldTick)
			}
			score += exploreNoveltyWeight * nov / 2
		}
		// Deterministic zero-alloc tie-breaker so uniform terrain still fans out cleanly.
		jitterHash := agentHash ^ (uint32(sec.SectorIndex) * 2654435761)
		score += float64(jitterHash%100) * 0.05

		if bestSec == nil || score > bestScore {
			bestScore, bestSec = score, sec
		}
	}

	chosenBearing := normalizeBearingDeg(bestSec.BearingDeg)
	feat := strings.ToUpper(strings.TrimSpace(bestSec.FeatureType))
	var dist float64
	if (feat == "FOREST_CANOPY" || feat == "STONE_OUTCROP") && bestSec.HitDistance > 0 {
		dist = math.Max(12.0, float64(bestSec.HitDistance)-3.0)
	} else if bestSec.OpenHorizon {
		dist = 48.0
	} else {
		h := agentHash ^ (uint32(loc.Hops) * 2654435761)
		dist = 32.0 + float64(h%17) // [32, 48] blocks
		if bestSec.HitDistance > 15.0 {
			dist = math.Min(dist, math.Max(12.0, float64(bestSec.HitDistance)-3.0))
		}
	}
	dx, dz := bearingToUnitVector(chosenBearing)
	return chosenBearing, [3]float64{
		math.Round((pos.X+dx*dist)*10) / 10,
		pos.Y,
		math.Round((pos.Z+dz*dist)*10) / 10,
	}
}

func buildRendezvousReportSummary(loc expeditionLocal, k expeditionKnowledge, pos *civquestv1.Vec3) string {
	hops := loc.Hops
	if hops < 1 {
		hops = 1
	}
	distFromCamp := 0.0
	if k.SurveyTick > 0 {
		distFromCamp = math.Hypot(k.SurveyOrigin[0]-loc.Home[0], k.SurveyOrigin[2]-loc.Home[2])
	} else if pos != nil {
		distFromCamp = math.Hypot(pos.X-loc.Home[0], pos.Z-loc.Home[2])
	}
	dir := bearingToCompassDir(loc.Bearing)
	if surveySummary := FormatHorizonSurveySummary(k.Survey); surveySummary != "" {
		return fmt.Sprintf("Returned from %d-hop %s expedition (~%.0fm out): %s", hops, dir, distFromCamp, surveySummary)
	}
	return fmt.Sprintf("Returned from %d-hop %s expedition (~%.0fm out) to share frontier map.", hops, dir, distFromCamp)
}

// ---- adapter glue: push request, stack projection, store writes, events ----

func (s *AgentState) hasActivityKindLocked(kind string) bool {
	for _, k := range s.activityKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (s *AgentState) setActivityKinds(kinds []string) {
	s.mu.Lock()
	s.activityKinds = kinds
	s.mu.Unlock()
}

// applyHomesteadClaim records a solo homestead claim (store write on expedition exit).
func (s *AgentState) applyHomesteadClaim(p [3]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HomesteadClaimed = true
	s.HomesteadPos = p
	posVec := &civquestv1.Vec3{X: p[0], Y: p[1], Z: p[2]}
	s.homeOrigin = posVec
	s.currentOutpost = posVec
	s.ShelterPos = &civquestv1.BlockPosProto{
		X: int32(math.Floor(p[0])),
		Y: int32(math.Floor(p[1])),
		Z: int32(math.Floor(p[2])),
	}
	s.goals.ImmediateGoal = "CLAIM_HOMESTEAD: Stake solo homestead and build shelter at discovered site"
}

func (s *AgentState) applyHazardMark(poi db.AgentPOI) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if si := s.SpatialIndexLocked(); si != nil {
		si.UpsertPOI(poi)
	}
}

// clearScoutDirective ends the System 2 scout-frontier directive an expedition satisfied.
func (s *AgentState) clearScoutDirective() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategicDirective = nil
}

// applyExpeditionStoreLocked applies Step.Store writes forwarded by the kernel (callers hold fsm.mu).
func (fsm *AgentFSM) applyExpeditionStoreLocked(ctx *FSMContext, writes []htpda.StoreWrite) {
	for _, wr := range writes {
		switch v := wr.(type) {
		case RendezvousReport:
			fsm.rendezvousOutbox = append(fsm.rendezvousOutbox, v)
		case homesteadClaim:
			if ctx != nil && ctx.State != nil {
				ctx.State.applyHomesteadClaim(v.Pos)
			}
		case hazardMark:
			if ctx != nil && ctx.State != nil {
				ctx.State.applyHazardMark(v.POI)
			}
		case scoutHopReached:
			if ctx != nil && ctx.State != nil {
				ctx.State.clearScoutDirective()
			}
		}
	}
}

// publishStackKindsLocked projects the stack's activity kinds onto AgentState so legacy
// waterfalls can ask "is an expedition/survey active" without owning that progress.
func (fsm *AgentFSM) publishStackKindsLocked(ctx *FSMContext) {
	if ctx == nil || ctx.State == nil || fsm.kernel == nil {
		return
	}
	frames := fsm.activities()
	kinds := make([]string, 0, len(frames))
	for _, f := range frames {
		kinds = append(kinds, string(f.Kind()))
	}
	ctx.State.setActivityKinds(kinds)
}

// DrainRendezvousReports returns and clears the rendezvous reports emitted by expeditions that
// returned to the clan rendezvous origin since the last call.
func (fsm *AgentFSM) DrainRendezvousReports() []RendezvousReport {
	if fsm == nil {
		return nil
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	out := fsm.rendezvousOutbox
	fsm.rendezvousOutbox = nil
	return out
}

// QueueRendezvousReport enqueues a rendezvous report event (the entry point a clan
// coordination service uses to deliver rendezvous events, cq-aik.19).
func (fsm *AgentFSM) QueueRendezvousReport(summary string) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	fsm.rendezvousOutbox = append(fsm.rendezvousOutbox, RendezvousReport{Summary: summary})
	fsm.mu.Unlock()
}

// ExpeditionStatus reports the live expedition frame's hops and flank count (len(Blocked)).
func (fsm *AgentFSM) ExpeditionStatus() (active bool, hops, flanks int, returning bool) {
	if fsm == nil {
		return
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if fsm.kernel == nil {
		return
	}
	for _, f := range fsm.activities() {
		if f.Kind() == activityKindExpedition {
			l := htpda.LocalAs[expeditionLocal](f.Local)
			return true, l.Hops, l.FlankCount(), l.Returning
		}
	}
	return
}

// preemptorAbove is the legacy ActivityArbiter search generalised to a priority floor and
// one excluded kind (the activity that started the running expedition).
func (w *fsmWorld) preemptorAbove(priority float64, skip string) (string, any) {
	acts := w.fsm.managed
	eu, eok := 0.0, false
	if skip != activityKindStrategicDirective {
		eu, eok = w.directiveExpeditionBid()
	}
	bid := func(floor float64) (string, any, bool) {
		if eok && eu > floor && eu > priority {
			return activityKindExpedition, expeditionArgs{Origin: activityKindStrategicDirective}, true
		}
		return "", nil, false
	}
	for _, act := range acts {
		kind, prio := string(act.Kind()), activityPriority(string(act.Kind()))
		if k, a, ok := bid(prio - 1e-9); ok && eu >= prio {
			return k, a
		}
		// One that just left for want of work does not interrupt to look again (live: bootstrap
		// pushed over gathering and popped, every tick).
		if w.skipped[kind] || kind == skip || w.backedOff(act.Kind()) || !strategyAllows(w.objectives(), kind) {
			continue
		}
		// An activity already on the stack is not launched again on top of it, or a
		// low-priority frame above it (gathering on a bootstrap trip) would relaunch it every
		// tick. A task handed over by the stepping frame is a new task, so it still frames.
		if w.onStack(kind) && !(kind == activityKindTaskSubroutine && w.pending != nil) {
			continue
		}
		if prio <= priority {
			break
		}
		if kind == activityKindShelter && !hasShelterWorkOrMaterials(w.ctx) {
			continue
		}
		args, ok := act.args(w)
		if !ok {
			continue
		}
		if act.eligible(w) {
			return kind, args
		}
	}
	if k, a, ok := bid(0); ok {
		return k, a
	}
	return "", nil
}

// onStack reports whether a frame of kind is on the activity stack (inside a Step only).
func (w *fsmWorld) onStack(kind string) bool {
	if w.fsm == nil || w.fsm.kernel == nil {
		return false
	}
	for _, f := range w.activitiesInStep() {
		if string(f.Kind()) == kind {
			return true
		}
	}
	return false
}

// PlanExpeditionTarget returns the first-hop target an expedition would choose from the state's
// latest survey (hop 0, no blocked bearings). ok is false without a position.
func PlanExpeditionTarget(agentID string, frame *civquestv1.TelemetryFrame, state *AgentState) (x, z float64, ok bool) {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return 0, 0, false
	}
	pos := frame.Status.Position
	k := state.expeditionKnowledge(pos)
	_, target := chooseBestSurveyBearing(agentID, frame, pos, k, expeditionLocal{})
	return target[0], target[2], true
}

// expeditionStart reports whether an expedition requested by a walk running as origin may
// start now (no expedition already on the stack, not skipped this tick, origin eligible).
func (w *fsmWorld) expeditionStart(origin string) (expeditionArgs, bool) {
	if w.skipped[activityKindExpedition] || w.ctx.State.IsExpeditionActive() || !w.expeditionEligible(origin) {
		return expeditionArgs{}, false
	}
	// Once settled only a resource trip (bootstrap: at once when unequipped, else after standing
	// idle) has anything to do; any other expedition would finish in the same tick and be
	// requested again.
	if w.ctx.State.settledAt(w.pos()) &&
		(origin != activityKindBootstrap || w.equipped() && !w.ctx.State.idleSince(w.tick, settledTripIdleTicks)) {
		return expeditionArgs{}, false
	}
	return expeditionArgs{Origin: origin}, true
}

// directiveExpeditionBid is the Expedition bid of an active SCOUT_FRONTIER directive.
func (w *fsmWorld) directiveExpeditionBid() (float64, bool) {
	if w.skipped[activityKindExpedition] || w.ctx == nil || w.ctx.State == nil || w.ctx.State.IsExpeditionActive() || w.surveyBackedOff() ||
		w.ctx.State.settledAt(w.pos()) {
		return 0, false
	}
	return directiveExpeditionUtility(w.fsm.currentStage, w.fsm.currentRoutine, w.ctx)
}
