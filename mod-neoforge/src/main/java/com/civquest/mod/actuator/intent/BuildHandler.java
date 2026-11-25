package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.mojang.logging.LogUtils;
import org.slf4j.Logger;

/** Single-voxel build (BuildBlockIntent), delegated to block placement. */
public class BuildHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private final InteractHandler interact;

    public BuildHandler(InteractHandler interact) {
        this.interact = interact;
    }

    public boolean actuateBuildBlock(AgentEntity agent, com.civquest.mod.grpc.v1.BuildBlockIntent intent) {
        if (agent == null || intent == null || !intent.hasTargetVoxel()) {
            return false;
        }
        return interact.actuateInteractBlock(agent, InteractBlockIntent.newBuilder()
                .setPos(intent.getTargetVoxel())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId(intent.getBlockStateString())
                .build());
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Place("BUILD_BLOCK", a -> actuateBuildBlock(a, intent.getBuildBlock()));
    }
}
