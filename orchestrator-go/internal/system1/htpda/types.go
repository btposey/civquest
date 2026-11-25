// Package htpda is the frame-local hierarchical timed pushdown automaton
// kernel described in ADR-036 §2–§3: one activity stack per agent, frames that
// own their activity's locals, pure Step functions, a single Dispatch entry
// point, and a generic bid arbiter whose priorities are configuration data.
//
// The package deliberately does not import system1: activities see the world
// only through the read-only World interface defined here, so a Step cannot
// reach AgentState (compile-enforced). The system1 wiring adapts its frozen
// FactGraph and state view to World.
package htpda

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

// FrameID identifies one frame. Per-kernel monotonic, starting at 1; never
// reused within a kernel's lifetime. 0 means "no frame".
type FrameID uint64

// Kind names an activity ("forage", "shelter", "task", "navigate", ...).
type Kind string

// Band is the coarse preemption tier. A bid preempts the top frame only when
// its Band is strictly greater than the top frame's Band.
type Band uint8

const (
	BandIdle      Band = iota // empty-stack routines (exploration, patrol)
	BandRoutine               // circadian duty/hobby/social work
	BandCommitted             // directives, tasks, shelter, expedition
	BandEmergency             // threat response, critical hunger
	BandDead                  // terminal
)

// Lease bounds a frame's lifetime. StartTick is the start of the current lease
// window (set by the kernel on push when zero). DeadlineTick 0 = no deadline.
// When RenewOnProgress is set, a delivered progress Receipt slides the window:
// the deadline moves to now + (DeadlineTick - StartTick).
type Lease struct {
	StartTick       int64
	DeadlineTick    int64
	RenewOnProgress bool
}

// Expired reports whether the lease deadline has been reached at tick.
func (l Lease) Expired(tick int64) bool {
	return l.DeadlineTick > 0 && tick >= l.DeadlineTick
}

// Scope declares what a frame may emit / react to.
type Scope uint8

const (
	ScopeMotor Scope = 1 << iota // may emit motor intents; receives Receipt events
	ScopeChat
)

// Frame is one entry of the activity stack. Local holds the activity's typed
// locals; it must be a value type (or treated as immutable) and only Act
// type-asserts it. Band is inherited by children pushed via OpPush/OpReplace.
type Frame struct {
	ID     FrameID
	Act    Activity
	Local  any
	Scope  Scope
	Lease  Lease
	Band   Band
	Parent FrameID // pushing frame; 0 for arbiter-pushed (root or preemptor)
}

// Kind returns the frame's activity kind.
func (f Frame) Kind() Kind { return f.Act.Kind() }

// Bid is an activity's proposal to be pushed. Utility <= 0 abstains. The
// arbiter's score is PriorityTable weight(stage, kind) * Utility, so numeric
// priorities live in configuration and Utility is the activity's own scale.
type Bid struct {
	Kind    Kind // filled by the arbiter from Activity.Kind()
	Band    Band
	Utility float64
	Args    any // passed to Init
	Reason  string
}

// Op is the stack operation a Step requests for its own frame.
type Op uint8

const (
	OpStay    Op = iota // keep the frame; Next becomes its locals
	OpPop               // pop self; Result goes to the parent as ChildDone (or Resumed)
	OpPush              // push Child over self; self suspended with Next as its locals
	OpReplace           // pop self and push Child in the same slot (same Parent, no notify)
)

func (o Op) String() string {
	switch o {
	case OpStay:
		return "stay"
	case OpPop:
		return "pop"
	case OpPush:
		return "push"
	case OpReplace:
		return "replace"
	}
	return "op?"
}

// StoreWrite is an opaque durable-store write, applied by the wiring layer.
// The kernel only forwards writes from exiting frames (OpPop, OpReplace,
// expiry, Cancel).
type StoreWrite interface{}

// MemoryWrite is an opaque cross-activity Memory write, forwarded after every
// step. Writes take effect for the next World, not later steps of the same
// Dispatch.
type MemoryWrite interface{}

// Step is the pure result of Activity.Step.
type Step struct {
	Intents []*civquestv1.AgentIntent // reported in Effects tagged with this frame's ID
	Next    any                       // this frame's locals after the step (OpStay/OpPush)
	Op      Op
	Child   Kind // OpPush / OpReplace
	Args    any  // OpPush / OpReplace, passed to the child's Init
	Result  any  // OpPop
	Store   []StoreWrite
	Memory  []MemoryWrite
	Lease   *Lease // optional renewal / new deadline for this frame
}

// Activity is stateless behaviour; one value per Kind is registered.
//
// Contract: Propose, Init and Step are PURE functions of their inputs. They
// must not perform I/O, mutate w, or mutate anything reachable from local
// (return a new value in Step.Next instead). Hooks must not call back into
// the Kernel (Dispatch holds the kernel lock).
type Activity interface {
	Kind() Kind
	Scope() Scope
	Propose(w World) Bid
	Init(w World, args any) (local any, lease Lease)
	Step(w World, local any, ev Event) Step
}

// LocalAs type-asserts a frame local, returning the zero T when local is nil
// or of another type.
func LocalAs[T any](local any) T {
	v, _ := local.(T)
	return v
}
