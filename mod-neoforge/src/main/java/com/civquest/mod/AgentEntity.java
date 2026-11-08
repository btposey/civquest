package com.civquest.mod;

import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.mojang.authlib.GameProfile;
import com.mojang.logging.LogUtils;
import net.minecraft.core.UUIDUtil;
import net.minecraft.network.chat.Component;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.tags.DamageTypeTags;
import net.minecraft.tags.EntityTypeTags;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.enchantment.EnchantmentHelper;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.gameevent.GameEvent;
import net.minecraft.world.scores.Team;
import net.neoforged.neoforge.common.CommonHooks;
import net.neoforged.neoforge.common.util.FakePlayer;
import org.slf4j.Logger;

import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;

/**
 * Autonomous humanoid agent avatar extending {@link FakePlayer}.
 * Supports player capabilities, food hunger decay, item usage, physical navigation,
 * and permadeath lifecycle hooks.
 */
public class AgentEntity extends FakePlayer {
    private static final Logger LOGGER = LogUtils.getLogger();

    private final String agentId;
    private final String clanId;
    private String skinId;
    private byte[] skinPngBytes;
    private ChunkPos forcedChunkPos;
    private StreamRegistry streamRegistry;
    private SensoryFrustumCollector sensoryCollector;
    private final AgentNavigator navigator;
    private final com.civquest.mod.actuator.BlockBreaker breaker = new com.civquest.mod.actuator.BlockBreaker(this);
    private Consumer<AgentEntity> deathListener;
    private Consumer<AgentEntity> removalListener;
    private final AtomicBoolean cleanedUp = new AtomicBoolean(false);
    private final ConcurrentLinkedQueue<com.civquest.mod.grpc.v1.ChatEvent> pendingChats = new ConcurrentLinkedQueue<>();
    private final ConcurrentLinkedQueue<com.civquest.mod.grpc.v1.TradeRequest> pendingTrades = new ConcurrentLinkedQueue<>();
    private long lastInventoryFullNoticeTick = -1000L;

    public void queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent chatEvent) {
        if (chatEvent != null) {
            pendingChats.add(chatEvent);
        }
    }

    public List<com.civquest.mod.grpc.v1.ChatEvent> pollIncomingChats() {
        List<com.civquest.mod.grpc.v1.ChatEvent> result = new ArrayList<>();
        com.civquest.mod.grpc.v1.ChatEvent chat;
        while ((chat = pendingChats.poll()) != null) {
            result.add(chat);
        }
        return result;
    }

    public void queuePendingTrade(com.civquest.mod.grpc.v1.TradeRequest tradeRequest) {
        if (tradeRequest != null) {
            pendingTrades.add(tradeRequest);
        }
    }

    public com.civquest.mod.grpc.v1.TradeRequest pollPendingTrade() {
        return pendingTrades.poll();
    }

    public AgentEntity(ServerLevel level, GameProfile profile, String agentId, String clanId) {
        super(level, profile);
        this.agentId = agentId;
        this.clanId = clanId;
        this.navigator = new AgentNavigator(this);
        try {
            var stepAttr = this.getAttribute(net.minecraft.world.entity.ai.attributes.Attributes.STEP_HEIGHT);
            if (stepAttr != null) {
                stepAttr.setBaseValue(1.1D);
            }
        } catch (Throwable ignored) {
        }
    }

    @Override
    public float maxUpStep() {
        return 1.1F;
    }

    /**
     * Generates a deterministic offline player UUID from display name or agent ID.
     */
    public static UUID createOfflineUUID(String nameOrId) {
        if (nameOrId == null || nameOrId.isBlank()) {
            return UUID.randomUUID();
        }
        return UUIDUtil.createOfflinePlayerUUID(nameOrId);
    }

    @Override
    public void tick() {
        super.tick();
        if (!this.level().isClientSide) {
            try {
                if (this.breaker.isBreaking()) {
                    this.breaker.tick();
                }
                if (this.navigator != null) {
                    this.navigator.tick();
                }
                if (this.isAlive() && !this.isSleeping() && !this.breaker.isBreaking()
                        && (this.navigator == null || this.navigator.getNavigationState() != com.civquest.mod.grpc.v1.NavigationState.NAVIGATION_STATE_PATHING)) {
                    updateIdleSocialGaze();
                }
                if (this.tickCount % 10 == 0 && this.isAlive()) {
                    tryPickupNearbyItems();
                }
                // Execute player tick logic (which FakePlayer stubbed out, including FoodData tick at 20 Hz)
                this.doTick();
            } catch (Exception e) {
                LOGGER.error("Exception during agent doTick for agent '{}': {}", agentId, e.getMessage(), e);
            }
        }
    }

    /**
     * Smoothly turns the agent's head, body, and pitch toward the nearest human player within 8 blocks
     * who has line of sight when the agent is not actively pathfinding or sleeping.
     */
    public void updateIdleSocialGaze() {
        if (this.level() == null || this.isRemoved() || !this.isAlive() || this.isSleeping()) {
            return;
        }
        Player nearestHuman = null;
        double bestDistSq = 64.0; // 8.0 blocks squared
        for (Player player : this.level().players()) {
            if (player == null || player == this || player instanceof AgentEntity || !player.isAlive() || player.isSpectator()) {
                continue;
            }
            double dSq = this.distanceToSqr(player);
            if (dSq <= bestDistSq && this.hasLineOfSight(player)) {
                bestDistSq = dSq;
                nearestHuman = player;
            }
        }
        if (nearestHuman != null) {
            net.minecraft.world.phys.Vec3 eye = this.getEyePosition();
            net.minecraft.world.phys.Vec3 targetEye = nearestHuman.getEyePosition();
            double dx = targetEye.x - eye.x;
            double dy = targetEye.y - eye.y;
            double dz = targetEye.z - eye.z;
            double horizontalDist = Math.sqrt(dx * dx + dz * dz);
            float targetYaw = (float) (net.minecraft.util.Mth.atan2(dz, dx) * (180.0 / Math.PI)) - 90.0F;
            float targetPitch = (float) (-(net.minecraft.util.Mth.atan2(dy, Math.max(0.001, horizontalDist)) * (180.0 / Math.PI)));
            // Gentle, rate-limited glance: head leads, body trails within a clamp, pitch low-passed.
            float smoothYaw = com.civquest.mod.actuator.LookSmoothing.approach(this.getYRot(), targetYaw, 0.25F, 5.0F, 3.0F);
            float smoothPitch = com.civquest.mod.actuator.LookSmoothing.approachLinear(this.getXRot(),
                    net.minecraft.util.Mth.clamp(targetPitch, -25.0F, 25.0F), 0.15F, 3.0F);
            this.setYRot(smoothYaw);
            this.setYHeadRot(smoothYaw);
            float body = com.civquest.mod.actuator.LookSmoothing.approach(this.yBodyRot, smoothYaw, 0.2F, 4.0F, 0.0F);
            this.yBodyRot = com.civquest.mod.actuator.LookSmoothing.clampBodyToHead(body, smoothYaw, 50.0F);
            this.setXRot(smoothPitch);
        }
    }

    /**
     * Scans for nearby dropped ItemEntities within 2.5 blocks and picks them up into the agent's inventory,
     * auto-equipping weapons/shields/tools and attributing nearby player gifts deterministically.
     * Notifies the nearest player when the agent's inventory is full.
     */
    public void tryPickupNearbyItems() {
        if (this.level() == null || this.isRemoved()) {
            return;
        }
        List<net.minecraft.world.entity.item.ItemEntity> items = this.level().getEntitiesOfClass(
                net.minecraft.world.entity.item.ItemEntity.class,
                this.getBoundingBox().inflate(2.5, 1.5, 2.5),
                ie -> ie != null && ie.isAlive() && !ie.hasPickUpDelay() && !ie.getItem().isEmpty()
        );
        if (items.isEmpty()) {
            return;
        }
        net.minecraft.server.level.ServerPlayer nearestPlayer = null;
        if (this.getServer() != null && this.getServer().getPlayerList() != null) {
            double bestDistSq = 16.0 * 16.0;
            for (net.minecraft.server.level.ServerPlayer sp : this.getServer().getPlayerList().getPlayers()) {
                if (sp.level() == this.level()) {
                    double dSq = this.distanceToSqr(sp);
                    if (dSq <= bestDistSq) {
                        bestDistSq = dSq;
                        nearestPlayer = sp;
                    }
                }
            }
        }

        String agentName = this.getGameProfile().getName();
        for (net.minecraft.world.entity.item.ItemEntity ie : items) {
            net.minecraft.world.item.ItemStack copy = ie.getItem().copy();
            int origCount = copy.getCount();
            String itemName = copy.getHoverName().getString();
            this.getInventory().add(copy);
            int pickedCount = origCount - copy.getCount();
            if (pickedCount > 0) {
                if (copy.isEmpty()) {
                    ie.discard();
                } else {
                    ie.setItem(copy);
                    ie.setNoPickUpDelay();
                }
                autoEquipBestGear();
                if (nearestPlayer != null) {
                    nearestPlayer.sendSystemMessage(Component.literal(
                            "<" + agentName + "> Thank you for the " + pickedCount + "x " + itemName + ", " + nearestPlayer.getName().getString() + "!"));
                    queueIncomingChat(com.civquest.mod.grpc.v1.ChatEvent.newBuilder()
                            .setSenderUuid(nearestPlayer.getUUID().toString())
                            .setSenderName(nearestPlayer.getName().getString())
                            .setMessage("[DETERMINISTIC_GIFT] Gifted " + pickedCount + "x " + itemName + " to " + agentName + ".")
                            .setDistance((float) Math.sqrt(this.distanceToSqr(nearestPlayer)))
                            .build());
                    if (!copy.isEmpty()) {
                        nearestPlayer.sendSystemMessage(Component.literal(
                                "<" + agentName + "> My pack is full, " + nearestPlayer.getName().getString() + "! I couldn't fit the remaining " + copy.getCount() + "x " + itemName + " — please pick them back up."));
                        this.lastInventoryFullNoticeTick = this.tickCount;
                    }
                }
            } else {
                ie.setNoPickUpDelay();
                if (nearestPlayer != null && (this.tickCount - this.lastInventoryFullNoticeTick >= 100)) {
                    nearestPlayer.sendSystemMessage(Component.literal(
                            "<" + agentName + "> My pack is completely full, " + nearestPlayer.getName().getString() + "! I can't carry the " + origCount + "x " + itemName + " — please pick it back up."));
                    this.lastInventoryFullNoticeTick = this.tickCount;
                }
            }
        }
    }

    /**
     * Equips the highest destroy-speed tool in the agent's inventory for the given {@link net.minecraft.world.level.block.state.BlockState}
     * into the selected hotbar slot ({@code inv.selected}).
     *
     * @return true if a tool faster than bare hands (> 1.0F) was found and equipped
     */
    public boolean equipBestToolFor(net.minecraft.world.level.block.state.BlockState state) {
        if (state == null || this.getInventory() == null) {
            return false;
        }
        int slot = com.civquest.mod.actuator.BlockBreaker.bestToolSlot(this, state);
        if (slot < 0) {
            return false;
        }
        com.civquest.mod.actuator.BlockBreaker.equipBestTool(this, state);
        return true;
    }

    /**
     * Scans the agent's inventory and equips the best melee weapon/tool into slot 0 (selected hotbar mainhand)
     * and any shield into the offhand slot.
     */
    public void autoEquipBestGear() {
        net.minecraft.world.entity.player.Inventory inv = this.getInventory();
        if (inv == null) {
            return;
        }
        // 1. Equip shield into offhand if not already holding one
        net.minecraft.world.item.ItemStack offhand = this.getOffhandItem();
        if (!offhand.is(net.minecraft.world.item.Items.SHIELD)) {
            for (int i = 0; i < inv.items.size(); i++) {
                net.minecraft.world.item.ItemStack s = inv.items.get(i);
                if (s.is(net.minecraft.world.item.Items.SHIELD)) {
                    inv.items.set(i, offhand);
                    this.setItemInHand(net.minecraft.world.InteractionHand.OFF_HAND, s);
                    break;
                }
            }
        }

        if (this.breaker != null && this.breaker.isBreaking()) {
            return;
        }

        // 2. Equip best weapon/tool into selected hotbar slot (0)
        int bestSlot = -1;
        int bestScore = scoreWeaponOrTool(inv.items.get(inv.selected));
        for (int i = 0; i < inv.items.size(); i++) {
            int score = scoreWeaponOrTool(inv.items.get(i));
            if (score > bestScore) {
                bestScore = score;
                bestSlot = i;
            }
        }
        if (bestSlot >= 0 && bestSlot != inv.selected) {
            net.minecraft.world.item.ItemStack current = inv.items.get(inv.selected);
            net.minecraft.world.item.ItemStack best = inv.items.get(bestSlot);
            inv.items.set(inv.selected, best);
            inv.items.set(bestSlot, current);
        }
    }

    private static int scoreWeaponOrTool(net.minecraft.world.item.ItemStack stack) {
        if (stack == null || stack.isEmpty()) {
            return 0;
        }
        String id = net.minecraft.core.registries.BuiltInRegistries.ITEM.getKey(stack.getItem()).toString();
        if (id.contains("netherite_sword")) return 100;
        if (id.contains("diamond_sword")) return 90;
        if (id.contains("netherite_axe") || id.contains("diamond_axe")) return 85;
        if (id.contains("iron_sword")) return 80;
        if (id.contains("iron_axe")) return 75;
        if (id.contains("stone_sword")) return 65;
        if (id.contains("stone_axe")) return 60;
        if (id.contains("wooden_sword") || id.contains("golden_sword")) return 50;
        if (id.contains("wooden_axe") || id.contains("pickaxe")) return 40;
        if (id.contains("shovel")) return 35;
        return 0;
    }

    @Override
    public void checkDespawn() {
        // Prevent natural despawn for autonomous agents
    }

    @Override
    public boolean canUsePortal(boolean allowVehicles) {
        // Autonomous agents are tethered to their assigned dimension
        return false;
    }

    @Override
    public boolean isInvulnerableTo(DamageSource source) {
        if (this.isRemoved()) {
            return true;
        }
        boolean isInvul = false;
        if (this.isInvulnerable() && !source.is(DamageTypeTags.BYPASSES_INVULNERABILITY) && !source.isCreativePlayer()) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_FIRE) && this.fireImmune()) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_FALL) &&
                (!this.level().getGameRules().getBoolean(GameRules.RULE_FALL_DAMAGE)
                        || this.getType().is(EntityTypeTags.FALL_DAMAGE_IMMUNE))) {
            isInvul = true;
        } else if (source.is(DamageTypeTags.IS_DROWNING) &&
                !this.level().getGameRules().getBoolean(GameRules.RULE_DROWNING_DAMAGE)) {
            isInvul = true;
        } else if (this.level() instanceof ServerLevel serverLevel &&
                EnchantmentHelper.isImmuneToDamage(serverLevel, this, source)) {
            isInvul = true;
        }
        return CommonHooks.isEntityInvulnerableTo(this, source, isInvul);
    }

    @Override
    public boolean canHarmPlayer(Player other) {
        if (this.getServer() != null && !this.getServer().isPvpAllowed()) {
            return false;
        }
        Team team = this.getTeam();
        Team otherTeam = other.getTeam();
        if (team != null) {
            if (team.isAlliedTo(otherTeam) && !team.isAllowFriendlyFire()) {
                return false;
            }
        }
        return super.canHarmPlayer(other);
    }

    @Override
    public void die(DamageSource cause) {
        LOGGER.info("AgentEntity died: agentId='{}', clanId='{}', cause='{}'",
                agentId, clanId, cause.getLocalizedDeathMessage(this).getString());

        if (CommonHooks.onLivingDeath(this, cause)) {
            return;
        }

        this.gameEvent(GameEvent.ENTITY_DIE);

        // Emit death notice or broadcast to all connected players
        if (this.getServer() != null && this.getServer().getPlayerList() != null) {
            Component deathMessage = this.getCombatTracker().getDeathMessage();
            this.getServer().getPlayerList().broadcastSystemMessage(deathMessage, false);
        }

        // Construct DeathEvent and emit zero-latency terminal telemetry frame immediately before stream closure
        if (this.streamRegistry != null && this.streamRegistry.isRegistered(this.agentId)) {
            try {
                DeathEvent deathEvent = SensoryFrustumCollector.buildDeathEvent(this, cause);
                SensoryFrustumCollector collector = this.sensoryCollector != null
                        ? this.sensoryCollector
                        : new SensoryFrustumCollector();
                TelemetryFrame deathFrame = collector.collectFrame(this, null, deathEvent);
                this.streamRegistry.sendCriticalFrame(this.agentId, deathFrame);
            } catch (Exception e) {
                LOGGER.error("Error sending terminal death telemetry frame for agent '{}': {}",
                        this.agentId, e.getMessage(), e);
            }
        }

        // Drop death inventory if not spectator
        if (!this.isSpectator() && !this.level().isClientSide) {
            this.dropAllDeathLoot(this.serverLevel(), cause);
        }

        // Release forced chunk
        releaseForcedChunk();

        // Trigger unregistration / cleanup callback
        if (cleanedUp.compareAndSet(false, true)) {
            if (this.deathListener != null) {
                try {
                    this.deathListener.accept(this);
                } catch (Exception e) {
                    LOGGER.error("Error executing deathListener for agent '{}': {}", agentId, e.getMessage(), e);
                }
            }
        }

        this.discard();
    }

    @Override
    public void remove(RemovalReason reason) {
        super.remove(reason);
        releaseForcedChunk();
        if (cleanedUp.compareAndSet(false, true)) {
            if (this.removalListener != null) {
                try {
                    this.removalListener.accept(this);
                } catch (Exception e) {
                    LOGGER.error("Error executing removalListener for agent '{}': {}", agentId, e.getMessage(), e);
                }
            }
        }
    }

    public void releaseForcedChunk() {
        if (this.forcedChunkPos != null && this.level() instanceof ServerLevel serverLevel) {
            try {
                serverLevel.setChunkForced(this.forcedChunkPos.x, this.forcedChunkPos.z, false);
                LOGGER.debug("Released forced chunk [{}, {}] for agent '{}'",
                        this.forcedChunkPos.x, this.forcedChunkPos.z, agentId);
            } catch (Exception e) {
                LOGGER.warn("Failed to release forced chunk for agent '{}': {}", agentId, e.getMessage());
            }
            this.forcedChunkPos = null;
        }
    }

    public ChunkPos getForcedChunkPos() {
        return forcedChunkPos;
    }

    public void setForcedChunkPos(ChunkPos forcedChunkPos) {
        this.forcedChunkPos = forcedChunkPos;
    }

    public String getAgentId() {
        return agentId;
    }

    public String getClanId() {
        return clanId;
    }

    public String getSkinId() {
        return skinId;
    }

    public void setSkinId(String skinId) {
        this.skinId = skinId;
    }

    public byte[] getSkinPngBytes() {
        return skinPngBytes;
    }

    public void setSkinPngBytes(byte[] skinPngBytes) {
        this.skinPngBytes = skinPngBytes;
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public void setStreamRegistry(StreamRegistry streamRegistry) {
        this.streamRegistry = streamRegistry;
    }

    public SensoryFrustumCollector getSensoryCollector() {
        return sensoryCollector;
    }

    public void setSensoryCollector(SensoryFrustumCollector sensoryCollector) {
        this.sensoryCollector = sensoryCollector;
    }

    public void setTelemetryContext(StreamRegistry streamRegistry, SensoryFrustumCollector sensoryCollector) {
        this.streamRegistry = streamRegistry;
        this.sensoryCollector = sensoryCollector;
    }

    public void setDeathListener(Consumer<AgentEntity> deathListener) {
        this.deathListener = deathListener;
    }

    public void setRemovalListener(Consumer<AgentEntity> removalListener) {
        this.removalListener = removalListener;
    }

    /** Current stamina (0-100) from the navigator, for debug telemetry. */
    public double getStamina() {
        return navigator != null ? navigator.getStamina() : AgentNavigator.STAMINA_MAX;
    }

    public com.civquest.mod.actuator.BlockBreaker getBreaker() {
        return breaker;
    }

    public AgentNavigator getNavigator() {
        return navigator;
    }
}
