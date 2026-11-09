package com.civquest.mod;

import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.DamageEvent;
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
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.world.level.ChunkPos;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.event.tick.ServerTickEvent;
import org.slf4j.Logger;

import java.util.Base64;
import java.util.Collections;
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
            existing.releaseForcedChunk();
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
            if (server != null && server.getPlayerList() != null && !server.getPlayerList().getPlayers().isEmpty()) {
                ServerPlayer player = server.getPlayerList().getPlayers().get(0);
                x = player.getX() + 2.0;
                y = player.getY();
                z = player.getZ() + 2.0;
            } else {
                BlockPos spawnPos = level.getSharedSpawnPos();
                x = spawnPos.getX() + 0.5;
                z = spawnPos.getZ() + 0.5;
                y = level.getHeight(net.minecraft.world.level.levelgen.Heightmap.Types.MOTION_BLOCKING, spawnPos.getX(), spawnPos.getZ());
            }
        }

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
            level.setChunkForced(chunkPos.x, chunkPos.z, true);
            boolean added = false;
            try {
                added = level.addFreshEntity(agent);
            } catch (Throwable t) {
                LOGGER.error("Unexpected error adding AgentEntity to level for agent '{}': {}", agentId, t.getMessage(), t);
                added = false;
            }
            if (!added) {
                level.setChunkForced(chunkPos.x, chunkPos.z, false);
                if (server != null && server.getPlayerList() != null) {
                    ClientboundPlayerInfoRemovePacket removePacket =
                            new ClientboundPlayerInfoRemovePacket(List.of(agent.getUUID()));
                    server.getPlayerList().broadcastAll(removePacket);
                }
                throw new IllegalStateException("Failed to add AgentEntity to level");
            }
            agent.setForcedChunkPos(chunkPos);
        }
        if (spawnListener != null) {
            spawnListener.onEntityAdded(agent);
        }

        agent.setDeathListener(deadAgent -> {
            if (deadAgent.getWatchdogFSM() != null) {
                deadAgent.getWatchdogFSM().onAgentDeath();
            }
            deadAgent.releaseForcedChunk();
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
            removedAgent.releaseForcedChunk();
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
                removed.releaseForcedChunk();
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

        if (server != null) {
            if (!server.isRunning()) {
                LOGGER.warn("Dropping intent {}: MinecraftServer is not running", intent.getActionCase());
                return CompletableFuture.failedFuture(new IllegalStateException("MinecraftServer is not running"));
            }
            try {
                return server.submit(task);
            } catch (RejectedExecutionException e) {
                LOGGER.warn("Rejected execution when submitting intent {}: {}", intent.getActionCase(), e.getMessage());
                return CompletableFuture.failedFuture(e);
            }
        } else {
            // Headless test environment without MinecraftServer
            String agentId = intent.getAgentId();
            AgentEntity agent = (agentId != null) ? agents.get(agentId) : null;
            if (agent != null && agent.level() != null) {
                LOGGER.warn("Dropping intent {}: no MinecraftServer running to execute world-modifying task safely",
                        intent.getActionCase());
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
                agent.releaseForcedChunk();
                agent.discard();
            } catch (Exception e) {
                LOGGER.debug("Error discarding agent during clearAll: {}", e.getMessage());
            }
        }
        agents.clear();
        LOGGER.info("AgentEntityManager cleared all active agents");
    }
}
