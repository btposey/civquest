package system1

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

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
	// RoutineCamp represents camping with player companion during evening/night, overriding tavern and bed return.
	RoutineCamp Routine = "CAMP"
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
	companionActive           bool
	civicPatrolActive         bool
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

	// Autonomous exploration, forward outposts & topological mapping state
	homeOrigin              *civquestv1.Vec3
	currentOutpost          *civquestv1.Vec3
	outposts                []*civquestv1.Vec3
	lastExploreTick         int64
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
}

// NewAgentState creates an initialized AgentState.
func NewAgentState() *AgentState {
	return &AgentState{
		stage:          StageBootstrap,
		routine:        RoutineDawnPrep,
		CurrentRoutine: RoutineDawnPrep,
		FSMState:       StageBootstrap,
		PavingTracker:  NewRoadPavingTracker(DefaultPavingCapacity, DefaultPavingThreshold),
		goals: GoalHierarchy{
			ImmediateGoal: "GATHER_TIMBER: Gather 4 logs and craft basic wooden tools",
			ShortTermGoal: "ESTABLISH_OUTPOST_CHAIN: Advance frontier and establish safe day-night outposts with tools & light",
			LongTermGoal:  "CHART_REGIONAL_ATLAS: Map biome sectors, consecrate landmarks, and link outposts for Clan Cartographer",
		},
	}
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
