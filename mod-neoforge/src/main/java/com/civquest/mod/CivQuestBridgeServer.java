package com.civquest.mod;

import com.civquest.mod.grpc.v1.CivQuestBridgeServiceGrpc;
import io.grpc.Server;
import io.grpc.health.v1.HealthCheckResponse;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.protobuf.services.HealthStatusManager;
import net.neoforged.neoforge.event.server.ServerStartedEvent;
import net.neoforged.neoforge.event.server.ServerStartingEvent;
import net.neoforged.neoforge.event.server.ServerStoppingEvent;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.util.Locale;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

/**
 * Embedded Netty gRPC server managing the IPC bridge between the Minecraft NeoForge server
 * and external orchestrators.
 */
public class CivQuestBridgeServer {
    private static final Logger LOGGER = LoggerFactory.getLogger(CivQuestBridgeServer.class);

    public static final String PROP_GRPC_BIND = "civquest.grpc.bind";
    public static final String PROP_GRPC_SHARED_KEY = "civquest.grpc.shared_key";
    public static final String PROP_DISCOVERY_PATH = "civquest.discovery.path";
    public static final String PROP_GRPC_ENABLED = "civquest.grpc.enabled";

    public static final String DEFAULT_BIND = "127.0.0.1:0";
    public static final String DEFAULT_DISCOVERY_PATH = "civquest-server-meta.json";

    private final String configuredHost;
    private final int configuredPort;
    private final String sharedKey;
    private final Path discoveryPath;
    private final boolean enabled;

    private Server server;
    private HealthStatusManager healthStatusManager;
    private SharedKeyAuthInterceptor authInterceptor;
    private CivQuestBridgeServiceImpl bridgeService;
    private int boundPort = -1;
    private String boundHost;
    private String transport = "tcp";

    public CivQuestBridgeServer() {
        this.enabled = Boolean.parseBoolean(System.getProperty(PROP_GRPC_ENABLED, "true"));
        String bind = System.getProperty(PROP_GRPC_BIND, DEFAULT_BIND).trim();
        String parsedHost = "127.0.0.1";
        int parsedPort = 0;

        int colonIndex = bind.lastIndexOf(':');
        if (colonIndex != -1) {
            String hostPart = bind.substring(0, colonIndex).trim();
            if (!hostPart.isEmpty()) {
                parsedHost = hostPart;
            }
            try {
                parsedPort = Integer.parseInt(bind.substring(colonIndex + 1).trim());
            } catch (NumberFormatException e) {
                LOGGER.warn("Invalid port in {}: '{}', defaulting to 0", PROP_GRPC_BIND, bind);
                parsedPort = 0;
            }
        } else if (!bind.isEmpty()) {
            try {
                parsedPort = Integer.parseInt(bind);
            } catch (NumberFormatException ignored) {
                parsedHost = bind;
            }
        }

        this.configuredHost = parsedHost;
        this.configuredPort = parsedPort;

        String key = System.getProperty(PROP_GRPC_SHARED_KEY);
        if (key == null || key.isBlank()) {
            this.sharedKey = UUID.randomUUID().toString();
        } else {
            this.sharedKey = key.trim();
        }

        String discPath = System.getProperty(PROP_DISCOVERY_PATH, DEFAULT_DISCOVERY_PATH).trim();
        this.discoveryPath = Path.of(discPath);
    }

    public CivQuestBridgeServer(String host, int port, String sharedKey, Path discoveryPath) {
        this.enabled = true;
        this.configuredHost = (host == null || host.isBlank()) ? "127.0.0.1" : host.trim();
        this.configuredPort = port;
        this.sharedKey = (sharedKey == null || sharedKey.isBlank()) ? UUID.randomUUID().toString() : sharedKey.trim();
        this.discoveryPath = discoveryPath != null ? discoveryPath : Path.of(DEFAULT_DISCOVERY_PATH);
    }

    public synchronized void start() throws IOException {
        if (!enabled) {
            LOGGER.info("CivQuestBridgeServer is disabled via configuration");
            return;
        }

        if (server != null && !server.isShutdown()) {
            LOGGER.warn("CivQuestBridgeServer is already running on port {}", boundPort);
            return;
        }

        this.healthStatusManager = new HealthStatusManager();
        this.authInterceptor = new SharedKeyAuthInterceptor(sharedKey);
        this.bridgeService = new CivQuestBridgeServiceImpl();

        InetSocketAddress socketAddress = new InetSocketAddress(configuredHost, configuredPort);
        this.server = NettyServerBuilder.forAddress(socketAddress)
                .addService(bridgeService)
                .addService(healthStatusManager.getHealthService())
                .intercept(authInterceptor)
                .build()
                .start();

        this.boundPort = server.getPort();
        this.boundHost = configuredHost;
        this.transport = "tcp";

        healthStatusManager.setStatus("", HealthCheckResponse.ServingStatus.SERVING);
        healthStatusManager.setStatus(CivQuestBridgeServiceGrpc.SERVICE_NAME, HealthCheckResponse.ServingStatus.SERVING);

        writeDiscoveryFile("READY");
        LOGGER.info("CivQuestBridgeServer listening on {}:{} (PID: {})",
                boundHost, boundPort, ProcessHandle.current().pid());
    }

    public synchronized void stop() {
        if (server == null || server.isShutdown()) {
            return;
        }

        LOGGER.info("Stopping CivQuestBridgeServer on port {}", boundPort);
        if (healthStatusManager != null) {
            healthStatusManager.setStatus("", HealthCheckResponse.ServingStatus.NOT_SERVING);
            healthStatusManager.setStatus(CivQuestBridgeServiceGrpc.SERVICE_NAME, HealthCheckResponse.ServingStatus.NOT_SERVING);
        }

        writeDiscoveryFile("STOPPED");

        server.shutdown();
        try {
            if (!server.awaitTermination(5, TimeUnit.SECONDS)) {
                LOGGER.warn("CivQuestBridgeServer did not terminate cleanly within 5s, forcing shutdown");
                server.shutdownNow();
            }
        } catch (InterruptedException e) {
            LOGGER.warn("CivQuestBridgeServer shutdown interrupted", e);
            server.shutdownNow();
            Thread.currentThread().interrupt();
        }
        LOGGER.info("CivQuestBridgeServer stopped");
    }

    public synchronized void writeDiscoveryFile(String status) {
        if (discoveryPath == null) {
            return;
        }

        long pid = ProcessHandle.current().pid();
        String json = String.format(Locale.ROOT,
                "{\n" +
                "  \"status\": \"%s\",\n" +
                "  \"grpc_transport\": \"%s\",\n" +
                "  \"grpc_port\": %d,\n" +
                "  \"grpc_host\": \"%s\",\n" +
                "  \"server_pid\": %d,\n" +
                "  \"shared_key\": \"%s\"\n" +
                "}\n",
                escapeJson(status),
                escapeJson(transport),
                boundPort,
                escapeJson(boundHost != null ? boundHost : configuredHost),
                pid,
                escapeJson(sharedKey)
        );

        try {
            Path target = discoveryPath.toAbsolutePath().normalize();
            Path parent = target.getParent();
            if (parent != null && !Files.exists(parent)) {
                Files.createDirectories(parent);
            }
            Path tempFile = Files.createTempFile(
                    parent != null ? parent : Path.of("."),
                    "civquest-meta-",
                    ".tmp"
            );
            Files.writeString(tempFile, json, StandardCharsets.UTF_8,
                    StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING);
            try {
                Files.move(tempFile, target, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
            } catch (AtomicMoveNotSupportedException e) {
                Files.move(tempFile, target, StandardCopyOption.REPLACE_EXISTING);
            }
            LOGGER.info("Wrote discovery metadata ({}) to {}", status, target);
        } catch (IOException e) {
            LOGGER.error("Failed to write discovery metadata file: {}", e.getMessage(), e);
        }
    }

    private static String escapeJson(String s) {
        if (s == null) {
            return "";
        }
        return s.replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("\b", "\\b")
                .replace("\f", "\\f")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                .replace("\t", "\\t");
    }

    // NeoForge lifecycle event handlers
    public void onServerStarting(ServerStartingEvent event) {
        LOGGER.info("CivQuest server starting (preparing gRPC bridge configuration)");
    }

    public void onServerStarted(ServerStartedEvent event) {
        LOGGER.info("CivQuest server started: activating gRPC bridge");
        try {
            start();
        } catch (IOException e) {
            LOGGER.error("Failed to start CivQuestBridgeServer on ServerStartedEvent", e);
            throw new RuntimeException("Failed to start CivQuest gRPC server", e);
        }
    }

    public void onServerStopping(ServerStoppingEvent event) {
        LOGGER.info("CivQuest server stopping: shutting down gRPC bridge");
        stop();
    }

    // Accessors
    public boolean isRunning() {
        return server != null && !server.isShutdown() && !server.isTerminated();
    }

    public int getBoundPort() {
        return boundPort;
    }

    public String getBoundHost() {
        return boundHost != null ? boundHost : configuredHost;
    }

    public String getSharedKey() {
        return sharedKey;
    }

    public Path getDiscoveryPath() {
        return discoveryPath;
    }

    public Server getServer() {
        return server;
    }

    public HealthStatusManager getHealthStatusManager() {
        return healthStatusManager;
    }
}
