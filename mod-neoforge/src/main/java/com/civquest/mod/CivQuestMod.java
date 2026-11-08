package com.civquest.mod;

import com.civquest.mod.client.driver.ClientDriverInit;
import com.civquest.mod.gametest.AgentGameTest;
import com.mojang.brigadier.CommandDispatcher;
import com.mojang.brigadier.arguments.StringArgumentType;
import com.mojang.brigadier.builder.LiteralArgumentBuilder;
import com.mojang.brigadier.context.CommandContext;
import com.mojang.brigadier.suggestion.Suggestions;
import com.mojang.brigadier.suggestion.SuggestionsBuilder;
import com.mojang.logging.LogUtils;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.commands.Commands;
import net.minecraft.network.chat.Component;
import net.minecraft.network.protocol.game.ClientboundPlayerInfoUpdatePacket;
import net.minecraft.server.level.ServerPlayer;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.fml.common.Mod;
import net.neoforged.fml.loading.FMLEnvironment;
import net.neoforged.neoforge.common.NeoForge;
import net.neoforged.neoforge.event.CommandEvent;
import net.neoforged.neoforge.event.RegisterCommandsEvent;
import net.neoforged.neoforge.event.RegisterGameTestsEvent;
import net.neoforged.neoforge.event.ServerChatEvent;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.event.entity.player.PlayerEvent;
import net.neoforged.neoforge.event.entity.player.PlayerInteractEvent;
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
import java.util.concurrent.CompletableFuture;

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
        NeoForge.EVENT_BUS.addListener(this::onEntityInteract);
        NeoForge.EVENT_BUS.addListener(this::onCommand);
        NeoForge.EVENT_BUS.addListener(this::onRegisterCommands);

        try {
            if (FMLEnvironment.dist != null && FMLEnvironment.dist.isClient()) {
                ClientDriverInit.register(modEventBus);
            }
        } catch (Throwable t) {
            LOGGER.debug("Skipping client driver initialization: {}", t.getMessage());
        }
    }

    private void onRegisterPayloadHandlers(RegisterPayloadHandlersEvent event) {
        LOGGER.info("Registering CivQuest network payloads");
        PayloadRegistrar registrar = event.registrar("1").optional();
        registrar.playToClient(
                ClientboundAgentSkinPacket.TYPE,
                ClientboundAgentSkinPacket.STREAM_CODEC,
                (payload, context) -> {
                    LOGGER.debug("Received ClientboundAgentSkinPacket on client: agentId='{}', uuid='{}'",
                            payload.agentId(), payload.agentUuid());
                }
        );
    }

    private void onRegisterGameTests(RegisterGameTestsEvent event) {
        LOGGER.info("Registering CivQuest GameTests");
        event.register(AgentGameTest.class);
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
        AgentEntityManager manager = getAgentEntityManager();
        if (manager != null) {
            manager.onServerTick(event);
        }
    }

    private void onLivingDamage(LivingDamageEvent.Post event) {
        AgentEntityManager manager = getAgentEntityManager();
        if (manager != null) {
            manager.onLivingDamage(event);
        }
    }

    private void onPlayerLoggedIn(PlayerEvent.PlayerLoggedInEvent event) {
        if (event.getEntity() instanceof ServerPlayer player) {
            AgentEntityManager manager = getAgentEntityManager();
            if (manager != null && !manager.getAllAgents().isEmpty()) {
                List<ServerPlayer> activeAgents = new java.util.ArrayList<>(manager.getAllAgents().values());
                ClientboundPlayerInfoUpdatePacket infoPacket = new ClientboundPlayerInfoUpdatePacket(
                        EnumSet.of(
                                ClientboundPlayerInfoUpdatePacket.Action.ADD_PLAYER,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LISTED,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_LATENCY,
                                ClientboundPlayerInfoUpdatePacket.Action.UPDATE_GAME_MODE
                        ),
                        activeAgents
                );
                player.connection.send(infoPacket);

                for (AgentEntity agent : manager.getAllAgents().values()) {
                    if (agent.getSkinPngBytes() != null && agent.getSkinPngBytes().length > 0) {
                        ClientboundAgentSkinPacket skinPacket = new ClientboundAgentSkinPacket(
                                agent.getUUID(),
                                agent.getAgentId(),
                                agent.getSkinPngBytes()
                        );
                        player.connection.send(skinPacket.toVanillaClientbound());
                    }
                }
                LOGGER.info("Synchronized {} active agents to joining player '{}'",
                        activeAgents.size(), player.getName().getString());
            }
        }
    }

    private void onServerChat(ServerChatEvent event) {
        ServerPlayer player = event.getPlayer();
        if (player == null) {
            return;
        }

        AgentEntityManager manager = getAgentEntityManager();
        if (manager == null || manager.getAllAgents().isEmpty()) {
            return;
        }

        String rawMessage = event.getRawText();
        if (rawMessage == null || rawMessage.isBlank()) {
            return;
        }

        double hearingRadiusSq = 32.0 * 32.0;
        String playerUuid = player.getUUID().toString();
        String playerName = player.getName().getString();

        for (AgentEntity agent : manager.getAllAgents().values()) {
            if (agent.level() != player.level()) {
                continue;
            }

            double distSq = agent.distanceToSqr(player);
            boolean inHearingRange = distSq <= hearingRadiusSq;
            boolean mentionsAgent = rawMessage.toLowerCase(Locale.ROOT).contains(agent.getGameProfile().getName().toLowerCase(Locale.ROOT))
                    || rawMessage.toLowerCase(Locale.ROOT).contains(agent.getAgentId().toLowerCase(Locale.ROOT));

            if (inHearingRange || mentionsAgent) {
                float dist = (float) Math.sqrt(distSq);
                com.civquest.mod.grpc.v1.ChatEvent chat = com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                        .setSenderUuid(playerUuid)
                        .setSenderName(playerName)
                        .setMessage(rawMessage)
                        .setDistance(dist)
                        .build();
                agent.queueIncomingChat(chat);
                LOGGER.info("Forwarded public chat from '{}' to agent '{}' (dist={})",
                        playerName, agent.getAgentId(), dist);
            }
        }
    }

    private void onEntityInteract(PlayerInteractEvent.EntityInteract event) {
        if (event == null || event.getLevel().isClientSide() || event.getHand() != net.minecraft.world.InteractionHand.MAIN_HAND) {
            return;
        }
        if (!(event.getEntity() instanceof ServerPlayer player) || !(event.getTarget() instanceof AgentEntity agent)) {
            return;
        }
        event.setCanceled(true);
        event.setCancellationResult(net.minecraft.world.InteractionResult.SUCCESS);

        net.minecraft.world.item.ItemStack held = player.getMainHandItem();
        if (player.isShiftKeyDown() && !held.isEmpty()) {
            // Shift + Right-Click with an item: deterministic instant gift & auto-equip
            executeGift(player, agent);
            return;
        }

        // Standard Right-Click: open deterministic Merchant Trade & Supply GUI
        AgentMerchant merchant = new AgentMerchant(agent, player);
        merchant.openScreen(player);
    }

    private void onRegisterCommands(RegisterCommandsEvent event) {
        CommandDispatcher<CommandSourceStack> dispatcher = event.getDispatcher();

        // Canonical commands: /cq tell <agent> <message>, /cq gift <agent>, /cq trade <agent> [proposal]
        dispatcher.register(
                Commands.literal("cq")
                        .then(buildTellSubcommand())
                        .then(buildGiftSubcommand())
                        .then(buildTradeSubcommand())
        );
    }

    private LiteralArgumentBuilder<CommandSourceStack> buildGiftSubcommand() {
        return Commands.literal("gift")
                .then(Commands.argument("agent", StringArgumentType.string())
                        .suggests(this::suggestAgents)
                        .executes(ctx -> executeGiftCommand(
                                ctx.getSource(),
                                StringArgumentType.getString(ctx, "agent")
                        )));
    }

    private LiteralArgumentBuilder<CommandSourceStack> buildTradeSubcommand() {
        return Commands.literal("trade")
                .then(Commands.argument("agent", StringArgumentType.string())
                        .suggests(this::suggestAgents)
                        .executes(ctx -> executeTradeCommand(
                                ctx.getSource(),
                                StringArgumentType.getString(ctx, "agent"),
                                ""
                        ))
                        .then(Commands.argument("proposal", StringArgumentType.greedyString())
                                .executes(ctx -> executeTradeCommand(
                                        ctx.getSource(),
                                        StringArgumentType.getString(ctx, "agent"),
                                        StringArgumentType.getString(ctx, "proposal")
                                ))));
    }

    private AgentEntity findAgentByNameCaseInsensitive(String targetName) {
        AgentEntityManager manager = getAgentEntityManager();
        if (manager == null || targetName == null) {
            return null;
        }
        for (AgentEntity agent : manager.getAllAgents().values()) {
            if (agent != null && ((agent.getGameProfile() != null && agent.getGameProfile().getName().equalsIgnoreCase(targetName))
                    || agent.getAgentId().equalsIgnoreCase(targetName))) {
                return agent;
            }
        }
        return null;
    }

    private int executeGiftCommand(CommandSourceStack source, String targetName) {
        ServerPlayer player = source.getPlayer();
        if (player == null) {
            source.sendFailure(Component.literal("§cOnly in-game players can gift held items."));
            return 0;
        }
        AgentEntity agent = findAgentByNameCaseInsensitive(targetName);
        if (agent == null) {
            source.sendFailure(Component.literal("§cNo agent found matching '" + targetName + "'"));
            return 0;
        }
        return executeGift(player, agent);
    }

    private int executeGift(ServerPlayer player, AgentEntity agent) {
        net.minecraft.world.item.ItemStack held = player.getMainHandItem();
        if (held.isEmpty()) {
            player.sendSystemMessage(Component.literal("§eHold an item in your main hand to gift it to " + agent.getGameProfile().getName() + "."));
            return 0;
        }
        int giftCount = held.getMaxStackSize() == 1 ? 1 : Math.min(16, held.getCount());
        net.minecraft.world.item.ItemStack giftStack = held.copyWithCount(giftCount);
        String itemName = giftStack.getHoverName().getString();
        if (!agent.getInventory().add(giftStack)) {
            player.sendSystemMessage(Component.literal("§c" + agent.getGameProfile().getName() + "'s inventory is full."));
            return 0;
        }
        int transferred = giftCount - giftStack.getCount();
        held.shrink(transferred);
        agent.autoEquipBestGear();

        String agentName = agent.getGameProfile().getName();
        player.sendSystemMessage(Component.literal("§aYou gifted " + transferred + "x " + itemName + " to " + agentName + "."));
        player.sendSystemMessage(Component.literal("<" + agentName + "> Thank you kindly for the " + transferred + "x " + itemName + ", " + player.getName().getString() + "!"));

        agent.queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                .setSenderUuid(player.getUUID().toString())
                .setSenderName(player.getName().getString())
                .setMessage("[DETERMINISTIC_GIFT] Gifted " + transferred + "x " + itemName + " to " + agentName + ".")
                .setDistance((float) Math.sqrt(agent.distanceToSqr(player)))
                .build());
        return 1;
    }

    private int executeTradeCommand(CommandSourceStack source, String targetName, String proposal) {
        ServerPlayer player = source.getPlayer();
        if (player == null) {
            source.sendFailure(Component.literal("§cOnly in-game players can trade with agents."));
            return 0;
        }
        AgentEntity agent = findAgentByNameCaseInsensitive(targetName);
        if (agent == null) {
            source.sendFailure(Component.literal("§cNo agent found matching '" + targetName + "'"));
            return 0;
        }
        net.minecraft.world.item.ItemStack held = player.getMainHandItem();
        if (held.isEmpty() && (proposal == null || proposal.isBlank())) {
            AgentMerchant merchant = new AgentMerchant(agent, player);
            merchant.openScreen(player);
            return 1;
        }

        com.civquest.mod.grpc.v1.TradeRequest.Builder reqBuilder = com.civquest.mod.grpc.v1.TradeRequest.newBuilder()
                .setPlayerEntityId(player.getId())
                .setPlayerUuid(player.getUUID().toString())
                .setPlayerName(player.getName().getString())
                .setSpokenProposal("[SYSTEM1_TRADE] " + (proposal != null ? proposal : ""));

        if (!held.isEmpty()) {
            int offerCount = held.getMaxStackSize() == 1 ? 1 : Math.min(16, held.getCount());
            String itemId = net.minecraft.core.registries.BuiltInRegistries.ITEM.getKey(held.getItem()).toString();
            reqBuilder.addPlayerOfferedItems(com.civquest.mod.grpc.v1.ItemStackProto.newBuilder()
                    .setSlot(player.getInventory().selected)
                    .setItemId(itemId)
                    .setCount(offerCount)
                    .build());
        }
        agent.queuePendingTrade(reqBuilder.build());
        return 1;
    }

    private LiteralArgumentBuilder<CommandSourceStack> buildTellSubcommand() {
        return Commands.literal("tell")
                .then(Commands.argument("agent", StringArgumentType.string())
                        .suggests(this::suggestAgents)
                        .then(Commands.argument("message", StringArgumentType.greedyString())
                                .executes(ctx -> executeTell(
                                        ctx.getSource(),
                                        StringArgumentType.getString(ctx, "agent"),
                                        StringArgumentType.getString(ctx, "message")
                                ))
                        ));
    }

    private CompletableFuture<Suggestions> suggestAgents(CommandContext<CommandSourceStack> context, SuggestionsBuilder builder) {
        AgentEntityManager manager = getAgentEntityManager();
        if (manager != null) {
            String remaining = builder.getRemaining().toLowerCase(Locale.ROOT);
            for (AgentEntity agent : manager.getAllAgents().values()) {
                if (agent == null) continue;
                String name = agent.getGameProfile() != null ? agent.getGameProfile().getName() : null;
                String agentId = agent.getAgentId();
                if (name != null && name.toLowerCase(Locale.ROOT).startsWith(remaining)) {
                    builder.suggest(name);
                }
                if (agentId != null && (name == null || !agentId.equalsIgnoreCase(name)) && agentId.toLowerCase(Locale.ROOT).startsWith(remaining)) {
                    builder.suggest(agentId);
                }
            }
        }
        return builder.buildFuture();
    }

    public int executeTell(CommandSourceStack source, String targetName, String message) {
        AgentEntityManager manager = getAgentEntityManager();
        if (manager == null || manager.getAllAgents().isEmpty()) {
            source.sendFailure(Component.literal("§cNo active agents found."));
            return 0;
        }

        AgentEntity targetAgent = null;
        for (AgentEntity agent : manager.getAllAgents().values()) {
            if (agent.getGameProfile().getName().equalsIgnoreCase(targetName)
                    || agent.getAgentId().equalsIgnoreCase(targetName)) {
                targetAgent = agent;
                break;
            }
        }

        if (targetAgent == null) {
            source.sendFailure(Component.literal("§cNo agent found matching '" + targetName + "'"));
            return 0;
        }

        ServerPlayer sender = source.getPlayer();
        String senderUuid = sender != null ? sender.getUUID().toString() : "CONSOLE";
        String senderName = sender != null ? sender.getName().getString() : "Console";
        float dist = (sender != null && sender.level() == targetAgent.level())
                ? (float) Math.sqrt(targetAgent.distanceToSqr(sender))
                : 0.0f;

        com.civquest.mod.grpc.v1.ChatEvent chat = com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                .setSenderUuid(senderUuid)
                .setSenderName(senderName)
                .setMessage(message)
                .setDistance(dist)
                .build();
        targetAgent.queueIncomingChat(chat);

        final AgentEntity finalAgent = targetAgent;
        source.sendSuccess(() -> Component.literal("§7You whisper to " + finalAgent.getGameProfile().getName() + ": " + message), false);
        LOGGER.info("Whispered to agent '{}' from '{}': {}", targetAgent.getAgentId(), senderName, message);
        return 1;
    }

    private void onCommand(CommandEvent event) {
        if (event == null || event.getParseResults() == null || event.getParseResults().getReader() == null) {
            return;
        }
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

        AgentEntityManager manager = getAgentEntityManager();
        if (manager == null) {
            return;
        }

        boolean agentExists = false;
        for (AgentEntity agent : manager.getAllAgents().values()) {
            if (agent != null && ((agent.getGameProfile() != null && agent.getGameProfile().getName().equalsIgnoreCase(targetName))
                    || agent.getAgentId().equalsIgnoreCase(targetName))) {
                agentExists = true;
                break;
            }
        }

        if (agentExists && event.getParseResults().getContext() != null) {
            CommandSourceStack source = event.getParseResults().getContext().getSource();
            if (source != null && executeTell(source, targetName, message) > 0) {
                event.setCanceled(true);
            }
        }
    }

    public CivQuestBridgeServer getBridgeServer() {
        return bridgeServer;
    }

    public AgentEntityManager getAgentEntityManager() {
        return bridgeServer != null ? bridgeServer.getAgentEntityManager() : null;
    }
}
