package com.civquest.mod.actuator.motor;

/**
 * Combat reflex frame (ADR-036 section 4, cq-2sr.15). Pushed over the top frame; its own tick
 * counter replaces the legacy FSM-level combat counter and it pops COMPLETED once
 * {@link #TIMEOUT_TICKS} have elapsed without being refreshed.
 */
public record Combat(int ticks, int targetEntityId) implements Reflex {
    /** 3 seconds of combat disengagement. */
    public static final int TIMEOUT_TICKS = 60;

    @Override
    public Outcome onTick(Body body) {
        body.tickNavigatorIfPathing();
        Combat next = new Combat(ticks + 1, targetEntityId);
        return ticks + 1 > TIMEOUT_TICKS ? Outcome.done(next) : Outcome.stay(next);
    }

    @Override
    public void onExit(Body body) {
        body.stopShieldUse();
    }
}
