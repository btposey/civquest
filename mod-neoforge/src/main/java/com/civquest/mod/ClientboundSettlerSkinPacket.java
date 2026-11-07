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
 * broadcast to connected client players for dynamic settler avatar rendering.
 */
public record ClientboundSettlerSkinPacket(UUID settlerUuid, String agentId, byte[] skinPng) implements CustomPacketPayload {
    public static final CustomPacketPayload.Type<ClientboundSettlerSkinPacket> TYPE =
            new CustomPacketPayload.Type<>(ResourceLocation.fromNamespaceAndPath(CivQuestMod.MOD_ID, "settler_skin"));

    public static final StreamCodec<ByteBuf, ClientboundSettlerSkinPacket> STREAM_CODEC =
            StreamCodec.composite(
                    UUIDUtil.STREAM_CODEC,
                    ClientboundSettlerSkinPacket::settlerUuid,
                    ByteBufCodecs.STRING_UTF8,
                    ClientboundSettlerSkinPacket::agentId,
                    ByteBufCodecs.BYTE_ARRAY,
                    ClientboundSettlerSkinPacket::skinPng,
                    ClientboundSettlerSkinPacket::new
            );

    public ClientboundSettlerSkinPacket {
        Objects.requireNonNull(settlerUuid, "settlerUuid cannot be null");
        agentId = agentId != null ? agentId : "";
        skinPng = skinPng != null ? skinPng : new byte[0];
    }

    @Override
    public Type<? extends CustomPacketPayload> type() {
        return TYPE;
    }
}
