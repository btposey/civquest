package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.ReflexNotice;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.projectile.Projectile;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

import java.util.ArrayDeque;
import java.util.Deque;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Layer 1A In-Engine Motor Actuator Pushdown Automaton (MotorPDA).
 * Governs physical agent action lifecycles, execution preemption matrices,
 * bounded LIFO state suspension/resumption for sub-tick reflexes, and
 * deterministic entry/exit cleanup on the 20 Hz server tick.
 */
public class AgentMotorFSM {
    private static final Logger LOGGER = LogUtils.getLogger();

    public static final int COMBAT_TIMEOUT_TICKS = 60; // 3 seconds of combat disengagement
    public static final int STUCK_RECOVERY_MAX_TICKS = 20; // 1 second stuck recovery attempt
    public static final int MAX_STACK_DEPTH = 4;
    private static final double PROJECTILE_THREAT_RADIUS = 6.0;
    private static final double PROJECTILE_MISS_THRESHOLD_SQ = 4.0; // 2.0 blocks

    private final AgentEntity agent;
    private final Deque<MotorStackFrame> stack = new ArrayDeque<>();
    private volatile MotorState currentState = MotorState.IDLE;
    private volatile MotorState previousState = MotorState.IDLE;
    private volatile long ticksInState = 0L;
    private volatile int combatTicks = 0;
    private volatile int stuckRecoveryTicks = 0;
    private volatile Vec3 lastNavTarget = null;
    private volatile AgentIntent currentIntent = null;

    private volatile long currentBatchId = 0L;
    private volatile int currentBatchIndex = 0;
    private volatile IntentReceipt.ReceiptStatus currentReceiptStatus = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED;
    private volatile String currentFailureReason = "";
    private volatile long lastProcessedSequenceId = 0L;
    private volatile boolean lastAsyncActionSucceeded = true;
    private volatile String lastAsyncFailureReason = "";

    public AgentMotorFSM(AgentEntity agent) {
        this.agent = agent;
    }

    public AgentEntity getAgent() {
        return agent;
    }

    public MotorState getState() {
        return currentState;
    }

    public MotorState getPreviousState() {
        return previousState;
    }

    public long getTicksInState() {
        return ticksInState;
    }

    public Vec3 getLastNavTarget() {
        return lastNavTarget;
    }

    public void setLastNavTarget(Vec3 lastNavTarget) {
        this.lastNavTarget = lastNavTarget;
    }

    public int getCombatTicks() {
        return combatTicks;
    }

    public void setCombatTicks(int combatTicks) {
        this.combatTicks = combatTicks;
    }

    public int getStuckRecoveryTicks() {
        return stuckRecoveryTicks;
    }

    public void setStuckRecoveryTicks(int stuckRecoveryTicks) {
        this.stuckRecoveryTicks = stuckRecoveryTicks;
    }

    public AgentIntent getCurrentIntent() {
        return currentIntent;
    }

    public long getLastProcessedSequenceId() {
        return lastProcessedSequenceId;
    }

    public void markSequenceProcessed(long sequenceId) {
        if (sequenceId > 0) {
            this.lastProcessedSequenceId = sequenceId;
        }
    }

    public boolean didLastAsyncActionSucceed() {
        return lastAsyncActionSucceeded;
    }

    public String getLastAsyncFailureReason() {
        return lastAsyncFailureReason;
    }

    public synchronized int getStackDepth() {
        return stack.size();
    }

    public synchronized Deque<MotorStackFrame> getStackSnapshot() {
        return new ArrayDeque<>(stack);
    }

    public synchronized void updateReceiptState(long batchId,
                                                int activeIndex,
                                                IntentReceipt.ReceiptStatus status,
                                                String failureReason) {
        this.currentBatchId = batchId;
        this.currentBatchIndex = activeIndex;
        this.currentReceiptStatus = status != null
                ? status
                : IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED;
        this.currentFailureReason = failureReason != null ? failureReason : "";
    }

    /**
     * Returns an {@link IntentReceipt} snapshot of the MotorPDA execution state.
     */
    public synchronized IntentReceipt getCurrentReceipt() {
        int depth = stack.size();
        IntentReceipt.ReceiptStatus effectiveStatus = this.currentReceiptStatus;
        if (depth > 0
                && effectiveStatus != IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                && effectiveStatus != IntentReceipt.ReceiptStatus.RECEIPT_STATUS_ABORTED
                && effectiveStatus != IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED) {
            effectiveStatus = IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX;
        }
        return IntentReceipt.newBuilder()
                .setBatchId(this.currentBatchId)
                .setActiveIndex(this.currentBatchIndex)
                .setStatus(effectiveStatus)
                .setFailureReason(this.currentFailureReason != null ? this.currentFailureReason : "")
                .setMotorStackDepth(depth)
                .build();
    }

    /**
     * Pushes the currently executing motor state onto the bounded LIFO stack before
     * switching into a reflex state (e.g., {@link MotorState#COMBAT_ENGAGED}).
     *
     * @return true if the frame was pushed and state transitioned, false if rejected (null/identical/stack full)
     */
    public synchronized boolean pushState(MotorState reflexState, String reason) {
        if (reflexState == null || reflexState == this.currentState) {
            return false;
        }
        if (stack.size() >= MAX_STACK_DEPTH) {
            LOGGER.warn("Agent '{}' MotorPDA stack is at capacity ({}); rejecting pushState({})",
                    agent != null ? agent.getAgentId() : "unknown", MAX_STACK_DEPTH, reflexState);
            return false;
        }

        Map<String, Object> context = new HashMap<>();
        int progressTicks = (int) this.ticksInState;
        double lastProgressValue = 0.0;

        if (this.currentState == MotorState.MINING && agent != null && agent.getBreaker() != null && agent.getBreaker().isBreaking()) {
            BlockBreaker breaker = agent.getBreaker();
            progressTicks = breaker.getTicks();
            lastProgressValue = breaker.getProgress();
            if (breaker.getPos() != null) {
                context.put("breakPos", breaker.getPos().immutable());
            }
        } else if (this.currentState == MotorState.MOVING) {
            if (this.lastNavTarget != null) {
                context.put("lastNavTarget", this.lastNavTarget);
            }
            if (agent != null && agent.getNavigator() != null && agent.getNavigator().getTargetCoords() != null) {
                AgentNavigator nav = agent.getNavigator();
                net.minecraft.world.phys.Vec3 targetVec = nav.getTargetCoords();
                context.put("navTargetVec", targetVec);
                context.put("navSpeedMultiplier", (float) nav.getSpeedMultiplier());
                context.put("navSprint", nav.isSprint());
                context.put("navAllowBreak", nav.isAllowBreak());
                context.put("navAllowPlace", nav.isAllowPlace());
                context.put("navArrivalTolerance", nav.getArrivalTolerance());
                lastProgressValue = nav.getPosition().distanceTo(targetVec);
            }
        }

        if (reason != null) {
            context.put("pushReason", reason);
        }
        context.put("ticksInState", this.ticksInState);

        MotorStackFrame frame = new MotorStackFrame(
                this.currentState,
                this.currentIntent,
                progressTicks,
                lastProgressValue,
                context
        );
        stack.push(frame);
        transitionTo(reflexState, reason != null ? reason : "pda_push");
        return true;
    }

    /**
     * Pops the most recently suspended {@link MotorStackFrame} from the LIFO stack
     * and resumes its execution (e.g., block breaking or pathfinding) with zero amnesia.
     *
     * @return true if a frame was popped and restored, false if the stack was empty
     */
    public synchronized boolean popState() {
        if (stack.isEmpty()) {
            return false;
        }

        MotorStackFrame frame = stack.pop();
        MotorState targetState = frame.getState() != null ? frame.getState() : MotorState.IDLE;

        transitionTo(targetState, "pda_pop_restore");
        this.currentIntent = frame.getIntent();

        Object savedTicks = frame.getContextValue("ticksInState");
        if (savedTicks instanceof Long l) {
            this.ticksInState = l;
        } else {
            this.ticksInState = frame.getProgressTicks();
        }

        if (targetState == MotorState.MINING) {
            Object bp = frame.getContextValue("breakPos");
            if (bp instanceof BlockPos breakPos && agent != null && agent.getBreaker() != null) {
                boolean resumed = agent.getBreaker().resume(
                        breakPos,
                        (float) frame.getLastProgressValue(),
                        frame.getProgressTicks()
                );
                if (!resumed && agent.level() != null) {
                    lastAsyncActionSucceeded = false;
                    lastAsyncFailureReason = "Failed to resume block breaking at " + breakPos;
                    transitionTo(MotorState.IDLE, "resume_mining_failed");
                }
            } else if (frame.getIntent() != null && frame.getIntent().hasInteractBlock()
                    && agent != null && agent.getBreaker() != null) {
                com.civquest.mod.grpc.v1.BlockPosProto protoPos = frame.getIntent().getInteractBlock().getPos();
                BlockPos breakPos = new BlockPos(protoPos.getX(), protoPos.getY(), protoPos.getZ());
                boolean resumed = agent.getBreaker().resume(
                        breakPos,
                        (float) frame.getLastProgressValue(),
                        frame.getProgressTicks()
                );
                if (!resumed && agent.level() != null) {
                    lastAsyncActionSucceeded = false;
                    lastAsyncFailureReason = "Failed to resume block breaking at " + breakPos;
                    transitionTo(MotorState.IDLE, "resume_mining_failed");
                }
            }
        } else if (targetState == MotorState.MOVING) {
            Object lnt = frame.getContextValue("lastNavTarget");
            if (lnt instanceof Vec3 v) {
                this.lastNavTarget = v;
            } else if (frame.getIntent() != null && frame.getIntent().hasNavigate()
                    && frame.getIntent().getNavigate().hasTarget()) {
                this.lastNavTarget = frame.getIntent().getNavigate().getTarget();
            }

            if (agent != null && agent.getNavigator() != null) {
                Object tv = frame.getContextValue("navTargetVec");
                if (tv instanceof net.minecraft.world.phys.Vec3 targetVec) {
                    float speed = frame.getContextValue("navSpeedMultiplier") instanceof Float f ? f : 1.0f;
                    boolean sprint = Boolean.TRUE.equals(frame.getContextValue("navSprint"));
                    boolean allowBreak = Boolean.TRUE.equals(frame.getContextValue("navAllowBreak"));
                    boolean allowPlace = Boolean.TRUE.equals(frame.getContextValue("navAllowPlace"));
                    int tolerance = frame.getContextValue("navArrivalTolerance") instanceof Integer i ? i : 1;
                    agent.getNavigator().navigate(
                            targetVec.x, targetVec.y, targetVec.z,
                            speed, sprint, allowBreak, allowPlace, tolerance
                    );
                } else if (frame.getIntent() != null && frame.getIntent().hasNavigate()) {
                    agent.getNavigator().navigate(frame.getIntent().getNavigate());
                }
            }
        }

        return true;
    }

    /**
     * Immediately flushes the MotorPDA: clears the LIFO stack, cancels active block breaking
     * and navigation, releases active item/sleep state, and resets the FSM to {@link MotorState#IDLE}.
     */
    public synchronized void flush() {
        stack.clear();
        if (agent != null) {
            if (agent.getBreaker() != null) {
                agent.getBreaker().cancel();
            }
            if (agent.getNavigator() != null) {
                agent.getNavigator().stop();
            }
            if (agent.isUsingItem()) {
                agent.stopUsingItem();
            }
            if (agent.isSleeping()) {
                agent.stopSleeping();
            }
        }
        this.previousState = this.currentState;
        this.currentState = MotorState.IDLE;
        this.ticksInState = 0L;
        this.combatTicks = 0;
        this.stuckRecoveryTicks = 0;
        this.lastNavTarget = null;
        this.currentIntent = null;
        this.lastAsyncActionSucceeded = true;
        this.lastAsyncFailureReason = "";
    }

    /**
     * Executes state transition with entry and exit cleanup hooks.
     * Returns true if transition occurred, false if state was already active.
     */
    public synchronized boolean transitionTo(MotorState newState, String reason) {
        if (newState == null || newState == this.currentState) {
            return false;
        }

        MotorState oldState = this.currentState;

        // ---- Exit Hooks ----
        switch (oldState) {
            case MINING -> {
                if (agent != null && agent.getBreaker() != null) {
                    agent.getBreaker().cancel();
                }
            }
            case MOVING -> {
                if (agent != null && agent.getNavigator() != null) {
                    agent.getNavigator().stop();
                }
            }
            case USING_ITEM -> {
                if (agent != null && agent.isUsingItem()) {
                    agent.stopUsingItem();
                }
            }
            case COMBAT_ENGAGED -> {
                combatTicks = 0;
                if (agent != null && agent.isUsingItem() && agent.getUseItem().is(Items.SHIELD)) {
                    agent.stopUsingItem();
                }
            }
            case SLEEPING -> {
                if (agent != null && agent.isSleeping()) {
                    agent.stopSleeping();
                }
            }
            case STUCK_RECOVERY -> {
                stuckRecoveryTicks = 0;
            }
            default -> {
            }
        }

        this.previousState = oldState;
        this.currentState = newState;
        this.ticksInState = 0L;

        // ---- Entry Hooks ----
        switch (newState) {
            case COMBAT_ENGAGED -> combatTicks = 0;
            case STUCK_RECOVERY -> stuckRecoveryTicks = 0;
            case IDLE -> {
                lastNavTarget = null;
                currentIntent = null;
            }
            default -> {
            }
        }

        LOGGER.debug("Agent '{}' MotorFSM transition: {} -> {} (reason: {})",
                agent != null ? agent.getAgentId() : "unknown", oldState, newState, reason);
        return true;
    }

    /**
     * Evaluates whether an incoming intent can preempt the current motor state.
     * Priority/emergency actions immediately interrupt routine mining/building/moving.
     */
    public boolean canPreempt(AgentIntent intent) {
        if (intent == null) {
            return false;
        }

        boolean isEmergency = isPriorityOrEmergencyIntent(intent);

        switch (currentState) {
            case IDLE -> {
                return true;
            }
            case SLEEPING -> {
                // Only emergency combat/panic or explicit wake can preempt sleep
                return isEmergency
                        || (intent.hasInteractBlock() && intent.getInteractBlock().getInteractionType() == InteractBlockIntent.InteractionType.WAKE_UP);
            }
            case COMBAT_ENGAGED -> {
                // Active combat can only be preempted by emergency actions (new attacks, emergency flee/shield, eating)
                return isEmergency;
            }
            case STUCK_RECOVERY -> {
                if (isEmergency) {
                    return true;
                }
                // Prevent repeated identical navigation intents from infinitely resetting STUCK_RECOVERY
                if (intent.hasNavigate()) {
                    NavigateIntent nav = intent.getNavigate();
                    if (isSameNavigationTarget(nav)) {
                        return false;
                    }
                }
                return true;
            }
            case MINING, BUILDING, USING_ITEM -> {
                // Priority / emergency intents immediately preempt routine actions
                // Routine navigation and other intents can also cleanly transition
                return true;
            }
            case MOVING -> {
                // Any valid intent can redirect or preempt movement
                return true;
            }
            default -> {
                return true;
            }
        }
    }

    /**
     * Determines whether an intent represents an emergency/priority action.
     * Routine sprint navigation is NOT treated as an emergency action.
     */
    public boolean isPriorityOrEmergencyIntent(AgentIntent intent) {
        if (intent == null) {
            return false;
        }
        if (intent.hasAttack()) {
            return true;
        }
        if (intent.hasUseItem()) {
            UseItemIntent use = intent.getUseItem();
            return use.getEquipArmorOrOffhand() || use.getIsEatingOrDrinking();
        }
        return false;
    }

    /**
     * Checks if a navigation intent targets the same voxel/location as the active navigation target.
     */
    public boolean isSameNavigationTarget(NavigateIntent nav) {
        if (nav == null || !nav.hasTarget()) {
            return lastNavTarget == null;
        }
        if (lastNavTarget == null) {
            return false;
        }
        double eps = 0.5;
        return Math.abs(nav.getTarget().getX() - lastNavTarget.getX()) < eps
                && Math.abs(nav.getTarget().getY() - lastNavTarget.getY()) < eps
                && Math.abs(nav.getTarget().getZ() - lastNavTarget.getZ()) < eps;
    }

    /**
     * Preempts {@link MotorState#MINING} or {@link MotorState#MOVING} with {@link MotorState#COMBAT_ENGAGED}
     * via the PDA stack when an immediate physical hazard is detected.
     */
    public synchronized boolean preemptWithCombatReflex(String reason) {
        if (currentState != MotorState.MINING && currentState != MotorState.MOVING) {
            if (currentState == MotorState.COMBAT_ENGAGED) {
                combatTicks = 0;
            }
            return false;
        }
        boolean pushed = pushState(MotorState.COMBAT_ENGAGED, reason);
        if (pushed) {
            combatTicks = 0;
            engageReflexShield();
        }
        return pushed;
    }

    private void engageReflexShield() {
        if (agent == null || !agent.isAlive()) {
            return;
        }
        try {
            ItemStack offhand = agent.getItemInHand(InteractionHand.OFF_HAND);
            boolean hasShield = offhand != null && offhand.is(Items.SHIELD);
            if (!hasShield && agent.getInventory() != null) {
                for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                    ItemStack item = agent.getInventory().getItem(i);
                    if (item != null && item.is(Items.SHIELD)) {
                        agent.setItemInHand(InteractionHand.OFF_HAND, item);
                        agent.getInventory().setItem(i, offhand != null ? offhand : ItemStack.EMPTY);
                        hasShield = true;
                        break;
                    }
                }
            }
            if (hasShield) {
                agent.startUsingItem(InteractionHand.OFF_HAND);
                agent.recordReflexNotice(ReflexNotice.newBuilder()
                        .setReflexType(ReflexNotice.ReflexType.REFLEX_TYPE_SHIELD_DEFENSE)
                        .setTick(agent.tickCount)
                        .setDetails("MotorPDA reflex shield defense engaged")
                        .build());
            }
        } catch (Throwable ignored) {
        }
    }

    private boolean checkIncomingProjectileHazard() {
        if (agent == null || agent.level() == null || !agent.isAlive()) {
            return false;
        }
        try {
            List<Projectile> projectiles = agent.level().getEntitiesOfClass(
                    Projectile.class,
                    agent.getBoundingBox().inflate(PROJECTILE_THREAT_RADIUS),
                    p -> p != null && p.isAlive() && p.getOwner() != agent
            );
            if (projectiles == null || projectiles.isEmpty()) {
                return false;
            }
            net.minecraft.world.phys.Vec3 agentCenter = new net.minecraft.world.phys.Vec3(
                    agent.getX(), agent.getY() + 0.9, agent.getZ()
            );
            for (Projectile proj : projectiles) {
                net.minecraft.world.phys.Vec3 vel = proj.getDeltaMovement();
                if (vel == null || vel.lengthSqr() < 0.04) {
                    continue;
                }
                net.minecraft.world.phys.Vec3 toAgent = agentCenter.subtract(proj.position());
                double dot = toAgent.dot(vel);
                if (dot <= 0.0) {
                    continue;
                }
                double t = dot / vel.lengthSqr();
                net.minecraft.world.phys.Vec3 closestPoint = proj.position().add(vel.scale(t));
                if (closestPoint.distanceToSqr(agentCenter) <= PROJECTILE_MISS_THRESHOLD_SQ) {
                    return preemptWithCombatReflex("arrow_trajectory_interception");
                }
            }
        } catch (Throwable ignored) {
        }
        return false;
    }

    /**
     * Routes intent execution through the motor FSM, handling preemption and state transitions.
     */
    public synchronized boolean handleIntent(AgentIntent intent, IntentActuator actuator) {
        if (intent == null || actuator == null) {
            return false;
        }

        if (!canPreempt(intent)) {
            LOGGER.debug("Agent '{}' MotorFSM rejected preemption of state {} by intent {}",
                    agent != null ? agent.getAgentId() : "null", currentState, intent.getActionCase());
            return false;
        }

        if (intent.getSequenceId() > 0) {
            this.lastProcessedSequenceId = intent.getSequenceId();
        }
        this.lastAsyncActionSucceeded = true;
        this.lastAsyncFailureReason = "";

        switch (intent.getActionCase()) {
            case NAVIGATE -> {
                NavigateIntent nav = intent.getNavigate();
                lastNavTarget = nav.hasTarget() ? nav.getTarget() : null;
                transitionTo(MotorState.MOVING, "navigate_intent");
                this.currentIntent = intent;
                if (agent == null) {
                    return true;
                }
                boolean started = actuator.actuateNavigate(agent, nav);
                if (!started || (agent.getNavigator() != null
                        && !agent.getNavigator().isPathing()
                        && !agent.getNavigator().isArrived())) {
                    lastAsyncActionSucceeded = false;
                    lastAsyncFailureReason = "Navigate rejected (invalid target coordinates)";
                    transitionTo(MotorState.IDLE, "navigate_start_failed");
                    return false;
                }
                if (agent.getNavigator() != null && agent.getNavigator().isArrived()) {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    transitionTo(MotorState.IDLE, "navigate_already_arrived");
                }
                return true;
            }
            case ATTACK -> {
                AttackIntent atk = intent.getAttack();
                if (currentState == MotorState.MINING || currentState == MotorState.MOVING) {
                    pushState(MotorState.COMBAT_ENGAGED, "attack_intent_reflex");
                } else {
                    transitionTo(MotorState.COMBAT_ENGAGED, "attack_intent");
                }
                this.currentIntent = intent;
                combatTicks = 0;
                return agent != null ? actuator.actuateAttack(agent, atk) : true;
            }
            case INTERACT_BLOCK -> {
                InteractBlockIntent interact = intent.getInteractBlock();
                switch (interact.getInteractionType()) {
                    case HARVEST_BREAK -> {
                        transitionTo(MotorState.MINING, "harvest_break_intent");
                        this.currentIntent = intent;
                        boolean started = agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                        if (!started && agent != null && (agent.getBreaker() == null || !agent.getBreaker().isBreaking())) {
                            lastAsyncActionSucceeded = false;
                            lastAsyncFailureReason = "HARVEST_BREAK failed (unbreakable, fluid, or out of reach)";
                            transitionTo(MotorState.IDLE, "mining_start_failed");
                        } else if (started && agent != null && (agent.getBreaker() == null || !agent.getBreaker().isBreaking())) {
                            lastAsyncActionSucceeded = true;
                            lastAsyncFailureReason = "";
                            transitionTo(MotorState.IDLE, "mining_instant_finished");
                        }
                        return started;
                    }
                    case SLEEP_IN_BED -> {
                        transitionTo(MotorState.SLEEPING, "sleep_intent");
                        this.currentIntent = intent;
                        boolean sleeping = agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                        if (!sleeping && agent != null && !agent.isSleeping()) {
                            lastAsyncActionSucceeded = false;
                            lastAsyncFailureReason = "SLEEP_IN_BED failed (out of reach, occupied, or daytime)";
                            transitionTo(MotorState.IDLE, "sleep_start_failed");
                        }
                        return sleeping;
                    }
                    case WAKE_UP -> {
                        boolean woke = agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                        transitionTo(MotorState.IDLE, "wake_up_intent");
                        return woke;
                    }
                    case PLACE_BLOCK, PAVE_ROAD_PATH, RING_BELL -> {
                        return agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                    }
                    default -> {
                        return agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                    }
                }
            }
            case BUILD_BLOCK -> {
                BuildBlockIntent build = intent.getBuildBlock();
                return agent != null ? actuator.actuateBuildBlock(agent, build) : true;
            }
            case PLACE_SIGNPOST -> {
                PlaceSignpostIntent sign = intent.getPlaceSignpost();
                return agent != null ? actuator.actuatePlaceSignpost(agent, sign) : true;
            }
            case USE_ITEM -> {
                UseItemIntent use = intent.getUseItem();
                boolean preempted = (currentState == MotorState.MINING || currentState == MotorState.MOVING)
                        && isPriorityOrEmergencyIntent(intent);
                if (preempted) {
                    pushState(MotorState.USING_ITEM, "use_item_reflex_preempt");
                } else {
                    transitionTo(MotorState.USING_ITEM, "use_item_intent");
                }
                this.currentIntent = intent;
                boolean used = agent != null ? actuator.actuateUseItem(agent, use) : true;
                if (agent != null && !agent.isUsingItem()) {
                    if (preempted && !stack.isEmpty()) {
                        popState();
                    } else {
                        transitionTo(MotorState.IDLE, "use_item_complete");
                    }
                }
                return used;
            }
            case CRAFT_ITEM -> {
                return agent != null ? actuator.actuateCraftItem(agent, intent.getCraftItem()) : true;
            }
            case CREATE_BOOK -> {
                return agent != null ? actuator.actuateCreateWrittenBook(agent, intent.getCreateBook()) : true;
            }
            case CHAT -> {
                return agent != null ? actuator.actuateSendChat(agent, intent.getChat()) : true;
            }
            case TRADE_RESPONSE -> {
                return agent != null ? actuator.actuateRespondTrade(agent, intent.getTradeResponse()) : true;
            }
            default -> {
                LOGGER.debug("Unhandled intent action in MotorFSM: {}", intent.getActionCase());
                return false;
            }
        }
    }

    /**
     * Server-thread 20 Hz tick update for active motor state progression.
     */
    public synchronized void tick() {
        ticksInState++;

        if (currentState == MotorState.MINING || currentState == MotorState.MOVING) {
            checkIncomingProjectileHazard();
        }

        switch (currentState) {
            case MINING -> {
                if (agent != null && agent.getBreaker() != null && agent.getBreaker().isBreaking()) {
                    boolean broken = agent.getBreaker().tick();
                    if (broken) {
                        lastAsyncActionSucceeded = true;
                        lastAsyncFailureReason = "";
                        transitionTo(MotorState.IDLE, "mining_finished");
                    } else if (!agent.getBreaker().isBreaking()) {
                        lastAsyncActionSucceeded = false;
                        lastAsyncFailureReason = "Block breaking cancelled or target unreachable";
                        transitionTo(MotorState.IDLE, "mining_cancelled");
                    }
                } else {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    transitionTo(MotorState.IDLE, "mining_inactive");
                }
            }
            case MOVING -> {
                if (agent != null && agent.getNavigator() != null) {
                    agent.getNavigator().tick();
                    if (agent.getNavigator().isArrived()) {
                        lastAsyncActionSucceeded = true;
                        lastAsyncFailureReason = "";
                        transitionTo(MotorState.IDLE, "navigation_arrived");
                    } else if (agent.getNavigator().isStuck()) {
                        transitionTo(MotorState.STUCK_RECOVERY, "navigation_stuck");
                    } else if (agent.getNavigator().isIdle()) {
                        lastAsyncActionSucceeded = false;
                        lastAsyncFailureReason = "Navigation stopped before reaching destination";
                        transitionTo(MotorState.IDLE, "navigation_idle");
                    }
                } else {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    transitionTo(MotorState.IDLE, "navigator_null");
                }
            }
            case COMBAT_ENGAGED -> {
                if (agent != null && agent.getNavigator() != null && agent.getNavigator().isPathing()) {
                    agent.getNavigator().tick();
                }
                combatTicks++;
                if (combatTicks > COMBAT_TIMEOUT_TICKS) {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    if (!stack.isEmpty()) {
                        popState();
                    } else {
                        transitionTo(MotorState.IDLE, "combat_timeout_disengaged");
                    }
                }
            }
            case USING_ITEM -> {
                if (agent != null && !agent.isUsingItem()) {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    if (!stack.isEmpty()) {
                        popState();
                    } else {
                        transitionTo(MotorState.IDLE, "use_item_finished");
                    }
                }
            }
            case SLEEPING -> {
                if (agent != null && !agent.isSleeping()) {
                    lastAsyncActionSucceeded = true;
                    lastAsyncFailureReason = "";
                    transitionTo(MotorState.IDLE, "sleeping_finished");
                }
            }
            case STUCK_RECOVERY -> {
                stuckRecoveryTicks++;
                if (stuckRecoveryTicks > STUCK_RECOVERY_MAX_TICKS) {
                    if (agent != null && agent.getNavigator() != null) {
                        agent.getNavigator().stop();
                    }
                    lastAsyncActionSucceeded = false;
                    lastAsyncFailureReason = "Navigation stuck recovery timeout";
                    if (!stack.isEmpty()) {
                        popState();
                    } else {
                        transitionTo(MotorState.IDLE, "stuck_recovery_timeout");
                    }
                }
            }
            case BUILDING -> {
                transitionTo(MotorState.IDLE, "building_cycle_finished");
            }
            case IDLE -> {
                // Receptive idle
            }
        }
    }
}
