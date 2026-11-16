package system1

import (
	"math"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	stuckWithObstruction := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 62, Z: 10.5},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 62, Z: 10},
				BlockId: "minecraft:dirt",
			},
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 63, Z: 10},
				BlockId: "minecraft:dirt",
			},
		},
	}

	intent := EvaluateScouting("Able", stuckWithObstruction, cfg, state)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected InteractBlockIntent when STUCK next to dirt wall, got %+v", intent)
	}
	ib := intent.GetInteractBlock()
	if ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK on obstructing block, got %v", ib.InteractionType)
	}
	if ib.Pos.GetY() != 63 {
		t.Fatalf("expected head-height block (Y=63) to be broken first to form a 1-block step-up, got Y=%d", ib.Pos.GetY())
	}
}

func TestSectorLockedRadialFrontierScouting_NoOscillationAndChainedOutward(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Frame 1: Warm-up telemetry
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	_ = EvaluateScouting("Able", frame1, cfg, state)

	// Frame 2: Start exploration at tick 1000 at origin (0, 64, 0)
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}

	intent0 := EvaluateScouting("Able", frame2, cfg, state)
	if intent0 == nil || intent0.GetNavigate() == nil {
		t.Fatalf("expected initial NavigateIntent, got %+v", intent0)
	}
	target0 := intent0.GetNavigate().GetTarget()
	dist0 := math.Hypot(target0.X, target0.Z)
	if math.Abs(dist0-48.0) > 1.0 {
		t.Fatalf("expected Leg 0 target at ~48m, got %.2fm (target: %+v)", dist0, target0)
	}
	azimuth0 := math.Atan2(target0.Z, target0.X)

	// Step 2: Simulate travel toward target over 3-second intervals (60 ticks each)
	// Actively navigating (PATHING) with forward progress: must NOT oscillate or re-issue intent
	ticks := []int64{1060, 1120, 1180}
	dists := []float64{15.0, 30.0, 45.0}
	for i, tick := range ticks {
		stepPos := &civquestv1.Vec3{
			X: math.Cos(azimuth0) * dists[i],
			Y: 64,
			Z: math.Sin(azimuth0) * dists[i],
		}
		pathingFrame := &civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 2100 + (tick - 1000),
			Status: &civquestv1.AgentStatus{
				Position:        stepPos,
				Health:          20,
				FoodLevel:       20,
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
				Inventory: []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
					{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
					{Slot: 2, ItemId: "minecraft:torch", Count: 8},
				},
			},
		}
		midIntent := EvaluateScouting("Able", pathingFrame, cfg, state)
		if midIntent != nil {
			t.Fatalf("expected nil intent while actively pathing at tick %d (eliminated 3-second oscillation), got %+v", tick, midIntent)
		}
		curAz, _ := state.GetFrontierAzimuth()
		if math.Abs(math.Atan2(math.Sin(curAz-azimuth0), math.Cos(curAz-azimuth0))) > 0.001 {
			t.Fatalf("azimuth changed during pathing: was %.4f, now %.4f", azimuth0, curAz)
		}
	}

	// Step 3: Arrival at Leg 0 (~48m, within 2m tolerance)
	arrivePos0 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 47.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 47.5,
	}
	arriveFrame0 := &civquestv1.TelemetryFrame{
		WorldTick: 1220,
		TimeOfDay: 2320,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos0,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent1 := EvaluateScouting("Able", arriveFrame0, cfg, state)
	if intent1 == nil || intent1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 1 upon arrival, got %+v", intent1)
	}
	target1 := intent1.GetNavigate().GetTarget()
	dist1 := math.Hypot(target1.X, target1.Z)
	if math.Abs(dist1-96.0) > 1.0 {
		t.Fatalf("expected Leg 1 target chained outward beyond 64m to ~96m, got %.2fm (target: %+v)", dist1, target1)
	}
	azimuth1 := math.Atan2(target1.Z, target1.X)
	if math.Abs(math.Atan2(math.Sin(azimuth1-azimuth0), math.Cos(azimuth1-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 1 to maintain same sector azimuth (%.4f), got %.4f (angular oscillation detected)", azimuth0, azimuth1)
	}

	// Step 4: Arrival at Leg 1 (~96m) -> Advances to Leg 2 (~144m)
	arrivePos1 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 95.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 95.5,
	}
	arriveFrame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1400,
		TimeOfDay: 2500,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos1,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent2 := EvaluateScouting("Able", arriveFrame1, cfg, state)
	if intent2 == nil || intent2.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 2 upon arrival, got %+v", intent2)
	}
	target2 := intent2.GetNavigate().GetTarget()
	dist2 := math.Hypot(target2.X, target2.Z)
	if math.Abs(dist2-144.0) > 1.0 {
		t.Fatalf("expected Leg 2 target chained to ~144m, got %.2fm", dist2)
	}
	azimuth2 := math.Atan2(target2.Z, target2.X)
	if math.Abs(math.Atan2(math.Sin(azimuth2-azimuth0), math.Cos(azimuth2-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 2 to maintain same sector azimuth, got %.4f vs %.4f", azimuth2, azimuth0)
	}

	// Step 5: Arrival at Leg 2 (~144m) -> Advances to Leg 3 (~192m)
	arrivePos2 := &civquestv1.Vec3{
		X: math.Cos(azimuth0) * 143.5,
		Y: 64,
		Z: math.Sin(azimuth0) * 143.5,
	}
	arriveFrame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1600,
		TimeOfDay: 2700,
		Status: &civquestv1.AgentStatus{
			Position:        arrivePos2,
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent3 := EvaluateScouting("Able", arriveFrame2, cfg, state)
	if intent3 == nil || intent3.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent for Leg 3 upon arrival, got %+v", intent3)
	}
	target3 := intent3.GetNavigate().GetTarget()
	dist3 := math.Hypot(target3.X, target3.Z)
	if math.Abs(dist3-192.0) > 1.0 {
		t.Fatalf("expected Leg 3 target chained to ~192m, got %.2fm", dist3)
	}
	azimuth3 := math.Atan2(target3.Z, target3.X)
	if math.Abs(math.Atan2(math.Sin(azimuth3-azimuth0), math.Cos(azimuth3-azimuth0))) > 0.001 {
		t.Fatalf("expected Leg 3 to maintain same sector azimuth, got %.4f vs %.4f", azimuth3, azimuth0)
	}
}

func TestSectorLockedRadialFrontierScouting_TimeoutPivotsSector(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	// Warm-up
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:    20,
			FoodLevel: 20,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	_ = EvaluateScouting("Able", frame1, cfg, state)

	// Initial start at tick 1000
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	intent0 := EvaluateScouting("Able", frame2, cfg, state)
	if intent0 == nil {
		t.Fatal("expected initial intent")
	}
	initialAz, _ := state.GetFrontierAzimuth()

	// Zero progress for 30s (600 ticks, at tick 1650)
	stalledFrame := &civquestv1.TelemetryFrame{
		WorldTick: 1650,
		TimeOfDay: 2750,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0}, // No movement from origin
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
				{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
				{Slot: 2, ItemId: "minecraft:torch", Count: 8},
			},
		},
	}
	pivotIntent := EvaluateScouting("Able", stalledFrame, cfg, state)
	if pivotIntent == nil || pivotIntent.GetNavigate() == nil {
		t.Fatalf("expected new NavigateIntent upon leg timeout, got %+v", pivotIntent)
	}
	newAz, _ := state.GetFrontierAzimuth()
	if math.Abs(math.Atan2(math.Sin(newAz-initialAz), math.Cos(newAz-initialAz))) < 0.1 {
		t.Fatalf("expected sector azimuth to pivot after 30s zero-progress timeout, but azimuth stayed %.4f", initialAz)
	}
	if state.GetFrontierRadialLeg() != 0 {
		t.Fatalf("expected radial leg to reset to 0 in new sector, got %d", state.GetFrontierRadialLeg())
	}
}

func TestEvaluateScoutingDirect(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	pos := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}

	start := time.Now()
	_ = EvaluateScoutingDirect("Able", frame, pos, state, cfg)
	duration := time.Since(start)

	// Invariant: Sub-5ms execution budget
	if duration > 5*time.Millisecond {
		t.Errorf("EvaluateScoutingDirect took %v, exceeded 5ms budget", duration)
	}
}
