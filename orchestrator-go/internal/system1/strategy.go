package system1

// Strategic machines (ADR-040 stage 7): the one long-running strategy an agent is following. It
// is the base of the activity stack and decides which objectives may be launched, the shape of
// the day, and who the agent is meanwhile.
//
// A strategy is replaced by a hard milestone (at once), by the morning review of a proposal made
// during the night, or by the clan assigning one. Idle is a pause inside a strategy: it ends when
// work appears, and never becomes the strategy the agent returns to.

import "strings"

// Strategy names a strategic machine.
type Strategy string

const (
	StrategyExplore    Strategy = "EXPLORE"    // no settlement, or scouting its frontier
	StrategySettle     Strategy = "SETTLE"     // making a home in the settlement
	StrategyProfession Strategy = "PROFESSION" // established: following a trade
	StrategyIdle       Strategy = "IDLE"       // nothing to do: time to think
)

// DESIGN BUDGET (cq-507.7): durations are first guesses to tune in play. A day is 24000 ticks.
const (
	professionTenureTicks  = int64(12000) // settled with a home this long takes up a profession
	professionMaxWaitTicks = int64(72000) // taken up after this long whatever the home
	reviewTenureTicks      = int64(48000) // a strategy runs this long before a review may replace it
	idleAfterTicks         = int64(600)   // nothing to do this long turns idle
	idleLimitTicks         = int64(600)   // idle gives up waiting for advice after this long
	morningEndsAt          = int64(6000)
	gatherNearHome         = 48.0
)

// StrategyProposal is a change System 2 suggested, to be weighed in the morning.
type StrategyProposal struct {
	Strategy Strategy
	Reason   string // what is new since the last review
	Tick     int64
}

// StrategyRecord is the agent's strategic state; it is saved across restarts.
type StrategyRecord struct {
	Kind  Strategy // "" until first decided
	Since int64    // when Resume (or Kind) began
	// Resume is what idle returns to; "" unless Kind is idle.
	Resume Strategy
	// Chosen marks a strategy picked by review or assignment: milestones leave it alone.
	Chosen      bool
	ReviewedDay int64
	Proposal    *StrategyProposal
}

// base is the strategy under any idle pause.
func (r StrategyRecord) base() Strategy {
	if r.Kind == StrategyIdle {
		return r.Resume
	}
	return r.Kind
}

// StrategyChange describes a replacement, for diagnostics.
type StrategyChange struct {
	From, To Strategy
	Why      string
}

// strategyLocked is the strategy in force, milestones already applied: a settlement ends
// exploring at once, and without one there is nothing but exploring.
func (s *AgentState) strategyLocked() StrategyRecord {
	r := s.strategy
	_, settled := s.settlementLocked()
	base := r.base()
	switch {
	case base == "" && settled, base == StrategyExplore && settled && !r.Chosen:
		base = StrategySettle
	case base == "" || !settled && !r.Chosen:
		base = StrategyExplore
	default:
		return r
	}
	if r.Kind == StrategyIdle && base == r.Resume {
		return r
	}
	r.Kind, r.Resume = base, ""
	return r
}

// Strategy returns the machine the agent is in, StrategyIdle included.
func (s *AgentState) Strategy() Strategy {
	if s == nil {
		return StrategyExplore
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.strategyLocked().Kind
}

// BaseStrategy returns the strategy the agent is following, looking through an idle pause.
func (s *AgentState) BaseStrategy() Strategy {
	if s == nil {
		return StrategyExplore
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.strategyLocked().base()
}

// StrategyRecord returns a copy of the strategic state.
func (s *AgentState) StrategyRecord() StrategyRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.strategy
}

// RestoreStrategy reinstates a record saved by an earlier run.
func (s *AgentState) RestoreStrategy(r StrategyRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy = r
}

// AssignStrategy replaces the strategy at once: the clan's assignment.
func (s *AgentState) AssignStrategy(to Strategy, tick int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy = StrategyRecord{Kind: to, Since: tick, Chosen: true, ReviewedDay: s.strategy.ReviewedDay}
}

// ProposeStrategy records a suggested change for the next morning review.
func (s *AgentState) ProposeStrategy(to Strategy, reason string, tick int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy.Proposal = &StrategyProposal{Strategy: to, Reason: reason, Tick: tick}
}

// AllowedStrategies lists what a morning review may change the current strategy to: judgement
// calls only. Leaving exploration for a first settlement is a milestone, not a choice.
func (s *AgentState) AllowedStrategies() []Strategy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, settled := s.settlementLocked()
	return allowedFrom(s.strategyLocked().base(), settled)
}

func allowedFrom(from Strategy, settled bool) []Strategy {
	switch {
	case !settled:
		return nil
	case from == StrategyExplore:
		return []Strategy{StrategySettle}
	case from == StrategySettle:
		return []Strategy{StrategyProfession}
	case from == StrategyProfession:
		return []Strategy{StrategySettle, StrategyExplore}
	}
	return nil
}

// advanceStrategy applies milestones and the morning review for one tick. homeReady reports a
// bed or finished shelter.
func (s *AgentState) advanceStrategy(tick, timeOfDay int64, homeReady bool) (StrategyChange, bool) {
	if s == nil {
		return StrategyChange{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.strategy.Kind == "" {
		s.strategy = s.strategyLocked()
		s.strategy.Since = tick
		s.strategy.ReviewedDay = tick / 24000
	}
	r := &s.strategy
	from := r.base()
	_, settled := s.settlementLocked()
	replace := func(to Strategy, why string, chosen bool) (StrategyChange, bool) {
		*r = StrategyRecord{Kind: to, Since: tick, Chosen: chosen, ReviewedDay: r.ReviewedDay, Proposal: r.Proposal}
		return StrategyChange{From: from, To: to, Why: why}, true
	}
	switch {
	case !settled && !r.Chosen && from != StrategyExplore:
		return replace(StrategyExplore, "no settlement", false)
	case settled && from == StrategyExplore && !r.Chosen:
		return replace(StrategySettle, "settlement established", false)
	case from == StrategySettle && !r.Chosen && settled &&
		(homeReady && tick-r.Since >= professionTenureTicks || tick-r.Since >= professionMaxWaitTicks):
		return replace(StrategyProfession, "established in the settlement", false)
	}
	day := tick / 24000
	if r.Proposal == nil || ((timeOfDay%24000)+24000)%24000 >= morningEndsAt || day <= r.ReviewedDay {
		return StrategyChange{}, false
	}
	// One review a morning, and the proposal is spent whatever the verdict.
	p := *r.Proposal
	r.Proposal, r.ReviewedDay = nil, day
	if strings.TrimSpace(p.Reason) == "" || tick-r.Since < reviewTenureTicks {
		return StrategyChange{}, false
	}
	for _, ok := range allowedFrom(from, settled) {
		if ok == p.Strategy {
			change, done := replace(p.Strategy, "morning review: "+p.Reason, true)
			r.ReviewedDay = day
			return change, done
		}
	}
	return StrategyChange{}, false
}

// enterIdle pauses the current strategy; leaveIdle resumes it.
func (s *AgentState) enterIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.strategyLocked()
	if r.Kind == StrategyIdle {
		return
	}
	r.Resume, r.Kind = r.Kind, StrategyIdle
	s.strategy = r
}

func (s *AgentState) leaveIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.strategy.Kind == StrategyIdle {
		s.strategy.Kind, s.strategy.Resume = s.strategy.Resume, ""
	}
}

// rhythm shapes the day for a strategy: until established in a trade, an agent keeps no trade's
// watches, rounds or hobby. Dawn, work, the evening gathering and night rest are everyone's.
func rhythm(strategy Strategy, routine Routine) Routine {
	if strategy == StrategyExplore || strategy == StrategySettle {
		switch routine {
		case RoutineSecondaryHobby, RoutineCivicPatrol, RoutineArmoryMaintenance:
			return RoutinePrimaryDuty
		}
	}
	return routine
}

// strategyAllows reports whether a strategy may launch an objective.
func strategyAllows(strategy Strategy, objective string) bool {
	return !(strategy == StrategyExplore && objective == activityKindConstruction)
}
