package com.civquest.mod.actuator.motor;

/**
 * State definitions for the Layer 1A In-Engine Motor Actuator Finite State Machine.
 * Governs the active physical action lifecycle of an {@link com.civquest.mod.AgentEntity}
 * on the 20 Hz server tick.
 */
public enum MotorState {
    /** Agent is idle and receptive to new physical actions. */
    IDLE,

    /** Agent is actively executing voxel or Baritone pathfinding. */
    MOVING,

    /** Agent is accumulating destroy progress on a target block via BlockBreaker. */
    MINING,

    /** Agent is placing blocks, paving roads, or constructing signposts. */
    BUILDING,

    /** Agent is consuming food, drinking, or charging an item (e.g. shield/bow). */
    USING_ITEM,

    /** Agent is engaged in combat reflexes, attacking or defending. */
    COMBAT_ENGAGED,

    /** Agent is resting in a bed. */
    SLEEPING,

    /** Agent is executing unstuck routines after pathfinding obstruction. */
    STUCK_RECOVERY
}
