package system1

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// StateHandler defines the interface for each HFSM state lifecycle hooks.
type StateHandler interface {
	Name() State
	Guard(ctx *FSMContext) bool
	OnEntry(ctx *FSMContext, from State)
	OnTick(ctx *FSMContext) []FSMIntent
	OnExit(ctx *FSMContext, to State)
}

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

// BootstrapStateHandler handles survival kit bootstrap goals.
type BootstrapStateHandler struct{}

func (h *BootstrapStateHandler) Name() State                      { return StageBootstrap }
func (h *BootstrapStateHandler) Guard(ctx *FSMContext) bool       { return true }
func (h *BootstrapStateHandler) OnEntry(ctx *FSMContext, _ State) {}
func (h *BootstrapStateHandler) OnExit(ctx *FSMContext, _ State)  {}
func (h *BootstrapStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return executeStageSkill(ctx, "stage_bootstrap", NewStageBootstrapSkill(), false)
}

// SettlementStateHandler handles established settlement living, vocational duties, hobbies, and social routines.
type SettlementStateHandler struct{}

func (h *SettlementStateHandler) Name() State                      { return StageSettlement }
func (h *SettlementStateHandler) Guard(ctx *FSMContext) bool       { return true }
func (h *SettlementStateHandler) OnEntry(ctx *FSMContext, _ State) {}
func (h *SettlementStateHandler) OnExit(ctx *FSMContext, to State) {
	if ctx != nil && ctx.State != nil && ctx.Frame != nil && to == StageSettlement {
		ctx.State.SetLastShelterTimeoutTick(ctx.Frame.WorldTick)
	}
}
func (h *SettlementStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return executeStageSkill(ctx, "stage_settlement", NewStageSettlementSkill(), false)
}

// ColonizeStateHandler handles establishing and building a new frontier settlement node.
type ColonizeStateHandler struct{}

func (h *ColonizeStateHandler) Name() State                      { return StageColonize }
func (h *ColonizeStateHandler) Guard(ctx *FSMContext) bool       { return true }
func (h *ColonizeStateHandler) OnEntry(ctx *FSMContext, _ State) {}
func (h *ColonizeStateHandler) OnExit(ctx *FSMContext, _ State)  {}
func (h *ColonizeStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return executeStageSkill(ctx, "stage_colonize", NewStageColonizeSkill(), false)
}

// EmergencyStateHandler handles reactive combat, fleeing, and survival reflexes.
type EmergencyStateHandler struct{}

func (h *EmergencyStateHandler) Name() State                { return StageEmergency }
func (h *EmergencyStateHandler) Guard(ctx *FSMContext) bool { return true }
func (h *EmergencyStateHandler) OnEntry(ctx *FSMContext, _ State) {
	if ctx != nil && ctx.State != nil {
		ctx.State.ClearExpeditionSurveyingIfActive()
	}
}
func (h *EmergencyStateHandler) OnExit(ctx *FSMContext, _ State) {}
func (h *EmergencyStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	return executeStageSkill(ctx, "stage_emergency", NewStageEmergencySkill(), true)
}

// DeadStateHandler handles terminal permadeath state where all agent loops cease.
type DeadStateHandler struct{}

func (h *DeadStateHandler) Name() State                         { return StageDead }
func (h *DeadStateHandler) Guard(ctx *FSMContext) bool          { return true }
func (h *DeadStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *DeadStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *DeadStateHandler) OnTick(ctx *FSMContext) []FSMIntent  { return nil }

// StateStackFrame represents a snapshot of an interrupted state context in the HTPDA pushdown automata stack.
type StateStackFrame struct {
	Stage       Stage             `json:"stage"`
	Routine     Routine           `json:"routine"`
	EnteredTick int64             `json:"entered_tick"`
	Reason      string            `json:"reason"`
	Metadata    map[string]string `json:"metadata,omitempty"`
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
	handlers            map[Stage]StateHandler
	transitions         []StageTransitionRule
	onStageTransition   func(from, to Stage, reason string)
	onRoutineTransition func(from, to Routine, reason string)
	stack               []StateStackFrame
	maxStackDepth       int
	activityStack       []Activity
	activitySuspended   bool
	lastFacts           *FactGraph
	registry            *EngineRegistry
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

// NewAgentFSM constructs an initialized AgentFSM with default state handlers, transition table,
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
		maxStackDepth:     8,
		handlers:          make(map[Stage]StateHandler),
		transitions:       defaultTransitionRules(),
		onStageTransition: onTransition,
		registry:          reg,
	}
	fsm.RegisterHandler(&BootstrapStateHandler{})
	fsm.RegisterHandler(&SettlementStateHandler{})
	fsm.RegisterHandler(&ColonizeStateHandler{})
	fsm.RegisterHandler(&EmergencyStateHandler{})
	fsm.RegisterHandler(&DeadStateHandler{})
	return fsm
}

func (fsm *AgentFSM) pushLocked(stage Stage, routine Routine, reason string, tick int64, metadata map[string]string) {
	if stage == "" {
		return
	}
	// Check duplicate top: ignore if duplicate top (same Stage and Routine)
	if n := len(fsm.stack); n > 0 {
		top := fsm.stack[n-1]
		if top.Stage == stage && top.Routine == routine {
			return
		}
	}

	maxDepth := fsm.maxStackDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}

	// Drop oldest if overflow
	if len(fsm.stack) >= maxDepth {
		fsm.stack = fsm.stack[1:]
	}

	var metaCopy map[string]string
	if metadata != nil {
		metaCopy = make(map[string]string, len(metadata))
		for k, v := range metadata {
			metaCopy[k] = v
		}
	}

	fsm.stack = append(fsm.stack, StateStackFrame{
		Stage:       stage,
		Routine:     routine,
		EnteredTick: tick,
		Reason:      reason,
		Metadata:    metaCopy,
	})
}

func (fsm *AgentFSM) popLocked() (StateStackFrame, bool) {
	n := len(fsm.stack)
	if n == 0 {
		return StateStackFrame{}, false
	}
	frame := fsm.stack[n-1]
	fsm.stack = fsm.stack[:n-1]
	return frame, true
}

func (fsm *AgentFSM) peekLocked() (StateStackFrame, bool) {
	n := len(fsm.stack)
	if n == 0 {
		return StateStackFrame{}, false
	}
	return fsm.stack[n-1], true
}

func (fsm *AgentFSM) stackDepthLocked() int {
	return len(fsm.stack)
}

func (fsm *AgentFSM) clearStackLocked() {
	fsm.stack = nil
}

// Push pushes a state stack frame onto the HTPDA stack. Caps at maxStackDepth, dropping oldest on overflow, or ignoring if duplicate top.
func (fsm *AgentFSM) Push(stage Stage, routine Routine, reason string, tick int64, metadata map[string]string) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.pushLocked(stage, routine, reason, tick, metadata)
}

// Pop pops the topmost state stack frame from the HTPDA stack. Returns false if empty.
func (fsm *AgentFSM) Pop() (StateStackFrame, bool) {
	if fsm == nil {
		return StateStackFrame{}, false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	return fsm.popLocked()
}

// Peek inspects the topmost state stack frame without removing it. Returns false if empty.
func (fsm *AgentFSM) Peek() (StateStackFrame, bool) {
	if fsm == nil {
		return StateStackFrame{}, false
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.peekLocked()
}

// StackDepth returns the number of frames currently stored in the HTPDA state stack.
func (fsm *AgentFSM) StackDepth() int {
	if fsm == nil {
		return 0
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.stackDepthLocked()
}

// StackFrames returns a snapshot copy of the current HTPDA state stack frames from bottom to top.
func (fsm *AgentFSM) StackFrames() []StateStackFrame {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if len(fsm.stack) == 0 {
		return nil
	}
	cpy := make([]StateStackFrame, len(fsm.stack))
	copy(cpy, fsm.stack)
	return cpy
}

// ClearStack empties the HTPDA state stack.
func (fsm *AgentFSM) ClearStack() {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.clearStackLocked()
}

// SetMaxStackDepth updates the maximum stack capacity.
func (fsm *AgentFSM) SetMaxStackDepth(depth int) {
	if fsm == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if depth > 0 {
		fsm.maxStackDepth = depth
	}
}

// RegisterHandler registers a lifecycle handler for an FSM state.
func (fsm *AgentFSM) RegisterHandler(handler StateHandler) {
	if fsm == nil || handler == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.handlers[handler.Name()] = handler
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

	targetHandler, ok := fsm.handlers[target]
	if ok && ctx != nil && !targetHandler.Guard(ctx) {
		return false
	}

	fromStage := fsm.currentStage

	// When transitioning into StageEmergency from any non-emergency stage,
	// suspend active Activity and push the current (currentStage, currentRoutine, reason, tick, metadata) onto the stack.
	if target == StageEmergency && fromStage != StageEmergency && fromStage != StageDead {
		tick := int64(0)
		if ctx != nil && ctx.Frame != nil {
			tick = ctx.Frame.WorldTick
		}
		fsm.suspendTopActivityLocked(ctx, reason)
		var taskMeta map[string]string
		if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
			taskMeta = ctx.State.HandleEmergencyPreempt(tick)
		}
		fsm.pushLocked(fromStage, fsm.currentRoutine, reason, tick, taskMeta)
	}

	// Terminal dead state: clear stack, activities, and any active task
	if target == StageDead {
		fsm.clearActivitiesLocked(ctx)
		fsm.clearStackLocked()
		if ctx != nil && ctx.State != nil {
			ctx.State.ClearActiveTask()
		}
	}

	if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
		currentHandler.OnExit(ctx, target)
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

	if targetHandler != nil && ctx != nil {
		targetHandler.OnEntry(ctx, fromStage)
	}

	if fsm.onStageTransition != nil {
		fsm.onStageTransition(fromStage, target, reason)
	}

	if ctx != nil && ctx.State != nil {
		ctx.State.SetStageAndRoutine(target, fsm.currentRoutine)
	}

	if fromStage == StageEmergency && target != StageEmergency && target != StageDead {
		fsm.resumeTopActivityLocked(ctx)
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
		evalCtx := &EvaluatorContext{
			AgentID:       ctx.AgentID,
			Frame:         ctx.Frame,
			Config:        ctx.Config,
			State:         ctx.State,
			CivicDB:       ctx.CivicDB,
			SafeNodes:     ctx.SafeNodes,
			PavingTracker: ctx.PavingTracker,
		}
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
		fsm.lastFacts = ctx.Facts
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
	isDead := false
	if fsm.currentStage == StageDead {
		isDead = true
	} else if ctx != nil && ctx.Frame != nil {
		if ctx.Frame.DeathEvent != nil || (ctx.Frame.Status != nil && ctx.Frame.Status.Health <= 0) {
			isDead = true
		}
	}

	if isDead {
		fsm.clearActivitiesLocked(ctx)
		fsm.clearStackLocked()
		if ctx != nil && ctx.State != nil {
			ctx.State.ClearActiveTask()
		}
		if fsm.currentStage != StageDead {
			fromStage := fsm.currentStage
			if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
				currentHandler.OnExit(ctx, StageDead)
			}
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

			if targetHandler, exists := fsm.handlers[StageDead]; exists && targetHandler != nil && ctx != nil {
				targetHandler.OnEntry(ctx, fromStage)
			}
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
	pdaRestored := false
	emergencyPreempted := false
	var transitionReason string

	// Priority 1: EMERGENCY Preemption from any non-emergency stage (PDA pushdown)
	if fsm.currentStage != StageEmergency && fsm.currentStage != StageDead && ctx != nil && ctx.IsEmergency() {
		fromStage := fsm.currentStage
		toStage := StageEmergency
		ruleDesc := "Hostile threat, damage, or critical vitals detected; preempting to emergency"

		fsm.suspendTopActivityLocked(ctx, ruleDesc)
		emergencyPreempted = true

		var taskMeta map[string]string
		if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
			taskMeta = ctx.State.HandleEmergencyPreempt(worldTick)
		}
		// Automatically push current state onto the stack before switching to emergency
		fsm.pushLocked(fromStage, fsm.currentRoutine, ruleDesc, worldTick, taskMeta)

		if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
			currentHandler.OnExit(ctx, toStage)
		}

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

		if targetHandler, exists := fsm.handlers[toStage]; exists && targetHandler != nil && ctx != nil {
			targetHandler.OnEntry(ctx, fromStage)
		}

		if fsm.onStageTransition != nil {
			fsm.onStageTransition(fromStage, toStage, ruleDesc)
		}
	}

	// Pushdown Automata check: pop back to interrupted frame once ThreatsCleared()
	if !stageTransitioned && fsm.currentStage == StageEmergency && ctx != nil && ctx.ThreatsCleared() {
		if frame, ok := fsm.popLocked(); ok {
			fromStage := fsm.currentStage
			toStage := frame.Stage

			if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
				currentHandler.OnExit(ctx, toStage)
			}

			fsm.previousStage = fromStage
			fsm.currentStage = toStage
			fsm.stateEnteredTick = worldTick
			fsm.stuckTicks = 0
			if ctx != nil {
				ctx.TimeInState = 0
				ctx.StuckTicks = 0
			}
			stageTransitioned = true
			transitionReason = "Threats cleared; restored previous state from PDA stack (" + string(toStage) + ")"

			if targetHandler, exists := fsm.handlers[toStage]; exists && targetHandler != nil && ctx != nil {
				targetHandler.OnEntry(ctx, fromStage)
			}

			if fsm.onStageTransition != nil {
				fsm.onStageTransition(fromStage, toStage, transitionReason)
			}

			// Restore frame.Routine directly from the stack
			prevR := fsm.currentRoutine
			fsm.previousRoutine = prevR
			fsm.currentRoutine = frame.Routine
			fsm.routineEnteredTick = worldTick
			if prevR != frame.Routine {
				routineTransitioned = true
				if fsm.onRoutineTransition != nil {
					fsm.onRoutineTransition(prevR, frame.Routine, "restored routine from PDA stack")
				}
			}
			pdaRestored = true

			if ctx.State != nil {
				ctx.State.ResumeActiveTask(worldTick)
				ctx.State.SetRoutine(frame.Routine)
				ctx.State.SetStageAndRoutine(toStage, frame.Routine)
			}
			fsm.resumeTopActivityLocked(ctx)
		}
	}

	if !stageTransitioned {
		for _, rule := range fsm.transitions {
			if rule.FromStage == fsm.currentStage && rule.Condition(ctx) {
				targetHandler, ok := fsm.handlers[rule.ToStage]
				if !ok || targetHandler.Guard(ctx) {
					fromStage := fsm.currentStage
					toStage := rule.ToStage

					if toStage == StageEmergency && fromStage != StageEmergency && fromStage != StageDead {
						fsm.suspendTopActivityLocked(ctx, rule.Description)
						emergencyPreempted = true
						var taskMeta map[string]string
						if ctx != nil && ctx.State != nil && ctx.State.HasActiveTask() {
							taskMeta = ctx.State.HandleEmergencyPreempt(worldTick)
						}
						fsm.pushLocked(fromStage, fsm.currentRoutine, rule.Description, worldTick, taskMeta)
					}

					if currentHandler, exists := fsm.handlers[fromStage]; exists && ctx != nil {
						currentHandler.OnExit(ctx, toStage)
					}

					fsm.previousStage = fromStage
					fsm.currentStage = toStage
					fsm.stateEnteredTick = worldTick
					fsm.stuckTicks = 0
					if ctx != nil {
						ctx.TimeInState = 0
						ctx.StuckTicks = 0
					}
					stageTransitioned = true
					transitionReason = rule.Description

					if targetHandler != nil && ctx != nil {
						targetHandler.OnEntry(ctx, fromStage)
					}

					if fsm.onStageTransition != nil {
						fsm.onStageTransition(fromStage, toStage, rule.Description)
					}
					break
				}
			}
		}
	}

	// Capture pre-Step-3 routine in case a TaskSubroutine was installed externally before Tick.
	preStep3Routine := fsm.currentRoutine
	taskActiveBeforeStep3 := false

	// 3. Evaluate Tier 2 Circadian Operational Routine
	if !pdaRestored {
		var newRoutine Routine
		if fsm.currentStage == StageEmergency {
			newRoutine = RoutineEmergency
		} else if fsm.currentStage == StageDead {
			newRoutine = RoutineDead
		} else if fsm.currentStage == Stage("DIALOGUE") || fsm.currentRoutine == Routine("DIALOGUE") {
			newRoutine = fsm.currentRoutine
		} else if ctx != nil && ctx.State != nil {
			if active, _, _, taskRoutine := ctx.State.ActiveTaskInfo(); active && taskRoutine != "" {
				taskActiveBeforeStep3 = true
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
	if stageTransitioned && !pdaRestored && fsm.previousStage == StageEmergency &&
		fsm.currentStage != StageEmergency && fsm.currentStage != StageDead {
		fsm.resumeTopActivityLocked(ctx)
	}

	preOnTickRoutine := fsm.currentRoutine

	// 5. Execute Tier 1 top Activity if active & unsuspended; otherwise execute Stage handler
	var intents []FSMIntent
	if ctx != nil && fsm.currentStage != StageEmergency && fsm.currentStage != StageDead &&
		len(fsm.activityStack) > 0 && !fsm.activitySuspended {
		if act := fsm.activityStack[len(fsm.activityStack)-1]; act != nil {
			var done bool
			intents, done = act.OnTick(ctx)
			if done {
				fsm.popActivityLocked(ctx)
			}
		}
	} else if handler, exists := fsm.handlers[fsm.currentStage]; exists && ctx != nil {
		intents = handler.OnTick(ctx)
	}

	// 5b. Synchronize active TaskSubroutine commitment with the PDA state stack
	if ctx != nil && ctx.State != nil && fsm.currentStage != StageEmergency && fsm.currentStage != StageDead {
		active, taskKind, _, taskRoutine := ctx.State.ActiveTaskInfo()
		topHasTaskFrame := len(fsm.stack) > 0 && fsm.stack[len(fsm.stack)-1].Metadata != nil &&
			fsm.stack[len(fsm.stack)-1].Metadata["task_frame"] == "true"

		if active {
			if !topHasTaskFrame && !ctx.State.IsActiveTaskPushedToPDA() {
				parentR := preOnTickRoutine
				if taskActiveBeforeStep3 {
					parentR = preStep3Routine
				}
				if parentR == "" || parentR == RoutineHarvestTree || parentR == RoutineCollectItems ||
					parentR == RoutineEmergency || parentR == RoutineDead {
					parentR = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
				}
				meta := ctx.State.GetActiveTaskMetadata()
				fsm.pushLocked(fsm.currentStage, parentR, "task subroutine committed: "+taskKind, worldTick, meta)
				ctx.State.SetActiveTaskPushedToPDA(true)
			}
			if taskRoutine != "" && fsm.currentRoutine != taskRoutine {
				prevR := fsm.currentRoutine
				fsm.previousRoutine = prevR
				fsm.currentRoutine = taskRoutine
				fsm.routineEnteredTick = worldTick
				routineTransitioned = true
				if fsm.onRoutineTransition != nil {
					fsm.onRoutineTransition(prevR, taskRoutine, "task subroutine active: "+taskKind)
				}
				ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
			}
		} else if topHasTaskFrame {
			if popped, ok := fsm.popLocked(); ok {
				restoredR := popped.Routine
				if restoredR == "" || restoredR == RoutineHarvestTree || restoredR == RoutineCollectItems ||
					restoredR == RoutineEmergency || restoredR == RoutineDead {
					restoredR = computeStageCircadianRoutine(fsm.currentStage, tod, weather, ctx.Config, ctx.State, ctx)
				}
				if restoredR != fsm.currentRoutine {
					prevR := fsm.currentRoutine
					fsm.previousRoutine = prevR
					fsm.currentRoutine = restoredR
					fsm.routineEnteredTick = worldTick
					routineTransitioned = true
					if fsm.onRoutineTransition != nil {
						fsm.onRoutineTransition(prevR, restoredR, "task subroutine completed/popped")
					}
				}
				ctx.State.SetStageAndRoutine(fsm.currentStage, fsm.currentRoutine)
			}
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
	shelterSatisfied := isStateShelterOrBedSatisfied(state)
	if !shelterSatisfied && ctx != nil {
		shelterSatisfied = ctx.HasAssignedBed() || ctx.IsShelterComplete()
	}
	return gateBootstrapCircadianRoutine(stage, routine, state, shelterSatisfied)
}
