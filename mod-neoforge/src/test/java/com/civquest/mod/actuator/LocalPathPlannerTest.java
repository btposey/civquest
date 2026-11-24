package com.civquest.mod.actuator;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.HashSet;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.*;

public class LocalPathPlannerTest {

    /** Flat world, floor top at y=63 (solid for y<=63), plus a set of extra solid blocks. */
    private static LocalPathPlanner.World world(Set<String> solids, Set<String> hazards) {
        return new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                return y <= 63 || solids.contains(x + "," + y + "," + z);
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return hazards.contains(x + "," + y + "," + z);
            }
        };
    }

    private static void wall(Set<String> s, int x, int z, int fromY, int toY) {
        for (int y = fromY; y <= toY; y++) {
            s.add(x + "," + y + "," + z);
        }
    }

    /**
     * 2x3 pit (x in 0..1, z in 0..2) dug 2 deep (floor y=62 inside, i.e. walls at y 63..65 ring around)
     * with one exit: a 1-block step at (2,1) whose top is y=63 (only 1 above pit floor 62... exit cell floor 63).
     */
    private static Set<String> pit() {
        Set<String> solids = new HashSet<>();
        // Dig the pit by removing the floor block y=63 inside; model by making y=63 solid everywhere except pit via solids
        // We instead model the pit as a 2+ tall wall ring at y=64,65 (and 66) around the 2x3 interior,
        // with a 1-block-high exit step at x=2,z=1 (only y=64 solid there).
        for (int x = -1; x <= 2; x++) {
            for (int z = -1; z <= 3; z++) {
                boolean interior = x >= 0 && x <= 1 && z >= 0 && z <= 2;
                if (interior) {
                    continue;
                }
                if (x == 2 && z == 1) {
                    wall(solids, x, z, 64, 64); // 1-block step exit
                } else {
                    wall(solids, x, z, 64, 66);
                }
            }
        }
        return solids;
    }

    @Test
    @DisplayName("Agent in a 2x3 pit with one 1-block step exit finds the exit toward a far target")
    void pitWithSingleStepExit() {
        Set<String> solids = pit();
        // Outer ring beyond the walls is open; target lies beyond the +X wall (2+ tall)
        for (int z = -1; z <= 3; z++) {
            if (z != 1) {
                wall(solids, 3, z, 64, 66);
            }
        }
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(solids, Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 20.5, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(r.hasPath());
        assertFalse(r.enclosed());
        boolean usesExit = r.path().stream().anyMatch(c -> c.x() == 2 && c.z() == 1 && c.y() == 65);
        assertTrue(usesExit, "path must climb the single 1-block step exit: " + r.path());
        // Never plans through a 2-tall wall cell
        for (LocalPathPlanner.Cell c : r.path()) {
            assertFalse(solids.contains(c.x() + ",65," + c.z()) && solids.contains(c.x() + ",66," + c.z()),
                    "path crosses a tall wall at " + c);
        }
    }

    @Test
    @DisplayName("ADR-026: Unreachable target behind solid barrier returns no path (no wall grinding)")
    void unreachableTargetBehindWallReturnsNoPath() {
        Set<String> solids = new HashSet<>();
        for (int z = -20; z <= 20; z++) {
            wall(solids, 3, z, 64, 70); // infinite tall wall to the east
        }
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(solids, Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 10.5, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(r.reachedTarget(), "Target behind solid wall must not be reached");
        assertFalse(r.hasPath(), "ADR-026: Must reject path into solid wall, avoiding wall grinding");
    }

    @Test
    @DisplayName("ADR-026: Distant overhead target from flat ground returns no path without vertical shaft")
    void overheadTargetWithoutShaftReturnsNoPath() {
        // Flat floor at y=64, target high in sky at y=96 (overhead tree stand)
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(Set.of(), Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 10.5, 96.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(r.reachedTarget());
        assertFalse(r.hasPath(), "ADR-026: Flat floor with no climbables must reject distant overhead target");
    }

    @Test
    @DisplayName("ADR-026: Target within local radius inside closed room returns no path (no wall grinding)")
    void localTargetInClosedRoomReturnsNoPath() {
        Set<String> solids = new HashSet<>();
        // Enclose target (3, 64, 0) inside solid walls:
        wall(solids, 2, -1, 64, 66);
        wall(solids, 2, 0, 64, 66);
        wall(solids, 2, 1, 64, 66);
        wall(solids, 4, -1, 64, 66);
        wall(solids, 4, 0, 64, 66);
        wall(solids, 4, 1, 64, 66);
        wall(solids, 3, -1, 64, 66);
        wall(solids, 3, 1, 64, 66);
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(solids, Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 3.5, 64.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(r.reachedTarget(), "Target inside closed room must not be reached");
        assertFalse(r.hasPath(), "ADR-026: Unreachable target within radius must return empty path to prevent wall grinding");
    }

    @Test
    @DisplayName("Fully enclosed region is reported as enclosed")
    void enclosedRegion() {
        Set<String> solids = new HashSet<>();
        for (int x = -1; x <= 2; x++) {
            for (int z = -1; z <= 3; z++) {
                boolean interior = x >= 0 && x <= 1 && z >= 0 && z <= 2;
                if (!interior) {
                    wall(solids, x, z, 64, 66);
                }
            }
        }
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(solids, Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 20.5, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(r.enclosed());
    }

    @Test
    @DisplayName("Drops of up to 3 are allowed, 4 is not; hazards are avoided; failed cells are avoided")
    void dropsHazardsAndFailedCells() {
        // Solid column cells under y=64 but remove floor for a 4-deep hole at x=1
        LocalPathPlanner.World hole = new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                if (x == 1 && z == 0) {
                    return y <= 59;
                }
                return y <= 63;
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };
        assertEquals(Integer.MIN_VALUE, LocalPathPlanner.moveTarget(hole, 0, 64, 0, 1, 0), "4-block drop rejected");
        LocalPathPlanner.World drop3 = new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                return (x == 1 && z == 0) ? y <= 60 : y <= 63;
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };
        assertEquals(61, LocalPathPlanner.moveTarget(drop3, 0, 64, 0, 1, 0));

        Set<String> lava = Set.of("1,63,0", "1,64,0");
        assertEquals(Integer.MIN_VALUE, LocalPathPlanner.moveTarget(world(new HashSet<>(), lava), 0, 64, 0, 1, 0));

        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(new HashSet<>(), Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 3.5, 0.5, LocalPathPlanner.DEFAULT_RADIUS,
                k -> k == LocalPathPlanner.cellKey(1, 64, 0));
        assertTrue(r.reachedTarget());
        assertNotEquals(new LocalPathPlanner.Cell(1, 64, 0), r.path().get(0), "failed direction must be avoided");
    }

    @Test
    @DisplayName("LookSmoothing: deadband, rate limit, head-body clamp")
    void lookSmoothing() {
        assertEquals(10.0f, LookSmoothing.approach(10.0f, 12.0f, 0.5f, 12.0f, 3.0f));
        assertEquals(12.0f, LookSmoothing.approach(0.0f, 170.0f, 0.5f, 12.0f, 3.0f), 1e-4);
        assertEquals(60.0f, LookSmoothing.clampBodyToHead(170.0f, 0.0f, 60.0f), 1e-4);
        assertEquals(6.0f, LookSmoothing.approachLinear(0.0f, 100.0f, 0.5f, 6.0f), 1e-4);
    }

    @Test
    @DisplayName("Climbable shafts allow descending and ascending more than MAX_DROP blocks")
    void climbableShaftDescentAndAscent() {
        // Upper plateau at y=70 (solid y<=69) for x<=1, cliff wall at x=2 (solid y<=75) except
        // for a single climbable ladder shaft at x=2, z=0 (climbable y=64..69, floor y<=63),
        // with doorway walls at x=3, z=0 (y=66..71) leading onto lower floor y=64 (solid y<=63) for x>=3.
        LocalPathPlanner.World shaftWorld = new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                if (x == 2) {
                    return z == 0 ? y <= 63 : y <= 75;
                }
                if (x == 3 && z == 0) {
                    return y <= 63 || (y >= 66 && y <= 71);
                }
                if (x <= 1) {
                    return y <= 69;
                }
                return y <= 63;
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }

            @Override
            public boolean climbable(int x, int y, int z) {
                return x == 2 && z == 0 && y >= 64 && y <= 69;
            }
        };

        // Descend from (0, 70, 0) down the 6-block shaft to (4, 64, 0)
        LocalPathPlanner.Result down = LocalPathPlanner.plan(shaftWorld,
                new LocalPathPlanner.Cell(0, 70, 0), 4.5, 64.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(down.reachedTarget(), "Planner must descend 6-block climbable shaft to target");
        assertFalse(down.enclosed());
        assertTrue(down.path().stream().anyMatch(c -> c.x() == 2 && c.y() == 65 && c.z() == 0),
                "Path must traverse ladder shaft vertically: " + down.path());

        // Ascend from (4, 64, 0) up the 6-block shaft to (0, 70, 0)
        LocalPathPlanner.Result up = LocalPathPlanner.plan(shaftWorld,
                new LocalPathPlanner.Cell(4, 64, 0), 0.5, 70.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(up.reachedTarget(), "Planner must ascend 6-block climbable shaft to target");
        assertFalse(up.enclosed());
        assertTrue(up.path().stream().anyMatch(c -> c.x() == 2 && c.y() == 67 && c.z() == 0),
                "Path must climb ladder shaft vertically: " + up.path());
    }

    @Test
    @DisplayName("Planner prefers dry land over plunging into deep water (>2 blocks) unless target is in water")
    void deepWaterAvoidedUnlessTargetInWater() {
        // Deep water pool (4 blocks deep: y=60..63 water, floor at y=59) at x in [2..4], z in [-1..1]
        // Dry land surrounds it at z=2 and z=-2 (floor y<=63)
        LocalPathPlanner.World pondWorld = new LocalPathPlanner.World() {
            private boolean inPool(int x, int z) {
                return x >= 2 && x <= 4 && z >= -1 && z <= 1;
            }

            @Override
            public boolean solid(int x, int y, int z) {
                if (inPool(x, z)) {
                    return y <= 59;
                }
                return y <= 63;
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }

            @Override
            public boolean water(int x, int y, int z) {
                return inPool(x, z) && y >= 60 && y <= 63;
            }
        };

        // 1. Target on dry land across the deep pond -> must route around on dry land
        LocalPathPlanner.Result dryRoute = LocalPathPlanner.plan(pondWorld,
                new LocalPathPlanner.Cell(0, 64, 0), 6.5, 64.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(dryRoute.reachedTarget());
        assertTrue(dryRoute.path().stream().noneMatch(c -> LocalPathPlanner.isDeepWater(pondWorld, c.x(), c.y(), c.z())),
                "Path to dry-land target must prefer dry land over deep water: " + dryRoute.path());

        // 2. Target inside the deep pond -> allowed to swim to target
        LocalPathPlanner.Result waterTargetRoute = LocalPathPlanner.plan(pondWorld,
                new LocalPathPlanner.Cell(0, 64, 0), 3.5, 63.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(waterTargetRoute.reachedTarget(),
                "Path to an in-water target must still reach the water cell");
    }

    @Test
    @DisplayName("Distant plateau target in lower hollow (|dy|=7) returns partial path to ridge edge, then descends")
    void distantPlateauDescentToHollowProducesPartialPath() {
        // Upper plateau at y=86 (floor y<=85) for x <= 8, stepping down 1 block per X from x=9..15 down to y=79 (floor y<=78)
        LocalPathPlanner.World ridgeWorld = new LocalPathPlanner.World() {
            private int floorY(int x) {
                if (x <= 8) {
                    return 85;
                }
                if (x >= 15) {
                    return 78;
                }
                return 85 - (x - 8);
            }

            @Override
            public boolean solid(int x, int y, int z) {
                return y <= floorY(x);
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };

        // 1. From (0, 86, 0), the slope is beyond DEFAULT_RADIUS=7, so border is still at y=86;
        //    must still emit a partial path toward the ridge edge reducing horizontal distance.
        LocalPathPlanner.Result plateauStep = LocalPathPlanner.plan(ridgeWorld,
                new LocalPathPlanner.Cell(0, 86, 0), 15.5, 79.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(plateauStep.reachedTarget());
        assertTrue(plateauStep.hasPath(), "Plateau descent must emit partial path toward ridge edge");
        LocalPathPlanner.Cell borderEnd = plateauStep.path().get(plateauStep.path().size() - 1);
        assertEquals(7, borderEnd.x(), "Partial path must advance to +X border toward hollow");
        assertEquals(86, borderEnd.y());

        // 2. Once at (8, 86, 0), the descending slope enters local radius and reaches (15, 79, 0)
        LocalPathPlanner.Result descent = LocalPathPlanner.plan(ridgeWorld,
                new LocalPathPlanner.Cell(8, 86, 0), 15.5, 79.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(descent.reachedTarget(), "From ridge edge, planner must descend slope to hollow target");
        assertTrue(descent.hasPath());
    }

    @Test
    @DisplayName("ADR-026: Directly overhead or below unreachable target with small horizontal distance rejects partial path")
    void localBelowUnreachableTargetReturnsNoPath() {
        // Solid plateau floor at y<=85 everywhere (no way down to hollow/cave at y=79)
        LocalPathPlanner.World sealedRoof = new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                return y <= 85 && y >= 81;
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };

        // Target directly below within local radius (dx=2, dy=-7) -> must reject partial path
        LocalPathPlanner.Result localBelow = LocalPathPlanner.plan(sealedRoof,
                new LocalPathPlanner.Cell(0, 86, 0), 2.5, 79.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(localBelow.reachedTarget());
        assertFalse(localBelow.hasPath(), "Unreachable target below solid roof within local radius must return empty path");

        // Target overhead within local radius (dx=2, dy=+8) -> must reject partial path
        LocalPathPlanner.Result localAbove = LocalPathPlanner.plan(sealedRoof,
                new LocalPathPlanner.Cell(0, 86, 0), 2.5, 94.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertFalse(localAbove.reachedTarget());
        assertFalse(localAbove.hasPath(), "Unreachable overhead target within local radius must return empty path");
    }

    @Test
    @DisplayName("Uphill approach across flat ground to elevated solid tree trunk reaches 4-adjacent cell")
    void uphillApproachToElevatedSolidTreeTrunkReachesAdjacentCell() {
        // Reproduces morning playtest topology: flat at y=87 (floor y<=86) for z<=8, rising 1 block/Z from z=9..14 to floor y=92,
        // with a solid oak trunk column at (-35, 93..96, 14).
        LocalPathPlanner.World forestHill = new LocalPathPlanner.World() {
            private int floorY(int z) {
                if (z <= 8) {
                    return 86;
                }
                if (z >= 14) {
                    return 92;
                }
                return 86 + (z - 8);
            }

            @Override
            public boolean solid(int x, int y, int z) {
                if (x == -35 && z == 14 && y >= 93 && y <= 96) {
                    return true;
                }
                return y <= floorY(z);
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };

        LocalPathPlanner.Cell cur = new LocalPathPlanner.Cell(-32, 87, 1);
        double targetX = -34.5; // floor(-34.5) == -35
        double targetY = 94.0;
        double targetZ = 14.5;

        // First plan from spawn (dy=+7 > 4, flat within DEFAULT_RADIUS=7) must emit non-empty partial path
        LocalPathPlanner.Result first = LocalPathPlanner.plan(forestHill, cur, targetX, targetY, targetZ,
                LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(first.hasPath(), "Uphill approach with supported target must emit non-empty partial path");

        // Follow local plans iteratively until arrival
        boolean reached = false;
        for (int step = 0; step < 40; step++) {
            LocalPathPlanner.Result r = LocalPathPlanner.plan(forestHill, cur, targetX, targetY, targetZ,
                    LocalPathPlanner.DEFAULT_RADIUS, null);
            if (r.reachedTarget() && !r.hasPath()) {
                reached = true;
                break;
            }
            assertTrue(r.hasPath(), "Step " + step + " from " + cur + " must not stall with empty path");
            cur = r.path().get(r.path().size() - 1);
            if (r.reachedTarget()) {
                reached = true;
                break;
            }
        }
        assertTrue(reached, "Agent must climb slope and reach adjacent to solid tree trunk; ended at " + cur);
        assertEquals(1, Math.abs(cur.x() - (-35)) + Math.abs(cur.z() - 14),
                "Final cell must be 4-adjacent to solid trunk at (-35, 14): " + cur);
    }

    @Test
    @DisplayName("Local solid column target inside radius plans to 4-adjacent walkable cell")
    void localSolidColumnTargetReachesAdjacentCell() {
        Set<String> solids = new HashSet<>();
        wall(solids, 4, 0, 64, 67); // 4-tall solid trunk at (4, 64..67, 0)
        LocalPathPlanner.Result r = LocalPathPlanner.plan(world(solids, Set.of()),
                new LocalPathPlanner.Cell(0, 64, 0), 4.5, 65.0, 0.5, LocalPathPlanner.DEFAULT_RADIUS, null);
        assertTrue(r.reachedTarget(), "Solid column within radius must be reachable via 4-adjacent cell");
        assertTrue(r.hasPath());
        LocalPathPlanner.Cell end = r.path().get(r.path().size() - 1);
        assertEquals(1, Math.abs(end.x() - 4) + Math.abs(end.z()), "End cell must be 4-adjacent to trunk: " + end);
    }

    @Test
    @DisplayName("Long-distance 80-block traversal across plains, uphill ridge, and valley reaches target")
    void longDistanceUnevenTerrainTraversal() {
        LocalPathPlanner.World rollingWorld = new LocalPathPlanner.World() {
            private int floorY(int x) {
                if (x <= 25) return 63;
                if (x <= 35) return 63 + (x - 25); // climbs to 73
                if (x <= 50) return 73;
                if (x <= 56) return 73 - (x - 50); // drops to 67
                return 67;
            }

            @Override
            public boolean solid(int x, int y, int z) {
                return y <= floorY(x);
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return false;
            }
        };

        LocalPathPlanner.Cell cur = new LocalPathPlanner.Cell(0, 64, 0);
        boolean reached = false;
        for (int iter = 0; iter < 30; iter++) {
            LocalPathPlanner.Result r = LocalPathPlanner.plan(rollingWorld, cur, 80.5, 68.0, 0.5,
                    LocalPathPlanner.DEFAULT_RADIUS, null);
            assertTrue(r.hasPath() || r.reachedTarget(), "Iteration " + iter + " from " + cur + " returned empty path");
            if (r.hasPath()) {
                cur = r.path().get(r.path().size() - 1);
            }
            if (r.reachedTarget()) {
                reached = true;
                break;
            }
        }
        assertTrue(reached, "Agent must traverse 80 blocks of uneven terrain to (80, 68, 0); ended at " + cur);
        assertEquals(80, cur.x());
        assertEquals(68, cur.y());
    }
}
