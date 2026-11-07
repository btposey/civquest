# ADR-009: Baritone API Pathfinding Wrapper

## Status
Accepted

## Context
Minecraft navigation across uneven terrain, caves, mountains, rivers, and man-made structures is computationally challenging. Standard vanilla entity pathfinding (`PathNavigation`) struggles with multi-block obstacles, vertical scaffolding, parkour jumps, and dynamic obstacle clearance. Implementing a ground-up voxel A* pathfinder would require extensive development and maintenance.

## Decision
We integrate the **Baritone API** within `mod-neoforge`:
- Wrap Baritone's `ICustomGoalProcess` to drive `SettlerEntity` instances using `GoalNear` and `GoalBlock`.
- Expose movement parameters over `NavigateIntent` (e.g., `speed_multiplier`, `sprint`, `allow_break_blocks`, `allow_place_scaffolding`, `arrival_tolerance`).
- Configure Baritone settings dynamically per intent (disabling breaking/placing during peaceful transit, enabling them during survival panic or construction).

## Consequences
### Positive
- Leverages the battle-tested, high-performance pathfinding, parkour, and voxel traversal capabilities of Baritone.
- Offloads complex 3D block traversal, scaffolding placement, and hazard avoidance from the Go agent.

### Negative / Trade-offs
- Creates an external mod dependency on Baritone for NeoForge.
- Tying navigation to Baritone requires tracking compatibility across Minecraft/NeoForge version updates.
