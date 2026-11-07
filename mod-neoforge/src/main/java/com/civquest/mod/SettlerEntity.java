package com.civquest.mod;

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
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.gameevent.GameEvent;
import net.neoforged.neoforge.common.CommonHooks;
import net.neoforged.neoforge.common.util.FakePlayer;
import org.slf4j.Logger;

import java.util.UUID;
import java.util.function.Consumer;

/**
 * Autonomous humanoid settler avatar extending {@link FakePlayer}.
 * Supports player capabilities, food hunger decay, item usage, physical navigation,
 * and permadeath lifecycle hooks.
 */
public class SettlerEntity extends FakePlayer {
    private static final Logger LOGGER = LogUtils.getLogger();

    private final String agentId;
    private final String clanId;
    private String skinId;
    private byte[] skinPngBytes;
    private Consumer<SettlerEntity> deathListener;

    public SettlerEntity(ServerLevel level, GameProfile profile, String agentId, String clanId) {
        super(level, profile);
        this.agentId = agentId;
        this.clanId = clanId;
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
                // Execute player tick logic (which FakePlayer stubbed out)
                this.doTick();
            } catch (Exception e) {
                LOGGER.error("Exception during settler doTick for agent '{}': {}", agentId, e.getMessage(), e);
            }

            // Ensure FoodData hunger decay and health regeneration ticks appropriately
            try {
                this.getFoodData().tick(this);
            } catch (Exception e) {
                LOGGER.debug("FoodData tick note for settler '{}': {}", agentId, e.getMessage());
            }
        }
    }

    @Override
    public void checkDespawn() {
        // Prevent natural despawn for autonomous settler agents
    }

    @Override
    public boolean isInvulnerableTo(DamageSource source) {
        if (this.isRemoved()) {
            return true;
        }
        if (this.isInvulnerable() && !source.is(DamageTypeTags.BYPASSES_INVULNERABILITY) && !source.isCreativePlayer()) {
            return true;
        }
        if (source.is(DamageTypeTags.IS_FIRE) && this.fireImmune()) {
            return true;
        }
        if (source.is(DamageTypeTags.IS_FALL) &&
                (!this.level().getGameRules().getBoolean(GameRules.RULE_FALL_DAMAGE)
                        || this.getType().is(EntityTypeTags.FALL_DAMAGE_IMMUNE))) {
            return true;
        }
        if (source.is(DamageTypeTags.IS_DROWNING) &&
                !this.level().getGameRules().getBoolean(GameRules.RULE_DROWNING_DAMAGE)) {
            return true;
        }
        if (this.level() instanceof ServerLevel serverLevel &&
                EnchantmentHelper.isImmuneToDamage(serverLevel, this, source)) {
            return true;
        }
        return false;
    }

    @Override
    public boolean canHarmPlayer(Player player) {
        return true;
    }

    @Override
    public void die(DamageSource cause) {
        LOGGER.info("SettlerEntity died: agentId='{}', clanId='{}', cause='{}'",
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

        // Drop death inventory if not spectator
        if (!this.isSpectator() && !this.level().isClientSide) {
            this.dropAllDeathLoot(this.serverLevel(), cause);
        }

        // Trigger unregistration / cleanup callback
        if (this.deathListener != null) {
            try {
                this.deathListener.accept(this);
            } catch (Exception e) {
                LOGGER.error("Error executing deathListener for settler '{}': {}", agentId, e.getMessage(), e);
            }
        }

        this.discard();
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

    public void setDeathListener(Consumer<SettlerEntity> deathListener) {
        this.deathListener = deathListener;
    }
}
