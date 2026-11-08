package com.civquest.mod.client.driver;

import com.mojang.blaze3d.platform.NativeImage;
import com.mojang.logging.LogUtils;
import net.minecraft.client.KeyMapping;
import net.minecraft.client.Minecraft;
import net.minecraft.client.Screenshot;
import net.minecraft.client.player.AbstractClientPlayer;
import net.minecraft.client.player.LocalPlayer;
import net.minecraft.world.entity.Entity;
import net.neoforged.neoforge.client.event.ClientPlayerNetworkEvent;
import net.neoforged.neoforge.client.event.ClientTickEvent;
import org.slf4j.Logger;

import java.io.File;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;

/**
 * Autonomous client-side driver harness for headless or automated testing.
 * Monitors client ticks, detects world join, simulates keyboard navigation,
 * points camera towards active Agent avatars, issues chat/whisper commands,
 * and captures in-game screenshots.
 *
 * <p>ADR-014 Invariant: This class references client-only APIs (Minecraft, KeyMapping, Screenshot)
 * and must NEVER be loaded or referenced on dedicated servers.</p>
 */
public class ClientDriverHarness {
    private static final Logger LOGGER = LogUtils.getLogger();

    public static final String PROP_ENABLED = "civquest.driver.enabled";
    public static final String PROP_TARGET = "civquest.driver.target";
    public static final String PROP_SCREENSHOTS_DIR = "civquest.driver.screenshots.dir";
    public static final String PROP_ACTIONS = "civquest.driver.actions";
    public static final String PROP_EXIT_ON_COMPLETE = "civquest.driver.exitOnComplete";

    private static volatile boolean initialized = false;
    private static volatile boolean enabled = false;
    private static volatile String defaultTarget = "aria";
    private static volatile String screenshotsDir = null;
    private static volatile boolean exitOnComplete = false;
    private static final List<DriverAction> actionQueue = new ArrayList<>();

    private static volatile boolean worldLoaded = false;
    private static volatile int currentActionIndex = 0;
    private static volatile int actionTickCounter = 0;
    private static volatile boolean completed = false;
    private static volatile String currentKeyHeld = null;
    private static volatile int connectionWaitTicks = 0;
    public static final int MAX_CONNECTION_WAIT_TICKS = 600; // 30s timeout

    public static boolean isEnabled() {
        return Boolean.parseBoolean(System.getProperty(PROP_ENABLED, "false"));
    }

    public static synchronized void initialize() {
        if (initialized) {
            return;
        }
        initialized = true;
        enabled = isEnabled();

        if (!enabled) {
            return;
        }

        defaultTarget = System.getProperty(PROP_TARGET, "aria").trim();
        screenshotsDir = System.getProperty(PROP_SCREENSHOTS_DIR);
        exitOnComplete = Boolean.parseBoolean(System.getProperty(PROP_EXIT_ON_COMPLETE, "false"));

        String actionsConfig = System.getProperty(PROP_ACTIONS);
        actionQueue.clear();

        if (actionsConfig != null && !actionsConfig.isBlank()) {
            parseActions(actionsConfig);
        } else {
            buildDefaultScenario();
        }

        LOGGER.info("[ClientDriver] Harness initialized: target='{}', screenshotsDir='{}', exitOnComplete={}, actionsCount={}",
                defaultTarget, screenshotsDir, exitOnComplete, actionQueue.size());
    }

    private static void buildDefaultScenario() {
        actionQueue.add(new DriverAction.WaitAction(40));
        actionQueue.add(new DriverAction.LookAtAgentAction(defaultTarget));
        actionQueue.add(new DriverAction.WaitAction(10));
        actionQueue.add(new DriverAction.ScreenshotAction("civquest_avatar_view_01.png"));
        actionQueue.add(new DriverAction.WaitAction(10));
        actionQueue.add(new DriverAction.WhisperAction(defaultTarget, "Hello " + defaultTarget + " from automated client driver harness!"));
        actionQueue.add(new DriverAction.WaitAction(20));
        actionQueue.add(new DriverAction.HoldKeyAction("FORWARD", 20));
        actionQueue.add(new DriverAction.HoldKeyAction("JUMP", 5));
        actionQueue.add(new DriverAction.HoldKeyAction("SNEAK", 15));
        actionQueue.add(new DriverAction.WaitAction(15));
        actionQueue.add(new DriverAction.LookAtAgentAction(defaultTarget));
        actionQueue.add(new DriverAction.WaitAction(10));
        actionQueue.add(new DriverAction.ScreenshotAction("civquest_avatar_view_02.png"));
        actionQueue.add(new DriverAction.WaitAction(30));
        if (exitOnComplete) {
            actionQueue.add(new DriverAction.ExitAction());
        }
    }

    private static void parseActions(String config) {
        String delimiter = config.contains("|") ? "\\|" : "[,;]";
        String[] tokens = config.split(delimiter);
        for (String rawToken : tokens) {
            String token = rawToken.trim();
            if (token.isEmpty()) continue;

            String[] parts = token.split(":", 3);
            String actionName = parts[0].toLowerCase(Locale.ROOT);

            switch (actionName) {
                case "wait", "delay" -> {
                    int ticks = parts.length > 1 ? Integer.parseInt(parts[1].trim()) : 20;
                    actionQueue.add(new DriverAction.WaitAction(ticks));
                }
                case "look", "look_at", "look_at_agent" -> {
                    String target = parts.length > 1 ? parts[1].trim() : defaultTarget;
                    actionQueue.add(new DriverAction.LookAtAgentAction(target));
                }
                case "forward", "backward", "left", "right", "jump", "sneak", "sprint" -> {
                    int ticks = parts.length > 1 ? Integer.parseInt(parts[1].trim()) : 20;
                    actionQueue.add(new DriverAction.HoldKeyAction(actionName.toUpperCase(Locale.ROOT), ticks));
                }
                case "release", "release_keys" -> {
                    actionQueue.add(new DriverAction.ReleaseKeysAction());
                }
                case "chat" -> {
                    String msg = parts.length > 1 ? parts[1].trim() : "Hello from CivQuest driver!";
                    actionQueue.add(new DriverAction.ChatAction(msg));
                }
                case "whisper" -> {
                    String target = defaultTarget;
                    String msg = "Hello!";
                    if (parts.length >= 3) {
                        target = parts[1].trim();
                        msg = parts[2].trim();
                    } else if (parts.length == 2) {
                        msg = parts[1].trim();
                    }
                    actionQueue.add(new DriverAction.WhisperAction(target, msg));
                }
                case "screenshot" -> {
                    String filename = parts.length > 1 ? parts[1].trim() : "civquest_driver_screenshot.png";
                    actionQueue.add(new DriverAction.ScreenshotAction(filename));
                }
                case "exit", "stop" -> {
                    actionQueue.add(new DriverAction.ExitAction());
                }
                default -> LOGGER.warn("[ClientDriver] Unknown action token: '{}'", token);
            }
        }
    }

    public static synchronized void reset() {
        currentActionIndex = 0;
        actionTickCounter = 0;
        connectionWaitTicks = 0;
        completed = false;
        if (currentKeyHeld != null) {
            Minecraft mc = Minecraft.getInstance();
            setKey(mc, currentKeyHeld, false);
            currentKeyHeld = null;
        }
    }

    public static void onClientLoggedIn(ClientPlayerNetworkEvent.LoggingIn event) {
        if (!enabled) return;
        LOGGER.info("[ClientDriver] Player logged in to server, resetting scenario execution.");
        reset();
    }

    public static void onClientLoggedOut(ClientPlayerNetworkEvent.LoggingOut event) {
        if (!enabled) return;
        LOGGER.info("[ClientDriver] Player logged out from server.");
        Minecraft mc = Minecraft.getInstance();
        releaseAllKeys(mc);
        reset();
    }

    public static void onClientTick(ClientTickEvent.Post event) {
        if (!enabled || completed) {
            return;
        }

        Minecraft mc = Minecraft.getInstance();
        if (mc.level == null || mc.player == null) {
            if (worldLoaded) {
                LOGGER.info("[ClientDriver] Client level or player became null; resetting.");
                worldLoaded = false;
                releaseAllKeys(mc);
                reset();
            } else if (exitOnComplete) {
                connectionWaitTicks++;
                if (connectionWaitTicks >= MAX_CONNECTION_WAIT_TICKS) {
                    LOGGER.error("[ClientDriver] Connection watchdog timed out after {} ticks (30s) waiting for server join. Stopping client.", MAX_CONNECTION_WAIT_TICKS);
                    completed = true;
                    mc.stop();
                }
            }
            return;
        }

        if (!worldLoaded) {
            worldLoaded = true;
            connectionWaitTicks = 0;
            LOGGER.info("[ClientDriver] Player joined world. Starting automated scenario execution.");
        }

        if (currentActionIndex >= actionQueue.size()) {
            completed = true;
            releaseAllKeys(mc);
            LOGGER.info("[ClientDriver] All scenario actions completed successfully!");
            if (exitOnComplete) {
                LOGGER.info("[ClientDriver] exitOnComplete enabled, closing client...");
                mc.stop();
            }
            return;
        }

        DriverAction action = actionQueue.get(currentActionIndex);
        executeAction(mc, action);
    }

    private static void executeAction(Minecraft mc, DriverAction action) {
        if (action instanceof DriverAction.WaitAction waitAction) {
            actionTickCounter++;
            if (actionTickCounter >= waitAction.ticks()) {
                actionTickCounter = 0;
                currentActionIndex++;
            }
        } else if (action instanceof DriverAction.LookAtAgentAction lookAction) {
            String target = (lookAction.target() != null && !lookAction.target().isBlank()) ? lookAction.target() : defaultTarget;
            Entity agentEntity = findAgentEntity(mc, target);
            if (agentEntity != null) {
                lookAtEntity(mc.player, agentEntity);
                LOGGER.info("[ClientDriver] Oriented camera towards agent '{}' (pos={})", target, agentEntity.position());
            } else {
                LOGGER.warn("[ClientDriver] Could not find agent entity '{}' in render range", target);
            }
            currentActionIndex++;
        } else if (action instanceof DriverAction.HoldKeyAction holdKeyAction) {
            if (actionTickCounter == 0) {
                setKey(mc, holdKeyAction.key(), true);
                currentKeyHeld = holdKeyAction.key();
                LOGGER.info("[ClientDriver] Pressing key '{}' for {} ticks", holdKeyAction.key(), holdKeyAction.ticks());
            }
            actionTickCounter++;
            if (actionTickCounter >= holdKeyAction.ticks()) {
                setKey(mc, holdKeyAction.key(), false);
                currentKeyHeld = null;
                actionTickCounter = 0;
                currentActionIndex++;
            }
        } else if (action instanceof DriverAction.ReleaseKeysAction) {
            releaseAllKeys(mc);
            LOGGER.info("[ClientDriver] Released all input keys");
            currentActionIndex++;
        } else if (action instanceof DriverAction.ChatAction chatAction) {
            sendChat(mc, chatAction.message());
            currentActionIndex++;
        } else if (action instanceof DriverAction.WhisperAction whisperAction) {
            sendWhisper(mc, whisperAction.target(), whisperAction.message());
            currentActionIndex++;
        } else if (action instanceof DriverAction.ScreenshotAction screenshotAction) {
            captureScreenshot(mc, screenshotAction.filename(), screenshotsDir);
            currentActionIndex++;
        } else if (action instanceof DriverAction.ExitAction) {
            LOGGER.info("[ClientDriver] Executing ExitAction: stopping Minecraft client");
            releaseAllKeys(mc);
            completed = true;
            mc.stop();
            currentActionIndex++;
        }
    }

    public static Entity findAgentEntity(Minecraft mc, String targetName) {
        if (mc.level == null || mc.player == null) return null;

        // 1. Search player list
        for (AbstractClientPlayer p : mc.level.players()) {
            if (p == mc.player) continue;
            if (targetName != null && !targetName.isBlank()) {
                if (p.getGameProfile().getName().equalsIgnoreCase(targetName)
                        || p.getStringUUID().equalsIgnoreCase(targetName)
                        || p.getName().getString().equalsIgnoreCase(targetName)) {
                    return p;
                }
            } else {
                return p;
            }
        }

        // 2. Search general entity list if not found in players()
        for (Entity entity : mc.level.entitiesForRendering()) {
            if (entity == mc.player) continue;
            if (targetName != null && !targetName.isBlank()) {
                if (entity.getName().getString().equalsIgnoreCase(targetName)) {
                    return entity;
                }
            }
        }

        // 3. Fallback: return any other player only if no specific targetName was requested
        if (targetName == null || targetName.isBlank()) {
            for (AbstractClientPlayer p : mc.level.players()) {
                if (p != mc.player) return p;
            }
        }

        return null;
    }

    public static void lookAtEntity(LocalPlayer player, Entity target) {
        if (player == null || target == null) return;
        double dx = target.getX() - player.getX();
        double dy = (target.getY() + target.getEyeHeight()) - (player.getY() + player.getEyeHeight());
        double dz = target.getZ() - player.getZ();
        double horizontalDist = Math.sqrt(dx * dx + dz * dz);

        float yaw = (float) Math.toDegrees(Math.atan2(-dx, dz));
        float pitch = (float) -Math.toDegrees(Math.atan2(dy, horizontalDist));

        player.setYRot(yaw);
        player.setXRot(pitch);
        player.setYHeadRot(yaw);
        player.yRotO = yaw;
        player.xRotO = pitch;
        player.yHeadRotO = yaw;
    }

    public static void captureScreenshot(Minecraft mc, String filename, String customDir) {
        if (mc == null) return;
        try {
            File targetDir;
            if (customDir != null && !customDir.isBlank()) {
                targetDir = new File(customDir);
            } else {
                targetDir = new File(mc.gameDirectory, "screenshots");
            }
            if (!targetDir.exists()) {
                targetDir.mkdirs();
            }

            File targetFile = new File(targetDir, filename);

            // Synchronously grab framebuffer NativeImage and write directly using try-with-resources
            try (NativeImage nativeImage = Screenshot.takeScreenshot(mc.getMainRenderTarget())) {
                if (nativeImage != null) {
                    nativeImage.writeToFile(targetFile);
                    LOGGER.info("[ClientDriver] Screenshot written directly to: {}", targetFile.getAbsolutePath());
                }
            } catch (Throwable t) {
                LOGGER.warn("[ClientDriver] Direct NativeImage capture failed: {}", t.getMessage());
            }
        } catch (Exception e) {
            LOGGER.error("[ClientDriver] Failed to capture screenshot '{}': {}", filename, e.getMessage(), e);
        }
    }

    public static void setKey(Minecraft mc, String keyName, boolean pressed) {
        if (mc == null || mc.options == null || keyName == null) return;
        KeyMapping mapping = switch (keyName.toUpperCase(Locale.ROOT)) {
            case "FORWARD", "UP", "W" -> mc.options.keyUp;
            case "BACKWARD", "DOWN", "S" -> mc.options.keyDown;
            case "LEFT", "A" -> mc.options.keyLeft;
            case "RIGHT", "D" -> mc.options.keyRight;
            case "JUMP", "SPACE" -> mc.options.keyJump;
            case "SNEAK", "SHIFT" -> mc.options.keyShift;
            case "SPRINT", "CTRL" -> mc.options.keySprint;
            default -> null;
        };
        if (mapping != null) {
            mapping.setDown(pressed);
            KeyMapping.set(mapping.getKey(), pressed);
        }
    }

    public static void releaseAllKeys(Minecraft mc) {
        if (mc == null || mc.options == null) return;
        for (KeyMapping mapping : new KeyMapping[]{
                mc.options.keyUp, mc.options.keyDown, mc.options.keyLeft, mc.options.keyRight,
                mc.options.keyJump, mc.options.keyShift, mc.options.keySprint
        }) {
            if (mapping != null) {
                mapping.setDown(false);
                KeyMapping.set(mapping.getKey(), false);
            }
        }
        currentKeyHeld = null;
    }

    public static void sendChat(Minecraft mc, String message) {
        if (mc == null || mc.player == null || mc.player.connection == null || message == null) return;
        if (message.startsWith("/")) {
            mc.player.connection.sendCommand(message.substring(1));
        } else {
            mc.player.connection.sendChat(message);
        }
        LOGGER.info("[ClientDriver] Dispatched player message: '{}'", message);
    }

    public static void sendWhisper(Minecraft mc, String agentTarget, String message) {
        if (mc == null || mc.player == null || mc.player.connection == null) return;
        String cmd = "civquest tell " + agentTarget + " " + message;
        mc.player.connection.sendCommand(cmd);
        LOGGER.info("[ClientDriver] Dispatched agent whisper: /{}", cmd);
    }

    public static List<DriverAction> getActionQueue() {
        return Collections.unmodifiableList(actionQueue);
    }

    public static boolean isCompleted() {
        return completed;
    }
}
