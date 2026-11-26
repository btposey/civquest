package system1

import (
	"strings"
	"sync"
	"sync/atomic"

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

type receiptSlot struct {
	last *civquestv1.IntentReceipt
}

// agentMemory is the single explicit cross-activity Memory (ADR-036): knowledge and
// progress marks that outlive any one activity frame because several activities (and the
// liveness heuristic in fsm.go / fsm_context.go) read them. Activity-private progress does
// not belong here; it lives in the owning frame's Local.
type agentMemory struct {
	lastHarvestTick      int64 // any harvest action; read by unstuck/shelter throttles and liveness
	lastCraftTick        int64 // craft throttle shared by bootstrap/shelter/armory/directive; liveness
	lastShelterPlaceTick int64 // place throttle shared by bootstrap/shelter/armory/directive; liveness
	telemetryFramesSeen  int64 // multi-increment quirk (ADR-036 section: replay pins it)
	// failedTrees is durable tree-trunk knowledge with decay (failedTreeCooldownTicks).
	failedTrees map[[2]int32]int64
}

// failedTreeCooldownTicks is how long a trunk that timed out or was unreachable stays blacklisted.
const failedTreeCooldownTicks = 600

// AgentState tracks volatile in-memory System 1 agent routine, needs, and paving state.
type AgentState struct {
	mu                        sync.RWMutex
	mem                       agentMemory
	AssignedBed               *civquestv1.BlockPosProto
	bedRegistry               BedRegistry // clan-shared bed ownership port (nil: single-agent, no claims)
	bedWatch                  bedWatch    // evidence behind releasing a claim (bed_release.go)
	ShelterPos                *civquestv1.BlockPosProto
	stage                     Stage
	routine                   Routine
	PrimaryDutyBlocked        bool
	armoryMaintenanceNeeded   bool
	companionActive           bool
	civicPatrolActive         bool
	patrolWaypoints           []*civquestv1.Vec3
	patrolWaypointIndex       int
	lastSleepAttemptTick      int64
	lastShelterTimeoutTick    int64
	lastBootstrapTimeoutTick  int64
	establishingNewSettlement bool
	CabinBuilt                int // Store: blocks of the frontier cabin at ShelterPos already placed (banked by the Shelter frame on exit)
	PavingTracker             *RoadPavingTracker
	construction              constructionState // Store: blueprint project cursor + finished blueprints (construction_activity.go)

	// taskView is the read-only value projection of the committed task (ADR-036): a task's
	// progress is its frame Local (taskLocal) or, between starting inline and being framed, the
	// starting frame's walk cursor. Waterfalls and the kernel adapter publish this snapshot
	// whenever the task changes; readers (eligibility, facts, stage derivation) never see the
	// task itself.
	taskView                atomic.Pointer[taskInfo]
	unreachableWorkstations map[[3]int32]int64
	unreachableTargets      []*unreachableTargetEntry

	// Autonomous exploration, forward outposts & topological mapping state
	homeOrigin     *civquestv1.Vec3
	currentOutpost *civquestv1.Vec3
	outposts       []*civquestv1.Vec3
	lastMapNodeID  string
	lastMapPos     *civquestv1.Vec3
	goals          GoalHierarchy

	// Intent execution receipt tracking from the motor periphery
	receipt receiptSlot

	// Consolidated per-agent runtime state owned by AgentState
	runtimeState RuntimeTracking

	// In-memory 16x16x16 spatial grid index for persistent POIs (ADR-022)
	spatialIndex *SpatialIndex

	// Breadcrumb habit trail and corridor navigation tracker (ADR-023)
	habitTrailTracker *HabitTrailTracker

	// Dynamic runtime crafting recipe catalog synced from server RecipeManager (ADR-029)
	recipeGraph *crafting.RecipeGraph

	// Active System 2 -> System 1 strategic directive bridge state (ADR-031)
	strategicDirective *StrategicDirectiveState

	// Peripheral 360-degree horizon survey & chained expedition state (ADR-032)
	LatestSurvey     *civquestv1.HorizonSurveyReport
	LastSurveyTick   int64
	LastSurveyOrigin [3]float64
	ClanSize         int
	RendezvousOrigin *[3]float64 // nil = the home origin (resolved when an expedition starts)
	HomesteadClaimed bool
	HomesteadPos     [3]float64
	// Collective clan settlement site chosen at rendezvous (durable knowledge).
	CollectiveSiteChosen bool
	CollectiveSitePos    [3]float64
	clanSizeExplicit     bool
	// Derived projection of the activity stack (kinds, bottom first); not progress state:
	// expedition progress lives in the expedition frame Local.
	activityKinds []string
}

// NewAgentState creates an initialized AgentState.
func NewAgentState() *AgentState {
	return &AgentState{
		stage:                   StageBootstrap,
		routine:                 RoutineDawnPrep,
		armoryMaintenanceNeeded: true,
		PavingTracker:           NewRoadPavingTracker(DefaultPavingCapacity, DefaultPavingThreshold),
		spatialIndex:            NewSpatialIndex(),
		habitTrailTracker:       NewHabitTrailTracker(DefaultBreadcrumbDisplacement, DefaultArrivalThreshold, DefaultFinalTransitionDist),
		runtimeState: RuntimeTracking{
			Idle: RuntimeIdleTracking{
				LastContemplatedDay: -1,
			},
		},
		ClanSize: 1,
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

func (s *AgentState) stageLocked() Stage {
	if s == nil || s.stage == "" {
		return StageBootstrap
	}
	return s.stage
}

func (s *AgentState) routineLocked() Routine {
	if s == nil || s.routine == "" {
		return RoutineDawnPrep
	}
	return s.routine
}

// Stage returns the currently active Tier 1 macro strategic stage.
func (s *AgentState) Stage() Stage {
	if s == nil {
		return StageBootstrap
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stageLocked()
}

// SetStage updates the active Tier 1 macro strategic stage.
func (s *AgentState) SetStage(st Stage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = st
}

// Routine returns the currently active Tier 2 circadian operational routine.
func (s *AgentState) Routine() Routine {
	if s == nil {
		return RoutineDawnPrep
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.routineLocked()
}

// SetRoutine updates the active Tier 2 circadian operational routine.
func (s *AgentState) SetRoutine(r Routine) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routine = r
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
	s.routine = r
}

// Tuple returns the unified (Stage, Routine) state tuple.
func (s *AgentState) Tuple() StateTuple {
	if s == nil {
		return StateTuple{Stage: StageBootstrap, Routine: RoutineDawnPrep}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StateTuple{Stage: s.stageLocked(), Routine: s.routineLocked()}
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
	return s.mem.telemetryFramesSeen
}

// GetLastShelterPlaceTick returns the world tick when a shelter block/torch/campfire was last placed.
func (s *AgentState) GetLastShelterPlaceTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mem.lastShelterPlaceTick
}

// SetLastShelterPlaceTick records the world tick when a shelter block/torch/campfire was placed.
func (s *AgentState) SetLastShelterPlaceTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem.lastShelterPlaceTick = tick
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
	return s.mem.lastHarvestTick
}

// SetLastHarvestTick safely records the tick of the last block harvesting event.
func (s *AgentState) SetLastHarvestTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem.lastHarvestTick = tick
}

// GetLastCraftTick safely returns the tick of the last crafting event.
func (s *AgentState) GetLastCraftTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mem.lastCraftTick
}

// SetLastCraftTick safely records the tick of the last crafting event.
func (s *AgentState) SetLastCraftTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem.lastCraftTick = tick
}

// GetCabinBuilt returns the durable count of frontier-cabin blocks already placed at ShelterPos.
func (s *AgentState) GetCabinBuilt() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CabinBuilt
}

// SetCabinBuilt records the durable frontier-cabin block count (Store write by the Shelter frame).
func (s *AgentState) SetCabinBuilt(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CabinBuilt = n
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

// HasActiveTask returns true if an active, non-terminal TaskSubroutine is committed.
func (s *AgentState) HasActiveTask() bool {
	if s == nil {
		return false
	}
	return s.HasActiveTaskLocked()
}

// HasActiveTaskLocked is HasActiveTask for callers that hold the mutex (the view is atomic).
func (s *AgentState) HasActiveTaskLocked() bool {
	if s == nil {
		return false
	}
	v := s.taskView.Load()
	return v != nil && v.Active
}

// ActiveTaskInfo returns a race-free value snapshot of the active task's status.
func (s *AgentState) ActiveTaskInfo() (active bool, kind string, phase TaskPhase, routine Routine) {
	if s == nil {
		return false, "", "", ""
	}
	v := s.taskView.Load()
	if v == nil || !v.Active {
		return false, "", "", ""
	}
	return true, v.Kind, v.Phase, v.Routine
}

// IngestReceipt records the latest IntentReceipt from a telemetry frame for diagnostics
// (GetLastReceipt). It does NOT deliver the receipt to any activity: receipts reach
// activities only via Kernel.Dispatch, routed by frame ID.
func (s *AgentState) IngestReceipt(receipt *civquestv1.IntentReceipt) {
	s.SetLastReceipt(receipt)
}

// SetLastReceipt stores a defensive copy of the most recent IntentReceipt.
func (s *AgentState) SetLastReceipt(receipt *civquestv1.IntentReceipt) {
	if s == nil {
		return
	}
	var cloned *civquestv1.IntentReceipt
	if receipt != nil {
		cloned = proto.Clone(receipt).(*civquestv1.IntentReceipt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cloned == nil {
		s.receipt = receiptSlot{}
		return
	}
	s.receipt.last = cloned
}

// GetLastReceipt returns a defensive copy of the most recently ingested IntentReceipt.
func (s *AgentState) GetLastReceipt() *civquestv1.IntentReceipt {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.receipt.last == nil {
		return nil
	}
	return proto.Clone(s.receipt.last).(*civquestv1.IntentReceipt)
}

// MarkTreeFailedLocked records a temporary cooldown for an unreachable/blocked tree trunk.
func (s *AgentState) MarkTreeFailedLocked(x, z int32, tick int64) {
	if s == nil {
		return
	}
	if s.mem.failedTrees == nil {
		s.mem.failedTrees = make(map[[2]int32]int64)
	}
	s.mem.failedTrees[[2]int32{x, z}] = tick
}

// IsTreeOnCooldownLocked returns true if a tree trunk recently timed out and is on retry backoff.
func (s *AgentState) IsTreeOnCooldownLocked(x, z int32, tick int64) bool {
	if s == nil || s.mem.failedTrees == nil {
		return false
	}
	failedTick, ok := s.mem.failedTrees[[2]int32{x, z}]
	if !ok {
		return false
	}
	if tick < failedTick || tick-failedTick >= failedTreeCooldownTicks {
		delete(s.mem.failedTrees, [2]int32{x, z})
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
	if tick < failedTick || tick-failedTick >= failedTreeCooldownTicks {
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

// RecordTargetNavigationStuck records one Recover give-up near (x, y, z) (ADR-037 §4).
// After giveUps give-ups within 2.5m, the target coordinate is blacklisted for ttlTicks.
func (s *AgentState) RecordTargetNavigationStuck(x, y, z float64, worldTick, ttlTicks int64, giveUps int) (bool, int) {
	if s == nil {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RecordTargetNavigationStuckLocked(x, y, z, worldTick, ttlTicks, giveUps)
}

// RecordTargetNavigationStuckLocked records a Recover give-up while s.mu is held.
func (s *AgentState) RecordTargetNavigationStuckLocked(x, y, z float64, worldTick, ttlTicks int64, giveUps int) (bool, int) {
	if giveUps < 1 {
		giveUps = 1
	}
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
			if entry.failures >= giveUps {
				if worldTick+ttlTicks > entry.blacklistUntil {
					entry.blacklistUntil = worldTick + ttlTicks
				}
				return true, entry.failures
			}
			return false, entry.failures
		}
	}
	e := &unreachableTargetEntry{x: x, y: y, z: z, failures: 1, lastFailTick: worldTick}
	if giveUps <= 1 {
		e.blacklistUntil = worldTick + ttlTicks
	}
	s.unreachableTargets = append(s.unreachableTargets, e)
	return giveUps <= 1, 1
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
		return
	}
	s.strategicDirective = &StrategicDirectiveState{
		Action:        normAction,
		Target:        strings.TrimSpace(target),
		ImmediateGoal: strings.TrimSpace(immediateGoal),
		IssuedTick:    issuedTick,
		ExpiresTick:   expiresTick,
	}
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
}
