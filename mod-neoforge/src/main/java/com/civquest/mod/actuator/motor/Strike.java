package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;

import java.util.function.Predicate;

/**
 * Attack intent: swings/raises the shield/kites once on enter, then stays engaged for
 * {@link Combat#TIMEOUT_TICKS} (ticking any kite/flee path) before completing, mirroring the
 * combat disengagement window. A fresh attack on top restarts the window via the FSM.
 */
public record Strike(Predicate<AgentEntity> action, int ticks) implements Motor {
    @Override
    public Outcome onEnter(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || action == null) {
            return Outcome.stay(this);
        }
        boolean ok = action.test(agent);
        return ok ? Outcome.stay(this) : Outcome.failed(this, "", null);
    }

    @Override
    public Outcome onTick(Body body) {
        body.tickNavigatorIfPathing();
        Strike next = new Strike(action, ticks + 1);
        return ticks + 1 > Combat.TIMEOUT_TICKS ? Outcome.done(next) : Outcome.stay(next);
    }

    @Override
    public void onExit(Body body) {
        body.stopShieldUse();
    }
}
