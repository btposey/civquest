package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.component.DataComponents;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.entity.MobCategory;
import net.minecraft.world.entity.monster.Enemy;
import net.minecraft.world.entity.projectile.Projectile;
import net.minecraft.world.food.FoodProperties;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

import java.util.List;

/**
 * Connection Watchdog & Autonomous Fail-Safe Finite State Machine.
 * Monitors gRPC stream activity for an {@link AgentEntity} and executes
 * in-engine survival reflexes (shield raise, crouch, auto-consume food)
 * when connectivity degrades or disconnects.
 */
public class AgentWatchdogFSM {
    private static final Logger LOGGER = LogUtils.getLogger();

    public static final int DEGRADED_THRESHOLD_TICKS = 40;   // 2.0s without intents
    public static final int DISCONNECTED_THRESHOLD_TICKS = 100; // 5.0s without intents
    public static final int THREAT_SUBSIDE_TICKS = 40;       // 2.0s without threat before lowering defense
    public static final double THREAT_SCAN_RADIUS = 8.0;     // 8 block threat scanning bubble
    public static final int EAT_COOLDOWN_TICKS = 32;         // 1.6s cooldown between autonomous eating actions
    public static final int THREAT_SCAN_PERIOD_TICKS = 10;   // 0.5s period for spatial scanning

    private final AgentEntity agent;
    private volatile WatchdogState currentState = WatchdogState.DISCONNECTED_SAFE_MODE;
    private volatile WatchdogState previousState = WatchdogState.DISCONNECTED_SAFE_MODE;
    private volatile long lastIntentTick = 0L;
    private volatile long ticksInState = 0L;
    private volatile long currentTick = 0L;
    private volatile boolean streamRegistered = false;
    private volatile boolean defensiveLockActive = false;
    private volatile int ticksSinceLastThreat = 100;
    private volatile int eatCooldownTicks = 0;
    private volatile int threatScanCooldownTicks = 0;
    private volatile boolean cachedHostileThreat = false;

    public AgentWatchdogFSM(AgentEntity agent) {
        this.agent = agent;
    }

    public AgentEntity getAgent() {
        return agent;
    }

    public WatchdogState getState() {
        return currentState;
    }

    public WatchdogState getPreviousState() {
        return previousState;
    }

    public long getTicksInState() {
        return ticksInState;
    }

    public long getLastIntentTick() {
        return lastIntentTick;
    }

    public void setLastIntentTick(long lastIntentTick) {
        this.lastIntentTick = lastIntentTick;
    }

    public long getCurrentTick() {
        return currentTick;
    }

    public void setCurrentTick(long currentTick) {
        this.currentTick = currentTick;
    }

    public boolean isStreamRegistered() {
        return streamRegistered;
    }

    public void setStreamRegistered(boolean streamRegistered) {
        this.streamRegistered = streamRegistered;
    }

    public boolean isDefensiveLockActive() {
        return defensiveLockActive;
    }

    public void setDefensiveLockActive(boolean defensiveLockActive) {
        this.defensiveLockActive = defensiveLockActive;
    }

    public int getEatCooldownTicks() {
        return eatCooldownTicks;
    }

    public void setEatCooldownTicks(int eatCooldownTicks) {
        this.eatCooldownTicks = eatCooldownTicks;
    }

    public int getThreatScanCooldownTicks() {
        return threatScanCooldownTicks;
    }

    public void setThreatScanCooldownTicks(int threatScanCooldownTicks) {
        this.threatScanCooldownTicks = threatScanCooldownTicks;
    }

    public boolean isCachedHostileThreat() {
        return cachedHostileThreat;
    }

    public void setCachedHostileThreat(boolean cachedHostileThreat) {
        this.cachedHostileThreat = cachedHostileThreat;
    }

    public int getTicksSinceLastThreat() {
        return ticksSinceLastThreat;
    }

    public void setTicksSinceLastThreat(int ticksSinceLastThreat) {
        this.ticksSinceLastThreat = ticksSinceLastThreat;
    }

    /**
     * Executes state transition with entry/exit cleanup.
     */
    public synchronized boolean transitionTo(WatchdogState newState, String reason) {
        if (newState == null || newState == this.currentState) {
            return false;
        }

        // Terminal state invariant: Once shut down, the agent cannot transition back
        if (this.currentState == WatchdogState.SHUTDOWN) {
            LOGGER.debug("Rejected transition from terminal SHUTDOWN to {} for agent '{}'",
                    newState, agent != null ? agent.getAgentId() : "unknown");
            return false;
        }

        WatchdogState oldState = this.currentState;
        this.previousState = oldState;
        this.currentState = newState;
        this.ticksInState = 0L;

        // When returning to CONNECTED_ACTIVE or moving to SHUTDOWN, release autonomous defense lock
        if (newState == WatchdogState.CONNECTED_ACTIVE || newState == WatchdogState.SHUTDOWN) {
            releaseDefensivePosture();
        }

        LOGGER.debug("Agent '{}' WatchdogFSM transition: {} -> {} (reason: {})",
                agent != null ? agent.getAgentId() : "unknown", oldState, newState, reason);
        return true;
    }

    /**
     * Called when the gRPC stream connects or registers for this agent.
     */
    public synchronized void onConnected() {
        if (this.currentState == WatchdogState.SHUTDOWN) {
            return;
        }
        this.streamRegistered = true;
        this.lastIntentTick = currentTick;
        transitionTo(WatchdogState.CONNECTED_ACTIVE, "grpc_stream_connected");
    }

    /**
     * Called when an intent is received over the gRPC stream.
     */
    public synchronized void onIntentReceived(AgentIntent intent) {
        if (this.currentState == WatchdogState.SHUTDOWN) {
            return;
        }
        this.lastIntentTick = currentTick;
        if (currentState == WatchdogState.STREAM_DEGRADED || currentState == WatchdogState.DISCONNECTED_SAFE_MODE) {
            if (streamRegistered) {
                transitionTo(WatchdogState.CONNECTED_ACTIVE, "grpc_intent_received");
            }
        }
    }

    /**
     * Called when the gRPC stream disconnects or is cancelled by the client.
     */
    public synchronized void onDisconnected() {
        this.streamRegistered = false;
        if (this.currentState == WatchdogState.SHUTDOWN) {
            return;
        }
        transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "grpc_stream_disconnected");
    }

    /**
     * Called when an unhandled gRPC stream error occurs.
     */
    public synchronized void onStreamError(Throwable t) {
        this.streamRegistered = false;
        if (this.currentState == WatchdogState.SHUTDOWN) {
            return;
        }
        transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "grpc_stream_error: " + (t != null ? t.getMessage() : "unknown"));
    }

    /**
     * Called when the agent is despawned via bridge request.
     */
    public synchronized void onAgentDespawn() {
        this.streamRegistered = false;
        transitionTo(WatchdogState.SHUTDOWN, "agent_despawned");
    }

    /**
     * Called when the agent dies (permadeath).
     */
    public synchronized void onAgentDeath() {
        this.streamRegistered = false;
        transitionTo(WatchdogState.SHUTDOWN, "agent_died");
    }

    /**
     * Server-thread 20 Hz tick update for watchdog monitoring and autonomous reflexes.
     */
    public synchronized void tick() {
        if (currentState == WatchdogState.SHUTDOWN) {
            return;
        }

        if (agent == null || !agent.isAlive() || agent.isRemoved()) {
            transitionTo(WatchdogState.SHUTDOWN, "agent_not_alive_or_removed");
            return;
        }

        currentTick = agent.tickCount;
        ticksInState++;

        if (eatCooldownTicks > 0) {
            eatCooldownTicks--;
        }
        if (threatScanCooldownTicks > 0) {
            threatScanCooldownTicks--;
        }

        // Evaluate connection timeouts
        if (currentState == WatchdogState.CONNECTED_ACTIVE) {
            if (streamRegistered && (currentTick - lastIntentTick > DEGRADED_THRESHOLD_TICKS)) {
                transitionTo(WatchdogState.STREAM_DEGRADED, "intent_timeout_degraded");
            } else if (!streamRegistered) {
                transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "stream_unregistered");
            }
        } else if (currentState == WatchdogState.STREAM_DEGRADED) {
            if (!streamRegistered || (currentTick - lastIntentTick > DISCONNECTED_THRESHOLD_TICKS)) {
                transitionTo(WatchdogState.DISCONNECTED_SAFE_MODE, "intent_timeout_disconnected");
            }
        }

        // Autonomous In-Engine Reflexes in DISCONNECTED_SAFE_MODE or STREAM_DEGRADED
        if (currentState == WatchdogState.DISCONNECTED_SAFE_MODE || currentState == WatchdogState.STREAM_DEGRADED) {
            evaluateAutonomousReflexes();
        }
    }

    /**
     * Executes in-engine autonomous survival reflexes:
     * 1. Damage & Threat Defense: Equips shield, raises shield, crouches to deflect damage.
     * 2. Low Food Reflex: Auto-consumes food when hunger <= 6.
     */
    public synchronized void evaluateAutonomousReflexes() {
        if (agent == null || !agent.isAlive() || agent.isRemoved()) {
            return;
        }

        boolean underAttack = agent.hurtTime > 0;
        boolean hostileThreat = false;

        if (threatScanCooldownTicks <= 0) {
            threatScanCooldownTicks = THREAT_SCAN_PERIOD_TICKS;
            cachedHostileThreat = scanForHostileThreats();
        }
        hostileThreat = cachedHostileThreat;

        if (underAttack || hostileThreat) {
            ticksSinceLastThreat = 0;
            engageDefensivePosture();
        } else {
            ticksSinceLastThreat++;
            if (defensiveLockActive && ticksSinceLastThreat >= THREAT_SUBSIDE_TICKS) {
                releaseDefensivePosture();
            }
        }

        // Autonomous hunger survival reflex: consume food when hunger is critical
        if (agent.getFoodData() != null && agent.getFoodData().getFoodLevel() <= 6 && agent.getInventory() != null) {
            consumeAvailableFood();
        }
    }

    private boolean scanForHostileThreats() {
        if (agent.level() != null) {
            try {
                List<LivingEntity> nearbyHostiles = agent.level().getEntitiesOfClass(
                        LivingEntity.class,
                        agent.getBoundingBox().inflate(THREAT_SCAN_RADIUS),
                        e -> e != null && e.isAlive() && e != agent && (e instanceof Enemy || e.getType().getCategory() == MobCategory.MONSTER)
                );
                if (!nearbyHostiles.isEmpty()) {
                    return true;
                }

                List<Projectile> nearbyProjectiles = agent.level().getEntitiesOfClass(
                        Projectile.class,
                        agent.getBoundingBox().inflate(THREAT_SCAN_RADIUS),
                        p -> p != null && p.isAlive()
                );
                if (!nearbyProjectiles.isEmpty()) {
                    return true;
                }
            } catch (Throwable ignored) {
            }
        }
        return false;
    }

    /**
     * Auto-equips shield and raises it while crouching to deflect incoming damage.
     */
    public synchronized void engageDefensivePosture() {
        if (agent == null || !agent.isAlive()) {
            return;
        }

        boolean hasShieldInHand = agent.getItemInHand(InteractionHand.OFF_HAND).is(Items.SHIELD)
                || agent.getItemInHand(InteractionHand.MAIN_HAND).is(Items.SHIELD);

        if (!hasShieldInHand && agent.getInventory() != null) {
            int shieldSlot = -1;
            for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                ItemStack item = agent.getInventory().getItem(i);
                if (item != null && item.is(Items.SHIELD)) {
                    shieldSlot = i;
                    break;
                }
            }
            if (shieldSlot >= 0) {
                ItemStack foundShield = agent.getInventory().getItem(shieldSlot);
                ItemStack currentOffhand = agent.getItemInHand(InteractionHand.OFF_HAND);
                agent.setItemInHand(InteractionHand.OFF_HAND, foundShield);
                agent.getInventory().setItem(shieldSlot, currentOffhand);
                hasShieldInHand = true;
            }
        }

        if (hasShieldInHand) {
            if (agent.getItemInHand(InteractionHand.OFF_HAND).is(Items.SHIELD)) {
                agent.startUsingItem(InteractionHand.OFF_HAND);
            } else if (agent.getItemInHand(InteractionHand.MAIN_HAND).is(Items.SHIELD)) {
                agent.startUsingItem(InteractionHand.MAIN_HAND);
            }
        }

        boolean wasDefensiveLock = defensiveLockActive;
        agent.setShiftKeyDown(true);
        defensiveLockActive = true;

        if (!wasDefensiveLock) {
            agent.recordReflexNotice(com.civquest.mod.grpc.v1.ReflexNotice.newBuilder()
                    .setReflexType(com.civquest.mod.grpc.v1.ReflexNotice.ReflexType.REFLEX_TYPE_SHIELD_DEFENSE)
                    .setTick(currentTick)
                    .setDetails("Autonomous watchdog shield defense engaged")
                    .build());
        }
    }

    /**
     * Smoothly releases shield and un-crouches.
     */
    public synchronized void releaseDefensivePosture() {
        if (defensiveLockActive) {
            if (agent != null) {
                if (agent.isUsingItem() && agent.getUseItem().is(Items.SHIELD)) {
                    agent.stopUsingItem();
                }
                agent.setShiftKeyDown(false);
            }
            defensiveLockActive = false;
        }
    }

    /**
     * Scans inventory and consumes the first available food item.
     * Enforces EAT_COOLDOWN_TICKS between consumption events.
     */
    public synchronized boolean consumeAvailableFood() {
        if (agent == null || agent.getInventory() == null || agent.getFoodData() == null) {
            return false;
        }

        if (eatCooldownTicks > 0) {
            return false;
        }

        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            ItemStack stack = agent.getInventory().getItem(i);
            if (stack != null && !stack.isEmpty()) {
                FoodProperties food = stack.get(DataComponents.FOOD);
                if (food != null) {
                    String foodId = net.minecraft.core.registries.BuiltInRegistries.ITEM.getKey(stack.getItem()).toString();
                    agent.getFoodData().eat(food);
                    stack.shrink(1);
                    this.eatCooldownTicks = EAT_COOLDOWN_TICKS;
                    agent.recordReflexNotice(com.civquest.mod.grpc.v1.ReflexNotice.newBuilder()
                            .setReflexType(com.civquest.mod.grpc.v1.ReflexNotice.ReflexType.REFLEX_TYPE_AUTO_EAT)
                            .setTick(currentTick)
                            .setDetails("Autonomous watchdog food consumption: " + foodId)
                            .build());
                    return true;
                }
            }
        }
        return false;
    }
}
