package system1

// Boxed-in agents (cq-6l3.23). A recovery that gives up leaves the agent where it was; if that
// keeps happening on one spot, sidestepping is not working and the agent digs out instead.

import (
	"math"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

const (
	trapRadius      = 2.5
	trapGiveUps     = 2
	trapMemoryTicks = int64(2400)
)

// gaveUpHere is the Memory write of a recovery that gave up with the agent at Pos.
type gaveUpHere struct {
	X, Z float64
	Tick int64
}

func spotGiveUp(pos *civquestv1.Vec3, tick int64) []htpda.MemoryWrite {
	if pos == nil {
		return nil
	}
	return []htpda.MemoryWrite{gaveUpHere{X: pos.X, Z: pos.Z, Tick: tick}}
}

// noteSpot remembers a give-up, dropping those too old to matter.
func (fsm *AgentFSM) noteSpot(g gaveUpHere) {
	kept := fsm.spots[:0]
	for _, s := range fsm.spots {
		if g.Tick >= s.Tick && g.Tick-s.Tick <= trapMemoryTicks {
			kept = append(kept, s)
		}
	}
	fsm.spots = append(kept, g)
}

// boxedIn reports whether recoveries have kept giving up around pos.
func (fsm *AgentFSM) boxedIn(pos *civquestv1.Vec3, tick int64) bool {
	if pos == nil {
		return false
	}
	n := 0
	for _, s := range fsm.spots {
		if tick >= s.Tick && tick-s.Tick <= trapMemoryTicks && math.Hypot(pos.X-s.X, pos.Z-s.Z) <= trapRadius {
			n++
		}
	}
	return n >= trapGiveUps
}
