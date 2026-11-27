package htpda

// run is the per-Dispatch cascade state. All stack mutation happens here.
type run struct {
	k       *Kernel
	w       World
	fx      Effects
	budget  int
	stepped map[FrameID]bool
	// replacing is the kind of the frame an OpReplace is swapping out (consumed by push).
	replacing Kind
}

func (r *run) life(op string, f Frame, from Kind, result any) {
	r.fx.Lifecycle = append(r.fx.Lifecycle, FrameEvent{Op: op, Frame: f.ID, Kind: f.Kind(), From: from, Parent: f.Parent, Depth: len(r.k.stack), Result: result})
}

func (r *run) diag(id FrameID, kind Kind, reason string) {
	r.fx.Diagnostics = append(r.fx.Diagnostics, Diagnostic{Frame: id, Kind: kind, Reason: reason})
}

func (r *run) top() *Frame {
	if len(r.k.stack) == 0 {
		return nil
	}
	return &r.k.stack[len(r.k.stack)-1]
}

func (r *run) tick() {
	now := r.w.Tick()
	for t := r.top(); t != nil && t.Lease.Expired(now) && r.budget > 0; t = r.top() {
		r.stepTop(Expired{Deadline: t.Lease.DeadlineTick})
	}
	r.supervise()
	if c, ok := Arbitrate(r.w, r.k.reg, r.k.prio); ok {
		d := &Decision{Winner: c}
		r.fx.Decision = d
		if Preempts(c, r.top()) {
			if act, reason := r.resolve(c.Bid.Kind, true); reason != "" {
				r.diag(0, c.Bid.Kind, reason)
			} else if r.checkShape(act, 0) == "" {
				d.Pushed = true
				d.Frame = r.push(act, c.Bid.Args, c.Bid.Band, 0)
			}
		}
	}
	if t := r.top(); t != nil && !r.stepped[t.ID] {
		r.stepTop(Tick{})
	}
}

func (r *run) routed(id FrameID, ev Event, progress bool) {
	i := r.k.index(id)
	switch {
	case i < 0:
		r.diag(id, "", ReasonNotOnStack)
	case i != len(r.k.stack)-1:
		r.diag(id, r.k.stack[i].Kind(), ReasonSuspended)
	default:
		t := r.top()
		if l := t.Lease; progress && l.RenewOnProgress && l.DeadlineTick > 0 {
			now := r.w.Tick()
			t.Lease.DeadlineTick = now + (l.DeadlineTick - l.StartTick)
			t.Lease.StartTick = now
		}
		if progress {
			t.progressed = true
		}
		r.stepTop(ev)
	}
}

// cancel unwinds every frame from the top. Each frame sees Cancel once; its
// Store and Memory writes are forwarded, its intents and Op are discarded.
func (r *run) cancel(ev Cancel) {
	for t := r.top(); t != nil; t = r.top() {
		f := *t
		s := f.Act.Step(r.w, f.Local, ev)
		if len(s.Intents) > 0 {
			r.diag(f.ID, f.Kind(), ReasonIntentsOnClose)
		}
		r.fx.Store = append(r.fx.Store, s.Store...)
		r.fx.Memory = append(r.fx.Memory, s.Memory...)
		r.k.stack = r.k.stack[:len(r.k.stack)-1]
		r.forget(f.ID)
		r.life(LifeCancel, f, "", nil)
	}
}

// resolve finds the activity for a push. grow is true when the push adds a
// frame (OpPush, arbiter) and false when it reuses a slot (OpReplace).
func (r *run) resolve(kind Kind, grow bool) (Activity, string) {
	act, ok := r.k.reg.Get(kind)
	if !ok {
		return nil, ReasonUnknownKind
	}
	if grow && len(r.k.stack) >= r.k.opts.MaxDepth {
		return nil, ReasonDepthLimit
	}
	return act, ""
}

// push initialises a frame, pushes it and delivers Started.
func (r *run) push(act Activity, args any, band Band, parent FrameID) FrameID {
	local, lease := act.Init(r.w, args)
	if lease.StartTick == 0 {
		lease.StartTick = r.w.Tick()
	}
	r.k.next++
	id := r.k.next
	nf := Frame{ID: id, Act: act, Local: local, Scope: act.Scope(), Lease: lease, Band: band, Parent: parent, args: args}
	r.k.stack = append(r.k.stack, nf)
	if r.replacing != "" {
		r.life(LifeReplace, nf, r.replacing, nil)
		r.replacing = ""
	} else {
		r.life(LifePush, nf, "", nil)
	}
	r.stepTop(Started{})
	return id
}

// stepTop delivers ev to the top frame and applies the resulting Step.
func (r *run) stepTop(ev Event) {
	t := r.top()
	if t == nil {
		return
	}
	if r.budget <= 0 {
		r.diag(t.ID, t.Kind(), ReasonCascadeLimit)
		return
	}
	r.budget--
	f := *t
	r.stepped[f.ID] = true
	s := f.Act.Step(r.w, f.Local, ev)
	if len(s.Intents) > 0 {
		r.fx.Emissions = append(r.fx.Emissions, Emission{Frame: f.ID, Kind: f.Kind(), Intents: s.Intents})
		t.progressed, f.progressed = true, true
	}
	r.fx.Memory = append(r.fx.Memory, s.Memory...)
	if s.Lease != nil {
		t.Lease = *s.Lease
	}
	if exp, ok := ev.(Expired); ok && s.Op != OpReplace {
		// An expired lease always ends the frame.
		s.Op = OpPop
		if s.Result == nil {
			s.Result = Timeout{Kind: f.Kind(), Deadline: exp.Deadline}
		}
	}
	switch s.Op {
	case OpStay:
		t.Local = s.Next
		r.discardStore(f, s)
	case OpPush:
		t.Local = s.Next
		r.discardStore(f, s)
		act, reason := r.resolve(s.Child, true)
		if reason == "" && r.backedOff(f.ID, s.Child, s.Args) {
			reason = ReasonNoProgress
		}
		if reason == "" {
			reason = r.checkShape(act, f.ID)
		}
		if reason != "" {
			r.diag(f.ID, s.Child, reason)
			r.stepTop(ChildDone{Child: s.Child, Result: Rejected{Kind: s.Child, Reason: reason}})
			return
		}
		r.push(act, s.Args, f.Band, f.ID)
	case OpReplace:
		act, reason := r.resolve(s.Child, false)
		r.pop(s.Store)
		if reason != "" {
			r.life(LifePop, f, "", Rejected{Kind: s.Child, Reason: reason})
			r.diag(f.ID, s.Child, reason)
			r.notify(f, Rejected{Kind: s.Child, Reason: reason})
			return
		}
		// The replacement takes the same slot: its shape is checked against the frame below.
		if reason := r.checkShape(act, f.Parent); reason != "" {
			r.life(LifePop, f, "", Rejected{Kind: s.Child, Reason: reason})
			r.notify(f, Rejected{Kind: s.Child, Reason: reason})
			return
		}
		r.replacing = f.Kind()
		r.push(act, s.Args, f.Band, f.Parent)
	case OpPop:
		r.pop(s.Store)
		if _, ok := ev.(Expired); ok {
			r.life(LifeExpire, f, "", s.Result)
		} else {
			r.life(LifePop, f, "", s.Result)
		}
		r.coolIfIdle(f)
		if p := r.top(); p != nil && f.progressed {
			p.progressed = true
		}
		r.notify(f, s.Result)
	default:
		r.diag(f.ID, f.Kind(), ReasonBadOp)
		t.Local = s.Next
	}
}

func (r *run) discardStore(f Frame, s Step) {
	if len(s.Store) > 0 {
		r.diag(f.ID, f.Kind(), ReasonStoreOnStay)
	}
}

func (r *run) pop(store []StoreWrite) {
	r.forget(r.k.stack[len(r.k.stack)-1].ID)
	r.k.stack = r.k.stack[:len(r.k.stack)-1]
	r.fx.Store = append(r.fx.Store, store...)
}

// notify delivers a popped frame's result to the new top: ChildDone if it is
// the frame's parent, Resumed if the popped frame was an arbiter preemptor.
func (r *run) notify(popped Frame, result any) {
	t := r.top()
	if t == nil {
		return
	}
	if popped.Parent != 0 && popped.Parent == t.ID {
		r.stepTop(ChildDone{Child: popped.Kind(), ID: popped.ID, Result: result})
		return
	}
	r.stepTop(Resumed{From: popped.Kind(), ID: popped.ID, Result: result})
}
