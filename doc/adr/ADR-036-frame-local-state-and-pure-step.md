# ADR-036: Frame-Local Activity State, Pure Step, Event Dispatch, Java Motor Stack, and Frame-ID Seam

## Status
Proposed (Amends [ADR-034](./ADR-034-single-owner-htpda-activity-stack-and-state-ownership.md) — which remains authoritative on *goals*: one owner, one stack, scoped receipts — by specifying the *mechanism*. Also amends [ADR-017](./ADR-017-hierarchical-timed-pushdown-automaton-with-preemptive-state-stack.md) §2 (state stack), [ADR-020](./ADR-020-factgraph-blackboard-and-modular-skill-engine.md) (FactGraph mutability), [ADR-015](./ADR-015-in-engine-peripheral-reflexes-and-afferent-telemetry.md) (Java reflexes) and [ADR-032](./ADR-032-peripheral-horizon-survey-and-clan-expedition-cartography.md) (expedition/survey state).)

Tracked by epic `cq-2sr`; migration beads `cq-2sr.2`–`cq-2sr.18`.

## Context

### Go orchestrator (as built)
- **Activity progress lives on a god object.** `system1.AgentState` (`orchestrator-go/internal/system1/state.go`) has 101 top-level fields plus a nested `RuntimeTracking` of 48 more. Most are the private progress of one behaviour (`Expedition*`, `survey*`, `ShelterBuild*`, `unstuck*`, `targetTree*`, `explore*`, `directive*`), yet they outlive that behaviour: nothing clears them when it stops, so every behaviour must defensively reset or ignore another behaviour's leftovers (`ClearExpeditionSurveyingIfActive`, `PauseActiveTask`, `lastMapNodeID = ""`).
- **Two stacks.** `AgentFSM` carries both `stack []StateStackFrame` (ADR-017, with a `task_frame` metadata convention and `AgentState.IsActiveTaskPushedToPDA`) and `activityStack []Activity` (ADR-034) plus an `activitySuspended` flag. `TaskSubroutine` is pushed onto one and tracked in the other.
- **`managedActivity` is a façade.** The seven activities in `activity_arbiter.go` hold no state of their own beyond an `ActivityReceiptScope`; their `tick` closures call the legacy waterfalls (`EvaluateShelterGoals`, `executeRegisteredSkill`, …) which read and write `AgentState`. `OnTick` even constructs a throw-away `AgentFSM` to pass `Stage`/`Routine`. Eligibility predicates (`isForagingActivityEligible`, …) live in the arbiter, not with the behaviour; priorities are hard-coded literals; `fallThroughOnEmpty` encodes per-activity quirks (e.g. shelter falls through except in `StageColonize`).
- **Stage is both a handler switch and a gate.** `StateHandler`s (`BootstrapStateHandler`, `EmergencyStateHandler`, …) and `StageTransitionRule`s run beside the activity stack; `SettlementStateHandler.OnExit` writes `lastShelterTimeoutTick`.
- **Parent/child hand-off is via mailboxes.** A survey result reaches its expedition through `PendingRendezvousSync`/`PendingRendezvousSummary`/`latestSurveySummary`; a harvest position reaches the pipeline through `RuntimeNavTracking.LastHarvestPos` (`TakeLastHarvestPos`).
- **Receipts are matched by counters.** `ActivityReceiptScope{StartReceiptBatchID, IssuedBatchCount, LastHandledBatchID}` re-anchors on start/resume against `AgentState.receipt.last`. Correctness depends on batch IDs being monotonic and on every emitter remembering `scope.RecordEmit()`.
- **Liveness is inferred globally.** `AgentFSM.stuckTicks`/`lastPos` and `FSMContext.IsStuckInState` treat "a shelter place, harvest or craft within 100 ticks" (`lastShelterPlaceTick`, `lastHarvestTick`, `lastCraftTick`) as progress for *any* activity.
- **Skills write facts back.** `shelter.complete` (`bootstrap_skills.go:158`), `armory.sufficient` (`armory_skill.go:152`), `construction.complete` (`construction_skill.go:277`) and `shelter.has_bed` (`stage_skills.go:146`) are written into the `FactGraph` during skill execution, so facts read later in the same tick depend on skill order.

### Java mod (as built)
- `AgentMotorFSM` holds a `MotorState` enum, a `Deque<MotorStackFrame>` whose frames carry an untyped `Map<String,Object>` context, and 25 fields of which 9 are loose horizon-survey cursor fields used only while `SURVEYING`.
- `IntentActuator.BatchSession` walks a batch with an `activeBatch`/`activeIndex` cursor, duplicating `AgentMotorFSM.currentBatchId/currentBatchIndex`.
- Stuck is a state (`STUCK_RECOVERY`) entered from `AgentNavigator.stuckTicks`, and Java both detects and decides recovery, while Go independently runs its own `unstuck*` waterfall.
- There is no per-frame deadline: `IntentBatch.timeout_ticks` is batch-wide and enforced by the session, not by the executing motor action.

## Decision

**Principle.** *If a field's lifetime equals one activity's lifetime, it lives in that activity's stack frame; popping the frame discards it.* Every other piece of per-agent state has exactly one of four other homes (below). There is exactly one activity stack per agent in Go and one motor stack per agent in Java.

### 1. Four homes for state (plus delete)

| Home | What belongs | Written by | Read by |
|---|---|---|---|
| **Frame-local** | Progress of one activity instance (cursor, phase, target, attempt counts, retry ticks) | Only that activity's `Step`, as `Step.Next` | Only that activity's `Step` |
| **Durable store** | World knowledge that outlives activities (`ShelterPos`, `HomesteadPos`, `ClanSize`, `LatestSurvey`, outposts, POIs, spatial indexes) | Kernel, applying `Step.Store` writes — only on `Pop`/`Replace` (activity exit), or by pipeline ingest for telemetry-sourced data | Evaluators → facts → `World` |
| **Derived** | Anything computable from World + store (stage, routine, "shelter complete", "expedition active", companion active) | Nobody — computed by evaluators each tick | `World` |
| **Memory** | Cross-activity cooldowns, blacklists with TTL, log/notify dedup — state that must survive a pop *and* is about the agent's own recent history, not the world | Kernel, applying `Step.Memory` writes | `Propose` and `Step` via `World.Memory()` |
| **Delete** | Never written in production, duplicated, or subsumed by the frame/lease/receipt mechanism | — | — |

`Memory` is one explicit struct (`system1.Memory`), not a bag: adding a field to it requires an entry in the migration table below.

### 2. Go core types (`internal/system1/htpda`)

```go
type FrameID uint64 // per-agent monotonic; never reused within a process lifetime

type Kind string     // "forage", "shelter", "task", "directive", "expedition", "survey", "navigate", "emergency", ...

// Band is the coarse preemption tier. Stage is no longer a dispatch switch: it is a derived
// input that selects which priority table (config) a Bid's utility is looked up in.
type Band uint8
const (
    BandIdle Band = iota      // empty-stack routines (exploration, patrol)
    BandRoutine               // circadian duty/hobby/social work
    BandCommitted             // directives, tasks, shelter, expedition
    BandEmergency             // threat response, critical hunger (pushed, not a StateHandler)
    BandDead                  // terminal; clears the stack
)

type Lease struct {
    StartTick    int64
    DeadlineTick int64 // 0 = no deadline; expiry delivers Expired{} to the frame
    RenewOnProgress bool
}

type Scope uint8 // what the frame may emit / react to
const (
    ScopeMotor Scope = 1 << iota // may emit motor intents; receives Receipt events
    ScopeStuck                   // decides stuck recovery for its own Navigate children
    ScopeChat
)

type Frame struct {
    ID     FrameID
    Act    Activity
    Local  any     // the activity's typed locals (value type); only Act type-asserts it
    Scope  Scope
    Lease  Lease
    Parent FrameID // 0 for root
}

// World is a read-only view for one tick: the immutable FactGraph produced by EvaluateAll,
// the TelemetryFrame, read-only store accessors and Memory. It has no setters.
type World interface {
    Tick() int64
    Frame() *civquestv1.TelemetryFrame
    Facts() FactReader          // immutable after EvaluateAll (cq-2sr.3)
    Store() StoreReader
    Memory() MemoryReader
    Stage() Stage               // derived
    Routine() Routine           // derived
}

type Bid struct {
    Kind    Kind
    Band    Band
    Utility float64 // looked up from config[Stage][Kind] and scaled by the activity
    Args    any     // initial arguments for Init
    Reason  string
}                   // zero Bid (Utility == 0) = abstain

type Op uint8
const (
    OpStay Op = iota
    OpPop          // pop self; Result delivered to parent as ChildDone
    OpPush         // push Child over self; self is suspended with Next as its locals
    OpReplace      // pop self and push Child in the same slot (same Parent)
)

type Step struct {
    Intents []*civquestv1.AgentIntent // stamped with this frame's ID by the kernel
    Next    any                       // this frame's locals after the step
    Op      Op
    Child   Kind                      // OpPush / OpReplace
    Args    any                       // OpPush / OpReplace
    Result  any                       // OpPop
    Store   []StoreWrite              // applied only on OpPop / OpReplace
    Memory  []MemoryWrite             // applied after the step
    Lease   *Lease                    // optional renewal / new deadline
}

// Activity is stateless behaviour. One value per Kind is registered in the EngineRegistry.
type Activity interface {
    Kind() Kind
    Scope() Scope
    Propose(w World) Bid                       // eligibility + utility; replaces arbiter predicates
    Init(w World, args any) (local any, l Lease)
    Step(w World, local any, ev Event) Step    // PURE: no I/O, no mutation of w or of anything reachable from local
}
```

**Events** (all values; `Dispatch` is the only entry point into the stack):

```go
type Event interface{ isEvent() }

type Tick      struct{}                                     // 5 Hz frame pipeline stage 4
type Started   struct{}                                     // first Step after Init
type Resumed   struct{ From Kind; Result any }              // after a preemptor pops (alias of ChildDone for non-child preemptors)
type ChildDone struct{ Child Kind; ID FrameID; Result any } // typed result from a popped child
type Receipt   struct{ Frame FrameID; Status ReceiptStatus; Facts MotorFacts }
type Reflex    struct{ Frame FrameID; Kind ReflexKind; Popped bool }
type Directive struct{ Action, Target, ImmediateGoal string; Expires int64 } // System 2 bridge (ADR-031)
type Expired   struct{}                                     // lease deadline passed
type Cancel    struct{ Reason string }                      // death, directive withdrawn
```

`Step` is a **pure function** of `(World, local, Event)`. Purity is what makes the replay harness (`cq-2sr.2`) a sufficient test oracle and what removes every reset-on-entry hack: a frame starts from `Init` and is discarded on pop.

### 3. Kernel: one stack, `Dispatch`, generic arbiter

```go
type Kernel struct {
    stack []Frame          // the ONLY activity stack; fsm.stack and activityStack are deleted
    next  FrameID
    reg   *EngineRegistry  // Kind -> Activity, injected (ADR-034 §4)
    prio  PriorityTable    // config data: map[Stage]map[Kind]float64
}

func (k *Kernel) Dispatch(w World, ev Event) []*civquestv1.AgentIntent
```

Routing rules:
1. `Tick`, `Directive`, `Expired`: delivered to the **top** frame. Before a `Tick` is delivered the kernel runs the arbiter.
2. `Receipt`, `Reflex`: looked up by `Frame` ID. If the ID is the top frame it is delivered; if it names a frame that is on the stack but suspended, or is not on the stack, it is **stale** and dropped (logged as diagnostic telemetry). A stale receipt is therefore a failed lookup, not a counter comparison — `ActivityReceiptScope` is deleted.
3. On `OpPop` the kernel removes the frame, applies its `Store` writes, and dispatches `ChildDone{Result}` to the new top (the parent) in the same tick. Parents are reached **only** this way.
4. On `OpPush` the parent's `Next` is stored in its frame; parent locals are intact on resume. Preemption is a push; resume is a pop.
5. `Cancel` unwinds from the top, delivering `Cancel` to each frame so it can return a `Result`; `Store` writes from cancelled frames are applied (a half-built shelter is still knowledge).

**Arbiter (≈50 lines).** On each `Tick`: collect `Propose(w)` from every registered activity; drop zero bids; pick max by `(Band, Utility)`; push it if the stack is empty, or if its `Band` is strictly greater than the top frame's band (preemption). Equal band never preempts — commitment is the default. Eligibility lives in each `Propose`; numeric priorities live in `PriorityTable` (config). `Emergency` is an activity whose `Propose` returns `BandEmergency` when threat facts hold; there is no `StateHandler` path and no `Stage` switch.

**Stage and Routine** are derived each tick from facts (`bootstrap.has_kit`, `colonize.is_establishing`, circadian time) and only select the `PriorityTable` row; transition callbacks (`onStageTransition`) fire when the derived value changes.

**Expedition as frames.** `Expedition{Target, Hops, Blocked []float32, Returning bool}` pushes `Survey{Origin}`; `Survey` pops with `SurveyResult{Report, Summary}` (replacing `PendingRendezvous*`/`latestSurveySummary`); `Expedition` pushes `Navigate{Target}`; a `Navigate` that pops with `Blocked{Bearing}` makes `Expedition` return `OpReplace` with `Expedition{Blocked: append(Blocked, bearing)}`. There is no `ExpeditionPhase` enum; `FlankCount == len(Blocked)`. On homestead claim it pops with `Store` writes for `HomesteadPos`/`CollectiveSitePos`.

**Read-only World (`cq-2sr.3`).** `FactGraph` is immutable once `EvaluateAll` returns; `EvaluatorContext` receives a read-only state view. The four skill fact write-backs above become computed facts (from store) or `Step` events.

### 4. Java motor stack (`com.civquest.mod.actuator.motor`)

```java
public sealed interface Motor permits Move, Mine, Place, UseItem, Sleep, Sweep, Recover, Reflex {}
public sealed interface Reflex extends Motor permits Combat, Dodge, Flee {}

public record Move(Vec3 target, float speed, boolean sprint, boolean allowBreak, boolean allowPlace,
                   int arrivalTolerance) implements Motor {}
public record Mine(BlockPos pos, int progressTicks, double progress) implements Motor {}
public record Sweep(Vec3 origin, int numSectors, float maxRange, int ticksPerSector,
                    int currentSector, int tickCounter, List<HorizonSector> sectors) implements Motor {}
public record Recover(int attempt, Vec3 stuckAt) implements Motor {}
// Place, UseItem, Sleep, Combat, Dodge, Flee analogous: each record holds exactly its own progress.

public record Lease(long startTick, long deadlineTick) {}

public record Frame(long batchId, long frameId, int index, Motor motor, Lease lease) {}
```

- One `Deque<Frame>` per agent. `MotorState` is deleted: the current state *is* `stack.peek().motor()`.
- A batch is one frame per intent, pushed in reverse order; `BatchSession.activeBatch`/`activeIndex` are deleted (`cq-2sr.16`). Completing a frame pops it and emits a receipt for `(batchId, frameId, index)`.
- Reflexes push `Combat`/`Dodge`/`Flee` over the top frame and pop with a `ReflexNotice` naming the preempted frame; an interrupted `Mine` resumes with its own `progressTicks` because the record was never discarded (`cq-2sr.15`).
- **Stuck is not a state.** The watchdog observes `AgentNavigator` stuck counters and pushes a bounded `Recover` frame; if `Recover` expires, the `Move` frame pops `FAILED` with `MotorFacts{stuck, enclosed, blockedBearing}`. Java reports facts; Go decides (expedition flank, blacklist, abandon) (`cq-2sr.14`; aligns with `cq-aik.15`).
- Every frame has a lease. Deadline expiry pops the frame with `RECEIPT_STATUS_TIMEOUT`.
- Hooks invoked from inside synchronized `AgentMotorFSM` methods must not re-enter other locking methods (same contract as the Go `Activity` comment).

### 5. Seam contract (`cq-2sr.11`)

Go pushes **plan** frames (`Navigate`, `Mine`, `Build`, …); Java pushes **motor** frames for the intents those plan frames emit. They are linked by `(batch_id, frame_id)`; neither side reads the other's stack. The 5 Hz/20 Hz split and bridge message set are unchanged. Proto additions (`proto/civquest/v1/civquest.proto`):

```proto
message IntentBatch {
  // existing 1..5
  uint64 frame_id = 6;               // Go Frame.ID that emitted the batch
}

message IntentReceipt {
  enum ReceiptStatus { /* existing 0..6 */ RECEIPT_STATUS_TIMEOUT = 7; }
  // existing 1..5 (motor_stack_depth retained for diagnostics only)
  uint64 frame_id = 6;               // echoed from IntentBatch.frame_id
  MotorFacts facts = 7;
}

message MotorFacts {
  bool stuck = 1;
  bool enclosed = 2;
  float blocked_bearing_deg = 3;     // ADR-032 yaw convention
  int32 stuck_ticks = 4;
  double progress = 5;               // action-specific (mine progress, remaining distance)
}

message ReflexNotice {
  // existing 1..3; ReflexType gains REFLEX_TYPE_DODGE = 4, REFLEX_TYPE_FLEE = 5, REFLEX_TYPE_COMBAT = 6
  uint64 preempted_frame_id = 4;
  bool popped = 5;                   // false = pushed, true = popped (Go may resume)
}
```

Receipts are delivered to Go as `Receipt{Frame: frame_id, ...}`; `frame_id == 0` (legacy mod) falls back to batch-ID scoping until `cq-2sr.17`.

## Consequences

### Positive
- **Popping is cleanup.** A behaviour's leftovers cannot leak into the next one, so the defensive resets and cross-activity "is X active?" checks disappear with their fields.
- **One stack, one router.** No `StateHandler`, no `Stage` switch, no `task_frame` metadata; `Dispatch` is the single entry point and every receipt has exactly one possible recipient.
- **Testable by replay.** Pure `Step` + recorded event log gives deterministic replay equivalence for the migration and for future regressions.
- **Java shrinks.** `MotorState`, `MotorStackFrame`'s untyped context map, the batch cursor and the nine survey fields are replaced by typed records; `IntentActuator` targets <500 lines.
- **Stuck has one decider** (Go), fed by Java facts, instead of two competing recovery loops.

### Negative / Risks
- **World view cost at 5 Hz is unmeasured.** The ≤5 ms System 1 budget ([ADR-005](./ADR-005-two-tier-cognitive-architecture-system1-utility-system2-llm.md)) is a design number; building an immutable `World` and copying value-type locals each tick must be benchmarked in `cq-2sr.4` before legacy deletion.
- **Legacy quirks encode playtest fixes.** `fallThroughOnEmpty`, the multi-increment of `telemetryFramesSeen` by several waterfalls per tick, and the 100-tick "recent progress" liveness heuristic are behaviour, not bugs, until proven otherwise; the replay harness must preserve them or a bead must record the intended change.
- **Stale-by-lookup drops receipts for suspended frames.** A suspended plan frame must re-emit on `Resumed`/`ChildDone`; activities that assumed a receipt would arrive after preemption will hang until their lease expires. Leases bound the damage.
- **Purity is a convention in Go.** `Local any` can hold pointers; activities must use value types (or copy on write) and a race/replay test per activity is the only enforcement.
- **Dual-run period.** Kernel and legacy arbiter coexist behind a flag (`cq-2sr.4`) and the seam must accept `frame_id == 0`, temporarily increasing surface.
- **Java locking.** Reflex push/pop hooks run under `AgentMotorFSM` monitors and must not call back into locking methods.

## Migration Table

Legend: **FL** = frame-local (activity named), **Store** = durable store, **Derived**, **Mem** = `Memory`, **Del** = delete. Rows marked † have an ambiguous home (see notes after the tables). The last column lists the bead that removes the field.

### A. `system1.AgentState` (101 top-level fields)

| # | Field | Home | Target / notes | Bead |
|---|---|---|---|---|
| 1 | `mu` | Del | Struct dissolves into store + `Memory`, each with its own lock | cq-2sr.17 |
| 2 | `AssignedBed` | Store | Bed POI assignment | cq-2sr.8 |
| 3 | `ShelterPos` | Store | Written by `Shelter` pop | cq-2sr.8 |
| 4 | `stage` | Derived | From facts; selects priority table | cq-2sr.6 |
| 5 | `routine` | Derived | From circadian facts | cq-2sr.6 |
| 6 | `PrimaryDutyBlocked` | Del | No production writer | cq-2sr.10 |
| 7 | `armoryMaintenanceNeeded` | Derived | Fact `armory.sufficient` (computed) | cq-2sr.3 |
| 8 | `companionActive` | Derived | Mirror of `CompanionService.IsActive()`; publish as fact | cq-2sr.10 |
| 9 | `civicPatrolActive` | Del | No production writer; patrol eligibility moves to `Patrol.Propose` | cq-2sr.10 |
| 10 | `patrolWaypoints` | Del | No production writer; becomes `Patrol` FL args if revived | cq-2sr.10 |
| 11 | `patrolWaypointIndex` | Del | No reader/writer outside state.go | cq-2sr.10 |
| 12 | `lastSleepAttemptTick` | Mem | Sleep retry cooldown | cq-2sr.17 |
| 13 | `lastShelterPlaceTick` † | FL | `Shelter` place throttle; liveness role → `Lease` | cq-2sr.8 |
| 14 | `lastShelterTimeoutTick` | Mem | Cross-activity shelter retry cooldown (written on settlement exit) | cq-2sr.6 |
| 15 | `lastBootstrapTimeoutTick` | Del | Never written in production (`IsBootstrapOnCooldown` always false) | cq-2sr.9 |
| 16 | `establishingNewSettlement` | Derived | No production writer; fact `colonize.is_establishing` from store | cq-2sr.6 |
| 17 | `shelterBlocksPlaced` | FL | `Shelter` | cq-2sr.8 |
| 18 | `ShelterBuildPhase` | FL | `Shelter` | cq-2sr.8 |
| 19 | `ShelterBuildIndex` | FL | `Shelter` | cq-2sr.8 |
| 20 | `lastHarvestTick` † | FL | `Task`(harvest) action throttle; liveness role → `Lease` | cq-2sr.5 |
| 21 | `lastCraftTick` † | FL | `Task`/`Armory` craft throttle; liveness role → `Lease` | cq-2sr.9 |
| 22 | `PavingTracker` | Store | Desire-path footstep ledger (ADR-023) | cq-2sr.17 |
| 23 | `unstuckLocked` | Del | Ambient unstuck replaced by Java `Recover` + Go `Navigate` failure handling | cq-2sr.14 |
| 24 | `unstuckDirX` | Del | as above | cq-2sr.14 |
| 25 | `unstuckDirZ` | Del | as above | cq-2sr.14 |
| 26 | `unstuckStartTick` | Del | as above | cq-2sr.14 |
| 27 | `unstuckStartX` | Del | as above | cq-2sr.14 |
| 28 | `unstuckStartZ` | Del | as above | cq-2sr.14 |
| 29 | `lastUnstuckNavTick` | Del | as above | cq-2sr.14 |
| 30 | `unstuckAttemptCount` | FL | `Navigate` (retry count before popping `Blocked`) | cq-2sr.14 |
| 31 | `lastUnstuckPosX` | Del | Java reports `MotorFacts.stuck` | cq-2sr.14 |
| 32 | `lastUnstuckPosZ` | Del | as above | cq-2sr.14 |
| 33 | `hasLastUnstuckPos` | Del | as above | cq-2sr.14 |
| 34 | `targetTreeLocked` | FL | `Task`(harvest tree) — the frame's existence is the lock | cq-2sr.5 |
| 35 | `targetTreeX` | FL | `Task`(harvest tree) target | cq-2sr.5 |
| 36 | `targetTreeZ` | FL | `Task`(harvest tree) target | cq-2sr.5 |
| 37 | `targetTreeStartTick` | FL | `Task` → `Lease.StartTick` | cq-2sr.5 |
| 38 | `activeTask` | FL | `Task` locals = all `TaskSubroutine` fields (see C) | cq-2sr.5 |
| 39 | `failedTrees` | Mem | TTL blacklist | cq-2sr.5 |
| 40 | `unreachableWorkstations` | Mem | TTL blacklist | cq-2sr.9 |
| 41 | `unreachableTargets` | Mem | TTL blacklist with failure counts | cq-2sr.8 |
| 42 | `homeOrigin` | Store | Settlement origin | cq-2sr.9 |
| 43 | `currentOutpost` | Store | | cq-2sr.9 |
| 44 | `outposts` | Store | | cq-2sr.9 |
| 45 | `lastExploreTick` † | FL | `Exploration`/`Patrol` re-emit throttle (also read by foraging, shelter) | cq-2sr.9 |
| 46 | `lastBootstrapNavTick` | Del | Subsumed by `Navigate` frame awaiting its receipt | cq-2sr.9 |
| 47 | `navigatingToPOI` | Del | = "top child is `Navigate` to a POI" | cq-2sr.9 |
| 48 | `exploreStepIndex` | FL | `Exploration` | cq-2sr.9 |
| 49 | `lastHeadingRad` | FL | `Exploration` | cq-2sr.9 |
| 50 | `frontierAzimuthRad` | FL | `Exploration` | cq-2sr.9 |
| 51 | `frontierAzimuthSet` | FL | `Exploration` | cq-2sr.9 |
| 52 | `frontierRadialLeg` | FL | `Exploration` | cq-2sr.9 |
| 53 | `currentExploreTarget` | FL | `Exploration` | cq-2sr.9 |
| 54 | `lastExploreProgressDist` | FL | `Exploration` → `Lease` renewal | cq-2sr.9 |
| 55 | `lastExploreProgressTick` | FL | `Exploration` → `Lease` renewal | cq-2sr.9 |
| 56 | `lastMapNodeID` | Store | Topological map cursor (map store) | cq-2sr.9 |
| 57 | `lastMapPos` | Store | Topological map cursor | cq-2sr.9 |
| 58 | `telemetryFramesSeen` † | Derived | Pipeline frame count in `World`; replay must preserve multi-increment quirk | cq-2sr.9 |
| 59 | `goals` † | Derived | `ImmediateGoal` from top frame; short/long-term from System 2 → Store | cq-2sr.17 |
| 60 | `receipt` | Del | Receipts are `Receipt` events routed by frame ID | cq-2sr.11 |
| 61 | `forageMemory` | FL | `Forage` (except `visitedContainerPOIs` → Mem; see B) | cq-2sr.9 |
| 62 | `constructionProgress` | FL | `Construction` (also deletes `ConstructionSkill.agentProgress`) | cq-2sr.8 |
| 63 | `runtimeState` | — | Container; fields mapped individually in B | cq-2sr.17 |
| 64 | `spatialIndex` | Store | POI grid (ADR-022) | cq-2sr.17 |
| 65 | `habitTrailTracker` | Store | ADR-023 | cq-2sr.17 |
| 66 | `recipeGraph` | Store | ADR-029 | cq-2sr.17 |
| 67 | `strategicDirective` | FL | `Directive` (pushed by `Directive` event; expiry = `Lease`) | cq-2sr.9 |
| 68 | `directiveScoutTarget` | FL | `Directive` | cq-2sr.9 |
| 69 | `lastDirectiveActionTick` | FL | `Directive` | cq-2sr.9 |
| 70 | `lastDirectiveNavKey` | Del | Subsumed by `Navigate` child | cq-2sr.9 |
| 71 | `LatestSurvey` | Store | Written by `Survey` pop | cq-2sr.7 |
| 72 | `LastSurveyTick` | Store | Survey report metadata | cq-2sr.7 |
| 73 | `LastSurveyOrigin` | Store | Survey report metadata | cq-2sr.7 |
| 74 | `ExpeditionPhase` | Del | Phase = which frame is on top | cq-2sr.7 |
| 75 | `ExpeditionHops` | FL | `Expedition.Hops` | cq-2sr.7 |
| 76 | `ExpeditionTarget` | FL | `Expedition.Target` | cq-2sr.7 |
| 77 | `ExpeditionBearing` | FL | `Expedition` | cq-2sr.7 |
| 78 | `ExpeditionFlankCount` | Del | `len(Expedition.Blocked)` | cq-2sr.7 |
| 79 | `ExpeditionNavAttempts` | FL | `Navigate` attempts | cq-2sr.7 |
| 80 | `ClanSize` | Store | | cq-2sr.7 |
| 81 | `RendezvousOrigin` | Store | Clan rendezvous point | cq-2sr.7 |
| 82 | `HomesteadClaimed` | Derived | `HomesteadPos != nil` in store | cq-2sr.7 |
| 83 | `HomesteadPos` | Store | Written by `Expedition` pop | cq-2sr.7 |
| 84 | `PendingRendezvousSync` | Del | Mailbox → `ChildDone{SurveyResult}` | cq-2sr.7 |
| 85 | `PendingRendezvousSummary` | Del | Mailbox → `SurveyResult.Summary` | cq-2sr.7 |
| 86 | `CollectiveSiteChosen` | Derived | `CollectiveSitePos != nil` in store | cq-2sr.7 |
| 87 | `CollectiveSitePos` | Store | | cq-2sr.7 |
| 88 | `expeditionSurveyStartTick` | FL | `Survey` → `Lease.StartTick` | cq-2sr.7 |
| 89 | `lastSurveyEmitTick` | Mem | Survey re-emit cooldown across expeditions | cq-2sr.7 |
| 90 | `lastSurveyEmitOrigin` | Mem | Paired with #89 (min-distance between surveys) | cq-2sr.7 |
| 91 | `lastSurveyEmitOriginSet` | Del | Pointer/optional replaces flag | cq-2sr.7 |
| 92 | `expeditionLastNavTick` | Del | Subsumed by `Navigate` child | cq-2sr.7 |
| 93 | `expeditionPrimaryTarget` | FL | `Expedition` | cq-2sr.7 |
| 94 | `expeditionPrimaryBearing` | FL | `Expedition` | cq-2sr.7 |
| 95 | `expeditionReturning` | FL | `Expedition.Returning` | cq-2sr.7 |
| 96 | `expeditionPrevOrigin` | FL | `Expedition` | cq-2sr.7 |
| 97 | `expeditionPrevOriginSet` | Del | Optional replaces flag | cq-2sr.7 |
| 98 | `expeditionBlockedBearings` | FL | `Expedition.Blocked` | cq-2sr.7 |
| 99 | `rendezvousOriginSet` | Del | Optional replaces flag | cq-2sr.7 |
| 100 | `clanSizeExplicit` | Store | Provenance of `ClanSize` | cq-2sr.7 |
| 101 | `latestSurveySummary` | Del | Mailbox → `SurveyResult.Summary` | cq-2sr.7 |

### B. Nested `AgentState` structs (`receiptSlot`, `forageAgentMemory`, `RuntimeTracking` — 56 fields)

| Field | Home | Target / notes | Bead |
|---|---|---|---|
| `receiptSlot.last` | Del | `Receipt` event | cq-2sr.11 |
| `receiptSlot.pending` | Del | `Receipt` event | cq-2sr.11 |
| `forageAgentMemory.lastActionTick` | FL | `Forage` | cq-2sr.9 |
| `forageAgentMemory.lastTargetKey` | FL | `Forage` | cq-2sr.9 |
| `forageAgentMemory.lastExploreTick` | FL | `Forage` | cq-2sr.9 |
| `forageAgentMemory.exploreHeadingRad` | FL | `Forage` | cq-2sr.9 |
| `forageAgentMemory.exploreTarget` | FL | `Forage` | cq-2sr.9 |
| `forageAgentMemory.visitedContainerPOIs` | Mem | Container visit TTL must survive `Forage` pop | cq-2sr.9 |
| `Timers.LastEatingTick` | Mem | | cq-2sr.6 |
| `Timers.LastMotionLogTick` | Mem | Log throttle | cq-2sr.17 |
| `Timers.LastMapPersistTick` | Mem | Store flush throttle | cq-2sr.17 |
| `Timers.LastTradeReassessTick` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastChronicleAttempt` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastChronicleTick` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastToponymyAttempt` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastToponymyTick` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastToponymyPos` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastTradeTick` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.LastTradePlayerUUID` | Mem | Trade dedup | cq-2sr.17 |
| `Timers.LastTradeAttemptAt` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Timers.ClanSizeResolved` | Store | Provenance of `ClanSize` (merge with `clanSizeExplicit`) | cq-2sr.7 |
| `Timers.LastClanSizeTick` | Mem | Store refresh throttle | cq-2sr.7 |
| `Nav.Destination` | FL | `Navigate.Target` | cq-2sr.7 |
| `Nav.TargetPOIID` | FL | `Navigate` | cq-2sr.7 |
| `Nav.LastHarvestPos` | Del | Mailbox → `Receipt`/`ChildDone` result of harvest `Task` | cq-2sr.5 |
| `Nav.LastFollowNavTarget` † | FL | `Follow` (companion) activity | cq-2sr.9 |
| `Nav.LastFollowNavTick` † | FL | `Follow` (companion) activity | cq-2sr.9 |
| `Combat.LastFleeTarget` | FL | `Emergency` (flee) | cq-2sr.6 |
| `Combat.LastFleeTick` | FL | `Emergency` (flee) | cq-2sr.6 |
| `Combat.LastCombatMemoryTick` | Mem | Episodic-memory write throttle | cq-2sr.6 |
| `Combat.LastCombatSummary` | FL | `Emergency` → `Result` on pop (written to memory pipeline) | cq-2sr.6 |
| `Idle.LastContemplatedDay` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Idle.LastIdleContemplationTick` | Mem | System 2 dispatcher section | cq-2sr.17 |
| `Idle.IdleStartTick` † | Derived | "stack empty since tick" — kernel-observed; else Mem | cq-2sr.17 |
| `Idle.HasIdleStartTick` | Del | Optional replaces flag | cq-2sr.17 |
| `Idle.LastActivePos` | Mem | Idle dwell detection | cq-2sr.17 |
| `Idle.HasLastActivePos` | Del | Optional replaces flag | cq-2sr.17 |
| `Viewer.LastLoggedRoutine` | Mem | Log dedup | cq-2sr.17 |
| `Viewer.LastLoggedGoal` | Mem | Log dedup | cq-2sr.17 |
| `Viewer.LatestThought` | Store | System 2 output projection | cq-2sr.17 |
| `Viewer.LatestDialogue` | Store | System 2 output projection | cq-2sr.17 |
| `Viewer.LatestChronicle` | Store | System 2 output projection | cq-2sr.17 |
| `Caches.SafeNodes` | Store | Map cache | cq-2sr.17 |
| `Caches.TradeValuations` | Store | | cq-2sr.17 |
| `Caches.HasCachedInventory` | Del | Optional replaces flag | cq-2sr.17 |
| `Caches.LastCachedItems` | Store | Inventory snapshot | cq-2sr.17 |
| `Caches.LatestFrame` | Derived | `World.Frame()` | cq-2sr.4 |
| `Caches.LastProcessedSeqID` | Store | Pipeline ingest cursor | cq-2sr.4 |
| `ActiveActivityKind` | Derived | `stack.top.Act.Kind()` | cq-2sr.4 |
| `ActiveActivityOwnsStuck` | Derived | `stack.top.Scope & ScopeStuck` | cq-2sr.4 |

(`RuntimeTracking.Timers/Nav/Combat/Idle/Viewer/Caches` containers dissolve with their fields.)

### C. Go stack/arbiter/runtime machinery

| Field | Home | Target / notes | Bead |
|---|---|---|---|
| `AgentFSM.mu` | Del | Kernel lock replaces | cq-2sr.17 |
| `AgentFSM.currentStage` | Derived | `World.Stage()` | cq-2sr.6 |
| `AgentFSM.previousStage` | Del | Transition callback carries `from` | cq-2sr.6 |
| `AgentFSM.currentRoutine` | Derived | `World.Routine()` | cq-2sr.6 |
| `AgentFSM.previousRoutine` | Del | as above | cq-2sr.6 |
| `AgentFSM.stateEnteredTick` | Del | `Frame.Lease.StartTick` | cq-2sr.6 |
| `AgentFSM.routineEnteredTick` | Del | Derived routine needs none | cq-2sr.6 |
| `AgentFSM.stuckTicks` | Del | Leases + `MotorFacts.stuck` | cq-2sr.14 |
| `AgentFSM.lastPos` | Del | as above | cq-2sr.14 |
| `AgentFSM.handlers` | Del | No `StateHandler` dispatch | cq-2sr.6 |
| `AgentFSM.transitions` | Del | Stage derived from facts | cq-2sr.6 |
| `AgentFSM.onStageTransition` | Store | Kept as kernel observer on derived change | cq-2sr.6 |
| `AgentFSM.onRoutineTransition` | Store | as above | cq-2sr.6 |
| `AgentFSM.stack` | Del | Single `Kernel.stack` | cq-2sr.5 |
| `AgentFSM.maxStackDepth` | Store | Kernel config | cq-2sr.5 |
| `AgentFSM.activityStack` | FL | Becomes `Kernel.stack []Frame` | cq-2sr.4 |
| `AgentFSM.activitySuspended` | Del | Suspended = not top | cq-2sr.4 |
| `AgentFSM.arbiter` | Del | Generic arbiter + `PriorityTable` | cq-2sr.10 |
| `AgentFSM.lastFacts` | Derived | `World.Facts()` | cq-2sr.3 |
| `AgentFSM.registry` | Store | Injected `EngineRegistry` (unchanged) | — |
| `StateStackFrame.Metadata["task_frame"]` + `AgentState.IsActiveTaskPushedToPDA` | Del | | cq-2sr.5 |
| `TaskSubroutine.*` (17 fields: `Spec`, `Phase`, `StartedTick`, `LastProgressTick`, `LastActionTick`, `LastNavigateTick`, `PausedAtTick`, `ActiveSubTarget`, `ActiveTargetID`, `ActiveApproachPos`, `ApproachBestDistSq`, `LastActionSubTarget`, `LastActionTargetID`, `BaselineCount`, `ActionsEmitted`, `PushedToPDA`, `LastReceiptBatchID`) | FL | `Task` locals; `PushedToPDA`, `PausedAtTick`, `LastReceiptBatchID` deleted (frame existence / suspension / frame-ID receipts) | cq-2sr.5 |
| `ActivityReceiptScope.*` (4 fields) | Del | Frame-ID lookup | cq-2sr.11 |
| `managedActivity.*`, `activitySpec.eligible/fallThroughOnEmpty/priority` | Del | `Propose` + `PriorityTable`; fall-through quirks preserved by replay | cq-2sr.10 |
| `ConstructionSkill.agentProgress` | FL | `Construction` | cq-2sr.8 |
| package `forageMemories` | Del | Already per-agent; now `Forage` FL | cq-2sr.9 |
| `AgentRuntime.nextBatchID` | Store | Seam counter (kept; frame ID added beside it) | cq-2sr.11 |
| `AgentRuntime.sequenceID` | Store | Seam counter (unchanged) | — |
| `AgentRuntime.status`, `isDead`, `deathEvent`, `permadeathResult` | Store | Lifecycle (unchanged); death dispatches `Cancel` + `BandDead` | cq-2sr.6 |
| `AgentRuntime.contemplationActive`, `tradeActive`, `chroniclerActive`, `toponymyActive` | Store | In-flight System 2 guards owned by `System2Dispatcher` (not activity state) | — |

Other `AgentRuntime` fields (IDs, DB handles, services, sockets, wait groups, lifecycle channels, config thresholds) are infrastructure and are unaffected.

### D. Java `AgentMotorFSM` (25 fields), `MotorStackFrame`, `MotorState`

| Field | Home | Target / notes | Bead |
|---|---|---|---|
| `agent` | Store | Owner reference (unchanged) | — |
| `stack` (`Deque<MotorStackFrame>`) | FL | `Deque<Frame>`; one frame per intent | cq-2sr.12 |
| `currentState` | Del | `stack.peek().motor()` | cq-2sr.16 |
| `previousState` | Del | Diagnostic only; log on pop | cq-2sr.16 |
| `ticksInState` | FL | `Frame.lease.startTick` | cq-2sr.12 |
| `combatTicks` | FL | `Combat` record | cq-2sr.15 |
| `stuckRecoveryTicks` | FL | `Recover` record / lease | cq-2sr.14 |
| `lastNavTarget` | FL | `Move.target` | cq-2sr.12 |
| `currentIntent` | FL | Intent that produced the top frame | cq-2sr.16 |
| `currentBatchId` | FL | `Frame.batchId` | cq-2sr.16 |
| `currentBatchIndex` | FL | `Frame.index` | cq-2sr.16 |
| `currentReceiptStatus` | Del | Receipt emitted on frame pop | cq-2sr.16 |
| `currentFailureReason` | Del | Receipt emitted on frame pop | cq-2sr.16 |
| `lastProcessedSequenceId` | Store | Intent dedup cursor (per agent, outlives frames) | cq-2sr.16 |
| `lastAsyncActionSucceeded` | FL | Result of the async frame (`Place`/`UseItem`) → its receipt | cq-2sr.16 |
| `lastAsyncFailureReason` | FL | as above | cq-2sr.16 |
| `surveyNumSectors` | FL | `Sweep` | cq-2sr.13 |
| `surveyMaxRange` | FL | `Sweep` | cq-2sr.13 |
| `surveyTicksPerSector` | FL | `Sweep` | cq-2sr.13 |
| `surveyCurrentSector` | FL | `Sweep` | cq-2sr.13 |
| `surveyTickCounter` | FL | `Sweep` | cq-2sr.13 |
| `surveySectors` | FL | `Sweep` | cq-2sr.13 |
| `surveyOriginX` | FL | `Sweep.origin` | cq-2sr.13 |
| `surveyOriginY` | FL | `Sweep.origin` | cq-2sr.13 |
| `surveyOriginZ` | FL | `Sweep.origin` | cq-2sr.13 |
| `MotorStackFrame.{state,intent,progressTicks,lastProgressValue,context}` | Del | Typed `Motor` records carry their own progress | cq-2sr.12 |
| `MotorState` enum | Del | Sealed `Motor` | cq-2sr.16 |

### E. Java `IntentActuator.BatchSession` and `AgentNavigator` stuck fields

| Field | Home | Target / notes | Bead |
|---|---|---|---|
| `BatchSession.agent` | Store | Session key (unchanged) | — |
| `BatchSession.activeBatch` | Del | Frames carry `batchId` | cq-2sr.16 |
| `BatchSession.pendingBatches` | Store | Inbound queue (unchanged; drained into frames) | cq-2sr.16 |
| `BatchSession.activeIndex` | Del | `Frame.index` | cq-2sr.16 |
| `BatchSession.status` | Del | Receipt on frame pop | cq-2sr.16 |
| `BatchSession.failureReason` | Del | Receipt on frame pop | cq-2sr.16 |
| `BatchSession.elapsedTicks` | Del | `Frame.lease` | cq-2sr.16 |
| `BatchSession.dispatched` | Del | Frame pushed = dispatched | cq-2sr.16 |
| `AgentNavigator.navigationState` | Store | Telemetry sensor; `STUCK` becomes `MotorFacts.stuck`, not a transition | cq-2sr.14 |
| `AgentNavigator.targetCoords` | FL | `Move.target` (navigator is parameterised per frame) | cq-2sr.12 |
| `AgentNavigator.stuckTicks` | Store | Sensor counter read by watchdog | cq-2sr.14 |
| `AgentNavigator.baritoneInactivityTicks` | Store | Sensor counter | cq-2sr.14 |
| `AgentNavigator.lastPosition` | Store | Sensor | cq-2sr.14 |
| `AgentNavigator.enclosed` | Store | Reported as `MotorFacts.enclosed` | cq-2sr.14 |
| `AgentNavigator.cliffDetected` | Store | Reported as `MotorFacts.stuck` | cq-2sr.14 |
| `AgentNavigator.failedCells` | Store | Planner TTL cache (navigator-owned) | — |
| `AgentNavigator.pillarBaseY` | FL | `Recover` (pillar/dig escape) | cq-2sr.14 |
| `AgentNavigator.lastDigTick` | FL | `Recover` | cq-2sr.14 |

### Ambiguous homes (†)
- `lastShelterPlaceTick`, `lastHarvestTick`, `lastCraftTick` serve two roles: per-activity action throttles (→ FL) and the global "recent progress" liveness heuristic in `IsStuckInState`/`AgentFSM.stuckTicks` (→ `Lease` renewal). `lastShelterPlaceTick` is also read by `armory.go` and `directive.go`; it stays FL only if directive `BUILD_SHELTER` pushes `Shelter` rather than placing directly.
- `lastExploreTick` is read by foraging, shelter, patrol, scouting and terrain; FL assumes those reads are re-emit throttles subsumed by `Navigate`. If any is a true cross-activity cooldown it moves to `Memory`.
- `telemetryFramesSeen` is incremented by several waterfalls per tick; "derived frame count" changes its value unless replay pins the quirk.
- `goals` mixes a derived immediate goal with System 2-authored horizons.
- `Nav.LastFollowNavTarget/Tick` assume a `Follow` activity exists for companion mode; today companion behaviour is a routine (`RoutineCamp`).
- `Idle.IdleStartTick` is derived only if "idle" means "empty stack"; if it means "not moving", it is `Memory`.

## Deviations (recorded during implementation)

### cq-2sr.4 — Go kernel (`internal/system1/htpda`)
1. **No dual path.** Per user directive (alpha), there is no per-agent legacy flag; the kernel is built standalone and later beads replace the legacy arbiter outright (supersedes "Dual-run period" under Risks).
2. **`Dispatch` returns `Effects`, not `[]*AgentIntent`.** `Effects{Emissions []{Frame, Kind, Intents}, Store, Memory, Decision, Diagnostics}`. The kernel performs no I/O: the wiring layer stamps `Emission.Frame` onto the outgoing `IntentBatch.frame_id`, applies Store/Memory writes, and logs Diagnostics (stale drops etc.). Memory writes take effect for the next `World`, not later steps within the same Dispatch.
3. **`World` is minimal for now:** `Tick, Frame, Facts (FactReader: GetBool/GetInt/GetFloat/GetString, satisfied by *system1.FactGraph), Stage, Routine`. `Store()`/`Memory()` readers are added by the beads that create those homes. `htpda.Stage`/`Routine` are string types to avoid importing system1 (import cycle); the adapter converts.
4. **`Frame.Band`** added (arbiter needs the top frame's band). Arbiter-pushed frames take the bid's band; `OpPush`/`OpReplace` children inherit the pusher's band.
5. **Bid scoring:** score = `PriorityTable.Weight(stage, kind) * Bid.Utility`; a kind with no positive weight in the stage row or the `AnyStage ("*")` fallback row cannot win. Ties broken by `(Band, Score)` then Kind ascending (deterministic). The registry is `htpda.Registry` (sorted by Kind), not system1's `EngineRegistry`.
6. **Expiry:** only the **top** frame's lease is checked (on `Tick`, before arbitration); a suspended frame whose deadline passed expires on the first Tick after it resumes unless it renews its lease in its `Resumed`/`ChildDone` step. On `Expired` the frame always ends: `OpReplace` is honoured (retry), anything else becomes `OpPop`, with `Result = Timeout{Kind, Deadline}` when the frame gave none. `RenewOnProgress` slides the window on a delivered `EXECUTING`/`COMPLETED` receipt.
7. **Started replaces Tick on push:** a frame pushed during a Dispatch receives `Started` and no `Tick` that Dispatch; a frame that already stepped during a Tick Dispatch (e.g. received `ChildDone`) is not stepped again with `Tick`.
8. **Arbiter preemptors have `Parent == 0`;** when one pops, the newly exposed frame gets `Resumed{From, ID, Result}`; frames pushed by `OpPush` get `ChildDone` on pop.
9. **Rejected pushes:** unknown kind or `MaxDepth` (default 16) answers the pusher with `ChildDone{ID: 0, Result: Rejected{Kind, Reason}}`; a rejected `OpReplace` pops the frame and notifies its parent with `Rejected`. `MaxCascade` (default 64) bounds Step calls per Dispatch.
10. **Cancel** steps every frame once top-down, forwards Store/Memory writes, discards intents (diagnostic) and Ops.
11. **Receipt `frame_id == 0`** is treated as stale by the kernel; any legacy fallback belongs in the seam adapter (cq-2sr.11), not the kernel.
12. **`Propose(w World) Bid`** takes no `local` (as in §2; the epic text's `Propose(World, local)` is superseded): bids are for frames not yet on the stack.
13. **Benchmark:** `BenchmarkDispatchTick` (4 activities, depth ≤ 8) ≈ 4 µs/op, ~0.9 KB/op — well inside the 5 ms budget; World construction cost is measured at wiring time.

### cq-2sr.5 — Wiring the kernel into AgentFSM (`internal/system1/kernel_adapter.go`)
1. **One stack.** `AgentFSM.kernel *htpda.Kernel` replaces `fsm.stack []StateStackFrame`, `activityStack`, `activitySuspended`, the `task_frame` metadata, `IsActiveTaskPushedToPDA` and the step-5b PDA sync. `StateStackFrame` is gone; the viewer gets `ActivityFrameView` (`activity_stack` JSON). Stage after an emergency is re-derived by the transition rules (no stage snapshot); routine is derived after the dispatch (task routine while a task is active, circadian otherwise).
2. **Legacy activities as kernel activities.** The seven `activitySpec`s run as `legacyActivity` (one `htpda.Activity` per kind, all `BandCommitted`, priorities copied into the `AnyStage` PriorityTable row). The old arbiter loop is reproduced inside the kernel: priority preemption among them is a **self-push** (the running frame `OpPush`es the highest-priority eligible spec above its own priority); ineligible/done frames pop with `legacyResult{Halt}` — a non-halting pop (legacy fall-through-on-empty) lets the exposed parent run in the same Dispatch, and an emptied stack is refilled by re-dispatching `Tick` (≤ 8 times). Legacy `Propose` abstains unless the stack was empty when the arbiter ran. Legacy frames still reach the `FSMContext` through the adapter's `World` (`fsmWorld`) and keep their progress on `AgentState` (`legacyLocal{}`) until cq-2sr.7/.8/.9.
3. **Task is frame-local.** `task_subroutine`'s Local is `taskLocal{Task, Receipt, LastStepTick}`. The `AgentState.activeTask` slot is (a) bound to a clone of the Local only while the task frame steps (legacy waterfalls step it there; the result is the next Local), (b) otherwise a read-only projection of the topmost active task frame published after each Dispatch, or (c) a pending start written by a legacy waterfall (`StartTaskLocked`) that the task's `Propose`/self-push adopts as `Init` args next tick. Suspension compensation uses `LastStepTick` on `Resumed`/`ChildDone` (deletes `Pause`/`Resume`/`PausedAtTick`); a resumed task re-issues its step (stale in-flight receipts were dropped). `PushedToPDA`, `LastReceiptBatchID`, `StartReceiptBatchID`, `IssuedBatchCount` and `MetadataSnapshot` are deleted. Non-preemptible tasks are failed on emergency entry and their frame pops when exposed.
4. **Emergency is a marker frame (bridge until cq-2sr.6).** `emergencyActivity` bids `BandEmergency` while Stage == EMERGENCY and pops on the first Tick after; intents still come from `EmergencyStateHandler` and are stamped with the emergency frame ID. Dead dispatches `Cancel`.

### cq-2sr.6 — Emergency/Dead as activities; StateHandler deleted (`emergency_activity.go`)
1. **`emergency` is a real activity.** It bids `BandEmergency` on `ctx.IsEmergency()`, steps the combat-defense/evasion/escape/critical-hunger skill itself (intents tagged with its frame ID, `Priority` set by the adapter), and pops on `ThreatsCleared()`. Its Local (`emergencyLocal`: mode, entered tick, last flee target/tick, reflex count) replaces `Combat.LastFleeTarget/LastFleeTick`; the pop `Result` (`emergencyResult`) is the combat summary. Entry side effects (abandon non-preemptible task, clear expedition surveying) are applied by the wiring layer (`onEmergencyEntry`), since Init/Step are pure.
2. **`dead` is a real activity** (`BandDead`). Death dispatches `Cancel`, then the arbiter pushes the dead frame (depth 1, never popped). Replay golden `death_clears_activities` now shows `stack=[dead/1]` (INTENTIONAL).
3. **`StateHandler`, `RegisterHandler` and the per-stage handler types are deleted.** `AgentFSM.Tick` has one dispatch path (`tickActivitiesLocked`); the stage transition table remains as data and only selects the PriorityTable row/routine.
4. **Reflex events.** `frame.ReflexNotices` are dispatched as `htpda.Reflex{Frame: preempted_frame_id (0 = top), Kind, Popped}` after the receipt and before the Tick; stale IDs are dropped by the kernel.

5. **Receipts.** `ActivityReceiptScope` is deleted. Each frame's receipt is dispatched as `htpda.Receipt` routed by `frame_id`, behind a seam guard that drops receipts older than the newest batch seen and `(batch, status)` re-deliveries (fixes "stale FAILED counted as task failure"). A legacy frame that started a task inline forwards receipts for it to the pending task. The runtime (`internal/agent/frame_seam.go`) stamps `IntentBatch.frame_id` with the last emitting frame (`FSMIntent.Frame`) and, while the mod does not echo `frame_id`, fills receipts' `frame_id` from its batch→frame binding. The replay harness models the same binding.
6. **Ownership is derived mid-cascade.** `AgentState.ActiveActivityKind/OwnsStuck` is set from the stepping (top) frame at each legacy step and from the final top after each Dispatch (ambient unstuck reads it inside waterfalls).

### cq-2sr.8 — Shelter and Construction progress as frame Locals (`shelter_activity.go`)
1. **Shelter Local.** `shelterLocal{BuildIndex, Phase, Pos}` is the `shelter` frame's Local (seeded from the store in `Init`). `ShelterBuildIndex`, `ShelterBuildPhase` and `shelterBlocksPlaced` (a redundant copy of the index) are deleted from `AgentState`; there is no separate `Placed` field. The legacy waterfall (`EvaluateShelterGoals(..., prog *shelterLocal)`) reads/writes the Local through `SkillContext.Shelter`, bound only while the frame steps. Callers outside a Shelter frame (`prog == nil`: directive `BUILD_SHELTER`, exploration reflex) may navigate/harvest/craft/light but never place cabin blocks; the Shelter frame owns placement.
2. **Progress bug fixed.** A tick with nothing to emit (place/craft throttle, pathing) no longer pops the frame while the agent holds progress or materials: the frame stays, so progress and the frame ID survive ticks and emergency suspension (replay `shelter_resume_after_flee`: the frame is `shelter/1` throughout, the emergency frame is `emergency/2`).
3. **Store.** `AgentState.CabinBuilt` (blocks of the cabin at `ShelterPos` already placed) and `ShelterPos` are written only by the frame's `Store` effect on pop/cancel/expiry (`shelterStore`, applied in `dispatchLocked`). "Shelter complete" is derived: the Local while the frame runs, `CabinBuilt >= CabinTotalBlocks` afterwards (`shelter.complete` fact). The daytime reset in `EvaluateExploration` now zeroes `CabinBuilt`.
4. **Bed shim deleted.** `effectiveHasBed` and the `shelter.had_assigned_bed` fact are gone; eligibility reads the frozen `shelter.has_bed` fact (a bed cleared mid-tick is seen one tick later). `AssignedBed` itself is still written by the sleep evaluator in `needs.go` (store field on `AgentState`); moving that write behind a `Sleep`/`Shelter` Store effect is left to the cq-2sr.9/.10 activities.
5. **Construction.** `AgentState.constructionProgress` and its accessors are deleted; `ConstructionLocal{Index}` is passed via `SkillContext.Construction` by whoever drives the skill (no production driver exists yet; `ConstructionSkill` itself holds no per-agent state). `lastShelterPlaceTick`/`lastCraftTick`/`lastHarvestTick` remain on `AgentState` (read by `armory.go`/`directive.go`; liveness role belongs to `Lease`, cq-2sr.9/.10).

### cq-2sr.7 — Expedition, Survey and Navigate as frames (`expedition_activity.go`)
1. **Frames.** `expedition` (Local `expeditionLocal{Goal, Target, Bearing, Hops, Blocked[], Returning, Home, ...}`) pushes `survey` (pops `SurveyReport`, lease = 120-tick timeout) and `navigate` (pops `navReached` or `navBlocked{Bearing}`). A flank is `OpReplace` of the expedition with an extended `Blocked`; `FlankCount == len(Blocked)` (the rim hit that triggers the re-survey counts, so the old `FlankCount >= 3` retreat rule is `len(Blocked) >= 4`). There is no `ExpeditionPhase`; the old `ExpeditionFlanking` is `Target != Goal`.
2. **Deleted from `AgentState` (21):** `ExpeditionPhase/Hops/Target/Bearing/FlankCount/NavAttempts`, `PendingRendezvousSync/Summary`, `latestSurveySummary`, `rendezvousOriginSet`, and the lowercase `expedition*`/`lastSurveyEmit*` progress fields. `RendezvousOrigin` is now `*[3]float64` (nil = home origin). Added: the derived `activityKinds` stack projection (backs `IsExpeditionActive/Surveying`) and the one-tick `pendingPush` request.
3. **Starting an expedition.** Legacy waterfalls (bootstrap, exploration, directive scout-frontier, task tick) no longer run the explorer inline: they call `RequestExpeditionStart`, and `legacyActivity.run/runTask` push an `expedition` child (Args `{Origin}`) over the running frame. This request is a transitional bridge removed by cq-2sr.9/.10 (Propose bids). A directive-started expedition ends its directive on the first hop via an `OpReplace` carrying the `scoutHopReached` store write and carries on (a returning leg survives).
4. **Store and events.** Steps read an immutable `expeditionKnowledge` snapshot; homestead claim and hazard POIs are `Step.Store` writes (`homesteadClaim`, `hazardMark`) applied on frame exit. `LatestSurvey` is still ingested by the pipeline (telemetry-sourced) rather than written by the Survey pop. The rendezvous mailbox is the `RendezvousReport` event (`AgentFSM.DrainRendezvousReports/QueueRendezvousReport`) that cq-aik.19 should feed. `HomesteadClaimed` remains a stored bool (not yet derived from `HomesteadPos`).
5. **Deviations.** The duplicate-survey storm guard (`lastSurveyEmit*`, ADR: Mem) lives in the expedition Local, so it no longer spans expeditions. Hops no longer survive an expedition that exits (ineligible, claim); bootstrap-started expeditions *yield* to bootstrap (push) while timber is in reach instead of exiting, keeping their hops. A preemptor that falls through within the same tick no longer aborts a survey or re-issues a navigate (only a preemptor that held the motor across ticks does).

### cq-2sr.9 (Foraging / Bootstrap / Exploration / Directive)

1. **Locals.** `forageLocal` (the old per-agent `forageMemory`; `AgentState.forageMemory` deleted), `directiveLocal` (action throttle, nav key, scout waypoint; `directiveScoutTarget`/`lastDirectiveActionTick`/`lastDirectiveNavKey` deleted), and `walkLocal` — the shared bootstrap/exploration/patrol/scouting/shelter cursor (`lastExploreTick`, `lastBootstrapNavTick`, `navigatingToPOI`, `exploreStepIndex`, `lastHeadingRad`, `frontier*`, `currentExploreTarget`, `lastExploreProgress*`; all deleted from `AgentState`) carried by `bootstrapLocal`, `explorationLocal`, `taskLocal.Walk`, `shelterLocal.Walk`, `directiveLocal.Walk`. The facts `scouting.radial_leg/azimuth_*` and `patrol.waypoint_index`'s explore fallback are deleted (no consumers).
2. **Directive is a bid source.** FORAGE_FOOD makes Foraging eligible; BUILD_SHELTER (materials in hand) makes Shelter eligible so it places blocks again; CRAFT_SURVIVAL_KIT makes Bootstrap eligible; SCOUT_FRONTIER is an `expedition` Propose bid (origin `strategic_directive`, no directive frame under it). Only GATHER_TIMBER/MINE_STONE/SOCIALIZE (and BUILD_SHELTER without materials) are hosted by the `strategic_directive` frame. Inline directive execution from the task tick is replaced by dropping the stale task.
3. **No `pendingPush`.** Bootstrap/exploration walks request an expedition as a step result (`walkLocal.wantExpedition` -> `OpPush`); `RequestExpeditionStart`/`takePendingPush` are deleted. The dead Stage*Skill waterfalls (bootstrap/settlement/colonize) are deleted.
4. **Hold vs pop.** A base frame with nothing to emit now stays (cursor preserved, foraging no longer dead-ends); a frame pushed over another still yields.
5. **Still on AgentState:** `lastCraftTick`, `lastShelterPlaceTick`, `lastHarvestTick`, `telemetryFramesSeen`, `lastMapNodeID/Pos`, target-tree/failedTrees (cq-2sr.21).

### cq-3a0.4 — Ladder placement (Java mod)
1. **No proto change.** `BuildBlockIntent.block_state_string` already carries block-state properties, so ladder facing is expressed as `minecraft:ladder[facing=<dir>]` (`facing` points away from the support block; the support is the neighbour at `facing.opposite`). No `facing`/`attach_face` field was added; Go still builds unchanged.
2. **Support check.** `InteractHandler` PLACE_BLOCK now rejects any state whose own `canSurvive` fails at the target (a floating ladder/torch is never placed, nothing is consumed). `Place` takes an optional failure-reason function; BUILD_BLOCK and INTERACT_BLOCK(PLACE_BLOCK) report concrete reasons (`no solid support for ...`, `no matching block item ...`, `place target cell is occupied`, ...) instead of a blank reason.
3. **Ladders never substitute.** `findMatchingBlockItemSlot` matches only the ladder item for a ladder request (no family/solid-block fallback), so a ladder request can never place cobblestone/planks.
4. **Pick-up.** Breaking a ladder (HARVEST_BREAK) drops the ladder item via the normal loot drop; the ItemEntity has a pickup delay and is collected by the agent's periodic `tryPickupNearbyItems()` (within 2.5 blocks) a few ticks later, not on the break tick.
