package com.civquest.mod.actuator;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.PriorityQueue;
import java.util.function.LongPredicate;

/**
 * Pure (world-independent) local walkability planner. Runs A* over a bounded grid around the
 * agent. A move may step up at most one block (needing two blocks of headroom, including above
 * the starting cell for the jump), or drop at most three blocks, and never enters hazard cells.
 *
 * <p>When the target is not reachable, the path leads to the reachable cell closest to the target.
 * If the whole reachable region is closed in (never touches the grid border and never contains
 * the target), {@link Result#enclosed()} is set so the caller can mine out or pillar up.
 */
public final class LocalPathPlanner {
    public static final int DEFAULT_RADIUS = 7; // 15x15 grid
    public static final int MAX_STEP_UP = 1;
    public static final int MAX_DROP = 3;
    private static final int ENCLOSED_MAX_CELLS = 60;

    /** World access abstraction so the planner can be unit tested without a Level. */
    public interface World {
        boolean solid(int x, int y, int z);

        boolean hazard(int x, int y, int z);
    }

    /** A standing cell: feet block coordinates. */
    public record Cell(int x, int y, int z) {
    }

    public record Result(List<Cell> path, boolean reachedTarget, boolean enclosed, int exploredCells) {
        public boolean hasPath() {
            return path != null && !path.isEmpty();
        }
    }

    private LocalPathPlanner() {
    }

    private static final int[][] DIRS = {{1, 0}, {-1, 0}, {0, 1}, {0, -1}};

    private static long key(int x, int y, int z) {
        return ((long) (x & 0x3FFFFFF) << 38) | ((long) (z & 0x3FFFFFF) << 12) | (long) (y & 0xFFF);
    }

    private static final class Node {
        final Cell cell;
        final Node parent;
        final double g;
        double f;

        Node(Cell cell, Node parent, double g, double f) {
            this.cell = cell;
            this.parent = parent;
            this.g = g;
            this.f = f;
        }
    }

    /**
     * Computes the destination Y for walking from (x,y,z) horizontally by (dx,dz); returns
     * {@link Integer#MIN_VALUE} when the move is not allowed.
     */
    static int moveTarget(World w, int x, int y, int z, int dx, int dz) {
        int nx = x + dx;
        int nz = z + dz;
        if (w.solid(nx, y, nz) || w.solid(nx, y + 1, nz)) {
            // Possibly step up one block: need clear dest (y+1,y+2) and headroom above current cell to jump.
            if (w.solid(nx, y + 1, nz) || w.solid(nx, y + 2, nz) || w.solid(x, y + 2, z)) {
                return Integer.MIN_VALUE;
            }
            if (w.hazard(nx, y + 1, nz) || w.hazard(nx, y, nz)) {
                return Integer.MIN_VALUE;
            }
            return y + 1;
        }
        // Level or drop: walk down the column until we find floor.
        for (int ny = y; ny >= y - MAX_DROP; ny--) {
            if (w.solid(nx, ny, nz) || w.solid(nx, ny + 1, nz)) {
                return Integer.MIN_VALUE;
            }
            if (w.hazard(nx, ny, nz) || w.hazard(nx, ny - 1, nz)) {
                return Integer.MIN_VALUE;
            }
            if (w.solid(nx, ny - 1, nz)) {
                return ny;
            }
        }
        return Integer.MIN_VALUE;
    }

    /**
     * @param blocked cells (feet-level keys from {@link #cellKey}) temporarily treated as impassable
     */
    public static Result plan(World w, Cell start, double targetX, double targetZ, int radius, LongPredicate blocked) {
        int tx = (int) Math.floor(targetX);
        int tz = (int) Math.floor(targetZ);
        Map<Long, Node> best = new HashMap<>();
        PriorityQueue<Node> open = new PriorityQueue<>((a, b) -> Double.compare(a.f, b.f));
        Node s = new Node(start, null, 0.0, heuristic(start.x, start.z, tx, tz));
        best.put(key(start.x, start.y, start.z), s);
        open.add(s);

        Node closest = s;
        double closestDist = distSq(start.x, start.z, tx, tz);
        boolean reached = false;
        boolean touchedBorder = false;
        int explored = 0;
        Map<Long, Boolean> closed = new HashMap<>();

        while (!open.isEmpty()) {
            Node cur = open.poll();
            long ck = key(cur.cell.x, cur.cell.y, cur.cell.z);
            if (closed.putIfAbsent(ck, Boolean.TRUE) != null) {
                continue;
            }
            explored++;
            double d = distSq(cur.cell.x, cur.cell.z, tx, tz);
            if (d < closestDist - 1e-9 || (Math.abs(d - closestDist) < 1e-9 && cur.g < closest.g)) {
                closest = cur;
                closestDist = d;
            }
            if (cur.cell.x == tx && cur.cell.z == tz && Math.abs(cur.cell.y - start.y) <= 3 + MAX_DROP) {
                reached = true;
                closest = cur;
                break;
            }
            if (Math.abs(cur.cell.x - start.x) >= radius || Math.abs(cur.cell.z - start.z) >= radius) {
                touchedBorder = true;
                continue;
            }
            for (int[] dir : DIRS) {
                int ny = moveTarget(w, cur.cell.x, cur.cell.y, cur.cell.z, dir[0], dir[1]);
                if (ny == Integer.MIN_VALUE) {
                    continue;
                }
                int nx = cur.cell.x + dir[0];
                int nz = cur.cell.z + dir[1];
                if (blocked != null && blocked.test(cellKey(nx, ny, nz))) {
                    continue;
                }
                long nk = key(nx, ny, nz);
                if (closed.containsKey(nk)) {
                    continue;
                }
                double g = cur.g + 1.0 + (ny != cur.cell.y ? 0.5 : 0.0);
                Node prev = best.get(nk);
                if (prev == null || g < prev.g) {
                    Node n = new Node(new Cell(nx, ny, nz), cur, g, g + heuristic(nx, nz, tx, tz));
                    best.put(nk, n);
                    open.add(n);
                }
            }
        }

        boolean enclosed = !reached && !touchedBorder && explored <= ENCLOSED_MAX_CELLS;
        List<Cell> path = new ArrayList<>();
        for (Node n = closest; n != null && n.parent != null; n = n.parent) {
            path.add(n.cell);
        }
        Collections.reverse(path);
        return new Result(path, reached, enclosed, explored);
    }

    public static long cellKey(int x, int y, int z) {
        return key(x, y, z);
    }

    private static double heuristic(int x, int z, int tx, int tz) {
        return Math.abs(x - tx) + Math.abs(z - tz);
    }

    private static double distSq(int x, int z, int tx, int tz) {
        double dx = x - tx;
        double dz = z - tz;
        return dx * dx + dz * dz;
    }

    /** Unused helper retained for debugging: BFS count of cells reachable from start. */
    static int reachableCount(World w, Cell start, int radius) {
        ArrayDeque<Cell> q = new ArrayDeque<>();
        Map<Long, Boolean> seen = new HashMap<>();
        q.add(start);
        seen.put(key(start.x, start.y, start.z), true);
        while (!q.isEmpty()) {
            Cell c = q.poll();
            if (Math.abs(c.x - start.x) >= radius || Math.abs(c.z - start.z) >= radius) {
                continue;
            }
            for (int[] dir : DIRS) {
                int ny = moveTarget(w, c.x, c.y, c.z, dir[0], dir[1]);
                if (ny == Integer.MIN_VALUE) {
                    continue;
                }
                long k = key(c.x + dir[0], ny, c.z + dir[1]);
                if (seen.putIfAbsent(k, true) == null) {
                    q.add(new Cell(c.x + dir[0], ny, c.z + dir[1]));
                }
            }
        }
        return seen.size();
    }
}
