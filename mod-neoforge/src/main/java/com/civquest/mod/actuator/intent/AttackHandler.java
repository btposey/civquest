package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.mojang.logging.LogUtils;
import net.minecraft.server.MinecraftServer;
import net.minecraft.util.Mth;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

/** Attack/shield/kite/flee primitive (AttackIntent). */
public class AttackHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double MELEE_REACH = 4.5;

    public boolean actuateAttack(AgentEntity agent, AttackIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        // 1. Shield raising / lowering reflex
        if (intent.getRaiseShield()) {
            ItemStack offhand = agent.getItemInHand(InteractionHand.OFF_HAND);
            if (offhand.is(Items.SHIELD)) {
                agent.startUsingItem(InteractionHand.OFF_HAND);
            } else {
                int shieldSlot = -1;
                for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                    ItemStack item = agent.getInventory().getItem(i);
                    if (item.is(Items.SHIELD)) {
                        shieldSlot = i;
                        break;
                    }
                }
                if (shieldSlot >= 0) {
                    ItemStack foundShield = agent.getInventory().getItem(shieldSlot);
                    agent.setItemInHand(InteractionHand.OFF_HAND, foundShield);
                    agent.getInventory().setItem(shieldSlot, offhand);
                    agent.startUsingItem(InteractionHand.OFF_HAND);
                }
                // If no shield exists in inventory or offhand, do NOT create one
            }
        } else {
            if (agent.isUsingItem() && agent.getUsedItemHand() == InteractionHand.OFF_HAND) {
                agent.stopUsingItem();
            }
        }

        // 2. Attack target entity within melee reach
        if (intent.getTargetEntityId() > 0 && agent.level() != null) {
            FishingHandler.abandon(agent);
            Entity target = agent.level().getEntity(intent.getTargetEntityId());
            if (target != null && target.isAlive() && target != agent) {
                boolean targetAllowed = true;
                if (target instanceof AgentEntity targetAgent) {
                    if (agent.getClanId() != null && !agent.getClanId().isBlank()
                            && agent.getClanId().equals(targetAgent.getClanId())) {
                        LOGGER.debug("Rejecting attack: target agent '{}' is in same clan '{}'",
                                targetAgent.getAgentId(), agent.getClanId());
                        targetAllowed = false;
                    }
                }
                if (targetAllowed && target instanceof Player) {
                    MinecraftServer server = agent.getServer();
                    if (server != null && !server.isPvpAllowed()) {
                        LOGGER.debug("Rejecting attack: PvP is disabled on the server");
                        targetAllowed = false;
                    }
                }

                if (targetAllowed) {
                    double distance = agent.distanceTo(target);
                    if (distance <= MELEE_REACH) {
                        if (agent.getAttackStrengthScale(0.5f) >= 0.9f) {
                            double dx = target.getX() - agent.getX();
                            double dy = target.getEyeY() - agent.getEyeY();
                            double dz = target.getZ() - agent.getZ();
                            double horizontalDist = Math.sqrt(dx * dx + dz * dz);

                            float yaw = (float) (Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
                            float pitch = (float) (-(Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
                            agent.setYRot(yaw);
                            agent.setYHeadRot(yaw);
                            agent.setXRot(pitch);

                            agent.attack(target);
                            agent.swing(InteractionHand.MAIN_HAND, true);
                            agent.resetAttackStrengthTicker();
                        }
                    }
                }
            }
        }

        // 3. Flee / Kite reflex movement. A flight already under way keeps its destination: steering it
        // away again every few ticks left the agent replanning and barely moving.
        boolean walking = agent.getNavigator() != null
                && agent.getNavigator().getNavigationState() == NavigationState.NAVIGATION_STATE_PATHING;
        boolean steer = agent.getNavigator() != null
                && (intent.getKeepDistanceKite() || intent.getFleeImmediately() && !walking);
        if (steer && intent.getTargetEntityId() > 0 && agent.level() != null) {
            Entity target = agent.level().getEntity(intent.getTargetEntityId());
            if (target != null) {
                double dx = agent.getX() - target.getX();
                double dz = agent.getZ() - target.getZ();
                double dist = Math.sqrt(dx * dx + dz * dz);
                double fleeDirX;
                double fleeDirZ;
                if (dist < 0.001) {
                    float fleeYaw = agent.getYRot() + 180.0F;
                    double rad = Math.toRadians(fleeYaw);
                    fleeDirX = -Math.sin(rad);
                    fleeDirZ = Math.cos(rad);
                } else {
                    fleeDirX = dx / dist;
                    fleeDirZ = dz / dist;
                }
                double fleeX = agent.getX() + fleeDirX * 10.0;
                double fleeZ = agent.getZ() + fleeDirZ * 10.0;
                agent.getNavigator().navigate(fleeX, agent.getY(), fleeZ, 1.25f, true, false, false, 1);
            }
        }

        return true;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Strike(a -> actuateAttack(a, intent.getAttack()), 0);
    }
}
