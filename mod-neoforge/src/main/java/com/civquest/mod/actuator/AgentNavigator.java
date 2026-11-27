package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.util.Mth;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import org.slf4j.Logger;

import java.lang.reflect.Constructor;
import java.lang.reflect.Method;
import java.util.List;

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
    private boolean intoCell;
    /** How close to the centre of its cell an {@code into_cell} walk must end. */
    private static final double CELL_CENTRE_TOLERANCE = 0.3;
    private int stuckTicks = 0;
    /** ADR-037: last tick an alive entity (ADR-033) blocked the heading; distinguishes congestion from terrain. */
    private long lastEntityBlockedTick = Long.MIN_VALUE;
    private boolean stuckByEntity = false;
    /** Last replan failed only because live entities occupy the corridor (ADR-033). */
    private boolean planBlockedByEntity = false;
    private boolean planSawEntities = false;
    private double lastAimDx = 0.0;
    private double lastAimDz = 0.0;
    /** Ticks within which an entity block still attributes a stuck declaration to congestion. */
    private static final int ENTITY_BLOCK_ATTRIBUTION_TICKS = 6;
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
                intent.getArrivalTolerance(),
                intent.getIntoCell());
    }

    public void navigate(double x, double y, double z,
                         float speedMultiplier,
                         boolean sprint,
                         boolean allowBreak,
                         boolean allowPlace,
                         int arrivalTolerance) {
        navigate(x, y, z, speedMultiplier, sprint, allowBreak, allowPlace, arrivalTolerance, false);
    }

    /** As above; with {@code intoCell} only the centre of the target's own cell counts as arrived. */
    public void navigate(double x, double y, double z,
                         float speedMultiplier,
                         boolean sprint,
                         boolean allowBreak,
                         boolean allowPlace,
                         int arrivalTolerance,
                         boolean intoCell) {
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
        this.intoCell = intoCell;
        this.stuckTicks = 0;
        this.lastEntityBlockedTick = Long.MIN_VALUE;
        this.stuckByEntity = false;
        this.planBlockedByEntity = false;
        this.baritoneInactivityTicks = 0;
        this.lastPosition = getPosition();
        this.path = new java.util.ArrayList<>();
        this.enclosed = false;
        this.reachedTargetCell = false;
        this.cliffDetected = false;
        this.lastPlanTick = Long.MIN_VALUE;
        this.widePath = false;
        this.searchWide = false;
        this.nextWideSearchTick = 0L;
        if (progressTarget == null || progressTarget.distanceToSqr(this.targetCoords) > 0.25) {
            this.progressTarget = this.targetCoords;
            this.progressAnchor = null;
        }
        syncRotationFromAgent();
        this.navigationState = NavigationState.NAVIGATION_STATE_PATHING;

        if (BARITONE_PRESENT && !intoCell) {
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
    private static final double STANDING_Y_ALLOWANCE = 0.6;
    /** From the water, a target overhead is reached only about head-high to it (cq-6l3.16). */
    private static final double SWIM_UP_Y_ALLOWANCE = 1.1;
    private static final int REPLAN_INTERVAL = 8;
    private static final int WIDE_REPLAN_INTERVAL = 100;
    private static final int WIDE_SEARCH_COOLDOWN_TICKS = 40;
    /** An agent that stays within this radius for {@link #STALL_WINDOW_TICKS} without closing in is stuck. */
    static final double STALL_RADIUS = 4.0;
    static final int STALL_WINDOW_TICKS = 200;
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
    private boolean reachedTargetCell = false;
    private boolean cliffDetected = false;
    private final java.util.Map<Long, Long> failedCells = new java.util.HashMap<>();
    private final java.util.Map<BlockPos, Long> openedDoors = new java.util.HashMap<>();
    /** The current path came from the wide search and is followed to its end. */
    private boolean widePath = false;
    private boolean searchWide = false;
    private long nextWideSearchTick = 0L;
    // Progress watchdog: kept across stop() so re-issuing the same target cannot reset it.
    private Vec3 progressTarget;
    private Vec3 progressAnchor;
    private double progressBest;
    private long progressSince;

    public double getStamina() {
        return stamina;
    }

    public boolean isExhausted() {
        return exhausted;
    }

    public boolean isEnclosed() {
        return enclosed;
    }

    public boolean isCliffDetected() {
        return cliffDetected;
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

    /** State reported to telemetry: enclosed agents or cliff-edge blocked agents count as STUCK so reflexes like paving are suppressed. */
    public NavigationState getReportedNavigationState() {
        if ((enclosed || cliffDetected) && navigationState == NavigationState.NAVIGATION_STATE_PATHING) {
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

    /** World adapter used by the local planner, with per-plan block and entity occupancy caching. */
    private LocalPathPlanner.World worldOf(net.minecraft.world.level.Level level) {
        return worldOf(level, false);
    }

    private LocalPathPlanner.World worldOf(net.minecraft.world.level.Level level, boolean ignoreEntities) {
        final java.util.Map<Long, Boolean> solidCache = new java.util.HashMap<>();
        final java.util.Map<Long, Boolean> hazardCache = new java.util.HashMap<>();
        final java.util.Map<Long, Boolean> climbableCache = new java.util.HashMap<>();
        final java.util.Map<Long, Boolean> waterCache = new java.util.HashMap<>();
        final java.util.Map<Long, Boolean> entityCache = new java.util.HashMap<>();
        final List<AABB> entityBoxes;
        if (level != null && !ignoreEntities) {
            Vec3 pos = getPosition();
            double r = LocalPathPlanner.DEFAULT_RADIUS + 2.0;
            double vr = LocalPathPlanner.MAX_VERTICAL_RANGE + 3.0;
            AABB snapshotBox = new AABB(
                    pos.x - r, pos.y - vr, pos.z - r,
                    pos.x + r, pos.y + vr, pos.z + r
            );
            entityBoxes = collectBlockingEntityBoxes(level, snapshotBox);
            this.planSawEntities = !entityBoxes.isEmpty();
        } else {
            entityBoxes = java.util.Collections.emptyList();
        }
        return new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                return solidCache.computeIfAbsent(LocalPathPlanner.cellKey(x, y, z),
                        k -> isBlockSolid(level, new BlockPos(x, y, z)));
            }

            @Override
            public boolean foliage(int x, int y, int z) {
                return isNaturalLeaves(level, new BlockPos(x, y, z));
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return hazardCache.computeIfAbsent(LocalPathPlanner.cellKey(x, y, z),
                        k -> isBlockHazard(level, new BlockPos(x, y, z)));
            }

            @Override
            public boolean climbable(int x, int y, int z) {
                return climbableCache.computeIfAbsent(LocalPathPlanner.cellKey(x, y, z),
                        k -> isBlockClimbable(level, new BlockPos(x, y, z)));
            }

            @Override
            public boolean water(int x, int y, int z) {
                return waterCache.computeIfAbsent(LocalPathPlanner.cellKey(x, y, z),
                        k -> isBlockWater(level, new BlockPos(x, y, z)));
            }

            @Override
            public boolean chunkLoaded(int x, int z) {
                return level != null && level.hasChunkAt(new BlockPos(x, 0, z));
            }

            @Override
            public boolean occupiedByEntity(int x, int y, int z) {
                if (entityBoxes.isEmpty()) {
                    return false;
                }
                return entityCache.computeIfAbsent(LocalPathPlanner.cellKey(x, y, z), k -> {
                    double minX = x + 0.05;
                    double maxX = x + 0.95;
                    double minY = y;
                    double maxY = y + 1.8;
                    double minZ = z + 0.05;
                    double maxZ = z + 0.95;
                    for (AABB box : entityBoxes) {
                        if (box.maxX > minX && box.minX < maxX
                                && box.maxY > minY && box.minY < maxY
                                && box.maxZ > minZ && box.minZ < maxZ) {
                            return true;
                        }
                    }
                    return false;
                });
            }
        };
    }

    private List<AABB> collectBlockingEntityBoxes(Level level, AABB queryBox) {
        if (level == null || queryBox == null) {
            return java.util.Collections.emptyList();
        }
        java.util.function.Predicate<net.minecraft.world.entity.Entity> filter = this::isBlockingLivingEntity;
        List<net.minecraft.world.entity.Entity> entities = null;
        try {
            entities = level.getEntities(this.agent, queryBox, filter);
        } catch (Throwable ignored) {
        }
        if (entities == null || entities.isEmpty()) {
            try {
                List<net.minecraft.world.entity.LivingEntity> livingList = level.getEntitiesOfClass(
                        net.minecraft.world.entity.LivingEntity.class,
                        queryBox,
                        e -> isBlockingLivingEntity(e));
                if (livingList != null && !livingList.isEmpty()) {
                    List<AABB> boxes = new java.util.ArrayList<>(livingList.size());
                    for (net.minecraft.world.entity.LivingEntity living : livingList) {
                        AABB b = living.getBoundingBox();
                        if (b != null) {
                            boxes.add(b);
                        }
                    }
                    return boxes;
                }
            } catch (Throwable ignored) {
            }
            return java.util.Collections.emptyList();
        }
        List<AABB> boxes = new java.util.ArrayList<>(entities.size());
        for (net.minecraft.world.entity.Entity entity : entities) {
            if (entity != null && entity.getBoundingBox() != null) {
                boxes.add(entity.getBoundingBox());
            }
        }
        return boxes;
    }

    private boolean isBlockingLivingEntity(net.minecraft.world.entity.Entity e) {
        if (e == null || e == this.agent || !(e instanceof net.minecraft.world.entity.LivingEntity living)) {
            return false;
        }
        if (living.isRemoved() || !living.isAlive() || isSpectatorSafe(living)) {
            return false;
        }
        if (living instanceof net.minecraft.world.entity.decoration.ArmorStand) {
            return false;
        }
        try {
            if (this.agent != null && this.agent.isPassengerOfSameVehicle(living)) {
                return false;
            }
        } catch (Throwable ignored) {
        }
        return living.getBoundingBox() != null;
    }

    private static boolean isSpectatorSafe(net.minecraft.world.entity.Entity entity) {
        if (entity == null) {
            return false;
        }
        try {
            if (entity instanceof net.minecraft.server.level.ServerPlayer sp && sp.gameMode == null) {
                return false;
            }
            return entity.isSpectator();
        } catch (Throwable ignored) {
            return false;
        }
    }

    private AABB resolveAgentBox(Vec3 currentPos) {
        if (this.agent != null) {
            AABB box = this.agent.getBoundingBox();
            if (box != null) {
                return box;
            }
        }
        return new AABB(
                currentPos.x - 0.3, currentPos.y, currentPos.z - 0.3,
                currentPos.x + 0.3, currentPos.y + 1.8, currentPos.z + 0.3
        );
    }

    private static boolean wouldPenetrateAnyEntity(AABB agentBox, double dx, double dz, List<AABB> blockers) {
        if (agentBox == null || blockers == null || blockers.isEmpty()) {
            return false;
        }
        AABB candBox = agentBox.move(dx, 0.0, dz);
        double curCx = (agentBox.minX + agentBox.maxX) * 0.5;
        double curCz = (agentBox.minZ + agentBox.maxZ) * 0.5;
        for (AABB other : blockers) {
            if (other == null) {
                continue;
            }
            if (other.maxY <= agentBox.minY + 1e-4 || other.minY >= agentBox.maxY - 1e-4) {
                continue;
            }
            boolean candIntersects = candBox.maxX > other.minX + 1e-4 && candBox.minX < other.maxX - 1e-4
                    && candBox.maxZ > other.minZ + 1e-4 && candBox.minZ < other.maxZ - 1e-4;
            if (!candIntersects) {
                continue;
            }
            boolean curIntersects = agentBox.maxX > other.minX + 1e-4 && agentBox.minX < other.maxX - 1e-4
                    && agentBox.maxZ > other.minZ + 1e-4 && agentBox.minZ < other.maxZ - 1e-4;
            if (!curIntersects) {
                return true;
            }
            double otherCx = (other.minX + other.maxX) * 0.5;
            double otherCz = (other.minZ + other.maxZ) * 0.5;
            double curDistSq = (curCx - otherCx) * (curCx - otherCx) + (curCz - otherCz) * (curCz - otherCz);
            double candDistSq = (curCx + dx - otherCx) * (curCx + dx - otherCx)
                    + (curCz + dz - otherCz) * (curCz + dz - otherCz);
            if (candDistSq <= curDistSq + 1e-7) {
                return true;
            }
        }
        return false;
    }

    private double[] chooseSafeEntitySidestep(
            Level level, Vec3 currentPos, AABB agentBox, double aimX, double aimZ, double speed, List<AABB> blockers) {
        double hdx = aimX - currentPos.x;
        double hdz = aimZ - currentPos.z;
        double hlen = Math.hypot(hdx, hdz);
        double ux;
        double uz;
        if (hlen > 1e-4) {
            ux = hdx / hlen;
            uz = hdz / hlen;
        } else {
            double rad = Math.toRadians(headYaw);
            ux = -Math.sin(rad);
            uz = Math.cos(rad);
        }
        double leftX = -uz;
        double leftZ = ux;
        double rightX = uz;
        double rightZ = -ux;

        // Prefer the lateral side moving away from the nearest blocker center (or toward target if symmetric).
        double blockerScore = 0.0;
        double bestDistSq = Double.MAX_VALUE;
        for (AABB other : blockers) {
            if (other.maxY <= agentBox.minY + 1e-4 || other.minY >= agentBox.maxY - 1e-4) {
                continue;
            }
            double ocx = (other.minX + other.maxX) * 0.5;
            double ocz = (other.minZ + other.maxZ) * 0.5;
            double dSq = (currentPos.x - ocx) * (currentPos.x - ocx) + (currentPos.z - ocz) * (currentPos.z - ocz);
            if (dSq < bestDistSq) {
                bestDistSq = dSq;
                blockerScore = (currentPos.x - ocx) * leftX + (currentPos.z - ocz) * leftZ;
            }
        }
        boolean tryLeftFirst;
        if (Math.abs(blockerScore) > 1e-4) {
            tryLeftFirst = blockerScore > 0.0;
        } else if (this.targetCoords != null) {
            double targetDot = (this.targetCoords.x - currentPos.x) * leftX + (this.targetCoords.z - currentPos.z) * leftZ;
            if (Math.abs(targetDot) > 1e-4) {
                tryLeftFirst = targetDot > 0.0;
            } else {
                tryLeftFirst = this.agent == null || (this.agent.getUUID().hashCode() & 1) == 0;
            }
        } else {
            tryLeftFirst = this.agent == null || (this.agent.getUUID().hashCode() & 1) == 0;
        }

        double[][] candidates = tryLeftFirst
                ? new double[][]{{leftX, leftZ}, {rightX, rightZ}}
                : new double[][]{{rightX, rightZ}, {leftX, leftZ}};

        boolean enforceFloorSupport = hasFloorSupport(level, currentPos.x, currentPos.y, currentPos.z);
        for (double[] dir : candidates) {
            double sx = dir[0] * speed;
            double sz = dir[1] * speed;
            double tx = currentPos.x + sx;
            double tz = currentPos.z + sz;
            if (wouldPenetrateAnyEntity(agentBox, sx, sz, blockers)) {
                continue;
            }
            if (isHazardFootprint(level, tx, currentPos.y, tz)) {
                continue;
            }
            if (collidesWithSolidFootprint(level, tx, currentPos.y, tz)) {
                continue;
            }
            if (enforceFloorSupport && !hasFloorSupport(level, tx, currentPos.y, tz)) {
                continue;
            }
            return new double[]{sx, sz};
        }
        return null;
    }

    private boolean collidesWithSolidFootprint(Level level, double x, double y, double z) {
        if (level == null) {
            return false;
        }
        int feetY = Mth.floor(y + 0.1);
        int headY = Mth.floor(y + 1.75);
        int minBx = Mth.floor(x - 0.25);
        int maxBx = Mth.floor(x + 0.25);
        int minBz = Mth.floor(z - 0.25);
        int maxBz = Mth.floor(z + 0.25);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                for (int by = feetY; by <= headY; by++) {
                    if (isBlockSolid(level, new BlockPos(bx, by, bz))) {
                        return true;
                    }
                }
            }
        }
        return false;
    }

    private boolean hasFloorSupport(Level level, double x, double y, double z) {
        if (level == null) {
            return false;
        }
        int feetY = Mth.floor(y + 0.05);
        int minBx = Mth.floor(x - 0.25);
        int maxBx = Mth.floor(x + 0.25);
        int minBz = Mth.floor(z - 0.25);
        int maxBz = Mth.floor(z + 0.25);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                boolean columnSupported = false;
                for (int drop = 1; drop <= LocalPathPlanner.MAX_DROP + 1; drop++) {
                    BlockPos below = new BlockPos(bx, feetY - drop, bz);
                    if (isBlockHazard(level, below)) {
                        break;
                    }
                    if (isBlockSolid(level, below) || isBlockClimbable(level, below) || isSafeDropBlock(level, below)) {
                        columnSupported = true;
                        break;
                    }
                }
                if (!columnSupported) {
                    return false;
                }
            }
        }
        return true;
    }

    /** Leaves that grew there; leaves a player placed are part of a build and stay. */
    private static boolean isNaturalLeaves(Level level, BlockPos pos) {
        if (level == null || pos == null || !level.hasChunkAt(pos)) {
            return false;
        }
        BlockState state = level.getBlockState(pos);
        return state != null && state.getBlock() instanceof net.minecraft.world.level.block.LeavesBlock
                && !state.getValue(net.minecraft.world.level.block.LeavesBlock.PERSISTENT);
    }

    /** Breaks the leaves standing in the agent's own cell and in the next cell of its path. */
    private void clearFoliage(Level level, Vec3 currentPos) {
        if (this.agent == null || path.isEmpty()) {
            return;
        }
        LocalPathPlanner.Cell next = path.get(0);
        BlockPos here = BlockPos.containing(currentPos.x, currentPos.y + 0.1, currentPos.z);
        BlockPos ahead = new BlockPos(next.x(), next.y(), next.z());
        BlockPos[] body = next.y() > here.getY()
                ? new BlockPos[] {here, here.above(), here.above(2), ahead, ahead.above()}
                : new BlockPos[] {here, here.above(), ahead, ahead.above(), new BlockPos(next.x(), here.getY() + 1, next.z())};
        for (BlockPos pos : body) {
            if (isNaturalLeaves(level, pos)) {
                level.destroyBlock(pos, true, this.agent);
            }
        }
    }

    static boolean isBlockWater(net.minecraft.world.level.Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return false;
        }
        try {
            if (!level.hasChunkAt(pos)) {
                return false;
            }
            net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
            if (state == null) {
                return false;
            }
            var fluid = state.getFluidState();
            return (fluid != null && fluid.is(net.minecraft.tags.FluidTags.WATER))
                    || state.is(net.minecraft.world.level.block.Blocks.WATER);
        } catch (Throwable ignored) {
            return false;
        }
    }

    boolean isDeepWater(net.minecraft.world.level.Level level, BlockPos pos) {
        if (!isBlockWater(level, pos)) {
            return false;
        }
        BlockPos top = pos;
        if (isBlockWater(level, pos.above())) {
            if (isBlockWater(level, pos.above(2))) {
                return true;
            }
            top = pos.above();
        }
        return !isBlockSolid(level, top.below()) && !isBlockSolid(level, top.below(2));
    }

    private static boolean isSafeDropBlock(Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return false;
        }
        try {
            if (!level.hasChunkAt(pos)) {
                return false;
            }
            BlockState state = level.getBlockState(pos);
            return state != null && (state.is(net.minecraft.tags.BlockTags.CLIMBABLE)
                    || state.is(net.minecraft.world.level.block.Blocks.VINE)
                    || state.is(net.minecraft.world.level.block.Blocks.LADDER)
                    || state.is(net.minecraft.world.level.block.Blocks.SCAFFOLDING));
        } catch (Throwable ignored) {
            return false;
        }
    }

    boolean isBlockClimbable(net.minecraft.world.level.Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return false;
        }
        try {
            if (!level.hasChunkAt(pos)) {
                return false;
            }
            return isClimbableState(level.getBlockState(pos), level, pos);
        } catch (Throwable ignored) {
            return false;
        }
    }

    public boolean isClimbableState(net.minecraft.world.level.block.state.BlockState state) {
        Level level = this.agent != null ? this.agent.level() : null;
        return isClimbableState(state, level, null);
    }

    public boolean isClimbableState(net.minecraft.world.level.block.state.BlockState state, net.minecraft.world.level.Level level, BlockPos pos) {
        if (state == null || state.isAir()) {
            return false;
        }
        if (state.is(net.minecraft.world.level.block.Blocks.VINE)) {
            Vec3 current = getPosition();
            if (this.targetCoords == null || current == null) {
                return false;
            }
            // Allow vertical descent down a drop shaft below the agent
            if (this.targetCoords.y < current.y && pos != null && pos.getY() < current.y) {
                return true;
            }
            // Do not treat wall vines as climbable shafts unless explicitly ascending and block above is air/open
            if (this.targetCoords.y <= current.y + 1.0) {
                return false;
            }
            if (level != null && pos != null) {
                BlockState above = level.getBlockState(pos.above());
                if (above != null && !above.isAir()) {
                    var shape = above.getCollisionShape(level, pos.above());
                    if (shape != null && !shape.isEmpty()) {
                        return false;
                    }
                }
            }
            return true;
        }
        return state.is(net.minecraft.tags.BlockTags.CLIMBABLE)
                || state.is(net.minecraft.world.level.block.Blocks.LADDER);
    }

    private BlockPos findClimbablePos(net.minecraft.world.level.Level level, Vec3 pos) {
        if (level == null || pos == null) {
            return null;
        }
        BlockPos feet = BlockPos.containing(pos.x, pos.y + 0.1, pos.z);
        if (isBlockClimbable(level, feet)) {
            return feet;
        }
        BlockPos above = feet.above();
        if (isBlockClimbable(level, above)) {
            return above;
        }
        BlockPos below = feet.below();
        if (isBlockClimbable(level, below)) {
            return below;
        }
        BlockPos[] neighbors = {feet.north(), feet.south(), feet.east(), feet.west()};
        for (BlockPos n : neighbors) {
            if (isBlockClimbable(level, n)) {
                return n;
            }
        }
        return null;
    }

    private static final double BODY_HALF_WIDTH = 0.3;
    /**
     * Wider than the agent's body: a narrower check let a corner overlap a campfire, which the
     * agent then stepped up onto (cq-6l3.5).
     */
    private static final double HAZARD_HALF_WIDTH = 0.35;
    /** Physics applies the set velocity again after the navigator's own move. */
    private static final double HAZARD_LOOKAHEAD = 2.0;

    /** Whether moving by (vx, vz) would bring the body into, or newly close to, a hazard. */
    private static boolean entersHazard(Level level, Vec3 from, double vx, double vz) {
        double x = from.x + vx * HAZARD_LOOKAHEAD;
        double z = from.z + vz * HAZARD_LOOKAHEAD;
        return isHazardWithin(level, x, from.y, z, BODY_HALF_WIDTH)
                || (isHazardFootprint(level, x, from.y, z) && !isHazardFootprint(level, from.x, from.y, from.z));
    }

    private static boolean isHazardFootprint(Level level, double x, double y, double z) {
        return isHazardWithin(level, x, y, z, HAZARD_HALF_WIDTH);
    }

    private static boolean isHazardWithin(Level level, double x, double y, double z, double halfWidth) {
        if (level == null) {
            return false;
        }
        int by = Mth.floor(y + 0.1);
        int minBx = Mth.floor(x - halfWidth);
        int maxBx = Mth.floor(x + halfWidth);
        int minBz = Mth.floor(z - halfWidth);
        int maxBz = Mth.floor(z + halfWidth);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                if (isBlockHazard(level, new BlockPos(bx, by, bz))
                        || isBlockHazard(level, new BlockPos(bx, by - 1, bz))) {
                    return true;
                }
            }
        }
        return false;
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
            if (fluid != null && (fluid.is(net.minecraft.tags.FluidTags.LAVA)
                    || fluid.is(net.minecraft.world.level.material.Fluids.LAVA)
                    || fluid.is(net.minecraft.world.level.material.Fluids.FLOWING_LAVA))) {
                return true;
            }
            return isHazardState(state);
        } catch (Throwable ignored) {
            return false;
        }
    }

    /**
     * Block-state hazard predicate. A lit campfire is a hazard (cq-gpa.11): its collision shape is only
     * 7/16 tall so the agent's feet end up inside the cell and take inFire damage. The planner treats a
     * hazard as impassable and never stands on top of one.
     */
    static boolean isHazardState(net.minecraft.world.level.block.state.BlockState state) {
        if (state == null) {
            return false;
        }
        if (state.is(net.minecraft.world.level.block.Blocks.CAMPFIRE)
                || state.is(net.minecraft.world.level.block.Blocks.SOUL_CAMPFIRE)) {
            return state.hasProperty(net.minecraft.world.level.block.CampfireBlock.LIT)
                    && state.getValue(net.minecraft.world.level.block.CampfireBlock.LIT);
        }
        return state.is(net.minecraft.world.level.block.Blocks.LAVA)
                || state.is(net.minecraft.world.level.block.Blocks.FIRE)
                || state.is(net.minecraft.world.level.block.Blocks.SOUL_FIRE)
                || state.is(net.minecraft.world.level.block.Blocks.CACTUS)
                || state.is(net.minecraft.world.level.block.Blocks.MAGMA_BLOCK)
                || state.is(net.minecraft.world.level.block.Blocks.SWEET_BERRY_BUSH)
                || state.is(net.minecraft.world.level.block.Blocks.WITHER_ROSE);
    }

    private void replan(net.minecraft.world.level.Level level, Vec3 pos) {
        int bx = Mth.floor(pos.x);
        int bz = Mth.floor(pos.z);
        int by = Mth.floor(pos.y + 0.1);
        long now = navTick;
        failedCells.values().removeIf(exp -> exp <= now);
        LocalPathPlanner.Result r = LocalPathPlanner.plan(worldOf(level),
                new LocalPathPlanner.Cell(bx, by, bz), targetCoords.x, targetCoords.y, targetCoords.z,
                LocalPathPlanner.DEFAULT_RADIUS, k -> failedCells.containsKey(k));
        // ADR-037: attribute a failed plan to entity congestion only if the same plan succeeds without entities.
        this.planBlockedByEntity = false;
        if (!r.reachedTarget() && level != null && planSawEntities) {
            LocalPathPlanner.Result ignoringEntities = LocalPathPlanner.plan(worldOf(level, true),
                    new LocalPathPlanner.Cell(bx, by, bz), targetCoords.x, targetCoords.y, targetCoords.z,
                    LocalPathPlanner.DEFAULT_RADIUS, k -> failedCells.containsKey(k));
            this.planBlockedByEntity = ignoringEntities.reachedTarget() && !ignoringEntities.enclosed();
            if (this.planBlockedByEntity) {
                r = new LocalPathPlanner.Result(r.path(), r.reachedTarget(), false, r.exploredCells());
            }
        }
        // Nothing leads there from nearby: the way out may leave the local window or start away
        // from the target (a stair out of a dugout). Search further before walking blind.
        this.widePath = false;
        boolean noRoute = !r.reachedTarget() && r.path().isEmpty();
        if (level != null && !r.enclosed() && (searchWide || (noRoute && now >= nextWideSearchTick))) {
            LocalPathPlanner.Result wide = LocalPathPlanner.plan(worldOf(level),
                    new LocalPathPlanner.Cell(bx, by, bz), targetCoords.x, targetCoords.y, targetCoords.z,
                    LocalPathPlanner.ESCAPE_RADIUS, k -> failedCells.containsKey(k));
            if (wide.hasPath()) {
                r = wide;
                this.widePath = true;
            } else {
                this.nextWideSearchTick = now + WIDE_SEARCH_COOLDOWN_TICKS;
            }
        }
        this.searchWide = false;
        this.path = new java.util.ArrayList<>(r.path());
        this.enclosed = r.enclosed();
        this.reachedTargetCell = r.reachedTarget();
        this.lastPlanTick = navTick;
    }

    private void tickNative() {
        navTick++;
        Vec3 currentPos = getPosition();
        double dx = this.targetCoords.x - currentPos.x;
        double dy = this.targetCoords.y - currentPos.y;
        double dz = this.targetCoords.z - currentPos.z;
        double horizontalDist = Math.sqrt(dx * dx + dz * dz);
        net.minecraft.world.level.Level level = this.agent != null ? this.agent.level() : null;
        boolean inWater = this.agent != null && this.agent.isInWater();
        boolean onClimbable = this.agent != null && this.agent.onClimbable();
        if (!onClimbable && level != null) {
            onClimbable = isBlockClimbable(level, BlockPos.containing(currentPos.x, currentPos.y + 0.1, currentPos.z));
        }

        // Check arrival within arrival_tolerance
        double baseYAllowance = onClimbable
                ? ((dy < 0) ? Math.max(1.35, (double) this.arrivalTolerance) : Math.max(1.0, (double) this.arrivalTolerance))
                : Math.max(3.5, (double) this.arrivalTolerance + 1.5);
        double arrivalYAllowance = resolveArrivalYAllowance(level, onClimbable, dy, baseYAllowance);
        if (arrivedAt(currentPos, horizontalDist) && Math.abs(dy) <= arrivalYAllowance) {
            finishArrival();
            return;
        }

        // Local walkability search (replaces reactive deflection).
        if (level != null) {
            handleDoorInteractions(level, currentPos);
            int interval = widePath ? WIDE_REPLAN_INTERVAL : REPLAN_INTERVAL;
            if (path.isEmpty() || enclosed || searchWide || navTick - lastPlanTick >= interval) {
                replan(level, currentPos);
            }
            // Advance along path: pop waypoint when close to center or standing within the waypoint cell
            while (!path.isEmpty()) {
                LocalPathPlanner.Cell c = path.get(0);
                double ddx = c.x() + 0.5 - currentPos.x;
                double ddz = c.z() + 0.5 - currentPos.z;
                double distSq = ddx * ddx + ddz * ddz;
                boolean isTurn = path.size() > 1 && (path.get(1).x() != c.x() || path.get(1).z() != c.z());
                boolean reachedCell;
                if (isTurn) {
                    reachedCell = (distSq < 0.20) && (Mth.floor(currentPos.x) == c.x() && Mth.floor(currentPos.z) == c.z());
                } else {
                    reachedCell = (distSq < 0.36) || (Mth.floor(currentPos.x) == c.x() && Mth.floor(currentPos.z) == c.z());
                }
                double yDiff = c.y() - currentPos.y;
                boolean reachedY;
                if (onClimbable) {
                    boolean descending = yDiff < 0 || (this.targetCoords != null && this.targetCoords.y < currentPos.y);
                    reachedY = descending ? (Math.abs(yDiff) < 1.35) : (Math.abs(yDiff) < 0.35);
                } else if (inWater) {
                    reachedY = Math.abs(yDiff) < 1.0;
                } else {
                    reachedY = yDiff <= 0.15 && Math.abs(yDiff) < 1.5;
                }
                if (reachedCell && reachedY) {
                    path.remove(0);
                } else {
                    break;
                }
            }
            // A walled-in agent reports STUCK through getReportedNavigationState; the brain
            // decides how to dig out (ADR-037).
            clearFoliage(level, currentPos);
        }

        // Desired heading toward waypoint (or straight at the target when no local path exists)
        BlockPos climbablePos = (onClimbable && level != null) ? findClimbablePos(level, currentPos) : null;
        boolean isVineClimbable = climbablePos != null && level != null
                && level.getBlockState(climbablePos).is(net.minecraft.world.level.block.Blocks.VINE);
        double aimX = this.targetCoords.x;
        double aimZ = this.targetCoords.z;
        boolean waypointHigher = false;
        if (!path.isEmpty()) {
            LocalPathPlanner.Cell c = path.get(0);
            aimX = c.x() + 0.5;
            aimZ = c.z() + 0.5;
            waypointHigher = c.y() > Mth.floor(currentPos.y + 0.1);
        } else if (climbablePos != null && Math.abs(dy) > 0.3) {
            if (!isVineClimbable || dy > 0.3) {
                aimX = climbablePos.getX() + 0.5;
                aimZ = climbablePos.getZ() + 0.5;
            }
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

        // Jump logic and cliff edge drop avoidance
        boolean stepUpOneBlock = waypointHigher;
        boolean cliffAhead = false;
        if (!onClimbable && !inWater && level != null && path.isEmpty()) {
            double rad = Math.toRadians(headYaw);
            double fx = -Math.sin(rad);
            double fz = Math.cos(rad);
            double probeX = currentPos.x + fx * 0.75;
            double probeZ = currentPos.z + fz * 0.75;

            // Probe ahead for a vertical drop-off (scan ground column at currentPos.x + fx*0.75,
            // currentPos.z + fz*0.75 down to currentPos.y - 4).
            // Climbable blocks (ladders/vines/scaffolding) and shallow water (<= 2 blocks deep)
            // provide safe traversal rather than a cliff; deep water is only entered if target is in water.
            boolean safeSupportFound = false;
            boolean targetInWater = isBlockWater(level, BlockPos.containing(this.targetCoords.x, this.targetCoords.y, this.targetCoords.z))
                    || isBlockWater(level, BlockPos.containing(this.targetCoords.x, this.targetCoords.y - 1.0, this.targetCoords.z));
            int startY = Mth.floor(currentPos.y + 0.1);
            int minY = Mth.floor(currentPos.y - 4.0);
            for (int py = startY; py >= minY; py--) {
                BlockPos probePos = BlockPos.containing(probeX, py, probeZ);
                if (isBlockHazard(level, probePos)) {
                    break;
                }
                if (isBlockSolid(level, probePos) || isBlockClimbable(level, probePos) || isSafeDropBlock(level, probePos)) {
                    safeSupportFound = true;
                    break;
                }
                if (isBlockWater(level, probePos)) {
                    if (targetInWater || !isDeepWater(level, probePos)) {
                        safeSupportFound = true;
                    }
                    break;
                }
            }
            if (!safeSupportFound) {
                cliffAhead = true;
            }

            if (!cliffAhead && this.agent != null) {
                // Fallback direct movement: probe ahead for a 1-block ledge
                BlockPos aheadFeet = BlockPos.containing(probeX, currentPos.y + 0.1, probeZ);
                stepUpOneBlock = isBlockSolid(level, aheadFeet) && !isBlockSolid(level, aheadFeet.above())
                        && !isBlockSolid(level, aheadFeet.above(2));
            }
        }

        if (cliffAhead) {
            this.cliffDetected = true;
            this.navigationState = NavigationState.NAVIGATION_STATE_STUCK;
            if (this.agent != null) {
                this.agent.setShiftKeyDown(true);
            }
        } else if (this.cliffDetected || onClimbable || inWater) {
            this.cliffDetected = false;
            if (this.agent != null) {
                this.agent.setShiftKeyDown(false);
            }
        }

        // Speed with smooth acceleration; slow down while still turning toward the heading
        double baseSpeed = sprintNow ? SPRINT_SPEED : WALK_SPEED;
        double mult = Math.max(0.5, Math.min(1.5, this.speedMultiplier));
        double moveSpeed = baseSpeed * mult;
        double arrivalRange = Math.max(1.5, (double) this.arrivalTolerance);
        boolean canCloseFinalStep = horizontalDist <= arrivalRange
                || (reachedTargetCell && horizontalDist <= Math.max(arrivalRange, 2.2));

        if (cliffAhead) {
            moveSpeed = 0.0;
        } else if (path.isEmpty()) {
            if (canCloseFinalStep) {
                moveSpeed *= Math.max(0.25, Math.min(1.0, horizontalDist / arrivalRange));
            } else if (level != null) {
                // ADR-026: In-world navigation with no local path to distant/unreachable target - stop driving into walls/ceilings
                moveSpeed = 0.0;
            }
        }

        double absYawDiff = Math.abs(yawDiff);
        // Forward throttle: gentle easing in the forward hemisphere, zero throttle when heading error > 90 deg or cliff detected.
        // When target is behind the agent (> 90 deg), align is 0.0 so the agent pivots in place
        // rather than entering a perpetual fixed-wing orbiting circle.
        double align = (absYawDiff <= 90.0 && !cliffAhead) ? Math.max(0.15, Math.cos(Math.toRadians(absYawDiff))) : 0.0;

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

        if (cliffAhead) {
            velX = 0.0;
            velZ = 0.0;
        } else if (onClimbable && climbablePos != null && Math.abs(dy) > 0.3) {
            if (!isVineClimbable || dy > 0.3) {
                // Subtle horizontal centering bias toward the climbable block so the agent stays on the shaft.
                velX += Mth.clamp((climbablePos.getX() + 0.5) - currentPos.x, -0.05, 0.05);
                velZ += Mth.clamp((climbablePos.getZ() + 0.5) - currentPos.z, -0.05, 0.05);
            }
        }

        // Prevent turning inertia or momentum from drifting into adjacent hazards (lava, fire, magma, etc.)
        // An agent already touching a hazard is left free to walk out of it.
        if (level != null && !isHazardWithin(level, currentPos.x, currentPos.y, currentPos.z, BODY_HALF_WIDTH)) {
            if (velX != 0.0 && entersHazard(level, currentPos, velX, 0.0)) {
                velX = 0.0;
            }
            if (velZ != 0.0 && entersHazard(level, currentPos, 0.0, velZ)) {
                velZ = 0.0;
            }
            if ((velX != 0.0 || velZ != 0.0) && entersHazard(level, currentPos, velX, velZ)) {
                velX = 0.0;
                velZ = 0.0;
            }
        }

        // ADR-033: Hard AABB non-overlap clamping & hazard-checked lateral sidestep around living entities
        boolean entityBlockedForward = false;
        this.lastAimDx = aimX - currentPos.x;
        this.lastAimDz = aimZ - currentPos.z;
        if (level != null && (velX != 0.0 || velZ != 0.0)) {
            AABB agentBox = resolveAgentBox(currentPos);
            AABB sweepBox = agentBox.inflate(Math.abs(velX) + 0.85, 0.2, Math.abs(velZ) + 0.85);
            List<AABB> blockers = collectBlockingEntityBoxes(level, sweepBox);
            if (!blockers.isEmpty() && wouldPenetrateAnyEntity(agentBox, velX, velZ, blockers)) {
                entityBlockedForward = true;
                this.lastEntityBlockedTick = navTick;
                boolean xSafe = velX != 0.0
                        && !wouldPenetrateAnyEntity(agentBox, velX, 0.0, blockers)
                        && !isHazardFootprint(level, currentPos.x + velX, currentPos.y, currentPos.z);
                boolean zSafe = velZ != 0.0
                        && !wouldPenetrateAnyEntity(agentBox, 0.0, velZ, blockers)
                        && !isHazardFootprint(level, currentPos.x, currentPos.y, currentPos.z + velZ);
                if (xSafe && (!zSafe || Math.abs(velX) >= Math.abs(velZ))) {
                    velZ = 0.0;
                } else if (zSafe) {
                    velX = 0.0;
                } else {
                    double speed = Math.max(0.08, Math.min(moveSpeed > 0.0 ? moveSpeed : WALK_SPEED, Math.hypot(velX, velZ)));
                    double[] sidestep = chooseSafeEntitySidestep(level, currentPos, agentBox, aimX, aimZ, speed, blockers);
                    if (sidestep != null) {
                        velX = sidestep[0];
                        velZ = sidestep[1];
                    } else {
                        velX = 0.0;
                        velZ = 0.0;
                    }
                    if (!path.isEmpty()) {
                        path.clear();
                        this.lastPlanTick = Long.MIN_VALUE;
                    }
                }
            }
        }

        double vy = 0.0;
        if (this.agent != null) {
            vy = this.agent.getDeltaMovement().y;
            if (onClimbable) {
                double verticalDelta = dy;
                if (Math.abs(verticalDelta) <= 0.3 && !path.isEmpty()) {
                    verticalDelta = path.get(0).y() - currentPos.y;
                }
                if (verticalDelta < -0.05) {
                    this.agent.setShiftKeyDown(false);
                    vy = -0.15;
                } else if (verticalDelta > 0.3) {
                    this.agent.setShiftKeyDown(false);
                    vy = 0.20;
                } else {
                    if (isVineClimbable) {
                        // Prevent zero vertical velocity wall clinging on vines when navigating downward or horizontally
                        this.agent.setShiftKeyDown(false);
                        vy = -0.15;
                    } else {
                        vy = 0.0;
                    }
                }
            } else if (inWater) {
                // Aquatic swimming & surface buoyancy: prevent downward pull into deep water
                if (this.agent.getAirSupply() < 200) {
                    // Running out of air: prioritize upward movement to surface air
                    vy = Math.max(vy, 0.15);
                } else if (this.targetCoords.y > currentPos.y + 0.5) {
                    vy = Math.max(vy, 0.15);
                } else if (this.targetCoords.y >= currentPos.y - 0.2
                        || this.agent.getAirSupply() < 280
                        || this.agent.isEyeInFluid(net.minecraft.tags.FluidTags.WATER)) {
                    vy = Math.max(vy, 0.08);
                } else {
                    // Gently damp downward drift without pulling the agent into deep water
                    vy = Math.max(vy * 0.5, 0.0);
                }
                if (this.agent.horizontalCollision && (stepUpOneBlock || this.targetCoords.y >= currentPos.y - 0.5)) {
                    vy = Math.max(vy, 0.30);
                }
            } else if (this.agent.onGround()) {
                boolean jumpSuppressed = false;
                double rad = Math.toRadians(headYaw);
                double fx = -Math.sin(rad);
                double fz = Math.cos(rad);
                boolean hasSolidStepUpAhead = false;
                if (level != null && this.agent != null) {
                    BlockPos aheadFeet = BlockPos.containing(currentPos.x + fx * 0.6, currentPos.y + 0.1, currentPos.z + fz * 0.6);
                    hasSolidStepUpAhead = isBlockSolid(level, aheadFeet)
                            && !isBlockSolid(level, aheadFeet.above())
                            && !isBlockSolid(level, aheadFeet.above(2));

                    BlockPos headPos = BlockPos.containing(currentPos.x, currentPos.y + 1.0, currentPos.z);
                    BlockPos ceilingPos = BlockPos.containing(currentPos.x, currentPos.y + 2.0, currentPos.z);
                    if (isBlockSolid(level, headPos) || isBlockSolid(level, ceilingPos)) {
                        jumpSuppressed = true;
                    }
                    if (!jumpSuppressed) {
                        try {
                            AABB ceilingBox = new AABB(
                                    currentPos.x - 0.3, currentPos.y + 1.8, currentPos.z - 0.3,
                                    currentPos.x + 0.3, currentPos.y + 2.2, currentPos.z + 0.3
                            );
                            var collisions = level.getBlockCollisions(this.agent, ceilingBox);
                            if (collisions != null && collisions.iterator().hasNext()) {
                                jumpSuppressed = true;
                            }
                        } catch (Throwable ignored) {
                        }
                    }
                    if (!jumpSuppressed) {
                        try {
                            AABB agentBox = this.agent.getBoundingBox();
                            if (agentBox != null) {
                                // 1. Always suppress jump if another entity is directly stacked on top of this agent.
                                AABB overheadBox = new AABB(
                                        agentBox.minX, agentBox.maxY - 0.25, agentBox.minZ,
                                        agentBox.maxX, agentBox.maxY + 0.5, agentBox.maxZ
                                );
                                List<net.minecraft.world.entity.Entity> overhead = level.getEntities(
                                        this.agent, overheadBox,
                                        e -> e != null && !e.isSpectator()
                                                && e.getBoundingBox() != null
                                                && e.getBoundingBox().minY >= agentBox.maxY - 0.25);
                                if (overhead != null && !overhead.isEmpty()) {
                                    jumpSuppressed = true;
                                }

                                // 2. When there is no solid 1-block terrain step-up ahead, suppress jumps if blocked by
                                //    an entity or near any non-spectator entity to prevent flat-ground hopping onto entities (ADR-033).
                                //    When stepping onto solid terrain, only suppress if a collidable non-agent entity
                                //    or an elevated stacked agent occupies the forward jump column.
                                if (!jumpSuppressed) {
                                    boolean solidStepUp = stepUpOneBlock || hasSolidStepUpAhead;
                                    if (!solidStepUp) {
                                        if (entityBlockedForward) {
                                            jumpSuppressed = true;
                                        } else {
                                            AABB entityProbeBox = agentBox.inflate(0.2, 0.05, 0.2);
                                            List<net.minecraft.world.entity.Entity> nearby = level.getEntities(
                                                    this.agent, entityProbeBox, e -> e != null && !e.isSpectator());
                                            if (nearby != null && !nearby.isEmpty()) {
                                                jumpSuppressed = true;
                                            }
                                        }
                                    } else {
                                        AABB forwardJumpColumn = new AABB(
                                                currentPos.x + fx * 0.6 - 0.2, currentPos.y + 0.5, currentPos.z + fz * 0.6 - 0.2,
                                                currentPos.x + fx * 0.6 + 0.2, currentPos.y + 2.0, currentPos.z + fz * 0.6 + 0.2
                                        );
                                        List<net.minecraft.world.entity.Entity> blockingAhead = level.getEntities(
                                                this.agent, forwardJumpColumn,
                                                e -> e != null && !e.isSpectator()
                                                        && ((!(e instanceof AgentEntity)
                                                                && (e.canBeCollidedWith()
                                                                    || (e instanceof net.minecraft.world.entity.LivingEntity && e.isAlive())))
                                                            || (e instanceof AgentEntity
                                                                && e.getBoundingBox() != null
                                                                && e.getBoundingBox().minY >= currentPos.y + 1.5)));
                                        if (blockingAhead != null && !blockingAhead.isEmpty()) {
                                            jumpSuppressed = true;
                                        }
                                    }
                                }
                            }
                        } catch (Throwable ignored) {
                        }
                    }
                }

                boolean jumpNeeded = stepUpOneBlock && !jumpSuppressed;
                if (!jumpNeeded && !jumpSuppressed && this.agent.horizontalCollision && hasSolidStepUpAhead) {
                    jumpNeeded = true;
                }
                boolean activelyMovingForward = moveSpeed > 0.05
                        && align > 0.15
                        && !(path.isEmpty() && !canCloseFinalStep);
                if (!cliffAhead && !jumpSuppressed && jumpNeeded && activelyMovingForward) {
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
        if (this.targetCoords == null) {
            return; // the move killed the agent, which stopped this navigator (cq-6l3.6)
        }

        // Re-check arrival after movement
        Vec3 postPos = getPosition();
        double postDx = this.targetCoords.x - postPos.x;
        double postDy = this.targetCoords.y - postPos.y;
        double postDz = this.targetCoords.z - postPos.z;
        double postH = Math.sqrt(postDx * postDx + postDz * postDz);
        double basePostYAllowance = onClimbable
                ? Math.max(1.0, (double) this.arrivalTolerance)
                : Math.max(2.0, (double) this.arrivalTolerance);
        double postYAllowance = resolveArrivalYAllowance(level, onClimbable, postDy, basePostYAllowance);
        if (arrivedAt(postPos, postH) && Math.abs(postDy) <= postYAllowance) {
            finishArrival();
            return;
        }

        if (stalled(postPos)) {
            declareStuck();
            this.lastPosition = postPos;
            return;
        }

        // Stuck detection using displacement squared (include vertical progress when climbing or swimming)
        if (this.lastPosition != null) {
            double hdx = postPos.x - this.lastPosition.x;
            double hdz = postPos.z - this.lastPosition.z;
            double vdy = (onClimbable || inWater) ? (postPos.y - this.lastPosition.y) : 0.0;
            double movedSq = hdx * hdx + hdz * hdz + vdy * vdy;
            if (movedSq < 0.0001 && absYawDiff < 60.0) {
                this.stuckTicks++;
                if (this.stuckTicks >= 4 && this.agent != null && this.agent.horizontalCollision) {
                    // Collision with corner or obstacle: trigger early replan to avoid grinding
                    if (!path.isEmpty()) {
                        LocalPathPlanner.Cell c = path.get(0);
                        failedCells.put(LocalPathPlanner.cellKey(c.x(), c.y(), c.z()), navTick + FAILED_MEMORY_TICKS);
                    }
                    path.clear();
                } else if (this.stuckTicks >= 6 && this.stuckTicks % 6 == 0) {
                    // Remember the failed direction for a few seconds and replan around it
                    if (!path.isEmpty()) {
                        LocalPathPlanner.Cell c = path.get(0);
                        failedCells.put(LocalPathPlanner.cellKey(c.x(), c.y(), c.z()), navTick + FAILED_MEMORY_TICKS);
                    }
                    path.clear();
                }
                if (this.stuckTicks >= 16) {
                    declareStuck();
                }
            } else if (movedSq >= 0.0001) {
                this.stuckTicks = 0;
            }
        }
        this.lastPosition = postPos;
    }

    /**
     * Hopping or jittering in place is movement, so the displacement check never fires. This
     * one does: no closer to the target and still in the same small area for a whole window.
     * Halfway through, one wide path search is tried first.
     */
    private boolean stalled(Vec3 pos) {
        double distance = pos.distanceTo(this.targetCoords);
        if (progressAnchor == null || pos.distanceTo(progressAnchor) > STALL_RADIUS
                || distance <= progressBest - 1.0) {
            progressAnchor = pos;
            progressBest = distance;
            progressSince = navTick;
            return false;
        }
        long waited = navTick - progressSince;
        if (waited == STALL_WINDOW_TICKS / 2) {
            searchWide = true;
        }
        if (waited < STALL_WINDOW_TICKS) {
            return false;
        }
        progressAnchor = null;
        return true;
    }

    private void declareStuck() {
        if (this.navigationState != NavigationState.NAVIGATION_STATE_STUCK) {
            this.stuckByEntity = isBlockedByEntity(); // latched at declaration (ADR-037)
        }
        this.navigationState = NavigationState.NAVIGATION_STATE_STUCK;
        this.velX = 0.0;
        this.velZ = 0.0;
        if (this.agent != null) {
            this.agent.setDeltaMovement(0.0, 0.0, 0.0);
            this.agent.setSprinting(false);
        }
    }

    private double resolveArrivalYAllowance(Level level, boolean onClimbable, double dy, double baseAllowance) {
        if (dy > 0.0 && this.agent != null && this.agent.isInWater()) {
            return SWIM_UP_Y_ALLOWANCE;
        }
        if (!onClimbable && level != null && this.targetCoords != null) {
            BlockPos targetBlock = BlockPos.containing(this.targetCoords.x, this.targetCoords.y, this.targetCoords.z);
            boolean solidTarget = isBlockSolid(level, targetBlock);
            boolean solidBelow = isBlockSolid(level, targetBlock.below());
            if (dy > 0.0 && solidTarget && solidBelow) {
                return Math.max(4.5, baseAllowance);
            }
            // A cell that can be stood in is reached only by standing at its height: otherwise a
            // one-block step up counts as arrived from the floor beside it.
            if (!solidTarget && solidBelow && !isBlockWater(level, targetBlock)) {
                return STANDING_Y_ALLOWANCE;
            }
        }
        return baseAllowance;
    }

    private boolean arrivedAt(Vec3 pos, double horizontalDist) {
        if (!this.intoCell) {
            return horizontalDist <= (double) this.arrivalTolerance;
        }
        return Mth.floor(pos.x) == Mth.floor(this.targetCoords.x) && Mth.floor(pos.z) == Mth.floor(this.targetCoords.z)
                && horizontalDist <= CELL_CENTRE_TOLERANCE;
    }

    private void finishArrival() {
        this.navigationState = NavigationState.NAVIGATION_STATE_ARRIVED;
        this.velX = 0.0;
        this.velZ = 0.0;
        this.enclosed = false;
        if (this.cliffDetected && this.agent != null) {
            this.agent.setShiftKeyDown(false);
        }
        this.cliffDetected = false;
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
    public static boolean isPassableDoor(net.minecraft.world.level.block.state.BlockState state) {
        if (state == null) {
            return false;
        }
        try {
            if (state.is(net.minecraft.tags.BlockTags.MOB_INTERACTABLE_DOORS)
                    || state.is(net.minecraft.tags.BlockTags.WOODEN_DOORS)
                    || state.is(net.minecraft.tags.BlockTags.WOODEN_TRAPDOORS)
                    || state.is(net.minecraft.tags.BlockTags.FENCE_GATES)) {
                return true;
            }
            if (state.is(net.minecraft.world.level.block.Blocks.IRON_DOOR)
                    || state.is(net.minecraft.world.level.block.Blocks.IRON_TRAPDOOR)) {
                return false;
            }
            net.minecraft.world.level.block.Block b = state.getBlock();
            return b instanceof net.minecraft.world.level.block.DoorBlock
                    || b instanceof net.minecraft.world.level.block.FenceGateBlock
                    || b instanceof net.minecraft.world.level.block.TrapDoorBlock;
        } catch (Throwable ignored) {}
        return false;
    }

    private void handleDoorInteractions(net.minecraft.world.level.Level level, Vec3 currentPos) {
        if (level == null || this.agent == null) {
            return;
        }

        // 1. Auto-close doors we previously opened and have now moved away from (> 2.2 blocks)
        if (!openedDoors.isEmpty()) {
            java.util.Iterator<java.util.Map.Entry<BlockPos, Long>> it = openedDoors.entrySet().iterator();
            while (it.hasNext()) {
                var entry = it.next();
                BlockPos pos = entry.getKey();
                long openTick = entry.getValue();
                double dSq = this.agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5);
                if (dSq > 4.84 || navTick - openTick > 100) {
                    try {
                        net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
                        if (state.getBlock() instanceof net.minecraft.world.level.block.DoorBlock door) {
                            if (state.getValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN)) {
                                door.setOpen(this.agent, level, state, pos, false);
                            }
                        } else if (state.getBlock() instanceof net.minecraft.world.level.block.FenceGateBlock) {
                            if (state.getValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN)) {
                                level.setBlockAndUpdate(pos, state.setValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN, false));
                                level.gameEvent(this.agent, net.minecraft.world.level.gameevent.GameEvent.BLOCK_CLOSE, pos);
                            }
                        } else if (state.getBlock() instanceof net.minecraft.world.level.block.TrapDoorBlock) {
                            if (state.getValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN)) {
                                level.setBlockAndUpdate(pos, state.setValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN, false));
                                level.gameEvent(this.agent, net.minecraft.world.level.gameevent.GameEvent.BLOCK_CLOSE, pos);
                            }
                        }
                    } catch (Throwable ignored) {}
                    it.remove();
                }
            }
        }

        // 2. Open any closed passable doors directly ahead of the agent (within 1.5 blocks)
        double rad = Math.toRadians(headYaw);
        double fx = -Math.sin(rad);
        double fz = Math.cos(rad);
        for (double dist = 0.5; dist <= 1.5; dist += 0.5) {
            double px = currentPos.x + fx * dist;
            double pz = currentPos.z + fz * dist;
            for (int dy = 0; dy <= 1; dy++) {
                BlockPos checkPos = BlockPos.containing(px, currentPos.y + dy, pz);
                try {
                    net.minecraft.world.level.block.state.BlockState state = level.getBlockState(checkPos);
                    if (isPassableDoor(state)) {
                        boolean isOpen = state.hasProperty(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN)
                                && state.getValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN);
                        if (!isOpen) {
                            if (state.getBlock() instanceof net.minecraft.world.level.block.DoorBlock door) {
                                door.setOpen(this.agent, level, state, checkPos, true);
                            } else if (state.getBlock() instanceof net.minecraft.world.level.block.FenceGateBlock) {
                                level.setBlockAndUpdate(checkPos, state.setValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN, true));
                                level.gameEvent(this.agent, net.minecraft.world.level.gameevent.GameEvent.BLOCK_OPEN, checkPos);
                            } else if (state.getBlock() instanceof net.minecraft.world.level.block.TrapDoorBlock) {
                                level.setBlockAndUpdate(checkPos, state.setValue(net.minecraft.world.level.block.state.properties.BlockStateProperties.OPEN, true));
                                level.gameEvent(this.agent, net.minecraft.world.level.gameevent.GameEvent.BLOCK_OPEN, checkPos);
                            }
                            openedDoors.put(checkPos.immutable(), navTick);
                        }
                    }
                } catch (Throwable ignored) {}
            }
        }
    }

    private boolean isBlockSolid(net.minecraft.world.level.Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return false;
        }
        try {
            if (!level.hasChunkAt(pos)) {
                return false;
            }
            net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
            if (state == null || state.isAir() || isClimbableState(state, level, pos) || isPassableDoor(state)) {
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
        this.lastEntityBlockedTick = Long.MIN_VALUE;
        this.stuckByEntity = false;
        this.planBlockedByEntity = false;
        this.baritoneInactivityTicks = 0;
        this.enclosed = false;
        this.reachedTargetCell = false;
        this.openedDoors.clear();
        if (this.cliffDetected && this.agent != null) {
            this.agent.setShiftKeyDown(false);
        }
        this.cliffDetected = false;
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
        return getReportedNavigationState() == NavigationState.NAVIGATION_STATE_STUCK;
    }

    /** True when the current obstruction on the heading is an alive entity (ADR-033), not terrain. */
    public boolean isBlockedByEntity() {
        if (navigationState == NavigationState.NAVIGATION_STATE_STUCK && stuckByEntity) {
            return true;
        }
        if (planBlockedByEntity && path.isEmpty()) {
            return true;
        }
        return lastEntityBlockedTick != Long.MIN_VALUE
                && navTick - lastEntityBlockedTick <= ENTITY_BLOCK_ATTRIBUTION_TICKS;
    }

    /**
     * ADR-037 stuck facts for the receipt: observations only, no recovery decision. Callers must
     * snapshot before {@link #stop()} (which resets the counters). Returns null if nothing is blocked.
     */
    public com.civquest.mod.grpc.v1.MotorFacts snapshotFacts() {
        boolean stuck = isStuck();
        if (!stuck && !enclosed && stuckTicks <= 0) {
            return null;
        }
        double bearing = Math.toDegrees(Math.atan2(-lastAimDx, lastAimDz));
        bearing = ((bearing % 360.0) + 360.0) % 360.0;
        double remaining = 0.0;
        if (targetCoords != null) {
            Vec3 pos = getPosition();
            remaining = Math.hypot(targetCoords.x - pos.x, targetCoords.z - pos.z);
        }
        return com.civquest.mod.grpc.v1.MotorFacts.newBuilder()
                .setStuck(stuck)
                .setEnclosed(enclosed)
                .setBlockedBearingDeg((float) bearing)
                .setStuckTicks(stuckTicks)
                .setProgress(remaining)
                .setBlockedByEntity(isBlockedByEntity())
                .build();
    }

    public boolean isIdle() {
        return navigationState == NavigationState.NAVIGATION_STATE_IDLE;
    }
}
