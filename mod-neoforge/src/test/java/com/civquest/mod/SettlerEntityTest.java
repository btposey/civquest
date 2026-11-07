package com.civquest.mod;

import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.SpawnAgentResponse;
import com.civquest.mod.grpc.v1.Vec3;
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

public class SettlerEntityTest {

    private SettlerEntityManager entityManager;
    private StreamRegistry streamRegistry;
    private CivQuestBridgeServiceImpl bridgeService;

    @BeforeEach
    void setUp() {
        entityManager = new SettlerEntityManager();
        streamRegistry = new StreamRegistry();
        bridgeService = new CivQuestBridgeServiceImpl(streamRegistry, entityManager, () -> null);
    }

    @Test
    @DisplayName("Deterministic offline player UUID generation adheres to Minecraft standard")
    void testOfflineUUIDGeneration() {
        String displayName = "Settler-Brian";
        UUID uuid1 = UUIDUtil.createOfflinePlayerUUID(displayName);
        UUID uuid2 = UUIDUtil.createOfflinePlayerUUID(displayName);

        assertNotNull(uuid1);
        assertEquals(uuid1, uuid2, "Same display name must yield identical offline UUID");

        UUID uuidDifferent = UUIDUtil.createOfflinePlayerUUID("Settler-Other");
        assertNotEquals(uuid1, uuidDifferent, "Different display names must produce different UUIDs");

        // Verify version 3 UUID (name-based MD5)
        assertEquals(3, uuid1.version(), "Offline player UUID must be version 3");
    }

    @Test
    @DisplayName("ClientboundSettlerSkinPacket encodes and decodes raw 64x64 PNG payloads correctly")
    void testSettlerSkinPacketCodec() {
        UUID settlerUuid = UUIDUtil.createOfflinePlayerUUID("Settler-Artisan");
        String agentId = "agent-artisan-01";
        byte[] dummySkinPng = "dummy-64x64-png-texture-bytes-content".getBytes(StandardCharsets.UTF_8);

        ClientboundSettlerSkinPacket packet = new ClientboundSettlerSkinPacket(settlerUuid, agentId, dummySkinPng);
        assertEquals(settlerUuid, packet.settlerUuid());
        assertEquals(agentId, packet.agentId());
        assertArrayEquals(dummySkinPng, packet.skinPng());
        assertEquals("civquest:settler_skin", packet.type().id().toString());

        // Test stream codec round-trip serialization
        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundSettlerSkinPacket.STREAM_CODEC.encode(buffer, packet);

        ClientboundSettlerSkinPacket decoded = ClientboundSettlerSkinPacket.STREAM_CODEC.decode(buffer);
        assertNotNull(decoded);
        assertEquals(packet.settlerUuid(), decoded.settlerUuid());
        assertEquals(packet.agentId(), decoded.agentId());
        assertArrayEquals(packet.skinPng(), decoded.skinPng());
    }

    @Test
    @DisplayName("ClientboundSettlerSkinPacket handles empty or null skin bytes safely")
    void testSettlerSkinPacketEmptyPayload() {
        UUID settlerUuid = UUID.randomUUID();
        ClientboundSettlerSkinPacket emptyPacket = new ClientboundSettlerSkinPacket(settlerUuid, "agent-bare", null);
        assertNotNull(emptyPacket.skinPng());
        assertEquals(0, emptyPacket.skinPng().length);

        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundSettlerSkinPacket.STREAM_CODEC.encode(buffer, emptyPacket);
        ClientboundSettlerSkinPacket decoded = ClientboundSettlerSkinPacket.STREAM_CODEC.decode(buffer);
        assertEquals(0, decoded.skinPng().length);
    }

    @Test
    @DisplayName("SettlerEntityManager tracks, queries, and clears settlers cleanly")
    void testSettlerEntityManagerLifecycle() throws ExecutionException, InterruptedException {
        assertEquals(0, entityManager.count());
        assertNull(entityManager.getSettler("non-existent"));

        // Despawn on non-existent returns false
        CompletableFuture<Boolean> despawnFuture = entityManager.despawnSettler(null, "non-existent");
        assertFalse(despawnFuture.get());

        // Clear all on empty manager does not throw
        assertDoesNotThrow(() -> entityManager.clearAll());
        assertEquals(0, entityManager.count());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl spawns settler in standalone/test mode with deterministic offline UUID")
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
    @DisplayName("CivQuestBridgeServiceImpl despawns settler and closes agent stream registry")
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
}
