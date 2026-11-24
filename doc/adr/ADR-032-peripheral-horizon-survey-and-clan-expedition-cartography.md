# ADR-032: Peripheral 360° Horizon Survey FSM, Shelter-Gated Bootstrap Circadian Rhythm & Clan Expedition Cartography

## Status
Accepted

## Context
Playtest analysis following Epic 14 (`7142e8d`) revealed three architectural limitations preventing pioneer agents from authentically exploring the world and founding settlements:

1. **Short-Range Sensory Blindness**: `SensoryFrustumCollector` performs an isotropic 16-block 3D scan (`BLOCK_SCAN_RADIUS = 16`, `MAX_INTERESTING_BLOCKS = 128`) to stay fast at 5 Hz. Any forest, river, cliff, or village beyond 16 blocks is completely invisible to agents even across wide-open terrain where a human player sees it immediately. Additionally, eager insertion of non-log blocks before `logCandidates` could starve nearby logs out of the 128-block cap.
2. **Premature Circadian Lockout in `BOOTSTRAP`**: `BootstrapStateHandler` routed all `tod in [11000, 23500)` ticks exclusively to `shelter.go`, suppressing timber harvesting, crafting, and exploration. Agents without logs returned `nil` and stood idle all night instead of actively securing basic survival needs and shelter.
3. **Lack of Structured Regional Reconnaissance & Clan Site Consensus**: Without long-range horizon vision or an explicit expedition-and-rendezvous cycle, agents remained clustered near their starting coordinates rather than fanning out to survey surrounding regions, pooling discoveries into a collective clan map, and choosing a strong settlement site together.

## Decision

### 1. Peripheral 360° Horizon Survey FSM (`MotorState.SURVEYING`)
We introduce an on-demand peripheral sensory sweep on `AgentMotorFSM`'s LIFO pushdown stack ([ADR-015](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-015-in-engine-peripheral-reflexes-and-afferent-telemetry.md), [ADR-017](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-017-hierarchical-timed-pushdown-automaton-with-preemptive-state-stack.md)):
- **Intent**: `SurveyHorizonIntent(num_sectors=36, max_range=64, ticks_per_sector=2)` (default 36 bearings at 10° increments, 64-block visible horizon configurable up to 96 blocks, ~3.6 seconds per full 360° sweep).
- **Physical Avatar Turning**: As each bearing is scanned, the periphery rotates the `AgentEntity`'s head and body (`yRot`, `yHeadRot`, `yBodyRot`) to face the active bearing and broadcasts entity rotation packets so observing players clearly see the avatar sweeping the horizon.
- **Line-of-Sight Raycasting Without Artificial Chunk Clamping**: Each bearing fires a vertical fan of `level.clip(ClipContext.Block.VISUAL)` rays from the agent's eye position out to `max_range`. Raycasts are **not** clamped to pre-loaded chunks—chunks load/generate naturally as agents honestly explore virgin wilderness beyond human player presence, with cost amortized across the 3.6-second sweep.
- **Obstruction & Feature Classification**: Rays stop at opaque obstructions and classify each of the 36 bearings (`FOREST_CANOPY` for `*_leaves`/`*_log`, `WATER`, `STONE_OUTCROP`, `STRUCTURE`, `RIDGE_OBSTRUCTION`, `OPEN_PLAINS`), recording hit distance, hit block ID, hit coordinates, elevation delta, and occlusion status.
- **Preemptibility & Completion**: Combat reflexes and emergency hazards immediately preempt `SURVEYING` via the MotorPDA stack. On completion, `SURVEYING` pops to `IDLE` and delivers a compact 36-sector `HorizonSurveyReport` on the `TelemetryFrame` stream.

### 2. Chained Reconnaissance Loop (`SURVEY -> CHOOSE_BEARING -> TRAVERSE -> SURVEY`)
When `HorizonSurveyReport` arrives in the Go orchestrator:
- All 36 sectors are persisted to `map_graph.db` (`horizon_sectors`) and distant resource/landmark bearings (`FOREST_CANOPY`, `WATER`, `STONE_OUTCROP`, `STRUCTURE`) are indexed as low-confidence distant POIs in `SpatialIndex` / `agent_pois`.
- A concise compass summary (`[LATEST HORIZON SURVEY]`) is exposed to System 2 contemplation and dialogue prompts.
- System 1 scores the 36 bearings deterministically by current need (e.g., `FOREST_CANOPY` when timber is needed, `WATER` + flat `OPEN_PLAINS` when scouting settlement sites) or picks a random unexplored open bearing if nothing of interest is visible, traverses 32–48 blocks along that bearing, and chains another 360° survey from the new vantage point.
- **Unexpected Obstruction Pause & Re-Survey**: If a minor obstacle lies within the proximal local A* bubble (`LocalPathPlanner`), the periphery routes around it reflexively without interrupting the traverse. When an unexpected larger barrier (such as a wide ravine or sheer cliff) halts traversal outside the proximal bubble, the explorer records the barrier as a `HAZARD` POI, **pauses at the rim to execute a fresh 360° Horizon Survey**, and reasons over the fresh rim-level sectors to select an unobstructed flanking or alternate bearing (with a minimum-distance guard to prevent repeated surveys from the exact same spot).

### 3. Multi-Member Clan Rendezvous (`clan_size > 1`) vs. Solo Homestead (`clan_size == 1`)
- **Multi-Member Clan (`clan_size > 1`)**:
  - Clan explorers fan out along distinct azimuth wedges so peers map complementary sectors.
  - After completing an outbound expedition leg (2–3 survey hops, or spotting a high-suitability candidate site, or low vitals), the explorer physically returns within conversational range ($\le 20\text{ m}$) of the clan's starting rendezvous centroid.
  - Upon arrival, their personal surveyed sectors and POIs merge into the clan's collective spatial ledger (`clan` DB, [ADR-024](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-024-communal-settlement-cartography-and-clan-spatial-ledger.md)), and they emit a local chat summary ([ADR-030](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-030-local-inter-agent-discourse.md)) sharing findings with nearby peers.
  - Once collective survey coverage identifies a strong candidate site (timber + buildable terrain + water/stone), the clan selects the highest-scoring site from the merged collective map to establish its settlement.
- **Solo Explorer (`clan_size == 1`)**:
  - Solo explorers have **no** rendezvous return obligation. They roam freely chaining `SURVEY -> TRAVERSE -> SURVEY` across the world until their personal map identifies a location meeting homestead resource suitability criteria, where they stake out and build their own homestead directly.

### 4. Shelter-Gated Circadian Transition in `BOOTSTRAP`
Agents in `BOOTSTRAP` do not enter passive circadian evening/sleep idling until basic shelter has been established. During night ticks without shelter, `BOOTSTRAP` agents remain task-driven—harvesting timber, crafting prerequisites via the dynamic Recipe Graph ([ADR-029](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-029-data-driven-runtime-recipe-resolution.md)), surveying/exploring if resources are absent, and building shelter—while still yielding to active combat and hunger survival reflexes.

## Consequences

### Positive
- Agents perceive distant forests, rivers, ridges, and plains out to 64–96 blocks using realistic line-of-sight vision rather than an arbitrary 16-block bubble.
- Players observe natural scouting behavior as avatars pause, visibly turn 360° to survey, march purposefully down chosen bearings, and return to camp to report findings to clanmates.
- Information locality is preserved: clan maps grow organically through physical return and conversation at the rendezvous rather than global telepathy.
- Solo explorers enjoy full frontier autonomy to discover and claim their own homestead.

### Negative
- Generating virgin chunks along distant bearings incurs modest server CPU/IO during exploration sweeps, mitigated by amortizing rays across 2 ticks per sector (~3.6s per full rotation).
- Adds a new `horizon_sectors` table to per-agent and per-clan SQLite spatial databases.
