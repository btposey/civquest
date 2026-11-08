package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.util.Mth;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.phys.Vec3;
import org.slf4j.Logger;

import java.lang.reflect.Constructor;
import java.lang.reflect.Method;

/**
 * Manages navigation for an {@link AgentEntity}.
 * Supports Baritone via dynamic reflection if available on the classpath,
 * with a high-performance native voxel navigation fallback.
 */
public class AgentNavigator {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final boolean BARITONE_PRESENT;
    private static final Method GET_PROVIDER_METHOD;
    private static final Method GET_BARITONE_FOR_PLAYER_METHOD;
    private static final Method GET_CUSTOM_GOAL_PROCESS_METHOD;
    private static final Method SET_GOAL_AND_PATH_METHOD;
    private static final Method SET_GOAL_METHOD;
    private static final Method IS_ACTIVE_METHOD;
    private static final Constructor<?> GOAL_NEAR_CTOR;
    private static final Constructor<?> GOAL_BLOCK_CTOR;

    static {
        boolean present = false;
        Method getProvider = null;
        Method getBaritoneForPlayer = null;
        Method getCustomGoalProcess = null;
        Method setGoalAndPath = null;
        Method setGoal = null;
        Method isActive = null;
        Constructor<?> goalNearCtor = null;
        Constructor<?> goalBlockCtor = null;

        try {
            Class<?> apiClass = Class.forName("baritone.api.BaritoneAPI");
            getProvider = apiClass.getMethod("getProvider");
            Class<?> providerClass = Class.forName("baritone.api.IBaritoneProvider");
            getBaritoneForPlayer = providerClass.getMethod("getBaritoneForPlayer", net.minecraft.world.entity.player.Player.class);
            Class<?> baritoneClass = Class.forName("baritone.api.IBaritone");
            getCustomGoalProcess = baritoneClass.getMethod("getCustomGoalProcess");
            Class<?> goalProcessClass = Class.forName("baritone.api.process.ICustomGoalProcess");
            Class<?> goalClass = Class.forName("baritone.api.pathing.goals.Goal");
            setGoalAndPath = goalProcessClass.getMethod("setGoalAndPath", goalClass);
            setGoal = goalProcessClass.getMethod("setGoal", goalClass);
            isActive = goalProcessClass.getMethod("isActive");

            try {
                Class<?> goalNearClass = Class.forName("baritone.api.pathing.goals.GoalNear");
                goalNearCtor = goalNearClass.getConstructor(BlockPos.class, int.class);
            } catch (Throwable ignored) {
            }

            try {
                Class<?> goalBlockClass = Class.forName("baritone.api.pathing.goals.GoalBlock");
                goalBlockCtor = goalBlockClass.getConstructor(BlockPos.class);
            } catch (Throwable ignored) {
            }

            present = true;
        } catch (Throwable ignored) {
            present = false;
        }

        BARITONE_PRESENT = present;
        GET_PROVIDER_METHOD = getProvider;
        GET_BARITONE_FOR_PLAYER_METHOD = getBaritoneForPlayer;
        GET_CUSTOM_GOAL_PROCESS_METHOD = getCustomGoalProcess;
        SET_GOAL_AND_PATH_METHOD = setGoalAndPath;
        SET_GOAL_METHOD = setGoal;
        IS_ACTIVE_METHOD = isActive;
        GOAL_NEAR_CTOR = goalNearCtor;
        GOAL_BLOCK_CTOR = goalBlockCtor;
    }

    private final AgentEntity agent;
    private NavigationState navigationState = NavigationState.NAVIGATION_STATE_IDLE;
    private Vec3 targetCoords;
    private double speedMultiplier = 1.0;
    private boolean sprint = false;
    private boolean allowBreak = false;
    private boolean allowPlace = false;
    private int arrivalTolerance = 1;
    private int stuckTicks = 0;
    private int baritoneInactivityTicks = 0;
    private Vec3 lastPosition = null;
    private Vec3 simulatedPos = Vec3.ZERO;
    private boolean usingBaritone = false;
    private Object baritoneInstance = null;

    public AgentNavigator() {
        this(null);
    }

    public AgentNavigator(AgentEntity agent) {
        this.agent = agent;
        if (agent != null) {
            this.simulatedPos = agent.position();
        }
    }

    public Vec3 getPosition() {
        return agent != null ? agent.position() : simulatedPos;
    }

    public void setPosition(Vec3 pos) {
        if (pos == null || !Double.isFinite(pos.x) || !Double.isFinite(pos.y) || !Double.isFinite(pos.z)) {
            return;
        }
        if (agent != null) {
            agent.setPos(pos.x, pos.y, pos.z);
        }
        this.simulatedPos = pos;
    }

    public void setPosition(double x, double y, double z) {
        setPosition(new Vec3(x, y, z));
    }

    public static boolean isBaritonePresent() {
        return BARITONE_PRESENT;
    }

    public void navigate(NavigateIntent intent) {
        if (intent == null || !intent.hasTarget()) {
            return;
        }
        com.civquest.mod.grpc.v1.Vec3 t = intent.getTarget();
        navigate(t.getX(), t.getY(), t.getZ(),
                intent.getSpeedMultiplier(),
                intent.getSprint(),
                intent.getAllowBreakBlocks(),
                intent.getAllowPlaceScaffolding(),
                intent.getArrivalTolerance());
    }

    public void navigate(double x, double y, double z,
                         float speedMultiplier,
                         boolean sprint,
                         boolean allowBreak,
                         boolean allowPlace,
                         int arrivalTolerance) {
        if (!Double.isFinite(x) || !Double.isFinite(y) || !Double.isFinite(z)
                || Math.abs(x) > 30000000.0 || Math.abs(z) > 30000000.0
                || y < -64.0 || y > 320.0) {
            LOGGER.warn("Rejecting navigate coordinates outside finite world bounds: ({}, {}, {})", x, y, z);
            return;
        }

        this.targetCoords = new Vec3(x, y, z);
        this.speedMultiplier = speedMultiplier > 0.0f ? speedMultiplier : 1.0;
        this.sprint = sprint;
        this.allowBreak = allowBreak;
        this.allowPlace = allowPlace;
        this.arrivalTolerance = arrivalTolerance > 0 ? arrivalTolerance : 1;
        this.stuckTicks = 0;
        this.baritoneInactivityTicks = 0;
        this.lastPosition = getPosition();
        this.navigationState = NavigationState.NAVIGATION_STATE_PATHING;

        if (BARITONE_PRESENT) {
            this.usingBaritone = tryStartBaritone(x, y, z, this.arrivalTolerance);
        } else {
            this.usingBaritone = false;
        }
    }

    private boolean tryStartBaritone(double x, double y, double z, int tolerance) {
        if (!BARITONE_PRESENT || GET_PROVIDER_METHOD == null || GET_BARITONE_FOR_PLAYER_METHOD == null
                || GET_CUSTOM_GOAL_PROCESS_METHOD == null || SET_GOAL_AND_PATH_METHOD == null) {
            return false;
        }
        try {
            Object provider = GET_PROVIDER_METHOD.invoke(null);
            if (provider == null) {
                return false;
            }

            Object baritone = GET_BARITONE_FOR_PLAYER_METHOD.invoke(provider, this.agent);
            if (baritone == null) {
                return false;
            }
            this.baritoneInstance = baritone;

            Object customGoalProcess = GET_CUSTOM_GOAL_PROCESS_METHOD.invoke(baritone);
            if (customGoalProcess == null) {
                return false;
            }

            BlockPos targetPos = BlockPos.containing(x, y, z);
            Object goal = null;
            if (GOAL_NEAR_CTOR != null) {
                goal = GOAL_NEAR_CTOR.newInstance(targetPos, tolerance);
            } else if (GOAL_BLOCK_CTOR != null) {
                goal = GOAL_BLOCK_CTOR.newInstance(targetPos);
            }

            if (goal == null) {
                return false;
            }

            SET_GOAL_AND_PATH_METHOD.invoke(customGoalProcess, goal);
            LOGGER.debug("Baritone navigation initiated for agent '{}' to ({}, {}, {})",
                    agent != null ? agent.getAgentId() : "unknown", x, y, z);
            return true;
        } catch (Throwable t) {
            LOGGER.debug("Baritone navigation not available or failed; using native navigation: {}", t.getMessage());
            this.baritoneInstance = null;
            return false;
        }
    }

    public void tick() {
        if (this.navigationState != NavigationState.NAVIGATION_STATE_PATHING || this.targetCoords == null) {
            return;
        }

        if (this.usingBaritone && this.baritoneInstance != null && this.agent != null
                && GET_CUSTOM_GOAL_PROCESS_METHOD != null && IS_ACTIVE_METHOD != null) {
            try {
                Object customGoalProcess = GET_CUSTOM_GOAL_PROCESS_METHOD.invoke(this.baritoneInstance);
                if (customGoalProcess != null) {
                    boolean active = (boolean) IS_ACTIVE_METHOD.invoke(customGoalProcess);
                    if (!active) {
                        double distSq = this.agent.distanceToSqr(this.targetCoords.x, this.targetCoords.y, this.targetCoords.z);
                        if (distSq <= (double) (this.arrivalTolerance * this.arrivalTolerance)) {
                            this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
                        } else {
                            this.navigationState = NavigationState.NAVIGATION_STATE_IDLE;
                        }
                        this.usingBaritone = false;
                        return;
                    }

                    // Check horizontal movement while Baritone is active
                    Vec3 currentPos = getPosition();
                    if (this.lastPosition != null) {
                        double hdx = currentPos.x - this.lastPosition.x;
                        double hdz = currentPos.z - this.lastPosition.z;
                        if ((hdx * hdx + hdz * hdz) < 0.0004) {
                            this.baritoneInactivityTicks++;
                            if (this.baritoneInactivityTicks >= 20) {
                                LOGGER.info("Baritone produced no horizontal movement for 20 ticks; falling back to native navigation");
                                this.usingBaritone = false;
                                this.baritoneInactivityTicks = 0;
                            }
                        } else {
                            this.baritoneInactivityTicks = 0;
                        }
                    }
                    this.lastPosition = currentPos;

                    if (this.usingBaritone) {
                        this.navigationState = NavigationState.NAVIGATION_STATE_PATHING;
                        return;
                    }
                }
            } catch (Throwable t) {
                LOGGER.debug("Baritone tick poll failed; falling back to native tick: {}", t.getMessage());
                this.usingBaritone = false;
            }
        }

        tickNative();
    }

    private void tickNative() {
        Vec3 currentPos = getPosition();
        double dx = this.targetCoords.x - currentPos.x;
        double dy = this.targetCoords.y - currentPos.y;
        double dz = this.targetCoords.z - currentPos.z;
        double horizontalDist = Math.sqrt(dx * dx + dz * dz);

        // Check arrival within arrival_tolerance
        if (horizontalDist <= (double) this.arrivalTolerance && Math.abs(dy) <= Math.max(2.0, (double) this.arrivalTolerance)) {
            this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
            if (this.agent != null) {
                this.agent.setDeltaMovement(0.0, this.agent.getDeltaMovement().y, 0.0);
                this.agent.setSprinting(false);
            }
            return;
        }

        // Direction angles and rotation update
        float targetYaw = (float) (Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
        float targetPitch = (float) (-(Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
        if (this.agent != null) {
            this.agent.setYRot(targetYaw);
            this.agent.setYHeadRot(targetYaw);
            this.agent.setXRot(targetPitch);
            this.agent.setSprinting(this.sprint);
        }

        // Movement velocity
        double dirX = dx / Math.max(horizontalDist, 0.001);
        double dirZ = dz / Math.max(horizontalDist, 0.001);
        if (this.agent != null && this.agent.horizontalCollision && this.stuckTicks >= 8) {
            // Lateral hill-contouring deflection when blocked by a 2+ block cliff face
            double angleOffset = ((this.stuckTicks / 16) % 2 == 0) ? (Math.PI * 0.42) : -(Math.PI * 0.42);
            double cos = Math.cos(angleOffset);
            double sin = Math.sin(angleOffset);
            double rotX = dirX * cos - dirZ * sin;
            double rotZ = dirX * sin + dirZ * cos;
            dirX = rotX;
            dirZ = rotZ;
        }
        double baseSpeed = this.sprint ? 0.28 : 0.21;
        double moveSpeed = baseSpeed * this.speedMultiplier;

        double vy = 0.0;
        if (this.agent != null) {
            vy = this.agent.getDeltaMovement().y;
            if (this.agent.onGround()) {
                if (this.agent.horizontalCollision || dy > 0.5) {
                    vy = 0.42; // Jump impulse
                } else {
                    vy = Math.max(-0.0784, vy);
                }
            } else {
                vy = (vy - 0.08) * 0.98;
                vy = Math.max(vy, -3.92); // Terminal velocity
            }
        }

        Vec3 movement = new Vec3(dirX * moveSpeed, vy, dirZ * moveSpeed);
        if (this.agent != null) {
            this.agent.setDeltaMovement(movement);
            this.agent.hasImpulse = true;
            try {
                this.agent.move(MoverType.SELF, movement);
            } catch (Throwable t) {
                LOGGER.debug("Movement exception: {}", t.getMessage());
                this.agent.setDeltaMovement(0.0, 0.0, 0.0);
            }
        } else {
            this.simulatedPos = this.simulatedPos.add(movement);
        }

        // Re-check arrival after movement
        Vec3 postPos = getPosition();
        double postDx = this.targetCoords.x - postPos.x;
        double postDy = this.targetCoords.y - postPos.y;
        double postDz = this.targetCoords.z - postPos.z;
        double postH = Math.sqrt(postDx * postDx + postDz * postDz);
        if (postH <= (double) this.arrivalTolerance && Math.abs(postDy) <= Math.max(2.0, (double) this.arrivalTolerance)) {
            this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
            if (this.agent != null) {
                this.agent.setDeltaMovement(0.0, this.agent.getDeltaMovement().y, 0.0);
                this.agent.setSprinting(false);
            }
            return;
        }

        // Stuck detection using horizontal displacement squared to avoid wall jumping false-negatives
        if (this.lastPosition != null) {
            double hdx = postPos.x - this.lastPosition.x;
            double hdz = postPos.z - this.lastPosition.z;
            double horizontalMovedSq = hdx * hdx + hdz * hdz;
            if (horizontalMovedSq < 0.0004) {
                this.stuckTicks++;
                if (this.stuckTicks >= 40) {
                    this.navigationState = NavigationState.NAVIGATION_STATE_STUCK;
                    if (this.agent != null) {
                        this.agent.setDeltaMovement(0.0, 0.0, 0.0);
                        this.agent.setSprinting(false);
                    }
                }
            } else {
                this.stuckTicks = 0;
            }
        }
        this.lastPosition = postPos;
    }

    public void stop() {
        this.navigationState = NavigationState.NAVIGATION_STATE_IDLE;
        this.targetCoords = null;
        this.stuckTicks = 0;
        this.baritoneInactivityTicks = 0;
        if (this.agent != null) {
            this.agent.setDeltaMovement(0.0, 0.0, 0.0);
            this.agent.setSprinting(false);
        }
        if (this.usingBaritone && this.baritoneInstance != null && GET_CUSTOM_GOAL_PROCESS_METHOD != null && SET_GOAL_METHOD != null) {
            try {
                Object customGoalProcess = GET_CUSTOM_GOAL_PROCESS_METHOD.invoke(this.baritoneInstance);
                if (customGoalProcess != null) {
                    SET_GOAL_METHOD.invoke(customGoalProcess, (Object) null);
                }
            } catch (Throwable ignored) {
            }
            this.usingBaritone = false;
        }
    }

    public NavigationState getNavigationState() {
        return navigationState;
    }

    public void setNavigationState(NavigationState navigationState) {
        this.navigationState = navigationState;
    }

    public Vec3 getTargetCoords() {
        return targetCoords;
    }

    public double getSpeedMultiplier() {
        return speedMultiplier;
    }

    public boolean isSprint() {
        return sprint;
    }

    public boolean isAllowBreak() {
        return allowBreak;
    }

    public boolean isAllowPlace() {
        return allowPlace;
    }

    public int getArrivalTolerance() {
        return arrivalTolerance;
    }

    public int getStuckTicks() {
        return stuckTicks;
    }

    public void setStuckTicks(int stuckTicks) {
        this.stuckTicks = stuckTicks;
    }

    public int getBaritoneInactivityTicks() {
        return baritoneInactivityTicks;
    }

    public void setBaritoneInactivityTicks(int baritoneInactivityTicks) {
        this.baritoneInactivityTicks = baritoneInactivityTicks;
    }

    public boolean isUsingBaritone() {
        return usingBaritone;
    }

    public boolean isPathing() {
        return navigationState == NavigationState.NAVIGATION_STATE_PATHING;
    }

    public boolean isArrived() {
        return navigationState == NavigationState.NAVIGATION_STATE_ARRIVED;
    }

    public boolean isStuck() {
        return navigationState == NavigationState.NAVIGATION_STATE_STUCK;
    }

    public boolean isIdle() {
        return navigationState == NavigationState.NAVIGATION_STATE_IDLE;
    }
}
