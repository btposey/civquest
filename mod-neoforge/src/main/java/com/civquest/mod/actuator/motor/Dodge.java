package com.civquest.mod.actuator.motor;

/** Projectile-dodge reflex frame: holds the shield up for {@code ticksRemaining} ticks, then pops. */
public record Dodge(int ticksRemaining) implements Reflex {
    public static final int DEFAULT_TICKS = 20;

    @Override
    public Outcome onTick(Body body) {
        Dodge next = new Dodge(ticksRemaining - 1);
        return next.ticksRemaining <= 0 ? Outcome.done(next) : Outcome.stay(next);
    }

    @Override
    public void onExit(Body body) {
        body.stopShieldUse();
    }
}
