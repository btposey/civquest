package com.civquest.mod.grpc.v1;

import com.google.protobuf.ByteString;
import com.google.protobuf.InvalidProtocolBufferException;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

public class ProtoContractTest {

    @Test
    @DisplayName("TelemetryFrame serialization and deserialization roundtrip")
    void testTelemetryFrameRoundtrip() throws InvalidProtocolBufferException {
        TelemetryFrame frame = TelemetryFrame.newBuilder()
                .setWorldTick(2400)
                .setTimeOfDay(12000)
                .setWeather(Weather.WEATHER_RAIN)
                .setBiomeId("minecraft:forest")
                .setLastProcessedSequenceId(100L)
                .setStatus(AgentStatus.newBuilder()
                        .setAgentId("settler-42")
                        .setEntityId(100)
                        .setPosition(Vec3.newBuilder().setX(10.0).setY(70.0).setZ(-50.0).build())
                        .setHealth(18.5f)
                        .setMaxHealth(20.0f)
                        .setFoodLevel(19)
                        .setSaturation(4.0f)
                        .setNavigationState(NavigationState.NAVIGATION_STATE_PATHING)
                        .build())
                .addVisibleEntities(VisibleEntity.newBuilder()
                        .setEntityId(200)
                        .setUuid("uuid-skele-1")
                        .setEntityType("minecraft:skeleton")
                        .setDisplayName("Skeleton")
                        .setPosition(Vec3.newBuilder().setX(15.0).setY(70.0).setZ(-52.0).build())
                        .setDistance(5.38f)
                        .setHealth(20.0f)
                        .setIsHostile(true)
                        .build())
                .addInterestingBlocks(VisibleBlock.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(10).setY(64).setZ(-50).build())
                        .setBlockId("minecraft:wheat")
                        .setBlockStateString("age=7")
                        .build())
                .setLastDamageTaken(DamageEvent.newBuilder()
                        .setSourceType(DamageSourceType.DAMAGE_SOURCE_TYPE_MOB)
                        .setAttackerEntityId(200)
                        .setAttackerUuid("uuid-skele-1")
                        .setAttackerName("Skeleton")
                        .setDamageAmount(2.5f)
                        .build())
                .build();

        byte[] serialized = frame.toByteArray();
        TelemetryFrame parsed = TelemetryFrame.parseFrom(serialized);

        assertEquals(2400, parsed.getWorldTick());
        assertEquals(Weather.WEATHER_RAIN, parsed.getWeather());
        assertEquals(100L, parsed.getLastProcessedSequenceId());
        assertEquals("settler-42", parsed.getStatus().getAgentId());
        assertEquals(NavigationState.NAVIGATION_STATE_PATHING, parsed.getStatus().getNavigationState());
        assertEquals(1, parsed.getVisibleEntitiesCount());
        assertTrue(parsed.getVisibleEntities(0).getIsHostile());
        assertEquals(1, parsed.getInterestingBlocksCount());
        assertEquals("age=7", parsed.getInterestingBlocks(0).getBlockStateString());
        assertEquals(DamageSourceType.DAMAGE_SOURCE_TYPE_MOB, parsed.getLastDamageTaken().getSourceType());
    }

    @Test
    @DisplayName("AgentIntent action oneof and nested parameters roundtrip")
    void testAgentIntentRoundtrip() throws InvalidProtocolBufferException {
        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("settler-42")
                .setSequenceId(101)
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(25.0).setY(65.0).setZ(-10.0).build())
                        .setSpeedMultiplier(1.2f)
                        .setSprint(true)
                        .setAllowBreakBlocks(false)
                        .setAllowPlaceScaffolding(true)
                        .setArrivalTolerance(2)
                        .build())
                .build();

        byte[] serialized = intent.toByteArray();
        AgentIntent parsed = AgentIntent.parseFrom(serialized);

        assertEquals("settler-42", parsed.getAgentId());
        assertEquals(101, parsed.getSequenceId());
        assertEquals(AgentIntent.ActionCase.NAVIGATE, parsed.getActionCase());
        assertTrue(parsed.getNavigate().getSprint());
        assertTrue(parsed.getNavigate().getAllowPlaceScaffolding());
        assertEquals(25.0, parsed.getNavigate().getTarget().getX());
    }

    @Test
    @DisplayName("Typed enum intents roundtrip and proto3 zero-value assertions")
    void testTypedEnumIntentsRoundtrip() throws InvalidProtocolBufferException {
        // Assert zero-value constants
        assertEquals(0, InteractBlockIntent.InteractionType.INTERACTION_TYPE_UNSPECIFIED.getNumber());
        assertEquals(0, CaravanIntent.CaravanAction.CARAVAN_ACTION_UNSPECIFIED.getNumber());
        assertEquals(0, Weather.WEATHER_UNSPECIFIED.getNumber());
        assertEquals(0, NavigationState.NAVIGATION_STATE_UNSPECIFIED.getNumber());
        assertEquals(0, DamageSourceType.DAMAGE_SOURCE_TYPE_UNSPECIFIED.getNumber());
        assertEquals(0, ChatChannel.CHAT_CHANNEL_UNSPECIFIED.getNumber());

        // SendChatIntent with typed ChatChannel
        SendChatIntent chatIntent = SendChatIntent.newBuilder()
                .setMessage("Hello village!")
                .setTargetChannel(ChatChannel.CHAT_CHANNEL_LOCAL)
                .build();
        byte[] chatBytes = chatIntent.toByteArray();
        SendChatIntent parsedChat = SendChatIntent.parseFrom(chatBytes);
        assertEquals(ChatChannel.CHAT_CHANNEL_LOCAL, parsedChat.getTargetChannel());

        // InteractBlockIntent with InteractionType
        InteractBlockIntent interactIntent = InteractBlockIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(5).setY(64).setZ(5).build())
                .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                .setBlockOrItemId("minecraft:carrots")
                .build();
        byte[] interactBytes = interactIntent.toByteArray();
        InteractBlockIntent parsedInteract = InteractBlockIntent.parseFrom(interactBytes);
        assertEquals(InteractBlockIntent.InteractionType.HARVEST_BREAK, parsedInteract.getInteractionType());

        // CaravanIntent with CaravanAction
        CaravanIntent caravanIntent = CaravanIntent.newBuilder()
                .setAction(CaravanIntent.CaravanAction.LEASH_BEAST)
                .setBeastEntityId(45)
                .build();
        byte[] caravanBytes = caravanIntent.toByteArray();
        CaravanIntent parsedCaravan = CaravanIntent.parseFrom(caravanBytes);
        assertEquals(CaravanIntent.CaravanAction.LEASH_BEAST, parsedCaravan.getAction());
    }

    @Test
    @DisplayName("Administrative SpawnAgentRequest and Response roundtrip")
    void testAdministrativeRpcRoundtrip() throws InvalidProtocolBufferException {
        byte[] mockSkin = new byte[]{(byte) 0x89, 0x50, 0x4E, 0x47};
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("settler-spawn-test")
                .setDisplayName("Brynn")
                .setClanId("ironpeak")
                .setSpawnCoords(Vec3.newBuilder().setX(0.0).setY(64.0).setZ(0.0).build())
                .setSkinPngBytes(ByteString.copyFrom(mockSkin))
                .setSkinTextureValue("base64value")
                .setSkinSignature("signature")
                .setIsChild(false)
                .build();

        byte[] bytes = request.toByteArray();
        SpawnAgentRequest parsedReq = SpawnAgentRequest.parseFrom(bytes);

        assertEquals("settler-spawn-test", parsedReq.getAgentId());
        assertEquals("Brynn", parsedReq.getDisplayName());
        assertEquals(4, parsedReq.getSkinPngBytes().size());
        assertFalse(parsedReq.getIsChild());

        SpawnAgentResponse response = SpawnAgentResponse.newBuilder()
                .setSuccess(true)
                .setMinecraftEntityId(55)
                .setOfflineUuid("mock-offline-uuid")
                .build();

        assertEquals(55, response.getMinecraftEntityId());
        assertTrue(response.getSuccess());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceGrpc service descriptor and class definition")
    void testGrpcServiceDescriptor() {
        assertNotNull(CivQuestBridgeServiceGrpc.SERVICE_NAME);
        assertEquals("civquest.v1.CivQuestBridgeService", CivQuestBridgeServiceGrpc.SERVICE_NAME);
        assertNotNull(CivQuestBridgeServiceGrpc.getConnectAgentMethod());
        assertNotNull(CivQuestBridgeServiceGrpc.getSpawnAgentEntityMethod());
        assertNotNull(CivQuestBridgeServiceGrpc.getDespawnAgentEntityMethod());
        assertNotNull(CivQuestBridgeServiceGrpc.getExportRecipeGraphMethod());
    }
}
