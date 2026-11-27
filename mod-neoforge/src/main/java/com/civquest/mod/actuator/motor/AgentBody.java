package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import net.minecraft.core.BlockPos;
import com.civquest.mod.grpc.v1.HorizonSector;
import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.Vec3;
import com.civquest.mod.sensory.HorizonSurveyScanner;
import com.mojang.logging.LogUtils;
import org.slf4j.Logger;

/** {@link Body} backed by an {@link AgentEntity}. Holds no reference to any FSM. */
public final class AgentBody implements Body {
    private static final Logger LOGGER = LogUtils.getLogger();
    private final AgentEntity agent;
    private final java.util.function.LongSupplier clock;

    public AgentBody(AgentEntity agent) {
        this(agent, null);
    }

    /** {@code clock} overrides the game-time source (the FSM passes its own 20 Hz counter). */
    public AgentBody(AgentEntity agent, java.util.function.LongSupplier clock) {
        this.agent = agent;
        this.clock = clock;
    }

    @Override
    public AgentEntity agent() {
        return agent;
    }

    @Override
    public long tick() {
        if (clock != null) {
            return clock.getAsLong();
        }
        return agent.level() != null ? agent.level().getGameTime() : agent.tickCount;
    }

    @Override
    public double x() {
        return agent.getX();
    }

    @Override
    public double y() {
        return agent.getY();
    }

    @Override
    public double z() {
        return agent.getZ();
    }

    @Override
    public void faceBearing(float yawDeg) {
        agent.faceBearingAndSync(yawDeg);
    }

    @Override
    public HorizonSector scanHorizonSector(Vec3 origin, int sectorIndex, float bearingDeg, float maxRange) {
        net.minecraft.world.phys.Vec3 eye = agent.getEyePosition();
        if (eye == null) {
            eye = new net.minecraft.world.phys.Vec3(origin.getX(), origin.getY(), origin.getZ());
        }
        return HorizonSurveyScanner.scanSector(agent.level(), agent, eye, sectorIndex, bearingDeg, maxRange);
    }

    @Override
    public void publishHorizonSurvey(HorizonSurveyReport report) {
        agent.queueHorizonSurveyReport(report);
    }

    @Override
    public boolean frozenAt(BlockPos pos) {
        if (agent.level() == null) {
            return false;
        }
        var biome = agent.level().getBiome(pos);
        return biome != null && biome.value() != null && biome.value().coldEnoughToSnow(pos);
    }

    @Override
    public boolean isUsingItem() {
        return agent.isUsingItem();
    }

    @Override
    public void stopShieldUse() {
        if (agent.isUsingItem() && agent.getUseItem() != null && agent.getUseItem().is(net.minecraft.world.item.Items.SHIELD)) {
            agent.stopUsingItem();
        }
    }

    @Override
    public void tickNavigatorIfPathing() {
        if (agent.getNavigator() != null && agent.getNavigator().isPathing()) {
            agent.getNavigator().tick();
        }
    }

    @Override
    public void log(String message) {
        LOGGER.debug("[motor-stack] {}", message);
    }
}
