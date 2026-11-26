package system1

// Skills the waterfall-hosted activities invoke through the registry (survey, directive,
// task subroutine, patrol exploration).

import (
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// SurveyExplorerSkill starts the ADR-032 chained 360-degree horizon survey expedition (an expedition frame).
type SurveyExplorerSkill struct{}

// NewSurveyExplorerSkill creates a SurveyExplorerSkill instance.
func NewSurveyExplorerSkill() *SurveyExplorerSkill { return &SurveyExplorerSkill{} }

func (s *SurveyExplorerSkill) Name() string     { return "survey_explorer" }
func (s *SurveyExplorerSkill) Category() string { return "exploration" }
func (s *SurveyExplorerSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil || facts.GetBool("vitals.is_sleeping") {
		return false
	}
	return facts.GetBool("scouting.active") ||
		facts.GetBool("colonize.is_establishing") ||
		facts.GetString("circadian.routine") == string(RoutinePrimaryDuty)
}
func (s *SurveyExplorerSkill) Interruptible() bool { return true }
func (s *SurveyExplorerSkill) Execute(ctx *SkillContext, _ *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	if ctx.State.Stage() == StageSettlement {
		return nil, nil
	}
	// The expedition runs as a frame: the running walk frame pushes one (a step result, no intents).
	if ctx.Walk != nil {
		ctx.Walk.wantExpedition = true
	}
	return nil, nil
}
func (s *SurveyExplorerSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}

// StrategicDirectiveSkill executes bridged System 2 strategic directives deterministically.
type StrategicDirectiveSkill struct{}

// NewStrategicDirectiveSkill creates a StrategicDirectiveSkill instance.
func NewStrategicDirectiveSkill() *StrategicDirectiveSkill { return &StrategicDirectiveSkill{} }

func (s *StrategicDirectiveSkill) Name() string     { return "strategic_directive" }
func (s *StrategicDirectiveSkill) Category() string { return "directive" }
func (s *StrategicDirectiveSkill) Preconditions(facts *FactGraph) bool {
	return facts != nil && facts.GetBool("system2.directive_active")
}
func (s *StrategicDirectiveSkill) Interruptible() bool { return true }
func (s *StrategicDirectiveSkill) Execute(ctx *SkillContext, _ *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	intent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config, ctx.Directive)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}
func (s *StrategicDirectiveSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}

// TaskSubroutineSkill steps the committed TaskSubroutine bound to the running frame's walk cursor.
type TaskSubroutineSkill struct{}

// NewTaskSubroutineSkill creates a TaskSubroutineSkill instance.
func NewTaskSubroutineSkill() *TaskSubroutineSkill { return &TaskSubroutineSkill{} }

func (s *TaskSubroutineSkill) Name() string     { return "task_subroutine" }
func (s *TaskSubroutineSkill) Category() string { return "task" }
func (s *TaskSubroutineSkill) Preconditions(facts *FactGraph) bool {
	return facts != nil && facts.GetBool("task.active")
}
func (s *TaskSubroutineSkill) Interruptible() bool { return true }
func (s *TaskSubroutineSkill) Execute(ctx *SkillContext, _ *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.State == nil {
		return nil, nil
	}
	res := ctx.Walk.stepTask(ctx.AgentID, ctx.State, ctx.Frame)
	if res.Intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{res.Intent}, nil
}
func (s *TaskSubroutineSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}

// PatrolExplorationSkill executes frontier scouting for explorers on primary duty and perimeter patrol otherwise.
type PatrolExplorationSkill struct{}

// NewPatrolExplorationSkill creates a PatrolExplorationSkill instance.
func NewPatrolExplorationSkill() *PatrolExplorationSkill { return &PatrolExplorationSkill{} }

func (s *PatrolExplorationSkill) Name() string     { return "patrol_exploration" }
func (s *PatrolExplorationSkill) Category() string { return "exploration" }
func (s *PatrolExplorationSkill) Preconditions(facts *FactGraph) bool {
	return facts != nil
}
func (s *PatrolExplorationSkill) Interruptible() bool { return true }
func (s *PatrolExplorationSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.State == nil {
		return nil, nil
	}
	prof := "UNASSIGNED"
	if ctx.Config != nil {
		prof = strings.ToUpper(strings.TrimSpace(ctx.Config.Profession))
	}
	routine := ctx.State.Routine()
	if facts != nil {
		if r := facts.GetString("circadian.routine"); r != "" {
			routine = Routine(r)
		}
	}
	var intent *civquestv1.AgentIntent
	if travelsTheWild(prof) && routine == RoutinePrimaryDuty {
		intent = EvaluateScouting(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	} else {
		intent = EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.Walk)
	}
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}
func (s *PatrolExplorationSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}
