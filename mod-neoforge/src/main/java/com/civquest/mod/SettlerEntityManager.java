package com.civquest.mod;

import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.mojang.authlib.GameProfile;
import com.mojang.authlib.properties.Property;
import com.mojang.logging.LogUtils;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoRemovePacket;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoUpdatePacket;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import org.slf4j.Logger;

import java.util.Collections;
import java.util.EnumSet;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.Supplier;

/**
 * Manages active {@link SettlerEntity} instances, server-thread lifecycle scheduling,
 * and skin packet broadcasting.
 */
public class SettlerEntityManager {
    private static final Logger LOGGER = LogUtils.getLogger();

    private final ConcurrentHashMap<String, SettlerEntity> settlers = new ConcurrentHashMap<>();

    public CompletableFuture<SettlerEntity> spawnSettler(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        Supplier<SettlerEntity> spawnSupplier = () -> doSpawnSettler(server, level, request);
        if (server != null) {
            return server.submit(spawnSupplier);
        } else {
            return CompletableFuture.completedFuture(spawnSupplier.get());
        }
    }

    private SettlerEntity doSpawnSettler(MinecraftServer server, ServerLevel level, SpawnAgentRequest request) {
        String agentId = request.getAgentId();
        String displayName = request.getDisplayName();
        if (displayName == null || displayName.isBlank()) {
            displayName = agentId;
        }

        UUID offlineUuid = SettlerEntity.createOfflineUUID(displayName);
        GameProfile profile = new GameProfile(offlineUuid, displayName);

        if (request.getSkinTextureValue() != null && !request.getSkinTextureValue().isBlank()) {
            String sig = (request.getSkinSignature() != null && !request.getSkinSignature().isBlank())
                    ? request.getSkinSignature()
                    : null;
            profile.getProperties().put("textures", new Property("textures", request.getSkinTextureValue(), sig));
        }

        SettlerEntity settler = new SettlerEntity(level, profile, agentId, request.getClanId());

        if (request.getSkinPngBytes() != null && !request.getSkinPngBytes().isEmpty()) {
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
            level.addFreshEntity(settler);
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
            settlers.remove(deadSettler.getAgentId(), deadSettler);
            if (server != null && server.getPlayerList() != null) {
                ClientboundPlayerInfoRemovePacket removePacket =
                        new ClientboundPlayerInfoRemovePacket(List.of(deadSettler.getUUID()));
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

    public void clearAll() {
        for (SettlerEntity settler : settlers.values()) {
            try {
                settler.discard();
            } catch (Exception e) {
                LOGGER.debug("Error discarding settler during clearAll: {}", e.getMessage());
            }
        }
        settlers.clear();
        LOGGER.info("SettlerEntityManager cleared all active settlers");
    }
}
