package system1

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Habit trail constants per ADR-023.
const (
	DefaultBreadcrumbDisplacement = 8.0  // Sample agent position every 8-12m of Euclidean displacement
	DefaultArrivalThreshold       = 4.0  // Waypoint arrival proximity threshold (<= 4m)
	DefaultFinalTransitionDist    = 12.0 // Direct destination arrival transition threshold (<= 12m)
	MinCorridorDistance           = 16.0 // Minimum distance between origin and destination for corridor creation (> 16m)
	DefaultRDPEpsilon             = 1.0  // RDP waypoint simplification epsilon (1.0m)
)

// Distance3D returns the 3D Euclidean distance between two points.
func Distance3D(a, b *civquestv1.Vec3) float64 {
	if a == nil || b == nil {
		return 0
	}
	dx := a.X - b.X
	dy := a.Y - b.Y
	dz := a.Z - b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// EncodeWaypointsJSON serializes a slice of 3D waypoints into a JSON array of [x, y, z] triples.
func EncodeWaypointsJSON(waypoints []*civquestv1.Vec3) (string, error) {
	if len(waypoints) == 0 {
		return "[]", nil
	}
	coords := make([][3]float64, len(waypoints))
	for i, wp := range waypoints {
		if wp != nil {
			coords[i] = [3]float64{
				math.Round(wp.X*100) / 100,
				math.Round(wp.Y*100) / 100,
				math.Round(wp.Z*100) / 100,
			}
		}
	}
	b, err := json.Marshal(coords)
	if err != nil {
		return "[]", err
	}
	return string(b), nil
}

// DecodeWaypointsJSON parses a JSON array of [x, y, z] triples into []*civquestv1.Vec3.
func DecodeWaypointsJSON(waypointsJSON string) ([]*civquestv1.Vec3, error) {
	str := strings.TrimSpace(waypointsJSON)
	if str == "" || str == "[]" {
		return nil, nil
	}
	var coords [][3]float64
	if err := json.Unmarshal([]byte(str), &coords); err != nil {
		return nil, fmt.Errorf("unmarshaling waypoints json: %w", err)
	}
	out := make([]*civquestv1.Vec3, len(coords))
	for i, c := range coords {
		out[i] = &civquestv1.Vec3{
			X: c[0],
			Y: c[1],
			Z: c[2],
		}
	}
	return out, nil
}

// perpendicularDistance3D computes the perpendicular Euclidean distance from point p
// to the 3D line segment a -> b.
func perpendicularDistance3D(p, a, b *civquestv1.Vec3) float64 {
	abX := b.X - a.X
	abY := b.Y - a.Y
	abZ := b.Z - a.Z
	lenSq := abX*abX + abY*abY + abZ*abZ
	if lenSq == 0 {
		return Distance3D(p, a)
	}

	apX := p.X - a.X
	apY := p.Y - a.Y
	apZ := p.Z - a.Z
	t := (apX*abX + apY*abY + apZ*abZ) / lenSq
	if t <= 0 {
		return Distance3D(p, a)
	} else if t >= 1 {
		return Distance3D(p, b)
	}

	proj := &civquestv1.Vec3{
		X: a.X + t*abX,
		Y: a.Y + t*abY,
		Z: a.Z + t*abZ,
	}
	return Distance3D(p, proj)
}

// SimplifyWaypointsRDP simplifies a sequence of 3D waypoints using the Ramer-Douglas-Peucker algorithm.
// If epsilon <= 0, DefaultRDPEpsilon (1.0m) is used.
func SimplifyWaypointsRDP(waypoints []*civquestv1.Vec3, epsilon float64) []*civquestv1.Vec3 {
	if len(waypoints) <= 2 {
		out := make([]*civquestv1.Vec3, len(waypoints))
		for i, w := range waypoints {
			if w != nil {
				out[i] = &civquestv1.Vec3{X: w.X, Y: w.Y, Z: w.Z}
			}
		}
		return out
	}
	if epsilon <= 0 {
		epsilon = DefaultRDPEpsilon
	}

	maxDist := 0.0
	maxIdx := 0
	a := waypoints[0]
	b := waypoints[len(waypoints)-1]

	for i := 1; i < len(waypoints)-1; i++ {
		d := perpendicularDistance3D(waypoints[i], a, b)
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}

	if maxDist > epsilon {
		left := SimplifyWaypointsRDP(waypoints[:maxIdx+1], epsilon)
		right := SimplifyWaypointsRDP(waypoints[maxIdx:], epsilon)
		res := make([]*civquestv1.Vec3, 0, len(left)+len(right)-1)
		res = append(res, left[:len(left)-1]...)
		res = append(res, right...)
		return res
	}

	return []*civquestv1.Vec3{
		{X: a.X, Y: a.Y, Z: a.Z},
		{X: b.X, Y: b.Y, Z: b.Z},
	}
}

// HabitTrailTracker manages breadcrumb recording, corridor consolidation, and
// multi-waypoint corridor navigation execution per ADR-023.
type HabitTrailTracker struct {
	mu sync.RWMutex

	// Origin tracking (last confirmed POI the agent was near)
	originPOIID string
	originPos   *civquestv1.Vec3

	// Destination tracking
	destPOIID string
	destPos   *civquestv1.Vec3

	// Breadcrumb recording
	breadcrumbs     []*civquestv1.Vec3
	lastSampledPos  *civquestv1.Vec3
	minDisplacement float64

	// Active corridor navigation execution
	navigatingCorridor  bool
	inFinalLeg          bool
	activeCorridor      *db.TrailCorridor
	activeWaypoints     []*civquestv1.Vec3
	currentWaypointIdx  int
	arrivalThreshold    float64
	finalTransitionDist float64
	finalTargetPos      *civquestv1.Vec3
	finalTargetPOIID    string
	agentID             string
}

// NewHabitTrailTracker creates an initialized HabitTrailTracker.
func NewHabitTrailTracker(minDisplacement, arrivalThreshold, finalTransitionDist float64) *HabitTrailTracker {
	if minDisplacement <= 0 {
		minDisplacement = DefaultBreadcrumbDisplacement
	}
	if arrivalThreshold <= 0 {
		arrivalThreshold = DefaultArrivalThreshold
	}
	if finalTransitionDist <= 0 {
		finalTransitionDist = DefaultFinalTransitionDist
	}

	return &HabitTrailTracker{
		minDisplacement:     minDisplacement,
		arrivalThreshold:    arrivalThreshold,
		finalTransitionDist: finalTransitionDist,
		breadcrumbs:         make([]*civquestv1.Vec3, 0),
	}
}

// SetOriginPOI sets the active origin POI (last confirmed POI the agent was near).
func (t *HabitTrailTracker) SetOriginPOI(poiID string, pos *civquestv1.Vec3) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.originPOIID = poiID
	if pos != nil {
		t.originPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	} else {
		t.originPos = nil
	}
}

// CurrentOriginPOIID returns the currently tracked origin POI ID.
func (t *HabitTrailTracker) CurrentOriginPOIID() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.originPOIID
}

// CurrentOriginPos returns a copy of the tracked origin POI position.
func (t *HabitTrailTracker) CurrentOriginPos() *civquestv1.Vec3 {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.originPos == nil {
		return nil
	}
	return &civquestv1.Vec3{X: t.originPos.X, Y: t.originPos.Y, Z: t.originPos.Z}
}

// SetDestinationPOI sets the destination POI being navigated towards.
func (t *HabitTrailTracker) SetDestinationPOI(poiID string, pos *civquestv1.Vec3) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.destPOIID = poiID
	if pos != nil {
		t.destPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
	} else {
		t.destPos = nil
	}
}

// CurrentDestPOIID returns the currently tracked destination POI ID.
func (t *HabitTrailTracker) CurrentDestPOIID() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.destPOIID
}

// CurrentDestPos returns a copy of the tracked destination POI position.
func (t *HabitTrailTracker) CurrentDestPos() *civquestv1.Vec3 {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.destPos == nil {
		return nil
	}
	return &civquestv1.Vec3{X: t.destPos.X, Y: t.destPos.Y, Z: t.destPos.Z}
}

// ClearDestination clears the tracked destination POI.
func (t *HabitTrailTracker) ClearDestination() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.destPOIID = ""
	t.destPos = nil
	t.inFinalLeg = false
}

// SampleBreadcrumb evaluates whether the agent has moved sufficiently (>= minDisplacement, 8-12m)
// since the last sampled position. If so, records the current position as a breadcrumb waypoint.
// Returns true if a new breadcrumb was recorded.
func (t *HabitTrailTracker) SampleBreadcrumb(pos *civquestv1.Vec3) bool {
	if t == nil || pos == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Only record breadcrumbs if navigating towards a destination POI
	if t.destPOIID == "" {
		return false
	}

	if t.lastSampledPos == nil {
		if t.originPos != nil {
			// Measure from origin position if available
			if Distance3D(t.originPos, pos) < t.minDisplacement {
				return false
			}
		}
		t.lastSampledPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		t.breadcrumbs = append(t.breadcrumbs, &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z})
		return true
	}

	disp := Distance3D(t.lastSampledPos, pos)
	if disp >= t.minDisplacement {
		t.lastSampledPos = &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}
		t.breadcrumbs = append(t.breadcrumbs, &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z})
		return true
	}

	return false
}

// Breadcrumbs returns a defensive copy of currently recorded breadcrumb waypoints.
func (t *HabitTrailTracker) Breadcrumbs() []*civquestv1.Vec3 {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*civquestv1.Vec3, len(t.breadcrumbs))
	for i, w := range t.breadcrumbs {
		if w != nil {
			out[i] = &civquestv1.Vec3{X: w.X, Y: w.Y, Z: w.Z}
		}
	}
	return out
}

// ClearBreadcrumbs resets recorded breadcrumbs and the last sampled position.
func (t *HabitTrailTracker) ClearBreadcrumbs() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.breadcrumbs = t.breadcrumbs[:0]
	t.lastSampledPos = nil
}

// ConsolidateCorridor constructs and returns a consolidated db.TrailCorridor from recorded breadcrumbs
// if distance between origin and destination > 16m and >= 1 intermediate waypoints were recorded.
// Simplifies waypoints via RDP (epsilon=1.0) and upserts into si if non-nil.
func (t *HabitTrailTracker) ConsolidateCorridor(si *SpatialIndex, epsilon float64) (*db.TrailCorridor, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.originPOIID == "" || t.destPOIID == "" || t.originPOIID == t.destPOIID {
		return nil, false
	}
	if t.originPos == nil || t.destPos == nil {
		return nil, false
	}

	dist := Distance3D(t.originPos, t.destPos)
	if dist <= MinCorridorDistance {
		return nil, false
	}
	if len(t.breadcrumbs) < 1 {
		return nil, false
	}

	if epsilon <= 0 {
		epsilon = DefaultRDPEpsilon
	}

	// Simplify intermediate waypoints
	simplified := SimplifyWaypointsRDP(t.breadcrumbs, epsilon)
	if len(simplified) == 0 {
		return nil, false
	}

	waypointsJSON, err := EncodeWaypointsJSON(simplified)
	if err != nil {
		return nil, false
	}

	// Compute total distance: origin -> w_0 -> ... -> w_n -> dest
	totalDist := Distance3D(t.originPos, simplified[0])
	for i := 0; i < len(simplified)-1; i++ {
		totalDist += Distance3D(simplified[i], simplified[i+1])
	}
	totalDist += Distance3D(simplified[len(simplified)-1], t.destPos)

	corridorID := fmt.Sprintf("corridor_%s_%s", t.originPOIID, t.destPOIID)

	traverseCount := int64(1)
	isPaved := false
	if si != nil {
		if existing, ok := si.GetCorridor(t.originPOIID, t.destPOIID); ok && existing != nil {
			traverseCount = existing.TraverseCount + 1
			isPaved = existing.IsPaved
		}
	}

	corridor := &db.TrailCorridor{
		ID:            corridorID,
		FromPOIID:     t.originPOIID,
		ToPOIID:       t.destPOIID,
		WaypointsJSON: waypointsJSON,
		TotalDistance: math.Round(totalDist*100) / 100,
		IsPaved:       isPaved,
		TraverseCount: traverseCount,
	}

	if si != nil {
		si.UpsertCorridor(*corridor)
	}

	return corridor, true
}

// StartCorridorNavigation initiates multi-waypoint navigation along a known TrailCorridor.
// Returns the initial NavigateIntent targeting waypoint W_0.
func (t *HabitTrailTracker) StartCorridorNavigation(agentID string, corridor *db.TrailCorridor, finalDestPos *civquestv1.Vec3) (*civquestv1.AgentIntent, error) {
	if t == nil {
		return nil, errors.New("nil tracker")
	}
	if corridor == nil {
		return nil, errors.New("nil corridor")
	}

	waypoints, err := DecodeWaypointsJSON(corridor.WaypointsJSON)
	if err != nil {
		return nil, fmt.Errorf("decoding corridor waypoints: %w", err)
	}
	if len(waypoints) == 0 {
		return nil, errors.New("corridor contains no waypoints")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.navigatingCorridor = true
	t.inFinalLeg = false
	t.activeCorridor = corridor
	t.activeWaypoints = waypoints
	t.currentWaypointIdx = 0
	t.agentID = agentID
	if finalDestPos != nil {
		t.finalTargetPos = &civquestv1.Vec3{X: finalDestPos.X, Y: finalDestPos.Y, Z: finalDestPos.Z}
	} else {
		t.finalTargetPos = nil
	}
	t.finalTargetPOIID = corridor.ToPOIID

	firstWP := waypoints[0]
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: firstWP.X,
					Y: firstWP.Y,
					Z: firstWP.Z,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}, nil
}

// UpdateNavigation evaluates agent position along the active corridor.
// When within arrivalThreshold (<= 4m) of current waypoint, advances to the next waypoint.
// When reaching the final waypoint or within finalTransitionDist (12m) of destination,
// transitions to direct destination arrival.
// Returns (intent, true) if an intent was emitted/transition occurred.
func (t *HabitTrailTracker) UpdateNavigation(currentPos *civquestv1.Vec3, navState civquestv1.NavigationState) (*civquestv1.AgentIntent, bool) {
	if t == nil || currentPos == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.navigatingCorridor || len(t.activeWaypoints) == 0 {
		return nil, false
	}

	// 1. Direct destination transition when within finalTransitionDist (<= 12m) of final target
	if t.finalTargetPos != nil {
		distToFinal := Distance3D(currentPos, t.finalTargetPos)
		if distToFinal <= t.finalTransitionDist {
			t.navigatingCorridor = false
			t.inFinalLeg = true
			intent := &civquestv1.AgentIntent{
				AgentId: t.agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: t.finalTargetPos.X,
							Y: t.finalTargetPos.Y,
							Z: t.finalTargetPos.Z,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
			return intent, true
		}
	}

	// 2. Check arrival at current waypoint
	if t.currentWaypointIdx < len(t.activeWaypoints) {
		currWP := t.activeWaypoints[t.currentWaypointIdx]
		distToWP := Distance3D(currentPos, currWP)

		if distToWP <= t.arrivalThreshold || navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
			t.currentWaypointIdx++
			if t.currentWaypointIdx >= len(t.activeWaypoints) {
				// Reached final waypoint; transition to direct destination
				t.navigatingCorridor = false
				t.inFinalLeg = true
				if t.finalTargetPos != nil {
					intent := &civquestv1.AgentIntent{
						AgentId: t.agentID,
						Action: &civquestv1.AgentIntent_Navigate{
							Navigate: &civquestv1.NavigateIntent{
								Target: &civquestv1.Vec3{
									X: t.finalTargetPos.X,
									Y: t.finalTargetPos.Y,
									Z: t.finalTargetPos.Z,
								},
								SpeedMultiplier:  1.0,
								ArrivalTolerance: 2,
							},
						},
					}
					return intent, true
				}
				return nil, true
			}

			// Advance to next waypoint
			nextWP := t.activeWaypoints[t.currentWaypointIdx]
			intent := &civquestv1.AgentIntent{
				AgentId: t.agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{
							X: nextWP.X,
							Y: nextWP.Y,
							Z: nextWP.Z,
						},
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
			return intent, true
		}
	}

	return nil, false
}

// IsNavigatingCorridor reports whether multi-waypoint corridor navigation is currently active.
func (t *HabitTrailTracker) IsNavigatingCorridor() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.navigatingCorridor
}

// InFinalLeg reports whether the agent has completed all intermediate waypoints and is pathing
// directly to the final destination POI.
func (t *HabitTrailTracker) InFinalLeg() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.inFinalLeg
}

// ActiveCorridor returns a defensive copy of the currently active TrailCorridor, if any.
func (t *HabitTrailTracker) ActiveCorridor() *db.TrailCorridor {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.activeCorridor == nil {
		return nil
	}
	cp := *t.activeCorridor
	return &cp
}

// CurrentWaypointIndex returns the active waypoint index.
func (t *HabitTrailTracker) CurrentWaypointIndex() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentWaypointIdx
}

// Waypoints returns a defensive copy of the active corridor waypoints.
func (t *HabitTrailTracker) Waypoints() []*civquestv1.Vec3 {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*civquestv1.Vec3, len(t.activeWaypoints))
	for i, w := range t.activeWaypoints {
		if w != nil {
			out[i] = &civquestv1.Vec3{X: w.X, Y: w.Y, Z: w.Z}
		}
	}
	return out
}

// ResetNavigation clears active corridor navigation state.
func (t *HabitTrailTracker) ResetNavigation() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.navigatingCorridor = false
	t.inFinalLeg = false
	t.activeCorridor = nil
	t.activeWaypoints = nil
	t.currentWaypointIdx = 0
	t.finalTargetPos = nil
	t.finalTargetPOIID = ""
}
