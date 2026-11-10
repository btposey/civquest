package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import com.mojang.logging.LogUtils;
import org.slf4j.Logger;

/**
 * Layer 1A In-Engine Motor Actuator Finite State Machine.
 * Governs physical agent action lifecycles, execution preemption matrices,
 * and deterministic entry/exit cleanup on the 20 Hz server tick.
 */
public class AgentMotorFSM {
    private static final Logger LOGGER = LogUtils.getLogger();

    public static final int COMBAT_TIMEOUT_TICKS = 60; // 3 seconds of combat disengagement
    public static final int STUCK_RECOVERY_MAX_TICKS = 20; // 1 second stuck recovery attempt

    private final AgentEntity agent;
    private volatile MotorState currentState = MotorState.IDLE;
    private volatile MotorState previousState = MotorState.IDLE;
    private volatile long ticksInState = 0L;
    private volatile int combatTicks = 0;
    private volatile int stuckRecoveryTicks = 0;
    private volatile Vec3 lastNavTarget = null;

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
            case IDLE -> lastNavTarget = null;
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
     * Routes intent execution through the motor FSM, handling preemption and state transitions.
     */
    public boolean handleIntent(AgentIntent intent, IntentActuator actuator) {
        if (intent == null || actuator == null) {
            return false;
        }

        if (!canPreempt(intent)) {
            LOGGER.debug("Agent '{}' MotorFSM rejected preemption of state {} by intent {}",
                    agent != null ? agent.getAgentId() : "null", currentState, intent.getActionCase());
            return false;
        }

        switch (intent.getActionCase()) {
            case NAVIGATE -> {
                NavigateIntent nav = intent.getNavigate();
                lastNavTarget = nav.hasTarget() ? nav.getTarget() : null;
                transitionTo(MotorState.MOVING, "navigate_intent");
                return agent != null ? actuator.actuateNavigate(agent, nav) : true;
            }
            case ATTACK -> {
                AttackIntent atk = intent.getAttack();
                transitionTo(MotorState.COMBAT_ENGAGED, "attack_intent");
                combatTicks = 0;
                return agent != null ? actuator.actuateAttack(agent, atk) : true;
            }
            case INTERACT_BLOCK -> {
                InteractBlockIntent interact = intent.getInteractBlock();
                switch (interact.getInteractionType()) {
                    case HARVEST_BREAK -> {
                        transitionTo(MotorState.MINING, "harvest_break_intent");
                        boolean started = agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                        if (!started && agent != null && (agent.getBreaker() == null || !agent.getBreaker().isBreaking())) {
                            transitionTo(MotorState.IDLE, "mining_start_failed");
                        }
                        return started;
                    }
                    case SLEEP_IN_BED -> {
                        transitionTo(MotorState.SLEEPING, "sleep_intent");
                        boolean sleeping = agent != null ? actuator.actuateInteractBlock(agent, interact) : true;
                        if (!sleeping && agent != null && !agent.isSleeping()) {
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
                transitionTo(MotorState.USING_ITEM, "use_item_intent");
                boolean used = agent != null ? actuator.actuateUseItem(agent, use) : true;
                if (agent != null && !agent.isUsingItem()) {
                    transitionTo(MotorState.IDLE, "use_item_complete");
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

        switch (currentState) {
            case MINING -> {
                if (agent != null && agent.getBreaker() != null && agent.getBreaker().isBreaking()) {
                    boolean broken = agent.getBreaker().tick();
                    if (broken || !agent.getBreaker().isBreaking()) {
                        transitionTo(MotorState.IDLE, "mining_finished");
                    }
                } else {
                    transitionTo(MotorState.IDLE, "mining_inactive");
                }
            }
            case MOVING -> {
                if (agent != null && agent.getNavigator() != null) {
                    agent.getNavigator().tick();
                    if (agent.getNavigator().isArrived()) {
                        transitionTo(MotorState.IDLE, "navigation_arrived");
                    } else if (agent.getNavigator().isStuck()) {
                        transitionTo(MotorState.STUCK_RECOVERY, "navigation_stuck");
                    } else if (agent.getNavigator().isIdle()) {
                        transitionTo(MotorState.IDLE, "navigation_idle");
                    }
                } else {
                    transitionTo(MotorState.IDLE, "navigator_null");
                }
            }
            case COMBAT_ENGAGED -> {
                combatTicks++;
                if (combatTicks > COMBAT_TIMEOUT_TICKS) {
                    transitionTo(MotorState.IDLE, "combat_timeout_disengaged");
                }
            }
            case USING_ITEM -> {
                if (agent != null && !agent.isUsingItem()) {
                    transitionTo(MotorState.IDLE, "use_item_finished");
                }
            }
            case SLEEPING -> {
                if (agent != null && !agent.isSleeping()) {
                    transitionTo(MotorState.IDLE, "sleeping_finished");
                }
            }
            case STUCK_RECOVERY -> {
                stuckRecoveryTicks++;
                if (stuckRecoveryTicks > STUCK_RECOVERY_MAX_TICKS) {
                    if (agent != null && agent.getNavigator() != null) {
                        agent.getNavigator().stop();
                    }
                    transitionTo(MotorState.IDLE, "stuck_recovery_timeout");
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
