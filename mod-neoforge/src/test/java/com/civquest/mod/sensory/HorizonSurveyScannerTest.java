package com.civquest.mod.sensory;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.HorizonSector;
import com.civquest.mod.test.MinecraftTestBootstrap;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.ClipContext;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.BlockHitResult;
import net.minecraft.world.phys.Vec3;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

public class HorizonSurveyScannerTest {

    @BeforeAll
    static void initMinecraft() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    private static AgentEntity mockAgentAt(Vec3 eyePos) {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.getEyePosition()).thenReturn(eyePos);
        when(agent.getX()).thenReturn(eyePos.x);
        when(agent.getY()).thenReturn(eyePos.y - 1.62);
        when(agent.getZ()).thenReturn(eyePos.z);
        when(agent.getMainHandItem()).thenReturn(ItemStack.EMPTY);
        return agent;
    }

    private static double extractPitchDeg(ClipContext ctx) {
        Vec3 delta = ctx.getTo().subtract(ctx.getFrom()).normalize();
        return Math.toDegrees(Math.asin(Math.max(-1.0, Math.min(1.0, delta.y))));
    }

    @Test
    @DisplayName("Bearing-to-vector math matches Minecraft native yaw convention (0=+Z, 90=-X, 180=-Z, 270=+X)")
    void testBearingToVectorMatchesMinecraftYaw() {
        Vec3 south = HorizonSurveyScanner.bearingToHorizontalDirection(0.0f);
        assertEquals(0.0, south.x, 1e-6);
        assertEquals(0.0, south.y, 1e-6);
        assertEquals(1.0, south.z, 1e-6);

        Vec3 west = HorizonSurveyScanner.bearingToHorizontalDirection(90.0f);
        assertEquals(-1.0, west.x, 1e-6);
        assertEquals(0.0, west.y, 1e-6);
        assertEquals(0.0, west.z, 1e-6);

        Vec3 north = HorizonSurveyScanner.bearingToHorizontalDirection(180.0f);
        assertEquals(0.0, north.x, 1e-6);
        assertEquals(0.0, north.y, 1e-6);
        assertEquals(-1.0, north.z, 1e-6);

        Vec3 east = HorizonSurveyScanner.bearingToHorizontalDirection(270.0f);
        assertEquals(1.0, east.x, 1e-6);
        assertEquals(0.0, east.y, 1e-6);
        assertEquals(0.0, east.z, 1e-6);

        Vec3 upRay = HorizonSurveyScanner.bearingAndPitchToDirection(0.0f, 22.0f);
        assertTrue(upRay.y > 0.35, "Positive pitch must point upward");
        assertEquals(1.0, upRay.length(), 1e-6);

        Vec3 downRay = HorizonSurveyScanner.bearingAndPitchToDirection(0.0f, -18.0f);
        assertTrue(downRay.y < -0.30, "Negative pitch must point downward");
        assertEquals(1.0, downRay.length(), 1e-6);
    }

    @Test
    @DisplayName("Unobstructed open plains returns OPEN_PLAINS with open_horizon=true and occluded=false without chunk clamping")
    void testUnobstructedOpenPlains() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        Map<BlockPos, BlockState> worldBlocks = new HashMap<>();
        BlockPos grassPos1 = new BlockPos(0, 64, 6);
        BlockPos grassPos2 = new BlockPos(0, 64, 12);
        worldBlocks.put(grassPos1, Blocks.GRASS_BLOCK.defaultBlockState());
        worldBlocks.put(grassPos2, Blocks.GRASS_BLOCK.defaultBlockState());

        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            return worldBlocks.getOrDefault(p, Blocks.AIR.defaultBlockState());
        });

        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - (-18.0)) < 1.0) {
                return new BlockHitResult(new Vec3(0.5, 64.0, 5.74), Direction.UP, grassPos1, false);
            }
            if (Math.abs(pitch - (-8.0)) < 1.0) {
                return new BlockHitResult(new Vec3(0.5, 64.0, 12.14), Direction.UP, grassPos2, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.NORTH, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 0, 0.0f, 64.0f);

        assertEquals(0, sector.getSectorIndex());
        assertEquals(0.0f, sector.getBearingDeg(), 1e-4);
        assertEquals(HorizonSurveyScanner.FEATURE_OPEN_PLAINS, sector.getFeatureType());
        assertEquals("minecraft:air", sector.getHitBlockId());
        assertEquals(64.0f, sector.getHitDistance(), 0.01f);
        assertTrue(sector.getOpenHorizon());
        assertFalse(sector.getOccluded());

        // Must cast all 5 fan rays without ever clamping to loaded chunks via hasChunkAt
        verify(level, times(5)).clip(any(ClipContext.class));
        verify(level, never()).hasChunkAt(any(BlockPos.class));
    }

    @Test
    @DisplayName("Distant oak_leaves canopy at 45m returns FOREST_CANOPY with exact hit coordinates and distance")
    void testDistantForestCanopyAt45m() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos leafPos = new BlockPos(0, 73, 45);
        Vec3 exactLeafHit = new Vec3(0.5, 73.43, 44.82); // distance = 45.0m along +Z at +10 deg
        double expectedDist = eyePos.distanceTo(exactLeafHit);

        when(level.getBlockState(leafPos)).thenReturn(Blocks.OAK_LEAVES.defaultBlockState());
        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - 10.0) < 1.0) {
                return new BlockHitResult(exactLeafHit, Direction.NORTH, leafPos, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.NORTH, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 0, 0.0f, 64.0f);

        assertEquals(HorizonSurveyScanner.FEATURE_FOREST_CANOPY, sector.getFeatureType());
        assertEquals("minecraft:oak_leaves", sector.getHitBlockId());
        assertEquals((float) expectedDist, sector.getHitDistance(), 0.01f);
        assertEquals(exactLeafHit.x, sector.getHitX(), 1e-4);
        assertEquals(exactLeafHit.y, sector.getHitY(), 1e-4);
        assertEquals(exactLeafHit.z, sector.getHitZ(), 1e-4);
        assertEquals((float) (exactLeafHit.y - eyePos.y), sector.getElevationDelta(), 1e-4);
        assertFalse(sector.getOccluded());
    }

    @Test
    @DisplayName("Close solid cliff/wall at 5m returns RIDGE_OBSTRUCTION with occluded=true and hides distant canopy")
    void testCloseWallOccludesAndHidesDistantCanopy() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos wallPos = new BlockPos(0, 65, 5);
        BlockPos leafPos = new BlockPos(0, 73, 45);
        when(level.getBlockState(wallPos)).thenReturn(Blocks.STONE.defaultBlockState());
        when(level.getBlockState(leafPos)).thenReturn(Blocks.OAK_LEAVES.defaultBlockState());

        // Case 1: Full cliff at 5m blocking all 5 rays
        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            Vec3 dir = ctx.getTo().subtract(ctx.getFrom()).normalize();
            Vec3 hit = eyePos.add(dir.scale(5.0));
            return new BlockHitResult(hit, Direction.NORTH, wallPos, false);
        });

        HorizonSector fullWall = HorizonSurveyScanner.scanSector(level, agent, eyePos, 9, 90.0f, 64.0f);
        assertEquals(HorizonSurveyScanner.FEATURE_RIDGE_OBSTRUCTION, fullWall.getFeatureType());
        assertTrue(fullWall.getOccluded());
        assertFalse(fullWall.getOpenHorizon());
        assertEquals(5.0f, fullWall.getHitDistance(), 0.05f);

        // Case 2: Opaque 3-block wall at 5m blocking level ray (0 deg) while +22 deg sees leaves behind it
        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (pitch <= 0.5) {
                Vec3 dir = ctx.getTo().subtract(ctx.getFrom()).normalize();
                return new BlockHitResult(eyePos.add(dir.scale(5.0)), Direction.NORTH, wallPos, false);
            }
            Vec3 dir = ctx.getTo().subtract(ctx.getFrom()).normalize();
            return new BlockHitResult(eyePos.add(dir.scale(45.0)), Direction.NORTH, leafPos, false);
        });

        HorizonSector faceWall = HorizonSurveyScanner.scanSector(level, agent, eyePos, 9, 90.0f, 64.0f);
        assertEquals(HorizonSurveyScanner.FEATURE_RIDGE_OBSTRUCTION, faceWall.getFeatureType());
        assertTrue(faceWall.getOccluded());
        assertFalse(faceWall.getOpenHorizon());
    }

    @Test
    @DisplayName("Water hit via ClipContext.Fluid.ANY returns WATER")
    void testWaterDetectionViaFluidAny() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos waterPos = new BlockPos(-20, 62, 0);
        Vec3 waterHit = new Vec3(-20.0, 62.8, 0.5);
        when(level.getBlockState(waterPos)).thenReturn(Blocks.WATER.defaultBlockState());
        when(level.getFluidState(any(BlockPos.class))).thenReturn(net.minecraft.world.level.material.Fluids.EMPTY.defaultFluidState());
        when(level.getFluidState(waterPos)).thenReturn(Blocks.WATER.defaultBlockState().getFluidState());

        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            // Verify Fluid.ANY is active by checking getFluidShape on water's fluid state
            assertFalse(ctx.getFluidShape(Blocks.WATER.defaultBlockState().getFluidState(), level, waterPos).isEmpty(),
                    "ClipContext must use ClipContext.Fluid.ANY so water surfaces are hit");
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - (-8.0)) < 1.0) {
                return new BlockHitResult(waterHit, Direction.UP, waterPos, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.EAST, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 9, 90.0f, 64.0f);

        assertEquals(HorizonSurveyScanner.FEATURE_WATER, sector.getFeatureType());
        assertEquals("minecraft:water", sector.getHitBlockId());
        assertEquals((float) eyePos.distanceTo(waterHit), sector.getHitDistance(), 0.01f);
        assertFalse(sector.getOccluded());
    }

    @Test
    @DisplayName("Ice beside open water returns ICE, not WATER")
    void testIceOutranksWater() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos waterPos = new BlockPos(-10, 62, 0);
        BlockPos icePos = new BlockPos(-20, 62, 0);
        when(level.getBlockState(waterPos)).thenReturn(Blocks.WATER.defaultBlockState());
        when(level.getBlockState(icePos)).thenReturn(Blocks.ICE.defaultBlockState());
        when(level.getFluidState(any(BlockPos.class))).thenReturn(net.minecraft.world.level.material.Fluids.EMPTY.defaultFluidState());
        when(level.getFluidState(waterPos)).thenReturn(Blocks.WATER.defaultBlockState().getFluidState());
        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - (-18.0)) < 1.0) {
                return new BlockHitResult(new Vec3(-10.0, 62.8, 0.5), Direction.UP, waterPos, false);
            }
            if (Math.abs(pitch - (-8.0)) < 1.0) {
                return new BlockHitResult(new Vec3(-20.0, 63.0, 0.5), Direction.UP, icePos, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.EAST, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 9, 90.0f, 64.0f);

        assertEquals(HorizonSurveyScanner.FEATURE_ICE, sector.getFeatureType());
        assertEquals("minecraft:ice", sector.getHitBlockId());
    }

    @Test
    @DisplayName("Distant stone and coal_ore cliff returns STONE_OUTCROP")
    void testStoneAndOreOutcrop() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos stonePos = new BlockPos(24, 65, 0);
        BlockPos orePos = new BlockPos(24, 69, 0);
        when(level.getBlockState(stonePos)).thenReturn(Blocks.STONE.defaultBlockState());
        when(level.getBlockState(orePos)).thenReturn(Blocks.COAL_ORE.defaultBlockState());

        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - 10.0) < 1.0) {
                return new BlockHitResult(new Vec3(24.0, 69.8, 0.5), Direction.WEST, orePos, false);
            }
            if (Math.abs(pitch - 0.0) < 1.0) {
                return new BlockHitResult(new Vec3(24.0, 65.62, 0.5), Direction.WEST, stonePos, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.WEST, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 27, 270.0f, 64.0f);

        assertEquals(HorizonSurveyScanner.FEATURE_STONE_OUTCROP, sector.getFeatureType());
        assertEquals("minecraft:stone", sector.getHitBlockId());
        assertEquals(23.5f, sector.getHitDistance(), 0.1f);
        assertFalse(sector.getOccluded());
    }

    @Test
    @DisplayName("Village structure blocks take priority as STRUCTURE")
    void testStructurePriority() {
        Vec3 eyePos = new Vec3(0.5, 65.62, 0.5);
        AgentEntity agent = mockAgentAt(eyePos);
        Level level = mock(ServerLevel.class);

        BlockPos planksPos = new BlockPos(0, 65, -30);
        BlockPos leavesPos = new BlockPos(0, 72, -35);
        when(level.getBlockState(planksPos)).thenReturn(Blocks.OAK_PLANKS.defaultBlockState());
        when(level.getBlockState(leavesPos)).thenReturn(Blocks.OAK_LEAVES.defaultBlockState());

        when(level.clip(any(ClipContext.class))).thenAnswer(inv -> {
            ClipContext ctx = inv.getArgument(0);
            double pitch = extractPitchDeg(ctx);
            if (Math.abs(pitch - 0.0) < 1.0) {
                return new BlockHitResult(new Vec3(0.5, 65.62, -30.0), Direction.SOUTH, planksPos, false);
            }
            if (Math.abs(pitch - 10.0) < 1.0) {
                return new BlockHitResult(new Vec3(0.5, 72.0, -35.0), Direction.SOUTH, leavesPos, false);
            }
            return BlockHitResult.miss(ctx.getTo(), Direction.NORTH, BlockPos.containing(ctx.getTo()));
        });

        HorizonSector sector = HorizonSurveyScanner.scanSector(level, agent, eyePos, 18, 180.0f, 64.0f);

        assertEquals(HorizonSurveyScanner.FEATURE_STRUCTURE, sector.getFeatureType());
        assertEquals("minecraft:oak_planks", sector.getHitBlockId());
        assertEquals(30.5f, sector.getHitDistance(), 0.1f);
    }
}
