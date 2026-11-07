package com.civquest.mod;

import com.civquest.mod.grpc.v1.TelemetryFrame;
import io.grpc.stub.StreamObserver;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Collections;
import java.util.HashSet;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Thread-safe registry mapping connected agent IDs to their active gRPC TelemetryFrame stream observers.
 * Provides unicast, broadcast, and lifecycle completion capabilities for agent telemetry feeds.
 */
public class StreamRegistry {
    private static final Logger LOGGER = LoggerFactory.getLogger(StreamRegistry.class);

    private final Map<String, StreamObserver<TelemetryFrame>> observers = new ConcurrentHashMap<>();

    /**
     * Registers an observer for the specified agent.
     *
     * @param agentId  the unique identifier of the agent
     * @param observer the gRPC stream observer for delivering telemetry frames
     * @return true if newly registered, false if an existing observer was replaced or updated
     */
    public boolean register(String agentId, StreamObserver<TelemetryFrame> observer) {
        Objects.requireNonNull(agentId, "agentId must not be null");
        Objects.requireNonNull(observer, "observer must not be null");
        StreamObserver<TelemetryFrame> previous = observers.put(agentId, observer);
        return previous == null;
    }

    /**
     * Unregisters the observer for the specified agent if present.
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
     * Thread-safely sends a telemetry frame to the specified agent.
     * Catches any runtime exceptions (e.g. stream terminated, closed), logs a warning,
     * unregisters the agent, and returns false.
     *
     * @param agentId the unique identifier of the recipient agent
     * @param frame   the telemetry frame to deliver
     * @return true if the frame was delivered successfully, false otherwise
     */
    public boolean sendFrame(String agentId, TelemetryFrame frame) {
        if (agentId == null || frame == null) {
            return false;
        }
        StreamObserver<TelemetryFrame> observer = observers.get(agentId);
        if (observer == null) {
            return false;
        }
        try {
            synchronized (observer) {
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
     * Broadcasts a telemetry frame to all actively registered agents.
     *
     * @param frame the telemetry frame to broadcast
     * @return count of successful deliveries
     */
    public int broadcastFrame(TelemetryFrame frame) {
        if (frame == null) {
            return 0;
        }
        int delivered = 0;
        for (String agentId : observers.keySet()) {
            if (sendFrame(agentId, frame)) {
                delivered++;
            }
        }
        return delivered;
    }

    /**
     * Completes all open observers via onCompleted() and clears the registry.
     * Safe against concurrent modifications.
     */
    public void closeAll() {
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
        observers.clear();
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
