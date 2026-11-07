package civquestv1_test

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryFrameSerialization(t *testing.T) {
	frame := &civquestv1.TelemetryFrame{
		WorldTick:               1200,
		TimeOfDay:               6000,
		Weather:                 civquestv1.Weather_WEATHER_CLEAR,
		BiomeId:                 "minecraft:plains",
		LastProcessedSequenceId: 42,
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
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
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
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:              &civquestv1.BlockPosProto{X: 102, Y: 64, Z: -198},
				BlockId:          "minecraft:wheat",
				BlockStateString: "age=7",
			},
		},
		LastDamageTaken: &civquestv1.DamageEvent{
			SourceType:       civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB,
			AttackerEntityId: 101,
			AttackerUuid:     "00000000-0000-0000-0000-000000000001",
			AttackerName:     "Zombie",
			DamageAmount:     3.5,
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
	if unmarshaled.GetWeather() != civquestv1.Weather_WEATHER_CLEAR {
		t.Errorf("expected Weather WEATHER_CLEAR, got %v", unmarshaled.GetWeather())
	}
	if unmarshaled.GetLastProcessedSequenceId() != 42 {
		t.Errorf("expected LastProcessedSequenceId 42, got %d", unmarshaled.GetLastProcessedSequenceId())
	}
	if unmarshaled.GetStatus().GetAgentId() != "settler-001" {
		t.Errorf("expected AgentId 'settler-001', got %s", unmarshaled.GetStatus().GetAgentId())
	}
	if unmarshaled.GetStatus().GetNavigationState() != civquestv1.NavigationState_NAVIGATION_STATE_IDLE {
		t.Errorf("expected NavigationState NAVIGATION_STATE_IDLE, got %v", unmarshaled.GetStatus().GetNavigationState())
	}
	if len(unmarshaled.GetVisibleEntities()) != 1 {
		t.Fatalf("expected 1 visible entity, got %d", len(unmarshaled.GetVisibleEntities()))
	}
	if !unmarshaled.GetVisibleEntities()[0].GetIsHostile() {
		t.Errorf("expected entity to be hostile")
	}
	if len(unmarshaled.GetInterestingBlocks()) != 1 {
		t.Fatalf("expected 1 interesting block, got %d", len(unmarshaled.GetInterestingBlocks()))
	}
	if unmarshaled.GetInterestingBlocks()[0].GetBlockStateString() != "age=7" {
		t.Errorf("expected BlockStateString 'age=7', got %s", unmarshaled.GetInterestingBlocks()[0].GetBlockStateString())
	}
	if unmarshaled.GetLastDamageTaken().GetSourceType() != civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB {
		t.Errorf("expected DamageSourceType MOB, got %v", unmarshaled.GetLastDamageTaken().GetSourceType())
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

func TestTypedEnumIntentsSerialization(t *testing.T) {
	// Test SendChatIntent with ChatChannel
	chatIntent := &civquestv1.AgentIntent{
		AgentId:    "settler-001",
		SequenceId: 2,
		Action: &civquestv1.AgentIntent_Chat{
			Chat: &civquestv1.SendChatIntent{
				Message:       "Greetings clan!",
				TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_CLAN_BROADCAST,
			},
		},
	}
	data, err := proto.Marshal(chatIntent)
	if err != nil {
		t.Fatalf("failed to marshal chatIntent: %v", err)
	}
	unmarshaledChat := &civquestv1.AgentIntent{}
	if err := proto.Unmarshal(data, unmarshaledChat); err != nil {
		t.Fatalf("failed to unmarshal chatIntent: %v", err)
	}
	if unmarshaledChat.GetChat().GetTargetChannel() != civquestv1.ChatChannel_CHAT_CHANNEL_CLAN_BROADCAST {
		t.Errorf("expected CHAT_CHANNEL_CLAN_BROADCAST, got %v", unmarshaledChat.GetChat().GetTargetChannel())
	}

	// Test InteractBlockIntent with InteractionType (UNSPECIFIED = 0, HARVEST_BREAK = 1)
	interactIntent := &civquestv1.AgentIntent{
		AgentId:    "settler-001",
		SequenceId: 3,
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 20},
				InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
				BlockOrItemId:   "minecraft:wheat",
			},
		},
	}
	data, err = proto.Marshal(interactIntent)
	if err != nil {
		t.Fatalf("failed to marshal interactIntent: %v", err)
	}
	unmarshaledInteract := &civquestv1.AgentIntent{}
	if err := proto.Unmarshal(data, unmarshaledInteract); err != nil {
		t.Fatalf("failed to unmarshal interactIntent: %v", err)
	}
	if unmarshaledInteract.GetInteractBlock().GetInteractionType() != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Errorf("expected HARVEST_BREAK, got %v", unmarshaledInteract.GetInteractBlock().GetInteractionType())
	}
	if int32(civquestv1.InteractBlockIntent_INTERACTION_TYPE_UNSPECIFIED) != 0 {
		t.Errorf("expected INTERACTION_TYPE_UNSPECIFIED to be 0")
	}

	// Test CaravanIntent with CaravanAction (UNSPECIFIED = 0, LEASH_BEAST = 1)
	caravanIntent := &civquestv1.AgentIntent{
		AgentId:    "settler-001",
		SequenceId: 4,
		Action: &civquestv1.AgentIntent_Caravan{
			Caravan: &civquestv1.CaravanIntent{
				Action:        civquestv1.CaravanIntent_LEASH_BEAST,
				BeastEntityId: 50,
			},
		},
	}
	data, err = proto.Marshal(caravanIntent)
	if err != nil {
		t.Fatalf("failed to marshal caravanIntent: %v", err)
	}
	unmarshaledCaravan := &civquestv1.AgentIntent{}
	if err := proto.Unmarshal(data, unmarshaledCaravan); err != nil {
		t.Fatalf("failed to unmarshal caravanIntent: %v", err)
	}
	if unmarshaledCaravan.GetCaravan().GetAction() != civquestv1.CaravanIntent_LEASH_BEAST {
		t.Errorf("expected LEASH_BEAST, got %v", unmarshaledCaravan.GetCaravan().GetAction())
	}
	if int32(civquestv1.CaravanIntent_CARAVAN_ACTION_UNSPECIFIED) != 0 {
		t.Errorf("expected CARAVAN_ACTION_UNSPECIFIED to be 0")
	}
}

func TestAdministrativeRPCStructures(t *testing.T) {
	spawnReq := &civquestv1.SpawnAgentRequest{
		AgentId:          "settler-002",
		DisplayName:      "Alden",
		ClanId:           "clan-ironpeak",
		SpawnCoords:      &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		SkinPngBytes:     []byte{0x89, 0x50, 0x4E, 0x47}, // mock png header
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
	if len(unmarshaled.GetSkinPngBytes()) != 4 {
		t.Errorf("expected 4 skin png bytes, got %d", len(unmarshaled.GetSkinPngBytes()))
	}
}
