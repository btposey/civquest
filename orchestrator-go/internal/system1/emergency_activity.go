package system1

// Emergency and Dead as kernel activities (ADR-036, bead cq-2sr.6).
//
//   - emergency: the Tier-0 reflex activity (combat defense, creeper evasion, fire/drowning
//     escape, critical-hunger eating). It bids BandEmergency while ctx.IsEmergency() and pops
//     once ThreatsCleared(); the preempted frame is suspended beneath it and receives Resumed
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

// emergencyLocal is the emergency frame's private state.
type emergencyLocal struct {
	Mode           emergencyMode
	EnteredTick    int64
	LastFleeTarget *civquestv1.Vec3
	LastFleeTick   int64
	Reflexes       int // Java in-engine reflex notices routed to this frame
	LastReflex     htpda.ReflexKind
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
		el.Mode = classifyEmergency(lw.ctx)
	}
	return el, htpda.Lease{}
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
		if _, started := ev.(htpda.Started); !started && lw.ctx.ThreatsCleared() {
			lw.refill = true
			return htpda.Step{Op: htpda.OpPop, Result: el.result(w.Tick())}
		}
		el.Mode = classifyEmergency(lw.ctx)
		intents := unwrapFSMIntents(executeStageSkill(lw.ctx, "stage_emergency", NewStageEmergencySkill(), true))
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

// classifyEmergency names the reflex family for the current threat picture.
func classifyEmergency(ctx *FSMContext) emergencyMode {
	if ctx == nil {
		return emergencyCombat
	}
	if ctx.IsDrowning() || ctx.IsOnFire() {
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
