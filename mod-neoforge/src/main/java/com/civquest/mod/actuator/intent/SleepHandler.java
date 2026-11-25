package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.tags.BlockTags;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.item.BowItem;
import net.minecraft.world.item.CrossbowItem;
import net.minecraft.world.item.DiggerItem;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.SwordItem;
import net.minecraft.world.item.TieredItem;
import net.minecraft.world.item.TridentItem;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BedPart;
import org.slf4j.Logger;

/** Bed sleeping and weapon sheathing. */
public class SleepHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double BED_REACH_SQ = 20.25;     // 4.5 blocks squared

    public boolean actuateSleepInBed(AgentEntity agent, BlockPos pos) {
        if (agent == null || pos == null) {
            return false;
        }
        Level level = agent.level();
        if (level == null || !level.hasChunkAt(pos)) {
            return false;
        }
        if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > BED_REACH_SQ) {
            return false;
        }
        BlockState state = level.getBlockState(pos);
        if (!state.is(BlockTags.BEDS)) {
            return false;
        }

        BlockPos targetPos = pos;
        if (state.getBlock() instanceof BedBlock) {
            if (state.hasProperty(BedBlock.PART) && state.getValue(BedBlock.PART) == BedPart.FOOT) {
                if (state.hasProperty(BedBlock.FACING)) {
                    targetPos = pos.relative(state.getValue(BedBlock.FACING));
                }
            }
        }

        BlockState targetState = level.getBlockState(targetPos);
        if (state.hasProperty(BedBlock.OCCUPIED) && state.getValue(BedBlock.OCCUPIED)) {
            return false;
        }
        if (targetState.hasProperty(BedBlock.OCCUPIED) && targetState.getValue(BedBlock.OCCUPIED)) {
            return false;
        }
        if (!level.isNight() && !level.isThundering()) {
            return false;
        }

        sheatheHeldWeaponOrTool(agent);
        agent.startSleeping(targetPos);
        return agent.isSleeping();
    }

    public void sheatheHeldWeaponOrTool(AgentEntity agent) {
        if (agent == null) {
            return;
        }
        var inv = agent.getInventory();
        if (inv == null) {
            return;
        }
        ItemStack held = agent.getMainHandItem();
        if (held.isEmpty() || !isWeaponOrTool(held)) {
            return;
        }

        // 1. Check if any hotbar slot (0-8) is empty
        for (int i = 0; i < 9; i++) {
            if (inv.items.get(i).isEmpty()) {
                inv.selected = i;
                return;
            }
        }

        // 2. Check if offhand is empty
        if (agent.getOffhandItem().isEmpty()) {
            agent.setItemInHand(InteractionHand.OFF_HAND, held);
            inv.items.set(inv.selected, ItemStack.EMPTY);
            return;
        }

        // 3. Check if any inventory slot (9-35) is empty
        for (int i = 9; i < inv.items.size(); i++) {
            if (inv.items.get(i).isEmpty()) {
                inv.items.set(i, held);
                inv.items.set(inv.selected, ItemStack.EMPTY);
                return;
            }
        }

        // 4. Check if any hotbar slot (0-8) has a non-weapon/non-tool item
        for (int i = 0; i < 9; i++) {
            if (!isWeaponOrTool(inv.items.get(i))) {
                inv.selected = i;
                return;
            }
        }

        // 5. Swap held weapon with any non-weapon slot in the rest of the inventory
        for (int i = 9; i < inv.items.size(); i++) {
            if (!isWeaponOrTool(inv.items.get(i))) {
                ItemStack nonWeapon = inv.items.get(i);
                inv.items.set(inv.selected, nonWeapon);
                inv.items.set(i, held);
                return;
            }
        }
    }

    public static boolean isWeaponOrTool(ItemStack stack) {
        if (stack == null || stack.isEmpty()) {
            return false;
        }
        Item item = stack.getItem();
        if (item instanceof TieredItem
                || item instanceof SwordItem
                || item instanceof DiggerItem
                || item instanceof BowItem
                || item instanceof CrossbowItem
                || item instanceof TridentItem) {
            return true;
        }
        String id = BuiltInRegistries.ITEM.getKey(item).toString();
        return id.contains("sword") || id.contains("axe") || id.contains("pickaxe")
                || id.contains("shovel") || id.contains("hoe") || id.contains("bow")
                || id.contains("trident");
    }

    /** SLEEP_IN_BED motor for an interact intent; the bed is tried on enter. */
    public Motor create(AgentEntity agent, AgentIntent intent) {
        InteractBlockIntent interact = intent.getInteractBlock();
        BlockPos pos = new BlockPos(interact.getPos().getX(), interact.getPos().getY(), interact.getPos().getZ());
        return new Sleep(pos, 0, a -> actuateSleepInBed(a, pos));
    }
}
