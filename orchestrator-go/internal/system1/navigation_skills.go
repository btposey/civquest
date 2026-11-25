package system1

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// CivicPatrolSkill executes autonomous perimeter polygon patrol and landmark sentry watch.
type CivicPatrolSkill struct{}

// NewCivicPatrolSkill creates a CivicPatrolSkill instance.
func NewCivicPatrolSkill() *CivicPatrolSkill {
	return &CivicPatrolSkill{}
}

func (s *CivicPatrolSkill) Name() string {
	return "civic_patrol"
}

func (s *CivicPatrolSkill) Category() string {
	return "security"
}

func (s *CivicPatrolSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	return facts.GetBool("patrol.active") || facts.GetString("circadian.routine") == "CIVIC_PATROL"
}

func (s *CivicPatrolSkill) Interruptible() bool {
	return true
}

func (s *CivicPatrolSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateCivicPatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *CivicPatrolSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

// FrontierScoutingSkill executes sector azimuth expansion, radial outward legs, and terrain trailblazing.
type FrontierScoutingSkill struct{}

// NewFrontierScoutingSkill creates a FrontierScoutingSkill instance.
func NewFrontierScoutingSkill() *FrontierScoutingSkill {
	return &FrontierScoutingSkill{}
}

func (s *FrontierScoutingSkill) Name() string {
	return "frontier_scouting"
}

func (s *FrontierScoutingSkill) Category() string {
	return "exploration"
}

func (s *FrontierScoutingSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	routine := facts.GetString("circadian.routine")
	return facts.GetBool("scouting.active") && (routine == "PRIMARY_DUTY" || routine == "DAWN_PREP")
}

func (s *FrontierScoutingSkill) Interruptible() bool {
	return true
}

func (s *FrontierScoutingSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateFrontierScouting(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *FrontierScoutingSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

// RoadPavingSkill evaluates foot traffic frequency and emits PAVE_ROAD_PATH along frequented corridors.
type RoadPavingSkill struct{}

// NewRoadPavingSkill creates a RoadPavingSkill instance.
func NewRoadPavingSkill() *RoadPavingSkill {
	return &RoadPavingSkill{}
}

func (s *RoadPavingSkill) Name() string {
	return "road_paving"
}

func (s *RoadPavingSkill) Category() string {
	return "infrastructure"
}

func (s *RoadPavingSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	return facts.GetBool("paving.needs_pave") && !facts.GetBool("vitals.is_sleeping")
}

func (s *RoadPavingSkill) Interruptible() bool {
	return true
}

func (s *RoadPavingSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, nil
	}

	tracker := ctx.PavingTracker
	if tracker == nil && ctx.State != nil {
		tracker = ctx.State.GetPavingTracker()
	}
	if tracker == nil {
		return nil, nil
	}

	if ctx.State != nil && tracker.GetHomeOrigin() == nil {
		if home := ctx.State.GetHomeOrigin(); home != nil {
			tracker.SetHomeOrigin(home)
		}
	}

	intent := tracker.EvaluatePaving(ctx.AgentID, ctx.Frame)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *RoadPavingSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}
