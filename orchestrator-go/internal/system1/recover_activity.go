package system1

// Recover as a frame-local activity (ADR-037, bead cq-2sr.22). Java reports stuck as a fact on
// the FAILED IntentReceipt (MotorFacts: stuck, enclosed, blocked_bearing_deg, blocked_by_entity);
// the frame that owns the failing intent (the top frame receiving the receipt for its own
// frame_id) pushes a Recover frame. Recover is the ONLY component that issues recovery
// intents and the only writer of the terrain blacklist; it consumes receipts and its own
// position, never telemetry navigation_state.
//
//	Congestion  Java says an alive entity blocks the heading (ADR-033): yield for a bounded
//	            wait, re-issue the parent's navigation, never dig, detour or blacklist. A second
//	            congestion result gives up as Blocked{Cause: Congestion}.
//	Terrain     re-issue with the blocked bearing excluded: sidestep along rotating bearings.
//	Enclosed    dig-out (ported from the retired unstuck.go): break overhead/step blocks,
//	            then step up/out. Java no longer does this itself (cq-2sr.14).
//
// Every Recover is bounded by a lease; expiry or an exhausted attempt budget pops
// Blocked{Bearing, Target, Cause} to the parent, which decides (Expedition flanks via Replace,
// tasks count a failure, Congestion means "retry later"). Terrain/enclosed give-ups are the
// only blacklist input (a Memory write): recoverTunables.TerrainGiveUpsToBlacklist of them on
// the same target blacklist it; congestion never does (and never decays POIs).

import (
	"hash/crc32"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const activityKindRecover = "recover"

// recoverTunables is the single home of Recover's numbers. They are initial values; the
// tuning pass (cq-aik.13) owns them.
var recoverTunables = struct {
	// LeaseTicks bounds one Recover frame (5 s at 20 Hz).
	LeaseTicks int64
	// AttemptTicks bounds one recovery attempt (the congestion yield wait, one sidestep or
	// dig/step leg); it replaces Java's STUCK_RECOVERY_MAX_TICKS.
	AttemptTicks int64
	// TerrainGiveUpsToBlacklist is how many Recover give-ups on one target blacklist it.
	TerrainGiveUpsToBlacklist int
	// SidestepDist is the length of a terrain sidestep leg in blocks.
	SidestepDist float64
	// ExpeditionAttempts is the attempt budget of a Recover pushed by an expedition's
	// Navigate: one sidestep, then the expedition's own flank takes over.
	ExpeditionAttempts int
	// CongestionRetries is how many congestion give-ups an expedition Navigate retries
	// before treating the way as blocked.
	CongestionRetries int
	// StallEpisodes is how many stuck episodes on one target, without the agent getting a
	// block closer between them, blacklist it however each episode ended.
	StallEpisodes int
	// StallMemoryTicks is how long an episode counts toward StallEpisodes.
	StallMemoryTicks int64
}{
	LeaseTicks:                100,
	AttemptTicks:              20,
	TerrainGiveUpsToBlacklist: 2,
	SidestepDist:              4.0,
	ExpeditionAttempts:        1,
	CongestionRetries:         3,
	StallEpisodes:             4,
	StallMemoryTicks:          600,
}

// RecoverCause is why a Recover frame exists, derived from receipt facts only.
type RecoverCause uint8

const (
	CauseTerrain RecoverCause = iota
	CauseCongestion
	CauseEnclosed
)

func (c RecoverCause) String() string {
	switch c {
	case CauseCongestion:
		return "congestion"
	case CauseEnclosed:
		return "enclosed"
	}
	return "terrain"
}

// ---- results and memory writes ----

// recovered is the pop Result of a Recover that got the agent moving again; the parent
// re-emits its intent.
type recovered struct{}

// recoverBlocked is the pop Result of a Recover that gave up (ADR-037 §5 Blocked).
type recoverBlocked struct {
	Bearing   float32
	Target    [3]float64
	HasTarget bool
	Cause     RecoverCause
	Pos       [3]float64
}

// recoverGaveUp is the Memory write of a terrain/enclosed give-up (ADR-037 §4). It is the only
// blacklist and POI-decay input.
type recoverGaveUp struct {
	Target [3]float64
	Cause  RecoverCause
	Tick   int64
	// Stalled: repeated episodes proved the target unreachable; blacklist it at once.
	Stalled bool
}

// stuckRecord counts stuck episodes against one navigation target. Sidesteps can each
// succeed while the parent keeps failing the same way (cq-yn1.6).
type stuckRecord struct {
	Target   [3]float64
	Best     float64 // closest 3D distance to Target at the start of an episode
	Episodes int
	Last     int64
}

// noteStuck records one episode and reports whether the target is now proven stalled.
func (fsm *AgentFSM) noteStuck(target [3]float64, dist float64, tick int64) bool {
	kept := fsm.stuckLog[:0]
	for _, r := range fsm.stuckLog {
		if tick >= r.Last && tick-r.Last < recoverTunables.StallMemoryTicks {
			kept = append(kept, r)
		}
	}
	fsm.stuckLog = kept
	for i := range fsm.stuckLog {
		r := &fsm.stuckLog[i]
		if r.Target != target {
			continue
		}
		r.Last = tick
		if dist <= r.Best-1.0 {
			r.Best, r.Episodes = dist, 1
			return false
		}
		r.Episodes++
		if r.Episodes < recoverTunables.StallEpisodes {
			return false
		}
		fsm.stuckLog = append(fsm.stuckLog[:i], fsm.stuckLog[i+1:]...)
		return true
	}
	fsm.stuckLog = append(fsm.stuckLog, stuckRecord{Target: target, Best: dist, Episodes: 1, Last: tick})
	return false
}

// recoverEscapeLock is the Memory write that keeps an enclosed dig-out heading across Recover
// episodes (the old unstuck heading lock): a later Recover of the same pocket keeps digging
// the same way instead of re-deriving a direction from whatever the parent retargeted to.
type recoverEscapeLock struct {
	DirX, DirZ int32
	Tick       int64
	// Clear releases the lock (the agent moved: Recover popped recovered).
	Clear bool
}

// escapeLockTTL is how long a dig-out heading lock stays valid (the old 120-tick renewal).
const escapeLockTTL = int64(120)

// RecoverGiveUp is the event the runtime drains (AgentFSM.DrainRecoverGiveUps) to decay the
// reliability of the POI the failed target belonged to.
type RecoverGiveUp struct {
	Target      [3]float64
	Cause       RecoverCause
	Tick        int64
	Blacklisted bool
}

// ---- args / local ----

type recoverArgs struct {
	Cause     RecoverCause
	Target    [3]float64 // the parent's failing navigation target
	HasTarget bool
	Bearing   float32 // blocked heading (ADR-032 yaw)
	Facts     htpda.MotorFacts
	// MaxAttempts overrides the lease/attempt budget when > 0.
	MaxAttempts int
	// Dig lets a terrain Recover break an adjacent diggable obstruction (staircase
	// excavation, as the enclosed dig-out does) before sidestepping. Set by a scouting
	// parent (exploration) whose stuck explorer should dig through a dirt wall instead of
	// only contouring around it (cq-aik.30; baseline scouting stuck dig-through).
	Dig bool
}

type recoverLocal struct {
	recoverArgs
	Attempts     int
	Congestions  int
	SawEntity    bool // blocked_by_entity seen at any point of the episode: never blacklist
	StartPos     [3]float64
	StartDist    float64 // horizontal distance to Target when Recover began
	HasStart     bool
	WaitUntil    int64 // congestion yield deadline
	LastEmit     int64
	Emitted      bool // an attempt's intent is in flight
	Locked       bool // enclosed dig direction lock
	DirX, DirZ   int32
	LastDig      int64
	NavNotBefore int64 // enclosed: a failed step-out waits before the next one (digging does not)
	Stalled      bool  // the target has defeated StallEpisodes episodes: give up at once
	NavSent      bool  // the intent in flight is a movement, not a dig
}

// moved reports that the agent is at least a block from where the episode began, in any
// direction including up.
func (l recoverLocal) moved(pos *civquestv1.Vec3) bool {
	return l.HasStart && pos != nil &&
		math.Sqrt(sq(pos.X-l.StartPos[0])+sq(pos.Y-l.StartPos[1])+sq(pos.Z-l.StartPos[2])) >= 1.0
}

func sq(v float64) float64 { return v * v }

// progressed is the success test (ADR-037 §3.5): with a target, the agent is at least a block
// closer to it than when Recover began (a sidestep that merely moves is not progress, or a
// wall would be "recovered" forever and never given up on); otherwise (enclosed, no target)
// it has moved a block from where it began.
func (l recoverLocal) progressed(pos *civquestv1.Vec3) bool {
	if !l.HasStart {
		return false
	}
	if l.HasTarget && l.Cause != CauseEnclosed {
		return math.Hypot(pos.X-l.Target[0], pos.Z-l.Target[2]) <= l.StartDist-1.0
	}
	return math.Hypot(pos.X-l.StartPos[0], pos.Z-l.StartPos[2]) >= 1.0
}

func (l recoverLocal) maxAttempts() int {
	if l.MaxAttempts > 0 {
		return l.MaxAttempts
	}
	if recoverTunables.AttemptTicks <= 0 {
		return 1
	}
	return int(recoverTunables.LeaseTicks / recoverTunables.AttemptTicks)
}

// ---- classification (receipt facts only) ----

// isStuckReceipt reports whether a receipt is the motor's "stuck" failure.
func isStuckReceipt(status civquestv1.IntentReceipt_ReceiptStatus, facts htpda.MotorFacts, reason string) bool {
	if status != civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED {
		return false
	}
	return facts.Stuck || facts.Enclosed || strings.Contains(strings.ToLower(reason), "navigation stuck")
}

// recoverCauseOf derives the cause from facts, not from Java state (ADR-037 §3.3).
func recoverCauseOf(f htpda.MotorFacts) RecoverCause {
	switch {
	case f.BlockedByEntity:
		return CauseCongestion
	case f.Enclosed:
		return CauseEnclosed
	}
	return CauseTerrain
}

// recoverArgsFor returns the Recover args a frame pushes when ev is a stuck receipt for its
// own intent (receipts for other frames never reach a Step). budget > 0 overrides the
// attempt budget.
func (w *fsmWorld) recoverArgsFor(ev htpda.Receipt, budget int) (recoverArgs, bool) {
	reason := ""
	if w.receipt != nil {
		reason = w.receipt.GetFailureReason()
	}
	if !isStuckReceipt(ev.Status, ev.Facts, reason) {
		return recoverArgs{}, false
	}
	a := recoverArgs{Cause: recoverCauseOf(ev.Facts), Bearing: ev.Facts.BlockedBearingDeg, Facts: ev.Facts, MaxAttempts: budget}
	if t, ok := w.fsm.navTargets[ev.Frame]; ok {
		a.Target, a.HasTarget = t, true
	}
	if a.Cause != CauseCongestion && w.fsm.boxedIn(w.pos(), w.Tick()) {
		a.Cause, a.Dig = CauseEnclosed, true
	}
	return a, true
}

// ---- activity ----

type recoverActivity struct{}

var _ htpda.Activity = recoverActivity{}

func (recoverActivity) Kind() htpda.Kind   { return activityKindRecover }
func (recoverActivity) Scope() htpda.Scope { return htpda.ScopeMotor }

// Propose abstains: a framed intent is recovered by the frame that owns it pushing Recover,
// and an orphan stuck receipt is picked up by the goal frame (bestWork).
func (recoverActivity) Propose(htpda.World) htpda.Bid { return htpda.Bid{} }

func (recoverActivity) Init(w htpda.World, args any) (any, htpda.Lease) {
	a, _ := args.(recoverArgs)
	if lw := asFSMWorld(w); lw != nil && lw.fsm != nil {
		lw.fsm.orphan = nil
	}
	loc := recoverLocal{recoverArgs: a, SawEntity: a.Cause == CauseCongestion || a.Facts.BlockedByEntity}
	if a.Cause == CauseCongestion {
		loc.Congestions = 1
	}
	if lw := asFSMWorld(w); lw != nil {
		if l := lw.fsm.escapeLock; l != nil && a.Cause == CauseEnclosed && w.Tick()-l.Tick < escapeLockTTL {
			loc.Locked, loc.DirX, loc.DirZ = true, l.DirX, l.DirZ
		}
		if p := lw.pos(); p != nil {
			loc.StartPos, loc.HasStart = [3]float64{p.X, p.Y, p.Z}, true
			if a.HasTarget {
				loc.StartDist = math.Hypot(p.X-a.Target[0], p.Z-a.Target[2])
				// Not for an enclosed agent: digging, not another target, is its remedy.
				if a.Cause == CauseTerrain && !loc.SawEntity && lw.fsm != nil {
					dist := math.Sqrt(sq(p.X-a.Target[0]) + sq(p.Y-a.Target[1]) + sq(p.Z-a.Target[2]))
					loc.Stalled = lw.fsm.noteStuck(a.Target, dist, w.Tick())
				}
			}
		}
	}
	return loc, htpda.Lease{StartTick: w.Tick(), DeadlineTick: w.Tick() + recoverTunables.LeaseTicks}
}

func (a recoverActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	loc := htpda.LocalAs[recoverLocal](local)
	if lw == nil || lw.ctx == nil || lw.ctx.State == nil {
		return htpda.Step{Op: htpda.OpPop, Result: a.blocked(loc, nil)}
	}
	if _, cancelled := ev.(htpda.Cancel); !cancelled && loc.Stalled {
		return a.giveUp(lw, loc)
	}
	switch e := ev.(type) {
	case htpda.Cancel:
		return htpda.Step{Op: htpda.OpPop}
	case htpda.Expired:
		return a.giveUp(lw, loc)
	case htpda.ChildDone, htpda.Resumed:
		// A preemptor (emergency) held the motor: whatever was in flight is gone.
		loc.Emitted = false
		return a.advance(lw, loc)
	case htpda.Receipt:
		return a.onReceipt(lw, loc, e)
	}
	return a.advance(lw, loc) // Started, Tick
}

func (a recoverActivity) onReceipt(lw *fsmWorld, loc recoverLocal, e htpda.Receipt) htpda.Step {
	switch e.Status {
	case civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED:
		// A finished leg or dig is not a recovery by itself: a sidestep already within
		// arrival tolerance completes without moving the agent at all.
		if pos := lw.pos(); loc.progressed(pos) || loc.moved(pos) {
			return a.pop(loc, recovered{})
		}
		if loc.Cause == CauseEnclosed && loc.NavSent {
			// Enclosed attempts are bounded only by the lease: pace a step that goes nowhere.
			loc.NavNotBefore = lw.Tick() + recoverTunables.AttemptTicks
		}
		loc.turnAfterFailedStep()
		loc.Emitted = false
		return a.advance(lw, loc)
	case civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED:
		loc.Emitted = false
		reason := ""
		if lw.receipt != nil {
			reason = lw.receipt.GetFailureReason()
		}
		if isStuckReceipt(e.Status, e.Facts, reason) {
			loc.turnAfterFailedStep()
			if loc.Cause == CauseEnclosed {
				loc.NavNotBefore = lw.Tick() + recoverTunables.AttemptTicks
			} else {
				loc.Bearing = e.Facts.BlockedBearingDeg
			}
			switch cause := recoverCauseOf(e.Facts); cause {
			case CauseCongestion:
				loc.SawEntity = true
				loc.Congestions++
				if loc.Congestions >= 2 {
					loc.Cause = CauseCongestion
					return a.giveUp(lw, loc)
				}
				loc.Cause = CauseCongestion
				loc.WaitUntil = 0
			default:
				loc.Cause = cause
			}
		}
		return a.advance(lw, loc)
	case civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED, civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX:
		loc.Emitted = false
		return a.advance(lw, loc)
	}
	return htpda.Step{Op: htpda.OpStay, Next: loc}
}

// advance decides this tick's move: recovered, give up, wait, or emit the next attempt.
func (a recoverActivity) advance(lw *fsmWorld, loc recoverLocal) htpda.Step {
	f, pos := lw.Frame(), lw.pos()
	if f == nil || f.Status == nil || pos == nil {
		return htpda.Step{Op: htpda.OpStay, Next: loc}
	}
	tick := lw.Tick()
	if loc.progressed(pos) && (loc.Attempts > 0 || loc.Cause == CauseCongestion) {
		return a.pop(loc, recovered{})
	}
	if loc.Emitted {
		if tick-loc.LastEmit < recoverTunables.AttemptTicks {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		loc.Emitted = false // attempt timed out: next one (or give up)
	}
	if loc.Attempts >= loc.maxAttempts() && loc.Cause != CauseEnclosed {
		return a.giveUp(lw, loc)
	}
	var intent *civquestv1.AgentIntent
	switch loc.Cause {
	case CauseCongestion:
		if loc.WaitUntil == 0 {
			loc.WaitUntil = tick + recoverTunables.AttemptTicks
		}
		if tick < loc.WaitUntil {
			return htpda.Step{Op: htpda.OpStay, Next: loc} // yield
		}
		if !loc.HasTarget {
			return a.pop(loc, recovered{})
		}
		intent = recoverNav(lw.agentID(), loc.Target[0], loc.Target[1], loc.Target[2], 2)
		loc.Attempts++
	case CauseEnclosed:
		var isNav bool
		intent, isNav = loc.enclosedStep(lw.agentID(), f, pos, tick, &loc)
		if intent == nil || (isNav && tick < loc.NavNotBefore) {
			return htpda.Step{Op: htpda.OpStay, Next: loc}
		}
		if isNav {
			loc.Attempts++ // enclosed is bounded by the lease, not the attempt budget (digging takes time)
		}
	default:
		if loc.Dig && hasAdjacentObstruction(f, loc.Bearing, pos) {
			var isNav bool
			intent, isNav = loc.enclosedStep(lw.agentID(), f, pos, tick, &loc)
			if intent == nil || (isNav && tick < loc.NavNotBefore) {
				return htpda.Step{Op: htpda.OpStay, Next: loc} // wait for the break to register
			}
			if isNav {
				loc.Attempts++
			}
			break
		}
		offsets := [...]float32{90, -90, 135, -135, 180}
		// Peers stuck against the same wall fan out to different sides first (the retired
		// unstuck's agent-specific angular rotation): even-hash agents try +90 first.
		if crc32.ChecksumIEEE([]byte(lw.agentID()))%2 == 1 {
			offsets = [...]float32{-90, 90, -135, 135, 180}
		}
		b := normalizeBearingDeg(loc.Bearing + offsets[loc.Attempts%len(offsets)])
		dx, dz := bearingToUnitVector(b)
		d := recoverTunables.SidestepDist
		intent = recoverNav(lw.agentID(), math.Round((pos.X+dx*d)*10)/10, pos.Y, math.Round((pos.Z+dz*d)*10)/10, 1)
		loc.Attempts++
	}
	loc.LastEmit, loc.Emitted = tick, true
	loc.NavSent = intent.GetNavigate() != nil
	return htpda.Step{Op: htpda.OpStay, Next: loc, Intents: []*civquestv1.AgentIntent{intent}}
}

// pop ends the frame with result, keeping the enclosed dig heading for the next episode.
func (recoverActivity) pop(loc recoverLocal, result any) htpda.Step {
	st := htpda.Step{Op: htpda.OpPop, Result: result}
	if _, ok := result.(recovered); ok {
		// The agent is moving again (the retired unstuck's movement-unlock): the heading lock
		// is released so the next episode derives its direction afresh.
		st.Memory = []htpda.MemoryWrite{recoverEscapeLock{Clear: true}}
	} else if loc.Locked && loc.Cause == CauseEnclosed {
		st.Memory = []htpda.MemoryWrite{recoverEscapeLock{DirX: loc.DirX, DirZ: loc.DirZ, Tick: loc.LastDig}}
	}
	return st
}

func (recoverActivity) blocked(loc recoverLocal, pos *civquestv1.Vec3) recoverBlocked {
	b := recoverBlocked{Bearing: loc.Bearing, Target: loc.Target, HasTarget: loc.HasTarget, Cause: loc.Cause}
	if pos != nil {
		b.Pos = [3]float64{pos.X, pos.Y, pos.Z}
	}
	return b
}

// giveUp pops Blocked to the parent. Only a terrain/enclosed give-up with no entity ever
// involved and no expedition (which flanks instead of blacklisting) writes Memory.
func (a recoverActivity) giveUp(lw *fsmWorld, loc recoverLocal) htpda.Step {
	st := a.pop(loc, a.blocked(loc, lw.pos()))
	if loc.Cause != CauseCongestion && !loc.SawEntity {
		st.Memory = append(st.Memory, spotGiveUp(lw.pos(), lw.Tick())...)
	}
	// An expedition flanks around one failure itself, so only a proven stall is recorded.
	if loc.Cause != CauseCongestion && !loc.SawEntity && loc.HasTarget && (loc.Stalled || !lw.ctx.State.IsExpeditionActive()) {
		st.Memory = append(st.Memory, recoverGaveUp{Target: loc.Target, Cause: loc.Cause, Tick: lw.Tick(), Stalled: loc.Stalled})
	}
	return st
}

func recoverNav(agentID string, x, y, z float64, tol int32) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target:           &civquestv1.Vec3{X: x, Y: y, Z: z},
			SpeedMultiplier:  1.0,
			ArrivalTolerance: tol,
		}},
	}
}

func recoverBreak(agentID string, b *civquestv1.VisibleBlock) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
			Pos:             b.Pos,
			InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
			BlockOrItemId:   b.BlockId,
		}},
	}
}

// ---- enclosed dig-out (ported from the retired EvaluateUnstuck; Java no longer digs) ----

// enclosedStep returns the next dig-out intent (isNav: a step-up/out navigation, which counts
// as an attempt) or nil to wait for a break to register. It updates the heading lock and the
// dig throttle in out. Pillaring up has no Go-side intent (no jump) and is not restored.
func (loc recoverLocal) enclosedStep(agentID string, frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3, tick int64, out *recoverLocal) (*civquestv1.AgentIntent, bool) {
	bx, by, bz := int32(math.Floor(pos.X)), int32(math.Floor(pos.Y)), int32(math.Floor(pos.Z))
	if !out.Locked {
		out.DirX, out.DirZ, _ = recoverEscapeDir(frame, loc.Bearing, bx, by, bz)
		out.Locked = true
	}
	dirX, dirZ := out.DirX, out.DirZ
	canDig := func() bool {
		return out.LastDig == 0 || tick < out.LastDig || tick-out.LastDig >= recoverTunables.AttemptTicks
	}
	diggable := func(x, y, z int32) *civquestv1.VisibleBlock {
		if b := FindVisibleBlockAt(frame, x, y, z); b != nil && IsDiggableObstruction(b.BlockId) {
			return b
		}
		return nil
	}
	// breakAt returns (intent, true) when b is set: a break if the throttle allows, else wait.
	breakAt := func(b *civquestv1.VisibleBlock) (*civquestv1.AgentIntent, bool) {
		if !canDig() {
			return nil, false
		}
		out.LastDig = tick
		return recoverBreak(agentID, b), false
	}

	if b := diggable(bx, by+2, bz); b != nil { // overhead clearance
		return breakAt(b)
	}
	s1x, s1z := bx+dirX, bz+dirZ
	if b := diggable(s1x, by+1, s1z); b != nil { // eye level of the first step
		return breakAt(b)
	}
	if b := diggable(s1x, by+2, s1z); b != nil {
		return breakAt(b)
	}
	s2x, s2z := bx+2*dirX, bz+2*dirZ
	if diggable(s1x, by, s1z) != nil { // 1-block elevated step: clear step-2 headroom, then jump onto it
		if b := diggable(s2x, by+2, s2z); b != nil {
			return breakAt(b)
		}
		return recoverNav(agentID, float64(s1x)+0.5, float64(by)+1.0, float64(s1z)+0.5, 1), true
	}
	if b := diggable(s2x, by+1, s2z); b != nil {
		return breakAt(b)
	}
	// Flat walkway is open: fan out with an agent-specific rotation so peers separate.
	base := math.Atan2(float64(dirZ), float64(dirX))
	h := crc32.ChecksumIEEE([]byte(agentID)) + uint32(loc.Attempts)*3
	angle := base + float64(h%8)*(math.Pi/4.0)
	return recoverNav(agentID, math.Round((pos.X+math.Cos(angle)*2.8)*10)/10, float64(by), math.Round((pos.Z+math.Sin(angle)*2.8)*10)/10, 1), true
}

// turnAfterFailedStep tries the next side after a step out that went nowhere (cq-6l3.23): the
// same step would be chosen again for ever.
func (loc *recoverLocal) turnAfterFailedStep() {
	if loc.Locked && loc.NavSent {
		loc.DirX, loc.DirZ = -loc.DirZ, loc.DirX
		loc.NavSent = false
	}
}

// recoverEscapeDir picks the cardinal escape direction: the blocked heading, or the cardinal
// with a diggable obstruction when the heading is open.
func recoverEscapeDir(frame *civquestv1.TelemetryFrame, bearing float32, bx, by, bz int32) (dirX, dirZ int32, obstructedDir bool) {
	dx, dz := bearingToUnitVector(bearing)
	cx, cz := int32(0), int32(1)
	if math.Abs(dx) > math.Abs(dz) {
		cx, cz = 1, 0
		if dx < 0 {
			cx = -1
		}
	} else if dz < 0 {
		cz = -1
	}
	obstructed := func(x, z int32) bool {
		for dy := int32(0); dy <= 2; dy++ {
			if b := FindVisibleBlockAt(frame, x, by+dy, z); b != nil && IsDiggableObstruction(b.BlockId) {
				return true
			}
		}
		return false
	}
	if obstructed(bx+cx, bz+cz) {
		return cx, cz, true
	}
	for _, d := range CardinalDirs {
		if obstructed(bx+d[0], bz+d[1]) {
			return d[0], d[1], true
		}
	}
	return cx, cz, false
}

// hasAdjacentObstruction reports whether a diggable block stands in a cardinal cell next to
// the agent (feet to head-clearance height): the precondition of a scouting dig-through.
func hasAdjacentObstruction(frame *civquestv1.TelemetryFrame, bearing float32, pos *civquestv1.Vec3) bool {
	_, _, ok := recoverEscapeDir(frame, bearing, int32(math.Floor(pos.X)), int32(math.Floor(pos.Y)), int32(math.Floor(pos.Z)))
	return ok
}
