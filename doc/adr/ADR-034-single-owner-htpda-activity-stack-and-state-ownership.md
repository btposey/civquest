# ADR-034: Single-Owner HTPDA Activity Stack, Scoped Receipt Ownership, and Runtime State Decomposition

## Status
Accepted (Amends [ADR-017](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-017-hierarchical-timed-pushdown-automaton-with-preemptive-state-stack.md) and [ADR-020](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-020-factgraph-blackboard-and-modular-skill-engine.md); Supersedes `cq-aik.3`)

## Context
Over successive feature epics, CivQuest's Go orchestrator (`orchestrator-go`) accumulated architectural fragmentation across both `AgentRuntime` (`internal/agent/runtime.go`) and System 1 (`internal/system1/`):

1. **Five Parallel Leaked Sub-FSMs**: Although [ADR-017](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-017-hierarchical-timed-pushdown-automaton-with-preemptive-state-stack.md) established an HTPDA under `AgentFSM`, five independent state machines evolved outside the formal pushdown stack:
   - `TaskSubroutine` (`internal/system1/task.go`),
   - `ExpeditionPhase` (`internal/system1/state.go`, `bootstrap.go`, `exploration.go`),
   - `ShelterBuildIndex` (`internal/system1/shelter.go`, `state.go`),
   - `StrategicDirectiveState` (`internal/system1/directive.go`, `state.go`), and
   - Imperative `Foraging` / `Bootstrap` / `Exploration` fallback waterfalls (`foraging.go`, `bootstrap.go`, `exploration.go`).
   On any tick, multiple subsystems could compete to emit intents, while ambient `EvaluateUnstuck` calls inside waterfalls hijacked motor control from active tasks.
2. **Receipt Cross-Contamination**: `AgentRuntime.lastReceipt` and `AgentState.pendingReceipt` stored global, un-scoped `IntentReceipt` copies. When a reflex preempted a task or an activity transitioned, stale `ARRIVED`, `COMPLETED`, `STUCK`, or `ENCLOSED` receipts from the prior motor command frequently leaked into the newly active routine.
3. **Split Mutable State & Package Globals**: Agent state was fragmented across `AgentRuntime` fields (`lastEatingTick`, `lastFleeTarget`, `lastFleeTick`, `lastMotionLogTick`, `lastFollowNavTick`, `lastTradeTick`, `lastChronicleTick`, `lastToponymyTick`, `lastIdleContemplationTick`, `lastTradeReassessTick`, `navDestination`, `lastReceipt`), a package-global `forageMemories` map in `internal/system1/foraging.go`, a skill-instance `ConstructionSkill.agentProgress` map in `internal/system1/construction_skill.go`, and legacy compatibility shims (`CurrentRoutine()`, `FSMState()`, `ShelterStateHandler`, `RestStateHandler`, `ExploreStateHandler`, `AgentHFSM`).
4. **Monolithic `HandleTelemetryFrame` & Global `DefaultRegistry`**: `HandleTelemetryFrame` mixed receipt ingestion, spatial indexing, System 1 evaluation, intent emission, and System 2 triggers in one large method, while `EngineRegistry` relied on global `DefaultRegistry` `init()` registration ([ADR-020](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-020-factgraph-blackboard-and-modular-skill-engine.md)), violating strict process/instance isolation in tests and multi-agent simulations.

## Decision

### 1. Single-Owner HTPDA Activity Stack (`AgentFSM`)
Amending [ADR-017](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-017-hierarchical-timed-pushdown-automaton-with-preemptive-state-stack.md), all five parallel sub-FSMs (`TaskSubroutine`, `ExpeditionPhase`, `ShelterBuildIndex` progression, `StrategicDirectiveState` execution, and `Foraging`/`Bootstrap`/`Exploration` waterfalls) are unified into **one Single-Owner HTPDA Activity Stack** managed by `AgentFSM`:

- **Formal `Activity` Lifecycle**: Every executable behavior implements a cohesive `Activity` contract (`ID()`, `Kind()`, `OnStart()`, `OnTick()`, `OnSuspend()`, `OnResume()`, `OnExit()`) whose private state lives inside the stack frame rather than ad-hoc global fields.
- **Strict 3-Tier Arbitration**:
  - **Tier 0 — Reflex Preemption Only**: Immediate survival reflexes (`CombatDefense`, creeper evasion, fire/drowning escape, critical hunger eating) preempt by pushing onto the top of the stack. Preemption invokes `OnSuspend()` on the interrupted `Activity` and sets `FlushPeriphery = true` to clear stale peripheral motor queues before the reflex executes. When the hazard clears, the reflex pops and invokes `OnResume()` on the prior `Activity`.
  - **Tier 1 — Committed Execution**: **Only** the top-of-stack `Activity` may emit motor intents (`NavigateTo`, `MineBlock`, `PlaceBlock`, `Interact`, `SurveyHorizon`, etc.) and react to `STUCK`, `ENCLOSED`, or `ARRIVED` receipts. Ambient `EvaluateUnstuck` tick-stealing across waterfalls is prohibited; unstuck recovery is an explicit capability/transition owned by the active `Activity`.
  - **Tier 2 — Goal Arbiter (Empty-Stack Only)**: Evaluates `FactGraph` preconditions and utility priority **only when the activity stack is empty** (or when the top `Activity` explicitly finishes and pops) to select and push the next `Activity`.

### 2. Scoped `IntentReceipt` & Batch Ownership
- Every `Activity` instance is assigned a monotonically increasing `StartReceiptBatchID` / activity batch token upon `OnStart()` (and refreshed on `OnResume()` after reflex preemption).
- Outgoing motor intents carry the owning activity's batch scope, and incoming `IntentReceipt` events are matched strictly against the active top-of-stack `Activity`'s `StartReceiptBatchID`.
- Receipts belonging to a suspended, popped, or superseded activity batch are discarded or routed exclusively as passive diagnostic telemetry—never triggering completion, advancement, or stuck transitions on a different `Activity`.

### 3. Single Mutable State Owner (`AgentState`)
`AgentState` (`internal/system1/state.go`) is established as the **single owner of all mutable per-agent System 1 and runtime-tick state**:
- Move all per-agent tick/cooldown and navigation state off `AgentRuntime` (`lastEatingTick`, `lastFleeTarget`, `lastFleeTick`, `lastMotionLogTick`, `lastFollowNavTick`, `lastTradeTick`, `lastChronicleTick`, `lastToponymyTick`, `lastIdleContemplationTick`, `lastTradeReassessTick`, `navDestination`) into `AgentState` (or explicit owned sub-structs inside `AgentState` / `System2Dispatcher`).
- Eliminate package-global `forageMemories` (`internal/system1/foraging.go`) and skill-singleton `ConstructionSkill.agentProgress` (`internal/system1/construction_skill.go`), storing per-agent foraging memory and construction progress inside `AgentState` / the owning `Activity` frame.
- Eliminate duplicate `AgentRuntime.lastReceipt` and `AgentState.pendingReceipt` storage in favor of a single canonical scoped receipt slot in `AgentState`.
- Remove legacy state-machine aliases and retired handlers (`CurrentRoutine()`, `FSMState()`, `ShelterStateHandler`, `RestStateHandler`, `ExploreStateHandler`, `AgentHFSM`) once callers are migrated to the unified HTPDA Activity Stack.

### 4. Typed `HandleTelemetryFrame` Pipeline, Subsystem Extraction & Injected `EngineRegistry`
- **Ordered Typed Stages**: `AgentRuntime.HandleTelemetryFrame` is decomposed into explicit, typed sequential stages:
  1. `Receipt Ingest` (validate batch ownership, update `AgentState` receipt state)
  2. `Spatial / Survey` (update `POIStore`, `SpatialIndex`, horizon sectors)
  3. `FactGraph Eval` (run registered `Evaluator`s to build immutable `FactGraph`)
  4. `HTPDA Stack Tick` (Tier 0 reflex check $\to$ Tier 1 top `Activity` tick or Tier 2 Goal Arbiter)
  5. `Intent Dispatch` (flush periphery if requested, enqueue motor/action intents with batch ID)
  6. `System 2 Dispatchers` (non-blocking triggers for dialogue, trade, chronicle, toponymy, contemplation)
- **Extracted Subsystems**:
  - `System2Dispatcher`: owns asynchronous System 2 cooldowns, triggers, and gateway dispatch outside `AgentRuntime`'s core loop.
  - `POIStore`: owns spatial POI persistence, indexing, and horizon survey updates cleanly separated from `AgentRuntime`.
- **Constructor-Injected `EngineRegistry`**: Amending [ADR-020](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-020-factgraph-blackboard-and-modular-skill-engine.md), `EngineRegistry` is explicitly constructed via a factory (e.g., `NewDefaultEngineRegistry()`) and injected into `AgentRuntime` / `AgentFSM` via constructor parameters, replacing mutable package-global `DefaultRegistry` reliance in `init()`.

## Consequences

### Positive
- **Deterministic Single-Owner Control**: Eliminates split-brain motor commands where parallel sub-FSMs or ambient unstuck waterfalls overwrite an active construction, mining, or survey plan.
- **Zero Receipt Leakage**: Batch-scoped `IntentReceipt` validation guarantees that a delayed `ARRIVED` or `STUCK` from a preempted combat/navigation command can never falsely advance or abort a resumed `Activity`.
- **Process & Test Isolation**: Moving state out of package globals (`forageMemories`, `ConstructionSkill.agentProgress`, `DefaultRegistry`) into `AgentState` and injected `EngineRegistry` instances makes unit and integration tests deterministic and parallel-safe.
- **Clear Runtime Boundaries**: Separating `HandleTelemetryFrame` into six typed stages alongside `System2Dispatcher` and `POIStore` keeps System 1's $<5\text{ ms}$ hot path auditable and decoupled from async LLM workflows.

### Negative / Trade-offs
- **Migration Surface**: Existing System 1 behaviors (`bootstrap.go`, `shelter.go`, `exploration.go`, `foraging.go`, `directive.go`, `construction_skill.go`) and tests targeting legacy `CurrentRoutine`/`FSMState` shims must be refactored into `Activity` implementations on the unified stack.
- **Strictness of Batch Scoping**: Peripheral intent responses in `mod-neoforge` and `AgentRuntime` must faithfully propagate batch/intent correlation IDs so legitimate completion receipts are never dropped.
