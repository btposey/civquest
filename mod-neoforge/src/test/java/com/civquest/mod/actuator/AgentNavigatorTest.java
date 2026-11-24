package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import net.minecraft.core.BlockPos;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.Mockito.*;

public class AgentNavigatorTest {

    @BeforeAll
    static void initMinecraft() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    @Test
    @DisplayName("AgentNavigator triggers proactive 0.42 jump impulse on 1-block step-up")
    void testOneBlockStepUpJumpImpulse() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.0, 64.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Block at feet ahead (0, 64, 0) -> (1, 64, 0) or (0, 64, 0) is solid at y=64, open at y=65 and y=66
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos pos = inv.getArgument(0);
            if (pos.getY() == 64) {
                return solidBlock;
            }
            return airBlock;
        });

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(10.0, 64.0, 0.0, 1.0f, false, false, false, 1);
        navigator.tick();

        assertNotNull(appliedDelta.get());
        assertEquals(0.42, appliedDelta.get().y, 0.001, "1-block ledge ahead must trigger 0.42 jump impulse");
        assertTrue(Math.hypot(appliedDelta.get().x, appliedDelta.get().z) < 0.15, "Acceleration must be smooth, not an instant full-speed jump");
    }

    @Test
    @DisplayName("Yaw is rate limited per tick and velocity ramps smoothly while walking by default")
    void testYawRateLimitAndSmoothWalk() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.0, 64.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());
        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv ->
                ((BlockPos) inv.getArgument(0)).getY() < 64 ? solidBlock : airBlock);

        java.util.List<Float> yaws = new java.util.ArrayList<>();
        doAnswer(inv -> {
            yaws.add(inv.getArgument(0));
            return null;
        }).when(agent).setYRot(anyFloat());
        java.util.List<Vec3> deltas = new java.util.ArrayList<>();
        doAnswer(inv -> {
            deltas.add(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(30.0, 64.0, 0.0, 1.0f, false, false, false, 1);
        for (int i = 0; i < 30; i++) {
            navigator.tick();
        }
        float prev = 0.0f;
        for (float y : yaws) {
            assertTrue(Math.abs(y - prev) <= AgentNavigator.MAX_YAW_STEP + 0.01f, "per-tick yaw step must be limited");
            prev = y;
        }
        double maxSpeed = 0;
        double prevSpeed = 0;
        for (Vec3 d : deltas) {
            double sp = Math.sqrt(d.x * d.x + d.z * d.z);
            assertTrue(Math.abs(sp - prevSpeed) <= AgentNavigator.DECEL + 1e-6, "horizontal speed must change gradually");
            prevSpeed = sp;
            maxSpeed = Math.max(maxSpeed, sp);
        }
        assertTrue(maxSpeed <= AgentNavigator.WALK_SPEED + 1e-6, "default travel must be walking speed");
        assertTrue(navigator.getStamina() <= AgentNavigator.STAMINA_MAX);
    }

    @Test
    @DisplayName("Target behind agent triggers zero forward throttle to pivot in place rather than orbiting")
    void testZeroThrottleWhenTargetBehindPreventsOrbiting() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.0, 64.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.getYRot()).thenReturn(0.0f); // Facing South (+Z)
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());
        when(level.getBlockState(any(BlockPos.class))).thenReturn(airBlock);

        AtomicReference<Vec3> lastDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            lastDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        // Target is directly behind the agent (North = -Z, hx=0, hz=-10) -> desiredYaw = -180 deg
        navigator.navigate(0.0, 64.0, -10.0, 1.0f, false, false, false, 1);
        navigator.tick();

        // Since yaw difference is 180 deg (> 90 deg), target is behind: throttle must be 0
        assertNotNull(lastDelta.get());
        assertEquals(0.0, Math.hypot(lastDelta.get().x, lastDelta.get().z), 1e-6,
                "Forward throttle must be 0 when target is behind agent (>90 deg yaw diff)");
    }

    @Test
    @DisplayName("Sprinting drains stamina, exhaustion forces walking until recovery at 30")
    void testStaminaExhaustion() {
        AgentNavigator navigator = new AgentNavigator();
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(1000.0, 64.0, 0.0, 1.0f, true, false, false, 1);
        boolean sawExhausted = false;
        for (int i = 0; i < 400 && !sawExhausted; i++) {
            navigator.tick();
            sawExhausted = navigator.isExhausted();
        }
        assertTrue(sawExhausted, "stamina must eventually run out while sprinting");
        assertTrue(navigator.getStamina() < 1.0);

        // While exhausted and below 30 stamina, stamina regenerates at 0.3/tick and stays locked out until >= 30
        for (int i = 0; i < 50; i++) {
            navigator.tick();
        }
        assertTrue(navigator.isExhausted(), "must remain exhausted while stamina < 30");
        assertTrue(navigator.getStamina() > 10.0 && navigator.getStamina() < 30.0);

        for (int i = 0; i < 60 && navigator.isExhausted(); i++) {
            navigator.tick();
        }
        assertFalse(navigator.isExhausted(), "exhaustion must clear once stamina recovers to >= 30");
        assertTrue(navigator.getStamina() >= AgentNavigator.STAMINA_RECOVER_AT - AgentNavigator.STAMINA_DRAIN);
    }

    @Test
    @DisplayName("Motion integration trace writes per-tick JSONL pose records to XDG_STATE_HOME")
    void testMotionIntegrationJsonlTraceOutput() throws Exception {
        String xdgState = System.getenv("XDG_STATE_HOME");
        java.nio.file.Path base = (xdgState != null && !xdgState.isBlank())
                ? java.nio.file.Paths.get(xdgState)
                : java.nio.file.Paths.get(System.getProperty("user.home", "."), ".local", "state");
        java.nio.file.Path tracePath = base.resolve(java.nio.file.Paths.get("civquest", "logs", "motion-integration-trace.jsonl"));
        java.nio.file.Files.createDirectories(tracePath.getParent());

        AgentNavigator navigator = new AgentNavigator();
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(25.0, 65.0, -15.0, 1.0f, false, false, false, 1);

        StringBuilder sb = new StringBuilder();
        float prevHead = navigator.getHeadYaw();
        float prevPitch = navigator.getNavPitch();
        for (int tick = 0; tick < 30; tick++) {
            Vec3 before = navigator.getPosition();
            navigator.tick();
            Vec3 after = navigator.getPosition();
            double vx = after.x - before.x;
            double vz = after.z - before.z;
            float head = navigator.getHeadYaw();
            float body = navigator.getBodyYaw();
            float pitch = navigator.getNavPitch();
            assertTrue(Math.abs(LookSmoothing.wrap(head - prevHead)) <= AgentNavigator.MAX_YAW_STEP + 0.01f);
            assertTrue(Math.abs(pitch - prevPitch) <= AgentNavigator.MAX_PITCH_STEP + 0.01f);
            assertTrue(Math.abs(LookSmoothing.wrap(body - head)) <= AgentNavigator.MAX_HEAD_BODY_DIFF + 0.01f);
            prevHead = head;
            prevPitch = pitch;

            sb.append(String.format(java.util.Locale.ROOT,
                    "{\"scenario\":\"unit_walk_turn\",\"tick\":%d,\"x\":%.4f,\"y\":%.4f,\"z\":%.4f,\"vx\":%.4f,\"vz\":%.4f,\"yaw\":%.2f,\"head_yaw\":%.2f,\"body_yaw\":%.2f,\"pitch\":%.2f,\"stamina\":%.2f,\"nav_state\":\"%s\"}%n",
                    tick, after.x, after.y, after.z, vx, vz, head, head, body, pitch,
                    navigator.getStamina(), navigator.getReportedNavigationState().name()));
        }

        java.nio.file.Files.writeString(tracePath, sb.toString(), java.nio.charset.StandardCharsets.UTF_8,
                java.nio.file.StandardOpenOption.CREATE, java.nio.file.StandardOpenOption.APPEND);
        assertTrue(java.nio.file.Files.exists(tracePath), "motion-integration-trace.jsonl must exist");
        assertTrue(java.nio.file.Files.size(tracePath) > 0, "motion-integration-trace.jsonl must be non-empty");
    }

    @Test
    @DisplayName("Enclosed pit escape equips best tool and breaks upper wall block first to create a 1-block step-up")
    void testEnclosedPitDigsStepUpWithBestTool() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        BlockBreaker breaker = new BlockBreaker(agent);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 62.0, 0.5));
        when(agent.getX()).thenReturn(0.5);
        when(agent.getY()).thenReturn(62.0);
        when(agent.getZ()).thenReturn(0.5);
        when(agent.getEyeY()).thenReturn(63.62);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.getBreaker()).thenReturn(breaker);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(level.getMinBuildHeight()).thenReturn(-64);

        BlockState solidDirt = mock(BlockState.class);
        when(solidDirt.isAir()).thenReturn(false);
        when(solidDirt.getDestroySpeed(any(), any())).thenReturn(0.5f);
        when(solidDirt.getDestroyProgress(any(), any(), any())).thenReturn(0.25f);
        when(solidDirt.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // 1x1x2-deep pit at (0, 62, 0): floor at y<=61, walls at y=62 and y=63 around (0, *, 0)
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getY() <= 61) {
                return solidDirt;
            }
            if (p.getX() == 0 && p.getZ() == 0) {
                return airBlock;
            }
            if (p.getY() <= 63) {
                return solidDirt;
            }
            return airBlock;
        });

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(10.5, 64.0, 0.5, 1.0f, false, true, false, 1);
        navigator.tick();

        assertTrue(navigator.isEnclosed(), "1x1x2 pit must be detected as enclosed");
        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getReportedNavigationState(),
                "Enclosed navigator must report STUCK to telemetry");
        verify(agent, atLeastOnce()).equipBestToolFor(solidDirt);
        // Must break head-height block (1, 63, 0) so (1, 62, 0) remains intact as a 1-block step-up
        assertTrue(breaker.isBreaking());
        assertEquals(new BlockPos(1, 63, 0), breaker.getPos());
    }

    @Test
    @DisplayName("AgentNavigator transitions to NAVIGATION_STATE_STUCK after 16 ticks of no horizontal progress")
    void testFastStuckTransitionAt16Ticks() {
        AgentNavigator navigator = new AgentNavigator();
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(50.0, 64.0, 50.0, 1.0f, false, false, false, 1);

        for (int i = 0; i < 60; i++) {
            navigator.tick();
            navigator.setPosition(0.0, 64.0, 0.0);
        }

        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getNavigationState());
        assertTrue(navigator.isStuck());
    }

    @Test
    @DisplayName("AgentNavigator engages sneaking, zeroes forward velocity, suppresses jump impulse, and reports STUCK on 4+ block drop-off")
    void testCliffEdgeDropAvoidanceAndSneaking() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.0, 64.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.getYRot()).thenReturn(0.0f); // Facing South (+Z)
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Cliff ahead: all blocks at and below probe location down to y=60 are air
        when(level.getBlockState(any(BlockPos.class))).thenReturn(airBlock);

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        // Direct navigation toward (0, 66, 10) with dy > 0.5 (would jump into air if unprotected)
        navigator.navigate(0.0, 66.0, 10.0, 1.0f, false, false, false, 1);
        navigator.tick();

        assertNotNull(appliedDelta.get());
        assertEquals(0.0, Math.hypot(appliedDelta.get().x, appliedDelta.get().z), 1e-6,
                "Forward velocity toward cliff edge must be 0");
        assertTrue(appliedDelta.get().y < 0.42, "Jump impulse into mid-air must be suppressed");
        verify(agent, atLeastOnce()).setShiftKeyDown(true);
        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getNavigationState(),
                "NavigationState must be STUCK when facing 4+ block drop-off");
        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getReportedNavigationState(),
                "Reported navigation state must be STUCK when facing 4+ block drop-off");
        assertTrue(navigator.isStuck(), "navigator.isStuck() must return true at cliff edge");
    }

    @Test
    @DisplayName("Approaching and descending a ladder/vine shaft avoids cliff freeze, clears sneak, and slides down smoothly")
    void testLadderAndVineDescentDoesNotTriggerCliffOrSneak() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        AtomicReference<Vec3> posRef = new AtomicReference<>(new Vec3(0.5, 70.0, 0.5));
        when(agent.position()).thenAnswer(inv -> posRef.get());
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(false);
        when(agent.onClimbable()).thenReturn(true);
        when(agent.getYRot()).thenReturn(0.0f);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        BlockState ladderBlock = mock(BlockState.class);
        when(ladderBlock.isAir()).thenReturn(false);
        when(ladderBlock.is(net.minecraft.tags.BlockTags.CLIMBABLE)).thenReturn(true);
        when(ladderBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState solidFloor = mock(BlockState.class);
        when(solidFloor.isAir()).thenReturn(false);
        when(solidFloor.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        // Ladder shaft at (0, 64..70, 0) with floor at y=63
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getY() <= 63) {
                return solidFloor;
            }
            if (p.getX() == 0 && p.getZ() == 0 && p.getY() <= 70) {
                return ladderBlock;
            }
            return airBlock;
        });

        AtomicReference<Vec3> lastDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            lastDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));
        doAnswer(inv -> {
            Vec3 move = inv.getArgument(1);
            posRef.set(posRef.get().add(move));
            return null;
        }).when(agent).move(any(), any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(0.5, 64.0, 0.5, 1.0f, false, false, false, 1);

        // Run 25 ticks (more than the 16-tick stuck threshold) to ensure vertical climbing progress resets stuckTicks
        for (int i = 0; i < 25; i++) {
            navigator.tick();
        }

        assertFalse(navigator.isCliffDetected(), "Ladder/vine shaft must never trigger cliffDetected");
        assertNotEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getNavigationState(),
                "Descending a climbable shaft must not enter STUCK state");
        assertNotEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getReportedNavigationState());
        verify(agent, never()).setShiftKeyDown(true);
        verify(agent, atLeastOnce()).setShiftKeyDown(false);
        assertEquals(-0.15, lastDelta.get().y, 1e-6, "Descending on climbable must use -0.15 controlled slide vy");
        assertTrue(posRef.get().y < 67.0, "Agent must descend smoothly down the shaft");
    }

    @Test
    @DisplayName("Approaching a vine drop shaft from a ledge does not trigger cliffDetected")
    void testApproachingVineShaftFromLedgeNotFlaggedAsCliff() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 70.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.onClimbable()).thenReturn(false);
        when(agent.getYRot()).thenReturn(0.0f); // Facing +Z
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        BlockState vineBlock = mock(BlockState.class);
        when(vineBlock.isAir()).thenReturn(false);
        when(vineBlock.is(net.minecraft.tags.BlockTags.CLIMBABLE)).thenReturn(true);
        when(vineBlock.is(net.minecraft.world.level.block.Blocks.VINE)).thenReturn(true);
        when(vineBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Vine shaft ahead along +Z at z=1 (y=64..69); no solid ground down to y=66
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getZ() == 1 && p.getY() >= 64 && p.getY() <= 69) {
                return vineBlock;
            }
            return airBlock;
        });

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(0.5, 64.0, 1.5, 1.0f, false, false, false, 1);
        navigator.tick();

        assertFalse(navigator.isCliffDetected(), "Shaft containing vines must not trigger cliffDetected");
        verify(agent, never()).setShiftKeyDown(true);
    }

    @Test
    @DisplayName("Ascending a ladder/vine sets upward climb velocity vy = 0.20 without sneak lock")
    void testLadderAscentSetsUpwardVelocity() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 64.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.onClimbable()).thenReturn(true);
        when(agent.getYRot()).thenReturn(0.0f);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState ladderBlock = mock(BlockState.class);
        when(ladderBlock.isAir()).thenReturn(false);
        when(ladderBlock.is(net.minecraft.tags.BlockTags.CLIMBABLE)).thenReturn(true);
        when(ladderBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());
        when(level.getBlockState(any(BlockPos.class))).thenReturn(ladderBlock);

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(0.5, 72.0, 0.5, 1.0f, false, false, false, 1);
        navigator.tick();

        assertFalse(navigator.isCliffDetected());
        verify(agent, never()).setShiftKeyDown(true);
        verify(agent, atLeastOnce()).setShiftKeyDown(false);
        assertEquals(0.20, appliedDelta.get().y, 1e-6, "Ascending on climbable must use 0.20 upward vy");
    }

    @Test
    @DisplayName("In-water navigation applies upward swim buoyancy and prioritizes surfacing when air is low")
    void testInWaterUpwardSwimAndBuoyancy() {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.level()).thenReturn(null);
        when(agent.getYRot()).thenReturn(0.0f);
        when(agent.isInWater()).thenReturn(true);
        when(agent.onGround()).thenReturn(false);

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        // Case 1: Deep underwater (Y=39) with downward momentum (-0.3) swimming up to Y=41 -> vy = 0.15
        when(agent.position()).thenReturn(new Vec3(0.5, 39.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(new Vec3(0.0, -0.3, 0.0));
        when(agent.getAirSupply()).thenReturn(300);
        when(agent.isEyeInFluid(net.minecraft.tags.FluidTags.WATER)).thenReturn(false);
        AgentNavigator nav = new AgentNavigator(agent);
        nav.navigate(5.5, 41.0, 0.5, 1.0f, false, false, false, 1);
        nav.tick();
        assertEquals(0.15, appliedDelta.get().y, 1e-6, "Swimming toward higher Y must set upward vy = 0.15");
        assertFalse(nav.isCliffDetected(), "In-water swimming must not trigger cliffDetected");

        // Case 2: At water surface (target near current Y) -> surface buoyancy vy = 0.08
        when(agent.position()).thenReturn(new Vec3(0.5, 63.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(new Vec3(0.0, -0.2, 0.0));
        nav.navigate(5.5, 63.0, 0.5, 1.0f, false, false, false, 1);
        nav.tick();
        assertEquals(0.08, appliedDelta.get().y, 1e-6, "Surface swimming must apply upward buoyancy >= 0.08");

        // Case 3: Eyes submerged in water -> buoyancy >= 0.08 even when target is below
        when(agent.position()).thenReturn(new Vec3(0.5, 50.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.isEyeInFluid(net.minecraft.tags.FluidTags.WATER)).thenReturn(true);
        nav.navigate(5.5, 48.0, 0.5, 1.0f, false, false, false, 1);
        nav.tick();
        assertTrue(appliedDelta.get().y >= 0.08 - 1e-6, "Submerged eyes must trigger upward buoyancy");

        // Case 4: Low air supply (< 200) -> prioritize upward surfacing (>= 0.15)
        when(agent.position()).thenReturn(new Vec3(0.5, 35.0, 0.5));
        when(agent.isEyeInFluid(net.minecraft.tags.FluidTags.WATER)).thenReturn(false);
        when(agent.getAirSupply()).thenReturn(150);
        nav.navigate(5.5, 33.0, 0.5, 1.0f, false, false, false, 1);
        nav.tick();
        assertTrue(appliedDelta.get().y >= 0.15 - 1e-6, "Low air supply (<200) must prioritize upward surfacing");
    }

    @Test
    @DisplayName("Shallow water is neither a hazard nor treated as a cliff")
    void testShallowWaterNotHazardOrCliff() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 64.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.isInWater()).thenReturn(false);
        when(agent.getYRot()).thenReturn(-90.0f); // Facing +X
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        net.minecraft.world.level.material.FluidState waterFluid = mock(net.minecraft.world.level.material.FluidState.class);
        when(waterFluid.is(net.minecraft.tags.FluidTags.WATER)).thenReturn(true);

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState waterBlock = mock(BlockState.class);
        when(waterBlock.isAir()).thenReturn(false);
        when(waterBlock.getFluidState()).thenReturn(waterFluid);
        when(waterBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Shallow 1-block water at x=1, y=63 with solid floor at y=62; solid ground at x=0, y<=63
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getX() >= 1 && p.getX() <= 2 && p.getY() == 63) {
                return waterBlock;
            }
            if (p.getX() >= 1 && p.getX() <= 2 && p.getY() <= 62) {
                return solidBlock;
            }
            if (p.getY() <= 63) {
                return solidBlock;
            }
            return airBlock;
        });

        assertFalse(AgentNavigator.isBlockHazard(level, new BlockPos(1, 63, 0)),
                "Shallow water must not be treated as a hazard");

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(5.5, 64.0, 0.5, 1.0f, false, false, false, 1);
        navigator.tick();

        assertFalse(navigator.isCliffDetected(), "Shallow water ahead must not trigger cliffDetected");
        verify(agent, never()).setShiftKeyDown(true);
    }

    @Test
    @DisplayName("IntentActuator actuateCraftItem handles null arguments safely")
    void testActuateCraftItemNullSafe() {
        IntentActuator actuator = new IntentActuator();
        assertFalse(actuator.actuateCraftItem(null, null));
        assertFalse(actuator.actuateCraftItem(null, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
    }

    @Test
    @DisplayName("isPassableDoor correctly classifies wooden doors, trapdoors, fence gates vs iron doors")
    void testIsPassableDoorClassifications() {
        assertFalse(AgentNavigator.isPassableDoor(null));

        BlockState oakDoor = mock(BlockState.class);
        when(oakDoor.is(net.minecraft.tags.BlockTags.WOODEN_DOORS)).thenReturn(true);
        assertTrue(AgentNavigator.isPassableDoor(oakDoor));

        BlockState oakTrapDoor = mock(BlockState.class);
        when(oakTrapDoor.is(net.minecraft.tags.BlockTags.WOODEN_TRAPDOORS)).thenReturn(true);
        assertTrue(AgentNavigator.isPassableDoor(oakTrapDoor));

        BlockState fenceGate = mock(BlockState.class);
        when(fenceGate.is(net.minecraft.tags.BlockTags.FENCE_GATES)).thenReturn(true);
        assertTrue(AgentNavigator.isPassableDoor(fenceGate));

        BlockState ironDoor = mock(BlockState.class);
        when(ironDoor.is(net.minecraft.world.level.block.Blocks.IRON_DOOR)).thenReturn(true);
        assertFalse(AgentNavigator.isPassableDoor(ironDoor));

        BlockState ironTrapdoor = mock(BlockState.class);
        when(ironTrapdoor.is(net.minecraft.world.level.block.Blocks.IRON_TRAPDOOR)).thenReturn(true);
        assertFalse(AgentNavigator.isPassableDoor(ironTrapdoor));

        BlockState stone = mock(BlockState.class);
        when(stone.getBlock()).thenReturn(net.minecraft.world.level.block.Blocks.STONE);
        assertFalse(AgentNavigator.isPassableDoor(stone));
    }

    @Test
    @DisplayName("isClimbableState rejects wall vines unless target is ascending and block above is air")
    void testIsClimbableStateWallVines() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 64.0, 0.5));
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState vine = mock(BlockState.class);
        when(vine.is(net.minecraft.world.level.block.Blocks.VINE)).thenReturn(true);
        when(vine.isAir()).thenReturn(false);

        BlockState air = mock(BlockState.class);
        when(air.isAir()).thenReturn(true);
        when(air.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        BlockState solid = mock(BlockState.class);
        when(solid.isAir()).thenReturn(false);
        when(solid.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockPos vinePos = new BlockPos(0, 64, 0);

        AgentNavigator nav = new AgentNavigator(agent);

        // 1. Horizontal movement: target at y=64 (not ascending) -> returns false
        nav.navigate(10.0, 64.0, 0.5, 1.0f, false, false, false, 1);
        when(level.getBlockState(vinePos.above())).thenReturn(air);
        assertFalse(nav.isClimbableState(vine, level, vinePos), "Horizontal movement must not treat wall vines as climbable");

        // 2. Ascending movement: target at y=70, block above is air -> returns true
        nav.navigate(0.5, 70.0, 0.5, 1.0f, false, false, false, 1);
        when(level.getBlockState(vinePos.above())).thenReturn(air);
        assertTrue(nav.isClimbableState(vine, level, vinePos), "Ascending into open shaft must treat vines as climbable");

        // 3. Ascending movement: target at y=70, but block directly above is solid ceiling -> returns false
        when(level.getBlockState(vinePos.above())).thenReturn(solid);
        assertFalse(nav.isClimbableState(vine, level, vinePos), "Ascending into closed ceiling must reject vines as climbable");
    }

    @Test
    @DisplayName("Horizontal collision jumping is suppressed when headroom is less than 2.2m")
    void testJumpSuppressedUnderLowHeadroom() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.0, 64.0, 0.0));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        agent.horizontalCollision = true;

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Low ceiling at y=66 (2.0m above feet at 64.0 -> headroom < 2.2m)
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos pos = inv.getArgument(0);
            if (pos.getY() == 66) {
                return solidBlock;
            }
            return airBlock;
        });

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(10.0, 64.0, 0.0, 1.0f, false, false, false, 1);
        navigator.tick();

        // Must NOT jump (vy must not be 0.42)
        assertNotEquals(0.42, appliedDelta.get().y, 1e-4,
                "Jumping must be suppressed when headroom is less than 2.2m");
    }

    @Test
    @DisplayName("Unreachable distant target with empty path and zero moveSpeed does not trigger in-place jumping despite dy > 1.0")
    void testNoInPlaceJumpForUnreachableDistantTarget() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 64.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.getYRot()).thenReturn(-90.0f); // Facing +X directly toward target
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Flat ground at y <= 63: target at (20.5, 72.0, 0.5) cannot make vertical progress within local radius -> path.isEmpty(), moveSpeed == 0.0
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos pos = inv.getArgument(0);
            return pos.getY() <= 63 ? solidBlock : airBlock;
        });

        java.util.List<Vec3> deltas = new java.util.ArrayList<>();
        doAnswer(inv -> {
            deltas.add(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        AgentNavigator navigator = new AgentNavigator(agent);
        navigator.navigate(20.5, 72.0, 0.5, 1.0f, false, false, false, 1);

        // 1. Multiple ticks on ground with dy = 8.0 > 1.0 -> zero horizontal speed and never vy = 0.42
        for (int i = 0; i < 5; i++) {
            navigator.tick();
        }
        // 2. Also verify horizontalCollision while path.isEmpty() and moveSpeed == 0.0 never triggers a jump
        agent.horizontalCollision = true;
        navigator.tick();

        assertEquals(6, deltas.size());
        for (Vec3 delta : deltas) {
            assertEquals(0.0, Math.hypot(delta.x, delta.z), 1e-6,
                    "Distant unreachable target with empty path must keep horizontal velocity at 0.0");
            assertNotEquals(0.42, delta.y, 1e-4,
                    "Agent must not jump in place when path is empty and moveSpeed is 0.0");
            assertTrue(delta.y <= 0.0, "Vertical velocity must remain non-positive on ground without jump");
        }
    }

    @Test
    @DisplayName("Jumping occurs only when actively moving forward (moveSpeed > 0.05 and align > 0.15) with stepUpOneBlock")
    void testJumpRequiresActiveForwardMovementWithStepUp() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new Vec3(0.5, 64.0, 0.5));
        when(agent.getDeltaMovement()).thenReturn(Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState solidBlock = mock(BlockState.class);
        when(solidBlock.isAir()).thenReturn(false);
        when(solidBlock.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);
        when(airBlock.getCollisionShape(any(), any())).thenReturn(Shapes.empty());

        // Floor at y <= 63 everywhere, plus a 1-block step-up starting at x >= 1 (y == 64 solid)
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos pos = inv.getArgument(0);
            if (pos.getY() <= 63 || (pos.getY() == 64 && pos.getX() >= 1)) {
                return solidBlock;
            }
            return airBlock;
        });

        AtomicReference<Vec3> appliedDelta = new AtomicReference<>(Vec3.ZERO);
        doAnswer(inv -> {
            appliedDelta.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));

        // Case 1: Facing away from step-up (+X) -> align == 0.0 (<= 0.15), must NOT jump
        when(agent.getYRot()).thenReturn(90.0f); // Facing -X (180 deg away from +X)
        AgentNavigator navAway = new AgentNavigator(agent);
        navAway.navigate(10.5, 65.0, 0.5, 1.0f, false, false, false, 1);
        navAway.tick();
        assertNotEquals(0.42, appliedDelta.get().y, 1e-4,
                "Must not jump when facing away from step-up (align <= 0.15)");

        // Case 2: Facing perpendicular (+Z = 0 deg) -> align == 0.15 (not > 0.15), must NOT jump on tick 1
        when(agent.getYRot()).thenReturn(0.0f);
        AgentNavigator navPerp = new AgentNavigator(agent);
        navPerp.navigate(10.5, 65.0, 0.5, 1.0f, false, false, false, 1);
        navPerp.tick();
        assertNotEquals(0.42, appliedDelta.get().y, 1e-4,
                "Must not jump when heading alignment is at or below 0.15 floor");

        // Case 3: Facing directly toward 1-block step-up (-90 deg = +X), but moveSpeed <= 0.05
        // (within arrivalRange 1.5 at horizontalDist=1.05, speedMultiplier=0.5 -> moveSpeed=0.049 <= 0.05, unreachable y=70 -> path.isEmpty())
        when(agent.getYRot()).thenReturn(-90.0f);
        AgentNavigator navSlow = new AgentNavigator(agent);
        navSlow.navigate(1.55, 70.0, 0.5, 0.5f, false, false, false, 1);
        navSlow.tick();
        assertNotEquals(0.42, appliedDelta.get().y, 1e-4,
                "Must not jump when moveSpeed <= 0.05 even with stepUpOneBlock ahead");

        // Case 4: Actively moving forward (moveSpeed == 0.14 > 0.05, align == 1.0 > 0.15) with stepUpOneBlock -> jumps!
        AgentNavigator navForward = new AgentNavigator(agent);
        navForward.navigate(10.5, 65.0, 0.5, 1.0f, false, false, false, 1);
        navForward.tick();
        assertEquals(0.42, appliedDelta.get().y, 1e-4,
                "Actively moving forward with stepUpOneBlock must trigger 0.42 jump impulse");
        assertTrue(Math.hypot(appliedDelta.get().x, appliedDelta.get().z) > 0.0,
                "Active jump must be accompanied by forward horizontal acceleration");
    }
}
