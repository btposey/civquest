package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func bhFrame(x, y, z float64, dmg bool, blocks ...[4]any) *civquestv1.TelemetryFrame {
	f := &civquestv1.TelemetryFrame{WorldTick: 100, Status: &civquestv1.AgentStatus{
		Health: 18, MaxHealth: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: x, Y: y, Z: z}}}
	if dmg {
		f.LastDamageTaken = &civquestv1.DamageEvent{SourceType: civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED, DamageAmount: 1}
	}
	for _, b := range blocks {
		f.InterestingBlocks = append(f.InterestingBlocks, &civquestv1.VisibleBlock{
			Pos: &civquestv1.BlockPosProto{X: int32(b[0].(int)), Y: int32(b[1].(int)), Z: int32(b[2].(int))}, BlockId: b[3].(string)})
	}
	return f
}

func TestDetectBlockHazard(t *testing.T) {
	ground := [4]any{0, 63, 0, "minecraft:grass_block"}
	t.Run("cactus adjacent with damage retreats away", func(t *testing.T) {
		h := detectBlockHazard(bhFrame(0.5, 64, 0.5, true, ground, [4]any{1, 64, 0, "minecraft:cactus"}))
		if h == nil || h.Kind != blockHazardCactus || h.Target == nil || h.Target.X >= 0.5 {
			t.Fatalf("want cactus retreat toward -X, got %+v", h)
		}
	})
	t.Run("cactus adjacent without damage is not a hazard", func(t *testing.T) {
		if h := detectBlockHazard(bhFrame(0.5, 64, 0.5, false, ground, [4]any{1, 64, 0, "minecraft:cactus"})); h != nil {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("standing in a berry bush", func(t *testing.T) {
		h := detectBlockHazard(bhFrame(0.5, 64, 0.5, false, ground, [4]any{0, 64, 0, "minecraft:sweet_berry_bush"}))
		if h == nil || h.Kind != blockHazardBerries || h.Target == nil {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("void edge: at border over a deep drop", func(t *testing.T) {
		h := detectBlockHazard(bhFrame(0.9, 64, 0.5, false, ground, [4]any{-1, 63, 0, "minecraft:stone"}))
		// Farther than the arrival tolerance, or the navigator would not move at all (cq-6l3.1).
		if h == nil || h.Kind != blockHazardVoidEdge || h.Target.X != -0.5 || h.Target.Z != 0.5 {
			t.Fatalf("got %+v", h)
		}
	})
	t.Run("void edge: cell centre and covered drops are safe", func(t *testing.T) {
		if h := detectBlockHazard(bhFrame(0.5, 64, 0.5, false, ground, [4]any{-1, 63, 0, "minecraft:stone"})); h != nil {
			t.Fatalf("centre: %+v", h)
		}
		if h := detectBlockHazard(bhFrame(0.8, 64, 0.5, false, ground, [4]any{-1, 63, 0, "minecraft:stone"})); h != nil {
			t.Fatalf("walking along a ledge, not yet at the border: %+v", h)
		}
		if h := detectBlockHazard(bhFrame(0.9, 64, 0.5, false, ground)); h != nil {
			t.Fatalf("sparse frame must not read as a void: %+v", h)
		}
		if h := detectBlockHazard(bhFrame(0.9, 64, 0.5, false, ground, [4]any{1, 62, 0, "minecraft:stone"})); h != nil {
			t.Fatalf("ledge with floor 2 down is not a deep drop: %+v", h)
		}
	})
	t.Run("suffocation: open side navigates, sealed digs", func(t *testing.T) {
		stone := func(x, y, z int) [4]any { return [4]any{x, y, z, "minecraft:stone"} }
		open := detectBlockHazard(bhFrame(0.5, 64, 0.5, true, stone(0, 64, 0), stone(0, 65, 0), stone(1, 64, 0), stone(1, 65, 0), stone(-1, 64, 0), stone(-1, 65, 0), stone(0, 64, 1), stone(0, 65, 1)))
		if open == nil || open.Kind != blockHazardSuffocation || open.Target == nil || open.Target.Z != -0.5 {
			t.Fatalf("open side (0,-1): %+v", open)
		}
		sealed := detectBlockHazard(bhFrame(0.5, 64, 0.5, true, stone(0, 64, 0), stone(0, 65, 0), stone(1, 64, 0), stone(1, 65, 0), stone(-1, 64, 0), stone(-1, 65, 0), stone(0, 64, 1), stone(0, 65, 1), stone(0, 64, -1), stone(0, 65, -1)))
		if sealed == nil || sealed.Dig == nil || sealed.Dig.Y != 65 {
			t.Fatalf("sealed: %+v", sealed)
		}
		if h := detectBlockHazard(bhFrame(0.5, 64, 0.5, false, stone(0, 65, 0))); h != nil {
			t.Fatalf("no damage, no suffocation: %+v", h)
		}
	})
}

func TestBlockHazardDamageIsNotCombat(t *testing.T) {
	f := bhFrame(0.5, 64, 0.5, true, [4]any{0, 63, 0, "minecraft:grass_block"}, [4]any{1, 64, 0, "minecraft:cactus"})
	ctx := &FSMContext{Frame: f}
	if ctx.DamageTaken() {
		t.Fatal("cactus damage must not read as combat damage")
	}
	if !ctx.IsEmergency() || ctx.BlockHazard() != blockHazardCactus {
		t.Fatalf("emergency=%v hazard=%q", ctx.IsEmergency(), ctx.BlockHazard())
	}
	el := emergencyLocal{}
	if classifyEmergency(ctx, el) != emergencyBlock {
		t.Fatal("expected block_hazard mode")
	}
	ins := el.reflexIntents(ctx, 100)
	if len(ins) != 1 || ins[0].GetNavigate() == nil {
		t.Fatalf("want one navigate, got %v", ins)
	}
	// moved away: resolved
	away := &FSMContext{Frame: bhFrame(-3.5, 64, 0.5, false, [4]any{1, 64, 0, "minecraft:cactus"})}
	if !el.resolved(away, 200) {
		t.Fatal("hazard cleared should resolve")
	}
}
