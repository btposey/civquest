package htpda

import (
	"slices"
	"testing"
)

// levelAct is a funcAct with a level, optional supervision and critical section.
type levelAct struct {
	*funcAct
	level     Level
	supervise func(w World, local any, child Kind) Verdict
	critical  func() bool
}

func (a levelAct) Level() Level { return a.level }
func (a levelAct) Supervise(w World, local any, child Kind) Verdict {
	if a.supervise == nil {
		return Verdict{}
	}
	return a.supervise(w, local, child)
}
func (a levelAct) Critical(World, any) bool { return a.critical != nil && a.critical() }

// pusher pushes the kind named by the fact "<kind>.child" on Started and Tick, and records
// what its children return.
func pusher(kind Kind, level Level, results *[]any) levelAct {
	return levelAct{level: level, funcAct: &funcAct{kind: kind, step: func(w World, local any, ev Event) Step {
		switch e := ev.(type) {
		case ChildDone:
			if results != nil {
				*results = append(*results, e.Result)
			}
			return Step{Next: local}
		case Cancel:
			return Step{Op: OpPop, Result: "memento:" + string(kind)}
		case Started, Tick:
			if child := w.Facts().GetString(string(kind) + ".child"); child != "" {
				return Step{Op: OpPush, Child: Kind(child), Next: local}
			}
		}
		return Step{Next: local}
	}}}
}

func goalBid(a levelAct) levelAct {
	a.funcAct.propose = func(World) Bid { return Bid{Band: BandCommitted, Utility: 1} }
	return a
}

func with(tick int64, facts fakeFacts) fakeWorld {
	return fakeWorld{tick: tick, stage: "s", facts: facts}
}

var onlyGoal = PriorityTable{AnyStage: {"goal": 1}}

func TestShape_ViolationsAreReportedAndRefusedWhenEnforced(t *testing.T) {
	cases := []struct {
		name   string
		child  string // pushed by the task frame
		reason string
	}{
		{"a more abstract child", "explore", ReasonNotDeeper},
		{"a sibling level", "fell", ReasonNotDeeper},
		{"itself", "mine", ReasonDuplicateKind},
		{"an ancestor", "goal", ReasonDuplicateKind},
	}
	for _, c := range cases {
		for _, enforce := range []bool{false, true} {
			var results []any
			k := mustKernel(t, onlyGoal, Options{EnforceShape: enforce},
				goalBid(pusher("goal", LevelGoal, nil)), pusher("explore", LevelObjective, nil),
				pusher("mine", LevelTask, &results), pusher("fell", LevelTask, nil))
			fx := k.Dispatch(with(1, fakeFacts{"goal.child": "mine", "mine.child": c.child}), Tick{})
			if !hasDiag(fx, c.reason) {
				t.Errorf("%s (enforce=%t): no %q diagnostic: %v", c.name, enforce, c.reason, fx.Diagnostics)
			}
			refused := slices.ContainsFunc(results, func(r any) bool { rej, ok := r.(Rejected); return ok && rej.Reason == c.reason })
			if refused != enforce {
				t.Errorf("%s (enforce=%t): refused=%t stack=%v", c.name, enforce, refused, kinds(k.Frames()))
			}
		}
	}
}

func TestShape_DeeperChildrenAndInterruptsAreAllowed(t *testing.T) {
	k := mustKernel(t, PriorityTable{AnyStage: {"goal": 1, "emergency": 2}}, Options{EnforceShape: true},
		goalBid(pusher("goal", LevelGoal, nil)), pusher("explore", LevelObjective, nil), pusher("walk", LevelStep, nil),
		levelAct{level: LevelInterrupt, funcAct: &funcAct{kind: "emergency", propose: gatedBid("threat", BandEmergency)}})
	facts := fakeFacts{"goal.child": "explore", "explore.child": "walk"}
	fx := k.Dispatch(with(1, facts), Tick{})
	if len(fx.Diagnostics) != 0 || !slices.Equal(kinds(k.Frames()), []Kind{"goal", "explore", "walk"}) {
		t.Fatalf("stack=%v diagnostics=%v", kinds(k.Frames()), fx.Diagnostics)
	}
	facts["threat"] = true
	fx = k.Dispatch(with(5, facts), Tick{})
	if len(fx.Diagnostics) != 0 || !slices.Equal(kinds(k.Frames()), []Kind{"goal", "explore", "walk", "emergency"}) {
		t.Fatalf("interrupt: stack=%v diagnostics=%v", kinds(k.Frames()), fx.Diagnostics)
	}
}

func TestShape_SidewaysArbiterPushAndNonGoalBaseAreViolations(t *testing.T) {
	k := mustKernel(t, PriorityTable{AnyStage: {"explore": 1, "rest": 1}}, Options{},
		levelAct{level: LevelObjective, funcAct: &funcAct{kind: "explore", propose: gatedBid("go", BandRoutine)}},
		levelAct{level: LevelObjective, funcAct: &funcAct{kind: "rest", propose: gatedBid("tired", BandCommitted)}})
	if fx := k.Dispatch(with(1, fakeFacts{"go": true}), Tick{}); !hasDiag(fx, ReasonBaseNotGoal) {
		t.Errorf("objective as base: %v", fx.Diagnostics)
	}
	if fx := k.Dispatch(with(5, fakeFacts{"go": true, "tired": true}), Tick{}); !hasDiag(fx, ReasonSideways) {
		t.Errorf("sideways push: %v", fx.Diagnostics)
	}
}

func supervised(results *[]any, critical func() bool) *Kernel {
	goal := goalBid(pusher("goal", LevelGoal, results))
	goal.supervise = func(w World, _ any, child Kind) Verdict {
		if child == "explore" && w.Facts().GetBool("night") {
			return Verdict{Recall: true, Reason: "night"}
		}
		return Verdict{}
	}
	walk := pusher("walk", LevelStep, nil)
	walk.critical = critical
	reg, err := NewRegistry(goal, pusher("explore", LevelObjective, nil), walk,
		levelAct{level: LevelInterrupt, funcAct: &funcAct{kind: "emergency", propose: gatedBid("threat", BandEmergency)}})
	if err != nil {
		panic(err)
	}
	return NewKernel(reg, PriorityTable{AnyStage: {"goal": 1, "emergency": 2}}, Options{EnforceShape: true})
}

func TestSupervise_ParentRecallsItsSubtreeAndGetsTheMemento(t *testing.T) {
	var results []any
	k := supervised(&results, nil)
	facts := fakeFacts{"goal.child": "explore", "explore.child": "walk"}
	k.Dispatch(with(1, facts), Tick{})
	delete(facts, "goal.child")
	facts["night"] = true
	fx := k.Dispatch(with(5, facts), Tick{})
	if !slices.Equal(kinds(k.Frames()), []Kind{"goal"}) {
		t.Fatalf("stack after recall: %v", kinds(k.Frames()))
	}
	if len(results) != 1 {
		t.Fatalf("results %v", results)
	}
	got, ok := results[0].(Recalled)
	if !ok || got.Child != "explore" || got.Reason != "night" || got.Memento != "memento:explore" {
		t.Errorf("recalled = %+v", results[0])
	}
	var cancelled []Kind
	for _, l := range fx.Lifecycle {
		if l.Op == LifeCancel {
			cancelled = append(cancelled, l.Kind)
		}
	}
	if !slices.Equal(cancelled, []Kind{"walk", "explore"}) {
		t.Errorf("cancel order %v, want top first", cancelled)
	}
}

func TestSupervise_CriticalSectionsAndInterruptsAreNotRecalled(t *testing.T) {
	busy := true
	k := supervised(nil, func() bool { return busy })
	facts := fakeFacts{"goal.child": "explore", "explore.child": "walk"}
	k.Dispatch(with(1, facts), Tick{})
	facts["night"] = true
	k.Dispatch(with(5, facts), Tick{})
	if k.Depth() != 3 {
		t.Fatalf("recalled during a critical section: %v", kinds(k.Frames()))
	}
	busy = false
	delete(facts, "night")
	facts["threat"] = true
	k.Dispatch(with(9, facts), Tick{})
	facts["night"] = true
	k.Dispatch(with(13, facts), Tick{})
	if !slices.Equal(kinds(k.Frames()), []Kind{"goal", "explore", "walk", "emergency"}) {
		t.Errorf("recalled under an interrupt: %v", kinds(k.Frames()))
	}
}

// A child that does nothing and pops at once is the churn this round kept patching.
func TestBackoff_IdleChildIsNotRelaunchedAtOnce(t *testing.T) {
	for _, c := range []struct {
		name  string
		emits bool
		want  int
	}{{"idle child", false, 1}, {"working child", true, 20}} {
		launches := 0
		child := levelAct{level: LevelTask, funcAct: &funcAct{kind: "fell", step: func(_ World, local any, ev Event) Step {
			s := Step{Op: OpPop}
			if _, ok := ev.(Started); ok {
				launches++
				if c.emits {
					s.Intents = append(s.Intents, intent("chop"))
				}
			}
			return s
		}}}
		k := mustKernel(t, onlyGoal, Options{NoProgressBackoff: 100}, goalBid(pusher("goal", LevelGoal, nil)), child)
		facts := fakeFacts{"goal.child": "fell"}
		for i := int64(0); i < 20; i++ {
			k.Dispatch(with(1+i*4, facts), Tick{})
		}
		if launches != c.want {
			t.Errorf("%s: launched %d times in 77 ticks, want %d", c.name, launches, c.want)
		}
		if !c.emits {
			k.Dispatch(with(200, facts), Tick{})
			if launches != 2 {
				t.Errorf("after the back-off: %d launches", launches)
			}
		}
	}
}
