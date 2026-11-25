package system1

import (
	"fmt"
	"strings"
	"testing"

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
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[1], "Survey(") {
				t.Fatalf("expected directive survey intent: %s", tr[1])
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
			{Frame: rf(250, 3000, withInv(survivalKit()...)), Annotation: "kit complete"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			if !hasIntentPrefix(tr[0], "Interact(HARVEST_BREAK") {
				t.Fatalf("expected gather first: %s", tr[0])
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
