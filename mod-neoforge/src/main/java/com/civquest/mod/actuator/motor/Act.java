package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.mojang.logging.LogUtils;

import java.util.function.Predicate;

/**
 * Instant (single-tick) intent motor: chat, trade, craft, book, wake-up and other primitives that
 * finish in the tick they start. {@code label} names the intent for failure receipts.
 */
public record Act(String label, Predicate<AgentEntity> action) implements Motor {
    @Override
    public Outcome onTick(Body body) {
        return run(this, label, action, body);
    }

    /** Shared instant execution: COMPLETED when the action returns true, FAILED (blank reason) otherwise. */
    static Outcome run(Motor self, String label, Predicate<AgentEntity> action, Body body) {
        AgentEntity agent = body.agent();
        if (agent == null) {
            return Outcome.done(self);
        }
        boolean ok;
        try {
            ok = action.test(agent);
        } catch (RuntimeException e) {
            LogUtils.getLogger().warn("Instant motor {} threw: {}", label, e.toString());
            ok = false;
        }
        return ok ? Outcome.done(self) : Outcome.failed(self, "", null);
    }
}
