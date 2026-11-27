package system1

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// StageTransitionRule defines a directed edge for Tier 1 macro stage transitions evaluated in priority order.
type StageTransitionRule struct {
	FromStage   Stage
	ToStage     Stage
	Condition   func(ctx *FSMContext) bool
	Description string
}

func defaultTransitionRules() []StageTransitionRule {
	return []StageTransitionRule{
		// Priority 1: EMERGENCY Preemption from all non-emergency stages
		{
			FromStage: StageBootstrap,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during bootstrap",
		},
		{
			FromStage: StageSettlement,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during settlement living",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageEmergency,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.is_emergency")
				}
				return c != nil && c.IsEmergency()
			},
			Description: "Hostile threat, damage, or critical vitals detected during colonization",
		},

		// Priority 2: Transitions OUT of EMERGENCY (Strictly guarded by ThreatsCleared)
		{
			FromStage: StageEmergency,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.ThreatsCleared() && c.IsEstablishingNewSettlement()
			},
			Description: "Threats cleared; resuming settlement colonization",
		},
		{
			FromStage: StageEmergency,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.ThreatsCleared() && !c.HasSurvivalKit()
			},
			Description: "Threats cleared; resuming bootstrap tool gathering",
		},
		{
			FromStage: StageEmergency,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("threat.threats_cleared") && c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.ThreatsCleared() && c.HasSurvivalKit() && !c.IsEstablishingNewSettlement()
			},
			Description: "Threats cleared; resuming established settlement living",
		},

		// Priority 3: Transitions from BOOTSTRAP
		{
			FromStage: StageBootstrap,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("bootstrap.has_kit") && c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.HasSurvivalKit() && c.IsEstablishingNewSettlement()
			},
			Description: "Survival kit acquired; advancing to colonization",
		},
		{
			FromStage: StageBootstrap,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("bootstrap.has_kit") && !c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.HasSurvivalKit() && !c.IsEstablishingNewSettlement()
			},
			Description: "Survival kit acquired (axe, sword, light); advancing to settlement living",
		},

		// Priority 4: Transitions from SETTLEMENT
		{
			FromStage: StageSettlement,
			ToStage:   StageColonize,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return c.Facts.GetBool("colonize.is_establishing")
				}
				return c != nil && c.IsEstablishingNewSettlement()
			},
			Description: "Founding new settlement node; advancing to colonization",
		},
		{
			FromStage: StageSettlement,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Survival kit broken or missing; reverting to bootstrap",
		},

		// Priority 5: Transitions from COLONIZE
		{
			FromStage: StageColonize,
			ToStage:   StageSettlement,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("colonize.is_establishing") && c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && !c.IsEstablishingNewSettlement() && c.HasSurvivalKit()
			},
			Description: "Colonization complete and kit secured; transitioning to settlement living",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("colonize.is_establishing") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && !c.IsEstablishingNewSettlement() && !c.HasSurvivalKit()
			},
			Description: "Colony site cancelled or tools broken; reverting to bootstrap",
		},
		{
			FromStage: StageColonize,
			ToStage:   StageBootstrap,
			Condition: func(c *FSMContext) bool {
				if c != nil && c.Facts != nil {
					return !c.Facts.GetBool("circadian.is_dusk_or_night") && !c.Facts.GetBool("bootstrap.has_kit")
				}
				return c != nil && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Survival kit broken or missing; reverting to bootstrap",
		},
	}
}

func executeStageSkill(ctx *FSMContext, skillName string, fallback Skill, emergency bool) []FSMIntent {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return nil
	}
	reg := resolveContextRegistry(ctx)
	ensureFSMFacts(ctx, reg)
	skillCtx := buildSkillContext(ctx, reg)
	skill := fallback
	if s, ok := reg.GetSkill(skillName); ok && s != nil {
		skill = s
	}
	if skill == nil {
		return nil
	}
	raw, err := skill.Execute(skillCtx, ctx.Facts)
	if err != nil {
		return nil
	}
	return wrapStageIntents(raw, emergency)
}

// ActivityFrameView is a read-only snapshot of one frame of the agent's activity stack
// (viewer / diagnostics). The stack itself is the htpda.Kernel owned by AgentFSM.
type ActivityFrameView struct {
	ID        uint64 `json:"id"`
	Kind      string `json:"kind"`
	Band      uint8  `json:"band"`
	Parent    uint64 `json:"parent,omitempty"`
	StartTick int64  `json:"start_tick"`
}

// FSMResult encapsulates the output of an FSM tick in the Two-Tier HFSM.
type FSMResult struct {
	Stage               Stage
	Routine             Routine
	PreviousStage       Stage
	PreviousRoutine     Routine
	StageTransitioned   bool
	RoutineTransitioned bool
	Transitioned        bool
	FlushPeriphery      bool
	State               State // Backward compatibility alias for Stage
	PreviousState       State // Backward compatibility alias for PreviousStage
	TransitionReason    string
	Intents             []FSMIntent
	GoalHierarchy       *GoalHierarchy
	CombatResult        *ReflexResult
	Facts               *FactGraph
}

// AgentFSM manages the formal Two-Tier Hierarchical Finite State Machine for System 1.
type AgentFSM struct {
	mu                  sync.RWMutex
	currentStage        Stage
	previousStage       Stage
	currentRoutine      Routine
	previousRoutine     Routine
	stateEnteredTick    int64
	routineEnteredTick  int64
	stuckTicks          int64
	lastPos             *civquestv1.Vec3
	transitions         []StageTransitionRule
	onStageTransition   func(from, to Stage, reason string)
	onRoutineTransition func(from, to Routine, reason string)
	// kernel is the ONE activity stack (ADR-036). Preemption is a push, resume is a pop.
	kernel      *htpda.Kernel
	managed     []waterfallActivity // waterfall activities, highest priority first
	startedTask *TaskSubroutine     // see StartTask; consumed by the task activity's Init
	// startedReceipt is the receipt routed to startedTask while it was an inline-started task
	// whose starting frame exited before the kernel framed it (handOffPendingTask).
	startedReceipt *civquestv1.IntentReceipt
	// rcptBatch/rcptStatus: newest receipt admitted by the seam guard (admitReceiptLocked).
	rcptBatch  int64
	rcptStatus civquestv1.IntentReceipt_ReceiptStatus
	lastFacts  *FactGraph
	registry   *EngineRegistry
	// rendezvousOutbox holds RendezvousReport events from returned expeditions until the
	// runtime drains them (DrainRendezvousReports).
	rendezvousOutbox []RendezvousReport
	// navTargets is the last Navigate target each live frame emitted (the target a Recover
	// frame pushed by that frame is recovering); pruned to the live stack after every dispatch.
	navTargets map[htpda.FrameID][3]float64
	// recoverOutbox holds Recover give-ups until the runtime drains them (POI decay).
	recoverOutbox []RecoverGiveUp
	// stuckLog counts recent stuck episodes per navigation target (recover_activity.go).
	stuckLog []stuckRecord
	// orphan is a stuck receipt for an intent no live frame owns (unframed reflex skills);
	// Recover bids for it as a root frame. orphanNav is the last unframed Navigate target.
	escapeLock   *recoverEscapeLock // enclosed dig heading kept across Recover episodes
	orphan       *recoverArgs
	orphanTick   int64
	orphanNav    [3]float64
	hasOrphanNav bool
	lastDiag     []htpda.Diagnostic
	// surveyBackoffUntil suppresses expeditions after repeated report-less survey timeouts.
	surveyBackoffUntil int64
	// workBackoffUntil suppresses activities that recently found nothing to do.
	workBackoffUntil map[htpda.Kind]int64
	gatherSkips      map[string]int64            // finds gathering gave up on, until a tick
	meter            stackMeter                  // ADR-040 measurements (shape_meter.go)
	walks            map[htpda.FrameID]walkWatch // walk_policy.go
	spots            []gaveUpHere                // where recoveries gave up (trapped.go)
	planning         planningMemory              // spatial Memory + banked plan (plan_memory.go, ADR-039)
	// eventSink receives playtest diagnostics (playtest_events.go); nil disables them.
	eventSink      EventSink
	vitalsEmitted  bool
	lastVitalsTick int64
}

// StartTask commits a task from outside any frame; the task activity pushes it as a task frame.
func (fsm *AgentFSM) StartTask(state *AgentState, t *TaskSubroutine) {
	if fsm == nil || !taskActive(t) {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.startedTask = t
	if state != nil {
		state.publishTask(t)
	}
}

// Registry returns the EngineRegistry bound to this AgentFSM.
func (fsm *AgentFSM) Registry() *EngineRegistry {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.registry
}

// SetRegistry replaces the EngineRegistry bound to this AgentFSM (falling back to NewDefaultEngineRegistry if nil).
func (fsm *AgentFSM) SetRegistry(reg *EngineRegistry) {
	if fsm == nil {
		return
	}
	if reg == nil {
		reg = NewDefaultEngineRegistry()
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.registry = reg
}

// LastFacts returns the most recently evaluated FactGraph from Tick.
func (fsm *AgentFSM) LastFacts() *FactGraph {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.lastFacts
}

// NewAgentFSM constructs an initialized AgentFSM with the stage transition table,
// and an isolated default EngineRegistry.
func NewAgentFSM(initial State, onTransition func(from, to State, reason string)) *AgentFSM {
	return NewAgentFSMWithRegistry(initial, onTransition, nil)
}

// NewAgentFSMWithRegistry constructs an initialized AgentFSM with an explicit EngineRegistry
// (falling back to NewDefaultEngineRegistry() if reg == nil).
func NewAgentFSMWithRegistry(initial State, onTransition func(from, to State, reason string), reg *EngineRegistry) *AgentFSM {
	if initial == "" {
		initial = StageBootstrap
	}
	initialRoutine := RoutineDawnPrep
	if initial == StageEmergency {
		initialRoutine = RoutineEmergency
	} else if initial == StageDead {
		initialRoutine = RoutineDead
	}
	if reg == nil {
		reg = NewDefaultEngineRegistry()
	}
	fsm := &AgentFSM{
		currentStage:      initial,
		previousStage:     initial,
		currentRoutine:    initialRoutine,
		previousRoutine:   initialRoutine,
		transitions:       defaultTransitionRules(),
		onStageTransition: onTransition,
		registry:          reg,
	}
	fsm.managed = newManagedActivities()
	fsm.kernel = newActivityKernel(fsm.managed)
	return fsm
}

// ActivityFrames returns a snapshot of the activity stack, bottom first.
func (fsm *AgentFSM) ActivityFrames() []ActivityFrameView {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if fsm.kernel == nil {
		return nil
	}
	frames := fsm.activities()
	out := make([]ActivityFrameView, 0, len(frames))
	for _, f := range frames {
		out = append(out, ActivityFrameView{ID: uint64(f.ID), Kind: string(f.Kind()), Band: uint8(f.Band), Parent: uint64(f.Parent), StartTick: f.Lease.StartTick})
	}
	return out
}

// ActiveActivityKind returns the kind of the top activity frame, or "" when the stack is empty.
func (fsm *AgentFSM) ActiveActivityKind() string {
	if fsm == nil {
		return ""
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if fsm.kernel == nil {
		return ""
	}
	if frames := fsm.activities(); len(frames) > 0 {
		return string(frames[len(frames)-1].Kind())
	}
	return ""
}

// CurrentStage returns the active Tier 1 macro strategic stage.
func (fsm *AgentFSM) CurrentStage() Stage {
	if fsm == nil {
		return StageBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.currentStage
}

// PreviousStage returns the previous Tier 1 macro strategic stage.
func (fsm *AgentFSM) PreviousStage() Stage {
	if fsm == nil {
		return StageBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.previousStage
}

// CurrentRoutine returns the active Tier 2 circadian operational routine.
func (fsm *AgentFSM) CurrentRoutine() Routine {
	if fsm == nil {
		return RoutineDawnPrep
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.currentRoutine
}

// PreviousRoutine returns the previous Tier 2 circadian operational routine.
func (fsm *AgentFSM) PreviousRoutine() Routine {
	if fsm == nil {
		return RoutineDawnPrep
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.previousRoutine
}

// CurrentState returns the active state (alias for CurrentStage for backward compatibility).
func (fsm *AgentFSM) CurrentState() State {
	return fsm.CurrentStage()
}

// PreviousState returns the previous state (alias for PreviousStage for backward compatibility).
func (fsm *AgentFSM) PreviousState() State {
	return fsm.PreviousStage()
}

// StateTuple returns the unified (Stage, Routine) tuple.
func (fsm *AgentFSM) StateTuple() StateTuple {
	if fsm == nil {
		return StateTuple{Stage: StageBootstrap, Routine: RoutineDawnPrep}
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return StateTuple{Stage: fsm.currentStage, Routine: fsm.currentRoutine}
}

// TransitionTo forces a state transition if the target state's guard allows it.
func (fsm *AgentFSM) TransitionTo(target State, reason string, ctx *FSMContext) bool {
	if fsm == nil {
		return false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	// Invariant: Terminal Dead state cannot be transitioned out of
	if fsm.currentStage == StageDead {
		return false
	}

	if fsm.currentStage == target {
		return false
	}

	// Invariant: Non-emergency target is rejected if emergency conditions are active
	if target != StageEmergency && target != StageDead && ctx != nil && ctx.IsEmergency() {
		return false
	}

	fromStage := fsm.currentStage

	// Emergency preemption is a push on the activity stack: the next Tick's arbitration
	// pushes the emergency frame over whatever runs (no separate stage stack).
	if target == StageEmergency {
		fsm.onEmergencyEntryLocked(ctx)
	}
	// Terminal dead state: unwind the activity stack and drop any committed task.
	if target == StageDead {
		fsm.cancelActivitiesLocked(ctx, reason)
	}

	fsm.previousStage = fromStage
	fsm.currentStage = target
	if ctx != nil && ctx.Frame != nil {
		fsm.stateEnteredTick = ctx.Frame.WorldTick
	}
	fsm.stuckTicks = 0

	if target == StageEmergency {
		fsm.previousRoutine = fsm.currentRoutine
		fsm.currentRoutine = RoutineEmergency
	} else if target == StageDead {
		fsm.previousRoutine = fsm.currentRoutine
		fsm.currentRoutine = RoutineDead
	} else if ctx != nil && ctx.Frame != nil {
		newR := computeStageCircadianRoutine(target, ctx.Frame.TimeOfDay, ctx.Frame.Weather, ctx.Config, ctx.State, ctx)
		if newR != "" {
			fsm.previousRoutine = fsm.currentRoutine
			fsm.currentRoutine = newR
		}
	}

	if fsm.onStageTransition != nil {
		fsm.onStageTransition(fromStage, target, reason)
	}

	if ctx != nil && ctx.State != nil {
		ctx.State.SetStageAndRoutine(target, fsm.currentRoutine)
	}

	return true
}

// Tick executes a single 5 Hz evaluation cycle of the formal Two-Tier HFSM.
func (fsm *AgentFSM) Tick(ctx *FSMContext) FSMResult {
	if fsm == nil {
		return FSMResult{Stage: StageBootstrap, Routine: RoutineDawnPrep, State: StageBootstrap}
	}

	var prevCtxRegistry *EngineRegistry
	var restoreCtxRegistry bool
	if ctx != nil {
		prevCtxRegistry = ctx.Registry
		reg := ctx.Registry
		if reg == nil {
			reg = fsm.Registry()
			if reg == nil {
				reg = NewDefaultEngineRegistry()
			}
			ctx.Registry = reg
			restoreCtxRegistry = true
		}
		// Claim a bed as soon as one is seen, day or night: unclaimed visible beds would
		// otherwise look available to every agent until dusk (cq-390.2).
		if ctx.State != nil && ctx.State.bedReg() != nil {
			ctx.State.ensureOwnBed(ctx.AgentID, ctx.Frame)
		}
		fsm.advanceStrategy(ctx)
		observeDangers(ctx)
		configured := ctx.Config
		ctx.Config = EffectiveIdentity(ctx.State, configured)
		defer func() { ctx.Config = configured }()
		evalCtx := newEvaluatorContext(ctx)
		ctx.Facts = reg.EvaluateAll(evalCtx)
	}
	if restoreCtxRegistry {
		defer func() {
			ctx.Registry = prevCtxRegistry
		}()
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	if ctx != nil {
		fsm.lastFacts = fsm.holdEmergencyFactsLocked(ctx)
	}

	worldTick := int64(0)
	tod := int64(0)
	var weather civquestv1.Weather
	if ctx != nil && ctx.Frame != nil {
		worldTick = ctx.Frame.WorldTick
		tod = ctx.Frame.TimeOfDay
		weather = ctx.Frame.Weather
		if ctx.State != nil && ctx.Frame.GetReceipt() != nil {
			ctx.State.IngestReceipt(ctx.Frame.GetReceipt())
		}
	}

	// 0. Terminal Dead State Check:
	// When ctx.Frame.Status.Health <= 0 or death detected:
	// - clear activities & stack
	// - transition to StageDead / RoutineDead.
	isDead := fsm.currentStage == StageDead || fsm.topIsDeadLocked() || (ctx != nil && frameIsDead(ctx.Frame))

	if isDead {
		// Death dispatches Cancel (unwinding every frame) and the arbiter then pushes the
		// terminal dead frame (BandDead); it is never popped.
		if !fsm.topIsDeadLocked() {
			fsm.cancelActivitiesLocked(ctx, "terminal agent death detected")
			fsm.dispatchLocked(newFSMWorld(fsm, ctx, nil, nil), htpda.Tick{})
		}
		if fsm.currentStage != StageDead {
			fromStage := fsm.currentStage
			fsm.previousStage = fromStage
			fsm.currentStage = StageDead
			fsm.stateEnteredTick = worldTick
			fsm.stuckTicks = 0
			if ctx != nil {
				ctx.TimeInState = 0
				ctx.StuckTicks = 0
			}
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = RoutineDead
			fsm.routineEnteredTick = worldTick

			if fsm.onStageTransition != nil {
				fsm.onStageTransition(fromStage, StageDead, "terminal agent death detected")
			}
			if fsm.onRoutineTransition != nil {
				fsm.onRoutineTransition(prevR, RoutineDead, "terminal agent death detected")
			}
			if ctx != nil && ctx.State != nil {
				ctx.State.SetStageAndRoutine(StageDead, RoutineDead)
			}
			return FSMResult{
				Stage:               StageDead,
				Routine:             RoutineDead,
				PreviousStage:       fromStage,
				PreviousRoutine:     prevR,
				StageTransitioned:   true,
				RoutineTransitioned: true,
				Transitioned:        true,
				FlushPeriphery:      true,
				State:               StageDead,
				PreviousState:       fromStage,
				TransitionReason:    "terminal agent death detected",
				Facts:               fsm.lastFacts,
			}
		}

		if ctx != nil && ctx.State != nil {
			ctx.State.SetStageAndRoutine(StageDead, RoutineDead)
		}
		return FSMResult{
			Stage:           StageDead,
			Routine:         RoutineDead,
			PreviousStage:   fsm.previousStage,
			PreviousRoutine: fsm.previousRoutine,
			State:           StageDead,
			PreviousState:   fsm.previousStage,
			Facts:           fsm.lastFacts,
		}
	}

	// 1. Track position & stuckness
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil && ctx.Frame.Status.Position != nil {
		pos := ctx.Frame.Status.Position
		if fsm.lastPos != nil {
			dx := pos.X - fsm.lastPos.X
			dy := pos.Y - fsm.lastPos.Y
			dz := pos.Z - fsm.lastPos.Z
			if dx*dx+dy*dy+dz*dz < 0.04 || ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
				activeProgress := false
				if ctx.State != nil {
					wt := ctx.Frame.WorldTick
					if (ctx.State.GetLastShelterPlaceTick() > 0 && wt-ctx.State.GetLastShelterPlaceTick() < 100) ||
						(ctx.State.GetLastHarvestTick() > 0 && wt-ctx.State.GetLastHarvestTick() < 100) ||
						(ctx.State.GetLastCraftTick() > 0 && wt-ctx.State.GetLastCraftTick() < 100) {
						activeProgress = true
					}
				}
				if activeProgress {
					fsm.stuckTicks = 0
					fsm.stateEnteredTick = worldTick
				} else {
					fsm.stuckTicks++
				}
			} else {
				fsm.stuckTicks = 0
			}
			fsm.lastPos.X = pos.X
			fsm.lastPos.Y = pos.Y
			fsm.lastPos.Z = pos.Z
		} else {
			fsm.lastPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		}
	}
	if ctx != nil {
		ctx.StuckTicks = fsm.stuckTicks
		if fsm.stateEnteredTick == 0 {
			fsm.stateEnteredTick = worldTick
		}
		ctx.TimeInState = worldTick - fsm.stateEnteredTick
		if ctx.TimeInState < 0 {
			ctx.TimeInState = 0
		}
	}

	// 2. Evaluate Tier 1 Macro Stage Transitions
	stageTransitioned := false
	routineTransitioned := false
	emergencyPreempted := false
	var transitionReason string

	// Priority 1: EMERGENCY preemption from any non-emergency stage. The running activity is
	// suspended by the emergency frame the kernel pushes this tick (step 5).
	if fsm.currentStage != StageEmergency && fsm.currentStage != StageDead && ctx != nil && ctx.IsEmergency() {
		fromStage := fsm.currentStage
		toStage := StageEmergency
		ruleDesc := "Hostile threat, damage, or critical vitals detected; preempting to emergency"
		emergencyPreempted = true
		fsm.onEmergencyEntryLocked(ctx)

		fsm.previousStage = fromStage
		fsm.currentStage = toStage
		fsm.stateEnteredTick = worldTick
		fsm.stuckTicks = 0
		if ctx != nil {
			ctx.TimeInState = 0
			ctx.StuckTicks = 0
		}
		stageTransitioned = true
		transitionReason = ruleDesc

		if fsm.onStageTransition != nil {
			fsm.onStageTransition(fromStage, toStage, ruleDesc)
		}
	}

	// Leaving EMERGENCY is an ordinary derived stage transition (rules below); the emergency
	// frame pops this Tick (ThreatsCleared) and the suspended frame resumes with its locals intact.
	if !stageTransitioned {
		for _, rule := range fsm.transitions {
			if rule.FromStage != fsm.currentStage || !rule.Condition(ctx) {
				continue
			}
			fromStage := fsm.currentStage
			fsm.previousStage = fromStage
			fsm.currentStage = rule.ToStage
			fsm.stateEnteredTick = worldTick
			fsm.stuckTicks = 0
			if ctx != nil {
				ctx.TimeInState = 0
				ctx.StuckTicks = 0
			}
			stageTransitioned = true
			transitionReason = rule.Description
			if fsm.onStageTransition != nil {
				fsm.onStageTransition(fromStage, rule.ToStage, rule.Description)
			}
			break
		}
	}

	// 3. Evaluate Tier 2 Circadian Operational Routine
	{
		var newRoutine Routine
		if fsm.currentStage == StageEmergency {
			newRoutine = RoutineEmergency
		} else if fsm.currentStage == StageDead {
			newRoutine = RoutineDead
		} else if fsm.currentStage == Stage("DIALOGUE") || fsm.currentRoutine == Routine("DIALOGUE") {
			newRoutine = fsm.currentRoutine
		} else if ctx != nil && ctx.State != nil {
			if active, _, _, taskRoutine := ctx.State.ActiveTaskInfo(); active && taskRoutine != "" {
				newRoutine = taskRoutine
			} else {
				newRoutine = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
			}
		} else {
			var cfg *xdg.AgentIdentityConfig
			if ctx != nil {
				cfg = ctx.Config
			}
			newRoutine = computeStageCircadianRoutine(fsm.currentStage, tod, weather, cfg, nil, ctx)
		}

		if newRoutine != fsm.currentRoutine {
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = newRoutine
			fsm.routineEnteredTick = worldTick
			routineTransitioned = true
			if fsm.onRoutineTransition != nil {
				fsm.onRoutineTransition(prevR, newRoutine, "circadian schedule progression")
			}
		}
	}

	// 4. Synchronize AgentState atomically
	if ctx != nil && ctx.State != nil {
		ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
	}

	// 5. Tick the activity stack (ADR-036 single stack, single dispatch path). Settled stages
	// first run the biological-needs and road-paving reflex skills, then every stage (emergency
	// and dead included) is driven by the kernel: Emergency and Dead are ordinary activities
	// whose private state is their frame Local, and ReflexNotices are Reflex events.
	var intents []FSMIntent
	if ctx != nil {
		var reg *EngineRegistry
		var skillCtx *SkillContext
		switch fsm.currentStage {
		case StageBootstrap, StageSettlement, StageColonize:
			if ctx.Frame != nil && ctx.Frame.Status != nil {
				reg = resolveContextRegistry(ctx)
				ensureFSMFacts(ctx, reg)
				skillCtx = buildSkillContext(ctx, reg)
				intents = wrapStageIntents(executeStageBiologicalNeeds(skillCtx, ctx.Facts, reg), false)
				intents = append(intents, wrapStageIntents(executeStageRoadPaving(skillCtx, ctx.Facts, reg, fsm.currentStage != StageBootstrap), false)...)
				for _, fi := range intents {
					if nav := fi.Intent.GetNavigate(); nav != nil && nav.Target != nil {
						fsm.orphanNav, fsm.hasOrphanNav = [3]float64{nav.Target.X, nav.Target.Y, nav.Target.Z}, true
					}
				}
			}
		}
		intents = append(intents, fsm.tickActivitiesLocked(ctx, reg, skillCtx)...)
	}

	// 5b. Routine is derived from the stack: a committed task's routine while one is active,
	// the circadian routine once it has ended.
	if ctx != nil && ctx.State != nil && fsm.currentStage != StageEmergency && fsm.currentStage != StageDead {
		active, taskKind, _, taskRoutine := ctx.State.ActiveTaskInfo()
		want := fsm.currentRoutine
		reason := ""
		if active && taskRoutine != "" {
			want, reason = taskRoutine, "task subroutine active: "+taskKind
		} else if !active && (fsm.currentRoutine == RoutineHarvestTree || fsm.currentRoutine == RoutineCollectItems) {
			want, reason = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx), "task subroutine ended"
		}
		if want != "" && want != fsm.currentRoutine {
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = want
			fsm.routineEnteredTick = worldTick
			routineTransitioned = true
			if fsm.onRoutineTransition != nil {
				fsm.onRoutineTransition(prevR, want, reason)
			}
			ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
		}
	}

	// 6. Evaluate Goals
	var goals GoalHierarchy
	if ctx != nil {
		goals = EvaluateGoals(ctx.Frame, ctx.Config, ctx.State)
	}

	stageTransitioned = stageTransitioned && fsm.previousStage != fsm.currentStage
	if !stageTransitioned {
		transitionReason = ""
	}

	return FSMResult{
		Stage:               fsm.currentStage,
		Routine:             fsm.currentRoutine,
		PreviousStage:       fsm.previousStage,
		PreviousRoutine:     fsm.previousRoutine,
		StageTransitioned:   stageTransitioned,
		RoutineTransitioned: routineTransitioned,
		Transitioned:        stageTransitioned,
		FlushPeriphery:      routineTransitioned || stageTransitioned || emergencyPreempted,
		State:               fsm.currentStage,
		PreviousState:       fsm.previousStage,
		TransitionReason:    transitionReason,
		Intents:             intents,
		GoalHierarchy:       &goals,
		Facts:               fsm.lastFacts,
	}
}

func computeStageCircadianRoutine(
	stage Stage,
	tod int64,
	weather civquestv1.Weather,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	ctx *FSMContext,
) Routine {
	routine := ComputeCircadianRoutine(tod, weather, cfg, state)
	if state != nil {
		routine = rhythm(state.BaseStrategy(), routine)
	}
	shelterSatisfied := isStateShelterOrBedSatisfied(state)
	if !shelterSatisfied && ctx != nil {
		shelterSatisfied = ctx.HasAssignedBed() || ctx.IsShelterComplete()
	}
	return gateBootstrapCircadianRoutine(stage, routine, state, shelterSatisfied)
}
