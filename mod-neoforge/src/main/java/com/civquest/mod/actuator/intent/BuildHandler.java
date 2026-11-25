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

    private static InteractBlockIntent asPlace(com.civquest.mod.grpc.v1.BuildBlockIntent intent) {
        return InteractBlockIntent.newBuilder()
                .setPos(intent.getTargetVoxel())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId(intent.getBlockStateString())
                .build();
    }

    /** Facing/attach is carried in block_state_string (e.g. "minecraft:ladder[facing=north]"). */
    public boolean actuateBuildBlock(AgentEntity agent, com.civquest.mod.grpc.v1.BuildBlockIntent intent) {
        if (agent == null || intent == null || !intent.hasTargetVoxel()) {
            return false;
        }
        return interact.actuateInteractBlock(agent, asPlace(intent));
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Place("BUILD_BLOCK", a -> actuateBuildBlock(a, intent.getBuildBlock()),
                a -> intent.getBuildBlock().hasTargetVoxel()
                        ? interact.placementFailureReason(a, asPlace(intent.getBuildBlock()))
                        : "build target voxel missing");
    }
}
