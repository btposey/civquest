package com.civquest.mod.actuator.motor;

/**
 * State definitions for the Connection Watchdog and Autonomous Fail-Safe FSM.
 * Tracks the gRPC intent stream health for an {@link com.civquest.mod.AgentEntity}
 * and governs in-engine autonomous survival reflexes during stream disruption.
 */
public enum WatchdogState {
    /** Active gRPC intent stream receiving frequent heartbeats and intent updates. */
    CONNECTED_ACTIVE,

    /** Stream is registered but no intents or heartbeats received within degraded threshold (>40 ticks / 2s). */
    STREAM_DEGRADED,

    /** Stream disconnected, unregistered, or timed out (>100 ticks); autonomous fail-safe active. */
    DISCONNECTED_SAFE_MODE,

    /** Agent entity has died or been despawned; watchdog inactive. */
    SHUTDOWN
}
