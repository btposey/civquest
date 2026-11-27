package system1

// A walk that came to nothing earns a fresh look round (user, play test 7): when a
// destination is given up, or a walk to a remembered sighting ends with nothing there, the next
// walk is replaced by a horizon survey, and walks are held until it has had time to finish, so
// the agent chooses its next bearing from what it can see now.

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	// scanHoldTicks covers a survey (36 sectors at 2 ticks) and its report.
	scanHoldTicks = int64(100)
	// scanCooldownTicks keeps repeated give-ups from turning the agent into a lighthouse.
	scanCooldownTicks = int64(600)

	// EventRescan: a walk was replaced by a survey.
	EventRescan = "RESCAN"
)

// wantFreshBearing asks for a survey before the next walk. Safe with or without the state lock.
func (s *AgentState) wantFreshBearing(why string) {
	if s != nil {
		s.rescanWhy.Store(&why)
	}
}

// takesFreshBearing reports whether kind's walks are subject to the rule. Emergency and
// Recover steer for themselves; an expedition's legs already survey between them, and the plan
// hands a dead end to an expedition.
func takesFreshBearing(kind htpda.Kind) bool {
	switch kind {
	case activityKindEmergency, activityKindRecover, activityKindExpedition, activityKindNavigate, activityKindPlan:
		return false
	}
	return true
}

// holdForScan applies the rule to one outgoing walk: it returns the survey to send in the
// walk's place, or (nil, true) while a survey is under way, or (nil, false) to let it go.
func (fsm *AgentFSM) holdForScan(w *fsmWorld, agentID string) (*civquestv1.AgentIntent, bool) {
	if w.tick < fsm.scanAt {
		fsm.scanAt = 0 // the world clock went back
	}
	if fsm.scanAt != 0 && w.tick-fsm.scanAt < scanHoldTicks {
		return nil, true
	}
	why := w.ctx.State.rescanWhy.Swap(nil)
	if why == nil || (fsm.scanAt != 0 && w.tick-fsm.scanAt < scanCooldownTicks) {
		return nil, false
	}
	fsm.scanAt = w.tick
	if fsm.eventSink != nil {
		fsm.eventSink(w.tick, EventRescan, "survey before the next walk: "+*why, map[string]any{"why": *why})
	}
	return surveyActivity{}.surveyIntent(agentID), true
}
