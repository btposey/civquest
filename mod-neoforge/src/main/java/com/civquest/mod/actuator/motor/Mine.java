package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.BlockBreaker;
import net.minecraft.core.BlockPos;

import java.util.function.Predicate;

/**
 * Timed block breaking. Progress ({@code progressTicks}, {@code progress}) lives in the record, so a
 * reflex that suspends the frame resumes it exactly where it stopped. {@code start} begins the break
 * on enter (null once started or when restored).
 */
public record Mine(BlockPos pos, int progressTicks, double progress, Predicate<AgentEntity> start) implements Motor {
    public Mine(BlockPos pos, int progressTicks, double progress) {
        this(pos, progressTicks, progress, null);
    }

    public Mine advance(double newProgress) {
        return new Mine(pos, progressTicks + 1, newProgress, null);
    }

    @Override
    public Outcome onEnter(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || start == null) {
            return Outcome.stay(this);
        }
        Mine started = new Mine(pos, progressTicks, progress, null);
        boolean ok = start.test(agent);
        BlockBreaker breaker = agent.getBreaker();
        boolean breaking = breaker != null && breaker.isBreaking();
        if (!ok && !breaking) {
            return Outcome.failed(started, "HARVEST_BREAK failed (unbreakable, fluid, or out of reach)", null);
        }
        if (ok && !breaking) {
            return Outcome.done(started); // instant break
        }
        return Outcome.stay(started);
    }

    @Override
    public Outcome onTick(Body body) {
        AgentEntity agent = body.agent();
        BlockBreaker breaker = agent != null ? agent.getBreaker() : null;
        if (agent == null) {
            return Outcome.stay(this);
        }
        if (breaker == null || !breaker.isBreaking()) {
            return Outcome.done(this);
        }
        boolean broken = breaker.tick();
        if (broken) {
            return Outcome.done(this);
        }
        if (!breaker.isBreaking()) {
            return Outcome.failed(this, "Block breaking cancelled or target unreachable", null);
        }
        return Outcome.stay(new Mine(pos, breaker.getTicks(), breaker.getProgress(), null));
    }

    @Override
    public Motor onSuspend(Body body) {
        AgentEntity agent = body.agent();
        BlockBreaker breaker = agent != null ? agent.getBreaker() : null;
        if (breaker == null) {
            return this;
        }
        BlockPos at = pos;
        int ticks = progressTicks;
        double prog = progress;
        if (breaker.isBreaking()) {
            ticks = breaker.getTicks();
            prog = breaker.getProgress();
            if (breaker.getPos() != null) {
                at = breaker.getPos().immutable();
            }
        }
        breaker.cancel();
        return new Mine(at, ticks, prog, null);
    }

    @Override
    public Outcome onResume(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || agent.getBreaker() == null || pos == null) {
            return Outcome.stay(this);
        }
        boolean resumed = agent.getBreaker().resume(pos, (float) progress, progressTicks);
        if (!resumed && agent.level() != null) {
            return Outcome.failed(this, "Failed to resume block breaking at " + pos, null);
        }
        return Outcome.stay(this);
    }

    @Override
    public void onExit(Body body) {
        AgentEntity agent = body.agent();
        if (agent != null && agent.getBreaker() != null) {
            agent.getBreaker().cancel();
        }
    }
}
