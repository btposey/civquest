package com.civquest.mod.actuator;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.util.Mth;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.phys.Vec3;
import org.slf4j.Logger;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Method;

/**
 * Manages navigation for a {@link SettlerEntity}.
 * Supports Baritone via dynamic reflection if available on the classpath,
 * with a high-performance native voxel navigation fallback.
 */
public class SettlerNavigator {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final boolean BARITONE_PRESENT;

    static {
        boolean present = false;
        try {
            Class.forName("baritone.api.BaritoneAPI");
            present = true;
        } catch (Throwable ignored) {
        }
        BARITONE_PRESENT = present;
    }

    private final SettlerEntity settler;
    private NavigationState navigationState = NavigationState.NAVIGATION_STATE_IDLE;
    private Vec3 targetCoords;
    private double speedMultiplier = 1.0;
    private boolean sprint = false;
    private boolean allowBreak = false;
    private boolean allowPlace = false;
    private int arrivalTolerance = 1;
    private int stuckTicks = 0;
    private Vec3 lastPosition = null;
    private Vec3 simulatedPos = Vec3.ZERO;
    private boolean usingBaritone = false;
    private Object baritoneInstance = null;

    public SettlerNavigator() {
        this(null);
    }

    public SettlerNavigator(SettlerEntity settler) {
        this.settler = settler;
        if (settler != null) {
            this.simulatedPos = settler.position();
        }
    }

    public Vec3 getPosition() {
        return settler != null ? settler.position() : simulatedPos;
    }

    public void setPosition(Vec3 pos) {
        if (pos == null) return;
        if (settler != null) {
            settler.setPos(pos.x, pos.y, pos.z);
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
        this.targetCoords = new Vec3(x, y, z);
        this.speedMultiplier = speedMultiplier > 0.0f ? speedMultiplier : 1.0;
        this.sprint = sprint;
        this.allowBreak = allowBreak;
        this.allowPlace = allowPlace;
        this.arrivalTolerance = arrivalTolerance > 0 ? arrivalTolerance : 1;
        this.stuckTicks = 0;
        this.lastPosition = getPosition();
        this.navigationState = NavigationState.NAVIGATION_STATE_PATHING;

        if (BARITONE_PRESENT) {
            this.usingBaritone = tryStartBaritone(x, y, z, allowBreak, allowPlace, sprint, this.arrivalTolerance);
        } else {
            this.usingBaritone = false;
        }
    }

    private boolean tryStartBaritone(double x, double y, double z, boolean allowBreak, boolean allowPlace, boolean allowSprint, int tolerance) {
        try {
            Class<?> apiClass = Class.forName("baritone.api.BaritoneAPI");
            Method getSettingsMethod = apiClass.getMethod("getSettings");
            Object settings = getSettingsMethod.invoke(null);
            if (settings != null) {
                setBaritoneSetting(settings, "allowBreak", allowBreak);
                setBaritoneSetting(settings, "allowPlace", allowPlace);
                setBaritoneSetting(settings, "allowSprint", allowSprint);
            }

            Method getProviderMethod = apiClass.getMethod("getProvider");
            Object provider = getProviderMethod.invoke(null);
            if (provider == null) {
                return false;
            }

            Method getBaritoneMethod = provider.getClass().getMethod("getBaritoneForPlayer", net.minecraft.world.entity.player.Player.class);
            Object baritone = getBaritoneMethod.invoke(provider, this.settler);
            if (baritone == null) {
                return false;
            }
            this.baritoneInstance = baritone;

            Method getCustomGoalProcessMethod = baritone.getClass().getMethod("getCustomGoalProcess");
            Object customGoalProcess = getCustomGoalProcessMethod.invoke(baritone);
            if (customGoalProcess == null) {
                return false;
            }

            BlockPos targetPos = BlockPos.containing(x, y, z);
            Object goal;
            try {
                Class<?> goalNearClass = Class.forName("baritone.api.pathing.goals.GoalNear");
                Constructor<?> ctor = goalNearClass.getConstructor(BlockPos.class, int.class);
                goal = ctor.newInstance(targetPos, tolerance);
            } catch (Throwable t) {
                Class<?> goalBlockClass = Class.forName("baritone.api.pathing.goals.GoalBlock");
                Constructor<?> ctor = goalBlockClass.getConstructor(BlockPos.class);
                goal = ctor.newInstance(targetPos);
            }

            Class<?> goalInterface = Class.forName("baritone.api.pathing.goals.Goal");
            Method setGoalAndPathMethod = customGoalProcess.getClass().getMethod("setGoalAndPath", goalInterface);
            setGoalAndPathMethod.invoke(customGoalProcess, goal);
            LOGGER.debug("Baritone navigation initiated for agent '{}' to ({}, {}, {})",
                    settler != null ? settler.getAgentId() : "unknown", x, y, z);
            return true;
        } catch (Throwable t) {
            LOGGER.debug("Baritone navigation not available or failed; using native navigation: {}", t.getMessage());
            this.baritoneInstance = null;
            return false;
        }
    }

    private void setBaritoneSetting(Object settings, String fieldName, boolean val) {
        try {
            Field field = settings.getClass().getField(fieldName);
            Object settingObj = field.get(settings);
            if (settingObj != null) {
                Field valueField = settingObj.getClass().getField("value");
                valueField.set(settingObj, val);
            }
        } catch (Throwable ignored) {
        }
    }

    public void tick() {
        if (this.navigationState != NavigationState.NAVIGATION_STATE_PATHING || this.targetCoords == null) {
            return;
        }

        if (this.usingBaritone && this.baritoneInstance != null && this.settler != null) {
            try {
                Method getCustomGoalProcessMethod = this.baritoneInstance.getClass().getMethod("getCustomGoalProcess");
                Object customGoalProcess = getCustomGoalProcessMethod.invoke(this.baritoneInstance);
                if (customGoalProcess != null) {
                    Method isActiveMethod = customGoalProcess.getClass().getMethod("isActive");
                    boolean active = (boolean) isActiveMethod.invoke(customGoalProcess);
                    if (!active) {
                        double distSq = this.settler.distanceToSqr(this.targetCoords.x, this.targetCoords.y, this.targetCoords.z);
                        if (distSq <= (double) (this.arrivalTolerance * this.arrivalTolerance)) {
                            this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
                        } else {
                            this.navigationState = NavigationState.NAVIGATION_STATE_IDLE;
                        }
                        this.usingBaritone = false;
                        return;
                    }
                    this.navigationState = NavigationState.NAVIGATION_STATE_PATHING;
                    return;
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
            if (this.settler != null) {
                this.settler.setDeltaMovement(0.0, this.settler.getDeltaMovement().y, 0.0);
                this.settler.setSprinting(false);
            }
            return;
        }

        // Direction angles and rotation update
        float targetYaw = (float) (Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
        float targetPitch = (float) (-(Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
        if (this.settler != null) {
            this.settler.setYRot(targetYaw);
            this.settler.setYHeadRot(targetYaw);
            this.settler.setXRot(targetPitch);
            this.settler.setSprinting(this.sprint);
        }

        // Movement velocity
        double dirX = dx / Math.max(horizontalDist, 0.001);
        double dirZ = dz / Math.max(horizontalDist, 0.001);
        double baseSpeed = this.sprint ? 0.28 : 0.21;
        double moveSpeed = baseSpeed * this.speedMultiplier;

        double vy = this.settler != null ? this.settler.getDeltaMovement().y : 0.0;
        if (this.settler != null && this.settler.onGround() && (this.settler.horizontalCollision || dy > 0.5)) {
            vy = 0.42; // Jump impulse
        }

        Vec3 movement = new Vec3(dirX * moveSpeed, vy, dirZ * moveSpeed);
        if (this.settler != null) {
            this.settler.setDeltaMovement(movement);
            this.settler.hasImpulse = true;
            try {
                this.settler.move(MoverType.SELF, movement);
            } catch (Throwable t) {
                this.settler.setPos(this.settler.getX() + movement.x, this.settler.getY() + movement.y, this.settler.getZ() + movement.z);
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
            if (this.settler != null) {
                this.settler.setDeltaMovement(0.0, this.settler.getDeltaMovement().y, 0.0);
                this.settler.setSprinting(false);
            }
            return;
        }

        // Stuck detection
        if (this.lastPosition != null) {
            double movedDistSq = postPos.distanceToSqr(this.lastPosition);
            if (movedDistSq < 0.0004) {
                this.stuckTicks++;
                if (this.stuckTicks >= 40) {
                    this.navigationState = NavigationState.NAVIGATION_STATE_STUCK;
                    if (this.settler != null) {
                        this.settler.setDeltaMovement(0.0, 0.0, 0.0);
                        this.settler.setSprinting(false);
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
        if (this.settler != null) {
            this.settler.setDeltaMovement(0.0, 0.0, 0.0);
            this.settler.setSprinting(false);
        }
        if (this.usingBaritone && this.baritoneInstance != null) {
            try {
                Method getCustomGoalProcessMethod = this.baritoneInstance.getClass().getMethod("getCustomGoalProcess");
                Object customGoalProcess = getCustomGoalProcessMethod.invoke(this.baritoneInstance);
                if (customGoalProcess != null) {
                    Method setGoalMethod = customGoalProcess.getClass().getMethod("setGoal", Class.forName("baritone.api.pathing.goals.Goal"));
                    setGoalMethod.invoke(customGoalProcess, (Object) null);
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
