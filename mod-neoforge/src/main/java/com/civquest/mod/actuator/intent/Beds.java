package com.civquest.mod.actuator.intent;

import com.civquest.mod.AgentEntity;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BedPart;
import net.minecraft.world.phys.AABB;

/**
 * A bed is two blocks, foot and head = foot + facing. Setting a block state places only one of
 * them, and vanilla wakes a sleeper in a half bed on the next tick (cq-6l3.10).
 */
final class Beds {
    private Beds() {
    }

    /** Whether the other half of the bed block at {@code pos} is in place. */
    static boolean isWhole(Level level, BlockPos pos, BlockState state) {
        if (!state.hasProperty(BedBlock.PART) || !state.hasProperty(BedBlock.FACING)) {
            return true; // not a two-part bed
        }
        Direction facing = state.getValue(BedBlock.FACING);
        boolean foot = state.getValue(BedBlock.PART) == BedPart.FOOT;
        BlockState other = level.getBlockState(pos.relative(foot ? facing : facing.getOpposite()));
        return other.getBlock() == state.getBlock()
                && other.getValue(BedBlock.PART) != state.getValue(BedBlock.PART);
    }

    /** A cell a bed half can go into: replaceable and with nobody standing in it. */
    static boolean isFree(AgentEntity agent, Level level, BlockPos cell) {
        if (!level.hasChunkAt(cell) || !level.getBlockState(cell).canBeReplaced()) {
            return false;
        }
        AABB box = new AABB(cell);
        if (agent.getBoundingBox() != null && agent.getBoundingBox().intersects(box)) {
            return false;
        }
        return level.getEntities(agent, box, e -> e != null && (e instanceof Player || (e.isAlive() && !e.isSpectator())))
                .stream().map(Entity::getBoundingBox).noneMatch(b -> b != null && b.intersects(box));
    }

    /**
     * The direction of the head for a bed whose foot goes at {@code foot}: only {@code required}
     * when the intent named a facing, else the first free side starting with the way the agent
     * looks, preferring one with solid ground under it. Null when there is no room.
     */
    static Direction headDirection(AgentEntity agent, Level level, BlockPos foot, Direction required) {
        if (required != null) {
            return isFree(agent, level, foot.relative(required)) ? required : null;
        }
        Direction ahead = agent.getDirection();
        Direction[] order = {ahead, ahead.getClockWise(), ahead.getCounterClockWise(), ahead.getOpposite()};
        for (Direction d : order) {
            BlockPos head = foot.relative(d);
            if (isFree(agent, level, head) && level.getBlockState(head.below()).isFaceSturdy(level, head.below(), Direction.UP)) {
                return d;
            }
        }
        for (Direction d : order) {
            if (isFree(agent, level, foot.relative(d))) {
                return d;
            }
        }
        return null;
    }

    /** Places the foot at {@code foot} and the head beside it, as BedBlock.setPlacedBy does. */
    static boolean place(Level level, BlockPos foot, BlockState bed, Direction facing) {
        BlockState state = bed.setValue(BedBlock.FACING, facing).setValue(BedBlock.PART, BedPart.FOOT);
        return level.setBlock(foot, state, Block.UPDATE_ALL)
                && level.setBlock(foot.relative(facing), state.setValue(BedBlock.PART, BedPart.HEAD), Block.UPDATE_ALL);
    }

    /** Adds the missing head to a lone foot half. False when this is not one or there is no room. */
    static boolean complete(AgentEntity agent, Level level, BlockPos pos, BlockState state) {
        if (state.getValue(BedBlock.PART) != BedPart.FOOT) {
            return false;
        }
        BlockPos head = pos.relative(state.getValue(BedBlock.FACING));
        return isFree(agent, level, head)
                && level.setBlock(head, state.setValue(BedBlock.PART, BedPart.HEAD), Block.UPDATE_ALL);
    }
}
