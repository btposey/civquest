package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestRoadPavingTracker_AccumulationAndPaving(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	pos := &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 20}

	// 1st Step on grass_block with active navigation pathing
	intent1 := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent1 != nil {
		t.Fatalf("expected no intent on 1st step, got %+v", intent1)
	}
	steps, paved, found := tracker.GetEntry(pos)
	if !found || steps != 1 || paved {
		t.Fatalf("expected steps=1, paved=false; got steps=%d, paved=%v", steps, paved)
	}

	// 2nd Step on grass_block
	intent2 := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent2 != nil {
		t.Fatalf("expected no intent on 2nd step, got %+v", intent2)
	}
	steps, paved, found = tracker.GetEntry(pos)
	if !found || steps != 2 || paved {
		t.Fatalf("expected steps=2, paved=false; got steps=%d, paved=%v", steps, paved)
	}

	// 3rd Step on grass_block -> Triggers PAVE_ROAD_PATH
	intent3 := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent3 == nil {
		t.Fatal("expected PAVE_ROAD_PATH intent on 3rd traversal")
	}
	interact := intent3.GetInteractBlock()
	if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
		t.Fatalf("expected InteractBlockIntent PAVE_ROAD_PATH, got %+v", interact)
	}
	if interact.Pos.X != pos.X || interact.Pos.Y != pos.Y || interact.Pos.Z != pos.Z {
		t.Fatalf("expected pos (%d,%d,%d), got (%d,%d,%d)",
			pos.X, pos.Y, pos.Z, interact.Pos.X, interact.Pos.Y, interact.Pos.Z)
	}
	steps, paved, found = tracker.GetEntry(pos)
	if !found || steps != 3 || !paved {
		t.Fatalf("expected steps=3, paved=true; got steps=%d, paved=%v", steps, paved)
	}

	// 4th Step: Already paved, must NOT re-emit intent
	intent4 := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent4 != nil {
		t.Fatalf("expected no duplicate intent on already paved block, got %+v", intent4)
	}
}

func TestRoadPavingTracker_AvoidsAlreadyPaved(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	pos := &civquestv1.BlockPosProto{X: 15, Y: 63, Z: 25}

	// Traversals over already paved dirt_path should never emit intent
	// and must NOT pollute the tracker cache with new entries
	for i := 0; i < 5; i++ {
		intent := tracker.RecordStep("agent1", pos, "minecraft:dirt_path")
		if intent != nil {
			t.Fatalf("expected nil intent for already paved dirt_path, got %+v on step %d", intent, i+1)
		}
	}

	// Cache must remain empty (no cache pollution)
	if tracker.Count() != 0 {
		t.Fatalf("expected 0 entries in tracker cache, got %d", tracker.Count())
	}
	_, _, found := tracker.GetEntry(pos)
	if found {
		t.Fatalf("expected dirt_path to NOT be added to tracker cache")
	}

	// If block was previously tracked as grass, stepping on dirt_path marks it paved without re-adding
	grassPos := &civquestv1.BlockPosProto{X: 16, Y: 63, Z: 25}
	_ = tracker.RecordStep("agent1", grassPos, "minecraft:grass_block")
	_ = tracker.RecordStep("agent1", grassPos, "minecraft:dirt_path")
	steps, paved, foundGrass := tracker.GetEntry(grassPos)
	if !foundGrass || !paved || steps != 1 {
		t.Fatalf("expected tracked block to be marked paved=true, steps=1; got found=%v, paved=%v, steps=%d",
			foundGrass, paved, steps)
	}
}

func TestRoadPavingTracker_NonPaveableBlocks(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	pos := &civquestv1.BlockPosProto{X: 20, Y: 63, Z: 30}

	// Stone should be ignored
	for i := 0; i < 5; i++ {
		intent := tracker.RecordStep("agent1", pos, "minecraft:stone")
		if intent != nil {
			t.Fatalf("expected nil intent for stone, got %+v", intent)
		}
	}
	_, _, found := tracker.GetEntry(pos)
	if found {
		t.Fatal("expected non-paveable block to not be tracked")
	}
}

func TestRoadPavingTracker_EvaluatePaving_MovementCheck(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// Agent standing at (10.5, 64.0, 10.5) -> below block is (10, 63, 10)
	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10},
				BlockId: "minecraft:grass_block",
			},
		},
	}

	// Step 1
	_ = tracker.EvaluatePaving("agent1", frame)
	steps, _, _ := tracker.GetEntry(&civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10})
	if steps != 1 {
		t.Fatalf("expected 1 step, got %d", steps)
	}

	// Same frame / same position -> should NOT increment step
	_ = tracker.EvaluatePaving("agent1", frame)
	steps, _, _ = tracker.GetEntry(&civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10})
	if steps != 1 {
		t.Fatalf("expected still 1 step when standing still, got %d", steps)
	}

	// Move to (11.5, 64.0, 10.5)
	frame2 := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 11.5, Y: 64.0, Z: 10.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 11, Y: 63, Z: 10},
				BlockId: "minecraft:grass_block",
			},
		},
	}
	_ = tracker.EvaluatePaving("agent1", frame2)

	// Move back to (10.5, 64.0, 10.5) -> step 2 on first block
	_ = tracker.EvaluatePaving("agent1", frame)
	steps, _, _ = tracker.GetEntry(&civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10})
	if steps != 2 {
		t.Fatalf("expected 2 steps after returning, got %d", steps)
	}

	// Move away and back -> step 3 on first block -> triggers intent!
	_ = tracker.EvaluatePaving("agent1", frame2)
	intent := tracker.EvaluatePaving("agent1", frame)
	if intent == nil {
		t.Fatal("expected pave intent on 3rd traversal")
	}
	if intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
		t.Fatalf("expected PAVE_ROAD_PATH intent, got %+v", intent)
	}
}

func TestRoadPavingTracker_LRUBoundedCacheEviction(t *testing.T) {
	// Small capacity of 3
	tracker := NewRoadPavingTracker(3, 3)

	posA := &civquestv1.BlockPosProto{X: 1, Y: 63, Z: 1}
	posB := &civquestv1.BlockPosProto{X: 2, Y: 63, Z: 2}
	posC := &civquestv1.BlockPosProto{X: 3, Y: 63, Z: 3}
	posD := &civquestv1.BlockPosProto{X: 4, Y: 63, Z: 4}

	tracker.RecordStep("agent1", posA, "minecraft:dirt")
	tracker.RecordStep("agent1", posB, "minecraft:dirt")
	tracker.RecordStep("agent1", posC, "minecraft:dirt")

	if tracker.Count() != 3 {
		t.Fatalf("expected 3 entries in tracker, got %d", tracker.Count())
	}

	// Access posA so posB becomes least recently used
	tracker.RecordStep("agent1", posA, "minecraft:dirt")

	// Adding posD must evict posB (since posA was refreshed, posC is newer than posB)
	tracker.RecordStep("agent1", posD, "minecraft:dirt")

	if tracker.Count() != 3 {
		t.Fatalf("expected capacity 3 maintained, got %d", tracker.Count())
	}

	// posB should have been evicted
	if _, _, found := tracker.GetEntry(posB); found {
		t.Fatal("expected posB to be evicted via LRU policy")
	}

	// posA, posC, posD should remain
	if _, _, found := tracker.GetEntry(posA); !found {
		t.Fatal("expected posA to remain in cache")
	}
	if _, _, found := tracker.GetEntry(posC); !found {
		t.Fatal("expected posC to remain in cache")
	}
	if _, _, found := tracker.GetEntry(posD); !found {
		t.Fatal("expected posD to remain in cache")
	}
}

func TestRoadPavingTracker_YOffsetAndDirtPathHeight(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// When standing on a 15/16 height dirt path or slab, pos.Y is 63.9375.
	// math.Floor(63.9375 - 0.2) = 63, correctly targeting ground block at Y=63.
	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10.5, Y: 63.9375, Z: 10.5},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10},
				BlockId: "minecraft:grass_block",
			},
		},
	}

	_ = tracker.EvaluatePaving("agent1", frame)
	steps, _, found := tracker.GetEntry(&civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10})
	if !found || steps != 1 {
		t.Fatalf("expected step recorded at Y=63 for pos.Y=63.9375, got found=%v steps=%d", found, steps)
	}
}

func TestRoadPavingTracker_MissingBlockDoesNotLockOut(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// Step onto block, but interesting_blocks has NOT yet delivered the block
	frame1 := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 20.5, Y: 64.0, Z: 20.5},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{}, // Empty frustum
	}

	intent1 := tracker.EvaluatePaving("agent1", frame1)
	if intent1 != nil {
		t.Fatalf("expected nil intent when block is not in frustum")
	}

	// Next tick at the exact same location, frustum now includes the block!
	// It must NOT be locked out by a premature lastStepped write!
	frame2 := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 20.5, Y: 64.0, Z: 20.5},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 20, Y: 63, Z: 20},
				BlockId: "minecraft:grass_block",
			},
		},
	}

	_ = tracker.EvaluatePaving("agent1", frame2)
	steps, _, found := tracker.GetEntry(&civquestv1.BlockPosProto{X: 20, Y: 63, Z: 20})
	if !found || steps != 1 {
		t.Fatalf("expected step to be recorded once block is delivered, got found=%v, steps=%d", found, steps)
	}
}

func TestRoadPavingTracker_HarmonizedBlockTypes(t *testing.T) {
	// grass_block and dirt are paveable
	if !isPaveable("minecraft:grass_block") || !isPaveable("dirt") {
		t.Fatal("expected grass_block and dirt to be paveable")
	}

	// podzol, coarse_dirt, rooted_dirt should NOT be paveable (matching IntentActuator.java:258)
	if isPaveable("minecraft:podzol") || isPaveable("minecraft:coarse_dirt") || isPaveable("minecraft:rooted_dirt") {
		t.Fatal("expected podzol, coarse_dirt, rooted_dirt to not be paveable")
	}
}

func TestRoadPavingTracker_DefaultThreshold(t *testing.T) {
	tracker := NewRoadPavingTracker(0, 0)
	if tracker.GetThreshold() != DefaultPavingThreshold {
		t.Fatalf("expected default threshold %d, got %d", DefaultPavingThreshold, tracker.GetThreshold())
	}
	if tracker.GetThreshold() != 15 {
		t.Fatalf("expected threshold to be 15, got %d", tracker.GetThreshold())
	}
	if tracker.GetExclusionRadius() != DefaultExclusionRadius {
		t.Fatalf("expected default exclusion radius %f, got %f", DefaultExclusionRadius, tracker.GetExclusionRadius())
	}
}

func TestRoadPavingTracker_RadialExclusionZone_Campfire(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	// Campfire / spawn origin at (10.0, 64.0, 10.0), default exclusion radius 4.0
	tracker.SetHomeOrigin(&civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0})

	// Block inside exclusion zone: (11, 63, 10) -> distance to center (10.5, 10.5) is ~1.0 block (<= 4.0)
	posInside := &civquestv1.BlockPosProto{X: 11, Y: 63, Z: 10}
	for i := 0; i < 20; i++ {
		intent := tracker.RecordStepWithNav("agent1", posInside, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
		if intent != nil {
			t.Fatalf("step %d inside campfire exclusion zone emitted pave intent: %+v", i+1, intent)
		}
	}
	steps, paved, _ := tracker.GetEntry(posInside)
	if steps != 20 || paved {
		t.Fatalf("expected 20 steps and paved=false inside exclusion zone, got steps=%d paved=%v", steps, paved)
	}

	// Block outside exclusion zone: (18, 63, 10) -> distance ~7.5 blocks (> 4.0)
	posOutside := &civquestv1.BlockPosProto{X: 18, Y: 63, Z: 10}
	_ = tracker.RecordStepWithNav("agent1", posOutside, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	_ = tracker.RecordStepWithNav("agent1", posOutside, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	intent := tracker.RecordStepWithNav("agent1", posOutside, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent == nil {
		t.Fatal("expected pave intent outside exclusion zone on 3rd traversal")
	}
	if intent.GetInteractBlock().InteractionType != civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
		t.Fatalf("expected PAVE_ROAD_PATH intent, got %+v", intent)
	}
}

func TestRoadPavingTracker_OutpostEntranceExemption(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	tracker.SetHomeOrigin(&civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0})

	// Block inside exclusion zone
	entrancePos := &civquestv1.BlockPosProto{X: 12, Y: 63, Z: 10}
	tracker.AddOutpostEntrance(entrancePos)

	if !tracker.IsOutpostEntrance(entrancePos) {
		t.Fatal("expected entrancePos to be registered as outpost entrance")
	}

	// Should pave despite being inside radius 4 because it is an explicit outpost entrance
	_ = tracker.RecordStepWithNav("agent1", entrancePos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	_ = tracker.RecordStepWithNav("agent1", entrancePos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	intent := tracker.RecordStepWithNav("agent1", entrancePos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intent == nil {
		t.Fatal("expected outpost entrance to pave despite being within campfire exclusion zone")
	}
}

func TestRoadPavingTracker_DirectionalCorridorGating_MillingDoesNotPave(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 5)

	// Agent mills omnidirectionally in a 2x2 square without directional progression:
	// (20, 20) -> (21, 20) -> (21, 21) -> (20, 21) -> (20, 20)...
	cells := []*civquestv1.BlockPosProto{
		{X: 20, Y: 63, Z: 20},
		{X: 21, Y: 63, Z: 20},
		{X: 21, Y: 63, Z: 21},
		{X: 20, Y: 63, Z: 21},
	}

	for cycle := 0; cycle < 10; cycle++ {
		for _, pos := range cells {
			intent := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
			if intent != nil {
				t.Fatalf("omnidirectional milling emitted pave intent at %+v: %+v", pos, intent)
			}
		}
	}

	// Verify all cells accumulated 10 steps (> threshold 5), but NONE were paved!
	for _, pos := range cells {
		steps, paved, found := tracker.GetEntry(pos)
		if !found || steps != 10 || paved {
			t.Fatalf("expected cell %+v steps=10, paved=false; got found=%v steps=%d paved=%v", pos, found, steps, paved)
		}
	}
}

func TestRoadPavingTracker_DirectionalCorridorGating_SequentialLinePaves(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// Agent walks along a linear corridor in consecutive steps:
	// (30, 20) -> (30, 21) -> (30, 22) -> (30, 23)
	// Walk back and forth 3 times along the line
	route := []*civquestv1.BlockPosProto{
		{X: 30, Y: 63, Z: 20},
		{X: 30, Y: 63, Z: 21},
		{X: 30, Y: 63, Z: 22},
		{X: 30, Y: 63, Z: 23},
	}

	var pavedIntents []*civquestv1.AgentIntent
	for pass := 0; pass < 3; pass++ {
		for _, pos := range route {
			intent := tracker.RecordStepWithNav("agent1", pos, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
			if intent != nil {
				pavedIntents = append(pavedIntents, intent)
			}
		}
	}

	// Sequential travel along the corridor should trigger paving
	if len(pavedIntents) == 0 {
		t.Fatal("expected sequential directional corridor travel to emit pave intents")
	}
}

func TestRoadPavingTracker_DirectionalCorridorGating_PavedNeighborPaves(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// Block A at (40, 63, 40) is already paved
	posA := &civquestv1.BlockPosProto{X: 40, Y: 63, Z: 40}
	_ = tracker.RecordStepWithNav("agent1", posA, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	_ = tracker.RecordStepWithNav("agent1", posA, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	intentA := tracker.RecordStepWithNav("agent1", posA, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_PATHING)
	if intentA == nil {
		t.Fatal("expected posA to be paved")
	}

	// Block B at (41, 63, 40) is adjacent to paved block A
	posB := &civquestv1.BlockPosProto{X: 41, Y: 63, Z: 40}
	// Even in IDLE navigation, because B is adjacent to paved A, extending the road is permitted!
	_ = tracker.RecordStepWithNav("agent1", posB, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	_ = tracker.RecordStepWithNav("agent1", posB, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	intentB := tracker.RecordStepWithNav("agent1", posB, "minecraft:grass_block", civquestv1.NavigationState_NAVIGATION_STATE_IDLE)
	if intentB == nil {
		t.Fatal("expected posB to be paved because it extends adjacent paved block A")
	}
}

func TestRoadPavingTracker_AutoDetectCampfireExclusion(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)

	// Frame includes a campfire at (50, 63, 50)
	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{
			Position:        &civquestv1.Vec3{X: 51.5, Y: 64.0, Z: 50.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 50, Y: 63, Z: 50},
				BlockId: "minecraft:campfire",
			},
			{
				Pos:     &civquestv1.BlockPosProto{X: 51, Y: 63, Z: 50},
				BlockId: "minecraft:grass_block",
			},
		},
	}

	// 1st tick -> detects campfire and sets homeOrigin
	_ = tracker.EvaluatePaving("agent1", frame)
	origin := tracker.GetHomeOrigin()
	if origin == nil || origin.X != 50.5 || origin.Z != 50.5 {
		t.Fatalf("expected homeOrigin auto-detected at (50.5, 63.0, 50.5), got %+v", origin)
	}

	// Stepping 10 times on (51, 63, 50) which is 1 block from campfire should NEVER pave
	for i := 0; i < 10; i++ {
		// Toggle position slightly to simulate moving onto the block
		frame.Status.Position = &civquestv1.Vec3{X: 51.2, Y: 64.0, Z: 50.5}
		intent := tracker.EvaluatePaving("agent1", frame)
		if intent != nil {
			t.Fatalf("step %d near auto-detected campfire emitted pave intent: %+v", i+1, intent)
		}
	}
}
