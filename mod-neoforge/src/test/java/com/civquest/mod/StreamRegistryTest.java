package com.civquest.mod;

import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
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
}
