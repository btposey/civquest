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
	_ = EvaluateExploration("Able", frame1, cfg, state)
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
	detourIntent := EvaluateExploration("Able", stuckFrame, cfg, state)
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
	_ = EvaluateExploration("Able", warmupFrame, cfg, state)

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
	intent := EvaluateExploration("Able", frameExplore, cfg, state)
	if intent == nil || intent.GetSurveyHorizon() == nil {
		t.Fatalf("expected initial SurveyHorizonIntent from EvaluateExploration (ADR-032), got %+v", intent)
	}

	// Simulate completed 360° HorizonSurveyReport -> next EvaluateExploration picks best bearing and emits NavigateIntent
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  float32(i * 10),
			HitDistance: 64.0,
			FeatureType: "OPEN_PLAINS",
			OpenHorizon: true,
		}
	}
	state.IngestHorizonSurveyReport(&civquestv1.HorizonSurveyReport{
		OriginX:       0,
		OriginY:       64,
		OriginZ:       0,
		CompletedTick: 220,
		MaxRange:      64.0,
		Sectors:       sectors,
	}, 220)

	frameExplore.WorldTick = 225
	navIntent := EvaluateExploration("Able", frameExplore, cfg, state)
	if navIntent == nil || navIntent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent from EvaluateExploration after survey report, got %+v", navIntent)
	}
}
