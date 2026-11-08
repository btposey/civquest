package com.civquest.mod;

import io.netty.buffer.ByteBuf;
import net.minecraft.core.UUIDUtil;
import net.minecraft.network.codec.ByteBufCodecs;
import net.minecraft.network.codec.StreamCodec;
import net.minecraft.network.protocol.common.custom.CustomPacketPayload;
import net.minecraft.resources.ResourceLocation;

import java.util.Objects;
import java.util.UUID;

/**
 * Custom NeoForge network payload carrying agent skin textures (raw PNG bytes or metadata)
 * broadcast to connected client players for dynamic agent avatar rendering.
 */
public record ClientboundAgentSkinPacket(UUID agentUuid, String agentId, byte[] skinPng) implements CustomPacketPayload {
    public static final CustomPacketPayload.Type<ClientboundAgentSkinPacket> TYPE =
            new CustomPacketPayload.Type<>(ResourceLocation.fromNamespaceAndPath(CivQuestMod.MOD_ID, "agent_skin"));

    public static final StreamCodec<ByteBuf, ClientboundAgentSkinPacket> STREAM_CODEC =
            StreamCodec.composite(
                    UUIDUtil.STREAM_CODEC,
                    ClientboundAgentSkinPacket::agentUuid,
                    ByteBufCodecs.STRING_UTF8,
                    ClientboundAgentSkinPacket::agentId,
                    ByteBufCodecs.byteArray(65536),
                    ClientboundAgentSkinPacket::skinPng,
                    ClientboundAgentSkinPacket::new
            );

    public ClientboundAgentSkinPacket {
        Objects.requireNonNull(agentUuid, "agentUuid cannot be null");
        agentId = agentId != null ? agentId : "";
        skinPng = skinPng != null ? skinPng : new byte[0];
    }

    @Override
    public Type<? extends CustomPacketPayload> type() {
        return TYPE;
    }
}
