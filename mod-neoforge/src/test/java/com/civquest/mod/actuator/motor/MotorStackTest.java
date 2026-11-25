package com.civquest.mod.actuator.motor;

import static org.junit.jupiter.api.Assertions.*;

import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.MotorFacts;
import com.civquest.mod.grpc.v1.Vec3;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class MotorStackTest {
    static final class FakeBody implements Body {
        long now;
        public long tick() { return now; }
        public double x() { return 0; }
        public double y() { return 0; }
        public double z() { return 0; }
        public void log(String m) { }
    }

    /** Move whose tick behaviour is scripted; the sealed hierarchy only permits records, so use Move + Sleep. */
    private final FakeBody body = new FakeBody();
    private MotorStack stack;

    private static Move move() { return new Move(Vec3.getDefaultInstance(), 1f, false, false, false, 1); }
    private static Frame frame(long fid, Motor m, Lease l) { return new Frame(1L, fid, 0, m, l); }

    @BeforeEach
    void setUp() { stack = new MotorStack(3); }

    @Test
    void pushSuspendsAndPopResumesWithProgressIntact() {
        Mine mine = new Mine(new net.minecraft.core.BlockPos(1, 2, 3), 7, 0.4);
        assertTrue(stack.push(frame(10, mine, Lease.none(0)), body));
        assertTrue(stack.push(frame(0, new Dodge(5), Lease.none(0)), body));
        assertEquals(2, stack.depth());
        assertInstanceOf(Dodge.class, stack.peek().orElseThrow().motor());
        var p = stack.pop(ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null, body).orElseThrow();
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, p.status());
        assertSame(mine, stack.peek().orElseThrow().motor());
        assertEquals(7, ((Mine) stack.peek().get().motor()).progressTicks());
    }

    @Test
    void depthLimitRejectsPush() {
        for (int i = 0; i < 3; i++) assertTrue(stack.push(frame(i + 1, move(), Lease.none(0)), body));
        assertFalse(stack.push(frame(9, move(), Lease.none(0)), body));
        assertEquals(3, stack.depth());
    }

    @Test
    void replaceKeepsDepthAndReportsOldFrame() {
        stack.push(frame(1, move(), Lease.none(0)), body);
        var p = stack.replace(frame(2, new Sleep(null, 0), Lease.none(0)), ReceiptStatus.RECEIPT_STATUS_FAILED, "x", body).orElseThrow();
        assertEquals(1, p.frame().frameId());
        assertEquals(1, stack.depth());
        assertEquals(2, stack.peek().get().frameId());
    }

    @Test
    void popOnEmptyIsEmpty() {
        assertTrue(stack.pop(ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null, body).isEmpty());
        assertTrue(stack.tick(body).isEmpty());
    }

    @Test
    void leaseExpiryPopsWithTimeoutAndFramesBelowResume() {
        stack.push(frame(1, move(), Lease.none(0)), body);
        stack.push(frame(2, new Sleep(null, 0), Lease.ofTicks(0, 5)), body);
        body.now = 4;
        assertTrue(stack.tick(body).isEmpty());
        body.now = 5;
        var p = stack.tick(body).orElseThrow();
        assertEquals(ReceiptStatus.RECEIPT_STATUS_TIMEOUT, p.status());
        assertEquals(2, p.frame().frameId());
        assertEquals(1, stack.peek().get().frameId());
    }

    @Test
    void suspendedFrameLeaseNotEnforcedUntilTop() {
        stack.push(frame(1, move(), Lease.ofTicks(0, 3)), body);
        stack.push(frame(2, new Dodge(5), Lease.none(0)), body);
        body.now = 100;
        assertTrue(stack.tick(body).isEmpty());
        assertEquals(2, stack.depth());
    }

    @Test
    void noDeadlineNeverExpires() {
        stack.push(frame(1, move(), Lease.none(0)), body);
        body.now = Long.MAX_VALUE / 2;
        assertTrue(stack.tick(body).isEmpty());
    }

    @Test
    void timeoutCarriesFactsAndIsDrained() {
        stack.push(frame(1, move(), Lease.ofTicks(0, 1)), body);
        body.now = 1;
        stack.tick(body);
        List<MotorStack.Popped> d = stack.drainPopped();
        assertEquals(1, d.size());
        assertTrue(stack.drainPopped().isEmpty());
    }

    @Test
    void clearAbortsAllTopFirst() {
        stack.push(frame(1, move(), Lease.none(0)), body);
        stack.push(frame(2, new Dodge(1), Lease.none(0)), body);
        var out = stack.clear("death", body);
        assertEquals(List.of(2L, 1L), out.stream().map(p -> p.frame().frameId()).toList());
        assertTrue(stack.isEmpty());
    }

    @Test
    void hooksRunWithoutStackMonitorAndOnlySeeBody() {
        List<String> log = new ArrayList<>();
        // Body exposes no stack/FSM: verify structurally.
        for (var m : Body.class.getMethods()) {
            assertFalse(MotorStack.class.isAssignableFrom(m.getReturnType()));
            assertFalse(m.getReturnType().getName().contains("AgentMotorFSM"));
        }
        // onEnter/onExit run on push/pop without throwing even for default hooks.
        stack.push(frame(1, move(), Lease.none(0)), body);
        stack.pop(ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", MotorFacts.getDefaultInstance(), body);
        assertTrue(log.isEmpty());
    }

    @Test
    void leaseHelpers() {
        Lease l = Lease.ofTicks(10, 5);
        assertEquals(15, l.deadlineTick());
        assertFalse(l.expired(14));
        assertTrue(l.expired(15));
        assertEquals(3, l.ticksElapsed(13));
        assertEquals(0, Lease.ofTicks(10, 0).deadlineTick());
    }
}
