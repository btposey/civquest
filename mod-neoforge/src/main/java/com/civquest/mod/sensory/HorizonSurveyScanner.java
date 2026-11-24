package com.civquest.mod.sensory;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.HorizonSector;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.tags.FluidTags;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.level.ClipContext;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.BlockHitResult;
import net.minecraft.world.phys.HitResult;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.CollisionContext;

import java.util.Locale;

/**
 * Peripheral 360-degree horizon line-of-sight raycast scanner (ADR-032).
 *
 * <p><b>Bearing / Coordinate Convention</b>:
 * {@code bearingDeg} is expressed in Minecraft's native yaw convention in {@code [0, 360)}:
 * <ul>
 *   <li>{@code 0°}   = South ({@code +Z}): {@code dx = -sin(rad(bearingDeg)) = 0}, {@code dz = cos(rad(bearingDeg)) = +1}</li>
 *   <li>{@code 90°}  = West  ({@code -X}): {@code dx = -1}, {@code dz = 0}</li>
 *   <li>{@code 180°} = North ({@code -Z}): {@code dx = 0},  {@code dz = -1}</li>
 *   <li>{@code 270°} = East  ({@code +X}): {@code dx = +1}, {@code dz = 0}</li>
 * </ul>
 * With vertical pitch {@code pitchDeg} (positive = upward toward canopy, negative = downward toward terrain/water):
 * {@code dx = cos(rad(pitchDeg)) * (-sin(rad(bearingDeg)))},
 * {@code dy = sin(rad(pitchDeg))},
 * {@code dz = cos(rad(pitchDeg)) * cos(rad(bearingDeg))}.
 *
 * <p>Raycasts intentionally do NOT clamp to already-loaded chunks so pioneer agents honestly
 * explore virgin terrain beyond human player presence.
 */
public final class HorizonSurveyScanner {

    public static final float DEFAULT_MAX_RANGE = 64.0f;
    public static final float MIN_MAX_RANGE = 16.0f;
    public static final float MAX_MAX_RANGE = 96.0f;
    public static final float[] FAN_PITCHES_DEG = {-18.0f, -8.0f, 0.0f, 10.0f, 22.0f};

    public static final double OCCLUDED_DISTANCE_THRESHOLD = 14.0;
    public static final double IMMEDIATE_WALL_THRESHOLD = 6.0;
    public static final double LOW_WALL_THRESHOLD = 8.0;
    public static final double RIDGE_ELEVATION_DELTA_THRESHOLD = 1.5;
    public static final float OPEN_HORIZON_RATIO = 0.75f;

    public static final String FEATURE_FOREST_CANOPY = "FOREST_CANOPY";
    public static final String FEATURE_STRUCTURE = "STRUCTURE";
    public static final String FEATURE_WATER = "WATER";
    public static final String FEATURE_STONE_OUTCROP = "STONE_OUTCROP";
    public static final String FEATURE_RIDGE_OBSTRUCTION = "RIDGE_OBSTRUCTION";
    public static final String FEATURE_OPEN_PLAINS = "OPEN_PLAINS";

    private HorizonSurveyScanner() {
    }

    /**
     * Normalizes an arbitrary degree angle into {@code [0, 360)}.
     */
    public static float normalizeBearing(float bearingDeg) {
        float mod = bearingDeg % 360.0f;
        if (mod < 0.0f) {
            mod += 360.0f;
        }
        return mod == 360.0f ? 0.0f : mod;
    }

    /**
     * Clamps {@code maxRange} to safe bounds {@code [16.0f, 96.0f]}, defaulting non-positive values to {@code 64.0f}.
     */
    public static float clampMaxRange(float maxRange) {
        if (maxRange <= 0.0f) {
            return DEFAULT_MAX_RANGE;
        }
        return Math.max(MIN_MAX_RANGE, Math.min(MAX_MAX_RANGE, maxRange));
    }

    /**
     * Computes the horizontal unit direction vector for {@code bearingDeg} in Minecraft's native yaw convention:
     * {@code dx = -sin(rad(bearingDeg))}, {@code dz = cos(rad(bearingDeg))}.
     */
    public static Vec3 bearingToHorizontalDirection(float bearingDeg) {
        double yawRad = Math.toRadians(normalizeBearing(bearingDeg));
        return new Vec3(-Math.sin(yawRad), 0.0, Math.cos(yawRad));
    }

    /**
     * Computes the 3D unit ray direction vector for {@code bearingDeg} and vertical {@code pitchDeg}:
     * {@code dx = cos(pitch) * (-sin(yaw))}, {@code dy = sin(pitch)}, {@code dz = cos(pitch) * cos(yaw)}.
     */
    public static Vec3 bearingAndPitchToDirection(float bearingDeg, float pitchDeg) {
        double yawRad = Math.toRadians(normalizeBearing(bearingDeg));
        double pitchRad = Math.toRadians(pitchDeg);
        double cosPitch = Math.cos(pitchRad);
        return new Vec3(-Math.sin(yawRad) * cosPitch, Math.sin(pitchRad), Math.cos(yawRad) * cosPitch);
    }

    /**
     * Scans a single bearing from the agent's eye position.
     */
    public static HorizonSector scanSector(
            Level level,
            AgentEntity agent,
            int sectorIndex,
            float bearingDeg,
            float maxRange) {
        Vec3 eyePos = agent != null ? agent.getEyePosition() : null;
        if (eyePos == null && agent != null) {
            eyePos = new Vec3(agent.getX(), agent.getY() + 1.62, agent.getZ());
        } else if (eyePos == null) {
            eyePos = Vec3.ZERO;
        }
        return scanSector(level, agent, eyePos, sectorIndex, bearingDeg, maxRange);
    }

    /**
     * Scans a single bearing along a 5-ray vertical fan {@code {-18°, -8°, 0°, +10°, +22°}}
     * from {@code eyePos} out to {@code maxRange} without clamping to already-loaded chunks.
     */
    public static HorizonSector scanSector(
            Level level,
            Entity agent,
            Vec3 eyePos,
            int sectorIndex,
            float bearingDeg,
            float maxRange) {
        float normalizedBearing = normalizeBearing(bearingDeg);
        float clampedRange = clampMaxRange(maxRange);
        Vec3 origin = eyePos != null ? eyePos : Vec3.ZERO;

        if (level == null) {
            Vec3 endPos = origin.add(bearingToHorizontalDirection(normalizedBearing).scale(clampedRange));
            return HorizonSector.newBuilder()
                    .setSectorIndex(sectorIndex)
                    .setBearingDeg(normalizedBearing)
                    .setHitDistance(clampedRange)
                    .setFeatureType(FEATURE_OPEN_PLAINS)
                    .setHitBlockId("minecraft:air")
                    .setHitX(endPos.x)
                    .setHitY(endPos.y)
                    .setHitZ(endPos.z)
                    .setElevationDelta(0.0f)
                    .setOccluded(false)
                    .setOpenHorizon(true)
                    .build();
        }

        RaySample[] samples = new RaySample[FAN_PITCHES_DEG.length];
        RaySample levelZeroSample = null;
        RaySample negEightSample = null;

        for (int i = 0; i < FAN_PITCHES_DEG.length; i++) {
            float pitchDeg = FAN_PITCHES_DEG[i];
            Vec3 dir = bearingAndPitchToDirection(normalizedBearing, pitchDeg);
            Vec3 endPos = origin.add(dir.scale(clampedRange));

            ClipContext clipContext = agent != null
                    ? new ClipContext(origin, endPos, ClipContext.Block.VISUAL, ClipContext.Fluid.ANY, agent)
                    : new ClipContext(origin, endPos, ClipContext.Block.VISUAL, ClipContext.Fluid.ANY, CollisionContext.empty());

            BlockHitResult hitResult = level.clip(clipContext);
            RaySample sample;
            if (hitResult == null || hitResult.getType() != HitResult.Type.BLOCK) {
                sample = new RaySample(
                        pitchDeg,
                        true,
                        clampedRange,
                        endPos,
                        "minecraft:air",
                        false,
                        0.0f,
                        FEATURE_OPEN_PLAINS
                );
            } else {
                BlockPos hitPos = hitResult.getBlockPos();
                Vec3 hitLoc = hitResult.getLocation() != null
                        ? hitResult.getLocation()
                        : Vec3.atCenterOf(hitPos);
                double dist = origin.distanceTo(hitLoc);
                BlockState state = hitPos != null ? level.getBlockState(hitPos) : null;
                String blockId = resolveBlockId(state);
                boolean isWaterFluid = isWaterBlockOrFluid(blockId, state);
                if (isWaterFluid && !blockId.contains("water")) {
                    blockId = "minecraft:water";
                }
                float elevDelta = (float) (hitLoc.y - origin.y);
                String category = classifyHit(blockId, isWaterFluid, pitchDeg, dist, elevDelta);
                sample = new RaySample(
                        pitchDeg,
                        false,
                        dist,
                        hitLoc,
                        blockId,
                        isWaterFluid,
                        elevDelta,
                        category
                );
            }

            samples[i] = sample;
            if (pitchDeg == 0.0f) {
                levelZeroSample = sample;
            } else if (pitchDeg == -8.0f) {
                negEightSample = sample;
            }
        }

        boolean allRaysBlockedUnder6m = true;
        for (RaySample sample : samples) {
            if (sample.missed || sample.isWater || sample.distance >= IMMEDIATE_WALL_THRESHOLD) {
                allRaysBlockedUnder6m = false;
                break;
            }
            if (FEATURE_STRUCTURE.equals(sample.category)) {
                allRaysBlockedUnder6m = false;
                break;
            }
        }

        boolean faceBlockedByOpaqueWall = levelZeroSample != null
                && !levelZeroSample.missed
                && !levelZeroSample.isWater
                && levelZeroSample.distance < IMMEDIATE_WALL_THRESHOLD
                && !FEATURE_FOREST_CANOPY.equals(levelZeroSample.category)
                && !FEATURE_STRUCTURE.equals(levelZeroSample.category);

        boolean occluded = isObstructionSample(levelZeroSample)
                || isObstructionSample(negEightSample);

        boolean levelZeroOpen = levelZeroSample != null
                && (levelZeroSample.missed || levelZeroSample.distance >= OPEN_HORIZON_RATIO * clampedRange);
        boolean openHorizon = !occluded && levelZeroOpen;

        RaySample bestStructure = null;
        RaySample bestForest = null;
        RaySample bestWater = null;
        RaySample bestStone = null;
        RaySample bestRidge = null;

        for (RaySample sample : samples) {
            if (sample.missed) {
                continue;
            }
            switch (sample.category) {
                case FEATURE_STRUCTURE -> {
                    if (!faceBlockedByOpaqueWall || sample.distance < IMMEDIATE_WALL_THRESHOLD) {
                        bestStructure = pickCloser(bestStructure, sample);
                    }
                }
                case FEATURE_FOREST_CANOPY -> {
                    if (!faceBlockedByOpaqueWall && !allRaysBlockedUnder6m) {
                        bestForest = pickCloser(bestForest, sample);
                    }
                }
                case FEATURE_WATER -> {
                    if (sample.pitchDeg <= 0.0f && !faceBlockedByOpaqueWall && !allRaysBlockedUnder6m) {
                        bestWater = pickCloser(bestWater, sample);
                    }
                }
                case FEATURE_STONE_OUTCROP -> {
                    if (!faceBlockedByOpaqueWall && !allRaysBlockedUnder6m) {
                        bestStone = pickCloser(bestStone, sample);
                    }
                }
                case FEATURE_RIDGE_OBSTRUCTION -> {
                    bestRidge = pickCloser(bestRidge, sample);
                }
                default -> {
                }
            }
        }

        String finalFeature;
        RaySample definingSample;

        if (allRaysBlockedUnder6m) {
            finalFeature = FEATURE_RIDGE_OBSTRUCTION;
            definingSample = levelZeroSample != null && !levelZeroSample.missed ? levelZeroSample : samples[0];
            occluded = true;
            openHorizon = false;
        } else if (faceBlockedByOpaqueWall && bestStructure == null) {
            finalFeature = FEATURE_RIDGE_OBSTRUCTION;
            definingSample = levelZeroSample;
            occluded = true;
            openHorizon = false;
        } else if (bestStructure != null) {
            finalFeature = FEATURE_STRUCTURE;
            definingSample = bestStructure;
        } else if (bestForest != null) {
            finalFeature = FEATURE_FOREST_CANOPY;
            definingSample = bestForest;
        } else if (bestWater != null) {
            finalFeature = FEATURE_WATER;
            definingSample = bestWater;
        } else if (bestStone != null) {
            finalFeature = FEATURE_STONE_OUTCROP;
            definingSample = bestStone;
        } else if (occluded && bestRidge != null) {
            finalFeature = FEATURE_RIDGE_OBSTRUCTION;
            definingSample = bestRidge;
        } else if (bestRidge != null) {
            finalFeature = FEATURE_RIDGE_OBSTRUCTION;
            definingSample = bestRidge;
            occluded = true;
            openHorizon = false;
        } else {
            finalFeature = FEATURE_OPEN_PLAINS;
            definingSample = levelZeroSample != null ? levelZeroSample : samples[0];
        }

        return HorizonSector.newBuilder()
                .setSectorIndex(sectorIndex)
                .setBearingDeg(normalizedBearing)
                .setHitDistance((float) definingSample.distance)
                .setFeatureType(finalFeature)
                .setHitBlockId(definingSample.blockId)
                .setHitX(definingSample.hitPos.x)
                .setHitY(definingSample.hitPos.y)
                .setHitZ(definingSample.hitPos.z)
                .setElevationDelta(definingSample.elevDelta)
                .setOccluded(occluded)
                .setOpenHorizon(openHorizon)
                .build();
    }

    private static boolean isObstructionSample(RaySample sample) {
        if (sample == null || sample.missed || sample.isWater) {
            return false;
        }
        if (sample.distance >= OCCLUDED_DISTANCE_THRESHOLD) {
            return false;
        }
        // Downward (-8°) ray hitting flat ground at foot height (elevDelta <= -1.0) is terrain underfoot, not a wall.
        if (sample.pitchDeg < 0.0f && sample.elevDelta <= -1.0f) {
            return false;
        }
        return true;
    }

    private static RaySample pickCloser(RaySample current, RaySample candidate) {
        if (current == null || candidate.distance < current.distance) {
            return candidate;
        }
        return current;
    }

    private static String resolveBlockId(BlockState state) {
        if (state == null || state.isAir()) {
            return "minecraft:air";
        }
        ResourceLocation key = BuiltInRegistries.BLOCK.getKey(state.getBlock());
        return key != null ? key.toString() : "minecraft:air";
    }

    private static boolean isWaterBlockOrFluid(String blockId, BlockState state) {
        String path = extractPath(blockId);
        if ("water".equals(path) || "flowing_water".equals(path)) {
            return true;
        }
        if (state != null) {
            try {
                if (state.getFluidState() != null && state.getFluidState().is(FluidTags.WATER)) {
                    return true;
                }
            } catch (Throwable ignored) {
            }
        }
        return false;
    }

    private static String classifyHit(
            String blockId,
            boolean isWaterFluid,
            float pitchDeg,
            double distance,
            float elevDelta) {
        String path = extractPath(blockId);

        if (isStructurePath(path)) {
            return FEATURE_STRUCTURE;
        }
        if (isForestPath(path)) {
            return FEATURE_FOREST_CANOPY;
        }
        if (isWaterFluid) {
            return FEATURE_WATER;
        }
        if (isStoneOutcropPath(path)) {
            return FEATURE_STONE_OUTCROP;
        }

        // Ridge obstruction: solid obstruction < 14m with elevation_delta >= 1.5, or solid wall < 8m above foot level
        boolean isHighRidge = distance < OCCLUDED_DISTANCE_THRESHOLD && elevDelta >= RIDGE_ELEVATION_DELTA_THRESHOLD;
        boolean isCloseWall = distance < LOW_WALL_THRESHOLD && (pitchDeg >= 0.0f || elevDelta > -1.0f);
        if (isHighRidge || isCloseWall) {
            return FEATURE_RIDGE_OBSTRUCTION;
        }

        return FEATURE_OPEN_PLAINS;
    }

    private static String extractPath(String blockId) {
        if (blockId == null) {
            return "";
        }
        String lower = blockId.toLowerCase(Locale.ROOT);
        int colon = lower.indexOf(':');
        return colon >= 0 ? lower.substring(colon + 1) : lower;
    }

    private static boolean isForestPath(String path) {
        return path.endsWith("_leaves")
                || path.endsWith("_log")
                || path.endsWith("_wood")
                || "mangrove_roots".equals(path)
                || "muddy_mangrove_roots".equals(path);
    }

    private static boolean isStructurePath(String path) {
        return path.endsWith("_planks")
                || "planks".equals(path)
                || path.contains("cobblestone")
                || path.contains("bricks")
                || "crafting_table".equals(path)
                || path.endsWith("_bed")
                || "bed".equals(path)
                || path.contains("campfire")
                || path.contains("lantern")
                || path.contains("torch")
                || path.endsWith("_door")
                || "door".equals(path)
                || path.endsWith("_trapdoor")
                || path.endsWith("_fence")
                || path.endsWith("_fence_gate")
                || "hay_block".equals(path)
                || "chest".equals(path)
                || path.endsWith("_chest")
                || path.contains("furnace")
                || "barrel".equals(path)
                || "bell".equals(path);
    }

    private static boolean isStoneOutcropPath(String path) {
        return "stone".equals(path)
                || "deepslate".equals(path)
                || path.endsWith("_ore")
                || "gravel".equals(path)
                || "andesite".equals(path)
                || "diorite".equals(path)
                || "granite".equals(path)
                || "tuff".equals(path)
                || "calcite".equals(path)
                || path.contains("terracotta");
    }

    private record RaySample(
            float pitchDeg,
            boolean missed,
            double distance,
            Vec3 hitPos,
            String blockId,
            boolean isWater,
            float elevDelta,
            String category) {
    }
}
