package system1

// Rest (ADR-041 section 4, cq-3sa.5; user, 2026-11-27): resting is realistic and wanted. An
// activity that means to wait declares it in its frame Local, with why, afresh on every step:
// the declaration lasts exactly as long as the conditions the owner checks. The watchdog takes
// the top frame's declaration for a meant wait and nothing else; a quiet frame that declares
// nothing is still reported. Each change is logged and noted for play test records.

import (
	"fmt"
	"log"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// EventRest: an activity began or ended a declared rest.
const EventRest = "REST"

// restingLocal is a frame Local that can declare a rest.
type restingLocal interface {
	// rest says why the frame is resting, or "" when it is not.
	rest() string
}

func restOf(local any) string {
	if r, ok := local.(restingLocal); ok {
		return r.rest()
	}
	return ""
}

// declaredRest is the rest declared by the frame on top of the stack. Parked work cannot speak
// for what runs over it.
func (fsm *AgentFSM) declaredRest() (htpda.Kind, string) {
	top, ok := fsm.kernel.Top()
	if !ok {
		return "", ""
	}
	return top.Kind(), restOf(top.Local)
}

// noteRest records the beginning and end of a rest, for diagnostics.
func (fsm *AgentFSM) noteRest(w *fsmWorld, kind htpda.Kind, why string) {
	was := fsm.watch.resting
	now := declaredWait{}
	if why != "" {
		now = declaredWait{activity: kind, why: why, since: w.tick}
	}
	if was.activity == now.activity && was.why == now.why {
		return
	}
	fsm.watch.resting = now
	note := func(summary string, d map[string]any) {
		log.Printf("[civquest-agent] %s %s", w.agentID(), summary)
		if fsm.eventSink != nil {
			fsm.eventSink(w.tick, EventRest, summary, d)
		}
	}
	if was.why != "" {
		note(fmt.Sprintf("%s: rest over after %d ticks (%s)", was.activity, w.tick-was.since, was.why),
			map[string]any{"activity": string(was.activity), "why": was.why, "since_tick": was.since, "resting": false})
	}
	if now.why != "" {
		note(fmt.Sprintf("%s: resting: %s", kind, why),
			map[string]any{"activity": string(kind), "why": why, "since_tick": w.tick, "resting": true})
	}
}
