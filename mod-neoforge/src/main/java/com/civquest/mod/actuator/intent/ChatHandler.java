package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.mojang.logging.LogUtils;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import net.minecraft.network.chat.Component;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerPlayer;
import org.slf4j.Logger;

/** Local/shout/radio chat delivery (SendChatIntent); instant. */
public class ChatHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private final java.util.function.Supplier<? extends java.util.Collection<AgentEntity>> knownAgents;

    /** {@code knownAgents} lists every agent the actuator has seen (acoustic peers beyond the entity query). */
    public ChatHandler(java.util.function.Supplier<? extends java.util.Collection<AgentEntity>> knownAgents) {
        this.knownAgents = knownAgents;
    }

    public boolean actuateSendChat(AgentEntity agent, SendChatIntent intent) {
        if (agent == null || intent == null || intent.getMessage().isBlank()) {
            return false;
        }

        String msg = intent.getMessage().trim();
        String name = agent.getGameProfile() != null ? agent.getGameProfile().getName()
                : (agent.getAgentId() != null ? agent.getAgentId() : "Agent");
        Component chatComponent = Component.literal("<" + name + "> " + msg);

        MinecraftServer server = agent.getServer();
        boolean hasServer = server != null && server.getPlayerList() != null;
        if (hasServer) {
            if (intent.getTargetChannel() == ChatChannel.CHAT_CHANNEL_CLAN_BROADCAST) {
                Component clanComponent = Component.literal("[" + agent.getClanId() + "] <" + name + "> " + msg);
                server.getPlayerList().broadcastSystemMessage(clanComponent, false);
            } else {
                // Local broadcast: deliver to human players within earshot (16 blocks)
                double playerRangeSq = 16.0 * 16.0;
                for (ServerPlayer player : server.getPlayerList().getPlayers()) {
                    if (player.level() == agent.level() && player.distanceToSqr(agent) <= playerRangeSq) {
                        player.sendSystemMessage(chatComponent);
                    }
                }
            }
        }

        // Acoustic horizon: deliver ChatEvent to nearby peer AgentEntity instances within 20m
        double agentRangeSq = 20.0 * 20.0;
        UUID speakerUuid = agent.getUUID();
        if (speakerUuid == null && agent.getGameProfile() != null) {
            speakerUuid = agent.getGameProfile().getId();
        }
        String senderUuidStr = speakerUuid != null ? speakerUuid.toString() : "";

        List<AgentEntity> peerAgents = new ArrayList<>();
        Set<UUID> seenUuids = new HashSet<>();
        Set<AgentEntity> seenEntities = Collections.newSetFromMap(new IdentityHashMap<>());

        if (agent.level() != null && agent.getBoundingBox() != null) {
            List<AgentEntity> nearbyAgents = agent.level().getEntitiesOfClass(
                    AgentEntity.class,
                    agent.getBoundingBox().inflate(20.0),
                    e -> e != agent && e.distanceToSqr(agent) <= agentRangeSq
            );
            for (AgentEntity peer : nearbyAgents) {
                if (peer == null || peer == agent) {
                    continue;
                }
                UUID peerUuid = peer.getUUID();
                if (speakerUuid != null && speakerUuid.equals(peerUuid)) {
                    continue;
                }
                if (seenEntities.add(peer) && (peerUuid == null || seenUuids.add(peerUuid))) {
                    peerAgents.add(peer);
                }
            }
        }

        for (AgentEntity peer : knownAgents.get()) {
            if (peer == null || peer == agent) {
                continue;
            }
            if (agent.level() != null && peer.level() != null && peer.level() != agent.level()) {
                continue;
            }
            UUID peerUuid = peer.getUUID();
            if (speakerUuid != null && speakerUuid.equals(peerUuid)) {
                continue;
            }
            if (peer.distanceToSqr(agent) <= agentRangeSq) {
                if (seenEntities.add(peer) && (peerUuid == null || seenUuids.add(peerUuid))) {
                    peerAgents.add(peer);
                }
            }
        }

        for (AgentEntity peer : peerAgents) {
            float distance = (float) Math.sqrt(peer.distanceToSqr(agent));
            ChatEvent chatEvent = ChatEvent.newBuilder()
                    .setSenderUuid(senderUuidStr)
                    .setSenderName(name)
                    .setMessage(msg)
                    .setDistance(distance)
                    .build();
            peer.queueIncomingChat(chatEvent);
        }

        if (hasServer || agent.level() != null || !peerAgents.isEmpty()) {
            LOGGER.info("Agent '{}' spoke in chat: {}", name, msg);
            return true;
        }
        return false;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Act("CHAT", a -> actuateSendChat(a, intent.getChat()));
    }
}
