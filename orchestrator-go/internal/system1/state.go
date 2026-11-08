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

// AgentState tracks volatile in-memory System 1 agent routine, needs, and paving state.
type AgentState struct {
	mu                   sync.RWMutex
	AssignedBed          *civquestv1.BlockPosProto
	CurrentRoutine       RoutineState
	PrimaryDutyBlocked   bool
	lastEatingTick       int64
	lastSleepAttemptTick int64
	PavingTracker        *RoadPavingTracker
}

// NewAgentState creates an initialized AgentState.
func NewAgentState() *AgentState {
	return &AgentState{
		CurrentRoutine: RoutineDawnAwakening,
		PavingTracker:  NewRoadPavingTracker(1000, 3),
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
