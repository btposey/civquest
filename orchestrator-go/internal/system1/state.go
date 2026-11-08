package system1

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// RoutineState represents the high-level daily routine bracket for an agent.
type RoutineState string

const (
	// RoutineEmergency represents critical survival reflex overrides (U = 1.0).
	RoutineEmergency RoutineState = "EMERGENCY"
	// RoutinePrimaryDuty represents primary profession duties during ticks 1000–8000.
	RoutinePrimaryDuty RoutineState = "PRIMARY_DUTY"
	// RoutineSecondaryHobby represents hobby gathering and practice during ticks 8000–11000.
	RoutineSecondaryHobby RoutineState = "SECONDARY_HOBBY"
	// RoutineTavernSocial represents tavern gathering and rumor sharing during ticks 11000–13000.
	RoutineTavernSocial RoutineState = "TAVERN_SOCIAL"
	// RoutineSleep represents navigating to assigned bed and sleeping during ticks 13000–23500.
	RoutineSleep RoutineState = "SLEEP"
	// RoutineDawnAwakening represents waking up, breakfast, and morning prep during ticks 23500–1000.
	RoutineDawnAwakening RoutineState = "DAWN_AWAKENING"
	// RoutineCamp represents camping with player companion during evening/night, overriding tavern and bed return.
	RoutineCamp RoutineState = "CAMP"
)

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
	mu                   sync.RWMutex
	AssignedBed          *civquestv1.BlockPosProto
	ShelterPos           *civquestv1.BlockPosProto
	CurrentRoutine       RoutineState
	PrimaryDutyBlocked   bool
	companionActive      bool
	lastEatingTick       int64
	lastSleepAttemptTick int64
	lastShelterPlaceTick int64
	lastHarvestTick      int64
	lastCraftTick        int64
	PavingTracker        *RoadPavingTracker

	// Autonomous exploration, forward outposts & topological mapping state
	homeOrigin          *civquestv1.Vec3
	currentOutpost      *civquestv1.Vec3
	outposts            []*civquestv1.Vec3
	lastExploreTick     int64
	exploreStepIndex    int
	lastHeadingRad      float64
	lastMapNodeID       string
	lastMapPos          *civquestv1.Vec3
	telemetryFramesSeen int64
	goals               GoalHierarchy
}

// NewAgentState creates an initialized AgentState.
func NewAgentState() *AgentState {
	return &AgentState{
		CurrentRoutine: RoutineDawnAwakening,
		PavingTracker:  NewRoadPavingTracker(1000, 3),
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

// GetCurrentRoutine returns the currently active routine state.
func (s *AgentState) GetCurrentRoutine() RoutineState {
	if s == nil {
		return RoutineDawnAwakening
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentRoutine
}

// SetCurrentRoutine sets the active routine state.
func (s *AgentState) SetCurrentRoutine(r RoutineState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentRoutine = r
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
