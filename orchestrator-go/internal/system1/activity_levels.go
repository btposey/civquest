package system1

// Where each activity sits in the goal-driven hierarchy (ADR-040): the one place levels are
// declared.

import "github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"

var activityLevels = map[htpda.Kind]htpda.Level{
	activityKindStrategicDirective: htpda.LevelObjective,
	activityKindForaging:           htpda.LevelObjective,
	activityKindShelter:            htpda.LevelObjective,
	activityKindConstruction:       htpda.LevelObjective,
	activityKindBed:                htpda.LevelObjective,
	activityKindPlan:               htpda.LevelObjective,
	activityKindBootstrap:          htpda.LevelObjective,
	activityKindExploration:        htpda.LevelObjective,
	activityKindExpedition:         htpda.LevelTask,
	activityKindTaskSubroutine:     htpda.LevelTask,
	activityKindWool:               htpda.LevelTask,
	activityKindGather:             htpda.LevelObjective,
	activityKindSurvey:             htpda.LevelStep,
	activityKindNavigate:           htpda.LevelStep,
	activityKindEmergency:          htpda.LevelInterrupt,
	activityKindRecover:            htpda.LevelInterrupt,
	activityKindDead:               htpda.LevelInterrupt,
}

func (b frameBase) Level() htpda.Level        { return activityLevels[b.kind] }
func (expeditionActivity) Level() htpda.Level { return activityLevels[activityKindExpedition] }
func (surveyActivity) Level() htpda.Level     { return activityLevels[activityKindSurvey] }
func (navigateActivity) Level() htpda.Level   { return activityLevels[activityKindNavigate] }
func (emergencyActivity) Level() htpda.Level  { return activityLevels[activityKindEmergency] }
func (recoverActivity) Level() htpda.Level    { return activityLevels[activityKindRecover] }
func (deadActivity) Level() htpda.Level       { return activityLevels[activityKindDead] }
