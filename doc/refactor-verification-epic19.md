# Epic 19 (frame-local HTPDA rewrite): post-refactor verification vs pre-refactor baseline

Beads: cq-2sr.18 (gate), cq-2sr.20 (comparison report). Date: 2026-11-25.
Baseline: commit 27584d2 in worktree `../civquest-baseline`, results in `build/baseline/`.
After: the uncommitted working tree of `main` (HEAD is still 27584d2; the whole Epic 19 change set is uncommitted), results in `build/after/`.

## 1. Verdict

**REGRESSIONS FOUND (1 measured performance regression; 2 coverage/behaviour items to verify). No functional pass->fail regression in any recorded suite.**

| # | Finding | Severity | Bead |
|---|---|---|---|
| R1 | `TestTimeWarp_Performance_24000TicksUnderOneSecond` median 0.995 s (baseline) -> 1.361 s (main), +37%. Fails its 1 s gate 5/5 when run standalone on main (passes inside the full hermetic run: 0.73 s vs 0.46 s baseline). Under `-race` 4.10 s -> 4.83 s, 2 of 5 main runs exceed the 5 s race gate. | measured regression | cq-aik.29 |
| R2 | Six baseline tests were deleted with no replacement test (tree-trunk focus, movement unlocks, angular fan-out cooldown, scouting stuck dig-through, ...). See section 4. | lost coverage | cq-aik.30 |
| R3 | Two replay-golden drifts are REGRESSION-SUSPECT, not proven either way (bootstrap `t=250` emits no Navigate; Survey frame holds silently until report/lease). See section 7. | unverified | cq-aik.31 |

What is clean: 0 pass->fail across 2135 recorded results; 2 fail->pass (both are the same pre-existing `-race` timing flake, see section 3); Java 278 vs 232 tests all pass; live-server integration and playtest harness pass.

What does NOT support a "no regression" claim: the playtest harness does not exercise System 1 at all (section 5), soak was not run (section 9), and R1 is real.

## 2. Method

Identical suites and file names as `build/baseline/README.md`, run on the main tree into `build/after/`:

| Output | Command | rc |
|---|---|---|
| go-hermetic.json | `cd orchestrator-go && go test -count=1 -json ./...` | 0 |
| go-race.json | `go test -race -count=1 -json ./internal/agent/... ./internal/system1/...` | 0 |
| junit/*.xml, java-test.log | `./gradlew --offline :mod-neoforge:cleanTest :mod-neoforge:test` | 0 (the baseline run used `test` without `cleanTest`) |
| arch-budgets.txt | `./scripts/check-arch-budgets.sh` | 0 (report mode) |
| playtest/*, playtest-harness.log | `./scripts/run-playtest-harness.sh` | 0 |
| integration-live.log | `./scripts/run-integration-test.sh` (`-tags integration`) | 0 |

Diff: `scripts/test-baseline.py diff build/baseline build/after` (full output `build/after/diff.md`). Performance: `build/after/perf/` (profiles, logs, `run.sh`). Replay ports: `build/after/replay-port/`.
Not run: `-tags livellm` (needs a live LLM endpoint), soak (harness cq-aik.17 not built).
The `./gradlew :mod-neoforge:build` registry-init flake noted in cq-2sr.15 did not occur in either tree in these runs (both used the `test` task).

## 3. Test deltas

| Suite | Baseline | After |
|---|---|---|
| go-hermetic (tests+subtests) | 1250 pass, 26 pkg pass | 1295 pass, 27 pkg pass |
| go-race (agent+system1) | 488 pass, **1 fail** (+1 pkg fail) | 532 pass, 0 fail |
| Java (JUnit cases) | 232 pass | 278 pass |
| Total results | 1997 pass, 2 fail | 2135 pass, 0 fail |

- Regressions (pass->fail): **0**.
- Fixed (fail->pass): 2, both `go-race: internal/system1` / `TestTwoTierHFSM_Sub5msExecutionBudget` and its package. This is the known timing flake, not a logic fix: in my isolated race runs it passed 5/5 on baseline and failed 1/5 on main (7.16 ms max tick). It is flaky in both trees.
- Missing after: 29 go-hermetic + 28 go-race + 19 java entries (go-race is the same tests as hermetic; unique Go top-level tests 23). Every one is accounted for in section 4.
- New: 74 go-hermetic + 71 go-race + 65 java entries (kernel `htpda` package tests, Recover, Expedition, Shelter frame, frame-id receipt routing, MotorStack/Motors/Sweep/IntentHandlers, MotorFacts fixture recorder, sim-vs-Java contract tests).
- Slower (>2x and >0.5 s): 1 Java test, `PeripheryHarnessTest$ActuationValidation.CRAFT_ITEM 3x3 ...` 0.30 s -> 0.94 s (likely one-off JIT/world setup; not investigated).
- Pre-existing, unchanged status: test-binary wall time of `internal/eval` is longer on main (see R1).

## 4. Missing / deleted tests

Every baseline test absent after the refactor, with where it went. "Replaced" means a test with the same intent exists; "Lost" means no equivalent test was found.

| Baseline test | Disposition |
|---|---|
| `TestActivityArbiter_AmbientUnstuckStealsZeroTicksFromActiveActivities` (+4 subtests) | Replaced: replay scenario `ambient_unstuck_does_not_steal_activity`, `TestRecover_PushedOnlyByOwningFrameOnStuckReceipt` |
| `TestActivityArbiter_ScopedReceiptRejectsStaleAndPreemptedBatches` | Replaced: `TestActivityKernel_ReceiptsRoutedByFrameID`, replay `stale_receipt_after_preemption_ignored` |
| `TestActivityArbiter_UnifiedSelectionAndSkillRegistry/survey_explorer` | Renamed subtest: `.../expedition_navigate` |
| `TestActivityStack_EmergencyPreemptSuspendsAndResumes` | Replaced: `TestActivityStack_EmergencyIsAPushAndResumeKeepsFrameLocal` |
| `TestActivityStack_StageDeadClearsAllActivitiesTopDown` | Replaced: replay `death_clears_activities` |
| `TestActivityStack_Tier1ExclusivityNestedPushAndDonePop` | Replaced: `htpda` kernel tests (`TestChildResultDeliveredToParentWithIntactLocals`, depth/cascade limits) |
| `TestAgentState_ShelterBuildPhaseAndIndex` | Accessors deleted; covered by `TestShelterFrame_*`, `TestShelterLocal_SeededFromStoreAndBankedOnExit` (replaced) |
| `TestChainedSurveyTraverseSurveyLoop_FindsDistantForestAndIndexesPOIs`, `TestHorizonExplorer_DuplicateSurveyGuardAndArrivalProximity`, `TestMultiMemberClan_FanOutAndRendezvousReturn`, `TestRavineMidTraverse_RimSurveyAndFlankFallback`, `TestSoloExplorer_NeverReturnsAndClaimsHomestead`, `TestSurvivalReflexesStrictlyPreemptSurveying` | Renamed/rewritten as `TestExpedition_*` (SurveyThenTraverseAsFrames, SurveyTimeoutResurveysAfterGuard, ClanExplorersFanOutOnHopZero, FlankIsReplaceWithExtendedBlockedList, SoloNeverReturnsAndClaimsHomestead, EmergencyAbortsSurveyAndResweeps) (replaced). The "FindsDistantForestAndIndexesPOIs" POI-indexing assertion is partially covered by `TestExpedition_BootstrapStartsSurveyWhenNoTimberVisible`. |
| `TestEvaluateStrategicDirective_ScoutFrontierFanOutAndCompletion` | Renamed `TestStrategicDirective_ScoutFrontierFanOutAndCompletion` |
| `TestEvaluateUnstuck_PitEntrapment`, `TestEvaluateUnstuck_CeilingOverheadClearance` | Replaced by `TestRecover_EnclosedDigsOutThenStepsUp` (overhead break then step-out), `TestRecoverContract_RealEnclosedReceiptDigsOut` |
| `TestEvaluateUnstuck_MovementUnlocks`, `TestEvaluateUnstuck_CooldownAngularFanOutAndActiveTaskSuppression` | **Lost.** unstuck.go deleted; Recover tests cover attempt budget/blacklist/yield but not movement-unlock detection or angular fan-out cooldown. |
| `TestEvaluateBootstrapTimber_TreeTrunkFocus` | **Lost.** Asserts bootstrap keeps targeting the same trunk after the bottom log is felled; `AgentState.targetTree*` removed, only `shelterLocal` keeps a focus. No replacement test. |
| `TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction` | **Lost, behaviour removed.** Scouting no longer digs an adjacent obstruction on `NAVIGATION_STATE_STUCK` (it rotates heading ~126 deg); dig-through exists only in Recover(Enclosed). |
| `TestEvaluateSleep_BlacklistsUnreachableAssignedBedAfterTwoStucks` | Semantics changed: replaced by `TestEvaluateSleep_TelemetryStuckDoesNotBlacklistAssignedBed` (inverse assertion). Receipt-fact blacklist path is covered by Recover tests. |
| `TestHandleTelemetryFrame_PipelineGoldenSequence/03_navigation_stuck_decays_poi_waits_cooldown` | Renamed `.../03_navigation_stuck_telemetry_waits_cooldown`; golden changed (10-line diff in frame_pipeline_golden_test.go). The "decays POI" half moved to `TestRecover_TwoTerrainGiveUpsBlacklistAndDecayPOI`. |
| `TestIntegration_ZeroProgressActiveTask_TriggersDaytimeIdleAndPreemptsWithScoutFrontier` | Split into `..._ZeroProgressActiveTask_RecoversOnItsOwnAndMovesOn` and `..._ZeroProgressBoxedIn_TriggersDaytimeIdleAndPreemptsWithScoutFrontier` (replaced; first half's assertion changed to "recovers on its own"). |
| `TestTwoTierHFSM_PushdownAutomata/Multi-Level_Push_Pop_(Dialogue_Interrupt_and_Emergency)`, `/Stack_Bounds_and_Duplicate_Top_Suppression` | Two subtests deleted from hfsm_test.go (~200 lines removed). Intent is covered by kernel tests (`TestDepthAndCascadeLimits`, `TestRejectedPushes`) but "duplicate top suppression" and the dialogue-interrupt case have no explicit equivalent. |
| Java `AgentMotorFSMTest`: 13 tests built on the `MotorState` enum / STUCK_RECOVERY | Replaced by frame-based tests: `MotorStackTest`, `MotorsTest`, `AgentMotorFSMTest` (ADR-037, Dodge/Flee/Combat reflex frames, Reflex nesting, "no STUCK_RECOVERY state"). `supports transitions across all MotorState values` and `rejects transition to identical active state` are obsolete by design (enum deleted). |
| Java `AgentEntityTest`, `IntentActuatorTest`, `AgentWatchdogFSMTest`, `PeripherySeparationAndQueueRegressionTest`, `PeripheryTerrainHazardIntegrationTest` (6 tests) | Renamed with changed wording (STUCK_RECOVERY -> stuck failure, SURVEYING -> Sweep, COMBAT_ENGAGED -> Dodge reflex frame); same scenario. |

No test was silently dropped without a named disposition; five Go behaviours are **lost** (R2, cq-aik.30).

## 5. Playtest metrics (`build/baseline/playtest/playtest-report.md` vs `build/after/playtest/playtest-report.md`)

| Metric | Baseline | After |
|---|---|---|
| max_radial_distance_m (frontier) | 64.00 | 64.00 |
| locked_azimuth_rad / max_angular_variance / spin_oscillations_count | 0.00 / 0.00 / 0 | 0.00 / 0.00 / 0 |
| pathing_observed / arrived_observed | true / true | true / true |
| blocks_paved / min_campfire_dist_m / threshold_15 / campfire_exclusion | 11 / 11.66 / true / true | 11 / 11.66 / true / true |
| shelter_blocks_placed / bottom_up_ordering / aabb_solid_intersections (entombment) | 4 / true / 0 | 4 / true / 0 |
| stall_timeout_freezes / fsm_oscillations_count | 0 / 0 | 0 / 0 |
| crafting 3x3 distant-rejected / nearby-accepted / 2x2 | true / true / true | true / true / true |
| drops: natural silent / gift collected / thank-you | true / true / true | true / true / true |
| Session duration | 153 ms | 176 ms |

The tables are identical (only key order and timestamps differ). **This carries almost no information about Epic 19.** The report claims 24000 world ticks in 153-176 ms, and `test/integration/playtest_scenarios_test.go` contains no reference to `NewAgentFSM`, `system1.` or `AgentRuntime`: the scenarios script the world and assert on their own synthetic trace, they do not drive the System 1 FSM. Identical metrics therefore cannot show that agents still explore/pave/build under the new stack. The "exploration frame holds with no Navigate" watch item from cq-2sr.20 is NOT answered by this harness; the only supporting evidence is the TimeWarp simulation (section 6) where the main agent explores farther than baseline (2.8 km vs 1.2 km at tick 24000). `-tags integration` live-server tests (`TestLiveServerEndToEnd`, `TestInWorldPlaytestScenarios_*`, `TestSystem1ImpulseAgentHarness_*`) pass in both trees.

## 6. Performance (measured)

Machine: 12 logical CPUs, other load average ~3. Same Go toolchain, both trees, runs interleaved by tree, `-count=5`. Raw logs and pprof files in `build/after/perf/`.

### 6.1 TimeWarp 24000 ticks (`internal/eval`, gate 1 s, 5 s under -race)

| Mode | Baseline runs (s) | Baseline median | Main runs (s) | Main median | Delta |
|---|---|---|---|---|---|
| no profiler | 0.831 0.986 0.995 1.009 1.051 | **0.995** | 1.209 1.332 1.361 1.363 1.375 | **1.361** | +37% |
| `-cpuprofile` | 0.884 0.902 0.914 0.942 0.978 | 0.914 | 1.207 1.214 1.217 1.250 1.282 | 1.217 | +33% |
| `-race -cpuprofile` | 3.924 4.075 4.095 4.115 4.446 | 4.095 | 4.438 4.668 4.833 5.026 5.158 | 4.833 | +18% |

Gate outcome: baseline passes 5/5 under profiler (and about 3/5 unprofiled: 1.009 and 1.051 exceed 1 s, so the baseline sits on the edge); main fails 5/5 non-race and 2/5 under race. Inside the full `go test ./...` run the test passed on both trees (0.46 s baseline, 0.73 s main, +59%).

### 6.2 System 1 `AgentFSM.Tick` latency (throwaway timed loop, not committed; 3 x 20000 ticks after 2000 warm-up, idle explorer, same scenario as the Sub5ms test)

| | p50 | p90 | p99 | p99.9 | max |
|---|---|---|---|---|---|
| baseline | 132.4 us | 259.4 us | 791.7 us | 1.088 ms | 2.11 ms |
| main | 133.9 us | 268.4 us | 895.7 us | 1.337 ms | 4.21 ms |
| baseline -race | 617 us | 1.243 ms | 2.044 ms | 2.557 ms | 4.21 ms |
| main -race | 659 us | 1.253 ms | 2.044 ms | 2.642 ms | 8.64 ms |

Per-tick FSM cost: p50 unchanged (+1%), p99 +13% (non-race), tail max is noise-dominated. All are far below the 5 ms design budget (p99 under 1 ms without -race). These are measured numbers for the idle-agent scenario only; they do not include the full frame pipeline, sqlite writes, or a busy agent. Existing Sub5ms test means: 146-198 us (baseline) vs 186-197 us (main); race mean 790-870 us vs 750-880 us.

### 6.3 Top cumulative functions (non-race, 5 runs, eval package, cum seconds of CPU samples, goroutines summed)

| Rank | Baseline | cum | Main | cum |
|---|---|---|---|---|
| 1 | `AgentRuntime.HandleTelemetryFrame` | 4.54 | `AgentRuntime.HandleTelemetryFrame` | 6.21 |
| 2 | `FramePipeline.Run` | 4.40 | `FramePipeline.Run` | 5.96 |
| 3 | `stageFactGraphAndSystem1` | 4.15 | `stageFactGraphAndSystem1` | 5.40 |
| 4 | `evaluateSystem1Reflexes` | 4.14 | `evaluateSystem1Reflexes` | 5.34 |
| 5 | `AgentFSM.Tick` | 3.61 | `runtime.gcBgMarkWorker` | 5.34 |
| 6 | `EngineRegistry.EvaluateAll` | 3.16 | `AgentFSM.Tick` | 4.38 |
| 7 | `runtime.gcBgMarkWorker` | 3.12 | `EngineRegistry.EvaluateAll` | 3.41 |
| 8 | `database/sql ... ExecContext` (sqlite) | 2.80 | `database/sql ... ExecContext` (sqlite) | 2.74 |
| 9 | `evaluateTopologicalMapping.func1` | 2.49 | `evaluateTopologicalMapping.func1` | 2.60 |
| 10 | `FactGraph.Set` | 1.53 | `FactGraph.Set` | 1.60 |

Under `-race` both trees are dominated by sqlite exec (`evaluateTopologicalMapping.func1` 14.9 s baseline vs 17.95 s main cum, +20%).

### 6.4 Attribution of the slowdown

Diff profile (main minus baseline) and alloc profiles show:
- GC: `gcBgMarkWorker` 3.12 s -> 5.34 s (+71%), `scanObject` +1.27 s. Total allocation per test run 340 MB -> 443 MB (+30%).
- `AgentState.SafeNodes()` (defensive per-frame copy of the cached safe-node slice, called once per frame in `evaluateSystem1Reflexes`) allocates 90.8 MB -> 188.7 MB (2.1x).
- `FactGraph.Set` allocations are unchanged (213 MB -> 208 MB); kernel `htpda.Kernel.Dispatch` adds only 5.6 MB of allocation but about +0.49 s cum CPU over 5 runs (+0.1 s per run, roughly a quarter of the +0.37 s gap).
- The reason SafeNodes grew: the simulated agent's trajectory differs between trees. Measured safe-node count and position at ticks 4000/8000/.../24000: baseline 100, 183, 210, 214, 215, 217 nodes (agent stays within roughly 1.2 km of origin); main 100, 212, 336, 470, 604, 738 nodes (agent keeps travelling, 2.8 km out at tick 24000). The two trees are identical until about tick 4000 and diverge after.
- Conclusion: the +37% is mostly a workload difference (main keeps exploring and mapping, so per-frame O(nodes) copying, GC and sqlite writes grow linearly), plus a small constant kernel dispatch cost. The FactGraph freeze/copy hypothesis is not supported: `FactGraph.Set` cost is flat, and FSM Tick p50 is unchanged. Candidate fixes (not applied; production code untouched): (1) make `SafeNodes` return an immutable shared snapshot, copy-on-write in `SetSafeNodes`; (2) cap/prune the cached safe-node list; (3) batch `AddMapNode`/`AddMapEdge` sqlite writes (16-20% of CPU in both trees); (4) re-scope the 1 s gate to a fixed workload so it measures per-tick cost rather than exploration breadth. Caveat: whether the divergent trajectory itself is desirable (main's agent travelling monotonically outward vs the baseline stalling) was not judged.

## 7. Golden drift (replay harness)

Method: `replay_test.go` and `replay_scenarios_test.go` plus `testdata/replay` were copied into the baseline worktree's `internal/system1/`, adapted, run with `-update-replay` to generate ORIGINAL goldens, then removed from the worktree (`git status` clean). The adapted copies and the original goldens are preserved in `build/after/replay-port/` (`original-goldens/`).
Adaptations (beyond `observeStack`, which now reads `fsm.ActiveActivity().Kind()`/`ActivityDepth()`): no `frame_id` on receipts/intents (assignment removed); `MotorFacts` does not exist in the baseline proto, so `terrainStuck/peerStuck/enclosedStuck` become plain FAILED receipts with reason "Navigation stuck" and no facts; `StartTask`/task probe use `AgentState` APIs; `expedition_flank` probe prints `ExpeditionPhase` instead of the stack; a `recover` kind constant shim. Scenario `Check` functions assert main behaviour and fail on baseline (5 of 13), but goldens are written before checks run. **Scenarios that depend on stuck facts are NOT like-for-like** (baseline never saw the stuck condition): marked N/C below.

| Scenario | Drift (original -> current) | Class | Reason |
|---|---|---|---|
| ambient_unstuck_does_not_steal_activity | t=190 Navigate(0.5,3.3) from ambient unstuck -> harvest continues; t=220 harvest -> Recover(Enclosed) Navigate | INTENTIONAL-FIX (N/C for t=220) | Baseline unstuck stole the task on telemetry stuck; ADR-037/cq-aik.12 removes it. The enclosed receipt cannot be expressed in baseline. |
| bootstrap_gather_then_craft | t=250 Navigate(-46.1,-11.1) -> no intent | **REGRESSION-SUSPECT** | Exploration frame holds without a Navigate on its first tick (known watch item); scenario ends there so the next tick is unobserved. Bead cq-aik.31. |
| death_clears_activities | stack `-/0` -> `dead/1` | INTENTIONAL-FIX | Dead is an activity (cq-2sr.6). |
| directive_preference_over_stale_task | t=204 routine DAWN_PREP/empty stack -> PRIMARY_DUTY/survey/2; t=210 baseline starts harvest task + Navigate, main emits nothing (survey holds) | INTENTIONAL-FIX with **REGRESSION-SUSPECT** idle | Directive lapse quirk fixed (survey holds to report/120-tick lease, cq-2sr.7/.9); possible idle window not proven bounded. cq-aik.31. |
| emergency_preempts_recover_then_resumes | Recover frame now present; emergency is `emergency/3` | N/C | Baseline has no Recover; facts not expressible. |
| expedition_flank | Baseline repeats Navigate(8.8,-46.8) and returns to idle at t=205; main alternates Survey/Navigate/Recover with new bearings (41.6,-4.0), (12.0,39.6) | INTENTIONAL-FIX (N/C) | Flank via Replace with extended blocked list (cq-2sr.7); baseline never saw the receipt facts. |
| foraging_fallthrough_on_empty | stack `-/0` -> `foraging/1` -> `exploration/1`; intents identical (empty) | INTENTIONAL-FIX | Frame holds / falls through instead of dead-ending (cq-2sr.9); no behavioural intent difference observed. |
| peer_yield_no_terrain_blacklist | target blacklisted at t=140 in baseline, not in main; Recover(congestion) frames | N/C | Needs blocked_by_entity fact. Baseline blacklist outcome is the bug the scenario guards (it stays true only on the main side because of facts). |
| reflex_preempts_mining_then_resumes | stack column during emergency `task_subroutine/2` -> `emergency/3`; intents identical | INTENTIONAL-FIX | Preemption is a push on the single stack (cq-2sr.5). |
| shelter_resume_after_flee | stack `-/0` -> `shelter/1`; t=170 baseline Survey(36) (lost progress) -> shelter resumes, no intent | INTENTIONAL-FIX | Shelter progress persists across suspension (cq-2sr.8). |
| stale_receipt_after_preemption_ignored | t=360 taskFailures=1 + re-emit -> taskFailures=0, re-emit moves to t=370 | INTENTIONAL-FIX | Stale FAILED no longer counted (cq-2sr.5/.11). |
| task_subroutine_survives_emergency | stack column `task_subroutine/1` -> `emergency/2`; intents identical | INTENTIONAL-FIX | Same as above. |
| terrain_stuck_blacklists_target_control | Recover sidestep frames vs baseline continuing harvest then Survey | N/C | Facts not expressible in baseline. |

Counts: 13 scenarios, all differ from the original; 0 identical. INTENTIONAL-FIX 6 clear (death, foraging, reflex, shelter, stale receipt, task-survives) + 2 (ambient, flank) with N/C caveat; N/C 3 (emergency_recover, peer_yield, terrain_stuck); REGRESSION-SUSPECT 2 (bootstrap t=250, directive survey hold). Note the stack-column drift on every scenario that includes an emergency is by design; the comparison of intents is the meaningful signal.

## 8. Known behaviour changes (from bead notes and ADR-036/037 Deviations)

| Change | Bead | Class |
|---|---|---|
| One stack replaces fsm.stack/activityStack/task_frame/PushedToPDA; Emergency/Dead are activities; routine derived after dispatch (directive scenario routine DAWN_PREP -> PRIMARY_DUTY) | cq-2sr.5, .6 | intentional |
| Stale/duplicate receipts dropped by frame-id lookup; stale FAILED no longer counts as task failure; ActivityReceiptScope deleted | cq-2sr.5, .11 | intentional fix |
| ReflexNotices now dispatched as events (previously ignored) | cq-2sr.6 | intentional fix |
| Shelter progress is a frame Local and persists across ticks and suspension; directive BUILD_SHELTER / exploration reflex can no longer place cabin blocks without a Shelter frame (made a bid source in .9); shelter OpStay hold may starve lower activities at night (flagged risk, unmeasured) | cq-2sr.8, .9 | intentional; starvation risk unverified |
| Expedition as frames; FlankCount rule `>=3` becomes `len(Blocked)>=4`; survey storm guard no longer spans expeditions; hops not kept across exits; bootstrap expeditions yield instead of exiting; a same-tick falling-through preemptor no longer aborts a survey | cq-2sr.7 | intentional |
| Foraging no longer dead-ends after one wander; directive lapse fixed; SCOUT_FRONTIER is a Propose bid; FORAGE_FOOD/BUILD_SHELTER/CRAFT_SURVIVAL_KIT become bid sources; bootstrap exploration walks request expeditions as a step result | cq-2sr.9 | intentional fix |
| Read-only FactGraph; 4 skill fact write-backs removed; `effectiveHasBed` shim removed (a cleared bed is seen one tick later) | cq-2sr.3, .8 | intentional |
| Java: batch is one frame per intent; MotorState enum and BatchSession cursor gone; batch lease timeout is `RECEIPT_STATUS_TIMEOUT` (was ABORTED); Dodge/Flee/Combat are reflex frames with ReflexNotice on pop; ATTACK is a non-reflex Strike frame (60-tick window: batch reports EXECUTING not PREEMPTED_BY_REFLEX); reflex frame ids from 1<<40 | cq-2sr.12, .14, .15, .16 | intentional |
| Stuck: Java fails stuck/enclosed Moves immediately with MotorFacts; Go Recover activity decides (sidestep, dig-out, yield, blacklist after two terrain give-ups); ambient unstuck removed; scouting STUCK branch no longer digs; `AgentNavigator.tickEnclosedEscape` (Java dig-out/pillar) no longer runs in the FSM flow | cq-2sr.14, .22, .23, ADR-037 | intentional; scouting dig-through and unlock/fan-out tests lost (R2) |
| TRANSFER_CONTAINER and CARAVAN have no implementation in either tree (no handler in baseline or main Java); main fails the intent with a named reason (`unsupportedIntentFailsInsteadOfStallingTheBatch`). Baseline behaviour on the unsupported intent was not measured | cq-2sr.16 | not verified |
| Untested by .17's own note: inline-started task dropped if its starting frame exits before next tick (since covered by `TestInlineStartedTaskSurvivesStartingFrameExit`) | cq-2sr.17 | fixed |

## 9. Not verified

- **Soak not run**: harness cq-aik.17 (N-agent soak/perf) is not built; no soak result is claimed.
- Live LLM suite (`-tags livellm`) not run.
- 5 Hz frame latency end to end and Java tick cost on a real server: only the Go FSM Tick (idle explorer) was measured; Java per-tick cost was not measured.
- Playtest harness does not exercise System 1 (section 5); no in-world behavioural comparison with real agents exists.
- Replay stuck-fact scenarios (4) are not like-for-like against baseline.
- Whether main's longer TimeWarp trajectory is the desired behaviour was not judged.
- Shelter OpStay night starvation risk and the Survey-hold idle window are unmeasured.
- Baseline `./gradlew :mod-neoforge:build` registry-init flake (cq-2sr.15) not re-tested; Java run used `cleanTest` and was green (278 tests; bead notes said 275).
- Arch budgets (strict mode not run; report mode): `AgentNavigator.java` 1760 -> 1836 lines (ceiling 1434, OVER, grew +76); `fsm.go` 1189 -> 871 (OVER its reset ceiling 852); `AgentRuntime` fields 57 -> 59; improvements: `runtime.go` 4121 -> 3875, `state.go` 1822 -> 1288, `IntentActuator.java` 1999 -> 413, `AgentState` fields 101 -> 44, `AgentEntity.java` 1076 -> 1075 (still OVER). Tracked in cq-aik.9, .26, .28.

## 10. Recommended follow-ups

1. cq-aik.29 (P1): fix the SafeNodes per-frame copy and re-scope the TimeWarp gate; re-measure.
2. cq-aik.30 (P2): add replacement tests or explicit deletion decisions for the five lost behaviours.
3. cq-aik.31 (P2): extend `bootstrap_gather_then_craft` by 10+ ticks and bound the Survey-hold idle window with a test.
4. Make the playtest harness drive the real System 1 pipeline (or document that it is a synthetic stub); otherwise it cannot serve as the Epic 19 success criterion.
5. Build cq-aik.17 (soak) and run it before closing Epic 19.
6. Relax or build-tag the `-race` Sub5ms assertion (flaky in both trees); record p50/p99 instead (numbers above).
7. Commit the Epic 19 change set (HEAD is still the pre-refactor commit) so a baseline/after comparison does not depend on a dirty tree.
