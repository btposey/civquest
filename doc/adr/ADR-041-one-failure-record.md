# ADR-041: One failure record, and refusals returned to their owner

## Status
Proposed (2026-11-27), awaiting review, except as follows. **Stage 1 is partly done** (`cq-3sa.1`): the watchdog only reports (§4), and `stonewalls`, the fresh-look request and the thrash back-off are deleted. Still to do from stage 1: the ratchet tests of §5 (`AgentFSM` field list, state calls, sizes with ceilings reset). Faults the watchdog exposed are `cq-3sa.2` to `cq-3sa.6`. No other stage has started. The Context section describes the code as it was before `cq-3sa.1`. Finishes [ADR-040](./ADR-040-goal-driven-activity-hierarchy.md) §4 ("one rule against churn") and builds the `Memory` home of [ADR-036](./ADR-036-frame-local-state-and-pure-step.md) §1. Tracked by `cq-3sa`.

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
| `unreachableWorkstations` | a block: a workstation not reached, or one that would not break (added by cq-6nz, after the commit above) | 600 | `state.go` |
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
    Reason  Reason // NoPath, NoProgress, TimedOut, NothingToDo, ...
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

The kernel already reports each popped frame as idle or not. The adapter turns a pop for want of work into `Failed{Work(kind), NothingToDo}`. This replaces `popNoWork` and the kernel's unused `cooled` map. Interrupts, travel and tasks are exempt, as now. Work that keeps starting and stopping, or falls straight through when pushed over other work, is a fault: it is reported (§4), not backed off.

### 4. The watchdog only reports

Decided in review (2026-11-27): "Frozen stays frozen. Doesn't mask the issue." The watchdog takes no action, in live play or in tests. An agent that freezes stays frozen until the cause is fixed.

- It watches for three conditions: an activity repeatedly refused, no progress through daylight, and work that keeps starting and stopping (`cq-xr7.4`).
- Only an action the game confirms counts as progress. Counting each send hid a 36-minute block-breaking loop (`cq-6nz`).
- A firing is a bug. It is logged at error level with the activity and what it was attempting, flagged in play test summaries, counted by `stack-metrics`, and fails simulation and replay tests.
- Its actions are deleted: taking the activity off the stack (`stonewalls`), asking for a fresh look, and backing off thrashing work.

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
| 1 | Watchdog reports only; delete `stonewalls` and the thrash back-off. Ratchet tests (sizes, fields, state calls) | yes |
| 2 | `Memory` type; move the activity back-offs into it | none intended |
| 3 | Move place, trunk and block records into it | none intended |
| 4 | `Refused` event; survey as a child; delete `refusals` (the watchdog counts `Refused` instead) | yes |
| 5 | One churn rule; delete `popNoWork`, `cooled`, and `skipped` if possible | yes |

The watchdog goes first so that every later play test shows what is really wrong. Stages 1, 4 and 5 each get a sandbox play test before the next begins.

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
- Replay golden files will change in stages 1, 4 and 5, with the reason recorded.
- With no safety net, play tests may show more frozen agents until the causes are fixed. This is intended.

## Open questions for review

1. **Size ceilings:** reset to today's sizes and ratchet down (proposed), or first split the seven oversized files back under the old ceilings?
2. **Order:** finish this before the night shelter bug `cq-xr7.3` (proposed)?
