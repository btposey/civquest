package com.civquest.mod;

import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.sounds.SoundEvent;
import net.minecraft.sounds.SoundEvents;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.trading.ItemCost;
import net.minecraft.world.item.trading.Merchant;
import net.minecraft.world.item.trading.MerchantOffer;
import net.minecraft.world.item.trading.MerchantOffers;

/**
 * Deterministic in-game Merchant GUI adapter for {@link AgentEntity}.
 * Constructs structured quid-pro-quo trade and equipment-supply offers directly from the agent's
 * live inventory and survival needs without blocking on an LLM round-trip, and records completed
 * trades into the agent's deterministic memory and relationship pipeline.
 */
public class AgentMerchant implements Merchant {
    private final AgentEntity agent;
    private Player tradingPlayer;
    private final MerchantOffers offers = new MerchantOffers();

    public AgentMerchant(AgentEntity agent, Player tradingPlayer) {
        this.agent = agent;
        this.tradingPlayer = tradingPlayer;
        buildDeterministicOffers();
    }

    private void buildDeterministicOffers() {
        Inventory inv = agent.getInventory();
        boolean hasSword = false;
        boolean hasShield = false;
        ItemStack surplusStack = ItemStack.EMPTY;

        if (inv != null) {
            for (int i = 0; i < inv.items.size(); i++) {
                ItemStack s = inv.items.get(i);
                if (s == null || s.isEmpty()) {
                    continue;
                }
                String id = BuiltInRegistries.ITEM.getKey(s.getItem()).toString();
                if (id.contains("_sword")) {
                    hasSword = true;
                } else if (s.is(Items.SHIELD)) {
                    hasShield = true;
                } else if (surplusStack.isEmpty() && s.getCount() >= 1) {
                    surplusStack = s.copyWithCount(Math.min(4, s.getCount()));
                }
            }
        }

        ItemStack rewardToken = !surplusStack.isEmpty() ? surplusStack.copy() : new ItemStack(Items.EMERALD, 1);

        // 1. High-priority survival gear & food provisioning offers (Agent needs weapon, shield, food, torches)
        if (!hasSword) {
            offers.add(new MerchantOffer(
                    new ItemCost(Items.IRON_SWORD, 1),
                    rewardToken.copy(),
                    4, 2, 0.05f
            ));
            offers.add(new MerchantOffer(
                    new ItemCost(Items.STONE_SWORD, 1),
                    rewardToken.copy(),
                    4, 2, 0.05f
            ));
        }
        if (!hasShield) {
            offers.add(new MerchantOffer(
                    new ItemCost(Items.SHIELD, 1),
                    rewardToken.copy(),
                    4, 2, 0.05f
            ));
        }
        offers.add(new MerchantOffer(
                new ItemCost(Items.BREAD, 4),
                rewardToken.copy(),
                8, 1, 0.05f
        ));
        offers.add(new MerchantOffer(
                new ItemCost(Items.COOKED_BEEF, 2),
                rewardToken.copy(),
                8, 1, 0.05f
        ));
        offers.add(new MerchantOffer(
                new ItemCost(Items.TORCH, 8),
                rewardToken.copy(),
                8, 1, 0.05f
        ));

        // 2. Surplus inventory barter offers (Player trades Bread, Torches, Iron, or Emeralds for Agent's surplus items)
        if (inv != null) {
            for (int i = 0; i < inv.items.size(); i++) {
                ItemStack s = inv.items.get(i);
                if (s == null || s.isEmpty()) {
                    continue;
                }
                String id = BuiltInRegistries.ITEM.getKey(s.getItem()).toString();
                // Do not offer up the agent's primary equipped sword or shield
                if ((id.contains("_sword") || s.is(Items.SHIELD)) && s.getCount() == 1) {
                    continue;
                }
                int sellCount = Math.min(8, s.getCount());
                offers.add(new MerchantOffer(
                        new ItemCost(Items.EMERALD, 1),
                        s.copyWithCount(sellCount),
                        4, 1, 0.05f
                ));
                offers.add(new MerchantOffer(
                        new ItemCost(Items.BREAD, 2),
                        s.copyWithCount(sellCount),
                        4, 1, 0.05f
                ));
                if (offers.size() >= 12) {
                    break;
                }
            }
        }
    }

    public void openScreen(ServerPlayer player) {
        this.tradingPlayer = player;
        String title = agent.getGameProfile().getName() + " (" + agent.getClanId() + ")";
        this.openTradingScreen(player, Component.literal(title), 1);
    }

    @Override
    public void setTradingPlayer(Player player) {
        this.tradingPlayer = player;
    }

    @Override
    public Player getTradingPlayer() {
        return this.tradingPlayer;
    }

    @Override
    public MerchantOffers getOffers() {
        return this.offers;
    }

    @Override
    public void overrideOffers(MerchantOffers offers) {
    }

    @Override
    public void notifyTrade(MerchantOffer offer) {
        offer.increaseUses();
        if (this.agent == null || !this.agent.isAlive()) {
            return;
        }
        ItemStack givenByPlayer = offer.getBaseCostA().copy();
        ItemStack soldByAgent = offer.getResult().copy();

        // Add player's payment item to the agent's actual inventory & auto-equip if it's a weapon/shield/tool
        if (!givenByPlayer.isEmpty() && this.agent.getInventory() != null) {
            this.agent.getInventory().add(givenByPlayer.copy());
            this.agent.autoEquipBestGear();
        }

        // Deduct sold item from agent's inventory if present
        if (!soldByAgent.isEmpty() && this.agent.getInventory() != null) {
            int remaining = soldByAgent.getCount();
            for (int i = 0; i < this.agent.getInventory().items.size() && remaining > 0; i++) {
                ItemStack slotStack = this.agent.getInventory().items.get(i);
                if (ItemStack.isSameItemSameComponents(slotStack, soldByAgent)) {
                    int take = Math.min(remaining, slotStack.getCount());
                    slotStack.shrink(take);
                    remaining -= take;
                }
            }
        }

        if (this.tradingPlayer instanceof ServerPlayer sp) {
            String agentName = this.agent.getGameProfile().getName();
            String givenDesc = givenByPlayer.getCount() + "x " + givenByPlayer.getHoverName().getString();
            String soldDesc = soldByAgent.getCount() + "x " + soldByAgent.getHoverName().getString();
            sp.sendSystemMessage(Component.literal(
                    "<" + agentName + "> Fair trade, " + sp.getName().getString() + " — thank you for the " + givenDesc + "!"));
            this.agent.queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                    .setSenderUuid(sp.getUUID().toString())
                    .setSenderName(sp.getName().getString())
                    .setMessage("[DETERMINISTIC_TRADE] Traded " + givenDesc + " with " + agentName + " for " + soldDesc + ".")
                    .setDistance((float) Math.sqrt(this.agent.distanceToSqr(sp)))
                    .build());
        }
    }

    @Override
    public void notifyTradeUpdated(ItemStack stack) {
    }

    @Override
    public int getVillagerXp() {
        return 0;
    }

    @Override
    public void overrideXp(int xp) {
    }

    @Override
    public boolean showProgressBar() {
        return false;
    }

    @Override
    public SoundEvent getNotifyTradeSound() {
        return SoundEvents.VILLAGER_YES;
    }

    @Override
    public boolean isClientSide() {
        return this.agent.level().isClientSide;
    }
}
