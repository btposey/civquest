package com.civquest.mod.sensory;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AgentStatus;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.DamageSourceType;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.ItemStackProto;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.Vec3;
import com.civquest.mod.grpc.v1.VisibleBlock;
import com.civquest.mod.grpc.v1.VisibleEntity;
import com.civquest.mod.grpc.v1.Weather;
import net.minecraft.core.BlockPos;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.tags.DamageTypeTags;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.level.Level;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import com.civquest.mod.test.MinecraftTestBootstrap;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

public class SensoryFrustumCollectorTest {

    private SensoryFrustumCollector collector;

    @BeforeAll
    static void initMinecraft() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        collector = new SensoryFrustumCollector();
    }

    @Test
    @DisplayName("collectFrame handles null agent safely returning default instance")
    void testCollectFrameNullAgent() {
        TelemetryFrame frame = collector.collectFrame(null);
        assertNotNull(frame);
        assertEquals(TelemetryFrame.getDefaultInstance(), frame);
    }

    @Test
    @DisplayName("determineWeather correctly reports weather state from level")
    void testDetermineWeather() {
        assertEquals(Weather.WEATHER_CLEAR, SensoryFrustumCollector.determineWeather(null));

        net.minecraft.server.level.ServerLevel thunderingLevel = mock(net.minecraft.server.level.ServerLevel.class);
        when(thunderingLevel.isThundering()).thenReturn(true);
        assertEquals(Weather.WEATHER_THUNDER, SensoryFrustumCollector.determineWeather(thunderingLevel));

        net.minecraft.server.level.ServerLevel rainingLevel = mock(net.minecraft.server.level.ServerLevel.class);
        when(rainingLevel.isThundering()).thenReturn(false);
        when(rainingLevel.isRaining()).thenReturn(true);
        assertEquals(Weather.WEATHER_RAIN, SensoryFrustumCollector.determineWeather(rainingLevel));

        net.minecraft.server.level.ServerLevel clearLevel = mock(net.minecraft.server.level.ServerLevel.class);
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
    @DisplayName("mapDamageSourceType maps IS_DROWNING to DAMAGE_SOURCE_TYPE_DROWN")
    void testMapDamageSourceTypeDrowning() {
        DamageSource drownSource = mock(DamageSource.class);
        when(drownSource.is(DamageTypeTags.IS_DROWNING)).thenReturn(true);
        assertEquals(DamageSourceType.DAMAGE_SOURCE_TYPE_DROWN, SensoryFrustumCollector.mapDamageSourceType(drownSource));
    }

    @Test
    @DisplayName("buildDeathEvent handles null agent safely returning default instance")
    void testBuildDeathEventNullAgent() {
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

    @Test
    @DisplayName("collectFrame populates IntentReceipt and motor_stack_depth from agent IntentActuator and MotorPDA")
    void testCollectFramePopulatesReceipt() {
        AgentEntity agent = mock(AgentEntity.class);
        IntentActuator actuator = new IntentActuator();
        AgentMotorFSM fsm = new AgentMotorFSM(agent);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);
        when(nav.getReportedNavigationState()).thenReturn(NavigationState.NAVIGATION_STATE_PATHING);
        when(nav.getPosition()).thenReturn(net.minecraft.world.phys.Vec3.ZERO);
        when(agent.getAgentId()).thenReturn("agent-receipt-test");
        when(agent.getId()).thenReturn(7);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getIntentActuator()).thenReturn(actuator);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(agent.pollIncomingChats()).thenReturn(java.util.Collections.emptyList());
        when(agent.pollReflexNotices()).thenReturn(java.util.Collections.emptyList());

        actuator.submitBatch(agent, IntentBatch.newBuilder()
                .setAgentId("agent-receipt-test")
                .setBatchId(555L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-receipt-test")
                        .setSequenceId(88L)
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(20).setY(64).setZ(20).build())
                                .build())
                        .build())
                .build());
        // Simulate reflex preemption depth = 1 on MotorPDA while navigating
        assertInstanceOf(com.civquest.mod.actuator.motor.Move.class, fsm.topMotor().orElse(null));
        fsm.pushReflex(new com.civquest.mod.actuator.motor.Combat(0, 0), "reflex");

        TelemetryFrame frame = collector.collectFrame(agent);
        assertTrue(frame.hasReceipt());
        assertEquals(555L, frame.getReceipt().getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX, frame.getReceipt().getStatus());
        assertEquals(1, frame.getReceipt().getMotorStackDepth());
        assertEquals(88L, frame.getLastProcessedSequenceId());
    }

    @Test
    @DisplayName("collectInterestingBlocks reserves up to MAX_LOG_BLOCKS slots so non-log clutter cannot starve nearby trees")
    void testCollectInterestingBlocksReservesLogCapacityUnderHeavyNonLogClutter() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(ServerLevel.class);
        BlockPos center = new BlockPos(0, 64, 0);
        when(agent.blockPosition()).thenReturn(center);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(level.getMinBuildHeight()).thenReturn(-64);
        when(level.getMaxBuildHeight()).thenReturn(320);

        var airState = net.minecraft.world.level.block.Blocks.AIR.defaultBlockState();
        var farmlandState = net.minecraft.world.level.block.Blocks.FARMLAND.defaultBlockState();
        var oakLogState = net.minecraft.world.level.block.Blocks.OAK_LOG.defaultBlockState();

        // Populate >150 farmland blocks across y=62..63 (scanned before y=64..70 logs) plus 30 oak logs at y=64..69
        java.util.Set<BlockPos> logPositions = new java.util.HashSet<>();
        for (int tree = 0; tree < 6; tree++) {
            int tx = 3 + tree * 2; // x = 3, 5, 7, 9, 11, 13 (varying distances from center)
            for (int y = 64; y <= 68; y++) {
                logPositions.add(new BlockPos(tx, y, 0));
            }
        }

        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (logPositions.contains(p)) {
                return oakLogState;
            }
            if ((p.getY() == 62 || p.getY() == 63) && Math.abs(p.getX()) <= 6 && Math.abs(p.getZ()) <= 6) {
                return farmlandState;
            }
            return airState;
        });

        TelemetryFrame.Builder frameBuilder = TelemetryFrame.newBuilder();
        collector.collectInterestingBlocks(agent, level, frameBuilder);

        assertEquals(SensoryFrustumCollector.MAX_INTERESTING_BLOCKS, frameBuilder.getInterestingBlocksCount(),
                "Frame must fill up to MAX_INTERESTING_BLOCKS when >128 interesting blocks exist");

        java.util.List<VisibleBlock> reportedLogs = frameBuilder.getInterestingBlocksList().stream()
                .filter(b -> b.getBlockId().equals("minecraft:oak_log"))
                .toList();
        assertEquals(SensoryFrustumCollector.MAX_LOG_BLOCKS, reportedLogs.size(),
                "Closest MAX_LOG_BLOCKS oak logs must never be starved out by farmland/underfoot clutter");

        // Closest log at (3, 64, 0) must be included, while farthest log at (13, 68, 0) is excluded by MAX_LOG_BLOCKS=24
        assertTrue(reportedLogs.stream().anyMatch(b -> b.getPos().getX() == 3 && b.getPos().getY() == 64 && b.getPos().getZ() == 0),
                "Closest oak log at (3,64,0) must be reported");
        assertFalse(reportedLogs.stream().anyMatch(b -> b.getPos().getX() == 13 && b.getPos().getY() == 68 && b.getPos().getZ() == 0),
                "Farthest oak log at (13,68,0) should be trimmed in favor of closer logs");
    }

    @Test
    @DisplayName("collectFrame attaches pending HorizonSurveyReport polled from AgentEntity onto TelemetryFrame")
    void testCollectFrameAttachesHorizonSurveyReport() {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.getAgentId()).thenReturn("horizon-telemetry-agent");
        when(agent.getId()).thenReturn(19);
        when(agent.pollIncomingChats()).thenReturn(java.util.Collections.emptyList());
        when(agent.pollReflexNotices()).thenReturn(java.util.Collections.emptyList());

        com.civquest.mod.grpc.v1.HorizonSurveyReport report = com.civquest.mod.grpc.v1.HorizonSurveyReport.newBuilder()
                .setOriginX(12.5)
                .setOriginY(65.62)
                .setOriginZ(-8.5)
                .setCompletedTick(2400L)
                .setMaxRange(64.0f)
                .addSectors(com.civquest.mod.grpc.v1.HorizonSector.newBuilder()
                        .setSectorIndex(0)
                        .setBearingDeg(0.0f)
                        .setHitDistance(40.0f)
                        .setFeatureType(HorizonSurveyScanner.FEATURE_WATER)
                        .setHitBlockId("minecraft:water")
                        .build())
                .build();
        when(agent.pollHorizonSurveyReport()).thenReturn(report).thenReturn(null);

        TelemetryFrame firstFrame = collector.collectFrame(agent);
        assertTrue(firstFrame.hasHorizonSurvey());
        assertEquals(2400L, firstFrame.getHorizonSurvey().getCompletedTick());
        assertEquals(1, firstFrame.getHorizonSurvey().getSectorsCount());
        assertEquals(HorizonSurveyScanner.FEATURE_WATER, firstFrame.getHorizonSurvey().getSectors(0).getFeatureType());

        TelemetryFrame secondFrame = collector.collectFrame(agent);
        assertFalse(secondFrame.hasHorizonSurvey());
    }
}
