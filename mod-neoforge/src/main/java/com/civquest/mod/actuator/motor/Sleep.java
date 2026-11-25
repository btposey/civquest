package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import net.minecraft.core.BlockPos;

import java.util.function.Predicate;

/** Resting in a bed. {@code start} runs once on enter (null once started or when resumed). */
public record Sleep(BlockPos bed, int ticksAsleep, Predicate<AgentEntity> start) implements Motor {
    public Sleep(BlockPos bed, int ticksAsleep) {
        this(bed, ticksAsleep, null);
    }

    @Override
    public Outcome onEnter(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || start == null) {
            return Outcome.stay(this);
        }
        Sleep started = new Sleep(bed, ticksAsleep, null);
        if (!start.test(agent) && !agent.isSleeping()) {
            return Outcome.failed(started, "SLEEP_IN_BED failed (out of reach, occupied, or daytime)", null);
        }
        return Outcome.stay(started);
    }

    @Override
    public Outcome onTick(Body body) {
        AgentEntity agent = body.agent();
        Sleep next = new Sleep(bed, ticksAsleep + 1, null);
        return agent != null && !agent.isSleeping() ? Outcome.done(next) : Outcome.stay(next);
    }

    @Override
    public Motor onSuspend(Body body) {
        stopSleeping(body);
        return this;
    }

    @Override
    public void onExit(Body body) {
        stopSleeping(body);
    }

    private static void stopSleeping(Body body) {
        AgentEntity agent = body.agent();
        if (agent != null && agent.isSleeping()) {
            agent.stopSleeping();
        }
    }
}
