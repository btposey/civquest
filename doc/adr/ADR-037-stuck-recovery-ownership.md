# ADR-037: Stuck-Recovery Ownership: Java Reports Facts, Go Decides, Congestion Is Not Terrain

## Status
Proposed (Specifies the stuck/unreachable policy that [ADR-036](./ADR-036-frame-local-state-and-pure-step.md) §5 left open. Builds on [ADR-034](./ADR-034-single-owner-htpda-activity-stack-and-state-ownership.md) (one stack, scoped receipts) and [ADR-033](./ADR-033-strict-non-overlapping-entity-physics-and-pathing.md) (entity yielding). Amends [ADR-015](./ADR-015-in-engine-peripheral-reflexes-and-afferent-telemetry.md) for stuck reflexes only.)

Tracked by `cq-aik.15` (policy). Implementation: `cq-2sr.14` (Java) and a Go `Recover` activity (inside `cq-2sr.7`/`cq-2sr.9`).

## Context

### Java mod (as built)
- **Detection lives in `AgentNavigator`.** `tickNative()` counts `stuckTicks` when horizontal displacement is under 0.01 blocks squared while the yaw is aligned (`absYawDiff < 60`). At 4 ticks with `horizontalCollision`, or every 6th tick from 6, it writes the next path cell into `failedCells` (a Java-side terrain blacklist with `FAILED_MEMORY_TICKS`) and clears the path to force a replan. At 16 ticks it sets `NAVIGATION_STATE_STUCK`, zeroes velocity and stops sprinting. `enclosed` and cliff-edge-blocked agents are also reported as `STUCK` (`getReportedNavigationState`).
- **Java also decides recovery.** `AgentMotorFSM` enters `MotorState.STUCK_RECOVERY` when the navigator reports stuck, counts `stuckRecoveryTicks` up to `STUCK_RECOVERY_MAX_TICKS = 20`, then stops the navigator and fails the action with `"Navigation stuck recovery timeout"`. While in `STUCK_RECOVERY`, `canPreempt` rejects an identical `NavigateIntent` target so the state is not reset by retries. The state does nothing to un-stick the agent; it only delays the next intent by up to one second.
- **`AgentWatchdogFSM` is not a stuck mechanism.** It is the gRPC disconnect fail-safe (`DISCONNECTED_THRESHOLD_TICKS = 100`, 5 s without intents). It must not be conflated with, or consume, stuck facts.
- **Entity yielding is already separate from terrain (ADR-033).** `LocalPathPlanner` treats `occupiedByEntity` cells as non-traversable, `tickNative()` clamps movement against entity AABBs, sidesteps laterally when an entity (not terrain) blocks the heading, and never jumps onto a head. None of that is reported to Go: an agent held up by a peer ends up with the same `stuckTicks` and the same `STUCK` state as an agent walled in by terrain.
- **Facts exist on the wire but are unused.** `IntentReceipt.facts` is `MotorFacts{stuck, enclosed, blocked_bearing_deg, stuck_ticks, progress}` (`proto/civquest/v1/civquest.proto`, ADR-036 §5). `blocked_bearing_deg` uses the ADR-032 yaw convention. Today Go reads stuck from `TelemetryFrame.status.navigation_state` and `navigation_enclosed`, not from receipts.

### Go orchestrator (as built)
- **Three uncoordinated responders react to the same telemetry bit.**
  1. `stageSpatialAndSurvey` (and the death path in `HandleTelemetryFrame`) calls `AgentRuntime.handleNavigationStuck()` on every `NAVIGATION_STATE_STUCK` frame. It calls `RecordTargetNavigationStuck` (unless an expedition is active) and decays the nav-target POI's reliability by 0.25, removing it at ≤ 0.1.
  2. `EvaluateUnstuck` (`system1/unstuck.go`) is called by bootstrap, exploration, patrol, scouting and directive walks. It picks a heading-locked escape direction (`GetUnstuckLock`, renewed after 120 ticks), excavates diggable obstructions and issues its own `NavigateIntent`/mine intents, throttled by `unstuckNavCooldownTicks = 25`. It stands down when stuck and not enclosed while a task is active or `ActiveActivityOwnsStuck` is set.
  3. Per-behaviour code (`needs.go`, `shelter.go`, `task.go`) calls `RecordTargetNavigationStuck[Locked]`/`MarkTargetUnreachableLocked`. A target is blacklisted for `DefaultUnreachableTargetTTL` once it has failed twice.
- **Terrain/entity are not distinguished.** Every STUCK frame feeds blacklisting and POI decay, so two agents blocking each other in a corridor can blacklist a valid target and decay a valid POI.
- **The kernel-era scope replaced the old per-activity flag.** `ScopedActivity.OwnsStuckRecovery` no longer exists; `htpda.ScopeStuck` ("decides stuck recovery for its own Navigate children", `htpda/types.go`) is declared by `navigateActivity` (`ScopeMotor | ScopeStuck`) and mirrored by `kernel_adapter.go` into `AgentState.ActiveActivityOwnsStuck`, which `EvaluateUnstuck` reads as a veto. Expedition's flanking is the one fully frame-local recovery: `Navigate` pops with `navBlocked{bearing}`, `Expedition` `OpReplace`s with an extended `Blocked` list, and gives up at `expeditionFlankBudget`. Emergency, shelter, foraging and bootstrap each handle (or ignore) stuck in their own way; `EvaluateUnstuck` still writes `unstuck*` fields on `AgentState` (ADR-036 migration row 30, `unstuckAttemptCount`).
- **`handleNavigationStuck` is outside the kernel.** It runs in pipeline stage code, mutates `AgentState`/`SpatialIndex` directly, and cannot see which frame emitted the failing intent.

## Decision

**Principle.** *Java reports facts and applies only a local reflex. Exactly one Go component decides recovery, and it does so as a frame on the single activity stack.*

### 1. Java: facts and minimal reflex only
1. Java **reports** motor facts on every `IntentReceipt` (status `EXECUTING`, `FAILED`, `TIMEOUT`) and on telemetry navigation state. It never decides to retry, detour, dig, jump-spam or abandon a target.
2. The **local reflex** is the only thing Java may do on its own, and only to protect the agent within the current motor frame: zero velocity and stop sprinting on stuck (as today); ADR-033 entity depenetration, lateral sidestep and path replan around `occupiedByEntity`; and the cheap early replan (`failedCells` for the next path cell) that keeps A* from grinding on one cell. These are bounded by the frame's `Lease`, produce no intents, and do not change the motor's declared result.
3. When the lease or the stuck threshold is reached, the motor frame **ends** with `FAILED`/`TIMEOUT` and facts attached. Java then returns to `IDLE`.

### 2. Fact schema (`MotorFacts`)
| Field | Semantics |
|---|---|
| `stuck` | Navigator reached its stuck threshold (16 ticks of no displacement with aligned yaw) for the action this receipt belongs to. Level-triggered while the frame is live; never set by the watchdog. |
| `enclosed` | No walkable neighbour within the agent's reach: a pit/trench/pocket. Distinguishes "dig or climb out" from "route around". |
| `blocked_bearing_deg` | Yaw (ADR-032 convention, `[0,360)`) of the heading that was blocked, i.e. the first path cell direction when stuck was declared. Meaningful only if `stuck` or `enclosed`. |
| `stuck_ticks` | Consecutive no-progress ticks at report time. Monotonic within a frame, reset on displacement. |
| `progress` | Action-specific scalar: remaining horizontal distance for Navigate, mine fraction for Mine. Lets Go tell "stuck far away" from "stuck at the door". |
| `blocked_by_entity` (new, field 6) | True when, at stuck time, the blocking obstruction on the heading is an alive non-spectator entity (agent, player or mob) per ADR-033, not a block. Set when the lateral-sidestep/replan path was taken for an entity rather than terrain. |

`blocked_by_entity` is the single new proto field. It is additive (a default `false` from a legacy mod means "terrain", which preserves today's behaviour). Without it Go cannot distinguish congestion from terrain.

### 3. Go: one decision point, the `Recover` activity
1. **Single owner.** Stuck-recovery intents are issued only by a `Recover` activity frame. `EvaluateUnstuck`, `handleNavigationStuck`'s blacklisting and per-behaviour `RecordTargetNavigationStuck` call sites are deleted or folded into it. `ActiveActivityOwnsStuck` and `htpda.ScopeStuck` exist only to enforce that rule during migration and are removed with it.
2. **Who pushes it.** The **top frame owns recovery for its own intents**. When a frame receives a receipt for its own `frame_id` (ADR-036 seam) with `facts.stuck` or `facts.enclosed` (or a `FAILED` receipt whose reason is navigation stuck), its `Step` returns `OpPush{Kind: Recover, Local: RecoverArgs{Cause, Target, BlockedBearing, Enclosed, Facts, Budget}}`. Receipts for suspended/stale frames are dropped by the kernel (ADR-036), so a frame can never recover someone else's intent. The pusher's `Scope` is irrelevant; there is no per-activity opt-in flag.
3. **`Cause` is derived from facts, not from Java state.** `Congestion` if `blocked_by_entity`; `Enclosed` if `enclosed`; otherwise `Terrain`.
4. **`Recover` strategy by cause.**
   - `Congestion`: no digging, no detour commitment, no blacklist. Wait/yield a bounded number of ticks (lease), then re-emit the parent's intent; a peer in the way usually resolves. A second congestion result within the same `Recover` escalates to give-up (below), still without blacklisting.
   - `Terrain`: re-issue with the blocked bearing excluded (reusing the heading-lock and excavation logic of `unstuck.go`, now frame-local in `RecoverLocal`: locked direction, start tick/position, attempt count).
   - `Enclosed`: pit/trench escape (staircase excavation or climb) as today, using the same logic.
5. **Intents and receipts.** `Recover` is the only frame that emits recovery intents; they carry its own `frame_id`, so receipts are scoped to it (ADR-034/036). Success means the next receipt shows `progress` improved, or displacement ≥ 1 block from `RecoverLocal.Start`; `Recover` then pops with `Result = Recovered{}` and the parent re-emits.
6. **Leases bound every attempt.** `Recover` is initialised with `Lease{Start, Deadline}` (default 100 ticks, i.e. 5 s at 20 Hz; per-cause overrides allowed). The motor frame it issues carries its own shorter lease (≤ 20 ticks per attempt, replacing `STUCK_RECOVERY_MAX_TICKS`). Expiry always ends the frame (ADR-036 deviation 6): `Recover` pops with `Timeout`, never loops.

### 4. Blacklisting rules
Blacklisting is a **Memory** write (ADR-036 §1) applied by the kernel from `Step.Memory`, and only by `Recover`:
- **Never** on `Cause == Congestion`, and never when `blocked_by_entity` was true for any receipt in the episode.
- On `Terrain`/`Enclosed` give-up, write `Unreachable{Pos, Until}` with the existing `DefaultUnreachableTargetTTL`. The current "second failure within TTL" rule becomes "a `Recover` that gave up counts as one failure"; two give-ups blacklist.
- POI reliability decay (`HandleNavigationStuckForPOI`, −0.25) runs only on a terrain give-up whose parent target maps to a POI, as an effect of the pop result (`Blocked{Target}`), not from telemetry.
- While an expedition's `Navigate` is being flanked, targets are not blacklisted (as today); expedition tracks `Blocked` bearings instead.
- The Java `failedCells` set remains a short-TTL, Java-local replan aid. It is not reported, not authoritative and not shared with Go.

### 5. Failure escalation
When `Recover` exhausts its budget or lease it pops with `Result = Blocked{Bearing, Target, Cause}`. The parent's `ChildDone` step decides:
- **Expedition** `OpReplace`s with `Blocked` extended by the bearing (flank), as today, until `expeditionFlankBudget`.
- **Task/foraging/shelter** abandon or retarget (existing behaviour) after the Memory write of §4.
- **Congestion** give-up pops `Blocked{Cause: Congestion}`; parents treat it as "retry later", not as "target invalid".
- If the top frame has no handler for `Blocked` it pops with the same result; the root activity idles. `Recover` cannot push `Recover` (kernel `MaxDepth` rejects a `Recover` whose parent is `Recover`, answering `Rejected`).

### 6. What Java deletes
`MotorState.STUCK_RECOVERY`, `AgentMotorFSM.stuckRecoveryTicks` (+ getter/setter), `STUCK_RECOVERY_MAX_TICKS`, the `navigation_stuck` transition and its `canPreempt` case, and `IntentActuator`'s handling of `STUCK_RECOVERY` batches. `AgentNavigator` keeps detection (`stuckTicks`, threshold 16) and the local reflex of §1, but loses any "decide to give up" role beyond ending the frame. `AgentWatchdogFSM` is untouched. This is `cq-2sr.14` ("no `stuckRecoveryTicks` field").

### 7. What Go deletes
`EvaluateUnstuck` call sites in bootstrap/exploration/patrol/scouting/directive walks (logic moves into `Recover`); `AgentRuntime.handleNavigationStuck` and its stage-5 call; `AgentState.unstuck*` fields and the `GetUnstuckLock*` family; `ActiveActivityOwnsStuck`/`SetActiveActivityOwnership` and `htpda.ScopeStuck`; scattered `RecordTargetNavigationStuck*` calls.

### 8. Pillar (jump + place-under-self): accepted gap, no proto added
Investigated for `cq-2sr.23`. The wire has no jump or place-under-self capability: `AgentIntent.action` offers Navigate, Attack, InteractBlock, UseItem, Craft, Chat, Trade, Caravan, `BuildBlockIntent{target_voxel, block_state_string, is_temporary_scaffold}`, books, signposts, containers and SurveyHorizon. `BuildBlockIntent` can name the voxel under the agent's feet, but Java performs no jump and the Navigator only jumps for path step-ups (ADR-033 forbids it onto entities), so the placement cannot be sequenced with an upward impulse in one motor frame; expressing it would need a new `Pillar` intent (or a `jump` flag on BuildBlock) plus a new motor, with mid-air placement timing and fall-damage rules.

**Decision: do not add proto.** Escaping an enclosed pit is break-and-step-out only: `Recover{Enclosed}` excavates a staircase / overhead cell and issues a step-out Navigate, which the navigator can climb because it already performs one-block step-up jumps. The accepted gap: a pit deeper than the agent can staircase out of (walls it cannot break, or no tool) is given up as `Blocked{Enclosed}` (two give-ups blacklist) rather than pillared out. Revisit only if soak runs show agents lost in unbreakable pits; the cheapest future shape is a `pillar_up` flag on `BuildBlockIntent` handled by a Java `Pillar` motor that jumps then places, reporting `MotorFacts` like any other frame.

## Consequences

### Positive
- Exactly one component (`Recover`) issues recovery intents; the 25-tick unstuck throttle, the Java 20-tick recovery timeout and per-behaviour retries stop fighting one another.
- Congestion never poisons the unreachable list or POI reliability, so peers sharing a corridor (ADR-033) cannot blacklist valid targets.
- Recovery state is frame-local and discarded on pop, consistent with ADR-036; escalation uses the existing `Blocked` pop contract Expedition already relies on.
- Java loses its largest decision branch and the dead `STUCK_RECOVERY` delay.

### Negative / Trade-offs
- **Latency.** Recovery now crosses the bridge (receipt → Go step → intent), adding up to one Go tick (5 Hz = 200 ms) to the first recovery action. The local reflex covers the first second.
- **Proto change** (`blocked_by_entity`) and a mod/orchestrator version coupling; legacy mods report `false` and are treated as terrain.
- **Telemetry vs receipt.** Stuck is currently read from telemetry. During migration both sources may be seen; `Recover` consumes receipts only, and the telemetry path is removed with `handleNavigationStuck`.
- **Behaviour risk.** Congestion no longer blacklists, so a genuinely wedged agent that Java mislabels as entity-blocked could retry longer; the `Recover` lease and the second-congestion give-up bound this.

### Test plan
- **Java unit:** navigator sets `blocked_by_entity` only when an entity AABB blocks the heading; terrain walls leave it `false`; `stuck_ticks` monotonic and reset on displacement; no `STUCK_RECOVERY` state exists; watchdog tests unchanged.
- **Go unit (`htpda` + system1):** `Recover` push from the owning frame only; stale-frame receipts are ignored; `Congestion` never writes `Unreachable` or decays POIs; two terrain give-ups blacklist; lease expiry pops `Timeout`; `Recover`-in-`Recover` rejected; Expedition flank on `Blocked` (extend existing expedition tests).
- **Replay scenarios** (`replay_scenarios_test.go` goldens, recorded as INTENTIONAL where they change): pit escape, wall-with-gap flank, expedition rim blocked, two agents in a one-wide corridor (no blacklist), emergency during `Recover` (preempt then resume).
- **Epic 16 closed-loop scenarios** stay green; add a congestion variant.
- **Soak:** multi-agent run with deliberate corridor congestion; assert no watchdog false alarms (no `DISCONNECTED_SAFE_MODE` entry from stuck), no unexpected blacklist growth, `Recover` frames always popped within lease.
- `./scripts/check-arch-budgets.sh --strict` must not regress (this removes code).
