package com.civquest.mod.actuator.motor;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.anyBoolean;
import static org.mockito.Mockito.*;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.MotorFacts;
import com.civquest.mod.grpc.v1.Vec3;
import net.minecraft.core.BlockPos;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Unit tests for the individual {@link Motor} records, driven through a mock-agent {@link Body}. */
class MotorsTest {
    private AgentEntity agent;
    private AgentNavigator nav;
    private BlockBreaker breaker;
    private Body body;

    @BeforeEach
    void setUp() {
        agent = mock(AgentEntity.class);
        nav = mock(AgentNavigator.class);
        breaker = mock(BlockBreaker.class);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getBreaker()).thenReturn(breaker);
        body = new AgentBody(agent, () -> 0L);
    }

    private static Move move() {
        return new Move(Vec3.newBuilder().setX(5).setY(64).setZ(5).build(), 1.5f, true, false, true, 2);
    }

    // ---- Move ----

    @Test
    void moveEnterArmsNavigatorWithItsOwnParameters() {
        when(nav.isPathing()).thenReturn(true);
        Outcome out = move().onEnter(body);
        assertFalse(out.finished());
        verify(nav).navigate(5.0, 64.0, 5.0, 1.5f, true, false, true, 2);
    }

    @Test
    void moveEnterFailsWhenNavigatorRejectsTarget() {
        Outcome out = move().onEnter(body); // mock nav: not pathing, not arrived
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, out.status());
        assertTrue(out.reason().contains("Navigate rejected"));
    }

    @Test
    void moveEnterCompletesWhenAlreadyArrived() {
        when(nav.isArrived()).thenReturn(true);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, move().onEnter(body).status());
    }

    @Test
    void moveTickReportsArrivalStuckFactsAndIdle() {
        when(nav.isArrived()).thenReturn(true);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, move().onTick(body).status());

        when(nav.isArrived()).thenReturn(false);
        MotorFacts facts = MotorFacts.newBuilder().setStuck(true).build();
        when(nav.isStuck()).thenReturn(true);
        when(nav.snapshotFacts()).thenReturn(facts);
        Outcome stuck = move().onTick(body);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, stuck.status());
        assertEquals("Navigation stuck", stuck.reason());
        assertEquals(facts, stuck.facts());

        when(nav.isStuck()).thenReturn(false);
        when(nav.isIdle()).thenReturn(true);
        assertTrue(move().onTick(body).reason().contains("stopped before reaching"));

        when(nav.isIdle()).thenReturn(false);
        assertFalse(move().onTick(body).finished());
    }

    @Test
    void moveSuspendStopsAndResumeReArmsNavigator() {
        Move m = move();
        assertSame(m, m.onSuspend(body));
        verify(nav).stop();
        m.onResume(body);
        verify(nav).navigate(5.0, 64.0, 5.0, 1.5f, true, false, true, 2);
        m.onExit(body);
        verify(nav, times(2)).stop();
    }

    // ---- Mine ----

    @Test
    void mineStartFailureFailsWhenNotBreakingAndInstantBreakCompletes() {
        Mine failing = new Mine(new BlockPos(1, 2, 3), 0, 0.0, a -> false);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, failing.onEnter(body).status());

        Mine instant = new Mine(new BlockPos(1, 2, 3), 0, 0.0, a -> true);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, instant.onEnter(body).status());

        when(breaker.isBreaking()).thenReturn(true);
        Outcome timed = instant.onEnter(body);
        assertFalse(timed.finished());
        assertNull(((Mine) timed.next()).start());
    }

    @Test
    void mineTickCarriesProgressAndFinishesOnBreakOrCancel() {
        BlockPos pos = new BlockPos(1, 2, 3);
        Mine mine = new Mine(pos, 0, 0.0);
        when(breaker.isBreaking()).thenReturn(true);
        when(breaker.tick()).thenReturn(false);
        when(breaker.getTicks()).thenReturn(4);
        when(breaker.getProgress()).thenReturn(0.25f);
        Outcome progressing = mine.onTick(body);
        assertFalse(progressing.finished());
        assertEquals(4, ((Mine) progressing.next()).progressTicks());
        assertEquals(0.25, ((Mine) progressing.next()).progress(), 1e-6);

        when(breaker.tick()).thenReturn(true);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, mine.onTick(body).status());

        when(breaker.tick()).thenReturn(false);
        when(breaker.isBreaking()).thenReturn(true, false);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, mine.onTick(body).status());
    }

    @Test
    void mineSuspendCapturesProgressAndResumeRestoresIt() {
        BlockPos pos = new BlockPos(1, 2, 3);
        when(breaker.isBreaking()).thenReturn(true);
        when(breaker.getPos()).thenReturn(pos);
        when(breaker.getTicks()).thenReturn(13);
        when(breaker.getProgress()).thenReturn(0.65f);
        when(breaker.resume(pos, 0.65f, 13)).thenReturn(true);

        Motor kept = new Mine(pos, 0, 0.0).onSuspend(body);
        verify(breaker).cancel();
        assertEquals(13, ((Mine) kept).progressTicks());
        assertFalse(kept.onResume(body).finished());
        verify(breaker).resume(pos, 0.65f, 13);

        when(breaker.resume(pos, 0.65f, 13)).thenReturn(false);
        when(agent.level()).thenReturn(mock(net.minecraft.world.level.Level.class));
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, kept.onResume(body).status());
    }

    // ---- Sleep / UseItem ----

    @Test
    void sleepFailsToStartThenCompletesOnWake() {
        Sleep bad = new Sleep(new BlockPos(0, 64, 0), 0, a -> false);
        assertTrue(bad.onEnter(body).reason().contains("SLEEP_IN_BED failed"));

        when(agent.isSleeping()).thenReturn(true);
        Sleep ok = new Sleep(new BlockPos(0, 64, 0), 0, a -> true);
        Outcome entered = ok.onEnter(body);
        assertFalse(entered.finished());
        assertFalse(entered.next().onTick(body).finished());
        when(agent.isSleeping()).thenReturn(false);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, entered.next().onTick(body).status());
    }

    @Test
    void sleepStopsSleepingOnSuspendAndExit() {
        when(agent.isSleeping()).thenReturn(true);
        Sleep s = new Sleep(null, 0);
        s.onSuspend(body);
        s.onExit(body);
        verify(agent, times(2)).stopSleeping();
    }

    @Test
    void useItemRoutineStartsAndStopsItemOnExitEmergencyDoesNot() {
        UseItem routine = new UseItem("slot:1", 0, false, a -> true);
        when(agent.isUsingItem()).thenReturn(true);
        assertFalse(routine.onEnter(body).finished());
        routine.onExit(body);
        verify(agent).stopUsingItem();

        UseItem emergency = new UseItem("slot:1", 0);
        assertTrue(emergency.emergency());
        emergency.onExit(body);
        verify(agent, times(1)).stopUsingItem();
    }

    @Test
    void useItemInstantAndFailingStarts() {
        when(agent.isUsingItem()).thenReturn(false);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, new UseItem("slot:0", 0, false, a -> true).onEnter(body).status());
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, new UseItem("slot:0", 0, false, a -> false).onEnter(body).status());
    }

    // ---- Act / Place / Strike ----

    @Test
    void actAndPlaceCompleteOrFailInOneTickAndNoAgentIsNoOp() {
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, new Act("CRAFT_ITEM", a -> true).onTick(body).status());
        Outcome failed = new Act("CRAFT_ITEM", a -> false).onTick(body);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, failed.status());
        assertEquals("", failed.reason(), "blank reason: the FSM composes the indexed reason");
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, new Place("BUILD_BLOCK", a -> false).onTick(body).status());
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED,
                new Act("X", a -> { throw new IllegalStateException("boom"); }).onTick(body).status());

        Body detached = new MotorStackTest.FakeBody();
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, new Act("X", a -> false).onTick(detached).status());
    }

    @Test
    void strikeAttacksOnEnterThenCompletesAfterCombatWindowAndLowersShield() {
        int[] calls = {0};
        Strike strike = new Strike(a -> { calls[0]++; return true; }, 0);
        assertFalse(strike.onEnter(body).finished());
        assertEquals(1, calls[0]);

        Motor m = strike;
        for (int i = 0; i < Combat.TIMEOUT_TICKS; i++) {
            Outcome out = m.onTick(body);
            assertFalse(out.finished(), "still engaged at tick " + i);
            m = out.next();
        }
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, m.onTick(body).status());

        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, new Strike(a -> false, 0).onEnter(body).status());
    }

    // ---- Reflexes keep their lifecycle ----

    @Test
    void reflexesAreNotBatchMotors() {
        assertTrue(new Combat(0, 1) instanceof Reflex);
        assertTrue(new Dodge(1) instanceof Reflex);
        assertTrue(new Flee(Vec3.getDefaultInstance(), 1) instanceof Reflex);
    }
}
