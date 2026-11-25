package htpda

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// rlocal is the value-type locals of a randomized activity.
type rlocal struct {
	UID   int
	Seed  uint64
	Steps int
	Path  [3]int
}

// harness drives randomized activities and checks invariants from inside Step.
type harness struct {
	t        testing.TB
	nextUID  int
	inits    int
	ends     int
	lastNext map[int]rlocal // UID -> locals the kernel must hand back
	receipts []FrameID      // Receipt/Reflex frame IDs actually delivered
	failed   bool
}

var rkinds = []Kind{"a", "b", "c", "d"}

func mix(xs ...uint64) uint64 {
	h := uint64(1469598103934665603)
	for _, x := range xs {
		h ^= x
		h *= 1099511628211
		h ^= h >> 29
	}
	return h
}

func evCode(ev Event) uint64 {
	switch ev.(type) {
	case Started:
		return 1
	case Tick:
		return 2
	case ChildDone:
		return 3
	case Resumed:
		return 4
	case Receipt:
		return 5
	case Reflex:
		return 6
	case Directive:
		return 7
	case Expired:
		return 8
	case Cancel:
		return 9
	}
	return 0
}

func (h *harness) fail(format string, args ...any) {
	h.failed = true
	h.t.Errorf(format, args...)
}

func (h *harness) act(kind Kind, band Band) Activity {
	return &funcAct{kind: kind, scope: ScopeMotor,
		propose: func(w World) Bid {
			if !w.Facts().GetBool("bid." + string(kind)) {
				return Bid{}
			}
			return Bid{Band: band, Utility: 1, Args: uint64(w.Tick())}
		},
		init: func(w World, args any) (any, Lease) {
			h.inits++
			h.nextUID++
			seed, _ := args.(uint64)
			l := rlocal{UID: h.nextUID, Seed: mix(seed, uint64(h.nextUID))}
			var lease Lease
			if l.Seed%3 == 0 {
				lease.DeadlineTick = w.Tick() + int64(l.Seed%7) + 1
			}
			return l, lease
		},
		step: func(w World, local any, ev Event) Step { return h.step(kind, w, local, ev) },
	}
}

func (h *harness) step(kind Kind, w World, local any, ev Event) Step {
	l, ok := local.(rlocal)
	if !ok {
		h.fail("%s: locals type %T", kind, local)
		return Step{}
	}
	if want, seen := h.lastNext[l.UID]; seen && want != l {
		h.fail("%s uid %d: locals %+v, want %+v (event %T)", kind, l.UID, l, want, ev)
	}
	switch e := ev.(type) {
	case Receipt:
		h.receipts = append(h.receipts, e.Frame)
	case Reflex:
		h.receipts = append(h.receipts, e.Frame)
	case Cancel:
		h.ends++
		delete(h.lastNext, l.UID)
		return Step{Store: []StoreWrite{fmt.Sprintf("cancel:%d", l.UID)}}
	}
	r := mix(l.Seed, uint64(l.Steps), evCode(ev), uint64(w.Tick()))
	next := l
	next.Steps++
	next.Path[l.Steps%3] = int(r % 97)
	s := Step{Next: next, Intents: []*civquestv1.AgentIntent{intent(fmt.Sprintf("%d:%d", l.UID, l.Steps))}}
	switch op := r % 16; {
	case op < 3:
		s.Op, s.Child, s.Args = OpPush, rkinds[(r>>8)%4], r>>16
	case op < 6:
		s.Op, s.Result, s.Store = OpPop, l, []StoreWrite{fmt.Sprintf("pop:%d", l.UID)}
	case op == 6:
		s.Op, s.Child, s.Args = OpReplace, rkinds[(r>>8)%4], r>>16
	}
	if r%11 == 0 {
		s.Memory = []MemoryWrite{fmt.Sprintf("mem:%d", l.UID)}
	}
	_, expired := ev.(Expired)
	if expired || s.Op == OpPop || s.Op == OpReplace {
		h.ends++
		delete(h.lastNext, l.UID)
	} else {
		h.lastNext[l.UID] = next
	}
	return s
}

func newHarness(t testing.TB, order []int) (*harness, *Kernel) {
	h := &harness{t: t, lastNext: map[int]rlocal{}}
	bands := []Band{BandIdle, BandRoutine, BandCommitted, BandEmergency}
	acts := make([]Activity, len(order))
	for i, idx := range order {
		acts[i] = h.act(rkinds[idx], bands[idx])
	}
	prio := PriorityTable{AnyStage: {"a": 1, "b": 1, "c": 1, "d": 1}}
	return h, mustKernel(t, prio, Options{MaxDepth: 8, MaxCascade: 32}, acts...)
}

// randomEvent draws the next event; target frame IDs mix top, suspended and stale.
func randomEvent(rng *rand.Rand, w *fakeWorld, k *Kernel) Event {
	frames := k.Frames()
	pickFrame := func() FrameID {
		switch {
		case len(frames) > 0 && rng.Intn(2) == 0:
			return frames[len(frames)-1].ID
		case len(frames) > 0 && rng.Intn(2) == 0:
			return frames[rng.Intn(len(frames))].ID
		}
		return FrameID(rng.Intn(64))
	}
	switch p := rng.Intn(100); {
	case p < 20:
		return Receipt{Frame: pickFrame(), Status: ReceiptStatus(rng.Intn(7))}
	case p < 30:
		return Reflex{Frame: pickFrame()}
	case p < 38:
		return Directive{Action: "X"}
	case p < 40:
		return Cancel{Reason: "test"}
	}
	w.tick += int64(rng.Intn(3))
	w.facts = fakeFacts{}
	for _, kd := range rkinds {
		if rng.Intn(4) == 0 {
			w.facts["bid."+string(kd)] = true
		}
	}
	return Tick{}
}

func checkStack(t testing.TB, h *harness, k *Kernel, maxDepth int) {
	frames := k.Frames()
	if len(frames) != h.inits-h.ends {
		h.fail("push/pop imbalance: depth %d, inits %d, ends %d", len(frames), h.inits, h.ends)
	}
	if len(frames) > maxDepth {
		h.fail("depth %d > %d", len(frames), maxDepth)
	}
	for i, f := range frames {
		if i > 0 && f.ID <= frames[i-1].ID {
			h.fail("frame IDs not increasing: %v", frames)
		}
		if f.Parent != 0 && (i == 0 || frames[i-1].ID != f.Parent) {
			h.fail("frame %d parent %d not directly below", f.ID, f.Parent)
		}
	}
}

// runLog runs a randomized event log and returns a canonical trace.
func runLog(t testing.TB, seed int64, n int, order []int) string {
	h, k := newHarness(t, order)
	rng := rand.New(rand.NewSource(seed))
	w := fakeWorld{tick: 1, stage: "s", facts: fakeFacts{}}
	var trace strings.Builder
	for i := 0; i < n && !h.failed; i++ {
		ev := randomEvent(rng, &w, k)
		var preTop FrameID
		if top, ok := k.Top(); ok {
			preTop = top.ID
		}
		nr := len(h.receipts)
		fx := k.Dispatch(w, ev)
		var target FrameID = 1<<63 | 1
		switch e := ev.(type) {
		case Receipt:
			target = e.Frame
		case Reflex:
			target = e.Frame
		}
		if target != 1<<63|1 {
			delivered := len(h.receipts) > nr
			if delivered != (preTop != 0 && target == preTop) {
				h.fail("event %+v to frame %d delivered=%v, top was %d", ev, target, delivered, preTop)
			}
			if delivered && h.receipts[nr] != preTop {
				h.fail("receipt for %d delivered as %d", preTop, h.receipts[nr])
			}
		}
		checkStack(t, h, k, 8)
		fmt.Fprintf(&trace, "%d %T|", i, ev)
		for _, em := range fx.Emissions {
			fmt.Fprintf(&trace, "e%d/%s/", em.Frame, em.Kind)
			for _, in := range em.Intents {
				trace.WriteString(in.GetAgentId() + ",")
			}
		}
		fmt.Fprintf(&trace, "s%v m%v d%v|", fx.Store, fx.Memory, fx.Diagnostics)
		if fx.Decision != nil {
			fmt.Fprintf(&trace, "w%s/%v/%d|", fx.Decision.Winner.Bid.Kind, fx.Decision.Pushed, fx.Decision.Frame)
		}
		for _, f := range k.Frames() {
			fmt.Fprintf(&trace, "[%d %s %v %+v]", f.ID, f.Kind(), f.Lease, f.Local)
		}
		trace.WriteByte('\n')
	}
	return trace.String()
}

func TestPropertyRandomEventSequences(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		runLog(t, seed, 400, []int{0, 1, 2, 3})
		if t.Failed() {
			t.Fatalf("seed %d failed", seed)
		}
	}
}

func TestDeterminismSameLogSameIntents(t *testing.T) {
	for seed := int64(1); seed <= 25; seed++ {
		a := runLog(t, seed, 300, []int{0, 1, 2, 3})
		b := runLog(t, seed, 300, []int{3, 1, 0, 2}) // registration order must not matter
		if a != b {
			t.Fatalf("seed %d: traces differ\n--- a\n%.2000s\n--- b\n%.2000s", seed, a, b)
		}
	}
}

func BenchmarkDispatchTick(b *testing.B) {
	_, k := newHarness(b, []int{0, 1, 2, 3})
	w := fakeWorld{tick: 1, stage: "s", facts: fakeFacts{"bid.c": true}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.tick++
		k.Dispatch(w, Tick{})
	}
}
