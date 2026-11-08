package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import net.minecraft.core.BlockPos;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.Mockito.*;

public class AgentNavigatorTest {

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
    @DisplayName("IntentActuator actuateCraftItem handles null arguments safely")
    void testActuateCraftItemNullSafe() {
        IntentActuator actuator = new IntentActuator();
        assertFalse(actuator.actuateCraftItem(null, null));
        assertFalse(actuator.actuateCraftItem(null, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
    }
}
