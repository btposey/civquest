package system1

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// workBackoffTicks keeps an activity that found nothing to do off the stack for a while. Pushed
// again at once it popped again every tick, and each push covered the frame below before that
// frame's stuck receipt arrived (cq-6l3).
const workBackoffTicks = int64(200)

// workBackoff is the Memory write of an activity that popped for want of work.
type workBackoff struct {
	Kind  htpda.Kind
	Until int64
}

func (w *fsmWorld) backedOff(kind htpda.Kind) bool {
	return w.fsm != nil && w.Tick() < w.fsm.workBackoffUntil[kind]
}

// popNoWork pops the stepping frame, with no intents, and backs its kind off.
func popNoWork(lw *fsmWorld, kind htpda.Kind) htpda.Step {
	st := popFrame(lw, kind, []*civquestv1.AgentIntent(nil), false)
	st.Memory = []htpda.MemoryWrite{workBackoff{Kind: kind, Until: lw.Tick() + workBackoffTicks}}
	return st
}
