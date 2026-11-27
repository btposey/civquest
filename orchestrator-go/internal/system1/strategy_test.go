package system1

import (
	"slices"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const day = int64(24000)

func baseKind(fsm *AgentFSM) htpda.Kind {
	if f := fsm.kernel.Frames(); len(f) > 0 {
		return f[0].Kind()
	}
	return ""
}

// The cycle the user described: explore, settle at once on a settlement, take up a profession
// over time, and trade the base frame for each.
func TestStrategy_ExploreSettleProfessionCycle(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("member")
	cfg.Profession = "FARMER"
	var changes []string
	fsm.SetEventSink(func(_ int64, category, summary string, _ map[string]any) {
		if category == EventStrategy {
			changes = append(changes, summary)
		}
	})
	tick := func(at int64) {
		fsm.Tick(&FSMContext{AgentID: "member", State: st, Config: cfg, Frame: makeArbiterTestFrame(at, 3000, 20, fullSurvivalKitInv(), nil)})
	}
	expect := func(at int64, strategy Strategy, base htpda.Kind, trade string) {
		t.Helper()
		tick(at)
		if got := st.Strategy(); got != strategy {
			t.Fatalf("t=%d strategy %s, want %s", at, got, strategy)
		}
		if got := baseKind(fsm); got != base {
			t.Fatalf("t=%d base frame %q, want %q (stack %v)", at, got, base, fsm.ActivityFrames())
		}
		if got := EffectiveIdentity(st, cfg).Profession; got != trade {
			t.Errorf("t=%d acts as %s, want %s", at, got, trade)
		}
	}
	expect(100, StrategyExplore, activityKindExplore, "EXPLORER")
	st.SetCollectiveSettlementSite(0.5, 64, 0.5) // hard milestone: at once
	expect(104, StrategySettle, activityKindSettle, "SETTLER")
	expect(104+professionTenureTicks, StrategySettle, activityKindSettle, "SETTLER") // no home yet
	expect(104+professionMaxWaitTicks, StrategyProfession, activityKindProfession, "FARMER")
	if want := 2; len(changes) != want {
		t.Errorf("changes %v", changes)
	}
}

// A morning review may evict a strategy only with news, after it has run, once a morning, and
// only for an allowed choice.
func TestStrategy_MorningReviewIsNotFickle(t *testing.T) {
	member := func() *AgentState {
		st := NewAgentState()
		st.SetCollectiveSettlementSite(0.5, 64, 0.5)
		st.AssignStrategy(StrategyProfession, 0)
		return st
	}
	morning := int64(1000)
	cases := []struct {
		name         string
		proposeAt    int64
		to           Strategy
		reason       string
		at, time     int64
		wantStrategy Strategy
	}{
		{"accepted in the morning", 2 * day, StrategyExplore, "surplus food and a new valley", 3*day + morning, morning, StrategyExplore},
		{"waits for morning", 2 * day, StrategyExplore, "news", 2*day + 14000, 14000, StrategyProfession},
		{"no reason", 2 * day, StrategyExplore, "  ", 3*day + morning, morning, StrategyProfession},
		{"too soon", 100, StrategyExplore, "news", day + morning, morning, StrategyProfession},
		{"not an allowed choice", 2 * day, StrategyIdle, "news", 3*day + morning, morning, StrategyProfession},
	}
	for _, c := range cases {
		st := member()
		st.ProposeStrategy(c.to, c.reason, c.proposeAt)
		st.advanceStrategy(c.at, c.time, true)
		if got := st.Strategy(); got != c.wantStrategy {
			t.Errorf("%s: %s, want %s", c.name, got, c.wantStrategy)
		}
	}
	// One review a morning: a second proposal the same day is left for tomorrow.
	st := member()
	st.ProposeStrategy(StrategyIdle, "news", 2*day)
	st.advanceStrategy(3*day+100, 100, true)
	st.ProposeStrategy(StrategyExplore, "more news", 3*day+200)
	st.advanceStrategy(3*day+300, 300, true)
	if st.Strategy() != StrategyProfession {
		t.Errorf("a second review the same morning changed the strategy to %s", st.Strategy())
	}
	// A chosen frontier trip is not undone by the settlement milestone.
	st = member()
	st.ProposeStrategy(StrategyExplore, "frontier", 2*day)
	st.advanceStrategy(3*day+100, 100, true)
	st.advanceStrategy(3*day+200, 200, true)
	if st.Strategy() != StrategyExplore {
		t.Errorf("frontier exploration was undone: %s", st.Strategy())
	}
	if got := st.AllowedStrategies(); !slices.Equal(got, []Strategy{StrategySettle}) {
		t.Errorf("a frontier explorer may return home, allowed %v", got)
	}
}

// Idle is a pause: entered after a while with nothing to do, left when no advice comes.
func TestStrategy_IdlePausesAndResumes(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("idler")
	tick := func(at int64) {
		// Asleep in all but name: a conversation keeps every activity ineligible.
		f := makeArbiterTestFrame(at, 3000, 20, fullSurvivalKitInv(), nil)
		f.Status.IsSleeping = true
		fsm.Tick(&FSMContext{AgentID: "idler", State: st, Config: cfg, Frame: f})
	}
	var seen []Strategy
	for at := int64(100); at < 100+idleAfterTicks+idleLimitTicks+200; at += 20 {
		tick(at)
		if s := st.Strategy(); len(seen) == 0 || seen[len(seen)-1] != s {
			seen = append(seen, s)
		}
	}
	if len(seen) < 3 || seen[0] != StrategyExplore || seen[1] != StrategyIdle || seen[2] != StrategyExplore {
		t.Errorf("strategies over time: %v", seen)
	}
	if st.BaseStrategy() == StrategyIdle {
		t.Error("idle must never become the strategy to return to")
	}
}

func TestStrategy_TradeRoutinesWaitForAProfession(t *testing.T) {
	for _, s := range []Strategy{StrategyExplore, StrategySettle} {
		if got := rhythm(s, RoutineCivicPatrol); got != RoutinePrimaryDuty {
			t.Errorf("%s patrols: %s", s, got)
		}
		if got := rhythm(s, RoutineSleep); got != RoutineSleep {
			t.Errorf("%s does not rest: %s", s, got)
		}
	}
	if got := rhythm(StrategyProfession, RoutineCivicPatrol); got != RoutineCivicPatrol {
		t.Errorf("a professional keeps its watch: %s", got)
	}
}
