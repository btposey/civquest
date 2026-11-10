package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.RespondTradeIntent;
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

    public void actuate(AgentEntity agent, AgentIntent intent) {
        if (agent == null || intent == null) {
            return;
        }

        if (agent.getMotorFSM() != null) {
            agent.getMotorFSM().handleIntent(intent, this);
            return;
        }

        switch (intent.getActionCase()) {
            case NAVIGATE -> actuateNavigate(agent, intent.getNavigate());
            case ATTACK -> actuateAttack(agent, intent.getAttack());
            case INTERACT_BLOCK -> actuateInteractBlock(agent, intent.getInteractBlock());
            case USE_ITEM -> actuateUseItem(agent, intent.getUseItem());
            case CRAFT_ITEM -> actuateCraftItem(agent, intent.getCraftItem());
            case BUILD_BLOCK -> actuateBuildBlock(agent, intent.getBuildBlock());
            case CREATE_BOOK -> actuateCreateWrittenBook(agent, intent.getCreateBook());
            case PLACE_SIGNPOST -> actuatePlaceSignpost(agent, intent.getPlaceSignpost());
            case CHAT -> actuateSendChat(agent, intent.getChat());
            case TRADE_RESPONSE -> actuateRespondTrade(agent, intent.getTradeResponse());
            default -> LOGGER.debug("Unhandled or unsupported intent action: {}", intent.getActionCase());
        }
    }

    public boolean actuateRespondTrade(AgentEntity agent, RespondTradeIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }
        if (!intent.getSpokenResponse().isBlank()) {
            actuateSendChat(agent, SendChatIntent.newBuilder()
                    .setMessage(intent.getSpokenResponse())
                    .setTargetChannel(ChatChannel.CHAT_CHANNEL_LOCAL)
                    .build());
        }
        if (!intent.getAcceptDeal() || agent.level() == null) {
            return false;
        }
        Entity entity = agent.level().getEntity(intent.getPlayerEntityId());
        if (!(entity instanceof ServerPlayer player)) {
            return true;
        }

        // Transfer player's held item (if offered) to agent and agent's counter-offer items to player
        ItemStack playerHeld = player.getMainHandItem();
        if (!playerHeld.isEmpty()) {
            int transferCount = playerHeld.getMaxStackSize() == 1 ? 1 : Math.min(16, playerHeld.getCount());
            ItemStack gift = playerHeld.copyWithCount(transferCount);
            if (agent.getInventory().add(gift)) {
                playerHeld.shrink(transferCount - gift.getCount());
                agent.autoEquipBestGear();
            }
        }

        for (com.civquest.mod.grpc.v1.ItemStackProto counter : intent.getAgentCounterOfferList()) {
            if (counter.getCount() <= 0 || counter.getItemId().isBlank()) {
                continue;
            }
            ResourceLocation id = ResourceLocation.tryParse(counter.getItemId());
            if (id != null && BuiltInRegistries.ITEM.containsKey(id)) {
                Item item = BuiltInRegistries.ITEM.get(id);
                int remaining = counter.getCount();
                for (int i = 0; i < agent.getInventory().items.size() && remaining > 0; i++) {
                    ItemStack slotStack = agent.getInventory().items.get(i);
                    if (slotStack.is(item)) {
                        int take = Math.min(remaining, slotStack.getCount());
                        ItemStack toGive = slotStack.copyWithCount(take);
                        slotStack.shrink(take);
                        remaining -= take;
                        if (!player.getInventory().add(toGive)) {
                            player.drop(toGive, false);
                        }
                    }
                }
            }
        }
        return true;
    }

    public boolean actuateSendChat(AgentEntity agent, SendChatIntent intent) {
        if (agent == null || intent == null || intent.getMessage().isBlank()) {
            return false;
        }

        String msg = intent.getMessage().trim();
        String name = agent.getGameProfile().getName();
        Component chatComponent = Component.literal("<" + name + "> " + msg);

        MinecraftServer server = agent.getServer();
        if (server != null && server.getPlayerList() != null) {
            if (intent.getTargetChannel() == ChatChannel.CHAT_CHANNEL_CLAN_BROADCAST) {
                Component clanComponent = Component.literal("[" + agent.getClanId() + "] <" + name + "> " + msg);
                server.getPlayerList().broadcastSystemMessage(clanComponent, false);
            } else {
                // Local broadcast: deliver to players within hearing range (48 blocks)
                double rangeSq = 48.0 * 48.0;
                for (ServerPlayer player : server.getPlayerList().getPlayers()) {
                    if (player.level() == agent.level() && player.distanceToSqr(agent) <= rangeSq) {
                        player.sendSystemMessage(chatComponent);
                    }
                }
            }
            LOGGER.info("Agent '{}' spoke in chat: {}", name, msg);
            return true;
        }
        return false;
    }

    public boolean actuateNavigate(AgentEntity agent, NavigateIntent intent) {
        if (agent == null || intent == null || agent.getNavigator() == null) {
            return false;
        }
        agent.getNavigator().navigate(intent);
        return true;
    }

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

        // 3. Flee / Kite reflex movement
        if ((intent.getFleeImmediately() || intent.getKeepDistanceKite()) && intent.getTargetEntityId() > 0 && agent.level() != null) {
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

    public boolean actuateInteractBlock(AgentEntity agent, InteractBlockIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        Level level = agent.level();
        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());

        switch (intent.getInteractionType()) {
            case HARVEST_BREAK -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                    return false;
                }
                BlockState state = level.getBlockState(pos);
                float destroySpeed = state.getDestroySpeed(level, pos);
                if (destroySpeed < 0.0f) {
                    return false;
                }
                agent.equipBestToolFor(state);
                // Timed, tool-aware breaking: best tool equipped, progress accrues per tick with swing animation.
                return agent.getBreaker().start(pos);
            }
            case PAVE_ROAD_PATH -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
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
                if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > BED_REACH_SQ) {
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
                agent.startSleeping(pos);
                return agent.isSleeping();
            }
            case WAKE_UP -> {
                agent.stopSleeping();
                return !agent.isSleeping();
            }
            case PLACE_BLOCK -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                    return false;
                }
                BlockState current = level.getBlockState(pos);
                if (!current.canBeReplaced()) {
                    return false;
                }
                BlockState blockState = resolveBlockState(agent, intent.getBlockOrItemId());
                if (blockState == null) {
                    return false;
                }

                if (!agent.isCreative()) {
                    int slot = findMatchingBlockItemSlot(agent, blockState, intent.getBlockOrItemId());
                    if (slot < 0) {
                        return false;
                    }
                    ItemStack chosen = agent.getInventory().getItem(slot);
                    if (chosen.getItem() instanceof BlockItem chosenBi) {
                        blockState = chosenBi.getBlock().defaultBlockState();
                    }
                    chosen.shrink(1);
                }
                agent.swing(InteractionHand.MAIN_HAND, true);
                return level.setBlockAndUpdate(pos, blockState);
            }
            case RING_BELL -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
                        BlockState state = level.getBlockState(pos);
                        if (state.getBlock() instanceof BellBlock bell) {
                            return bell.attemptToRing(agent, level, pos, Direction.NORTH);
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

    private int findMatchingBlockItemSlot(AgentEntity agent, BlockState targetState, String requestedId) {
        var inv = agent.getInventory();
        if (inv == null) {
            return -1;
        }
        // 1. Exact block match
        if (targetState != null) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock() == targetState.getBlock()) {
                    return i;
                }
            }
        }
        // 2. Category / family match (e.g. any planks, log, stone/cobble, or light source)
        String req = requestedId != null ? requestedId.trim().toLowerCase() : "";
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack item = inv.getItem(i);
            if (item.isEmpty() || !(item.getItem() instanceof BlockItem bi)) {
                continue;
            }
            String key = BuiltInRegistries.ITEM.getKey(item.getItem()).toString().toLowerCase();
            if (req.isEmpty() || req.contains("planks") && key.endsWith("_planks")
                    || req.contains("log") && (key.endsWith("_log") || key.endsWith("_wood") || key.endsWith("_stem"))
                    || (req.contains("cobble") || req.contains("stone") || req.contains("dirt"))
                    && (key.contains("cobblestone") || key.endsWith(":stone") || key.endsWith(":dirt") || key.endsWith("_planks") || key.endsWith("_log"))
                    || (req.contains("campfire") || req.contains("torch") || req.contains("lantern"))
                    && (key.contains("campfire") || key.contains("torch") || key.contains("lantern"))) {
                return i;
            }
        }
        // 3. Fallback to any solid placeable building block if a building block was requested
        if (req.isEmpty() || req.contains("planks") || req.contains("log") || req.contains("cobble") || req.contains("dirt") || req.contains("stone")) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock().defaultBlockState().isSolid()) {
                    return i;
                }
            }
        }
        return -1;
    }

    private BlockState resolveBlockState(AgentEntity agent, String blockOrItemId) {
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
        ItemStack mainHand = agent.getMainHandItem();
        if (mainHand.getItem() instanceof BlockItem bi) {
            return bi.getBlock().defaultBlockState();
        }
        return Blocks.COBBLESTONE.defaultBlockState();
    }

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

    public boolean actuateCreateWrittenBook(AgentEntity agent, CreateWrittenBookIntent intent) {
        if (agent == null || intent == null) {
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
                intent.getAuthor() != null ? intent.getAuthor() : agent.getGameProfile().getName(),
                0,
                pages,
                true
        );
        book.set(DataComponents.WRITTEN_BOOK_CONTENT, content);

        if (intent.hasTargetLecternOrShelf()) {
            if (agent.level() == null) {
                return false;
            }
            BlockPos targetPos = new BlockPos(
                    intent.getTargetLecternOrShelf().getX(),
                    intent.getTargetLecternOrShelf().getY(),
                    intent.getTargetLecternOrShelf().getZ()
            );
            if (!agent.level().hasChunkAt(targetPos)) {
                return false;
            }
            if (agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY() + 0.5, targetPos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                return false;
            }
            BlockState state = agent.level().getBlockState(targetPos);
            if (state.is(Blocks.LECTERN)) {
                BlockEntity be = agent.level().getBlockEntity(targetPos);
                if (!(be instanceof LecternBlockEntity lectern) || lectern.hasBook()) {
                    return false;
                }
                lectern.setBook(book, agent);
                LecternBlock.resetBookState(agent, agent.level(), targetPos, state, true);
                return true;
            } else if (state.is(Blocks.CHISELED_BOOKSHELF)) {
                BlockEntity be = agent.level().getBlockEntity(targetPos);
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

        agent.getInventory().add(book);
        return true;
    }

    public boolean actuatePlaceSignpost(AgentEntity agent, PlaceSignpostIntent intent) {
        if (agent == null || intent == null || agent.level() == null) {
            return false;
        }

        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        if (!agent.level().hasChunkAt(pos)) {
            return false;
        }
        if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
            return false;
        }

        BlockState existing = agent.level().getBlockState(pos);
        if (!existing.canBeReplaced()) {
            pos = pos.above();
            existing = agent.level().getBlockState(pos);
            if (!existing.canBeReplaced()) {
                return false;
            }
        }

        BlockState signState = intent.getIsHangingSign()
                ? Blocks.OAK_HANGING_SIGN.defaultBlockState()
                : Blocks.OAK_SIGN.defaultBlockState();

        if (!signState.canSurvive(agent.level(), pos)) {
            return false;
        }

        if (!agent.isCreative()) {
            int signSlot = -1;
            int woodSlot = -1;
            for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                ItemStack stack = agent.getInventory().getItem(i);
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
                agent.getInventory().getItem(signSlot).shrink(1);
            } else if (woodSlot >= 0) {
                agent.getInventory().getItem(woodSlot).shrink(1);
            } else {
                return false;
            }
        }

        boolean placed = agent.level().setBlockAndUpdate(pos, signState);
        if (!placed) {
            return false;
        }

        BlockEntity be = agent.level().getBlockEntity(pos);
        if (be instanceof SignBlockEntity signBe) {
            SignText front = signBe.getFrontText();
            front = front.setMessage(0, Component.literal(sanitizeSignLine(intent.getLine1())));
            front = front.setMessage(1, Component.literal(sanitizeSignLine(intent.getLine2())));
            front = front.setMessage(2, Component.literal(sanitizeSignLine(intent.getLine3())));
            front = front.setMessage(3, Component.literal(sanitizeSignLine(intent.getLine4())));
            signBe.setText(front, true);
            signBe.setChanged();
            agent.level().sendBlockUpdated(pos, signState, signState, 3);
            return true;
        }
        return placed;
    }

    public boolean actuateBuildBlock(AgentEntity agent, com.civquest.mod.grpc.v1.BuildBlockIntent intent) {
        if (agent == null || intent == null || !intent.hasTargetVoxel()) {
            return false;
        }
        return actuateInteractBlock(agent, InteractBlockIntent.newBuilder()
                .setPos(intent.getTargetVoxel())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId(intent.getBlockStateString())
                .build());
    }

    public boolean actuateCraftItem(AgentEntity agent, com.civquest.mod.grpc.v1.CraftItemIntent intent) {
        if (agent == null || intent == null || intent.getRecipeId() == null || intent.getRecipeId().isBlank()) {
            return false;
        }
        net.minecraft.world.entity.player.Inventory inv = agent.getInventory();
        if (inv == null) {
            return false;
        }

        String recipe = intent.getRecipeId().trim().toLowerCase();
        if (recipe.contains(":")) {
            recipe = recipe.substring(recipe.indexOf(':') + 1);
        }

        switch (recipe) {
            case "oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks", "planks" -> {
                if (consumeInventoryMatch(inv, id -> id.endsWith("_log") || id.endsWith("_wood") || id.endsWith("_stem"), 1)) {
                    inv.add(new ItemStack(Items.OAK_PLANKS, 4));
                    return true;
                }
                return false;
            }
            case "stick", "sticks" -> {
                if (consumeInventoryMatch(inv, id -> id.endsWith("_planks"), 2)
                        || consumeInventoryMatch(inv, id -> id.endsWith("_log") || id.endsWith("_wood"), 1)) {
                    inv.add(new ItemStack(Items.STICK, 4));
                    return true;
                }
                return false;
            }
            case "wooden_pickaxe", "wooden_axe", "wooden_sword" -> {
                boolean paid = consumeInventoryMatch(inv, id -> id.endsWith("_log") || id.endsWith("_wood"), 1);
                if (!paid && countInventoryMatch(inv, id -> id.endsWith("_planks")) >= 3
                        && countInventoryMatch(inv, id -> id.equals("minecraft:stick") || id.endsWith(":stick")) >= 2) {
                    consumeInventoryMatch(inv, id -> id.endsWith("_planks"), 3);
                    consumeInventoryMatch(inv, id -> id.equals("minecraft:stick") || id.endsWith(":stick"), 2);
                    paid = true;
                } else if (!paid && countInventoryMatch(inv, id -> id.endsWith("_planks")) >= 4) {
                    consumeInventoryMatch(inv, id -> id.endsWith("_planks"), 4);
                    paid = true;
                }
                if (!paid) {
                    return false;
                }
                Item resultItem = switch (recipe) {
                    case "wooden_pickaxe" -> Items.WOODEN_PICKAXE;
                    case "wooden_axe" -> Items.WOODEN_AXE;
                    default -> Items.WOODEN_SWORD;
                };
                inv.add(new ItemStack(resultItem, 1));
                agent.autoEquipBestGear();
                return true;
            }
            case "stone_pickaxe", "stone_axe", "stone_sword" -> {
                int cobbleCount = countInventoryMatch(inv, id -> id.contains("cobblestone") || id.contains("cobbled_deepslate") || id.contains("blackstone"));
                int woodCount = countInventoryMatch(inv, id -> id.endsWith("_log") || id.endsWith("_planks") || id.endsWith("stick"));
                if (cobbleCount < 3 || woodCount < 1) {
                    return false;
                }
                consumeInventoryMatch(inv, id -> id.contains("cobblestone") || id.contains("cobbled_deepslate") || id.contains("blackstone"), 3);
                consumeInventoryMatch(inv, id -> id.endsWith("stick") || id.endsWith("_planks") || id.endsWith("_log"), 1);
                Item resultItem = switch (recipe) {
                    case "stone_pickaxe" -> Items.STONE_PICKAXE;
                    case "stone_axe" -> Items.STONE_AXE;
                    default -> Items.STONE_SWORD;
                };
                inv.add(new ItemStack(resultItem, 1));
                agent.autoEquipBestGear();
                return true;
            }
            case "torch", "torches" -> {
                if (consumeInventoryMatch(inv, id -> id.contains("coal") || id.endsWith("_log") || id.endsWith("_planks") || id.endsWith("stick"), 1)) {
                    inv.add(new ItemStack(Items.TORCH, 4));
                    return true;
                }
                return false;
            }
            case "campfire" -> {
                if (consumeInventoryMatch(inv, id -> id.endsWith("_log") || id.endsWith("_planks") || id.contains("coal") || id.endsWith("stick"), 1)) {
                    inv.add(new ItemStack(Items.CAMPFIRE, 1));
                    return true;
                }
                return false;
            }
            default -> {
                return false;
            }
        }
    }

    private static int countInventoryMatch(net.minecraft.world.entity.player.Inventory inv, java.util.function.Predicate<String> matcher) {
        int total = 0;
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack s = inv.getItem(i);
            if (s != null && !s.isEmpty()) {
                String id = BuiltInRegistries.ITEM.getKey(s.getItem()).toString().toLowerCase();
                if (matcher.test(id)) {
                    total += s.getCount();
                }
            }
        }
        return total;
    }

    private static boolean consumeInventoryMatch(net.minecraft.world.entity.player.Inventory inv, java.util.function.Predicate<String> matcher, int required) {
        if (countInventoryMatch(inv, matcher) < required) {
            return false;
        }
        int remaining = required;
        for (int i = 0; i < inv.getContainerSize() && remaining > 0; i++) {
            ItemStack s = inv.getItem(i);
            if (s != null && !s.isEmpty()) {
                String id = BuiltInRegistries.ITEM.getKey(s.getItem()).toString().toLowerCase();
                if (matcher.test(id)) {
                    int take = Math.min(remaining, s.getCount());
                    s.shrink(take);
                    remaining -= take;
                }
            }
        }
        return true;
    }

    private static String sanitizeSignLine(String line) {
        if (line == null) {
            return "";
        }
        return line.length() > 80 ? line.substring(0, 80) : line;
    }
}
