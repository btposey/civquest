package com.civquest.mod;

import com.civquest.mod.gametest.SettlerGameTest;
import com.mojang.logging.LogUtils;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.network.chat.Component;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoUpdatePacket;
import net.minecraft.server.level.ServerPlayer;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.fml.common.Mod;
import net.neoforged.neoforge.common.NeoForge;
import net.neoforged.neoforge.event.CommandEvent;
import net.neoforged.neoforge.event.RegisterGameTestsEvent;
import net.neoforged.neoforge.event.ServerChatEvent;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.event.entity.player.PlayerEvent;
import net.neoforged.neoforge.event.server.ServerStartedEvent;
import net.neoforged.neoforge.event.server.ServerStartingEvent;
import net.neoforged.neoforge.event.server.ServerStoppingEvent;
import net.neoforged.neoforge.event.tick.ServerTickEvent;
import net.neoforged.neoforge.network.event.RegisterPayloadHandlersEvent;
import net.neoforged.neoforge.network.registration.PayloadRegistrar;
import org.slf4j.Logger;

import java.util.EnumSet;
import java.util.List;
import java.util.Locale;

@Mod(CivQuestMod.MOD_ID)
public class CivQuestMod {
    public static final String MOD_ID = "civquest";
    private static final Logger LOGGER = LogUtils.getLogger();
    private CivQuestBridgeServer bridgeServer;

    public CivQuestMod(IEventBus modEventBus) {
        LOGGER.info("CivQuest mod initializing");
        modEventBus.addListener(this::onRegisterGameTests);
        modEventBus.addListener(this::onRegisterPayloadHandlers);

        NeoForge.EVENT_BUS.addListener(this::onServerStarting);
        NeoForge.EVENT_BUS.addListener(this::onServerStarted);
        NeoForge.EVENT_BUS.addListener(this::onServerStopping);
        NeoForge.EVENT_BUS.addListener(this::onPlayerLoggedIn);
        NeoForge.EVENT_BUS.addListener(this::onServerTick);
        NeoForge.EVENT_BUS.addListener(this::onLivingDamage);
        NeoForge.EVENT_BUS.addListener(this::onServerChat);
        NeoForge.EVENT_BUS.addListener(this::onCommand);
    }

    private void onRegisterPayloadHandlers(RegisterPayloadHandlersEvent event) {
        LOGGER.info("Registering CivQuest network payloads");
        PayloadRegistrar registrar = event.registrar("1").optional();
        registrar.playToClient(
                ClientboundSettlerSkinPacket.TYPE,
                ClientboundSettlerSkinPacket.STREAM_CODEC,
                (payload, context) -> {
                    LOGGER.debug("Received ClientboundSettlerSkinPacket on client: agentId='{}', uuid='{}'",
                            payload.agentId(), payload.settlerUuid());
                }
        );
    }

    private void onRegisterGameTests(RegisterGameTestsEvent event) {
        LOGGER.info("Registering CivQuest GameTests");
        event.register(SettlerGameTest.class);
    }

    private void onServerStarting(ServerStartingEvent event) {
        if (bridgeServer == null) {
            bridgeServer = new CivQuestBridgeServer();
        }
        bridgeServer.onServerStarting(event);
    }

    private void onServerStarted(ServerStartedEvent event) {
        if (bridgeServer != null) {
            bridgeServer.onServerStarted(event);
        }
    }

    private void onServerStopping(ServerStoppingEvent event) {
        if (bridgeServer != null) {
            bridgeServer.onServerStopping(event);
        }
    }

    private void onServerTick(ServerTickEvent.Post event) {
        SettlerEntityManager manager = getSettlerEntityManager();
        if (manager != null) {
            manager.onServerTick(event);
        }
    }

    private void onLivingDamage(LivingDamageEvent.Post event) {
        SettlerEntityManager manager = getSettlerEntityManager();
        if (manager != null) {
            manager.onLivingDamage(event);
        }
    }

    private void onPlayerLoggedIn(PlayerEvent.PlayerLoggedInEvent event) {
        if (event.getEntity() instanceof ServerPlayer player) {
            SettlerEntityManager manager = getSettlerEntityManager();
            if (manager != null && !manager.getAllSettlers().isEmpty()) {
                List<ServerPlayer> activeSettlers = new java.util.ArrayList<>(manager.getAllSettlers().values());
                ClientboundPlayerInfoUpdatePacket infoPacket = new ClientboundPlayerInfoUpdatePacket(
                        EnumSet.of(
                                ClientboundPlayerInfoUpdatePacket.Action.ADD_PLAYER,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LISTED,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LATENCY,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_GAME_MODE
                        ),
                        activeSettlers
                );
                player.connection.send(infoPacket);

                for (SettlerEntity settler : manager.getAllSettlers().values()) {
                    if (settler.getSkinPngBytes() != null && settler.getSkinPngBytes().length > 0) {
                        ClientboundSettlerSkinPacket skinPacket = new ClientboundSettlerSkinPacket(
                                settler.getUUID(),
                                settler.getAgentId(),
                                settler.getSkinPngBytes()
                        );
                        player.connection.send(skinPacket.toVanillaClientbound());
                    }
                }
                LOGGER.info("Synchronized {} active settlers to joining player '{}'",
                        activeSettlers.size(), player.getName().getString());
            }
        }
    }

    private void onServerChat(ServerChatEvent event) {
        ServerPlayer player = event.getPlayer();
        if (player == null) {
            return;
        }

        SettlerEntityManager manager = getSettlerEntityManager();
        if (manager == null || manager.getAllSettlers().isEmpty()) {
            return;
        }

        String rawMessage = event.getRawText();
        if (rawMessage == null || rawMessage.isBlank()) {
            return;
        }

        double hearingRadiusSq = 32.0 * 32.0;
        String playerUuid = player.getUUID().toString();
        String playerName = player.getName().getString();

        for (SettlerEntity settler : manager.getAllSettlers().values()) {
            if (settler.level() != player.level()) {
                continue;
            }

            double distSq = settler.distanceToSqr(player);
            boolean inHearingRange = distSq <= hearingRadiusSq;
            boolean mentionsSettler = rawMessage.toLowerCase(Locale.ROOT).contains(settler.getGameProfile().getName().toLowerCase(Locale.ROOT))
                    || rawMessage.toLowerCase(Locale.ROOT).contains(settler.getAgentId().toLowerCase(Locale.ROOT));

            if (inHearingRange || mentionsSettler) {
                float dist = (float) Math.sqrt(distSq);
                com.civquest.mod.grpc.v1.ChatEvent chat = com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                        .setSenderUuid(playerUuid)
                        .setSenderName(playerName)
                        .setMessage(rawMessage)
                        .setDistance(dist)
                        .build();
                settler.queueIncomingChat(chat);
                LOGGER.info("Forwarded public chat from '{}' to settler '{}' (dist={})",
                        playerName, settler.getAgentId(), dist);
            }
        }
    }

    private void onCommand(CommandEvent event) {
        String command = event.getParseResults().getReader().getString();
        if (command == null || command.isBlank()) {
            return;
        }

        String trimmed = command.startsWith("/") ? command.substring(1).trim() : command.trim();
        String[] parts = trimmed.split("\\s+", 3);
        if (parts.length < 3) {
            return;
        }

        String cmdName = parts[0].toLowerCase(Locale.ROOT);
        if (!cmdName.equals("tell") && !cmdName.equals("msg") && !cmdName.equals("w")) {
            return;
        }

        String targetName = parts[1];
        String message = parts[2];

        SettlerEntityManager manager = getSettlerEntityManager();
        if (manager == null) {
            return;
        }

        SettlerEntity targetSettler = null;
        for (SettlerEntity settler : manager.getAllSettlers().values()) {
            if (settler.getGameProfile().getName().equalsIgnoreCase(targetName)
                    || settler.getAgentId().equalsIgnoreCase(targetName)) {
                targetSettler = settler;
                break;
            }
        }

        if (targetSettler != null) {
            CommandSourceStack source = event.getParseResults().getContext().getSource();
            ServerPlayer sender = source.getPlayer();
            String senderUuid = sender != null ? sender.getUUID().toString() : "CONSOLE";
            String senderName = sender != null ? sender.getName().getString() : "Console";
            float dist = (sender != null && sender.level() == targetSettler.level())
                    ? (float) Math.sqrt(targetSettler.distanceToSqr(sender))
                    : 0.0f;

            com.civquest.mod.grpc.v1.ChatEvent chat = com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                    .setSenderUuid(senderUuid)
                    .setSenderName(senderName)
                    .setMessage(message)
                    .setDistance(dist)
                    .build();
            targetSettler.queueIncomingChat(chat);

            if (sender != null) {
                sender.sendSystemMessage(Component.literal("§7You whisper to " + targetSettler.getGameProfile().getName() + ": " + message));
            } else {
                LOGGER.info("Whispered to {}: {}", targetSettler.getGameProfile().getName(), message);
            }

            event.setCanceled(true);
        }
    }

    public CivQuestBridgeServer getBridgeServer() {
        return bridgeServer;
    }

    public SettlerEntityManager getSettlerEntityManager() {
        return bridgeServer != null ? bridgeServer.getSettlerEntityManager() : null;
    }
}
