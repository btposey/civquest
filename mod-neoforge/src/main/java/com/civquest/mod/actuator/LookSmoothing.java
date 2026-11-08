package com.civquest.mod.actuator;

/**
 * Pure helpers for fluid, human-looking head/body/pitch motion: eased and rate-limited angular
 * approach, a hysteresis dead-band against tiny heading changes, and a head-body clamp.
 */
public final class LookSmoothing {
    private LookSmoothing() {
    }

    /** Wraps an angle in degrees to (-180, 180]. */
    public static float wrap(float deg) {
        float d = deg % 360.0F;
        if (d >= 180.0F) {
            d -= 360.0F;
        } else if (d < -180.0F) {
            d += 360.0F;
        }
        return d;
    }

    /**
     * Moves {@code current} toward {@code target} by {@code ease * difference}, limited to
     * {@code maxStep} degrees. Differences smaller than {@code deadband} are ignored (hysteresis).
     */
    public static float approach(float current, float target, float ease, float maxStep, float deadband) {
        float diff = wrap(target - current);
        if (Math.abs(diff) < deadband) {
            return current;
        }
        float step = diff * ease;
        if (step > maxStep) {
            step = maxStep;
        } else if (step < -maxStep) {
            step = -maxStep;
        }
        return current + step;
    }

    /** Pitch (no wrap) low-pass approach with a per-tick step limit. */
    public static float approachLinear(float current, float target, float ease, float maxStep) {
        float step = (target - current) * ease;
        if (step > maxStep) {
            step = maxStep;
        } else if (step < -maxStep) {
            step = -maxStep;
        }
        return current + step;
    }

    /** Clamps {@code body} so it is never more than {@code maxDiff} degrees away from {@code head}. */
    public static float clampBodyToHead(float body, float head, float maxDiff) {
        float diff = wrap(body - head);
        if (diff > maxDiff) {
            return head + maxDiff;
        }
        if (diff < -maxDiff) {
            return head - maxDiff;
        }
        return body;
    }
}
