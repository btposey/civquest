package system1

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// BootstrapGatherSkill gathers timber logs when survival kit is incomplete and log count is low.
type BootstrapGatherSkill struct{}

// NewBootstrapGatherSkill creates a BootstrapGatherSkill instance.
func NewBootstrapGatherSkill() *BootstrapGatherSkill {
	return &BootstrapGatherSkill{}
}

func (s *BootstrapGatherSkill) Name() string {
	return "bootstrap_gather"
}

func (s *BootstrapGatherSkill) Category() string {
	return "bootstrap"
}

func (s *BootstrapGatherSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	if facts.GetBool("bootstrap.has_kit") || facts.GetBool("bootstrap.on_cooldown") {
		return false
	}
	if facts.GetBool("task.active") {
		kind := facts.GetString("task.kind")
		if kind == "harvest_tree" || kind == "collect_items" {
			return true
		}
	}
	logCount, _ := facts.GetInt("bootstrap.log_count")
	return logCount < 4
}

func (s *BootstrapGatherSkill) Interruptible() bool {
	return true
}

func (s *BootstrapGatherSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *BootstrapGatherSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

// BootstrapCraftSkill crafts crafting tables, basic tools, and campfire when materials are available.
type BootstrapCraftSkill struct{}

// NewBootstrapCraftSkill creates a BootstrapCraftSkill instance.
func NewBootstrapCraftSkill() *BootstrapCraftSkill {
	return &BootstrapCraftSkill{}
}

func (s *BootstrapCraftSkill) Name() string {
	return "bootstrap_craft"
}

func (s *BootstrapCraftSkill) Category() string {
	return "bootstrap"
}

func (s *BootstrapCraftSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	if facts.GetBool("bootstrap.has_kit") || facts.GetBool("bootstrap.on_cooldown") {
		return false
	}
	if facts.GetBool("task.active") {
		kind := facts.GetString("task.kind")
		if kind == "harvest_tree" || kind == "collect_items" {
			return false
		}
	}
	logCount, _ := facts.GetInt("bootstrap.log_count")
	plankCount, _ := facts.GetInt("bootstrap.plank_count")
	stickCount, _ := facts.GetInt("bootstrap.stick_count")
	coalCount, _ := facts.GetInt("bootstrap.coal_count")
	return logCount >= 1 || plankCount >= 4 || (stickCount >= 1 && coalCount >= 1)
}

func (s *BootstrapCraftSkill) Interruptible() bool {
	return true
}

func (s *BootstrapCraftSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

func (s *BootstrapCraftSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}

// ShelterConstructionSkill builds forward outpost cabin shelter and places campfires during dusk/night.
type ShelterConstructionSkill struct{}

// NewShelterConstructionSkill creates a ShelterConstructionSkill instance.
func NewShelterConstructionSkill() *ShelterConstructionSkill {
	return &ShelterConstructionSkill{}
}

func (s *ShelterConstructionSkill) Name() string {
	return "shelter_construction"
}

func (s *ShelterConstructionSkill) Category() string {
	return "shelter"
}

func (s *ShelterConstructionSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}
	if facts.GetBool("shelter.complete") || facts.GetBool("shelter.on_cooldown") {
		return false
	}
	isDuskOrNight := facts.GetBool("circadian.is_dusk_or_night")
	routine := facts.GetString("circadian.routine")
	isSleepRoutine := (routine == "SLEEP" || routine == string(RoutineSleep))
	return isDuskOrNight || isSleepRoutine
}

func (s *ShelterConstructionSkill) Interruptible() bool {
	return true
}

func (s *ShelterConstructionSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Shelter)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}

// OnComplete does not write facts: shelter.complete is derived by ShelterEvaluator each tick from AgentState.
func (s *ShelterConstructionSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {}
