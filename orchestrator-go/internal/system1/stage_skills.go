package system1

import (
	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func ensureFSMFacts(ctx *FSMContext, reg *EngineRegistry) {
	if ctx == nil || ctx.Facts != nil || reg == nil {
		return
	}
	evalCtx := newEvaluatorContext(ctx)
	ctx.Facts = reg.EvaluateAll(evalCtx)
}

func buildSkillContext(ctx *FSMContext, reg *EngineRegistry) *SkillContext {
	if ctx == nil {
		return &SkillContext{Registry: reg}
	}
	return &SkillContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		State:         ctx.State,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
		Registry:      reg,
	}
}

func wrapStageIntents(raw []*civquestv1.AgentIntent, emergency bool) []FSMIntent {
	if len(raw) == 0 {
		return nil
	}
	out := make([]FSMIntent, 0, len(raw))
	for _, ni := range raw {
		if ni == nil {
			continue
		}
		priority := emergency || (ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking)
		out = append(out, FSMIntent{Intent: ni, Priority: priority})
	}
	return out
}

func appendNonNilIntents(dst []*civquestv1.AgentIntent, src []*civquestv1.AgentIntent) []*civquestv1.AgentIntent {
	for _, intent := range src {
		if intent != nil {
			dst = append(dst, intent)
		}
	}
	return dst
}

func skillIsDuskOrNight(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("circadian.is_dusk_or_night"); exists {
			return facts.GetBool("circadian.is_dusk_or_night")
		}
	}
	if ctx == nil || ctx.Frame == nil {
		return false
	}
	if ctx.Frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		return true
	}
	tod := ((ctx.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 11000 && tod < 23500
}

func skillHasAssignedBed(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.has_bed"); exists {
			return facts.GetBool("shelter.has_bed")
		}
	}
	if ctx == nil || ctx.State == nil {
		return false
	}
	return ctx.State.GetAssignedBed() != nil
}

func skillIsShelterComplete(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.complete"); exists {
			return facts.GetBool("shelter.complete")
		}
	}
	if ctx == nil || ctx.State == nil {
		return false
	}
	return ctx.State.GetCabinBuilt() >= blueprints.CabinTotalBlocks
}

func skillIsShelterOnCooldown(ctx *SkillContext, facts *FactGraph) bool {
	if facts != nil {
		if _, exists := facts.Get("shelter.on_cooldown"); exists {
			return facts.GetBool("shelter.on_cooldown")
		}
	}
	if ctx == nil || ctx.State == nil || ctx.Frame == nil {
		return false
	}
	return ctx.State.IsShelterOnCooldown(ctx.Frame.WorldTick)
}

func executeStageBiologicalNeeds(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry) []*civquestv1.AgentIntent {
	if skillCtx == nil || skillCtx.Frame == nil || skillCtx.Frame.Status == nil {
		return nil
	}
	var needsIntents []*civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("biological_needs"); ok && skill != nil {
		if skill.Preconditions(facts) {
			if si, err := skill.Execute(skillCtx, facts); err == nil {
				needsIntents = si
			}
		}
	} else {
		bioSkill := NewBiologicalNeedsSkill()
		if bioSkill.Preconditions(facts) {
			if si, err := bioSkill.Execute(skillCtx, facts); err == nil {
				needsIntents = si
			}
		} else {
			needsIntents = EvaluateBiologicalNeeds(skillCtx.AgentID, skillCtx.Frame, skillCtx.State)
		}
	}

	return needsIntents
}

func executeStageRoadPaving(skillCtx *SkillContext, facts *FactGraph, reg *EngineRegistry, tryInfrastructureSkill bool) []*civquestv1.AgentIntent {
	if skillCtx == nil || skillCtx.Frame == nil || skillCtx.Frame.Status == nil {
		return nil
	}
	if skillCtx.State != nil && skillCtx.State.IsExpeditionSurveying(skillCtx.Frame.WorldTick) {
		return nil
	}
	if tryInfrastructureSkill {
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, facts, "infrastructure"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
	}
	if !skillCtx.Frame.Status.IsSleeping && skillCtx.PavingTracker != nil &&
		skillCtx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		!skillCtx.Frame.Status.NavigationEnclosed {
		if skillCtx.State != nil && skillCtx.PavingTracker.GetHomeOrigin() == nil {
			if home := skillCtx.State.GetHomeOrigin(); home != nil {
				skillCtx.PavingTracker.SetHomeOrigin(home)
			}
		}
		if paveIntent := skillCtx.PavingTracker.EvaluatePaving(skillCtx.AgentID, skillCtx.Frame); paveIntent != nil {
			return []*civquestv1.AgentIntent{paveIntent}
		}
	}
	return nil
}

// StageEmergencySkill executes reactive Tier 0 combat, evasion, and survival emergency reflexes.
type StageEmergencySkill struct{}

// NewStageEmergencySkill creates a StageEmergencySkill instance.
func NewStageEmergencySkill() *StageEmergencySkill { return &StageEmergencySkill{} }

func (s *StageEmergencySkill) Name() string                    { return "stage_emergency" }
func (s *StageEmergencySkill) Category() string                { return "stage_emergency" }
func (s *StageEmergencySkill) Preconditions(f *FactGraph) bool { return f != nil }
func (s *StageEmergencySkill) Interruptible() bool             { return false }
func (s *StageEmergencySkill) OnComplete(_ *SkillContext, _ *FactGraph) {
}

func (s *StageEmergencySkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}
	reg := ctx.Registry
	var skillIntents []*civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("combat_defense"); ok && skill != nil && skill.Preconditions(facts) {
		if si, err := skill.Execute(ctx, facts); err == nil {
			skillIntents = si
		}
	}
	if len(skillIntents) == 0 {
		combatSkill := NewCombatDefenseSkill()
		if si, err := combatSkill.Execute(ctx, facts); err == nil {
			skillIntents = si
		}
	}
	return skillIntents, nil
}
