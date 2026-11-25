package system1

// Activity defines a formal single-owner behavior unit managed on AgentFSM's HTPDA activity stack.
//
// Concurrency contract: All Activity lifecycle hooks (OnStart, OnTick, OnSuspend, OnResume, OnExit)
// are invoked while AgentFSM.mu is held (matching StateHandler). Implementations MUST NOT re-enter
// public locking AgentFSM methods from inside these callbacks.
type Activity interface {
	ID() string
	Kind() string
	OnStart(ctx *FSMContext)
	OnTick(ctx *FSMContext) ([]FSMIntent, bool) // emitted intents, done (true => pop & OnExit)
	OnSuspend(ctx *FSMContext, reason string)
	OnResume(ctx *FSMContext)
	OnExit(ctx *FSMContext)
}

// PushActivity suspends any current top activity (if not already suspended), pushes act onto the
// HTPDA activity stack, clears the suspended flag, and invokes act.OnStart(ctx).
func (fsm *AgentFSM) PushActivity(act Activity, ctx *FSMContext) {
	if fsm == nil || act == nil {
		return
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.pushActivityLocked(act, ctx)
}

func (fsm *AgentFSM) pushActivityLocked(act Activity, ctx *FSMContext) {
	if fsm == nil || act == nil {
		return
	}
	if len(fsm.activityStack) > 0 {
		fsm.suspendTopActivityLocked(ctx, "preempted by activity: "+act.Kind())
	}
	fsm.activityStack = append(fsm.activityStack, act)
	fsm.activitySuspended = false
	act.OnStart(ctx)
}

// PopActivity removes the top activity from the HTPDA activity stack, invokes its OnExit(ctx),
// and resumes any remaining parent activity underneath via OnResume(ctx).
func (fsm *AgentFSM) PopActivity(ctx *FSMContext) (Activity, bool) {
	if fsm == nil {
		return nil, false
	}
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	return fsm.popActivityLocked(ctx)
}

func (fsm *AgentFSM) popActivityLocked(ctx *FSMContext) (Activity, bool) {
	n := len(fsm.activityStack)
	if n == 0 {
		return nil, false
	}
	popped := fsm.activityStack[n-1]
	fsm.activityStack[n-1] = nil
	fsm.activityStack = fsm.activityStack[:n-1]
	fsm.activitySuspended = false
	if popped != nil {
		popped.OnExit(ctx)
	}
	if len(fsm.activityStack) > 0 {
		if parent := fsm.activityStack[len(fsm.activityStack)-1]; parent != nil {
			parent.OnResume(ctx)
		}
	}
	return popped, true
}

// ActiveActivity returns the topmost Activity on the HTPDA activity stack, or nil if empty.
func (fsm *AgentFSM) ActiveActivity() Activity {
	if fsm == nil {
		return nil
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if len(fsm.activityStack) == 0 {
		return nil
	}
	return fsm.activityStack[len(fsm.activityStack)-1]
}

// ActivityDepth returns the number of stacked activities on the HTPDA activity stack.
func (fsm *AgentFSM) ActivityDepth() int {
	if fsm == nil {
		return 0
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return len(fsm.activityStack)
}

func (fsm *AgentFSM) suspendTopActivityLocked(ctx *FSMContext, reason string) {
	if len(fsm.activityStack) == 0 || fsm.activitySuspended {
		return
	}
	fsm.activitySuspended = true
	if top := fsm.activityStack[len(fsm.activityStack)-1]; top != nil {
		top.OnSuspend(ctx, reason)
	}
}

func (fsm *AgentFSM) resumeTopActivityLocked(ctx *FSMContext) {
	if len(fsm.activityStack) == 0 || !fsm.activitySuspended {
		return
	}
	fsm.activitySuspended = false
	if top := fsm.activityStack[len(fsm.activityStack)-1]; top != nil {
		top.OnResume(ctx)
	}
}

func (fsm *AgentFSM) clearActivitiesLocked(ctx *FSMContext) {
	for len(fsm.activityStack) > 0 {
		idx := len(fsm.activityStack) - 1
		act := fsm.activityStack[idx]
		fsm.activityStack[idx] = nil
		fsm.activityStack = fsm.activityStack[:idx]
		if act != nil {
			act.OnExit(ctx)
		}
	}
	fsm.activityStack = nil
	fsm.activitySuspended = false
}
