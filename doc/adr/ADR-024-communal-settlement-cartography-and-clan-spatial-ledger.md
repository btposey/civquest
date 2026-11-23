# ADR-024: Communal Settlement Cartography and Clan Spatial Ledger

## Status
Accepted

## Context
In CivQuest, each agent operates as an independent OS process with a private SQLite database (`map_graph.db`). While process isolation guarantees safety and prevents shared-memory corruption (ADR-004), it creates a collective coordination problem:
1. When a builder constructs a communal shelter, granary, or workshop, other clan members remain completely unaware of the new infrastructure until they personally stumble across it.
2. New agents born into the dynastic clan or returning from long hunting expeditions have empty private mental maps and must rediscover communal facilities from scratch.
3. Multiple agents may construct redundant workstations in adjacent quadrants because they lack a shared settlement ledger of public amenities.

Human settlements in Minecraft function via shared social knowledge: members know where the village well, community farm, smithy, and armory are located. CivQuest requires a clan-level spatial ledger that synchronizes public infrastructure without compromising process-per-agent isolation.

## Decision

### 1. Clan Spatial Ledger Schema (`settlement_pois`)
We introduce a `settlement_pois` table in each clan's shared database `$XDG_DATA_HOME/civquest/clans/<clan_id>/civic_registry.db`:

```sql
CREATE TABLE IF NOT EXISTS settlement_pois (
    settlement_poi_id TEXT PRIMARY KEY,
    settlement_id TEXT NOT NULL,
    category TEXT NOT NULL,         -- 'WORKSTATION', 'GRANARY', 'ARMORY', 'BED_BARRACKS', 'ROAD_NODE'
    block_id TEXT NOT NULL,
    x REAL NOT NULL,
    y REAL NOT NULL,
    z REAL NOT NULL,
    access_x REAL NOT NULL,
    access_y REAL NOT NULL,
    access_z REAL NOT NULL,
    elevation_layer TEXT NOT NULL,
    creator_agent_id TEXT NOT NULL,
    registered_tick INTEGER NOT NULL,
    last_verified_tick INTEGER NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_settlement_pois_settlement ON settlement_pois(settlement_id, category);
```

### 2. Immediate Municipal Registration
When an agent with a civic role (e.g. Builder, Architect, or Armorer) completes a public structure or places a communal workstation in an assigned functional zone (ADR-010, ADR-016), the agent writes the POI directly to `settlement_pois` in `civic_registry.db`.

### 3. Circadian Synchronization at the Tavern
During the evening tavern social routine (ADR-018):
1. Agents read recent entries from `settlement_pois` into their private in-memory `SpatialIndex` and `agent_pois`.
2. Agents share discovered natural resource stands (e.g. large birch forests, clay deposits, coal outcrops) and high-danger sectors.
3. Confirmed destroyed or relocated communal infrastructure is pruned across all attendees' mental maps.

### 4. Starter Cartography for New Agents
When the Dynastic Reproduction Governor (ADR-008) spawns a new agent process, the agent's initialization routine bootstraps its `SpatialIndex` by reading all active communal POIs from `settlement_pois`. The new agent immediately knows the locations of the clan granary, communal crafting tables, and available barracks beds.

## Consequences

### Positive
- Prevents redundant construction of communal workstations.
- Newborn and returning agents immediately navigate to community facilities.
- Reinforces social cohesion and circadian gathering at the tavern.
- Respects multi-process isolation by using SQLite transactions on `civic_registry.db`.

### Negative
- Requires clan-level SQLite writes during civic building completion.
- Agents must filter communal POIs from private personal storage (e.g. private chests remain private in `agent_pois`).
