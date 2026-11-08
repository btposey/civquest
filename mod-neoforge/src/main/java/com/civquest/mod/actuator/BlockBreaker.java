package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.tags.FluidTags;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;

/**
 * Tick-based block breaking for an {@link AgentEntity}: equips the best tool from the inventory,
 * accumulates vanilla destroy progress per tick, swings the arm while mining, shows crack stages,
 * and finally destroys the block. Refuses fluids, lava-adjacent blocks, bedrock and void edges.
 */
public class BlockBreaker {
    private static final double REACH_SQ = 20.25;

    private final AgentEntity agent;
    private BlockPos pos;
    private float progress;
    private int ticks;
    private int lastStage = -1;

    public BlockBreaker(AgentEntity agent) {
        this.agent = agent;
    }

    public boolean isBreaking() {
        return pos != null;
    }

    public BlockPos getPos() {
        return pos;
    }

    /** Whether it is safe and permitted to break {@code p}. */
    public static boolean isBreakable(Level level, BlockPos p) {
        if (level == null || p == null || !level.hasChunkAt(p) || p.getY() <= level.getMinBuildHeight() + 1) {
            return false;
        }
        BlockState state = level.getBlockState(p);
        if (state == null || state.isAir() || state.getDestroySpeed(level, p) < 0.0F
                || (state.getFluidState() != null && !state.getFluidState().isEmpty())) {
            return false;
        }
        for (Direction d : Direction.values()) {
            BlockState n = level.getBlockState(p.relative(d));
            if (n != null && n.getFluidState() != null && !n.getFluidState().isEmpty()) {
                return false;
            }
        }
        return true;
    }

    public float getProgress() {
        return progress;
    }

    /** Index of the inventory slot whose item mines {@code state} fastest, or -1 if nothing beats bare hands. */
    public static int bestToolSlot(AgentEntity agent, BlockState state) {
        int best = -1;
        float bestSpeed = 1.0F;
        var inv = agent.getInventory();
        if (inv == null || inv.items == null) {
            return -1;
        }
        for (int i = 0; i < inv.items.size(); i++) {
            ItemStack s = inv.items.get(i);
            if (s.isEmpty()) {
                continue;
            }
            float sp = s.getDestroySpeed(state);
            if (sp > bestSpeed) {
                bestSpeed = sp;
                best = i;
            }
        }
        return best;
    }

    /** Equips the best tool for {@code state} into the selected hotbar slot (swapping if needed). */
    public static void equipBestTool(AgentEntity agent, BlockState state) {
        int slot = bestToolSlot(agent, state);
        if (slot < 0) {
            return;
        }
        var inv = agent.getInventory();
        if (slot == inv.selected) {
            return;
        }
        if (slot < 9) {
            inv.selected = slot;
        } else {
            ItemStack tool = inv.items.get(slot);
            ItemStack cur = inv.items.get(inv.selected);
            inv.items.set(slot, cur);
            inv.items.set(inv.selected, tool);
        }
    }

    /**
     * Starts (or continues) breaking {@code target}. Instant-break blocks (hardness 0) are
     * destroyed immediately. Returns false when the block cannot be broken.
     */
    public boolean start(BlockPos target) {
        Level level = agent.level();
        if (!isBreakable(level, target)) {
            return false;
        }
        if (agent.distanceToSqr(target.getX() + 0.5, target.getY() + 0.5, target.getZ() + 0.5) > REACH_SQ) {
            return false;
        }
        if (target.equals(pos)) {
            return true;
        }
        cancel();
        BlockState state = level.getBlockState(target);
        equipBestTool(agent, state);
        this.pos = target.immutable();
        this.progress = 0.0F;
        this.ticks = 0;
        this.lastStage = -1;
        // Face the block
        double dx = target.getX() + 0.5 - agent.getX();
        double dy = target.getY() + 0.5 - agent.getEyeY();
        double dz = target.getZ() + 0.5 - agent.getZ();
        float yaw = (float) (Math.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
        float pitch = (float) -(Math.atan2(dy, Math.max(0.001, Math.sqrt(dx * dx + dz * dz))) * (180.0 / Math.PI));
        agent.setYRot(yaw);
        agent.setYHeadRot(yaw);
        agent.setXRot(pitch);
        tick(); // an instant-break block completes on the very first tick
        return true;
    }

    public void cancel() {
        if (pos != null) {
            try {
                agent.level().destroyBlockProgress(agent.getId(), pos, -1);
            } catch (Throwable ignored) {
            }
        }
        pos = null;
        progress = 0.0F;
        ticks = 0;
        lastStage = -1;
    }

    /** Advances the current break by one tick. Returns true when the block broke this tick. */
    public boolean tick() {
        if (pos == null) {
            return false;
        }
        Level level = agent.level();
        if (level == null || !isBreakable(level, pos)
                || agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > REACH_SQ) {
            cancel();
            return false;
        }
        BlockState state = level.getBlockState(pos);
        agent.equipBestToolFor(state);
        ticks++;
        progress += state.getDestroyProgress(agent, level, pos);
        if (ticks % 4 == 1) {
            agent.swing(InteractionHand.MAIN_HAND, true);
        }
        int stage = (int) (progress * 10.0F);
        if (stage != lastStage && progress < 1.0F) {
            level.destroyBlockProgress(agent.getId(), pos, stage);
            lastStage = stage;
        }
        if (progress >= 1.0F) {
            BlockPos done = pos;
            boolean broken = false;
            try {
                if (agent.gameMode != null) {
                    broken = agent.gameMode.destroyBlock(done);
                }
            } catch (Throwable ignored) {
            }
            if (!broken) {
                broken = level.destroyBlock(done, true, agent);
            }
            level.destroyBlockProgress(agent.getId(), done, -1);
            pos = null;
            progress = 0.0F;
            ticks = 0;
            lastStage = -1;
            if (broken) {
                agent.swing(InteractionHand.MAIN_HAND, true);
                agent.tryPickupNearbyItems();
            }
            return broken;
        }
        return false;
    }
}
