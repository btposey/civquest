// Package spatialmem is the agent's pure spatial memory: where it has been,
// which coarse sectors it has explored, and which resource sites it has
// already exhausted. It answers two planning questions without touching the
// world: "how expensive is it to go back there?" (RevisitCost) and "how new is
// that place?" (Novelty).
//
// # ADR-lite: placement and integration
//
// Per ADR-036 this is not a field on AgentState. A Snapshot is a value type
// meant to live in the kernel's Memory (own recent history, survives a frame
// pop) and to be replaced through Step.Memory writes: every mutator returns a
// new Snapshot and never changes its receiver. Maps are copy-on-write
// (cloned per update), so no reference is shared between ticks, and there are
// no mutexes. The package imports nothing from system1 or the proto layer.
//
// Relation to spatial_index.go: SpatialIndex stores persistent, named POIs
// (workstations, containers, resources, hazards) in 16-block buckets for
// "where is X" queries. spatialmem stores anonymous, decaying, agent-relative
// history for "have I been here / is it spent" queries. They are
// complementary and deliberately separate:
//   - A RESOURCE POI that is later found empty is not deleted from the
//     index; the planner calls MarkDepleted at the POI's block position and
//     filters POI candidates with Depleted / DepletedIn.
//   - Visits come from the agent's own telemetry position, never from POIs.
//   - Cells (default 8 blocks) are finer than SpatialIndex buckets (16), and
//     sectors (default 8x8 cells = 64 blocks) are coarser; both are
//     configured by Config, not shared constants.
//
// No activity consumes this yet (cq-dcx.1); PlanActivity (cq-dcx.4) will.
package spatialmem

import (
	"math"
	"sort"
)

// Cell is a coarse grid cell in the XZ plane (Y is ignored for visits).
type Cell struct{ X, Z int32 }

// Sector is a coarse group of cells used for "explored" bookkeeping.
type Sector struct{ X, Z int32 }

// Pos is an exact block position, used for depleted-resource marks.
type Pos struct{ X, Y, Z int32 }

// Kind classifies a depleted resource.
type Kind uint8

const (
	KindTrunk  Kind = iota // felled tree trunk cell
	KindForage             // empty forage site (berries, crops, ...)
	KindOre                // mined-out ore site
)

// Config holds tuning knobs. Zero fields take the DefaultConfig value.
type Config struct {
	CellSize          int32   // blocks per cell edge
	SectorCells       int32   // cells per sector edge
	HalfLifeTicks     float64 // visit weight halves every this many ticks
	ExploredCoverage  float64 // fraction of a sector's cells visited => explored
	DepletedPenalty   float64 // RevisitCost added per live depleted mark in a cell
	ForageRegrowTicks int64   // forage marks expire after this long (0 = never)
	// Trunk and ore marks are permanent.
	PruneBelow float64 // Prune drops cells whose decayed weight is under this
}

// DefaultConfig returns the baseline tuning.
func DefaultConfig() Config {
	return Config{
		CellSize:          8,
		SectorCells:       8,
		HalfLifeTicks:     6000,
		ExploredCoverage:  0.5,
		DepletedPenalty:   1.0,
		ForageRegrowTicks: 24000,
		PruneBelow:        0.01,
	}
}

func (c Config) norm() Config {
	d := DefaultConfig()
	if c.CellSize <= 0 {
		c.CellSize = d.CellSize
	}
	if c.SectorCells <= 0 {
		c.SectorCells = d.SectorCells
	}
	if c.HalfLifeTicks <= 0 {
		c.HalfLifeTicks = d.HalfLifeTicks
	}
	if c.ExploredCoverage <= 0 {
		c.ExploredCoverage = d.ExploredCoverage
	}
	if c.DepletedPenalty <= 0 {
		c.DepletedPenalty = d.DepletedPenalty
	}
	if c.PruneBelow <= 0 {
		c.PruneBelow = d.PruneBelow
	}
	return c
}

// CellStat is the stored visit record of one cell.
type CellStat struct {
	Visits int     // raw lifetime visit count
	Weight float64 // decayed weight as of LastTick
	Last   int64   // tick of last visit
}

// Mark is one depleted-resource record.
type Mark struct {
	Kind Kind
	Tick int64
}

type markKey struct {
	Pos  Pos
	Kind Kind
}

// Snapshot is the immutable spatial memory value. The zero value is an empty
// memory with DefaultConfig. Never mutate fields; use the returned copies.
type Snapshot struct {
	cfg      Config
	cells    map[Cell]CellStat
	explored map[Sector]struct{} // explicit marks (e.g. expedition surveyed)
	depleted map[markKey]int64
}

// New returns an empty Snapshot with the given config.
func New(cfg Config) Snapshot { return Snapshot{cfg: cfg.norm()} }

func (s Snapshot) conf() Config { return s.cfg.norm() }

// floorDiv is floor(a/b) for b > 0.
func floorDiv(a, b int32) int32 {
	q := a / b
	if a%b != 0 && (a < 0) {
		q--
	}
	return q
}

// CellOf maps a world position to its coarse cell.
func (s Snapshot) CellOf(x, z float64) Cell {
	cs := float64(s.conf().CellSize)
	return Cell{int32(math.Floor(x / cs)), int32(math.Floor(z / cs))}
}

// SectorOf returns the sector containing a cell.
func (s Snapshot) SectorOf(c Cell) Sector {
	n := s.conf().SectorCells
	return Sector{floorDiv(c.X, n), floorDiv(c.Z, n)}
}

// CellAt maps an exact block position to its coarse cell.
func (s Snapshot) CellAt(p Pos) Cell {
	cs := s.conf().CellSize
	return Cell{floorDiv(p.X, cs), floorDiv(p.Z, cs)}
}

func decay(w float64, last, now int64, half float64) float64 {
	if now <= last {
		return w
	}
	return w * math.Exp2(-float64(now-last)/half)
}

func cloneCells(m map[Cell]CellStat, extra int) map[Cell]CellStat {
	o := make(map[Cell]CellStat, len(m)+extra)
	for k, v := range m {
		o[k] = v
	}
	return o
}

// Visit records the agent standing in cell at tick, returning a new Snapshot.
func (s Snapshot) Visit(c Cell, tick int64) Snapshot {
	cfg := s.conf()
	n := s
	n.cfg = cfg
	n.cells = cloneCells(s.cells, 1)
	st := n.cells[c]
	st.Weight = decay(st.Weight, st.Last, tick, cfg.HalfLifeTicks) + 1
	st.Visits++
	if tick > st.Last {
		st.Last = tick
	}
	n.cells[c] = st
	return n
}

// VisitAt is Visit for a world position.
func (s Snapshot) VisitAt(x, z float64, tick int64) Snapshot {
	return s.Visit(s.CellOf(x, z), tick)
}

// Prune drops cells whose decayed weight at now is below Config.PruneBelow.
func (s Snapshot) Prune(now int64) Snapshot {
	cfg := s.conf()
	n := s
	n.cfg = cfg
	n.cells = make(map[Cell]CellStat, len(s.cells))
	for c, st := range s.cells {
		if decay(st.Weight, st.Last, now, cfg.HalfLifeTicks) >= cfg.PruneBelow {
			n.cells[c] = st
		}
	}
	return n
}

// MarkExplored explicitly marks a sector explored (e.g. after a survey).
func (s Snapshot) MarkExplored(sec Sector) Snapshot {
	n := s
	n.cfg = s.conf()
	n.explored = make(map[Sector]struct{}, len(s.explored)+1)
	for k := range s.explored {
		n.explored[k] = struct{}{}
	}
	n.explored[sec] = struct{}{}
	return n
}

// MarkDepleted records the resource at p as exhausted at tick.
func (s Snapshot) MarkDepleted(p Pos, k Kind, tick int64) Snapshot {
	n := s
	n.cfg = s.conf()
	n.depleted = make(map[markKey]int64, len(s.depleted)+1)
	for mk, t := range s.depleted {
		n.depleted[mk] = t
	}
	n.depleted[markKey{p, k}] = tick
	return n
}

// ClearDepleted forgets a depleted mark (e.g. the site was seen to regrow).
func (s Snapshot) ClearDepleted(p Pos, k Kind) Snapshot {
	if _, ok := s.depleted[markKey{p, k}]; !ok {
		return s
	}
	n := s
	n.cfg = s.conf()
	n.depleted = make(map[markKey]int64, len(s.depleted))
	for mk, t := range s.depleted {
		if mk != (markKey{p, k}) {
			n.depleted[mk] = t
		}
	}
	return n
}

func (s Snapshot) live(k Kind, t, now int64) bool {
	if k == KindForage {
		if ttl := s.conf().ForageRegrowTicks; ttl > 0 && now-t >= ttl {
			return false
		}
	}
	return true
}

// Depleted reports whether the resource of kind k at p is currently marked
// exhausted at tick now (forage marks lapse after ForageRegrowTicks).
func (s Snapshot) Depleted(p Pos, k Kind, now int64) bool {
	t, ok := s.depleted[markKey{p, k}]
	return ok && s.live(k, t, now)
}

// DepletedIn counts live depleted marks (all kinds) inside a cell.
func (s Snapshot) DepletedIn(c Cell, now int64) int {
	n := 0
	for mk, t := range s.depleted {
		if s.CellAt(mk.Pos) == c && s.live(mk.Kind, t, now) {
			n++
		}
	}
	return n
}

// Marks returns all depleted marks live at now, ordered deterministically.
func (s Snapshot) Marks(now int64) []struct {
	Pos Pos
	Mark
} {
	type row = struct {
		Pos Pos
		Mark
	}
	var out []row
	for mk, t := range s.depleted {
		if s.live(mk.Kind, t, now) {
			out = append(out, row{mk.Pos, Mark{mk.Kind, t}})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pos.X != b.Pos.X {
			return a.Pos.X < b.Pos.X
		}
		if a.Pos.Y != b.Pos.Y {
			return a.Pos.Y < b.Pos.Y
		}
		if a.Pos.Z != b.Pos.Z {
			return a.Pos.Z < b.Pos.Z
		}
		return a.Kind < b.Kind
	})
	return out
}

// Stat returns the raw stored record for a cell.
func (s Snapshot) Stat(c Cell) (CellStat, bool) { st, ok := s.cells[c]; return st, ok }

// Weight is the decayed visit weight of c at tick now (0 if never visited).
func (s Snapshot) Weight(c Cell, now int64) float64 {
	st, ok := s.cells[c]
	if !ok {
		return 0
	}
	return decay(st.Weight, st.Last, now, s.conf().HalfLifeTicks)
}

// RevisitCost is how unattractive returning to c is at tick now: the decayed
// visit weight plus DepletedPenalty per live depleted mark in the cell.
// It is >= 0, rises with each visit, and falls monotonically with time since
// the last visit. A never-visited, undepleted cell costs 0.
func (s Snapshot) RevisitCost(c Cell, now int64) float64 {
	return s.Weight(c, now) + s.conf().DepletedPenalty*float64(s.DepletedIn(c, now))
}

// Novelty in (0,1]: 1 for a cell never visited, shrinking as RevisitCost
// grows. Cells in explored sectors are scaled by 0.5.
func (s Snapshot) Novelty(c Cell, now int64) float64 {
	v := 1 / (1 + s.RevisitCost(c, now))
	if s.IsExplored(s.SectorOf(c), now) {
		v *= 0.5
	}
	return v
}

// Coverage is the fraction of a sector's cells with non-negligible decayed
// weight at now (visited and not yet pruned-worthy).
func (s Snapshot) Coverage(sec Sector, now int64) float64 {
	cfg := s.conf()
	seen := 0
	for c, st := range s.cells {
		if s.SectorOf(c) == sec && decay(st.Weight, st.Last, now, cfg.HalfLifeTicks) >= cfg.PruneBelow {
			seen++
		}
	}
	return float64(seen) / float64(cfg.SectorCells*cfg.SectorCells)
}

// IsExplored reports an explicit mark or sufficient visit coverage.
func (s Snapshot) IsExplored(sec Sector, now int64) bool {
	if _, ok := s.explored[sec]; ok {
		return true
	}
	return s.Coverage(sec, now) >= s.conf().ExploredCoverage
}

// ExploredSectors lists explicitly marked sectors, ordered.
func (s Snapshot) ExploredSectors() []Sector {
	out := make([]Sector, 0, len(s.explored))
	for k := range s.explored {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Z < out[j].Z
	})
	return out
}

// Len returns the number of tracked cells.
func (s Snapshot) Len() int { return len(s.cells) }
