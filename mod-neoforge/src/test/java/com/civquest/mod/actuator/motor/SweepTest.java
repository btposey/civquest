package com.civquest.mod.actuator.motor;

import static org.junit.jupiter.api.Assertions.*;

import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.Vec3;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class SweepTest {
    static final class FakeBody implements Body {
        long now;
        final List<Float> faced = new ArrayList<>();
        final List<HorizonSurveyReport> reports = new ArrayList<>();
        public long tick() { return now; }
        public double x() { return 0; }
        public double y() { return 0; }
        public double z() { return 0; }
        public void log(String m) { }
        @Override public void faceBearing(float yaw) { faced.add(yaw); }
        @Override public void publishHorizonSurvey(HorizonSurveyReport r) { reports.add(r); }
    }

    private final FakeBody body = new FakeBody();
    private MotorStack stack;

    private static Sweep sweep(int sectors, int ticksPer) {
        return new Sweep(Vec3.newBuilder().setX(1).setY(2).setZ(3).build(), sectors, 48f, ticksPer, 0, 0, List.of());
    }

    private static Frame frame(Motor m, Lease l) { return new Frame(1L, 0L, 0, m, l); }

    private Sweep top() { return (Sweep) stack.peek().orElseThrow().motor(); }

    @BeforeEach
    void setUp() { stack = new MotorStack(); }

    @Test
    void fullSweepCollectsSectorsPublishesReportAndPopsCompleted() {
        stack.push(frame(sweep(4, 2), Lease.ofTicks(0, Sweep.leaseTicks(4, 2))), body);
        MotorStack.Popped popped = null;
        int ticks = 0;
        while (popped == null && ticks < 50) {
            body.now = ++ticks;
            popped = stack.tick(body).orElse(null);
        }
        assertNotNull(popped);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, popped.status());
        assertEquals(7, ticks); // 4 sectors * 2 ticks, last scan on tick 7
        assertEquals(List.of(0f, 90f, 180f, 270f), body.faced);
        assertEquals(1, body.reports.size());
        HorizonSurveyReport r = body.reports.get(0);
        assertEquals(4, r.getSectorsCount());
        assertEquals(1.0, r.getOriginX(), 1e-9);
        assertEquals(3.0, r.getOriginZ(), 1e-9);
        assertEquals(48f, r.getMaxRange(), 1e-4);
        assertEquals(7L, r.getCompletedTick());
        assertTrue(stack.isEmpty());
    }

    @Test
    void reflexSuspendsSweepAndItResumesWithProgressIntact() {
        stack.push(frame(sweep(6, 2), Lease.none(0)), body);
        for (int i = 0; i < 5; i++) { body.now++; stack.tick(body); }
        Sweep before = top();
        assertEquals(3, before.currentSector());
        assertEquals(3, before.sectors().size());

        stack.push(frame(new Dodge(3), Lease.none(0)), body);
        assertInstanceOf(Dodge.class, stack.peek().orElseThrow().motor());
        int scansWhileSuspended = body.faced.size();
        stack.snapshot(); // sweep is below the reflex; only the reflex is ticked
        stack.pop(ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null, body);

        assertEquals(scansWhileSuspended, body.faced.size());
        assertEquals(before, top());
        // finishing the remaining sectors still yields all six, in order
        MotorStack.Popped popped = null;
        for (int i = 0; i < 20 && popped == null; i++) { body.now++; popped = stack.tick(body).orElse(null); }
        assertNotNull(popped);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, popped.status());
        assertEquals(6, body.reports.get(0).getSectorsCount());
        assertEquals(List.of(0f, 60f, 120f, 180f, 240f, 300f), body.faced);
    }

    @Test
    void leaseExpiryPopsWithTimeoutAndNoReport() {
        stack.push(frame(sweep(36, 2), Lease.ofTicks(0, 5)), body);
        body.now = 5;
        var popped = stack.tick(body).orElseThrow();
        assertEquals(ReceiptStatus.RECEIPT_STATUS_TIMEOUT, popped.status());
        assertTrue(body.reports.isEmpty());
    }

    @Test
    void sectorsListIsDefensivelyCopiedAndImmutable() {
        List<com.civquest.mod.grpc.v1.HorizonSector> src = new ArrayList<>();
        Sweep s = new Sweep(Vec3.getDefaultInstance(), 4, 48f, 2, 0, 0, src);
        src.add(com.civquest.mod.grpc.v1.HorizonSector.getDefaultInstance());
        assertTrue(s.sectors().isEmpty());
        assertThrows(UnsupportedOperationException.class, () -> s.sectors().add(com.civquest.mod.grpc.v1.HorizonSector.getDefaultInstance()));
    }
}
