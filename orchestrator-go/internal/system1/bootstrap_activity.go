package system1

// Bootstrap as a frame-local activity (ADR-036, bead cq-2sr.9/.10). The frame's Local carries
// its walk cursor. The activity owns its eligibility and waterfall; its weight is
// priorityBootstrap.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

type bootstrapLocal struct{ Walk walkLocal }

type bootstrapActivity struct{ frameBase }

func (a bootstrapActivity) Propose(w htpda.World) htpda.Bid { return proposeManaged(a, w) }
func (a bootstrapActivity) Init(htpda.World, any) (any, htpda.Lease) {
	return bootstrapLocal{}, htpda.Lease{}
}
func (a bootstrapActivity) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	return stepManaged(a, w, local, ev)
}
func (bootstrapActivity) fallThrough(lw *fsmWorld) bool { return lw.fsm.currentStage == StageColonize }

func (a bootstrapActivity) eligible(lw *fsmWorld) bool {
	return isBootstrapActivityEligible(lw.fsm.currentStage, lw.fsm.currentRoutine, lw.ctx)
}

func (a bootstrapActivity) run(lw *fsmWorld, local any) htpda.Step {
	bl, _ := local.(bootstrapLocal)
	return runWalkFrame(a, lw, bl.Walk, func(w walkLocal) any { return bootstrapLocal{Walk: w} })
}

// tick: in the Bootstrap stage the bootstrap skills run first and the goal evaluator is a
// fallback only while no task is active (a started task owns the next tick); Colonize uses the
// evaluator directly.
func (bootstrapActivity) tick(lw *fsmWorld) ([]FSMIntent, bool) {
	ctx, skillCtx := lw.ctx, lw.skillCtx
	var si []*civquestv1.AgentIntent
	if lw.fsm.currentStage == StageBootstrap {
		if skillIntents, _, ok := lw.reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "bootstrap"); ok && len(skillIntents) > 0 {
			si = appendNonNilIntents(nil, skillIntents)
		} else if ctx.State == nil || !ctx.State.HasActiveTask() {
			if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); intent != nil {
				si = []*civquestv1.AgentIntent{intent}
			}
		}
	} else if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, skillCtx.Walk); intent != nil {
		si = []*civquestv1.AgentIntent{intent}
	}
	return wrapOrDone(si)
}

// isBootstrapActivityEligible: a CRAFT_SURVIVAL_KIT directive, an open bootstrap window, or
// (Colonize) awake and not shelter-gated while no survey is in flight.
func isBootstrapActivityEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if directiveCraftsKit(stage, routine, ctx) {
		return true
	}
	if stage == StageBootstrap {
		return isBootstrapWindowOpen(routine, ctx)
	}
	if stage == StageColonize {
		if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
			return false
		}
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		return ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick)
	}
	return false
}
