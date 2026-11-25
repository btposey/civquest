package system1

import (
	"os"
	"path/filepath"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"google.golang.org/protobuf/encoding/protojson"
)

// Contract tests (cq-2sr.23): the receipts below were RECORDED from the real Java
// AgentNavigator + AgentMotorFSM by MotorFactsFixtureRecorderTest (which also fails if the mod
// drifts from the committed files). Feeding them to Recover keeps the Go side honest about what
// the mod really reports (e.g. an enclosed pit reports stuck_ticks=0, a terrain wall 16).

func loadMotorFactsFixture(t *testing.T, name string) *civquestv1.IntentReceipt {
	t.Helper()
	path := filepath.Join("..", "..", "test", "integration", "testdata", "motorfacts", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	rc := &civquestv1.IntentReceipt{}
	if err := protojson.Unmarshal(raw, rc); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	if rc.GetStatus() != rcFailed || rc.GetFailureReason() != "Navigation stuck" || !rc.GetFacts().GetStuck() {
		t.Fatalf("fixture %s is not a stuck FAILED receipt: %v", name, rc)
	}
	return rc
}

func TestRecoverContract_RealJavaFactsClassifyCause(t *testing.T) {
	cases := []struct {
		file string
		want RecoverCause
	}{
		{"terrain_stuck.json", CauseTerrain},
		{"enclosed_pit.json", CauseEnclosed},
		{"congestion_peer.json", CauseCongestion},
	}
	for _, c := range cases {
		rc := loadMotorFactsFixture(t, c.file)
		f := motorFacts(rc.GetFacts())
		if got := recoverCauseOf(f); got != c.want {
			t.Errorf("%s: cause=%v want %v", c.file, got, c.want)
		}
		if !isStuckReceipt(rc.GetStatus(), f, rc.GetFailureReason()) {
			t.Errorf("%s: real receipt is not recognised as a stuck receipt", c.file)
		}
	}
}

func TestRecoverContract_RealTerrainReceiptSidestepsThenGivesUpAndBlacklists(t *testing.T) {
	giveUp := func(r *recoverRig, start int64) {
		fx := r.stuck(start, loadMotorFactsFixture(t, "terrain_stuck.json"))
		if r.topKind() != activityKindRecover || len(fx.Intents()) != 1 || fx.Intents()[0].GetNavigate() == nil {
			t.Fatalf("real terrain receipt must push Recover with a sidestep, top=%s intents=%v", r.topKind(), fx.Intents())
		}
		r.dispatch(start+recoverTunables.LeaseTicks, nil, htpda.Tick{}) // never recovers: lease gives up
		if b, ok := r.result.(recoverBlocked); !ok || b.Cause != CauseTerrain {
			t.Fatalf("expected Blocked{Terrain}, got %#v", r.result)
		}
	}
	r := newRecoverRig(t, rigTarget)
	giveUp(r, 110)
	giveUp(r, 300)
	if !r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 405) {
		t.Fatal("two terrain give-ups on real Java facts must blacklist the target")
	}
}

func TestRecoverContract_RealCongestionReceiptNeverBlacklists(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	fx := r.stuck(110, loadMotorFactsFixture(t, "congestion_peer.json"))
	if r.topKind() != activityKindRecover || len(fx.Intents()) != 0 {
		t.Fatalf("congestion yields without intents, top=%s intents=%v", r.topKind(), fx.Intents())
	}
	r.stuck(135, loadMotorFactsFixture(t, "congestion_peer.json"))
	if b, ok := r.result.(recoverBlocked); !ok || b.Cause != CauseCongestion {
		t.Fatalf("second congestion must give up Blocked{Congestion}, got %#v", r.result)
	}
	if r.st.IsTargetUnreachable(rigTarget[0], rigTarget[1], rigTarget[2], 140) || len(r.fsm.recoverOutbox) != 0 {
		t.Fatal("congestion facts recorded from Java must never blacklist or decay POIs")
	}
}

func TestRecoverContract_RealEnclosedReceiptDigsOut(t *testing.T) {
	r := newRecoverRig(t, rigTarget)
	blocks := withBlocks(blk(0, 66, 0, "minecraft:dirt"))
	fx := r.stuck(110, loadMotorFactsFixture(t, "enclosed_pit.json"), blocks, withPos(0.5, 64, 0.5))
	in := fx.Intents()
	if r.topKind() != activityKindRecover || len(in) != 1 || in[0].GetInteractBlock() == nil {
		t.Fatalf("real enclosed receipt must start an overhead dig, top=%s intents=%v", r.topKind(), in)
	}
}
