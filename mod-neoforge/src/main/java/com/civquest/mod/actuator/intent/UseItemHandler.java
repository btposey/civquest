package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.component.DataComponents;
import net.minecraft.sounds.SoundSource;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EquipmentSlot;
import net.minecraft.world.entity.Shearable;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

/** Eat/drink/equip primitive (UseItemIntent). */
public class UseItemHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double SHEAR_REACH = 4.5;

    public boolean actuateUseItem(AgentEntity agent, UseItemIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        switch (intent.getMode()) {
            case SHEAR_ENTITY -> {
                return shear(agent, intent.getTargetEntityId());
            }
            case CAST_FISHING_ROD -> {
                return FishingHandler.cast(agent, intent.hasAimAt()
                        ? new double[] {intent.getAimAt().getX(), intent.getAimAt().getY(), intent.getAimAt().getZ()} : null);
            }
            case REEL_FISHING_ROD -> {
                return FishingHandler.reel(agent);
            }
            default -> { }
        }

        if (intent.getIsEatingOrDrinking()) {
            int slot = intent.getInventorySlot();
            ItemStack stack = (slot >= 0 && slot < agent.getInventory().getContainerSize())
                    ? agent.getInventory().getItem(slot)
                    : agent.getMainHandItem();

            if (stack != null && !stack.isEmpty()) {
                FoodProperties food = stack.get(DataComponents.FOOD);
                if (food == null) {
                    return false;
                }
                if (!agent.getFoodData().needsFood() && !food.canAlwaysEat()) {
                    return false;
                }
                agent.getFoodData().eat(food);
                stack.shrink(1);
                return true;
            }
            return false;
        }

        if (intent.getEquipArmorOrOffhand()) {
            int slot = intent.getInventorySlot();
            if (slot >= 0 && slot < agent.getInventory().getContainerSize()) {
                ItemStack stack = agent.getInventory().getItem(slot);
                if (stack != null && !stack.isEmpty()) {
                    EquipmentSlot eqSlot = agent.getEquipmentSlotForItem(stack);
                    if (eqSlot == EquipmentSlot.MAINHAND && stack.is(Items.SHIELD)) {
                        eqSlot = EquipmentSlot.OFFHAND;
                    }
                    ItemStack currentEquipped = agent.getItemBySlot(eqSlot);
                    agent.setItemSlot(eqSlot, stack.copy());
                    agent.getInventory().setItem(slot, currentEquipped);
                    return true;
                }
            }
        }

        return false;
    }

    /** Sheep melee-free wool harvest (cq-390.3): shears from the inventory on a ready, adult sheep in reach. */
    public boolean shear(AgentEntity agent, int targetEntityId) {
        String why = shearFailureReason(agent, targetEntityId);
        if (!why.isEmpty()) {
            return false;
        }
        Entity target = agent.level().getEntity(targetEntityId);
        FishingHandler.equipMainHand(agent, s -> s.is(Items.SHEARS));
        ItemStack shears = agent.getMainHandItem();
        agent.swing(InteractionHand.MAIN_HAND, true);
        ((Shearable) target).shear(SoundSource.PLAYERS);
        shears.hurtAndBreak(1, agent, EquipmentSlot.MAINHAND);
        return true;
    }

    /** Why a shear attempt would fail now (blank when it can proceed). */
    public String shearFailureReason(AgentEntity agent, int targetEntityId) {
        if (agent == null || agent.level() == null) {
            return "no level";
        }
        Entity target = targetEntityId > 0 ? agent.level().getEntity(targetEntityId) : null;
        if (target == null || !target.isAlive()) {
            return "shear target not found";
        }
        if (!(target instanceof Shearable shearable) || !shearable.readyForShearing()) {
            return "target is not shearable (baby, already sheared or not a sheep)";
        }
        if (agent.distanceTo(target) > SHEAR_REACH) {
            return "shear target out of reach";
        }
        if (!hasShears(agent)) {
            return "no shears in inventory";
        }
        return "";
    }

    private static boolean hasShears(AgentEntity agent) {
        var inv = agent.getInventory();
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack s = inv.getItem(i);
            if (!s.isEmpty() && s.is(Items.SHEARS)) {
                return true;
            }
        }
        return false;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        UseItemIntent use = intent.getUseItem();
        return switch (use.getMode()) {
            case SHEAR_ENTITY -> new Place("USE_ITEM_SHEAR", a -> actuateUseItem(a, use),
                    a -> shearFailureReason(a, use.getTargetEntityId()));
            case CAST_FISHING_ROD -> new Place("USE_ITEM_CAST", a -> actuateUseItem(a, use),
                    a -> FishingHandler.isCast(a) ? "fishing line already cast" : "no fishing rod in inventory");
            case REEL_FISHING_ROD -> new Place("USE_ITEM_REEL", a -> actuateUseItem(a, use),
                    a -> "no fishing line cast");
            default -> new UseItem("slot:" + use.getInventorySlot(), 0, false, a -> actuateUseItem(a, use));
        };
    }
}
