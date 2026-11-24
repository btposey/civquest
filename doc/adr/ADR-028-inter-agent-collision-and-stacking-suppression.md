# ADR-028: Inter-Agent Collision and Stacking Suppression for FakePlayers

## Status
Superseded by [ADR-033](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-033-strict-non-overlapping-entity-physics-and-pathing.md)

## Context
In vanilla Minecraft, player avatars (`ServerPlayer` / `FakePlayer`) participate in mutual entity pushing via `LivingEntity.pushEntities()` and `Entity.push(Entity other)`. 
When multiple autonomous `AgentEntity` instances share enclosed settlement spaces (such as $1 \times 2$ mine shafts, $3 \times 3$ starter dugouts, stairwells, and doorways), this default vanilla collision behavior causes severe simulation pathologies:

1. **Mutual Pushing Locks**: Two agents attempting to pass each other or stand near shared workstation blocks continuously nudge each other out of interaction range.
2. **Head Stacking Traps**: When an agent detects a horizontal obstruction (`horizontalCollision`) while moving toward a waypoint, the navigation controller commands a jump. If another agent is adjacent, the jumping agent lands directly on the second agent's collision bounding box ($1.8\text{ m}$ height), stacking on top of their head and becoming trapped against the ceiling.
3. **Wall and Vine Pinning**: Collision forces from adjacent agents push pathfinding agents into decorative wall blocks (such as hanging vines), triggering unintended climbing states.

Human players on multiplayer servers often experience similar collision congestion, which is commonly resolved in server plugins via no-collision / pass-through mechanics for friendly players.

## Decision

### 1. Inter-Agent Collision Immunity
In `AgentEntity`, we suppress mutual physical pushing between autonomous agents:
- `canCollideWith(Entity other)`: returns `false` if `other instanceof AgentEntity`.
- `canBeCollidedWith()`: returns `false` for collisions against other `AgentEntity` instances.
- `push(Entity other)`: returns immediately as a no-op if `other instanceof AgentEntity`.

### 2. Preservation of External Entity Physics
- Agents continue to collide with and be pushed by external non-agent entities: hostile mobs (Zombies, Skeletons, Creepers), neutral livestock (Cows, Sheep), and human players.
- Environmental collisions (solid blocks, stairs, slabs, ladders, doors) remain fully active.

### 3. Headroom-Aware Jump Suppression
In `AgentNavigator`, jumping during horizontal obstruction is conditionally suppressed if:
- Headroom above the agent is less than $2.2\text{ m}$ (enclosed dugout ceiling), or
- The obstruction is an entity rather than a step-climbable block (slab/stair).

## Consequences

### Positive
- Completely eliminates agents stacking on top of each other's heads in dugouts and corridors.
- Prevents mutual displacement during collective tasks (e.g. gathering around a chest, crafting table, or communal fire).
- Eliminates deadlock in single-block choke points.

### Negative
- Agents can visually overlap or clip through each other when occupying the same block coordinate.
- Multi-agent spatial path deconfliction must rely on high-level waypoint scheduling rather than physical body blocking.
