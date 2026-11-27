package system1

// Remembered dangers (cq-gey.2). A mob that hurts an agent, or one that stays where it lives
// (a Drowned in its water), marks its spot for a game day. The agent does not choose targets
// there, walks round it, and having been hit keeps going until it is clear. Dangers are shared
// with the clan (agent/danger_sync.go).

import (
	"fmt"
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
	// A danger met again is told to the clan at most this often.
	dangerRefreshTicks = int64(1200)
	// Food is not sought towards a danger this near the walk's end, nor fished this near a
	// water dweller.
	dangerWideRadius = 48.0
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
	shared  int64 // when this agent last told the clan
}

// lingers reports mobs that stay about the place they were seen.
func lingers(kind string) bool {
	for _, k := range []string{"drowned", "polar_bear", "guardian", "witch", "pillager", "vindicator", "evoker", "ravager", "warden"} {
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

// NoteDanger registers d, or moves and refreshes the matching danger: mobs walk. It reports
// whether the clan should be told.
func (s *AgentState) NoteDanger(d Danger) bool {
	if s == nil || d.Kind == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.noteDangerLocked(d)
	at := &s.dangers[i]
	if at.shared != 0 && d.Seen-at.shared < dangerRefreshTicks {
		return false
	}
	at.shared = d.Seen
	s.freshDangers = append(s.freshDangers, *at)
	return true
}

// noteDangerLocked records d and returns its place in the register. An older report never
// displaces a newer one.
func (s *AgentState) noteDangerLocked(d Danger) int {
	kept := s.dangers[:0]
	for _, old := range s.dangers {
		if d.Seen-old.Seen < DangerMemoryTicks {
			kept = append(kept, old)
		}
	}
	s.dangers = kept
	for i, old := range s.dangers {
		if old.Kind == d.Kind && math.Hypot(old.X-d.X, old.Z-d.Z) <= dangerMergeRadius {
			if d.Seen >= old.Seen {
				d.shared = old.shared
				s.dangers[i] = d
			}
			return i
		}
	}
	s.noteDiag(EventDanger, fmt.Sprintf("%s at (%.0f, %.0f)", d.Kind, d.X, d.Z),
		map[string]any{"kind": d.Kind, "x": d.X, "z": d.Z, "seen_tick": d.Seen})
	s.dangers = append(s.dangers, d)
	return len(s.dangers) - 1
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

// towardDangerLocked reports that walking from (fx, fz) to (tx, tz) closes on a danger nearby.
func (s *AgentState) towardDangerLocked(fx, fz, tx, tz float64, tick int64) bool {
	for _, d := range s.dangers {
		if tick-d.Seen >= DangerMemoryTicks {
			continue
		}
		if to := math.Hypot(d.X-tx, d.Z-tz); to < dangerWideRadius && to < math.Hypot(d.X-fx, d.Z-fz) {
			return true
		}
	}
	return false
}

// waterUnsafeLocked reports a water-dwelling danger near enough to make fishing foolish.
func (s *AgentState) waterUnsafeLocked(pos *civquestv1.Vec3, tick int64) bool {
	if s == nil || pos == nil {
		return false
	}
	for _, d := range s.dangers {
		if tick-d.Seen < DangerMemoryTicks && (d.Kind == "drowned" || d.Kind == "guardian") &&
			math.Hypot(d.X-pos.X, d.Z-pos.Z) < dangerWideRadius {
			return true
		}
	}
	return false
}

func (s *AgentState) waterUnsafe(pos *civquestv1.Vec3, tick int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.waterUnsafeLocked(pos, tick)
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
		// A polar bear is registered on sight although it only attacks when it chooses to.
		if e != nil && e.Position != nil && lingers(mobKind(e.EntityType)) &&
			(IsHostile(e) || mobKind(e.EntityType) == "polar_bear") {
			note(mobKind(e.EntityType), e.Position)
		}
	}
	hit := f.LastDamageTaken
	if hit == nil || IsPassiveOrEnvironmentalDamage(hit) {
		return
	}
	// Anything that is not a person and does harm is a danger, hostile by nature or not.
	byMob := hit.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB ||
		hit.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_ARROW
	for _, e := range f.VisibleEntities {
		if e != nil && e.Position != nil && e.EntityId == hit.AttackerEntityId && (IsHostile(e) || byMob && !isPlayerEntity(e)) {
			note(mobKind(e.EntityType), e.Position)
			return
		}
	}
	// Unseen attacker: mark where the blow landed, if it was a mob's.
	if kind := mobKind(hit.AttackerName); kind != "" && (byMob || IsHostile(&civquestv1.VisibleEntity{EntityType: kind})) {
		note(kind, f.Status.Position)
	}
}

func isPlayerEntity(e *civquestv1.VisibleEntity) bool { return mobKind(e.EntityType) == "player" }
