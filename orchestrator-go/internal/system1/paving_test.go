package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestRoadPavingTracker_AccumulationAndPaving(t *testing.T) {
	tracker := NewRoadPavingTracker(100, 3)
	pos := &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 20}

	// 1st Step on grass_block
	intent1 := tracker.RecordStep("agent1", pos, "minecraft:grass_block")
	if intent1 != nil {
		t.Fatalf("expected no intent on 1st step, got %+v", intent1)
	}
	steps, paved, found := tracker.GetEntry(pos)
	if !found || steps != 1 || paved {
		t.Fatalf("expected steps=1, paved=false; got steps=%d, paved=%v", steps, paved)
	}

	// 2nd Step on grass_block
	intent2 := tracker.RecordStep("agent1", pos, "minecraft:grass_block")
	if intent2 != nil {
		t.Fatalf("expected no intent on 2nd step, got %+v", intent2)
	}
	steps, paved, found = tracker.GetEntry(pos)
	if !found || steps != 2 || paved {
		t.Fatalf("expected steps=2, paved=false; got steps=%d, paved=%v", steps, paved)
	}

	// 3rd Step on grass_block -> Triggers PAVE_ROAD_PATH
	intent3 := tracker.RecordStep("agent1", pos, "minecraft:grass_block")
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
	intent4 := tracker.RecordStep("agent1", pos, "minecraft:grass_block")
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
			Position: &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5},
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
			Position: &civquestv1.Vec3{X: 11.5, Y: 64.0, Z: 10.5},
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
