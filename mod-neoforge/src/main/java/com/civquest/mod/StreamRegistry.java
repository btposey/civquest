package com.civquest.mod;

import com.civquest.mod.grpc.v1.TelemetryFrame;
import io.grpc.stub.CallStreamObserver;
import io.grpc.stub.StreamObserver;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Collections;
import java.util.HashSet;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Thread-safe registry mapping connected agent IDs to their active gRPC TelemetryFrame stream observers.
 * Provides unicast, broadcast, flow-control backpressure evaluation, and lifecycle completion capabilities
 * for agent telemetry feeds.
 */
public class StreamRegistry {
    private static final Logger LOGGER = LoggerFactory.getLogger(StreamRegistry.class);

    private final Map<String, StreamObserver<TelemetryFrame>> observers = new ConcurrentHashMap<>();
    private final AtomicBoolean closed = new AtomicBoolean(false);

    /**
     * Checks if this StreamRegistry has been closed.
     *
     * @return true if closeAll() has been invoked, false otherwise
     */
    public boolean isClosed() {
        return closed.get();
    }

    /**
     * Registers an observer for the specified agent.
     * <p>
     * If the registry is closed, the observer is completed immediately under synchronization and registration is rejected.
     * If an existing observer was registered for this agent and is different from {@code observer},
     * the superseded observer is completed cleanly under synchronization.
     *
     * @param agentId  the unique identifier of the agent
     * @param observer the gRPC stream observer for delivering telemetry frames
     * @return true if newly registered, false if registry is closed or an existing observer was replaced or updated
     */
    public boolean register(String agentId, StreamObserver<TelemetryFrame> observer) {
        Objects.requireNonNull(agentId, "agentId must not be null");
        Objects.requireNonNull(observer, "observer must not be null");

        if (closed.get()) {
            LOGGER.warn("Cannot register agent '{}': StreamRegistry is closed", agentId);
            try {
                synchronized (observer) {
                    observer.onCompleted();
                }
            } catch (Throwable t) {
                LOGGER.debug("Error completing observer during rejected registration on closed registry: {}", t.getMessage());
            }
            return false;
        }

        StreamObserver<TelemetryFrame> previous = observers.put(agentId, observer);

        if (closed.get()) {
            observers.remove(agentId, observer);
            try {
                synchronized (observer) {
                    observer.onCompleted();
                }
            } catch (Throwable t) {
                LOGGER.debug("Error completing observer during rejected registration on closed registry: {}", t.getMessage());
            }
            if (previous != null && previous != observer) {
                try {
                    synchronized (previous) {
                        previous.onCompleted();
                    }
                } catch (Throwable t) {
                    LOGGER.warn("Error completing superseded observer for agent '{}': {}", agentId, t.getMessage());
                }
            }
            return false;
        }

        if (previous != null && previous != observer) {
            LOGGER.info("Superseding existing observer for agent '{}'", agentId);
            try {
                synchronized (previous) {
                    previous.onCompleted();
                }
            } catch (Throwable t) {
                LOGGER.warn("Error completing superseded observer for agent '{}': {}", agentId, t.getMessage());
            }
        }
        return previous == null;
    }

    /**
     * Unregisters the observer for the specified agent if present without completing it.
     *
     * @param agentId the unique identifier of the agent
     * @return true if an observer was present and removed, false otherwise
     */
    public boolean unregister(String agentId) {
        if (agentId == null) {
            return false;
        }
        return observers.remove(agentId) != null;
    }

    /**
     * Unregisters the observer for the specified agent only if currently mapped to the given observer.
     *
     * @param agentId  the unique identifier of the agent
     * @param observer the expected observer instance
     * @return true if removed, false otherwise
     */
    public boolean unregister(String agentId, StreamObserver<TelemetryFrame> observer) {
        if (agentId == null || observer == null) {
            return false;
        }
        return observers.remove(agentId, observer);
    }

    /**
     * Atomically unregisters and completes the stream observer for the specified agent.
     *
     * @param agentId the unique identifier of the agent
     * @return true if an observer was present and completed, false otherwise
     */
    public boolean close(String agentId) {
        if (agentId == null) {
            return false;
        }
        StreamObserver<TelemetryFrame> observer = observers.remove(agentId);
        if (observer == null) {
            return false;
        }
        try {
            synchronized (observer) {
                observer.onCompleted();
            }
            return true;
        } catch (Throwable t) {
            LOGGER.warn("Failed to cleanly complete stream during close for agent '{}': {}", agentId, t.getMessage());
            return false;
        }
    }

    /**
     * Atomically unregisters and completes the stream observer for the specified agent only if mapped
     * to the expected observer.
     *
     * @param agentId          the unique identifier of the agent
     * @param expectedObserver the expected stream observer instance
     * @return true if matching observer was present and completed, false otherwise
     */
    public boolean close(String agentId, StreamObserver<TelemetryFrame> expectedObserver) {
        if (agentId == null || expectedObserver == null) {
            return false;
        }
        boolean removed = observers.remove(agentId, expectedObserver);
        if (!removed) {
            return false;
        }
        try {
            synchronized (expectedObserver) {
                expectedObserver.onCompleted();
            }
            return true;
        } catch (Throwable t) {
            LOGGER.warn("Failed to cleanly complete expected stream during close for agent '{}': {}", agentId, t.getMessage());
            return false;
        }
    }

    /**
     * Retrieves the active stream observer for the specified agent.
     *
     * @param agentId the unique identifier of the agent
     * @return the stream observer, or null if not registered
     */
    public StreamObserver<TelemetryFrame> getObserver(String agentId) {
        if (agentId == null) {
            return null;
        }
        return observers.get(agentId);
    }

    /**
     * Delivers a telemetry frame to the given observer with flow-control backpressure evaluation.
     *
     * @param agentId  the agent identifier
     * @param observer the observer to send to
     * @param frame    the telemetry frame to deliver
     * @param force    if true, bypasses backpressure checks (for terminal death frames or critical events)
     * @return true if delivered, false if dropped due to backpressure or delivery failure
     */
    private boolean sendFrameToObserver(String agentId, StreamObserver<TelemetryFrame> observer, TelemetryFrame frame, boolean force) {
        if (!force && observer instanceof CallStreamObserver<?> callObserver && !callObserver.isReady()) {
            LOGGER.warn("Dropping telemetry frame for agent '{}': stream is not ready (backpressure)", agentId);
            return false;
        }
        try {
            synchronized (observer) {
                if (!force && observer instanceof CallStreamObserver<?> callObserver && !callObserver.isReady()) {
                    LOGGER.warn("Dropping telemetry frame for agent '{}': stream is not ready (backpressure)", agentId);
                    return false;
                }
                observer.onNext(frame);
            }
            return true;
        } catch (Throwable t) {
            LOGGER.warn("Failed to send TelemetryFrame to agent '{}': {}", agentId, t.getMessage());
            unregister(agentId, observer);
            return false;
        }
    }

    /**
     * Thread-safely sends a telemetry frame to the specified agent.
     * Evaluates gRPC flow-control ready state, dropping stale frames if backpressure is detected.
     * Catches any runtime exceptions (e.g. stream terminated, closed), logs a warning,
     * unregisters the agent, and returns false.
     *
     * @param agentId the unique identifier of the recipient agent
     * @param frame   the telemetry frame to deliver
     * @return true if the frame was delivered successfully, false otherwise
     */
    public boolean sendFrame(String agentId, TelemetryFrame frame) {
        if (agentId == null || frame == null || closed.get()) {
            return false;
        }
        StreamObserver<TelemetryFrame> observer = observers.get(agentId);
        if (observer == null) {
            return false;
        }
        return sendFrameToObserver(agentId, observer, frame, false);
    }

    /**
     * Thread-safely sends a critical telemetry frame (such as a terminal death frame) to the specified agent,
     * bypassing advisory backpressure checks to ensure guaranteed delivery.
     *
     * @param agentId the unique identifier of the recipient agent
     * @param frame   the critical telemetry frame to deliver
     * @return true if the frame was delivered successfully, false otherwise
     */
    public boolean sendCriticalFrame(String agentId, TelemetryFrame frame) {
        if (agentId == null || frame == null || closed.get()) {
            return false;
        }
        StreamObserver<TelemetryFrame> observer = observers.get(agentId);
        if (observer == null) {
            return false;
        }
        return sendFrameToObserver(agentId, observer, frame, true);
    }

    /**
     * Broadcasts a telemetry frame to all actively registered agents.
     * Directly iterates over registered stream observers without redundant key lookups,
     * applying flow control and evicting broken streams safely.
     *
     * @param frame the telemetry frame to broadcast
     * @return count of successful deliveries
     */
    public int broadcastFrame(TelemetryFrame frame) {
        if (frame == null || closed.get()) {
            return 0;
        }
        int delivered = 0;
        for (Map.Entry<String, StreamObserver<TelemetryFrame>> entry : observers.entrySet()) {
            if (sendFrameToObserver(entry.getKey(), entry.getValue(), frame, false)) {
                delivered++;
            }
        }
        return delivered;
    }

    /**
     * Completes all open observers via onCompleted() under synchronization and clears the registry.
     * This method is idempotent; subsequent calls return immediately.
     */
    public void closeAll() {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        LOGGER.info("Closing all agent streams in StreamRegistry (active count: {})", observers.size());
        Map<String, StreamObserver<TelemetryFrame>> snapshot = new ConcurrentHashMap<>(observers);
        observers.clear();

        for (Map.Entry<String, StreamObserver<TelemetryFrame>> entry : snapshot.entrySet()) {
            String agentId = entry.getKey();
            StreamObserver<TelemetryFrame> observer = entry.getValue();
            try {
                synchronized (observer) {
                    observer.onCompleted();
                }
            } catch (Throwable t) {
                LOGGER.warn("Failed to cleanly complete stream for agent '{}': {}", agentId, t.getMessage());
            }
        }
    }

    /**
     * Reopens the StreamRegistry, resetting the closed state so new streams can be registered.
     */
    public void reopen() {
        if (closed.compareAndSet(true, false)) {
            LOGGER.info("Reopened StreamRegistry for new agent connections");
        }
    }

    /**
     * Returns the number of actively registered agent streams.
     *
     * @return the active agent count
     */
    public int getActiveAgentCount() {
        return observers.size();
    }

    /**
     * Returns an immutable snapshot set of all registered agent IDs.
     *
     * @return set of registered agent IDs
     */
    public Set<String> getRegisteredAgentIds() {
        return Collections.unmodifiableSet(new HashSet<>(observers.keySet()));
    }

    /**
     * Checks if the specified agent is currently registered.
     *
     * @param agentId the agent ID to check
     * @return true if registered, false otherwise
     */
    public boolean isRegistered(String agentId) {
        return agentId != null && observers.containsKey(agentId);
    }
}
