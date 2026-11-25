package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// recoverRig hosts Recover under a stub parent that pushes it on a stuck receipt (the same
// rule the real frames apply via recoverArgsFor) and records the pop Result it receives.
type recoverRig struct {
	t      *testing.T
	fsm    *AgentFSM
	st     *AgentState
	k      *htpda.Kernel
	parent *stubParent
	result any
}

type stubParent struct {
	rig    *recoverRig
	target [3]float64
	budget int
	dig    bool
}

func (*stubParent) Kind() htpda.Kind   { return "stub" }
func (*stubParent) Scope() htpda.Scope { return htpda.ScopeMotor }
func (*stubParent) Propose(htpda.World) htpda.Bid {
	return htpda.Bid{Band: htpda.BandCommitted, Utility: 1}
}
func (*stubParent) Init(htpda.World, any) (any, htpda.Lease) { return nil, htpda.Lease{} }
func (p *stubParent) Step(w htpda.World, local any, ev htpda.Event) htpda.Step {
	lw := asFSMWorld(w)
	switch e := ev.(type) {
	case htpda.Receipt:
		lw.fsm.navTargets = map[htpda.FrameID][3]float64{e.Frame: p.target}
		if args, ok := lw.recoverArgsFor(e, p.budget); ok {
			args.Dig = p.dig
			return htpda.Step{Op: htpda.OpPush, Child: activityKindRecover, Args: args}
		}
	case htpda.ChildDone:
		p.rig.result = e.Result
	}
	return htpda.Step{Op: htpda.OpStay}
}

func newRecoverRig(t *testing.T, target [3]float64) *recoverRig {
	t.Helper()
	r := &recoverRig{t: t, fsm: NewAgentFSM(StageBootstrap, nil), st: NewAgentState()}
	r.parent = &stubParent{rig: r, target: target}
	reg, err := htpda.NewRegistry(r.parent, recoverActivity{})
	if err != nil {
		t.Fatal(err)
	}
	r.k = htpda.NewKernel(reg, htpda.PriorityTable{htpda.AnyStage: {"stub": 1}}, htpda.Options{})
	r.dispatch(100, nil, htpda.Tick{}) // arbiter pushes the stub parent
	return r
}

func (r *recoverRig) world(tick int64, opts ...fo) *fsmWorld {
	f := rf(tick, 3000, opts...)
	cfg := xdg.DefaultAgentIdentityConfig("rig")
	return newFSMWorld(r.fsm, &FSMContext{AgentID: "rig", State: r.st, Config: cfg, Frame: f}, nil, nil)
}

// dispatch delivers ev (with rc as the full receipt proto) and applies Memory writes.
func (r *recoverRig) dispatch(tick int64, rc *civquestv1.IntentReceipt, ev htpda.Event, opts ...fo) htpda.Effects {
	r.t.Helper()
	w := r.world(tick, opts...)
	w.receipt = rc
	fx := r.k.Dispatch(w, ev)
	r.fsm.applyMemoryLocked(w, fx.Memory)
	return fx
}

func (r *recoverRig) topKind() htpda.Kind {
	f, _ := r.k.Top()
	return f.Kind()
}

func (r *recoverRig) stuck(tick int64, rc *civquestv1.IntentReceipt, opts ...fo) htpda.Effects {
	r.t.Helper()
	top, _ := r.k.Top()
	rc.FrameId = uint64(top.ID)
	return r.dispatch(tick, rc, htpda.Receipt{Frame: top.ID, Status: rc.GetStatus(), Facts: motorFacts(rc.GetFacts())}, opts...)
}

var rigTarget = [3]float64{20.5, 64, 0.5}

func TestRecover_PushedOnlyByOwningFrameOnStuckReceipt(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	// A stuck receipt for a frame that is not on the stack is dropped (ADR-036 seam).
	fx := r.dispatch(110, terrainStuck(2), htpda.Receipt{Frame: 999, Status: rcFailed, Facts: htpda.MotorFacts{Stuck: true}})
	if r.topKind() != "stub" || len(fx.Diagnostics) == 0 {
		t.Fatalf("stale receipt must be dropped, top=%s diags=%v", r.topKind(), fx.Diagnostics)
	}
	// A non-stuck FAILED receipt does not push Recover.
	plain := receipt(2, rcFailed, "no path")
	r.stuck(111, plain)
	if r.topKind() != "stub" {
		t.Fatalf("plain failure must not push Recover, top=%s", r.topKind())
	}
	// The owning frame's stuck receipt does, and the first attempt is a sidestep.
	fx = r.stuck(112, terrainStuck(3))
	if r.topKind() != activityKindRecover || len(fx.Intents()) != 1 || fx.Intents()[0].GetNavigate() == nil {
		t.Fatalf("expected Recover with a sidestep, top=%s intents=%v", r.topKind(), fx.Intents())
	}
	// Recover never pushes Recover: a stuck receipt addressed to it stays in the same frame.
	depth := r.k.Depth()
	r.stuck(113, terrainStuck(4))
	if r.k.Depth() != depth || r.topKind() != activityKindRecover {
		t.Fatalf("Recover must not nest, depth %d -> %d", depth, r.k.Depth())
	}
}

func TestRecover_CongestionYieldsNeverBlacklistsAndSecondResultGivesUp(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	fx := r.stuck(110, peerStuck(2))
	if r.topKind() != activityKindRecover || len(fx.Intents()) != 0 {
		t.Fatalf("congestion yields without intents, top=%s intents=%v", r.topKind(), fx.Intents())
	}
	// Yield elapses (20 ticks): the parent's navigation is re-issued under the Recover frame.
	fx = r.dispatch(131, nil, htpda.Tick{})
	if n := fx.Intents(); len(n) != 1 || n[0].GetNavigate() == nil || n[0].GetNavigate().Target.X != rigTarget[0] {
		t.Fatalf("expected the parent's navigation re-issued, got %v", fx.Intents())
	}
	// A second congestion result in the same Recover escalates to give-up: Blocked{Congestion}.
	r.stuck(135, peerStuck(3))
	b, ok := r.result.(recoverBlocked)
	if !ok || b.Cause != CauseCongestion {
		t.Fatalf("expected Blocked{Congestion}, got %#v", r.result)
	}
	if r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 140) || len(r.fsm.recoverOutbox) != 0 {
		t.Fatal("congestion must never blacklist terrain or decay POIs")
	}
}

func TestRecover_TerrainSwitchedByEntityEvidenceNeverBlacklists(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	r.stuck(110, terrainStuck(2))
	r.stuck(115, peerStuck(3))         // a peer shows up mid-episode: the episode is congestion from now on
	r.dispatch(300, nil, htpda.Tick{}) // lease expires
	if r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 310) || len(r.fsm.recoverOutbox) != 0 {
		t.Fatal("an episode that saw blocked_by_entity must not blacklist")
	}
}

func TestRecover_TwoTerrainGiveUpsBlacklistAndDecayPOI(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	giveUp := func(start int64) {
		r.stuck(start, terrainStuck(start))
		// Lease expiry (100 ticks) always ends the frame with Blocked.
		r.dispatch(start+recoverTunables.LeaseTicks, nil, htpda.Tick{})
		if b, ok := r.result.(recoverBlocked); !ok || b.Cause != CauseTerrain || !b.HasTarget || b.Target != rigTarget {
			t.Fatalf("expected Blocked{Terrain, target}, got %#v", r.result)
		}
		if r.topKind() != "stub" {
			t.Fatalf("lease expiry must pop Recover, top=%s", r.topKind())
		}
	}
	giveUp(110)
	if r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 215) {
		t.Fatal("one give-up must not blacklist")
	}
	giveUp(300)
	if !r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 405) {
		t.Fatalf("%d give-ups must blacklist the target", recoverTunables.TerrainGiveUpsToBlacklist)
	}
	if got := r.fsm.DrainRecoverGiveUps(); len(got) != 2 || got[0].Blacklisted || !got[1].Blacklisted {
		t.Fatalf("expected two give-up events (second blacklisted), got %+v", got)
	}
}

func TestRecover_AttemptBudgetAndRecoveredOnDisplacement(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	r.parent.budget = 1
	r.stuck(110, terrainStuck(2))
	// One attempt only: its timeout gives up before the 100-tick lease.
	r.dispatch(131, nil, htpda.Tick{})
	if _, ok := r.result.(recoverBlocked); !ok {
		t.Fatalf("budget of 1 attempt must give up after it times out, got %#v", r.result)
	}
	// A sidestep that only moves sideways is not progress toward the target.
	r3 := newRecoverRig(t, rigTarget)
	r3.stuck(110, terrainStuck(2))
	r3.dispatch(115, nil, htpda.Tick{}, withPos(0.5, 64, 4.5))
	if r3.result != nil || r3.topKind() != activityKindRecover {
		t.Fatalf("lateral movement must not count as recovered, got %#v top=%s", r3.result, r3.topKind())
	}
	// Getting a block closer to the target means the agent is moving again.
	r2 := newRecoverRig(t, rigTarget)
	r2.stuck(110, terrainStuck(2))
	r2.dispatch(115, nil, htpda.Tick{}, withPos(2.5, 64, 0.5))
	if _, ok := r2.result.(recovered); !ok || r2.topKind() != "stub" {
		t.Fatalf("expected Recovered on progress toward the target, got %#v top=%s", r2.result, r2.topKind())
	}
}

func TestRecover_EnclosedDigsOutThenStepsUp(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	blocks := withBlocks(blk(0, 66, 0, "minecraft:dirt"))
	fx := r.stuck(110, enclosedStuck(2), blocks, withPos(0.5, 64, 0.5))
	in := fx.Intents()
	if len(in) != 1 || in[0].GetInteractBlock() == nil || in[0].GetInteractBlock().Pos.Y != 66 {
		t.Fatalf("expected overhead HARVEST_BREAK first, got %v", in)
	}
	// Throttled: no second break within an attempt window.
	if fx = r.dispatch(115, nil, htpda.Tick{}, blocks, withPos(0.5, 64, 0.5)); len(fx.Intents()) != 0 {
		t.Fatalf("dig must wait for the break to register, got %v", fx.Intents())
	}
	// Overhead clear and the escape cell open: a step-out navigation (counts as an attempt).
	fx = r.dispatch(135, nil, htpda.Tick{}, withPos(0.5, 64, 0.5))
	if in = fx.Intents(); len(in) != 1 || in[0].GetNavigate() == nil {
		t.Fatalf("expected a step-out navigation, got %v", in)
	}
}

func TestRecover_ExpeditionNavigateDoesNotBlacklist(t *testing.T) {
	r := newExpRig(t, "scout", 2)
	r.step(110)
	r.step(125, withSurvey(uniformSurvey(0, 0, 120)))
	if r.stack() != "expedition>navigate" {
		t.Fatalf("setup stack=%s", r.stack())
	}
	r.stepR(180, terrainStuck(2), withPos(0, 64, 20))
	r.step(205, withPos(0, 64, 20))
	if len(r.fsm.recoverOutbox) != 0 {
		t.Fatalf("an expedition flanks instead of blacklisting, got %+v", r.fsm.recoverOutbox)
	}
}
