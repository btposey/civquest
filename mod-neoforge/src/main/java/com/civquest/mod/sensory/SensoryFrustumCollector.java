package com.civquest.mod.sensory;

import com.civquest.mod.CivQuestMod;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.BoundingBoxProto;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.DamageSourceType;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.FarmlandSurveyEvent;
import com.civquest.mod.grpc.v1.ItemStackProto;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.VisibleBlock;
import com.civquest.mod.grpc.v1.VisibleEntity;
import com.civquest.mod.grpc.v1.Weather;
import net.minecraft.core.BlockPos;
import net.minecraft.core.component.DataComponents;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.tags.DamageTypeTags;
import net.minecraft.tags.TagKey;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.entity.Mob;
import net.minecraft.world.entity.TamableAnimal;
import net.minecraft.world.entity.monster.Creeper;
import net.minecraft.world.entity.monster.Enemy;
import net.minecraft.world.entity.monster.Monster;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.entity.projectile.Projectile;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.ClipContext;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.CampfireBlock;
import net.minecraft.world.level.block.ChestBlock;
import net.minecraft.world.level.block.CraftingTableBlock;
import net.minecraft.world.level.block.CropBlock;
import net.minecraft.world.level.block.DoorBlock;
import net.minecraft.world.level.block.FarmBlock;
import net.minecraft.world.level.block.FurnaceBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BlockStateProperties;
import net.minecraft.world.level.block.state.properties.Property;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.BlockHitResult;
import net.minecraft.world.phys.HitResult;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;

/**
 * Collects sensory frustum frames (5 Hz / 4 ticks) and zero-latency event telemetry
 * for {@link AgentEntity} instances.
 */
public class SensoryFrustumCollector {
    private static final Logger LOGGER = LoggerFactory.getLogger(SensoryFrustumCollector.class);

    public static final double ENTITY_SCAN_RADIUS = 24.0;
    public static final double ENTITY_LOS_BYPASS_DISTANCE = 2.0;
    public static final int BLOCK_SCAN_RADIUS = 16;
    public static final int MAX_INTERESTING_BLOCKS = 128;

    public static final TagKey<Block> INTERESTING_BLOCKS =
            TagKey.create(Registries.BLOCK, ResourceLocation.fromNamespaceAndPath(CivQuestMod.MOD_ID, "interesting_blocks"));

    public TelemetryFrame collectFrame(AgentEntity agent) {
        return collectFrame(agent, null, null);
    }

    public TelemetryFrame collectFrame(AgentEntity agent, DamageEvent damageEvent) {
        return collectFrame(agent, damageEvent, null);
    }

    public TelemetryFrame collectFrame(AgentEntity agent, DeathEvent deathEvent) {
        return collectFrame(agent, null, deathEvent);
    }

    /**
     * Constructs a comprehensive {@link TelemetryFrame} for the given agent.
     *
     * @param agent     the agent entity to scan and snapshot
     * @param damageEvent non-null if triggered by an immediate damage event
     * @param deathEvent  non-null if triggered by a terminal death event
     * @return constructed telemetry frame ready for streaming
     */
    public TelemetryFrame collectFrame(AgentEntity agent, DamageEvent damageEvent, DeathEvent deathEvent) {
        if (agent == null) {
            return TelemetryFrame.getDefaultInstance();
        }

        TelemetryFrame.Builder frameBuilder = TelemetryFrame.newBuilder();
        Level level = agent.level();

        // 1. World ticks and time
        long worldTick = level != null ? level.getGameTime() : 0L;
        long timeOfDay = level != null ? ((level.getDayTime() % 24000L) + 24000L) % 24000L : 0L;
        frameBuilder.setWorldTick(worldTick);
        frameBuilder.setTimeOfDay(timeOfDay);

        // 2. Weather
        frameBuilder.setWeather(determineWeather(level));

        // 3. Biome ID
        frameBuilder.setBiomeId(resolveBiomeId(level, agent.blockPosition()));

        // 4. Agent Status
        AgentStatus.Builder statusBuilder = AgentStatus.newBuilder()
                .setAgentId(agent.getAgentId() != null ? agent.getAgentId() : "")
                .setEntityId(agent.getId())
                .setPosition(toVec3Proto(agent.getX(), agent.getY(), agent.getZ()))
                .setYaw(agent.getYRot())
                .setPitch(agent.getXRot())
                .setHealth(agent.getHealth())
                .setMaxHealth(agent.getMaxHealth())
                .setIsSleeping(agent.isSleeping())
                .setIsInWater(agent.isInWater())
                .setIsOnFire(agent.isOnFire())
                .setNavigationState(agent.getNavigator() != null
                        ? agent.getNavigator().getNavigationState()
                        : NavigationState.NAVIGATION_STATE_IDLE);

        if (agent.getFoodData() != null) {
            statusBuilder.setFoodLevel(agent.getFoodData().getFoodLevel());
            statusBuilder.setSaturation(agent.getFoodData().getSaturationLevel());
        } else {
            statusBuilder.setFoodLevel(20);
            statusBuilder.setSaturation(5.0f);
        }

        Inventory inventory = agent.getInventory();
        if (inventory != null) {
            for (int slot = 0; slot < inventory.getContainerSize(); slot++) {
                ItemStack stack = inventory.getItem(slot);
                if (stack != null && !stack.isEmpty()) {
                    statusBuilder.addInventory(serializeItemStack(stack, slot));
                }
            }
        }

        // 5. Visible Entities within R = 24.0 blocks
        if (deathEvent == null && level != null) {
            AABB searchBox = agent.getBoundingBox().inflate(ENTITY_SCAN_RADIUS);
            List<Entity> nearbyEntities = level.getEntities(agent, searchBox,
                    e -> e != null && e.isAlive() && !e.isRemoved() && !e.isSpectator() && e != agent);
            net.minecraft.world.phys.Vec3 agentEye = agent.getEyePosition();

            for (Entity entity : nearbyEntities) {
                double dist = agent.distanceTo(entity);
                if (dist > ENTITY_SCAN_RADIUS) {
                    continue;
                }

                if (entity instanceof Mob mob && mob.getLeashHolder() == agent) {
                    statusBuilder.addLeashedEntityIds(mob.getId());
                }

                boolean visible = true;
                if (dist > ENTITY_LOS_BYPASS_DISTANCE) {
                    net.minecraft.world.phys.Vec3 entityEye = entity.getEyePosition();
                    ClipContext clipContext = new ClipContext(
                            agentEye,
                            entityEye,
                            ClipContext.Block.VISUAL,
                            ClipContext.Fluid.NONE,
                            agent
                    );
                    BlockHitResult hitResult = level.clip(clipContext);
                    if (hitResult.getType() == HitResult.Type.BLOCK) {
                        visible = false;
                    }
                }

                if (visible) {
                    VisibleEntity.Builder veBuilder = VisibleEntity.newBuilder()
                            .setEntityId(entity.getId())
                            .setUuid(entity.getUUID().toString())
                            .setEntityType(BuiltInRegistries.ENTITY_TYPE.getKey(entity.getType()).toString())
                            .setDisplayName(entity.getName().getString())
                            .setPosition(toVec3Proto(entity.getX(), entity.getY(), entity.getZ()))
                            .setDistance((float) dist)
                            .setHealth(entity instanceof LivingEntity living ? living.getHealth() : 0.0f)
                            .setIsHostile(isHostileEntity(entity))
                            .setIsTamed(isTamedEntity(entity))
                            .setHeldItemId(getHeldItemId(entity))
                            .setFuseTicks(getCreeperFuseTicks(entity));

                    frameBuilder.addVisibleEntities(veBuilder.build());
                }
            }
        }

        // 6. Interesting Blocks & Farmland Survey within R = 16 blocks
        if (deathEvent == null && level != null) {
            collectInterestingBlocks(agent, level, frameBuilder);
        }

        // 7. Events
        if (damageEvent != null) {
            frameBuilder.setLastDamageTaken(damageEvent);
        }
        if (deathEvent != null) {
            frameBuilder.setDeathEvent(deathEvent);
        }

        // 8. Pending incoming chat events
        List<com.civquest.mod.grpc.v1.ChatEvent> chats = agent.pollIncomingChats();
        if (chats != null && !chats.isEmpty()) {
            frameBuilder.addAllIncomingChats(chats);
        }

        frameBuilder.setStatus(statusBuilder.build());
        return frameBuilder.build();
    }

    public static Weather determineWeather(Level level) {
        if (level == null) {
            return Weather.WEATHER_CLEAR;
        }
        if (level.isThundering()) {
            return Weather.WEATHER_THUNDER;
        }
        if (level.isRaining()) {
            return Weather.WEATHER_RAIN;
        }
        return Weather.WEATHER_CLEAR;
    }

    public static String resolveBiomeId(Level level, BlockPos pos) {
        if (level == null || pos == null) {
            return "unknown";
        }
        try {
            return level.getBiome(pos)
                    .unwrapKey()
                    .map(k -> k.location().toString())
                    .orElse("unknown");
        } catch (Throwable t) {
            return "unknown";
        }
    }

    public static boolean isHostileEntity(Entity entity) {
        if (entity == null) {
            return false;
        }
        return entity instanceof Enemy;
    }

    public static boolean isTamedEntity(Entity entity) {
        if (entity instanceof TamableAnimal tamable) {
            return tamable.isTame();
        }
        return false;
    }

    public static String getHeldItemId(Entity entity) {
        if (entity instanceof LivingEntity living) {
            ItemStack held = living.getMainHandItem();
            if (held != null && !held.isEmpty()) {
                return BuiltInRegistries.ITEM.getKey(held.getItem()).toString();
            }
        }
        return "";
    }

    public static int getCreeperFuseTicks(Entity entity) {
        if (!(entity instanceof Creeper creeper)) {
            return 0;
        }
        if (creeper.isIgnited()) {
            return 30;
        }
        float swelling = creeper.getSwelling(0.0f);
        if (swelling > 0.0f || creeper.getSwellDir() != 0) {
            return Math.round(swelling * 30.0f);
        }
        return 0;
    }

    /**
     * Collects visible interesting blocks within R = 16 and performs farmland survey.
     * Explicitly samples blocks at and directly below agent's feet (feetPos = agent.blockPosition(),
     * belowFeet = feetPos.below()) to ensure underfoot terrain (grass_block, dirt, dirt_path) is always
     * delivered in interesting_blocks for paving and locomotion reflexes.
     */
    public void collectInterestingBlocks(AgentEntity agent, Level level, TelemetryFrame.Builder frameBuilder) {
        if (agent == null || level == null) {
            return;
        }

        Set<BlockPos> addedPositions = new HashSet<>();
        BlockPos center = agent.blockPosition();
        BlockPos feetPos = center;
        BlockPos belowFeet = feetPos.below();

        // Explicitly sample blocks at and directly below agent's feet
        for (BlockPos footPos : new BlockPos[]{feetPos, belowFeet}) {
            if (level.hasChunkAt(footPos)) {
                BlockState state = level.getBlockState(footPos);
                if (!state.isAir()) {
                    String blockId = BuiltInRegistries.BLOCK.getKey(state.getBlock()).toString();
                    String stateString = formatBlockProperties(state);
                    frameBuilder.addInterestingBlocks(VisibleBlock.newBuilder()
                            .setPos(toBlockPosProto(footPos))
                            .setBlockId(blockId)
                            .setBlockStateString(stateString)
                            .build());
                    addedPositions.add(footPos.immutable());
                }
            }
        }

        int radius = BLOCK_SCAN_RADIUS;
        int radiusSq = radius * radius;
        BlockPos.MutableBlockPos mpos = new BlockPos.MutableBlockPos();

        int totalFarmlandBlocks = 0;
        int matureCropCount = 0;
        Map<String, Integer> matureCropFrequency = new HashMap<>();
        int farmMinX = Integer.MAX_VALUE, farmMinY = Integer.MAX_VALUE, farmMinZ = Integer.MAX_VALUE;
        int farmMaxX = Integer.MIN_VALUE, farmMaxY = Integer.MIN_VALUE, farmMaxZ = Integer.MIN_VALUE;

        int minBuildHeight = level.getMinBuildHeight();
        int maxBuildHeight = level.getMaxBuildHeight();

        int lastChunkX = Integer.MIN_VALUE;
        int lastChunkZ = Integer.MIN_VALUE;
        boolean lastChunkLoaded = false;

        for (int dy = -radius; dy <= radius; dy++) {
            int y = center.getY() + dy;
            if (y < minBuildHeight || y >= maxBuildHeight) {
                continue;
            }
            int remRadiusSq = radiusSq - dy * dy;
            if (remRadiusSq < 0) continue;
            int maxDx = (int) Math.sqrt(remRadiusSq);
            for (int dx = -maxDx; dx <= maxDx; dx++) {
                int remZSq = remRadiusSq - dx * dx;
                if (remZSq < 0) continue;
                int maxDz = (int) Math.sqrt(remZSq);
                for (int dz = -maxDz; dz <= maxDz; dz++) {
                    mpos.set(center.getX() + dx, y, center.getZ() + dz);
                    int curChunkX = mpos.getX() >> 4;
                    int curChunkZ = mpos.getZ() >> 4;
                    if (curChunkX != lastChunkX || curChunkZ != lastChunkZ) {
                        lastChunkX = curChunkX;
                        lastChunkZ = curChunkZ;
                        lastChunkLoaded = level.hasChunkAt(mpos);
                    }
                    if (!lastChunkLoaded) {
                        continue;
                    }

                    BlockState state = level.getBlockState(mpos);
                    if (state.isAir()) {
                        continue;
                    }

                    if (isInterestingBlock(state)) {
                        if (frameBuilder.getInterestingBlocksCount() < MAX_INTERESTING_BLOCKS && !addedPositions.contains(mpos)) {
                            String blockId = BuiltInRegistries.BLOCK.getKey(state.getBlock()).toString();
                            String stateString = formatBlockProperties(state);
                            frameBuilder.addInterestingBlocks(VisibleBlock.newBuilder()
                                    .setPos(toBlockPosProto(mpos))
                                    .setBlockId(blockId)
                                    .setBlockStateString(stateString)
                                    .build());
                            addedPositions.add(mpos.immutable());
                        }
                    }

                    if (isFarmland(state)) {
                        totalFarmlandBlocks++;
                        farmMinX = Math.min(farmMinX, mpos.getX());
                        farmMinY = Math.min(farmMinY, mpos.getY());
                        farmMinZ = Math.min(farmMinZ, mpos.getZ());
                        farmMaxX = Math.max(farmMaxX, mpos.getX());
                        farmMaxY = Math.max(farmMaxY, mpos.getY());
                        farmMaxZ = Math.max(farmMaxZ, mpos.getZ());
                    }

                    if (isCrop(state)) {
                        if (isMatureCrop(state)) {
                            matureCropCount++;
                            String cropId = BuiltInRegistries.BLOCK.getKey(state.getBlock()).toString();
                            matureCropFrequency.merge(cropId, 1, Integer::sum);
                            farmMinX = Math.min(farmMinX, mpos.getX());
                            farmMinY = Math.min(farmMinY, mpos.getY());
                            farmMinZ = Math.min(farmMinZ, mpos.getZ());
                            farmMaxX = Math.max(farmMaxX, mpos.getX());
                            farmMaxY = Math.max(farmMaxY, mpos.getY());
                            farmMaxZ = Math.max(farmMaxZ, mpos.getZ());
                        }
                    }
                }
            }
        }

        if ((totalFarmlandBlocks > 0 || matureCropCount > 0) && farmMinX <= farmMaxX) {
            String primaryCropId = matureCropCount > 0 ? matureCropFrequency.entrySet().stream()
                    .max(Map.Entry.comparingByValue())
                    .map(Map.Entry::getKey)
                    .orElse("") : "";

            FarmlandSurveyEvent survey = FarmlandSurveyEvent.newBuilder()
                    .setBounds(BoundingBoxProto.newBuilder()
                            .setMin(BlockPosProto.newBuilder().setX(farmMinX).setY(farmMinY).setZ(farmMinZ).build())
                            .setMax(BlockPosProto.newBuilder().setX(farmMaxX).setY(farmMaxY).setZ(farmMaxZ).build())
                            .build())
                    .setPrimaryCropId(primaryCropId)
                    .setMatureCropCount(matureCropCount)
                    .setTotalFarmlandBlocks(totalFarmlandBlocks)
                    .setInferredOwnerUuid("")
                    .build();
            frameBuilder.setFarmlandSurvey(survey);
        }
    }

    public static boolean isInterestingBlock(BlockState state) {
        if (state == null || state.isAir()) {
            return false;
        }
        try {
            if (state.is(INTERESTING_BLOCKS)) {
                return true;
            }
        } catch (Throwable ignored) {
            // In unit test environments where registry tags are uninitialized
        }

        Block block = state.getBlock();
        return block instanceof FarmBlock
                || block instanceof CropBlock
                || block instanceof BedBlock
                || block instanceof DoorBlock
                || block instanceof ChestBlock
                || block instanceof FurnaceBlock
                || block instanceof CampfireBlock
                || block instanceof CraftingTableBlock
                || state.is(Blocks.CRAFTING_TABLE)
                || state.is(Blocks.FURNACE)
                || state.is(Blocks.BLAST_FURNACE)
                || state.is(Blocks.SMOKER)
                || state.is(Blocks.CAMPFIRE)
                || state.is(Blocks.SOUL_CAMPFIRE)
                || state.is(Blocks.CHEST)
                || state.is(Blocks.TRAPPED_CHEST)
                || state.is(Blocks.BARREL)
                || state.is(Blocks.FARMLAND)
                || state.is(Blocks.COMPOSTER)
                || state.is(Blocks.BEEHIVE)
                || state.is(Blocks.BEE_NEST)
                || state.is(Blocks.LECTERN)
                || state.is(Blocks.BELL);
    }

    public static boolean isFarmland(BlockState state) {
        if (state == null) {
            return false;
        }
        return state.getBlock() instanceof FarmBlock || state.is(Blocks.FARMLAND);
    }

    public static boolean isCrop(BlockState state) {
        if (state == null) {
            return false;
        }
        return state.getBlock() instanceof CropBlock
                || state.is(Blocks.WHEAT)
                || state.is(Blocks.CARROTS)
                || state.is(Blocks.POTATOES)
                || state.is(Blocks.BEETROOTS);
    }

    public static boolean isMatureCrop(BlockState state) {
        if (state == null) {
            return false;
        }
        if (state.getBlock() instanceof CropBlock crop) {
            return crop.isMaxAge(state);
        }
        if (state.hasProperty(BlockStateProperties.AGE_7)) {
            return state.getValue(BlockStateProperties.AGE_7) >= 7;
        }
        if (state.hasProperty(BlockStateProperties.AGE_3)) {
            return state.getValue(BlockStateProperties.AGE_3) >= 3;
        }
        return false;
    }

    public static String formatBlockProperties(BlockState state) {
        if (state == null || state.getValues().isEmpty()) {
            return "";
        }
        return state.getValues().entrySet().stream()
                .map(entry -> entry.getKey().getName() + "=" + getPropertyValueName(entry.getKey(), entry.getValue()))
                .sorted()
                .collect(Collectors.joining(","));
    }

    @SuppressWarnings("unchecked")
    private static <T extends Comparable<T>> String getPropertyValueName(Property<T> property, Comparable<?> value) {
        return property.getName((T) value);
    }

    public static ItemStackProto serializeItemStack(ItemStack stack, int slot) {
        if (stack == null || stack.isEmpty()) {
            return ItemStackProto.getDefaultInstance();
        }
        String itemId = BuiltInRegistries.ITEM.getKey(stack.getItem()).toString();
        int count = stack.getCount();
        int durability = stack.isDamageableItem() ? (stack.getMaxDamage() - stack.getDamageValue()) : 0;
        int maxDurability = stack.getMaxDamage();
        String customName = stack.get(DataComponents.CUSTOM_NAME) != null
                ? stack.get(DataComponents.CUSTOM_NAME).getString()
                : "";

        return ItemStackProto.newBuilder()
                .setItemId(itemId)
                .setCount(count)
                .setSlot(slot)
                .setDurability(durability)
                .setMaxDurability(maxDurability)
                .setCustomName(customName)
                .build();
    }

    public static DamageSourceType mapDamageSourceType(DamageSource source) {
        if (source == null) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_UNSPECIFIED;
        }
        if (source.is(DamageTypeTags.IS_PROJECTILE) || source.getDirectEntity() instanceof Projectile) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_ARROW;
        }
        if (source.is(DamageTypeTags.IS_EXPLOSION)) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_EXPLOSION;
        }
        if (source.is(DamageTypeTags.IS_FALL)) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_FALL;
        }
        if (source.is(DamageTypeTags.IS_FIRE)) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_LAVA;
        }
        Entity attacker = source.getEntity();
        if (attacker instanceof Player) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_PLAYER;
        }
        if (attacker instanceof Mob || attacker instanceof Monster) {
            return DamageSourceType.DAMAGE_SOURCE_TYPE_MOB;
        }
        return DamageSourceType.DAMAGE_SOURCE_TYPE_UNSPECIFIED;
    }

    public static DamageEvent buildDamageEvent(DamageSource source, float damageAmount) {
        DamageEvent.Builder builder = DamageEvent.newBuilder()
                .setSourceType(mapDamageSourceType(source))
                .setDamageAmount(damageAmount);
        if (source != null) {
            Entity attacker = source.getEntity();
            if (attacker == null) {
                attacker = source.getDirectEntity();
            }
            if (attacker != null) {
                builder.setAttackerEntityId(attacker.getId());
                builder.setAttackerUuid(attacker.getUUID().toString());
                builder.setAttackerName(attacker.getName().getString());
            }
        }
        return builder.build();
    }

    public static DeathEvent buildDeathEvent(AgentEntity agent, DamageSource cause) {
        if (agent == null) {
            return DeathEvent.getDefaultInstance();
        }
        DeathEvent.Builder deathBuilder = DeathEvent.newBuilder()
                .setAgentId(agent.getAgentId() != null ? agent.getAgentId() : "")
                .setWorldTick(agent.level() != null ? agent.level().getGameTime() : 0L)
                .setDeathLocation(toVec3Proto(agent.getX(), agent.getY(), agent.getZ()))
                .setDamageSource(cause != null && cause.getMsgId() != null ? cause.getMsgId() : "generic");

        if (cause != null) {
            Entity killer = cause.getEntity();
            if (killer == null) {
                killer = cause.getDirectEntity();
            }
            if (killer != null) {
                deathBuilder.setKillerEntityId(killer.getId());
                deathBuilder.setKillerUuid(killer.getUUID().toString());
                deathBuilder.setKillerDisplayName(killer.getName().getString());
                if (killer instanceof Player && !deathBuilder.getDamageSource().contains("player")) {
                    deathBuilder.setDamageSource("player." + deathBuilder.getDamageSource());
                }
            }
        }

        Inventory inventory = agent.getInventory();
        if (inventory != null) {
            for (int i = 0; i < inventory.getContainerSize(); i++) {
                ItemStack stack = inventory.getItem(i);
                if (stack != null && !stack.isEmpty()) {
                    deathBuilder.addDroppedLoot(serializeItemStack(stack, i));
                }
            }
        }

        return deathBuilder.build();
    }

    public static com.civquest.mod.grpc.v1.Vec3 toVec3Proto(net.minecraft.world.phys.Vec3 vec) {
        if (vec == null) {
            return com.civquest.mod.grpc.v1.Vec3.getDefaultInstance();
        }
        return com.civquest.mod.grpc.v1.Vec3.newBuilder()
                .setX(vec.x)
                .setY(vec.y)
                .setZ(vec.z)
                .build();
    }

    public static com.civquest.mod.grpc.v1.Vec3 toVec3Proto(double x, double y, double z) {
        return com.civquest.mod.grpc.v1.Vec3.newBuilder()
                .setX(x)
                .setY(y)
                .setZ(z)
                .build();
    }

    public static BlockPosProto toBlockPosProto(BlockPos pos) {
        if (pos == null) {
            return BlockPosProto.getDefaultInstance();
        }
        return BlockPosProto.newBuilder()
                .setX(pos.getX())
                .setY(pos.getY())
                .setZ(pos.getZ())
                .build();
    }
}
