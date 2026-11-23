package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.AgentIntent;

import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

/**
 * Snapshot of a suspended motor execution frame stored on the {@link AgentMotorFSM} PDA stack.
 * Preserves the interrupted {@link MotorState}, originating {@link AgentIntent}, progress metrics,
 * and subsystem context (e.g., break coordinates, navigation parameters) for zero-amnesia resumption.
 */
public class MotorStackFrame {
    private final MotorState state;
    private final AgentIntent intent;
    private final int progressTicks;
    private final double lastProgressValue;
    private final Map<String, Object> context;

    public MotorStackFrame(MotorState state,
                           AgentIntent intent,
                           int progressTicks,
                           double lastProgressValue,
                           Map<String, Object> context) {
        this.state = state;
        this.intent = intent;
        this.progressTicks = progressTicks;
        this.lastProgressValue = lastProgressValue;
        this.context = context != null ? new HashMap<>(context) : new HashMap<>();
    }

    public MotorState getState() {
        return state;
    }

    public AgentIntent getIntent() {
        return intent;
    }

    public int getProgressTicks() {
        return progressTicks;
    }

    public double getLastProgressValue() {
        return lastProgressValue;
    }

    public Map<String, Object> getContext() {
        return Collections.unmodifiableMap(context);
    }

    public Object getContextValue(String key) {
        return context.get(key);
    }

    @Override
    public String toString() {
        return "MotorStackFrame{"
                + "state=" + state
                + ", action=" + (intent != null ? intent.getActionCase() : "null")
                + ", progressTicks=" + progressTicks
                + ", lastProgressValue=" + lastProgressValue
                + '}';
    }
}
