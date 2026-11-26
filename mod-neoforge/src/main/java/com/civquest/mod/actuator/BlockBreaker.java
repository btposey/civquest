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
    /** Vanilla survival block interaction range, measured from the eye to the nearest point of the block. */
    public static final double REACH = 4.5;

    /**
     * Vanilla reach predicate: squared distance from the eye to the nearest point of {@code p}'s unit AABB is
     * within {@link #REACH}. Mirrors Go {@code vertical.ReachableAABB}; both are pinned by the shared fixture
     * {@code orchestrator-go/test/integration/testdata/reach/block_reach.json} (cq-gpa.9).
     */
    public static boolean withinReach(double eyeX, double eyeY, double eyeZ, BlockPos p) {
        double dx = nearestAxis(eyeX, p.getX());
        double dy = nearestAxis(eyeY, p.getY());
        double dz = nearestAxis(eyeZ, p.getZ());
        return dx * dx + dy * dy + dz * dz <= REACH * REACH + 1e-9;
    }

    private static double nearestAxis(double v, double lo) {
        if (v < lo) {
            return lo - v;
        }
        if (v > lo + 1.0) {
            return v - lo - 1.0;
        }
        return 0.0;
    }

    private static boolean agentWithinReach(AgentEntity agent, BlockPos p) {
        return withinReach(agent.getX(), agent.getEyeY(), agent.getZ(), p);
    }

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

    public int getTicks() {
        return ticks;
    }

    public int getLastStage() {
        return lastStage;
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
        if (!agentWithinReach(agent, target)) {
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

    /**
     * Resumes breaking {@code target} from previously saved progress and tick count
     * (used by MotorPDA stack restoration after reflex preemption).
     */
    public boolean resume(BlockPos target, float savedProgress, int savedTicks) {
        Level level = agent.level();
        if (!isBreakable(level, target)) {
            return false;
        }
        if (!agentWithinReach(agent, target)) {
            return false;
        }
        cancel();
        BlockState state = level.getBlockState(target);
        equipBestTool(agent, state);
        this.pos = target.immutable();
        this.progress = Math.max(0.0F, savedProgress);
        this.ticks = Math.max(0, savedTicks);
        this.lastStage = (int) (this.progress * 10.0F);
        double dx = target.getX() + 0.5 - agent.getX();
        double dy = target.getY() + 0.5 - agent.getEyeY();
        double dz = target.getZ() + 0.5 - agent.getZ();
        float yaw = (float) (Math.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
        float pitch = (float) -(Math.atan2(dy, Math.max(0.001, Math.sqrt(dx * dx + dz * dz))) * (180.0 / Math.PI));
        agent.setYRot(yaw);
        agent.setYHeadRot(yaw);
        agent.setXRot(pitch);
        if (this.progress > 0.0F && this.progress < 1.0F) {
            level.destroyBlockProgress(agent.getId(), this.pos, this.lastStage);
        }
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
                || !agentWithinReach(agent, pos)) {
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
                if (broken) {
                    ItemStack tool = agent.getMainHandItem();
                    if (tool != null && !tool.isEmpty()) {
                        tool.mineBlock(level, state, done, agent);
                    }
                }
            }
            level.destroyBlockProgress(agent.getId(), done, -1);
            pos = null;
            progress = 0.0F;
            ticks = 0;
            lastStage = -1;
            if (broken) {
                agent.swing(InteractionHand.MAIN_HAND, true);
                agent.tryPickupNearbyItems();
                agent.causeFoodExhaustion(0.025F);
            }
            return broken;
        }
        return false;
    }
}
