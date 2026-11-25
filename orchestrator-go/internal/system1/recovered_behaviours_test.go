package system1

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Behaviours the retired EvaluateUnstuck / scouting stuck branch / sleep stuck counter provided,
// recovered in the frame-local architecture (cq-aik.30). The baseline tests each ported here:
//   TestEvaluateUnstuck_MovementUnlocks                                 -> MovementUnlocksEscapeLock
//   TestEvaluateUnstuck_CooldownAngularFanOutAndActiveTaskSuppression   -> FanOut..., ActiveTaskNotStolen
//   TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction                -> ScoutingStuckDigsThrough
//   TestEvaluateSleep_BlacklistsUnreachableAssignedBedAfterTwoStucks    -> Sleep...

// Baseline: once the agent moved, the unstuck heading lock cleared. Recover keeps the dig
// heading across give-ups but releases it when it pops recovered (the agent moves again).
func TestRecover_MovementUnlocksEscapeLock(t *testing.T) {
	blocks := withBlocks(blk(0, 66, 0, "minecraft:dirt"))
	r := newRecoverRig(t, rigTarget)
	r.stuck(110, enclosedStuck(2), blocks, withPos(0.5, 64, 0.5))
	r.dispatch(135, nil, htpda.Tick{}, withPos(0.5, 64, 0.5)) // overhead clear: step-out leg (attempt 1)
	// Still stuck in the pocket: lease expiry gives up and the heading lock is kept.
	r.dispatch(110+recoverTunables.LeaseTicks, nil, htpda.Tick{}, withPos(0.5, 64, 0.5))
	if r.fsm.escapeLock == nil {
		t.Fatal("a give-up must keep the dig heading lock for the next episode")
	}
	r2 := newRecoverRig(t, rigTarget)
	r2.stuck(110, enclosedStuck(2), blocks, withPos(0.5, 64, 0.5))
	r2.dispatch(135, nil, htpda.Tick{}, withPos(0.5, 64, 0.5))
	r2.fsm.escapeLock = &recoverEscapeLock{DirX: 1, Tick: 120} // a lock from an earlier episode
	// The agent walked out (>= 1 block): Recover pops recovered and the lock is released.
	r2.dispatch(140, nil, htpda.Tick{}, withPos(2.5, 64, 0.5))
	if _, ok := r2.result.(recovered); !ok {
		t.Fatalf("expected recovered after moving a block, got %#v", r2.result)
	}
	if r2.fsm.escapeLock != nil {
		t.Fatalf("movement must unlock the escape heading, still %+v", r2.fsm.escapeLock)
	}
}

// Baseline: >=25-tick cooldown between unstuck navigations, 2.5-3.0 block angular fan-out that
// differs per agent and rotates per attempt.
func TestRecover_EnclosedFanOutAngularCooldownAndPeerSeparation(t *testing.T) {
	pos := &civquestv1.Vec3{X: 10, Y: 64, Z: 10}
	frame := rf(100, 3000, withPos(10, 64, 10))
	step := func(id string, attempts int) (float64, float64) {
		var out recoverLocal
		in, isNav := recoverLocal{Attempts: attempts}.enclosedStep(id, frame, pos, 100, &out)
		if in == nil || in.GetNavigate() == nil || !isNav {
			t.Fatalf("flat enclosed walkway must fan out with a navigation, got %v", in)
		}
		tg := in.GetNavigate().Target
		if d := math.Hypot(tg.X-pos.X, tg.Z-pos.Z); d < 2.0 || d > 3.5 {
			t.Fatalf("fan-out step %.2f outside 2.0-3.5 blocks", d)
		}
		return tg.X, tg.Z
	}
	ax, az := step("able", 0)
	bx, bz := step("carl", 0)
	if math.Hypot(ax-bx, az-bz) < 0.5 {
		t.Fatalf("peers must fan out to distinct headings, both went (%.1f,%.1f)", ax, az)
	}
	a2x, a2z := step("able", 1)
	if math.Hypot(ax-a2x, az-a2z) < 0.5 {
		t.Fatalf("the next attempt must rotate the heading, got (%.1f,%.1f) twice", a2x, a2z)
	}

	// Cooldown through the real frame: one navigation per attempt window.
	r := newRecoverRig(t, rigTarget)
	fx := r.stuck(110, enclosedStuck(2), withPos(10, 64, 10))
	if len(fx.Intents()) != 1 || fx.Intents()[0].GetNavigate() == nil {
		t.Fatalf("expected the first fan-out navigation, got %v", fx.Intents())
	}
	if fx = r.dispatch(115, nil, htpda.Tick{}, withPos(10, 64, 10)); len(fx.Intents()) != 0 {
		t.Fatalf("no duplicate navigation inside the cooldown, got %v", fx.Intents())
	}
	r.stuck(120, enclosedStuck(3), withPos(10, 64, 10)) // the leg failed again
	if fx = r.dispatch(125, nil, htpda.Tick{}, withPos(10, 64, 10)); len(fx.Intents()) != 0 {
		t.Fatalf("a failed leg must wait out its cooldown, got %v", fx.Intents())
	}
	fx = r.dispatch(141, nil, htpda.Tick{}, withPos(10, 64, 10))
	if len(fx.Intents()) != 1 || fx.Intents()[0].GetNavigate() == nil {
		t.Fatalf("expected a rotated retry after the cooldown, got %v", fx.Intents())
	}
}

// Terrain sidesteps also separate clustered peers: agents stuck against the same wall try
// opposite sides first.
func TestRecover_TerrainSidestepFansOutAcrossPeers(t *testing.T) {
	first := func(id string) *civquestv1.Vec3 {
		fsm := NewAgentFSM(StageBootstrap, nil)
		st := NewAgentState()
		f := rf(100, 3000, withPos(10, 64, 10))
		lw := newFSMWorld(fsm, &FSMContext{AgentID: id, State: st, Config: xdg.DefaultAgentIdentityConfig(id), Frame: f}, nil, nil)
		loc := recoverLocal{recoverArgs: recoverArgs{Cause: CauseTerrain}, HasStart: true}
		step := recoverActivity{}.advance(lw, loc)
		if len(step.Intents) != 1 || step.Intents[0].GetNavigate() == nil {
			t.Fatalf("%s: expected a sidestep, got %+v", id, step.Intents)
		}
		return step.Intents[0].GetNavigate().Target
	}
	a, c := first("able"), first("carl")
	if math.Hypot(a.X-c.X, a.Z-c.Z) < 1.0 {
		t.Fatalf("peers stuck on one wall must sidestep to different sides, got (%.1f,%.1f) and (%.1f,%.1f)", a.X, a.Z, c.X, c.Z)
	}
}

// Baseline: when an active task is handling the stuck (not enclosed), unstuck must not mask
// or steal it. Ambient telemetry STUCK never starts a Recover or emits a recovery intent.
func TestRecover_ActiveTaskNotStolenByAmbientStuckTelemetry(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	fsm.StartTask(st, NewHarvestTreeTask(20, 64, 0, StageBootstrap, 100))
	tree := withBlocks(blk(20, 64, 0, "minecraft:oak_log"))
	for _, tick := range []int64{100, 112, 125, 140, 170} {
		f := rf(tick, 3000, tree, withNav(navStuck))
		res := fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
		if k := fsm.ActiveActivityKind(); k == activityKindRecover {
			t.Fatalf("t=%d: telemetry stuck alone must not push Recover", tick)
		}
		for _, fi := range res.Intents {
			if n := fi.Intent.GetNavigate(); n != nil && math.Hypot(n.Target.X-0.5, n.Target.Z-0.5) < 5 {
				t.Fatalf("t=%d: an unstuck-style nearby fan-out stole the task: %v", tick, n.Target)
			}
		}
	}
	if !st.HasActiveTask() {
		t.Fatal("ambient stuck telemetry must not abandon the active task")
	}
}

// Baseline TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction: a stuck scouting explorer
// breaks the adjacent obstructing block, head-height first, to form a step-up.
func TestRecover_ScoutingStuckDigsThroughAdjacentObstruction(t *testing.T) {
	pos := withPos(10.5, 62, 10.5)
	wall := withBlocks(blk(11, 62, 10, "minecraft:dirt"), blk(11, 63, 10, "minecraft:dirt"))

	r := newRecoverRig(t, rigTarget)
	r.parent.dig = true
	fx := r.stuck(110, terrainStuck(2), wall, pos)
	in := fx.Intents()
	if r.topKind() != activityKindRecover || len(in) != 1 || in[0].GetInteractBlock() == nil {
		t.Fatalf("expected a HARVEST_BREAK on the obstructing wall, got %v", in)
	}
	ib := in[0].GetInteractBlock()
	if ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK || ib.Pos.Y != 63 {
		t.Fatalf("expected head-height (Y=63) block broken first, got %v at Y=%d", ib.InteractionType, ib.Pos.Y)
	}
	// The break is throttled while it registers; no sidestep is mixed in.
	if fx = r.dispatch(115, nil, htpda.Tick{}, wall, pos); len(fx.Intents()) != 0 {
		t.Fatalf("must wait for the break to register, got %v", fx.Intents())
	}

	// Without the scouting flag (a task approach, for instance) the stuck is only sidestepped.
	r2 := newRecoverRig(t, rigTarget)
	fx = r2.stuck(110, terrainStuck(2), wall, pos)
	if in = fx.Intents(); len(in) != 1 || in[0].GetNavigate() == nil {
		t.Fatalf("non-scouting terrain stuck must sidestep, got %v", in)
	}
	// Scouting with open ground (nothing adjacent to dig) also sidesteps.
	r3 := newRecoverRig(t, rigTarget)
	r3.parent.dig = true
	fx = r3.stuck(110, terrainStuck(2), pos)
	if in = fx.Intents(); len(in) != 1 || in[0].GetNavigate() == nil {
		t.Fatalf("open ground must sidestep even for a scout, got %v", in)
	}
}

func sleepFSM(t *testing.T) (*AgentFSM, *AgentState, func(tick int64, rc *civquestv1.IntentReceipt)) {
	t.Helper()
	fsm := NewAgentFSM(StageSettlement, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	cfg.Profession = "EXPLORER"
	st.SpatialIndex().UpsertPOI(db.AgentPOI{POIID: FormatPOIID(POICategoryHabitation, -46, 79, 17), Category: POICategoryHabitation,
		BlockID: "minecraft:red_bed", X: -45.5, Y: 79, Z: 17.5, AccessX: -45.5, AccessY: 79, AccessZ: 16.5,
		ElevationLayer: ElevationDugout, Reliability: 1, IsActive: true})
	tick := func(tk int64, rc *civquestv1.IntentReceipt) {
		f := rf(tk, 19000+tk, withPos(-30, 86, 0))
		f.Receipt = rc // frame_id 0: the biological-needs skill's intents are unframed
		fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
	}
	return fsm, st, tick
}

// Baseline blacklisted an unreachable assigned bed after two stucks. Now the evidence is the
// receipt: two terrain give-ups on the bed's access target blacklist it (cleared assignment);
// ambient telemetry stuck and congestion never do.
func TestSleep_TwoTerrainGiveUpsOnBedBlacklistAndClearAssignment(t *testing.T) {
	_, st, tick := sleepFSM(t)
	tick(20, nil)
	tick(30, terrainStuck(2)) // stuck receipt 1 -> orphan Recover
	tick(135, nil)            // lease expired: give-up 1
	if st.IsTargetUnreachable(-45.5, 79, 16.5, 140) || st.GetAssignedBed() == nil {
		t.Fatal("one terrain give-up must still retry the bed")
	}
	tick(150, terrainStuck(3))
	tick(260, nil) // give-up 2
	if !st.IsTargetUnreachable(-45.5, 79, 16.5, 265) {
		t.Fatal("two terrain give-ups must blacklist the bed access target")
	}
	tick(270, nil)
	if st.GetAssignedBed() != nil {
		t.Fatalf("assignment must be cleared once blacklisted, got %+v", st.GetAssignedBed())
	}
	if st.IsTargetUnreachable(-45.5, 79, 16.5, 270+DefaultUnreachableTargetTTL+300) {
		t.Fatal("the bed blacklist must expire after its TTL")
	}
}

func TestSleep_CongestionNeverBlacklistsAssignedBed(t *testing.T) {
	fsm, st, tick := sleepFSM(t)
	tick(20, nil)
	for i, at := range []int64{30, 150, 270, 390} {
		tick(at, peerStuck(int64(2+i)))
		tick(at+105, nil)
	}
	if st.IsTargetUnreachable(-45.5, 79, 16.5, 500) || st.GetAssignedBed() == nil {
		t.Fatal("peer congestion must never blacklist the bed")
	}
	if len(fsm.DrainRecoverGiveUps()) != 0 {
		t.Fatal("congestion must not decay POIs")
	}
}
