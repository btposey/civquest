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
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Objects;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Initial bridge service implementation providing baseline responses for agent spawning,
 * despawning, control streaming, and recipe graph export.
 */
public class CivQuestBridgeServiceImpl extends CivQuestBridgeServiceGrpc.CivQuestBridgeServiceImplBase {
    private static final Logger LOGGER = LoggerFactory.getLogger(CivQuestBridgeServiceImpl.class);

    private final StreamRegistry streamRegistry;

    public CivQuestBridgeServiceImpl() {
        this(new StreamRegistry());
    }

    public CivQuestBridgeServiceImpl(StreamRegistry streamRegistry) {
        this.streamRegistry = Objects.requireNonNull(streamRegistry, "streamRegistry cannot be null");
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
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
                    LOGGER.info("Unregistered agent '{}' from StreamRegistry due to stream error", agentId);
                }
            }

            @Override
            public void onCompleted() {
                LOGGER.info("ConnectAgent stream completed by client");
                String agentId = sessionAgentId.get();
                if (agentId != null) {
                    streamRegistry.unregister(agentId, responseObserver);
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
        LOGGER.info("SpawnAgentEntity requested: agent_id='{}', name='{}', clan='{}'",
                request.getAgentId(), request.getDisplayName(), request.getClanId());

        String offlineUuid = java.util.UUID.nameUUIDFromBytes(
                ("OfflinePlayer:" + request.getAgentId()).getBytes(java.nio.charset.StandardCharsets.UTF_8)
        ).toString();

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
        LOGGER.info("DespawnAgentEntity requested: agent_id='{}'", request.getAgentId());

        String agentId = request.getAgentId();
        if (agentId != null && !agentId.isBlank()) {
            streamRegistry.close(agentId);
        }

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
