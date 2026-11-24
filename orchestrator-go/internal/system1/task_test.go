package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestTaskSubroutine_SuppressesDuplicateActWhileExecuting(t *testing.T) {
	state := NewAgentState()
	task := NewHarvestTreeTask(4, 64, 0, StageBootstrap, 100)
	state.StartTask(task)

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 3.5, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}

	// Tick 100: initial HARVEST_BREAK for (4, 64, 0)
	res1 := state.StepActiveTask("agent", frame)
	if res1.Intent == nil || res1.Intent.GetInteractBlock() == nil ||
		res1.Intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK ||
		res1.Intent.GetInteractBlock().Pos.Y != 64 {
		t.Fatalf("expected initial HARVEST_BREAK at Y=64, got %+v", res1)
	}
	if task.ActionsEmitted != 1 {
		t.Fatalf("expected ActionsEmitted=1, got %d", task.ActionsEmitted)
	}

	// Tick 101: periphery reports EXECUTING for batch 50
	frame.WorldTick = 101
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId: 50,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING,
	}
	res2 := state.StepActiveTask("agent", frame)
	if res2.Phase != TaskPhaseAct || res2.Intent != nil || !res2.Active {
		t.Fatalf("expected waiting ACT step with nil intent while EXECUTING, got %+v", res2)
	}

	// Tick 121 (past 20-tick ActionCooldown): still EXECUTING (no new receipt) -> MUST NOT re-emit HARVEST_BREAK
	frame.WorldTick = 121
	frame.Receipt = nil
	res3 := state.StepActiveTask("agent", frame)
	if res3.Phase != TaskPhaseAct || res3.Intent != nil || !res3.Active {
		t.Fatalf("expected duplicate HARVEST_BREAK suppressed past cooldown while EXECUTING, got %+v", res3)
	}
	if task.ActionsEmitted != 1 {
		t.Fatalf("expected ActionsEmitted to remain 1 while EXECUTING, got %d", task.ActionsEmitted)
	}

	// Tick 130: (4, 64, 0) breaks and COMPLETED receipt arrives -> immediately emit HARVEST_BREAK for (4, 65, 0)
	frame.WorldTick = 130
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId: 50,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
	}
	frame.InterestingBlocks = []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	res4 := state.StepActiveTask("agent", frame)
	if res4.Intent == nil || res4.Intent.GetInteractBlock() == nil ||
		res4.Intent.GetInteractBlock().Pos.Y != 65 {
		t.Fatalf("expected immediate HARVEST_BREAK on next log Y=65 after completion, got %+v", res4)
	}
	if task.ActionsEmitted != 2 {
		t.Fatalf("expected ActionsEmitted=2, got %d", task.ActionsEmitted)
	}
}

func TestTaskSubroutine_ApproachExecutingDoesNotBlockInitialAct(t *testing.T) {
	state := NewAgentState()
	task := NewHarvestTreeTask(10, 64, 0, StageBootstrap, 100)
	state.StartTask(task)

	// Tick 100: far away -> APPROACH NavigateIntent
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}
	res1 := state.StepActiveTask("agent", frame)
	if res1.Phase != TaskPhaseApproach || res1.Intent == nil || res1.Intent.GetNavigate() == nil {
		t.Fatalf("expected APPROACH NavigateIntent, got %+v", res1)
	}

	// Tick 110: arrives within reach while NavigateIntent receipt still says EXECUTING -> initial ACT must fire!
	frame.WorldTick = 110
	frame.Status.Position = &civquestv1.Vec3{X: 9.0, Y: 64, Z: 0.5}
	frame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId: 12,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING,
	}
	res2 := state.StepActiveTask("agent", frame)
	if res2.Phase != TaskPhaseAct || res2.Intent == nil || res2.Intent.GetInteractBlock() == nil {
		t.Fatalf("expected initial ACT HARVEST_BREAK upon arrival despite approach EXECUTING receipt, got %+v", res2)
	}
}

// TestHarvestTreeTask_BranchLogDetection verifies that contiguous trunk and branch logs within
// horizontal distance |dx| <= 2, |dz| <= 2 around (tx, tz) (e.g. at tx+1, tz and tx-1, tz+1)
// are detected and included in felling candidates (ADR-025).
func TestHarvestTreeTask_BranchLogDetection(t *testing.T) {
	tx, ty, tz := int32(10), int32(64), int32(10)
	task := NewHarvestTreeTask(tx, ty, tz, StageBootstrap, 100)
	state := NewAgentState()
	state.StartTask(task)

	// Frame includes trunk at (10, 64, 10), and branches at (11, 65, 10) (tx+1, tz)
	// and (9, 66, 11) (tx-1, tz+1). Also includes an oak log at (14, 64, 10) outside the cluster cylinder.
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 9, Y: 66, Z: 11}, BlockId: "minecraft:oak_log"},
			// Distant log outside |dx|<=2, |dz|<=2 that must NOT be included:
			{Pos: &civquestv1.BlockPosProto{X: 14, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
		},
	}

	totalInCluster, reachableLogs, _, _, _ := scanTreeClusterLogs(frame, tx, ty, tz, 4.5, "oak")
	if totalInCluster != 3 {
		t.Fatalf("expected 3 logs in tree cluster cylinder (|dx|<=2, |dz|<=2), got %d", totalInCluster)
	}
	if len(reachableLogs) != 3 {
		t.Fatalf("expected all 3 cluster logs reachable within 4.5m, got %d", len(reachableLogs))
	}

	foundBranch1, foundBranch2 := false, false
	for _, b := range reachableLogs {
		if b.Pos.X == tx+1 && b.Pos.Z == tz {
			foundBranch1 = true
		}
		if b.Pos.X == tx-1 && b.Pos.Z == tz+1 {
			foundBranch2 = true
		}
	}
	if !foundBranch1 {
		t.Fatalf("expected branch log at (tx+1, tz) = (11, 10) to be included in candidates")
	}
	if !foundBranch2 {
		t.Fatalf("expected branch log at (tx-1, tz+1) = (9, 11) to be included in candidates")
	}
}

// TestHarvestTreeTask_TallTreeTopDownOrdering verifies that for tall trees (height >= 5 blocks
// above ground / apex beyond ground reach), the top log is selected before the lower trunk (ADR-025).
func TestHarvestTreeTask_TallTreeTopDownOrdering(t *testing.T) {
	// Tall tree anchor at (10, 64, 10): height = 6 blocks (Y=64 to Y=69)
	tx, ty, tz := int32(10), int32(64), int32(10)
	task := NewHarvestTreeTask(tx, ty, tz, StageBootstrap, 100)
	state := NewAgentState()
	state.StartTask(task)

	// Agent is elevated/scaffolded at Y=67 near the tree crown, able to reach multiple logs including apex
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 67.0, Z: 10.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 66, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 67, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 68, Z: 10}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 69, Z: 10}, BlockId: "minecraft:oak_log"},
		},
	}

	total, reachable, _, isTall, apexLog := scanTreeClusterLogs(frame, tx, ty, tz, 4.5, "oak")
	if !isTall {
		t.Fatalf("expected tree of height 6 to be classified as tall tree, got isTall=false")
	}
	if apexLog == nil || apexLog.Pos.Y != 69 {
		t.Fatalf("expected apexLog at Y=69, got %+v", apexLog)
	}
	if total != 6 {
		t.Fatalf("expected total 6 logs, got %d", total)
	}
	if len(reachable) == 0 {
		t.Fatalf("expected reachable logs from agent at Y=67")
	}

	// In tall tree mode, top log (Y=69) must be selected before lower trunk logs (e.g. Y=64..66)
	res := state.StepActiveTask("agent", frame)
	if res.Intent == nil || res.Intent.GetInteractBlock() == nil {
		t.Fatalf("expected HARVEST_BREAK intent, got %+v", res)
	}
	interact := res.Intent.GetInteractBlock()
	if interact.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Fatalf("expected HARVEST_BREAK, got %v", interact.InteractionType)
	}
	if interact.Pos.Y != 69 {
		t.Fatalf("expected highest log at Y=69 to be selected first in tall tree top-down mode, got Y=%d", interact.Pos.Y)
	}
}

// TestHarvestTreeTask_SaplingReplanting verifies that when all logs are cleared, if a sapling
// is present in inventory, an outbound PLACE_BLOCK intent is emitted at the tree root base and
// registered in SpatialIndex under POICategoryCrop (ADR-025).
func TestHarvestTreeTask_SaplingReplanting(t *testing.T) {
	tx, ty, tz := int32(10), int32(64), int32(10)
	task := NewHarvestTreeTask(tx, ty, tz, StageBootstrap, 100)
	state := NewAgentState()
	state.StartTask(task)

	// Agent has felled all logs (zero logs remain in cluster), is standing near stump, and has oak_sapling in inventory
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 11.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_sapling", Count: 2},
				{ItemId: "minecraft:oak_log", Count: 5},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			// Root soil beneath stump at (10, 63, 10)
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10}, BlockId: "minecraft:dirt"},
		},
	}

	// Step 1: Trunk is cleared, sapling in inventory -> emits PLACE_BLOCK intent for sapling
	res := state.StepActiveTask("agent", frame)
	if res.Intent == nil || res.Intent.GetInteractBlock() == nil {
		t.Fatalf("expected PLACE_BLOCK intent for sapling replanting, got %+v", res)
	}
	interact := res.Intent.GetInteractBlock()
	if interact.InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK {
		t.Fatalf("expected PLACE_BLOCK interaction type, got %v", interact.InteractionType)
	}
	if interact.BlockOrItemId != "minecraft:oak_sapling" {
		t.Fatalf("expected BlockOrItemId 'minecraft:oak_sapling', got %q", interact.BlockOrItemId)
	}
	if interact.Pos.X != tx || interact.Pos.Y != ty || interact.Pos.Z != tz {
		t.Fatalf("expected sapling placed at base (%d,%d,%d), got (%d,%d,%d)", tx, ty, tz, interact.Pos.X, interact.Pos.Y, interact.Pos.Z)
	}

	// Verify newly planted sapling is registered into SpatialIndex with POICategoryCrop ("CROP")
	si := state.SpatialIndexLocked()
	if si == nil {
		t.Fatalf("expected non-nil SpatialIndex")
	}
	poi := si.FindNearestPOI(frame.Status.Position, POICategoryCrop, "minecraft:oak_sapling", 5.0, "")
	if poi == nil {
		t.Fatalf("expected newly planted sapling to be registered in SpatialIndex under POICategoryCrop")
	}
	if poi.Category != POICategoryCrop {
		t.Fatalf("expected POI category %q, got %q", POICategoryCrop, poi.Category)
	}

	// Step 2: Next step after planting -> task finishes cleanly with TaskPhaseComplete
	frame.WorldTick = 101
	res2 := state.StepActiveTask("agent", frame)
	if !res2.Done || res2.Phase != TaskPhaseComplete {
		t.Fatalf("expected task to complete on next tick after planting, got done=%v phase=%v", res2.Done, res2.Phase)
	}
}

func TestTaskSubroutine_IgnoresStalePreTaskFailedReceipt(t *testing.T) {
	state := NewAgentState()
	// Simulate a prior CraftItemIntent failure from before harvest_tree was created.
	staleReceipt := &civquestv1.IntentReceipt{
		BatchId:       42,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: "craft failed: missing planks",
	}
	state.IngestReceipt(staleReceipt)

	task := NewHarvestTreeTask(20, 64, 0, StageBootstrap, 100)
	state.StartTask(task)

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
		Receipt: staleReceipt,
	}

	// Tick 100: brand-new harvest_tree must NOT count the stale CraftItemIntent FAILED receipt.
	res1 := state.StepActiveTask("agent", frame)
	if !res1.Active || res1.Done {
		t.Fatalf("expected active harvest_tree task on first tick, got %+v", res1)
	}
	if res1.Intent == nil || res1.Intent.GetNavigate() == nil {
		t.Fatalf("expected APPROACH NavigateIntent toward tree, got %+v", res1)
	}
	if task.ConsecutiveFailures != 0 {
		t.Fatalf("expected ConsecutiveFailures=0 despite stale pre-task FAILED receipt, got %d", task.ConsecutiveFailures)
	}
	if task.StartReceiptBatchID != 42 || task.IssuedBatchCount != 1 {
		t.Fatalf("expected StartReceiptBatchID=42 and IssuedBatchCount=1, got start=%d issued=%d",
			task.StartReceiptBatchID, task.IssuedBatchCount)
	}

	// Tick 101: frame still echoes stale batch 42 FAILED -> must still be ignored.
	frame.WorldTick = 101
	res2 := state.StepActiveTask("agent", frame)
	if !res2.Active || res2.Done || task.ConsecutiveFailures != 0 {
		t.Fatalf("expected repeated stale receipt (batch 42) to remain ignored, got res=%+v failures=%d",
			res2, task.ConsecutiveFailures)
	}

	// Tick 102: a failure belonging to THIS task's batch (43 > 42) counts as 1 and recovers.
	frame.WorldTick = 102
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId:       43,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: "nav stuck",
	}
	res3 := state.StepActiveTask("agent", frame)
	if !res3.Active || res3.Done || task.ConsecutiveFailures != 1 {
		t.Fatalf("expected first task-owned failure to recover with ConsecutiveFailures=1, got res=%+v failures=%d",
			res3, task.ConsecutiveFailures)
	}

	// Tick 103: second task-owned failure (44) exceeds MaxReceiptFailures=2 and fails cleanly.
	frame.WorldTick = 103
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId:       44,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: "nav unreachable",
	}
	res4 := state.StepActiveTask("agent", frame)
	if !res4.Done || res4.Phase != TaskPhaseFailed {
		t.Fatalf("expected TaskPhaseFailed on second task-owned failure, got %+v", res4)
	}
}

func TestHarvestTreeTask_IgnoresZeroDisplacementCompletedAndBlacklistsOnFail(t *testing.T) {
	state := NewAgentState()
	task := NewHarvestTreeTask(20, 64, 0, StageBootstrap, 100)
	state.StartTask(task)

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		},
	}

	// Emit initial APPROACH NavigateIntent
	res1 := state.StepActiveTask("agent", frame)
	if res1.Intent == nil || res1.Intent.GetNavigate() == nil {
		t.Fatalf("expected APPROACH NavigateIntent, got %+v", res1)
	}

	// Zero-displacement fake COMPLETED receipt at tick 180 must NOT reset LastProgressTick or advance phase
	frame.WorldTick = 180
	frame.Receipt = &civquestv1.IntentReceipt{
		BatchId: 10,
		Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
	}
	res2 := state.StepActiveTask("agent", frame)
	if !res2.Active || res2.Done || task.LastProgressTick != 100 || task.Phase != TaskPhaseApproach {
		t.Fatalf("expected zero-displacement COMPLETED ignored (LastProgressTick=100, phase=APPROACH), got active=%v done=%v progressTick=%d phase=%s",
			res2.Active, res2.Done, task.LastProgressTick, task.Phase)
	}

	// At tick 250 (150-tick ProgressTimeout), task fails and OnFail blacklists target tree
	frame.WorldTick = 250
	frame.Receipt = nil
	res3 := state.StepActiveTask("agent", frame)
	if !res3.Done || res3.Phase != TaskPhaseFailed {
		t.Fatalf("expected TaskPhaseFailed at ProgressTimeout=150, got %+v", res3)
	}
	if !state.IsTargetUnreachable(20.5, 64.0, 0.5, 250) {
		t.Fatalf("expected OnFail to blacklist (20.5, 64.0, 0.5) as unreachable")
	}

	// New task on already-blacklisted tree immediately fails via IsUnachievable
	task2 := NewHarvestTreeTask(20, 64, 0, StageBootstrap, 255)
	state.StartTask(task2)
	frame.WorldTick = 255
	res4 := state.StepActiveTask("agent", frame)
	if !res4.Done || res4.Phase != TaskPhaseFailed {
		t.Fatalf("expected IsUnachievable to abort blacklisted tree immediately, got %+v", res4)
	}
}
