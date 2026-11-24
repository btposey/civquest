package system1

import (
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultUnreachableTargetTTL is the default blacklist duration (2400 ticks = 2 minutes)
	// for coordinates that repeatedly produce NAVIGATION_STATE_STUCK.
	DefaultUnreachableTargetTTL int64 = 2400
	unreachableTargetRadius           = 2.5
)

type unreachableTargetEntry struct {
	x              float64
	y              float64
	z              float64
	failures       int
	lastFailTick   int64
	blacklistUntil int64
}

// Stage represents the Tier 1 macro strategic stage of an agent in the Two-Tier HFSM.
type Stage string

const (
	// StageEmergency represents active lethal threats, fleeing, combat panic, or critical survival reflexes.
	StageEmergency Stage = "EMERGENCY"
	// StageBootstrap represents initial survival kit acquisition, timber harvesting, basic tool crafting, starter shelter materials.
	StageBootstrap Stage = "BOOTSTRAP"
	// StageSettlement represents established clan settlement living, daily profession routines, and civic duties.
	StageSettlement Stage = "SETTLEMENT"
	// StageColonize represents founding a new frontier settlement node.
	StageColonize Stage = "COLONIZE"
	// StageDead represents terminal permadeath state where all agent loops cease.
	StageDead Stage = "DEAD"
)

// State is an alias for Stage, preserving backward compatibility with legacy FSM code.
type State = Stage

const (
	StateEmergency  State = StageEmergency
	StateBootstrap  State = StageBootstrap
	StateSettlement State = StageSettlement
	StateColonize   State = StageColonize
	StateDead       State = StageDead

	// Backward compatibility aliases for legacy states
	StateExplore State = StageSettlement
	StateShelter State = "SHELTER"
	StateRest    State = "REST"
)

// Routine represents the Tier 2 circadian operational routine of an agent.
type Routine string

// RoutineState is an alias for Routine, preserving backward compatibility.
type RoutineState = Routine

const (
	// RoutineEmergency represents critical survival reflex overrides (U = 1.0).
	RoutineEmergency Routine = "EMERGENCY"
	// RoutineDawnPrep represents morning awakening, breakfast, equipment check during ticks 23500–1000.
	RoutineDawnPrep Routine = "DAWN_PREP"
	// RoutineDawnAwakening is a backward compatibility alias for RoutineDawnPrep.
	RoutineDawnAwakening Routine = RoutineDawnPrep
	// RoutinePrimaryDuty represents scheduled vocational work (farming, building, mining, ranching, chronicler, trading) during ticks 1000–8000.
	RoutinePrimaryDuty Routine = "PRIMARY_DUTY"
	// RoutineSecondaryHobby represents off-shift artisanal crafting, botany, tool sharpening during ticks 8000–11000.
	RoutineSecondaryHobby Routine = "SECONDARY_HOBBY"
	// RoutineTavernSocial represents evening hearth gathering, trade, rumors, civic assemblies during ticks 11000–13000.
	RoutineTavernSocial Routine = "TAVERN_SOCIAL"
	// RoutineSleep represents nighttime rest in bed/shelter, System 2 contemplation during ticks 13000–23500.
	RoutineSleep Routine = "SLEEP"
	// RoutineCivicPatrol represents perimeter security and landmark watches.
	RoutineCivicPatrol Routine = "CIVIC_PATROL"
	// RoutineArmoryMaintenance represents off-watch armory maintenance and munitions crafting.
	RoutineArmoryMaintenance Routine = "ARMORY_MAINTENANCE"
	// RoutineCamp represents camping with player companion during evening/night, overriding tavern and bed return.
	RoutineCamp Routine = "CAMP"
	// RoutineHarvestTree represents committed timber felling until trunk logs within reach are exhausted.
	RoutineHarvestTree Routine = "HARVEST_TREE"
	// RoutineCollectItems represents collecting dropped items near a completed harvest target.
	RoutineCollectItems Routine = "COLLECT_ITEMS"
	// RoutineDead represents terminal permadeath cessation of all operational routines.
	RoutineDead Routine = "DEAD"
)

// StateTuple encapsulates the unified (Stage, Routine) state of an agent in the Two-Tier HFSM.
type StateTuple struct {
	Stage   Stage   `json:"stage"`
	Routine Routine `json:"routine"`
}

// UtilityDecision encapsulates the output of daily utility scheduling.
type UtilityDecision struct {
	State      RoutineState
	Utility    float64
	Profession string
	Hobby      string
	Reason     string
	WorldTick  int64
	TimeOfDay  int64
}

// GoalHierarchy tracks the agent's explicit 3-horizon goals across immediate, short-term, and long-term timescales.
type GoalHierarchy struct {
	ImmediateGoal string `json:"immediate_goal"`
	ShortTermGoal string `json:"short_term_goal"`
	LongTermGoal  string `json:"long_term_goal"`
}

// StrategicDirectiveState stores the active executable System 2 directive bridged into System 1 (ADR-031).
type StrategicDirectiveState struct {
	Action        string `json:"action"`
	Target        string `json:"target,omitempty"`
	ImmediateGoal string `json:"immediate_goal,omitempty"`
	IssuedTick    int64  `json:"issued_tick"`
	ExpiresTick   int64  `json:"expires_tick"`
}

// AgentState tracks volatile in-memory System 1 agent routine, needs, and paving state.
type AgentState struct {
	mu                        sync.RWMutex
	AssignedBed               *civquestv1.BlockPosProto
	ShelterPos                *civquestv1.BlockPosProto
	stage                     Stage
	routine                   Routine
	CurrentRoutine            RoutineState
	FSMState                  State
	PrimaryDutyBlocked        bool
	armoryMaintenanceNeeded   bool
	companionActive           bool
	civicPatrolActive         bool
	patrolWaypoints           []*civquestv1.Vec3
	patrolWaypointIndex       int
	lastEatingTick            int64
	lastSleepAttemptTick      int64
	lastShelterPlaceTick      int64
	lastShelterTimeoutTick    int64
	lastBootstrapTimeoutTick  int64
	establishingNewSettlement bool
	shelterBlocksPlaced       int
	ShelterBuildPhase         int
	ShelterBuildIndex         int
	lastHarvestTick           int64
	lastCraftTick             int64
	PavingTracker             *RoadPavingTracker

	// Unstuck and excavation navigation state
	unstuckLocked    bool
	unstuckDirX      int32
	unstuckDirZ      int32
	unstuckStartTick int64
	unstuckStartX    float64
	unstuckStartZ    float64

	// Timber tree trunk harvesting focus and generic task subroutine commitment
	targetTreeLocked        bool
	targetTreeX             int32
	targetTreeZ             int32
	targetTreeStartTick     int64
	activeTask              *TaskSubroutine
	failedTrees             map[[2]int32]int64
	unreachableWorkstations map[[3]int32]int64
	unreachableTargets      []*unreachableTargetEntry

	// Autonomous exploration, forward outposts & topological mapping state
	homeOrigin              *civquestv1.Vec3
	currentOutpost          *civquestv1.Vec3
	outposts                []*civquestv1.Vec3
	lastExploreTick         int64
	lastBootstrapNavTick    int64
	navigatingToPOI         bool
	exploreStepIndex        int
	lastHeadingRad          float64
	frontierAzimuthRad      float64
	frontierAzimuthSet      bool
	frontierRadialLeg       int
	currentExploreTarget    *civquestv1.Vec3
	lastExploreProgressDist float64
	lastExploreProgressTick int64
	lastMapNodeID           string
	lastMapPos              *civquestv1.Vec3
	telemetryFramesSeen     int64
	goals                   GoalHierarchy

	// Intent execution receipt tracking from the motor periphery
	lastReceipt    *civquestv1.IntentReceipt
	pendingReceipt *civquestv1.IntentReceipt

	// In-memory 16x16x16 spatial grid index for persistent POIs (ADR-022)
	spatialIndex *SpatialIndex

	// Breadcrumb habit trail and corridor navigation tracker (ADR-023)
	habitTrailTracker *HabitTrailTracker

	// Dynamic runtime crafting recipe catalog synced from server RecipeManager (ADR-029)
	recipeGraph *crafting.RecipeGraph

	// Active System 2 -> System 1 strategic directive bridge state (ADR-031)
	strategicDirective      *StrategicDirectiveState
	directiveScoutTarget    *civquestv1.Vec3
	lastDirectiveActionTick int64
	lastDirectiveNavKey     string

	// Peripheral 360-degree horizon survey & chained expedition state (ADR-032)
	LatestSurvey              *civquestv1.HorizonSurveyReport
	LastSurveyTick            int64
	LastSurveyOrigin          [3]float64
	ExpeditionPhase           ExpeditionPhase
	ExpeditionHops            int
	ExpeditionTarget          [3]float64
	ExpeditionBearing         float32
	ExpeditionFlankCount      int
	ExpeditionNavAttempts     int
	ClanSize                  int
	RendezvousOrigin          [3]float64
	HomesteadClaimed          bool
	HomesteadPos              [3]float64
	PendingRendezvousSync     bool
	PendingRendezvousSummary  string
	CollectiveSiteChosen      bool
	CollectiveSitePos         [3]float64
	expeditionSurveyStartTick int64
	lastSurveyEmitTick        int64
	lastSurveyEmitOrigin      [3]float64
	lastSurveyEmitOriginSet   bool
	expeditionLastNavTick     int64
	expeditionPrimaryTarget   [3]float64
	expeditionPrimaryBearing  float32
	expeditionReturning       bool
	expeditionPrevOrigin      [3]float64
	expeditionPrevOriginSet   bool
	expeditionBlockedBearings []float32
	rendezvousOriginSet       bool
	clanSizeExplicit          bool
	latestSurveySummary       string
}

// NewAgentState creates an initialized AgentState.
func NewAgentState() *AgentState {
	return &AgentState{
		stage:                   StageBootstrap,
		routine:                 RoutineDawnPrep,
		CurrentRoutine:          RoutineDawnPrep,
		FSMState:                StageBootstrap,
		armoryMaintenanceNeeded: true,
		PavingTracker:           NewRoadPavingTracker(DefaultPavingCapacity, DefaultPavingThreshold),
		spatialIndex:            NewSpatialIndex(),
		habitTrailTracker:       NewHabitTrailTracker(DefaultBreadcrumbDisplacement, DefaultArrivalThreshold, DefaultFinalTransitionDist),
		ExpeditionPhase:         ExpeditionIdle,
		ClanSize:                1,
		goals: GoalHierarchy{
			ImmediateGoal: "GATHER_TIMBER: Gather 4 logs and craft basic wooden tools",
			ShortTermGoal: "ESTABLISH_OUTPOST_CHAIN: Advance frontier and establish safe day-night outposts with tools & light",
			LongTermGoal:  "CHART_REGIONAL_ATLAS: Map biome sectors, consecrate landmarks, and link outposts for Clan Cartographer",
		},
	}
}

// SpatialIndex returns the agent's in-memory SpatialIndex for persistent POIs.
func (s *AgentState) SpatialIndex() *SpatialIndex {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	idx := s.spatialIndex
	s.mu.RUnlock()
	if idx != nil {
		return idx
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SpatialIndexLocked()
}

// SpatialIndexLocked returns the agent's SpatialIndex while s.mu is already held.
func (s *AgentState) SpatialIndexLocked() *SpatialIndex {
	if s == nil {
		return nil
	}
	if s.spatialIndex == nil {
		s.spatialIndex = NewSpatialIndex()
	}
	return s.spatialIndex
}

// HabitTrailTracker returns the agent's in-memory HabitTrailTracker (ADR-023).
func (s *AgentState) HabitTrailTracker() *HabitTrailTracker {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	tracker := s.habitTrailTracker
	s.mu.RUnlock()
	if tracker != nil {
		return tracker
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HabitTrailTrackerLocked()
}

// HabitTrailTrackerLocked returns the agent's HabitTrailTracker while s.mu is already held.
func (s *AgentState) HabitTrailTrackerLocked() *HabitTrailTracker {
	if s == nil {
		return nil
	}
	if s.habitTrailTracker == nil {
		s.habitTrailTracker = NewHabitTrailTracker(DefaultBreadcrumbDisplacement, DefaultArrivalThreshold, DefaultFinalTransitionDist)
	}
	return s.habitTrailTracker
}

// GetAssignedBed returns a defensive copy of the assigned bed pos.
func (s *AgentState) GetAssignedBed() *civquestv1.BlockPosProto {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.AssignedBed == nil {
		return nil
	}
	return &civquestv1.BlockPosProto{
		X: s.AssignedBed.X,
		Y: s.AssignedBed.Y,
		Z: s.AssignedBed.Z,
	}
}

// SetAssignedBed sets the agent's assigned bed position.
func (s *AgentState) SetAssignedBed(pos *civquestv1.BlockPosProto) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos == nil {
		s.AssignedBed = nil
		return
	}
	s.AssignedBed = &civquestv1.BlockPosProto{
		X: pos.X,
		Y: pos.Y,
		Z: pos.Z,
	}
}

// ClearAssignedBed clears the agent's assigned bed position.
func (s *AgentState) ClearAssignedBed() {
	s.SetAssignedBed(nil)
}

// SetPrimaryDutyBlocked marks whether the primary profession duty is currently blocked.
func (s *AgentState) SetPrimaryDutyBlocked(blocked bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PrimaryDutyBlocked = blocked
}

// IsPrimaryDutyBlocked returns whether the primary profession duty is blocked.
func (s *AgentState) IsPrimaryDutyBlocked() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PrimaryDutyBlocked
}

// SetArmoryMaintenanceNeeded updates whether armory maintenance chores are needed.
func (s *AgentState) SetArmoryMaintenanceNeeded(needed bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armoryMaintenanceNeeded = needed
}

// IsArmoryMaintenanceNeeded returns whether armory maintenance chores are needed.
// Defaults to true for 3rd shift guards unless explicitly satisfied or cleared.
func (s *AgentState) IsArmoryMaintenanceNeeded() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.armoryMaintenanceNeeded
}

// Stage returns the currently active Tier 1 macro strategic stage.
func (s *AgentState) Stage() Stage {
	if s == nil {
		return StageBootstrap
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stage == "" {
		if s.FSMState != "" {
			return s.FSMState
		}
		return StageBootstrap
	}
	return s.stage
}

// SetStage updates the active Tier 1 macro strategic stage.
func (s *AgentState) SetStage(st Stage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = st
	s.FSMState = st
}

// Routine returns the currently active Tier 2 circadian operational routine.
func (s *AgentState) Routine() Routine {
	if s == nil {
		return RoutineDawnPrep
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.routine == "" {
		if s.CurrentRoutine != "" {
			return s.CurrentRoutine
		}
		return RoutineDawnPrep
	}
	return s.routine
}

// SetRoutine updates the active Tier 2 circadian operational routine.
func (s *AgentState) SetRoutine(r Routine) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routine = r
	s.CurrentRoutine = r
}

// SetStageAndRoutine updates both Tier 1 stage and Tier 2 routine atomically under a single lock,
// guaranteeing zero desynchronization between macro strategy and operational routines.
func (s *AgentState) SetStageAndRoutine(st Stage, r Routine) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = st
	s.FSMState = st
	s.routine = r
	s.CurrentRoutine = r
}

// Tuple returns the unified (Stage, Routine) state tuple.
func (s *AgentState) Tuple() StateTuple {
	if s == nil {
		return StateTuple{Stage: StageBootstrap, Routine: RoutineDawnPrep}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.stage
	if st == "" {
		st = s.FSMState
		if st == "" {
			st = StageBootstrap
		}
	}
	r := s.routine
	if r == "" {
		r = s.CurrentRoutine
		if r == "" {
			r = RoutineDawnPrep
		}
	}
	return StateTuple{Stage: st, Routine: r}
}

// GetCurrentRoutine returns the currently active routine state (backward compatibility).
func (s *AgentState) GetCurrentRoutine() RoutineState {
	return s.Routine()
}

// SetCurrentRoutine sets the active routine state (backward compatibility).
func (s *AgentState) SetCurrentRoutine(r RoutineState) {
	s.SetRoutine(r)
}

// GetFSMState returns the active formal System 1 HFSM state (backward compatibility).
func (s *AgentState) GetFSMState() State {
	return s.Stage()
}

// SetFSMState updates the active formal System 1 HFSM state (backward compatibility).
func (s *AgentState) SetFSMState(st State) {
	s.SetStage(st)
}

// GetLastEatingTick safely returns the world tick of the last consumption event.
func (s *AgentState) GetLastEatingTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastEatingTick
}

// SetLastEatingTick safely records the world tick of an item consumption event.
func (s *AgentState) SetLastEatingTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEatingTick = tick
}

// GetLastSleepAttemptTick safely returns the world tick of the last bed interaction attempt.
func (s *AgentState) GetLastSleepAttemptTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSleepAttemptTick
}

// SetLastSleepAttemptTick safely records the world tick of a bed interaction attempt.
func (s *AgentState) SetLastSleepAttemptTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSleepAttemptTick = tick
}

// IsCompanionActive returns true if the agent is actively accompanying a player as a bonded companion.
func (s *AgentState) IsCompanionActive() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.companionActive
}

// SetCompanionActive sets whether the agent is actively accompanying a player as a bonded companion.
func (s *AgentState) SetCompanionActive(active bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.companionActive = active
}

// IsCivicPatrolActive returns whether civic patrol / sentry duty is currently active.
func (s *AgentState) IsCivicPatrolActive() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.civicPatrolActive
}

// SetCivicPatrolActive sets whether civic patrol / sentry duty is active.
func (s *AgentState) SetCivicPatrolActive(active bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.civicPatrolActive = active
}

// GetShelterPos returns a defensive copy of the agent's known shelter position.
func (s *AgentState) GetShelterPos() *civquestv1.BlockPosProto {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ShelterPos == nil {
		return nil
	}
	return &civquestv1.BlockPosProto{
		X: s.ShelterPos.X,
		Y: s.ShelterPos.Y,
		Z: s.ShelterPos.Z,
	}
}

// SetShelterPos sets the agent's shelter position.
func (s *AgentState) SetShelterPos(pos *civquestv1.BlockPosProto) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos == nil {
		s.ShelterPos = nil
		return
	}
	s.ShelterPos = &civquestv1.BlockPosProto{
		X: pos.X,
		Y: pos.Y,
		Z: pos.Z,
	}
}

// GetHomeOrigin returns a defensive copy of the agent's home/outpost origin.
func (s *AgentState) GetHomeOrigin() *civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.homeOrigin == nil {
		return nil
	}
	return &civquestv1.Vec3{
		X: s.homeOrigin.X,
		Y: s.homeOrigin.Y,
		Z: s.homeOrigin.Z,
	}
}

// SetHomeOrigin updates the agent's home/outpost origin.
func (s *AgentState) SetHomeOrigin(pos *civquestv1.Vec3) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos == nil {
		s.homeOrigin = nil
		return
	}
	s.homeOrigin = &civquestv1.Vec3{
		X: pos.X,
		Y: pos.Y,
		Z: pos.Z,
	}
}

// GetCurrentOutpost returns a defensive copy of the agent's active forward outpost.
func (s *AgentState) GetCurrentOutpost() *civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentOutpost != nil {
		return &civquestv1.Vec3{
			X: s.currentOutpost.X,
			Y: s.currentOutpost.Y,
			Z: s.currentOutpost.Z,
		}
	}
	if s.homeOrigin != nil {
		return &civquestv1.Vec3{
			X: s.homeOrigin.X,
			Y: s.homeOrigin.Y,
			Z: s.homeOrigin.Z,
		}
	}
	return nil
}

// GetOutposts returns a defensive copy of all established forward outposts.
func (s *AgentState) GetOutposts() []*civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*civquestv1.Vec3, len(s.outposts))
	for i, p := range s.outposts {
		if p != nil {
			out[i] = &civquestv1.Vec3{X: p.X, Y: p.Y, Z: p.Z}
		}
	}
	return out
}

// GetGoals returns the current 3-horizon GoalHierarchy.
func (s *AgentState) GetGoals() GoalHierarchy {
	if s == nil {
		return GoalHierarchy{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.goals
}

// SetGoals updates the 3-horizon GoalHierarchy.
func (s *AgentState) SetGoals(g GoalHierarchy) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goals = g
}

// GetTelemetryFramesSeen returns the number of telemetry frames observed by this state.
func (s *AgentState) GetTelemetryFramesSeen() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.telemetryFramesSeen
}

// GetLastShelterPlaceTick returns the world tick when a shelter block/torch/campfire was last placed.
func (s *AgentState) GetLastShelterPlaceTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastShelterPlaceTick
}

// SetLastShelterPlaceTick records the world tick when a shelter block/torch/campfire was placed.
func (s *AgentState) SetLastShelterPlaceTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastShelterPlaceTick = tick
}

// GetLastShelterTimeoutTick safely returns the world tick of the last shelter fallback timeout.
func (s *AgentState) GetLastShelterTimeoutTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastShelterTimeoutTick
}

// SetLastShelterTimeoutTick safely records the world tick of a shelter fallback timeout.
func (s *AgentState) SetLastShelterTimeoutTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastShelterTimeoutTick = tick
}

// IsShelterOnCooldown returns true if shelter construction is on backoff cooldown after a timeout.
func (s *AgentState) IsShelterOnCooldown(currentTick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastShelterTimeoutTick <= 0 {
		return false
	}
	return (currentTick - s.lastShelterTimeoutTick) < 600
}

// GetLastBootstrapTimeoutTick safely returns the world tick of a bootstrap fallback timeout.
func (s *AgentState) GetLastBootstrapTimeoutTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBootstrapTimeoutTick
}

// SetLastBootstrapTimeoutTick safely records the world tick of a bootstrap fallback timeout.
func (s *AgentState) SetLastBootstrapTimeoutTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBootstrapTimeoutTick = tick
}

// IsBootstrapOnCooldown returns true if bootstrap retry is on backoff cooldown after a timeout.
func (s *AgentState) IsBootstrapOnCooldown(currentTick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastBootstrapTimeoutTick <= 0 {
		return false
	}
	return (currentTick - s.lastBootstrapTimeoutTick) < 600
}

// IsEstablishingNewSettlement returns true if the agent is founding a new settlement node.
func (s *AgentState) IsEstablishingNewSettlement() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.establishingNewSettlement
}

// SetEstablishingNewSettlement sets the flag indicating the agent is founding a new settlement node.
func (s *AgentState) SetEstablishingNewSettlement(establishing bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.establishingNewSettlement = establishing
}

// GetLastHarvestTick safely returns the tick of the last block harvesting event.
func (s *AgentState) GetLastHarvestTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHarvestTick
}

// SetLastHarvestTick safely records the tick of the last block harvesting event.
func (s *AgentState) SetLastHarvestTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHarvestTick = tick
}

// GetLastCraftTick safely returns the tick of the last crafting event.
func (s *AgentState) GetLastCraftTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCraftTick
}

// SetLastCraftTick safely records the tick of the last crafting event.
func (s *AgentState) SetLastCraftTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCraftTick = tick
}

// GetShelterBlocksPlaced returns the count of emergency shelter wall blocks placed during the current night.
func (s *AgentState) GetShelterBlocksPlaced() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shelterBlocksPlaced
}

// SetShelterBlocksPlaced updates the count of emergency shelter wall blocks placed.
func (s *AgentState) SetShelterBlocksPlaced(count int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shelterBlocksPlaced = count
}

// GetShelterBuildPhase returns the active construction phase for the emergency shelter / frontier cabin.
func (s *AgentState) GetShelterBuildPhase() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ShelterBuildPhase
}

// SetShelterBuildPhase safely updates the active construction phase for the frontier cabin.
func (s *AgentState) SetShelterBuildPhase(phase int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShelterBuildPhase = phase
}

// GetShelterBuildIndex returns the current block index within the frontier cabin construction sequence.
func (s *AgentState) GetShelterBuildIndex() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ShelterBuildIndex
}

// SetShelterBuildIndex safely updates the block index within the frontier cabin construction sequence.
func (s *AgentState) SetShelterBuildIndex(idx int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShelterBuildIndex = idx
}

// GetFrontierAzimuth returns the current locked frontier sector azimuth heading and whether it is set.
func (s *AgentState) GetFrontierAzimuth() (float64, bool) {
	if s == nil {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.frontierAzimuthRad, s.frontierAzimuthSet
}

// SetFrontierAzimuth locks the agent's outward frontier sector azimuth heading.
func (s *AgentState) SetFrontierAzimuth(rad float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frontierAzimuthRad = rad
	s.frontierAzimuthSet = true
	s.lastHeadingRad = rad
}

// GetFrontierRadialLeg returns the current progressive outward radial leg index.
func (s *AgentState) GetFrontierRadialLeg() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.frontierRadialLeg
}

// SetFrontierRadialLeg sets the current progressive outward radial leg index.
func (s *AgentState) SetFrontierRadialLeg(leg int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frontierRadialLeg = leg
}

// GetCurrentExploreTarget returns a defensive copy of the current active exploration target waypoint.
func (s *AgentState) GetCurrentExploreTarget() *civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentExploreTarget == nil {
		return nil
	}
	return &civquestv1.Vec3{
		X: s.currentExploreTarget.X,
		Y: s.currentExploreTarget.Y,
		Z: s.currentExploreTarget.Z,
	}
}

// SetCurrentExploreTarget sets the current active exploration target waypoint.
func (s *AgentState) SetCurrentExploreTarget(pos *civquestv1.Vec3) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos == nil {
		s.currentExploreTarget = nil
		return
	}
	s.currentExploreTarget = &civquestv1.Vec3{
		X: pos.X,
		Y: pos.Y,
		Z: pos.Z,
	}
}

// GetLastHeadingRad returns the last recorded heading in radians.
func (s *AgentState) GetLastHeadingRad() float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHeadingRad
}

// SetLastHeadingRad sets the last recorded heading in radians.
func (s *AgentState) SetLastHeadingRad(rad float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeadingRad = rad
}

// GetExploreStepIndex returns the exploration/patrol step index.
func (s *AgentState) GetExploreStepIndex() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exploreStepIndex
}

// SetExploreStepIndex sets the exploration/patrol step index.
func (s *AgentState) SetExploreStepIndex(idx int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exploreStepIndex = idx
}

// GetPatrolWaypoints returns a defensive copy of configured patrol waypoints.
func (s *AgentState) GetPatrolWaypoints() []*civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*civquestv1.Vec3, len(s.patrolWaypoints))
	for i, p := range s.patrolWaypoints {
		if p != nil {
			out[i] = &civquestv1.Vec3{X: p.X, Y: p.Y, Z: p.Z}
		}
	}
	return out
}

// SetPatrolWaypoints sets configured patrol waypoints.
func (s *AgentState) SetPatrolWaypoints(waypoints []*civquestv1.Vec3) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patrolWaypoints = make([]*civquestv1.Vec3, len(waypoints))
	for i, p := range waypoints {
		if p != nil {
			s.patrolWaypoints[i] = &civquestv1.Vec3{X: p.X, Y: p.Y, Z: p.Z}
		}
	}
}

// GetPatrolWaypointIndex returns the current patrol waypoint index.
func (s *AgentState) GetPatrolWaypointIndex() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.patrolWaypointIndex
}

// SetPatrolWaypointIndex sets the current patrol waypoint index.
func (s *AgentState) SetPatrolWaypointIndex(idx int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patrolWaypointIndex = idx
}

// GetPavingTracker returns the agent's RoadPavingTracker.
func (s *AgentState) GetPavingTracker() *RoadPavingTracker {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PavingTracker
}

// SetPavingTracker sets the agent's RoadPavingTracker.
func (s *AgentState) SetPavingTracker(t *RoadPavingTracker) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PavingTracker = t
}

// GetUnstuckLock returns the active unstuck escape direction lock.
func (s *AgentState) GetUnstuckLock() (locked bool, dirX, dirZ int32, startTick int64, startX, startZ float64) {
	if s == nil {
		return false, 0, 0, 0, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.GetUnstuckLockLocked()
}

// GetUnstuckLockLocked returns the active unstuck escape direction lock assuming mutex is held.
func (s *AgentState) GetUnstuckLockLocked() (locked bool, dirX, dirZ int32, startTick int64, startX, startZ float64) {
	if s == nil {
		return false, 0, 0, 0, 0, 0
	}
	return s.unstuckLocked, s.unstuckDirX, s.unstuckDirZ, s.unstuckStartTick, s.unstuckStartX, s.unstuckStartZ
}

// SetUnstuckLock locks the unstuck escape direction and origin.
func (s *AgentState) SetUnstuckLock(locked bool, dirX, dirZ int32, startTick int64, startX, startZ float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SetUnstuckLockLocked(locked, dirX, dirZ, startTick, startX, startZ)
}

// SetUnstuckLockLocked locks the unstuck escape direction assuming mutex is held.
func (s *AgentState) SetUnstuckLockLocked(locked bool, dirX, dirZ int32, startTick int64, startX, startZ float64) {
	if s == nil {
		return
	}
	s.unstuckLocked = locked
	s.unstuckDirX = dirX
	s.unstuckDirZ = dirZ
	s.unstuckStartTick = startTick
	s.unstuckStartX = startX
	s.unstuckStartZ = startZ
}

// ClearUnstuckLock clears any active unstuck escape lock.
func (s *AgentState) ClearUnstuckLock() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClearUnstuckLockLocked()
}

// ClearUnstuckLockLocked clears any active unstuck escape lock assuming mutex is held.
func (s *AgentState) ClearUnstuckLockLocked() {
	if s == nil {
		return
	}
	s.unstuckLocked = false
	s.unstuckDirX = 0
	s.unstuckDirZ = 0
	s.unstuckStartTick = 0
	s.unstuckStartX = 0
	s.unstuckStartZ = 0
}

// GetTargetTree returns the focused tree trunk coordinates.
func (s *AgentState) GetTargetTree() (locked bool, x, z int32, startTick int64) {
	if s == nil {
		return false, 0, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.GetTargetTreeLocked()
}

// GetTargetTreeLocked returns the focused tree trunk coordinates assuming mutex is held.
func (s *AgentState) GetTargetTreeLocked() (locked bool, x, z int32, startTick int64) {
	if s == nil {
		return false, 0, 0, 0
	}
	return s.targetTreeLocked, s.targetTreeX, s.targetTreeZ, s.targetTreeStartTick
}

// SetTargetTree sets the focused tree trunk coordinates.
func (s *AgentState) SetTargetTree(locked bool, x, z int32, startTick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SetTargetTreeLocked(locked, x, z, startTick)
}

// SetTargetTreeLocked sets the focused tree trunk coordinates assuming mutex is held.
func (s *AgentState) SetTargetTreeLocked(locked bool, x, z int32, startTick int64) {
	if s == nil {
		return
	}
	s.targetTreeLocked = locked
	s.targetTreeX = x
	s.targetTreeZ = z
	s.targetTreeStartTick = startTick
}

// ClearTargetTree clears the focused tree trunk.
func (s *AgentState) ClearTargetTree() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClearTargetTreeLocked()
}

// ClearTargetTreeLocked clears the focused tree trunk assuming mutex is held.
func (s *AgentState) ClearTargetTreeLocked() {
	if s == nil {
		return
	}
	s.targetTreeLocked = false
	s.targetTreeX = 0
	s.targetTreeZ = 0
	s.targetTreeStartTick = 0
}

// StartTask installs a committed TaskSubroutine on the agent state.
func (s *AgentState) StartTask(sub *TaskSubroutine) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.StartTaskLocked(sub)
}

// StartTaskLocked installs a committed TaskSubroutine assuming mutex is held.
func (s *AgentState) StartTaskLocked(sub *TaskSubroutine) {
	if s == nil {
		return
	}
	s.activeTask = sub
	if sub != nil && sub.Spec.Kind == "harvest_tree" && sub.Spec.Target.BlockPos != nil {
		s.SetTargetTreeLocked(true, sub.Spec.Target.BlockPos.X, sub.Spec.Target.BlockPos.Z, sub.StartedTick)
	}
}

// GetActiveTask returns the currently active TaskSubroutine if any.
func (s *AgentState) GetActiveTask() *TaskSubroutine {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeTask
}

// GetActiveTaskLocked returns the currently active TaskSubroutine assuming mutex is held.
func (s *AgentState) GetActiveTaskLocked() *TaskSubroutine {
	if s == nil {
		return nil
	}
	return s.activeTask
}

// HasActiveTask returns true if an active, non-terminal TaskSubroutine is committed.
func (s *AgentState) HasActiveTask() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HasActiveTaskLocked()
}

// HasActiveTaskLocked returns true if an active, non-terminal TaskSubroutine is committed assuming mutex is held.
func (s *AgentState) HasActiveTaskLocked() bool {
	if s == nil || s.activeTask == nil {
		return false
	}
	return s.activeTask.Phase != TaskPhaseComplete && s.activeTask.Phase != TaskPhaseFailed
}

// ActiveTaskInfo returns a race-free value snapshot of the active task's status.
func (s *AgentState) ActiveTaskInfo() (active bool, kind string, phase TaskPhase, routine Routine) {
	if s == nil {
		return false, "", "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeTask == nil || s.activeTask.Phase == TaskPhaseComplete || s.activeTask.Phase == TaskPhaseFailed {
		return false, "", "", ""
	}
	return true, s.activeTask.Spec.Kind, s.activeTask.Phase, s.activeTask.Spec.Routine
}

// IsActiveTaskPushedToPDA returns whether the active task's parent frame has been pushed onto the PDA stack.
func (s *AgentState) IsActiveTaskPushedToPDA() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeTask != nil && s.activeTask.PushedToPDA
}

// SetActiveTaskPushedToPDA updates whether the active task's parent frame is on the PDA stack.
func (s *AgentState) SetActiveTaskPushedToPDA(pushed bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTask != nil {
		s.activeTask.PushedToPDA = pushed
	}
}

// GetActiveTaskMetadata returns a defensive metadata snapshot for the active task, or nil if none.
func (s *AgentState) GetActiveTaskMetadata() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeTask == nil {
		return nil
	}
	return s.activeTask.MetadataSnapshot()
}

// PauseActiveTask marks the active task as paused at the given tick during emergency preemption.
func (s *AgentState) PauseActiveTask(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTask != nil {
		s.activeTask.Pause(tick)
	}
}

// HandleEmergencyPreempt pauses a preemptible active task and returns its metadata for the PDA stack,
// or clears a non-preemptible active task so it is not resumed after emergency clearance.
func (s *AgentState) HandleEmergencyPreempt(tick int64) map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTask == nil || s.activeTask.Phase == TaskPhaseComplete || s.activeTask.Phase == TaskPhaseFailed {
		return nil
	}
	if !s.activeTask.Spec.Preemptible {
		s.ClearActiveTaskLocked()
		return nil
	}
	s.activeTask.Pause(tick)
	return s.activeTask.MetadataSnapshot()
}

// ResumeActiveTask resumes the active task after emergency clearance, compensating timeout ticks.
func (s *AgentState) ResumeActiveTask(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTask != nil {
		s.activeTask.Resume(tick)
	}
}

// IngestReceipt stores the latest IntentReceipt from a telemetry frame so
// active tasks can react immediately to completion, preemption, or failure.
func (s *AgentState) IngestReceipt(receipt *civquestv1.IntentReceipt) {
	if s == nil || receipt == nil {
		return
	}
	cloned := proto.Clone(receipt).(*civquestv1.IntentReceipt)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReceipt = cloned
	s.pendingReceipt = proto.Clone(cloned).(*civquestv1.IntentReceipt)
}

// GetLastReceipt returns a defensive copy of the most recently ingested IntentReceipt.
func (s *AgentState) GetLastReceipt() *civquestv1.IntentReceipt {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastReceipt == nil {
		return nil
	}
	return proto.Clone(s.lastReceipt).(*civquestv1.IntentReceipt)
}

// StepActiveTask advances the active TaskSubroutine by one tick, clearing it upon completion or failure.
func (s *AgentState) StepActiveTask(agentID string, frame *civquestv1.TelemetryFrame) TaskStepResult {
	if s == nil {
		return TaskStepResult{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.StepActiveTaskLocked(agentID, frame)
}

// StepActiveTaskLocked advances the active TaskSubroutine assuming mutex is held.
func (s *AgentState) StepActiveTaskLocked(agentID string, frame *civquestv1.TelemetryFrame) TaskStepResult {
	if s == nil || s.activeTask == nil {
		if s != nil {
			s.pendingReceipt = nil
		}
		return TaskStepResult{}
	}
	var receipt *civquestv1.IntentReceipt
	if frame != nil && frame.GetReceipt() != nil {
		receipt = frame.GetReceipt()
		s.lastReceipt = proto.Clone(receipt).(*civquestv1.IntentReceipt)
	} else if s.pendingReceipt != nil {
		receipt = s.pendingReceipt
	}
	s.pendingReceipt = nil

	res := s.activeTask.TickWithReceipt(agentID, s, frame, receipt)
	if res.Done {
		s.activeTask = nil
	}
	return res
}

// ClearActiveTask clears any active task subroutine and unlocks any focused tree.
func (s *AgentState) ClearActiveTask() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClearActiveTaskLocked()
}

// ClearActiveTaskLocked clears any active task subroutine assuming mutex is held.
func (s *AgentState) ClearActiveTaskLocked() {
	if s == nil {
		return
	}
	s.activeTask = nil
	s.ClearTargetTreeLocked()
}

// MarkTreeFailedLocked records a temporary cooldown for an unreachable/blocked tree trunk.
func (s *AgentState) MarkTreeFailedLocked(x, z int32, tick int64) {
	if s == nil {
		return
	}
	if s.failedTrees == nil {
		s.failedTrees = make(map[[2]int32]int64)
	}
	s.failedTrees[[2]int32{x, z}] = tick
}

// IsTreeOnCooldownLocked returns true if a tree trunk recently timed out and is on retry backoff.
func (s *AgentState) IsTreeOnCooldownLocked(x, z int32, tick int64) bool {
	if s == nil || s.failedTrees == nil {
		return false
	}
	failedTick, ok := s.failedTrees[[2]int32{x, z}]
	if !ok {
		return false
	}
	if tick < failedTick || tick-failedTick >= 600 {
		delete(s.failedTrees, [2]int32{x, z})
		return false
	}
	return true
}

// MarkWorkstationUnreachable records a temporary cooldown for an unreachable/blocked workstation.
func (s *AgentState) MarkWorkstationUnreachable(x, y, z int32, tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MarkWorkstationUnreachableLocked(x, y, z, tick)
}

// MarkWorkstationUnreachableLocked records workstation failure while s.mu is already held.
func (s *AgentState) MarkWorkstationUnreachableLocked(x, y, z int32, tick int64) {
	if s == nil {
		return
	}
	if s.unreachableWorkstations == nil {
		s.unreachableWorkstations = make(map[[3]int32]int64)
	}
	s.unreachableWorkstations[[3]int32{x, y, z}] = tick
}

// IsWorkstationUnreachable returns true if a workstation is marked unreachable and still on cooldown.
func (s *AgentState) IsWorkstationUnreachable(x, y, z int32, tick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsWorkstationUnreachableLocked(x, y, z, tick)
}

// IsWorkstationUnreachableLocked checks workstation cooldown while s.mu is already held.
func (s *AgentState) IsWorkstationUnreachableLocked(x, y, z int32, tick int64) bool {
	if s == nil || s.unreachableWorkstations == nil {
		return false
	}
	failedTick, ok := s.unreachableWorkstations[[3]int32{x, y, z}]
	if !ok {
		return false
	}
	if tick < failedTick || tick-failedTick >= 600 {
		return false
	}
	return true
}

func unreachableCoordsMatch(ax, ay, az, bx, by, bz float64) bool {
	dx := ax - bx
	dy := ay - by
	dz := az - bz
	return dx*dx+dy*dy+dz*dz <= unreachableTargetRadius*unreachableTargetRadius
}

func (s *AgentState) pruneExpiredUnreachableLocked(worldTick int64) {
	if s == nil || len(s.unreachableTargets) == 0 {
		return
	}
	kept := s.unreachableTargets[:0]
	for _, entry := range s.unreachableTargets {
		if entry == nil {
			continue
		}
		if entry.blacklistUntil > 0 && worldTick >= entry.blacklistUntil {
			continue
		}
		if entry.blacklistUntil == 0 && worldTick >= entry.lastFailTick && worldTick-entry.lastFailTick >= DefaultUnreachableTargetTTL {
			continue
		}
		kept = append(kept, entry)
	}
	s.unreachableTargets = kept
}

// MarkTargetUnreachable blacklists coordinates within 2.5m of (x, y, z) for ttlTicks.
func (s *AgentState) MarkTargetUnreachable(x, y, z float64, worldTick, ttlTicks int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MarkTargetUnreachableLocked(x, y, z, worldTick, ttlTicks)
}

// MarkTargetUnreachableLocked blacklists coordinates within 2.5m of (x, y, z) while s.mu is held.
func (s *AgentState) MarkTargetUnreachableLocked(x, y, z float64, worldTick, ttlTicks int64) {
	if s == nil {
		return
	}
	if ttlTicks <= 0 {
		ttlTicks = DefaultUnreachableTargetTTL
	}
	s.pruneExpiredUnreachableLocked(worldTick)
	for _, entry := range s.unreachableTargets {
		if unreachableCoordsMatch(entry.x, entry.y, entry.z, x, y, z) {
			if entry.failures < 2 {
				entry.failures = 2
			}
			entry.lastFailTick = worldTick
			if worldTick+ttlTicks > entry.blacklistUntil {
				entry.blacklistUntil = worldTick + ttlTicks
			}
			return
		}
	}
	s.unreachableTargets = append(s.unreachableTargets, &unreachableTargetEntry{
		x:              x,
		y:              y,
		z:              z,
		failures:       2,
		lastFailTick:   worldTick,
		blacklistUntil: worldTick + ttlTicks,
	})
}

// RecordTargetNavigationStuck records a NAVIGATION_STATE_STUCK failure near (x, y, z).
// After 2 stuck failures within 2.5m, the target coordinate is blacklisted for ttlTicks.
func (s *AgentState) RecordTargetNavigationStuck(x, y, z float64, worldTick, ttlTicks int64) (bool, int) {
	if s == nil {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RecordTargetNavigationStuckLocked(x, y, z, worldTick, ttlTicks)
}

// RecordTargetNavigationStuckLocked records a NAVIGATION_STATE_STUCK failure while s.mu is held.
func (s *AgentState) RecordTargetNavigationStuckLocked(x, y, z float64, worldTick, ttlTicks int64) (bool, int) {
	if s == nil {
		return false, 0
	}
	if ttlTicks <= 0 {
		ttlTicks = DefaultUnreachableTargetTTL
	}
	s.pruneExpiredUnreachableLocked(worldTick)
	for _, entry := range s.unreachableTargets {
		if unreachableCoordsMatch(entry.x, entry.y, entry.z, x, y, z) {
			if entry.lastFailTick == worldTick && worldTick != 0 {
				return entry.blacklistUntil > 0 && worldTick < entry.blacklistUntil, entry.failures
			}
			entry.failures++
			entry.lastFailTick = worldTick
			if entry.failures >= 2 {
				if worldTick+ttlTicks > entry.blacklistUntil {
					entry.blacklistUntil = worldTick + ttlTicks
				}
				return true, entry.failures
			}
			return false, entry.failures
		}
	}
	s.unreachableTargets = append(s.unreachableTargets, &unreachableTargetEntry{
		x:            x,
		y:            y,
		z:            z,
		failures:     1,
		lastFailTick: worldTick,
	})
	return false, 1
}

// IsTargetUnreachable returns true if (x, y, z) is within 2.5m of an active blacklisted coordinate.
func (s *AgentState) IsTargetUnreachable(x, y, z float64, worldTick int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsTargetUnreachableLocked(x, y, z, worldTick)
}

// IsTargetUnreachableLocked returns true if (x, y, z) is blacklisted while s.mu is held.
func (s *AgentState) IsTargetUnreachableLocked(x, y, z float64, worldTick int64) bool {
	if s == nil || len(s.unreachableTargets) == 0 {
		return false
	}
	for _, entry := range s.unreachableTargets {
		if entry == nil || entry.blacklistUntil <= 0 {
			continue
		}
		if worldTick >= entry.blacklistUntil {
			continue
		}
		if unreachableCoordsMatch(entry.x, entry.y, entry.z, x, y, z) {
			return true
		}
	}
	return false
}

// isPOIUnreachableLocked returns true if either a POI's block coordinate or its access target is blacklisted.
func (s *AgentState) isPOIUnreachableLocked(poi *db.AgentPOI, worldTick int64) bool {
	if s == nil || poi == nil {
		return false
	}
	if s.IsTargetUnreachableLocked(poi.X, poi.Y, poi.Z, worldTick) {
		return true
	}
	acc := poiAccessTarget(poi)
	if acc != nil && s.IsTargetUnreachableLocked(acc.X, acc.Y, acc.Z, worldTick) {
		return true
	}
	return false
}

// findNearestReachablePOILocked returns the nearest active, reliable POI matching the filters
// that is not currently blacklisted in unreachableTargets. Caller must hold s.mu.
func (s *AgentState) findNearestReachablePOILocked(
	pos *civquestv1.Vec3,
	category, blockID, elevationLayer string,
	maxDist, minReliability float64,
	worldTick int64,
) *db.AgentPOI {
	if s == nil || pos == nil {
		return nil
	}
	si := s.SpatialIndexLocked()
	if si == nil {
		return nil
	}
	blk := strings.TrimSpace(blockID)
	layer := strings.TrimSpace(elevationLayer)
	for _, p := range si.FindPOIsInRadius(pos, category, maxDist) {
		if p == nil || !p.IsActive || p.Reliability < minReliability {
			continue
		}
		if layer != "" && !strings.EqualFold(p.ElevationLayer, layer) {
			continue
		}
		if blk != "" && !matchesPOIBlockID(p.BlockID, blk) {
			continue
		}
		if s.isPOIUnreachableLocked(p, worldTick) {
			continue
		}
		return p
	}
	return nil
}

// FindNearestReachablePOI returns the nearest active, reliable, non-blacklisted POI.
func (s *AgentState) FindNearestReachablePOI(
	pos *civquestv1.Vec3,
	category, blockID, elevationLayer string,
	maxDist, minReliability float64,
	worldTick int64,
) *db.AgentPOI {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findNearestReachablePOILocked(pos, category, blockID, elevationLayer, maxDist, minReliability, worldTick)
}

// SetStrategicDirective installs or replaces the active System 2 strategic directive (ADR-031).
func (s *AgentState) SetStrategicDirective(action, target, immediateGoal string, issuedTick, expiresTick int64) {
	if s == nil {
		return
	}
	normAction := strings.ToUpper(strings.TrimSpace(action))
	s.mu.Lock()
	defer s.mu.Unlock()
	if normAction == "" {
		s.strategicDirective = nil
		s.directiveScoutTarget = nil
		s.lastDirectiveActionTick = 0
		s.lastDirectiveNavKey = ""
		return
	}
	s.strategicDirective = &StrategicDirectiveState{
		Action:        normAction,
		Target:        strings.TrimSpace(target),
		ImmediateGoal: strings.TrimSpace(immediateGoal),
		IssuedTick:    issuedTick,
		ExpiresTick:   expiresTick,
	}
	s.directiveScoutTarget = nil
	s.lastDirectiveActionTick = 0
	s.lastDirectiveNavKey = ""
}

// ActiveStrategicDirective returns the current non-expired StrategicDirectiveState, if any.
func (s *AgentState) ActiveStrategicDirective(worldTick int64) (StrategicDirectiveState, bool) {
	if s == nil {
		return StrategicDirectiveState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.strategicDirective == nil || s.strategicDirective.Action == "" {
		return StrategicDirectiveState{}, false
	}
	if s.strategicDirective.ExpiresTick > 0 && worldTick >= s.strategicDirective.ExpiresTick {
		return StrategicDirectiveState{}, false
	}
	return *s.strategicDirective, true
}

// ClearStrategicDirective clears any active System 2 strategic directive.
func (s *AgentState) ClearStrategicDirective() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategicDirective = nil
	s.directiveScoutTarget = nil
	s.lastDirectiveActionTick = 0
	s.lastDirectiveNavKey = ""
}
