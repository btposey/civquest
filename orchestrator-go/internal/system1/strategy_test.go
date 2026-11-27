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

// A morning review may evict a strategy only with news from the night just past, after the
// strategy has run, and only for an allowed choice.
func TestStrategy_MorningReviewIsNotFickle(t *testing.T) {
	member := func() *AgentState {
		st := NewAgentState()
		st.SetCollectiveSettlementSite(0.5, 64, 0.5)
		st.AssignStrategy(StrategyProfession, 0)
		return st
	}
	const night, dawn = int64(14000), int64(24500)
	cases := []struct {
		name      string
		nights    int64 // whole days before the proposal's night
		to        Strategy
		reason    string
		lateBy    int64 // ticks after that night's dawn at which the answer arrives
		looksAt   int64 // time of day, in the following day, at which the review is looked for
		wantAfter Strategy
	}{
		{"accepted at dawn", 2, StrategySettle, "the storehouse burned down", 0, dawn, StrategySettle},
		{"waits for dawn", 2, StrategySettle, "news", 0, night + 2000, StrategyProfession},
		{"no reason", 2, StrategySettle, "  ", 0, dawn, StrategyProfession},
		{"too soon", 0, StrategySettle, "news", 0, dawn, StrategyProfession},
		{"not an allowed choice", 2, StrategyExplore, "a new valley", 0, dawn, StrategyProfession},
		{"arrived after dawn", 2, StrategySettle, "news", 11000, dawn + 24000, StrategyProfession},
	}
	for _, c := range cases {
		st := member()
		start := c.nights * day
		st.advanceStrategy(start+night, night, true)
		if c.lateBy == 0 {
			st.ProposeStrategy(c.to, c.reason, start+night)
		}
		if c.looksAt >= dawn {
			st.advanceStrategy(start+dawn, dawn%day, true)
		}
		if c.lateBy > 0 {
			st.ProposeStrategy(c.to, c.reason, start+dawn+500)
			st.advanceStrategy(start+day+night, night, true)
			st.advanceStrategy(start+2*day+500, 500, true)
		} else if c.looksAt < dawn {
			st.advanceStrategy(start+c.looksAt, c.looksAt, true)
		}
		if got := st.Strategy(); got != c.wantAfter {
			t.Errorf("%s: %s, want %s", c.name, got, c.wantAfter)
		}
	}
}

// A frontier explorer is the clan's assignment: the settlement milestone leaves it alone.
func TestStrategy_AssignedFrontierExplorerStaysOut(t *testing.T) {
	st := NewAgentState()
	st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	st.AssignStrategy(StrategyExplore, 100)
	st.advanceStrategy(100+professionMaxWaitTicks, 3000, true)
	if st.Strategy() != StrategyExplore {
		t.Errorf("frontier exploration was undone: %s", st.Strategy())
	}
	if got := st.AllowedStrategies(); !slices.Equal(got, []Strategy{StrategySettle}) {
		t.Errorf("a frontier explorer may propose coming home, allowed %v", got)
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
