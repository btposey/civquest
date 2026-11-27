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
    // Supervise is pure. Recall ends the subtree above this frame.
    Supervise(w World, local any, child Kind) Verdict // Keep | Recall{Reason}
}
```

The first frame that answers `Recall` has its subtree cancelled from the top down and receives `ChildDone{Recalled}`; it then chooses again. This one mechanism covers urgency (night falls, the agent grows hungry) and opportunity (ore comes into view while travelling). A child never needs to know what might outrank it.

Commitment rules:

- A parent recalls only for a different, strictly more pressing choice.
- A child may declare a short critical section (a craft awaiting its inventory change) during which only an interrupt covers it.

### 3. A recalled child can be resumed

A cancelled frame returns a **memento**: a value holding what it needs to carry on (an expedition's destination, a plan's progress). The parent keeps it in its own Local and passes it back as `Init` args. Progress stays frame-local; no mailbox on shared state.

### 4. One rule against churn

If a child ends without having emitted an intent or made progress, the kernel will not push the same kind with the same args from the same parent for a back-off period. This replaces `workBackoff`, `surveyBackoff`, `gatherSkips` and the per-tick `skipped`/`refill` scratch where they exist only to stop relaunching.

### 5. Interrupts are unchanged in intent

`emergency` covers any frame for threats, hazards and critical hunger. `recover` is pushed by the step that owns a stuck intent. `dead` clears the stack. The interrupted frames resume with their Locals intact, as today.

## Migration

Each stage keeps the tests green and is committed separately. Levels are declared in `system1/activity_levels.go`. `scripts/stack-metrics.sh` scores any test run against the baseline in `doc/stack-baseline.json`; `scripts/stack-metrics.py` scores a live run's records.

1. **Kernel mechanics** (additive): levels, invariants in report-only mode, supervision, mementos, no-progress back-off.
2. **Measure**: run every simulation scenario and the live smoke with stack-shape assertions, and record the violations as the baseline.
3. **Goal frame**: add the base frame; move choice and urgency into its `Propose`-equivalent and `Supervise`; delete `preemptorAbove` and `preemptionPush`.
4. **Explicit children**: replace inline task starts and `wantExpedition` with ordinary pushes.
5. **Shared steps**: foraging, gathering, wool, bed and plan use the same navigate and interact steps, with one stuck and throttle policy.
6. **Enforce and remove**: invariants strict; priority-preemption code, scratch fields and ad hoc back-offs deleted; replay baselines re-recorded with the reason noted.

## Consequences

- A stack reads as goal → objective → task → step, and a log line explains why the agent is doing something.
- Sideways and recursive pushes become impossible rather than patched.
- Replay golden files will change in stages 3 to 5.
- The skills written as top-to-bottom waterfalls (bootstrap, patrol, directive) must be split into "what next" and "do it"; that is most of the work.
- Supervision runs one extra pure call per frame per tick; stack depth is small.
- New behaviour (for example the quarry, cq-rfx) is added as an objective with tasks, without editing a global priority order.
