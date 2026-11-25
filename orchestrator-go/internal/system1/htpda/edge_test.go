package htpda

import "testing"

func TestRejectedPushes(t *testing.T) {
	log := &stepLog{}
	bad := log.wrap(&funcAct{kind: "bad", propose: gatedBid("go", BandCommitted),
		step: func(_ World, local any, ev Event) Step {
			if _, ok := ev.(Started); ok {
				return Step{Next: "kept", Op: OpPush, Child: "nope"}
			}
			return Step{Next: local}
		}})
	k := mustKernel(t, PriorityTable{AnyStage: {"bad": 1}}, Options{}, bad)
	w := at(1)
	w.facts["go"] = true
	fx := k.Dispatch(w, Tick{})
	cd, ok := log.last().ev.(ChildDone)
	if !ok || cd.ID != 0 || cd.Result != (Rejected{Kind: "nope", Reason: ReasonUnknownKind}) || log.last().local != "kept" {
		t.Fatalf("last = %+v", log.last())
	}
	if !hasDiag(fx, ReasonUnknownKind) || k.Depth() != 1 {
		t.Fatalf("fx=%+v depth=%d", fx, k.Depth())
	}
}

func TestDepthAndCascadeLimits(t *testing.T) {
	// "deep" pushes itself on every Started: bounded by MaxDepth.
	deep := &funcAct{kind: "deep", propose: gatedBid("go", BandCommitted),
		step: func(_ World, local any, ev Event) Step {
			if _, ok := ev.(Started); ok {
				return Step{Op: OpPush, Child: "deep"}
			}
			return Step{Next: local}
		}}
	k := mustKernel(t, PriorityTable{AnyStage: {"deep": 1}}, Options{MaxDepth: 4}, deep)
	w := at(1)
	w.facts["go"] = true
	fx := k.Dispatch(w, Tick{})
	if k.Depth() != 4 || !hasDiag(fx, ReasonDepthLimit) {
		t.Fatalf("depth=%d fx=%+v", k.Depth(), fx.Diagnostics)
	}

	// "spin" replaces itself forever: bounded by MaxCascade.
	spin := &funcAct{kind: "spin", propose: gatedBid("go", BandCommitted),
		step: func(World, any, Event) Step { return Step{Op: OpReplace, Child: "spin"} }}
	k = mustKernel(t, PriorityTable{AnyStage: {"spin": 1}}, Options{MaxCascade: 8}, spin)
	fx = k.Dispatch(w, Tick{})
	if k.Depth() != 1 || !hasDiag(fx, ReasonCascadeLimit) {
		t.Fatalf("depth=%d fx=%+v", k.Depth(), fx.Diagnostics)
	}
}

func TestExpiredReplaceHonouredAndStayForced(t *testing.T) {
	retry := &funcAct{kind: "retry", propose: gatedBid("go", BandCommitted),
		init: func(w World, args any) (any, Lease) { return args, Lease{DeadlineTick: w.Tick() + 2} },
		step: func(_ World, local any, ev Event) Step {
			n := LocalAs[int](local)
			if _, ok := ev.(Expired); ok {
				if n < 1 {
					return Step{Op: OpReplace, Child: "retry", Args: n + 1}
				}
				// Stay + renewal is ignored on expiry: the frame still ends.
				return Step{Next: n, Lease: &Lease{DeadlineTick: 1 << 40}}
			}
			return Step{Next: n}
		}}
	k := mustKernel(t, PriorityTable{AnyStage: {"retry": 1}}, Options{}, retry)
	w := at(1)
	w.facts["go"] = true
	k.Dispatch(w, Tick{})
	w.facts["go"] = false
	w.tick = 3
	k.Dispatch(w, Tick{})
	if top, ok := k.Top(); !ok || top.Local != 1 || top.Lease.DeadlineTick != 5 {
		t.Fatalf("replace on expiry: %+v", top)
	}
	w.tick = 5
	k.Dispatch(w, Tick{})
	if k.Depth() != 0 {
		t.Fatalf("stay on expiry not forced to pop: depth %d", k.Depth())
	}
}

func TestDispatchRejectsInternalEventsAndEmptyDirective(t *testing.T) {
	k := mustKernel(t, nil, Options{})
	for _, ev := range []Event{Started{}, ChildDone{}, Resumed{}, Expired{}} {
		if fx := k.Dispatch(at(1), ev); !hasDiag(fx, ReasonInternalEvent) {
			t.Fatalf("%T accepted", ev)
		}
	}
	if fx := k.Dispatch(at(1), Directive{Action: "BUILD"}); !hasDiag(fx, ReasonEmptyStack) {
		t.Fatal("directive on empty stack not reported")
	}
	if fx := k.Dispatch(at(1), Tick{}); fx.Decision != nil || len(fx.Diagnostics) != 0 {
		t.Fatalf("empty tick = %+v", fx)
	}
}

func TestStoreOnStayDiscarded(t *testing.T) {
	a := &funcAct{kind: "a", propose: gatedBid("go", BandIdle),
		step: func(World, any, Event) Step { return Step{Store: []StoreWrite{1}, Memory: []MemoryWrite{"m"}} }}
	k := mustKernel(t, PriorityTable{AnyStage: {"a": 1}}, Options{}, a)
	w := at(1)
	w.facts["go"] = true
	fx := k.Dispatch(w, Tick{})
	if len(fx.Store) != 0 || !hasDiag(fx, ReasonStoreOnStay) || len(fx.Memory) != 1 {
		t.Fatalf("fx = %+v", fx)
	}
}

func TestRegistryValidation(t *testing.T) {
	if _, err := NewRegistry(&funcAct{kind: "a"}, &funcAct{kind: "a"}); err == nil {
		t.Fatal("duplicate kind accepted")
	}
	if _, err := NewRegistry(&funcAct{}); err == nil {
		t.Fatal("empty kind accepted")
	}
	if _, err := NewRegistry(nil); err == nil {
		t.Fatal("nil activity accepted")
	}
}
