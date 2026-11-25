package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.MotorFacts;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Optional;
import java.util.function.Consumer;
import java.util.function.Predicate;

/**
 * Per-agent stack of typed motor {@link Frame}s (ADR-036 section 4), mirroring the Go htpda kernel:
 * pushing over an entered frame suspends it (its motor record is kept, with progress captured by
 * {@link Motor#onSuspend}), pop/replace/timeout remove a frame and report a {@link Popped}, and only the
 * top frame is ticked and subject to its lease. A frame is entered lazily the first time it is top
 * ({@link Motor#onEnter}) and resumed ({@link Motor#onResume}) when the frame above it pops.
 *
 * <p>Locking: all mutation is guarded by this stack's own monitor, but {@link Motor} hooks and the
 * pop listener are invoked with the monitor released and hooks receive only a {@link Body}, so a hook
 * can never re-enter a locking method of this class or of AgentMotorFSM.
 */
public final class MotorStack {
    public static final int DEFAULT_MAX_DEPTH = 64;

    /** A frame that left the stack, with the receipt it should produce. */
    public record Popped(Frame frame, ReceiptStatus status, String reason, MotorFacts facts) {
    }

    private enum Phase { NEW, ACTIVE, SUSPENDED }

    private static final class Slot {
        Frame frame;
        Phase phase = Phase.NEW;

        Slot(Frame frame) {
            this.frame = frame;
        }
    }

    private final Deque<Slot> slots = new ArrayDeque<>();
    private final List<Popped> history = new ArrayList<>();
    private final int maxDepth;
    private volatile Consumer<Popped> listener = p -> { };

    public MotorStack() {
        this(DEFAULT_MAX_DEPTH);
    }

    public MotorStack(int maxDepth) {
        this.maxDepth = maxDepth;
    }

    /** Called after every pop (listener runs with the stack monitor released, before the next frame is entered). */
    public void setListener(Consumer<Popped> listener) {
        this.listener = listener != null ? listener : p -> { };
    }

    public synchronized int depth() {
        return slots.size();
    }

    public synchronized boolean isEmpty() {
        return slots.isEmpty();
    }

    public synchronized Optional<Frame> peek() {
        Slot s = slots.peek();
        return s == null ? Optional.empty() : Optional.of(s.frame);
    }

    /** Top-first snapshot. */
    public synchronized List<Frame> snapshot() {
        List<Frame> out = new ArrayList<>(slots.size());
        for (Slot s : slots) {
            out.add(s.frame);
        }
        return out;
    }

    /** Pushes over the current top (which is suspended). Returns false if the depth limit is hit. */
    public boolean push(Frame frame, Body body) {
        return pushAll(List.of(frame), body);
    }

    /**
     * Pushes {@code frames} so that the first element ends on top (execution order); only that frame
     * is entered. Returns false (pushing nothing) if the depth limit would be exceeded.
     */
    public boolean pushAll(List<Frame> frames, Body body) {
        if (frames.isEmpty()) {
            return true;
        }
        Slot below;
        synchronized (this) {
            if (slots.size() + frames.size() > maxDepth) {
                return false;
            }
            below = slots.peek();
        }
        if (below != null && below.phase == Phase.ACTIVE) {
            Motor kept = below.frame.motor().onSuspend(body);
            synchronized (this) {
                below.frame = below.frame.withMotor(kept);
                below.phase = Phase.SUSPENDED;
            }
        }
        synchronized (this) {
            for (int i = frames.size() - 1; i >= 0; i--) {
                slots.push(new Slot(frames.get(i)));
            }
        }
        enterTop(body);
        return true;
    }

    /** Pops the top frame with the given status; the frame below (if any) resumes or enters. */
    public Optional<Popped> pop(ReceiptStatus status, String reason, MotorFacts facts, Body body) {
        Optional<Popped> popped = popTop(status, reason, facts, body);
        if (popped.isPresent()) {
            enterTop(body);
        }
        return popped;
    }

    /** Pops top frames while they match (ABORTED, no frame below is entered until all are gone). */
    public void popWhile(Predicate<Frame> match, String reason, Body body) {
        while (true) {
            Optional<Frame> top = peek();
            if (top.isEmpty() || !match.test(top.get())) {
                break;
            }
            popTop(ReceiptStatus.RECEIPT_STATUS_ABORTED, reason, null, body);
        }
        enterTop(body);
    }

    /** Pops the top frame and pushes {@code frame} in the same slot (same depth). */
    public Optional<Popped> replace(Frame frame, ReceiptStatus status, String reason, Body body) {
        Optional<Popped> popped = popTop(status, reason, null, body);
        if (popped.isEmpty()) {
            return popped;
        }
        synchronized (this) {
            slots.push(new Slot(frame));
        }
        enterTop(body);
        return popped;
    }

    /** Rewrites the top frame's motor and lease in place (no pop, no hooks); used to refresh a reflex. */
    public synchronized boolean refreshTop(Motor motor, Lease lease) {
        Slot top = slots.peek();
        if (top == null) {
            return false;
        }
        top.frame = top.frame.withMotor(motor).withLease(lease);
        return true;
    }

    /** Drops every frame (death, flush), reporting each as ABORTED top-first. */
    public List<Popped> clear(String reason, Body body) {
        List<Popped> out = new ArrayList<>();
        List<Slot> removed = new ArrayList<>();
        synchronized (this) {
            Slot s;
            while ((s = slots.poll()) != null) {
                removed.add(s);
                Popped p = new Popped(s.frame, ReceiptStatus.RECEIPT_STATUS_ABORTED, reason == null ? "" : reason, null);
                out.add(p);
                record(p);
            }
        }
        for (int i = 0; i < removed.size(); i++) {
            if (removed.get(i).phase != Phase.NEW) {
                removed.get(i).frame.motor().onExit(body);
            }
            listener.accept(out.get(i));
        }
        return out;
    }

    /**
     * One 20 Hz step: expire the top frame's lease (pop with TIMEOUT), otherwise run its
     * {@code onTick} and apply the outcome. Returns the frame popped this tick, if any.
     */
    public Optional<Popped> tick(Body body) {
        enterTop(body);
        Slot top;
        synchronized (this) {
            top = slots.peek();
        }
        if (top == null) {
            return Optional.empty();
        }
        if (top.frame.lease().expired(body.tick())) {
            return pop(ReceiptStatus.RECEIPT_STATUS_TIMEOUT, "lease_expired", null, body);
        }
        Outcome out = top.frame.motor().onTick(body);
        synchronized (this) {
            if (slots.peek() != top) {
                return Optional.empty(); // stack changed under the hook's caller; drop the stale outcome
            }
            top.frame = top.frame.withMotor(out.next());
        }
        if (out.finished()) {
            return pop(out.status(), out.reason(), out.facts(), body);
        }
        return Optional.empty();
    }

    /** Drains frames popped since the last call. */
    public synchronized List<Popped> drainPopped() {
        List<Popped> copy = List.copyOf(history);
        history.clear();
        return copy;
    }

    private Optional<Popped> popTop(ReceiptStatus status, String reason, MotorFacts facts, Body body) {
        Slot top;
        Popped popped;
        synchronized (this) {
            top = slots.poll();
            if (top == null) {
                return Optional.empty();
            }
            popped = new Popped(top.frame, status, reason == null ? "" : reason, facts);
            record(popped);
        }
        if (top.phase != Phase.NEW) {
            top.frame.motor().onExit(body);
        }
        listener.accept(popped);
        return Optional.of(popped);
    }

    private void record(Popped p) {
        if (history.size() >= 64) {
            history.remove(0);
        }
        history.add(p);
    }

    /** Enters (NEW) or resumes (SUSPENDED) the top frame until an active frame is on top. */
    private void enterTop(Body body) {
        while (true) {
            Slot s;
            Phase was;
            synchronized (this) {
                s = slots.peek();
                if (s == null || s.phase == Phase.ACTIVE) {
                    return;
                }
                was = s.phase;
                s.phase = Phase.ACTIVE;
            }
            Outcome out = was == Phase.NEW ? s.frame.motor().onEnter(body) : s.frame.motor().onResume(body);
            synchronized (this) {
                if (slots.peek() != s) {
                    return;
                }
                s.frame = s.frame.withMotor(out.next());
            }
            if (!out.finished()) {
                return;
            }
            popTop(out.status(), out.reason(), out.facts(), body);
        }
    }
}
