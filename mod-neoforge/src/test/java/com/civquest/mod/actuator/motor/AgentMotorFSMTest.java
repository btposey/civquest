package com.civquest.mod.actuator.motor;

import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

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
}
