package com.civquest.mod;

import com.civquest.mod.grpc.v1.CivQuestBridgeServiceGrpc;
import io.grpc.Server;
import io.grpc.health.v1.HealthCheckResponse;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.protobuf.services.HealthStatusManager;
import net.minecraft.server.MinecraftServer;
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.storage.LevelResource;
import net.neoforged.neoforge.event.server.ServerStartedEvent;
import net.neoforged.neoforge.event.server.ServerStartingEvent;
import net.neoforged.neoforge.event.server.ServerStoppingEvent;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.SecureRandom;
import java.util.HexFormat;
import java.util.Locale;
import java.util.Set;
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
    public static final String ENV_GRPC_SHARED_KEY = "CIVQUEST_GRPC_SHARED_KEY";
    public static final String PROP_GRPC_ADVERTISED_HOST = "civquest.grpc.advertised_host";
    public static final String ENV_GRPC_ADVERTISED_HOST = "CIVQUEST_GRPC_ADVERTISED_HOST";
    public static final String PROP_DISCOVERY_PATH = "civquest.discovery.path";
    public static final String PROP_GRPC_ENABLED = "civquest.grpc.enabled";
    public static final String PROP_SERVER_ID = "civquest.server.id";
    public static final String ENV_SERVER_ID = "CIVQUEST_SERVER_ID";

    public static final String DEFAULT_BIND = "127.0.0.1:0";
    public static final String DEFAULT_DISCOVERY_PATH = "civquest-server-meta.json";

    private static final SecureRandom SECURE_RANDOM = new SecureRandom();
    private static final boolean IS_POSIX =
            FileSystems.getDefault().supportedFileAttributeViews().contains("posix");
    private static final Set<PosixFilePermission> POSIX_0600 =
            PosixFilePermissions.fromString("rw-------");

    private final String configuredHost;
    private final int configuredPort;
    private final String sharedKey;
    private final Path discoveryPath;
    private final boolean enabled;
    private final StreamRegistry streamRegistry;
    private final AgentEntityManager agentEntityManager;
    private volatile MinecraftServer minecraftServer;
    private volatile String serverId;
    private volatile String worldName;

    private Server server;
    private HealthStatusManager healthStatusManager;
    private SharedKeyAuthInterceptor authInterceptor;
    private CivQuestBridgeServiceImpl bridgeService;
    private int boundPort = -1;
    private String boundHost;
    private String transport = "tcp";
    private Thread shutdownHookThread;

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
        this.sharedKey = resolveSharedKey(null);

        String discPath = System.getProperty(PROP_DISCOVERY_PATH, DEFAULT_DISCOVERY_PATH).trim();
        this.discoveryPath = Path.of(discPath);
        this.streamRegistry = new StreamRegistry();
        this.agentEntityManager = new AgentEntityManager(this.streamRegistry);
    }

    public CivQuestBridgeServer(String host, int port, String sharedKey, Path discoveryPath) {
        this(host, port, sharedKey, discoveryPath, new StreamRegistry(), null);
    }

    public CivQuestBridgeServer(String host, int port, String sharedKey, Path discoveryPath, StreamRegistry streamRegistry) {
        this(host, port, sharedKey, discoveryPath, streamRegistry, null);
    }

    public CivQuestBridgeServer(String host, int port, String sharedKey, Path discoveryPath,
                                StreamRegistry streamRegistry, AgentEntityManager agentEntityManager) {
        this.enabled = true;
        this.configuredHost = (host == null || host.isBlank()) ? "127.0.0.1" : host.trim();
        this.configuredPort = port;
        this.sharedKey = resolveSharedKey(sharedKey);
        this.discoveryPath = discoveryPath != null ? discoveryPath : Path.of(DEFAULT_DISCOVERY_PATH);
        this.streamRegistry = streamRegistry != null ? streamRegistry : new StreamRegistry();
        this.agentEntityManager = agentEntityManager != null ? agentEntityManager : new AgentEntityManager(this.streamRegistry);
        if (this.agentEntityManager.getStreamRegistry() == null) {
            this.agentEntityManager.setStreamRegistry(this.streamRegistry);
        }
    }

    public static String generateSecureSharedKey() {
        byte[] bytes = new byte[32];
        SECURE_RANDOM.nextBytes(bytes);
        return HexFormat.of().formatHex(bytes);
    }

    private static String resolveSharedKey(String explicitKey) {
        if (explicitKey != null && !explicitKey.isBlank()) {
            return explicitKey.trim();
        }
        String key = System.getProperty(PROP_GRPC_SHARED_KEY);
        if (key == null || key.isBlank()) {
            key = System.getenv(ENV_GRPC_SHARED_KEY);
        }
        if (key != null && !key.isBlank()) {
            return key.trim();
        }
        return generateSecureSharedKey();
    }

    private static boolean isWildcardHost(String host) {
        if (host == null) {
            return false;
        }
        String trimmed = host.trim();
        return "0.0.0.0".equals(trimmed) || "::".equals(trimmed) || "[::]".equals(trimmed);
    }

    public String resolveAdvertisedHost() {
        if (isWildcardHost(configuredHost)) {
            String advertised = System.getProperty(PROP_GRPC_ADVERTISED_HOST);
            if (advertised == null || advertised.isBlank()) {
                advertised = System.getenv(ENV_GRPC_ADVERTISED_HOST);
            }
            if (advertised != null && !advertised.isBlank()) {
                return advertised.trim();
            }
            return "127.0.0.1";
        }
        return boundHost != null ? boundHost : configuredHost;
    }

    public String getAdvertisedHost() {
        return resolveAdvertisedHost();
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

        if (streamRegistry != null) {
            streamRegistry.reopen();
        }

        this.healthStatusManager = new HealthStatusManager();
        this.authInterceptor = new SharedKeyAuthInterceptor(sharedKey);
        this.bridgeService = new CivQuestBridgeServiceImpl(streamRegistry, agentEntityManager, this::getMinecraftServer);

        InetSocketAddress socketAddress = new InetSocketAddress(configuredHost, configuredPort);
        this.server = NettyServerBuilder.forAddress(socketAddress)
                .addService(bridgeService)
                .addService(healthStatusManager.getHealthService())
                .intercept(authInterceptor)
                .build()
                .start();

        this.boundPort = server.getPort();
        if (this.boundPort <= 0 || this.boundPort > 65535) {
            throw new IllegalStateException("Failed to bind gRPC server: invalid port " + this.boundPort);
        }
        this.boundHost = configuredHost;
        this.transport = "tcp";

        healthStatusManager.setStatus("", HealthCheckResponse.ServingStatus.SERVING);
        healthStatusManager.setStatus(CivQuestBridgeServiceGrpc.SERVICE_NAME, HealthCheckResponse.ServingStatus.SERVING);

        this.shutdownHookThread = new Thread(() -> {
            LOGGER.info("CivQuestBridgeServer JVM shutdown hook triggered");
            stop();
        }, "civquest-grpc-shutdown-hook");
        Runtime.getRuntime().addShutdownHook(shutdownHookThread);

        writeDiscoveryFile("READY");
        LOGGER.info("CivQuestBridgeServer listening on {}:{} (PID: {})",
                boundHost, boundPort, ProcessHandle.current().pid());
    }

    public synchronized void stop() {
        if (shutdownHookThread != null && Thread.currentThread() != shutdownHookThread) {
            try {
                Runtime.getRuntime().removeShutdownHook(shutdownHookThread);
            } catch (IllegalStateException ignored) {
                // JVM is already shutting down
            }
            shutdownHookThread = null;
        }

        if (server == null || server.isShutdown()) {
            return;
        }

        LOGGER.info("Stopping CivQuestBridgeServer on port {}", boundPort);
        if (healthStatusManager != null) {
            healthStatusManager.setStatus("", HealthCheckResponse.ServingStatus.NOT_SERVING);
            healthStatusManager.setStatus(CivQuestBridgeServiceGrpc.SERVICE_NAME, HealthCheckResponse.ServingStatus.NOT_SERVING);
        }

        writeDiscoveryFile("STOPPED");

        if (streamRegistry != null) {
            streamRegistry.closeAll();
        }

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
        long timestampMs = System.currentTimeMillis();
        String json = String.format(Locale.ROOT,
                "{\n" +
                "  \"status\": \"%s\",\n" +
                "  \"grpc_transport\": \"%s\",\n" +
                "  \"grpc_port\": %d,\n" +
                "  \"grpc_host\": \"%s\",\n" +
                "  \"server_pid\": %d,\n" +
                "  \"shared_key\": \"%s\",\n" +
                "  \"server_id\": \"%s\",\n" +
                "  \"world_name\": \"%s\",\n" +
                "  \"timestamp_ms\": %d\n" +
                "}\n",
                escapeJson(status),
                escapeJson(transport),
                boundPort,
                escapeJson(resolveAdvertisedHost()),
                pid,
                escapeJson(sharedKey),
                escapeJson(getServerId()),
                escapeJson(getWorldName()),
                timestampMs
        );

        Path target = discoveryPath.toAbsolutePath().normalize();
        Path parent = target.getParent();
        Path tempFile = null;
        try {
            if (parent != null && !Files.exists(parent)) {
                Files.createDirectories(parent);
            }
            tempFile = Files.createTempFile(
                    parent != null ? parent : Path.of("."),
                    "civquest-meta-",
                    ".tmp"
            );
            if (IS_POSIX) {
                try {
                    Files.setPosixFilePermissions(tempFile, POSIX_0600);
                } catch (IOException | UnsupportedOperationException e) {
                    LOGGER.debug("Could not set POSIX permissions on temp file: {}", e.getMessage());
                }
            }
            Files.writeString(tempFile, json, StandardCharsets.UTF_8,
                    StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING);
            try {
                Files.move(tempFile, target, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
            } catch (AtomicMoveNotSupportedException e) {
                Files.move(tempFile, target, StandardCopyOption.REPLACE_EXISTING);
            }
            if (IS_POSIX) {
                try {
                    Files.setPosixFilePermissions(target, POSIX_0600);
                } catch (IOException | UnsupportedOperationException e) {
                    LOGGER.debug("Could not set POSIX permissions on target file: {}", e.getMessage());
                }
            }
            LOGGER.info("Wrote discovery metadata ({}) to {}", status, target);
        } catch (IOException e) {
            LOGGER.error("Failed to write discovery metadata file: {}", e.getMessage(), e);
        } finally {
            if (tempFile != null) {
                try {
                    Files.deleteIfExists(tempFile);
                } catch (IOException ignored) {
                }
            }
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
        setMinecraftServer(event.getServer());
    }

    public void onServerStarted(ServerStartedEvent event) {
        LOGGER.info("CivQuest server started: activating gRPC bridge");
        setMinecraftServer(event.getServer());
        if (event.getServer() != null) {
            try {
                event.getServer().getGameRules().getRule(GameRules.RULE_MOBGRIEFING).set(false, event.getServer());
                LOGGER.info("CivQuest: Enforced GameRule mobGriefing = false to protect settlements from creeper/mob destruction");
            } catch (Exception e) {
                LOGGER.warn("Failed to set GameRule mobGriefing = false", e);
            }
        }
        try {
            start();
        } catch (IOException e) {
            LOGGER.error("Failed to start CivQuestBridgeServer on ServerStartedEvent", e);
            throw new RuntimeException("Failed to start CivQuest gRPC server", e);
        }
    }

    public void onServerStopping(ServerStoppingEvent event) {
        LOGGER.info("CivQuest server stopping: shutting down gRPC bridge");
        if (agentEntityManager != null) {
            agentEntityManager.clearAll();
        }
        stop();
        setMinecraftServer(null);
    }

    public MinecraftServer getMinecraftServer() {
        return minecraftServer;
    }

    public void setMinecraftServer(MinecraftServer minecraftServer) {
        this.minecraftServer = minecraftServer;
    }

    public AgentEntityManager getAgentEntityManager() {
        return agentEntityManager;
    }

    public static String sanitizeServerId(String input) {
        if (input == null) {
            return "default";
        }
        String cleaned = input.replaceAll("[^a-zA-Z0-9_-]", "-");
        cleaned = cleaned.replaceAll("^[^a-zA-Z0-9]+", "");
        cleaned = cleaned.replaceAll("[^a-zA-Z0-9]+$", "");
        if (cleaned.length() > 32) {
            cleaned = cleaned.substring(0, 32);
            cleaned = cleaned.replaceAll("[^a-zA-Z0-9]+$", "");
        }
        if (cleaned.isEmpty()) {
            return "default";
        }
        return cleaned;
    }

    public synchronized String resolveServerId() {
        // 1) Check system property civquest.server.id or env var CIVQUEST_SERVER_ID
        String prop = System.getProperty(PROP_SERVER_ID);
        if (prop != null && !prop.isBlank()) {
            String sanitized = sanitizeServerId(prop.trim());
            if (!"default".equals(sanitized)) {
                this.serverId = sanitized;
                return sanitized;
            }
        }
        String env = System.getenv(ENV_SERVER_ID);
        if (env != null && !env.isBlank()) {
            String sanitized = sanitizeServerId(env.trim());
            if (!"default".equals(sanitized)) {
                this.serverId = sanitized;
                return sanitized;
            }
        }

        // If explicitly set and not "default"
        if (this.serverId != null && !this.serverId.isBlank() && !"default".equals(this.serverId)) {
            return this.serverId;
        }

        // 2) If minecraftServer != null, read or persist civquest-server-id.txt in LevelResource.ROOT
        if (minecraftServer != null) {
            try {
                Path rootPath = minecraftServer.getWorldPath(LevelResource.ROOT);
                return resolveServerIdFromWorldPath(rootPath);
            } catch (Exception e) {
                LOGGER.warn("Failed to resolve server ID from Minecraft server: {}", e.getMessage());
            }
        }

        // 3) Fallback: return "default"
        return "default";
    }

    public synchronized String resolveServerIdFromWorldPath(Path rootPath) {
        if (rootPath != null) {
            Path idFile = rootPath.resolve("civquest-server-id.txt");
            if (Files.exists(idFile)) {
                try {
                    String content = Files.readString(idFile, StandardCharsets.UTF_8).trim();
                    String sanitized = sanitizeServerId(content);
                    if (!"default".equals(sanitized)) {
                        this.serverId = sanitized;
                        return sanitized;
                    }
                } catch (IOException e) {
                    LOGGER.warn("Failed to read civquest-server-id.txt from {}: {}", idFile, e.getMessage());
                }
            }
            // Generate new ID: <world_name>-<short-uuid>
            String rawWorld = getWorldName();
            String worldClean = sanitizeServerId(rawWorld);
            if ("default".equals(worldClean)) {
                worldClean = "world";
            }
            if (worldClean.length() > 23) {
                worldClean = worldClean.substring(0, 23).replaceAll("[^a-zA-Z0-9]+$", "");
            }
            String shortUuid = UUID.randomUUID().toString().replace("-", "").substring(0, 8);
            String generated = sanitizeServerId(worldClean + "-" + shortUuid);
            try {
                if (!Files.exists(rootPath)) {
                    Files.createDirectories(rootPath);
                }
                Files.writeString(idFile, generated, StandardCharsets.UTF_8,
                        StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING);
            } catch (IOException e) {
                LOGGER.warn("Failed to write civquest-server-id.txt to {}: {}", idFile, e.getMessage());
            }
            this.serverId = generated;
            return generated;
        }
        return "default";
    }

    public synchronized String getServerId() {
        if (serverId != null && !serverId.isBlank() && !"default".equals(serverId)) {
            return serverId;
        }
        return resolveServerId();
    }

    public synchronized void setServerId(String serverId) {
        if (serverId != null && !serverId.isBlank()) {
            this.serverId = sanitizeServerId(serverId.trim());
        } else {
            this.serverId = serverId;
        }
    }

    public synchronized String getWorldName() {
        if (worldName != null && !worldName.isBlank()) {
            return worldName;
        }
        if (minecraftServer != null) {
            try {
                if (minecraftServer.getWorldData() != null && minecraftServer.getWorldData().getLevelName() != null) {
                    this.worldName = minecraftServer.getWorldData().getLevelName();
                    return this.worldName;
                }
            } catch (Exception ignored) {
            }
            try {
                Path root = minecraftServer.getWorldPath(LevelResource.ROOT);
                if (root != null && root.getFileName() != null) {
                    this.worldName = root.getFileName().toString();
                    return this.worldName;
                }
            } catch (Exception ignored) {
            }
        }
        String prop = System.getProperty("civquest.world.name");
        if (prop != null && !prop.isBlank()) {
            return prop.trim();
        }
        return "default";
    }

    public synchronized void setWorldName(String worldName) {
        this.worldName = worldName;
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

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public CivQuestBridgeServiceImpl getBridgeService() {
        return bridgeService;
    }
}
