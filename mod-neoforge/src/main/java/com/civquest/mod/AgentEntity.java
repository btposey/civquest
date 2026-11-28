package com.civquest.mod;

import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.AgentWatchdogFSM;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.mojang.authlib.GameProfile;
import com.mojang.logging.LogUtils;
import net.minecraft.tags.FluidTags;
import net.minecraft.core.SectionPos;
import net.minecraft.core.UUIDUtil;
import net.minecraft.network.chat.Component;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.tags.DamageTypeTags;
import net.minecraft.tags.EntityTypeTags;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.effect.MobEffects;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.enchantment.EnchantmentHelper;
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.gameevent.GameEvent;
import net.minecraft.world.scores.Team;
import net.neoforged.neoforge.common.CommonHooks;
import net.neoforged.neoforge.common.util.FakePlayer;
import org.slf4j.Logger;

import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;

/**
 * Autonomous humanoid agent avatar extending {@link FakePlayer}.
 * Supports player capabilities, food hunger decay, item usage, physical navigation,
 * and permadeath lifecycle hooks.
 */
public class AgentEntity extends FakePlayer {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double PEER_SEPARATION_RADIUS = 1.15;
    private static final double PEER_SEPARATION_STEP = 0.10;
    private static final double PEER_SEPARATION_FOOTPRINT_RADIUS = 0.30;
    private static final int PEER_SEPARATION_MAX_SAFE_DROP = 3;
    private static final double[] PEER_SEPARATION_FALLBACK_ANGLES_RAD = {
            Math.toRadians(45.0),
            Math.toRadians(-45.0),
            Math.toRadians(90.0),
            Math.toRadians(-90.0),
            Math.toRadians(135.0),
            Math.toRadians(-135.0),
            Math.PI
    };
    private static final java.lang.reflect.Field SPAWN_INVULNERABLE_TIME_FIELD = resolveSpawnInvulnerableField();

    private static java.lang.reflect.Field resolveSpawnInvulnerableField() {
        try {
            java.lang.reflect.Field f = net.minecraft.server.level.ServerPlayer.class.getDeclaredField("spawnInvulnerableTime");
            f.setAccessible(true);
            return f;
        } catch (Throwable t) {
            LOGGER.warn("Could not resolve ServerPlayer.spawnInvulnerableTime field: {}", t.getMessage());
            return null;
        }
    }

    private void clearSpawnInvulnerability() {
        if (SPAWN_INVULNERABLE_TIME_FIELD != null) {
            try {
                SPAWN_INVULNERABLE_TIME_FIELD.setInt(this, 0);
            } catch (Throwable t) {
                LOGGER.warn("Failed to clear spawnInvulnerableTime for agent '{}': {}", agentId, t.getMessage());
            }
        }
    }

    private final String agentId;
    private final String clanId;
    private String skinId;
    private byte[] skinPngBytes;
    private StreamRegistry streamRegistry;
    private SensoryFrustumCollector sensoryCollector;
    private com.civquest.mod.actuator.IntentActuator intentActuator;
    private final AgentNavigator navigator;
    private final com.civquest.mod.actuator.BlockBreaker breaker = new com.civquest.mod.actuator.BlockBreaker(this);
    private final AgentMotorFSM motorFSM;
    private final AgentWatchdogFSM watchdogFSM;
    private Consumer<AgentEntity> deathListener;
    private Consumer<AgentEntity> removalListener;
    private final AtomicBoolean cleanedUp = new AtomicBoolean(false);
    private final ConcurrentLinkedQueue<com.civquest.mod.grpc.v1.ChatEvent> pendingChats = new ConcurrentLinkedQueue<>();
    private final ConcurrentLinkedQueue<com.civquest.mod.grpc.v1.TradeRequest> pendingTrades = new ConcurrentLinkedQueue<>();
    private final ConcurrentLinkedQueue<com.civquest.mod.grpc.v1.ReflexNotice> pendingReflexNotices = new ConcurrentLinkedQueue<>();
    private final java.util.concurrent.atomic.AtomicReference<com.civquest.mod.grpc.v1.HorizonSurveyReport> pendingHorizonSurvey =
            new java.util.concurrent.atomic.AtomicReference<>();
    private long lastInventoryFullNoticeTick = -1000L;

    public void queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent chatEvent) {
        if (chatEvent != null) {
            pendingChats.add(chatEvent);
        }
    }

    public List<com.civquest.mod.grpc.v1.ChatEvent> pollIncomingChats() {
        List<com.civquest.mod.grpc.v1.ChatEvent> result = new ArrayList<>();
        com.civquest.mod.grpc.v1.ChatEvent chat;
        while ((chat = pendingChats.poll()) != null) {
            result.add(chat);
        }
        return result;
    }

    public void queuePendingTrade(com.civquest.mod.grpc.v1.TradeRequest tradeRequest) {
        if (tradeRequest != null) {
            pendingTrades.add(tradeRequest);
        }
    }

    public com.civquest.mod.grpc.v1.TradeRequest pollPendingTrade() {
        return pendingTrades.poll();
    }

    public void recordReflexNotice(com.civquest.mod.grpc.v1.ReflexNotice notice) {
        if (notice != null) {
            pendingReflexNotices.add(notice);
        }
    }

    public List<com.civquest.mod.grpc.v1.ReflexNotice> pollReflexNotices() {
        List<com.civquest.mod.grpc.v1.ReflexNotice> result = new ArrayList<>();
        com.civquest.mod.grpc.v1.ReflexNotice notice;
        while ((notice = pendingReflexNotices.poll()) != null) {
            result.add(notice);
        }
        return result;
    }

    public void queueHorizonSurveyReport(com.civquest.mod.grpc.v1.HorizonSurveyReport report) {
        if (report != null) {
            pendingHorizonSurvey.set(report);
        }
    }

    public com.civquest.mod.grpc.v1.HorizonSurveyReport pollHorizonSurveyReport() {
        return pendingHorizonSurvey.getAndSet(null);
    }

    /**
     * Rotates the agent's head and body yaw to {@code yawDeg} (level pitch 0) and broadcasts
     * rotation packets to connected players on the level so observers see the avatar turn smoothly.
     */
    public void faceBearingAndSync(float yawDeg) {
        this.setYRot(yawDeg);
        this.setYHeadRot(yawDeg);
        this.yBodyRot = yawDeg;
        this.setXRot(0.0F);
        if (this.level() instanceof ServerLevel serverLevel) {
            List<net.minecraft.server.level.ServerPlayer> players = serverLevel.players();
            if (players != null && !players.isEmpty()) {
                byte encodedYaw = (byte) net.minecraft.util.Mth.floor(yawDeg * 256.0F / 360.0F);
                net.minecraft.network.protocol.game.ClientboundRotateHeadPacket headPacket =
                        new net.minecraft.network.protocol.game.ClientboundRotateHeadPacket(this, encodedYaw);
                net.minecraft.network.protocol.game.ClientboundMoveEntityPacket.Rot rotPacket =
                        new net.minecraft.network.protocol.game.ClientboundMoveEntityPacket.Rot(
                                this.getId(), encodedYaw, (byte) 0, this.onGround());
                for (net.minecraft.server.level.ServerPlayer player : players) {
                    if (player != null && player != this && !(player instanceof AgentEntity) && player.connection != null) {
                        player.connection.send(headPacket);
                        player.connection.send(rotPacket);
                    }
                }
            }
        }
    }

    public AgentEntity(ServerLevel level, GameProfile profile, String agentId, String clanId) {
        super(level, profile);
        this.agentId = agentId;
        this.clanId = clanId;
        // FakePlayer.tick() is empty so ServerPlayer.spawnInvulnerableTime (60) never decrements; clear it immediately.
        clearSpawnInvulnerability();
        this.navigator = new AgentNavigator(this);
        this.motorFSM = new AgentMotorFSM(this);
        this.watchdogFSM = new AgentWatchdogFSM(this);
        try {
            var stepAttr = this.getAttribute(net.minecraft.world.entity.ai.attributes.Attributes.STEP_HEIGHT);
            if (stepAttr != null) {
                stepAttr.setBaseValue(1.1D);
            }
        } catch (Throwable ignored) {
        }
    }

    @Override
    public float maxUpStep() {
        return 1.1F;
    }

    /**
     * Generates a deterministic offline player UUID from display name or agent ID.
     */
    public static UUID createOfflineUUID(String nameOrId) {
        if (nameOrId == null || nameOrId.isBlank()) {
            return UUID.randomUUID();
        }
        return UUIDUtil.createOfflinePlayerUUID(nameOrId);
    }

    @Override
    public void tick() {
        super.tick();
        if (!this.level().isClientSide) {
            try {
                // LivingEntity.baseTick() skips ServerPlayer invulnerableTime countdown because ServerPlayer.tick() normally handles it.
                if (this.invulnerableTime > 0) {
                    this.invulnerableTime--;
                }
                if (this.watchdogFSM != null) {
                    this.watchdogFSM.tick();
                }
                if (this.motorFSM != null) {
                    this.motorFSM.tick(); // also ticks the ADR-036 typed motor stack (Sweep)
                } else {
                    if (this.breaker.isBreaking()) {
                        this.breaker.tick();
                    }
                    if (this.navigator != null) {
                        this.navigator.tick();
                    }
                }
                if (this.navigator != null) {
                    this.navigator.closeOpenedDoors();
                }
                if (this.intentActuator != null) {
                    this.intentActuator.tick(this);
                }
                if (this.isAlive() && !this.isSleeping()
                        && (this.motorFSM == null || !this.motorFSM.isSweeping())
                        && (this.breaker == null || !this.breaker.isBreaking())) {
                    applyHorizontalPeerSeparation();
                }
                if (this.isAlive() && !this.isSleeping() && !this.breaker.isBreaking()
                        && (this.motorFSM == null || !this.motorFSM.isSweeping())
                        && (this.navigator == null || this.navigator.getNavigationState() != com.civquest.mod.grpc.v1.NavigationState.NAVIGATION_STATE_PATHING)) {
                    updateIdleSocialGaze();
                }
                keepAfloat();
                if (this.tickCount % 10 == 0 && this.isAlive()) {
                    tryPickupNearbyItems();
                }
                if (this.isAlive()) {
                    super.causeFoodExhaustion(Metabolism.perTick(Metabolism.isExerting(this)));
                }
                // Execute player tick logic (which FakePlayer stubbed out, including FoodData tick at 20 Hz)
                this.doTick();
            } catch (Exception e) {
                LOGGER.error("Exception during agent doTick for agent '{}': {}", agentId, e.getMessage(), e);
            }
        }
    }

    /** Upward speed of an agent keeping its head above water. */
    private static final double FLOAT_SPEED = 0.1;

    /**
     * Swims up while the eyes are under water and no walk is steering: nothing else holds an
     * agent that has arrived or stopped, so it sank and drowned (cq-6l3.16).
     */
    private void keepAfloat() {
        if (!this.isAlive() || !this.isEyeInFluid(FluidTags.WATER) || (this.navigator != null
                && this.navigator.getNavigationState() == com.civquest.mod.grpc.v1.NavigationState.NAVIGATION_STATE_PATHING)) {
            return;
        }
        net.minecraft.world.phys.Vec3 v = this.getDeltaMovement();
        this.setDeltaMovement(v.x, Math.max(v.y, FLOAT_SPEED), v.z);
    }

    /**
     * Food use is metered by time and exertion ({@link Metabolism}); vanilla's per-action costs
     * (jumping, attacking, taking damage) would stack on top, so only the Hunger effect applies.
     */
    @Override
    public void causeFoodExhaustion(float amount) {
        if (this.hasEffect(MobEffects.HUNGER)) {
            super.causeFoodExhaustion(amount);
        }
    }

    /**
     * Smoothly turns the agent's head, body, and pitch toward the nearest human player within 8 blocks
     * who has line of sight when the agent is not actively pathfinding or sleeping.
     */
    public void updateIdleSocialGaze() {
        if (this.level() == null || this.isRemoved() || !this.isAlive() || this.isSleeping()) {
            return;
        }
        Player nearestHuman = null;
        double bestDistSq = 64.0; // 8.0 blocks squared
        for (Player player : this.level().players()) {
            if (player == null || player == this || player instanceof AgentEntity || !player.isAlive() || player.isSpectator()) {
                continue;
            }
            double dSq = this.distanceToSqr(player);
            if (dSq <= bestDistSq && this.hasLineOfSight(player)) {
                bestDistSq = dSq;
                nearestHuman = player;
            }
        }
        if (nearestHuman != null) {
            net.minecraft.world.phys.Vec3 eye = this.getEyePosition();
            net.minecraft.world.phys.Vec3 targetEye = nearestHuman.getEyePosition();
            double dx = targetEye.x - eye.x;
            double dy = targetEye.y - eye.y;
            double dz = targetEye.z - eye.z;
            double horizontalDist = Math.sqrt(dx * dx + dz * dz);
            float targetYaw = (float) (net.minecraft.util.Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
            float targetPitch = (float) (-(net.minecraft.util.Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
            // Gentle, rate-limited glance: head leads, body trails within a clamp, pitch low-passed.
            float smoothYaw = com.civquest.mod.actuator.LookSmoothing.approach(this.getYRot(), targetYaw, 0.25F, 5.0F, 3.0F);
            float smoothPitch = com.civquest.mod.actuator.LookSmoothing.approachLinear(this.getXRot(),
                    net.minecraft.util.Mth.clamp(targetPitch, -25.0F, 25.0F), 0.15F, 3.0F);
            this.setYRot(smoothYaw);
            this.setYHeadRot(smoothYaw);
            float body = com.civquest.mod.actuator.LookSmoothing.approach(this.yBodyRot, smoothYaw, 0.2F, 4.0F, 0.0F);
            this.yBodyRot = com.civquest.mod.actuator.LookSmoothing.clampBodyToHead(body, smoothYaw, 50.0F);
            this.setXRot(smoothPitch);
        }
    }

    /**
     * Applies strictly horizontal personal-space separation away from nearby alive, non-sleeping,
     * non-spectator {@link LivingEntity} instances (peers, human players, and mobs) while guarding
     * against solid obstacles, hazards, and >3-block cliffs.
     */
    public void applyHorizontalPeerSeparation() {
        net.minecraft.world.level.Level level = this.level();
        if (level == null || level.isClientSide || this.isRemoved() || !this.isAlive() || this.isSleeping()) {
            return;
        }
        if (this.motorFSM != null && this.motorFSM.isSweeping()) {
            return;
        }
        if (this.breaker != null && this.breaker.isBreaking()) {
            return;
        }
        net.minecraft.world.phys.AABB myBox = this.getBoundingBox();
        if (myBox == null) {
            return;
        }

        List<LivingEntity> nearbyEntities = level.getEntitiesOfClass(
                LivingEntity.class,
                myBox.inflate(1.5, 1.5, 1.5),
                e -> e != null && e != this && e.isAlive() && !e.isSleeping()
                        && !isEntitySpectatorSafe(e)
                        && !this.isPassengerOfSameVehicle(e)
                        && !(e instanceof net.minecraft.world.entity.decoration.ArmorStand)
        );
        if (nearbyEntities == null || nearbyEntities.isEmpty()) {
            return;
        }

        double curX = this.getX();
        double curY = this.getY();
        double curZ = this.getZ();

        double rx = 0.0;
        double rz = 0.0;
        int affectingCount = 0;
        List<LivingEntity> exactOverlapGroup = null;

        for (LivingEntity other : nearbyEntities) {
            net.minecraft.world.phys.AABB otherBox = other.getBoundingBox();
            if (otherBox != null) {
                if (otherBox.maxY <= myBox.minY + 1e-4 || otherBox.minY >= myBox.maxY - 1e-4) {
                    continue;
                }
            } else if (Math.abs(other.getY() - curY) >= 1.8) {
                continue;
            }
            double dx = curX - other.getX();
            double dz = curZ - other.getZ();
            double dist = Math.hypot(dx, dz);
            double combinedHalfWidths = (this.getBbWidth() + other.getBbWidth()) * 0.5;
            double requiredRadius = Math.max(PEER_SEPARATION_RADIUS, combinedHalfWidths + 0.15);
            if (dist >= requiredRadius) {
                continue;
            }
            affectingCount++;
            if (dist < 1e-4) {
                if (exactOverlapGroup == null) {
                    exactOverlapGroup = new ArrayList<>();
                    exactOverlapGroup.add(this);
                }
                exactOverlapGroup.add(other);
            } else {
                double weight = (requiredRadius - dist) / requiredRadius;
                rx += (dx / dist) * weight;
                rz += (dz / dist) * weight;
            }
        }

        if (affectingCount == 0) {
            return;
        }

        if (exactOverlapGroup != null) {
            exactOverlapGroup.sort(java.util.Comparator.comparing(
                    Entity::getUUID, java.util.Comparator.nullsLast(UUID::compareTo)));
            int rank = exactOverlapGroup.indexOf(this);
            if (rank < 0) {
                rank = 0;
            }
            int groupSize = exactOverlapGroup.size();
            UUID anchorUuid = exactOverlapGroup.get(0).getUUID();
            double baseOffset = anchorUuid != null
                    ? ((anchorUuid.hashCode() & 0xFFFF) * (2.0 * Math.PI / 65536.0))
                    : 0.0;
            double angle = baseOffset + (2.0 * Math.PI * rank) / groupSize;
            rx += Math.cos(angle);
            rz += Math.sin(angle);
        }

        double mag = Math.hypot(rx, rz);
        if (mag < 1e-6) {
            UUID selfId = this.getUUID();
            double tieAngle = selfId != null
                    ? ((selfId.hashCode() & 0xFFFF) * (2.0 * Math.PI / 65536.0))
                    : 0.0;
            rx = Math.cos(tieAngle);
            rz = Math.sin(tieAngle);
            mag = 1.0;
        }

        double ux = rx / mag;
        double uz = rz / mag;

        double chosenX = Double.NaN;
        double chosenZ = Double.NaN;
        if (isHorizontalSeparationSafe(level, curX, curY, curZ, curX + ux * PEER_SEPARATION_STEP, curZ + uz * PEER_SEPARATION_STEP)) {
            chosenX = ux * PEER_SEPARATION_STEP;
            chosenZ = uz * PEER_SEPARATION_STEP;
        } else {
            for (double rot : PEER_SEPARATION_FALLBACK_ANGLES_RAD) {
                double cos = Math.cos(rot);
                double sin = Math.sin(rot);
                double candUx = ux * cos - uz * sin;
                double candUz = ux * sin + uz * cos;
                double stepX = candUx * PEER_SEPARATION_STEP;
                double stepZ = candUz * PEER_SEPARATION_STEP;
                if (isHorizontalSeparationSafe(level, curX, curY, curZ, curX + stepX, curZ + stepZ)) {
                    chosenX = stepX;
                    chosenZ = stepZ;
                    break;
                }
            }
        }

        if (!Double.isFinite(chosenX) || !Double.isFinite(chosenZ)) {
            return;
        }

        boolean wasOnGround = this.onGround();
        this.move(net.minecraft.world.entity.MoverType.SELF, new net.minecraft.world.phys.Vec3(chosenX, 0.0, chosenZ));
        if (wasOnGround && hasDirectSupportBelow(level, this.getX(), this.getY(), this.getZ())) {
            this.setOnGround(true);
        }
    }

    private boolean isHorizontalSeparationSafe(
            net.minecraft.world.level.Level level,
            double curX, double curY, double curZ,
            double targetX, double targetZ) {
        if (level == null || !Double.isFinite(targetX) || !Double.isFinite(targetZ)) {
            return false;
        }
        try {
            if (level.getWorldBorder() != null && !level.getWorldBorder().isWithinBounds(targetX, targetZ)) {
                return false;
            }
        } catch (Throwable ignored) {
        }
        if (isFootprintSafe(level, curX, curY, curZ)) {
            return isFootprintSafe(level, targetX, curY, targetZ);
        }
        int feetY = net.minecraft.util.Mth.floor(curY + 0.05);
        return isColumnSafe(level, net.minecraft.util.Mth.floor(targetX), feetY, net.minecraft.util.Mth.floor(targetZ), curY);
    }

    private static boolean isFootprintSafe(net.minecraft.world.level.Level level, double x, double curY, double z) {
        int minBx = net.minecraft.util.Mth.floor(x - PEER_SEPARATION_FOOTPRINT_RADIUS + 1e-6);
        int maxBx = net.minecraft.util.Mth.floor(x + PEER_SEPARATION_FOOTPRINT_RADIUS - 1e-6);
        int minBz = net.minecraft.util.Mth.floor(z - PEER_SEPARATION_FOOTPRINT_RADIUS + 1e-6);
        int maxBz = net.minecraft.util.Mth.floor(z + PEER_SEPARATION_FOOTPRINT_RADIUS - 1e-6);
        int feetY = net.minecraft.util.Mth.floor(curY + 0.05);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                if (!isColumnSafe(level, bx, feetY, bz, curY)) {
                    return false;
                }
            }
        }
        return true;
    }

    private static boolean isColumnSafe(net.minecraft.world.level.Level level, int bx, int feetY, int bz, double curY) {
        net.minecraft.core.BlockPos feetPos = new net.minecraft.core.BlockPos(bx, feetY, bz);
        if (!level.hasChunkAt(feetPos)) {
            return false;
        }
        int headY = net.minecraft.util.Mth.floor(curY + 1.75);
        for (int y = feetY; y <= headY; y++) {
            net.minecraft.core.BlockPos pos = new net.minecraft.core.BlockPos(bx, y, bz);
            if (isSeparationHazard(level, pos) || isSolidObstacleForSeparation(level, pos)) {
                return false;
            }
        }
        for (int drop = 1; drop <= PEER_SEPARATION_MAX_SAFE_DROP + 1; drop++) {
            net.minecraft.core.BlockPos belowPos = new net.minecraft.core.BlockPos(bx, feetY - drop, bz);
            if (isSeparationHazard(level, belowPos)) {
                return false;
            }
            if (isSolidObstacleForSeparation(level, belowPos)) {
                return true;
            }
        }
        return false;
    }

    private static boolean isSolidObstacleForSeparation(net.minecraft.world.level.Level level, net.minecraft.core.BlockPos pos) {
        if (level == null || pos == null || !level.hasChunkAt(pos)) {
            return false;
        }
        try {
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

    private static boolean isSeparationHazard(net.minecraft.world.level.Level level, net.minecraft.core.BlockPos pos) {
        if (level == null || pos == null || !level.hasChunkAt(pos)) {
            return false;
        }
        try {
            net.minecraft.world.level.block.state.BlockState state = level.getBlockState(pos);
            if (state != null) {
                net.minecraft.world.level.material.FluidState fluid = state.getFluidState();
                if (fluid != null && (fluid.is(net.minecraft.tags.FluidTags.LAVA)
                        || fluid.is(net.minecraft.world.level.material.Fluids.LAVA)
                        || fluid.is(net.minecraft.world.level.material.Fluids.FLOWING_LAVA))) {
                    return true;
                }
                if (state.is(net.minecraft.world.level.block.Blocks.LAVA)
                        || state.is(net.minecraft.world.level.block.Blocks.FIRE)
                        || state.is(net.minecraft.world.level.block.Blocks.SOUL_FIRE)
                        || state.is(net.minecraft.world.level.block.Blocks.CAMPFIRE)
                        || state.is(net.minecraft.world.level.block.Blocks.SOUL_CAMPFIRE)
                        || state.is(net.minecraft.world.level.block.Blocks.MAGMA_BLOCK)
                        || state.is(net.minecraft.world.level.block.Blocks.CACTUS)
                        || state.is(net.minecraft.world.level.block.Blocks.SWEET_BERRY_BUSH)
                        || state.is(net.minecraft.world.level.block.Blocks.WITHER_ROSE)) {
                    return true;
                }
            }
            net.minecraft.world.level.material.FluidState levelFluid = level.getFluidState(pos);
            if (levelFluid != null && (levelFluid.is(net.minecraft.tags.FluidTags.LAVA)
                    || levelFluid.is(net.minecraft.world.level.material.Fluids.LAVA)
                    || levelFluid.is(net.minecraft.world.level.material.Fluids.FLOWING_LAVA))) {
                return true;
            }
        } catch (Throwable ignored) {
        }
        return false;
    }

    private static boolean hasDirectSupportBelow(net.minecraft.world.level.Level level, double x, double curY, double z) {
        if (level == null) {
            return false;
        }
        int minBx = net.minecraft.util.Mth.floor(x - PEER_SEPARATION_FOOTPRINT_RADIUS + 1e-6);
        int maxBx = net.minecraft.util.Mth.floor(x + PEER_SEPARATION_FOOTPRINT_RADIUS - 1e-6);
        int minBz = net.minecraft.util.Mth.floor(z - PEER_SEPARATION_FOOTPRINT_RADIUS + 1e-6);
        int maxBz = net.minecraft.util.Mth.floor(z + PEER_SEPARATION_FOOTPRINT_RADIUS - 1e-6);
        int groundY = net.minecraft.util.Mth.floor(curY - 0.05);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                if (isSolidObstacleForSeparation(level, new net.minecraft.core.BlockPos(bx, groundY, bz))) {
                    return true;
                }
            }
        }
        return false;
    }

    /**
     * Scans for nearby dropped ItemEntities within 2.5 blocks and picks them up into the agent's inventory,
     * auto-equipping weapons/shields/tools and attributing nearby player gifts deterministically.
     * Notifies the nearest player when the agent's inventory is full.
     */
    public void tryPickupNearbyItems() {
        if (this.level() == null || this.isRemoved()) {
            return;
        }
        List<net.minecraft.world.entity.item.ItemEntity> items = this.level().getEntitiesOfClass(
                net.minecraft.world.entity.item.ItemEntity.class,
                this.getBoundingBox().inflate(2.5, 1.5, 2.5),
                ie -> ie != null && ie.isAlive() && !ie.hasPickUpDelay() && !ie.getItem().isEmpty()
        );
        if (items.isEmpty()) {
            return;
        }
        net.minecraft.server.level.ServerPlayer nearestPlayer = null;
        if (this.getServer() != null && this.getServer().getPlayerList() != null) {
            double bestDistSq = 16.0 * 16.0;
            for (net.minecraft.server.level.ServerPlayer sp : this.getServer().getPlayerList().getPlayers()) {
                if (sp.level() == this.level()) {
                    double dSq = this.distanceToSqr(sp);
                    if (dSq <= bestDistSq) {
                        bestDistSq = dSq;
                        nearestPlayer = sp;
                    }
                }
            }
        }

        String agentName = this.getGameProfile().getName();
        for (net.minecraft.world.entity.item.ItemEntity ie : items) {
            net.minecraft.world.item.ItemStack copy = ie.getItem().copy();
            int origCount = copy.getCount();
            String itemName = copy.getHoverName().getString();
            this.getInventory().add(copy);
            int pickedCount = origCount - copy.getCount();
            if (pickedCount > 0) {
                if (copy.isEmpty()) {
                    ie.discard();
                } else {
                    ie.setItem(copy);
                    ie.setNoPickUpDelay();
                }
                autoEquipBestGear();
                net.minecraft.world.entity.Entity owner = ie.getOwner();
                UUID thrower = owner != null ? owner.getUUID() : null;
                if (nearestPlayer != null && thrower != null && thrower.equals(nearestPlayer.getUUID())) {
                    nearestPlayer.sendSystemMessage(Component.literal(
                            "<" + agentName + "> Thank you for the " + pickedCount + "x " + itemName + ", " + nearestPlayer.getName().getString() + "!"));
                    queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                            .setSenderUuid(nearestPlayer.getUUID().toString())
                            .setSenderName(nearestPlayer.getName().getString())
                            .setMessage("[DETERMINISTIC_GIFT] Gifted " + pickedCount + "x " + itemName + " to " + agentName + ".")
                            .setDistance((float) Math.sqrt(this.distanceToSqr(nearestPlayer)))
                            .build());
                    if (!copy.isEmpty()) {
                        nearestPlayer.sendSystemMessage(Component.literal(
                                "<" + agentName + "> My pack is full, " + nearestPlayer.getName().getString() + "! I couldn't fit the remaining " + copy.getCount() + "x " + itemName + " — please pick them back up."));
                        this.lastInventoryFullNoticeTick = this.tickCount;
                    }
                }
            } else {
                ie.setNoPickUpDelay();
                net.minecraft.world.entity.Entity owner = ie.getOwner();
                UUID thrower = owner != null ? owner.getUUID() : null;
                if (nearestPlayer != null && thrower != null && thrower.equals(nearestPlayer.getUUID()) && (this.tickCount - this.lastInventoryFullNoticeTick >= 100)) {
                    nearestPlayer.sendSystemMessage(Component.literal(
                            "<" + agentName + "> My pack is completely full, " + nearestPlayer.getName().getString() + "! I can't carry the " + origCount + "x " + itemName + " — please pick it back up."));
                    this.lastInventoryFullNoticeTick = this.tickCount;
                }
            }
        }
    }

    /**
     * Equips the highest destroy-speed tool in the agent's inventory for the given {@link net.minecraft.world.level.block.state.BlockState}
     * into the selected hotbar slot ({@code inv.selected}).
     *
     * @return true if a tool faster than bare hands (> 1.0F) was found and equipped
     */
    public boolean equipBestToolFor(net.minecraft.world.level.block.state.BlockState state) {
        if (state == null || this.getInventory() == null) {
            return false;
        }
        int slot = com.civquest.mod.actuator.BlockBreaker.bestToolSlot(this, state);
        if (slot < 0) {
            return false;
        }
        com.civquest.mod.actuator.BlockBreaker.equipBestTool(this, state);
        return true;
    }

    /**
     * Scans the agent's inventory and equips the best melee weapon/tool into slot 0 (selected hotbar mainhand)
     * and any shield into the offhand slot.
     */
    public void autoEquipBestGear() {
        net.minecraft.world.entity.player.Inventory inv = this.getInventory();
        // Swapping the rod out of the main hand would discard a cast fishing line (cq-390.4).
        if (inv == null || this.fishing != null) {
            return;
        }
        // 1. Equip shield into offhand if not already holding one
        net.minecraft.world.item.ItemStack offhand = this.getOffhandItem();
        if (!offhand.is(net.minecraft.world.item.Items.SHIELD)) {
            for (int i = 0; i < inv.items.size(); i++) {
                net.minecraft.world.item.ItemStack s = inv.items.get(i);
                if (s.is(net.minecraft.world.item.Items.SHIELD)) {
                    inv.items.set(i, offhand);
                    this.setItemInHand(net.minecraft.world.InteractionHand.OFF_HAND, s);
                    break;
                }
            }
        }

        if (this.breaker != null && this.breaker.isBreaking()) {
            return;
        }

        // 2. Equip best weapon/tool into selected hotbar slot (0)
        int bestSlot = -1;
        int bestScore = scoreWeaponOrTool(inv.items.get(inv.selected));
        for (int i = 0; i < inv.items.size(); i++) {
            int score = scoreWeaponOrTool(inv.items.get(i));
            if (score > bestScore) {
                bestScore = score;
                bestSlot = i;
            }
        }
        if (bestSlot >= 0 && bestSlot != inv.selected) {
            net.minecraft.world.item.ItemStack current = inv.items.get(inv.selected);
            net.minecraft.world.item.ItemStack best = inv.items.get(bestSlot);
            inv.items.set(inv.selected, best);
            inv.items.set(bestSlot, current);
        }
    }

    private static int scoreWeaponOrTool(net.minecraft.world.item.ItemStack stack) {
        if (stack == null || stack.isEmpty()) {
            return 0;
        }
        String id = net.minecraft.core.registries.BuiltInRegistries.ITEM.getKey(stack.getItem()).toString();
        if (id.contains("netherite_sword")) return 100;
        if (id.contains("diamond_sword")) return 90;
        if (id.contains("netherite_axe") || id.contains("diamond_axe")) return 85;
        if (id.contains("iron_sword")) return 80;
        if (id.contains("iron_axe")) return 75;
        if (id.contains("stone_sword")) return 65;
        if (id.contains("stone_axe")) return 60;
        if (id.contains("wooden_sword") || id.contains("golden_sword")) return 50;
        if (id.contains("wooden_axe") || id.contains("pickaxe")) return 40;
        if (id.contains("shovel")) return 35;
        return 0;
    }

    @Override
    public void checkDespawn() {
        // Prevent natural despawn for autonomous agents
    }

    @Override
    public boolean canCollideWith(Entity other) {
        if (other == null || other == this || this.isPassengerOfSameVehicle(other)) {
            return false;
        }
        if (other instanceof LivingEntity) {
            return !other.isRemoved() && other.isAlive() && !isEntitySpectatorSafe(other);
        }
        return super.canCollideWith(other);
    }

    @Override
    public boolean canBeCollidedWith() {
        return !this.isRemoved() && this.isAlive() && !isEntitySpectatorSafe(this);
    }

    @Override
    public boolean isPushable() {
        boolean climbable = false;
        try {
            climbable = this.onClimbable();
        } catch (Throwable ignored) {
        }
        return this.isAlive() && !isEntitySpectatorSafe(this) && !climbable;
    }

    @Override
    public void push(Entity other) {
        if (other == null || other == this || this.isSleeping() || this.isPassengerOfSameVehicle(other)) {
            return;
        }
        if (other.noPhysics || this.noPhysics) {
            return;
        }
        if (other instanceof LivingEntity living
                && (!living.isAlive() || living.isSleeping() || isEntitySpectatorSafe(living))) {
            return;
        }
        double dx = other.getX() - this.getX();
        double dz = other.getZ() - this.getZ();
        double maxAbs = net.minecraft.util.Mth.absMax(dx, dz);
        if (maxAbs < 0.01) {
            // Deterministic horizontal tie-breaker when co-located; never apply vertical lift.
            UUID selfId = this.getUUID();
            UUID otherId = other.getUUID();
            double angle = selfId != null
                    ? ((selfId.hashCode() & 0xFFFF) * (2.0 * Math.PI / 65536.0))
                    : 0.0;
            if (selfId != null && otherId != null && selfId.compareTo(otherId) > 0) {
                angle += Math.PI;
            }
            double pushX = Math.cos(angle) * 0.05;
            double pushZ = Math.sin(angle) * 0.05;
            if (!this.isVehicle() && this.isPushable()) {
                this.push(-pushX, 0.0, -pushZ);
            }
            if (!other.isVehicle() && other.isPushable()) {
                other.push(pushX, 0.0, pushZ);
            }
            return;
        }
        super.push(other);
    }

    private static boolean isEntitySpectatorSafe(Entity entity) {
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

    @Override
    public boolean canUsePortal(boolean allowVehicles) {
        // Autonomous agents are tethered to their assigned dimension
        return false;
    }

    @Override
    public boolean isInvulnerableTo(DamageSource source) {
        if (this.isRemoved()) {
            return true;
        }
        boolean isInvul = false;
        if (this.isInvulnerable() && !source.is(DamageTypeTags.BYPASSES_INVULNERABILITY) && !source.isCreativePlayer()) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_FIRE) && this.fireImmune()) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_FALL) &&
                (!this.level().getGameRules().getBoolean(GameRules.RULE_FALL_DAMAGE)
                        || this.getType().is(EntityTypeTags.FALL_DAMAGE_IMMUNE))) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_DROWNING) &&
                !this.level().getGameRules().getBoolean(GameRules.RULE_DROWNING_DAMAGE)) {
            isInvul = true;
        } else if (this.level() instanceof ServerLevel serverLevel &&
                EnchantmentHelper.isImmuneToDamage(serverLevel, this, source)) {
            isInvul = true;
        }
        return CommonHooks.isEntityInvulnerableTo(this, source, isInvul);
    }

    @Override
    public boolean hurt(DamageSource source, float amount) {
        clearSpawnInvulnerability();
        boolean hurt = super.hurt(source, amount);
        if (hurt && this.level() != null && !this.level().isClientSide && this.isAlive()
                && this.motorFSM != null && source != null) {
            boolean hazardSource = source.getEntity() != null
                    || source.getDirectEntity() instanceof net.minecraft.world.entity.projectile.Projectile;
            if (hazardSource) {
                this.motorFSM.preemptWithCombatReflex("damage_reflex_" + source.getMsgId());
            }
        }
        return hurt;
    }

    @Override
    public boolean canHarmPlayer(Player other) {
        if (other == null) {
            return false;
        }
        if (this.getServer() != null && !this.getServer().isPvpAllowed()) {
            return false;
        }
        if (other instanceof AgentEntity otherAgent
                && this.clanId != null && !this.clanId.isBlank()
                && this.clanId.equals(otherAgent.clanId)) {
            return false;
        }
        Team team = this.getTeam();
        Team otherTeam = other.getTeam();
        if (team != null) {
            if (team.isAlliedTo(otherTeam) && !team.isAllowFriendlyFire()) {
                return false;
            }
        }
        return true;
    }

    @Override
    public void die(DamageSource cause) {
        if (this.isRemoved() || this.dead) {
            return;
        }
        String causeMsg = cause != null ? cause.getLocalizedDeathMessage(this).getString() : "unknown";
        LOGGER.info("AgentEntity died: agentId='{}', clanId='{}', cause='{}'",
                agentId, clanId, causeMsg);

        if (CommonHooks.onLivingDeath(this, cause)) {
            return;
        }
        this.dead = true;

        this.gameEvent(GameEvent.ENTITY_DIE);

        // Emit death notice or broadcast to all connected players
        if (this.getServer() != null && this.getServer().getPlayerList() != null) {
            Component deathMessage = this.getCombatTracker().getDeathMessage();
            this.getServer().getPlayerList().broadcastSystemMessage(deathMessage, false);
        }

        // Construct DeathEvent and emit zero-latency terminal telemetry frame immediately before stream closure
        if (this.streamRegistry != null && this.streamRegistry.isRegistered(this.agentId)) {
            try {
                DeathEvent deathEvent = SensoryFrustumCollector.buildDeathEvent(this, cause);
                SensoryFrustumCollector collector = this.sensoryCollector != null
                        ? this.sensoryCollector
                        : new SensoryFrustumCollector();
                TelemetryFrame deathFrame = collector.collectFrame(this, null, deathEvent);
                this.streamRegistry.sendCriticalFrame(this.agentId, deathFrame);
            } catch (Exception e) {
                LOGGER.error("Error sending terminal death telemetry frame for agent '{}': {}",
                        this.agentId, e.getMessage(), e);
            }
        }

        // Drop death inventory if not spectator
        if (!this.isSpectator() && !this.level().isClientSide) {
            this.dropAllDeathLoot(this.serverLevel(), cause);
        }

        // Notify watchdog and motor state machines
        if (this.watchdogFSM != null) {
            this.watchdogFSM.onAgentDeath();
        }
        if (this.motorFSM != null) {
            this.motorFSM.flush();
        }

        // Trigger unregistration / cleanup callback
        if (cleanedUp.compareAndSet(false, true)) {
            if (this.deathListener != null) {
                try {
                    this.deathListener.accept(this);
                } catch (Exception e) {
                    LOGGER.error("Error executing deathListener for agent '{}': {}", agentId, e.getMessage(), e);
                }
            }
        }

        this.discard();
    }

    @Override
    public void remove(RemovalReason reason) {
        super.remove(reason);
        if (this.watchdogFSM != null) {
            this.watchdogFSM.onAgentDespawn();
        }
        if (this.motorFSM != null) {
            this.motorFSM.flush();
        }
        if (cleanedUp.compareAndSet(false, true)) {
            if (this.removalListener != null) {
                try {
                    this.removalListener.accept(this);
                } catch (Exception e) {
                    LOGGER.error("Error executing removalListener for agent '{}': {}", agentId, e.getMessage(), e);
                }
            }
        }
    }

    @Override
    public void setPos(double x, double y, double z) {
        super.setPos(x, y, z);
        followAsPlayer();
    }

    /**
     * Moves the server's player records to the agent. Adding an agent to a level registers it there
     * as a player, which loads and ticks the chunks round it and lets mobs spawn and blocks tick at
     * random nearby. Vanilla moves that registration only for a connected client (movement packets,
     * {@code ServerPlayer.tick}), and an agent has neither: left alone it stays at the spawn point
     * and an agent that walks away gets none of this. Removal from the level releases it.
     */
    private void followAsPlayer() {
        // setPos also runs before the agent is in a level, when there is no record to move.
        if (this.isAddedToLevel() && !this.isRemoved() && this.level() instanceof ServerLevel serverLevel
                && !SectionPos.of(this).equals(this.getLastSectionPos())) {
            serverLevel.getChunkSource().move(this);
        }
    }

    public String getAgentId() {
        return agentId;
    }

    public String getClanId() {
        return clanId;
    }

    public String getSkinId() {
        return skinId;
    }

    public void setSkinId(String skinId) {
        this.skinId = skinId;
    }

    public byte[] getSkinPngBytes() {
        return skinPngBytes;
    }

    public void setSkinPngBytes(byte[] skinPngBytes) {
        this.skinPngBytes = skinPngBytes;
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public void setStreamRegistry(StreamRegistry streamRegistry) {
        this.streamRegistry = streamRegistry;
    }

    public SensoryFrustumCollector getSensoryCollector() {
        return sensoryCollector;
    }

    public void setSensoryCollector(SensoryFrustumCollector sensoryCollector) {
        this.sensoryCollector = sensoryCollector;
    }

    public com.civquest.mod.actuator.IntentActuator getIntentActuator() {
        return intentActuator;
    }

    public void setIntentActuator(com.civquest.mod.actuator.IntentActuator intentActuator) {
        this.intentActuator = intentActuator;
    }

    public void setTelemetryContext(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector) {
        this.streamRegistry = streamRegistry;
        this.sensoryCollector = sensoryCollector;
    }

    public void setDeathListener(Consumer<AgentEntity> deathListener) {
        this.deathListener = deathListener;
    }

    public void setRemovalListener(Consumer<AgentEntity> removalListener) {
        this.removalListener = removalListener;
    }

    /** Current stamina (0-100) from the navigator, for debug telemetry. */
    public double getStamina() {
        return navigator != null ? navigator.getStamina() : AgentNavigator.STAMINA_MAX;
    }

    public com.civquest.mod.actuator.BlockBreaker getBreaker() {
        return breaker;
    }

    public AgentNavigator getNavigator() {
        return navigator;
    }

    public AgentMotorFSM getMotorFSM() {
        return motorFSM;
    }

    public AgentWatchdogFSM getWatchdogFSM() {
        return watchdogFSM;
    }
}
