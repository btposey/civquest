package system1

import "testing"

func holdCtx(tick int64, opts ...fo) *FSMContext {
	f := rf(tick, 3000, opts...)
	return &FSMContext{AgentID: "hold", State: NewAgentState(), Frame: f}
}

func TestEmergencyResolved_Drowning(t *testing.T) {
	var el emergencyLocal
	el.observe(holdCtx(100, withWater(true, true, 100)), 100) // 33% air, submerged
	if !el.Drowning {
		t.Fatal("submerged with 33% air must latch drowning (no damage edge needed)")
	}
	for _, tc := range []struct {
		name string
		opts []fo
		want bool
	}{
		{"still submerged", []fo{withWater(true, true, 290)}, false},
		{"surface swimming, air full", []fo{withWater(true, false, 300)}, false},
		{"dry but air 60%", []fo{withWater(false, false, 180)}, false},
		{"dry, air 80%", []fo{withWater(false, false, 240)}, true},
	} {
		e := el
		ctx := holdCtx(200, tc.opts...)
		e.observe(ctx, 200)
		if got := e.resolved(ctx, 200); got != tc.want {
			t.Errorf("%s: resolved=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestEmergencyResolved_FireAndMobWindows(t *testing.T) {
	var el emergencyLocal
	el.observe(holdCtx(100, withBurning(true)), 100)
	ctx := holdCtx(105, withBurning(true))
	el.observe(ctx, 105)
	if el.resolved(ctx, 105) {
		t.Fatal("burning must not resolve")
	}
	ctx = holdCtx(110)
	el.observe(ctx, 110)
	if !el.resolved(ctx, 110) {
		t.Fatal("not burning and no mob must resolve")
	}

	el = emergencyLocal{}
	mob := withEntities(zombieAt(5, 3, 0, 3))
	ctx = holdCtx(100, mob)
	el.observe(ctx, 100)
	for tick, want := range map[int64]bool{105: false, 109: false, 110: true} {
		c := holdCtx(tick)
		e := el
		e.observe(c, tick)
		if got := e.resolved(c, tick); got != want {
			t.Errorf("tick %d: resolved=%v want %v (settle %d)", tick, got, want, emergencySettleHostileTicks)
		}
	}
	el.LastDamageTick = 105
	c := holdCtx(112)
	el.observe(c, 112)
	if el.resolved(c, 112) {
		t.Fatal("damage 7 ticks ago must keep the hold")
	}
}

func TestEngagement_HeldVerdictEscalatesOnceAndDirectiveOverrides(t *testing.T) {
	var e engagement
	in := engagementInput{Tick: 100, Target: 7, HasThreat: true, DefaultFlee: false, HP: 20, MaxHP: 20}
	if e.resolve(in) || e.ID != "7@100" {
		t.Fatalf("expected fight, id 7@100: %+v", e)
	}
	in.Tick, in.DefaultFlee, in.HP = 110, true, 9 // confidence collapsed: raw panic would flip
	if e.resolve(in) {
		t.Fatal("verdict must be held through a default flip")
	}
	in.Tick, in.HP = 120, 3
	if !e.resolve(in) || !e.Escalated {
		t.Fatal("HP floor must escalate to flee once")
	}
	in.Tick, in.DefaultFlee, in.HP = 130, false, 18
	if !e.resolve(in) {
		t.Fatal("flee must never revert to fight inside the engagement")
	}

	// Midbrain override arrives mid-engagement; absent a directive the default stands.
	e = engagement{}
	in = engagementInput{Tick: 100, Target: 7, HasThreat: true, DefaultFlee: true, HP: 20, MaxHP: 20}
	if !e.resolve(in) {
		t.Fatal("default flee expected")
	}
	in.Tick, in.DirectiveAct, in.DirectiveAt = 105, DirectiveActionEngageThreat, 103
	if e.resolve(in) {
		t.Fatal("ENGAGE_THREAT issued during the engagement must override to fight")
	}
	in.Tick = 110
	if e.resolve(in) {
		t.Fatal("applied override is held, not re-applied/flipped back")
	}
	// A directive issued before the engagement started is not about it.
	e = engagement{}
	in = engagementInput{Tick: 200, Target: 8, HasThreat: true, DefaultFlee: true, HP: 20, MaxHP: 20, DirectiveAct: DirectiveActionEngageThreat, DirectiveAt: 150}
	if !e.resolve(in) {
		t.Fatal("stale directive must not override")
	}
	// Engagement ends after the idle window and the next one decides afresh.
	e.resolve(engagementInput{Tick: 200 + engagementIdleTicks + 1, DefaultFlee: false, HP: 20, MaxHP: 20}) // quiet past the idle window
	in = engagementInput{Tick: 250, Target: 9, HasThreat: true, DefaultFlee: false, HP: 20, MaxHP: 20}
	if e.resolve(in) || e.ID != "9@250" {
		t.Fatalf("expected a fresh fight engagement: %+v", e)
	}
}
