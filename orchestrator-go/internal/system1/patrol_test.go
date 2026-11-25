package system1

import (
	"math"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestNonExplorerRotatesAzimuthAfterLegs(t *testing.T) {
	state := NewAgentState()
	walk := &walkLocal{}
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "CHRONICLER" // Non-explorer profession
	cfg.Traits.Curiosity = 0.5    // legStep = 16.0 + 0.5*8.0 = 20.0

	// Warm-up to establish home origin
	warmupFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	_ = EvaluatePatrol("Able", warmupFrame, cfg, state, walk)

	// Tick 1000: Initial patrol target (Leg 0: 12m)
	frame0 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent0 := EvaluatePatrol("Able", frame0, cfg, state, walk)
	if intent0 == nil || intent0.GetNavigate() == nil {
		t.Fatalf("expected initial NavigateIntent, got %+v", intent0)
	}
	initialAz, _ := walk.frontierAzimuth()
	target0 := intent0.GetNavigate().GetTarget()
	dist0 := math.Hypot(target0.X, target0.Z)
	if math.Abs(dist0-12.0) > 0.5 {
		t.Fatalf("expected leg 0 target ~12m, got %.2f", dist0)
	}

	// Arrive at Leg 0 -> Advances to Leg 1 (32m) along same azimuth
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 2200,
		Status: &civquestv1.AgentStatus{
			Position:        target0,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent1 := EvaluatePatrol("Able", frame1, cfg, state, walk)
	if intent1 == nil || intent1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 1, got %+v", intent1)
	}
	target1 := intent1.GetNavigate().GetTarget()
	dist1 := math.Hypot(target1.X, target1.Z)
	if math.Abs(dist1-32.0) > 0.5 {
		t.Fatalf("expected leg 1 target ~32m, got %.2f", dist1)
	}
	az1, _ := walk.frontierAzimuth()
	if math.Abs(math.Atan2(math.Sin(az1-initialAz), math.Cos(az1-initialAz))) > 0.001 {
		t.Fatalf("expected Leg 1 to keep same azimuth, got %.4f vs %.4f", az1, initialAz)
	}

	// Arrive at Leg 1 -> Advances to Leg 2 (52m) along same azimuth
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1400,
		TimeOfDay: 2300,
		Status: &civquestv1.AgentStatus{
			Position:        target1,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent2 := EvaluatePatrol("Able", frame2, cfg, state, walk)
	if intent2 == nil || intent2.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 2, got %+v", intent2)
	}
	target2 := intent2.GetNavigate().GetTarget()
	dist2 := math.Hypot(target2.X, target2.Z)
	if math.Abs(dist2-52.0) > 0.5 {
		t.Fatalf("expected leg 2 target ~52m, got %.2f", dist2)
	}

	// Arrive at Leg 2 -> Leg completion! Must rotate azimuth (+math.Pi/4) and reset radial leg to 0
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick: 1600,
		TimeOfDay: 2400,
		Status: &civquestv1.AgentStatus{
			Position:        target2,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
			},
		},
	}
	intent3 := EvaluatePatrol("Able", frame3, cfg, state, walk)
	if intent3 == nil || intent3.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent after completing Leg 2, got %+v", intent3)
	}
	newAz, _ := walk.frontierAzimuth()
	expectedAz := math.Atan2(math.Sin(initialAz+math.Pi/4), math.Cos(initialAz+math.Pi/4))
	azDiff := math.Abs(math.Atan2(math.Sin(newAz-expectedAz), math.Cos(newAz-expectedAz)))
	if azDiff > 0.01 {
		t.Fatalf("expected azimuth to rotate by +45 deg (Pi/4) to %.4f, got %.4f (diff: %.4f)", expectedAz, newAz, azDiff)
	}
	if walk.frontierRadialLeg != 0 {
		t.Fatalf("expected frontierRadialLeg to reset to 0 after completing legs, got %d", walk.frontierRadialLeg)
	}
}

func TestEvaluatePatrolLandmarkWatch(t *testing.T) {
	state := NewAgentState()
	walk := &walkLocal{}
	cfg := xdg.DefaultAgentIdentityConfig("Guard")
	cfg.Profession = "GUARD"

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 100, Y: 64, Z: 200},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		CurrentLandmark: &civquestv1.LandmarkContext{
			DisplayName: "Village Market Square",
			Category:    "SETTLEMENT",
		},
	}

	// Warmup
	_ = EvaluatePatrol("Guard", frame, cfg, state, walk)

	frame.WorldTick = 400
	intent := EvaluatePatrol("Guard", frame, cfg, state, walk)
	if intent == nil || intent.GetNavigate() == nil {
		t.Fatalf("expected patrol NavigateIntent within landmark perimeter, got %+v", intent)
	}

	target := intent.GetNavigate().Target
	// Target should patrol near the landmark center
	dist := math.Hypot(target.X-100, target.Z-200)
	if dist > 50.0 {
		t.Fatalf("expected patrol target within perimeter of landmark (<= 50m), got %.2f", dist)
	}
}

func TestEvaluatePatrolDirect(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Baker")
	cfg.Profession = "BAKER"

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	pos := &civquestv1.Vec3{X: 10, Y: 64, Z: 10}

	start := time.Now()
	_ = EvaluatePatrolDirect("Baker", frame, pos, state, cfg, nil)
	duration := time.Since(start)

	// Invariant: Sub-5ms execution budget
	if duration > 5*time.Millisecond {
		t.Errorf("EvaluatePatrolDirect took %v, exceeded 5ms budget", duration)
	}
}
