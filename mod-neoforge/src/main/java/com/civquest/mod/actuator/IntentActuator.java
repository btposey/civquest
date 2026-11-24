package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.MotorState;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.RespondTradeIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import it.unimi.dsi.fastutil.ints.IntArrayList;
import it.unimi.dsi.fastutil.ints.IntList;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.NonNullList;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.network.Filterable;
import net.minecraft.sounds.SoundEvents;
import net.minecraft.sounds.SoundSource;
import net.minecraft.tags.BlockTags;
import net.minecraft.tags.TagKey;
import net.minecraft.util.Mth;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EquipmentSlot;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.entity.player.StackedContents;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.BowItem;
import net.minecraft.world.item.CrossbowItem;
import net.minecraft.world.item.DiggerItem;
import net.minecraft.world.item.HangingSignItem;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.SignItem;
import net.minecraft.world.item.SwordItem;
import net.minecraft.world.item.TieredItem;
import net.minecraft.world.item.TridentItem;
import net.minecraft.world.item.component.WrittenBookContent;
import net.minecraft.world.item.crafting.CraftingInput;
import net.minecraft.world.item.crafting.CraftingRecipe;
import net.minecraft.world.item.crafting.Ingredient;
import net.minecraft.world.item.crafting.RecipeHolder;
import net.minecraft.world.item.crafting.RecipeManager;
import net.minecraft.world.item.crafting.ShapedRecipe;
import net.minecraft.world.item.crafting.ShapelessRecipe;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.BellBlock;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.LecternBlock;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.entity.LecternBlockEntity;
import net.minecraft.world.level.block.entity.SignBlockEntity;
import net.minecraft.world.level.block.entity.SignText;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BedPart;
import net.minecraft.world.phys.AABB;
import org.slf4j.Logger;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Deque;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Executes physical agent intents on the Minecraft server thread.
 * Translates protobuf intent commands and {@link IntentBatch} sequences into in-world entity actions,
 * block interactions, combat reflexes, and inventory manipulations.
 */
public class IntentActuator {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double MELEE_REACH = 4.5;
    private static final double INTERACT_REACH_SQ = 36.0; // 6.0 blocks squared
    private static final double BED_REACH_SQ = 20.25;     // 4.5 blocks squared

    private static final class BatchSession {
        AgentEntity agent;
        IntentBatch activeBatch;
        final Deque<IntentBatch> pendingBatches = new ArrayDeque<>();
        int activeIndex = 0;
        IntentReceipt.ReceiptStatus status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED;
        String failureReason = "";
        long elapsedTicks = 0L;
        boolean dispatched = false;
    }

    private final ConcurrentHashMap<String, BatchSession> sessions = new ConcurrentHashMap<>();
    private final RecipeManager fallbackRecipeManager;

    public IntentActuator() {
        this(null);
    }

    /**
     * Constructs an IntentActuator with an optional fallback {@link RecipeManager} for mock-driven unit tests
     * where {@code agent.getServer()} and {@code agent.level()} are null.
     */
    public IntentActuator(RecipeManager fallbackRecipeManager) {
        this.fallbackRecipeManager = fallbackRecipeManager;
    }

    private static String resolveAgentKey(AgentEntity agent) {
        String id = agent.getAgentId();
        return (id != null && !id.isBlank()) ? id : ("entity@" + System.identityHashCode(agent));
    }

    /**
     * Submits a prioritized {@link IntentBatch} vector for sequential execution by the agent.
     */
    public void submitBatch(AgentEntity agent, IntentBatch batch) {
        if (agent == null || batch == null) {
            return;
        }
        if (agent.getIntentActuator() == null) {
            agent.setIntentActuator(this);
        }

        BatchSession session = sessions.computeIfAbsent(resolveAgentKey(agent), k -> new BatchSession());
        session.agent = agent;

        // Out-of-band chat dispatch: if batch contains SendChatIntent, execute it immediately
        // so chat is never queued behind long-running or stalled NavigateIntent in pendingBatches.
        List<AgentIntent> nonChatIntents = new ArrayList<>();
        boolean hasChat = false;
        for (AgentIntent intent : batch.getIntentsList()) {
            if (intent.hasChat()) {
                hasChat = true;
                actuateSendChat(session, intent.getChat());
            } else {
                nonChatIntents.add(intent);
            }
        }

        if (hasChat && nonChatIntents.isEmpty()) {
            synchronized (session) {
                if (!isSessionInFlight(session)) {
                    session.activeBatch = batch;
                    session.activeIndex = batch.getIntentsCount();
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED;
                    session.failureReason = "";
                    session.elapsedTicks = 0L;
                    session.dispatched = true;
                    syncReceiptToMotor(agent, session);
                }
            }
            return;
        }

        IntentBatch queueBatch = hasChat ? batch.toBuilder().clearIntents().addAllIntents(nonChatIntents).build() : batch;

        // Stream handshake / keepalive message with no flush and no intents: bind stream without mutating active batch
        if (!queueBatch.getFlushPeriphery() && queueBatch.getIntentsCount() == 0 && queueBatch.getBatchId() == 0L) {
            return;
        }

        synchronized (session) {
            if (queueBatch.getFlushPeriphery()) {
                flushSessionAndMotor(agent, session);
                if (queueBatch.getIntentsCount() == 0) {
                    session.activeBatch = queueBatch;
                    session.activeIndex = 0;
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED;
                    session.failureReason = "";
                    session.elapsedTicks = 0L;
                    session.dispatched = true;
                    syncReceiptToMotor(agent, session);
                    return;
                }
            } else if (isSessionInFlight(session)) {
                session.pendingBatches.addLast(queueBatch);
                return;
            } else if (queueBatch.getIntentsCount() == 0) {
                session.activeBatch = queueBatch;
                session.activeIndex = 0;
                session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED;
                session.failureReason = "";
                session.elapsedTicks = 0L;
                session.dispatched = true;
                syncReceiptToMotor(agent, session);
                return;
            }

            session.activeBatch = queueBatch;
            session.activeIndex = 0;
            session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_QUEUED;
            session.failureReason = "";
            session.elapsedTicks = 0L;
            session.dispatched = false;
            syncReceiptToMotor(agent, session);
            advanceBatch(agent, session);
        }
    }

    public void actuateIntentBatch(AgentEntity agent, IntentBatch batch) {
        submitBatch(agent, batch);
    }

    public void actuateIntentBatch(BatchSession session, IntentBatch batch) {
        if (session != null && session.agent != null) {
            submitBatch(session.agent, batch);
        }
    }

    public boolean actuateSendChat(BatchSession session, SendChatIntent intent) {
        if (session == null || session.agent == null || intent == null) {
            return false;
        }
        return actuateSendChat(session.agent, intent);
    }

    public static boolean hasSendChat(IntentBatch batch) {
        if (batch == null) {
            return false;
        }
        for (AgentIntent intent : batch.getIntentsList()) {
            if (intent.hasChat()) {
                return true;
            }
        }
        return false;
    }

    public static SendChatIntent getSendChat(IntentBatch batch) {
        if (batch == null) {
            return null;
        }
        for (AgentIntent intent : batch.getIntentsList()) {
            if (intent.hasChat()) {
                return intent.getChat();
            }
        }
        return null;
    }

    /**
     * Clears the agent's active and queued batches, clears the MotorPDA LIFO stack,
     * cancels active breaking/navigation, and resets MotorPDA to {@link MotorState#IDLE}.
     */
    public void flush(AgentEntity agent) {
        if (agent == null) {
            return;
        }
        BatchSession session = sessions.get(resolveAgentKey(agent));
        if (session != null) {
            synchronized (session) {
                flushSessionAndMotor(agent, session);
            }
        } else if (agent.getMotorFSM() != null) {
            agent.getMotorFSM().flush();
            agent.getMotorFSM().updateReceiptState(
                    0L, 0, IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED, ""
            );
        }
    }

    private void flushSessionAndMotor(AgentEntity agent, BatchSession session) {
        session.pendingBatches.clear();
        session.activeBatch = null;
        session.activeIndex = 0;
        session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED;
        session.failureReason = "";
        session.elapsedTicks = 0L;
        session.dispatched = false;
        if (agent.getMotorFSM() != null) {
            agent.getMotorFSM().flush();
        }
        syncReceiptToMotor(agent, session);
    }

    /**
     * Advances the active {@link IntentBatch} on each 20 Hz server tick, monitoring primitive completion,
     * failure short-circuiting, timeout aborts, and reflex preemption stack depth.
     */
    public void tick(AgentEntity agent) {
        if (agent == null) {
            return;
        }
        BatchSession session = sessions.get(resolveAgentKey(agent));
        if (session == null) {
            return;
        }
        synchronized (session) {
            if (session.activeBatch == null) {
                return;
            }
            if (!isSessionInFlight(session)) {
                return;
            }

            session.elapsedTicks++;
            long timeoutTicks = session.activeBatch.getTimeoutTicks();
            if (timeoutTicks > 0 && session.elapsedTicks >= timeoutTicks) {
                session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_ABORTED;
                session.failureReason = "Batch timed out after " + session.elapsedTicks + " ticks";
                session.pendingBatches.clear();
                if (agent.getMotorFSM() != null) {
                    agent.getMotorFSM().flush();
                }
                syncReceiptToMotor(agent, session);
                return;
            }

            advanceBatch(agent, session);
        }
    }

    private static boolean isSessionInFlight(BatchSession session) {
        return session.activeBatch != null && (session.status == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_QUEUED
                || session.status == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING
                || session.status == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX);
    }

    private void advanceBatch(AgentEntity agent, BatchSession session) {
        AgentMotorFSM fsm = agent.getMotorFSM();
        while (session.activeBatch != null) {
            int count = session.activeBatch.getIntentsCount();
            if (session.activeIndex >= count) {
                session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED;
                session.failureReason = "";
                syncReceiptToMotor(agent, session);
                if (!session.pendingBatches.isEmpty()) {
                    IntentBatch next = session.pendingBatches.pollFirst();
                    session.activeBatch = next;
                    session.activeIndex = 0;
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_QUEUED;
                    session.failureReason = "";
                    session.elapsedTicks = 0L;
                    session.dispatched = false;
                    syncReceiptToMotor(agent, session);
                    continue;
                }
                break;
            }

            AgentIntent currentIntent = session.activeBatch.getIntents(session.activeIndex);
            if (!session.dispatched) {
                if (isAlreadyBrokenHarvestIntent(agent, currentIntent)) {
                    if (fsm != null && currentIntent.getSequenceId() > 0) {
                        fsm.markSequenceProcessed(currentIntent.getSequenceId());
                    }
                    session.activeIndex++;
                    session.dispatched = false;
                    continue;
                }
                session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING;
                syncReceiptToMotor(agent, session);

                boolean ok = actuateWithResult(agent, currentIntent);
                session.dispatched = true;

                if (!ok) {
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED;
                    session.failureReason = resolveFailureReason(fsm, session.activeIndex, currentIntent);
                    session.pendingBatches.clear();
                    syncReceiptToMotor(agent, session);
                    break;
                }

                if (fsm != null) {
                    if (fsm.getStackDepth() > 0) {
                        session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX;
                        syncReceiptToMotor(agent, session);
                        break;
                    }
                    if (fsm.getState() == MotorState.IDLE) {
                        if (!fsm.didLastAsyncActionSucceed()) {
                            session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED;
                            session.failureReason = resolveFailureReason(fsm, session.activeIndex, currentIntent);
                            session.pendingBatches.clear();
                            syncReceiptToMotor(agent, session);
                            break;
                        }
                        session.activeIndex++;
                        session.dispatched = false;
                    } else {
                        session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING;
                        syncReceiptToMotor(agent, session);
                        break;
                    }
                } else {
                    session.activeIndex++;
                    session.dispatched = false;
                }
            } else {
                if (fsm != null && fsm.getStackDepth() > 0) {
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX;
                    syncReceiptToMotor(agent, session);
                    break;
                }
                if (fsm == null || fsm.getState() == MotorState.IDLE) {
                    if (fsm != null && !fsm.didLastAsyncActionSucceed()) {
                        session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED;
                        session.failureReason = resolveFailureReason(fsm, session.activeIndex, currentIntent);
                        session.pendingBatches.clear();
                        syncReceiptToMotor(agent, session);
                        break;
                    }
                    session.activeIndex++;
                    session.dispatched = false;
                } else {
                    session.status = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING;
                    syncReceiptToMotor(agent, session);
                    break;
                }
            }
        }
    }

    private static boolean isAlreadyBrokenHarvestIntent(AgentEntity agent, AgentIntent intent) {
        if (agent == null || intent == null || !intent.hasInteractBlock()) {
            return false;
        }
        InteractBlockIntent interact = intent.getInteractBlock();
        if (interact.getInteractionType() != InteractBlockIntent.InteractionType.HARVEST_BREAK) {
            return false;
        }
        Level level = agent.level();
        if (level == null) {
            return false;
        }
        BlockPos targetPos = new BlockPos(interact.getPos().getX(), interact.getPos().getY(), interact.getPos().getZ());
        if (!level.hasChunkAt(targetPos)) {
            return false;
        }
        BlockState state = level.getBlockState(targetPos);
        return state != null && state.isAir();
    }

    private static String resolveFailureReason(AgentMotorFSM fsm, int index, AgentIntent intent) {
        if (fsm != null && fsm.getLastAsyncFailureReason() != null && !fsm.getLastAsyncFailureReason().isBlank()) {
            return fsm.getLastAsyncFailureReason();
        }
        return "Primitive at index " + index + " (" + intent.getActionCase() + ") failed";
    }

    private static void syncReceiptToMotor(AgentEntity agent, BatchSession session) {
        if (agent.getMotorFSM() == null) {
            return;
        }
        long batchId = session.activeBatch != null ? session.activeBatch.getBatchId() : 0L;
        agent.getMotorFSM().updateReceiptState(batchId, session.activeIndex, session.status, session.failureReason);
    }

    /**
     * Returns the current {@link IntentReceipt} for the given agent.
     */
    public IntentReceipt getCurrentReceipt(AgentEntity agent) {
        if (agent == null) {
            return IntentReceipt.getDefaultInstance();
        }
        BatchSession session = sessions.get(resolveAgentKey(agent));
        if (session == null) {
            return agent.getMotorFSM() != null
                    ? agent.getMotorFSM().getCurrentReceipt()
                    : IntentReceipt.getDefaultInstance();
        }
        synchronized (session) {
            int depth = agent.getMotorFSM() != null ? agent.getMotorFSM().getStackDepth() : 0;
            IntentReceipt.ReceiptStatus effectiveStatus = session.status;
            if (depth > 0 && isSessionInFlight(session)) {
                effectiveStatus = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX;
            }
            return IntentReceipt.newBuilder()
                    .setBatchId(session.activeBatch != null ? session.activeBatch.getBatchId() : 0L)
                    .setActiveIndex(session.activeIndex)
                    .setStatus(effectiveStatus)
                    .setFailureReason(session.failureReason != null ? session.failureReason : "")
                    .setMotorStackDepth(depth)
                    .build();
        }
    }

    public int getQueuedBatchCount(AgentEntity agent) {
        if (agent == null) {
            return 0;
        }
        BatchSession session = sessions.get(resolveAgentKey(agent));
        if (session == null) {
            return 0;
        }
        synchronized (session) {
            return session.pendingBatches.size();
        }
    }

    public void actuate(AgentEntity agent, AgentIntent intent) {
        actuateWithResult(agent, intent);
    }

    public boolean actuateWithResult(AgentEntity agent, AgentIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        if (agent.getMotorFSM() != null) {
            return agent.getMotorFSM().handleIntent(intent, this);
        }

        return switch (intent.getActionCase()) {
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
            default -> {
                LOGGER.debug("Unhandled or unsupported intent action: {}", intent.getActionCase());
                yield false;
            }
        };
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
        String name = agent.getGameProfile() != null ? agent.getGameProfile().getName()
                : (agent.getAgentId() != null ? agent.getAgentId() : "Agent");
        Component chatComponent = Component.literal("<" + name + "> " + msg);

        MinecraftServer server = agent.getServer();
        boolean hasServer = server != null && server.getPlayerList() != null;
        if (hasServer) {
            if (intent.getTargetChannel() == ChatChannel.CHAT_CHANNEL_CLAN_BROADCAST) {
                Component clanComponent = Component.literal("[" + agent.getClanId() + "] <" + name + "> " + msg);
                server.getPlayerList().broadcastSystemMessage(clanComponent, false);
            } else {
                // Local broadcast: deliver to human players within earshot (16 blocks)
                double playerRangeSq = 16.0 * 16.0;
                for (ServerPlayer player : server.getPlayerList().getPlayers()) {
                    if (player.level() == agent.level() && player.distanceToSqr(agent) <= playerRangeSq) {
                        player.sendSystemMessage(chatComponent);
                    }
                }
            }
        }

        // Acoustic horizon: deliver ChatEvent to nearby peer AgentEntity instances within 20m
        double agentRangeSq = 20.0 * 20.0;
        UUID speakerUuid = agent.getUUID();
        if (speakerUuid == null && agent.getGameProfile() != null) {
            speakerUuid = agent.getGameProfile().getId();
        }
        String senderUuidStr = speakerUuid != null ? speakerUuid.toString() : "";

        List<AgentEntity> peerAgents = new ArrayList<>();
        Set<UUID> seenUuids = new HashSet<>();
        Set<AgentEntity> seenEntities = Collections.newSetFromMap(new IdentityHashMap<>());

        if (agent.level() != null && agent.getBoundingBox() != null) {
            List<AgentEntity> nearbyAgents = agent.level().getEntitiesOfClass(
                    AgentEntity.class,
                    agent.getBoundingBox().inflate(20.0),
                    e -> e != agent && e.distanceToSqr(agent) <= agentRangeSq
            );
            for (AgentEntity peer : nearbyAgents) {
                if (peer == null || peer == agent) {
                    continue;
                }
                UUID peerUuid = peer.getUUID();
                if (speakerUuid != null && speakerUuid.equals(peerUuid)) {
                    continue;
                }
                if (seenEntities.add(peer) && (peerUuid == null || seenUuids.add(peerUuid))) {
                    peerAgents.add(peer);
                }
            }
        }

        for (BatchSession session : sessions.values()) {
            AgentEntity peer = session.agent;
            if (peer == null || peer == agent) {
                continue;
            }
            if (agent.level() != null && peer.level() != null && peer.level() != agent.level()) {
                continue;
            }
            UUID peerUuid = peer.getUUID();
            if (speakerUuid != null && speakerUuid.equals(peerUuid)) {
                continue;
            }
            if (peer.distanceToSqr(agent) <= agentRangeSq) {
                if (seenEntities.add(peer) && (peerUuid == null || seenUuids.add(peerUuid))) {
                    peerAgents.add(peer);
                }
            }
        }

        for (AgentEntity peer : peerAgents) {
            float distance = (float) Math.sqrt(peer.distanceToSqr(agent));
            ChatEvent chatEvent = ChatEvent.newBuilder()
                    .setSenderUuid(senderUuidStr)
                    .setSenderName(name)
                    .setMessage(msg)
                    .setDistance(distance)
                    .build();
            peer.queueIncomingChat(chatEvent);
        }

        if (hasServer || agent.level() != null || !peerAgents.isEmpty()) {
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
                return actuateSleepInBed(agent, pos);
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

                int slot = -1;
                if (!agent.isCreative()) {
                    slot = findMatchingBlockItemSlot(agent, blockState, intent.getBlockOrItemId());
                    if (slot < 0) {
                        return false;
                    }
                    ItemStack chosen = agent.getInventory().getItem(slot);
                    if (chosen.getItem() instanceof BlockItem chosenBi) {
                        if (chosenBi.getBlock() != blockState.getBlock()) {
                            BlockState newState = chosenBi.getBlock().defaultBlockState();
                            var stateDef = chosenBi.getBlock().getStateDefinition();
                            for (var prop : blockState.getProperties()) {
                                var matchingProp = stateDef.getProperty(prop.getName());
                                if (matchingProp != null) {
                                    newState = applyBlockProperty(newState, matchingProp, blockState.getValue(prop).toString().toLowerCase());
                                }
                            }
                            blockState = newState;
                        }
                    }
                }

                // Verify that solid block placement does not collide with the agent's bounding box
                // or any other player/living entity's bounding box to prevent entombment or suffocation.
                AABB blockBox = new AABB(pos);
                boolean isSolid = blockState.blocksMotion() || !blockState.getCollisionShape(level, pos).isEmpty();
                if (isSolid) {
                    if (agent.getBoundingBox() != null && agent.getBoundingBox().intersects(blockBox)) {
                        return false;
                    }
                    List<Entity> colliding = level.getEntities(agent, blockBox,
                            e -> e != null && (e instanceof Player || (e.isAlive() && !e.isSpectator())));
                    if (colliding != null) {
                        for (Entity entity : colliding) {
                            if (entity.getBoundingBox() != null && entity.getBoundingBox().intersects(blockBox)) {
                                return false;
                            }
                        }
                    }
                }

                if (!agent.isCreative() && slot >= 0) {
                    ItemStack chosen = agent.getInventory().getItem(slot);
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
        // 0. Taxonomy tag match (#minecraft:planks, #minecraft:stairs, #c:stones, etc.)
        String req = requestedId != null ? requestedId.trim().toLowerCase() : "";
        if (req.startsWith("#")) {
            String tagId = req.substring(1);
            if (tagId.contains("[")) {
                tagId = tagId.substring(0, tagId.indexOf('['));
            }
            if (!tagId.contains(":")) {
                tagId = "minecraft:" + tagId;
            }
            ResourceLocation rl = ResourceLocation.tryParse(tagId);
            if (rl != null) {
                TagKey<Item> itemTag = TagKey.create(Registries.ITEM, rl);
                TagKey<Block> blockTag = TagKey.create(Registries.BLOCK, rl);
                for (int i = 0; i < inv.getContainerSize(); i++) {
                    ItemStack item = inv.getItem(i);
                    if (item.isEmpty() || !(item.getItem() instanceof BlockItem bi)) {
                        continue;
                    }
                    if (item.is(itemTag) || bi.getBlock().defaultBlockState().is(blockTag)) {
                        return i;
                    }
                }
            }
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

    @SuppressWarnings("unchecked")
    private static <T extends Comparable<T>> BlockState applyBlockProperty(BlockState state, net.minecraft.world.level.block.state.properties.Property<T> prop, String valStr) {
        java.util.Optional<T> val = prop.getValue(valStr);
        return val.map(t -> state.setValue(prop, t)).orElse(state);
    }

    protected BlockState resolveBlockState(AgentEntity agent, String blockOrItemId) {
        if (blockOrItemId != null && !blockOrItemId.isBlank()) {
            String trimmed = blockOrItemId.trim();
            String baseId = trimmed;
            String propString = null;
            int bracketStart = trimmed.indexOf('[');
            int bracketEnd = trimmed.lastIndexOf(']');
            if (bracketStart != -1 && bracketEnd > bracketStart) {
                baseId = trimmed.substring(0, bracketStart).trim();
                propString = trimmed.substring(bracketStart + 1, bracketEnd).trim();
            }

            // NeoForge / Minecraft taxonomy tag resolution (#tag)
            if (baseId.startsWith("#")) {
                if (agent != null) {
                    int slot = findMatchingBlockItemSlot(agent, null, trimmed);
                    if (slot >= 0) {
                        ItemStack chosen = agent.getInventory().getItem(slot);
                        if (chosen.getItem() instanceof BlockItem bi) {
                            String concreteId = BuiltInRegistries.BLOCK.getKey(bi.getBlock()).toString();
                            if (propString != null && !propString.isEmpty()) {
                                concreteId += "[" + propString + "]";
                            }
                            return resolveBlockState(agent, concreteId);
                        }
                    }
                }
                String tag = baseId.toLowerCase();
                String fallbackId = switch (tag) {
                    case "#minecraft:stairs", "#stairs" -> "minecraft:oak_stairs";
                    case "#minecraft:logs", "#minecraft:wood", "#logs", "#wood" -> "minecraft:oak_log";
                    case "#minecraft:doors", "#doors" -> "minecraft:oak_door";
                    case "#minecraft:trapdoors", "#trapdoors" -> "minecraft:oak_trapdoor";
                    case "#minecraft:slabs", "#slabs" -> "minecraft:oak_slab";
                    case "#minecraft:fence_gates", "#fence_gates" -> "minecraft:oak_fence_gate";
                    case "#c:stones", "#minecraft:base_stone_overworld", "#stones", "#c:cobblestones" -> "minecraft:cobblestone";
                    default -> "minecraft:oak_planks";
                };
                if (propString != null && !propString.isEmpty()) {
                    fallbackId += "[" + propString + "]";
                }
                return resolveBlockState(agent, fallbackId);
            }

            ResourceLocation id = ResourceLocation.tryParse(baseId);
            if (id != null) {
                Block block = null;
                if (BuiltInRegistries.BLOCK.containsKey(id)) {
                    block = BuiltInRegistries.BLOCK.get(id);
                } else if (BuiltInRegistries.ITEM.containsKey(id)) {
                    Item item = BuiltInRegistries.ITEM.get(id);
                    if (item instanceof BlockItem bi) {
                        block = bi.getBlock();
                    }
                }

                if (block != null) {
                    BlockState state = block.defaultBlockState();
                    if (propString != null && !propString.isEmpty()) {
                        var stateDef = block.getStateDefinition();
                        String[] pairs = propString.split(",");
                        for (String pair : pairs) {
                            String[] kv = pair.split("=", 2);
                            if (kv.length == 2) {
                                String propName = kv[0].trim();
                                String propVal = kv[1].trim();
                                var prop = stateDef.getProperty(propName);
                                if (prop != null) {
                                    state = applyBlockProperty(state, prop, propVal);
                                }
                            }
                        }
                    }
                    return state;
                }
            }
        }
        if (agent != null) {
            ItemStack mainHand = agent.getMainHandItem();
            if (mainHand != null && !mainHand.isEmpty() && mainHand.getItem() instanceof BlockItem bi) {
                return bi.getBlock().defaultBlockState();
            }
        }
        return Blocks.COBBLESTONE != null ? Blocks.COBBLESTONE.defaultBlockState() : null;
    }

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

    /**
     * Resolves the authoritative {@link RecipeManager} from the agent's server or level,
     * falling back to any test-injected manager when server and level are absent.
     */
    protected RecipeManager resolveRecipeManager(AgentEntity agent) {
        if (agent != null) {
            MinecraftServer server = agent.getServer();
            if (server != null && server.getRecipeManager() != null) {
                return server.getRecipeManager();
            }
            Level level = agent.level();
            if (level != null && level.getRecipeManager() != null) {
                return level.getRecipeManager();
            }
        }
        return fallbackRecipeManager;
    }

    /**
     * Resolves registry lookup access for recipe result assembly, falling back to built-in registries
     * in headless mock tests.
     */
    protected HolderLookup.Provider resolveRegistryAccess(AgentEntity agent) {
        if (agent != null) {
            MinecraftServer server = agent.getServer();
            if (server != null && server.registryAccess() != null) {
                return server.registryAccess();
            }
            Level level = agent.level();
            if (level != null && level.registryAccess() != null) {
                return level.registryAccess();
            }
        }
        return RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);
    }

    /**
     * Executes a data-driven crafting intent against Minecraft's {@link RecipeManager} (ADR-029).
     * Enforces 2x2 pocket vs. 3x3 crafting table dimension rules, atomic ingredient consumption,
     * remainder container item preservation, and automatic gear equipping.
     */
    public boolean actuateCraftItem(AgentEntity agent, com.civquest.mod.grpc.v1.CraftItemIntent intent) {
        if (agent == null || intent == null || intent.getRecipeId() == null || intent.getRecipeId().isBlank()) {
            return false;
        }
        Inventory inv = agent.getInventory();
        if (inv == null) {
            return false;
        }

        ResourceLocation recipeLoc = ResourceLocation.tryParse(intent.getRecipeId().trim().toLowerCase(Locale.ROOT));
        if (recipeLoc == null) {
            LOGGER.debug("Invalid recipe ResourceLocation '{}'", intent.getRecipeId());
            return false;
        }

        RecipeManager recipeManager = resolveRecipeManager(agent);
        if (recipeManager == null) {
            LOGGER.debug("No RecipeManager available to resolve recipe '{}'", recipeLoc);
            return false;
        }

        Optional<RecipeHolder<?>> holderOpt = recipeManager.byKey(recipeLoc);
        if (holderOpt.isEmpty() || !(holderOpt.get().value() instanceof CraftingRecipe craftingRecipe)) {
            LOGGER.debug("Recipe '{}' not found in RecipeManager or is not a CraftingRecipe", recipeLoc);
            return false;
        }

        NonNullList<Ingredient> ingredients = craftingRecipe.getIngredients();
        if (ingredients.isEmpty() || ingredients.stream().allMatch(Ingredient::isEmpty)) {
            LOGGER.debug("Recipe '{}' has no static ingredients and cannot be crafted directly", recipeLoc);
            return false;
        }

        if (!craftingRecipe.canCraftInDimensions(2, 2) && !isCraftingTableWithinReach(agent, intent)) {
            LOGGER.debug("Recipe '{}' requires 3x3 workstation within reach", recipeLoc);
            return false;
        }

        HolderLookup.Provider registryAccess = resolveRegistryAccess(agent);
        Level level = agent.level();
        int requestedBatches = Math.max(1, intent.getBatchCount());
        int crafted = 0;

        for (int b = 0; b < requestedBatches; b++) {
            if (!craftSingleBatch(agent, inv, craftingRecipe, ingredients, level, registryAccess)) {
                break;
            }
            crafted++;
        }

        if (crafted > 0) {
            agent.autoEquipBestGear();
            return true;
        }
        return false;
    }

    private static boolean craftSingleBatch(
            AgentEntity agent,
            Inventory inv,
            CraftingRecipe craftingRecipe,
            NonNullList<Ingredient> ingredients,
            Level level,
            HolderLookup.Provider registryAccess) {
        int slotLimit = (inv.items != null && !inv.items.isEmpty())
                ? Math.min(inv.items.size(), inv.getContainerSize())
                : inv.getContainerSize();

        int[] remainingInSlot = new int[slotLimit];
        for (int i = 0; i < slotLimit; i++) {
            ItemStack stack = inv.getItem(i);
            remainingInSlot[i] = (stack != null && !stack.isEmpty()) ? stack.getCount() : 0;
        }

        int[] plannedSlots = matchInventorySlots(inv, slotLimit, remainingInSlot, craftingRecipe, ingredients);
        if (plannedSlots == null) {
            return false;
        }

        List<ItemStack> inputStacks = new ArrayList<>(ingredients.size());
        for (int k = 0; k < ingredients.size(); k++) {
            int slot = plannedSlots[k];
            inputStacks.add(slot >= 0 ? inv.getItem(slot).copyWithCount(1) : ItemStack.EMPTY);
        }

        CraftingInput craftingInput = buildCraftingInput(craftingRecipe, inputStacks);
        if (craftingInput.isEmpty()) {
            return false;
        }
        if ((level != null || craftingRecipe instanceof ShapedRecipe || craftingRecipe instanceof ShapelessRecipe)
                && !craftingRecipe.matches(craftingInput, level)) {
            return false;
        }

        ItemStack assembled = craftingRecipe.assemble(craftingInput, registryAccess);
        if (assembled == null || assembled.isEmpty()) {
            ItemStack fallbackResult = craftingRecipe.getResultItem(registryAccess);
            if (fallbackResult == null || fallbackResult.isEmpty()) {
                return false;
            }
            assembled = fallbackResult.copy();
        }

        NonNullList<ItemStack> remainingItems;
        net.neoforged.neoforge.common.CommonHooks.setCraftingPlayer(agent);
        try {
            remainingItems = craftingRecipe.getRemainingItems(craftingInput);
        } finally {
            net.neoforged.neoforge.common.CommonHooks.setCraftingPlayer(null);
        }

        for (int slot : plannedSlots) {
            if (slot >= 0) {
                ItemStack slotStack = inv.getItem(slot);
                slotStack.shrink(1);
                if (slotStack.isEmpty()) {
                    inv.setItem(slot, ItemStack.EMPTY);
                }
            }
        }
        inv.setChanged();

        addOrDropItem(agent, inv, assembled);
        if (remainingItems != null) {
            for (ItemStack rem : remainingItems) {
                if (rem != null && !rem.isEmpty()) {
                    addOrDropItem(agent, inv, rem);
                }
            }
        }
        return true;
    }

    private static int[] matchInventorySlots(
            Inventory inv,
            int slotLimit,
            int[] remainingInSlot,
            CraftingRecipe craftingRecipe,
            NonNullList<Ingredient> ingredients) {
        boolean allSimple = true;
        for (Ingredient ing : ingredients) {
            if (!ing.isEmpty() && !ing.isSimple()) {
                allSimple = false;
                break;
            }
        }

        if (allSimple) {
            StackedContents stacked = new StackedContents();
            for (int i = 0; i < slotLimit; i++) {
                ItemStack s = inv.getItem(i);
                if (s != null && !s.isEmpty() && remainingInSlot[i] > 0) {
                    stacked.accountStack(s, Integer.MAX_VALUE);
                }
            }
            IntList pickedStackingIds = new IntArrayList();
            if (!stacked.canCraft(craftingRecipe, pickedStackingIds) || pickedStackingIds.size() != ingredients.size()) {
                return null;
            }

            int[] plannedSlots = new int[ingredients.size()];
            int[] availableCopy = Arrays.copyOf(remainingInSlot, slotLimit);
            for (int k = 0; k < ingredients.size(); k++) {
                Ingredient ing = ingredients.get(k);
                if (ing.isEmpty()) {
                    plannedSlots[k] = -1;
                    continue;
                }
                int targetId = pickedStackingIds.getInt(k);
                int chosenSlot = -1;
                for (int s = 0; s < slotLimit; s++) {
                    if (availableCopy[s] <= 0) {
                        continue;
                    }
                    ItemStack stack = inv.getItem(s);
                    if (stack != null && !stack.isEmpty()
                            && StackedContents.getStackingIndex(stack) == targetId
                            && ing.test(stack)) {
                        chosenSlot = s;
                        break;
                    }
                }
                if (chosenSlot < 0) {
                    return null;
                }
                availableCopy[chosenSlot]--;
                plannedSlots[k] = chosenSlot;
            }
            return plannedSlots;
        }

        // Exact backtracking fallback for recipes containing NeoForge custom ingredients
        for (Ingredient ing : ingredients) {
            if (ing.isEmpty()) {
                continue;
            }
            boolean anySlotMatches = false;
            for (int s = 0; s < slotLimit; s++) {
                if (remainingInSlot[s] > 0 && ing.test(inv.getItem(s))) {
                    anySlotMatches = true;
                    break;
                }
            }
            if (!anySlotMatches) {
                return null;
            }
        }

        int[] plannedSlots = new int[ingredients.size()];
        Arrays.fill(plannedSlots, -1);
        int[] availableCopy = Arrays.copyOf(remainingInSlot, slotLimit);
        return matchCustomIngredientsBacktrack(inv, slotLimit, availableCopy, ingredients, 0, plannedSlots)
                ? plannedSlots
                : null;
    }

    private static boolean matchCustomIngredientsBacktrack(
            Inventory inv,
            int slotLimit,
            int[] available,
            NonNullList<Ingredient> ingredients,
            int idx,
            int[] plannedSlots) {
        if (idx >= ingredients.size()) {
            return true;
        }
        Ingredient ing = ingredients.get(idx);
        if (ing.isEmpty()) {
            plannedSlots[idx] = -1;
            return matchCustomIngredientsBacktrack(inv, slotLimit, available, ingredients, idx + 1, plannedSlots);
        }

        List<ItemStack> triedVariants = new ArrayList<>();
        for (int s = 0; s < slotLimit; s++) {
            if (available[s] <= 0) {
                continue;
            }
            ItemStack stack = inv.getItem(s);
            if (stack == null || stack.isEmpty() || !ing.test(stack)) {
                continue;
            }
            boolean alreadyTried = false;
            for (ItemStack prev : triedVariants) {
                if (ItemStack.isSameItemSameComponents(prev, stack)) {
                    alreadyTried = true;
                    break;
                }
            }
            if (alreadyTried) {
                continue;
            }
            triedVariants.add(stack);

            available[s]--;
            plannedSlots[idx] = s;
            if (matchCustomIngredientsBacktrack(inv, slotLimit, available, ingredients, idx + 1, plannedSlots)) {
                return true;
            }
            available[s]++;
            plannedSlots[idx] = -1;
        }
        return false;
    }

    private static CraftingInput buildCraftingInput(CraftingRecipe recipe, List<ItemStack> inputStacks) {
        if (recipe instanceof ShapedRecipe shaped) {
            int w = shaped.getWidth();
            int h = shaped.getHeight();
            if (w > 0 && h > 0 && inputStacks.size() == w * h) {
                return CraftingInput.of(w, h, inputStacks);
            }
        }
        int count = inputStacks.size();
        if (count <= 0) {
            return CraftingInput.EMPTY;
        }
        if (count == 1) {
            return CraftingInput.of(1, 1, inputStacks);
        }
        int width = count <= 4 ? 2 : 3;
        int height = (count + width - 1) / width;
        List<ItemStack> padded = new ArrayList<>(width * height);
        padded.addAll(inputStacks);
        while (padded.size() < width * height) {
            padded.add(ItemStack.EMPTY);
        }
        return CraftingInput.of(width, height, padded);
    }

    private static void addOrDropItem(AgentEntity agent, Inventory inv, ItemStack stack) {
        if (stack == null || stack.isEmpty()) {
            return;
        }
        ItemStack toInsert = stack.copy();
        inv.add(toInsert);
        if (!toInsert.isEmpty() && agent.level() != null && !agent.level().isClientSide()) {
            agent.drop(toInsert, false);
        }
    }

    static final double CRAFTING_TABLE_REACH_SQ = 20.25; // 4.5 blocks squared

    boolean isCraftingTableWithinReach(AgentEntity agent, com.civquest.mod.grpc.v1.CraftItemIntent intent) {
        if (agent == null || agent.level() == null) {
            return false;
        }
        if (intent != null && intent.hasWorkstationPos()) {
            com.civquest.mod.grpc.v1.BlockPosProto wp = intent.getWorkstationPos();
            BlockPos targetPos = new BlockPos(wp.getX(), wp.getY(), wp.getZ());
            if (agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY() + 0.5, targetPos.getZ() + 0.5) <= CRAFTING_TABLE_REACH_SQ) {
                if (isCraftingTableBlock(agent.level().getBlockState(targetPos))) {
                    return true;
                }
            }
        }
        BlockPos center = agent.blockPosition();
        if (center == null) {
            return false;
        }
        int reach = 5;
        for (int dx = -reach; dx <= reach; dx++) {
            for (int dy = -reach; dy <= reach; dy++) {
                for (int dz = -reach; dz <= reach; dz++) {
                    BlockPos p = center.offset(dx, dy, dz);
                    if (agent.distanceToSqr(p.getX() + 0.5, p.getY() + 0.5, p.getZ() + 0.5) <= CRAFTING_TABLE_REACH_SQ) {
                        if (isCraftingTableBlock(agent.level().getBlockState(p))) {
                            return true;
                        }
                    }
                }
            }
        }
        return false;
    }

    static boolean isCraftingTableBlock(BlockState bs) {
        if (bs == null) {
            return false;
        }
        try {
            if (bs.is(Blocks.CRAFTING_TABLE)) {
                return true;
            }
        } catch (Throwable ignored) {}
        try {
            if (bs.toString().contains("crafting_table")) {
                return true;
            }
        } catch (Throwable ignored) {}
        try {
            if (bs.getBlock() != null) {
                ResourceLocation loc = BuiltInRegistries.BLOCK.getKey(bs.getBlock());
                if (loc != null && (loc.getPath().equals("crafting_table") || loc.toString().equals("minecraft:crafting_table"))) {
                    return true;
                }
            }
        } catch (Throwable ignored) {}
        return false;
    }

    private static String sanitizeSignLine(String line) {
        if (line == null) {
            return "";
        }
        return line.length() > 80 ? line.substring(0, 80) : line;
    }
}
