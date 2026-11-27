package system1

// Planning-layer state homes (ADR-039, bead cq-dcx.4).
//
//   - Memory: the agent's spatial memory (spatialmem.Snapshot: visited cells, depleted trunks and
//     empty sites). It is about the agent's own recent history and must survive any frame pop,
//     so it is Memory (ADR-036 §1), written only through Step.Memory writes (spatialVisit,
//     spatialDepleted) applied by the kernel adapter. Visits are telemetry-sourced: the adapter
//     emits one spatialVisit per tick from the agent position (applied only on a cell change).
//   - Store: the banked plan (planBank), written only by the plan frame's Step.Store on exit
//     (pop/cancel/yield), read by the next plan frame's Init so a sleep/emergency interruption
//     resumes the same plan, committed target and in-flight craft.
//
// Both live in one value-typed field on AgentFSM (like surveyBackoffUntil / escapeLock), not
// on AgentState. Readers get value copies; the snapshot's maps are copy-on-write.

import (
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/plan"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/spatialmem"
)

// spatialPruneAbove bounds the visit grid (design budget): past this many tracked cells the
// decayed-away ones are pruned on the next visit.
const spatialPruneAbove = 4096

// planningMemory is the AgentFSM slot for the planning layer (see file comment).
type planningMemory struct {
	Spatial  spatialmem.Snapshot
	lastCell spatialmem.Cell
	hasCell  bool
	Bank     planBank
}

// spatialVisit (Memory write): the agent stood at (X, Z) at Tick.
type spatialVisit struct {
	X, Z float64
	Tick int64
}

// spatialDepleted (Memory write): the resource at Pos is exhausted (felled trunk, empty site).
type spatialDepleted struct {
	Pos  spatialmem.Pos
	Kind spatialmem.Kind
	Tick int64
}

// planBank (Store write): the plan frame's progress at exit. Done clears the bank.
type planBank struct {
	Plan    plan.Plan
	Target  planTarget
	Latch   craftLatch
	Tick    int64
	Backoff int64 // tree gathering backoff (planLocal.Backoff)
	Done    bool
}

func (b planBank) live(tick int64) bool {
	if b.Done || b.Plan.ID == "" {
		return false
	}
	return b.Plan.Lease.ExpiresTick == 0 || tick < b.Plan.Lease.ExpiresTick
}

// applyPlanningMemory applies one Memory write; it reports whether the write was a planning one.
func (m *planningMemory) applyMemory(wr htpda.MemoryWrite) bool {
	switch v := wr.(type) {
	case spatialVisit:
		c := m.Spatial.CellOf(v.X, v.Z)
		if m.hasCell && c == m.lastCell {
			return true // one visit per cell entry: dwell time is not retreading
		}
		m.Spatial = m.Spatial.Visit(c, v.Tick)
		m.lastCell, m.hasCell = c, true
		if m.Spatial.Len() > spatialPruneAbove {
			m.Spatial = m.Spatial.Prune(v.Tick) // bounded: decayed-away cells are forgotten
		}
		return true
	case spatialDepleted:
		if !m.Spatial.Depleted(v.Pos, v.Kind, v.Tick) {
			m.Spatial = m.Spatial.MarkDepleted(v.Pos, v.Kind, v.Tick)
		}
		return true
	}
	return false
}

// applyStore applies the plan frame's banked progress from a dispatch's Store writes.
func (m *planningMemory) applyStore(writes []htpda.StoreWrite) {
	for _, wr := range writes {
		if b, ok := wr.(planBank); ok {
			if b.Done {
				m.Bank = planBank{}
				continue
			}
			m.Bank = b
		}
	}
}

// SpatialMemory returns a value copy of the agent's spatial memory (tests, diagnostics).
func (fsm *AgentFSM) SpatialMemory() spatialmem.Snapshot {
	if fsm == nil {
		return spatialmem.Snapshot{}
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	return fsm.planning.Spatial
}

// BankedPlan returns the plan banked by the last plan frame that exited (ok=false when none).
func (fsm *AgentFSM) BankedPlan() (plan.Plan, bool) {
	if fsm == nil {
		return plan.Plan{}, false
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	b := fsm.planning.Bank
	return b.Plan, b.Plan.ID != "" && !b.Done
}

// ActivityDepth returns the number of frames on the activity stack.
func (fsm *AgentFSM) ActivityDepth() int {
	if fsm == nil {
		return 0
	}
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if fsm.kernel == nil {
		return 0
	}
	return len(fsm.activities())
}

// spatial returns the spatial memory snapshot visible to this tick's steps.
func (w *fsmWorld) spatial() spatialmem.Snapshot {
	if w == nil || w.fsm == nil {
		return spatialmem.Snapshot{}
	}
	return w.fsm.planning.Spatial
}

// visitWrite is the per-tick telemetry visit (nil when the position is unknown).
func (w *fsmWorld) visitWrite() []htpda.MemoryWrite {
	f := w.Frame()
	if f == nil || f.Status == nil || f.Status.Position == nil {
		return nil
	}
	return []htpda.MemoryWrite{spatialVisit{X: f.Status.Position.X, Z: f.Status.Position.Z, Tick: f.WorldTick}}
}

// spatialPenalty is the revisit cost of the cell containing block (x, z), scaled by weight.
func spatialPenalty(m spatialmem.Snapshot, x, z float64, tick int64, weight float64) float64 {
	return weight * m.RevisitCost(m.CellOf(x, z), tick)
}

// Explore-target steering (tag: design-budget).
const (
	// noveltyTieMargin: a heading must be this much more novel than the default to win.
	noveltyTieMargin = 0.05
)

// forageHeadingOffsets: the forage wander's default +0.9 rad turn first, then alternatives.
var forageHeadingOffsets = []float64{0.9, 0, -0.9, 1.8, -1.8, math.Pi}

// scoutPivotOffsets: the frontier scout's default +1.2 rad pivot first, then alternatives.
var scoutPivotOffsets = []float64{1.2, 2.4, -1.2, 3.6, -2.4}

// exploreNoveltyWeight: survey-bearing score points per unit novelty of the leg's cells (the
// feature bonuses it competes with are 10-45 points).
const exploreNoveltyWeight = 30.0

// noveltyHeading returns base+offset for the offset whose leg (samples at half and full dist)
// crosses the most novel cells; the first offset wins ties within noveltyTieMargin, so an empty
// memory reproduces the default heading exactly.
func noveltyHeading(m spatialmem.Snapshot, x, z, base, dist float64, tick int64, offsets []float64) float64 {
	best, bestScore := base+offsets[0], -1.0
	for i, off := range offsets {
		h := base + off
		s := 0.0
		for _, f := range [...]float64{0.5, 1.0} {
			s += spatialNovelty(m, x+math.Cos(h)*dist*f, z+math.Sin(h)*dist*f, tick)
		}
		s /= 2
		if i == 0 {
			bestScore = s
			continue
		}
		if s > bestScore+noveltyTieMargin {
			best, bestScore = h, s
		}
	}
	return best
}

// spatialNovelty is the novelty in (0,1] of the cell containing (x, z): 1/(1+RevisitCost).
// (spatialmem.Novelty additionally halves cells in explored sectors, but its coverage scan is
// O(cells); target steering runs every leg, so it uses the O(1) cell cost.)
func spatialNovelty(m spatialmem.Snapshot, x, z float64, tick int64) float64 {
	return 1 / (1 + m.RevisitCost(m.CellOf(x, z), tick))
}
