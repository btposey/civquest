package com.civquest.mod;

import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.mojang.authlib.GameProfile;
import com.mojang.authlib.properties.Property;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoRemovePacket;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoUpdatePacket;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.util.Mth;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.levelgen.Heightmap;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.event.tick.ServerTickEvent;
import org.slf4j.Logger;

import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Comparator;
import java.util.EnumSet;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.RejectedExecutionException;
import java.util.function.Supplier;

/**
 * Manages active {@link AgentEntity} instances, server-thread lifecycle scheduling,
 * and skin packet broadcasting.
 */
public class AgentEntityManager {
    private static final Logger LOGGER = LogUtils.getLogger();

    private final ConcurrentHashMap<String, AgentEntity> agents = new ConcurrentHashMap<>();
    private volatile StreamRegistry streamRegistry;
    private final SensoryFrustumCollector sensoryCollector;
    private final IntentActuator intentActuator;
    private volatile SpawnListener spawnListener;
    private AgentEntityFactory entityFactory = AgentEntity::new;

    @FunctionalInterface
    public interface AgentEntityFactory {
        AgentEntity create(ServerLevel level, GameProfile profile, String agentId, String clanId);
    }

    public void setAgentEntityFactory(AgentEntityFactory entityFactory) {
        this.entityFactory = entityFactory != null ? entityFactory : AgentEntity::new;
    }

    public interface SpawnListener {
        void onPlayerInfoBroadcast(ClientboundPlayerInfoUpdatePacket packet);
        void onEntityAdded(AgentEntity entity);
    }

    public void setSpawnListener(SpawnListener spawnListener) {
        this.spawnListener = spawnListener;
    }

    public AgentEntityManager() {
        this(null, new SensoryFrustumCollector(), new IntentActuator());
    }

    public AgentEntityManager(StreamRegistry streamRegistry) {
        this(streamRegistry, new SensoryFrustumCollector(), new IntentActuator());
    }

    public AgentEntityManager(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector) {
        this(streamRegistry, sensoryCollector, new IntentActuator());
    }

    public AgentEntityManager(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector, IntentActuator intentActuator) {
        this.streamRegistry = streamRegistry;
        this.sensoryCollector = sensoryCollector != null ? sensoryCollector : new SensoryFrustumCollector();
        this.intentActuator = intentActuator != null ? intentActuator : new IntentActuator();
    }

    public IntentActuator getIntentActuator() {
        return intentActuator;
    }

    public SensoryFrustumCollector getSensoryCollector() {
        return sensoryCollector;
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public void setStreamRegistry(StreamRegistry streamRegistry) {
        this.streamRegistry = streamRegistry;
        for (AgentEntity agent : agents.values()) {
            agent.setStreamRegistry(streamRegistry);
        }
    }

    public CompletableFuture<AgentEntity> spawnAgent(MinecraftServer server, SpawnAgentRequest request) {
        return spawnAgent(server, null, request);
    }

    public CompletableFuture<AgentEntity> spawnAgent(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        Supplier<AgentEntity> spawnSupplier = () -> {
            ServerLevel targetLevel = level;
            if (targetLevel == null && server != null) {
                targetLevel = server.overworld();
                if (targetLevel == null && server.getAllLevels().iterator().hasNext()) {
                    targetLevel = server.getAllLevels().iterator().next();
                }
            }
            return doSpawnAgent(server, targetLevel, request);
        };
        if (server != null) {
            return server.submit(spawnSupplier);
        } else {
            try {
                return CompletableFuture.completedFuture(spawnSupplier.get());
            } catch (Throwable t) {
                CompletableFuture<AgentEntity> future = new CompletableFuture<>();
                future.completeExceptionally(t);
                return future;
            }
        }
    }

    private AgentEntity doSpawnAgent(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        String agentId = request.getAgentId();
        String displayName = request.getDisplayName();
        if (displayName == null || displayName.isBlank()) {
            displayName = agentId;
        }

        // Check if agentId already exists (case-insensitively): cleanly discard and despawn old entity before spawning
        String existingKey = findCanonicalAgentKey(agentId);
        AgentEntity existing = existingKey != null ? agents.remove(existingKey) : null;
        if (existing != null) {
            LOGGER.warn("Duplicate agent spawn detected for agentId='{}' (existing='{}'). Despawning old entity (uuid='{}')",
                    agentId, existingKey, existing.getUUID());
            if (existing.getWatchdogFSM() != null) {
                existing.getWatchdogFSM().onAgentDespawn();
            }
            existing.releaseHeldChunk();
            existing.discard();
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(existing.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        }

        UUID offlineUuid = AgentEntity.createOfflineUUID(displayName);
        GameProfile profile = new GameProfile(offlineUuid, displayName);

        if (request.getSkinTextureValue() != null && !request.getSkinTextureValue().isBlank()) {
            String textureValue = request.getSkinTextureValue().trim();
            if (textureValue.length() > 4096) {
                throw new IllegalArgumentException("skin_texture_value exceeds maximum allowed length of 4096 characters");
            }
            try {
                Base64.getDecoder().decode(textureValue);
            } catch (IllegalArgumentException e) {
                throw new IllegalArgumentException("skin_texture_value is not valid Base64", e);
            }
            String sig = (request.getSkinSignature() != null && !request.getSkinSignature().isBlank())
                    ? request.getSkinSignature()
                    : null;
            profile.getProperties().put("textures", new Property("textures", textureValue, sig));
        }

        AgentEntity agent = entityFactory.create(level, profile, agentId, request.getClanId());
        agent.setStreamRegistry(this.streamRegistry);
        agent.setSensoryCollector(this.sensoryCollector);
        agent.setIntentActuator(this.intentActuator);

        if (request.getSkinPngBytes() != null && !request.getSkinPngBytes().isEmpty()) {
            if (request.getSkinPngBytes().size() > 65536) {
                throw new IllegalArgumentException("skin_png_bytes exceeds maximum allowed size of 65536 bytes (was "
                        + request.getSkinPngBytes().size() + ")");
            }
            agent.setSkinPngBytes(request.getSkinPngBytes().toByteArray());
        }

        double x = 0.0;
        double y = 0.0;
        double z = 0.0;
        if (request.hasSpawnCoords()) {
            x = request.getSpawnCoords().getX();
            y = request.getSpawnCoords().getY();
            z = request.getSpawnCoords().getZ();
        } else if (level != null) {
            BlockPos spawnPos = level.getSharedSpawnPos();
            if (spawnPos == null) {
                spawnPos = BlockPos.ZERO;
            }
            int existingCount = agents.size();
            double offsetX = (existingCount % 4) * 1.5;
            double offsetZ = ((existingCount / 4) % 4) * 1.5;
            x = spawnPos.getX() + 0.5 + offsetX;
            z = spawnPos.getZ() + 0.5 + offsetZ;
            int surfaceX = Mth.floor(x);
            int surfaceZ = Mth.floor(z);
            y = level.getHeight(Heightmap.Types.MOTION_BLOCKING_NO_LEAVES, surfaceX, surfaceZ);
        }

        Vec3 resolvedSpawn = findSafeUnoccupiedSpawnPos(level, x, y, z);
        x = resolvedSpawn.x;
        y = resolvedSpawn.y;
        z = resolvedSpawn.z;

        agent.moveTo(x, y, z, 0.0f, 0.0f);
        agent.setYRot(0.0f);
        agent.setXRot(0.0f);
        agent.setYHeadRot(0.0f);

        // Broadcast player info packet to connected tracking clients BEFORE adding entity to level.
        // Minecraft client requires PlayerInfo to exist in playerInfoMap before processing ClientboundAddEntityPacket.
        // Broadcasting this first prevents "Server attempted to add player prior to sending player info"
        // and "Skipping Entity with id entity.minecraft.player".
        ClientboundPlayerInfoUpdatePacket infoPacket = null;
        try {
            infoPacket = new ClientboundPlayerInfoUpdatePacket(
                    EnumSet.of(
                            ClientboundPlayerInfoUpdatePacket.Action.ADD_PLAYER,
                            ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LISTED,
                            ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LATENCY,
                            ClientboundPlayerInfoUpdatePacket.Action.UPDATE_GAME_MODE
                    ),
                    List.of(agent)
            );
        } catch (Throwable t) {
            LOGGER.debug("Could not construct ClientboundPlayerInfoUpdatePacket (headless/mock environment): {}", t.getMessage());
        }

        if (infoPacket != null) {
            if (server != null && server.getPlayerList() != null) {
                server.getPlayerList().broadcastAll(infoPacket);
            }
            if (spawnListener != null) {
                spawnListener.onPlayerInfoBroadcast(infoPacket);
            }
        } else if (spawnListener != null) {
            spawnListener.onPlayerInfoBroadcast(null);
        }

        // Broadcast custom skin payload packet if custom PNG bytes are supplied
        if (agent.getSkinPngBytes() != null && agent.getSkinPngBytes().length > 0) {
            ClientboundAgentSkinPacket skinPacket = new ClientboundAgentSkinPacket(
                    agent.getUUID(),
                    agent.getAgentId(),
                    agent.getSkinPngBytes()
            );
            if (server != null && server.getPlayerList() != null) {
                server.getPlayerList().broadcastAll(skinPacket.toVanillaClientbound());
            }
        }

        if (level != null) {
            ChunkPos chunkPos = new ChunkPos(BlockPos.containing(x, y, z));
            agent.holdChunk(chunkPos);
            level.getChunk(chunkPos.x, chunkPos.z); // loaded before the agent enters it
            boolean added = false;
            try {
                added = level.addFreshEntity(agent);
            } catch (Throwable t) {
                LOGGER.error("Unexpected error adding AgentEntity to level for agent '{}': {}", agentId, t.getMessage(), t);
                added = false;
            }
            if (!added) {
                agent.releaseHeldChunk();
                if (server != null && server.getPlayerList() != null) {
                    ClientboundPlayerInfoRemovePacket removePacket =
                            new ClientboundPlayerInfoRemovePacket(List.of(agent.getUUID()));
                    server.getPlayerList().broadcastAll(removePacket);
                }
                throw new IllegalStateException("Failed to add AgentEntity to level");
            }
        }
        if (spawnListener != null) {
            spawnListener.onEntityAdded(agent);
        }

        agent.setDeathListener(deadAgent -> {
            if (deadAgent.getWatchdogFSM() != null) {
                deadAgent.getWatchdogFSM().onAgentDeath();
            }
            deadAgent.releaseHeldChunk();
            agents.remove(deadAgent.getAgentId(), deadAgent);
            if (streamRegistry != null) {
                streamRegistry.close(deadAgent.getAgentId());
            }
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(deadAgent.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        });

        agent.setRemovalListener(removedAgent -> {
            if (removedAgent.getWatchdogFSM() != null) {
                removedAgent.getWatchdogFSM().onAgentDespawn();
            }
            removedAgent.releaseHeldChunk();
            agents.remove(removedAgent.getAgentId(), removedAgent);
            if (streamRegistry != null) {
                streamRegistry.close(removedAgent.getAgentId());
            }
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(removedAgent.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        });

        agents.put(agentId, agent);
        LOGGER.info("AgentEntity registered and spawned: agentId='{}', uuid='{}', entityId={}",
                agentId, agent.getUUID(), agent.getId());
        return agent;
    }

    private static final int SPAWN_DECONFLICT_MAX_RADIUS = 8;
    private static final double MIN_SPAWN_ENTITY_SEPARATION = 1.0;

    /**
     * Resolves the nearest safe, standable, non-hazardous, and horizontally unoccupied spawn
     * coordinate starting from {@code (desiredX, desiredY, desiredZ)} and spiraling outward.
     */
    public Vec3 findSafeUnoccupiedSpawnPos(ServerLevel level, double desiredX, double desiredY, double desiredZ) {
        int baseBx = Mth.floor(desiredX);
        int baseBy = Mth.floor(desiredY + 0.05);
        int baseBz = Mth.floor(desiredZ);

        Vec3 tier2Fallback = null;

        // Ring 0: exact desired coordinate first
        Vec3 exact = evaluateSpawnColumn(level, desiredX, desiredY, desiredZ, baseBy, false);
        if (exact != null) {
            return exact;
        }
        if (isSpawnSpotUnoccupied(level, desiredX, desiredY, desiredZ)
                && isSpawnSpotPassableAndHazardFree(level, desiredX, baseBy, desiredZ)) {
            tier2Fallback = new Vec3(desiredX, desiredY, desiredZ);
        }

        // Rings 1..SPAWN_DECONFLICT_MAX_RADIUS sorted by squared horizontal distance
        for (int radius = 1; radius <= SPAWN_DECONFLICT_MAX_RADIUS; radius++) {
            List<int[]> ringOffsets = new ArrayList<>();
            for (int dx = -radius; dx <= radius; dx++) {
                for (int dz = -radius; dz <= radius; dz++) {
                    if (Math.max(Math.abs(dx), Math.abs(dz)) == radius) {
                        ringOffsets.add(new int[]{dx, dz});
                    }
                }
            }
            ringOffsets.sort(Comparator.comparingInt(o -> o[0] * o[0] + o[1] * o[1]));

            for (int[] offset : ringOffsets) {
                double candX = baseBx + offset[0] + 0.5;
                double candZ = baseBz + offset[1] + 0.5;
                Vec3 idealSameY = evaluateSpawnColumnAtY(level, candX, candZ, baseBy);
                if (idealSameY != null) {
                    return idealSameY;
                }
            }
            for (int[] offset : ringOffsets) {
                double candX = baseBx + offset[0] + 0.5;
                double candZ = baseBz + offset[1] + 0.5;
                Vec3 ideal = evaluateSpawnColumn(level, candX, desiredY, candZ, baseBy, true);
                if (ideal != null) {
                    return ideal;
                }
                if (tier2Fallback == null
                        && isSpawnSpotUnoccupied(level, candX, desiredY, candZ)
                        && isSpawnSpotPassableAndHazardFree(level, candX, baseBy, candZ)) {
                    tier2Fallback = new Vec3(candX, desiredY, candZ);
                }
            }
        }

        return tier2Fallback != null ? tier2Fallback : new Vec3(desiredX, desiredY, desiredZ);
    }

    private Vec3 evaluateSpawnColumnAtY(ServerLevel level, double candX, double candZ, int by) {
        if (level == null) {
            return null;
        }
        double candY = (double) by;
        if (hasSpawnStandableFloorAndNoCliff(level, candX, by, candZ)
                && isSpawnSpotPassableAndHazardFree(level, candX, by, candZ)
                && isSpawnSpotUnoccupied(level, candX, candY, candZ)) {
            return new Vec3(candX, candY, candZ);
        }
        return null;
    }

    private Vec3 evaluateSpawnColumn(
            ServerLevel level, double candX, double desiredY, double candZ, int baseBy, boolean snapYToBlock) {
        if (level == null) {
            return null;
        }
        List<Integer> candidateYs = new ArrayList<>(6);
        candidateYs.add(baseBy);
        if (snapYToBlock) {
            candidateYs.add(baseBy + 1);
            candidateYs.add(baseBy - 1);
            candidateYs.add(baseBy + 2);
            candidateYs.add(baseBy - 2);
            try {
                int hmY = level.getHeight(Heightmap.Types.MOTION_BLOCKING_NO_LEAVES, Mth.floor(candX), Mth.floor(candZ));
                if (hmY > level.getMinBuildHeight() && Math.abs(hmY - baseBy) <= 6 && !candidateYs.contains(hmY)) {
                    candidateYs.add(hmY);
                }
            } catch (Throwable ignored) {
            }
        }

        for (int by : candidateYs) {
            double candY = (!snapYToBlock && by == baseBy) ? desiredY : (double) by;
            if (hasSpawnStandableFloorAndNoCliff(level, candX, by, candZ)
                    && isSpawnSpotPassableAndHazardFree(level, candX, by, candZ)
                    && isSpawnSpotUnoccupied(level, candX, candY, candZ)) {
                return new Vec3(candX, candY, candZ);
            }
        }
        return null;
    }

    private boolean isSpawnSpotUnoccupied(ServerLevel level, double candX, double candY, double candZ) {
        for (AgentEntity existing : agents.values()) {
            if (conflictsWithSpawnPos(existing, candX, candY, candZ)) {
                return false;
            }
        }
        if (level == null) {
            return true;
        }
        try {
            for (var player : level.players()) {
                if (conflictsWithSpawnPos(player, candX, candY, candZ)) {
                    return false;
                }
            }
        } catch (Throwable ignored) {
        }
        try {
            AABB searchBox = new AABB(
                    candX - 2.0, candY - 2.0, candZ - 2.0,
                    candX + 2.0, candY + 3.0, candZ + 2.0
            );
            List<LivingEntity> nearby = level.getEntitiesOfClass(
                    LivingEntity.class,
                    searchBox,
                    e -> e != null && !e.isRemoved() && e.isAlive()
                            && !(e instanceof net.minecraft.world.entity.decoration.ArmorStand)
            );
            if (nearby != null) {
                for (LivingEntity entity : nearby) {
                    if (conflictsWithSpawnPos(entity, candX, candY, candZ)) {
                        return false;
                    }
                }
            }
        } catch (Throwable ignored) {
        }
        return true;
    }

    private static boolean conflictsWithSpawnPos(Entity entity, double candX, double candY, double candZ) {
        if (entity == null || entity.isRemoved() || !entity.isAlive()) {
            return false;
        }
        try {
            if (entity instanceof net.minecraft.server.level.ServerPlayer sp && sp.gameMode != null && sp.isSpectator()) {
                return false;
            }
        } catch (Throwable ignored) {
        }
        if (entity instanceof net.minecraft.world.entity.decoration.ArmorStand) {
            return false;
        }
        double otherHeight = Math.max(1.8, entity.getBbHeight());
        if (candY + 1.8 <= entity.getY() + 1e-4 || candY >= entity.getY() + otherHeight - 1e-4) {
            return false;
        }
        double minDist = Math.max(MIN_SPAWN_ENTITY_SEPARATION, 0.3 + entity.getBbWidth() * 0.5 + 0.1);
        double dist = Math.hypot(entity.getX() - candX, entity.getZ() - candZ);
        return dist < minDist;
    }

    private static boolean isSpawnSpotPassableAndHazardFree(ServerLevel level, double candX, int by, double candZ) {
        if (level == null) {
            return true;
        }
        int minBx = Mth.floor(candX - 0.25);
        int maxBx = Mth.floor(candX + 0.25);
        int minBz = Mth.floor(candZ - 0.25);
        int maxBz = Mth.floor(candZ + 0.25);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                BlockPos feetPos = new BlockPos(bx, by, bz);
                BlockPos headPos = new BlockPos(bx, by + 1, bz);
                BlockPos belowPos = new BlockPos(bx, by - 1, bz);
                if (isSpawnSolid(level, feetPos) || isSpawnSolid(level, headPos)) {
                    return false;
                }
                if (isSpawnHazard(level, feetPos) || isSpawnHazard(level, headPos) || isSpawnHazard(level, belowPos)) {
                    return false;
                }
                if (isSpawnWater(level, feetPos) || isSpawnWater(level, belowPos)) {
                    // Reject deep water (> 2 blocks deep)
                    boolean shallowFloor = isSpawnSolid(level, new BlockPos(bx, by - 1, bz))
                            || isSpawnSolid(level, new BlockPos(bx, by - 2, bz))
                            || isSpawnSolid(level, new BlockPos(bx, by - 3, bz));
                    if (!shallowFloor || isSpawnWater(level, headPos)) {
                        return false;
                    }
                }
            }
        }
        return true;
    }

    private static boolean hasSpawnStandableFloorAndNoCliff(ServerLevel level, double candX, int by, double candZ) {
        if (level == null) {
            return false;
        }
        int minBx = Mth.floor(candX - 0.25);
        int maxBx = Mth.floor(candX + 0.25);
        int minBz = Mth.floor(candZ - 0.25);
        int maxBz = Mth.floor(candZ + 0.25);
        for (int bx = minBx; bx <= maxBx; bx++) {
            for (int bz = minBz; bz <= maxBz; bz++) {
                BlockPos belowPos = new BlockPos(bx, by - 1, bz);
                if (isSpawnHazard(level, belowPos) || !isSpawnSolid(level, belowPos)) {
                    return false;
                }
            }
        }
        return true;
    }

    private static boolean isSpawnSolid(ServerLevel level, BlockPos pos) {
        try {
            BlockState state = level.getBlockState(pos);
            if (state == null || state.isAir()) {
                return false;
            }
            var shape = state.getCollisionShape(level, pos);
            return shape != null && !shape.isEmpty();
        } catch (Throwable ignored) {
            return false;
        }
    }

    private static boolean isSpawnWater(ServerLevel level, BlockPos pos) {
        try {
            BlockState state = level.getBlockState(pos);
            if (state == null) {
                return false;
            }
            var fluid = state.getFluidState();
            return (fluid != null && fluid.is(net.minecraft.tags.FluidTags.WATER))
                    || state.is(Blocks.WATER);
        } catch (Throwable ignored) {
            return false;
        }
    }

    private static boolean isSpawnHazard(ServerLevel level, BlockPos pos) {
        try {
            BlockState state = level.getBlockState(pos);
            if (state != null) {
                var fluid = state.getFluidState();
                if (fluid != null && (fluid.is(net.minecraft.tags.FluidTags.LAVA)
                        || fluid.is(net.minecraft.world.level.material.Fluids.LAVA)
                        || fluid.is(net.minecraft.world.level.material.Fluids.FLOWING_LAVA))) {
                    return true;
                }
                if (state.is(Blocks.LAVA)
                        || state.is(Blocks.FIRE)
                        || state.is(Blocks.SOUL_FIRE)
                        || state.is(Blocks.CAMPFIRE)
                        || state.is(Blocks.SOUL_CAMPFIRE)
                        || state.is(Blocks.MAGMA_BLOCK)
                        || state.is(Blocks.CACTUS)
                        || state.is(Blocks.SWEET_BERRY_BUSH)
                        || state.is(Blocks.WITHER_ROSE)
                        || state.is(Blocks.POWDER_SNOW)) {
                    return true;
                }
            }
            var levelFluid = level.getFluidState(pos);
            if (levelFluid != null && (levelFluid.is(net.minecraft.tags.FluidTags.LAVA)
                    || levelFluid.is(net.minecraft.world.level.material.Fluids.LAVA)
                    || levelFluid.is(net.minecraft.world.level.material.Fluids.FLOWING_LAVA))) {
                return true;
            }
        } catch (Throwable ignored) {
        }
        return false;
    }

    /**
     * Registers an agent entity directly (useful for testing or external initialization).
     */
    public void registerAgent(AgentEntity agent) {
        if (agent != null && agent.getAgentId() != null) {
            if (agent.getIntentActuator() == null) {
                agent.setIntentActuator(this.intentActuator);
            }
            agents.put(agent.getAgentId(), agent);
        }
    }

    public CompletableFuture<Boolean> despawnAgent(MinecraftServer server, String agentId) {
        if (agentId == null || agentId.isBlank()) {
            return CompletableFuture.completedFuture(false);
        }

        Supplier<Boolean> despawnSupplier = () -> {
            String canonicalKey = findCanonicalAgentKey(agentId);
            AgentEntity removed = canonicalKey != null ? agents.remove(canonicalKey) : null;
            if (removed != null) {
                if (removed.getWatchdogFSM() != null) {
                    removed.getWatchdogFSM().onAgentDespawn();
                }
                removed.releaseHeldChunk();
                removed.discard();
                if (server != null && server.getPlayerList() != null) {
                    ClientboundPlayerInfoRemovePacket removePacket =
                            new ClientboundPlayerInfoRemovePacket(List.of(removed.getUUID()));
                    server.getPlayerList().broadcastAll(removePacket);
                }
                LOGGER.info("AgentEntity despawned and discarded: agentId='{}'", canonicalKey);
                return true;
            }
            return false;
        };

        if (server != null) {
            return server.submit(despawnSupplier);
        } else {
            return CompletableFuture.completedFuture(despawnSupplier.get());
        }
    }

    public String findCanonicalAgentKey(String agentId) {
        if (agentId == null || agentId.isBlank()) {
            return null;
        }
        if (agents.containsKey(agentId)) {
            return agentId;
        }
        for (Map.Entry<String, AgentEntity> entry : agents.entrySet()) {
            if (entry.getKey().equalsIgnoreCase(agentId)) {
                return entry.getKey();
            }
            AgentEntity ent = entry.getValue();
            if (ent != null && ent.getGameProfile() != null
                    && ent.getGameProfile().getName() != null
                    && ent.getGameProfile().getName().equalsIgnoreCase(agentId)) {
                return entry.getKey();
            }
        }
        return null;
    }

    public AgentEntity getAgent(String agentId) {
        String key = findCanonicalAgentKey(agentId);
        return key != null ? agents.get(key) : null;
    }

    public Map<String, AgentEntity> getAllAgents() {
        return Collections.unmodifiableMap(agents);
    }

    public int count() {
        return agents.size();
    }

    public void onServerTick(ServerTickEvent.Post event) {
        MinecraftServer server = event.getServer();
        if (server != null) {
            // A teleported agent may land where nothing ticks it, so this cannot be left to it.
            agents.values().forEach(AgentEntity::followChunk);
            tickTelemetry(server.getTickCount());
        }
    }

    public void tickTelemetry() {
        tickTelemetry(-1);
    }

    public void tickTelemetry(int tickCount) {
        if (streamRegistry == null || agents.isEmpty()) {
            return;
        }
        for (AgentEntity agent : agents.values()) {
            if (tickCount >= 0) {
                int hash = Math.abs(agent.getUUID().hashCode());
                if (hash < 0) {
                    hash = 0;
                }
                if ((hash + tickCount) % 4 != 0) {
                    continue;
                }
            }
            if (agent.isAlive() && !agent.isRemoved()) {
                if (streamRegistry.isRegistered(agent.getAgentId())) {
                    try {
                        TelemetryFrame frame = sensoryCollector.collectFrame(agent, null, null);
                        streamRegistry.sendFrame(agent.getAgentId(), frame);
                    } catch (Exception e) {
                        LOGGER.error("Error streaming 5 Hz telemetry frame for agent '{}': {}",
                                agent.getAgentId(), e.getMessage(), e);
                    }
                }
            }
        }
    }

    public void onLivingDamage(LivingDamageEvent.Post event) {
        if (event.getEntity() instanceof AgentEntity agent) {
            if (agent.isDeadOrDying() || agent.getHealth() <= 0.0f) {
                return;
            }
            if (streamRegistry != null && streamRegistry.isRegistered(agent.getAgentId())) {
                try {
                    DamageEvent damageEvent = SensoryFrustumCollector.buildDamageEvent(event.getSource(), event.getNewDamage());
                    TelemetryFrame frame = sensoryCollector.collectFrame(agent, damageEvent, null);
                    streamRegistry.sendFrame(agent.getAgentId(), frame);
                } catch (Exception e) {
                    LOGGER.error("Error streaming immediate damage telemetry frame for agent '{}': {}",
                            agent.getAgentId(), e.getMessage(), e);
                }
            }
        }
    }

    public CompletableFuture<Void> handleIntent(MinecraftServer server, AgentIntent intent) {
        if (intent == null) {
            return CompletableFuture.completedFuture(null);
        }

        Runnable task = () -> {
            try {
                String agentId = intent.getAgentId();
                if (agentId == null || agentId.isBlank()) {
                    LOGGER.warn("Received AgentIntent without agent_id");
                    return;
                }

                AgentEntity agent = getAgent(agentId);
                if (agent == null || agent.isRemoved() || !agent.isAlive()) {
                    LOGGER.warn("Received intent {} for unknown or inactive agent '{}'",
                            intent.getActionCase(), agentId);
                    return;
                }

                intentActuator.actuate(agent, intent);
            } catch (Throwable t) {
                LOGGER.error("Error executing intent {} for agent '{}': {}",
                        intent.getActionCase(), intent.getAgentId(), t.getMessage(), t);
            }
        };

        return submitServerOrHeadless(server, intent.getAgentId(), intent.getActionCase().name(), task);
    }

    public CompletableFuture<Void> handleIntentBatch(MinecraftServer server, IntentBatch batch) {
        if (batch == null) {
            return CompletableFuture.completedFuture(null);
        }

        Runnable task = () -> {
            try {
                String agentId = batch.getAgentId();
                if (agentId == null || agentId.isBlank()) {
                    LOGGER.warn("Received IntentBatch without agent_id");
                    return;
                }

                AgentEntity agent = getAgent(agentId);
                if (agent == null || agent.isRemoved() || !agent.isAlive()) {
                    LOGGER.warn("Received IntentBatch {} for unknown or inactive agent '{}'",
                            batch.getBatchId(), agentId);
                    return;
                }

                intentActuator.submitBatch(agent, batch);
            } catch (Throwable t) {
                LOGGER.error("Error executing IntentBatch {} for agent '{}': {}",
                        batch.getBatchId(), batch.getAgentId(), t.getMessage(), t);
            }
        };

        return submitServerOrHeadless(server, batch.getAgentId(), "IntentBatch#" + batch.getBatchId(), task);
    }

    private CompletableFuture<Void> submitServerOrHeadless(MinecraftServer server,
                                                             String agentId,
                                                             String actionLabel,
                                                             Runnable task) {
        if (server != null) {
            if (!server.isRunning()) {
                LOGGER.warn("Dropping {}: MinecraftServer is not running", actionLabel);
                return CompletableFuture.failedFuture(new IllegalStateException("MinecraftServer is not running"));
            }
            try {
                return server.submit(task);
            } catch (RejectedExecutionException e) {
                LOGGER.warn("Rejected execution when submitting {}: {}", actionLabel, e.getMessage());
                return CompletableFuture.failedFuture(e);
            }
        } else {
            // Headless test environment without MinecraftServer
            AgentEntity agent = (agentId != null) ? agents.get(agentId) : null;
            if (agent != null && agent.level() != null) {
                LOGGER.warn("Dropping {}: no MinecraftServer running to execute world-modifying task safely",
                        actionLabel);
                return CompletableFuture.failedFuture(new IllegalStateException("No MinecraftServer present for world-bound agent"));
            }
            task.run();
            return CompletableFuture.completedFuture(null);
        }
    }

    public void clearAll() {
        for (AgentEntity agent : agents.values()) {
            try {
                if (agent.getWatchdogFSM() != null) {
                    agent.getWatchdogFSM().onAgentDespawn();
                }
                agent.releaseHeldChunk();
                agent.discard();
            } catch (Exception e) {
                LOGGER.debug("Error discarding agent during clearAll: {}", e.getMessage());
            }
        }
        agents.clear();
        LOGGER.info("AgentEntityManager cleared all active agents");
    }
}
