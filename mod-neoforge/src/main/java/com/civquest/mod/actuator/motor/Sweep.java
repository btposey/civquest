package com.civquest.mod.actuator.motor;

import com.civquest.mod.grpc.v1.HorizonSector;
import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.Vec3;

import java.util.ArrayList;
import java.util.List;

/**
 * Peripheral 360-degree horizon survey (ADR-032) as a frame-local value record (ADR-036 section 4).
 * All progress (cursor, tick counter, collected sectors) lives here; each tick returns a new Sweep.
 * Completing the final sector publishes the {@link HorizonSurveyReport} through the {@link Body}
 * and pops with COMPLETED.
 */
public record Sweep(Vec3 origin, int numSectors, float maxRange, int ticksPerSector,
                    int currentSector, int tickCounter, List<HorizonSector> sectors) implements Motor {
    public Sweep {
        sectors = sectors == null ? List.of() : List.copyOf(sectors);
    }

    /** Lease budget for a full sweep: scan time plus slack for reflex suspension jitter. */
    public static long leaseTicks(int numSectors, int ticksPerSector) {
        return (long) Math.max(1, numSectors) * Math.max(1, ticksPerSector) + 40L;
    }

    @Override
    public long defaultLeaseTicks() {
        return leaseTicks(numSectors, ticksPerSector);
    }

    @Override
    public Outcome onTick(Body body) {
        if (numSectors <= 0) {
            return Outcome.failed(this, "survey_invalid_sectors", null);
        }
        if (tickCounter % Math.max(1, ticksPerSector) != 0) {
            return Outcome.stay(new Sweep(origin, numSectors, maxRange, ticksPerSector,
                    currentSector, tickCounter + 1, sectors));
        }
        float bearingDeg = (currentSector * 360.0f) / numSectors;
        body.faceBearing(bearingDeg);
        HorizonSector sector = body.scanHorizonSector(origin, currentSector, bearingDeg, maxRange);
        List<HorizonSector> collected = new ArrayList<>(sectors);
        collected.add(sector);
        int next = currentSector + 1;
        Sweep advanced = new Sweep(origin, numSectors, maxRange, ticksPerSector, next, tickCounter + 1, collected);
        if (next >= numSectors) {
            body.publishHorizonSurvey(HorizonSurveyReport.newBuilder()
                    .setOriginX(origin.getX())
                    .setOriginY(origin.getY())
                    .setOriginZ(origin.getZ())
                    .setCompletedTick(body.tick())
                    .setMaxRange(maxRange)
                    .addAllSectors(collected)
                    .build());
            return Outcome.done(advanced);
        }
        return Outcome.stay(advanced);
    }
}
