package com.civquest.mod.actuator.motor;

/** Sub-tick reflex motors pushed over the top frame (cq-2sr.15). */
public sealed interface Reflex extends Motor permits Combat, Dodge, Flee {
}
