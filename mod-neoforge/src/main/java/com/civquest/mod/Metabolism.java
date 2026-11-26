package com.civquest.mod;

import net.minecraft.world.entity.player.Player;

/**
 * How fast an agent's food bar empties (cq-6l3.8): three in-game days at normal exertion, one
 * day when sprinting, fast swimming or fighting. Saturation from spawning and eating comes on
 * top, and healing still costs food as in vanilla.
 */
public final class Metabolism {
    /** Exhaustion that empties a full food bar: 20 food points at 4 each. */
    static final float FULL_BAR_EXHAUSTION = 80.0F;
    static final int TICKS_PER_DAY = 24000;
    public static final float NORMAL_PER_TICK = FULL_BAR_EXHAUSTION / (3 * TICKS_PER_DAY);
    public static final float EXERTION_FACTOR = 3.0F;
    /** A fight lasts this long past the last blow given or taken. */
    static final int COMBAT_TICKS = 100;

    private Metabolism() {
    }

    static float perTick(boolean exerting) {
        return exerting ? NORMAL_PER_TICK * EXERTION_FACTOR : NORMAL_PER_TICK;
    }

    static boolean isExerting(Player agent) {
        boolean moving = agent.getDeltaMovement().horizontalDistanceSqr() > 0.001;
        if (agent.isSwimming() || (agent.isSprinting() && moving)) {
            return true;
        }
        return (agent.getLastHurtMob() != null && agent.tickCount - agent.getLastHurtMobTimestamp() < COMBAT_TICKS)
                || (agent.getLastHurtByMob() != null && agent.tickCount - agent.getLastHurtByMobTimestamp() < COMBAT_TICKS);
    }
}
