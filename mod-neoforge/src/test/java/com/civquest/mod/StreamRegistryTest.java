package com.civquest.mod;

import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import io.grpc.Status;
import io.grpc.stub.ServerCallStreamObserver;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

public class StreamRegistryTest {

    @TempDir
    Path tempDir;

    static class RecordingObserver implements StreamObserver<TelemetryFrame> {
        final List<TelemetryFrame> receivedFrames = new CopyOnWriteArrayList<>();
        final AtomicBoolean completed = new AtomicBoolean(false);
        final AtomicReference<Throwable> error = new AtomicReference<>(null);
        volatile boolean throwOnNext = false;
        volatile RuntimeException exceptionToThrow = null;
        volatile boolean throwOnCompleted = false;

        @Override
        public void onNext(TelemetryFrame value) {
            if (throwOnNext) {
                throw (exceptionToThrow != null ? exceptionToThrow : new RuntimeException("Simulated stream error"));
            }
            receivedFrames.add(value);
        }

        @Override
        public void onError(Throwable t) {
            error.set(t);
        }

        @Override
        public void onCompleted() {
            if (throwOnCompleted) {
                throw new RuntimeException("Simulated complete error");
            }
            completed.set(true);
        }
    }

    static class TestServerCallStreamObserver<T> extends ServerCallStreamObserver<T> {
        final List<T> receivedFrames = new CopyOnWriteArrayList<>();
        final AtomicBoolean completed = new AtomicBoolean(false);
        final AtomicReference<Throwable> error = new AtomicReference<>(null);
        volatile boolean ready = true;
        volatile Runnable cancelHandler;
        volatile boolean cancelled = false;

        @Override
        public boolean isCancelled() {
            return cancelled;
        }

        @Override
        public void setOnCancelHandler(Runnable onCancelHandler) {
            this.cancelHandler = onCancelHandler;
        }

        @Override
        public void setCompression(String compression) {}

        @Override
        public boolean isReady() {
            return ready;
        }

        @Override
        public void setOnReadyHandler(Runnable onReadyHandler) {}

        @Override
        public void disableAutoInboundFlowControl() {}

        @Override
        public void request(int count) {}

        @Override
        public void setMessageCompression(boolean enable) {}

        @Override
        public void onNext(T value) {
            receivedFrames.add(value);
        }

        @Override
        public void onError(Throwable t) {
            error.set(t);
        }

        @Override
        public void onCompleted() {
            completed.set(true);
        }

        public void triggerCancel() {
            cancelled = true;
            if (cancelHandler != null) {
                cancelHandler.run();
            }
        }
    }

    private TelemetryFrame createSampleFrame(long tick, float health) {
        return TelemetryFrame.newBuilder()
                .setWorldTick(tick)
                .setStatus(AgentStatus.newBuilder()
                        .setHealth(health)
                        .setMaxHealth(20.0f)
                        .setFoodLevel(20)
                        .build())
                .build();
    }

    @Test
    @DisplayName("Register, unregister, contains check, and agent count tracking")
    void testRegistrationAndUnregistration() {
        StreamRegistry registry = new StreamRegistry();
        assertEquals(0, registry.getActiveAgentCount());
        assertTrue(registry.getRegisteredAgentIds().isEmpty());

        RecordingObserver obs1 = new RecordingObserver();
        RecordingObserver obs2 = new RecordingObserver();

        // New registration returns true
        assertTrue(registry.register("agent-1", obs1));
        assertEquals(1, registry.getActiveAgentCount());
        assertTrue(registry.isRegistered("agent-1"));
        assertSame(obs1, registry.getObserver("agent-1"));
        assertEquals(Set.of("agent-1"), registry.getRegisteredAgentIds());

        // Replaced registration returns false
        assertFalse(registry.register("agent-1", obs2));
        assertEquals(1, registry.getActiveAgentCount());
        assertSame(obs2, registry.getObserver("agent-1"));

        // Second distinct agent
        assertTrue(registry.register("agent-2", obs1));
        assertEquals(2, registry.getActiveAgentCount());
        assertTrue(registry.isRegistered("agent-2"));
        assertEquals(Set.of("agent-1", "agent-2"), registry.getRegisteredAgentIds());

        // Unregister existing
        assertTrue(registry.unregister("agent-1"));
        assertEquals(1, registry.getActiveAgentCount());
        assertFalse(registry.isRegistered("agent-1"));
        assertNull(registry.getObserver("agent-1"));

        // Unregister non-existent returns false
        assertFalse(registry.unregister("agent-1"));
        assertFalse(registry.unregister("non-existent"));
        assertFalse(registry.unregister(null));

        // Exact match overload unregister
        assertFalse(registry.unregister("agent-2", obs2)); // obs2 does not match obs1
        assertTrue(registry.isRegistered("agent-2"));
        assertTrue(registry.unregister("agent-2", obs1));  // obs1 matches
        assertFalse(registry.isRegistered("agent-2"));

        // Null checks
        assertFalse(registry.isRegistered(null));
        assertNull(registry.getObserver(null));
        assertThrows(NullPointerException.class, () -> registry.register(null, obs1));
        assertThrows(NullPointerException.class, () -> registry.register("agent-x", null));
    }

    @Test
    @DisplayName("Unicast sendFrame successfully delivers frame to target agent")
    void testSendFrameUnicastSuccess() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver observer = new RecordingObserver();
        registry.register("agent-alpha", observer);

        TelemetryFrame frame = createSampleFrame(42, 18.5f);
        boolean delivered = registry.sendFrame("agent-alpha", frame);

        assertTrue(delivered);
        assertEquals(1, observer.receivedFrames.size());
        assertEquals(42, observer.receivedFrames.get(0).getWorldTick());
        assertEquals(18.5f, observer.receivedFrames.get(0).getStatus().getHealth());

        // Delivery to unknown agent fails
        assertFalse(registry.sendFrame("agent-unknown", frame));
        assertFalse(registry.sendFrame(null, frame));
        assertFalse(registry.sendFrame("agent-alpha", null));
    }

    @Test
    @DisplayName("Unicast sendFrame handles stream exception by logging, unregistering agent, and returning false")
    void testSendFrameErrorHandling() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver brokenObserver = new RecordingObserver();
        brokenObserver.throwOnNext = true;
        brokenObserver.exceptionToThrow = new IllegalStateException("gRPC call was closed by peer");

        registry.register("agent-broken", brokenObserver);
        assertTrue(registry.isRegistered("agent-broken"));
        assertEquals(1, registry.getActiveAgentCount());

        TelemetryFrame frame = createSampleFrame(100, 20.0f);
        boolean result = registry.sendFrame("agent-broken", frame);

        assertFalse(result, "sendFrame must return false when exception occurs");
        assertFalse(registry.isRegistered("agent-broken"), "Failing agent must be automatically unregistered");
        assertNull(registry.getObserver("agent-broken"));
        assertEquals(0, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("Broadcast frame delivers to all active agents and evicts failing streams")
    void testBroadcastFrame() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver obs1 = new RecordingObserver();
        RecordingObserver obs2 = new RecordingObserver();
        RecordingObserver obs3 = new RecordingObserver();
        RecordingObserver obsBroken = new RecordingObserver();
        obsBroken.throwOnNext = true;

        registry.register("agent-1", obs1);
        registry.register("agent-2", obs2);
        registry.register("agent-3", obs3);
        registry.register("agent-4-broken", obsBroken);

        assertEquals(4, registry.getActiveAgentCount());

        TelemetryFrame frame = createSampleFrame(200, 15.0f);
        int deliveredCount = registry.broadcastFrame(frame);

        assertEquals(3, deliveredCount, "Should successfully deliver to 3 of 4 agents");
        assertEquals(1, obs1.receivedFrames.size());
        assertEquals(1, obs2.receivedFrames.size());
        assertEquals(1, obs3.receivedFrames.size());
        assertEquals(0, obsBroken.receivedFrames.size());

        assertFalse(registry.isRegistered("agent-4-broken"), "Broken observer should have been unregistered");
        assertEquals(3, registry.getActiveAgentCount());

        // Null broadcast returns 0
        assertEquals(0, registry.broadcastFrame(null));
    }

    @Test
    @DisplayName("closeAll completes all observers and clears registry safely")
    void testCloseAll() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver obs1 = new RecordingObserver();
        RecordingObserver obs2 = new RecordingObserver();
        RecordingObserver obsException = new RecordingObserver();
        obsException.throwOnCompleted = true; // Observer throws during onCompleted

        registry.register("agent-1", obs1);
        registry.register("agent-2", obs2);
        registry.register("agent-faulty", obsException);

        assertEquals(3, registry.getActiveAgentCount());

        registry.closeAll();

        assertEquals(0, registry.getActiveAgentCount());
        assertTrue(registry.getRegisteredAgentIds().isEmpty());
        assertFalse(registry.isRegistered("agent-1"));
        assertFalse(registry.isRegistered("agent-2"));
        assertFalse(registry.isRegistered("agent-faulty"));

        assertTrue(obs1.completed.get(), "obs1 must be completed");
        assertTrue(obs2.completed.get(), "obs2 must be completed");

        // Subsequent closeAll on empty registry is safe
        assertDoesNotThrow(registry::closeAll);
    }

    @Test
    @DisplayName("Concurrent registrations, unregistrations, and frame broadcasts are thread-safe")
    void testConcurrentOperations() throws InterruptedException {
        StreamRegistry registry = new StreamRegistry();
        int threadCount = 16;
        int operationsPerThread = 500;
        ExecutorService executor = Executors.newFixedThreadPool(threadCount);
        CountDownLatch startLatch = new CountDownLatch(1);
        CountDownLatch doneLatch = new CountDownLatch(threadCount);
        AtomicInteger successfulSends = new AtomicInteger(0);

        List<RecordingObserver> allObservers = new CopyOnWriteArrayList<>();

        for (int i = 0; i < threadCount; i++) {
            final int threadId = i;
            executor.submit(() -> {
                try {
                    startLatch.await();
                    TelemetryFrame frame = createSampleFrame(threadId, 20.0f);

                    for (int op = 0; op < operationsPerThread; op++) {
                        String agentId = "agent-" + (ThreadLocalRandom.current().nextInt(20));
                        int action = ThreadLocalRandom.current().nextInt(4);
                        switch (action) {
                            case 0 -> {
                                RecordingObserver obs = new RecordingObserver();
                                allObservers.add(obs);
                                registry.register(agentId, obs);
                            }
                            case 1 -> registry.unregister(agentId);
                            case 2 -> {
                                if (registry.sendFrame(agentId, frame)) {
                                    successfulSends.incrementAndGet();
                                }
                            }
                            case 3 -> registry.broadcastFrame(frame);
                        }
                    }
                } catch (Exception e) {
                    fail("Thread " + threadId + " encountered unexpected exception: " + e.getMessage());
                } finally {
                    doneLatch.countDown();
                }
            });
        }

        startLatch.countDown();
        assertTrue(doneLatch.await(10, TimeUnit.SECONDS), "Concurrent test tasks timed out");
        executor.shutdown();
        assertTrue(executor.awaitTermination(2, TimeUnit.SECONDS));

        // Registry should still be operable and consistent
        assertTrue(registry.getActiveAgentCount() >= 0);
        registry.closeAll();
        assertEquals(0, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl registers agent on intent and unregisters on complete/error")
    void testBridgeServiceImplLifecycleIntegration() {
        StreamRegistry registry = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(registry);
        assertSame(registry, service.getStreamRegistry());

        RecordingObserver responseObserver = new RecordingObserver();
        StreamObserver<AgentIntent> requestObserver = service.connectAgent(responseObserver);

        // connectAgent immediately sends initial TelemetryFrame probe
        assertEquals(1, responseObserver.receivedFrames.size());
        assertEquals(0, registry.getActiveAgentCount(), "Agent should not yet be registered before intent");

        // Send first intent from agent-beta
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-beta")
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(42).build())
                .build());

        assertTrue(registry.isRegistered("agent-beta"));
        assertSame(responseObserver, registry.getObserver("agent-beta"));
        assertEquals(1, registry.getActiveAgentCount());

        // Agent completes stream
        requestObserver.onCompleted();

        assertFalse(registry.isRegistered("agent-beta"), "Agent should be unregistered after stream completion");
        assertEquals(0, registry.getActiveAgentCount());
        assertTrue(responseObserver.completed.get());

        // Test error lifecycle with another agent
        RecordingObserver responseObserver2 = new RecordingObserver();
        StreamObserver<AgentIntent> requestObserver2 = service.connectAgent(responseObserver2);

        requestObserver2.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-gamma")
                .build());
        assertTrue(registry.isRegistered("agent-gamma"));

        requestObserver2.onError(new RuntimeException("Simulated agent connection drop"));
        assertFalse(registry.isRegistered("agent-gamma"), "Agent should be unregistered on stream error");
        assertEquals(0, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("CivQuestBridgeServer manages StreamRegistry lifecycle and closes observers on stop")
    void testBridgeServerStreamRegistryLifecycle() throws IOException {
        Path discoveryPath = tempDir.resolve("server-test-meta.json");
        CivQuestBridgeServer server = new CivQuestBridgeServer("127.0.0.1", 0, "test-key-" + UUID.randomUUID(), discoveryPath);
        StreamRegistry registry = server.getStreamRegistry();
        assertNotNull(registry);

        server.start();
        assertSame(registry, server.getBridgeService().getStreamRegistry());

        RecordingObserver observer = new RecordingObserver();
        registry.register("agent-standalone", observer);
        assertEquals(1, registry.getActiveAgentCount());

        server.stop();

        // Server stop must invoke streamRegistry.closeAll()
        assertEquals(0, registry.getActiveAgentCount());
        assertTrue(observer.completed.get(), "Observer must be marked completed on server stop");
    }

    @Test
    @DisplayName("close(agentId) atomically unregisters and completes observer")
    void testCloseIndividualAgent() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver obs1 = new RecordingObserver();
        RecordingObserver obs2 = new RecordingObserver();

        registry.register("agent-1", obs1);
        registry.register("agent-2", obs2);
        assertEquals(2, registry.getActiveAgentCount());

        // Close agent-1 completes obs1 and removes it
        assertTrue(registry.close("agent-1"));
        assertTrue(obs1.completed.get(), "obs1 must be completed on close");
        assertFalse(registry.isRegistered("agent-1"));
        assertEquals(1, registry.getActiveAgentCount());

        // Closing again returns false
        assertFalse(registry.close("agent-1"));
        assertFalse(registry.close(null));

        // Exact match close overload
        assertFalse(registry.close("agent-2", obs1), "Closing with mismatching observer must return false");
        assertFalse(obs2.completed.get());
        assertTrue(registry.isRegistered("agent-2"));

        assertTrue(registry.close("agent-2", obs2), "Closing with matching observer must return true");
        assertTrue(obs2.completed.get(), "obs2 must be completed on close");
        assertFalse(registry.isRegistered("agent-2"));
        assertEquals(0, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("DespawnAgentEntity in CivQuestBridgeServiceImpl closes agent stream")
    void testDespawnAgentEntityClosesStream() {
        StreamRegistry registry = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(registry);

        RecordingObserver streamObserver = new RecordingObserver();
        registry.register("agent-despawn", streamObserver);
        assertTrue(registry.isRegistered("agent-despawn"));

        RecordingObserver despawnResponseObserver = new RecordingObserver();
        service.despawnAgentEntity(
                DespawnAgentRequest.newBuilder().setAgentId("agent-despawn").build(),
                new StreamObserver<>() {
                    @Override
                    public void onNext(DespawnAgentResponse value) {
                        assertTrue(value.getSuccess());
                    }

                    @Override
                    public void onError(Throwable t) {
                        fail("Unexpected despawn error: " + t.getMessage());
                    }

                    @Override
                    public void onCompleted() {}
                }
        );

        assertFalse(registry.isRegistered("agent-despawn"), "Agent must be closed and removed after despawn");
        assertTrue(streamObserver.completed.get(), "Agent stream observer must be completed on despawn");
    }

    @Test
    @DisplayName("Duplicate registration cleanly completes superseded observer")
    void testDuplicateRegistrationCompletesReplacedObserver() {
        StreamRegistry registry = new StreamRegistry();
        RecordingObserver obs1 = new RecordingObserver();
        RecordingObserver obs2 = new RecordingObserver();

        assertTrue(registry.register("agent-dup", obs1));
        assertFalse(obs1.completed.get());

        // Registering obs2 replaces obs1 and completes obs1
        assertFalse(registry.register("agent-dup", obs2));
        assertTrue(obs1.completed.get(), "Superseded observer must be completed");
        assertFalse(obs2.completed.get(), "New observer must remain open");
        assertSame(obs2, registry.getObserver("agent-dup"));

        // Re-registering the same observer does not complete it
        assertFalse(registry.register("agent-dup", obs2));
        assertFalse(obs2.completed.get(), "Re-registering same observer instance should not complete it");
    }

    @Test
    @DisplayName("Registration is rejected and completes observer post-closeAll")
    void testRegistrationRejectionPostCloseAll() {
        StreamRegistry registry = new StreamRegistry();
        assertFalse(registry.isClosed());

        registry.closeAll();
        assertTrue(registry.isClosed());

        // Subsequent closeAll is idempotent
        registry.closeAll();
        assertTrue(registry.isClosed());

        RecordingObserver lateObserver = new RecordingObserver();
        boolean registered = registry.register("agent-late", lateObserver);

        assertFalse(registered, "Registering on closed registry must return false");
        assertTrue(lateObserver.completed.get(), "Observer passed to closed registry must be completed");
        assertEquals(0, registry.getActiveAgentCount());
        assertFalse(registry.isRegistered("agent-late"));

        TelemetryFrame frame = createSampleFrame(50, 20.0f);
        assertFalse(registry.sendFrame("agent-late", frame));
        assertEquals(0, registry.broadcastFrame(frame));
    }

    @Test
    @DisplayName("Flow control drops stale telemetry frames when observer is not ready (backpressure)")
    void testFlowControlBackpressureDropsStaleFrame() {
        StreamRegistry registry = new StreamRegistry();
        TestServerCallStreamObserver<TelemetryFrame> slowObserver = new TestServerCallStreamObserver<>();
        RecordingObserver normalObserver = new RecordingObserver();

        registry.register("agent-slow", slowObserver);
        registry.register("agent-normal", normalObserver);

        TelemetryFrame frame1 = createSampleFrame(1, 20.0f);
        assertTrue(registry.sendFrame("agent-slow", frame1));
        assertEquals(1, slowObserver.receivedFrames.size());

        // Simulate backpressure: client buffer full
        slowObserver.ready = false;

        TelemetryFrame frame2 = createSampleFrame(2, 19.0f);
        boolean sent = registry.sendFrame("agent-slow", frame2);
        assertFalse(sent, "sendFrame must drop frame and return false when observer is not ready");
        assertEquals(1, slowObserver.receivedFrames.size(), "Stale frame must not be delivered");
        assertTrue(registry.isRegistered("agent-slow"), "Backpressured agent must NOT be evicted from registry");

        // Broadcast should deliver to normalObserver while skipping backpressured slowObserver
        TelemetryFrame frame3 = createSampleFrame(3, 18.0f);
        int delivered = registry.broadcastFrame(frame3);
        assertEquals(1, delivered, "Broadcast must only deliver to ready observer");
        assertEquals(1, slowObserver.receivedFrames.size());
        assertEquals(1, normalObserver.receivedFrames.size());

        // Simulate client catching up: stream becomes ready again
        slowObserver.ready = true;
        TelemetryFrame frame4 = createSampleFrame(4, 17.0f);
        assertTrue(registry.sendFrame("agent-slow", frame4));
        assertEquals(2, slowObserver.receivedFrames.size());
    }

    @Test
    @DisplayName("Agent ID mutation attempts on an established stream are rejected with PERMISSION_DENIED")
    void testAgentIdMutationRejectedWithPermissionDenied() {
        StreamRegistry registry = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(registry);

        RecordingObserver responseObserver = new RecordingObserver();
        StreamObserver<AgentIntent> requestObserver = service.connectAgent(responseObserver);

        // Bind stream to agent-alpha on first valid intent
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-alpha")
                .build());

        assertTrue(registry.isRegistered("agent-alpha"));
        assertNull(responseObserver.error.get());

        // Subsequent intent with same agent ID is accepted
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-alpha")
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(10).build())
                .build());
        assertNull(responseObserver.error.get());

        // Attempting to mutate stream to agent-bravo must be rejected
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-bravo")
                .build());

        assertNotNull(responseObserver.error.get(), "Stream should be terminated with an error");
        Status status = Status.fromThrowable(responseObserver.error.get());
        assertEquals(Status.Code.PERMISSION_DENIED, status.getCode());
        assertFalse(registry.isRegistered("agent-alpha"), "Agent should be unregistered after fatal error");
        assertFalse(registry.isRegistered("agent-bravo"));
    }

    @Test
    @DisplayName("Blank or empty agent IDs are rejected with INVALID_ARGUMENT")
    void testBlankOrEmptyAgentIdRejectedWithInvalidArgument() {
        StreamRegistry registry = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(registry);

        // Test empty string
        RecordingObserver responseObserver1 = new RecordingObserver();
        StreamObserver<AgentIntent> requestObserver1 = service.connectAgent(responseObserver1);

        requestObserver1.onNext(AgentIntent.newBuilder()
                .setAgentId("")
                .build());

        assertNotNull(responseObserver1.error.get());
        assertEquals(Status.Code.INVALID_ARGUMENT, Status.fromThrowable(responseObserver1.error.get()).getCode());
        assertEquals(0, registry.getActiveAgentCount());

        // Test whitespace blank string
        RecordingObserver responseObserver2 = new RecordingObserver();
        StreamObserver<AgentIntent> requestObserver2 = service.connectAgent(responseObserver2);

        requestObserver2.onNext(AgentIntent.newBuilder()
                .setAgentId("   ")
                .build());

        assertNotNull(responseObserver2.error.get());
        assertEquals(Status.Code.INVALID_ARGUMENT, Status.fromThrowable(responseObserver2.error.get()).getCode());
        assertEquals(0, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("Abrupt client disconnect triggers cancellation handler and unregisters agent")
    void testCancellationHandlerUnregistration() {
        StreamRegistry registry = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(registry);

        TestServerCallStreamObserver<TelemetryFrame> responseObserver = new TestServerCallStreamObserver<>();
        StreamObserver<AgentIntent> requestObserver = service.connectAgent(responseObserver);

        assertNotNull(responseObserver.cancelHandler, "Cancellation handler must be registered on ServerCallStreamObserver");

        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-disconnect")
                .build());

        assertTrue(registry.isRegistered("agent-disconnect"));

        // Simulate abrupt client network reset or cancel
        responseObserver.triggerCancel();

        assertFalse(registry.isRegistered("agent-disconnect"), "Agent must be unregistered upon stream cancellation");
        assertEquals(0, registry.getActiveAgentCount());
    }
}
