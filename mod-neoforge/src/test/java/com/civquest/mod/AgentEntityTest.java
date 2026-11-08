package com.civquest.mod;

import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.SpawnAgentResponse;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.Vec3;
import com.google.protobuf.ByteString;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import io.netty.buffer.Unpooled;
import net.minecraft.core.UUIDUtil;
import net.minecraft.network.FriendlyByteBuf;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

public class AgentEntityTest {

    private AgentEntityManager entityManager;
    private StreamRegistry streamRegistry;
    private CivQuestBridgeServiceImpl bridgeService;

    @BeforeEach
    void setUp() {
        entityManager = new AgentEntityManager();
        streamRegistry = new StreamRegistry();
        bridgeService = new CivQuestBridgeServiceImpl(streamRegistry, entityManager, () -> null);
    }

    @Test
    @DisplayName("Deterministic offline player UUID generation adheres to Minecraft standard")
    void testOfflineUUIDGeneration() {
        String displayName = "Agent-Brian";
        UUID uuid1 = UUIDUtil.createOfflinePlayerUUID(displayName);
        UUID uuid2 = UUIDUtil.createOfflinePlayerUUID(displayName);

        assertNotNull(uuid1);
        assertEquals(uuid1, uuid2, "Same display name must yield identical offline UUID");

        UUID uuidDifferent = UUIDUtil.createOfflinePlayerUUID("Agent-Other");
        assertNotEquals(uuid1, uuidDifferent, "Different display names must produce different UUIDs");

        // Verify version 3 UUID (name-based MD5)
        assertEquals(3, uuid1.version(), "Offline player UUID must be version 3");
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket encodes and decodes raw 64x64 PNG payloads correctly")
    void testAgentSkinPacketCodec() {
        UUID agentUuid = UUIDUtil.createOfflinePlayerUUID("Agent-Artisan");
        String agentId = "agent-artisan-01";
        byte[] dummySkinPng = "dummy-64x64-png-texture-bytes-content".getBytes(StandardCharsets.UTF_8);

        ClientboundAgentSkinPacket packet = new ClientboundAgentSkinPacket(agentUuid, agentId, dummySkinPng);
        assertEquals(agentUuid, packet.agentUuid());
        assertEquals(agentId, packet.agentId());
        assertArrayEquals(dummySkinPng, packet.skinPng());
        assertEquals("civquest:agent_skin", packet.type().id().toString());

        // Test stream codec round-trip serialization
        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundAgentSkinPacket.STREAM_CODEC.encode(buffer, packet);

        ClientboundAgentSkinPacket decoded = ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer);
        assertNotNull(decoded);
        assertEquals(packet.agentUuid(), decoded.agentUuid());
        assertEquals(packet.agentId(), decoded.agentId());
        assertArrayEquals(packet.skinPng(), decoded.skinPng());
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket handles empty or null skin bytes safely")
    void testAgentSkinPacketEmptyPayload() {
        UUID agentUuid = UUID.randomUUID();
        ClientboundAgentSkinPacket emptyPacket = new ClientboundAgentSkinPacket(agentUuid, "agent-bare", null);
        assertNotNull(emptyPacket.skinPng());
        assertEquals(0, emptyPacket.skinPng().length);

        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundAgentSkinPacket.STREAM_CODEC.encode(buffer, emptyPacket);
        ClientboundAgentSkinPacket decoded = ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer);
        assertEquals(0, decoded.skinPng().length);
    }

    @Test
    @DisplayName("AgentEntityManager tracks, queries, and clears agents cleanly")
    void testAgentEntityManagerLifecycle() throws ExecutionException, InterruptedException {
        assertEquals(0, entityManager.count());
        assertNull(entityManager.getAgent("non-existent"));

        // Despawn on non-existent returns false
        CompletableFuture<Boolean> despawnFuture = entityManager.despawnAgent(null, "non-existent");
        assertFalse(despawnFuture.get());

        // Clear all on empty manager does not throw
        assertDoesNotThrow(() -> entityManager.clearAll());
        assertEquals(0, entityManager.count());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl spawns agent in standalone/test mode with deterministic offline UUID")
    void testBridgeServiceSpawnStandalone() {
        String agentId = "agent-unit-01";
        String displayName = "AgentUnit";
        String clanId = "clan-ironwood";

        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId(agentId)
                .setDisplayName(displayName)
                .setClanId(clanId)
                .setSpawnCoords(Vec3.newBuilder().setX(100.0).setY(64.0).setZ(-200.0).build())
                .build();

        AtomicReference<SpawnAgentResponse> responseRef = new AtomicReference<>();
        AtomicReference<Throwable> errorRef = new AtomicReference<>();

        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {
                responseRef.set(value);
            }

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {
            }
        });

        assertNull(errorRef.get());
        SpawnAgentResponse response = responseRef.get();
        assertNotNull(response);
        assertTrue(response.getSuccess());
        assertEquals(1001, response.getMinecraftEntityId());

        String expectedUuid = UUIDUtil.createOfflinePlayerUUID(displayName).toString();
        assertEquals(expectedUuid, response.getOfflineUuid());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl despawns agent and closes agent stream registry")
    void testBridgeServiceDespawn() {
        String agentId = "agent-despawn-test";

        DespawnAgentRequest request = DespawnAgentRequest.newBuilder()
                .setAgentId(agentId)
                .setReason("Test cycle completed")
                .build();

        AtomicReference<DespawnAgentResponse> responseRef = new AtomicReference<>();
        bridgeService.despawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(DespawnAgentResponse value) {
                responseRef.set(value);
            }

            @Override
            public void onError(Throwable t) {
            }

            @Override
            public void onCompleted() {
            }
        });

        DespawnAgentResponse response = responseRef.get();
        assertNotNull(response);
        assertTrue(response.getSuccess());
        assertNull(streamRegistry.getObserver(agentId));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with empty agent_id")
    void testBridgeServiceSpawnValidation() {
        SpawnAgentRequest invalidRequest = SpawnAgentRequest.newBuilder()
                .setAgentId("")
                .setDisplayName("NoId")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(invalidRequest, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {
            }

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {
            }
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with invalid Base64 skin_texture_value")
    void testSkinValidationInvalidBase64() {
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-invalid-base64")
                .setSkinTextureValue("not-valid-base64-!@#$%^&*()")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("skin_texture_value is not valid Base64"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with skin_texture_value exceeding 4096 characters")
    void testSkinValidationTextureTooLong() {
        String longTexture = "A".repeat(4097);
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-long-texture")
                .setSkinTextureValue(longTexture)
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("exceeds maximum allowed length of 4096"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with skin_png_bytes exceeding 64KB")
    void testSkinPngBytesExceeds64KB() {
        byte[] oversizedBytes = new byte[65537];
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-oversized-skin")
                .setSkinPngBytes(ByteString.copyFrom(oversizedBytes))
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("exceeds maximum allowed size of 65536"));
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket rejects payloads larger than 64KB on decode")
    void testAgentSkinPacketCodecRejectsOversizedPayload() {
        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        buffer.writeUUID(UUID.randomUUID());
        buffer.writeUtf("agent-oversized-test");
        buffer.writeByteArray(new byte[65537]);

        assertThrows(Exception.class, () -> ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects DespawnAgentRequest with empty or blank agent_id")
    void testDespawnAgentEmptyIdValidation() {
        DespawnAgentRequest request = DespawnAgentRequest.newBuilder()
                .setAgentId("   ")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.despawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(DespawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("StreamRegistry reopen restores closed registry allowing new registrations")
    void testStreamRegistryReopen() {
        StreamRegistry registry = new StreamRegistry();
        assertFalse(registry.isClosed());

        StreamObserver<TelemetryFrame> dummyObserver = new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {}
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        };

        assertTrue(registry.register("agent-reopen-1", dummyObserver));
        assertEquals(1, registry.getActiveAgentCount());

        registry.closeAll();
        assertTrue(registry.isClosed());
        assertEquals(0, registry.getActiveAgentCount());

        // While closed, registrations are rejected
        assertFalse(registry.register("agent-reopen-2", dummyObserver));

        // Reopen resets closed state
        registry.reopen();
        assertFalse(registry.isClosed());

        // Registrations succeed after reopen
        assertTrue(registry.register("agent-reopen-3", dummyObserver));
        assertEquals(1, registry.getActiveAgentCount());
    }
}
