package htpda

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Default kernel limits.
const (
	DefaultMaxDepth   = 16
	DefaultMaxCascade = 64
)

// Options configures a Kernel. Zero values select the defaults.
type Options struct {
	MaxDepth   int // maximum stack depth; pushes beyond it are Rejected
	MaxCascade int // maximum Step calls per Dispatch (bounds push/pop cascades)
}

// Emission is the intents one frame emitted during a step. The wiring layer
// stamps Frame onto the outgoing IntentBatch (frame_id seam, cq-2sr.11).
type Emission struct {
	Frame   FrameID
	Kind    Kind
	Intents []*civquestv1.AgentIntent
}

// Diagnostic records a dropped event or a refused/ignored request.
type Diagnostic struct {
	Frame  FrameID
	Kind   Kind
	Reason string
}

// Diagnostic reasons.
const (
	ReasonNotOnStack     = "stale: frame not on stack"
	ReasonSuspended      = "stale: frame suspended"
	ReasonEmptyStack     = "no frame to receive event"
	ReasonInternalEvent  = "kernel-internal event rejected"
	ReasonCascadeLimit   = "cascade limit reached"
	ReasonDepthLimit     = "stack depth limit reached"
	ReasonUnknownKind    = "unknown activity kind"
	ReasonStoreOnStay    = "store writes discarded: frame did not exit"
	ReasonIntentsOnClose = "intents discarded: frame cancelled"
	ReasonBadOp          = "unknown op treated as stay"
)

// Decision reports the arbiter outcome of a Tick.
type Decision struct {
	Winner Candidate
	Pushed bool
	Frame  FrameID // pushed frame, when Pushed
}

// Effects is everything one Dispatch produced. The kernel performs no I/O:
// the caller sends Emissions, applies Store and Memory writes, and logs
// Diagnostics.
type Effects struct {
	Emissions   []Emission
	Store       []StoreWrite
	Memory      []MemoryWrite
	Decision    *Decision
	Diagnostics []Diagnostic
}

// Intents flattens all emitted intents in emission order.
func (e Effects) Intents() []*civquestv1.AgentIntent {
	var out []*civquestv1.AgentIntent
	for _, em := range e.Emissions {
		out = append(out, em.Intents...)
	}
	return out
}

// Kernel owns one agent's activity stack. It is safe for concurrent use; each
// Dispatch is serialized.
type Kernel struct {
	mu    sync.Mutex
	stack []Frame // the ONLY activity stack, bottom first
	next  FrameID
	reg   *Registry
	prio  PriorityTable
	opts  Options
}

// NewKernel builds a kernel over an injected registry and priority table.
func NewKernel(reg *Registry, prio PriorityTable, opts Options) *Kernel {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxCascade <= 0 {
		opts.MaxCascade = DefaultMaxCascade
	}
	return &Kernel{reg: reg, prio: prio, opts: opts}
}

// Dispatch is the single entry point into the stack. Routing (ADR-036 §3):
//   - Tick: expire the top frame's lease, arbitrate, then step the top frame
//     (unless it already stepped during this Dispatch).
//   - Directive: the top frame.
//   - Receipt, Reflex: the frame named by ID, only if it is the top frame;
//     otherwise dropped as stale.
//   - Cancel: unwinds the whole stack, forwarding Store/Memory writes.
//
// Kernel-generated events (Started, ChildDone, Resumed, Expired) are rejected.
func (k *Kernel) Dispatch(w World, ev Event) Effects {
	k.mu.Lock()
	defer k.mu.Unlock()
	r := &run{k: k, w: w, budget: k.opts.MaxCascade, stepped: map[FrameID]bool{}}
	switch e := ev.(type) {
	case Tick:
		r.tick()
	case Directive:
		if len(k.stack) == 0 {
			r.diag(0, "", ReasonEmptyStack)
			break
		}
		r.stepTop(e)
	case Receipt:
		r.routed(e.Frame, e, IsProgress(e.Status))
	case Reflex:
		r.routed(e.Frame, e, false)
	case Cancel:
		r.cancel(e)
	default:
		r.diag(0, "", ReasonInternalEvent)
	}
	return r.fx
}

// Depth returns the current stack depth.
func (k *Kernel) Depth() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.stack)
}

// DepthInStep returns the stack depth without taking the kernel lock. It may only be called
// from inside Propose/Init/Step (i.e. during a Dispatch on the same goroutine).
func (k *Kernel) DepthInStep() int { return len(k.stack) }

// Top returns a copy of the top frame.
func (k *Kernel) Top() (Frame, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.stack) == 0 {
		return Frame{}, false
	}
	return k.stack[len(k.stack)-1], true
}

// Frames returns a copy of the stack, bottom first.
func (k *Kernel) Frames() []Frame {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]Frame(nil), k.stack...)
}

func (k *Kernel) index(id FrameID) int {
	for i := len(k.stack) - 1; i >= 0; i-- {
		if k.stack[i].ID == id {
			return i
		}
	}
	return -1
}
