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

    private static final Lease NO_LEASE = Lease.none(0);

    /** Pushes {@code motor} as the single frame of batch {@code batchId} / frame {@code frameId} (index 0). */
    private static void pushBatch(AgentMotorFSM fsm, long batchId, long frameId, Motor motor) {
        assertTrue(fsm.pushBatch(List.of(new Frame(batchId, frameId, 0, motor, NO_LEASE)), 1));
    }

    private static AgentEntity agentWithFsmHook(AgentEntity agent, AgentMotorFSM fsm) {
        when(agent.getMotorFSM()).thenReturn(fsm);
        return agent;
    }

    @Test
    @DisplayName("MotorFSM starts with an empty motor stack, no receipt and no MotorState enum")
    void testInitialState() {
        assertTrue(motorFSM.isMotorIdle());
        assertTrue(motorFSM.topMotor().isEmpty());
        assertEquals(0, motorFSM.getStackDepth());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED, motorFSM.getCurrentReceipt().getStatus());
        assertThrows(ClassNotFoundException.class,
                () -> Class.forName("com.civquest.mod.actuator.motor.MotorState"));
        assertThrows(ClassNotFoundException.class,
                () -> Class.forName("com.civquest.mod.actuator.motor.Recover"));
    }

    @Test
    @DisplayName("The motor state is the top frame: pushing a frame changes it, popping restores the one below")
    void testStateIsTopOfStack() {
        assertTrue(motorFSM.pushDirect(new Mine(null, 0, 0.0)));
        assertInstanceOf(Mine.class, motorFSM.topMotor().orElseThrow());
        assertTrue(motorFSM.pushDirect(new Sleep(null, 0)));
        assertInstanceOf(Sleep.class, motorFSM.topMotor().orElseThrow());
        assertTrue(motorFSM.isSleeping());
        assertEquals(2, motorFSM.getMotorStack().depth());
    }

    @Test
    @DisplayName("MotorFSM preemption matrix protects SLEEPING state from routine navigation and actions")
    void testSleepingPreemptionMatrix() {
        assertTrue(motorFSM.pushDirect(new Sleep(null, 0)));

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
    @DisplayName("MotorFSM preemption matrix protects an active Combat reflex frame from routine intents")
    void testCombatPreemptionMatrix() {
        assertTrue(motorFSM.pushReflex(new Combat(0, 0), "in_combat"));

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
    @DisplayName("ADR-037: there is no STUCK_RECOVERY state; a stuck Move ends FAILED with facts and the stack drains")
    void testNoStuckRecoveryState() {
        for (java.lang.reflect.Field f : AgentMotorFSM.class.getDeclaredFields()) {
            assertFalse(f.getName().toLowerCase().contains("stuckrecovery"), "field " + f.getName());
        }
    }

    @Test
    @DisplayName("Combat reflex frame pops after COMBAT_TIMEOUT_TICKS (60) and emits pushed/popped ReflexNotices")
    void testCombatDisengagementTimeout() {
        assertEquals(60, AgentMotorFSM.COMBAT_TIMEOUT_TICKS);
        assertTrue(motorFSM.pushReflex(new Combat(0, 0), "start_combat"));
        assertEquals(1, motorFSM.getStackDepth());
        assertInstanceOf(Combat.class, motorFSM.activeReflex().orElseThrow());

        for (int i = 0; i < AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            motorFSM.tick();
            assertEquals(1, motorFSM.getStackDepth(), "still engaged at tick " + (i + 1));
        }

        // Ticking past COMBAT_TIMEOUT_TICKS pops the frame
        motorFSM.tick();
        assertEquals(0, motorFSM.getStackDepth());
        assertTrue(motorFSM.activeReflex().isEmpty());
        assertTrue(motorFSM.isMotorIdle());
    }

    @Test
    @DisplayName("Reflex push/pop emit ReflexNotice with preempted_frame_id and popped flag")
    void testReflexNoticesCarryPreemptedFrameAndPoppedFlag() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        when(mockAgent.getAgentId()).thenReturn("notice-agent");
        when(mockAgent.isAlive()).thenReturn(true);
        mockAgent.tickCount = 7;
        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        pushBatch(fsm, 1L, 4242L, new Mine(null, 0, 0.0));

        assertTrue(fsm.pushReflex(new Combat(0, 0), "test_push"));
        long reflexFrameId = fsm.getMotorStack().peek().orElseThrow().frameId();
        assertTrue(reflexFrameId >= AgentMotorFSM.LOCAL_FRAME_ID_BASE, "reflex frames carry real (non-zero) ids");
        for (int i = 0; i <= AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            fsm.tick();
        }

        org.mockito.ArgumentCaptor<com.civquest.mod.grpc.v1.ReflexNotice> cap =
                org.mockito.ArgumentCaptor.forClass(com.civquest.mod.grpc.v1.ReflexNotice.class);
        verify(mockAgent, times(2)).recordReflexNotice(cap.capture());
        var pushed = cap.getAllValues().get(0);
        var popped = cap.getAllValues().get(1);
        assertEquals(com.civquest.mod.grpc.v1.ReflexNotice.ReflexType.REFLEX_TYPE_COMBAT, pushed.getReflexType());
        assertEquals(4242L, pushed.getPreemptedFrameId());
        assertFalse(pushed.getPopped());
        assertEquals(4242L, popped.getPreemptedFrameId());
        assertTrue(popped.getPopped());
    }

    @Test
    @DisplayName("Dodge and Flee frames are real reflex frames that time out and resume the interrupted frame")
    void testDodgeAndFleeFramesExpire() {
        assertTrue(motorFSM.pushDirect(new Mine(null, 0, 0.0)));
        assertTrue(motorFSM.pushReflex(new Dodge(Dodge.DEFAULT_TICKS), "dodge"));
        assertTrue(motorFSM.preemptWithFleeReflex(Vec3.getDefaultInstance(), "flee"));
        assertEquals(2, motorFSM.getStackDepth());
        assertInstanceOf(Flee.class, motorFSM.activeReflex().orElseThrow());
        for (int i = 0; i < Flee.DEFAULT_TICKS + Dodge.DEFAULT_TICKS && motorFSM.getStackDepth() > 0; i++) {
            motorFSM.tick();
        }
        assertEquals(0, motorFSM.getStackDepth());
        assertInstanceOf(Mine.class, motorFSM.topMotor().orElseThrow(), "interrupted frame is untouched while reflexes run");
    }

    private AgentMotorFSM stuckFsm(AgentNavigator nav) {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.getNavigator()).thenReturn(nav);
        when(nav.isPathing()).thenReturn(true);
        AgentMotorFSM fsm = new AgentMotorFSM(agent);
        pushBatch(fsm, 9L, 90L, new Move(Vec3.newBuilder().setX(5).setY(64).setZ(5).build(), 1f, false, false, false, 1));
        return fsm;
    }

    @Test
    @DisplayName("ADR-037: navigator stuck ends the Move immediately as a failure carrying terrain facts")
    void testStuckEndsMoveWithTerrainFacts() {
        AgentNavigator nav = mock(AgentNavigator.class);
        com.civquest.mod.grpc.v1.MotorFacts facts = com.civquest.mod.grpc.v1.MotorFacts.newBuilder()
                .setStuck(true).setStuckTicks(16).setBlockedBearingDeg(90.0f).setProgress(7.5).build();
        when(nav.isStuck()).thenReturn(true);
        when(nav.snapshotFacts()).thenReturn(facts);
        AgentMotorFSM fsm = stuckFsm(nav);

        fsm.tick();

        assertTrue(fsm.isMotorIdle(), "no recovery delay state: Java reports and drains the stack");
        IntentReceipt receipt = fsm.getCurrentReceipt();
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED, receipt.getStatus());
        assertEquals("Navigation stuck", receipt.getFailureReason());
        assertEquals(9L, receipt.getBatchId());
        assertEquals(90L, receipt.getFrameId(), "receipts carry the batch frame id");
        assertEquals(facts, receipt.getFacts());
    }

    @Test
    @DisplayName("ADR-037: entity congestion (ADR-033 yield) is reported as blocked_by_entity, distinct from terrain")
    void testStuckEndsMoveWithCongestionFacts() {
        AgentNavigator nav = mock(AgentNavigator.class);
        com.civquest.mod.grpc.v1.MotorFacts facts = com.civquest.mod.grpc.v1.MotorFacts.newBuilder()
                .setStuck(true).setStuckTicks(16).setBlockedByEntity(true).build();
        when(nav.isStuck()).thenReturn(true);
        when(nav.snapshotFacts()).thenReturn(facts);
        AgentMotorFSM fsm = stuckFsm(nav);

        fsm.tick();

        com.civquest.mod.grpc.v1.MotorFacts taken = fsm.getCurrentReceipt().getFacts();
        assertNotNull(taken);
        assertTrue(taken.getBlockedByEntity());
        assertTrue(taken.getStuck());
    }

    @Test
    @DisplayName("ADR-037: an identical NavigateIntent is no longer rejected after a stuck failure (Go decides retries)")
    void testNavigateAcceptedAfterStuck() {
        AgentNavigator nav = mock(AgentNavigator.class);
        when(nav.isStuck()).thenReturn(true);
        AgentMotorFSM fsm = stuckFsm(nav);
        Vec3 target = Vec3.newBuilder().setX(100.0).setY(64.0).setZ(200.0).build();
        AgentIntent navIntent = AgentIntent.newBuilder()
                .setNavigate(NavigateIntent.newBuilder().setTarget(target).build())
                .build();
        fsm.tick();
        assertTrue(fsm.canPreempt(navIntent));
    }

    @Test
    @DisplayName("MotorFSM isSameNavigationTarget checks coordinate proximity correctly")
    void testIsSameNavigationTarget() {
        Vec3 base = Vec3.newBuilder().setX(10.0).setY(64.0).setZ(10.0).build();
        assertTrue(motorFSM.isSameNavigationTarget(NavigateIntent.getDefaultInstance()), "no Move frame: no-target is same");
        assertTrue(motorFSM.pushDirect(new Move(base, 1f, false, false, false, 1)));

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

        motorFSM.flush();
        assertTrue(motorFSM.isSameNavigationTarget(noTarget));
    }

    @Test
    @DisplayName("Reflex nesting is bounded at MAX_STACK_DEPTH = 4 and unwinds LIFO back to the interrupted state")
    void testBoundedReflexNestingLimit() {
        assertTrue(motorFSM.pushDirect(new Mine(null, 0, 0.0)));

        assertTrue(motorFSM.pushReflex(new Dodge(500), "push_1"));
        assertTrue(motorFSM.pushReflex(new Flee(Vec3.getDefaultInstance(), 500), "push_2"));
        assertTrue(motorFSM.pushReflex(new UseItem("slot:0", 0), "push_3"));
        assertTrue(motorFSM.pushReflex(new Combat(0, 0), "push_4"));
        assertEquals(AgentMotorFSM.MAX_STACK_DEPTH, motorFSM.getStackDepth());

        // 5th push must be rejected cleanly without overflowing the stack
        assertFalse(motorFSM.pushReflex(new Combat(0, 0), "push_overflow"));
        assertEquals(AgentMotorFSM.MAX_STACK_DEPTH, motorFSM.getStackDepth());

        // LIFO order on the typed stack (top first)
        var frames = motorFSM.getMotorStack().snapshot();
        assertInstanceOf(Combat.class, frames.get(0).motor());
        assertInstanceOf(UseItem.class, frames.get(1).motor());
        assertInstanceOf(Flee.class, frames.get(2).motor());
        assertInstanceOf(Dodge.class, frames.get(3).motor());

        // Combat times out, then the detached UseItem (not using an item) pops, leaving Flee over Dodge
        for (int i = 0; i <= AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            motorFSM.tick();
        }
        assertEquals(3, motorFSM.getStackDepth());
        motorFSM.tick();
        assertEquals(2, motorFSM.getStackDepth());
        assertInstanceOf(Flee.class, motorFSM.activeReflex().orElseThrow());
    }

    @Test
    @DisplayName("MotorPDA reflex push/pop preserves and resumes BlockBreaker and AgentNavigator with zero amnesia")
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

        assertTrue(fsm.pushDirect(new Mine(minePos, 0, 0.0)));
        assertTrue(fsm.pushReflex(new Combat(0, 0), "reflex_interrupt"));
        assertInstanceOf(Mine.class, fsm.getMotorStack().snapshot().get(1).motor());
        assertEquals(1, fsm.getStackDepth());
        verify(mockBreaker).cancel();

        tickOutCombat(fsm);
        assertInstanceOf(Mine.class, fsm.topMotor().orElseThrow());
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
        when(mockNavigator.isPathing()).thenReturn(true);
        fsm.flush();
        clearInvocations(mockNavigator);
        assertTrue(fsm.pushDirect(new Move(protoTarget, 1.2f, true, true, false, 2)));
        assertTrue(fsm.pushReflex(new Dodge(5), "reflex_move_interrupt"));
        assertInstanceOf(Move.class, fsm.getMotorStack().snapshot().get(1).motor());
        assertEquals(1, fsm.getStackDepth());
        verify(mockNavigator).stop();

        for (int i = 0; i < 6; i++) {
            fsm.tick();
        }
        assertEquals(0, fsm.getStackDepth());
        assertEquals(protoTarget, ((Move) fsm.topMotor().orElseThrow()).target());
        verify(mockNavigator, times(2)).navigate(50.5, 64.0, -25.5, 1.2f, true, true, false, 2, false); // enter + resume
    }

    private static void tickOutCombat(AgentMotorFSM fsm) {
        for (int i = 0; i <= AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            fsm.tick();
        }
    }

    @Test
    @DisplayName("ATTACK intent during MINING pushes a Strike frame over Mine and resumes the dig after the combat window")
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
        agentWithFsmHook(mockAgent, fsm);
        pushBatch(fsm, 77L, 770L, new Mine(minePos, 0, 0.0));

        AgentIntent atk = AgentIntent.newBuilder()
                .setAgentId("pda-attack-preempt")
                .setAttack(AttackIntent.newBuilder().setRaiseShield(false).build())
                .build();
        assertTrue(actuator.actuateWithResult(mockAgent, atk));
        assertInstanceOf(Strike.class, fsm.topMotor().orElseThrow());
        assertInstanceOf(Mine.class, fsm.getMotorStack().snapshot().get(1).motor(), "interrupted Mine stays suspended underneath");
        verify(mockBreaker, never()).tick();
        verify(mockBreaker).cancel();

        IntentReceipt inFlight = fsm.getCurrentReceipt();
        assertEquals(77L, inFlight.getBatchId());
        assertEquals(770L, inFlight.getFrameId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, inFlight.getStatus());

        // Tick past COMBAT_TIMEOUT_TICKS -> Strike completes and the Mine resumes via resume()
        tickOutCombat(fsm);
        assertInstanceOf(Mine.class, fsm.topMotor().orElseThrow());
        assertEquals(0, fsm.getStackDepth());
        verify(mockBreaker).resume(minePos, 0.4f, 8);
    }

    @Test
    @DisplayName("MotorPDA tick detects incoming projectile trajectory (6-block threat radius) and preempts MINING with a Dodge frame")
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
        pushBatch(fsm, 5L, 50L, new Mine(minePos, 0, 0.0));

        fsm.tick();
        assertInstanceOf(Mine.class, fsm.getMotorStack().snapshot().get(1).motor());
        assertInstanceOf(Dodge.class, fsm.activeReflex().orElseThrow());
        assertEquals(1, fsm.getStackDepth());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX, fsm.getCurrentReceipt().getStatus());
    }

    @Test
    @DisplayName("Projectile outside the 6-block threat radius or flying away does not trigger a Dodge")
    void testProjectileOutsideThreatRadiusIgnored() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        ServerLevel mockLevel = mock(ServerLevel.class);
        when(mockAgent.getAgentId()).thenReturn("pda-arrow-far");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.level()).thenReturn(mockLevel);
        when(mockAgent.getX()).thenReturn(0.0);
        when(mockAgent.getY()).thenReturn(64.0);
        when(mockAgent.getZ()).thenReturn(0.0);
        when(mockAgent.getBoundingBox()).thenReturn(new AABB(-0.3, 64.0, -0.3, 0.3, 65.8, 0.3));

        Arrow awayArrow = mock(Arrow.class);
        when(awayArrow.isAlive()).thenReturn(true);
        when(awayArrow.position()).thenReturn(new net.minecraft.world.phys.Vec3(4.0, 65.0, 0.0));
        when(awayArrow.getDeltaMovement()).thenReturn(new net.minecraft.world.phys.Vec3(1.5, 0.0, 0.0));
        // The level query is bounded by getBoundingBox().inflate(PROJECTILE_THREAT_RADIUS): an arrow beyond it is never returned.
        when(mockLevel.getEntitiesOfClass(eq(net.minecraft.world.entity.projectile.Projectile.class), any(AABB.class), any()))
                .thenReturn(List.of(awayArrow));

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        assertTrue(fsm.pushDirect(new Mine(null, 0, 0.0)));
        fsm.tick();
        assertEquals(0, fsm.getStackDepth(), "arrow moving away must not preempt");

        org.mockito.ArgumentCaptor<AABB> box = org.mockito.ArgumentCaptor.forClass(AABB.class);
        verify(mockLevel).getEntitiesOfClass(eq(net.minecraft.world.entity.projectile.Projectile.class), box.capture(), any());
        assertEquals(0.6 + 12.0, box.getValue().getXsize(), 1e-6, "threat box is the agent box inflated by 6 blocks per side");
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
        pushBatch(fsm, 3L, 30L, new Mine(null, 0, 0.0));
        fsm.pushReflex(new Combat(0, 0), "combat");
        assertEquals(1, fsm.getStackDepth());

        fsm.flush();
        assertEquals(0, fsm.getStackDepth());
        assertTrue(fsm.isMotorIdle());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_UNSPECIFIED, fsm.getCurrentReceipt().getStatus());
        verify(mockBreaker, atLeastOnce()).cancel();
        verify(mockNavigator, atLeastOnce()).stop();
    }

    @Test
    @DisplayName("SurveyHorizonIntent enters SURVEYING, sweeps 36 bearings over 72 ticks rotating yaw, queues report, and returns to IDLE")
    void testSurveyHorizonFullSweepAndCompletion() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        ServerLevel mockLevel = mock(ServerLevel.class);
        AgentNavigator mockNavigator = mock(AgentNavigator.class);
        net.minecraft.world.phys.Vec3 eyePos = new net.minecraft.world.phys.Vec3(10.5, 65.62, -4.5);

        when(mockAgent.getAgentId()).thenReturn("survey-agent");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.level()).thenReturn(mockLevel);
        when(mockAgent.getEyePosition()).thenReturn(eyePos);
        when(mockAgent.getNavigator()).thenReturn(mockNavigator);
        when(mockAgent.getMainHandItem()).thenReturn(net.minecraft.world.item.ItemStack.EMPTY);
        when(mockAgent.getBoundingBox()).thenReturn(new AABB(10.2, 64.0, -4.8, 10.8, 65.8, -4.2));
        when(mockLevel.getGameTime()).thenReturn(1200L);
        when(mockLevel.getEntitiesOfClass(eq(net.minecraft.world.entity.projectile.Projectile.class), any(AABB.class), any()))
                .thenReturn(List.of());
        when(mockLevel.clip(any(net.minecraft.world.level.ClipContext.class))).thenAnswer(inv -> {
            net.minecraft.world.level.ClipContext ctx = inv.getArgument(0);
            return net.minecraft.world.phys.BlockHitResult.miss(
                    ctx.getTo(),
                    net.minecraft.core.Direction.NORTH,
                    BlockPos.containing(ctx.getTo()));
        });

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        agentWithFsmHook(mockAgent, fsm);
        AgentIntent surveyIntent = AgentIntent.newBuilder()
                .setAgentId("survey-agent")
                .setSequenceId(321L)
                .setSurveyHorizon(com.civquest.mod.grpc.v1.SurveyHorizonIntent.newBuilder()
                        .setNumSectors(36)
                        .setMaxRange(64.0f)
                        .setTicksPerSector(2)
                        .build())
                .build();

        assertTrue(actuator.actuateWithResult(mockAgent, surveyIntent));
        assertTrue(fsm.isSweeping());
        assertEquals(321L, fsm.getLastProcessedSequenceId());
        verify(mockNavigator, atLeastOnce()).stop();

        for (int t = 0; t < 72; t++) {
            fsm.tick();
        }

        assertTrue(fsm.isMotorIdle());

        org.mockito.ArgumentCaptor<Float> yawCaptor = org.mockito.ArgumentCaptor.forClass(Float.class);
        verify(mockAgent, times(36)).faceBearingAndSync(yawCaptor.capture());
        List<Float> rotatedYaws = yawCaptor.getAllValues();
        assertEquals(36, rotatedYaws.size());
        assertEquals(0.0f, rotatedYaws.get(0), 1e-4);
        assertEquals(10.0f, rotatedYaws.get(1), 1e-4);
        assertEquals(180.0f, rotatedYaws.get(18), 1e-4);
        assertEquals(350.0f, rotatedYaws.get(35), 1e-4);

        org.mockito.ArgumentCaptor<com.civquest.mod.grpc.v1.HorizonSurveyReport> reportCaptor =
                org.mockito.ArgumentCaptor.forClass(com.civquest.mod.grpc.v1.HorizonSurveyReport.class);
        verify(mockAgent, times(1)).queueHorizonSurveyReport(reportCaptor.capture());
        com.civquest.mod.grpc.v1.HorizonSurveyReport report = reportCaptor.getValue();
        assertEquals(36, report.getSectorsCount());
        assertEquals(10.5, report.getOriginX(), 1e-4);
        assertEquals(65.62, report.getOriginY(), 1e-4);
        assertEquals(-4.5, report.getOriginZ(), 1e-4);
        assertEquals(1200L, report.getCompletedTick());
        assertEquals(64.0f, report.getMaxRange(), 1e-4);
    }

    @Test
    @DisplayName("Incoming combat threat mid-survey immediately preempts the Sweep frame with a Combat reflex frame")
    void testCombatThreatPreemptsSurveying() {
        AgentEntity mockAgent = mock(AgentEntity.class);
        ServerLevel mockLevel = mock(ServerLevel.class);
        when(mockAgent.getAgentId()).thenReturn("survey-preempt-agent");
        when(mockAgent.isAlive()).thenReturn(true);
        when(mockAgent.level()).thenReturn(mockLevel);
        when(mockAgent.getEyePosition()).thenReturn(new net.minecraft.world.phys.Vec3(0.5, 65.62, 0.5));
        when(mockAgent.getMainHandItem()).thenReturn(net.minecraft.world.item.ItemStack.EMPTY);
        when(mockAgent.getBoundingBox()).thenReturn(new AABB(-0.3, 64.0, -0.3, 0.3, 65.8, 0.3));
        when(mockLevel.clip(any(net.minecraft.world.level.ClipContext.class))).thenAnswer(inv -> {
            net.minecraft.world.level.ClipContext ctx = inv.getArgument(0);
            return net.minecraft.world.phys.BlockHitResult.miss(
                    ctx.getTo(),
                    net.minecraft.core.Direction.NORTH,
                    BlockPos.containing(ctx.getTo()));
        });

        AgentMotorFSM fsm = new AgentMotorFSM(mockAgent);
        agentWithFsmHook(mockAgent, fsm);
        AgentIntent surveyIntent = AgentIntent.newBuilder()
                .setAgentId("survey-preempt-agent")
                .setSurveyHorizon(com.civquest.mod.grpc.v1.SurveyHorizonIntent.newBuilder()
                        .setNumSectors(36)
                        .setMaxRange(64.0f)
                        .setTicksPerSector(2)
                        .build())
                .build();

        assertTrue(actuator.actuateWithResult(mockAgent, surveyIntent));
        assertTrue(fsm.isSweeping());
        fsm.tick(); // Sector 0 scanned

        // Direct combat reflex preemption (e.g., hurt by mob) immediately pushes a Combat frame
        assertTrue(fsm.preemptWithCombatReflex("damage_reflex_mob"));
        assertInstanceOf(Combat.class, fsm.activeReflex().orElseThrow());
        assertEquals(1, fsm.getStackDepth());
        assertTrue(fsm.hasSweepFrame(), "sweep frame is suspended beneath the reflex, locals intact");
        assertFalse(fsm.isSweeping());
        verify(mockAgent, never()).queueHorizonSurveyReport(any());

        // Also verify AttackIntent mid-survey preempts cleanly
        fsm.flush();
        assertTrue(actuator.actuateWithResult(mockAgent, surveyIntent));
        assertTrue(fsm.isSweeping());
        AgentIntent atk = AgentIntent.newBuilder()
                .setAgentId("survey-preempt-agent")
                .setAttack(AttackIntent.newBuilder().setTargetEntityId(77).build())
                .build();
        assertTrue(actuator.actuateWithResult(mockAgent, atk));
        assertInstanceOf(Strike.class, fsm.topMotor().orElseThrow());
        assertTrue(fsm.hasSweepFrame(), "sweep stays suspended beneath the attack frame");
        assertFalse(fsm.isSweeping());
    }
}
