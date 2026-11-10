package system1

import (
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// State represents the explicit formal HFSM state of an agent in System 1.
type State string

const (
	// StateBootstrap represents the initial survival kit phase (gathering timber, crafting tools, making light).
	StateBootstrap State = "BOOTSTRAP"
	// StateExplore represents autonomous frontier scouting, routine tasks, and road paving.
	StateExplore State = "EXPLORE"
	// StateShelter represents seeking or constructing emergency night outpost shelter.
	StateShelter State = "SHELTER"
	// StateEmergency represents critical combat, fleeing, damage survival, or panic overrides.
	StateEmergency State = "EMERGENCY"
	// StateRest represents sleeping in bed or resting through the night until dawn.
	StateRest State = "REST"
)

// FSMIntent encapsulates an emitted intent alongside its priority classification.
type FSMIntent struct {
	Intent   *civquestv1.AgentIntent
	Priority bool
}

// FSMContext encapsulates sensory frames, agent configuration, spatial state, and vitals for evaluation.
type FSMContext struct {
	AgentID        string
	Frame          *civquestv1.TelemetryFrame
	State          *AgentState
	Config         *xdg.AgentIdentityConfig
	SafeNodes      []db.MapNode
	CanEat         bool
	LastEatingTick int64
	PavingTracker  *RoadPavingTracker
	StuckTicks     int64
	TimeInState    int64
}

// HostilesPresent checks if any hostile mobs are within perception range.
func (c *FSMContext) HostilesPresent() bool {
	if c == nil || c.Frame == nil {
		return false
	}
	for _, e := range c.Frame.VisibleEntities {
		if IsHostile(e) {
			return true
		}
	}
	return false
}

// DamageTaken checks if the agent took damage in the current frame.
func (c *FSMContext) DamageTaken() bool {
	if c == nil || c.Frame == nil {
		return false
	}
	return c.Frame.LastDamageTaken != nil
}

// IsHealthCritical checks if HP <= 35% max HP.
func (c *FSMContext) IsHealthCritical() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	maxHP := float64(c.Frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	return float64(c.Frame.Status.Health) <= LowHPThresholdRatio*maxHP
}

// IsOnFire checks if the agent is burning.
func (c *FSMContext) IsOnFire() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return c.Frame.Status.IsOnFire
}

// IsCriticalHunger checks if hunger <= 6 and edible food exists.
func (c *FSMContext) IsCriticalHunger() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	if c.Frame.Status.FoodLevel <= 6 {
		if _, _, hasFood := FindOptimalFood(c.Frame.Status.Inventory); hasFood {
			return true
		}
	}
	return false
}

// ShouldPanic checks if combat panic rules trigger.
func (c *FSMContext) ShouldPanic() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	bravery := 0.5
	if c.Config != nil {
		bravery = c.Config.Traits.Bravery
	}
	_, conf, hostiles := AssessImmediateThreats(c.Frame, bravery)
	selfHP := float64(c.Frame.Status.Health)
	maxHP := float64(c.Frame.Status.MaxHealth)
	if maxHP <= 0 {
		maxHP = 20.0
	}
	creeperNearby := false
	for _, h := range hostiles {
		if strings.Contains(strings.ToLower(h), "creeper") {
			creeperNearby = true
			break
		}
	}
	return ShouldPanic(selfHP, maxHP, conf, len(hostiles) > 0, creeperNearby)
}

// IsEmergency evaluates whether any high-priority emergency condition is active.
func (c *FSMContext) IsEmergency() bool {
	return c.HostilesPresent() || c.DamageTaken() || c.IsHealthCritical() || c.IsOnFire() || c.IsCriticalHunger() || c.ShouldPanic()
}

// ThreatsCleared evaluates whether all hostiles, panic, critical health, and critical hunger issues have cleared.
func (c *FSMContext) ThreatsCleared() bool {
	return !c.HostilesPresent() && !c.DamageTaken() && !c.IsHealthCritical() && !c.IsOnFire() && !c.IsCriticalHunger() && !c.ShouldPanic()
}

// HasSurvivalKit returns true if the agent holds an axe, weapon/sword, and light source.
func (c *FSMContext) HasSurvivalKit() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return HasSurvivalKit(c.Frame.Status.Inventory)
}

// IsDuskOrNight returns true if current time is dusk, night, or thunderstorm.
func (c *FSMContext) IsDuskOrNight() bool {
	if c == nil || c.Frame == nil {
		return false
	}
	if c.Frame.Weather == civquestv1.Weather_WEATHER_THUNDER {
		return true
	}
	tod := ((c.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 11000 && tod < 23500
}

// IsNight returns true if current time is deep night (13000-23500).
func (c *FSMContext) IsNight() bool {
	if c == nil || c.Frame == nil {
		return false
	}
	tod := ((c.Frame.TimeOfDay % 24000) + 24000) % 24000
	return tod >= 13000 && tod < 23500
}

// IsDawnOrDay returns true if daytime (23500 to 11000).
func (c *FSMContext) IsDawnOrDay() bool {
	return !c.IsDuskOrNight()
}

// HasAssignedBed returns true if state has an assigned bed.
func (c *FSMContext) HasAssignedBed() bool {
	if c == nil || c.State == nil {
		return false
	}
	return c.State.GetAssignedBed() != nil
}

// IsShelterComplete checks if all shelter walls have been placed or shelter pos secured.
func (c *FSMContext) IsShelterComplete() bool {
	if c == nil || c.State == nil {
		return false
	}
	return c.State.GetShelterBlocksPlaced() >= len(shelterWallOffsets)
}

// IsShelterOnCooldown checks if shelter construction is on backoff cooldown after a stuck timeout.
func (c *FSMContext) IsShelterOnCooldown() bool {
	if c == nil || c.State == nil || c.Frame == nil {
		return false
	}
	lastTimeout := c.State.GetLastShelterTimeoutTick()
	if lastTimeout <= 0 {
		return false
	}
	return (c.Frame.WorldTick - lastTimeout) < 600
}

// IsSleeping checks if agent status reports sleeping.
func (c *FSMContext) IsSleeping() bool {
	if c == nil || c.Frame == nil || c.Frame.Status == nil {
		return false
	}
	return c.Frame.Status.IsSleeping
}

// IsStuckInState returns true if time in state or stuck ticks exceeds threshold.
func (c *FSMContext) IsStuckInState(maxTicks int64) bool {
	if c == nil {
		return false
	}
	return c.StuckTicks >= maxTicks || (maxTicks > 0 && c.TimeInState >= maxTicks)
}

// StateHandler defines the interface for each HFSM state lifecycle hooks.
type StateHandler interface {
	Name() State
	Guard(ctx *FSMContext) bool
	OnEntry(ctx *FSMContext, from State)
	OnTick(ctx *FSMContext) []FSMIntent
	OnExit(ctx *FSMContext, to State)
}

// TransitionRule defines a directed edge in the HFSM transition graph evaluated in priority order.
type TransitionRule struct {
	FromState   State
	ToState     State
	Condition   func(ctx *FSMContext) bool
	Description string
}

func defaultTransitionRules() []TransitionRule {
	return []TransitionRule{
		// Priority 1: EMERGENCY Preemption from all non-emergency states
		{
			FromState:   StateBootstrap,
			ToState:     StateEmergency,
			Condition:   func(c *FSMContext) bool { return c.IsEmergency() },
			Description: "Hostile threat, damage, or critical vitals detected during bootstrap",
		},
		{
			FromState:   StateExplore,
			ToState:     StateEmergency,
			Condition:   func(c *FSMContext) bool { return c.IsEmergency() },
			Description: "Hostile threat, damage, or critical vitals detected during exploration",
		},
		{
			FromState:   StateShelter,
			ToState:     StateEmergency,
			Condition:   func(c *FSMContext) bool { return c.IsEmergency() },
			Description: "Hostile threat or damage detected; preempting shelter construction",
		},
		{
			FromState:   StateRest,
			ToState:     StateEmergency,
			Condition:   func(c *FSMContext) bool { return c.IsEmergency() },
			Description: "Hostile threat or damage detected while resting; waking to defend or flee",
		},

		// Priority 2: Transitions OUT of EMERGENCY (Strictly guarded by ThreatsCleared)
		{
			FromState: StateEmergency,
			ToState:   StateRest,
			Condition: func(c *FSMContext) bool {
				return c.ThreatsCleared() && c.IsNight() && (c.HasAssignedBed() || c.IsSleeping() || c.IsShelterComplete())
			},
			Description: "Threats cleared and vitals stable; returning to bed/shelter rest",
		},
		{
			FromState: StateEmergency,
			ToState:   StateShelter,
			Condition: func(c *FSMContext) bool {
				return c.ThreatsCleared() && c.IsDuskOrNight() && !c.IsShelterComplete() && !c.HasAssignedBed()
			},
			Description: "Threats cleared at dusk/night; seeking or fortifying outpost shelter",
		},
		{
			FromState: StateEmergency,
			ToState:   StateBootstrap,
			Condition: func(c *FSMContext) bool {
				return c.ThreatsCleared() && c.IsDawnOrDay() && !c.HasSurvivalKit()
			},
			Description: "Threats cleared in daylight; resuming bootstrap tool gathering",
		},
		{
			FromState: StateEmergency,
			ToState:   StateExplore,
			Condition: func(c *FSMContext) bool {
				return c.ThreatsCleared() && c.IsDawnOrDay() && c.HasSurvivalKit()
			},
			Description: "Threats cleared in daylight; resuming frontier exploration",
		},

		// Priority 3: Transitions from BOOTSTRAP
		{
			FromState:   StateBootstrap,
			ToState:     StateShelter,
			Condition:   func(c *FSMContext) bool { return c.IsDuskOrNight() },
			Description: "Dusk/night approaching during bootstrap; seeking immediate shelter",
		},
		{
			FromState:   StateBootstrap,
			ToState:     StateExplore,
			Condition:   func(c *FSMContext) bool { return c.HasSurvivalKit() },
			Description: "Survival kit acquired (axe, sword, light); advancing to exploration",
		},
		{
			FromState:   StateBootstrap,
			ToState:     StateExplore,
			Condition:   func(c *FSMContext) bool { return c.IsStuckInState(1200) },
			Description: "Bootstrap timber gathering timed out; falling back to explore wider area",
		},

		// Priority 4: Transitions from EXPLORE
		{
			FromState:   StateExplore,
			ToState:     StateRest,
			Condition:   func(c *FSMContext) bool { return c.IsNight() && c.HasAssignedBed() },
			Description: "Night fallen; navigating to assigned bed to sleep",
		},
		{
			FromState:   StateExplore,
			ToState:     StateShelter,
			Condition:   func(c *FSMContext) bool { return c.IsDuskOrNight() && !c.HasAssignedBed() && !c.IsShelterOnCooldown() },
			Description: "Dusk/night approaching with no bed; constructing outpost shelter",
		},
		{
			FromState:   StateExplore,
			ToState:     StateBootstrap,
			Condition:   func(c *FSMContext) bool { return c.IsDawnOrDay() && !c.HasSurvivalKit() },
			Description: "Survival kit broken or missing; reverting to bootstrap",
		},

		// Priority 5: Transitions from SHELTER
		{
			FromState: StateShelter,
			ToState:   StateRest,
			Condition: func(c *FSMContext) bool {
				return c.IsNight() && (c.HasAssignedBed() || c.IsShelterComplete() || c.IsSleeping())
			},
			Description: "Shelter completed or bed reached; resting through the night",
		},
		{
			FromState:   StateShelter,
			ToState:     StateBootstrap,
			Condition:   func(c *FSMContext) bool { return c.IsDawnOrDay() && !c.HasSurvivalKit() },
			Description: "Dawn arrived; resuming bootstrap survival crafting",
		},
		{
			FromState:   StateShelter,
			ToState:     StateExplore,
			Condition:   func(c *FSMContext) bool { return c.IsDawnOrDay() && c.HasSurvivalKit() },
			Description: "Dawn arrived; resuming frontier exploration",
		},
		{
			FromState:   StateShelter,
			ToState:     StateExplore,
			Condition:   func(c *FSMContext) bool { return c.IsStuckInState(600) },
			Description: "Shelter construction timed out/stuck; falling back to explore",
		},

		// Priority 6: Transitions from REST
		{
			FromState:   StateRest,
			ToState:     StateBootstrap,
			Condition:   func(c *FSMContext) bool { return c.IsDawnOrDay() && !c.HasSurvivalKit() && !c.IsSleeping() },
			Description: "Dawn awakening; starting survival kit bootstrap",
		},
		{
			FromState:   StateRest,
			ToState:     StateExplore,
			Condition:   func(c *FSMContext) bool { return c.IsDawnOrDay() && c.HasSurvivalKit() && !c.IsSleeping() },
			Description: "Dawn awakening; starting daily exploration and duties",
		},
		{
			FromState:   StateRest,
			ToState:     StateShelter,
			Condition:   func(c *FSMContext) bool { return c.IsDuskOrNight() && !c.HasAssignedBed() && !c.IsSleeping() },
			Description: "Rest interrupted and no bed available; seeking outpost shelter",
		},
	}
}

// BootstrapStateHandler handles survival kit bootstrap goals.
type BootstrapStateHandler struct{}

func (h *BootstrapStateHandler) Name() State { return StateBootstrap }
func (h *BootstrapStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *BootstrapStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *BootstrapStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *BootstrapStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	// Synchronize daily routine schedule with time of day
	if utilRes := EvaluateUtility(ctx.Frame, ctx.Config, ctx.State); utilRes != nil && utilRes.State != "" && ctx.State != nil {
		ctx.State.SetCurrentRoutine(utilRes.State)
	}

	// Biological needs (eating, sleep, wake)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Road paving
	if !ctx.Frame.Status.IsSleeping && ctx.PavingTracker != nil &&
		ctx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		!ctx.Frame.Status.NavigationEnclosed {
		if paveIntent := ctx.PavingTracker.EvaluatePaving(ctx.AgentID, ctx.Frame); paveIntent != nil {
			intents = append(intents, FSMIntent{Intent: paveIntent, Priority: false})
		}
	}

	// Bootstrap timber and tool crafting (only if not busy with incoming chats/trade)
	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if exploreIntent := EvaluateExploration(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); exploreIntent != nil {
			intents = append(intents, FSMIntent{Intent: exploreIntent, Priority: false})
		}
	}

	return intents
}

// ExploreStateHandler handles daytime exploration, duties, and road paving.
type ExploreStateHandler struct{}

func (h *ExploreStateHandler) Name() State { return StateExplore }
func (h *ExploreStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *ExploreStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *ExploreStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *ExploreStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	// Synchronize daily routine schedule with time of day
	if utilRes := EvaluateUtility(ctx.Frame, ctx.Config, ctx.State); utilRes != nil && utilRes.State != "" && ctx.State != nil {
		ctx.State.SetCurrentRoutine(utilRes.State)
	}

	// Biological needs (eating, sleep, wake)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Road paving
	if !ctx.Frame.Status.IsSleeping && ctx.PavingTracker != nil &&
		ctx.Frame.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_STUCK &&
		!ctx.Frame.Status.NavigationEnclosed {
		if paveIntent := ctx.PavingTracker.EvaluatePaving(ctx.AgentID, ctx.Frame); paveIntent != nil {
			intents = append(intents, FSMIntent{Intent: paveIntent, Priority: false})
		}
	}

	// Autonomous exploration & terrain navigation
	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if exploreIntent := EvaluateExploration(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); exploreIntent != nil {
			intents = append(intents, FSMIntent{Intent: exploreIntent, Priority: false})
		}
	}

	return intents
}

// ShelterStateHandler handles dusk/night outpost shelter construction.
type ShelterStateHandler struct{}

func (h *ShelterStateHandler) Name() State { return StateShelter }
func (h *ShelterStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *ShelterStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *ShelterStateHandler) OnExit(ctx *FSMContext, to State) {
	if ctx != nil && ctx.State != nil && ctx.Frame != nil && to == StateExplore {
		ctx.State.SetLastShelterTimeoutTick(ctx.Frame.WorldTick)
	}
}
func (h *ShelterStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	// Biological needs (eating, sleep, wake)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	// Shelter building and campfire placement via exploration reflexes (only if not busy with chat/trade)
	if len(ctx.Frame.IncomingChats) == 0 && ctx.Frame.PendingTrade == nil {
		if shelterIntent := EvaluateExploration(ctx.AgentID, ctx.Frame, ctx.Config, ctx.State); shelterIntent != nil {
			intents = append(intents, FSMIntent{Intent: shelterIntent, Priority: false})
		}
	}

	return intents
}

// EmergencyStateHandler handles reactive combat, fleeing, and survival reflexes.
type EmergencyStateHandler struct{}

func (h *EmergencyStateHandler) Name() State { return StateEmergency }
func (h *EmergencyStateHandler) Guard(ctx *FSMContext) bool {
	return true
}
func (h *EmergencyStateHandler) OnEntry(ctx *FSMContext, from State) {}
func (h *EmergencyStateHandler) OnExit(ctx *FSMContext, to State)    {}
func (h *EmergencyStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	bravery := 0.5
	if ctx.Config != nil {
		bravery = ctx.Config.Traits.Bravery
	}

	rCtx := ReflexContext{
		AgentID:   ctx.AgentID,
		Bravery:   bravery,
		SafeNodes: ctx.SafeNodes,
		Frame:     ctx.Frame,
		CanEat:    ctx.CanEat,
	}

	combatResult := EvaluateReflexes(rCtx)
	if combatResult.ShouldAct {
		for _, intent := range combatResult.Intents {
			intents = append(intents, FSMIntent{
				Intent:   intent,
				Priority: true,
			})
		}
	}

	return intents
}

// RestStateHandler handles sleeping in bed or safe resting through the night.
type RestStateHandler struct{}

func (h *RestStateHandler) Name() State { return StateRest }
func (h *RestStateHandler) Guard(ctx *FSMContext) bool {
	// Guard Invariant: REST is strictly blocked when hostiles are present, damage is taken, health is critical, or hunger is critical!
	if ctx == nil {
		return true
	}
	if ctx.HostilesPresent() || ctx.DamageTaken() || ctx.IsHealthCritical() || ctx.IsOnFire() || ctx.IsCriticalHunger() || ctx.ShouldPanic() {
		return false
	}
	return true
}
func (h *RestStateHandler) OnEntry(ctx *FSMContext, from State) {
	if ctx != nil && ctx.State != nil {
		ctx.State.SetCurrentRoutine(RoutineSleep)
	}
}
func (h *RestStateHandler) OnExit(ctx *FSMContext, to State) {}
func (h *RestStateHandler) OnTick(ctx *FSMContext) []FSMIntent {
	var intents []FSMIntent
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return intents
	}

	// Biological needs (sleep / wake up intents)
	needsIntents := EvaluateBiologicalNeeds(ctx.AgentID, ctx.Frame, ctx.State)
	for _, ni := range needsIntents {
		if ni.GetUseItem() != nil && ni.GetUseItem().IsEatingOrDrinking {
			intents = append(intents, FSMIntent{Intent: ni, Priority: true})
		} else {
			intents = append(intents, FSMIntent{Intent: ni, Priority: false})
		}
	}

	return intents
}

// FSMResult encapsulates the output of an FSM tick.
type FSMResult struct {
	State            State
	PreviousState    State
	Transitioned     bool
	TransitionReason string
	Intents          []FSMIntent
	GoalHierarchy    *GoalHierarchy
	CombatResult     *ReflexResult
}

// AgentFSM manages the formal Hierarchical Finite State Machine for System 1.
type AgentFSM struct {
	mu               sync.RWMutex
	currentState     State
	previousState    State
	stateEnteredTick int64
	stuckTicks       int64
	lastPos          *civquestv1.Vec3
	handlers         map[State]StateHandler
	transitions      []TransitionRule
	onTransition     func(from, to State, reason string)
}

// NewAgentFSM constructs an initialized AgentFSM with default state handlers and transition table.
func NewAgentFSM(initial State, onTransition func(from, to State, reason string)) *AgentFSM {
	if initial == "" {
		initial = StateBootstrap
	}
	fsm := &AgentFSM{
		currentState: initial,
		handlers:     make(map[State]StateHandler),
		transitions:  defaultTransitionRules(),
		onTransition: onTransition,
	}
	fsm.RegisterHandler(&BootstrapStateHandler{})
	fsm.RegisterHandler(&ExploreStateHandler{})
	fsm.RegisterHandler(&ShelterStateHandler{})
	fsm.RegisterHandler(&EmergencyStateHandler{})
	fsm.RegisterHandler(&RestStateHandler{})
	return fsm
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

// CurrentState returns the active state.
func (fsm *AgentFSM) CurrentState() State {
	if fsm == nil {
		return StateBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.currentState
}

// PreviousState returns the previous state before the last transition.
func (fsm *AgentFSM) PreviousState() State {
	if fsm == nil {
		return StateBootstrap
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.previousState
}

// TransitionTo forces a state transition if the target state's guard allows it.
func (fsm *AgentFSM) TransitionTo(target State, reason string, ctx *FSMContext) bool {
	if fsm == nil {
		return false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	if fsm.currentState == target {
		return false
	}

	targetHandler, ok := fsm.handlers[target]
	if ok && ctx != nil && !targetHandler.Guard(ctx) {
		return false
	}

	fromState := fsm.currentState
	if currentHandler, exists := fsm.handlers[fromState]; exists && ctx != nil {
		currentHandler.OnExit(ctx, target)
	}

	fsm.previousState = fromState
	fsm.currentState = target
	if ctx != nil && ctx.Frame != nil {
		fsm.stateEnteredTick = ctx.Frame.WorldTick
	}
	fsm.stuckTicks = 0

	if targetHandler != nil && ctx != nil {
		targetHandler.OnEntry(ctx, fromState)
	}

	if fsm.onTransition != nil {
		fsm.onTransition(fromState, target, reason)
	}

	if ctx != nil && ctx.State != nil {
		ctx.State.SetFSMState(target)
	}

	return true
}

// Tick executes a single 5 Hz evaluation cycle of the FSM.
func (fsm *AgentFSM) Tick(ctx *FSMContext) FSMResult {
	if fsm == nil {
		return FSMResult{State: StateBootstrap}
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()

	worldTick := int64(0)
	if ctx != nil && ctx.Frame != nil {
		worldTick = ctx.Frame.WorldTick
	}

	// Track position & stuckness
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil && ctx.Frame.Status.Position != nil {
		pos := ctx.Frame.Status.Position
		if fsm.lastPos != nil {
			dx := pos.X - fsm.lastPos.X
			dy := pos.Y - fsm.lastPos.Y
			dz := pos.Z - fsm.lastPos.Z
			if dx*dx+dy*dy+dz*dz < 0.04 || ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
				fsm.stuckTicks++
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

	// 1. Evaluate Transition Table in Priority Order
	transitioned := false
	var transitionReason string
	for _, rule := range fsm.transitions {
		if rule.FromState == fsm.currentState && rule.Condition(ctx) {
			targetHandler, ok := fsm.handlers[rule.ToState]
			if !ok || targetHandler.Guard(ctx) {
				fromState := fsm.currentState
				toState := rule.ToState

				if currentHandler, exists := fsm.handlers[fromState]; exists && ctx != nil {
					currentHandler.OnExit(ctx, toState)
				}

				fsm.previousState = fromState
				fsm.currentState = toState
				fsm.stateEnteredTick = worldTick
				fsm.stuckTicks = 0
				if ctx != nil {
					ctx.TimeInState = 0
					ctx.StuckTicks = 0
				}
				transitioned = true
				transitionReason = rule.Description

				if targetHandler != nil && ctx != nil {
					targetHandler.OnEntry(ctx, fromState)
				}

				if fsm.onTransition != nil {
					fsm.onTransition(fromState, toState, rule.Description)
				}

				if ctx != nil && ctx.State != nil {
					ctx.State.SetFSMState(toState)
					switch toState {
					case StateEmergency:
						ctx.State.SetCurrentRoutine(RoutineEmergency)
					case StateRest:
						ctx.State.SetCurrentRoutine(RoutineSleep)
					case StateShelter:
						if ctx.State.IsCompanionActive() {
							ctx.State.SetCurrentRoutine(RoutineCamp)
						} else {
							ctx.State.SetCurrentRoutine(RoutineSleep)
						}
					case StateBootstrap, StateExplore:
						utilRes := EvaluateUtility(ctx.Frame, ctx.Config, ctx.State)
						if utilRes != nil && utilRes.State != "" {
							ctx.State.SetCurrentRoutine(utilRes.State)
						}
					}
				}
				break
			}
		}
	}

	// 2. Execute OnTick for the active state
	var intents []FSMIntent
	if handler, exists := fsm.handlers[fsm.currentState]; exists && ctx != nil {
		intents = handler.OnTick(ctx)
	}

	var goals GoalHierarchy
	if ctx != nil {
		goals = EvaluateGoals(ctx.Frame, ctx.Config, ctx.State)
	}

	return FSMResult{
		State:            fsm.currentState,
		PreviousState:    fsm.previousState,
		Transitioned:     transitioned,
		TransitionReason: transitionReason,
		Intents:          intents,
		GoalHierarchy:    &goals,
	}
}
