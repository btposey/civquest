package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;

import java.util.function.Predicate;

/**
 * Eat/drink/equip. {@code emergency} frames are pushed over the top frame as a reflex and pop when
 * the agent stops using the item; routine frames belong to a batch. {@code start} runs once on enter.
 */
public record UseItem(String itemId, int useTicks, boolean emergency, Predicate<AgentEntity> start) implements Motor {
    /** Emergency (reflex) use-item with no start action. */
    public UseItem(String itemId, int useTicks) {
        this(itemId, useTicks, true, null);
    }

    @Override
    public Outcome onEnter(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || start == null) {
            return Outcome.stay(this);
        }
        UseItem started = new UseItem(itemId, useTicks, emergency, null);
        boolean ok = start.test(agent);
        if (!ok) {
            return Outcome.failed(started, "", null);
        }
        return agent.isUsingItem() ? Outcome.stay(started) : Outcome.done(started);
    }

    @Override
    public Outcome onTick(Body body) {
        UseItem next = new UseItem(itemId, useTicks + 1, emergency, null);
        return body.isUsingItem() ? Outcome.stay(next) : Outcome.done(next);
    }

    @Override
    public Motor onSuspend(Body body) {
        stopRoutine(body);
        return this;
    }

    @Override
    public void onExit(Body body) {
        stopRoutine(body);
    }

    private void stopRoutine(Body body) {
        AgentEntity agent = body.agent();
        if (!emergency && agent != null && agent.isUsingItem()) {
            agent.stopUsingItem();
        }
    }
}
