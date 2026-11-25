package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.MotorFacts;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.ReflexNotice;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import com.mojang.logging.LogUtils;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.projectile.Projectile;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import org.slf4j.Logger;

import java.util.List;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Thin per-agent owner of the typed {@link MotorStack} (ADR-036 section 4): ticks it, owns the sub-tick
 * reflex pushes (Combat/Dodge/Flee/emergency UseItem) and turns popped batch frames into the one receipt
 * per batch (status, reason, {@link MotorFacts}). The agent's motor state is {@code stack.peek().motor()};
 * there is no state enum and no batch cursor: a failure or lease timeout pops the rest of its batch.
 */
public class AgentMotorFSM {
    private static final Logger LOGGER = LogUtils.getLogger();

    public static final int COMBAT_TIMEOUT_TICKS = Combat.TIMEOUT_TICKS; // 3 seconds of combat disengagement
    /** Maximum number of nested reflex frames (Combat/Dodge/Flee/UseItem) over the interrupted frame. */
    public static final int MAX_STACK_DEPTH = 4;
    /** Locally generated (reflex) frame ids start here, far above Go-assigned Frame.ID values. */
    public static final long LOCAL_FRAME_ID_BASE = 1L << 40;
    private static final double PROJECTILE_THREAT_RADIUS = 6.0;
    private static final double PROJECTILE_MISS_THRESHOLD_SQ = 4.0; // 2.0 blocks

    private final AgentEntity agent;
    private final MotorStack motorStack = new MotorStack();
    private final AtomicLong localFrameIds = new AtomicLong(LOCAL_FRAME_ID_BASE);
    private volatile long tickCounter = 0L;
    private volatile long lastProcessedSequenceId = 0L;
    /** Receipt of the last settled batch (null until one settles or after flush). */
    private volatile IntentReceipt terminal = null;
    private volatile int pushedBatchCount = 0;
    private volatile boolean timedOut = false;
    private volatile boolean directFailed = false;

    public AgentMotorFSM(AgentEntity agent) {
        this.agent = agent;
        this.motorStack.setListener(this::onFramePopped);
    }

    public MotorStack getMotorStack() {
        return motorStack;
    }

    public AgentEntity getAgent() {
        return agent;
    }

    /** The motor of the top frame, if any: the agent's current motor state. */
    public Optional<Motor> topMotor() {
        return motorStack.peek().map(Frame::motor);
    }

    /** The motor clock: world game time when the agent is in a level, else the FSM's own 20 Hz counter. */
    public long nowTick() {
        net.minecraft.world.level.Level level = agent != null ? agent.level() : null;
        return level != null ? level.getGameTime() : tickCounter;
    }

    public long getLastProcessedSequenceId() {
        return lastProcessedSequenceId;
    }

    public void markSequenceProcessed(long sequenceId) {
        if (sequenceId > 0) {
            this.lastProcessedSequenceId = sequenceId;
        }
    }

    // ---- queries over the stack ----------------------------------------------------------------

    /** Number of reflex frames (Combat/Dodge/Flee/emergency UseItem) currently suspending the interrupted frame. */
    public int getStackDepth() {
        int n = 0;
        for (Frame f : motorStack.snapshot()) {
            if (isSuspender(f.motor())) {
                n++;
            }
        }
        return n;
    }

    private static boolean isSuspender(Motor m) {
        return m instanceof Reflex || (m instanceof UseItem u && u.emergency());
    }

    private static boolean isInstant(Motor m) {
        return m instanceof Act || m instanceof Place;
    }

    /** The reflex frame on top of the motor stack, if one is preempting. */
    public Optional<Motor> activeReflex() {
        return topMotor().filter(AgentMotorFSM::isSuspender);
    }

    /** True while a horizon Sweep is the active (top, unsuspended) motor frame. */
    public boolean isSweeping() {
        return topMotor().filter(m -> m instanceof Sweep).isPresent();
    }

    /** True if any Sweep frame is on the motor stack, even when suspended under a reflex. */
    public boolean hasSweepFrame() {
        return motorStack.snapshot().stream().anyMatch(f -> f.motor() instanceof Sweep);
    }

    /** True if a Sleep frame is on the stack (even when suspended). */
    public boolean isSleeping() {
        return motorStack.snapshot().stream().anyMatch(f -> f.motor() instanceof Sleep);
    }

    /** No motor frame of any kind is executing. */
    public boolean isMotorIdle() {
        return motorStack.isEmpty();
    }

    /** True while a batch has at least one frame on the stack (reflexes and direct frames do not count). */
    public boolean hasBatchFrames() {
        return motorStack.snapshot().stream().anyMatch(f -> f.index() >= 0 && !isSuspender(f.motor()));
    }

    /** True if a routine frame (Mine/Move/Sweep) is on top and can be preempted by a reflex. */
    private boolean hasPreemptableFrame() {
        return topMotor().filter(m -> m instanceof Move || m instanceof Mine || m instanceof Sweep).isPresent();
    }

    /** Checks if a navigation intent targets the same voxel as the topmost Move frame. */
    public boolean isSameNavigationTarget(NavigateIntent nav) {
        Vec3 current = motorStack.snapshot().stream()
                .map(Frame::motor).filter(m -> m instanceof Move).map(m -> ((Move) m).target())
                .findFirst().orElse(null);
        if (nav == null || !nav.hasTarget()) {
            return current == null;
        }
        if (current == null) {
            return false;
        }
        double eps = 0.5;
        return Math.abs(nav.getTarget().getX() - current.getX()) < eps
                && Math.abs(nav.getTarget().getY() - current.getY()) < eps
                && Math.abs(nav.getTarget().getZ() - current.getZ()) < eps;
    }

    // ---- receipts ------------------------------------------------------------------------------

    /** Records the settled receipt of a batch (also used for empty/chat-only batches). */
    public void recordReceipt(long batchId, long frameId, int index, ReceiptStatus status, String reason, MotorFacts facts) {
        IntentReceipt.Builder b = IntentReceipt.newBuilder()
                .setBatchId(batchId)
                .setFrameId(frameId)
                .setActiveIndex(index)
                .setStatus(status)
                .setFailureReason(reason != null ? reason : "");
        if (facts != null) {
            b.setFacts(facts);
        }
        terminal = b.build();
    }

    /** True once if a batch ended with a lease TIMEOUT since the last call (the actuator drops its queue). */
    public boolean takeTimedOut() {
        boolean t = timedOut;
        timedOut = false;
        return t;
    }

    /**
     * Receipt snapshot: EXECUTING (PREEMPTED_BY_REFLEX under a reflex) with live facts while a batch has
     * frames on the stack, otherwise the last settled batch receipt.
     */
    public IntentReceipt getCurrentReceipt() {
        int depth = getStackDepth();
        List<Frame> frames = motorStack.snapshot();
        Frame batchFrame = null;
        for (Frame f : frames) {
            if (f.index() >= 0 && !isSuspender(f.motor())) {
                batchFrame = f;
                break;
            }
        }
        if (batchFrame != null) {
            IntentReceipt.Builder b = IntentReceipt.newBuilder()
                    .setBatchId(batchFrame.batchId())
                    .setFrameId(batchFrame.frameId())
                    .setActiveIndex(batchFrame.index())
                    .setStatus(depth > 0 ? ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX
                            : ReceiptStatus.RECEIPT_STATUS_EXECUTING)
                    .setMotorStackDepth(depth);
            if (depth == 0 && batchFrame.motor() instanceof Move && agent != null && agent.getNavigator() != null) {
                MotorFacts live = agent.getNavigator().snapshotFacts();
                if (live != null) {
                    b.setFacts(live);
                }
            }
            return b.build();
        }
        IntentReceipt t = terminal;
        return (t != null ? t.toBuilder() : IntentReceipt.newBuilder()).setMotorStackDepth(depth).build();
    }

    private void onFramePopped(MotorStack.Popped p) {
        Frame f = p.frame();
        Motor m = f.motor();
        if (isSuspender(m)) {
            emitNotice(m, preemptedFrameId(motorStack.peek()), true,
                    p.reason().isEmpty() ? String.valueOf(p.status()) : p.reason());
            return;
        }
        boolean failed = p.status() == ReceiptStatus.RECEIPT_STATUS_FAILED
                || p.status() == ReceiptStatus.RECEIPT_STATUS_TIMEOUT;
        if (f.index() < 0) {
            directFailed |= failed;
            return;
        }
        if (p.status() == ReceiptStatus.RECEIPT_STATUS_COMPLETED) {
            boolean more = motorStack.snapshot().stream().anyMatch(o -> sameBatch(o, f) && !isSuspender(o.motor()));
            if (!more) {
                recordReceipt(f.batchId(), f.frameId(), Math.max(pushedBatchCount, f.index() + 1),
                        ReceiptStatus.RECEIPT_STATUS_COMPLETED, "", null);
            }
        } else if (failed) {
            String reason = p.reason();
            if (p.status() == ReceiptStatus.RECEIPT_STATUS_TIMEOUT) {
                reason = "Batch timed out after " + f.lease().ticksElapsed(nowTick()) + " ticks";
                timedOut = true;
            } else if (reason.isBlank()) {
                String label = m instanceof Act a ? a.label() : m instanceof Place pl ? pl.label() : m.getClass().getSimpleName();
                reason = "Primitive at index " + f.index() + " (" + label + ") failed";
            }
            recordReceipt(f.batchId(), f.frameId(), f.index(), p.status(), reason, p.facts());
            motorStack.popWhile(o -> sameBatch(o, f) && !isSuspender(o.motor()), "batch_aborted", body());
        }
    }

    private static boolean sameBatch(Frame a, Frame b) {
        return a.index() >= 0 && a.batchId() == b.batchId() && a.frameId() == b.frameId();
    }

    // ---- pushing frames ------------------------------------------------------------------------

    /** Pushes one batch's frames (first element runs first). Returns false when the stack is too deep. */
    public synchronized boolean pushBatch(List<Frame> frames, int intentCount) {
        pushedBatchCount = intentCount;
        boolean ok = motorStack.pushAll(frames, body());
        settle();
        return ok;
    }

    /**
     * Pushes a single non-batch frame (direct intent). Returns false if it could not be pushed or
     * failed in the tick it started.
     */
    public synchronized boolean pushDirect(Motor motor) {
        directFailed = false;
        Frame f = new Frame(0L, 0L, -1, motor, Lease.ofTicks(nowTick(), motor.defaultLeaseTicks()));
        boolean ok = motorStack.push(f, body());
        settle();
        return ok && !directFailed;
    }

    /** Runs instant frames (Act/Place) that reached the top so batch chaining needs no extra tick. */
    public synchronized void settle() {
        for (int i = 0; i < 512; i++) {
            Optional<Motor> top = topMotor();
            if (top.isEmpty() || !isInstant(top.get())) {
                return;
            }
            motorStack.tick(body());
        }
    }

    /**
     * Pushes a reflex (or emergency use-item) frame over the top frame; the interrupted frame is
     * suspended through its own {@link Motor#onSuspend}. Emits a pushed {@link ReflexNotice}.
     *
     * @return false if the nesting limit {@link #MAX_STACK_DEPTH} is hit
     */
    public synchronized boolean pushReflex(Motor reflex, String reason) {
        if (reflex == null || !isSuspender(reflex)) {
            return false;
        }
        if (getStackDepth() >= MAX_STACK_DEPTH) {
            LOGGER.warn("Agent '{}' reflex stack is at capacity ({}); rejecting {}",
                    agent != null ? agent.getAgentId() : "unknown", MAX_STACK_DEPTH, reflex);
            return false;
        }
        long preempted = preemptedFrameId(motorStack.peek());
        long batchId = motorStack.peek().map(Frame::batchId).orElse(0L);
        Frame frame = new Frame(batchId, localFrameIds.incrementAndGet(), -1, reflex,
                Lease.ofTicks(nowTick(), reflexLeaseTicks(reflex)));
        if (!motorStack.push(frame, body())) {
            return false;
        }
        emitNotice(reflex, preempted, false, reason);
        return true;
    }

    private static long reflexLeaseTicks(Motor m) {
        return switch (m) {
            case Combat c -> Combat.TIMEOUT_TICKS + 20L;
            case Dodge d -> d.ticksRemaining() + 20L;
            case Flee f -> f.ticksRemaining() + 20L;
            default -> 400L; // emergency use-item
        };
    }

    private static long preemptedFrameId(Optional<Frame> below) {
        return below.map(Frame::frameId).orElse(0L);
    }

    private void emitNotice(Motor motor, long preemptedFrameId, boolean popped, String details) {
        if (agent == null || !(motor instanceof Reflex)) {
            return;
        }
        ReflexNotice.ReflexType type = switch (motor) {
            case Dodge d -> ReflexNotice.ReflexType.REFLEX_TYPE_DODGE;
            case Flee f -> ReflexNotice.ReflexType.REFLEX_TYPE_FLEE;
            default -> ReflexNotice.ReflexType.REFLEX_TYPE_COMBAT;
        };
        agent.recordReflexNotice(ReflexNotice.newBuilder()
                .setReflexType(type)
                .setTick(agent.tickCount)
                .setDetails(details != null ? details : "")
                .setPreemptedFrameId(preemptedFrameId)
                .setPopped(popped)
                .build());
    }

    /**
     * Immediately flushes the stack: every frame is dropped ABORTED (hooks release breaking, navigation,
     * item use and sleep), and the settled receipt is cleared.
     */
    public synchronized void flush() {
        motorStack.clear("flush", body());
        if (agent != null) {
            if (agent.getBreaker() != null) {
                agent.getBreaker().cancel();
            }
            if (agent.getNavigator() != null) {
                agent.getNavigator().stop();
            }
            if (agent.isUsingItem()) {
                agent.stopUsingItem();
            }
            if (agent.isSleeping()) {
                agent.stopSleeping();
            }
        }
        terminal = null;
        timedOut = false;
    }

    /** Lock-free {@link Body} over this FSM's agent clocked by the FSM's own tick counter. */
    private Body body() {
        return agent != null ? new AgentBody(agent, this::nowTick) : new Body() {
            public long tick() { return nowTick(); }
            public double x() { return 0; }
            public double y() { return 0; }
            public double z() { return 0; }
            public void log(String message) { }
        };
    }

    /**
     * Evaluates whether an incoming intent can preempt the current top frame. Priority/emergency actions
     * interrupt routine frames; sleep yields only to emergencies and an explicit wake.
     */
    public boolean canPreempt(AgentIntent intent) {
        if (intent == null) {
            return false;
        }
        boolean isEmergency = isPriorityOrEmergencyIntent(intent);
        if (activeReflex().filter(m -> m instanceof Reflex).isPresent()) {
            return isEmergency;
        }
        if (topMotor().filter(m -> m instanceof Sleep).isPresent()) {
            return isEmergency
                    || (intent.hasInteractBlock() && intent.getInteractBlock().getInteractionType() == InteractBlockIntent.InteractionType.WAKE_UP);
        }
        return true;
    }

    /** Whether an intent is an emergency/priority action. Routine sprint navigation is NOT. */
    public boolean isPriorityOrEmergencyIntent(AgentIntent intent) {
        if (intent == null) {
            return false;
        }
        if (intent.hasAttack()) {
            return true;
        }
        if (intent.hasUseItem()) {
            UseItemIntent use = intent.getUseItem();
            return use.getEquipArmorOrOffhand() || use.getIsEatingOrDrinking();
        }
        return false;
    }

    /** True if the agent has a routine frame that an emergency use-item may preempt (or is already preempted). */
    public boolean canEmergencyPreempt() {
        return hasPreemptableFrame() || getStackDepth() > 0;
    }

    // ---- reflexes ------------------------------------------------------------------------------

    /**
     * Pushes a {@link Combat} frame over a Mine/Move/Sweep when an immediate physical hazard is
     * detected. If a Combat frame or an engaged Strike is on top it is refreshed (timeout restarts).
     */
    public synchronized boolean preemptWithCombatReflex(String reason) {
        if (refreshCombat(-1)) {
            return false;
        }
        if (!hasPreemptableFrame() && getStackDepth() == 0) {
            return false;
        }
        boolean pushed = pushReflex(new Combat(0, 0), reason);
        if (pushed) {
            engageReflexShield();
        }
        return pushed;
    }

    /** Pushes a {@link Dodge} frame (incoming projectile) over a Mine/Move/Sweep. */
    public synchronized boolean preemptWithDodgeReflex(String reason) {
        if (!hasPreemptableFrame()) {
            return false;
        }
        boolean pushed = pushReflex(new Dodge(Dodge.DEFAULT_TICKS), reason);
        if (pushed) {
            engageReflexShield();
        }
        return pushed;
    }

    /** Pushes a {@link Flee} frame retreating from {@code from} over the top frame. */
    public synchronized boolean preemptWithFleeReflex(Vec3 from, String reason) {
        if (!hasPreemptableFrame() && getStackDepth() == 0) {
            return false;
        }
        return pushReflex(new Flee(from, Flee.DEFAULT_TICKS), reason);
    }

    /** Restarts the timeout of a Combat/Strike frame already on top; returns true if one was. */
    private boolean refreshCombat(int targetEntityId) {
        Optional<Frame> top = motorStack.peek();
        if (top.isPresent() && top.get().motor() instanceof Combat c) {
            motorStack.refreshTop(new Combat(0, targetEntityId >= 0 ? targetEntityId : c.targetEntityId()),
                    Lease.ofTicks(nowTick(), reflexLeaseTicks(c)));
            return true;
        }
        if (top.isPresent() && top.get().motor() instanceof Strike s) {
            motorStack.refreshTop(new Strike(s.action(), 0), top.get().lease());
            return true;
        }
        return false;
    }

    private void engageReflexShield() {
        if (agent == null || !agent.isAlive()) {
            return;
        }
        try {
            ItemStack offhand = agent.getItemInHand(InteractionHand.OFF_HAND);
            boolean hasShield = offhand != null && offhand.is(Items.SHIELD);
            if (!hasShield && agent.getInventory() != null) {
                for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                    ItemStack item = agent.getInventory().getItem(i);
                    if (item != null && item.is(Items.SHIELD)) {
                        agent.setItemInHand(InteractionHand.OFF_HAND, item);
                        agent.getInventory().setItem(i, offhand != null ? offhand : ItemStack.EMPTY);
                        hasShield = true;
                        break;
                    }
                }
            }
            if (hasShield) {
                agent.startUsingItem(InteractionHand.OFF_HAND);
                agent.recordReflexNotice(ReflexNotice.newBuilder()
                        .setReflexType(ReflexNotice.ReflexType.REFLEX_TYPE_SHIELD_DEFENSE)
                        .setTick(agent.tickCount)
                        .setDetails("MotorPDA reflex shield defense engaged")
                        .build());
            }
        } catch (Throwable ignored) {
        }
    }

    private boolean checkIncomingProjectileHazard() {
        if (agent == null || agent.level() == null || !agent.isAlive()) {
            return false;
        }
        try {
            List<Projectile> projectiles = agent.level().getEntitiesOfClass(
                    Projectile.class,
                    agent.getBoundingBox().inflate(PROJECTILE_THREAT_RADIUS),
                    p -> p != null && p.isAlive() && p.getOwner() != agent
            );
            if (projectiles == null || projectiles.isEmpty()) {
                return false;
            }
            net.minecraft.world.phys.Vec3 agentCenter = new net.minecraft.world.phys.Vec3(
                    agent.getX(), agent.getY() + 0.9, agent.getZ()
            );
            for (Projectile proj : projectiles) {
                net.minecraft.world.phys.Vec3 vel = proj.getDeltaMovement();
                if (vel == null || vel.lengthSqr() < 0.04) {
                    continue;
                }
                net.minecraft.world.phys.Vec3 toAgent = agentCenter.subtract(proj.position());
                double dot = toAgent.dot(vel);
                if (dot <= 0.0) {
                    continue;
                }
                double t = dot / vel.lengthSqr();
                net.minecraft.world.phys.Vec3 closestPoint = proj.position().add(vel.scale(t));
                if (closestPoint.distanceToSqr(agentCenter) <= PROJECTILE_MISS_THRESHOLD_SQ) {
                    return preemptWithDodgeReflex("arrow_trajectory_interception");
                }
            }
        } catch (Throwable ignored) {
        }
        return false;
    }

    /** Server-thread 20 Hz tick: projectile hazard check, then one step of the motor stack. */
    public synchronized void tick() {
        tickCounter++;
        if (getStackDepth() == 0 && hasPreemptableFrame()) {
            checkIncomingProjectileHazard();
        }
        motorStack.tick(body());
        settle();
    }
}
