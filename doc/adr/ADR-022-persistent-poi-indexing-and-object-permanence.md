# ADR-022: Persistent Point-of-Interest (POI) Indexing and Environmental Object Permanence

## Status
Accepted

## Context
In early versions of CivQuest, autonomous agents observed their surroundings strictly through the ephemeral 5 Hz `InterestingBlocks` frustum (~16-block radius). The moment an agent turned its back or walked 20 blocks away, all knowledge of nearby workstations, chests, beds, and tree stands vanished. 

Consequently, agents suffered from a lack of object permanence:
- When needing tools or armor, an agent would fail to remember an existing crafting table 25 blocks away and either craft a redundant table or enter a wandering state.
- Workstations placed directly below in dugout caves ($Y=50$) vs. surface terrain ($Y=64$) could not be distinguished by elevation layer, trapping agents directly above the cave.
- When an agent chopped down a tree or broke a block, nearby agents had no mechanism to maintain a consistent belief state that the object was removed.

Human players in Minecraft maintain active object permanence: they remember where they placed their crafting tables, furnaces, and beds, and they update their mental map when blocks are placed or destroyed. CivQuest requires a dedicated, persistent Point-of-Interest (POI) indexing system that operates within the sub-$5\text{ ms}$ zero-token System 1 budget.

## Decision

### 1. SQLite POI Persistence Schema (`agent_pois`)
We introduce an `agent_pois` table within each agent's `$XDG_DATA_HOME/civquest/agents/<name>/map_graph.db` SQLite database:

```sql
CREATE TABLE IF NOT EXISTS agent_pois (
    poi_id TEXT PRIMARY KEY,
    category TEXT NOT NULL,         -- 'WORKSTATION', 'CONTAINER', 'HABITATION', 'RESOURCE', 'HAZARD'
    block_id TEXT NOT NULL,         -- e.g. 'minecraft:crafting_table', 'minecraft:chest'
    x REAL NOT NULL,
    y REAL NOT NULL,
    z REAL NOT NULL,
    access_x REAL NOT NULL,        -- Walkable adjacent coordinate
    access_y REAL NOT NULL,
    access_z REAL NOT NULL,
    elevation_layer TEXT NOT NULL,  -- 'SURFACE', 'DUGOUT', 'UNDERGROUND', 'ELEVATED'
    safety_score REAL NOT NULL,     -- [0.0 - 1.0]
    discovered_tick INTEGER NOT NULL,
    last_verified_tick INTEGER NOT NULL,
    reliability REAL NOT NULL DEFAULT 1.0, -- Decays on pathing failure / unreachable
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_agent_pois_cat_pos ON agent_pois(category, x, y, z);
CREATE INDEX IF NOT EXISTS idx_agent_pois_layer ON agent_pois(elevation_layer);
```

### 2. In-Memory Spatial Grid Index (`SpatialIndex`)
Disk queries on every 200 ms frame would violate the sub-$5\text{ ms}$ System 1 budget. An in-memory spatial index (`SpatialIndex`) is maintained within `AgentState`:
- Partitions space into $16 \times 16 \times 16$ block grid buckets.
- Provides thread-safe, $O(1)$ spatial lookups (`FindNearestPOI(category, blockID, maxDist, elevationLayer)`).
- Loaded from `agent_pois` on agent startup; mutations are applied to `SpatialIndex` immediately and persisted asynchronously to SQLite.

### 3. Continuous Ingestion and Belief Lifecycle
1. **Passive Ingestion**: Each 5 Hz frame, newly observed workstations, containers, beds, and harvestable resources in `InterestingBlocks` are registered into `SpatialIndex`.
2. **Immediate Placement**: When the agent executes a `PLACE_BLOCK` intent (e.g. placing a crafting table or bed), the block is registered immediately at the target coordinates without waiting for the next sensory frame.
3. **Harvest & Invalidation**: When an agent destroys or harvests a block (e.g. tree log), the POI is immediately pruned from the index.
4. **Line-of-Sight Verification**: If an agent arrives within 3 blocks of a recorded POI coordinate and detects that the block is air or destroyed, `is_active` is set to 0 and the POI is removed.
5. **Reliability Decay**: If navigation to a POI encounters `NAVIGATION_STATE_STUCK`, its `reliability` decays. POIs with low reliability are bypassed in favor of local crafting or alternative workstations.

## Consequences

### Positive
- Agents maintain human-like object permanence: they remember and reuse existing workstations and beds.
- Eliminates redundant tool and workstation crafting.
- Elevation layers prevent agents on the surface from mistaking dugout cave tables as accessible.
- Queries execute in $< 0.1\text{ ms}$ in memory without token overhead.

### Negative
- Requires schema migration in `internal/db/agent_db.go`.
- Memory footprint increases by ~200 KB per agent process for up to 10,000 tracked POIs.
