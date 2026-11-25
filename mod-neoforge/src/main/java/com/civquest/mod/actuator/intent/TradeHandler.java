package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.RespondTradeIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import org.slf4j.Logger;

/** Villager/player trade response (RespondTradeIntent); instant. */
public class TradeHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private final ChatHandler chat;

    public TradeHandler(ChatHandler chat) {
        this.chat = chat;
    }

    public boolean actuateRespondTrade(AgentEntity agent, RespondTradeIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }
        if (!intent.getSpokenResponse().isBlank()) {
            chat.actuateSendChat(agent, SendChatIntent.newBuilder()
                    .setMessage(intent.getSpokenResponse())
                    .setTargetChannel(ChatChannel.CHAT_CHANNEL_LOCAL)
                    .build());
        }
        if (!intent.getAcceptDeal() || agent.level() == null) {
            return false;
        }
        Entity entity = agent.level().getEntity(intent.getPlayerEntityId());
        if (!(entity instanceof ServerPlayer player)) {
            return true;
        }

        // Transfer player's held item (if offered) to agent and agent's counter-offer items to player
        ItemStack playerHeld = player.getMainHandItem();
        if (!playerHeld.isEmpty()) {
            int transferCount = playerHeld.getMaxStackSize() == 1 ? 1 : Math.min(16, playerHeld.getCount());
            ItemStack gift = playerHeld.copyWithCount(transferCount);
            if (agent.getInventory().add(gift)) {
                playerHeld.shrink(transferCount - gift.getCount());
                agent.autoEquipBestGear();
            }
        }

        for (com.civquest.mod.grpc.v1.ItemStackProto counter : intent.getAgentCounterOfferList()) {
            if (counter.getCount() <= 0 || counter.getItemId().isBlank()) {
                continue;
            }
            ResourceLocation id = ResourceLocation.tryParse(counter.getItemId());
            if (id != null && BuiltInRegistries.ITEM.containsKey(id)) {
                Item item = BuiltInRegistries.ITEM.get(id);
                int remaining = counter.getCount();
                for (int i = 0; i < agent.getInventory().items.size() && remaining > 0; i++) {
                    ItemStack slotStack = agent.getInventory().items.get(i);
                    if (slotStack.is(item)) {
                        int take = Math.min(remaining, slotStack.getCount());
                        ItemStack toGive = slotStack.copyWithCount(take);
                        slotStack.shrink(take);
                        remaining -= take;
                        if (!player.getInventory().add(toGive)) {
                            player.drop(toGive, false);
                        }
                    }
                }
            }
        }
        return true;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Act("TRADE_RESPONSE", a -> actuateRespondTrade(a, intent.getTradeResponse()));
    }
}
