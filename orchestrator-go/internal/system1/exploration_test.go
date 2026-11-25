package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestResolveAndFormatDayPhase(t *testing.T) {
	cases := []struct {
		tod      int64
		wantEnum DayPhase
	}{
		{0, DayPhaseDawn},
		{23800, DayPhaseDawn},
		{1500, DayPhaseMorning},
		{6000, DayPhaseMidday},
		{9000, DayPhaseAfternoon},
		{12000, DayPhaseDusk},
		{15000, DayPhaseNight},
	}

	for _, tc := range cases {
		got := ResolveDayPhase(tc.tod)
		if got != tc.wantEnum {
			t.Errorf("ResolveDayPhase(%d) = %s, want %s", tc.tod, got, tc.wantEnum)
		}
		if FormatDayPhase(tc.tod) == "" {
			t.Errorf("FormatDayPhase(%d) returned empty string", tc.tod)
		}
	}
}

func TestEvaluateTopologicalMappingAndExploration(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	node1, edge1 := EvaluateTopologicalMapping(frame1, state)
	if node1 == nil || node1.Category != "SETTLEMENT" {
		t.Fatalf("expected initial SETTLEMENT node, got %+v", node1)
	}
	if edge1 != nil {
		t.Fatalf("expected nil edge on first node, got %+v", edge1)
	}

	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 1120,
		TimeOfDay: 2120,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 16, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	node2, edge2 := EvaluateTopologicalMapping(frame2, state)
	if node2 == nil || edge2 == nil {
		t.Fatalf("expected second node and connecting edge, got node=%+v edge=%+v", node2, edge2)
	}

	// Test terrain unsticking when NAVIGATION_STATE_STUCK
	_ = EvaluateExploration("Able", frame1, cfg, state, nil)
	stuckFrame := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 2200,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 16, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
	}
	detourIntent := EvaluateExploration("Able", stuckFrame, cfg, state, nil)
	if detourIntent == nil || detourIntent.GetNavigate() == nil {
		t.Fatalf("expected terrain detour navigate intent when STUCK, got %+v", detourIntent)
	}
}

func TestEvaluateExplorationBackwardCompatibility(t *testing.T) {
	state := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("Able")
	cfg.Profession = "EXPLORER"

	warmupFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			Health:          20,
			FoodLevel:       20,
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	walk := &walkLocal{}
	_ = EvaluateExploration("Able", warmupFrame, cfg, state, walk)

	frameExplore := &civquestv1.TelemetryFrame{
		WorldTick: 200,
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
	// Explorers start an expedition frame (ADR-032/036): the evaluator only asks the running
	// activity to push it (the frames themselves are covered by the TestExpedition_* tests).
	if intent := EvaluateExploration("Able", frameExplore, cfg, state, walk); intent != nil {
		t.Fatalf("expected no direct intent from EvaluateExploration, got %+v", intent)
	}
	if !walk.wantExpedition {
		t.Fatal("expected EvaluateExploration to request an expedition frame for an explorer")
	}
}
