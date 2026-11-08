package com.civquest.mod.actuator;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.network.Filterable;
import net.minecraft.sounds.SoundEvents;
import net.minecraft.sounds.SoundSource;
import net.minecraft.tags.BlockTags;
import net.minecraft.util.Mth;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EquipmentSlot;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.HangingSignItem;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.SignItem;
import net.minecraft.world.item.component.WrittenBookContent;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
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
    private static final double INTERACT_REACH_SQ = 36.0; // 6.0 blocks squared
    private static final double BED_REACH_SQ = 20.25;     // 4.5 blocks squared

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
            case CHAT -> actuateSendChat(settler, intent.getChat());
            default -> LOGGER.debug("Unhandled or unsupported intent action: {}", intent.getActionCase());
        }
    }

    public boolean actuateSendChat(SettlerEntity settler, SendChatIntent intent) {
        if (settler == null || intent == null || intent.getMessage().isBlank()) {
            return false;
        }

        String msg = intent.getMessage().trim();
        String name = settler.getGameProfile().getName();
        Component chatComponent = Component.literal("<" + name + "> " + msg);

        MinecraftServer server = settler.getServer();
        if (server != null && server.getPlayerList() != null) {
            if (intent.getTargetChannel() == ChatChannel.CHAT_CHANNEL_CLAN_BROADCAST) {
                Component clanComponent = Component.literal("[" + settler.getClanId() + "] <" + name + "> " + msg);
                server.getPlayerList().broadcastSystemMessage(clanComponent, false);
            } else {
                // Local broadcast: deliver to players within hearing range (48 blocks)
                double rangeSq = 48.0 * 48.0;
                for (ServerPlayer player : server.getPlayerList().getPlayers()) {
                    if (player.level() == settler.level() && player.distanceToSqr(settler) <= rangeSq) {
                        player.sendSystemMessage(chatComponent);
                    }
                }
            }
            LOGGER.info("Settler '{}' spoke in chat: {}", name, msg);
            return true;
        }
        return false;
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
            if (offhand.is(Items.SHIELD)) {
                settler.startUsingItem(InteractionHand.OFF_HAND);
            } else {
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
                    settler.startUsingItem(InteractionHand.OFF_HAND);
                }
                // If no shield exists in inventory or offhand, do NOT create one
            }
        } else {
            if (settler.isUsingItem() && settler.getUsedItemHand() == InteractionHand.OFF_HAND) {
                settler.stopUsingItem();
            }
        }

        // 2. Attack target entity within melee reach
        if (intent.getTargetEntityId() > 0 && settler.level() != null) {
            Entity target = settler.level().getEntity(intent.getTargetEntityId());
            if (target != null && target.isAlive() && target != settler) {
                boolean targetAllowed = true;
                if (target instanceof SettlerEntity targetSettler) {
                    if (settler.getClanId() != null && !settler.getClanId().isBlank()
                            && settler.getClanId().equals(targetSettler.getClanId())) {
                        LOGGER.debug("Rejecting attack: target settler '{}' is in same clan '{}'",
                                targetSettler.getAgentId(), settler.getClanId());
                        targetAllowed = false;
                    }
                }
                if (targetAllowed && target instanceof Player) {
                    MinecraftServer server = settler.getServer();
                    if (server != null && !server.isPvpAllowed()) {
                        LOGGER.debug("Rejecting attack: PvP is disabled on the server");
                        targetAllowed = false;
                    }
                }

                if (targetAllowed) {
                    double distance = settler.distanceTo(target);
                    if (distance <= MELEE_REACH) {
                        if (settler.getAttackStrengthScale(0.5f) >= 0.9f) {
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
            }
        }

        // 3. Flee / Kite reflex movement
        if ((intent.getFleeImmediately() || intent.getKeepDistanceKite()) && intent.getTargetEntityId() > 0 && settler.level() != null) {
            Entity target = settler.level().getEntity(intent.getTargetEntityId());
            if (target != null) {
                double dx = settler.getX() - target.getX();
                double dz = settler.getZ() - target.getZ();
                double dist = Math.sqrt(dx * dx + dz * dz);
                double fleeDirX;
                double fleeDirZ;
                if (dist < 0.001) {
                    float fleeYaw = settler.getYRot() + 180.0F;
                    double rad = Math.toRadians(fleeYaw);
                    fleeDirX = -Math.sin(rad);
                    fleeDirZ = Math.cos(rad);
                } else {
                    fleeDirX = dx / dist;
                    fleeDirZ = dz / dist;
                }
                double fleeX = settler.getX() + fleeDirX * 10.0;
                double fleeZ = settler.getZ() + fleeDirZ * 10.0;
                settler.getNavigator().navigate(fleeX, settler.getY(), fleeZ, 1.25f, true, false, false, 1);
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
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                    return false;
                }
                BlockState state = level.getBlockState(pos);
                float destroySpeed = state.getDestroySpeed(level, pos);
                if (destroySpeed < 0.0f) {
                    return false;
                }
                boolean broken = false;
                if (settler.gameMode != null) {
                    broken = settler.gameMode.destroyBlock(pos);
                }
                if (!broken) {
                    broken = level.destroyBlock(pos, true, settler);
                }
                return broken;
            }
            case PAVE_ROAD_PATH -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
                        BlockState current = level.getBlockState(pos);
                        if (current.is(Blocks.GRASS_BLOCK) || current.is(Blocks.DIRT)) {
                            boolean modified = level.setBlockAndUpdate(pos, Blocks.DIRT_PATH.defaultBlockState());
                            level.playSound(null, pos, SoundEvents.SHOVEL_FLATTEN, SoundSource.BLOCKS, 1.0F, 1.0F);
                            return modified;
                        }
                    }
                }
                return false;
            }
            case SLEEP_IN_BED -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > BED_REACH_SQ) {
                    return false;
                }
                BlockState state = level.getBlockState(pos);
                if (!state.is(BlockTags.BEDS)) {
                    return false;
                }
                if (state.hasProperty(BedBlock.OCCUPIED) && state.getValue(BedBlock.OCCUPIED)) {
                    return false;
                }
                if (!level.isNight() && !level.isThundering()) {
                    return false;
                }
                settler.startSleeping(pos);
                return settler.isSleeping();
            }
            case WAKE_UP -> {
                settler.stopSleeping();
                return !settler.isSleeping();
            }
            case PLACE_BLOCK -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                    return false;
                }
                BlockState current = level.getBlockState(pos);
                if (!current.canBeReplaced()) {
                    return false;
                }
                BlockState blockState = resolveBlockState(settler, intent.getBlockOrItemId());
                if (blockState == null) {
                    return false;
                }

                if (!settler.isCreative()) {
                    int slot = -1;
                    for (int i = 0; i < settler.getInventory().getContainerSize(); i++) {
                        ItemStack item = settler.getInventory().getItem(i);
                        if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock() == blockState.getBlock()) {
                            slot = i;
                            break;
                        }
                    }
                    if (slot < 0) {
                        return false;
                    }
                    settler.getInventory().getItem(slot).shrink(1);
                }
                return level.setBlockAndUpdate(pos, blockState);
            }
            case RING_BELL -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
                        BlockState state = level.getBlockState(pos);
                        if (state.getBlock() instanceof BellBlock bell) {
                            return bell.attemptToRing(settler, level, pos, Direction.NORTH);
                        }
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
                if (food == null) {
                    return false;
                }
                if (!settler.getFoodData().needsFood() && !food.canAlwaysEat()) {
                    return false;
                }
                settler.getFoodData().eat(food);
                stack.shrink(1);
                return true;
            }
            return false;
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

        List<String> rawPages = intent.getPagesList();
        if (rawPages.size() > 100) {
            return false;
        }

        String title = intent.getTitle() != null ? intent.getTitle() : "Chronicle";
        if (title.length() > 32) {
            return false;
        }

        List<Filterable<Component>> pages = new ArrayList<>();
        for (String pageText : rawPages) {
            if (pageText != null && pageText.length() > 1024) {
                return false;
            }
            pages.add(Filterable.passThrough(Component.literal(pageText != null ? pageText : "")));
        }

        ItemStack book = new ItemStack(Items.WRITTEN_BOOK);
        WrittenBookContent content = new WrittenBookContent(
                Filterable.passThrough(title),
                intent.getAuthor() != null ? intent.getAuthor() : settler.getGameProfile().getName(),
                0,
                pages,
                true
        );
        book.set(DataComponents.WRITTEN_BOOK_CONTENT, content);

        if (intent.hasTargetLecternOrShelf()) {
            if (settler.level() == null) {
                return false;
            }
            BlockPos targetPos = new BlockPos(
                    intent.getTargetLecternOrShelf().getX(),
                    intent.getTargetLecternOrShelf().getY(),
                    intent.getTargetLecternOrShelf().getZ()
            );
            if (!settler.level().hasChunkAt(targetPos)) {
                return false;
            }
            if (settler.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY() + 0.5, targetPos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                return false;
            }
            BlockState state = settler.level().getBlockState(targetPos);
            if (state.is(Blocks.LECTERN)) {
                BlockEntity be = settler.level().getBlockEntity(targetPos);
                if (!(be instanceof LecternBlockEntity lectern) || lectern.hasBook()) {
                    return false;
                }
                lectern.setBook(book, settler);
                LecternBlock.resetBookState(settler, settler.level(), targetPos, state, true);
                return true;
            } else if (state.is(Blocks.CHISELED_BOOKSHELF)) {
                BlockEntity be = settler.level().getBlockEntity(targetPos);
                if (be instanceof net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity shelf) {
                    int slot = intent.getBookshelfSlot();
                    if (slot < 0 || slot >= shelf.getContainerSize() || !shelf.getItem(slot).isEmpty()) {
                        // Find first available slot
                        slot = -1;
                        for (int i = 0; i < shelf.getContainerSize(); i++) {
                            if (shelf.getItem(i).isEmpty()) {
                                slot = i;
                                break;
                            }
                        }
                    }
                    if (slot >= 0 && slot < shelf.getContainerSize() && shelf.getItem(slot).isEmpty()) {
                        shelf.setItem(slot, book);
                        return true;
                    }
                }
                return false;
            }
            return false;
        }

        settler.getInventory().add(book);
        return true;
    }

    public boolean actuatePlaceSignpost(SettlerEntity settler, PlaceSignpostIntent intent) {
        if (settler == null || intent == null || settler.level() == null) {
            return false;
        }

        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        if (!settler.level().hasChunkAt(pos)) {
            return false;
        }
        if (settler.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
            return false;
        }

        BlockState existing = settler.level().getBlockState(pos);
        if (!existing.canBeReplaced()) {
            pos = pos.above();
            existing = settler.level().getBlockState(pos);
            if (!existing.canBeReplaced()) {
                return false;
            }
        }

        BlockState signState = intent.getIsHangingSign()
                ? Blocks.OAK_HANGING_SIGN.defaultBlockState()
                : Blocks.OAK_SIGN.defaultBlockState();

        if (!signState.canSurvive(settler.level(), pos)) {
            return false;
        }

        if (!settler.isCreative()) {
            int signSlot = -1;
            int woodSlot = -1;
            for (int i = 0; i < settler.getInventory().getContainerSize(); i++) {
                ItemStack stack = settler.getInventory().getItem(i);
                if (!stack.isEmpty()) {
                    if (intent.getIsHangingSign()) {
                        if (stack.getItem() instanceof HangingSignItem) {
                            signSlot = i;
                            break;
                        }
                    } else {
                        if (stack.getItem() instanceof SignItem && !(stack.getItem() instanceof HangingSignItem)) {
                            signSlot = i;
                            break;
                        }
                    }
                    if (stack.is(net.minecraft.tags.ItemTags.PLANKS) || stack.is(net.minecraft.tags.ItemTags.LOGS)) {
                        woodSlot = i;
                    }
                }
            }
            if (signSlot >= 0) {
                settler.getInventory().getItem(signSlot).shrink(1);
            } else if (woodSlot >= 0) {
                settler.getInventory().getItem(woodSlot).shrink(1);
            } else {
                return false;
            }
        }

        boolean placed = settler.level().setBlockAndUpdate(pos, signState);
        if (!placed) {
            return false;
        }

        BlockEntity be = settler.level().getBlockEntity(pos);
        if (be instanceof SignBlockEntity signBe) {
            SignText front = signBe.getFrontText();
            front = front.setMessage(0, Component.literal(sanitizeSignLine(intent.getLine1())));
            front = front.setMessage(1, Component.literal(sanitizeSignLine(intent.getLine2())));
            front = front.setMessage(2, Component.literal(sanitizeSignLine(intent.getLine3())));
            front = front.setMessage(3, Component.literal(sanitizeSignLine(intent.getLine4())));
            signBe.setText(front, true);
            signBe.setChanged();
            settler.level().sendBlockUpdated(pos, signState, signState, 3);
            return true;
        }
        return placed;
    }

    private static String sanitizeSignLine(String line) {
        if (line == null) {
            return "";
        }
        return line.length() > 80 ? line.substring(0, 80) : line;
    }
}
