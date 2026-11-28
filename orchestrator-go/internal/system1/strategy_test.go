package system1

import (
	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
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
	frameInv, blocks := fullSurvivalKitInv(), []*civquestv1.VisibleBlock(nil)
	fsm.SetEventSink(func(_ int64, category, summary string, _ map[string]any) {
		if category == EventStrategy {
			changes = append(changes, summary)
		}
	})
	tick := func(at int64) {
		f := makeArbiterTestFrame(at, 3000, 20, frameInv, nil)
		f.InterestingBlocks = blocks
		fsm.Tick(&FSMContext{AgentID: "member", State: st, Config: cfg, Frame: f})
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
	// Neither time nor tenure opens a trade while farming is impossible.
	expect(104+10*professionTenureTicks, StrategySettle, activityKindSettle, "SETTLER")
	st.SetCabinBuilt(blueprints.CabinTotalBlocks)
	frameInv, blocks = append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{ItemId: "minecraft:bucket", Count: 1}),
		[]*civquestv1.VisibleBlock{{BlockId: "minecraft:water", Pos: &civquestv1.BlockPosProto{X: 6, Y: 63, Z: 0}}}
	expect(104+11*professionTenureTicks, StrategyProfession, activityKindProfession, "FARMER")
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
		st.ObserveFarming(canFarm)
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
		st.advanceStrategy(start+night, night, canFarm)
		if c.lateBy == 0 {
			st.ProposeStrategy(c.to, c.reason, start+night)
		}
		if c.looksAt >= dawn {
			st.advanceStrategy(start+dawn, dawn%day, canFarm)
		}
		if c.lateBy > 0 {
			st.ProposeStrategy(c.to, c.reason, start+dawn+500)
			st.advanceStrategy(start+day+night, night, canFarm)
			st.advanceStrategy(start+2*day+500, 500, canFarm)
		} else if c.looksAt < dawn {
			st.advanceStrategy(start+c.looksAt, c.looksAt, canFarm)
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
	st.advanceStrategy(100+reviewTenureTicks, 3000, canFarm)
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

// canFarm is a settlement with all a first field needs.
var canFarm = FarmingNeeds{Shelter: true, Water: true, Bucket: true}

// cq-vuu: no road into a profession is open while farming is impossible: not time, not a
// morning review, not an assignment, not a record saved before.
func TestStrategy_NoProfessionUntilFarmingIsPossible(t *testing.T) {
	const night, dawn = int64(14000), int64(24500)
	for name, needs := range map[string]FarmingNeeds{
		"no shelter": {Water: true, Bucket: true},
		"no water":   {Shelter: true, Bucket: true},
		"no bucket":  {Shelter: true, Water: true},
	} {
		st := NewAgentState()
		st.SetCollectiveSettlementSite(0.5, 64, 0.5)
		st.advanceStrategy(100, 3000, needs)
		start := 5 * day
		st.advanceStrategy(start+night, night, needs)
		if got := st.AllowedStrategies(); len(got) != 0 {
			t.Errorf("%s: a review may choose %v", name, got)
		}
		st.ProposeStrategy(StrategyProfession, "time I took up a trade", start+night)
		st.advanceStrategy(start+dawn, dawn%day, needs)
		if st.AssignStrategy(StrategyProfession, start+dawn) || st.Strategy() != StrategySettle {
			t.Errorf("%s: entered %s", name, st.Strategy())
		}
		st.RestoreStrategy(StrategyRecord{Kind: StrategyIdle, Resume: StrategyProfession, Since: 100})
		if got := st.BaseStrategy(); got != StrategySettle {
			t.Errorf("%s: a saved trade was resumed as %s", name, got)
		}
		// The same record qualifies at once when the needs are met.
		st.advanceStrategy(start+dawn+4, dawn%day, canFarm)
		if got := st.BaseStrategy(); got != StrategyProfession {
			t.Errorf("%s: able to farm, still %s", name, got)
		}
	}
}

// cq-vuu: the needs are read from the frame, water counting only near the settlement.
func TestFarmingNeeds(t *testing.T) {
	st := NewAgentState()
	st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	frame := func(item string, waterX int32) *civquestv1.TelemetryFrame {
		f := makeArbiterTestFrame(100, 3000, 20, []*civquestv1.ItemStackProto{{ItemId: item, Count: 1}}, nil)
		f.InterestingBlocks = []*civquestv1.VisibleBlock{{BlockId: "minecraft:water", Pos: &civquestv1.BlockPosProto{X: waterX, Y: 63}}}
		return f
	}
	if got := farmingNeeds(st, frame("minecraft:lava_bucket", 200)); got != (FarmingNeeds{}) {
		t.Errorf("needs %+v, want none", got)
	}
	st.SetCabinBuilt(blueprints.CabinTotalBlocks)
	if got := farmingNeeds(st, frame("minecraft:water_bucket", 30)); got != canFarm {
		t.Errorf("needs %+v, want all", got)
	}
}
