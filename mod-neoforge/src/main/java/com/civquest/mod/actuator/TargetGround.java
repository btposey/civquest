package com.civquest.mod.actuator;

import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;

/**
 * Where a walk should end when its target gives no footing. The brain picks places from what it
 * has seen and remembered, often at the wrong height: in mid-air over a gully, or inside a hill.
 * Play test 7: half of all walks aimed at such a point.
 *
 * <p>A target is kept when it can be stood in, or when it is a block someone could stand beside
 * (a trunk to fell, ore to mine). Otherwise the nearest standing place in the same column is
 * taken: below first for a target in the air or in a tree, above first for one in the ground.
 */
final class TargetGround {
    /** How far up or down the column a standing place is looked for. */
    static final int SEARCH = 32;
    static final int APPROACH_BELOW = 4;

    /** What the resolver needs to know about the blocks. */
    interface Cells {
        boolean solid(BlockPos p);

        /** Water or a climbable: a body can be here without a floor. */
        boolean holds(BlockPos p);

        boolean hazard(BlockPos p);

        /** Part of a tree: its top is not where a walker wants to be. */
        boolean natural(BlockPos p);
    }

    private TargetGround() {
    }

    static boolean standable(Cells cells, BlockPos p) {
        return !cells.solid(p) && !cells.solid(p.above()) && !cells.hazard(p)
                && (cells.holds(p) || cells.solid(p.below()) || cells.holds(p.below()));
    }

    /** The cell to walk to for {@code wanted}; {@code wanted} itself when nothing better exists. */
    static BlockPos resolve(Cells cells, BlockPos wanted) {
        if (standable(cells, wanted) || (cells.solid(wanted) && approachable(cells, wanted))) {
            return wanted;
        }
        int first = cells.solid(wanted) && !cells.natural(wanted) ? 1 : -1;
        for (int sign : new int[] {first, -first}) {
            for (int d = 1; d <= SEARCH; d++) {
                BlockPos at = wanted.above(sign * d);
                if (standable(cells, at)) {
                    return at;
                }
            }
        }
        return wanted;
    }

    /** Whether someone can stand next to {@code block} and work it: from a step above it to
     * {@link #APPROACH_BELOW} blocks under it, the height a walk to a solid block counts as arrived. */
    private static boolean approachable(Cells cells, BlockPos block) {
        for (Direction side : Direction.Plane.HORIZONTAL) {
            for (int dy = -APPROACH_BELOW; dy <= 1; dy++) {
                if (standable(cells, block.relative(side).above(dy))) {
                    return true;
                }
            }
        }
        return false;
    }
}
