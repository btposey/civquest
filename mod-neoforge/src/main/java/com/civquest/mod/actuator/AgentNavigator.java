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
        this.path = new java.util.ArrayList<>();
        this.enclosed = false;
        this.lastPlanTick = Long.MIN_VALUE;
        this.pillarBaseY = Integer.MIN_VALUE;
        syncRotationFromAgent();
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
            updateStamina(false);
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

    // ---- Tunables -------------------------------------------------------------------------
    static final double WALK_SPEED = 0.14;      // blocks/tick base walking speed
    static final double SPRINT_SPEED = 0.26;    // blocks/tick sprinting
    static final double ACCEL = 0.025;          // horizontal speed-up per tick
    static final double DECEL = 0.05;           // horizontal slow-down per tick
    static final float MAX_YAW_STEP = 12.0F;
    static final float MAX_HEAD_STEP = 14.0F;
    static final float MAX_PITCH_STEP = 6.0F;
    static final float MAX_HEAD_BODY_DIFF = 60.0F;
    static final float HEADING_DEADBAND = 3.0F;
    public static final double STAMINA_MAX = 100.0;
    static final double STAMINA_DRAIN = 0.6;
    static final double STAMINA_REGEN = 0.3;
    static final double STAMINA_RECOVER_AT = 30.0;
    private static final int REPLAN_INTERVAL = 8;
    private static final long FAILED_MEMORY_TICKS = 100L;

    private double stamina = STAMINA_MAX;
    private boolean exhausted = false;
    private float headYaw;
    private float bodyYaw;
    private float pitch;
    private double velX;
    private double velZ;
    private long navTick = 0L;
    private long lastPlanTick = Long.MIN_VALUE;
    private java.util.List<LocalPathPlanner.Cell> path = new java.util.ArrayList<>();
    private boolean enclosed = false;
    private final java.util.Map<Long, Long> failedCells = new java.util.HashMap<>();
    private int pillarBaseY = Integer.MIN_VALUE;
    private long lastDigTick = Long.MIN_VALUE;

    public double getStamina() {
        return stamina;
    }

    public boolean isExhausted() {
        return exhausted;
    }

    public boolean isEnclosed() {
        return enclosed;
    }

    public float getHeadYaw() {
        return headYaw;
    }

    public float getBodyYaw() {
        return bodyYaw;
    }

    public float getNavPitch() {
        return pitch;
    }

    /** State reported to telemetry: enclosed agents count as STUCK so reflexes like paving are suppressed. */
    public NavigationState getReportedNavigationState() {
        if (enclosed && navigationState == NavigationState.NAVIGATION_STATE_PATHING) {
            return NavigationState.NAVIGATION_STATE_STUCK;
        }
        return navigationState;
    }

    private void syncRotationFromAgent() {
        if (agent != null) {
            this.headYaw = agent.getYRot();
            this.bodyYaw = agent.yBodyRot;
            this.pitch = agent.getXRot();
        }
        this.velX = 0.0;
        this.velZ = 0.0;
    }

    private void updateStamina(boolean sprintingNow) {
        if (sprintingNow) {
            stamina = Math.max(0.0, stamina - STAMINA_DRAIN);
            if (stamina <= 0.0) {
                exhausted = true;
            }
        } else {
            stamina = Math.min(STAMINA_MAX, stamina + STAMINA_REGEN);
            if (exhausted && stamina >= STAMINA_RECOVER_AT) {
                exhausted = false;
            }
        }
    }

    /** World adapter used by the local planner. */
    private static LocalPathPlanner.World worldOf(net.minecraft.world.level.Level level) {
        return new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                return isBlockSolid(level, new BlockPos(x, y, z));
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return isBlockHazard(level, new BlockPos(x, y, z));
            }
        };
    }

    static boolean isBlockHazard(net.minecraft.world.level.Level level, BlockPos pos) {
        try {
            if (level == null || !level.hasChunkAt(pos)) {
                return false;
            }
            net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
            if (state == null) {
                return false;
            }
            var fluid = state.getFluidState();
            if (fluid != null && fluid.is(net.minecraft.tags.FluidTags.LAVA)) {
                return true;
            }
            return state.is(net.minecraft.world.level.block.Blocks.FIRE)
                    || state.is(net.minecraft.world.level.block.Blocks.SOUL_FIRE)
                    || state.is(net.minecraft.world.level.block.Blocks.CACTUS)
                    || state.is(net.minecraft.world.level.block.Blocks.MAGMA_BLOCK)
                    || state.is(net.minecraft.world.level.block.Blocks.SWEET_BERRY_BUSH)
                    || state.is(net.minecraft.world.level.block.Blocks.WITHER_ROSE);
        } catch (Throwable ignored) {
            return false;
        }
    }

    private void replan(net.minecraft.world.level.Level level, Vec3 pos) {
        int bx = Mth.floor(pos.x);
        int bz = Mth.floor(pos.z);
        int by = Mth.floor(pos.y + 0.1);
        long now = navTick;
        failedCells.values().removeIf(exp -> exp <= now);
        LocalPathPlanner.Result r = LocalPathPlanner.plan(worldOf(level),
                new LocalPathPlanner.Cell(bx, by, bz), targetCoords.x, targetCoords.z,
                LocalPathPlanner.DEFAULT_RADIUS, k -> failedCells.containsKey(k));
        this.path = new java.util.ArrayList<>(r.path());
        this.enclosed = r.enclosed();
        this.lastPlanTick = navTick;
    }

    private void tickNative() {
        navTick++;
        Vec3 currentPos = getPosition();
        double dx = this.targetCoords.x - currentPos.x;
        double dy = this.targetCoords.y - currentPos.y;
        double dz = this.targetCoords.z - currentPos.z;
        double horizontalDist = Math.sqrt(dx * dx + dz * dz);

        // Check arrival within arrival_tolerance
        if (horizontalDist <= (double) this.arrivalTolerance && Math.abs(dy) <= Math.max(3.5, (double) this.arrivalTolerance + 1.5)) {
            finishArrival();
            return;
        }

        net.minecraft.world.level.Level level = this.agent != null ? this.agent.level() : null;

        // Local walkability search (replaces reactive deflection).
        if (level != null) {
            boolean breakingNow = this.agent != null && this.agent.getBreaker() != null && this.agent.getBreaker().isBreaking();
            if (path.isEmpty() || (enclosed && !breakingNow) || navTick - lastPlanTick >= REPLAN_INTERVAL) {
                replan(level, currentPos);
            }
            // Advance along path: pop waypoint when close to center or standing within the waypoint cell
            while (!path.isEmpty()) {
                LocalPathPlanner.Cell c = path.get(0);
                double ddx = c.x() + 0.5 - currentPos.x;
                double ddz = c.z() + 0.5 - currentPos.z;
                boolean reachedCell = (ddx * ddx + ddz * ddz < 0.64)
                        || (Mth.floor(currentPos.x) == c.x() && Mth.floor(currentPos.z) == c.z());
                if (reachedCell && Math.abs(c.y() - currentPos.y) < 1.5) {
                    path.remove(0);
                } else {
                    break;
                }
            }
            if (enclosed && path.isEmpty()) {
                if (tickEnclosedEscape(level, currentPos)) {
                    this.stuckTicks = 0;
                    this.lastPosition = getPosition();
                    return;
                }
            } else if (!enclosed) {
                pillarBaseY = Integer.MIN_VALUE;
            }
        }

        // Desired heading toward waypoint (or straight at the target when no local path exists)
        double aimX = this.targetCoords.x;
        double aimZ = this.targetCoords.z;
        boolean waypointHigher = false;
        if (!path.isEmpty()) {
            LocalPathPlanner.Cell c = path.get(0);
            aimX = c.x() + 0.5;
            aimZ = c.z() + 0.5;
            waypointHigher = c.y() > Mth.floor(currentPos.y + 0.1);
        }
        double hx = aimX - currentPos.x;
        double hz = aimZ - currentPos.z;
        double hlen = Math.sqrt(hx * hx + hz * hz);
        float desiredYaw = hlen > 0.05 ? (float) (Mth.atan2(hz, hx) * (180.0 / Math.PI)) - 90.0F : headYaw;
        float yawDiff = LookSmoothing.wrap(desiredYaw - headYaw);

        // Rotation: head leads (eased, rate limited, dead-band), body trails, pitch low-passed.
        headYaw = LookSmoothing.approach(headYaw, desiredYaw, 0.5F, MAX_YAW_STEP, HEADING_DEADBAND);
        bodyYaw = LookSmoothing.approach(bodyYaw, headYaw, 0.35F, MAX_YAW_STEP, 0.0F);
        bodyYaw = LookSmoothing.clampBodyToHead(bodyYaw, headYaw, MAX_HEAD_BODY_DIFF);
        float desiredPitch = (float) Mth.clamp(-(Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)) * 0.2, -12.0, 12.0);
        pitch = LookSmoothing.approachLinear(pitch, desiredPitch, 0.15F, MAX_PITCH_STEP);

        // Stamina / sprint gate: walk by default
        boolean sprintNow = this.sprint && !exhausted && stamina > 0.0;
        updateStamina(sprintNow);

        if (this.agent != null) {
            this.agent.setYRot(headYaw);
            this.agent.setYHeadRot(headYaw);
            this.agent.yBodyRot = bodyYaw;
            this.agent.setXRot(pitch);
            this.agent.setSprinting(sprintNow);
        }

        // Jump logic
        boolean stepUpOneBlock = waypointHigher;
        if (level != null && path.isEmpty() && this.agent != null) {
            // Fallback direct movement: probe ahead for a 1-block ledge
            double rad = Math.toRadians(headYaw);
            double fx = -Math.sin(rad);
            double fz = Math.cos(rad);
            BlockPos aheadFeet = BlockPos.containing(currentPos.x + fx * 0.75, currentPos.y + 0.1, currentPos.z + fz * 0.75);
            stepUpOneBlock = isBlockSolid(level, aheadFeet) && !isBlockSolid(level, aheadFeet.above())
                    && !isBlockSolid(level, aheadFeet.above(2));
        }

        // Speed with smooth acceleration; slow down while still turning toward the heading
        double baseSpeed = sprintNow ? SPRINT_SPEED : WALK_SPEED;
        double mult = Math.max(0.5, Math.min(1.5, this.speedMultiplier));
        double moveSpeed = baseSpeed * mult;

        // Arrival easing: when on the final approach leg without intermediate waypoints,
        // decelerate smoothly into the arrival zone to prevent overshoot.
        if (path.isEmpty() && horizontalDist < 2.0) {
            moveSpeed *= Math.max(0.25, horizontalDist / 2.0);
        }

        double absYawDiff = Math.abs(yawDiff);
        // Forward throttle: gentle easing in the forward hemisphere, zero throttle when heading error > 90 deg.
        // When target is behind the agent (> 90 deg), align is 0.0 so the agent pivots in place
        // rather than entering a perpetual fixed-wing orbiting circle.
        double align = absYawDiff <= 90.0 ? Math.max(0.15, Math.cos(Math.toRadians(absYawDiff))) : 0.0;

        double headRad = Math.toRadians(headYaw);
        double tvx = -Math.sin(headRad) * moveSpeed * align;
        double tvz = Math.cos(headRad) * moveSpeed * align;
        double ddvx = tvx - velX;
        double ddvz = tvz - velZ;
        double dlen = Math.sqrt(ddvx * ddvx + ddvz * ddvz);
        double targetMag = Math.sqrt(tvx * tvx + tvz * tvz);
        double curMag = Math.sqrt(velX * velX + velZ * velZ);
        double maxStep = targetMag >= curMag ? ACCEL : DECEL;
        if (dlen > maxStep) {
            ddvx *= maxStep / dlen;
            ddvz *= maxStep / dlen;
        }
        velX += ddvx;
        velZ += ddvz;

        double vy = 0.0;
        if (this.agent != null) {
            vy = this.agent.getDeltaMovement().y;
            if (this.agent.onGround()) {
                if (stepUpOneBlock || (this.agent.horizontalCollision && path.isEmpty()) || (dy > 0.5 && path.isEmpty())) {
                    vy = 0.42; // Jump impulse
                } else {
                    vy = Math.max(-0.0784, vy);
                }
            } else {
                vy = (vy - 0.08) * 0.98;
                vy = Math.max(vy, -3.92); // Terminal velocity
            }
        }

        Vec3 movement = new Vec3(velX, vy, velZ);
        if (this.agent != null) {
            this.agent.setDeltaMovement(movement);
            this.agent.hasImpulse = true;
            try {
                this.agent.move(MoverType.SELF, movement);
            } catch (Throwable t) {
                LOGGER.debug("Movement exception: {}", t.getMessage());
                this.agent.setDeltaMovement(0.0, 0.0, 0.0);
            }
            if (this.agent.horizontalCollision) {
                velX *= 0.5;
                velZ *= 0.5;
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
            finishArrival();
            return;
        }

        // Stuck detection using horizontal displacement squared; not counted while still turning
        if (this.lastPosition != null) {
            double hdx = postPos.x - this.lastPosition.x;
            double hdz = postPos.z - this.lastPosition.z;
            double horizontalMovedSq = hdx * hdx + hdz * hdz;
            if (horizontalMovedSq < 0.0001 && absYawDiff < 60.0) {
                this.stuckTicks++;
                if (this.stuckTicks >= 6 && this.stuckTicks % 6 == 0) {
                    // Remember the failed direction for a few seconds and replan around it
                    if (!path.isEmpty()) {
                        LocalPathPlanner.Cell c = path.get(0);
                        failedCells.put(LocalPathPlanner.cellKey(c.x(), c.y(), c.z()), navTick + FAILED_MEMORY_TICKS);
                    }
                    path.clear();
                }
                if (this.stuckTicks >= 16) {
                    this.navigationState = NavigationState.NAVIGATION_STATE_STUCK;
                    velX = 0.0;
                    velZ = 0.0;
                    if (this.agent != null) {
                        this.agent.setDeltaMovement(0.0, 0.0, 0.0);
                        this.agent.setSprinting(false);
                    }
                }
            } else if (horizontalMovedSq >= 0.0001) {
                this.stuckTicks = 0;
            }
        }
        this.lastPosition = postPos;
    }

    private void finishArrival() {
        this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
        this.velX = 0.0;
        this.velZ = 0.0;
        this.enclosed = false;
        if (this.agent != null) {
            this.agent.setDeltaMovement(0.0, this.agent.getDeltaMovement().y, 0.0);
            this.agent.setSprinting(false);
        }
    }

    /**
     * Escapes a closed-in region by mining out a 1-block step-up or exit toward the target
     * (equipping the best tool), or pillaring up with an inventory block when allowed/needed.
     * Returns true when an escape action consumed the tick.
     */
    private boolean tickEnclosedEscape(net.minecraft.world.level.Level level, Vec3 pos) {
        if (this.agent == null) {
            return false;
        }
        velX = 0.0;
        velZ = 0.0;
        int bx = Mth.floor(pos.x);
        int bz = Mth.floor(pos.z);
        int by = Mth.floor(pos.y + 0.1);
        agent.setSprinting(false);

        if (agent.getBreaker() != null && agent.getBreaker().isBreaking()) {
            return true; // keep mining until the block gives way
        }

        BlockPos dig = findEscapeDigTarget(level, pos, bx, by, bz);
        if (dig != null && agent.getBreaker() != null) {
            try {
                agent.equipBestToolFor(level.getBlockState(dig));
                if (agent.getBreaker().start(dig)) {
                    lastDigTick = navTick;
                    this.lastPlanTick = Long.MIN_VALUE;
                    return true;
                }
            } catch (Throwable t) {
                LOGGER.debug("Escape dig failed: {}", t.getMessage());
            }
        }

        net.minecraft.world.item.ItemStack block = findPlaceableBlock();
        boolean headroom = !isBlockSolid(level, new BlockPos(bx, by + 2, bz));
        if (!block.isEmpty() && headroom) {
            // Pillar up: jump, then place a block beneath once high enough
            if (agent.onGround()) {
                BlockPos below = new BlockPos(bx, by - 1, bz);
                pillarBaseY = by;
                if (!isBlockSolid(level, below)) {
                    return false;
                }
                agent.setDeltaMovement(0.0, 0.42, 0.0);
                agent.hasImpulse = true;
                try {
                    agent.move(MoverType.SELF, new Vec3(0.0, 0.42, 0.0));
                } catch (Throwable ignored) {
                }
            } else if (pillarBaseY != Integer.MIN_VALUE && pos.y >= pillarBaseY + 1.0) {
                BlockPos place = new BlockPos(bx, pillarBaseY, bz);
                if (!isBlockSolid(level, place)) {
                    try {
                        level.setBlock(place, ((net.minecraft.world.item.BlockItem) block.getItem()).getBlock().defaultBlockState(), 3);
                        block.shrink(1);
                        this.lastPlanTick = Long.MIN_VALUE;
                    } catch (Throwable t) {
                        LOGGER.debug("Pillar placement failed: {}", t.getMessage());
                    }
                }
            } else {
                double vy = (agent.getDeltaMovement().y - 0.08) * 0.98;
                try {
                    agent.setDeltaMovement(0.0, vy, 0.0);
                    agent.move(MoverType.SELF, new Vec3(0.0, vy, 0.0));
                } catch (Throwable ignored) {
                }
            }
            return true;
        }

        return false;
    }

    private BlockPos findEscapeDigTarget(net.minecraft.world.level.Level level, Vec3 pos, int bx, int by, int bz) {
        BlockPos ceiling = new BlockPos(bx, by + 2, bz);
        if (isBlockSolid(level, ceiling) && BlockBreaker.isBreakable(level, ceiling)) {
            return ceiling;
        }
        double tdx = targetCoords != null ? targetCoords.x - pos.x : 1.0;
        double tdz = targetCoords != null ? targetCoords.z - pos.z : 0.0;
        int primaryX = Math.abs(tdx) >= Math.abs(tdz) ? (tdx >= 0 ? 1 : -1) : 0;
        int primaryZ = primaryX == 0 ? (tdz >= 0 ? 1 : -1) : 0;
        int secondaryX = primaryX == 0 ? (tdx >= 0 ? 1 : -1) : 0;
        int secondaryZ = primaryZ == 0 ? (tdz >= 0 ? 1 : -1) : 0;
        int[][] dirs = {
                {primaryX, primaryZ},
                {secondaryX, secondaryZ},
                {-secondaryX, -secondaryZ},
                {-primaryX, -primaryZ}
        };
        for (int[] dir : dirs) {
            BlockPos feet = new BlockPos(bx + dir[0], by, bz + dir[1]);
            BlockPos head = feet.above();
            BlockPos aboveHead = feet.above(2);
            if (isBlockSolid(level, feet)) {
                // Prefer breaking head (y+1) and aboveHead (y+2) so feet (y) becomes a 1-block step-up staircase!
                if (isBlockSolid(level, head) && BlockBreaker.isBreakable(level, head)) {
                    return head;
                }
                if (isBlockSolid(level, aboveHead) && BlockBreaker.isBreakable(level, aboveHead)) {
                    return aboveHead;
                }
                if (BlockBreaker.isBreakable(level, feet)) {
                    return feet;
                }
            } else if (isBlockSolid(level, head) && BlockBreaker.isBreakable(level, head)) {
                return head;
            }
        }
        return null;
    }

    private net.minecraft.world.item.ItemStack findPlaceableBlock() {
        try {
            var items = agent.getInventory().items;
            for (net.minecraft.world.item.ItemStack s : items) {
                if (!s.isEmpty() && s.getItem() instanceof net.minecraft.world.item.BlockItem bi) {
                    var b = bi.getBlock();
                    if (b.defaultBlockState().isSolid()
                            && !(b instanceof net.minecraft.world.level.block.FallingBlock)
                            && !(b instanceof net.minecraft.world.level.block.EntityBlock)) {
                        return s;
                    }
                }
            }
        } catch (Throwable ignored) {
        }
        return net.minecraft.world.item.ItemStack.EMPTY;
    }

    private static boolean isBlockSolid(net.minecraft.world.level.Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return false;
        }
        try {
            if (!level.hasChunkAt(pos)) {
                return false;
            }
            net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
            if (state == null || state.isAir()) {
                return false;
            }
            var shape = state.getCollisionShape(level, pos);
            return shape != null && !shape.isEmpty();
        } catch (Throwable ignored) {
            return false;
        }
    }

    public void stop() {
        this.navigationState = NavigationState.NAVIGATION_STATE_IDLE;
        this.targetCoords = null;
        this.stuckTicks = 0;
        this.baritoneInactivityTicks = 0;
        this.enclosed = false;
        this.velX = 0.0;
        this.velZ = 0.0;
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
