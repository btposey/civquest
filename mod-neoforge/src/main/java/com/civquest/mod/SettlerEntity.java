package com.civquest.mod;

import com.civquest.mod.actuator.SettlerNavigator;
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

import java.util.UUID;
import java.util.concurrent.atomic.AtomicBoolean;
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
    private ChunkPos forcedChunkPos;
    private StreamRegistry streamRegistry;
    private SensoryFrustumCollector sensoryCollector;
    private final SettlerNavigator navigator;
    private Consumer<SettlerEntity> deathListener;
    private Consumer<SettlerEntity> removalListener;
    private final AtomicBoolean cleanedUp = new AtomicBoolean(false);

    public SettlerEntity(ServerLevel level, GameProfile profile, String agentId, String clanId) {
        super(level, profile);
        this.agentId = agentId;
        this.clanId = clanId;
        this.navigator = new SettlerNavigator(this);
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
                if (this.navigator != null) {
                    this.navigator.tick();
                }
                // Execute player tick logic (which FakePlayer stubbed out, including FoodData tick at 20 Hz)
                this.doTick();
            } catch (Exception e) {
                LOGGER.error("Exception during settler doTick for agent '{}': {}", agentId, e.getMessage(), e);
            }
        }
    }

    @Override
    public void checkDespawn() {
        // Prevent natural despawn for autonomous settler agents
    }

    @Override
    public boolean canUsePortal(boolean allowVehicles) {
        // Autonomous settler agents are tethered to their assigned dimension
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
                LOGGER.error("Error sending terminal death telemetry frame for settler '{}': {}",
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
                    LOGGER.error("Error executing deathListener for settler '{}': {}", agentId, e.getMessage(), e);
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
                    LOGGER.error("Error executing removalListener for settler '{}': {}", agentId, e.getMessage(), e);
                }
            }
        }
    }

    public void releaseForcedChunk() {
        if (this.forcedChunkPos != null && this.level() instanceof ServerLevel serverLevel) {
            try {
                serverLevel.setChunkForced(this.forcedChunkPos.x, this.forcedChunkPos.z, false);
                LOGGER.debug("Released forced chunk [{}, {}] for settler '{}'",
                        this.forcedChunkPos.x, this.forcedChunkPos.z, agentId);
            } catch (Exception e) {
                LOGGER.warn("Failed to release forced chunk for settler '{}': {}", agentId, e.getMessage());
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

    public void setDeathListener(Consumer<SettlerEntity> deathListener) {
        this.deathListener = deathListener;
    }

    public void setRemovalListener(Consumer<SettlerEntity> removalListener) {
        this.removalListener = removalListener;
    }

    public SettlerNavigator getNavigator() {
        return navigator;
    }
}
