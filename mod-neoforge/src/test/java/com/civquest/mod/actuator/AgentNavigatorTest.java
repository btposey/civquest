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
        assertTrue(appliedDelta.get().x > 0.1, "Agent must continue moving forward over 1-block ledge");
    }

    @Test
    @DisplayName("AgentNavigator deflects away from >=2-block barrier and shifts target after 6 stuck ticks")
    void testTwoBlockBarrierDeflectionAndDetour() {
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

        // 2-block wall ahead along +X (x >= 0, z == 0, y in [64, 65]), while lateral +Z / -Z is open air
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos pos = inv.getArgument(0);
            if (pos.getZ() == 0 && (pos.getY() == 64 || pos.getY() == 65)) {
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
        navigator.setStuckTicks(6);
        navigator.tick();

        assertNotNull(appliedDelta.get());
        // Instead of moving strictly along +X into the wall (z == 0), velocity must deflect along Z
        assertTrue(Math.abs(appliedDelta.get().z) > 0.1, "Agent must deflect laterally around >=2-block barrier");
        assertNotEquals(0.0, navigator.getTargetCoords().z, "Target coordinates must shift toward open detour when stuckTicks >= 6");
    }

    @Test
    @DisplayName("AgentNavigator transitions to NAVIGATION_STATE_STUCK after 16 ticks of no horizontal progress")
    void testFastStuckTransitionAt16Ticks() {
        AgentNavigator navigator = new AgentNavigator();
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(50.0, 64.0, 50.0, 1.0f, false, false, false, 1);

        for (int i = 0; i < 17; i++) {
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
