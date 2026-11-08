package com.civquest.mod;

import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.CivQuestBridgeServiceGrpc;
import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.ExportRecipeRequest;
import com.civquest.mod.grpc.v1.ExportRecipeResponse;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.SpawnAgentResponse;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.health.v1.HealthCheckRequest;
import io.grpc.health.v1.HealthCheckResponse;
import io.grpc.health.v1.HealthGrpc;
import io.grpc.stub.MetadataUtils;
import io.grpc.stub.StreamObserver;
import net.minecraft.server.MinecraftServer;
import net.minecraft.world.level.storage.LevelResource;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.DirectoryStream;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.UUID;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

public class CivQuestBridgeServerTest {

    @TempDir
    Path tempDir;

    private CivQuestBridgeServer bridgeServer;
    private ManagedChannel channel;
    private Path discoveryPath;
    private String sharedKey;

    @BeforeEach
    void setUp() throws IOException {
        discoveryPath = tempDir.resolve("civquest-server-meta.json");
        sharedKey = "test-secret-" + UUID.randomUUID();
        bridgeServer = new CivQuestBridgeServer("127.0.0.1", 0, sharedKey, discoveryPath);
        bridgeServer.start();

        channel = ManagedChannelBuilder.forAddress("127.0.0.1", bridgeServer.getBoundPort())
                .usePlaintext()
                .build();
    }

    @AfterEach
    void tearDown() throws InterruptedException {
        if (channel != null && !channel.isShutdown()) {
            channel.shutdown();
            channel.awaitTermination(2, TimeUnit.SECONDS);
        }
        if (bridgeServer != null && bridgeServer.isRunning()) {
            bridgeServer.stop();
        }
    }

    @Test
    @DisplayName("Ephemeral port binding and metadata discovery generation")
    void testEphemeralPortAndDiscovery() throws IOException {
        assertTrue(bridgeServer.isRunning());
        assertTrue(bridgeServer.getBoundPort() > 0 && bridgeServer.getBoundPort() <= 65535,
                "Bound port must be valid dynamic port 1-65535");
        assertEquals("127.0.0.1", bridgeServer.getBoundHost());
        assertEquals(sharedKey, bridgeServer.getSharedKey());

        assertTrue(Files.exists(discoveryPath), "Discovery JSON must exist");
        String jsonContent = Files.readString(discoveryPath);

        assertTrue(jsonContent.contains("\"status\": \"READY\""), "Must contain READY status");
        assertTrue(jsonContent.contains("\"grpc_transport\": \"tcp\""), "Must contain tcp transport");
        assertTrue(jsonContent.contains("\"grpc_port\": " + bridgeServer.getBoundPort()), "Must contain bound port");
        assertTrue(jsonContent.contains("\"grpc_host\": \"127.0.0.1\""), "Must contain host");
        assertTrue(jsonContent.contains("\"server_pid\": " + ProcessHandle.current().pid()), "Must contain current PID");
        assertTrue(jsonContent.contains("\"shared_key\": \"" + sharedKey + "\""), "Must contain shared key");
        assertTrue(jsonContent.contains("\"server_id\": \"default\""), "Must contain default server_id");
        assertTrue(jsonContent.contains("\"world_name\": \"default\""), "Must contain default world_name");
        assertTrue(jsonContent.contains("\"timestamp_ms\": "), "Must contain timestamp_ms");

        if (FileSystems.getDefault().supportedFileAttributeViews().contains("posix")) {
            assertEquals(PosixFilePermissions.fromString("rw-------"),
                    Files.getPosixFilePermissions(discoveryPath),
                    "Discovery file must have 0600 POSIX permissions");
        }

        // Verify no lingering temp files
        try (DirectoryStream<Path> stream = Files.newDirectoryStream(tempDir, "civquest-meta-*.tmp")) {
            assertFalse(stream.iterator().hasNext(), "No temp files should remain in discovery dir");
        }
    }

    @Test
    @DisplayName("Unary health check succeeds unauthenticated but streaming watch is rejected")
    void testHealthCheckUnauthenticatedAndWatchRestricted() throws InterruptedException {
        HealthGrpc.HealthBlockingStub healthStub = HealthGrpc.newBlockingStub(channel);
        HealthCheckResponse response = healthStub.check(HealthCheckRequest.getDefaultInstance());

        assertEquals(HealthCheckResponse.ServingStatus.SERVING, response.getStatus());

        HealthCheckResponse serviceResponse = healthStub.check(
                HealthCheckRequest.newBuilder().setService(CivQuestBridgeServiceGrpc.SERVICE_NAME).build()
        );
        assertEquals(HealthCheckResponse.ServingStatus.SERVING, serviceResponse.getStatus());

        // Unauthenticated Health/Watch must be rejected (H-3)
        HealthGrpc.HealthStub asyncHealthStub = HealthGrpc.newStub(channel);
        BlockingQueue<Throwable> watchErrors = new ArrayBlockingQueue<>(1);
        asyncHealthStub.watch(HealthCheckRequest.getDefaultInstance(), new StreamObserver<>() {
            @Override
            public void onNext(HealthCheckResponse value) {
            }

            @Override
            public void onError(Throwable t) {
                watchErrors.offer(t);
            }

            @Override
            public void onCompleted() {
            }
        });

        Throwable error = watchErrors.poll(5, TimeUnit.SECONDS);
        assertNotNull(error, "Health/Watch without auth must fail");
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.UNAUTHENTICATED, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("Shared key authentication rejects missing or invalid token")
    void testAuthRejection() {
        CivQuestBridgeServiceGrpc.CivQuestBridgeServiceBlockingStub unauthStub =
                CivQuestBridgeServiceGrpc.newBlockingStub(channel);

        // Missing authorization header
        StatusRuntimeException ex1 = assertThrows(StatusRuntimeException.class, () ->
                unauthStub.spawnAgentEntity(SpawnAgentRequest.newBuilder().setAgentId("agent-1").build())
        );
        assertEquals(Status.Code.UNAUTHENTICATED, ex1.getStatus().getCode());

        // Invalid authorization header
        Metadata invalidHeaders = new Metadata();
        invalidHeaders.put(SharedKeyAuthInterceptor.AUTHORIZATION_METADATA_KEY, "Bearer invalid-key");
        CivQuestBridgeServiceGrpc.CivQuestBridgeServiceBlockingStub invalidStub =
                unauthStub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(invalidHeaders));

        StatusRuntimeException ex2 = assertThrows(StatusRuntimeException.class, () ->
                invalidStub.spawnAgentEntity(SpawnAgentRequest.newBuilder().setAgentId("agent-1").build())
        );
        assertEquals(Status.Code.UNAUTHENTICATED, ex2.getStatus().getCode());
    }

    @Test
    @DisplayName("Case-insensitive Bearer prefix authentication succeeds")
    void testAuthCaseInsensitive() {
        String[] prefixes = {"bearer ", "BEARER ", "Bearer ", "  bearer  "};
        for (String prefix : prefixes) {
            Metadata authHeaders = new Metadata();
            authHeaders.put(SharedKeyAuthInterceptor.AUTHORIZATION_METADATA_KEY, prefix + sharedKey);

            CivQuestBridgeServiceGrpc.CivQuestBridgeServiceBlockingStub authStub =
                    CivQuestBridgeServiceGrpc.newBlockingStub(channel)
                            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(authHeaders));

            SpawnAgentResponse spawnResp = authStub.spawnAgentEntity(SpawnAgentRequest.newBuilder()
                    .setAgentId("agent-" + UUID.randomUUID())
                    .setDisplayName("TestAgent")
                    .setClanId("clan-1")
                    .build());
            assertTrue(spawnResp.getSuccess(), "Auth should succeed with prefix: '" + prefix + "'");
        }
    }

    @Test
    @DisplayName("Default generated key is 64-character lowercase hex (256-bit SecureRandom)")
    void testDefaultSecureKeyGeneration() {
        CivQuestBridgeServer defaultServer = new CivQuestBridgeServer("127.0.0.1", 0, null, tempDir.resolve("auto-meta.json"));
        assertNotNull(defaultServer.getSharedKey());
        assertEquals(64, defaultServer.getSharedKey().length(), "Generated key must be 64 characters");
        assertTrue(defaultServer.getSharedKey().matches("^[0-9a-f]{64}$"), "Key must be lowercase hex");
    }

    @Test
    @DisplayName("Wildcard 0.0.0.0 binds resolve advertised host correctly")
    void testAdvertisedHostForWildcard() throws IOException {
        Path metaPath = tempDir.resolve("wildcard-meta.json");
        CivQuestBridgeServer wildcardServer = new CivQuestBridgeServer("0.0.0.0", 0, "test-key", metaPath);
        assertEquals("127.0.0.1", wildcardServer.getAdvertisedHost(), "Default wildcard host must be 127.0.0.1");

        System.setProperty(CivQuestBridgeServer.PROP_GRPC_ADVERTISED_HOST, "192.168.1.123");
        try {
            assertEquals("192.168.1.123", wildcardServer.getAdvertisedHost());
            wildcardServer.writeDiscoveryFile("READY");
            String content = Files.readString(metaPath);
            assertTrue(content.contains("\"grpc_host\": \"192.168.1.123\""));
        } finally {
            System.clearProperty(CivQuestBridgeServer.PROP_GRPC_ADVERTISED_HOST);
        }
    }

    @Test
    @DisplayName("Authenticated RPCs succeed with Bearer token")
    void testAuthenticatedRPCs() {
        Metadata authHeaders = new Metadata();
        authHeaders.put(SharedKeyAuthInterceptor.AUTHORIZATION_METADATA_KEY, "Bearer " + sharedKey);

        CivQuestBridgeServiceGrpc.CivQuestBridgeServiceBlockingStub authStub =
                CivQuestBridgeServiceGrpc.newBlockingStub(channel)
                        .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(authHeaders));

        // 1. SpawnAgentEntity
        SpawnAgentResponse spawnResp = authStub.spawnAgentEntity(SpawnAgentRequest.newBuilder()
                .setAgentId("agent-alpha")
                .setDisplayName("Alpha")
                .setClanId("clan-1")
                .build());
        assertTrue(spawnResp.getSuccess());
        assertTrue(spawnResp.getMinecraftEntityId() > 0);
        assertFalse(spawnResp.getOfflineUuid().isEmpty());

        // 2. DespawnAgentEntity
        DespawnAgentResponse despawnResp = authStub.despawnAgentEntity(DespawnAgentRequest.newBuilder()
                .setAgentId("agent-alpha")
                .build());
        assertTrue(despawnResp.getSuccess());

        // 3. ExportRecipeGraph
        ExportRecipeResponse recipeResp = authStub.exportRecipeGraph(ExportRecipeRequest.newBuilder().build());
        assertNotNull(recipeResp);
    }

    @Test
    @DisplayName("ConnectAgent bidirectional stream receives baseline telemetry")
    void testConnectAgentStream() throws InterruptedException {
        Metadata authHeaders = new Metadata();
        authHeaders.put(SharedKeyAuthInterceptor.AUTHORIZATION_METADATA_KEY, "Bearer " + sharedKey);

        CivQuestBridgeServiceGrpc.CivQuestBridgeServiceStub asyncStub =
                CivQuestBridgeServiceGrpc.newStub(channel)
                        .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(authHeaders));

        BlockingQueue<TelemetryFrame> receivedFrames = new ArrayBlockingQueue<>(5);

        StreamObserver<AgentIntent> requestObserver = asyncStub.connectAgent(new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame frame) {
                receivedFrames.offer(frame);
            }

            @Override
            public void onError(Throwable t) {
            }

            @Override
            public void onCompleted() {
            }
        });

        TelemetryFrame initialFrame = receivedFrames.poll(5, TimeUnit.SECONDS);
        assertNotNull(initialFrame, "Must receive initial TelemetryFrame");
        assertTrue(initialFrame.getStatus().getHealth() > 0, "Initial frame health must be > 0");

        requestObserver.onCompleted();
    }

    @Test
    @DisplayName("Clean shutdown updates metadata file to STOPPED")
    void testCleanShutdown() throws IOException {
        assertTrue(bridgeServer.isRunning());
        bridgeServer.stop();
        assertFalse(bridgeServer.isRunning());

        assertTrue(Files.exists(discoveryPath), "Discovery JSON must exist after shutdown");
        String content = Files.readString(discoveryPath);
        assertTrue(content.contains("\"status\": \"STOPPED\""), "Status must be updated to STOPPED");
    }

    @Test
    @DisplayName("Server ID resolution from system property and explicit setter")
    void testServerIdResolution() throws IOException {
        System.setProperty(CivQuestBridgeServer.PROP_SERVER_ID, "test-server-alpha_1");
        try {
            CivQuestBridgeServer server = new CivQuestBridgeServer("127.0.0.1", 0, "test-key", tempDir.resolve("sysprop-meta.json"));
            assertEquals("test-server-alpha_1", server.getServerId());
            assertEquals("test-server-alpha_1", server.resolveServerId());

            server.writeDiscoveryFile("READY");
            String json = Files.readString(tempDir.resolve("sysprop-meta.json"));
            assertTrue(json.contains("\"server_id\": \"test-server-alpha_1\""), "Discovery JSON must contain resolved server ID");
        } finally {
            System.clearProperty(CivQuestBridgeServer.PROP_SERVER_ID);
        }

        // Test explicit setter
        bridgeServer.setServerId("custom-override-id");
        bridgeServer.setWorldName("Overworld-Prime");
        assertEquals("custom-override-id", bridgeServer.getServerId());
        assertEquals("Overworld-Prime", bridgeServer.getWorldName());

        bridgeServer.writeDiscoveryFile("READY");
        String json = Files.readString(discoveryPath);
        assertTrue(json.contains("\"server_id\": \"custom-override-id\""));
        assertTrue(json.contains("\"world_name\": \"Overworld-Prime\""));
    }

    @Test
    @DisplayName("Server ID generation and persistence in Minecraft world LevelResource.ROOT")
    void testServerIdWorldPersistence() throws IOException {
        Path worldDir = tempDir.resolve("saves").resolve("World_1");
        Files.createDirectories(worldDir);

        CivQuestBridgeServer server = new CivQuestBridgeServer("127.0.0.1", 0, "test-key", tempDir.resolve("world-meta.json"));
        server.setWorldName("TestWorld");

        String generatedId = server.resolveServerIdFromWorldPath(worldDir);
        assertNotNull(generatedId);
        assertTrue(generatedId.startsWith("TestWorld-"), "Generated server ID must start with sanitized world name");
        assertTrue(generatedId.length() <= 32, "Server ID must not exceed 32 characters");
        assertTrue(generatedId.matches("^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$"), "Server ID must match identifier regex");

        Path idFile = worldDir.resolve("civquest-server-id.txt");
        assertTrue(Files.exists(idFile), "civquest-server-id.txt must be persisted in world directory");
        assertEquals(generatedId, Files.readString(idFile).trim());

        // Second instance with the same world directory must read the existing file
        CivQuestBridgeServer server2 = new CivQuestBridgeServer("127.0.0.1", 0, "test-key-2", tempDir.resolve("world-meta-2.json"));
        server2.setWorldName("DifferentWorldName");
        assertEquals(generatedId, server2.resolveServerIdFromWorldPath(worldDir), "Must reuse persisted server ID from civquest-server-id.txt");
    }

    @Test
    @DisplayName("SanitizeServerId handles edge cases, lengths, and special characters")
    void testSanitizeServerId() {
        assertEquals("default", CivQuestBridgeServer.sanitizeServerId(null));
        assertEquals("default", CivQuestBridgeServer.sanitizeServerId(""));
        assertEquals("default", CivQuestBridgeServer.sanitizeServerId("!!!$$$###"));
        assertEquals("alpha-beta", CivQuestBridgeServer.sanitizeServerId("alpha-beta"));
        assertEquals("hello-world", CivQuestBridgeServer.sanitizeServerId("hello world!"));
        assertEquals("server_123", CivQuestBridgeServer.sanitizeServerId("--server_123--"));

        // Max 32 chars length
        String longId = "a".repeat(50);
        String sanitized = CivQuestBridgeServer.sanitizeServerId(longId);
        assertEquals(32, sanitized.length());
        assertTrue(sanitized.matches("^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$"));
    }

    @Test
    @DisplayName("getWorldName returns cached worldName once set or resolved")
    void testGetWorldNameCaching() {
        assertEquals("default", bridgeServer.getWorldName());

        bridgeServer.setWorldName("CachedWorldAlpha");
        assertEquals("CachedWorldAlpha", bridgeServer.getWorldName());

        // System property fallback test
        System.setProperty("civquest.world.name", "SysPropWorld");
        try {
            CivQuestBridgeServer uninitServer = new CivQuestBridgeServer("127.0.0.1", 0, "test-key", tempDir.resolve("prop-meta.json"));
            assertEquals("SysPropWorld", uninitServer.getWorldName());
        } finally {
            System.clearProperty("civquest.world.name");
        }
    }
}
