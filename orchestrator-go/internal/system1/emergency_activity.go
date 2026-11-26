package system1

// Emergency and Dead as kernel activities (ADR-036, bead cq-2sr.6).
//
//   - emergency: the Tier-0 reflex activity (combat defense, creeper evasion, fire/drowning
//     escape, critical-hunger eating). It bids BandEmergency while ctx.IsEmergency() and pops
//     once its own resolution predicate holds (emergencyLocal.resolved: hazards cleared by
//     sustained telemetry, hostile/damage quiet windows; cq-gpa.2); the preempted frame is suspended beneath it and receives Resumed
//     with its locals intact. Everything the reflex remembers (mode, last flee target/tick,
//     reflex notices seen) is its frame Local, so it dies with the frame.
//   - dead: terminal frame at BandDead. Death dispatches Cancel (unwinding the stack) and the
//     arbiter then pushes this frame; nothing outranks it, so it never pops.
//
// Stage (BOOTSTRAP/SETTLEMENT/COLONIZE/EMERGENCY/DEAD) is a derived band that only selects the
// PriorityTable row and routine; no per-stage handler dispatches work.

import (
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	activityKindEmergency = "emergency"
	activityKindDead      = "dead"
)

// emergencyMode is the reflex family currently being exercised.
type emergencyMode string

const (
	emergencyCombat  emergencyMode = "combat_defense"
	emergencyEvasion emergencyMode = "creeper_evasion"
	emergencyEscape  emergencyMode = "fire_drowning_escape"
	emergencyHunger  emergencyMode = "critical_hunger"
)

// emergencyLocal is the emergency frame's private state. It also holds the resolution state
// (cq-gpa.2) and the held fight-vs-flee verdict (cq-gpa.3): the reflex owns the top of the
// motor stack until resolved() holds, instead of exiting on the first damage-free frame.
type emergencyLocal struct {
	Mode           emergencyMode
	EnteredTick    int64
	LastFleeTarget *civquestv1.Vec3
	LastFleeTick   int64
	Reflexes       int // Java in-engine reflex notices routed to this frame
	LastReflex     htpda.ReflexKind

	Drowning        bool  // latched until drowningResolved
	Burning         bool  // latched until the agent stops burning
	LastHostileTick int64 // last tick an immediate hostile was present (0 never)
	LastDamageTick  int64 // last tick combat damage landed (0 never)
	Eng             engagement
}

// emergencyResult is the emergency frame's pop Result (the combat summary).
type emergencyResult struct {
	Mode           emergencyMode
	EnteredTick    int64
	ExitTick       int64
	LastFleeTarget *civquestv1.Vec3
	Reflexes       int
}

type emergencyActivity struct{}

var _ htpda.Activity = emergencyActivity{}

func (emergencyActivity) Kind() htpda.Kind   { return activityKindEmergency }
func (emergencyActivity) Scope() htpda.Scope { return htpda.ScopeMotor }

func (emergencyActivity) Propose(w htpda.World) htpda.Bid {
	lw := asFSMWorld(w)
	if lw == nil || lw.ctx == nil || lw.ctx.Frame == nil || lw.ctx.Frame.Status == nil || lw.fsm.currentStage == StageDead {
		return htpda.Bid{}
	}
	if !lw.ctx.IsEmergency() {
		return htpda.Bid{}
	}
	return htpda.Bid{Band: htpda.BandEmergency, Utility: 1, Reason: "threat"}
}

func (emergencyActivity) Init(w htpda.World, _ any) (any, htpda.Lease) {
	el := emergencyLocal{EnteredTick: w.Tick(), Mode: emergencyCombat}
	if lw := asFSMWorld(w); lw != nil {
		el.observe(lw.ctx, w.Tick())
		el.Mode = classifyEmergency(lw.ctx, el)
	}
	return el, htpda.Lease{StartTick: w.Tick(), DeadlineTick: w.Tick() + emergencyLeaseTicks}
}

func (emergencyActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	el := htpda.LocalAs[emergencyLocal](local)
	switch e := ev.(type) {
	case htpda.Cancel, htpda.Expired:
		return htpda.Step{Op: htpda.OpPop, Result: el.result(w.Tick())}
	case htpda.Reflex:
		el.Reflexes++
		el.LastReflex = e.Kind
		return htpda.Step{Op: htpda.OpStay, Next: el}
	case htpda.Started, htpda.Tick, htpda.ChildDone, htpda.Resumed:
		lw := asFSMWorld(w)
		if lw == nil || lw.ctx == nil {
			return htpda.Step{Op: htpda.OpStay, Next: el}
		}
		el.observe(lw.ctx, w.Tick())
		if _, started := ev.(htpda.Started); !started && el.resolved(lw.ctx, w.Tick()) {
			lw.refill = true
			return htpda.Step{Op: htpda.OpPop, Result: el.result(w.Tick())}
		}
		el.Mode = classifyEmergency(lw.ctx, el)
		intents := el.reflexIntents(lw.ctx, w.Tick())
		for _, in := range intents {
			if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
				el.LastFleeTarget = nav.Target
				el.LastFleeTick = w.Tick()
			}
		}
		return htpda.Step{Op: htpda.OpStay, Intents: intents, Next: el}
	}
	return htpda.Step{Op: htpda.OpStay, Next: el}
}

func (el emergencyLocal) result(tick int64) emergencyResult {
	return emergencyResult{Mode: el.Mode, EnteredTick: el.EnteredTick, ExitTick: tick, LastFleeTarget: el.LastFleeTarget, Reflexes: el.Reflexes}
}

// observe folds this frame's sustained facts into the frame-local resolution state.
func (el *emergencyLocal) observe(ctx *FSMContext, tick int64) {
	if ctx == nil || ctx.Frame == nil {
		return
	}
	if ctx.IsDrowning() {
		el.Drowning = true
	}
	if ctx.IsOnFire() {
		el.Burning = true
	}
	if el.Drowning && drowningResolved(ctx.Frame) && !ctx.IsDrowning() {
		el.Drowning = false
	}
	if el.Burning && !ctx.IsOnFire() {
		el.Burning = false
	}
	if ctx.HostilesPresent() {
		el.LastHostileTick = tick
	}
	if ctx.DamageTaken() {
		el.LastDamageTick = tick
	}
}

// resolved is the pop predicate: the hazards cleared by sustained facts (not damage edges),
// no hostile for the settle window, no combat damage for the quiet window, and no critical
// hunger / panic left.
func (el emergencyLocal) resolved(ctx *FSMContext, tick int64) bool {
	quiet := func(last, n int64) bool { return last == 0 || tick-last >= n }
	return !el.Drowning && !el.Burning &&
		quiet(el.LastHostileTick, emergencySettleHostileTicks) &&
		quiet(el.LastDamageTick, emergencyQuietDamageTicks) &&
		!ctx.IsCriticalHunger() && !ctx.ShouldPanic()
}

// reflexIntents is the emergency frame's motor output. A latched hazard owns the output
// (life first: drowning, then fire without hostiles); otherwise the combat/hunger reflex runs
// with the held engagement verdict bound.
func (el *emergencyLocal) reflexIntents(ctx *FSMContext, tick int64) []*civquestv1.AgentIntent {
	if el.Drowning || (el.Burning && !ctx.HostilesPresent()) {
		if el.Drowning && ctx.Frame != nil && ctx.Frame.Status != nil && !ctx.Frame.Status.IsInWater && !ctx.Frame.Status.Submerged {
			return nil // on dry ground waiting for air to regenerate: do not move
		}
		var out []*civquestv1.AgentIntent
		if in := hazardEscapeIntent(ctx.AgentID, ctx.Frame, el.Drowning, el.LastFleeTarget, el.LastFleeTick, tick); in != nil {
			out = append(out, in)
		}
		return out
	}
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil
	}
	reg := resolveContextRegistry(ctx)
	ensureFSMFacts(ctx, reg)
	sc := buildSkillContext(ctx, reg)
	eng := el.Eng
	sc.Engagement = &eng
	var skill Skill = NewStageEmergencySkill()
	if s, ok := reg.GetSkill("stage_emergency"); ok && s != nil {
		skill = s
	}
	raw, err := skill.Execute(sc, ctx.Facts)
	el.Eng = eng
	if err != nil {
		return nil
	}
	return raw
}

// classifyEmergency names the reflex family for the current threat picture.
func classifyEmergency(ctx *FSMContext, el emergencyLocal) emergencyMode {
	if ctx == nil {
		return emergencyCombat
	}
	if el.Drowning || el.Burning || ctx.IsDrowning() || ctx.IsOnFire() {
		return emergencyEscape
	}
	if ctx.Frame != nil {
		for _, e := range ctx.Frame.VisibleEntities {
			if e != nil && IsHostile(e) && e.FuseTicks > 0 && strings.Contains(strings.ToLower(e.EntityType), "creeper") {
				return emergencyEvasion
			}
		}
	}
	if ctx.HostilesPresent() || ctx.DamageTaken() || ctx.ShouldPanic() {
		return emergencyCombat
	}
	return emergencyHunger
}

// holdEmergencyFactsLocked keeps the derived EMERGENCY stage and the threat facts asserted
// while the emergency frame on top of the motor stack has not resolved (cq-gpa.2), so stage
// derivation and the frame agree: the reflex owns the stack until resolved() holds, however
// quiet the raw frame looks between periodic damage edges. resolved() implies the frame-derived
// facts are cleared, so releasing the hold cannot immediately re-trigger an emergency.
func (fsm *AgentFSM) holdEmergencyFactsLocked(ctx *FSMContext) *FactGraph {
	if ctx == nil || ctx.Facts == nil || ctx.Frame == nil || fsm.kernel == nil {
		return nil
	}
	if top, ok := fsm.kernel.Top(); ok && top.Kind() == activityKindEmergency {
		el := htpda.LocalAs[emergencyLocal](top.Local)
		tick := ctx.Frame.WorldTick
		el.observe(ctx, tick)
		if !el.resolved(ctx, tick) {
			// The evaluated graph is frozen (read-only world view): publish an overridden copy.
			ctx.Facts = ctx.Facts.Clone()
			defer ctx.Facts.Freeze()
			ctx.Facts.SetBool("threat.is_emergency", "threat", true, "Emergency held by reflex frame until resolved")
			ctx.Facts.SetBool("threat.threats_cleared", "threat", false, "Threats not cleared: emergency frame unresolved")
		}
	}
	return ctx.Facts
}

// deadActivity is the terminal frame.
type deadActivity struct{}

var _ htpda.Activity = deadActivity{}

func (deadActivity) Kind() htpda.Kind   { return activityKindDead }
func (deadActivity) Scope() htpda.Scope { return 0 }

func (deadActivity) Propose(w htpda.World) htpda.Bid {
	if frameIsDead(w.Frame()) || Stage(w.Stage()) == StageDead {
		return htpda.Bid{Band: htpda.BandDead, Utility: 1, Reason: "death"}
	}
	return htpda.Bid{}
}

func (deadActivity) Init(htpda.World, any) (any, htpda.Lease) { return struct{}{}, htpda.Lease{} }

func (deadActivity) Step(_ htpda.World, local any, ev htpda.Event) htpda.Step {
	if _, ok := ev.(htpda.Cancel); ok {
		return htpda.Step{Op: htpda.OpPop}
	}
	return htpda.Step{Op: htpda.OpStay, Next: local}
}

func frameIsDead(f *civquestv1.TelemetryFrame) bool {
	return f != nil && (f.DeathEvent != nil || (f.Status != nil && f.Status.Health <= 0))
}

// topIsDeadLocked reports whether the terminal frame is on top of the stack.
func (fsm *AgentFSM) topIsDeadLocked() bool {
	if fsm.kernel == nil {
		return false
	}
	top, ok := fsm.kernel.Top()
	return ok && top.Kind() == activityKindDead
}

// onEmergencyEntryLocked applies the entry side effects of preempting into the emergency band
// (the wiring layer's counterpart of the pure frame push): committed tasks that cannot
// survive a preemption fail (the task frame pops when exposed again). An in-flight expedition
// survey frame aborts itself when the emergency pops.
func (fsm *AgentFSM) onEmergencyEntryLocked(ctx *FSMContext) {
	if ctx == nil || ctx.State == nil || fsm.kernel == nil {
		return
	}
	frames := fsm.kernel.Frames()
	for _, f := range frames {
		var t *TaskSubroutine
		if tl, ok := f.Local.(taskLocal); ok {
			t = tl.Task
		} else if p := pendingTaskOf(f.Local); p != nil {
			t = p.Task
		}
		if taskActive(t) && !t.Spec.Preemptible {
			t.Phase = TaskPhaseFailed
			t.FailureReason = "preempted by emergency"
		}
	}
	ctx.State.publishTask(topTaskOf(frames))
}
