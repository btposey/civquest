# ADR-041: One failure record, and refusals returned to their owner

## Status
Proposed (2026-11-27), awaiting review. No code has changed. Finishes [ADR-040](./ADR-040-goal-driven-activity-hierarchy.md) §4 ("one rule against churn") and builds the `Memory` home of [ADR-036](./ADR-036-frame-local-state-and-pure-step.md) §1. Tracked by `cq-3sa`.

## Summary

Today an agent remembers "do not try that again" in about ten separate places, and the kernel adapter can drop an activity's walk without telling it. Both lead to the same cycle: an activity repeats itself, a new timer is added to stop it, and the timer creates the next bug. This ADR replaces the places with one record, tells the owner when its action is refused, and adds tests that stop the pattern returning.

## Context

State at `b201b2a`, in `orchestrator-go/internal/system1`.

### Places that suppress work

| Mechanism | Remembers | Duration (ticks) | File |
|---|---|---|---|
| `workBackoffUntil` | an activity that found nothing to do, or thrashed | 200 | `work_backoff.go`, `stonewall.go` |
| `stonewalls` | an activity refused or standing still | 600 | `stonewall.go` |
| `surveyBackoffUntil` | surveys timing out | 600 | `expedition_activity.go` |
| `planLocal.Backoff` | failed tree gathering | 400 | `plan_activity.go` |
| `gatherSkips` | a find that took too long | 6000 | `gather_activity.go` |
| `unreachableTargets` | a place (2.5 block radius) | 2400 | `state.go`, `blacklist.go` |
| `failedTrees` | a trunk | 600 | `state.go` |
| `unreachableWorkstations` | a workstation block | 600 | `state.go` |
| `w.skipped` | an activity, for the current tick | 1 tick | `kernel_adapter.go` |
| kernel `idled` / `cooled` | a child that ended without progress | `cooled` is off (`NoProgressBackoff` = 0) | `htpda/hierarchy.go` |

Two trackers exist only to feed these: `interruptions` (feeds `workBackoffUntil`) and `refusals` (feeds `stonewalls`).

ADR-040 §4 said one kernel rule would replace `workBackoff`, `surveyBackoff`, `gatherSkips` and `skipped`. The rule was written, but its timed part is switched off (it only prevents a relaunch within the same tick), and all four remain.

### Actions changed behind the owner's back

`dispatchLocked` filters intents after the activity has returned them:

| Filter | Effect | Owner told? |
|---|---|---|
| destination already given up | walk dropped | no |
| fresh look wanted | walk replaced by a survey, then walks held 100 ticks | no |
| walk going nowhere | walk dropped, place marked unreachable | no |
| danger on the way | target moved to a waypoint | no (acceptable: it is routing) |

An activity that is not told keeps asking. `stonewall.go` was added to catch that (cq-xr7.1), and play test 10 then showed a push/pop loop around it (cq-xr7.4). The check `./scripts/check-arch-budgets.sh --strict` reports 9 violations and no test enforces it.

## Decision

### 1. One record of failure

```go
// Memory is the agent's own recent history (ADR-036 §1). Only the kernel adapter writes it.
type Subject struct {
    Kind  SubjectKind // Place, Block, Find, Work
    Key   string      // find key or activity kind
    Pos   [3]float64  // Place and Block
}

type Failure struct {
    Subject Subject
    Reason  Reason // NoPath, NoProgress, TimedOut, NothingToDo, Thrashed, ...
    At      int64
    Until   int64
    Count   int
}

// Write, from any Step:          Memory: []htpda.MemoryWrite{Failed{Subject, Reason}}
// Read, through the World only:  w.Memory().Suppressed(subject) (Failure, bool)
```

- Duration and match radius come from **one table** keyed by subject kind and reason. The first version copies today's values, so behaviour does not change.
- Dangers stay a separate store (they are world knowledge shared with the clan). `Suppressed` consults them for places, as `IsTargetUnreachableLocked` does now.
- All rows of the table above go, except `w.skipped`, which is removed in stage 5 if the kernel's within-dispatch rule covers it.

### 2. A refusal goes back to the owner

- Choosers ask `Suppressed` before they choose, so a refusal should be rare.
- The gate on outgoing intents remains as a check. When it refuses a walk it writes the failure and delivers `Refused{Frame, Subject, Reason}` to the frame that sent it, in the same tick, at most once per frame per tick.
- Every activity handles `Refused`. The shared default pops the frame with a failure, so no activity can ignore it by omission. The parent then chooses something else.
- A fresh look becomes an ordinary `survey` child that the owner pushes when `Memory` says one is wanted. The adapter no longer substitutes intents.
- The danger detour stays at the gate: it changes the route, not the decision.

### 3. One rule against churn

The kernel already reports each popped frame as idle or not. The adapter turns an idle or thrashing pop into `Failed{Work(kind), ...}`. This replaces `popNoWork`, the interruption tracker and the kernel's unused `cooled` map. Interrupts, travel and tasks are exempt, as now.

### 4. The watchdog becomes an alarm

The standstill watch stays as a last resort, because a frozen agent stops all progress. Its meaning changes: **a firing is a bug**.

- Simulation and replay tests fail if it fires.
- In live play it still acts, logs at error level, and is counted by `stack-metrics`.
- It is deleted after three consecutive play tests with no firing.

### 5. Tests enforce the design

| Test | Fails when |
|---|---|
| `AgentFSM` field list | a field is added that is not on the allowed list |
| no private timers | a `*Until`, `*Backoff` or `*Skips` field is declared outside `memory.go` (frame-local deadlines for a running job are allowed by name) |
| intent integrity | an emitted intent is dropped or swapped without a `Refused` event |
| state ratchet | calls that mutate `AgentState` outside its own files exceed the recorded count |
| size ratchet | any budget in `check-arch-budgets` exceeds its ceiling; ceilings are reset to today's sizes and only move down |

Resetting the ceilings accepts today's overruns as debt. It stops growth but does not pay the debt back.

## Stages

Each stage is one or more commits with all tests green, scored with `stack-metrics`.

| # | Stage | Behaviour change |
|---|---|---|
| 1 | Ratchet tests (sizes, fields, state calls) | none |
| 2 | `Memory` type; move the activity back-offs into it | none intended |
| 3 | Move place, trunk and workstation records into it | none intended |
| 4 | `Refused` event; survey as a child; delete `refusals` | yes |
| 5 | One churn rule; delete `popNoWork`, `interruptions`, `cooled`, and `skipped` if possible | yes |
| 6 | Watchdog alarm in tests; delete `stonewalls` in favour of `Memory` | yes |

Stages 4 to 6 each get a sandbox play test before the next begins.

## Done means

- One suppression store (plus the per-tick scratch only if stage 5 cannot remove it).
- No intent dropped or swapped without the owner hearing of it.
- Zero stack-shape violations, as now.
- Share of failed walks no worse than play test 10 (6.4%).
- No watchdog firing in any simulation scenario.

## Not in this pass

- Moving all activity progress off `AgentState` so that `Step` is pure (the rest of ADR-036). The state ratchet stops it getting worse.
- System 2 as problem solver (`cq-4yd`). This pass prepares for it: the failure record says what failed, where and why, which is what a model needs in its contemplation context to propose a way round.

## Risks

- Merging stores can change behaviour by accident, mostly through match radius and duration. Stages 2 and 3 keep today's values and compare replays.
- Popping on refusal may make an agent switch activities more often. Stage 4 is measured on pushes per 1000 ticks.
- Replay golden files will change in stages 4 to 6, with the reason recorded.

## Open questions for review

1. **Watchdog:** keep it acting in live play until proven unused (proposed), or make it log only from stage 6?
2. **Size ceilings:** reset to today's sizes and ratchet down (proposed), or first split the seven oversized files back under the old ceilings?
3. **Order:** finish this before the night shelter bug `cq-xr7.3` (proposed)?
