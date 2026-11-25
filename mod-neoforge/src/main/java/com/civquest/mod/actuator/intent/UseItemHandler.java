package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.component.DataComponents;
import net.minecraft.world.entity.EquipmentSlot;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

/** Eat/drink/equip primitive (UseItemIntent). */
public class UseItemHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();

    public boolean actuateUseItem(AgentEntity agent, UseItemIntent intent) {
        if (agent == null || intent == null) {
            return false;
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

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new UseItem("slot:" + intent.getUseItem().getInventorySlot(), 0, false,
                a -> actuateUseItem(a, intent.getUseItem()));
    }
}
