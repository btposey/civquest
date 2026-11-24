# ADR-031: Idle-Triggered System 2 Strategic Directive Bridge & LLM Contention Control

## Status
Accepted

## Context
During full day-night playtest cycles following [ADR-029](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-029-data-driven-runtime-recipe-resolution.md) and [ADR-030](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-030-local-inter-agent-discourse.md), telemetry analysis revealed three systemic gaps between System 2 deliberation and System 1 execution:

1. **Nighttime-Only Contemplation**: `AgentRuntime.dispatchContemplation()` only fired during nighttime/bedtime ticks (`13000–23500` or `IsSleeping`) and was debounced to once per 24,000-tick Minecraft day. Agents that became idle, exhausted local tasks, or wandered aimlessly during daylight hours never entered System 2 contemplation to formulate a new plan.
2. **Disconnected Strategic Goals**: Although nighttime `STRATEGIC_CONTEMPLATION` succeeded and persisted `ImmediateGoal`, `ShortTermGoal`, and `LongTermGoal` strings into agent YAML configs and episodic memory, System 1's `EvaluateGoals` overwrote `GoalHierarchy` every tick and no System 1 evaluator or HTPDA skill actually read System 2's chosen goal. Strategic contemplation was effectively narrative-only rather than driving physical agency.
3. **Peer-Chat LLM Gateway Contention**: Under [ADR-030](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-030-local-inter-agent-discourse.md), when one agent spoke within $20\text{ m}$ of 5 peers, all 5 peers queued concurrent LLM dialogue requests against the local gateway (`max_concurrent_llm_requests: 4`), resulting in 24 of 36 dialogue calls timing out at $30\text{ s}$ (`context deadline exceeded`).

## Decision

### 1. Idle-Triggered Daytime Strategic Contemplation
In addition to the existing once-per-night bedtime reflection, `AgentRuntime` tracks continuous **idle / unproductive dwell duration** (ticks where the agent has no active productive skill/task—e.g. `IDLE`, `WANDER`, or stalled navigation without progress):
- **Trigger Threshold**: When an agent remains idle or unproductive for $\ge 300\text{ ticks}$ ($15\text{ s}$ of sustained idleness) and has no active strategic directive (or its previous directive has expired/completed), `dispatchContemplation()` may trigger asynchronously during daytime hours.
- **Rate Limiting**: Idle-triggered contemplation enforces a per-agent cooldown of $\ge 3,600\text{ ticks}$ ($3\text{ minutes}$ of game time) and never runs concurrently with an in-flight contemplation or while the agent is in `EMERGENCY` combat/evasion.
- **System 1 Invariant Preserved**: System 1 survival reflexes remain 100% deterministic, zero-token, and $<5\text{ ms}$; System 2 deliberation always runs asynchronously on a background goroutine.

### 2. Structured Executable Directives (`StrategicDirective`) Wired into System 1 FactGraph
`StrategicContemplationResponse` is extended with a structured, machine-actionable `Directive` object (with deterministic inference/fallback from `ImmediateGoal` when an LLM omits the field):
- **`DirectiveAction`** (canonical enum):
  - `GATHER_TIMBER` — harvest nearby logs and replant saplings.
  - `MINE_STONE` — gather stone/coal/ores from exposed outcrops or mines.
  - `FORAGE_FOOD` — harvest wild crops/berries or hunt passive livestock for food.
  - `CRAFT_SURVIVAL_KIT` — craft missing survival tools, crafting table, or light sources via the dynamic Recipe Graph ([ADR-029](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-029-data-driven-runtime-recipe-resolution.md)).
  - `BUILD_SHELTER` — construct or improve shelter / camp infrastructure.
  - `SCOUT_FRONTIER` — explore unmapped sectors beyond camp radius and index new POIs.
  - `SOCIALIZE` — move near camp/tavern peers and share status or coordinate work.
- **FactGraph Publication**: `AgentRuntime` publishes the active directive into System 1's `FactGraph` (`system2.directive_action`, `system2.directive_target`, `system2.directive_expires_tick`, `system2.immediate_goal`) with a bounded TTL (default `6,000` ticks / 5 minutes, or until satisfied).
- **System 1 Execution Priority**:
  - High-priority survival reflexes (`COMBAT_SURVIVAL`, `CRITICAL_HUNGER`, `EMERGENCY`) always preempt directives.
  - During normal `PRIMARY_DUTY` / `SETTLEMENT` / post-kit `BOOTSTRAP` ticks, System 1 evaluates the active `system2.directive_action` and dispatches the corresponding deterministic skill/task (e.g. `NewHarvestTreeTask`, foraging/hunting, mining, scouting outward along an unvisited sector, or crafting) instead of falling into aimless random wandering.
  - `GoalHierarchy` preserves the active System 2 `ImmediateGoal` label when executing a System 2 directive rather than clobbering it with static defaults.

### 3. Peer-Chat LLM Contention Relief
To eliminate simultaneous LLM request storms when multiple agents hear a local broadcast:
- **Direct-Address Priority**: If an incoming peer message explicitly mentions the listener's name (case-insensitive word match) or contains a direct question (`?`) or hazard alert, the listener may reply (subject to existing pairwise cooldowns).
- **Non-Addressed Broadcast Gating**: For general ambient peer statements that do not address the listener by name, only a single deterministic/probabilistic subset responds (low reply probability or hash-selected responder per `(speaker, message)` + short staggered jitter of $500\text{–}2500\text{ ms}$), while **all** listeners within $20\text{ m}$ still deterministically ingest the statement into `[PEER_DIALOGUE]` episodic memory at zero token cost.
- **Global Peer-Reply Cooldown**: Each agent enforces a minimum $30\text{ s}$ (`600` ticks) global cooldown between LLM-generated replies to peer chatter (independent of the $90\text{ s}$ pairwise cooldown), keeping human player chat responses prioritized and preventing local LLM queue saturation.

## Consequences

### Positive
- Idle agents proactively deliberate and select meaningful goals during the day instead of standing around or wandering aimlessly.
- System 2 strategic plans and community dialogue awareness actually shape System 1 physical behavior in the Minecraft world.
- Episodic community awareness (`[PEER_DIALOGUE]`) remains 100% intact for every agent within earshot, while local LLM gateway timeouts drop dramatically.

### Negative
- Slightly higher System 2 LLM call frequency during periods of extended daytime idleness, strictly bounded by the 3-minute per-agent idle contemplation cooldown.
- Directives that require unavailable world resources (e.g. `MINE_STONE` with no stone nearby) must gracefully fall back to scouting or timber gathering after bounded retries so agents do not stall.
