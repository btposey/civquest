# ADR-023: Breadcrumb Habit Trails and Desire Path Corridors

## Status
Accepted

## Context
In early versions of CivQuest, long-distance navigation between points of interest (POIs) was initiated by emitting a single Euclidean `NavigateIntent(Target: Vec3)`. The mod-side `LocalPathPlanner` only searches a local 16-block radius. When navigating across distances exceeding 20–50 blocks (e.g. from an outpost or tree stand back to the settlement center), agents walked along direct straight-line vectors, frequently colliding with cliffs, falling into ravines, or becoming trapped against natural obstacles.

Human players rarely navigate open wilderness in straight Euclidean lines. Instead, humans form and follow **desire paths (habit trails)**—proven, safe sequences of walkable waypoints that skirt ravines, cross streams at shallow fords, and avoid steep vertical ascents. CivQuest requires a mechanism to record successful traversals and navigate along multi-waypoint corridors.

## Decision

### 1. SQLite Trail Corridor Schema (`trail_corridors`)
We introduce a `trail_corridors` table in `$XDG_DATA_HOME/civquest/agents/<name>/map_graph.db`:

```sql
CREATE TABLE IF NOT EXISTS trail_corridors (
    corridor_id TEXT PRIMARY KEY,
    from_poi_id TEXT NOT NULL,
    to_poi_id TEXT NOT NULL,
    waypoints_json TEXT NOT NULL,      -- JSON array of [x, y, z] triples
    total_distance REAL NOT NULL,
    danger_score REAL NOT NULL,        -- Aggregated hostile risk along trail
    is_paved INTEGER NOT NULL DEFAULT 0,
    traverse_count INTEGER NOT NULL DEFAULT 1,
    last_traversed_tick INTEGER NOT NULL,
    FOREIGN KEY(from_poi_id) REFERENCES agent_pois(poi_id),
    FOREIGN KEY(to_poi_id) REFERENCES agent_pois(poi_id)
);

CREATE INDEX IF NOT EXISTS idx_trail_corridors_endpoints ON trail_corridors(from_poi_id, to_poi_id);
```

### 2. Breadcrumb Recording & Consolidation
1. **Breadcrumb Logging**: While an agent travels between two confirmed POIs, the agent runtime records intermediate positional coordinates every 8–12 blocks whenever horizontal clearance and safe ground are verified.
2. **Corridor Consolidation**: Upon arrival at the target POI without encountering `NAVIGATION_STATE_STUCK` or falling damage, the recorded breadcrumbs are simplified (Ramer-Douglas-Peucker algorithm with $\epsilon = 1.0$) and upserted into `trail_corridors`.
3. **Traverse Reinforcement**: Repeated successful traversals increase `traverse_count`, reinforcing the corridor as a preferred route.

### 3. Corridor Navigation Execution
When an agent selects a destination POI further than 16 blocks away:
1. The agent checks `trail_corridors` for a known path connecting the nearest origin POI to the destination POI.
2. If a corridor exists, System 1 navigates sequentially from waypoint to waypoint:
   $$\text{Current Pos} \longrightarrow W_1 \longrightarrow W_2 \longrightarrow \dots \longrightarrow \text{Destination}$$
3. Only when within 12 blocks of the final waypoint does the agent transition to direct destination arrival.
4. If a corridor is blocked or damaged by terrain changes, the corridor's `danger_score` is increased, triggering fallback to frontier scouting.

### 4. Integration with Road Paving
Heavily traversed corridors (`traverse_count >= 10` and `is_paved == 0`) are flagged as prime candidates for the Road Paving Skill (`RoadPavingSkill` per ADR-018), turning spontaneous desire paths into permanent paved infrastructure.

## Consequences

### Positive
- Prevents agents from colliding with cliffs and terrain barriers during long-distance navigation.
- Natural, human-like movement along established settlement trails.
- Seamlessly bridges autonomous exploratory navigation with civil infrastructure road paving.

### Negative
- Requires waypoint queue tracking in `AgentState`.
- If catastrophic terrain changes occur along a trail (e.g. creeper explosion), the trail must be re-evaluated or repaired.
