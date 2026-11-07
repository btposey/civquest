package com.civquest.mod.grpc.v1;

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
                .setWeather("RAIN")
                .setBiomeId("minecraft:forest")
                .setStatus(AgentStatus.newBuilder()
                        .setAgentId("settler-42")
                        .setEntityId(100)
                        .setPosition(Vec3.newBuilder().setX(10.0).setY(70.0).setZ(-50.0).build())
                        .setHealth(18.5f)
                        .setMaxHealth(20.0f)
                        .setFoodLevel(19)
                        .setSaturation(4.0f)
                        .setNavigationState("PATHING")
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
                .build();

        byte[] serialized = frame.toByteArray();
        TelemetryFrame parsed = TelemetryFrame.parseFrom(serialized);

        assertEquals(2400, parsed.getWorldTick());
        assertEquals("RAIN", parsed.getWeather());
        assertEquals("settler-42", parsed.getStatus().getAgentId());
        assertEquals("PATHING", parsed.getStatus().getNavigationState());
        assertEquals(1, parsed.getVisibleEntitiesCount());
        assertTrue(parsed.getVisibleEntities(0).getIsHostile());
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
    @DisplayName("Administrative SpawnAgentRequest and Response roundtrip")
    void testAdministrativeRpcRoundtrip() throws InvalidProtocolBufferException {
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("settler-spawn-test")
                .setDisplayName("Brynn")
                .setClanId("ironpeak")
                .setSpawnCoords(Vec3.newBuilder().setX(0.0).setY(64.0).setZ(0.0).build())
                .setSkinTextureValue("base64value")
                .setSkinSignature("signature")
                .setIsChild(false)
                .build();

        byte[] bytes = request.toByteArray();
        SpawnAgentRequest parsedReq = SpawnAgentRequest.parseFrom(bytes);

        assertEquals("settler-spawn-test", parsedReq.getAgentId());
        assertEquals("Brynn", parsedReq.getDisplayName());
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
