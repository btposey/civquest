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

// supervised: the goal sets explore aside at night, rests, and takes it up again.
func supervised(results *[]any, critical func() bool) *Kernel {
	var parked []Frame
	goal := levelAct{level: LevelGoal, funcAct: &funcAct{kind: "goal", propose: func(World) Bid { return Bid{Band: BandCommitted, Utility: 1} },
		step: func(w World, local any, ev Event) Step {
			switch e := ev.(type) {
			case Started:
				return Step{Op: OpPush, Child: "explore"}
			case ChildDone:
				if results != nil {
					*results = append(*results, e.Result)
				}
				if p, ok := e.Result.(Parked); ok {
					parked = p.Frames
					return Step{Op: OpPush, Child: "rest"}
				}
				if e.Child == "rest" {
					return Step{Op: OpResume, Resume: parked, Child: "rest", Result: "rested"}
				}
			}
			return Step{}
		}}}
	goal.supervise = func(w World, _ any, child Kind) Verdict {
		if child == "explore" && w.Facts().GetBool("night") {
			return Verdict{Park: true, Reason: "night", Token: "rest"}
		}
		return Verdict{}
	}
	explore := levelAct{level: LevelObjective, funcAct: &funcAct{kind: "explore", step: func(_ World, local any, ev Event) Step {
		if _, ok := ev.(Started); ok {
			return Step{Op: OpPush, Child: "walk", Next: "route"}
		}
		return Step{Next: local}
	}}}
	walk := levelAct{level: LevelStep, funcAct: &funcAct{kind: "walk", step: func(w World, local any, ev Event) Step {
		if e, ok := ev.(ChildDone); ok && results != nil {
			*results = append(*results, e.Result)
		}
		return Step{Next: local}
	}}}
	walk.critical = critical
	rest := levelAct{level: LevelTask, funcAct: &funcAct{kind: "rest", step: func(w World, local any, ev Event) Step {
		if w.Facts().GetBool("rested") {
			return Step{Op: OpPop}
		}
		return Step{Next: local}
	}}}
	reg, err := NewRegistry(goal, explore, walk, rest,
		levelAct{level: LevelInterrupt, funcAct: &funcAct{kind: "emergency", propose: gatedBid("threat", BandEmergency)}})
	if err != nil {
		panic(err)
	}
	return NewKernel(reg, PriorityTable{AnyStage: {"goal": 1, "emergency": 2}}, Options{EnforceShape: true})
}

func TestSupervise_ParentParksASubtreeAndResumesItIntact(t *testing.T) {
	var results []any
	k := supervised(&results, nil)
	facts := fakeFacts{}
	k.Dispatch(with(1, facts), Tick{})
	before := k.Frames()
	facts["night"] = true
	k.Dispatch(with(5, facts), Tick{})
	if !slices.Equal(kinds(k.Frames()), []Kind{"goal", "rest"}) {
		t.Fatalf("stack after parking: %v", kinds(k.Frames()))
	}
	got, ok := results[0].(Parked)
	if !ok || got.Child != "explore" || got.Reason != "night" || got.Token != "rest" || !slices.Equal(kinds(got.Frames), []Kind{"explore", "walk"}) {
		t.Fatalf("parked = %+v", results[0])
	}
	delete(facts, "night")
	facts["rested"] = true
	k.Dispatch(with(9, facts), Tick{})
	after := k.Frames()
	if !slices.Equal(kinds(after), []Kind{"goal", "explore", "walk"}) {
		t.Fatalf("stack after resuming: %v", kinds(after))
	}
	if after[1].ID != before[1].ID || after[1].Local != "route" || after[2].ID != before[2].ID {
		t.Errorf("resumed frames lost identity or locals: %+v", after[1:])
	}
	if results[len(results)-1] != "rested" {
		t.Errorf("the restored top did not receive the result: %v", results)
	}
}

func TestSupervise_CriticalSectionsAndInterruptsAreNotParked(t *testing.T) {
	busy := true
	k := supervised(nil, func() bool { return busy })
	facts := fakeFacts{}
	k.Dispatch(with(1, facts), Tick{})
	facts["night"] = true
	k.Dispatch(with(5, facts), Tick{})
	if k.Depth() != 3 {
		t.Fatalf("parked during a critical section: %v", kinds(k.Frames()))
	}
	busy = false
	delete(facts, "night")
	facts["threat"] = true
	k.Dispatch(with(9, facts), Tick{})
	facts["night"] = true
	k.Dispatch(with(13, facts), Tick{})
	if !slices.Equal(kinds(k.Frames()), []Kind{"goal", "explore", "walk", "emergency"}) {
		t.Errorf("parked under an interrupt: %v", kinds(k.Frames()))
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
