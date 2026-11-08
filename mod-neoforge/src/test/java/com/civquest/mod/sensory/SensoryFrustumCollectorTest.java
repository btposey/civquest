package com.civquest.mod.sensory;

import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.DamageSourceType;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.ItemStackProto;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.Vec3;
import com.civquest.mod.grpc.v1.VisibleBlock;
import com.civquest.mod.grpc.v1.VisibleEntity;
import com.civquest.mod.grpc.v1.Weather;
import net.minecraft.core.BlockPos;
import net.minecraft.world.level.Level;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

public class SensoryFrustumCollectorTest {

    private SensoryFrustumCollector collector;

    @BeforeEach
    void setUp() {
        collector = new SensoryFrustumCollector();
    }

    @Test
    @DisplayName("collectFrame handles null settler safely returning default instance")
    void testCollectFrameNullSettler() {
        TelemetryFrame frame = collector.collectFrame(null);
        assertNotNull(frame);
        assertEquals(TelemetryFrame.getDefaultInstance(), frame);
    }

    @Test
    @DisplayName("determineWeather correctly reports weather state from level")
    void testDetermineWeather() {
        assertEquals(Weather.WEATHER_CLEAR, SensoryFrustumCollector.determineWeather(null));

        Level thunderingLevel = mock(Level.class);
        when(thunderingLevel.isThundering()).thenReturn(true);
        assertEquals(Weather.WEATHER_THUNDER, SensoryFrustumCollector.determineWeather(thunderingLevel));

        Level rainingLevel = mock(Level.class);
        when(rainingLevel.isThundering()).thenReturn(false);
        when(rainingLevel.isRaining()).thenReturn(true);
        assertEquals(Weather.WEATHER_RAIN, SensoryFrustumCollector.determineWeather(rainingLevel));

        Level clearLevel = mock(Level.class);
        when(clearLevel.isThundering()).thenReturn(false);
        when(clearLevel.isRaining()).thenReturn(false);
        assertEquals(Weather.WEATHER_CLEAR, SensoryFrustumCollector.determineWeather(clearLevel));
    }

    @Test
    @DisplayName("resolveBiomeId returns unknown for null level or null position")
    void testResolveBiomeIdNullSafe() {
        assertEquals("unknown", SensoryFrustumCollector.resolveBiomeId(null, null));
        Level level = mock(Level.class);
        assertEquals("unknown", SensoryFrustumCollector.resolveBiomeId(level, null));
        assertEquals("unknown", SensoryFrustumCollector.resolveBiomeId(null, BlockPos.ZERO));
    }

    @Test
    @DisplayName("toVec3Proto converts coordinates and null correctly")
    void testToVec3Proto() {
        Vec3 nullVec = SensoryFrustumCollector.toVec3Proto((net.minecraft.world.phys.Vec3) null);
        assertNotNull(nullVec);
        assertEquals(0.0, nullVec.getX());

        Vec3 proto = SensoryFrustumCollector.toVec3Proto(12.5, 64.0, -99.25);
        assertNotNull(proto);
        assertEquals(12.5, proto.getX(), 0.001);
        assertEquals(64.0, proto.getY(), 0.001);
        assertEquals(-99.25, proto.getZ(), 0.001);

        net.minecraft.world.phys.Vec3 mcVec = new net.minecraft.world.phys.Vec3(1.0, 2.0, 3.0);
        Vec3 converted = SensoryFrustumCollector.toVec3Proto(mcVec);
        assertEquals(1.0, converted.getX(), 0.001);
        assertEquals(2.0, converted.getY(), 0.001);
        assertEquals(3.0, converted.getZ(), 0.001);
    }

    @Test
    @DisplayName("toBlockPosProto converts coordinates and null correctly")
    void testToBlockPosProto() {
        BlockPosProto nullPos = SensoryFrustumCollector.toBlockPosProto(null);
        assertNotNull(nullPos);
        assertEquals(0, nullPos.getX());

        BlockPos mcPos = new BlockPos(100, 72, -300);
        BlockPosProto proto = SensoryFrustumCollector.toBlockPosProto(mcPos);
        assertNotNull(proto);
        assertEquals(100, proto.getX());
        assertEquals(72, proto.getY());
        assertEquals(-300, proto.getZ());
    }

    @Test
    @DisplayName("buildDamageEvent creates basic DamageEvent with null source safely")
    void testBuildDamageEventNullSource() {
        DamageEvent event = SensoryFrustumCollector.buildDamageEvent(null, 4.0f);
        assertNotNull(event);
        assertEquals(DamageSourceType.DAMAGE_SOURCE_TYPE_UNSPECIFIED, event.getSourceType());
        assertEquals(4.0f, event.getDamageAmount(), 0.001);
        assertEquals(0, event.getAttackerEntityId());
        assertEquals("", event.getAttackerUuid());
        assertEquals("", event.getAttackerName());
    }

    @Test
    @DisplayName("mapDamageSourceType returns UNSPECIFIED for null source")
    void testMapDamageSourceTypeNull() {
        assertEquals(DamageSourceType.DAMAGE_SOURCE_TYPE_UNSPECIFIED, SensoryFrustumCollector.mapDamageSourceType(null));
    }

    @Test
    @DisplayName("buildDeathEvent handles null settler safely returning default instance")
    void testBuildDeathEventNullSettler() {
        DeathEvent deathEvent = SensoryFrustumCollector.buildDeathEvent(null, null);
        assertNotNull(deathEvent);
        assertEquals(DeathEvent.getDefaultInstance(), deathEvent);
    }

    @Test
    @DisplayName("serializeItemStack returns default instance on null or empty stack")
    void testSerializeItemStackEmpty() {
        ItemStackProto protoNull = SensoryFrustumCollector.serializeItemStack(null, 0);
        assertNotNull(protoNull);
        assertEquals(ItemStackProto.getDefaultInstance(), protoNull);
    }

    @Test
    @DisplayName("formatBlockProperties returns empty string on null state")
    void testFormatBlockPropertiesNull() {
        assertEquals("", SensoryFrustumCollector.formatBlockProperties(null));
    }

    @Test
    @DisplayName("Entity classification and fuse ticks handle null safely")
    void testEntityClassificationNull() {
        assertFalse(SensoryFrustumCollector.isHostileEntity(null));
        assertFalse(SensoryFrustumCollector.isTamedEntity(null));
        assertEquals("", SensoryFrustumCollector.getHeldItemId(null));
        assertEquals(0, SensoryFrustumCollector.getCreeperFuseTicks(null));
    }

    @Test
    @DisplayName("Block classifications handle null safely")
    void testBlockClassificationsNull() {
        assertFalse(SensoryFrustumCollector.isInterestingBlock(null));
        assertFalse(SensoryFrustumCollector.isFarmland(null));
        assertFalse(SensoryFrustumCollector.isCrop(null));
        assertFalse(SensoryFrustumCollector.isMatureCrop(null));
    }

    @Test
    @DisplayName("SensoryFrustumCollector constants adhere to architectural specifications")
    void testConstants() {
        assertEquals(24.0, SensoryFrustumCollector.ENTITY_SCAN_RADIUS, 0.001);
        assertEquals(2.0, SensoryFrustumCollector.ENTITY_LOS_BYPASS_DISTANCE, 0.001);
        assertEquals(16, SensoryFrustumCollector.BLOCK_SCAN_RADIUS);
        assertEquals(128, SensoryFrustumCollector.MAX_INTERESTING_BLOCKS);
        assertNotNull(SensoryFrustumCollector.INTERESTING_BLOCKS);
        assertEquals("civquest:interesting_blocks", SensoryFrustumCollector.INTERESTING_BLOCKS.location().toString());
    }

    @Test
    @DisplayName("collectInterestingBlocks handles null arguments safely")
    void testCollectInterestingBlocksNullSafe() {
        TelemetryFrame.Builder builder = TelemetryFrame.newBuilder();
        collector.collectInterestingBlocks(null, null, builder);
        assertEquals(0, builder.getInterestingBlocksCount());
    }

    @Test
    @DisplayName("TelemetryFrame assembly roundtrip preserves all sensory frustum components")
    void testTelemetryFrameAssembly() {
        TelemetryFrame frame = TelemetryFrame.newBuilder()
                .setWorldTick(12345L)
                .setTimeOfDay(6000L)
                .setWeather(Weather.WEATHER_CLEAR)
                .setBiomeId("minecraft:plains")
                .setStatus(AgentStatus.newBuilder()
                        .setAgentId("agent-sim-01")
                        .setEntityId(42)
                        .setPosition(Vec3.newBuilder().setX(10.0).setY(64.0).setZ(20.0).build())
                        .setHealth(20.0f)
                        .setMaxHealth(20.0f)
                        .setFoodLevel(20)
                        .setSaturation(5.0f)
                        .setNavigationState(NavigationState.NAVIGATION_STATE_IDLE)
                        .build())
                .addVisibleEntities(VisibleEntity.newBuilder()
                        .setEntityId(101)
                        .setUuid("uuid-creeper-1")
                        .setEntityType("minecraft:creeper")
                        .setDisplayName("Creeper")
                        .setPosition(Vec3.newBuilder().setX(12.0).setY(64.0).setZ(20.0).build())
                        .setDistance(2.0f)
                        .setIsHostile(true)
                        .setFuseTicks(15)
                        .build())
                .addInterestingBlocks(VisibleBlock.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(10).setY(63).setZ(20).build())
                        .setBlockId("minecraft:farmland")
                        .setBlockStateString("moisture=7")
                        .build())
                .setLastDamageTaken(DamageEvent.newBuilder()
                        .setSourceType(DamageSourceType.DAMAGE_SOURCE_TYPE_MOB)
                        .setAttackerEntityId(101)
                        .setDamageAmount(5.0f)
                        .build())
                .build();

        assertNotNull(frame);
        assertEquals(12345L, frame.getWorldTick());
        assertEquals(6000L, frame.getTimeOfDay());
        assertEquals(Weather.WEATHER_CLEAR, frame.getWeather());
        assertEquals("minecraft:plains", frame.getBiomeId());
        assertEquals("agent-sim-01", frame.getStatus().getAgentId());
        assertEquals(1, frame.getVisibleEntitiesCount());
        assertEquals("minecraft:creeper", frame.getVisibleEntities(0).getEntityType());
        assertEquals(15, frame.getVisibleEntities(0).getFuseTicks());
        assertEquals(1, frame.getInterestingBlocksCount());
        assertEquals("minecraft:farmland", frame.getInterestingBlocks(0).getBlockId());
        assertEquals(5.0f, frame.getLastDamageTaken().getDamageAmount(), 0.001);
    }
}
