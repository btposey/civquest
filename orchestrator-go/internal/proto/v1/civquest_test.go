package civquestv1_test

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryFrameSerialization(t *testing.T) {
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 6000,
		Weather:   "CLEAR",
		BiomeId:   "minecraft:plains",
		Status: &civquestv1.AgentStatus{
			AgentId:         "settler-001",
			EntityId:        42,
			Position:        &civquestv1.Vec3{X: 100.5, Y: 64.0, Z: -200.5},
			Yaw:             90.0,
			Pitch:           0.0,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Saturation:      5.0,
			NavigationState: "IDLE",
			Inventory: []*civquestv1.ItemStackProto{
				{
					ItemId: "minecraft:iron_sword",
					Count:  1,
					Slot:   0,
				},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:    101,
				Uuid:        "00000000-0000-0000-0000-000000000001",
				EntityType:  "minecraft:zombie",
				DisplayName: "Zombie",
				Position:    &civquestv1.Vec3{X: 105.0, Y: 64.0, Z: -200.0},
				Distance:    4.5,
				Health:      20.0,
				IsHostile:   true,
			},
		},
	}

	data, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("failed to marshal TelemetryFrame: %v", err)
	}

	unmarshaled := &civquestv1.TelemetryFrame{}
	if err := proto.Unmarshal(data, unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal TelemetryFrame: %v", err)
	}

	if unmarshaled.GetWorldTick() != 1200 {
		t.Errorf("expected WorldTick 1200, got %d", unmarshaled.GetWorldTick())
	}
	if unmarshaled.GetStatus().GetAgentId() != "settler-001" {
		t.Errorf("expected AgentId 'settler-001', got %s", unmarshaled.GetStatus().GetAgentId())
	}
	if len(unmarshaled.GetVisibleEntities()) != 1 {
		t.Fatalf("expected 1 visible entity, got %d", len(unmarshaled.GetVisibleEntities()))
	}
	if !unmarshaled.GetVisibleEntities()[0].GetIsHostile() {
		t.Errorf("expected entity to be hostile")
	}
}

func TestAgentIntentSerialization(t *testing.T) {
	intent := &civquestv1.AgentIntent{
		AgentId:    "settler-001",
		SequenceId: 1,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:                &civquestv1.Vec3{X: 150.0, Y: 64.0, Z: -180.0},
				SpeedMultiplier:       1.0,
				Sprint:                true,
				AllowBreakBlocks:      false,
				AllowPlaceScaffolding: true,
				ArrivalTolerance:      1,
			},
		},
	}

	data, err := proto.Marshal(intent)
	if err != nil {
		t.Fatalf("failed to marshal AgentIntent: %v", err)
	}

	unmarshaled := &civquestv1.AgentIntent{}
	if err := proto.Unmarshal(data, unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal AgentIntent: %v", err)
	}

	if unmarshaled.GetAgentId() != "settler-001" {
		t.Errorf("expected AgentId 'settler-001', got %s", unmarshaled.GetAgentId())
	}
	nav := unmarshaled.GetNavigate()
	if nav == nil {
		t.Fatalf("expected navigate intent action, got nil")
	}
	if !nav.GetSprint() {
		t.Errorf("expected sprint to be true")
	}
	if nav.GetTarget().GetX() != 150.0 {
		t.Errorf("expected target X 150.0, got %f", nav.GetTarget().GetX())
	}
}

func TestAdministrativeRPCStructures(t *testing.T) {
	spawnReq := &civquestv1.SpawnAgentRequest{
		AgentId:          "settler-002",
		DisplayName:      "Alden",
		ClanId:           "clan-ironpeak",
		SpawnCoords:      &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		SkinTextureValue: "mock-texture-base64",
		SkinSignature:    "mock-sig",
		IsChild:          false,
	}

	data, err := proto.Marshal(spawnReq)
	if err != nil {
		t.Fatalf("failed to marshal SpawnAgentRequest: %v", err)
	}

	unmarshaled := &civquestv1.SpawnAgentRequest{}
	if err := proto.Unmarshal(data, unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal SpawnAgentRequest: %v", err)
	}
	if unmarshaled.GetDisplayName() != "Alden" {
		t.Errorf("expected DisplayName 'Alden', got %s", unmarshaled.GetDisplayName())
	}
}
