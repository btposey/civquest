package system1

// Remembered dangers (cq-gey.2). A mob that hurts an agent, or one that stays where it lives
// (a Drowned in its water), marks its spot for a game day. The agent does not choose targets
// there, walks round it, and having been hit keeps going until it is clear. Dangers are shared
// with the clan (agent/danger_sync.go).

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// DangerMemoryTicks is how long a danger stays registered after it was last met: one game
	// day (the user's minimum).
	DangerMemoryTicks = int64(24000)
	// DESIGN BUDGETS: a Drowned throws its trident about this far.
	dangerRadius = 20.0
	// Sightings of one kind this close are one danger.
	dangerMergeRadius = 16.0
	// The ground right round home is never off limits: an agent must be able to get home.
	dangerHomeExempt = 8.0
	// A sighting refreshes a registered danger (and is shared again) at most this often.
	dangerRefreshTicks = int64(1200)
	// A detour passes this far outside the danger, its waypoint snapped to a grid so that
	// re-sending a walk does not move it a little each time.
	detourMargin = 6.0
	detourGrid   = 4.0
)

// Danger is one registered mob danger.
type Danger struct {
	Kind    string // mob type without namespace, e.g. "drowned"
	X, Y, Z float64
	Seen    int64
}

// lingers reports mobs that stay about the place they were seen.
func lingers(kind string) bool {
	for _, k := range []string{"drowned", "guardian", "witch", "pillager", "vindicator", "evoker", "ravager", "warden"} {
		if strings.Contains(kind, k) {
			return true
		}
	}
	return false
}

func mobKind(entityType string) string {
	t := strings.ToLower(strings.TrimSpace(entityType))
	if i := strings.Index(t, ":"); i >= 0 {
		t = t[i+1:]
	}
	return strings.ReplaceAll(t, " ", "_")
}

// NoteDanger registers d, or refreshes the matching danger. It reports whether the register
// changed enough to be worth telling the clan.
func (s *AgentState) NoteDanger(d Danger) bool {
	if s == nil || d.Kind == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.noteDangerLocked(d) {
		return false
	}
	s.freshDangers = append(s.freshDangers, d)
	return true
}

func (s *AgentState) noteDangerLocked(d Danger) bool {
	kept := s.dangers[:0]
	for _, old := range s.dangers {
		if d.Seen-old.Seen < DangerMemoryTicks {
			kept = append(kept, old)
		}
	}
	s.dangers = kept
	for i, old := range s.dangers {
		if old.Kind == d.Kind && math.Hypot(old.X-d.X, old.Z-d.Z) <= dangerMergeRadius {
			if d.Seen-old.Seen < dangerRefreshTicks {
				return false
			}
			s.dangers[i] = d
			return true
		}
	}
	s.dangers = append(s.dangers, d)
	return true
}

// MergeDangers takes in dangers the clan reported; they are not reported back.
func (s *AgentState) MergeDangers(list []Danger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range list {
		s.noteDangerLocked(d)
	}
}

// DrainFreshDangers returns the dangers met since the last call, for sharing.
func (s *AgentState) DrainFreshDangers() []Danger {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.freshDangers
	s.freshDangers = nil
	return out
}

// Dangers returns the dangers still registered at tick.
func (s *AgentState) Dangers(tick int64) []Danger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Danger
	for _, d := range s.dangers {
		if tick-d.Seen < DangerMemoryTicks {
			out = append(out, d)
		}
	}
	return out
}

// nearestDangerLocked returns the registered danger closest to (x, z) and its distance.
func (s *AgentState) nearestDangerLocked(x, z float64, tick int64) (Danger, float64, bool) {
	var best Danger
	bestDist, found := math.MaxFloat64, false
	for _, d := range s.dangers {
		if tick-d.Seen >= DangerMemoryTicks {
			continue
		}
		if dist := math.Hypot(d.X-x, d.Z-z); dist < bestDist {
			best, bestDist, found = d, dist, true
		}
	}
	return best, bestDist, found
}

func (s *AgentState) nearestDanger(x, z float64, tick int64) (Danger, float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nearestDangerLocked(x, z, tick)
}

// endangeredLocked reports a spot an agent should not choose to go to.
func (s *AgentState) endangeredLocked(x, z float64, tick int64) bool {
	if _, dist, ok := s.nearestDangerLocked(x, z, tick); !ok || dist >= dangerRadius {
		return false
	}
	st, settled := s.settlementLocked()
	return !settled || math.Hypot(st.Pos[0]-x, st.Pos[2]-z) > dangerHomeExempt
}

// detour returns a waypoint round the first danger the straight walk from -> to would pass
// through. An agent already inside a danger, or heading into one, gets no detour.
func (s *AgentState) detour(from, to [3]float64, tick int64) ([3]float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dx, dz := to[0]-from[0], to[2]-from[2]
	length := math.Hypot(dx, dz)
	if length < 1 {
		return to, false
	}
	ux, uz := dx/length, dz/length
	best, bestAlong := Danger{}, math.MaxFloat64
	for _, d := range s.dangers {
		if tick-d.Seen >= DangerMemoryTicks ||
			math.Hypot(d.X-from[0], d.Z-from[2]) <= dangerRadius || math.Hypot(d.X-to[0], d.Z-to[2]) <= dangerRadius {
			continue
		}
		along := (d.X-from[0])*ux + (d.Z-from[2])*uz
		if along <= 0 || along >= length || along >= bestAlong {
			continue
		}
		if across := math.Abs((d.X-from[0])*uz - (d.Z-from[2])*ux); across < dangerRadius {
			best, bestAlong = d, along
		}
	}
	if bestAlong == math.MaxFloat64 {
		return to, false
	}
	// Pass on the side away from the danger's centre.
	side := 1.0
	if (best.X-from[0])*-uz+(best.Z-from[2])*ux > 0 {
		side = -1
	}
	off := dangerRadius + detourMargin
	snap := func(v float64) float64 { return math.Round(v/detourGrid) * detourGrid }
	return [3]float64{snap(best.X - uz*side*off), to[1], snap(best.Z + ux*side*off)}, true
}

// observeDangers registers the dangers this frame shows.
func observeDangers(ctx *FSMContext) {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Frame.Status.Position == nil {
		return
	}
	f := ctx.Frame
	note := func(kind string, at *civquestv1.Vec3) {
		ctx.State.NoteDanger(Danger{Kind: kind, X: at.X, Y: at.Y, Z: at.Z, Seen: f.WorldTick})
	}
	for _, e := range f.VisibleEntities {
		if e != nil && e.Position != nil && IsHostile(e) && lingers(mobKind(e.EntityType)) {
			note(mobKind(e.EntityType), e.Position)
		}
	}
	hit := f.LastDamageTaken
	if hit == nil || IsPassiveOrEnvironmentalDamage(hit) {
		return
	}
	for _, e := range f.VisibleEntities {
		if e != nil && e.Position != nil && e.EntityId == hit.AttackerEntityId && IsHostile(e) {
			note(mobKind(e.EntityType), e.Position)
			return
		}
	}
	// Unseen attacker: mark where the blow landed, if its name is a mob's.
	if kind := mobKind(hit.AttackerName); kind != "" && IsHostile(&civquestv1.VisibleEntity{EntityType: kind}) {
		note(kind, f.Status.Position)
	}
}
