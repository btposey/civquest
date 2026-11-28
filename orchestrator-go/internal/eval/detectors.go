package eval

import (
	"fmt"
	"math"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// TransitionRecord logs an HFSM state transition during simulation.
type TransitionRecord struct {
	FromState system1.State
	ToState   system1.State
	Tick      int64
	Reason    string
}

// FSMOscillationDetector detects rapid fluttering or oscillation between states
// (e.g. EXPLORE <-> SHELTER freeze where >2 transitions between identical states occur within 60s).
type FSMOscillationDetector struct {
	WindowTicks     int64
	MaxOscillations int
	transitions     []TransitionRecord
	violations      []string
}

// NewFSMOscillationDetector creates an oscillation detector. Default window is 1200 ticks (60s).
func NewFSMOscillationDetector(windowTicks int64, maxOscillations int) *FSMOscillationDetector {
	if windowTicks <= 0 {
		windowTicks = 1200 // 60s at 20 ticks/sec
	}
	if maxOscillations <= 0 {
		maxOscillations = 2
	}
	return &FSMOscillationDetector{
		WindowTicks:     windowTicks,
		MaxOscillations: maxOscillations,
		transitions:     make([]TransitionRecord, 0),
		violations:      make([]string, 0),
	}
}

// RecordTransition logs an FSM transition and evaluates the sliding window for flutter violations.
func (d *FSMOscillationDetector) RecordTransition(from, to system1.State, tick int64, reason string) {
	if d == nil {
		return
	}
	rec := TransitionRecord{
		FromState: from,
		ToState:   to,
		Tick:      tick,
		Reason:    reason,
	}
	d.transitions = append(d.transitions, rec)

	// Check sliding window ending at current transition
	count := 0
	for i := len(d.transitions) - 1; i >= 0; i-- {
		t := d.transitions[i]
		if tick-t.Tick > d.WindowTicks {
			break
		}
		if (t.FromState == from && t.ToState == to) || (t.FromState == to && t.ToState == from) {
			count++
		}
	}

	if count > d.MaxOscillations {
		msg := fmt.Sprintf("FSM oscillation violation at tick %d: %d transitions between %s and %s within %d ticks (last reason: %q)",
			tick, count, from, to, d.WindowTicks, reason)
		d.violations = append(d.violations, msg)
	}
}

// Violations returns all detected oscillation violations.
func (d *FSMOscillationDetector) Violations() []string {
	if d == nil {
		return nil
	}
	return d.violations
}

// Check returns an error if any oscillation violations occurred.
func (d *FSMOscillationDetector) Check() error {
	if d == nil || len(d.violations) == 0 {
		return nil
	}
	return fmt.Errorf("FSM oscillation detector failed with %d violation(s): %s", len(d.violations), d.violations[0])
}

// Transitions returns the full recorded transition history.
func (d *FSMOscillationDetector) Transitions() []TransitionRecord {
	if d == nil {
		return nil
	}
	return d.transitions
}

// NavigationStallDetector detects when an active agent is stuck in PATHING or IDLE without forward progress.
type NavigationStallDetector struct {
	MaxStallTicks    int64
	MinProgressDist  float64
	lastProgressTick int64
	lastPos          *civquestv1.Vec3
	violations       []string
}

// NewNavigationStallDetector creates a navigation stall detector. Default threshold is 600 ticks (30s).
func NewNavigationStallDetector(maxStallTicks int64, minProgressDist float64) *NavigationStallDetector {
	if maxStallTicks <= 0 {
		maxStallTicks = 600 // 30s at 20 ticks/sec
	}
	if minProgressDist <= 0 {
		minProgressDist = 1.0
	}
	return &NavigationStallDetector{
		MaxStallTicks:   maxStallTicks,
		MinProgressDist: minProgressDist,
		violations:      make([]string, 0),
	}
}

// OnStep evaluates progress on each simulation tick/step.
func (d *NavigationStallDetector) OnStep(tick int64, pos *civquestv1.Vec3, navState civquestv1.NavigationState, fsmState system1.State, routine system1.RoutineState, isSleeping bool, actionOccurred bool) {
	if d == nil || pos == nil {
		return
	}

	// Legitimate stationary states: sleeping, resting in safe shelter/bed, or evening social gathering
	if isSleeping || fsmState == system1.StateRest || routine == system1.RoutineSleep || routine == system1.RoutineTavernSocial {
		d.lastProgressTick = tick
		d.lastPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		return
	}

	// If block interaction occurred (harvesting, crafting, placing), progress was made
	if actionOccurred {
		d.lastProgressTick = tick
		d.lastPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		return
	}

	if d.lastPos == nil {
		d.lastProgressTick = tick
		d.lastPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		return
	}

	dist := math.Hypot(pos.X-d.lastPos.X, pos.Z-d.lastPos.Z)
	if dist >= d.MinProgressDist {
		d.lastProgressTick = tick
		d.lastPos.X = pos.X
		d.lastPos.Y = pos.Y
		d.lastPos.Z = pos.Z
		return
	}

	if tick-d.lastProgressTick > d.MaxStallTicks {
		msg := fmt.Sprintf("navigation stall violation at tick %d: agent in %s (%s) made no forward progress for %d ticks at (%.1f, %.1f, %.1f)",
			tick, fsmState, navState, tick-d.lastProgressTick, pos.X, pos.Y, pos.Z)
		d.violations = append(d.violations, msg)
		d.lastProgressTick = tick // Reset to prevent log flood
	}
}

// Check returns an error if any navigation stall violations were detected.
func (d *NavigationStallDetector) Check() error {
	if d == nil || len(d.violations) == 0 {
		return nil
	}
	return fmt.Errorf("navigation stall detector failed with %d violation(s): %s", len(d.violations), d.violations[0])
}

// Violations returns all detected navigation stall violations.
func (d *NavigationStallDetector) Violations() []string {
	if d == nil {
		return nil
	}
	return d.violations
}

// BlueprintBlockRecord tracks a single block placed during blueprint construction.
type BlueprintBlockRecord struct {
	Pos     *civquestv1.BlockPosProto
	BlockID string
	Tick    int64
	Index   int
}

// BlueprintAdvancementDetector verifies bottom-up shelter construction progresses step-by-step to completion.
type BlueprintAdvancementDetector struct {
	TargetBlocks int
	placements   []BlueprintBlockRecord
	violations   []string
}

// NewBlueprintAdvancementDetector creates a blueprint advancement detector.
func NewBlueprintAdvancementDetector(targetBlocks int) *BlueprintAdvancementDetector {
	return &BlueprintAdvancementDetector{
		TargetBlocks: targetBlocks,
		placements:   make([]BlueprintBlockRecord, 0),
		violations:   make([]string, 0),
	}
}

// OnBlockPlaced records a placed block and checks bottom-up ordering.
func (d *BlueprintAdvancementDetector) OnBlockPlaced(pos *civquestv1.BlockPosProto, blockID string, tick int64, buildIndex int) {
	if d == nil || pos == nil {
		return
	}
	rec := BlueprintBlockRecord{
		Pos:     pos,
		BlockID: blockID,
		Tick:    tick,
		Index:   buildIndex,
	}
	if len(d.placements) > 0 {
		last := d.placements[len(d.placements)-1]
		// Bottom-up verification: Y must never decrease between sequential construction phases
		// The door and windows are hung after the structure stands.
		if pos.Y < last.Pos.Y-1 && !blueprints.IsFitting(blockID) {
			msg := fmt.Sprintf("blueprint bottom-up violation at tick %d: placed Y=%d after previous block at Y=%d",
				tick, pos.Y, last.Pos.Y)
			d.violations = append(d.violations, msg)
		}
	}
	d.placements = append(d.placements, rec)
}

// Count returns the number of placed blueprint blocks.
func (d *BlueprintAdvancementDetector) Count() int {
	if d == nil {
		return 0
	}
	return len(d.placements)
}

// Check verifies that construction advanced and achieved target blocks without violations.
func (d *BlueprintAdvancementDetector) Check() error {
	if d == nil {
		return nil
	}
	if len(d.violations) > 0 {
		return fmt.Errorf("blueprint advancement violation: %s", d.violations[0])
	}
	if d.TargetBlocks > 0 && len(d.placements) < d.TargetBlocks {
		return fmt.Errorf("blueprint advancement incomplete: placed %d blocks, expected %d", len(d.placements), d.TargetBlocks)
	}
	return nil
}

// SectorAzimuthLockDetector verifies frontier exploration maintains sector azimuth without golden-angle oscillation.
type SectorAzimuthLockDetector struct {
	Origin          *civquestv1.Vec3
	LockedAzimuth   float64
	AzimuthSet      bool
	MaxVarianceRad  float64
	ExpectedLegs    []float64
	recordedTargets []*civquestv1.Vec3
	observedLegs    []float64
	violations      []string
}

// NewSectorAzimuthLockDetector creates an azimuth lock detector.
func NewSectorAzimuthLockDetector(origin *civquestv1.Vec3, maxVarianceRad float64, expectedLegs []float64) *SectorAzimuthLockDetector {
	if maxVarianceRad <= 0 {
		maxVarianceRad = 0.20 // ~11 degrees tolerance
	}
	return &SectorAzimuthLockDetector{
		Origin:         origin,
		MaxVarianceRad: maxVarianceRad,
		ExpectedLegs:   expectedLegs,
		violations:     make([]string, 0),
	}
}

// OnNavigationWaypoint records an exploration navigation waypoint.
func (d *SectorAzimuthLockDetector) OnNavigationWaypoint(target *civquestv1.Vec3, tick int64) {
	if d == nil || target == nil || d.Origin == nil {
		return
	}

	dx := target.X - d.Origin.X
	dz := target.Z - d.Origin.Z
	dist := math.Hypot(dx, dz)
	if dist < 5.0 {
		return // Ignore micro-step centering or immediate adjustments
	}

	azimuth := math.Atan2(dz, dx)
	if !d.AzimuthSet {
		d.LockedAzimuth = azimuth
		d.AzimuthSet = true
	} else {
		// Angular difference wrapped to [-pi, pi]
		diff := math.Atan2(math.Sin(azimuth-d.LockedAzimuth), math.Cos(azimuth-d.LockedAzimuth))
		if math.Abs(diff) > d.MaxVarianceRad {
			msg := fmt.Sprintf("sector azimuth lock violation at tick %d: waypoint (%.1f, %.1f) azimuth %.3f rad differs from locked azimuth %.3f rad by %.3f rad (> %.3f max)",
				tick, target.X, target.Z, azimuth, d.LockedAzimuth, math.Abs(diff), d.MaxVarianceRad)
			d.violations = append(d.violations, msg)
		}
	}

	d.recordedTargets = append(d.recordedTargets, target)
	d.observedLegs = append(d.observedLegs, dist)
}

// Check verifies that waypoints locked to azimuth and progressive radial legs were observed.
func (d *SectorAzimuthLockDetector) Check() error {
	if d == nil {
		return nil
	}
	if len(d.violations) > 0 {
		return fmt.Errorf("sector azimuth lock failed: %s", d.violations[0])
	}
	if len(d.ExpectedLegs) > 0 {
		if len(d.observedLegs) < len(d.ExpectedLegs) {
			return fmt.Errorf("expected at least %d radial legs, observed %d", len(d.ExpectedLegs), len(d.observedLegs))
		}
		for i, exp := range d.ExpectedLegs {
			matched := false
			for _, obs := range d.observedLegs {
				if math.Abs(obs-exp) <= 6.0 {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("expected radial leg ~%.1fm was not observed in %+v", exp, d.observedLegs)
			}
			_ = i
		}
	}
	return nil
}

// ObservedLegs returns all observed radial distances from origin.
func (d *SectorAzimuthLockDetector) ObservedLegs() []float64 {
	if d == nil {
		return nil
	}
	return d.observedLegs
}
