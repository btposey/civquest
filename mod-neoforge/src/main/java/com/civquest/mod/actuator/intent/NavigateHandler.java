package com.civquest.mod.actuator.intent;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.motor.Motor;
import com.civquest.mod.actuator.motor.Move;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;

/** NavigateIntent to a {@link Move} frame. */
public class NavigateHandler implements IntentHandler {
    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        NavigateIntent nav = intent.getNavigate();
        return new Move(nav.hasTarget() ? nav.getTarget() : null, nav.getSpeedMultiplier(), nav.getSprint(),
                nav.getAllowBreakBlocks(), nav.getAllowPlaceScaffolding(), nav.getArrivalTolerance(),
                nav.getIntoCell());
    }
}
