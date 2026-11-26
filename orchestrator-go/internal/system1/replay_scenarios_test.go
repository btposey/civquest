package system1

import (
	"fmt"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	rcQueued    = civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED
	rcExecuting = civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING
	rcCompleted = civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED
	rcPreempted = civquestv1.IntentReceipt_RECEIPT_STATUS_PREEMPTED_BY_REFLEX
	rcFailed    = civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED
	rcAborted   = civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED

	navIdle    = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	navPathing = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	navStuck   = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
	navArrived = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
)

func replayScenarios() []replayScenario {
	return []replayScenario{
		scenarioReflexPreemptsMining(),
		scenarioStaleReceiptAfterPreemption(),
		scenarioExpeditionFlank(),
		scenarioShelterResumeAfterFlee(),
		scenarioPeerYieldNoBlacklist(),
		scenarioAmbientUnstuckDoesNotSteal(),
		scenarioTaskSurvivesEmergency(),
		scenarioDirectivePreference(),
		scenarioForagingFallThroughOnEmpty(),
		scenarioBootstrapGatherCraft(),
		scenarioDeathClearsEverything(),
		scenarioTerrainStuckBlacklistsControl(),
		scenarioEmergencyPreemptsRecover(),
		scenarioDirectiveSurveyReportProceeds(),
		scenarioDirectiveSurveyWithoutReportIsBounded(),
		scenarioScoutingStuckDigsThrough(),
		scenarioSleepBedUnreachableBlacklists(),
		scenarioDrownHoldsUntilAirRestored(),
		scenarioBurningHoldsUntilExtinguished(),
		scenarioZombieEngagementHeldOnce(),
		scenarioSettlementBuildsRecommendedBlueprint(),
		scenarioNineLogTrunkHarvestedToReach(),
	}
}

var treeColumn = []*civquestv1.VisibleBlock{blk(3, 64, 0, "minecraft:oak_log"), blk(3, 65, 0, "minecraft:oak_log"), blk(3, 66, 0, "minecraft:oak_log")}

func shieldNotice(tick int64) []*civquestv1.ReflexNotice {
	return []*civquestv1.ReflexNotice{{ReflexType: civquestv1.ReflexNotice_REFLEX_TYPE_SHIELD_DEFENSE, Tick: tick, Details: "creeper"}}
}

// 1
func scenarioReflexPreemptsMining() replayScenario {
	threat := func(f *civquestv1.TelemetryFrame) {}
	_ = threat
	return replayScenario{
		Name: "reflex_preempts_mining_then_resumes", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "commit to trunk (3,0), break y=64"},
			{Frame: rf(105, 3000, withBlocks(treeColumn...)), Annotation: "mid-cooldown, no wander"},
			{Frame: rf(126, 3000, withInv(item(0, "minecraft:oak_log", 1)), withBlocks(blk(3, 65, 0, "minecraft:oak_log"), blk(3, 66, 0, "minecraft:oak_log"), blk(1, 64, 1, "minecraft:birch_log"))), Annotation: "stay on locked trunk despite closer tree"},
			{Frame: rf(135, 3000, withHP(6), withEntities(creeperAt(901, 2.5, 0.5, 2.5)), withBlocks(blk(3, 65, 0, "minecraft:oak_log"))), Reflexes: shieldNotice(135), Annotation: "creeper + low HP: emergency preempts"},
			{Frame: rf(335, 3000, withHP(6), withEntities(creeperAt(901, 2.5, 0.5, 2.5)), withBlocks(blk(3, 65, 0, "minecraft:oak_log"))), Annotation: "200 ticks in emergency, no false timeout"},
			{Frame: rf(350, 3000, withInv(item(0, "minecraft:oak_log", 1)), withBlocks(blk(3, 65, 0, "minecraft:oak_log"), blk(3, 66, 0, "minecraft:oak_log"))), Annotation: "threat cleared: resumes harvest on (3,65,0)"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if tr[0].Routine != RoutineHarvestTree || !hasIntentPrefix(tr[0], "Interact(HARVEST_BREAK 3,64,0") {
				t.Fatalf("expected harvest break on 3,64,0 at start: %s", tr[0])
			}
			if tr[3].Stage != StageEmergency || tr[4].Stage != StageEmergency {
				t.Fatalf("expected emergency on preempt/hold: %s | %s", tr[3], tr[4])
			}
			if tr[5].Stage != StageBootstrap || tr[5].Routine != RoutineHarvestTree || !hasIntentPrefix(tr[5], "Interact(HARVEST_BREAK 3,65,0") {
				t.Fatalf("expected resume of harvest on 3,65,0: %s", tr[5])
			}
		},
	}
}

// 2
func scenarioStaleReceiptAfterPreemption() replayScenario {
	fails := func(tk *TaskSubroutine) string {
		if tk != nil {
			return fmt.Sprintf("taskFailures=%d", tk.ConsecutiveFailures)
		}
		return "noTask"
	}
	return replayScenario{
		Name: "stale_receipt_after_preemption_ignored", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Receipt: receipt(1, rcCompleted, ""), ProbeTask: fails, Annotation: "pre-task receipt batch 1 carried in"},
			{Frame: rf(110, 3000, withBlocks(treeColumn...)), Receipt: receipt(2, rcExecuting, ""), ProbeTask: fails, Annotation: "task issued batch 2"},
			{Frame: rf(135, 3000, withHP(6), withEntities(creeperAt(901, 2.5, 0.5, 2.5)), withBlocks(treeColumn...)), Receipt: receipt(3, rcPreempted, "reflex"), Reflexes: shieldNotice(135), ProbeTask: fails, Annotation: "reflex preempts; batch 3 PREEMPTED_BY_REFLEX"},
			{Frame: rf(350, 3000, withBlocks(treeColumn...)), Receipt: receipt(3, rcPreempted, "reflex"), ProbeTask: fails, Annotation: "cleared; redelivered PREEMPTED batch 3 does not abort the task"},
			{Frame: rf(360, 3000, withBlocks(treeColumn...)), Receipt: receipt(2, rcFailed, "stale failure from before preemption"), ProbeTask: fails, Annotation: "stale FAILED batch 2 (older than preempt batch 3): dropped by the seam's monotonic batch guard, not a task failure (cq-2sr.5 fix)"},
			{Frame: rf(370, 3000, withBlocks(treeColumn...)), Receipt: receipt(2, rcFailed, "stale failure from before preemption"), ProbeTask: fails, Annotation: "same stale FAILED replayed: still dropped"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i := 3; i < len(tr); i++ {
				if tr[i].Routine != RoutineHarvestTree {
					t.Fatalf("stale receipt aborted the task at step %d: %s", i, tr[i])
				}
			}
			if tr[3].Probe != "taskFailures=0" {
				t.Fatalf("redelivered PREEMPTED receipt must not count as failure: %s", tr[3])
			}
			for i := 4; i < len(tr); i++ {
				if tr[i].Probe != "taskFailures=0" {
					t.Fatalf("stale FAILED receipt counted as a task failure at step %d: %s", i, tr[i])
				}
			}
		},
	}
}

// 3
func scenarioExpeditionFlank() replayScenario {
	dir := &replayDirective{Action: DirectiveActionScoutFrontier, Target: "south", Goal: "SCOUT_FRONTIER: scout", IssuedTick: 100, ExpiresAt: 6100}
	ravine := uniformSurvey(0, 20, 250)
	for i := 0; i < 36; i++ {
		if i <= 13 || i >= 32 {
			ravine.Sectors[i].Occluded = true
			ravine.Sectors[i].FeatureType = "RIDGE_OBSTRUCTION"
			ravine.Sectors[i].HitDistance = 6.0
			ravine.Sectors[i].OpenHorizon = false
		}
	}
	phase := func(st *AgentState) string {
		return fmt.Sprintf("stack=%s", strings.Join(st.activityKinds, ">"))
	}
	return replayScenario{
		Name: "expedition_flank", Stage: StageSettlement, Profession: "EXPLORER", Clan: 2,
		Steps: []replayStep{
			{Frame: rf(110, 4000, withInv(survivalKit()...)), Directive: dir, Probe: phase, Annotation: "scout directive: initial 360 survey"},
			{Frame: rf(125, 4000, withInv(survivalKit()...), withSurvey(uniformSurvey(0, 0, 120))), Probe: phase, Annotation: "survey report arrives: traverse"},
			{Frame: rf(180, 4000, withInv(survivalKit()...), withPos(0, 64, 20)), Receipt: terrainStuck(2), Probe: phase, Annotation: "stuck receipt mid-traverse (ravine): Navigate pushes a one-attempt Recover (sidestep)"},
			{Frame: rf(205, 4000, withInv(survivalKit()...), withPos(0, 64, 20)), Probe: phase, Annotation: "Recover attempt times out: Blocked pops to Navigate, which pops navBlocked: rim survey"},
			{Frame: rf(255, 4000, withInv(survivalKit()...), withPos(0, 64, 20), withSurvey(ravine)), Probe: phase, Annotation: "rim survey done: detour bearing"},
			{Frame: rf(280, 4000, withInv(survivalKit()...), withPos(1, 64, 20.5)), Receipt: terrainStuck(3), Probe: phase, Annotation: "stuck receipt again at rim: Recover sidestep"},
			{Frame: rf(305, 4000, withInv(survivalKit()...), withPos(1, 64, 20.5)), Probe: phase, Annotation: "Recover gives up: perpendicular flank"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			const base = "stack=expedition>"
			want := []string{base + "survey", base + "navigate", base + "navigate>recover", base + "survey", base + "navigate", base + "navigate>recover", base + "navigate"}
			for i, w := range want {
				if tr[i].Probe != w {
					t.Fatalf("step %d expedition probe = %q, want %q", i, tr[i].Probe, w)
				}
			}
			if !hasIntentPrefix(tr[0], "Survey(") || !hasIntentPrefix(tr[1], "Navigate(") || !hasIntentPrefix(tr[2], "Navigate(") || !hasIntentPrefix(tr[3], "Survey(") || !hasIntentPrefix(tr[6], "Navigate(") {
				t.Fatalf("unexpected expedition intent kinds:\n%s", renderTrace(tr))
			}
		},
	}
}

// 4
func scenarioShelterResumeAfterFlee() replayScenario {
	inv := withInv(item(0, "minecraft:oak_planks", 16), item(1, "minecraft:torch", 4))
	return replayScenario{
		Name: "shelter_resume_after_flee", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 14000, inv), Annotation: "night with planks: shelter build starts"},
			{Frame: rf(110, 14000, inv), Annotation: "shelter continues"},
			{Frame: rf(120, 14000, inv, withHP(6), withEntities(creeperAt(7, 3, 0.5, 3))), Annotation: "hostile: flee/emergency"},
			{Frame: rf(130, 14000, inv, withHP(6), withEntities(creeperAt(7, 3, 0.5, 3))), Annotation: "emergency holds"},
			{Frame: rf(160, 14000, inv), Annotation: "threat gone: shelter resumes"},
			{Frame: rf(170, 14000, inv), Annotation: "shelter continues"},
		},
	}
}

// 5
func scenarioPeerYieldNoBlacklist() replayScenario {
	peer := &civquestv1.VisibleEntity{EntityId: 55, EntityType: "civquest:agent", DisplayName: "bran", Distance: 2, Position: &civquestv1.Vec3{X: 3.5, Y: 64, Z: 0.5}}
	cur := int64(0)
	tick := func() int64 { return cur }
	unreach := probeUnreachable(3.5, 64, 0.5, tick)
	withTick := func(t int64, p func(*AgentState) string) func(*AgentState) string {
		return func(st *AgentState) string { cur = t; return p(st) }
	}
	return replayScenario{
		Name: "peer_yield_no_terrain_blacklist", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Probe: withTick(100, unreach), Annotation: "commit to trunk"},
			{Frame: rf(110, 3000, withBlocks(treeColumn...), withEntities(peer)), Receipt: peerStuck(2), Probe: withTick(110, unreach), Annotation: "peer occupies trunk: stuck receipt with blocked_by_entity -> Recover(congestion) yields"},
			{Frame: rf(135, 3000, withBlocks(treeColumn...), withEntities(peer)), Probe: withTick(135, unreach), Annotation: "yield elapsed (20 ticks): Recover pops Recovered, parent re-emits"},
			{Frame: rf(140, 3000, withBlocks(treeColumn...), withEntities(peer)), Receipt: peerStuck(3), Probe: withTick(140, unreach), Annotation: "peer still there: a fresh Recover(congestion) yields again, still no blacklist"},
			{Frame: rf(150, 3000, withBlocks(treeColumn...)), Probe: withTick(150, unreach), Annotation: "peer leaves; the yield continues, no terrain blacklist at any step"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i, r := range tr {
				if r.Probe != "unreachable(3.5,64,0.5)=false" {
					t.Fatalf("step %d: peer yielding blacklisted terrain: %s", i, r)
				}
			}
		},
	}
}

// 6
func scenarioAmbientUnstuckDoesNotSteal() replayScenario {
	return replayScenario{
		Name: "ambient_unstuck_does_not_steal_activity", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "commit to trunk"},
			{Frame: rf(130, 3000, withBlocks(treeColumn...), withNav(navStuck)), Annotation: "telemetry stuck, no receipt: owning activity keeps control, no recovery intent"},
			{Frame: rf(160, 3000, withBlocks(treeColumn...), withNav(navStuck)), Annotation: "still telemetry stuck"},
			{Frame: rf(190, 3000, withBlocks(treeColumn...), withNav(navStuck), func(f *civquestv1.TelemetryFrame) { f.Status.NavigationEnclosed = true }), Annotation: "telemetry enclosed, no receipt: still no ambient unstuck"},
			{Frame: rf(220, 3000, withBlocks(treeColumn...)), Receipt: enclosedStuck(2), Annotation: "enclosed stuck RECEIPT for the owning frame: Recover(Enclosed) acts"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i := 1; i < 4; i++ {
				if tr[i].Routine != RoutineHarvestTree || tr[i].Top == activityKindRecover {
					t.Fatalf("ambient telemetry stuck stole the activity at step %d: %s", i, tr[i])
				}
			}
			if tr[4].Top != activityKindRecover || len(tr[4].Intents) == 0 {
				t.Fatalf("expected Recover(enclosed) intent on the receipt: %s", tr[4])
			}
		},
	}
}

// 7
func scenarioTaskSurvivesEmergency() replayScenario {
	return replayScenario{
		Name: "task_subroutine_survives_emergency", Stage: StageSettlement, Profession: "FARMER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withInv(survivalKit()...), withBlocks(blk(2, 64, 0, "minecraft:oak_log"), blk(2, 65, 0, "minecraft:oak_log"))),
				StartTask: NewHarvestTreeTask(2, 64, 0, StageSettlement, 90), Annotation: "committed harvest_tree in Settlement"},
			{Frame: rf(110, 3000, withInv(survivalKit()...), withHP(6), withEntities(creeperAt(9, 2.5, 0.5, 2.5)), withBlocks(blk(2, 65, 0, "minecraft:oak_log"))), Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "emergency preempts the task"},
			{Frame: rf(120, 3000, withInv(survivalKit()...), withHP(6), withEntities(creeperAt(9, 2.5, 0.5, 2.5)), withBlocks(blk(2, 65, 0, "minecraft:oak_log"))), Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "emergency holds"},
			{Frame: rf(140, 3000, withInv(survivalKit()...), withBlocks(blk(2, 65, 0, "minecraft:oak_log"))), Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "cleared: task resumes"},
			{Frame: rf(150, 3000, withInv(survivalKit()...), withBlocks(blk(2, 65, 0, "minecraft:oak_log"))), Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "task continues"},
		},
	}
}

// 8
func scenarioDirectivePreference() replayScenario {
	return replayScenario{
		Name: "directive_preference_over_stale_task", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 4000, withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))),
				StartTask: NewHarvestTreeTask(-46, 86, -8, StageBootstrap, 100), Annotation: "harvest_tree committed, no directive"},
			{Frame: rf(204, 4000, withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))),
				Directive: &replayDirective{Action: DirectiveActionScoutFrontier, Target: "eastern forest", Goal: "SCOUT_FRONTIER: pivot", IssuedTick: 200, ExpiresAt: 6200},
				Probe:     func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "fresh scout directive preempts older task"},
			{Frame: rf(210, 4000, withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))),
				Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "directive continues"},
			// cq-aik.31: the survey holds silently while Java sweeps (36 sectors x 2 ticks); it is
			// bounded by its 120-tick lease and ends in a report (next scenario) or a re-survey.
			{Frame: rf(250, 4000, withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))), Annotation: "sweep in progress: holding, lease not expired"},
			{Frame: rf(325, 4000, withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))), Annotation: "lease (120) expired without a report: bounded re-survey, not idle"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[1], "Survey(") {
				t.Fatalf("expected directive survey intent: %s", tr[1])
			}
			if len(tr[3].Intents) != 0 || tr[3].Top != activityKindSurvey {
				t.Fatalf("survey must hold inside its lease: %s", tr[3])
			}
			if !hasIntentPrefix(tr[4], "Survey(") {
				t.Fatalf("an expired survey lease must re-survey (bounded wait): %s", tr[4])
			}
		},
	}
}

// 9
func scenarioForagingFallThroughOnEmpty() replayScenario {
	return replayScenario{
		Name: "foraging_fallthrough_on_empty", Stage: StageSettlement, Profession: "FARMER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withFood(6), withInv(survivalKit()...), withBlocks(blk(2, 64, 0, "minecraft:sweet_berry_bush"))), Annotation: "hungry with berries: foraging"},
			{Frame: rf(110, 3000, withFood(6), withInv(survivalKit()...)), Annotation: "hungry, nothing to forage: what runs instead"},
			{Frame: rf(120, 3000, withFood(6), withInv(survivalKit()...)), Annotation: "still empty"},
			{Frame: rf(130, 3000, withFood(20), withInv(survivalKit()...)), Annotation: "fed again"},
		},
	}
}

// 10
func scenarioBootstrapGatherCraft() replayScenario {
	graph := func(st *AgentState) { st.SetRecipeGraph(newTestRecipeGraph()) }
	return replayScenario{
		Name: "bootstrap_gather_then_craft", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Setup: graph, Annotation: "empty inventory: gather logs"},
			{Frame: rf(126, 3000, withInv(item(0, "minecraft:oak_log", 2)), withBlocks(blk(3, 66, 0, "minecraft:oak_log"))), Annotation: "some logs, keep gathering"},
			{Frame: rf(150, 3000, withInv(item(0, "minecraft:oak_log", 6))), Annotation: "enough logs: craft step"},
			{Frame: rf(175, 3000, withInv(item(0, "minecraft:oak_planks", 12))), Annotation: "planks: next craft step"},
			{Frame: rf(200, 3000, withInv(item(0, "minecraft:oak_planks", 8), item(1, "minecraft:stick", 4))), Annotation: "planks+sticks: tool craft or table"},
			{Frame: rf(225, 3000, withInv(item(0, "minecraft:wooden_axe", 1), item(1, "minecraft:oak_planks", 4), item(2, "minecraft:stick", 2))), Annotation: "axe in hand: sword step"},
			{Frame: rf(250, 3000, withInv(survivalKit()...)), Annotation: "kit complete: exploration's first leg is issued at once (baseline Navigate(-46.1,-11.1))"},
			{Frame: rf(260, 3000, withInv(survivalKit()...)), Annotation: "leg in flight: no duplicate"},
			{Frame: rf(295, 3000, withInv(survivalKit()...)), Annotation: "still idle after the 40-tick re-issue window: the leg is re-issued"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[0], "Interact(HARVEST_BREAK") {
				t.Fatalf("expected gather first: %s", tr[0])
			}
			if !hasIntentPrefix(tr[6], "Navigate(") || tr[6].Top != activityKindExploration {
				t.Fatalf("after the kit completes exploration must navigate on its first tick: %s", tr[6])
			}
			if len(tr[7].Intents) != 0 || !hasIntentPrefix(tr[8], "Navigate(") {
				t.Fatalf("expected a hold then a re-issue, got %s / %s", tr[7], tr[8])
			}
			crafted := false
			for _, r := range tr[2:] {
				if hasIntentPrefix(r, "Craft(") {
					crafted = true
				}
			}
			if !crafted {
				t.Fatalf("expected at least one Craft intent after gathering:\n%s", renderTrace(tr))
			}
		},
	}
}

// 11
func scenarioDeathClearsEverything() replayScenario {
	return replayScenario{
		Name: "death_clears_activities", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "commit to trunk"},
			{Frame: rf(110, 3000, withHP(0), withBlocks(treeColumn...)), Probe: func(st *AgentState) string { return fmt.Sprintf("hasTask=%t", st.HasActiveTask()) }, Annotation: "death: everything cleared"},
		},
	}
}

// 12: control for #5 -- without a peer, repeated FAILED receipts on the same trunk DO blacklist it,
// proving the peer-yield probe discriminates.
func scenarioTerrainStuckBlacklistsControl() replayScenario {
	cur := int64(0)
	probe := func(tick int64) func(*AgentState) string {
		return func(st *AgentState) string {
			cur = tick
			return probeUnreachable(3.5, 64, 0.5, func() int64 { return cur })(st)
		}
	}
	return replayScenario{
		Name: "terrain_stuck_blacklists_target_control", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Probe: probe(100), Annotation: "commit to trunk"},
			{Frame: rf(110, 3000, withBlocks(treeColumn...)), Receipt: terrainStuck(2), Probe: probe(110), Annotation: "terrain stuck receipt 1: Recover(terrain) sidesteps"},
			{Frame: rf(215, 3000, withBlocks(treeColumn...)), Probe: probe(215), Annotation: "lease expired: give-up 1 (one failure, not yet blacklisted)"},
			{Frame: rf(225, 3000, withBlocks(treeColumn...)), Receipt: terrainStuck(3), Probe: probe(225), Annotation: "terrain stuck receipt 2: Recover(terrain) again"},
			{Frame: rf(330, 3000, withBlocks(treeColumn...)), Probe: probe(330), Annotation: "lease expired: give-up 2 blacklists the target"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if tr[2].Probe != "unreachable(3.5,64,0.5)=false" {
				t.Fatalf("a single terrain give-up must not blacklist: %s", tr[2])
			}
			if tr[4].Probe != "unreachable(3.5,64,0.5)=true" {
				t.Fatalf("two terrain give-ups must blacklist the target: %s", tr[4])
			}
		},
	}
}

// 13: ADR-037 test plan: an emergency preempts a Recover frame (Band inherited from the
// parent, so Emergency outranks it), and the suspended Recover resumes on top once it clears.
func scenarioEmergencyPreemptsRecover() replayScenario {
	return replayScenario{
		Name: "emergency_preempts_recover_then_resumes", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "commit to trunk"},
			{Frame: rf(110, 3000, withBlocks(treeColumn...)), Receipt: terrainStuck(2), Annotation: "terrain stuck receipt: Recover(terrain) sidesteps"},
			{Frame: rf(120, 3000, withHP(6), withEntities(creeperAt(901, 2.5, 0.5, 2.5)), withBlocks(treeColumn...)), Reflexes: shieldNotice(120), Annotation: "creeper: emergency preempts Recover"},
			{Frame: rf(130, 3000, withBlocks(treeColumn...)), Annotation: "threat gone: Recover is exposed again (within its lease)"},
			{Frame: rf(215, 3000, withBlocks(treeColumn...)), Annotation: "lease expired: Recover gives up Blocked to the task"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if tr[1].Top != activityKindRecover {
				t.Fatalf("expected Recover on top after the stuck receipt: %s", tr[1])
			}
			if tr[2].Stage != StageEmergency {
				t.Fatalf("expected emergency to preempt Recover: %s", tr[2])
			}
			if tr[4].Top == activityKindRecover {
				t.Fatalf("an expired Recover must pop: %s", tr[4])
			}
		},
	}
}

// 14 (cq-aik.31): a survey that reports ends the wait at once and the expedition navigates.
func scenarioDirectiveSurveyReportProceeds() replayScenario {
	pos, tree := withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))
	return replayScenario{
		Name: "directive_survey_report_proceeds", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(204, 4000, pos, tree),
				Directive:  &replayDirective{Action: DirectiveActionScoutFrontier, Target: "eastern forest", Goal: "SCOUT_FRONTIER", IssuedTick: 200, ExpiresAt: 6200},
				Annotation: "scout directive: survey starts"},
			{Frame: rf(250, 4000, pos, tree), Annotation: "sweep in progress"},
			{Frame: rf(280, 4000, pos, tree, withSurvey(uniformSurvey(-40.5, -7.5, 279))), Annotation: "report arrives: the wait ends and the expedition navigates"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[0], "Survey(") || len(tr[1].Intents) != 0 {
				t.Fatalf("expected a survey then a silent hold: %s / %s", tr[0], tr[1])
			}
			if !hasIntentPrefix(tr[2], "Navigate(") {
				t.Fatalf("the report must end the wait with a Navigate: %s", tr[2])
			}
		},
	}
}

// 15 (cq-aik.31): a survey whose report never arrives is bounded: two lease expiries, then the
// expedition gives up and backs off, and the agent resumes productive work (harvest).
func scenarioDirectiveSurveyWithoutReportIsBounded() replayScenario {
	pos, tree := withPos(-40.5, 86, -7.5), withBlocks(blk(-46, 86, -8, "minecraft:oak_log"))
	return replayScenario{
		Name: "directive_survey_without_report_is_bounded", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(204, 4000, pos, tree),
				Directive:  &replayDirective{Action: DirectiveActionScoutFrontier, Target: "eastern forest", Goal: "SCOUT_FRONTIER", IssuedTick: 200, ExpiresAt: 6200},
				Annotation: "scout directive: survey 1"},
			{Frame: rf(325, 4000, pos, tree), Annotation: "no report, lease expired: survey 2"},
			{Frame: rf(446, 4000, pos, tree), Annotation: "no report again: expedition gives up (backoff), nothing held"},
			{Frame: rf(475, 4000, pos, tree), Annotation: "agent is working again (harvest) while the directive stays active"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[1], "Survey(") {
				t.Fatalf("second survey expected: %s", tr[1])
			}
			if tr[2].Top == activityKindSurvey || tr[2].Top == activityKindExpedition {
				t.Fatalf("expedition must give up after repeated report-less surveys: %s", tr[2])
			}
			if !hasIntentPrefix(tr[3], "Navigate(") && !hasIntentPrefix(tr[3], "Interact(") {
				t.Fatalf("agent idle after the survey gave up: %s", tr[3])
			}
		},
	}
}

// 16 (cq-aik.30 c): a stuck explorer breaks the adjacent obstruction (baseline
// TestStuckAgentEmitsHarvestBreakOnAdjacentObstruction).
func scenarioScoutingStuckDigsThrough() replayScenario {
	wall := withBlocks(blk(1, 64, 0, "minecraft:dirt"), blk(1, 65, 0, "minecraft:dirt"))
	kit := withInv(survivalKit()...)
	return replayScenario{
		Name: "scouting_stuck_digs_through", Stage: StageSettlement, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(250, 3000, kit), Setup: func(st *AgentState) { st.homeOrigin = &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5} },
				Annotation: "exploration entered (agent already past its spawn warm-up)"},
			{Frame: rf(310, 3000, kit), Annotation: "exploration leg in flight"},
			{Frame: rf(320, 3000, kit, wall), Receipt: terrainStuck(2), Annotation: "stuck against a dirt wall: Recover(terrain, dig) breaks the head-height block"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if tr[2].Top != activityKindRecover || !hasIntentPrefix(tr[2], "Interact(HARVEST_BREAK 1,65,0") {
				t.Fatalf("expected Recover to break the obstructing block (1,65,0): %s", tr[2])
			}
		},
	}
}

// 17 (cq-aik.30 d): two terrain give-ups on the assigned bed's access target blacklist it and
// clear the assignment (baseline TestEvaluateSleep_BlacklistsUnreachableAssignedBedAfterTwoStucks).
func scenarioSleepBedUnreachableBlacklists() replayScenario {
	seed := func(st *AgentState) {
		st.SpatialIndex().UpsertPOI(db.AgentPOI{POIID: FormatPOIID(POICategoryHabitation, -46, 79, 17), Category: POICategoryHabitation,
			BlockID: "minecraft:red_bed", X: -45.5, Y: 79, Z: 17.5, AccessX: -45.5, AccessY: 79, AccessZ: 16.5,
			ElevationLayer: ElevationDugout, Reliability: 1, IsActive: true})
	}
	at := func(tick int64) *civquestv1.TelemetryFrame { return rf(tick, 19000+tick, withPos(-30, 86, 0)) }
	probe := func(tick int64) func(*AgentState) string {
		return func(st *AgentState) string {
			return fmt.Sprintf("bedAssigned=%t unreachable=%t", st.GetAssignedBed() != nil, st.IsTargetUnreachable(-45.5, 79, 16.5, tick))
		}
	}
	return replayScenario{
		Name: "sleep_bed_unreachable_blacklists", Stage: StageSettlement, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: at(20), Setup: seed, Probe: probe(20), Annotation: "night: navigate to the assigned bed"},
			{Frame: at(30), Receipt: terrainStuck(2), Unframed: true, Probe: probe(30), Annotation: "bed navigation stuck (terrain): Recover"},
			{Frame: at(135), Probe: probe(135), Annotation: "lease expired: give-up 1, bed still retried"},
			{Frame: at(150), Receipt: terrainStuck(3), Unframed: true, Probe: probe(150), Annotation: "stuck again"},
			{Frame: at(260), Probe: probe(260), Annotation: "give-up 2: bed blacklisted"},
			{Frame: at(270), Probe: probe(270), Annotation: "assignment cleared, no more navigation to the bed"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if tr[2].Probe != "bedAssigned=true unreachable=false" {
				t.Fatalf("one give-up must not blacklist the bed: %s", tr[2])
			}
			if tr[4].Probe != "bedAssigned=true unreachable=true" || tr[5].Probe != "bedAssigned=false unreachable=true" {
				t.Fatalf("two terrain give-ups must blacklist the bed and clear the assignment: %s / %s", tr[4], tr[5])
			}
		},
	}
}

// cq-gpa.9: a 9-log trunk worked from directly beside it is felled bottom-up up
// to the eye's real reach (y+6); the top two logs are left (ladders: cq-3a0).
func scenarioNineLogTrunkHarvestedToReach() replayScenario {
	trunk := func(from, to int32) []*civquestv1.VisibleBlock {
		var out []*civquestv1.VisibleBlock
		for y := from; y <= to; y++ {
			out = append(out, blk(0, y, 0, "minecraft:oak_log"))
		}
		return out
	}
	at := withPos(1.5, 64, 0.5)
	return replayScenario{
		Name: "nine_log_trunk_harvested_to_eye_reach", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, at, withBlocks(trunk(64, 72)...)), Annotation: "all nine present: break bottom log y=64"},
			{Frame: rf(125, 3000, at, withBlocks(trunk(65, 72)...)), Annotation: "bottom-up: y=65"},
			{Frame: rf(150, 3000, at, withBlocks(trunk(68, 72)...)), Annotation: "y=68 (old feet-centre reach stopped at y+4=68)"},
			{Frame: rf(175, 3000, at, withBlocks(trunk(69, 72)...)), Annotation: "y=69 now reachable (eye reach)"},
			{Frame: rf(200, 3000, at, withBlocks(trunk(70, 72)...)), Annotation: "y=70 = y+6, last reachable log"},
			{Frame: rf(225, 3000, at, withBlocks(trunk(71, 72)...)), Annotation: "only y=71,72 left: out of reach, left standing"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i, y := range []int{64, 65, 68, 69, 70} {
				if !hasIntentPrefix(tr[i], fmt.Sprintf("Interact(HARVEST_BREAK 0,%d,0", y)) {
					t.Fatalf("step %d: expected HARVEST_BREAK at y=%d: %s", i, y, tr[i])
				}
			}
			if hasIntentPrefix(tr[5], "Interact(HARVEST_BREAK") {
				t.Fatalf("y=71/72 are out of reach and must not be targeted: %s", tr[5])
			}
		},
	}
}
