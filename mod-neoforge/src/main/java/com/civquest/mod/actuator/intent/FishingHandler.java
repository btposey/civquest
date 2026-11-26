package com.civquest.mod.actuator.intent;

import com.civquest.mod.AgentEntity;
import com.mojang.logging.LogUtils;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.projectile.FishingHook;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

import java.lang.reflect.Field;
import java.util.function.Predicate;

/**
 * Fishing-rod primitives for UseItemIntent (cq-390.4): cast, reel, and bobber/bite state.
 * The vanilla rod item toggles on use: no hook out casts, a hook out retrieves (and catches on a bite),
 * so cast and reel are guarded here so a stray CAST never reels a half-waited line (and vice versa).
 */
public final class FishingHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final Field BITING = resolveBiting();

    private FishingHandler() {}

    private static Field resolveBiting() {
        try {
            Field f = FishingHook.class.getDeclaredField("biting");
            f.setAccessible(true);
            return f;
        } catch (Throwable t) {
            LOGGER.warn("Could not resolve FishingHook.biting: {}", t.getMessage());
            return null;
        }
    }

    /** True when the agent has a live hook entity out. */
    public static boolean isCast(AgentEntity agent) {
        FishingHook hook = agent == null ? null : agent.fishing;
        return hook != null && !hook.isRemoved();
    }

    /** True when a fish is on the line (vanilla: the bobber dips; reel now). */
    public static boolean isBiting(AgentEntity agent) {
        if (!isCast(agent) || BITING == null) {
            return false;
        }
        try {
            return BITING.getBoolean(agent.fishing);
        } catch (Throwable t) {
            return false;
        }
    }

    /** Moves the first stack matching pred into the selected hotbar slot (swap); false when none is carried. */
    public static boolean equipMainHand(AgentEntity agent, Predicate<ItemStack> pred) {
        if (agent == null) {
            return false;
        }
        ItemStack held = agent.getMainHandItem();
        if (!held.isEmpty() && pred.test(held)) {
            return true;
        }
        Inventory inv = agent.getInventory();
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack s = inv.getItem(i);
            if (!s.isEmpty() && pred.test(s) && i != inv.selected) {
                ItemStack prev = inv.getItem(inv.selected);
                inv.setItem(inv.selected, s);
                inv.setItem(i, prev);
                return true;
            }
        }
        return false;
    }

    /** Turns the agent's head toward a world point (the cast direction is the look direction). */
    public static void faceTowards(AgentEntity agent, double x, double y, double z) {
        double dx = x - agent.getX();
        double dy = y - agent.getEyeY();
        double dz = z - agent.getZ();
        double horizontal = Math.sqrt(dx * dx + dz * dz);
        float yaw = (float) (Math.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
        float pitch = (float) (-(Math.atan2(dy, Math.max(0.001, horizontal)) * (180.0 / Math.PI)));
        agent.setYRot(yaw);
        agent.setYHeadRot(yaw);
        agent.setXRot(pitch);
    }

    /** Equips a rod and casts it. False when no rod is carried or a line is already out. */
    public static boolean cast(AgentEntity agent) {
        return cast(agent, null);
    }

    /** As {@link #cast(AgentEntity)}, facing {@code aim} (x, y, z) first when given. */
    public static boolean cast(AgentEntity agent, double[] aim) {
        if (agent == null || agent.level() == null || isCast(agent)) {
            return false;
        }
        if (!equipMainHand(agent, s -> s.is(Items.FISHING_ROD))) {
            return false;
        }
        if (aim != null) {
            faceTowards(agent, aim[0], aim[1], aim[2]);
        }
        agent.swing(InteractionHand.MAIN_HAND, true);
        agent.getMainHandItem().use(agent.level(), agent, InteractionHand.MAIN_HAND);
        return isCast(agent);
    }

    /** Retrieves the line (catches on a bite). False when no line is out. */
    public static boolean reel(AgentEntity agent) {
        if (!isCast(agent)) {
            return false;
        }
        if (!agent.getMainHandItem().is(Items.FISHING_ROD)) {
            // The rod left the main hand: vanilla would have discarded the hook already.
            agent.fishing.discard();
            return false;
        }
        agent.swing(InteractionHand.MAIN_HAND, true);
        agent.getMainHandItem().use(agent.level(), agent, InteractionHand.MAIN_HAND);
        return !isCast(agent);
    }
}
