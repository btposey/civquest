package com.civquest.mod.actuator.intent;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.motor.Motor;
import com.civquest.mod.actuator.motor.Sweep;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.SurveyHorizonIntent;
import com.civquest.mod.grpc.v1.Vec3;
import com.civquest.mod.sensory.HorizonSurveyScanner;

import java.util.List;

/** SurveyHorizonIntent (ADR-032) to a {@link Sweep} frame; the navigator is stopped while sweeping. */
public class SurveyHandler implements IntentHandler {
    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        SurveyHorizonIntent survey = intent.getSurveyHorizon();
        if (agent != null && agent.getNavigator() != null) {
            agent.getNavigator().stop();
        }
        int rawSectors = survey.getNumSectors();
        int numSectors = rawSectors <= 0 ? 36 : Math.max(1, Math.min(360, rawSectors));
        float maxRange = HorizonSurveyScanner.clampMaxRange(survey.getMaxRange());
        int rawTicks = survey.getTicksPerSector();
        int ticksPerSector = rawTicks <= 0 ? 2 : Math.max(1, Math.min(10, rawTicks));
        double ox = 0.0;
        double oy = 0.0;
        double oz = 0.0;
        if (agent != null) {
            net.minecraft.world.phys.Vec3 eye = agent.getEyePosition();
            if (eye != null) {
                ox = eye.x;
                oy = eye.y;
                oz = eye.z;
            } else {
                ox = agent.getX();
                oy = agent.getY() + 1.62;
                oz = agent.getZ();
            }
        }
        return new Sweep(Vec3.newBuilder().setX(ox).setY(oy).setZ(oz).build(),
                numSectors, maxRange, ticksPerSector, 0, 0, List.of());
    }
}
