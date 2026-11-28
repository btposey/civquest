package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.intent.AttackHandler;
import com.civquest.mod.actuator.intent.BookHandler;
import com.civquest.mod.actuator.intent.BuildHandler;
import com.civquest.mod.actuator.intent.ChatHandler;
import com.civquest.mod.actuator.intent.CraftHandler;
import com.civquest.mod.actuator.intent.IntentHandler;
import com.civquest.mod.actuator.intent.InteractHandler;
import com.civquest.mod.actuator.intent.NavigateHandler;
import com.civquest.mod.actuator.intent.SignpostHandler;
import com.civquest.mod.actuator.intent.SleepHandler;
import com.civquest.mod.actuator.intent.SurveyHandler;
import com.civquest.mod.actuator.intent.TradeHandler;
import com.civquest.mod.actuator.intent.UseItemHandler;
import com.civquest.mod.actuator.motor.Act;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.Frame;
import com.civquest.mod.actuator.motor.Lease;
import com.civquest.mod.actuator.motor.Motor;
import com.civquest.mod.actuator.motor.Sleep;
import com.civquest.mod.actuator.motor.UseItem;
import com.civquest.mod.actuator.motor.Move;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AgentIntent.ActionCase;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.item.crafting.RecipeManager;
import org.slf4j.Logger;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.EnumMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Thin dispatcher from {@link IntentBatch}es to the agent's motor stack (ADR-036, cq-2sr.16). Each intent is
 * turned into a {@link Motor} by the {@link IntentHandler} registered for its {@code ActionCase}; a batch is
 * one frame per intent, pushed in order under the batch's {@code frame_id}. There is no cursor: the
 * {@link AgentMotorFSM} pops the rest of a batch when one of its frames fails or times out and emits the one
 * receipt. This class owns only admission (flush, wake preemption, dedupe, coalescing) and the FIFO of batches
 * waiting for the stack to drain.
 */
public class IntentActuator {
    private static final Logger LOGGER = LogUtils.getLogger();

    /** Per-agent queue of batches waiting for the in-flight batch to finish. */
    private static final class Inbox {
        volatile AgentEntity agent;
        final Deque<IntentBatch> pending = new ArrayDeque<>();
    }

    private final ConcurrentHashMap<String, Inbox> inboxes = new ConcurrentHashMap<>();
    private final Map<ActionCase, IntentHandler> table = new EnumMap<>(ActionCase.class);
    private final ChatHandler chat = new ChatHandler(this::knownAgents);
    private final AttackHandler attack = new AttackHandler();
    private final SleepHandler sleep = new SleepHandler();
    private final UseItemHandler useItem = new UseItemHandler();
    private final BookHandler book = new BookHandler();
    private final SignpostHandler signpost = new SignpostHandler();
    private final InteractHandler interact;
    private final CraftHandler craft;

    public IntentActuator() {
        this(null);
    }

    /**
     * Constructs an IntentActuator with an optional fallback {@link RecipeManager} for mock-driven unit tests
     * where {@code agent.getServer()} and {@code agent.level()} are null.
     */
    public IntentActuator(RecipeManager fallbackRecipeManager) {
        this.interact = new InteractHandler(sleep, this::resolveBlockState);
        this.craft = new CraftHandler(fallbackRecipeManager);
        table.put(ActionCase.NAVIGATE, new NavigateHandler());
        table.put(ActionCase.ATTACK, attack);
        table.put(ActionCase.INTERACT_BLOCK, interact);
        table.put(ActionCase.USE_ITEM, useItem);
        table.put(ActionCase.CRAFT_ITEM, craft);
        table.put(ActionCase.CHAT, chat);
        table.put(ActionCase.TRADE_RESPONSE, new TradeHandler(chat));
        table.put(ActionCase.BUILD_BLOCK, new BuildHandler(interact));
        table.put(ActionCase.CREATE_BOOK, book);
        table.put(ActionCase.PLACE_SIGNPOST, signpost);
        table.put(ActionCase.SURVEY_HORIZON, new SurveyHandler());
    }

    private static String resolveAgentKey(AgentEntity agent) {
        String id = agent.getAgentId();
        return (id != null && !id.isBlank()) ? id : ("entity@" + System.identityHashCode(agent));
    }

    private List<AgentEntity> knownAgents() {
        return inboxes.values().stream().map(i -> i.agent).filter(java.util.Objects::nonNull).toList();
    }

    /** Builds the motor for one intent via the dispatch table; unsupported intents become a failing instant. */
    private Motor motorFor(AgentEntity agent, AgentIntent intent) {
        IntentHandler handler = table.get(intent.getActionCase());
        if (handler == null) {
            LOGGER.debug("Unhandled or unsupported intent action: {}", intent.getActionCase());
            return new Act(intent.getActionCase().name(), a -> false);
        }
        return handler.create(agent, intent);
    }

    /** Submits a prioritized {@link IntentBatch} vector for sequential execution by the agent. */
    public void submitBatch(AgentEntity agent, IntentBatch batch) {
        if (agent == null || batch == null) {
            return;
        }
        if (agent.getIntentActuator() == null) {
            agent.setIntentActuator(this);
        }
        Inbox inbox = inboxes.computeIfAbsent(resolveAgentKey(agent), k -> new Inbox());
        inbox.agent = agent;
        AgentMotorFSM fsm = agent.getMotorFSM();
        if (fsm == null) {
            LOGGER.debug("Agent '{}' has no motor FSM; dropping batch {}", agent.getAgentId(), batch.getBatchId());
            return;
        }

        // Out-of-band chat: executed immediately so chat never queues behind a stalled navigate.
        List<AgentIntent> motorIntents = new ArrayList<>();
        boolean hasChat = false;
        for (AgentIntent intent : batch.getIntentsList()) {
            if (intent.hasChat()) {
                hasChat = true;
                chat.actuateSendChat(agent, intent.getChat());
            } else {
                motorIntents.add(intent);
            }
        }

        synchronized (inbox) {
            if (isCombatOrder(motorIntents)) {
                actuateCombatOrder(agent, fsm, inbox, batch, motorIntents);
                return;
            }
            if (hasChat && motorIntents.isEmpty()) {
                if (!fsm.hasBatchFrames()) {
                    complete(fsm, batch, batch.getIntentsCount());
                }
                return;
            }
            IntentBatch queued = hasChat ? batch.toBuilder().clearIntents().addAllIntents(motorIntents).build() : batch;
            // Stream handshake / keepalive message with no flush and no intents: bind stream only.
            if (!queued.getFlushPeriphery() && queued.getIntentsCount() == 0 && queued.getBatchId() == 0L) {
                return;
            }
            if (queued.getFlushPeriphery() || isWakeUpOrSleepPreempt(agent, fsm, queued)) {
                inbox.pending.clear();
                fsm.flush();
                if (queued.getIntentsCount() == 0) {
                    complete(fsm, queued, 0);
                    return;
                }
            } else if (fsm.hasBatchFrames()) {
                admitInFlight(agent, fsm, inbox, queued);
                return;
            } else if (queued.getIntentsCount() == 0) {
                complete(fsm, queued, 0);
                return;
            }
            inbox.pending.addLast(queued);
            pump(agent, fsm, inbox);
        }
    }

    /** An attack together with a walk: fight on the move. */
    private static boolean isCombatOrder(List<AgentIntent> intents) {
        return intents.stream().anyMatch(AgentIntent::hasAttack) && intents.stream().anyMatch(AgentIntent::hasNavigate);
    }

    /**
     * A combat order is carried out at once: the blows land now and the ordered walk replaces whatever
     * the agent was doing. Queued behind a strike's three-second engagement, flights started late and
     * new ones waited behind it, so agents barely moved while being attacked (cq-6l3.19). The ordered
     * walk owns movement, so the attack's own flight or kiting is switched off.
     */
    private void actuateCombatOrder(AgentEntity agent, AgentMotorFSM fsm, Inbox inbox, IntentBatch batch,
                                    List<AgentIntent> intents) {
        List<AgentIntent> rest = new ArrayList<>();
        AgentIntent walk = null;
        for (AgentIntent intent : intents) {
            if (intent.hasNavigate()) {
                walk = intent;
            }
            if (!intent.hasAttack()) {
                rest.add(intent);
            }
        }
        for (AgentIntent intent : intents) {
            if (intent.hasAttack()) {
                fsm.markSequenceProcessed(intent.getSequenceId());
                attack.actuateAttack(agent, intent.getAttack().toBuilder()
                        .setFleeImmediately(false).setKeepDistanceKite(false).build());
            }
        }
        IntentBatch remaining = batch.toBuilder().clearIntents().addAllIntents(rest).build();
        if (walk != null && isAlreadyWalkingTo(fsm, walk)) {
            complete(fsm, batch, batch.getIntentsCount()); // the walk under way carries on
            return;
        }
        inbox.pending.clear();
        fsm.flush();
        inbox.pending.addLast(remaining);
        pump(agent, fsm, inbox);
    }

    private static boolean isAlreadyWalkingTo(AgentMotorFSM fsm, AgentIntent walk) {
        com.civquest.mod.grpc.v1.Vec3 to = walk.getNavigate().getTarget();
        return fsm.topMotor().filter(m -> m instanceof Move move && move.target() != null
                && Math.abs(move.target().getX() - to.getX()) < 0.5 && Math.abs(move.target().getZ() - to.getZ()) < 0.5)
                .isPresent();
    }

    /**
     * Admission while a batch is in flight: dedupe surveys, coalesce navigates, let a break end a walk, inline
     * pocket crafts, else queue.
     */
    private void admitInFlight(AgentEntity agent, AgentMotorFSM fsm, Inbox inbox, IntentBatch queued) {
        if (allIntents(queued, AgentIntent::hasSurveyHorizon)
                && (fsm.hasSweepFrame() || inbox.pending.stream().anyMatch(b -> anyIntent(b, AgentIntent::hasSurveyHorizon)))) {
            return; // one clean 360-degree sweep is already active or queued
        }
        if (allIntents(queued, AgentIntent::hasNavigate)) {
            inbox.pending.removeIf(b -> allIntents(b, AgentIntent::hasNavigate));
            inbox.pending.addLast(queued);
            return;
        }
        // A break is ordered from where the brain means to stand. Behind a walk still pathing it never
        // started and was never reported, so the walk ends instead (cq-qh7).
        if (allIntents(queued, IntentActuator::isHarvestBreak) && fsm.isOnlyWalking()) {
            inbox.pending.removeIf(b -> allIntents(b, AgentIntent::hasNavigate) || allIntents(b, IntentActuator::isHarvestBreak));
            inbox.pending.addFirst(queued);
            fsm.endWalk("replaced by a break order");
            pump(agent, fsm, inbox);
            return;
        }
        if (fsm.topMotor().filter(m -> m instanceof Sleep).isEmpty() && craft.isPocketCraftOnlyBatch(agent, queued)) {
            for (AgentIntent craftIntent : queued.getIntentsList()) {
                craft.actuateCraftItem(agent, craftIntent.getCraftItem());
            }
            return;
        }
        inbox.pending.addLast(queued);
    }

    private static boolean isHarvestBreak(AgentIntent intent) {
        return intent.hasInteractBlock()
                && intent.getInteractBlock().getInteractionType() == InteractBlockIntent.InteractionType.HARVEST_BREAK;
    }

    private static boolean allIntents(IntentBatch batch, java.util.function.Predicate<AgentIntent> p) {
        return batch != null && batch.getIntentsCount() > 0 && batch.getIntentsList().stream().allMatch(p);
    }

    private static boolean anyIntent(IntentBatch batch, java.util.function.Predicate<AgentIntent> p) {
        return batch != null && batch.getIntentsList().stream().anyMatch(p);
    }

    private static boolean isWakeUpOrSleepPreempt(AgentEntity agent, AgentMotorFSM fsm, IntentBatch batch) {
        if (batch == null || batch.getIntentsCount() == 0) {
            return false;
        }
        boolean sleepFrame = fsm.topMotor().filter(m -> m instanceof Sleep).isPresent();
        if (!sleepFrame && !agent.isSleeping()) {
            return false;
        }
        for (AgentIntent intent : batch.getIntentsList()) {
            if (intent.hasInteractBlock()
                    && intent.getInteractBlock().getInteractionType() == InteractBlockIntent.InteractionType.WAKE_UP) {
                return true;
            }
        }
        return sleepFrame && fsm.canPreempt(batch.getIntents(0));
    }

    private static void complete(AgentMotorFSM fsm, IntentBatch batch, int index) {
        fsm.recordReceipt(batch.getBatchId(), batch.getFrameId(), index, ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null);
    }

    /** Starts queued batches while the stack holds no batch frames; a lease timeout also drops the queue. */
    private void pump(AgentEntity agent, AgentMotorFSM fsm, Inbox inbox) {
        if (fsm.takeTimedOut()) {
            inbox.pending.clear();
        }
        while (!fsm.hasBatchFrames() && !inbox.pending.isEmpty()) {
            start(agent, fsm, inbox.pending.pollFirst());
        }
    }

    /** One frame per intent, pushed in order under the batch's frame_id; the lease is the batch timeout. */
    private void start(AgentEntity agent, AgentMotorFSM fsm, IntentBatch batch) {
        long now = fsm.nowTick();
        long timeout = batch.getTimeoutTicks();
        List<Frame> frames = new ArrayList<>();
        for (int i = 0; i < batch.getIntentsCount(); i++) {
            AgentIntent intent = batch.getIntents(i);
            fsm.markSequenceProcessed(intent.getSequenceId());
            Motor motor = motorFor(agent, intent);
            if (motor != null) { // null: already satisfied (e.g. harvest of a block that is gone)
                frames.add(new Frame(batch.getBatchId(), batch.getFrameId(), i, motor,
                        Lease.ofTicks(now, timeout > 0 ? timeout : motor.defaultLeaseTicks())));
            }
        }
        if (frames.isEmpty()) {
            complete(fsm, batch, batch.getIntentsCount());
        } else if (!fsm.pushBatch(frames, batch.getIntentsCount())) {
            fsm.recordReceipt(batch.getBatchId(), batch.getFrameId(), 0, ReceiptStatus.RECEIPT_STATUS_FAILED,
                    "motor stack capacity exceeded", null);
        }
    }

    /** Clears the agent's queued batches and motor stack, cancelling breaking, navigation, item use and sleep. */
    public void flush(AgentEntity agent) {
        if (agent == null) {
            return;
        }
        Inbox inbox = inboxes.get(resolveAgentKey(agent));
        if (inbox != null) {
            synchronized (inbox) {
                inbox.pending.clear();
            }
        }
        if (agent.getMotorFSM() != null) {
            agent.getMotorFSM().flush();
        }
    }

    /** 20 Hz server tick: starts the next queued batch once the in-flight one has drained. */
    public void tick(AgentEntity agent) {
        if (agent == null || agent.getMotorFSM() == null) {
            return;
        }
        Inbox inbox = inboxes.get(resolveAgentKey(agent));
        if (inbox == null) {
            return;
        }
        synchronized (inbox) {
            pump(agent, agent.getMotorFSM(), inbox);
        }
    }

    /** Returns the current {@link IntentReceipt} for the given agent. */
    public IntentReceipt getCurrentReceipt(AgentEntity agent) {
        if (agent == null || agent.getMotorFSM() == null) {
            return IntentReceipt.getDefaultInstance();
        }
        return agent.getMotorFSM().getCurrentReceipt();
    }

    public int getQueuedBatchCount(AgentEntity agent) {
        Inbox inbox = agent != null ? inboxes.get(resolveAgentKey(agent)) : null;
        if (inbox == null) {
            return 0;
        }
        synchronized (inbox) {
            return inbox.pending.size();
        }
    }

    public void actuate(AgentEntity agent, AgentIntent intent) {
        actuateWithResult(agent, intent);
    }

    public static boolean isAsyncMotorIntent(AgentIntent intent) {
        if (intent == null) {
            return false;
        }
        return switch (intent.getActionCase()) {
            case NAVIGATE, SURVEY_HORIZON, ATTACK, USE_ITEM -> true;
            case INTERACT_BLOCK -> {
                InteractBlockIntent.InteractionType type = intent.getInteractBlock().getInteractionType();
                yield type == InteractBlockIntent.InteractionType.HARVEST_BREAK
                        || type == InteractBlockIntent.InteractionType.SLEEP_IN_BED;
            }
            default -> false;
        };
    }

    /**
     * Direct (non-batch) single-intent path: preemption-checked, one frame outside any batch (no receipt).
     * Emergency use-item over a routine frame is pushed as a reflex.
     *
     * @return false if the intent could not preempt, could not be pushed, or failed as it started
     */
    public boolean actuateWithResult(AgentEntity agent, AgentIntent intent) {
        if (agent == null || intent == null || agent.getMotorFSM() == null) {
            return false;
        }
        AgentMotorFSM fsm = agent.getMotorFSM();
        if (!fsm.canPreempt(intent)) {
            return false;
        }
        fsm.markSequenceProcessed(intent.getSequenceId());
        Motor motor = motorFor(agent, intent);
        if (motor == null) {
            return true;
        }
        if (motor instanceof UseItem routine && fsm.isPriorityOrEmergencyIntent(intent) && fsm.canEmergencyPreempt()) {
            return fsm.pushReflex(new UseItem(routine.itemId(), 0, true, routine.start()), "use_item_reflex_preempt");
        }
        return fsm.pushDirect(motor);
    }

    // ---- thin delegates kept for callers/tests that drive one primitive directly --------------------

    public boolean actuateSendChat(AgentEntity agent, SendChatIntent intent) {
        return chat.actuateSendChat(agent, intent);
    }

    public boolean actuateNavigate(AgentEntity agent, NavigateIntent intent) {
        if (agent == null || intent == null || agent.getNavigator() == null) {
            return false;
        }
        agent.getNavigator().navigate(intent);
        return true;
    }

    public boolean actuateAttack(AgentEntity agent, AttackIntent intent) {
        return attack.actuateAttack(agent, intent);
    }

    public boolean actuateInteractBlock(AgentEntity agent, InteractBlockIntent intent) {
        return interact.actuateInteractBlock(agent, intent);
    }

    public boolean actuateSleepInBed(AgentEntity agent, BlockPos pos) {
        return sleep.actuateSleepInBed(agent, pos);
    }

    public boolean actuateUseItem(AgentEntity agent, UseItemIntent intent) {
        return useItem.actuateUseItem(agent, intent);
    }

    public boolean actuateCreateWrittenBook(AgentEntity agent, CreateWrittenBookIntent intent) {
        return book.actuateCreateWrittenBook(agent, intent);
    }

    public boolean actuatePlaceSignpost(AgentEntity agent, PlaceSignpostIntent intent) {
        return signpost.actuatePlaceSignpost(agent, intent);
    }

    public boolean actuateCraftItem(AgentEntity agent, CraftItemIntent intent) {
        return craft.actuateCraftItem(agent, intent);
    }

    public static boolean isWeaponOrTool(ItemStack stack) {
        return SleepHandler.isWeaponOrTool(stack);
    }

    /** Overridable block-state resolution seam (tests substitute fixed states). */
    protected BlockState resolveBlockState(AgentEntity agent, String blockOrItemId) {
        return interact.resolveBlockState(agent, blockOrItemId);
    }

    boolean isCraftingTableWithinReach(AgentEntity agent, CraftItemIntent intent) {
        return craft.isCraftingTableWithinReach(agent, intent);
    }

    static boolean isCraftingTableBlock(BlockState bs) {
        return CraftHandler.isCraftingTableBlock(bs);
    }
}
