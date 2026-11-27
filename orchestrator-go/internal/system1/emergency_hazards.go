package system1

// Sustained environmental hazards (bead cq-gpa.2).
//
// Drowning and burning are derived from the per-frame AgentStatus facts added in cq-gpa.1
// (is_in_water, submerged, air_supply, max_air_supply, is_on_fire), NOT from damage edges:
// vanilla damage lands once per 20 ticks, so a damage-edge trigger exits ~2 ticks after it
// enters and the routine walks straight back into the water. All ratios/budgets below are
// design budgets, not measurements.

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// drowningEntryAirRatio: submerged with air below this fraction is a drowning hazard.
	drowningEntryAirRatio = 0.75
	// drowningResolvedAirRatio: out of water AND air at/above this fraction ends it (> entry => hysteresis).
	drowningResolvedAirRatio = 0.80

	// emergencySettleHostileTicks: no immediate hostile for this long before a mob emergency
	// ends (filters visibility flicker; design budget).
	emergencySettleHostileTicks = int64(10)
	// emergencyQuietDamageTicks: no combat damage for this long (one melee attack cooldown,
	// design budget).
	emergencyQuietDamageTicks = int64(20)
	// emergencyEscapeTicks: an agent that was hit and is still inside the danger gives up
	// getting clear after this long without sight or blow (design budget).
	emergencyEscapeTicks = int64(200)
	// emergencyLeaseTicks caps how long one emergency frame may hold the motor stack before
	// it expires (and, if the threat persists, re-bids with a fresh frame); design budget ~30s.
	emergencyLeaseTicks = int64(600)
	// hazardNavReissueTicks re-issues the escape navigate this often (design budget).
	hazardNavReissueTicks = int64(20)
)

// airRatio returns remaining/max air, 1 when the frame carries no air telemetry.
func airRatio(s *civquestv1.AgentStatus) float64 {
	if s == nil || s.MaxAirSupply <= 0 {
		return 1
	}
	return float64(s.AirSupply) / float64(s.MaxAirSupply)
}

// sustainedDrowning is the telemetry-derived drowning hazard (eyes in water, air draining).
func sustainedDrowning(f *civquestv1.TelemetryFrame) bool {
	return f != nil && f.Status != nil && f.Status.Submerged && airRatio(f.Status) < drowningEntryAirRatio
}

// drowningResolved reports the agent is out of the water with air restored.
func drowningResolved(f *civquestv1.TelemetryFrame) bool {
	if f == nil || f.Status == nil {
		return true
	}
	s := f.Status
	return !s.IsInWater && !s.Submerged && airRatio(s) >= drowningResolvedAirRatio
}

func isBlockFluid(id string) bool {
	id = strings.ToLower(id)
	return strings.Contains(id, "water") || strings.Contains(id, "lava")
}

func isFireBlock(id string) bool {
	id = strings.ToLower(id)
	return strings.Contains(id, "lava") || strings.HasSuffix(id, "fire") || strings.Contains(id, "magma")
}

// dryStandTarget returns the nearest known dry standable cell (a non-fluid block with nothing
// listed above it), or nil. Uses only the frame's block list.
func dryStandTarget(f *civquestv1.TelemetryFrame) *civquestv1.Vec3 {
	if f == nil || f.Status == nil || f.Status.Position == nil {
		return nil
	}
	self := f.Status.Position
	occupied := map[[3]int32]bool{}
	for _, b := range f.InterestingBlocks {
		if b != nil && b.Pos != nil {
			occupied[[3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}] = true
		}
	}
	var best *civquestv1.Vec3
	bestD := math.MaxFloat64
	for _, b := range f.InterestingBlocks {
		if b == nil || b.Pos == nil || isBlockFluid(b.BlockId) || isFireBlock(b.BlockId) {
			continue
		}
		if occupied[[3]int32{b.Pos.X, b.Pos.Y + 1, b.Pos.Z}] {
			continue
		}
		dx, dz := float64(b.Pos.X)+0.5-self.X, float64(b.Pos.Z)+0.5-self.Z
		d := dx*dx + dz*dz + math.Pow(float64(b.Pos.Y+1)-self.Y, 2)
		if d < bestD {
			bestD = d
			best = &civquestv1.Vec3{X: float64(b.Pos.X) + 0.5, Y: float64(b.Pos.Y + 1), Z: float64(b.Pos.Z) + 0.5}
		}
	}
	return best
}

// fireEscapeTarget prefers the nearest known water (extinguishes), else a point away from
// known fire/lava blocks, else nil (nothing better known: let it burn out).
func fireEscapeTarget(f *civquestv1.TelemetryFrame) *civquestv1.Vec3 {
	if f == nil || f.Status == nil || f.Status.Position == nil {
		return nil
	}
	self := f.Status.Position
	var water *civquestv1.Vec3
	bestD := math.MaxFloat64
	var fx, fz float64
	nf := 0
	for _, b := range f.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		x, z := float64(b.Pos.X)+0.5, float64(b.Pos.Z)+0.5
		if strings.Contains(id, "water") {
			d := (x-self.X)*(x-self.X) + (z-self.Z)*(z-self.Z) + math.Pow(float64(b.Pos.Y)-self.Y, 2)
			if d < bestD {
				bestD = d
				water = &civquestv1.Vec3{X: x, Y: float64(b.Pos.Y), Z: z}
			}
		} else if isFireBlock(id) {
			fx += x
			fz += z
			nf++
		}
	}
	if water != nil {
		return water
	}
	if nf > 0 {
		dx, dz := self.X-fx/float64(nf), self.Z-fz/float64(nf)
		n := math.Hypot(dx, dz)
		if n < 0.01 {
			dx, dz, n = 1, 0, 1
		}
		return &civquestv1.Vec3{X: self.X + dx/n*8, Y: self.Y, Z: self.Z + dz/n*8}
	}
	return nil
}

// hazardEscapeIntents builds the navigate for a latched drowning/burning hazard. It returns
// the (possibly unchanged) last target/tick so the caller can dedupe re-issues.
func hazardEscapeIntent(agentID string, f *civquestv1.TelemetryFrame, drowning bool, lastTarget *civquestv1.Vec3, lastTick, tick int64) *civquestv1.AgentIntent {
	var target *civquestv1.Vec3
	if drowning {
		target = dryStandTarget(f)
		if target == nil && f != nil && f.Status != nil {
			target = computeSurfaceTarget(f.Status.Position, f.InterestingBlocks)
		}
	} else {
		target = fireEscapeTarget(f)
	}
	if target == nil {
		return nil
	}
	idle := f != nil && f.Status != nil && f.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	same := lastTarget != nil && math.Hypot(lastTarget.X-target.X, lastTarget.Z-target.Z) <= 2 && math.Abs(lastTarget.Y-target.Y) <= 2
	if same && (tick-lastTick < 5 || (!idle && tick-lastTick < hazardNavReissueTicks)) {
		return nil
	}
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target: target, SpeedMultiplier: 1.5, Sprint: true, ArrivalTolerance: 1,
		}},
	}
}
