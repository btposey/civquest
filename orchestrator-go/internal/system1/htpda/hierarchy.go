package htpda

// Goal-driven hierarchy (ADR-040): stack depth is level of abstraction. A frame pushes only
// work more concrete than itself, a parent supervises its child and may recall it, and a child
// that ends having done nothing is not launched again at once.

import "reflect"

// Level places an activity in the hierarchy; a child's level exceeds its parent's.
type Level uint8

const (
	LevelUnset     Level = iota // not yet classified: no shape checks
	LevelGoal                   // the base frame: chooses objectives
	LevelObjective              // pursues one purpose by choosing tasks
	LevelTask                   // sequences steps to one concrete result
	LevelStep                   // one physical job with receipts
	LevelInterrupt              // may cover any frame, then returns
)

// Leveled is implemented by activities that declare their level.
type Leveled interface{ Level() Level }

// LevelOf returns act's declared level, LevelUnset when it declares none.
func LevelOf(act Activity) Level {
	if l, ok := act.(Leveled); ok {
		return l.Level()
	}
	return LevelUnset
}

// Verdict is a supervising parent's decision about the work above it.
type Verdict struct {
	Park   bool // set the subtree aside, intact, and tell the parent
	Reason string
	Token  any // handed back in Parked, typically what the parent wants to do instead
}

// Supervisor is implemented by activities that watch their child while it works. Supervise is
// pure; it is asked before each Tick, base frame first.
type Supervisor interface {
	Supervise(w World, local any, child Kind) Verdict
}

// CriticalSection is implemented by activities that must briefly not be set aside (a craft
// awaiting its inventory change). Interrupts still cover them.
type CriticalSection interface {
	Critical(w World, local any) bool
}

// Parked is the ChildDone Result a parent receives for work it set aside. Frames is the whole
// subtree, bottom first, with its locals intact; the parent keeps it in its own Local and
// returns it with OpResume to carry on.
type Parked struct {
	Child  Kind
	Reason string
	Token  any
	Frames []Frame
}

// Shape and back-off diagnostics.
const (
	ReasonBaseNotGoal   = "shape: base frame is not the goal"
	ReasonSideways      = "shape: pushed over a frame without being its child"
	ReasonNotDeeper     = "shape: child is not more concrete than its parent"
	ReasonDuplicateKind = "shape: kind already on the stack"
	ReasonNoProgress    = "back-off: ended without progress moments ago"
)

// shapeViolation names the hierarchy rule that pushing act would break ("" for none). parent is
// the pushing frame's ID, 0 for an arbiter push; replacing skips the frame being swapped out.
func (r *run) shapeViolation(act Activity, parent FrameID) string {
	level := LevelOf(act)
	if level == LevelUnset {
		return ""
	}
	for _, f := range r.k.stack {
		if f.Kind() == act.Kind() {
			return ReasonDuplicateKind
		}
	}
	if level == LevelInterrupt {
		return ""
	}
	if len(r.k.stack) == 0 {
		if level != LevelGoal {
			return ReasonBaseNotGoal
		}
		return ""
	}
	under := r.k.stack[len(r.k.stack)-1]
	underLevel := LevelOf(under.Act)
	switch {
	case parent == 0 || parent != under.ID:
		return ReasonSideways
	case underLevel != LevelUnset && underLevel != LevelInterrupt && level <= underLevel:
		return ReasonNotDeeper
	}
	return ""
}

// checkShape records a violation and reports whether the push must be refused.
func (r *run) checkShape(act Activity, parent FrameID) (refuse string) {
	reason := r.shapeViolation(act, parent)
	if reason == "" {
		return ""
	}
	d := Diagnostic{Frame: parent, Kind: act.Kind(), Reason: reason}
	if t := r.top(); t != nil {
		d.Under = t.Kind()
	}
	r.fx.Diagnostics = append(r.fx.Diagnostics, d)
	if r.k.opts.EnforceShape {
		return reason
	}
	return ""
}

// protected reports whether the frames from index i upwards may not be set aside.
func (r *run) protected(i int) bool {
	for _, f := range r.k.stack[i:] {
		if LevelOf(f.Act) == LevelInterrupt {
			return true
		}
		if c, ok := f.Act.(CriticalSection); ok && c.Critical(r.w, f.Local) {
			return true
		}
	}
	return false
}

// supervise asks each frame, base first, about the work above it and parks the first subtree a
// parent wants set aside.
func (r *run) supervise() {
	for i := 0; i < len(r.k.stack)-1; i++ {
		f := r.k.stack[i]
		s, ok := f.Act.(Supervisor)
		if !ok || r.protected(i+1) {
			continue
		}
		if v := s.Supervise(r.w, f.Local, r.k.stack[i+1].Kind()); v.Park {
			r.park(i, v)
			return
		}
	}
}

// park detaches every frame above index i, without stepping them, and tells frame i.
func (r *run) park(i int, v Verdict) {
	frames := append([]Frame(nil), r.k.stack[i+1:]...)
	r.k.stack = r.k.stack[:i+1]
	for j := len(frames) - 1; j >= 0; j-- {
		r.life(LifePark, frames[j], "", nil)
	}
	child := frames[0]
	r.stepTop(ChildDone{Child: child.Kind(), ID: child.ID, Result: Parked{Child: child.Kind(), Reason: v.Reason, Token: v.Token, Frames: frames}})
}

// resume restores a parked subtree over parent and hands its top frame the result of the work
// that was done in the meantime.
func (r *run) resume(parent Frame, s Step) {
	if len(s.Resume) == 0 || len(r.k.stack)+len(s.Resume) > r.k.opts.MaxDepth {
		r.diag(parent.ID, parent.Kind(), ReasonDepthLimit)
		return
	}
	for i, f := range s.Resume {
		if i == 0 {
			f.Parent = parent.ID
		}
		r.k.stack = append(r.k.stack, f)
		r.life(LifeResume, f, "", nil)
	}
	r.stepTop(ChildDone{Child: s.Child, Result: s.Result})
}

type cooledKey struct {
	parent FrameID
	kind   Kind
}

type cooled struct {
	args  any
	until int64
}

// coolIfIdle remembers a child that ended without progress.
func (r *run) coolIfIdle(f Frame) {
	if r.k.opts.NoProgressBackoff <= 0 || f.progressed || f.Parent == 0 || LevelOf(f.Act) == LevelInterrupt {
		return
	}
	if r.k.cooled == nil {
		r.k.cooled = map[cooledKey]cooled{}
	}
	r.k.cooled[cooledKey{f.Parent, f.Kind()}] = cooled{args: f.args, until: r.w.Tick() + r.k.opts.NoProgressBackoff}
}

// backedOff reports whether parent launching kind with args again must wait.
func (r *run) backedOff(parent FrameID, kind Kind, args any) bool {
	c, ok := r.k.cooled[cooledKey{parent, kind}]
	return ok && r.w.Tick() < c.until && reflect.DeepEqual(c.args, args)
}

// forget drops the back-off entries of a frame that left the stack.
func (r *run) forget(parent FrameID) {
	for k := range r.k.cooled {
		if k.parent == parent {
			delete(r.k.cooled, k)
		}
	}
}
