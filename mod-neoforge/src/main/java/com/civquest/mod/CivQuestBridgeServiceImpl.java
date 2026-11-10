package com.civquest.mod;

import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.CivQuestBridgeServiceGrpc;
import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.ExportRecipeRequest;
import com.civquest.mod.grpc.v1.ExportRecipeResponse;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.SpawnAgentResponse;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.ServerCallStreamObserver;
import io.grpc.stub.StreamObserver;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Base64;
import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Supplier;

/**
 * Initial bridge service implementation providing baseline responses for agent spawning,
 * despawning, control streaming, and recipe graph export.
 */
public class CivQuestBridgeServiceImpl extends CivQuestBridgeServiceGrpc.CivQuestBridgeServiceImplBase {
    private static final Logger LOGGER = LoggerFactory.getLogger(CivQuestBridgeServiceImpl.class);

    private final StreamRegistry streamRegistry;
    private final AgentEntityManager agentEntityManager;
    private final Supplier<MinecraftServer> serverSupplier;

    public CivQuestBridgeServiceImpl() {
        this(new StreamRegistry());
    }

    public CivQuestBridgeServiceImpl(StreamRegistry streamRegistry) {
        this(streamRegistry, new AgentEntityManager(streamRegistry), () -> null);
    }

    public CivQuestBridgeServiceImpl(StreamRegistry streamRegistry, AgentEntityManager agentEntityManager) {
        this(streamRegistry, agentEntityManager, () -> null);
    }

    public CivQuestBridgeServiceImpl(StreamRegistry streamRegistry,
                                     AgentEntityManager agentEntityManager,
                                     Supplier<MinecraftServer> serverSupplier) {
        this.streamRegistry = Objects.requireNonNull(streamRegistry, "streamRegistry cannot be null");
        this.agentEntityManager = Objects.requireNonNull(agentEntityManager, "agentEntityManager cannot be null");
        this.serverSupplier = Objects.requireNonNull(serverSupplier, "serverSupplier cannot be null");
        if (this.agentEntityManager.getStreamRegistry() == null) {
            this.agentEntityManager.setStreamRegistry(this.streamRegistry);
        }
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public AgentEntityManager getAgentEntityManager() {
        return agentEntityManager;
    }

    public MinecraftServer getServer() {
        return serverSupplier.get();
    }

    @Override
    public StreamObserver<AgentIntent> connectAgent(StreamObserver<TelemetryFrame> responseObserver) {
        LOGGER.info("ConnectAgent stream initiated by client");

        // Send baseline initial TelemetryFrame for probe readiness verification
        TelemetryFrame initialFrame = TelemetryFrame.newBuilder()
                .setStatus(AgentStatus.newBuilder()
                        .setHealth(20.0f)
                        .setMaxHealth(20.0f)
                        .setFoodLevel(20)
                        .build())
                .setWorldTick(0)
                .setTimeOfDay(0)
                .build();
        synchronized (responseObserver) {
            responseObserver.onNext(initialFrame);
        }

        final AtomicReference<String> sessionAgentId = new AtomicReference<>(null);
        final AtomicBoolean terminated = new AtomicBoolean(false);

        if (responseObserver instanceof ServerCallStreamObserver<TelemetryFrame> serverCallObserver) {
            serverCallObserver.setOnCancelHandler(() -> {
                terminated.set(true);
                String agentId = sessionAgentId.get();
                LOGGER.info("ConnectAgent stream cancelled by client for agent '{}'", agentId);
                if (agentId != null) {
                    streamRegistry.unregister(agentId, responseObserver);
                    MinecraftServer server = getServer();
                    if (server != null && server.isRunning()) {
                        server.execute(() -> {
                            AgentEntity agent = agentEntityManager.getAgent(agentId);
                            if (agent != null && agent.getWatchdogFSM() != null) {
                                agent.getWatchdogFSM().onDisconnected();
                            }
                        });
                    } else {
                        AgentEntity agent = agentEntityManager.getAgent(agentId);
                        if (agent != null && agent.getWatchdogFSM() != null) {
                            agent.getWatchdogFSM().onDisconnected();
                        }
                    }
                }
            });
        }

        return new StreamObserver<AgentIntent>() {
            @Override
            public void onNext(AgentIntent value) {
                if (terminated.get()) {
                    return;
                }

                String intentAgentId = value.getAgentId();
                if (intentAgentId == null || intentAgentId.isBlank()) {
                    LOGGER.error("Received AgentIntent with empty or blank agent_id");
                    terminateWithError(Status.INVALID_ARGUMENT
                            .withDescription("AgentIntent must contain a non-blank agent_id"));
                    return;
                }

                String current = sessionAgentId.get();
                if (current == null) {
                    if (sessionAgentId.compareAndSet(null, intentAgentId)) {
                        boolean registered = streamRegistry.register(intentAgentId, responseObserver);
                        if (!registered && streamRegistry.isClosed()) {
                            terminated.set(true);
                            return;
                        }
                        MinecraftServer server = getServer();
                        if (server != null && server.isRunning()) {
                            server.execute(() -> {
                                AgentEntity agent = agentEntityManager.getAgent(intentAgentId);
                                if (agent != null && agent.getWatchdogFSM() != null) {
                                    agent.getWatchdogFSM().onConnected();
                                }
                            });
                        } else {
                            AgentEntity agent = agentEntityManager.getAgent(intentAgentId);
                            if (agent != null && agent.getWatchdogFSM() != null) {
                                agent.getWatchdogFSM().onConnected();
                            }
                        }
                        LOGGER.info("Registered agent '{}' in StreamRegistry", intentAgentId);
                    } else {
                        current = sessionAgentId.get();
                        if (current != null && !current.equals(intentAgentId)) {
                            terminateWithPermissionDenied(current, intentAgentId);
                            return;
                        }
                    }
                } else if (!current.equals(intentAgentId)) {
                    terminateWithPermissionDenied(current, intentAgentId);
                    return;
                }

                LOGGER.debug("Received AgentIntent: agent_id={}, action_case={}",
                        value.getAgentId(), value.getActionCase());

                MinecraftServer server = getServer();
                if (server != null && server.isRunning()) {
                    server.execute(() -> {
                        AgentEntity agent = agentEntityManager.getAgent(intentAgentId);
                        if (agent != null && agent.getWatchdogFSM() != null) {
                            agent.getWatchdogFSM().onIntentReceived(value);
                        }
                    });
                } else {
                    AgentEntity agent = agentEntityManager.getAgent(intentAgentId);
                    if (agent != null && agent.getWatchdogFSM() != null) {
                        agent.getWatchdogFSM().onIntentReceived(value);
                    }
                }
                agentEntityManager.handleIntent(server, value)
                        .whenComplete((res, err) -> {
                            if (err != null) {
                                LOGGER.warn("Intent actuation failed: {}", err.getMessage());
                            }
                        });
            }

            private void terminateWithPermissionDenied(String boundAgentId, String requestedAgentId) {
                LOGGER.error("Stream agent ID mutation attempted: bound='{}', requested='{}'", boundAgentId, requestedAgentId);
                terminateWithError(Status.PERMISSION_DENIED
                        .withDescription("Agent stream is bound to '" + boundAgentId + "' and cannot be rebound to '" + requestedAgentId + "'"));
            }

            private void terminateWithError(Status status) {
                if (terminated.compareAndSet(false, true)) {
                    StatusRuntimeException error = status.asRuntimeException();
                    synchronized (responseObserver) {
                        try {
                            responseObserver.onError(error);
                        } catch (Throwable t) {
                            LOGGER.debug("Error delivering {} to observer: {}", status.getCode(), t.getMessage());
                        }
                    }
                    String current = sessionAgentId.get();
                    if (current != null) {
                        streamRegistry.unregister(current, responseObserver);
                        MinecraftServer server = getServer();
                        if (server != null && server.isRunning()) {
                            server.execute(() -> {
                                AgentEntity agent = agentEntityManager.getAgent(current);
                                if (agent != null && agent.getWatchdogFSM() != null) {
                                    agent.getWatchdogFSM().onStreamError(error);
                                }
                            });
                        } else {
                            AgentEntity agent = agentEntityManager.getAgent(current);
                            if (agent != null && agent.getWatchdogFSM() != null) {
                                agent.getWatchdogFSM().onStreamError(error);
                            }
                        }
                    }
                }
            }

            @Override
            public void onError(Throwable t) {
                LOGGER.warn("ConnectAgent stream error: {}", t.getMessage());
                terminated.set(true);
                String agentId = sessionAgentId.get();
                if (agentId != null) {
                    streamRegistry.unregister(agentId, responseObserver);
                    MinecraftServer server = getServer();
                    if (server != null && server.isRunning()) {
                        server.execute(() -> {
                            AgentEntity agent = agentEntityManager.getAgent(agentId);
                            if (agent != null && agent.getWatchdogFSM() != null) {
                                agent.getWatchdogFSM().onStreamError(t);
                            }
                        });
                    } else {
                        AgentEntity agent = agentEntityManager.getAgent(agentId);
                        if (agent != null && agent.getWatchdogFSM() != null) {
                            agent.getWatchdogFSM().onStreamError(t);
                        }
                    }
                    LOGGER.info("Unregistered agent '{}' from StreamRegistry due to stream error", agentId);
                }
            }

            @Override
            public void onCompleted() {
                LOGGER.info("ConnectAgent stream completed by client");
                String agentId = sessionAgentId.get();
                if (agentId != null) {
                    streamRegistry.unregister(agentId, responseObserver);
                    MinecraftServer server = getServer();
                    if (server != null && server.isRunning()) {
                        server.execute(() -> {
                            AgentEntity agent = agentEntityManager.getAgent(agentId);
                            if (agent != null && agent.getWatchdogFSM() != null) {
                                agent.getWatchdogFSM().onDisconnected();
                            }
                        });
                    } else {
                        AgentEntity agent = agentEntityManager.getAgent(agentId);
                        if (agent != null && agent.getWatchdogFSM() != null) {
                            agent.getWatchdogFSM().onDisconnected();
                        }
                    }
                    LOGGER.info("Unregistered agent '{}' from StreamRegistry due to stream completion", agentId);
                }
                if (terminated.compareAndSet(false, true)) {
                    try {
                        synchronized (responseObserver) {
                            responseObserver.onCompleted();
                        }
                    } catch (Throwable t) {
                        LOGGER.debug("Exception while completing responseObserver: {}", t.getMessage());
                    }
                }
            }
        };
    }

    @Override
    public void spawnAgentEntity(SpawnAgentRequest request, StreamObserver<SpawnAgentResponse> responseObserver) {
        String agentId = request.getAgentId();
        LOGGER.info("SpawnAgentEntity requested: agent_id='{}', name='{}', clan='{}'",
                agentId, request.getDisplayName(), request.getClanId());

        if (agentId == null || agentId.isBlank()) {
            responseObserver.onError(Status.INVALID_ARGUMENT
                    .withDescription("agent_id must not be empty")
                    .asRuntimeException());
            return;
        }

        if (request.getSkinPngBytes() != null && request.getSkinPngBytes().size() > 65536) {
            responseObserver.onError(Status.INVALID_ARGUMENT
                    .withDescription("skin_png_bytes exceeds maximum allowed size of 65536 bytes")
                    .asRuntimeException());
            return;
        }

        if (request.getSkinTextureValue() != null && !request.getSkinTextureValue().isBlank()) {
            String textureVal = request.getSkinTextureValue().trim();
            if (textureVal.length() > 4096) {
                responseObserver.onError(Status.INVALID_ARGUMENT
                        .withDescription("skin_texture_value exceeds maximum allowed length of 4096 characters")
                        .asRuntimeException());
                return;
            }
            try {
                Base64.getDecoder().decode(textureVal);
            } catch (IllegalArgumentException e) {
                responseObserver.onError(Status.INVALID_ARGUMENT
                        .withDescription("skin_texture_value is not valid Base64")
                        .asRuntimeException());
                return;
            }
        }

        MinecraftServer server = getServer();
        if (server != null && server.isRunning()) {
            CompletableFuture<AgentEntity> future = null;
            try {
                // Dimension lookup and entity spawn occur inside server.submit() via AgentEntityManager
                future = agentEntityManager.spawnAgent(server, request);
                AgentEntity agent = future.get(5, TimeUnit.SECONDS);

                SpawnAgentResponse response = SpawnAgentResponse.newBuilder()
                        .setSuccess(true)
                        .setMinecraftEntityId(agent.getId())
                        .setOfflineUuid(agent.getUUID().toString())
                        .build();

                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            } catch (TimeoutException e) {
                LOGGER.error("Timeout waiting to spawn agent entity for agent '{}'", agentId);
                if (future != null) {
                    future.cancel(false);
                }
                agentEntityManager.despawnAgent(server, agentId);
                SpawnAgentResponse response = SpawnAgentResponse.newBuilder()
                        .setSuccess(false)
                        .setErrorMessage("Timed out spawning agent on server: " + e.getMessage())
                        .build();
                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            } catch (Exception e) {
                LOGGER.error("Failed to spawn agent for agent '{}': {}", agentId, e.getMessage(), e);
                if (future != null) {
                    future.cancel(false);
                }
                agentEntityManager.despawnAgent(server, agentId);
                Throwable cause = e.getCause() != null ? e.getCause() : e;
                SpawnAgentResponse response = SpawnAgentResponse.newBuilder()
                        .setSuccess(false)
                        .setErrorMessage("Failed to spawn agent on server: " + cause.getMessage())
                        .build();
                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            }
        }

        // Standalone test fallback without active MinecraftServer
        LOGGER.info("Standalone/mock spawn for agent '{}' (no active MinecraftServer)", agentId);
        String displayName = request.getDisplayName().isBlank() ? request.getAgentId() : request.getDisplayName();
        String offlineUuid = net.minecraft.core.UUIDUtil.createOfflinePlayerUUID(displayName).toString();

        SpawnAgentResponse response = SpawnAgentResponse.newBuilder()
                .setSuccess(true)
                .setMinecraftEntityId(1001)
                .setOfflineUuid(offlineUuid)
                .build();
        synchronized (responseObserver) {
            responseObserver.onNext(response);
            responseObserver.onCompleted();
        }
    }

    @Override
    public void despawnAgentEntity(DespawnAgentRequest request, StreamObserver<DespawnAgentResponse> responseObserver) {
        String agentId = request.getAgentId();
        LOGGER.info("DespawnAgentEntity requested: agent_id='{}'", agentId);

        if (agentId == null || agentId.isBlank()) {
            responseObserver.onError(Status.INVALID_ARGUMENT
                    .withDescription("agent_id must not be empty")
                    .asRuntimeException());
            return;
        }

        streamRegistry.close(agentId);
        MinecraftServer server = getServer();
        if (server != null && server.isRunning()) {
            CompletableFuture<Boolean> future = null;
            try {
                future = agentEntityManager.despawnAgent(server, agentId);
                boolean despawned = future.get(5, TimeUnit.SECONDS);
                DespawnAgentResponse response = DespawnAgentResponse.newBuilder()
                        .setSuccess(despawned)
                        .build();
                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            } catch (TimeoutException e) {
                LOGGER.error("Timeout while despawning agent entity for agent '{}': {}", agentId, e.getMessage());
                if (future != null) {
                    future.cancel(false);
                }
                DespawnAgentResponse response = DespawnAgentResponse.newBuilder()
                        .setSuccess(false)
                        .build();
                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            } catch (Exception e) {
                LOGGER.error("Failed to despawn agent entity for agent '{}': {}", agentId, e.getMessage(), e);
                if (future != null) {
                    future.cancel(false);
                }
                DespawnAgentResponse response = DespawnAgentResponse.newBuilder()
                        .setSuccess(false)
                        .build();
                synchronized (responseObserver) {
                    responseObserver.onNext(response);
                    responseObserver.onCompleted();
                }
                return;
            }
        }

        // Standalone/mock fallback without active MinecraftServer
        agentEntityManager.despawnAgent(null, agentId);
        DespawnAgentResponse response = DespawnAgentResponse.newBuilder()
                .setSuccess(true)
                .build();
        synchronized (responseObserver) {
            responseObserver.onNext(response);
            responseObserver.onCompleted();
        }
    }

    @Override
    public void exportRecipeGraph(ExportRecipeRequest request, StreamObserver<ExportRecipeResponse> responseObserver) {
        LOGGER.info("ExportRecipeGraph requested");

        ExportRecipeResponse response = ExportRecipeResponse.newBuilder()
                .build();
        synchronized (responseObserver) {
            responseObserver.onNext(response);
            responseObserver.onCompleted();
        }
    }
}
