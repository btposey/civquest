package system1

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// TestHabitTrails_RDP_CollinearSimplification verifies that collinear 3D breadcrumb points
// are simplified down to their endpoints using RDP with epsilon=1.0 (ADR-023).
func TestHabitTrails_RDP_CollinearSimplification(t *testing.T) {
	// 7 points in a straight line along the X axis
	points := []*civquestv1.Vec3{
		{X: 0.0, Y: 64.0, Z: 0.0},
		{X: 10.0, Y: 64.0, Z: 0.0},
		{X: 20.0, Y: 64.0, Z: 0.0},
		{X: 30.0, Y: 64.0, Z: 0.0},
		{X: 40.0, Y: 64.0, Z: 0.0},
		{X: 50.0, Y: 64.0, Z: 0.0},
		{X: 60.0, Y: 64.0, Z: 0.0},
	}

	simplified := SimplifyWaypointsRDP(points, 1.0)
	if len(simplified) != 2 {
		t.Fatalf("expected 2 simplified points for collinear sequence, got %d", len(simplified))
	}
	if simplified[0].X != 0.0 || simplified[1].X != 60.0 {
		t.Errorf("expected endpoints [0, 60], got [%.1f, %.1f]", simplified[0].X, simplified[1].X)
	}
}

// TestHabitTrails_RDP_DetourPreserved verifies that a significant lateral detour
// around a ravine or obstacle is preserved while collinear segments are pruned.
func TestHabitTrails_RDP_DetourPreserved(t *testing.T) {
	// Path with lateral detour along Z
	points := []*civquestv1.Vec3{
		{X: 0.0, Y: 64.0, Z: 0.0},
		{X: 10.0, Y: 64.0, Z: 0.0},
		{X: 20.0, Y: 64.0, Z: 0.0},
		{X: 30.0, Y: 64.0, Z: 25.0}, // Detour apex
		{X: 40.0, Y: 64.0, Z: 25.0},
		{X: 50.0, Y: 64.0, Z: 0.0},
		{X: 60.0, Y: 64.0, Z: 0.0},
	}

	simplified := SimplifyWaypointsRDP(points, 1.0)
	// Expect fewer points than original (collinear points pruned), but detour apex preserved
	if len(simplified) >= len(points) {
		t.Fatalf("expected simplification to reduce points (%d), got %d", len(points), len(simplified))
	}

	hasDetour := false
	for _, p := range simplified {
		if p.Z >= 20.0 {
			hasDetour = true
			break
		}
	}
	if !hasDetour {
		t.Fatalf("expected detour around obstacle (Z >= 20.0) to be preserved in simplified path")
	}
}

// TestHabitTrails_JSONSerialization verifies encoding and decoding of waypoints.
func TestHabitTrails_JSONSerialization(t *testing.T) {
	original := []*civquestv1.Vec3{
		{X: 10.5, Y: 64.0, Z: -5.25},
		{X: 25.0, Y: 65.5, Z: 18.0},
	}

	jsonStr, err := EncodeWaypointsJSON(original)
	if err != nil {
		t.Fatalf("EncodeWaypointsJSON failed: %v", err)
	}

	decoded, err := DecodeWaypointsJSON(jsonStr)
	if err != nil {
		t.Fatalf("DecodeWaypointsJSON failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("expected %d decoded waypoints, got %d", len(original), len(decoded))
	}

	for i := range original {
		if math.Abs(decoded[i].X-original[i].X) > 0.01 ||
			math.Abs(decoded[i].Y-original[i].Y) > 0.01 ||
			math.Abs(decoded[i].Z-original[i].Z) > 0.01 {
			t.Errorf("waypoint %d mismatch: got (%f,%f,%f), expected (%f,%f,%f)",
				i, decoded[i].X, decoded[i].Y, decoded[i].Z,
				original[i].X, original[i].Y, original[i].Z)
		}
	}

	// Empty waypoints handling
	emptyJSON, err := EncodeWaypointsJSON(nil)
	if err != nil || emptyJSON != "[]" {
		t.Errorf("expected '[]' for nil waypoints, got %q (err=%v)", emptyJSON, err)
	}
	emptyDecoded, err := DecodeWaypointsJSON("[]")
	if err != nil || len(emptyDecoded) != 0 {
		t.Errorf("expected empty slice for '[]', got %+v (err=%v)", emptyDecoded, err)
	}
}

// TestHabitTrails_SpatialIndex_CorridorCRUD verifies storing, retrieving, and listing
// trail corridors in SpatialIndex with thread safety and defensive copies (ADR-023).
func TestHabitTrails_SpatialIndex_CorridorCRUD(t *testing.T) {
	si := NewSpatialIndex()

	c1 := db.TrailCorridor{
		ID:            "corridor_poi-home_poi-farm",
		FromPOIID:     "poi-home",
		ToPOIID:       "poi-farm",
		WaypointsJSON: `[[10,64,0],[20,64,5]]`,
		TotalDistance: 24.5,
		IsPaved:       false,
		TraverseCount: 3,
	}
	c2 := db.TrailCorridor{
		ID:            "corridor_poi-home_poi-mine",
		FromPOIID:     "poi-home",
		ToPOIID:       "poi-mine",
		WaypointsJSON: `[[5,60,-10],[15,55,-30]]`,
		TotalDistance: 45.0,
		IsPaved:       true,
		TraverseCount: 15,
	}

	si.UpsertCorridor(c1)
	si.UpsertCorridor(c2)

	// Direct lookup by endpoints
	got1, ok := si.GetCorridor("poi-home", "poi-farm")
	if !ok || got1 == nil {
		t.Fatalf("expected to find corridor poi-home -> poi-farm")
	}
	if got1.ID != c1.ID || got1.TraverseCount != 3 {
		t.Errorf("corridor data mismatch: %+v", got1)
	}

	// Defensive copy verification: mutating returned corridor must not alter internal index state
	got1.TraverseCount = 999
	checkCopy, _ := si.GetCorridor("poi-home", "poi-farm")
	if checkCopy.TraverseCount != 3 {
		t.Errorf("defensive copy violated: internal traverse_count modified to %d", checkCopy.TraverseCount)
	}

	// FindCorridorsFrom verification (sorted by TraverseCount DESC)
	fromHome := si.FindCorridorsFrom("poi-home")
	if len(fromHome) != 2 {
		t.Fatalf("expected 2 corridors from poi-home, got %d", len(fromHome))
	}
	if fromHome[0].ID != "corridor_poi-home_poi-mine" {
		t.Errorf("expected highest traverse count corridor first (mine, 15), got %s", fromHome[0].ID)
	}

	// LoadCorridors bulk load verification
	si2 := NewSpatialIndex()
	si2.LoadCorridors([]db.TrailCorridor{c1, c2})
	if len(si2.AllCorridors()) != 2 {
		t.Fatalf("expected 2 corridors loaded in si2, got %d", len(si2.AllCorridors()))
	}

	// Concurrent read/write stress test
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cor := db.TrailCorridor{
				ID:            fmt.Sprintf("cor_%d", idx),
				FromPOIID:     fmt.Sprintf("origin_%d", idx%3),
				ToPOIID:       fmt.Sprintf("dest_%d", idx),
				WaypointsJSON: "[]",
				TotalDistance: float64(idx * 5),
				TraverseCount: int64(idx),
			}
			si.UpsertCorridor(cor)
			_, _ = si.GetCorridor(cor.FromPOIID, cor.ToPOIID)
			_ = si.FindCorridorsFrom(cor.FromPOIID)
			_ = si.AllCorridors()
		}(i)
	}
	wg.Wait()
}

// TestHabitTrails_SequentialWaypointProgression verifies sequential multi-waypoint navigation,
// arrival proximity advancing (<= 4m), and direct destination transition (<= 12m) (ADR-023).
func TestHabitTrails_SequentialWaypointProgression(t *testing.T) {
	tracker := NewHabitTrailTracker(8.0, 4.0, 12.0)

	w0 := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 0.0}
	w1 := &civquestv1.Vec3{X: 20.0, Y: 64.0, Z: 10.0}
	w2 := &civquestv1.Vec3{X: 30.0, Y: 64.0, Z: 20.0}
	finalTarget := &civquestv1.Vec3{X: 50.0, Y: 64.0, Z: 40.0}

	jsonStr, _ := EncodeWaypointsJSON([]*civquestv1.Vec3{w0, w1, w2})
	corridor := &db.TrailCorridor{
		ID:            "corridor_origin_target",
		FromPOIID:     "poi-origin",
		ToPOIID:       "poi-target",
		WaypointsJSON: jsonStr,
		TotalDistance: 65.0,
		TraverseCount: 1,
	}

	// 1. Start corridor navigation
	intent, err := tracker.StartCorridorNavigation("agent-1", corridor, finalTarget)
	if err != nil || intent == nil || intent.GetNavigate() == nil {
		t.Fatalf("StartCorridorNavigation failed: %v", err)
	}
	nav := intent.GetNavigate()
	if nav.Target.X != w0.X || nav.Target.Y != w0.Y || nav.Target.Z != w0.Z {
		t.Fatalf("expected first waypoint targeting W0 (10, 64, 0), got %+v", nav.Target)
	}
	if !tracker.IsNavigatingCorridor() {
		t.Fatalf("expected tracker to report navigating corridor")
	}

	// 2. Far from W0 -> no progression
	farPos := &civquestv1.Vec3{X: 0.0, Y: 64.0, Z: 0.0}
	next, advanced := tracker.UpdateNavigation(farPos, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if advanced || next != nil {
		t.Fatalf("expected no advancement when far from W0, got advanced=%t, next=%+v", advanced, next)
	}
	if tracker.CurrentWaypointIndex() != 0 {
		t.Errorf("expected waypoint index 0, got %d", tracker.CurrentWaypointIndex())
	}

	// 3. Proximity to W0 (distance <= 4m, e.g. at (9.0, 64.0, 0.0), dist=1.0m)
	nearW0 := &civquestv1.Vec3{X: 9.0, Y: 64.0, Z: 0.0}
	next, advanced = tracker.UpdateNavigation(nearW0, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if !advanced || next == nil || next.GetNavigate() == nil {
		t.Fatalf("expected advancement to W1 near W0, got advanced=%t, next=%+v", advanced, next)
	}
	if next.GetNavigate().Target.X != w1.X || next.GetNavigate().Target.Z != w1.Z {
		t.Fatalf("expected target W1 (20, 64, 10), got %+v", next.GetNavigate().Target)
	}
	if tracker.CurrentWaypointIndex() != 1 {
		t.Errorf("expected waypoint index 1, got %d", tracker.CurrentWaypointIndex())
	}

	// 4. Proximity to W1 (distance <= 4m, e.g. at (19.0, 64.0, 10.0))
	nearW1 := &civquestv1.Vec3{X: 19.0, Y: 64.0, Z: 10.0}
	next, advanced = tracker.UpdateNavigation(nearW1, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if !advanced || next == nil || next.GetNavigate() == nil {
		t.Fatalf("expected advancement to W2 near W1, got advanced=%t, next=%+v", advanced, next)
	}
	if next.GetNavigate().Target.X != w2.X || next.GetNavigate().Target.Z != w2.Z {
		t.Fatalf("expected target W2 (30, 64, 20), got %+v", next.GetNavigate().Target)
	}
	if tracker.CurrentWaypointIndex() != 2 {
		t.Errorf("expected waypoint index 2, got %d", tracker.CurrentWaypointIndex())
	}

	// 5. Proximity to final waypoint W2 -> transition to direct destination!
	nearW2 := &civquestv1.Vec3{X: 29.5, Y: 64.0, Z: 20.0}
	next, advanced = tracker.UpdateNavigation(nearW2, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if !advanced || next == nil || next.GetNavigate() == nil {
		t.Fatalf("expected transition to direct destination near final waypoint W2")
	}
	if next.GetNavigate().Target.X != finalTarget.X || next.GetNavigate().Target.Z != finalTarget.Z {
		t.Fatalf("expected target direct destination %+v, got %+v", finalTarget, next.GetNavigate().Target)
	}
	if tracker.IsNavigatingCorridor() {
		t.Fatalf("expected corridor navigation to end upon reaching direct destination transition")
	}
}

// TestHabitTrails_FinalDestinationTransitionProximity verifies that when the agent is
// within 12m of the final target, it directly transitions to destination arrival regardless
// of remaining intermediate waypoints (ADR-023).
func TestHabitTrails_FinalDestinationTransitionProximity(t *testing.T) {
	tracker := NewHabitTrailTracker(8.0, 4.0, 12.0)

	w0 := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 0.0}
	w1 := &civquestv1.Vec3{X: 20.0, Y: 64.0, Z: 0.0}
	finalTarget := &civquestv1.Vec3{X: 25.0, Y: 64.0, Z: 0.0}

	jsonStr, _ := EncodeWaypointsJSON([]*civquestv1.Vec3{w0, w1})
	corridor := &db.TrailCorridor{
		ID:            "corridor_short",
		FromPOIID:     "poi-a",
		ToPOIID:       "poi-b",
		WaypointsJSON: jsonStr,
		TotalDistance: 25.0,
		TraverseCount: 1,
	}

	_, err := tracker.StartCorridorNavigation("agent-1", corridor, finalTarget)
	if err != nil {
		t.Fatalf("StartCorridorNavigation failed: %v", err)
	}

	// Agent position is at (15.0, 64.0, 0.0), which is 10m away from finalTarget (25.0, 64.0, 0.0) <= 12m
	closePos := &civquestv1.Vec3{X: 15.0, Y: 64.0, Z: 0.0}
	next, advanced := tracker.UpdateNavigation(closePos, civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if !advanced || next == nil || next.GetNavigate() == nil {
		t.Fatalf("expected transition to direct destination within 12m")
	}
	if next.GetNavigate().Target.X != finalTarget.X {
		t.Errorf("expected target %f, got %f", finalTarget.X, next.GetNavigate().Target.X)
	}
	if tracker.IsNavigatingCorridor() {
		t.Errorf("expected corridor navigation complete")
	}
}

// TestHabitTrails_BreadcrumbSamplingAndConsolidation verifies breadcrumb recording
// every 8-12m, RDP simplification, deterministic corridor ID construction,
// and traverse count incrementation (ADR-023).
func TestHabitTrails_BreadcrumbSamplingAndConsolidation(t *testing.T) {
	si := NewSpatialIndex()
	tracker := NewHabitTrailTracker(8.0, 4.0, 12.0)

	originPos := &civquestv1.Vec3{X: 0.0, Y: 64.0, Z: 0.0}
	destPos := &civquestv1.Vec3{X: 50.0, Y: 64.0, Z: 50.0}

	tracker.SetOriginPOI("poi-settlement", originPos)
	tracker.SetDestinationPOI("poi-outpost", destPos)

	// Small moves (< 8m) should NOT record breadcrumbs
	if tracker.SampleBreadcrumb(&civquestv1.Vec3{X: 3.0, Y: 64.0, Z: 3.0}) {
		t.Errorf("expected no breadcrumb recorded for small displacement (< 8m)")
	}

	// Move >= 8m: (8.0, 64.0, 8.0) distance from origin is ~11.3m
	if !tracker.SampleBreadcrumb(&civquestv1.Vec3{X: 8.0, Y: 64.0, Z: 8.0}) {
		t.Errorf("expected breadcrumb recorded for displacement >= 8m")
	}

	// Next small move from last sampled: (10.0, 64.0, 10.0) -> dist ~2.8m -> no breadcrumb
	if tracker.SampleBreadcrumb(&civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0}) {
		t.Errorf("expected no breadcrumb for incremental move < 8m")
	}

	// Move another 10m: (18.0, 64.0, 18.0)
	if !tracker.SampleBreadcrumb(&civquestv1.Vec3{X: 18.0, Y: 64.0, Z: 18.0}) {
		t.Errorf("expected second breadcrumb recorded")
	}

	// Move another 15m: (30.0, 64.0, 30.0)
	if !tracker.SampleBreadcrumb(&civquestv1.Vec3{X: 30.0, Y: 64.0, Z: 30.0}) {
		t.Errorf("expected third breadcrumb recorded")
	}

	if len(tracker.Breadcrumbs()) != 3 {
		t.Fatalf("expected 3 recorded breadcrumbs, got %d", len(tracker.Breadcrumbs()))
	}

	// Consolidate corridor
	corridor, ok := tracker.ConsolidateCorridor(si, 1.0)
	if !ok || corridor == nil {
		t.Fatalf("expected corridor consolidation to succeed")
	}

	expectedID := "corridor_poi-settlement_poi-outpost"
	if corridor.ID != expectedID {
		t.Errorf("expected deterministic corridor ID %q, got %q", expectedID, corridor.ID)
	}
	if corridor.TraverseCount != 1 {
		t.Errorf("expected initial traverse count 1, got %d", corridor.TraverseCount)
	}
	if corridor.TotalDistance <= 0 {
		t.Errorf("expected positive total distance, got %f", corridor.TotalDistance)
	}

	// Verify corridor was indexed into SpatialIndex
	inIndex, found := si.GetCorridor("poi-settlement", "poi-outpost")
	if !found || inIndex == nil {
		t.Fatalf("expected corridor in SpatialIndex after consolidation")
	}
	if inIndex.ID != expectedID {
		t.Errorf("SpatialIndex corridor ID mismatch: %s", inIndex.ID)
	}

	// Second traversal consolidation: verify traverse count increments
	corridor2, ok2 := tracker.ConsolidateCorridor(si, 1.0)
	if !ok2 || corridor2 == nil {
		t.Fatalf("expected second consolidation to succeed")
	}
	if corridor2.TraverseCount != 2 {
		t.Errorf("expected traverse count incremented to 2, got %d", corridor2.TraverseCount)
	}
}
