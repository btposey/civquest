package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.grpc.v1.Vec3;

/**
 * Navigation to a target. Stuck is reported as facts on a FAILED pop (ADR-037); the navigator is
 * parameterised per frame, stopped on suspend/exit and re-armed from this record on resume.
 */
public record Move(Vec3 target, float speed, boolean sprint, boolean allowBreak, boolean allowPlace,
                   int arrivalTolerance, boolean intoCell) implements Motor {
    public Move(Vec3 target, float speed, boolean sprint, boolean allowBreak, boolean allowPlace,
                int arrivalTolerance) {
        this(target, speed, sprint, allowBreak, allowPlace, arrivalTolerance, false);
    }

    @Override
    public Outcome onEnter(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null) {
            return Outcome.stay(this);
        }
        AgentNavigator nav = agent.getNavigator();
        if (nav == null || target == null) {
            return Outcome.failed(this, "Navigate rejected (invalid target coordinates)", null);
        }
        arm(nav);
        if (!nav.isPathing() && !nav.isArrived()) {
            return Outcome.failed(this, "Navigate rejected (invalid target coordinates)", null);
        }
        return nav.isArrived() ? Outcome.done(this) : Outcome.stay(this);
    }

    @Override
    public Outcome onTick(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null) {
            return Outcome.stay(this);
        }
        AgentNavigator nav = agent.getNavigator();
        if (nav == null) {
            return Outcome.done(this);
        }
        nav.tick();
        if (nav.isArrived()) {
            return Outcome.done(this);
        }
        if (nav.isStuck()) {
            return Outcome.failed(this, "Navigation stuck", nav.snapshotFacts());
        }
        if (nav.isIdle()) {
            return Outcome.failed(this, "Navigation stopped before reaching destination", null);
        }
        return Outcome.stay(this);
    }

    @Override
    public Motor onSuspend(Body body) {
        stop(body);
        return this;
    }

    @Override
    public Outcome onResume(Body body) {
        AgentEntity agent = body.agent();
        if (agent != null && agent.getNavigator() != null && target != null) {
            arm(agent.getNavigator());
        }
        return Outcome.stay(this);
    }

    @Override
    public void onExit(Body body) {
        stop(body);
    }

    private void arm(AgentNavigator nav) {
        nav.navigate(target.getX(), target.getY(), target.getZ(), speed, sprint, allowBreak, allowPlace, arrivalTolerance,
                intoCell);
    }

    private static void stop(Body body) {
        AgentEntity agent = body.agent();
        if (agent != null && agent.getNavigator() != null) {
            agent.getNavigator().stop();
        }
    }
}
