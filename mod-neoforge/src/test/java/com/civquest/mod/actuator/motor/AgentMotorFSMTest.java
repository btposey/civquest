package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import net.minecraft.core.BlockPos;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.world.entity.projectile.Arrow;
import net.minecraft.world.phys.AABB;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.*;

public class AgentMotorFSMTest {

    private AgentMotorFSM motorFSM;
    private IntentActuator actuator;

    @BeforeEach
    void setUp() {
        motorFSM = new AgentMotorFSM(null);
        actuator = new IntentActuator();
    }

    @Test
    @DisplayName("MotorFSM initializes in IDLE state with 0 ticks")
    void testInitialState() {
        assertEquals(MotorState.IDLE, motorFSM.getState());
        assertEquals(MotorState.IDLE, motorFSM.getPreviousState());
        assertEquals(0L, motorFSM.getTicksInState());
        assertNull(motorFSM.getLastNavTarget());
    }

    @Test
    @DisplayName("MotorFSM supports transitions across all MotorState values")
    void testAllStateTransitions() {
        for (MotorState state : MotorState.values()) {
            boolean transitioned = motorFSM.transitionTo(state, "test_transition_" + state.name());
            if (state == MotorState.IDLE) {
                // If it was already IDLE
                assertFalse(transitioned);
            } else {
                assertTrue(transitioned);
                assertEquals(state, motorFSM.getState());
                assertEquals(0L, motorFSM.getTicksInState());
            }
        }
    }

    @Test
    @DisplayName("MotorFSM rejects transition to the identical active state or null")
    void testIdenticalOrNullTransitions() {
        motorFSM.transitionTo(MotorState.MOVING, "start_moving");
        assertEquals(MotorState.MOVING, motorFSM.getState());

        assertFalse(motorFSM.transitionTo(MotorState.MOVING, "duplicate_moving"));
        assertFalse(motorFSM.transitionTo(null, "null_state"));
        assertEquals(MotorState.MOVING, motorFSM.getState());
    }

    @Test
    @DisplayName("MotorFSM entry and exit hooks reset counters and clear targets")
    void testEntryAndExitHooks() {
        motorFSM.setLastNavTarget(Vec3.newBuilder().setX(10).setY(64).setZ(10).build());
        motorFSM.setCombatTicks(25);
        motorFSM.setStuckRecoveryTicks(15);

        // Transition to COMBAT_ENGAGED resets combatTicks to 0
        motorFSM.transitionTo(MotorState.COMBAT_ENGAGED, "enter_combat");
        assertEquals(0, motorFSM.getCombatTicks());

        // Transition to STUCK_RECOVERY resets stuckRecoveryTicks to 0
        motorFSM.setStuckRecoveryTicks(10);
        motorFSM.transitionTo(MotorState.STUCK_RECOVERY, "enter_stuck_recovery");
        assertEquals(0, motorFSM.getStuckRecoveryTicks());

        // Transition to IDLE clears lastNavTarget
        motorFSM.setLastNavTarget(Vec3.newBuilder().setX(5).setY(64).setZ(5).build());
        motorFSM.transitionTo(MotorState.IDLE, "return_idle");
        assertNull(motorFSM.getLastNavTarget());
    }

    @Test
    @DisplayName("MotorFSM preemption matrix protects SLEEPING state from routine navigation and actions")
    void testSleepingPreemptionMatrix() {
        motorFSM.transitionTo(MotorState.SLEEPING, "agent_asleep");

        // Routine sprint navigation should NOT preempt sleep
        AgentIntent routineSprint = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(10).setY(64).setZ(10).build())
                        .setSprint(true)
                        .setSpeedMultiplier(1.5f)
                        .build())
                .build();
        assertFalse(motorFSM.canPreempt(routineSprint));

        // Routine mining/building should NOT preempt sleep
        AgentIntent routineBreak = AgentIntent.newBuilder()
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(1).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                        .build())
                .build();
        assertFalse(motorFSM.canPreempt(routineBreak));

        // Explicit wake-up interaction CAN preempt sleep
        AgentIntent wakeUp = AgentIntent.newBuilder()
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(1).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.WAKE_UP)
                        .build())
                .build();
        assertTrue(motorFSM.canPreempt(wakeUp));

        // Emergency attack/panic CAN preempt sleep
        AgentIntent emergencyAttack = AgentIntent.newBuilder()
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(42).build())
                .build();
        assertTrue(motorFSM.canPreempt(emergencyAttack));

        // Emergency eating/shielding CAN preempt sleep
        AgentIntent emergencyEat = AgentIntent.newBuilder()
                .setUseItem(UseItemIntent.newBuilder().setIsEatingOrDrinking(true).build())
                .build();
        assertTrue(motorFSM.canPreempt(emergencyEat));
    }

    @Test
    @DisplayName("MotorFSM preemption matrix protects COMBAT_ENGAGED state from routine intents")
    void testCombatPreemptionMatrix() {
        motorFSM.transitionTo(MotorState.COMBAT_ENGAGED, "in_combat");

        // Routine sprint navigation should NOT preempt active combat
        AgentIntent routineSprint = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(20).setY(64).setZ(20).build())
                        .setSprint(true)
                        .build())
                .build();
        assertFalse(motorFSM.canPreempt(routineSprint));

        // Routine building should NOT preempt combat
        AgentIntent build = AgentIntent.newBuilder()
                .setBuildBlock(BuildBlockIntent.newBuilder()
                        .setTargetVoxel(BlockPosProto.newBuilder().setX(5).setY(64).setZ(5).build())
                        .build())
                .build();
        assertFalse(motorFSM.canPreempt(build));

        // Emergency attack/flee CAN preempt or update combat
        AgentIntent attack = AgentIntent.newBuilder()
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(99).setFleeImmediately(true).build())
                .build();
        assertTrue(motorFSM.canPreempt(attack));

        // Emergency shield or food CAN preempt combat
        AgentIntent shield = AgentIntent.newBuilder()
                .setUseItem(UseItemIntent.newBuilder().setEquipArmorOrOffhand(true).build())
                .build();
        assertTrue(motorFSM.canPreempt(shield));
    }

    @Test
    @DisplayName("MotorFSM prevents duplicate navigation intents from resetting STUCK_RECOVERY")
    void testStuckRecoveryOscillationPrevention() {
        Vec3 target = Vec3.newBuilder().setX(100.0).setY(64.0).setZ(200.0).build();

        // Agent begins moving to target
        AgentIntent navIntent = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder().setTarget(target).build())
                .build();
        motorFSM.handleIntent(navIntent, actuator);
        assertEquals(MotorState.MOVING, motorFSM.getState());
        assertEquals(target, motorFSM.getLastNavTarget());

        // Agent gets stuck and enters STUCK_RECOVERY
        motorFSM.transitionTo(MotorState.STUCK_RECOVERY, "navigation_stuck");
        assertEquals(MotorState.STUCK_RECOVERY, motorFSM.getState());

        // Repeated identical navigation intent to same target is rejected from preempting STUCK_RECOVERY
        assertFalse(motorFSM.canPreempt(navIntent));

        // Different navigation destination is allowed to redirect and preempt STUCK_RECOVERY
        Vec3 altTarget = Vec3.newBuilder().setX(150.0).setY(64.0).setZ(250.0).build();
        AgentIntent altNavIntent = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder().setTarget(altTarget).build())
                .build();
        assertTrue(motorFSM.canPreempt(altNavIntent));

        // Emergency attack is allowed to preempt STUCK_RECOVERY
        AgentIntent attack = AgentIntent.newBuilder()
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(12).build())
                .build();
        assertTrue(motorFSM.canPreempt(attack));
    }

    @Test
    @DisplayName("MotorFSM tick advances combatTicks and disengages to IDLE after COMBAT_TIMEOUT_TICKS")
    void testCombatDisengagementTimeout() {
        motorFSM.transitionTo(MotorState.COMBAT_ENGAGED, "start_combat");
        assertEquals(0, motorFSM.getCombatTicks());

        for (int i = 0; i < AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            motorFSM.tick();
            assertEquals(MotorState.COMBAT_ENGAGED, motorFSM.getState());
        }

        // Ticking past COMBAT_TIMEOUT_TICKS transitions to IDLE
        motorFSM.tick();
        assertEquals(MotorState.IDLE, motorFSM.getState());
    }

    @Test
    @DisplayName("MotorFSM tick advances stuckRecoveryTicks and times out to IDLE after STUCK_RECOVERY_MAX_TICKS")
    void testStuckRecoveryTimeout() {
        motorFSM.transitionTo(MotorState.STUCK_RECOVERY, "start_stuck_recovery");
        assertEquals(0, motorFSM.getStuckRecoveryTicks());

        for (int i = 0; i < AgentMotorFSM.STUCK_RECOVERY_MAX_TICKS; i++) {
            motorFSM.tick();
            assertEquals(MotorState.STUCK_RECOVERY, motorFSM.getState());
        }

        // Ticking past STUCK_RECOVERY_MAX_TICKS transitions to IDLE
        motorFSM.tick();
        assertEquals(MotorState.IDLE, motorFSM.getState());
    }

    @Test
    @DisplayName("MotorFSM isSameNavigationTarget checks coordinate proximity correctly")
    void testIsSameNavigationTarget() {
        Vec3 base = Vec3.newBuilder().setX(10.0).setY(64.0).setZ(10.0).build();
        motorFSM.setLastNavTarget(base);

        NavigateIntent exact = NavigateIntent.newBuilder().setTarget(base).build();
        assertTrue(motorFSM.isSameNavigationTarget(exact));

        NavigateIntent close = NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder().setX(10.2).setY(64.1).setZ(10.1).build())
                .build();
        assertTrue(motorFSM.isSameNavigationTarget(close));

        NavigateIntent far = NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder().setX(15.0).setY(64.0).setZ(10.0).build())
                .build();
        assertFalse(motorFSM.isSameNavigationTarget(far));

        NavigateIntent noTarget = NavigateIntent.newBuilder().build();
        assertFalse(motorFSM.isSameNavigationTarget(noTarget));

        motorFSM.setLastNavTarget(null);
        assertTrue(motorFSM.isSameNavigationTarget(noTarget));
    }

    @Test
    @DisplayName("MotorPDA bounded LIFO stack enforces MAX_STACK_DEPTH = 4 and restores LIFO order")
    void testBoundedLifoStackDepthLimit() {
        motorFSM.transitionTo(MotorState.MINING, "start_mining");

        assertTrue(motorFSM.pushState(MotorState.MOVING, "push_1"));
        assertTrue(motorFSM.pushState(MotorState.BUILDING, "push_2"));
        assertTrue(motorFSM.pushState(MotorState.USING_ITEM, "push_3"));
        assertTrue(motorFSM.pushState(MotorState.COMBAT_ENGAGED, "push_4"));
        assertEquals(AgentMotorFSM.MAX_STACK_DEPTH, motorFSM.getStackDepth());
        assertEquals(MotorState.COMBAT_ENGAGED, motorFSM.getState());

        // 5th push must be rejected cleanly without overflowing stack
        assertFalse(motorFSM.pushState(MotorState.STUCK_RECOVERY, "push_overflow"));
        assertEquals(AgentMotorFSM.MAX_STACK_DEPTH, motorFSM.getStackDepth());
        assertEquals(MotorState.COMBAT_ENGAGED, motorFSM.getState());

        // LIFO pop order verification
        assertTrue(motorFSM.popState());
        assertEquals(MotorState.USING_ITEM, motorFSM.getState());
        assertEquals(3, motorFSM.getStackDepth());

        assertTrue(motorFSM.popState());
        assertEquals(MotorState.BUILDING, motorFSM.getState());
        assertEquals(2, motorFSM.getStackDepth());

        assertTrue(motorFSM.popState());
        assertEquals(MotorState.MOVING, motorFSM.getState());
        assertEquals(1, motorFSM.getStackDepth());

        assertTrue(motorFSM.popState());
        assertEquals(MotorState.MINING, motorFSM.getState());
        assertEquals(0, motorFSM.getStackDepth());

        // Popping an empty stack returns false cleanly
        assertFalse(motorFSM.popState());
    }

    @Test
    @DisplayName("MotorPDA pushState/popState preserves and resumes BlockBreaker and AgentNavigator with zero amnesia")
    void testPushPopResumesMiningAndNavigation() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        BlockBreaker mockBreaker = mock(BlockBreaker.class);
        AgentNavigator mockNavigator = mock(AgentNavigator.class);
        when(mockAgent.getAgentId()).thenReturn("pda-agent");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.getBreaker()).thenReturn(mockBreaker);
        when(mockAgent.getNavigator()).thenReturn(mockNavigator);

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);

        // 1. Mining suspension and resumption
        BlockPos minePos = new BlockPos(10, 64, -5);
        when(mockBreaker.isBreaking()).thenReturn(true);
        when(mockBreaker.getPos()).thenReturn(minePos);
        when(mockBreaker.getProgress()).thenReturn(0.65f);
        when(mockBreaker.getTicks()).thenReturn(13);
        when(mockBreaker.resume(minePos, 0.65f, 13)).thenReturn(true);

        fsm.transitionTo(MotorState.MINING, "start_mine");
        assertTrue(fsm.pushState(MotorState.COMBAT_ENGAGED, "reflex_interrupt"));
        assertEquals(MotorState.COMBAT_ENGAGED, fsm.getState());
        assertEquals(1, fsm.getStackDepth());

        MotorStackFrame suspendedFrame = fsm.getStackSnapshot().peek();
        assertNotNull(suspendedFrame);
        assertEquals(MotorState.MINING, suspendedFrame.getState());
        assertEquals(13, suspendedFrame.getProgressTicks());
        assertEquals(0.65, suspendedFrame.getLastProgressValue(), 1e-4);
        assertEquals(minePos, suspendedFrame.getContextValue("breakPos"));

        assertTrue(fsm.popState());
        assertEquals(MotorState.MINING, fsm.getState());
        assertEquals(0, fsm.getStackDepth());
        verify(mockBreaker).resume(minePos, 0.65f, 13);

        // 2. Navigation suspension and resumption
        net.minecraft.world.phys.Vec3 navVec = new net.minecraft.world.phys.Vec3(50.5, 64.0, -25.5);
        when(mockNavigator.getTargetCoords()).thenReturn(navVec);
        when(mockNavigator.getPosition()).thenReturn(new net.minecraft.world.phys.Vec3(10.0, 64.0, 0.0));
        when(mockNavigator.getSpeedMultiplier()).thenReturn(1.2);
        when(mockNavigator.isSprint()).thenReturn(true);
        when(mockNavigator.isAllowBreak()).thenReturn(true);
        when(mockNavigator.isAllowPlace()).thenReturn(false);
        when(mockNavigator.getArrivalTolerance()).thenReturn(2);

        Vec3 protoTarget = Vec3.newBuilder().setX(50.5).setY(64.0).setZ(-25.5).build();
        fsm.setLastNavTarget(protoTarget);
        fsm.transitionTo(MotorState.MOVING, "start_move");
        assertTrue(fsm.pushState(MotorState.COMBAT_ENGAGED, "reflex_move_interrupt"));
        assertEquals(MotorState.COMBAT_ENGAGED, fsm.getState());
        assertEquals(1, fsm.getStackDepth());

        assertTrue(fsm.popState());
        assertEquals(MotorState.MOVING, fsm.getState());
        assertEquals(protoTarget, fsm.getLastNavTarget());
        verify(mockNavigator).navigate(50.5, 64.0, -25.5, 1.2f, true, true, false, 2);
    }

    @Test
    @DisplayName("MotorPDA handleIntent(ATTACK) during MINING preempts via stack and auto-pops after timeout")
    void testAttackIntentPreemptsAndAutoPops() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        BlockBreaker mockBreaker = mock(BlockBreaker.class);
        AgentNavigator mockNavigator = mock(AgentNavigator.class);
        when(mockAgent.getAgentId()).thenReturn("pda-attack-preempt");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.getBreaker()).thenReturn(mockBreaker);
        when(mockAgent.getNavigator()).thenReturn(mockNavigator);

        BlockPos minePos = new BlockPos(4, 64, 4);
        when(mockBreaker.isBreaking()).thenReturn(true);
        when(mockBreaker.getPos()).thenReturn(minePos);
        when(mockBreaker.getProgress()).thenReturn(0.4f);
        when(mockBreaker.getTicks()).thenReturn(8);
        when(mockBreaker.resume(minePos, 0.4f, 8)).thenReturn(true);

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        fsm.updateReceiptState(77L, 0, IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, "");
        fsm.transitionTo(MotorState.MINING, "mining_ore");

        AgentIntent atk = AgentIntent.newBuilder()
                .setAgentId("pda-attack-preempt")
                .setAttack(AttackIntent.newBuilder().setRaiseShield(false).build())
                .build();
        assertTrue(fsm.handleIntent(atk, actuator));
        assertEquals(MotorState.COMBAT_ENGAGED, fsm.getState());
        assertEquals(1, fsm.getStackDepth());

        IntentReceipt preemptedReceipt = fsm.getCurrentReceipt();
        assertEquals(77L, preemptedReceipt.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX, preemptedReceipt.getStatus());
        assertEquals(1, preemptedReceipt.getMotorStackDepth());

        // Tick past COMBAT_TIMEOUT_TICKS -> automatically pops back to MINING and calls resume()
        for (int i = 0; i <= AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            fsm.tick();
        }
        assertEquals(MotorState.MINING, fsm.getState());
        assertEquals(0, fsm.getStackDepth());
        verify(mockBreaker).resume(minePos, 0.4f, 8);
    }

    @Test
    @DisplayName("MotorPDA tick detects incoming projectile trajectory and preempts MINING into COMBAT_ENGAGED")
    void testProjectileTrajectoryPreemption() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        ServerLevel mockLevel = mock(ServerLevel.class);
        BlockBreaker mockBreaker = mock(BlockBreaker.class);
        when(mockAgent.getAgentId()).thenReturn("pda-arrow-reflex");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.level()).thenReturn(mockLevel);
        when(mockAgent.getX()).thenReturn(0.0);
        when(mockAgent.getY()).thenReturn(64.0);
        when(mockAgent.getZ()).thenReturn(0.0);
        when(mockAgent.getBoundingBox()).thenReturn(new AABB(-0.3, 64.0, -0.3, 0.3, 65.8, 0.3));
        when(mockAgent.getBreaker()).thenReturn(mockBreaker);

        BlockPos minePos = new BlockPos(1, 64, 0);
        when(mockBreaker.isBreaking()).thenReturn(true);
        when(mockBreaker.getPos()).thenReturn(minePos);
        when(mockBreaker.getProgress()).thenReturn(0.5f);
        when(mockBreaker.getTicks()).thenReturn(10);

        Arrow incomingArrow = mock(Arrow.class);
        when(incomingArrow.isAlive()).thenReturn(true);
        when(incomingArrow.getOwner()).thenReturn(null);
        when(incomingArrow.position()).thenReturn(new net.minecraft.world.phys.Vec3(4.0, 65.0, 0.0));
        when(incomingArrow.getDeltaMovement()).thenReturn(new net.minecraft.world.phys.Vec3(-1.5, 0.0, 0.0));

        when(mockLevel.getEntitiesOfClass(eq(net.minecraft.world.entity.projectile.Projectile.class), any(AABB.class), any()))
                .thenReturn(List.of(incomingArrow));

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        fsm.transitionTo(MotorState.MINING, "mining_block");

        fsm.tick();
        assertEquals(MotorState.COMBAT_ENGAGED, fsm.getState());
        assertEquals(1, fsm.getStackDepth());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX, fsm.getCurrentReceipt().getStatus());
    }

    @Test
    @DisplayName("MotorPDA flush() clears stack, stops breaker/navigator, and resets to IDLE")
    void testFlushClearsStackAndActuators() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        BlockBreaker mockBreaker = mock(BlockBreaker.class);
        AgentNavigator mockNavigator = mock(AgentNavigator.class);
        when(mockAgent.getAgentId()).thenReturn("pda-flush");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.getBreaker()).thenReturn(mockBreaker);
        when(mockAgent.getNavigator()).thenReturn(mockNavigator);

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        fsm.transitionTo(MotorState.MINING, "mining");
        fsm.pushState(MotorState.COMBAT_ENGAGED, "combat");
        assertEquals(1, fsm.getStackDepth());

        fsm.flush();
        assertEquals(0, fsm.getStackDepth());
        assertEquals(MotorState.IDLE, fsm.getState());
        assertNull(fsm.getCurrentIntent());
        verify(mockBreaker, atLeastOnce()).cancel();
        verify(mockNavigator, atLeastOnce()).stop();
    }
}
