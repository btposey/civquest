package com.civquest.mod.actuator.intent;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.motor.Motor;
import com.civquest.mod.grpc.v1.AgentIntent;

/**
 * Factory from one {@link AgentIntent} to the {@link Motor} that executes it (cq-2sr.16). Registered per
 * {@code ActionCase} in the {@link com.civquest.mod.actuator.IntentActuator} dispatch table.
 */
@FunctionalInterface
public interface IntentHandler {
    /** Returns the motor for {@code intent}, or null when the intent is already satisfied and needs no frame. */
    Motor create(AgentEntity agent, AgentIntent intent);
}
