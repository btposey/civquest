package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.Vec3;

/**
 * Flee reflex frame: keeps the agent in a retreat posture away from {@code from} for
 * {@code ticksRemaining} ticks (ticking any path already started), then pops.
 */
public record Flee(Vec3 from, int ticksRemaining) implements Reflex {
    public static final int DEFAULT_TICKS = 40;

    @Override
    public Outcome onTick(Body body) {
        body.tickNavigatorIfPathing();
        Flee next = new Flee(from, ticksRemaining - 1);
        return next.ticksRemaining <= 0 ? Outcome.done(next) : Outcome.stay(next);
    }
}
