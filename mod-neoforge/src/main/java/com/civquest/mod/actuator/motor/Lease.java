package com.civquest.mod.actuator.motor;

/** Frame deadline. {@code deadlineTick == 0} means no deadline. */
public record Lease(long startTick, long deadlineTick) {
    public static Lease none(long startTick) {
        return new Lease(startTick, 0L);
    }

    public static Lease ofTicks(long startTick, long durationTicks) {
        return new Lease(startTick, durationTicks <= 0 ? 0L : startTick + durationTicks);
    }

    public boolean expired(long nowTick) {
        return deadlineTick != 0L && nowTick >= deadlineTick;
    }

    public long ticksElapsed(long nowTick) {
        return Math.max(0L, nowTick - startTick);
    }
}
