package com.civquest.mod.actuator.motor;

/**
 * Typed motor activity (ADR-036 section 4). Each record holds exactly its own progress, so a
 * suspended frame resumes with its progress intact. Hooks receive only a {@link Body}: they must
 * never reach a locking AgentMotorFSM method. The current motor state of an agent is simply
 * {@code stack.peek().motor()}; there is no separate state enum.
 */
public sealed interface Motor permits Move, Mine, Place, UseItem, Sleep, Sweep, Act, Strike, Reflex {
    /** Called once when the frame first becomes top. May finish immediately (start failure / instant completion). */
    default Outcome onEnter(Body body) {
        return Outcome.stay(this);
    }

    /** Called every 20 Hz tick while the frame is on top. */
    default Outcome onTick(Body body) {
        return Outcome.stay(this);
    }

    /** Called when another frame is pushed over this (entered) frame; returns the motor to keep (progress captured). */
    default Motor onSuspend(Body body) {
        return this;
    }

    /** Called when the frame above pops and this suspended frame is top again. */
    default Outcome onResume(Body body) {
        return Outcome.stay(this);
    }

    /** Called when an entered frame leaves the stack for any reason (pop, timeout, clear). */
    default void onExit(Body body) {
    }

    /** Lease length used when the batch carries no timeout; 0 means no deadline. */
    default long defaultLeaseTicks() {
        return 0L;
    }
}
