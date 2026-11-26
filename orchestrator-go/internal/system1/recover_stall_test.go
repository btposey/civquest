package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// fredTarget is the log fred navigated to 171 times from below (playtest 3).
var fredTarget = [3]float64{-35.5, 92, 15.5}

// cq-yn1.6: a sidestep already within arrival tolerance reports COMPLETED without moving the
// agent; that is not a recovery.
func TestRecover_CompletedWithoutMovingIsNotRecovered(t *testing.T) {
	r := newRecoverRig(t, fredTarget)
	at := withPos(-34.5, 86, 12.3)
	r.stuck(110, terrainStuck(2), at)
	r.stuck(112, receipt(3, rcCompleted, ""), at)
	if r.result != nil || r.topKind() != activityKindRecover {
		t.Fatalf("a motionless completion popped %#v (top=%s)", r.result, r.topKind())
	}
	// Moving, in any direction, still counts.
	r.stuck(115, receipt(4, rcCompleted, ""), withPos(-34.5, 87, 12.3))
	if _, ok := r.result.(recovered); !ok {
		t.Fatalf("a completion after climbing a block must recover, got %#v", r.result)
	}
}

// Sidesteps can each succeed while the target stays out of reach. After StallEpisodes
// episodes without getting closer the target is blacklisted.
func TestRecover_RepeatedStallsBlacklistTarget(t *testing.T) {
	r := newRecoverRig(t, fredTarget)
	at, aside := withPos(-34.5, 86, 12.3), withPos(-33.4, 86, 12.3)
	tick := int64(110)
	for episode := 1; episode <= recoverTunables.StallEpisodes; episode++ {
		r.result = nil
		r.stuck(tick, terrainStuck(tick), at)
		if episode < recoverTunables.StallEpisodes {
			r.stuck(tick+4, receipt(tick+4, rcCompleted, ""), aside) // "recovered"
			if _, ok := r.result.(recovered); !ok {
				t.Fatalf("episode %d: expected recovered, got %#v", episode, r.result)
			}
			if r.st.IsTargetUnreachable(fredTarget[0], fredTarget[1], fredTarget[2], tick+5) {
				t.Fatalf("episode %d: blacklisted too early", episode)
			}
		} else {
			r.dispatch(tick+1, nil, htpda.Tick{}, at)
		}
		tick += 40
	}
	if _, ok := r.result.(recoverBlocked); !ok || r.topKind() != "stub" {
		t.Fatalf("stalled target must give up at once, got %#v top=%s", r.result, r.topKind())
	}
	if !r.st.IsTargetUnreachable(fredTarget[0], fredTarget[1], fredTarget[2], tick) {
		t.Fatal("stalled target not blacklisted")
	}
}

// Progress between episodes means the route is working: no blacklist.
func TestRecover_ProgressResetsStallCount(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	tick := int64(110)
	for i := 0; i < recoverTunables.StallEpisodes+3; i++ {
		x := 0.5 + float64(i)*2 // two blocks closer each time
		r.result = nil
		r.stuck(tick, terrainStuck(tick), withPos(x, 64, 0.5))
		r.stuck(tick+4, receipt(tick+4, rcCompleted, ""), withPos(x+1.5, 64, 0.5))
		if _, ok := r.result.(recovered); !ok {
			t.Fatalf("episode %d: got %#v", i, r.result)
		}
		tick += 40
	}
	if r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], tick) {
		t.Fatal("a target being approached must not be blacklisted")
	}
}
