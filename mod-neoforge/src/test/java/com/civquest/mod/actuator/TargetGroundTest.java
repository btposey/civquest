package com.civquest.mod.actuator;

import static org.junit.jupiter.api.Assertions.assertEquals;

import net.minecraft.core.BlockPos;
import org.junit.jupiter.api.Test;

class TargetGroundTest {
    /** Flat ground whose top solid block is at y=63, plus whatever a case adds. */
    private static TargetGround.Cells flat(java.util.function.Predicate<BlockPos> extraSolid,
            java.util.function.Predicate<BlockPos> trees) {
        return new TargetGround.Cells() {
            @Override
            public boolean solid(BlockPos p) {
                return p.getY() <= 63 || extraSolid.test(p) || trees.test(p);
            }

            @Override
            public boolean holds(BlockPos p) {
                return false;
            }

            @Override
            public boolean hazard(BlockPos p) {
                return false;
            }

            @Override
            public boolean natural(BlockPos p) {
                return trees.test(p);
            }
        };
    }

    private static final java.util.function.Predicate<BlockPos> NONE = p -> false;

    @Test
    void midAirTargetDropsToTheGround() {
        assertEquals(new BlockPos(3, 64, 3), TargetGround.resolve(flat(NONE, NONE), new BlockPos(3, 75, 3)));
    }

    @Test
    void buriedTargetRisesToTheSurface() {
        assertEquals(new BlockPos(3, 64, 3), TargetGround.resolve(flat(NONE, NONE), new BlockPos(3, 59, 3)));
    }

    @Test
    void standableAndApproachableTargetsAreKept() {
        BlockPos open = new BlockPos(3, 64, 3);
        assertEquals(open, TargetGround.resolve(flat(NONE, NONE), open));
        BlockPos trunk = new BlockPos(0, 64, 0);
        assertEquals(trunk, TargetGround.resolve(flat(NONE, p -> p.getX() == 0 && p.getZ() == 0 && p.getY() < 70), trunk));
    }

    @Test
    void highLogSendsTheWalkerToTheFootOfTheTree() {
        java.util.function.Predicate<BlockPos> trunk = p -> p.getX() == 0 && p.getZ() == 0 && p.getY() >= 66 && p.getY() < 72;
        assertEquals(new BlockPos(0, 64, 0), TargetGround.resolve(flat(NONE, trunk), new BlockPos(0, 70, 0)));
    }
}
