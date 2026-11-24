package system1

import (
	"math"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestEvaluateUnstuck_PitEntrapment(t *testing.T) {
	// Agent is trapped in a 2-block pit at (10.5, 64.0, 10.5)
	// Floor is Y=63.
	// Wall to the East (X=11):
	// - (11, 64, 10): dirt (foot level)
	// - (11, 65, 10): dirt (eye level)
	// - (11, 66, 10): dirt (headroom of step)
	// Wall to the West (X=9), North (Z=9), South (Z=11) also 2-3 blocks high.
	state := NewAgentState()
	state.lastHeadingRad = 0.0 // East (+X)

	pos := &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
			NavigationEnclosed: true,
			Position:           pos,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 66, Z: 10}, BlockId: "minecraft:dirt"},
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 65, Z: 10}, BlockId: "minecraft:dirt"},
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:dirt"},
			{Pos: &civquestv1.BlockPosProto{X: 9, Y: 65, Z: 10}, BlockId: "minecraft:stone"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 9}, BlockId: "minecraft:stone"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 11}, BlockId: "minecraft:stone"},
		},
	}

	// 1. First evaluation: should lock East (+X) and break eye level (11, 65, 10) to form a 1-block step
	intent := EvaluateUnstuck("agent-test", frame, pos, state)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected InteractBlockIntent for eye level, got %+v", intent)
	}
	ib := intent.GetInteractBlock()
	if ib.Pos.X != 11 || ib.Pos.Y != 65 || ib.Pos.Z != 10 {
		t.Errorf("expected break at (11, 65, 10), got (%d, %d, %d)", ib.Pos.X, ib.Pos.Y, ib.Pos.Z)
	}
	if ib.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Errorf("expected HARVEST_BREAK, got %v", ib.InteractionType)
	}

	locked, dirX, dirZ, _, _, _ := state.GetUnstuckLock()
	if !locked || dirX != 1 || dirZ != 0 {
		t.Errorf("expected locked East (+1, 0), got locked=%t, dir=(%d, %d)", locked, dirX, dirZ)
	}

	// 2. Eye level (11, 65, 10) broken: next should break step headroom (11, 66, 10)
	frame.WorldTick = 125
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 66, Z: 10}, BlockId: "minecraft:dirt"},
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:dirt"},
	}

	intent2 := EvaluateUnstuck("agent-test", frame, pos, state)
	if intent2 == nil || intent2.GetInteractBlock() == nil {
		t.Fatalf("expected InteractBlockIntent for headroom, got %+v", intent2)
	}
	ib2 := intent2.GetInteractBlock()
	if ib2.Pos.X != 11 || ib2.Pos.Y != 66 || ib2.Pos.Z != 10 {
		t.Errorf("expected break at (11, 66, 10), got (%d, %d, %d)", ib2.Pos.X, ib2.Pos.Y, ib2.Pos.Z)
	}

	// 3. Eye level broken: (11, 64, 10) remains as a 1-block elevated step!
	// Step headroom (11, 66) is air, step eye (11, 65) is air.
	// Step 2 headroom (12, 66) is air.
	// Should emit NavigateIntent to step up onto (11.5, 65.0, 10.5)!
	frame.WorldTick = 150
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:dirt"},
	}

	intent3 := EvaluateUnstuck("agent-test", frame, pos, state)
	if intent3 == nil || intent3.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent to step up, got %+v", intent3)
	}
	nav := intent3.GetNavigate()
	if nav.Target.X != 11.5 || nav.Target.Y != 65.0 || nav.Target.Z != 10.5 {
		t.Errorf("expected navigate target (11.5, 65.0, 10.5), got (%f, %f, %f)", nav.Target.X, nav.Target.Y, nav.Target.Z)
	}
}

func TestEvaluateUnstuck_CeilingOverheadClearance(t *testing.T) {
	// Agent standing under a low ceiling at (10.5, 64.0, 10.5)
	// Solid block at (10, 66, 10) prevents jumping up
	state := NewAgentState()
	pos := &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
			Position:        pos,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 66, Z: 10}, BlockId: "minecraft:stone"},
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 65, Z: 10}, BlockId: "minecraft:dirt"},
		},
	}

	intent := EvaluateUnstuck("agent-test", frame, pos, state)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected break intent for overhead ceiling, got %+v", intent)
	}
	ib := intent.GetInteractBlock()
	if ib.Pos.X != 10 || ib.Pos.Y != 66 || ib.Pos.Z != 10 {
		t.Errorf("expected break overhead ceiling (10, 66, 10), got (%d, %d, %d)", ib.Pos.X, ib.Pos.Y, ib.Pos.Z)
	}
}

func TestEvaluateUnstuck_MovementUnlocks(t *testing.T) {
	state := NewAgentState()
	state.SetUnstuckLock(true, 1, 0, 100, 10.0, 10.0)

	// Agent moved to (12.0, 64.0, 10.0) and is IDLE
	pos := &civquestv1.Vec3{X: 12.0, Y: 64.0, Z: 10.0}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 120,
		Status: &civquestv1.AgentStatus{
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Position:        pos,
		},
	}

	intent := EvaluateUnstuck("agent-test", frame, pos, state)
	if intent != nil {
		t.Errorf("expected nil intent for free agent, got %+v", intent)
	}

	locked, _, _, _, _, _ := state.GetUnstuckLock()
	if locked {
		t.Errorf("expected unstuck lock to be cleared after movement")
	}
}

func TestEvaluateBootstrapTimber_TreeTrunkFocus(t *testing.T) {
	// Agent at (10.0, 64.0, 10.0) needs wood
	// Tree at (12, 10) with trunk logs at Y=64 and Y=65
	// Another tree further away at (15, 10)
	state := NewAgentState()
	pos := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Position:        pos,
			Inventory:       nil, // 0 logs
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 12, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 15, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
		},
	}

	// First break on tree trunk at (12, 10)
	intent := EvaluateBootstrapGoalsDirect("able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state)
	if intent == nil || intent.GetInteractBlock() == nil {
		t.Fatalf("expected harvest intent on nearest log, got %+v", intent)
	}

	treeLocked, tx, tz, _ := state.GetTargetTree()
	if !treeLocked || tx != 12 || tz != 10 {
		t.Errorf("expected tree focus locked on (12, 10), got locked=%t, (%d, %d)", treeLocked, tx, tz)
	}

	// Bottom log felled; upper log at (12, 65, 10) remains
	frame.WorldTick = 225
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 12, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 15, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
	}

	intent2 := EvaluateBootstrapGoalsDirect("able", frame, pos, civquestv1.NavigationState_NAVIGATION_STATE_IDLE, state)
	if intent2 == nil || intent2.GetInteractBlock() == nil {
		t.Fatalf("expected harvest intent on remaining trunk log, got %+v", intent2)
	}
	ib2 := intent2.GetInteractBlock()
	if ib2.Pos.X != 12 || ib2.Pos.Y != 65 || ib2.Pos.Z != 10 {
		t.Errorf("expected focus on same tree trunk (12, 65, 10), got (%d, %d, %d)", ib2.Pos.X, ib2.Pos.Y, ib2.Pos.Z)
	}
}

func TestEvaluateUnstuck_CooldownAngularFanOutAndActiveTaskSuppression(t *testing.T) {
	// 1. Cooldown >= 25 ticks & 2.5-3.0 block angular fan-out across peer IDs on flat ground
	stateA := NewAgentState()
	stateB := NewAgentState()
	pos := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0}
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
			Position:        pos,
		},
	}

	inA1 := EvaluateUnstuck("able", frame, pos, stateA)
	inB1 := EvaluateUnstuck("carl", frame, pos, stateB)
	if inA1 == nil || inA1.GetNavigate() == nil || inB1 == nil || inB1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntents for able and carl, got A=%+v B=%+v", inA1, inB1)
	}
	tA := inA1.GetNavigate().Target
	tB := inB1.GetNavigate().Target
	distA := math.Hypot(tA.X-pos.X, tA.Z-pos.Z)
	distB := math.Hypot(tB.X-pos.X, tB.Z-pos.Z)
	if distA < 2.0 || distA > 3.5 || distB < 2.0 || distB > 3.5 {
		t.Fatalf("expected 2.5-3.0 block unstuck step, got distA=%.2f distB=%.2f", distA, distB)
	}
	if math.Hypot(tA.X-tB.X, tA.Z-tB.Z) < 0.5 {
		t.Fatalf("expected distinct angular fan-out targets for able vs carl, got A=(%.1f,%.1f) B=(%.1f,%.1f)",
			tA.X, tA.Z, tB.X, tB.Z)
	}

	// Within 25-tick cooldown, EvaluateUnstuck must suppress duplicate NavigateIntents
	frame.WorldTick = 112
	if dup := EvaluateUnstuck("able", frame, pos, stateA); dup != nil {
		t.Fatalf("expected nil within 25-tick unstuck cooldown, got %+v", dup)
	}

	// After >= 25 ticks, next unstuck rotates angle
	frame.WorldTick = 125
	inA2 := EvaluateUnstuck("able", frame, pos, stateA)
	if inA2 == nil || inA2.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent after 25-tick cooldown, got %+v", inA2)
	}
	tA2 := inA2.GetNavigate().Target
	if math.Hypot(tA.X-tA2.X, tA.Z-tA2.Z) < 0.5 {
		t.Fatalf("expected rotated angular offset on subsequent attempt, got same target (%.1f,%.1f)", tA2.X, tA2.Z)
	}

	// 2. When active task is running and NOT enclosed, EvaluateUnstuck must NOT mask task stuck receipt
	stateTask := NewAgentState()
	stateTask.StartTask(NewHarvestTreeTask(20, 64, 0, StageBootstrap, 100))
	if masked := EvaluateUnstuck("able", frame, pos, stateTask); masked != nil {
		t.Fatalf("expected nil unstuck intent when active non-enclosed task is handling stuck, got %+v", masked)
	}
}
