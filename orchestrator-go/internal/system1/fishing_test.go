package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func fishFrame(tick int64) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: tick,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}, FoodLevel: 9, Health: 20, MaxHealth: 20,
			Inventory: []*civquestv1.ItemStackProto{{ItemId: "minecraft:fishing_rod", Count: 1}},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{{Pos: &civquestv1.BlockPosProto{X: 0, Y: 63, Z: 5}, BlockId: "minecraft:water"}},
	}
}

func TestEvaluateFishingUtilityAndLineState(t *testing.T) {
	cow := func(z float64) *civquestv1.VisibleEntity {
		return &civquestv1.VisibleEntity{EntityId: 9, EntityType: "minecraft:cow", Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: z}}
	}
	mode := func(in *civquestv1.AgentIntent) civquestv1.UseItemIntent_Mode { return in.GetUseItem().GetMode() }

	// A rod beside water beats a cow that is a long walk away.
	f := fishFrame(1000)
	f.VisibleEntities = []*civquestv1.VisibleEntity{cow(-15)}
	if in := EvaluateForaging("a", f, nil, newForageLocal()); mode(in) != civquestv1.UseItemIntent_CAST_FISHING_ROD {
		t.Fatalf("rod beside water should beat a far cow, got %v", in)
	}
	// Livestock in reach is taken first.
	f = fishFrame(1000)
	f.VisibleEntities = []*civquestv1.VisibleEntity{cow(2.5)}
	if in := EvaluateForaging("a", f, nil, newForageLocal()); in.GetAttack() == nil {
		t.Fatalf("a cow in reach should be hunted before fishing, got %v", in)
	}

	// On the move the agent stops before casting.
	f = fishFrame(1000)
	f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	if in := EvaluateForaging("a", f, nil, newForageLocal()); in.GetNavigate().GetTarget().GetZ() != f.Status.Position.Z {
		t.Fatalf("a walking agent should stop before casting, got %v", in)
	}

	// A cast line owns the frame: hold while floating, reel on the bite, even with a cow in reach.
	mem := newForageLocal()
	step := func(tick int64, inWater, bite bool) *civquestv1.AgentIntent {
		f := fishFrame(tick)
		f.VisibleEntities = []*civquestv1.VisibleEntity{cow(2.5)}
		f.Status.Fishing = &civquestv1.FishingState{Cast: true, InWater: inWater, Bite: bite}
		return EvaluateForaging("a", f, nil, mem)
	}
	mem.Fish.LastCastTick = 1000
	if in := step(1004, false, false); in != nil {
		t.Fatalf("airborne bobber must not be reeled, got %v", in)
	}
	if in := step(1060, true, false); in != nil {
		t.Fatalf("floating bobber should hold, got %v", in)
	}
	if in := step(1064, false, false); in != nil {
		t.Fatalf("one dry frame (bobbing) must not reel, got %v", in)
	}
	if in := step(1100, true, true); mode(in) != civquestv1.UseItemIntent_REEL_FISHING_ROD {
		t.Fatalf("bite should reel, got %v", in)
	}
	// A bobber on dry land is reeled in after it stays out.
	step(1200, false, false)
	if in := step(1224, false, false); mode(in) != civquestv1.UseItemIntent_REEL_FISHING_ROD || mem.Fish.DryCasts != 1 {
		t.Fatalf("dry line should be reeled and counted, got %v dry=%d", in, mem.Fish.DryCasts)
	}
	// After too many misses the rod option is abandoned.
	mem.Fish.DryCasts = fishMaxDryCasts
	if in := EvaluateForaging("a", fishFrame(2000), nil, mem); mode(in) == civquestv1.UseItemIntent_CAST_FISHING_ROD {
		t.Fatalf("rod fishing should be given up after %d dry casts", fishMaxDryCasts)
	}
}
