package com.civquest.mod.actuator;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.network.Filterable;
import net.minecraft.sounds.SoundEvents;
import net.minecraft.sounds.SoundSource;
import net.minecraft.util.Mth;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EquipmentSlot;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.component.WrittenBookContent;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BellBlock;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.LecternBlock;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.entity.LecternBlockEntity;
import net.minecraft.world.level.block.entity.SignBlockEntity;
import net.minecraft.world.level.block.entity.SignText;
import net.minecraft.world.level.block.state.BlockState;
import org.slf4j.Logger;

import java.util.ArrayList;
import java.util.List;

/**
 * Executes physical agent intents on the Minecraft server thread.
 * Translates protobuf intent commands into in-world entity actions, block interactions,
 * combat reflexes, and inventory manipulations.
 */
public class IntentActuator {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double MELEE_REACH = 4.5;

    public void actuate(SettlerEntity settler, AgentIntent intent) {
        if (settler == null || intent == null) {
            return;
        }

        switch (intent.getActionCase()) {
            case NAVIGATE -> actuateNavigate(settler, intent.getNavigate());
            case ATTACK -> actuateAttack(settler, intent.getAttack());
            case INTERACT_BLOCK -> actuateInteractBlock(settler, intent.getInteractBlock());
            case USE_ITEM -> actuateUseItem(settler, intent.getUseItem());
            case CREATE_BOOK -> actuateCreateWrittenBook(settler, intent.getCreateBook());
            case PLACE_SIGNPOST -> actuatePlaceSignpost(settler, intent.getPlaceSignpost());
            default -> LOGGER.debug("Unhandled or unsupported intent action: {}", intent.getActionCase());
        }
    }

    public boolean actuateNavigate(SettlerEntity settler, NavigateIntent intent) {
        if (settler == null || intent == null || settler.getNavigator() == null) {
            return false;
        }
        settler.getNavigator().navigate(intent);
        return true;
    }

    public boolean actuateAttack(SettlerEntity settler, AttackIntent intent) {
        if (settler == null || intent == null) {
            return false;
        }

        // 1. Shield raising / lowering reflex
        if (intent.getRaiseShield()) {
            ItemStack offhand = settler.getItemInHand(InteractionHand.OFF_HAND);
            if (!offhand.is(Items.SHIELD)) {
                int shieldSlot = -1;
                for (int i = 0; i < settler.getInventory().getContainerSize(); i++) {
                    ItemStack item = settler.getInventory().getItem(i);
                    if (item.is(Items.SHIELD)) {
                        shieldSlot = i;
                        break;
                    }
                }
                if (shieldSlot >= 0) {
                    ItemStack foundShield = settler.getInventory().getItem(shieldSlot);
                    settler.setItemInHand(InteractionHand.OFF_HAND, foundShield);
                    settler.getInventory().setItem(shieldSlot, offhand);
                } else {
                    settler.setItemInHand(InteractionHand.OFF_HAND, new ItemStack(Items.SHIELD));
                }
            }
            settler.startUsingItem(InteractionHand.OFF_HAND);
        } else {
            if (settler.isUsingItem() && settler.getUsedItemHand() == InteractionHand.OFF_HAND) {
                settler.stopUsingItem();
            }
        }

        // 2. Attack target entity within melee reach
        if (intent.getTargetEntityId() > 0 && settler.level() != null) {
            Entity target = settler.level().getEntity(intent.getTargetEntityId());
            if (target != null && target.isAlive() && target != settler) {
                double distance = settler.distanceTo(target);
                if (distance <= MELEE_REACH) {
                    // Orient head and body towards target
                    double dx = target.getX() - settler.getX();
                    double dy = target.getEyeY() - settler.getEyeY();
                    double dz = target.getZ() - settler.getZ();
                    double horizontalDist = Math.sqrt(dx * dx + dz * dz);

                    float yaw = (float) (Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
                    float pitch = (float) (-(Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
                    settler.setYRot(yaw);
                    settler.setYHeadRot(yaw);
                    settler.setXRot(pitch);

                    settler.attack(target);
                    settler.swing(InteractionHand.MAIN_HAND, true);
                    settler.resetAttackStrengthTicker();
                }
            }
        }

        // 3. Flee / Kite reflex movement
        if ((intent.getFleeImmediately() || intent.getKeepDistanceKite()) && intent.getTargetEntityId() > 0 && settler.level() != null) {
            Entity target = settler.level().getEntity(intent.getTargetEntityId());
            if (target != null) {
                double awayX = settler.getX() + (settler.getX() - target.getX());
                double awayZ = settler.getZ() + (settler.getZ() - target.getZ());
                settler.getNavigator().navigate(awayX, settler.getY(), awayZ, 1.25f, true, false, false, 2);
            }
        }

        return true;
    }

    public boolean actuateInteractBlock(SettlerEntity settler, InteractBlockIntent intent) {
        if (settler == null || intent == null) {
            return false;
        }

        Level level = settler.level();
        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());

        switch (intent.getInteractionType()) {
            case HARVEST_BREAK -> {
                boolean broken = false;
                if (settler.gameMode != null) {
                    broken = settler.gameMode.destroyBlock(pos);
                }
                if (!broken && level != null) {
                    broken = level.destroyBlock(pos, true, settler);
                }
                return broken;
            }
            case PAVE_ROAD_PATH -> {
                if (level != null) {
                    BlockState current = level.getBlockState(pos);
                    if (current.is(Blocks.GRASS_BLOCK) || current.is(Blocks.DIRT)) {
                        boolean modified = level.setBlockAndUpdate(pos, Blocks.DIRT_PATH.defaultBlockState());
                        level.playSound(null, pos, SoundEvents.SHOVEL_FLATTEN, SoundSource.BLOCKS, 1.0F, 1.0F);
                        return modified;
                    }
                }
                return false;
            }
            case SLEEP_IN_BED -> {
                settler.startSleeping(pos);
                return settler.isSleeping();
            }
            case WAKE_UP -> {
                settler.stopSleeping();
                return !settler.isSleeping();
            }
            case PLACE_BLOCK -> {
                if (level == null) {
                    return false;
                }
                BlockState blockState = resolveBlockState(settler, intent.getBlockOrItemId());
                if (blockState == null) {
                    return false;
                }
                // Consume 1 matching block from inventory if available
                for (int i = 0; i < settler.getInventory().getContainerSize(); i++) {
                    ItemStack item = settler.getInventory().getItem(i);
                    if (item.getItem() instanceof BlockItem bi && bi.getBlock() == blockState.getBlock()) {
                        item.shrink(1);
                        break;
                    }
                }
                return level.setBlockAndUpdate(pos, blockState);
            }
            case RING_BELL -> {
                if (level != null) {
                    BlockState state = level.getBlockState(pos);
                    if (state.getBlock() instanceof BellBlock bell) {
                        return bell.attemptToRing(settler, level, pos, Direction.NORTH);
                    }
                }
                return false;
            }
            default -> {
                LOGGER.debug("InteractBlock type {} not explicitly handled", intent.getInteractionType());
                return false;
            }
        }
    }

    private BlockState resolveBlockState(SettlerEntity settler, String blockOrItemId) {
        if (blockOrItemId != null && !blockOrItemId.isBlank()) {
            ResourceLocation id = ResourceLocation.tryParse(blockOrItemId);
            if (id != null) {
                if (BuiltInRegistries.BLOCK.containsKey(id)) {
                    return BuiltInRegistries.BLOCK.get(id).defaultBlockState();
                } else if (BuiltInRegistries.ITEM.containsKey(id)) {
                    Item item = BuiltInRegistries.ITEM.get(id);
                    if (item instanceof BlockItem bi) {
                        return bi.getBlock().defaultBlockState();
                    }
                }
            }
        }
        ItemStack mainHand = settler.getMainHandItem();
        if (mainHand.getItem() instanceof BlockItem bi) {
            return bi.getBlock().defaultBlockState();
        }
        return Blocks.COBBLESTONE.defaultBlockState();
    }

    public boolean actuateUseItem(SettlerEntity settler, UseItemIntent intent) {
        if (settler == null || intent == null) {
            return false;
        }

        if (intent.getIsEatingOrDrinking()) {
            int slot = intent.getInventorySlot();
            ItemStack stack = (slot >= 0 && slot < settler.getInventory().getContainerSize())
                    ? settler.getInventory().getItem(slot)
                    : settler.getMainHandItem();

            if (stack != null && !stack.isEmpty()) {
                FoodProperties food = stack.get(DataComponents.FOOD);
                if (food != null) {
                    settler.getFoodData().eat(food);
                } else {
                    settler.getFoodData().eat(4, 2.5f);
                }
                stack.shrink(1);
                return true;
            }
        }

        if (intent.getEquipArmorOrOffhand()) {
            int slot = intent.getInventorySlot();
            if (slot >= 0 && slot < settler.getInventory().getContainerSize()) {
                ItemStack stack = settler.getInventory().getItem(slot);
                if (stack != null && !stack.isEmpty()) {
                    EquipmentSlot eqSlot = settler.getEquipmentSlotForItem(stack);
                    if (eqSlot == EquipmentSlot.MAINHAND && stack.is(Items.SHIELD)) {
                        eqSlot = EquipmentSlot.OFFHAND;
                    }
                    ItemStack currentEquipped = settler.getItemBySlot(eqSlot);
                    settler.setItemSlot(eqSlot, stack.copy());
                    settler.getInventory().setItem(slot, currentEquipped);
                    return true;
                }
            }
        }

        return false;
    }

    public boolean actuateCreateWrittenBook(SettlerEntity settler, CreateWrittenBookIntent intent) {
        if (settler == null || intent == null) {
            return false;
        }

        ItemStack book = new ItemStack(Items.WRITTEN_BOOK);
        List<Filterable<Component>> pages = new ArrayList<>();
        for (String pageText : intent.getPagesList()) {
            pages.add(Filterable.passThrough(Component.literal(pageText)));
        }

        WrittenBookContent content = new WrittenBookContent(
                Filterable.passThrough(intent.getTitle() != null ? intent.getTitle() : "Chronicle"),
                intent.getAuthor() != null ? intent.getAuthor() : settler.getGameProfile().getName(),
                0,
                pages,
                true
        );
        book.set(DataComponents.WRITTEN_BOOK_CONTENT, content);

        if (intent.hasTargetLecternOrShelf() && settler.level() != null) {
            BlockPos lecternPos = new BlockPos(
                    intent.getTargetLecternOrShelf().getX(),
                    intent.getTargetLecternOrShelf().getY(),
                    intent.getTargetLecternOrShelf().getZ()
            );
            BlockEntity be = settler.level().getBlockEntity(lecternPos);
            if (be instanceof LecternBlockEntity lectern) {
                lectern.setBook(book, settler);
                LecternBlock.resetBookState(settler, settler.level(), lecternPos, settler.level().getBlockState(lecternPos), true);
                return true;
            }
        }

        settler.getInventory().add(book);
        return true;
    }

    public boolean actuatePlaceSignpost(SettlerEntity settler, PlaceSignpostIntent intent) {
        if (settler == null || intent == null || settler.level() == null) {
            return false;
        }

        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        BlockState signState = intent.getIsHangingSign()
                ? Blocks.OAK_HANGING_SIGN.defaultBlockState()
                : Blocks.OAK_SIGN.defaultBlockState();

        boolean placed = settler.level().setBlockAndUpdate(pos, signState);
        BlockEntity be = settler.level().getBlockEntity(pos);
        if (be instanceof SignBlockEntity signBe) {
            SignText front = signBe.getFrontText();
            front = front.setMessage(0, Component.literal(intent.getLine1() != null ? intent.getLine1() : ""));
            front = front.setMessage(1, Component.literal(intent.getLine2() != null ? intent.getLine2() : ""));
            front = front.setMessage(2, Component.literal(intent.getLine3() != null ? intent.getLine3() : ""));
            front = front.setMessage(3, Component.literal(intent.getLine4() != null ? intent.getLine4() : ""));
            signBe.setText(front, true);
            signBe.setChanged();
            settler.level().sendBlockUpdated(pos, signState, signState, 3);
            return true;
        }
        return placed;
    }
}
