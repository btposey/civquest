package system1

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

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

// ScopedActivity extends Activity with scoped IntentReceipt batch ownership and unstuck-recovery ownership.
type ScopedActivity interface {
	Activity
	ReceiptScope() *ActivityReceiptScope
	OwnsStuckRecovery() bool
}

// ActivityReceiptScope anchors IntentReceipt batch ownership to an Activity's start or resume boundary
// so stale receipts from preempted or superseded activities cannot advance or abort the current activity.
type ActivityReceiptScope struct {
	ActivityID          string
	StartReceiptBatchID int64
	IssuedBatchCount    int
	LastHandledBatchID  int64
}

// Anchor sets StartReceiptBatchID from lastReceipt (0 if nil) and resets issued/handled counters.
func (s *ActivityReceiptScope) Anchor(lastReceipt *civquestv1.IntentReceipt) {
	if s == nil {
		return
	}
	s.StartReceiptBatchID = lastReceipt.GetBatchId()
	s.IssuedBatchCount = 0
	s.LastHandledBatchID = 0
}

// RecordEmit increments IssuedBatchCount when the owning activity emits a motor intent.
func (s *ActivityReceiptScope) RecordEmit() {
	if s == nil {
		return
	}
	s.IssuedBatchCount++
}

// Accept returns true only when receipt belongs to a fresh batch emitted after the last Anchor
// and has not already been handled; records LastHandledBatchID upon acceptance.
func (s *ActivityReceiptScope) Accept(receipt *civquestv1.IntentReceipt) bool {
	if !s.PeekValid(receipt) {
		return false
	}
	s.LastHandledBatchID = receipt.GetBatchId()
	return true
}

// PeekValid evaluates the same scoped batch predicate as Accept without mutating LastHandledBatchID.
func (s *ActivityReceiptScope) PeekValid(receipt *civquestv1.IntentReceipt) bool {
	if s == nil || receipt == nil || s.IssuedBatchCount <= 0 {
		return false
	}
	batchID := receipt.GetBatchId()
	return batchID > s.StartReceiptBatchID && batchID != s.LastHandledBatchID
}

func syncActivityOwnership(act Activity, ctx *FSMContext) {
	if ctx == nil || ctx.State == nil {
		return
	}
	if act == nil {
		ctx.State.ClearActiveActivityOwnership()
		return
	}
	ownsStuck := false
	if sa, ok := act.(ScopedActivity); ok {
		ownsStuck = sa.OwnsStuckRecovery()
	}
	ctx.State.SetActiveActivityOwnership(act.Kind(), ownsStuck)
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
	syncActivityOwnership(act, ctx)
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
			syncActivityOwnership(parent, ctx)
			return popped, true
		}
	}
	syncActivityOwnership(nil, ctx)
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
	syncActivityOwnership(nil, ctx)
}

func (fsm *AgentFSM) resumeTopActivityLocked(ctx *FSMContext) {
	if len(fsm.activityStack) == 0 || !fsm.activitySuspended {
		return
	}
	fsm.activitySuspended = false
	if top := fsm.activityStack[len(fsm.activityStack)-1]; top != nil {
		top.OnResume(ctx)
		syncActivityOwnership(top, ctx)
		return
	}
	syncActivityOwnership(nil, ctx)
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
	syncActivityOwnership(nil, ctx)
}
