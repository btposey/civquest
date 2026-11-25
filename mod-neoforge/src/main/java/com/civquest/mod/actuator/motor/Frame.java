package com.civquest.mod.actuator.motor;

import java.util.Objects;

/**
 * One motor activation. {@code frameId} is the Go Frame.ID echoed from IntentBatch.frame_id
 * (locally generated frames such as reflexes carry ids from the FSM's local range; {@code index < 0} marks a frame outside any batch); {@code batchId}/{@code index}
 * locate the intent inside its batch.
 */
public record Frame(long batchId, long frameId, int index, Motor motor, Lease lease) {
    public Frame {
        Objects.requireNonNull(motor, "motor");
        Objects.requireNonNull(lease, "lease");
    }

    public Frame withMotor(Motor next) {
        return new Frame(batchId, frameId, index, next, lease);
    }

    public Frame withLease(Lease next) {
        return new Frame(batchId, frameId, index, motor, next);
    }
}
