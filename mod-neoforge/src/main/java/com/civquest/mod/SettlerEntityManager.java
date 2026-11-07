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
 * Manages active {@link SettlerEntity} instances, server-thread lifecycle scheduling,
 * and skin packet broadcasting.
 */
public class SettlerEntityManager {
    private static final Logger LOGGER = LogUtils.getLogger();

    private final ConcurrentHashMap<String, SettlerEntity> settlers = new ConcurrentHashMap<>();
    private volatile StreamRegistry streamRegistry;
    private final SensoryFrustumCollector sensoryCollector;
    private final IntentActuator intentActuator;

    public SettlerEntityManager() {
        this(null, new SensoryFrustumCollector(), new IntentActuator());
    }

    public SettlerEntityManager(StreamRegistry streamRegistry) {
        this(streamRegistry, new SensoryFrustumCollector(), new IntentActuator());
    }

    public SettlerEntityManager(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector) {
        this(streamRegistry, sensoryCollector, new IntentActuator());
    }

    public SettlerEntityManager(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector, IntentActuator intentActuator) {
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
        for (SettlerEntity settler : settlers.values()) {
            settler.setStreamRegistry(streamRegistry);
        }
    }

    public CompletableFuture<SettlerEntity> spawnSettler(MinecraftServer server, SpawnAgentRequest request) {
        return spawnSettler(server, null, request);
    }

    public CompletableFuture<SettlerEntity> spawnSettler(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        Supplier<SettlerEntity> spawnSupplier = () -> {
            ServerLevel targetLevel = level;
            if (targetLevel == null && server != null) {
                targetLevel = server.overworld();
                if (targetLevel == null && server.getAllLevels().iterator().hasNext()) {
                    targetLevel = server.getAllLevels().iterator().next();
                }
            }
            return doSpawnSettler(server, targetLevel, request);
        };
        if (server != null) {
            return server.submit(spawnSupplier);
        } else {
            try {
                return CompletableFuture.completedFuture(spawnSupplier.get());
            } catch (Throwable t) {
                CompletableFuture<SettlerEntity> future = new CompletableFuture<>();
                future.completeExceptionally(t);
                return future;
            }
        }
    }

    private SettlerEntity doSpawnSettler(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        String agentId = request.getAgentId();
        String displayName = request.getDisplayName();
        if (displayName == null || displayName.isBlank()) {
            displayName = agentId;
        }

        // Check if agentId already exists: cleanly discard and despawn old entity before spawning
        SettlerEntity existing = settlers.remove(agentId);
        if (existing != null) {
            LOGGER.warn("Duplicate agent spawn detected for agentId='{}'. Despawning old entity (uuid='{}')",
                    agentId, existing.getUUID());
            existing.releaseForcedChunk();
            existing.discard();
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(existing.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        }

        UUID offlineUuid = SettlerEntity.createOfflineUUID(displayName);
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

        SettlerEntity settler = new SettlerEntity(level, profile, agentId, request.getClanId());
        settler.setStreamRegistry(this.streamRegistry);
        settler.setSensoryCollector(this.sensoryCollector);

        if (request.getSkinPngBytes() != null && !request.getSkinPngBytes().isEmpty()) {
            if (request.getSkinPngBytes().size() > 65536) {
                throw new IllegalArgumentException("skin_png_bytes exceeds maximum allowed size of 65536 bytes (was "
                        + request.getSkinPngBytes().size() + ")");
            }
            settler.setSkinPngBytes(request.getSkinPngBytes().toByteArray());
        }

        double x = 0.0;
        double y = 0.0;
        double z = 0.0;
        if (request.hasSpawnCoords()) {
            x = request.getSpawnCoords().getX();
            y = request.getSpawnCoords().getY();
            z = request.getSpawnCoords().getZ();
        }

        settler.moveTo(x, y, z, 0.0f, 0.0f);
        settler.setYRot(0.0f);
        settler.setXRot(0.0f);
        settler.setYHeadRot(0.0f);

        if (level != null) {
            ChunkPos chunkPos = new ChunkPos(BlockPos.containing(x, y, z));
            level.setChunkForced(chunkPos.x, chunkPos.z, true);
            boolean added = level.addFreshEntity(settler);
            if (!added) {
                level.setChunkForced(chunkPos.x, chunkPos.z, false);
                throw new IllegalStateException("Failed to add SettlerEntity to level");
            }
            settler.setForcedChunkPos(chunkPos);
        }

        // Broadcast player info packet to connected tracking clients
        ClientboundPlayerInfoUpdatePacket infoPacket = new ClientboundPlayerInfoUpdatePacket(
                EnumSet.of(
                        ClientboundPlayerInfoUpdatePacket.Action.ADD_PLAYER,
                        ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LISTED,
                        ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LATENCY,
                        ClientboundPlayerInfoUpdatePacket.Action.UPDATE_GAME_MODE
                ),
                List.of(settler)
        );

        if (server != null && server.getPlayerList() != null) {
            server.getPlayerList().broadcastAll(infoPacket);
        }

        // Broadcast custom skin payload packet if custom PNG bytes are supplied
        if (settler.getSkinPngBytes() != null && settler.getSkinPngBytes().length > 0) {
            ClientboundSettlerSkinPacket skinPacket = new ClientboundSettlerSkinPacket(
                    settler.getUUID(),
                    settler.getAgentId(),
                    settler.getSkinPngBytes()
            );
            if (server != null && server.getPlayerList() != null) {
                server.getPlayerList().broadcastAll(skinPacket.toVanillaClientbound());
            }
        }

        settler.setDeathListener(deadSettler -> {
            deadSettler.releaseForcedChunk();
            settlers.remove(deadSettler.getAgentId(), deadSettler);
            if (streamRegistry != null) {
                streamRegistry.close(deadSettler.getAgentId());
            }
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(deadSettler.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        });

        settler.setRemovalListener(removedSettler -> {
            removedSettler.releaseForcedChunk();
            settlers.remove(removedSettler.getAgentId(), removedSettler);
            if (streamRegistry != null) {
                streamRegistry.close(removedSettler.getAgentId());
            }
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(removedSettler.getUUID()));
                server.getPlayerList().broadcastAll(removePacket);
            }
        });

        settlers.put(agentId, settler);
        LOGGER.info("SettlerEntity registered and spawned: agentId='{}', uuid='{}', entityId={}",
                agentId, settler.getUUID(), settler.getId());
        return settler;
    }

    public CompletableFuture<Boolean> despawnSettler(MinecraftServer server, String agentId) {
        if (agentId == null || agentId.isBlank()) {
            return CompletableFuture.completedFuture(false);
        }

        Supplier<Boolean> despawnSupplier = () -> {
            SettlerEntity removed = settlers.remove(agentId);
            if (removed != null) {
                removed.releaseForcedChunk();
                removed.discard();
                if (server != null && server.getPlayerList() != null) {
                    ClientboundPlayerInfoRemovePacket removePacket =
                            new ClientboundPlayerInfoRemovePacket(List.of(removed.getUUID()));
                    server.getPlayerList().broadcastAll(removePacket);
                }
                LOGGER.info("SettlerEntity despawned and discarded: agentId='{}'", agentId);
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

    public SettlerEntity getSettler(String agentId) {
        if (agentId == null) {
            return null;
        }
        return settlers.get(agentId);
    }

    public Map<String, SettlerEntity> getAllSettlers() {
        return Collections.unmodifiableMap(settlers);
    }

    public int count() {
        return settlers.size();
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
        if (streamRegistry == null || settlers.isEmpty()) {
            return;
        }
        for (SettlerEntity settler : settlers.values()) {
            if (tickCount >= 0) {
                int hash = Math.abs(settler.getUUID().hashCode());
                if (hash < 0) {
                    hash = 0;
                }
                if ((hash + tickCount) % 4 != 0) {
                    continue;
                }
            }
            if (settler.isAlive() && !settler.isRemoved()) {
                if (streamRegistry.isRegistered(settler.getAgentId())) {
                    try {
                        TelemetryFrame frame = sensoryCollector.collectFrame(settler, null, null);
                        streamRegistry.sendFrame(settler.getAgentId(), frame);
                    } catch (Exception e) {
                        LOGGER.error("Error streaming 5 Hz telemetry frame for settler '{}': {}",
                                settler.getAgentId(), e.getMessage(), e);
                    }
                }
            }
        }
    }

    public void onLivingDamage(LivingDamageEvent.Post event) {
        if (event.getEntity() instanceof SettlerEntity settler) {
            if (settler.isDeadOrDying() || settler.getHealth() <= 0.0f) {
                return;
            }
            if (streamRegistry != null && streamRegistry.isRegistered(settler.getAgentId())) {
                try {
                    DamageEvent damageEvent = SensoryFrustumCollector.buildDamageEvent(event.getSource(), event.getNewDamage());
                    TelemetryFrame frame = sensoryCollector.collectFrame(settler, damageEvent, null);
                    streamRegistry.sendFrame(settler.getAgentId(), frame);
                } catch (Exception e) {
                    LOGGER.error("Error streaming immediate damage telemetry frame for settler '{}': {}",
                            settler.getAgentId(), e.getMessage(), e);
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

                SettlerEntity settler = settlers.get(agentId);
                if (settler == null || settler.isRemoved() || !settler.isAlive()) {
                    LOGGER.warn("Received intent {} for unknown or inactive settler '{}'",
                            intent.getActionCase(), agentId);
                    return;
                }

                intentActuator.actuate(settler, intent);
            } catch (Throwable t) {
                LOGGER.error("Error executing intent {} for settler '{}': {}",
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
            SettlerEntity settler = (agentId != null) ? settlers.get(agentId) : null;
            if (settler != null && settler.level() != null) {
                LOGGER.warn("Dropping intent {}: no MinecraftServer running to execute world-modifying task safely",
                        intent.getActionCase());
                return CompletableFuture.failedFuture(new IllegalStateException("No MinecraftServer present for world-bound settler"));
            }
            task.run();
            return CompletableFuture.completedFuture(null);
        }
    }

    public void clearAll() {
        for (SettlerEntity settler : settlers.values()) {
            try {
                settler.releaseForcedChunk();
                settler.discard();
            } catch (Exception e) {
                LOGGER.debug("Error discarding settler during clearAll: {}", e.getMessage());
            }
        }
        settlers.clear();
        LOGGER.info("SettlerEntityManager cleared all active settlers");
    }
}
