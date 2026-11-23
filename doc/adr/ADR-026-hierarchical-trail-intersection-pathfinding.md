# ADR-026: Hierarchical Trail-Intersection Pathfinding

## Status
Accepted

## Context
In early versions of CivQuest, when an agent chose a distant destination (such as surface timber while inside an underground dugout, or an outpost 50 blocks away), System 1 emitted a direct Euclidean `NavigateIntent(Target: destinationVec3)`. 
The mod-side `LocalPathPlanner` only searches a local 7–16 block radius. When the true destination was beyond this radius or obscured behind solid terrain, `LocalPathPlanner` fell back to choosing the reachable cell that minimized 3D Euclidean distance to the destination. 

This created catastrophic navigation failure modes:
1. **Ceiling & Wall Grinding**: Underground agents aiming for surface trees walked straight into solid rock walls and cave ceilings because that was mathematically the closest reachable 3D point.
2. **Vine Clinging Traps**: Agents climbed decorative wall vines toward overhead targets, becoming suspended in mid-air against solid ceilings.
3. **Failure to Use Corridors**: Agents ignored open corridors, stairways, and doorways that momentarily moved away from or perpendicular to the Euclidean vector.

Human players never pathfind through solid obstacles in direct Euclidean vectors. When navigating from an unfamiliar or enclosed space toward a distant destination, a human navigates along locally visible, walkable openings until intersecting an established, proven trail corridor, subsequently following waypoints to the destination.

## Decision

### 1. Two-Tier Hierarchical Navigation Engine
We decompose pathfinding into two distinct cognitive tiers:
1. **Tier 1 (Visible Frontier Probing)**:
   - When an agent is not currently standing on an established trail corridor (`trail_corridors`), System 1 restricts movement strictly to **visible, walkable paths** within local line-of-sight and navigable radius ($R \le 12\text{ m}$).
   - The agent never navigates toward a global Euclidean coordinate that lies through unexcavated solid rock or beyond sensory perception.
   - The immediate goal of frontier probing is to reach the nearest known **Trail Intersection Node** or **Portal POI** (e.g. cave entrance, stairway landing, doorway, or known desire path).
2. **Tier 2 (Corridor Waypoint Following)**:
   - Once the agent intersects an established trail corridor in its `SpatialIndex`, System 1 switches from frontier probing to sequential corridor traversal ($W_0 \to W_1 \dots \to W_k$).
   - Corridor waypoints guarantee proven walkability around terrain obstacles, across rivers, and through cave passageways.
   - Only when the agent reaches the final corridor waypoint within local line-of-sight ($< 12\text{ m}$) of the destination does it transition to local target interaction.

### 2. Elimination of Direct Unreachable Fallback
- `LocalPathPlanner` must never fall back to picking arbitrary 3D cells that terminate into solid rock ceilings or dead-end climbable walls.
- If a target is beyond local navigable reach and no corridor connects the agent's current sector to the target sector, the navigation fails gracefully with `NO_PATH_TO_CORRIDOR`, triggering local exploration rather than wall grinding.

## Consequences

### Positive
- Completely eliminates agents pathfinding into cave ceilings or wall vines when targeting surface objects.
- Forces agents to seek out and use natural exits, stairs, and doorways to transition between elevation layers.
- Produces organic, human-like navigation behavior along established settlement pathways.

### Negative
- Requires maintaining trail corridor connectivity and identifying portal transitions (e.g. cave entrances).
- If an agent is in an unmapped underground cavern, it must explore until discovering a way out before pursuing surface tasks.
