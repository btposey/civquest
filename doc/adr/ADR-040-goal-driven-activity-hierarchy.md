# ADR-040: Goal-driven activity hierarchy — stack depth is level of abstraction

## Status
Proposed (2026-11-26). Amends [ADR-036](./ADR-036-frame-local-state-and-pure-step.md) §3 (arbitration and preemption); frame-local state and pure `Step` are unchanged.

## Context

ADR-036 gave each agent one stack of frames. It did not say what depth means, and the implementation reproduced the earlier flat priority loop inside that stack:

- When the stack is empty, the highest-scoring activity of any kind becomes the base frame. There is no frame that holds the agent's goal.
- Any running frame pushes any higher-priority activity onto itself (`preemptorAbove`, `preemptionPush`), although ADR-036 §3 says equal urgency never preempts. All ten ordinary activities bid in one band and compete by priority number.
- Some waterfalls also start work sideways: a task stored in the walk cursor, `wantExpedition`, `fsm.startedTask`.

The stack therefore records the order of interruptions. Live play on 2026-11-26 showed `bootstrap/plan/expedition/navigate/gather/bootstrap` and `bootstrap/plan/expedition/navigate/gather/task_subroutine/foraging`. Repeat loops follow from this shape: an activity relaunched over itself (cq-6l3.21), a task started and ended every tick, activities pushed and popped for want of work (cq-6l3.17, .18), a route lost when a sideways frame ended. Each was patched separately, with its own back-off or skip list.

## Decision

### 1. Every activity has a level, and a child is always more concrete than its parent

| Level | Role | Activities |
|---|---|---|
| 0 Goal | Always the base frame; the only chooser of objectives | `goal` (new) |
| 1 Objective | Pursues one purpose by choosing tasks | bootstrap, plan, foraging, shelter, bed, construction, exploration, strategic directive |
| 2 Task | Sequences steps to one concrete result | expedition, gathering, wool, task subroutine (fell a tree, collect drops); later mine, hunt, fish, craft |
| 3 Step | One physical job with receipts | navigate, survey, break or place a block, use an item |
| Interrupt | May cover any frame, then returns | emergency, recover, dead |

Kernel invariants, enforced at push time:

1. A pushed child has a level strictly greater than its parent's (levels may be skipped, never repeated or reversed).
2. Only an interrupt may be pushed by something other than the frame's own `Step`.
3. A kind appears at most once on the stack.
4. The base frame is the goal. It never pops while the agent lives.

A rejected push is delivered to the parent as `Rejected` and recorded as a diagnostic, so a violation is visible and cannot loop silently.

### 2. A parent supervises its child

Only the top frame is stepped, so a parent needs a way to react while its child works. Before each tick the kernel asks the frames from the base upwards:

```go
type Supervisor interface {
    // Supervise is pure. Park sets aside the subtree above this frame.
    Supervise(w World, local any, child Kind) Verdict // Keep | Park{Reason, Token}
}
```

The first frame that answers `Park` has its subtree set aside, intact, and receives `ChildDone{Parked}`; it then does something else. This one mechanism covers urgency (night falls, the agent grows hungry) and opportunity (ore comes into view while travelling). A child never needs to know what might outrank it.

Commitment rules:

- A parent parks only for a different, strictly more pressing choice.
- A child may declare a short critical section (a craft awaiting its inventory change) during which only an interrupt covers it.

### 3. Parked work is resumed intact

`Parked` carries the frames themselves, locals included. The parent keeps them in its own Local and returns them with `OpResume`, and the restored top frame receives the result of the work done in the meantime. Progress stays frame-local; nothing is serialised or rebuilt.

### 4. One rule against churn

If a child ends without having emitted an intent or made progress, the kernel will not push the same kind with the same args from the same parent for a back-off period. This replaces `workBackoff`, `surveyBackoff`, `gatherSkips` and the per-tick `skipped`/`refill` scratch where they exist only to stop relaunching.

### 5. Interrupts are unchanged in intent

`emergency` covers any frame for threats, hazards and critical hunger. `recover` is pushed by the step that owns a stuck intent. `dead` clears the stack. The interrupted frames resume with their Locals intact, as today.

## Migration

Each stage keeps the tests green and is committed separately. Levels are declared in `system1/activity_levels.go`. `scripts/stack-metrics.sh` scores any test run against the baseline in `doc/stack-baseline.json`; `scripts/stack-metrics.py` scores a live run's records.

1. **Kernel mechanics** (additive): levels, invariants in report-only mode, supervision, park and resume, no-progress back-off.
2. **Measure**: run every simulation scenario and the live smoke with stack-shape assertions, and record the violations as the baseline.
3. **Goal frame**: add the base frame; move choice and urgency into it and its `Supervise`; delete the self-push preemption (`system1/goal_activity.go`). The policy is still the earlier priority order, applied in one place; parked work counts for "already running".
4. **Explicit children**: replace inline task starts and `wantExpedition` with ordinary pushes.
5. **Shared steps**: one walking policy for every activity (`system1/walk_policy.go`): a walk sent repeatedly while the agent neither moves nor has a path is withheld and its target marked unreachable, so the owner chooses again. It is applied where intents leave the stack rather than as a separate step frame, because an activity re-aims a walk every tick (a moving animal, a nearer drop) and a frame holding one target would go stale. Shared interaction steps and send throttles remain.
6. **Enforce and remove**: invariants strict; paths the rules refuse deleted; replay baselines re-recorded with the reason noted.
7. **Strategic machines**: see below.

## Strategic machines (stage 7)

User direction, 2026-11-26. The base frame is one long-running **strategic machine**: `explore`, `settle`, `profession` or `idle` (war is deferred, cq-wt0). All four share the goal engine above; they differ in which objectives they may launch, in their daily rhythm, and in who the agent is while in them.

| Machine | When | Objectives | Rhythm | Acts as |
|---|---|---|---|---|
| explore | no settlement | equip, provision, gather, shelter, bed, travel | day work, evening camp, night rest | explorer |
| settle | has a settlement, not yet established in it | equip, provision, gather near home, shelter, bed, build | day work, evening together, night rest | settler (a homesteader if alone) |
| profession | has a bed or shelter there and has been settled a while | its duties, build, provision, shelter, bed | the profession's shifts | its profession |
| idle | nothing to do for a while | provision, shelter, bed | as before | as before |

**Replacing a machine.** Three sources, each with its own timing:

1. *Hard milestones* apply at once: a chosen clan site or homestead ends exploring.
2. *Morning review*: System 2 may propose a change during the night, naming what is new. At the next dawn code weighs it once: it applies if it is among the options allowed from the current machine (settle ↔ profession, and a frontier explorer may come home) and the machine has run at least two days. A proposal that arrives after dawn expires before the next one, and a failed answer changes nothing.
3. *Clan assignment* (for example a frontier explorer chosen from a surplus; the selection itself is cq-dhm).

A replaced machine abandons its parked work, which banks what it knows.

**Advice, not commands.** A System 2 directive is advice a machine reads when it next chooses work; it does not park or end work that is getting somewhere. Two exceptions keep a stuck agent rescuable: advice asked for because the agent stood idle may interrupt until the work it asks for starts, and a bootstrap harvest that has stalled (two failures, or no progress for 200 ticks) may be dropped for it.

**Contemplation.** Night requests are spread across the night. Idle asks what to become while the body keeps a safe default, and returns to what it was if no answer comes.

**Several settlements.** A clan's chosen sites are kept per site, the first being home; founding colonies and trade between them are cq-dhm.

**Committing to a site (playtest 5, cq-gey.3).** A site is claimed only when a majority of the clan's living members, and never one alone while others live, have endorsed it. Ice and frozen country count heavily against a site and cancel the value of water beside it, and no site is taken within 48 blocks of a registered danger.

**Dangers (cq-gey.2).** A mob that hurts an agent, or one that lingers such as a Drowned, marks its spot for at least a game day, shared with the clan. Agents choose no targets within 20 blocks (home excepted), walk round it, and once hit keep going until clear.

## Consequences

- A stack reads as goal → objective → task → step, and a log line explains why the agent is doing something.
- Sideways and recursive pushes become impossible rather than patched.
- Replay golden files will change in stages 3 to 5.
- The skills written as top-to-bottom waterfalls (bootstrap, patrol, directive) must be split into "what next" and "do it"; that is most of the work.
- Supervision runs one extra pure call per frame per tick; stack depth is small.
- New behaviour (for example the quarry, cq-rfx) is added as an objective with tasks, without editing a global priority order.
