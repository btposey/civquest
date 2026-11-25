package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.MotorFacts;

/**
 * Result of one {@link Motor#onTick}. {@code next} is the motor's new progress value (records are
 * immutable); {@code status == null} keeps the frame, otherwise it pops with that receipt status.
 */
public record Outcome(Motor next, ReceiptStatus status, String reason, MotorFacts facts) {
    public static Outcome stay(Motor next) {
        return new Outcome(next, null, "", null);
    }

    public static Outcome done(Motor next) {
        return new Outcome(next, ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null);
    }

    public static Outcome failed(Motor next, String reason, MotorFacts facts) {
        return new Outcome(next, ReceiptStatus.RECEIPT_STATUS_FAILED, reason, facts);
    }

    public boolean finished() {
        return status != null;
    }
}
