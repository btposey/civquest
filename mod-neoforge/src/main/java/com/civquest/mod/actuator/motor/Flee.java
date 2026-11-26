package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.Vec3;

/**
 * Flee reflex frame: runs away from {@code from} for {@code ticksRemaining} ticks, then pops.
 * The frame starts and stops its own navigation, so it works over any frame or an idle agent.
 */
public record Flee(Vec3 from, int ticksRemaining) implements Reflex {
    public static final int DEFAULT_TICKS = 40;
    public static final double DISTANCE = 12.0;
    private static final float SPEED = 1.25f;

    @Override
    public Outcome onEnter(Body body) {
        retreat(body);
        return Outcome.stay(this);
    }

    @Override
    public Outcome onTick(Body body) {
        body.tickNavigatorIfPathing();
        Flee next = new Flee(from, ticksRemaining - 1);
        return next.ticksRemaining <= 0 ? Outcome.done(next) : Outcome.stay(next);
    }

    @Override
    public Outcome onResume(Body body) {
        retreat(body);
        return Outcome.stay(this);
    }

    @Override
    public void onExit(Body body) {
        AgentEntity agent = body.agent();
        if (agent != null && agent.getNavigator() != null) {
            agent.getNavigator().stop();
        }
    }

    private void retreat(Body body) {
        AgentEntity agent = body.agent();
        if (agent == null || agent.getNavigator() == null || from == null) {
            return;
        }
        double dx = agent.getX() - from.getX();
        double dz = agent.getZ() - from.getZ();
        double length = Math.hypot(dx, dz);
        if (length < 0.001) {
            double yaw = Math.toRadians(agent.getYRot() + 180.0F);
            dx = -Math.sin(yaw);
            dz = Math.cos(yaw);
            length = 1.0;
        }
        agent.setShiftKeyDown(false);
        agent.getNavigator().navigate(agent.getX() + dx / length * DISTANCE, agent.getY(),
                agent.getZ() + dz / length * DISTANCE, SPEED, true, false, false, 2);
    }
}
