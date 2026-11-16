package system1

import (
	"math"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestNavigationEvaluator_Registration(t *testing.T) {
	eval := NewNavigationEvaluator()
	if eval.Name() != "navigation" {
		t.Errorf("expected name 'navigation', got %q", eval.Name())
	}
	if eval.Category() != "navigation" {
		t.Errorf("expected category 'navigation', got %q", eval.Category())
	}
}

func TestInfrastructureEvaluator_Registration(t *testing.T) {
	eval := NewInfrastructureEvaluator()
	if eval.Name() != "infrastructure" {
		t.Errorf("expected name 'infrastructure', got %q", eval.Name())
	}
	if eval.Category() != "infrastructure" {
		t.Errorf("expected category 'infrastructure', got %q", eval.Category())
	}
}

func TestNavigationEvaluator_PatrolFacts(t *testing.T) {
	eval := NewNavigationEvaluator()

	// 1. Guard profession sets patrol.active = true
	graph := NewFactGraph()
	cfgGuard := &xdg.AgentIdentityConfig{
		Profession: "GUARD",
	}
	state := NewAgentState()
	ctx := &EvaluatorContext{
		AgentID: "guard-1",
		Config:  cfgGuard,
		State:   state,
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("patrol.active") {
		t.Errorf("expected patrol.active true for GUARD profession")
	}

	// 2. Farmer profession with sentry watch off -> patrol.active = false
	graph = NewFactGraph()
	cfgFarmer := &xdg.AgentIdentityConfig{
		Profession: "FARMER",
	}
	ctxFarmer := &EvaluatorContext{
		AgentID: "farmer-1",
		Config:  cfgFarmer,
		State:   state,
	}
	if err := eval.Evaluate(ctxFarmer, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph.GetBool("patrol.active") {
		t.Errorf("expected patrol.active false for FARMER without active sentry watch")
	}

	// 3. Farmer with SetCivicPatrolActive(true) -> patrol.active = true
	state.SetCivicPatrolActive(true)
	graph = NewFactGraph()
	if err := eval.Evaluate(ctxFarmer, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !graph.GetBool("patrol.active") {
		t.Errorf("expected patrol.active true when civic patrol is explicitly activated")
	}

	// 4. Waypoint count from configured patrol waypoints
	state.SetPatrolWaypoints([]*civquestv1.Vec3{
		{X: 10, Y: 64, Z: 10},
		{X: 20, Y: 64, Z: 20},
		{X: 30, Y: 64, Z: 30},
	})
	state.SetPatrolWaypointIndex(1)
	graph = NewFactGraph()
	if err := eval.Evaluate(ctxFarmer, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wpCount, _ := graph.GetInt("patrol.waypoint_count")
	if wpCount != 3 {
		t.Errorf("expected patrol.waypoint_count 3, got %d", wpCount)
	}
	wpIdx, _ := graph.GetInt("patrol.waypoint_index")
	if wpIdx != 1 {
		t.Errorf("expected patrol.waypoint_index 1, got %d", wpIdx)
	}

	// 5. Waypoint count fallback from SafeNodes
	state.SetPatrolWaypoints(nil)
	ctxWithNodes := &EvaluatorContext{
		AgentID: "farmer-1",
		Config:  cfgFarmer,
		State:   state,
		SafeNodes: []db.MapNode{
			{ID: "node1", X: 10, Z: 10},
			{ID: "node2", X: 20, Z: 20},
		},
	}
	graph = NewFactGraph()
	if err := eval.Evaluate(ctxWithNodes, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wpCountSafe, _ := graph.GetInt("patrol.waypoint_count")
	if wpCountSafe != 2 {
		t.Errorf("expected patrol.waypoint_count 2 from SafeNodes, got %d", wpCountSafe)
	}
}

func TestNavigationEvaluator_ScoutingFacts(t *testing.T) {
	eval := NewNavigationEvaluator()

	// 1. Explorer profession sets scouting.active = true
	graph := NewFactGraph()
	cfgExplorer := &xdg.AgentIdentityConfig{
		Profession: "EXPLORER",
	}
	state := NewAgentState()
	state.SetFrontierRadialLeg(2)
	state.SetFrontierAzimuth(math.Pi / 2) // 90 degrees (East/South)

	ctx := &EvaluatorContext{
		AgentID: "explorer-1",
		Config:  cfgExplorer,
		State:   state,
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("scouting.active") {
		t.Errorf("expected scouting.active true for EXPLORER")
	}
	leg, _ := graph.GetInt("scouting.radial_leg")
	if leg != 2 {
		t.Errorf("expected scouting.radial_leg 2, got %d", leg)
	}
	azDeg, _ := graph.GetFloat("scouting.azimuth_deg")
	if math.Abs(azDeg-90.0) > 0.01 {
		t.Errorf("expected scouting.azimuth_deg ~90.0, got %f", azDeg)
	}

	// 2. StageColonize sets scouting.active = true even for Farmer
	cfgFarmer := &xdg.AgentIdentityConfig{
		Profession: "FARMER",
	}
	stateColonize := NewAgentState()
	stateColonize.SetStage(StageColonize)
	stateColonize.SetRoutine(RoutineSecondaryHobby)

	graph = NewFactGraph()
	ctxColonize := &EvaluatorContext{
		AgentID: "colonist-1",
		Config:  cfgFarmer,
		State:   stateColonize,
	}
	if err := eval.Evaluate(ctxColonize, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !graph.GetBool("scouting.active") {
		t.Errorf("expected scouting.active true for StageColonize")
	}

	// 3. Primary duty sets scouting.active = true
	statePrimary := NewAgentState()
	statePrimary.SetStage(StageSettlement)
	statePrimary.SetRoutine(RoutinePrimaryDuty)
	graph = NewFactGraph()
	ctxPrimary := &EvaluatorContext{
		AgentID: "worker-1",
		Config:  cfgFarmer,
		State:   statePrimary,
	}
	if err := eval.Evaluate(ctxPrimary, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !graph.GetBool("scouting.active") {
		t.Errorf("expected scouting.active true during PRIMARY_DUTY routine")
	}

	// 4. Non-explorer sleeping in settlement has scouting.active = false
	stateSleep := NewAgentState()
	stateSleep.SetStage(StageSettlement)
	stateSleep.SetRoutine(RoutineSleep)
	graph = NewFactGraph()
	ctxSleep := &EvaluatorContext{
		AgentID: "sleeper-1",
		Config:  cfgFarmer,
		State:   stateSleep,
	}
	if err := eval.Evaluate(ctxSleep, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph.GetBool("scouting.active") {
		t.Errorf("expected scouting.active false for sleeping farmer")
	}
}

func TestNavigationEvaluator_PavingFacts(t *testing.T) {
	eval := NewNavigationEvaluator()

	state := NewAgentState()
	tracker := state.GetPavingTracker()
	if tracker == nil {
		tracker = NewRoadPavingTracker(100, 10)
		state.SetPavingTracker(tracker)
	}

	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:           &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			NavigationEnclosed: false,
			IsSleeping:         false,
		},
	}

	ctx := &EvaluatorContext{
		AgentID:       "paver-1",
		Frame:         frame,
		State:         state,
		PavingTracker: tracker,
	}

	graph := NewFactGraph()
	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("paving.needs_pave") {
		t.Errorf("expected paving.needs_pave true when moving awake, not stuck, not enclosed")
	}
	if graph.GetBool("paving.home_origin_set") {
		t.Errorf("expected paving.home_origin_set false before setting origin")
	}

	// Set home origin
	tracker.SetHomeOrigin(&civquestv1.Vec3{X: 100, Y: 64, Z: 100})
	graph = NewFactGraph()
	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !graph.GetBool("paving.home_origin_set") {
		t.Errorf("expected paving.home_origin_set true after setting home origin")
	}

	// Sleeping agent -> needs_pave = false
	frame.Status.IsSleeping = true
	graph = NewFactGraph()
	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph.GetBool("paving.needs_pave") {
		t.Errorf("expected paving.needs_pave false when agent is sleeping")
	}

	// Stuck agent -> needs_pave = false
	frame.Status.IsSleeping = false
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
	graph = NewFactGraph()
	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph.GetBool("paving.needs_pave") {
		t.Errorf("expected paving.needs_pave false when agent is stuck")
	}

	// Enclosed agent -> needs_pave = false
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	frame.Status.NavigationEnclosed = true
	graph = NewFactGraph()
	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph.GetBool("paving.needs_pave") {
		t.Errorf("expected paving.needs_pave false when agent is enclosed")
	}
}

func TestInfrastructureEvaluator_PavingFacts(t *testing.T) {
	eval := NewInfrastructureEvaluator()
	graph := NewFactGraph()

	state := NewAgentState()
	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:           &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
			NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			NavigationEnclosed: false,
			IsSleeping:         false,
		},
	}
	ctx := &EvaluatorContext{
		AgentID:       "infra-1",
		Frame:         frame,
		State:         state,
		PavingTracker: state.GetPavingTracker(),
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("paving.needs_pave") {
		t.Errorf("expected paving.needs_pave true from InfrastructureEvaluator")
	}
}

func TestNavigationEvaluator_NilSafety(t *testing.T) {
	eval := NewNavigationEvaluator()
	if err := eval.Evaluate(nil, nil); err != nil {
		t.Errorf("expected no error for nil ctx and graph")
	}
	graph := NewFactGraph()
	if err := eval.Evaluate(&EvaluatorContext{}, graph); err != nil {
		t.Errorf("expected no error for empty EvaluatorContext")
	}
}

func TestNavigationEvaluator_Sub5msBenchmark(t *testing.T) {
	eval := NewNavigationEvaluator()
	state := NewAgentState()
	state.SetFrontierRadialLeg(3)
	state.SetFrontierAzimuth(1.23)
	state.SetCivicPatrolActive(true)

	tracker := state.GetPavingTracker()
	tracker.SetHomeOrigin(&civquestv1.Vec3{X: 0, Y: 64, Z: 0})

	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:           &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			NavigationEnclosed: false,
			IsSleeping:         false,
		},
	}
	ctx := &EvaluatorContext{
		AgentID:       "bench-agent",
		Frame:         frame,
		Config:        &xdg.AgentIdentityConfig{Profession: "GUARD"},
		State:         state,
		PavingTracker: tracker,
	}

	start := time.Now()
	iterations := 1000
	for i := 0; i < iterations; i++ {
		graph := NewFactGraph()
		_ = eval.Evaluate(ctx, graph)
	}
	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(iterations)

	if perOp > 5*time.Millisecond {
		t.Fatalf("NavigationEvaluator took %v per op, exceeded 5ms SLA", perOp)
	}
}
