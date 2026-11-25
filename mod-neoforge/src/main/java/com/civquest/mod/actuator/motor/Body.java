package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.HorizonSector;
import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.Vec3;

/**
 * Narrow, lock-free view of the agent handed to {@link Motor} hooks (ADR-036 section 4).
 * It deliberately exposes no {@code AgentMotorFSM}, {@code MotorStack} or other locking
 * object, so a hook cannot re-enter a synchronized method by construction. Later beads
 * add read-only sensors and non-locking actuator calls here as motors migrate.
 */
public interface Body {
    /** Current game tick (20 Hz server clock). */
    long tick();

    double x();

    double y();

    double z();

    /** Turns the agent to a yaw bearing (survey sweep). Default: no-op. */
    default void faceBearing(float yawDeg) {
    }

    /** Scans one horizon sector from the agent's eye. Default: open-plains placeholder. */
    default HorizonSector scanHorizonSector(Vec3 origin, int sectorIndex, float bearingDeg, float maxRange) {
        return HorizonSector.newBuilder()
                .setSectorIndex(sectorIndex)
                .setBearingDeg(bearingDeg)
                .setHitDistance(maxRange)
                .setFeatureType("OPEN_PLAINS")
                .setHitBlockId("minecraft:air")
                .setOpenHorizon(true)
                .setOccluded(false)
                .build();
    }

    /** Hands a completed horizon survey to the telemetry path. Default: dropped. */
    default void publishHorizonSurvey(HorizonSurveyReport report) {
    }

    /** True while the agent is holding a use-item action (eating, shield, bow). Default: false. */
    default boolean isUsingItem() {
        return false;
    }

    /** Lowers the shield if it is the item being used. Default: no-op. */
    default void stopShieldUse() {
    }

    /** Advances the navigator one tick if it is currently pathing. Default: no-op. */
    default void tickNavigatorIfPathing() {
    }

    /** The owning agent for motors that drive the world directly; null for detached test bodies. */
    default AgentEntity agent() {
        return null;
    }

    /** Diagnostic log line; never throws. */
    void log(String message);
}
