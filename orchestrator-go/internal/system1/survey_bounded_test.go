package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-aik.31: neither an exploration entry nor a directive survey may leave the agent idle
// without a bound.

func TestExploration_FreshFrameIssuesFirstLegWithoutIdleCooldown(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	st.homeOrigin = &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5} // not the spawn warm-up: the agent has been around
	kit := withInv(survivalKit()...)
	navAt := int64(-1)
	for tk := int64(100); tk <= 140; tk += 5 {
		res := fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: rf(tk, 3000, kit)})
		for _, fi := range res.Intents {
			if fi.Intent.GetNavigate() != nil && navAt < 0 {
				navAt = tk
			}
		}
	}
	if navAt < 0 || navAt > 105 {
		t.Fatalf("a newly entered exploration frame must navigate on its first ticks, first Navigate at t=%d", navAt)
	}
}

func scoutDirectiveFSM() (*AgentFSM, *AgentState, func(tick int64, opts ...fo) FSMResult) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	tree := withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))
	st.SetStrategicDirective(DirectiveActionScoutFrontier, "eastern forest", "SCOUT_FRONTIER", 200, 6200)
	return fsm, st, func(tick int64, opts ...fo) FSMResult {
		opts = append([]fo{withPos(-40.5, 86, -7.5), tree}, opts...)
		f := rf(tick, 4000, opts...)
		if sv := f.GetHorizonSurvey(); sv != nil {
			st.IngestHorizonSurveyReport(sv, tick)
		}
		return fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
	}
}

func TestSurvey_NoReportIsBoundedBackoffThenResumes(t *testing.T) {
	fsm, _, tick := scoutDirectiveFSM()
	tick(204)
	if fsm.ActiveActivityKind() != activityKindSurvey {
		t.Fatalf("survey expected, top=%q", fsm.ActiveActivityKind())
	}
	tick(325) // lease 1 expired: one re-survey
	if fsm.SurveyBackoffUntil() != 0 || fsm.ActiveActivityKind() != activityKindSurvey {
		t.Fatalf("first timeout must re-survey, backoff=%d top=%q", fsm.SurveyBackoffUntil(), fsm.ActiveActivityKind())
	}
	tick(446) // lease 2 expired: give up
	until := fsm.SurveyBackoffUntil()
	if until <= 446 || fsm.ActiveActivityKind() == activityKindSurvey || fsm.ActiveActivityKind() == activityKindExpedition {
		t.Fatalf("second timeout must end the expedition with a backoff, until=%d top=%q", until, fsm.ActiveActivityKind())
	}
	// The agent is productive during the backoff: no new survey is bid for.
	for tk := int64(460); tk < until; tk += 40 {
		tick(tk)
		if k := fsm.ActiveActivityKind(); k == activityKindSurvey || k == activityKindExpedition {
			t.Fatalf("t=%d: expedition resumed inside the backoff (top=%q)", tk, k)
		}
	}
	// The agent is busy (not idle) all through, and the suppression lapses with the backoff
	// (a running task frame keeps the stack; the expedition bids again once it is empty).
	if fsm.ActiveActivityKind() == "" {
		t.Fatal("agent idle during the backoff")
	}
	tick(until + 1)
	w := &fsmWorld{fsm: fsm, tick: until + 1}
	if w.surveyBackedOff() {
		t.Fatal("backoff must lapse")
	}
}

func TestSurvey_ReportResetsTimeoutCountAndEndsWait(t *testing.T) {
	fsm, _, tick := scoutDirectiveFSM()
	tick(204)
	tick(325) // one timeout
	res := tick(360, withSurvey(uniformSurvey(-40.5, -7.5, 359)))
	if fsm.ActiveActivityKind() == activityKindSurvey {
		t.Fatalf("a report must end the survey wait")
	}
	nav := false
	for _, fi := range res.Intents {
		nav = nav || fi.Intent.GetNavigate() != nil
	}
	if !nav {
		t.Fatal("the report tick must navigate toward the chosen bearing")
	}
	if fsm.SurveyBackoffUntil() != 0 {
		t.Fatal("a survey that reports must not back off")
	}
}
