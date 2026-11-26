# ADR-039: Planning Layer: Committed Plans, Target Commitment and Spatial Memory

## Status
Accepted (epic `cq-dcx`; implemented by `cq-dcx.1` spatialmem, `cq-dcx.2` craft latch, `cq-dcx.3` plan package, `cq-dcx.4` PlanActivity). Builds on [ADR-036](./ADR-036-frame-local-state-and-pure-step.md) (state homes, frames) and [ADR-037](./ADR-037-stuck-recovery-ownership.md) (a frame owns recovery of its own intents).

## Context
Playtest 2 showed no planning beyond reactive nearest-thing gathering: ~7 navigates per harvest, the same ground walked again and again, wander/nav targets re-chosen from whatever was visible each tick, and the wooden axe crafted 3-4 times because a craft was re-issued before the inventory reflected it. Nothing remembered where the agent had been or which trunks it had already felled.

## Decision

### 1. A plan is a frame-local value
`plan.Plan` (pure package `internal/system1/plan`: goal, ordered steps, BOM, site, lease) lives in the `plan` activity's Local (`planLocal`), with the committed target, the craft latch and throttles. `plan.Next` re-derives the remaining steps from live facts, so progress needs no cursor; `plan.Check` fires `lease_expired / recipe_missing / bom_drift / site_lost` and the frame calls `plan.Replan`. The first goal is `GoalToolProgression`; the bed plan (`cq-390.2`) uses the same package. The tool gate is the `cq-gpa.10` block model adapted as `Facts.ToolGate` (`planToolGate`: item -> source block -> `RequirementForBlock` -> `CraftTargetForRequirement`).

### 2. Steps delegate to existing machinery
Gather(tree) -> a `harvest_tree` task (framed as a `task_subroutine` child by self-push preemption) or a walk to a recalled timber POI; Gather(stone/ore) -> navigate + `HARVEST_BREAK` on a visible block the held tool drops; Craft -> `CraftItem` under the craft latch (inherited/handed down across frames, `a19304d`); Place -> crafting table; Goto -> navigate to the known table. The plan frame (priority 0.61, the Bootstrap/Colonize window) bids only while its next step is actionable; discovery (surveys, expeditions) stays with the bootstrap/exploration paths. An expedition that yields for timber pushes the plan instead of bootstrap when the plan can act. One plan frame per stack.

### 3. Target commitment with hysteresis
A gather target is chosen once and held until it is reached (felled), invalid (cooldown, unreachable, gone) or its 600-tick lease expires. While valid, a new candidate replaces it only if its score is below 0.5x the committed one. Candidates score distance + 6 blocks per unit of spatial revisit cost (+16 for a peer at the trunk). A trunk whose harvest ended with the trunk still standing is cooled down; tree gathering then backs off for 400 ticks and the plan pushes an expedition so the agent moves on. All numbers are design budgets.

### 4. Spatial memory is Memory, the plan bank is Store
`spatialmem.Snapshot` (visit grid, depleted marks) is the agent's own history and survives every pop: it is **Memory**, written only through Step.Memory writes (`spatialVisit`, `spatialDepleted`). Visits are telemetry-sourced: the kernel adapter applies one `spatialVisit` per tick from the agent position, counted once per cell entry (dwell is not retreading). The plan frame writes `spatialDepleted` for felled trunks and empty recalled stands. On exit (pop, yield, cancel) the plan frame writes a **Store** record `planBank{Plan, Target, Latch, Backoff}`; the next plan frame's `Init` resumes it while the plan lease is live (emergency is a push: the Local survives untouched). Both live in one value-typed `AgentFSM.planning` slot (like `surveyBackoffUntil`/`escapeLock`), not on `AgentState`.

### 5. Spatial cost steers exploration
The forage wander (+0.9 rad per leg) and the frontier scout pivot (+1.2 rad) become the default of a novelty choice over alternative headings (the default wins ties within 0.05, so an empty memory reproduces the old heading). Survey-bearing scoring adds 30 points per unit novelty of the leg. Novelty here is `1/(1+RevisitCost)` of the cell (O(1)); `spatialmem.Novelty`'s explored-sector halving scans all cells and is too slow per leg.

## Consequences
- Bootstrap crafting and harvesting are visibly owned by `plan/N` frames (replay goldens `bootstrap_gather_then_craft`, `craft_latch_*` changed accordingly); the plan holds a latched craft instead of surveying while the inventory lags.
- Playtest-2 scenario 8 (hungry forager exploring a 200x200 world for 5 minutes): revisit ratio 0.756 before, 0.015 after (budget < 0.25).
- The plan's BOM gathers timber for the whole progression, so an agent with a wooden kit keeps harvesting where the old waterfall stopped at 6 logs.
- Not yet: discovery driven by the plan itself (Goto/explore for an unknown resource), food/shelter/settlement plans and System 2 choosing among plans (`cq-dcx.5`), revisit metrics in the playtest report (`cq-dcx.6`).
