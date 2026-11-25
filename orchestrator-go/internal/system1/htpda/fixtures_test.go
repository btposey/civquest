package htpda

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// fakeFacts is a map-backed FactReader.
type fakeFacts map[string]any

func (f fakeFacts) GetBool(k string) bool { v, _ := f[k].(bool); return v }
func (f fakeFacts) GetInt(k string) (int, bool) {
	v, ok := f[k].(int)
	return v, ok
}
func (f fakeFacts) GetFloat(k string) (float64, bool) {
	v, ok := f[k].(float64)
	return v, ok
}
func (f fakeFacts) GetString(k string) string { v, _ := f[k].(string); return v }

// fakeWorld is an immutable World value.
type fakeWorld struct {
	tick  int64
	stage Stage
	facts fakeFacts
}

func (w fakeWorld) Tick() int64                       { return w.tick }
func (w fakeWorld) Frame() *civquestv1.TelemetryFrame { return nil }
func (w fakeWorld) Facts() FactReader                 { return w.facts }
func (w fakeWorld) Stage() Stage                      { return w.stage }
func (w fakeWorld) Routine() Routine                  { return "" }

func at(tick int64) fakeWorld { return fakeWorld{tick: tick, stage: "s", facts: fakeFacts{}} }

// funcAct is a scriptable Activity.
type funcAct struct {
	kind    Kind
	scope   Scope
	propose func(w World) Bid
	init    func(w World, args any) (any, Lease)
	step    func(w World, local any, ev Event) Step
}

func (a *funcAct) Kind() Kind   { return a.kind }
func (a *funcAct) Scope() Scope { return a.scope }
func (a *funcAct) Propose(w World) Bid {
	if a.propose == nil {
		return Bid{}
	}
	return a.propose(w)
}
func (a *funcAct) Init(w World, args any) (any, Lease) {
	if a.init == nil {
		return args, Lease{}
	}
	return a.init(w, args)
}
func (a *funcAct) Step(w World, local any, ev Event) Step {
	if a.step == nil {
		return Step{Next: local}
	}
	return a.step(w, local, ev)
}

// stepLog records every Step call for assertions.
type stepLog struct {
	calls []stepCall
}

type stepCall struct {
	kind  Kind
	local any
	ev    Event
}

func (l *stepLog) wrap(a *funcAct) *funcAct {
	inner := a.step
	return &funcAct{kind: a.kind, scope: a.scope, propose: a.propose, init: a.init,
		step: func(w World, local any, ev Event) Step {
			l.calls = append(l.calls, stepCall{kind: a.kind, local: local, ev: ev})
			if inner == nil {
				return Step{Next: local}
			}
			return inner(w, local, ev)
		}}
}

func (l *stepLog) last() stepCall { return l.calls[len(l.calls)-1] }

func mustKernel(t testing.TB, prio PriorityTable, opts Options, acts ...Activity) *Kernel {
	t.Helper()
	reg, err := NewRegistry(acts...)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return NewKernel(reg, prio, opts)
}

func intent(tag string) *civquestv1.AgentIntent { return &civquestv1.AgentIntent{AgentId: tag} }

func kinds(fs []Frame) []Kind {
	out := make([]Kind, len(fs))
	for i, f := range fs {
		out[i] = f.Kind()
	}
	return out
}

func hasDiag(fx Effects, reason string) bool {
	for _, d := range fx.Diagnostics {
		if d.Reason == reason {
			return true
		}
	}
	return false
}

// always bids for kind at band with utility 1 while the fact gate holds.
func gatedBid(fact string, band Band) func(World) Bid {
	return func(w World) Bid {
		if !w.Facts().GetBool(fact) {
			return Bid{}
		}
		return Bid{Band: band, Utility: 1}
	}
}
