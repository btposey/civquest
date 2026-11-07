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
import io.grpc.stub.StreamObserver;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Objects;
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
        responseObserver.onNext(initialFrame);

        return new StreamObserver<AgentIntent>() {
            private final AtomicReference<String> sessionAgentId = new AtomicReference<>(null);

            @Override
            public void onNext(AgentIntent value) {
                String intentAgentId = value.getAgentId();
                if (intentAgentId != null && !intentAgentId.isBlank()) {
                    String current = sessionAgentId.get();
                    if (!intentAgentId.equals(current) || streamRegistry.getObserver(intentAgentId) != responseObserver) {
                        if (current != null && !current.equals(intentAgentId)) {
                            streamRegistry.unregister(current, responseObserver);
                        }
                        sessionAgentId.set(intentAgentId);
                        streamRegistry.register(intentAgentId, responseObserver);
                        LOGGER.info("Registered agent '{}' in StreamRegistry", intentAgentId);
                    }
                }

                LOGGER.debug("Received AgentIntent: agent_id={}, action_case={}",
                        value.getAgentId(), value.getActionCase());
            }

            @Override
            public void onError(Throwable t) {
                LOGGER.warn("ConnectAgent stream error: {}", t.getMessage());
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
                try {
                    responseObserver.onCompleted();
                } catch (Throwable t) {
                    LOGGER.debug("Exception while completing responseObserver: {}", t.getMessage());
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
        responseObserver.onNext(response);
        responseObserver.onCompleted();
    }

    @Override
    public void despawnAgentEntity(DespawnAgentRequest request, StreamObserver<DespawnAgentResponse> responseObserver) {
        LOGGER.info("DespawnAgentEntity requested: agent_id='{}'", request.getAgentId());

        DespawnAgentResponse response = DespawnAgentResponse.newBuilder()
                .setSuccess(true)
                .build();
        responseObserver.onNext(response);
        responseObserver.onCompleted();
    }

    @Override
    public void exportRecipeGraph(ExportRecipeRequest request, StreamObserver<ExportRecipeResponse> responseObserver) {
        LOGGER.info("ExportRecipeGraph requested");

        ExportRecipeResponse response = ExportRecipeResponse.newBuilder()
                .build();
        responseObserver.onNext(response);
        responseObserver.onCompleted();
    }
}
