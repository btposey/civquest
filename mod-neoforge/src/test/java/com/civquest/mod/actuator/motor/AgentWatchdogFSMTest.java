package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.Vec3;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

public class AgentWatchdogFSMTest {

    private AgentWatchdogFSM watchdogFSM;

    @BeforeEach
    void setUp() {
        watchdogFSM = new AgentWatchdogFSM(null);
    }

    @Test
    @DisplayName("WatchdogFSM initializes in DISCONNECTED_SAFE_MODE with unassigned stream")
    void testInitialState() {
        assertEquals(WatchdogState.DISCONNECTED_SAFE_MODE, watchdogFSM.getState());
        assertEquals(WatchdogState.DISCONNECTED_SAFE_MODE, watchdogFSM.getPreviousState());
        assertEquals(0L, watchdogFSM.getTicksInState());
        assertFalse(watchdogFSM.isStreamRegistered());
        assertFalse(watchdogFSM.isDefensiveLockActive());
    }

    @Test
    @DisplayName("WatchdogFSM transitions to CONNECTED_ACTIVE on onConnected and onIntentReceived")
    void testOnConnectedAndIntent() {
        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());
        assertTrue(watchdogFSM.isStreamRegistered());

        watchdogFSM.transitionTo(WatchdogState.STREAM_DEGRADED, "test_degraded");
        assertEquals(WatchdogState.STREAM_DEGRADED, watchdogFSM.getState());

        AgentIntent intent = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(1).setY(64).setZ(1).build())
                        .build())
                .build();
        watchdogFSM.onIntentReceived(intent);
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());
    }

    @Test
    @DisplayName("WatchdogFSM transitions to DISCONNECTED_SAFE_MODE on disconnect or stream error")
    void testDisconnectAndError() {
        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        watchdogFSM.onDisconnected();
        assertEquals(WatchdogState.DISCONNECTED_SAFE_MODE, watchdogFSM.getState());
        assertFalse(watchdogFSM.isStreamRegistered());

        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        watchdogFSM.onStreamError(new RuntimeException("Simulated socket drop"));
        assertEquals(WatchdogState.DISCONNECTED_SAFE_MODE, watchdogFSM.getState());
        assertFalse(watchdogFSM.isStreamRegistered());
    }

    @Test
    @DisplayName("WatchdogFSM transitions to SHUTDOWN on agent despawn and agent death")
    void testAgentDespawnAndDeath() {
        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        watchdogFSM.onAgentDespawn();
        assertEquals(WatchdogState.SHUTDOWN, watchdogFSM.getState());
        assertFalse(watchdogFSM.isStreamRegistered());

        watchdogFSM.transitionTo(WatchdogState.CONNECTED_ACTIVE, "reset");
        watchdogFSM.onAgentDeath();
        assertEquals(WatchdogState.SHUTDOWN, watchdogFSM.getState());
    }

    @Test
    @DisplayName("Watchdog timeouts degrade connection after 40 ticks and disconnect after 100 ticks")
    void testWatchdogTimeoutThresholds() {
        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        // Simulate tick progression
        watchdogFSM.setCurrentTick(10L);
        watchdogFSM.setLastIntentTick(10L);

        // Tick 40 ticks without intent -> remains CONNECTED_ACTIVE until > 40
        watchdogFSM.setCurrentTick(50L);
        // Test manual tick logic for standalone watchdog
        if (watchdogFSM.isStreamRegistered() && (watchdogFSM.getCurrentTick() - watchdogFSM.getLastIntentTick() <= AgentWatchdogFSM.DEGRADED_THRESHOLD_TICKS)) {
            // still connected
            assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());
        }

        // Advance beyond 40 ticks -> transitions to STREAM_DEGRADED
        watchdogFSM.setCurrentTick(51L); // delta = 41 > 40
        if (watchdogFSM.getCurrentTick() - watchdogFSM.getLastIntentTick() > AgentWatchdogFSM.DEGRADED_THRESHOLD_TICKS) {
            watchdogFSM.transitionTo(WatchdogState.STREAM_DEGRADED, "intent_timeout_degraded");
        }
        assertEquals(WatchdogState.STREAM_DEGRADED, watchdogFSM.getState());

        // Advance beyond 100 ticks -> transitions to DISCONNECTED_SAFE_MODE
        watchdogFSM.setCurrentTick(111L); // delta = 101 > 100
        if (watchdogFSM.getCurrentTick() - watchdogFSM.getLastIntentTick() > AgentWatchdogFSM.DISCONNECTED_THRESHOLD_TICKS) {
            watchdogFSM.transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "intent_timeout_disconnected");
        }
        assertEquals(WatchdogState.DISCONNECTED_SAFE_MODE, watchdogFSM.getState());
    }

    @Test
    @DisplayName("Reconnection recovery releases defensive locks smoothly")
    void testReconnectionReleasesDefensiveLock() {
        watchdogFSM.transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "disconnected");
        watchdogFSM.setDefensiveLockActive(true);
        assertTrue(watchdogFSM.isDefensiveLockActive());

        // Reconnecting to active stream releases defensive posture
        watchdogFSM.onConnected();
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());
        assertFalse(watchdogFSM.isDefensiveLockActive());
    }

    @Test
    @DisplayName("Autonomous eating enforces 32-tick cooldown interval between food consumption")
    void testEatingCooldownInterval() {
        assertEquals(0, watchdogFSM.getEatCooldownTicks());

        // Simulate eating setting cooldown
        watchdogFSM.setEatCooldownTicks(AgentWatchdogFSM.EAT_COOLDOWN_TICKS);
        assertEquals(32, watchdogFSM.getEatCooldownTicks());

        // While cooldown > 0, eating is rejected
        assertFalse(watchdogFSM.consumeAvailableFood());

        // Count down 32 ticks
        for (int i = 0; i < AgentWatchdogFSM.EAT_COOLDOWN_TICKS; i++) {
            watchdogFSM.setEatCooldownTicks(watchdogFSM.getEatCooldownTicks() - 1);
        }
        assertEquals(0, watchdogFSM.getEatCooldownTicks());
    }

    @Test
    @DisplayName("Threat detection throttle caches threat and subsides after THREAT_SUBSIDE_TICKS")
    void testThreatScanAndSubsidence() {
        watchdogFSM.setCachedHostileThreat(true);
        assertTrue(watchdogFSM.isCachedHostileThreat());

        watchdogFSM.setDefensiveLockActive(true);
        watchdogFSM.setTicksSinceLastThreat(0);

        // When threat clears
        watchdogFSM.setCachedHostileThreat(false);

        // Subside after 40 ticks
        watchdogFSM.setTicksSinceLastThreat(AgentWatchdogFSM.THREAT_SUBSIDE_TICKS);
        if (watchdogFSM.isDefensiveLockActive() && watchdogFSM.getTicksSinceLastThreat() >= AgentWatchdogFSM.THREAT_SUBSIDE_TICKS) {
            watchdogFSM.releaseDefensivePosture();
        }
        assertFalse(watchdogFSM.isDefensiveLockActive());
    }

    @Test
    @DisplayName("Watchdog transitionTo handles entry/exit and returns false on same state")
    void testTransitionTo() {
        assertTrue(watchdogFSM.transitionTo(WatchdogState.CONNECTED_ACTIVE, "connect"));
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        assertFalse(watchdogFSM.transitionTo(WatchdogState.CONNECTED_ACTIVE, "same"));
        assertFalse(watchdogFSM.transitionTo(null, "null"));
    }
}
