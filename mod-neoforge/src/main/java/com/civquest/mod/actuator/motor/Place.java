package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;

import java.util.function.Function;
import java.util.function.Predicate;

/**
 * Instant block placement (place, build, paving road, signpost). Finishes in the tick it starts.
 * {@code why} (optional) explains a failed placement so receipts carry a concrete reason.
 */
public record Place(String label, Predicate<AgentEntity> action, Function<AgentEntity, String> why) implements Motor {
    public Place(String label, Predicate<AgentEntity> action) {
        this(label, action, null);
    }

    @Override
    public Outcome onTick(Body body) {
        Outcome out = Act.run(this, label, action, body);
        if (why != null && out.status() == com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                && body.agent() != null) {
            String reason;
            try {
                reason = why.apply(body.agent());
            } catch (RuntimeException e) {
                reason = "";
            }
            if (reason != null && !reason.isBlank()) {
                return Outcome.failed(this, reason, null);
            }
        }
        return out;
    }
}
