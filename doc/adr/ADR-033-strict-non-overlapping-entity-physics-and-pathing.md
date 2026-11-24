# ADR-033: Strict Non-Overlapping Entity Physics, Entity-Aware Pathing, and Spawn Deconfliction

## Status
Accepted (Supersedes [ADR-028](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-028-inter-agent-collision-and-stacking-suppression.md))

## Context
[ADR-028](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-028-inter-agent-collision-and-stacking-suppression.md) suppressed inter-agent collisions in `AgentEntity` (`canCollideWith(Entity other)` returning `false` for `AgentEntity`, `canBeCollidedWith()` returning `false`, and `push(Entity other)` returning as a no-op for `AgentEntity`) to avoid mutual pushing locks and head-stacking traps.

However, pass-through physics violated a fundamental simulation invariant: **player models (`AgentEntity`) must NEVER occupy the same space as another agent, human player, or mob**. In practice, collision suppression caused multiple agents to merge into identical coordinates around chests, crafting tables, beds, and corridors, creating visually broken overlapping avatars and failing to trigger local obstacle avoidance. Meanwhile, `LocalPathPlanner` had no awareness of entity-occupied cells, so A* planned paths straight through standing entities, and `AgentEntityManager` lacked comprehensive spawn-coordinate deconfliction against existing entities and hazards.

## Decision

### 1. Core Physical Invariant
An `AgentEntity` avatar must never occupy the same horizontal collision volume (AABB) as another alive, non-spectator `AgentEntity`, `ServerPlayer`, or `Mob`. Physical separation is enforced across all four layers: entity physics, local path planning, motor navigation, and spawn placement.

### 2. Restore Standard Entity Collision and Pushing in `AgentEntity`
In `mod-neoforge/src/main/java/com/civquest/mod/AgentEntity.java`:
- Remove the `other instanceof AgentEntity` bypasses in `canCollideWith(Entity other)` and `push(Entity other)`.
- Remove the unconditional `canBeCollidedWith()` `false` override so standard `ServerPlayer` / `LivingEntity` physical collision and pushing apply uniformly across `AgentEntity`, `ServerPlayer`, and `Mob` instances.
- Retain **hazard/cliff-safe active depenetration** in `AgentEntity` tick handling: if an external entity (such as a human player or mob) walks into a stationary agent and creates AABB overlap, the agent applies a bounded horizontal separation impulse away from the overlapping entity's center—**only** onto walkable, non-hazardous adjacent cells (never pushing the agent into lava, fire, cactus, void, or cliffs $>1$ block drop without solid footing).

### 3. Entity-Aware `LocalPathPlanner`
In `mod-neoforge/src/main/java/com/civquest/mod/actuator/LocalPathPlanner.java`:
- Extend `LocalPathPlanner.World` with `occupiedByEntity(int x, int y, int z)` (defaulting to `false` for unit-test world stubs that do not override it).
- Wire `AgentNavigator.worldOf(Level)` to query alive, non-spectator `AgentEntity`, `ServerPlayer`, and `Mob` bounding boxes expanded to the target cell footprint `(x, y, z)` (excluding the navigating agent itself).
- Update `LocalPathPlanner.moveTarget(...)` / A* neighbor expansion so entity-occupied cells are treated as non-traversable (except when an entity occupies the immediate start cell during active depenetration), causing A* to route *around* occupied spaces rather than through them.

### 4. Hard AABB Non-Overlap Clamping, Lateral Sidestep & Jump-on-Head Suppression in `AgentNavigator`
In `mod-neoforge/src/main/java/com/civquest/mod/actuator/AgentNavigator.java`:
- **`tickNative()` Horizontal Non-Overlap Clamping**: Before applying a horizontal step, project the agent's candidate AABB against nearby alive non-spectator `AgentEntity`, `ServerPlayer`, and `Mob` AABBs. Reject or clamp any movement component that would reduce horizontal separation below combined half-widths (non-overlapping AABB constraint).
- **Lateral Sidestep**: When forward movement toward a waypoint is blocked by an entity rather than terrain, attempt a hazard-checked perpendicular sidestep (`left`/`right` relative to heading) or trigger an immediate `LocalPathPlanner` replan around the updated `occupiedByEntity` grid.
- **Jump-on-Head Suppression (Preserved from ADR-028)**: `AgentNavigator` must **never** jump onto another entity's head. Jump commands during `horizontalCollision` remain strictly suppressed when the obstruction is caused by an entity (rather than a step-climbable block) or when overhead clearance is $<2.2\text{ m}$.

### 5. Spawn-Coordinate Deconfliction in `AgentEntityManager`
In `mod-neoforge/src/main/java/com/civquest/mod/AgentEntityManager.java`:
- Both default grid spawning and explicit `(x, y, z)` spawn requests must pass through a unified spawn-site validator/deconfliction search.
- Candidate spawn coordinates are checked against:
  1. Solid/suffocating blocks at feet and head height, with solid supporting floor beneath;
  2. Environmental hazards (lava, fire, magma, powder snow, cactus, deep water, sheer drops);
  3. Existing alive non-spectator `AgentEntity`, `ServerPlayer`, and `Mob` AABBs within minimum separation distance.
- If the requested or grid coordinate fails any check, spiral outward in the local neighborhood to select the nearest safe, unoccupied standable coordinate.

## Consequences

### Positive
- Eliminates overlapping/merged agent models around workstations, beds, doorways, and spawn points.
- A* proactively steers around standing players, mobs, and other agents instead of walking into their hitboxes and stalling.
- Preserves ADR-028's elimination of head-stacking traps without sacrificing physical body solidity.
- Prevents newly spawned agents from spawning inside existing entities, walls, or hazards.

### Negative / Trade-offs
- Narrow $1$-block corridors occupied by a stationary entity can temporarily block passage until the occupant moves or a flanking route exists, requiring higher-level navigation timeouts/unstuck handling when a corridor is completely sealed.
- Per-plan entity AABB queries in `AgentNavigator.worldOf(Level)` add minor overhead during local A* replans, mitigated by caching entity occupancy snapshots per plan invocation alongside block lookups.
