package system1

import (
	"fmt"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const (
	activityKindForaging           = "foraging"
	activityKindShelter            = "shelter"
	activityKindTaskSubroutine     = "task_subroutine"
	activityKindStrategicDirective = "strategic_directive"
	activityKindSurveyExplorer     = "survey_explorer"
	activityKindBootstrap          = "bootstrap"
	activityKindExploration        = "exploration"
)

// SurveyExplorerSkill executes ADR-032 chained 360-degree horizon survey and expedition traversal.
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
	if ctx.State.Stage() == StageSettlement && ctx.State.GetExpeditionPhase() == ExpeditionIdle {
		return nil, nil
	}
	intent := EvaluateHorizonExplorer(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
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
	intent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config)
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}
func (s *StrategicDirectiveSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}

// TaskSubroutineSkill steps the active committed TaskSubroutine on AgentState.
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
	res := ctx.State.StepActiveTask(ctx.AgentID, ctx.Frame)
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
	if (prof == "EXPLORER" || prof == "TRAILBLAZER" || prof == "SCOUT") && routine == RoutinePrimaryDuty {
		intent = EvaluateScouting(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
	} else {
		intent = EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State)
	}
	if intent == nil {
		return nil, nil
	}
	return []*civquestv1.AgentIntent{intent}, nil
}
func (s *PatrolExplorationSkill) OnComplete(_ *SkillContext, _ *FactGraph) {}

type activitySpec struct {
	kind               string
	skillName          string
	priority           float64
	ownsStuck          bool
	eligible           func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry) bool
	onStart            func(ctx *FSMContext)
	onResume           func(ctx *FSMContext)
	tick               func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool)
	fallThroughOnEmpty func(fsm *AgentFSM, ctx *FSMContext) bool
}

type managedActivity struct {
	id        string
	kind      string
	priority  float64
	ownsStuck bool
	scope     ActivityReceiptScope
	spec      *activitySpec
	reg       *EngineRegistry
}

func newManagedActivity(id string, spec *activitySpec, reg *EngineRegistry) *managedActivity {
	return &managedActivity{
		id:        id,
		kind:      spec.kind,
		priority:  spec.priority,
		ownsStuck: spec.ownsStuck,
		scope:     ActivityReceiptScope{ActivityID: id},
		spec:      spec,
		reg:       reg,
	}
}

func (a *managedActivity) ID() string                          { return a.id }
func (a *managedActivity) Kind() string                        { return a.kind }
func (a *managedActivity) ReceiptScope() *ActivityReceiptScope { return &a.scope }
func (a *managedActivity) OwnsStuckRecovery() bool             { return a.ownsStuck }

func (a *managedActivity) OnStart(ctx *FSMContext) {
	var lastReceipt *civquestv1.IntentReceipt
	if ctx != nil && ctx.State != nil {
		lastReceipt = ctx.State.GetLastReceipt()
	}
	a.scope.Anchor(lastReceipt)
	if a.spec != nil && a.spec.onStart != nil {
		a.spec.onStart(ctx)
	}
}

func (a *managedActivity) OnTick(ctx *FSMContext) ([]FSMIntent, bool) {
	if a.spec == nil || ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil, true
	}
	reg := a.reg
	if ctx.Registry != nil {
		reg = ctx.Registry
	}
	if reg == nil {
		reg = resolveContextRegistry(ctx)
	}
	ensureFSMFacts(ctx, reg)
	skillCtx := buildSkillContext(ctx, reg)
	stage := StageSettlement
	if ctx.State != nil {
		stage = ctx.State.Stage()
	}
	routine := RoutinePrimaryDuty
	if ctx.State != nil {
		routine = ctx.State.Routine()
	}
	tmpFSM := &AgentFSM{currentStage: stage, currentRoutine: routine, registry: reg}
	return a.spec.tick(tmpFSM, ctx, skillCtx, reg, &a.scope)
}

func (a *managedActivity) OnSuspend(_ *FSMContext, _ string) {}

func (a *managedActivity) OnResume(ctx *FSMContext) {
	var lastReceipt *civquestv1.IntentReceipt
	if ctx != nil && ctx.State != nil {
		lastReceipt = ctx.State.GetLastReceipt()
	}
	a.scope.Anchor(lastReceipt)
	if a.spec != nil && a.spec.onResume != nil {
		a.spec.onResume(ctx)
	}
}

func (a *managedActivity) OnExit(_ *FSMContext) {}

// ActivityArbiter unifies the 7 System 1 sub-behaviors onto AgentFSM's single-owner HTPDA activity stack.
type ActivityArbiter struct {
	specs []activitySpec
	seq   int64
}

func newActivityArbiter() *ActivityArbiter {
	return &ActivityArbiter{
		specs: []activitySpec{
			{
				kind:      activityKindForaging,
				skillName: "foraging",
				priority:  0.95,
				ownsStuck: true,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, reg *EngineRegistry) bool {
					return isForagingActivityEligible(fsm.currentStage, fsm.currentRoutine, ctx, reg)
				},
				onStart: func(ctx *FSMContext) {
					if ctx != nil && ctx.State != nil {
						ctx.State.ClearExpeditionSurveyingIfActive()
						if ctx.State.HasActiveTask() && ctx.Frame != nil {
							ctx.State.PauseActiveTask(ctx.Frame.WorldTick)
						}
					}
				},
				tick: func(_ *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					si := executeRegisteredSkill(reg, "foraging", NewForagingSkill(), skillCtx, ctx.Facts)
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(_ *AgentFSM, _ *FSMContext) bool { return false },
			},
			{
				kind:      activityKindShelter,
				skillName: "shelter_construction",
				priority:  0.85,
				ownsStuck: true,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, _ *EngineRegistry) bool {
					return isShelterActivityEligible(fsm.currentStage, fsm.currentRoutine, ctx, skillCtx)
				},
				tick: func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					var si []*civquestv1.AgentIntent
					if fsm.currentStage == StageBootstrap {
						if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "shelter"); ok && len(skillIntents) > 0 {
							si = appendNonNilIntents(nil, skillIntents)
						}
					}
					if len(si) == 0 {
						if skill, ok := reg.GetSkill("shelter_construction"); ok && skill != nil && skill.Preconditions(ctx.Facts) {
							if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
								si = appendNonNilIntents(nil, out)
							}
						}
					}
					if len(si) == 0 {
						if intent := EvaluateShelterGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); intent != nil {
							si = []*civquestv1.AgentIntent{intent}
						}
					}
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(fsm *AgentFSM, _ *FSMContext) bool {
					return fsm.currentStage != StageColonize
				},
			},
			{
				kind:      activityKindTaskSubroutine,
				skillName: "task_subroutine",
				priority:  0.75,
				ownsStuck: true,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, _ *EngineRegistry) bool {
					return isTaskSubroutineEligible(fsm.currentStage, fsm.currentRoutine, ctx)
				},
				onResume: func(ctx *FSMContext) {
					if ctx != nil && ctx.State != nil && ctx.Frame != nil {
						ctx.State.ResumeActiveTask(ctx.Frame.WorldTick)
					}
				},
				tick: func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					return tickTaskSubroutineActivity(fsm.currentStage, ctx, skillCtx, reg, scope)
				},
				fallThroughOnEmpty: func(fsm *AgentFSM, ctx *FSMContext) bool {
					return ctx.State == nil || !ctx.State.HasActiveTask()
				},
			},
			{
				kind:      activityKindStrategicDirective,
				skillName: "strategic_directive",
				priority:  0.70,
				ownsStuck: false,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, _ *EngineRegistry) bool {
					return isStrategicDirectiveEligible(fsm.currentStage, fsm.currentRoutine, ctx)
				},
				tick: func(_ *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					si := executeRegisteredSkill(reg, "strategic_directive", NewStrategicDirectiveSkill(), skillCtx, ctx.Facts)
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(_ *AgentFSM, _ *FSMContext) bool { return true },
			},
			{
				kind:      activityKindSurveyExplorer,
				skillName: "survey_explorer",
				priority:  0.65,
				ownsStuck: true,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, _ *EngineRegistry) bool {
					return isSurveyExplorerEligible(fsm.currentStage, fsm.currentRoutine, ctx)
				},
				tick: func(_ *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					if ctx.State != nil && ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
						return nil, false
					}
					si := executeRegisteredSkill(reg, "survey_explorer", NewSurveyExplorerSkill(), skillCtx, ctx.Facts)
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(_ *AgentFSM, _ *FSMContext) bool { return true },
			},
			{
				kind:      activityKindBootstrap,
				skillName: "bootstrap_gather",
				priority:  0.60,
				ownsStuck: false,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, _ *EngineRegistry) bool {
					return isBootstrapActivityEligible(fsm.currentStage, fsm.currentRoutine, ctx)
				},
				tick: func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					var si []*civquestv1.AgentIntent
					if fsm.currentStage == StageBootstrap {
						if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "bootstrap"); ok && len(skillIntents) > 0 {
							si = appendNonNilIntents(nil, skillIntents)
						} else if ctx.State == nil || !ctx.State.HasActiveTask() {
							if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); intent != nil {
								si = []*civquestv1.AgentIntent{intent}
							}
						}
					} else {
						if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); intent != nil {
							si = []*civquestv1.AgentIntent{intent}
						}
					}
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(fsm *AgentFSM, _ *FSMContext) bool {
					return fsm.currentStage == StageColonize
				},
			},
			{
				kind:      activityKindExploration,
				skillName: "patrol_exploration",
				priority:  0.40,
				ownsStuck: false,
				eligible: func(fsm *AgentFSM, ctx *FSMContext, _ *SkillContext, _ *EngineRegistry) bool {
					return isExplorationActivityEligible(fsm.currentStage, fsm.currentRoutine, ctx)
				},
				tick: func(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
					si := tickExplorationActivity(fsm.currentStage, fsm.currentRoutine, ctx, skillCtx, reg)
					if len(si) == 0 {
						return nil, true
					}
					scope.RecordEmit()
					return wrapStageIntents(si, false), false
				},
				fallThroughOnEmpty: func(_ *AgentFSM, _ *FSMContext) bool { return false },
			},
		},
	}
}

func (fsm *AgentFSM) tickActivityArbiterLocked(ctx *FSMContext) []FSMIntent {
	if fsm == nil || ctx == nil {
		return nil
	}
	// Preserve strict Tier 1 exclusivity for manually pushed custom unmanaged Activities.
	if len(fsm.activityStack) > 0 && !fsm.activitySuspended {
		if top := fsm.activityStack[len(fsm.activityStack)-1]; top != nil {
			if _, isManaged := top.(*managedActivity); !isManaged {
				intents, done := top.OnTick(ctx)
				if done {
					fsm.popActivityLocked(ctx)
				}
				return intents
			}
		}
	}
	if ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil
	}

	reg := resolveContextRegistry(ctx)
	ensureFSMFacts(ctx, reg)
	skillCtx := buildSkillContext(ctx, reg)

	intents := wrapStageIntents(executeStageBiologicalNeeds(skillCtx, ctx.Facts, reg), false)
	intents = append(intents, wrapStageIntents(executeStageRoadPaving(skillCtx, ctx.Facts, reg, fsm.currentStage != StageBootstrap), false)...)

	if fsm.arbiter == nil {
		fsm.arbiter = newActivityArbiter()
	}
	actIntents := fsm.arbiter.tickLocked(fsm, ctx, skillCtx, reg)
	if len(actIntents) > 0 {
		intents = append(intents, actIntents...)
	}
	return intents
}

func (a *ActivityArbiter) tickLocked(fsm *AgentFSM, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry) []FSMIntent {
	skipped := make(map[string]bool, len(a.specs))

	for len(fsm.activityStack) > 0 && !fsm.activitySuspended {
		tm, ok := fsm.activityStack[len(fsm.activityStack)-1].(*managedActivity)
		if !ok || tm == nil {
			break
		}
		if tm.spec.eligible(fsm, ctx, skillCtx, reg) {
			break
		}
		fsm.popActivityLocked(ctx)
	}

	for {
		var topManaged *managedActivity
		var topPriority float64
		if len(fsm.activityStack) > 0 && !fsm.activitySuspended {
			if tm, ok := fsm.activityStack[len(fsm.activityStack)-1].(*managedActivity); ok && tm != nil {
				topManaged = tm
				topPriority = tm.priority
			}
		}

		var nextSpec *activitySpec
		for i := range a.specs {
			sp := &a.specs[i]
			if skipped[sp.kind] {
				continue
			}
			if topManaged != nil && sp.priority <= topPriority {
				break
			}
			if sp.kind == activityKindShelter && topManaged != nil && !hasShelterWorkOrMaterials(ctx) {
				continue
			}
			if sp.eligible(fsm, ctx, skillCtx, reg) {
				nextSpec = sp
				break
			}
		}

		if nextSpec != nil {
			a.seq++
			id := fmt.Sprintf("%s-%d", nextSpec.kind, a.seq)
			act := newManagedActivity(id, nextSpec, reg)
			fsm.pushActivityLocked(act, ctx)
			topManaged = act
		} else if topManaged == nil {
			return nil
		}

		if !topManaged.spec.eligible(fsm, ctx, skillCtx, reg) {
			skipped[topManaged.kind] = true
			fsm.popActivityLocked(ctx)
			continue
		}

		actIntents, done := topManaged.spec.tick(fsm, ctx, skillCtx, reg, &topManaged.scope)
		if done {
			skipped[topManaged.kind] = true
			fsm.popActivityLocked(ctx)
			if len(actIntents) > 0 || !topManaged.spec.fallThroughOnEmpty(fsm, ctx) {
				return actIntents
			}
			continue
		}
		return actIntents
	}
}

func executeRegisteredSkill(reg *EngineRegistry, name string, fallback Skill, skillCtx *SkillContext, facts *FactGraph) []*civquestv1.AgentIntent {
	skill := fallback
	if reg != nil {
		if s, ok := reg.GetSkill(name); ok && s != nil {
			skill = s
		}
	}
	if skill == nil {
		return nil
	}
	if facts != nil && !skill.Preconditions(facts) {
		return nil
	}
	si, err := skill.Execute(skillCtx, facts)
	if err != nil {
		return nil
	}
	return appendNonNilIntents(nil, si)
}

func isForagingActivityEligible(stage Stage, routine Routine, ctx *FSMContext, reg *EngineRegistry) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Facts == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if stage == StageSettlement && routine == RoutineArmoryMaintenance {
		return false
	}
	if ctx.IsDuskOrNight() && ctx.Frame.Status.FoodLevel > 6 {
		return false
	}
	var fSkill Skill = NewForagingSkill()
	if reg != nil {
		if s, ok := reg.GetSkill("foraging"); ok && s != nil {
			fSkill = s
		}
	}
	return fSkill.Preconditions(ctx.Facts)
}

func isShelterActivityEligible(stage Stage, routine Routine, ctx *FSMContext, skillCtx *SkillContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if skillHasAssignedBed(skillCtx, ctx.Facts) || skillIsShelterComplete(skillCtx, ctx.Facts) || skillIsShelterOnCooldown(skillCtx, ctx.Facts) {
		return false
	}
	if stage == StageBootstrap {
		if !skillIsDuskOrNight(skillCtx, ctx.Facts) {
			return false
		}
		return !shouldDeferShelterForHarvest(ctx)
	}
	return skillIsDuskOrNight(skillCtx, ctx.Facts) || routine == RoutineSleep
}

func shouldDeferShelterForHarvest(ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if !ctx.State.HasActiveTask() {
		return false
	}
	_, kind, _, _ := ctx.State.ActiveTaskInfo()
	if kind != "harvest_tree" && kind != "collect_items" {
		return false
	}
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	return inv.buildingBlockCount == 0 && !inv.hasLight
}

func hasShelterWorkOrMaterials(ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if ctx.State != nil && ctx.State.GetShelterBuildIndex() > 0 {
		return true
	}
	inv := inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	if inv.logCount > 0 || inv.planksCount > 0 || inv.buildingBlockCount > 0 || inv.hasLight {
		return true
	}
	for _, b := range ctx.Frame.InterestingBlocks {
		if b == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem") {
			return true
		}
	}
	return false
}

func isColonizeNightShelterGated(routine Routine, ctx *FSMContext) bool {
	if ctx == nil {
		return false
	}
	return (ctx.IsDuskOrNight() || routine == RoutineSleep) &&
		!ctx.HasAssignedBed() && !ctx.IsShelterComplete() && !ctx.IsShelterOnCooldown()
}

func isBootstrapWindowOpen(routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	shelterSatisfied := ctx.HasAssignedBed() || ctx.IsShelterComplete()
	return !shelterSatisfied || (!ctx.IsDuskOrNight() && routine != RoutineSleep && routine != RoutineTavernSocial)
}

func isTaskSubroutineEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if !ctx.State.HasActiveTask() || len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil {
		return false
	}
	if stage == StageBootstrap {
		return isBootstrapWindowOpen(routine, ctx)
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		if !ctx.IsDuskOrNight() && routine != RoutineSleep && isStrategicDirectiveEligible(stage, routine, ctx) {
			return false
		}
	}
	return true
}

func tickTaskSubroutineActivity(stage Stage, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry, scope *ActivityReceiptScope) ([]FSMIntent, bool) {
	ctx.State.ResumeActiveTask(ctx.Frame.WorldTick)
	_, kind, _, _ := ctx.State.ActiveTaskInfo()

	if stage == StageBootstrap || stage == StageColonize {
		if kind == "harvest_tree" && stage == StageBootstrap && ctx.Facts != nil && ctx.Facts.GetBool("system2.directive_active") &&
			(HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)) {
			dir, dirActive := ctx.State.ActiveStrategicDirective(ctx.Frame.WorldTick)
			task := ctx.State.GetActiveTask()
			if dirActive && task != nil && dir.Action != DirectiveActionGatherTimber &&
				(dir.Action == DirectiveActionScoutFrontier ||
					dir.Action == DirectiveActionMineStone ||
					dir.Action == DirectiveActionSocialize ||
					dir.Action == DirectiveActionBuildShelter ||
					(dir.Action == DirectiveActionForageFood && NeedsForaging(ctx.Frame))) {
				isStuckOrStalled := ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK ||
					task.ConsecutiveFailures > 0 ||
					(ctx.Frame.WorldTick >= task.LastProgressTick && ctx.Frame.WorldTick-task.LastProgressTick >= 40)
				if dir.IssuedTick >= task.StartedTick || isStuckOrStalled {
					if dirIntent := EvaluateStrategicDirectiveWithID(ctx.AgentID, ctx.Frame, ctx.State, ctx.Config); dirIntent != nil {
						ctx.State.ClearActiveTask()
						scope.RecordEmit()
						return wrapStageIntents([]*civquestv1.AgentIntent{dirIntent}, false), true
					} else if dir.Action == DirectiveActionScoutFrontier && ctx.State.IsExpeditionActive() {
						ctx.State.ClearActiveTask()
						return nil, true
					}
				}
			}
		}

		if kind == "harvest_tree" || kind == "collect_items" || stage == StageColonize {
			var si []*civquestv1.AgentIntent
			if skill, ok := reg.GetSkill("bootstrap_gather"); ok && skill != nil && skill.Preconditions(ctx.Facts) {
				if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
					si = appendNonNilIntents(nil, out)
				}
			}
			if len(si) == 0 {
				if intent := EvaluateBootstrapGoals(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); intent != nil {
					si = []*civquestv1.AgentIntent{intent}
				}
			}
			done := !ctx.State.HasActiveTask()
			if len(si) > 0 {
				scope.RecordEmit()
				return wrapStageIntents(si, false), done
			}
			return nil, done
		}
	}

	var intent *civquestv1.AgentIntent
	if skill, ok := reg.GetSkill("task_subroutine"); ok && skill != nil && (ctx.Facts == nil || skill.Preconditions(ctx.Facts)) {
		if out, err := skill.Execute(skillCtx, ctx.Facts); err == nil {
			out = appendNonNilIntents(nil, out)
			if len(out) > 0 {
				intent = out[0]
			}
		}
	} else {
		stepRes := ctx.State.StepActiveTask(ctx.AgentID, ctx.Frame)
		intent = stepRes.Intent
	}
	done := !ctx.State.HasActiveTask()
	if intent != nil {
		scope.RecordEmit()
		return wrapStageIntents([]*civquestv1.AgentIntent{intent}, false), done
	}
	return nil, done
}

func isStrategicDirectiveEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Facts == nil {
		return false
	}
	if !ctx.Facts.GetBool("system2.directive_active") {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	switch stage {
	case StageBootstrap:
		if !isBootstrapWindowOpen(routine, ctx) {
			return false
		}
		return HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)
	case StageColonize:
		if isColonizeNightShelterGated(routine, ctx) || ctx.IsDuskOrNight() || routine == RoutineSleep {
			return false
		}
		return HasSurvivalKit(ctx.Frame.Status.Inventory) || !canExecuteImmediateBootstrapCraft(ctx.Frame, ctx.State)
	default:
		return routine != RoutineSleep && routine != RoutineArmoryMaintenance && !ctx.IsDuskOrNight()
	}
}

func isSurveyExplorerEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if stage == StageBootstrap {
		return false
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		if ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
			return true
		}
		if !HasSurvivalKit(ctx.Frame.Status.Inventory) {
			return false
		}
		phase := ctx.State.GetExpeditionPhase()
		return phase != ExpeditionIdle && phase != ExpeditionHomesteadClaimed
	}
	if routine == RoutineSleep || routine == RoutineArmoryMaintenance {
		return false
	}
	if ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick) {
		return true
	}
	phase := ctx.State.GetExpeditionPhase()
	return routine == RoutinePrimaryDuty && phase != ExpeditionIdle && phase != ExpeditionHomesteadClaimed
}

func isBootstrapActivityEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
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

func isExplorationActivityEligible(stage Stage, routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	if stage == StageColonize {
		if isColonizeNightShelterGated(routine, ctx) {
			return false
		}
		return ctx.State == nil || !ctx.State.IsExpeditionSurveying(ctx.Frame.WorldTick)
	}
	if stage != StageSettlement {
		return false
	}
	switch routine {
	case RoutinePrimaryDuty, RoutineCivicPatrol, RoutineArmoryMaintenance, RoutineSecondaryHobby, RoutineSleep:
		return true
	default:
		return false
	}
}

func tickExplorationActivity(stage Stage, routine Routine, ctx *FSMContext, skillCtx *SkillContext, reg *EngineRegistry) []*civquestv1.AgentIntent {
	if stage == StageColonize {
		if surveyIntents := executeRegisteredSkill(reg, "survey_explorer", NewSurveyExplorerSkill(), skillCtx, ctx.Facts); len(surveyIntents) > 0 {
			return surveyIntents
		}
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
		return nil
	}

	switch routine {
	case RoutinePrimaryDuty:
		if ctx.State != nil && ctx.State.GetExpeditionPhase() != ExpeditionIdle {
			if surveyIntents := executeRegisteredSkill(reg, "survey_explorer", NewSurveyExplorerSkill(), skillCtx, ctx.Facts); len(surveyIntents) > 0 {
				return surveyIntents
			}
		}
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if si := executeRegisteredSkill(reg, "patrol_exploration", NewPatrolExplorationSkill(), skillCtx, ctx.Facts); len(si) > 0 {
			return si
		}
	case RoutineCivicPatrol:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "security", "exploration"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	case RoutineArmoryMaintenance:
		if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
			return appendNonNilIntents(nil, skillIntents)
		}
		if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
			return []*civquestv1.AgentIntent{armoryIntent}
		}
		if ctx.State != nil {
			ctx.State.SetArmoryMaintenanceNeeded(false)
		}
		if forageIntents, preempted := tryForagingPreemptionRaw(skillCtx, ctx.Facts, reg); preempted {
			return appendNonNilIntents(nil, forageIntents)
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	case RoutineSecondaryHobby:
		if ctx.Config != nil && ctx.Config.EffectiveShift() == xdg.ShiftThird && ctx.State != nil && ctx.State.IsArmoryMaintenanceNeeded() {
			if skillIntents, _, ok := reg.SelectAndExecuteSkill(skillCtx, ctx.Facts, "Armory"); ok && len(skillIntents) > 0 {
				return appendNonNilIntents(nil, skillIntents)
			}
			if armoryIntent := EvaluateArmoryMaintenance(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State, ctx.CivicDB); armoryIntent != nil {
				return []*civquestv1.AgentIntent{armoryIntent}
			}
		}
		if patrolIntent := EvaluatePatrol(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); patrolIntent != nil {
			return []*civquestv1.AgentIntent{patrolIntent}
		}
	}
	return nil
}
