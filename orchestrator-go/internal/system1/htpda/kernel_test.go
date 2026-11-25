package htpda

import (
	"reflect"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

type parentLocal struct {
	Phase   int
	Targets [2]int
}

// parentChild builds "parent" (pushes "child" on its first Tick) and "child"
// (pops with Result "done" on its first Tick).
func parentChild(log *stepLog) (*funcAct, *funcAct) {
	parent := log.wrap(&funcAct{kind: "parent", scope: ScopeMotor,
		propose: gatedBid("want.parent", BandCommitted),
		init:    func(World, any) (any, Lease) { return parentLocal{Phase: 1, Targets: [2]int{7, 9}}, Lease{} },
		step: func(_ World, local any, ev Event) Step {
			l := LocalAs[parentLocal](local)
			if _, ok := ev.(Tick); ok && l.Phase == 1 {
				l.Phase = 2
				return Step{Next: l, Op: OpPush, Child: "child", Args: 42}
			}
			return Step{Next: l}
		}})
	child := log.wrap(&funcAct{kind: "child", scope: ScopeMotor,
		step: func(_ World, local any, ev Event) Step {
			if _, ok := ev.(Tick); ok {
				return Step{Op: OpPop, Result: "done", Store: []StoreWrite{"child-store"}}
			}
			return Step{Next: local, Intents: []*civquestv1.AgentIntent{intent("child")}}
		}})
	return parent, child
}

func TestArbiterPushDeliversStartedThenTick(t *testing.T) {
	log := &stepLog{}
	p, c := parentChild(log)
	k := mustKernel(t, PriorityTable{AnyStage: {"parent": 1}}, Options{}, p, c)
	w := at(10)
	w.facts["want.parent"] = true

	fx := k.Dispatch(w, Tick{})
	if fx.Decision == nil || !fx.Decision.Pushed || fx.Decision.Winner.Bid.Kind != "parent" {
		t.Fatalf("decision = %+v", fx.Decision)
	}
	if _, ok := log.last().ev.(Started); !ok || len(log.calls) != 1 {
		t.Fatalf("want only Started on push tick, got %+v", log.calls)
	}
	top, _ := k.Top()
	if top.Lease.StartTick != 10 || top.Band != BandCommitted || top.Parent != 0 || top.ID != 1 {
		t.Fatalf("top frame = %+v", top)
	}
}

func TestChildResultDeliveredToParentWithIntactLocals(t *testing.T) {
	log := &stepLog{}
	p, c := parentChild(log)
	k := mustKernel(t, PriorityTable{AnyStage: {"parent": 1}}, Options{}, p, c)
	w := at(1)
	w.facts["want.parent"] = true
	k.Dispatch(w, Tick{}) // push parent
	fx := k.Dispatch(w, Tick{})
	// parent Tick -> push child -> child Started (emits intent)
	if got := kinds(k.Frames()); !reflect.DeepEqual(got, []Kind{"parent", "child"}) {
		t.Fatalf("stack = %v", got)
	}
	if len(fx.Emissions) != 1 || fx.Emissions[0].Frame != 2 || fx.Emissions[0].Kind != "child" {
		t.Fatalf("emissions = %+v", fx.Emissions)
	}
	frames := k.Frames()
	if frames[1].Parent != frames[0].ID || frames[1].Local != 42 {
		t.Fatalf("child frame = %+v", frames[1])
	}
	fx = k.Dispatch(w, Tick{}) // child pops
	if k.Depth() != 1 {
		t.Fatalf("depth = %d", k.Depth())
	}
	last := log.last()
	cd, ok := last.ev.(ChildDone)
	if !ok || last.kind != "parent" || cd.Result != "done" || cd.Child != "child" || cd.ID != 2 {
		t.Fatalf("parent got %+v", last)
	}
	if want := (parentLocal{Phase: 2, Targets: [2]int{7, 9}}); last.local != want {
		t.Fatalf("parent locals = %+v, want %+v", last.local, want)
	}
	if !reflect.DeepEqual(fx.Store, []StoreWrite{"child-store"}) {
		t.Fatalf("store = %v", fx.Store)
	}
}

func TestReceiptRouting(t *testing.T) {
	log := &stepLog{}
	p, c := parentChild(log)
	k := mustKernel(t, PriorityTable{AnyStage: {"parent": 1}}, Options{}, p, c)
	w := at(1)
	w.facts["want.parent"] = true
	k.Dispatch(w, Tick{})
	k.Dispatch(w, Tick{}) // stack: parent(1), child(2)
	n := len(log.calls)

	fx := k.Dispatch(w, Receipt{Frame: 1})
	if !hasDiag(fx, ReasonSuspended) || len(log.calls) != n {
		t.Fatalf("suspended receipt not dropped: %+v", fx)
	}
	fx = k.Dispatch(w, Receipt{Frame: 99})
	if !hasDiag(fx, ReasonNotOnStack) || len(log.calls) != n {
		t.Fatalf("unknown receipt not dropped: %+v", fx)
	}
	fx = k.Dispatch(w, Reflex{Frame: 0})
	if !hasDiag(fx, ReasonNotOnStack) {
		t.Fatalf("frame 0 reflex not dropped: %+v", fx)
	}
	fx = k.Dispatch(w, Receipt{Frame: 2, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED})
	if len(fx.Diagnostics) != 0 || len(log.calls) != n+1 {
		t.Fatalf("top receipt not delivered: %+v", fx)
	}
	if r, ok := log.last().ev.(Receipt); !ok || r.Frame != 2 {
		t.Fatalf("child got %+v", log.last())
	}
}

func TestReplaceKeepsSlotParentAndBand(t *testing.T) {
	log := &stepLog{}
	var hops int
	exp := log.wrap(&funcAct{kind: "exp",
		propose: gatedBid("go", BandCommitted),
		step: func(_ World, local any, ev Event) Step {
			n := LocalAs[int](local)
			if _, ok := ev.(Tick); ok && n < 2 {
				hops++
				return Step{Op: OpReplace, Child: "exp", Args: n + 1, Store: []StoreWrite{n}}
			}
			return Step{Next: n}
		}})
	k := mustKernel(t, PriorityTable{AnyStage: {"exp": 1}}, Options{}, exp)
	w := at(5)
	w.facts["go"] = true
	k.Dispatch(w, Tick{})
	first, _ := k.Top()
	fx := k.Dispatch(w, Tick{})
	top, _ := k.Top()
	if k.Depth() != 1 || top.ID == first.ID || top.Local != 1 || top.Band != BandCommitted || top.Parent != 0 {
		t.Fatalf("after replace top = %+v (first %+v)", top, first)
	}
	if !reflect.DeepEqual(fx.Store, []StoreWrite{0}) {
		t.Fatalf("replace store = %v", fx.Store)
	}
	for _, c := range log.calls {
		if _, ok := c.ev.(ChildDone); ok {
			t.Fatalf("replace must not notify: %+v", c)
		}
	}
}

func TestLeaseExpiryPopsWithTimeout(t *testing.T) {
	log := &stepLog{}
	parent := log.wrap(&funcAct{kind: "parent", propose: gatedBid("go", BandCommitted),
		step: func(_ World, local any, ev Event) Step {
			if _, ok := ev.(Started); ok {
				return Step{Next: "waiting", Op: OpPush, Child: "slow"}
			}
			return Step{Next: local}
		}})
	slow := log.wrap(&funcAct{kind: "slow",
		init: func(w World, _ any) (any, Lease) { return 0, Lease{DeadlineTick: w.Tick() + 3} }})
	k := mustKernel(t, PriorityTable{AnyStage: {"parent": 1}}, Options{}, parent, slow)
	w := at(10)
	w.facts["go"] = true
	k.Dispatch(w, Tick{})
	if k.Depth() != 2 {
		t.Fatalf("depth = %d", k.Depth())
	}
	w.tick = 12
	k.Dispatch(w, Tick{})
	if k.Depth() != 2 {
		t.Fatal("expired early")
	}
	w.tick = 13
	k.Dispatch(w, Tick{})
	if k.Depth() != 1 {
		t.Fatalf("not expired: depth %d", k.Depth())
	}
	var sawExpired bool
	for _, c := range log.calls {
		if e, ok := c.ev.(Expired); ok && c.kind == "slow" && e.Deadline == 13 {
			sawExpired = true
		}
	}
	cd, ok := log.last().ev.(ChildDone)
	if !sawExpired || !ok || cd.Result != (Timeout{Kind: "slow", Deadline: 13}) || log.last().local != "waiting" {
		t.Fatalf("expired=%v last=%+v", sawExpired, log.last())
	}
}

func TestRenewOnProgressSlidesDeadline(t *testing.T) {
	act := &funcAct{kind: "nav", propose: gatedBid("go", BandCommitted),
		init: func(w World, _ any) (any, Lease) {
			return nil, Lease{DeadlineTick: w.Tick() + 5, RenewOnProgress: true}
		}}
	k := mustKernel(t, PriorityTable{AnyStage: {"nav": 1}}, Options{}, act)
	w := at(100)
	w.facts["go"] = true
	k.Dispatch(w, Tick{})
	w.tick = 104
	k.Dispatch(w, Receipt{Frame: 1, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED})
	if top, _ := k.Top(); top.Lease.DeadlineTick != 105 {
		t.Fatalf("failed receipt renewed lease: %+v", top.Lease)
	}
	k.Dispatch(w, Receipt{Frame: 1, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING})
	if top, _ := k.Top(); top.Lease != (Lease{StartTick: 104, DeadlineTick: 109, RenewOnProgress: true}) {
		t.Fatalf("lease = %+v", top.Lease)
	}
}

func TestPreemptionAndResume(t *testing.T) {
	log := &stepLog{}
	work := log.wrap(&funcAct{kind: "work", propose: gatedBid("work", BandRoutine),
		init: func(World, any) (any, Lease) { return parentLocal{Phase: 5}, Lease{} },
		step: func(_ World, local any, ev Event) Step {
			l := LocalAs[parentLocal](local)
			l.Phase++
			return Step{Next: l}
		}})
	flee := log.wrap(&funcAct{kind: "flee", propose: gatedBid("threat", BandEmergency),
		step: func(_ World, local any, ev Event) Step {
			if _, ok := ev.(Tick); ok {
				return Step{Op: OpPop, Result: "safe"}
			}
			return Step{Next: local}
		}})
	other := &funcAct{kind: "other", propose: gatedBid("work", BandRoutine)}
	k := mustKernel(t, PriorityTable{AnyStage: {"work": 2, "flee": 1, "other": 1}}, Options{}, work, flee, other)
	w := at(1)
	w.facts["work"] = true
	k.Dispatch(w, Tick{}) // push work (Started: Phase 6)
	fx := k.Dispatch(w, Tick{})
	if fx.Decision.Pushed || k.Depth() != 1 {
		t.Fatalf("equal band preempted: %+v", fx.Decision)
	}
	w.facts["threat"] = true
	k.Dispatch(w, Tick{}) // push flee over work
	if got := kinds(k.Frames()); !reflect.DeepEqual(got, []Kind{"work", "flee"}) {
		t.Fatalf("stack = %v", got)
	}
	w.facts["threat"] = false
	k.Dispatch(w, Tick{}) // flee pops -> work Resumed
	last := log.last()
	r, ok := last.ev.(Resumed)
	if !ok || r.From != "flee" || r.Result != "safe" || last.local != (parentLocal{Phase: 7}) {
		t.Fatalf("resume = %+v", last)
	}
}

func TestCancelUnwindsAndForwardsStore(t *testing.T) {
	var order []Kind
	mk := func(kind Kind, push Kind) *funcAct {
		return &funcAct{kind: kind, propose: gatedBid("go."+string(kind), BandCommitted),
			step: func(_ World, local any, ev Event) Step {
				switch ev.(type) {
				case Started:
					if push != "" {
						return Step{Op: OpPush, Child: push}
					}
				case Cancel:
					order = append(order, kind)
					return Step{Store: []StoreWrite{kind}, Intents: []*civquestv1.AgentIntent{intent("x")}}
				}
				return Step{Next: local}
			}}
	}
	k := mustKernel(t, PriorityTable{AnyStage: {"a": 1}}, Options{}, mk("a", "b"), mk("b", ""))
	w := at(1)
	w.facts["go.a"] = true
	k.Dispatch(w, Tick{})
	fx := k.Dispatch(w, Cancel{Reason: "death"})
	if k.Depth() != 0 || !reflect.DeepEqual(order, []Kind{"b", "a"}) || !reflect.DeepEqual(fx.Store, []StoreWrite{Kind("b"), Kind("a")}) {
		t.Fatalf("depth=%d order=%v store=%v", k.Depth(), order, fx.Store)
	}
	if len(fx.Emissions) != 0 || !hasDiag(fx, ReasonIntentsOnClose) {
		t.Fatalf("cancel intents leaked: %+v", fx)
	}
}
