package integration

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newReflexRuntime(t *testing.T, id string) *agent.AgentRuntime {
	t.Helper()
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	_ = r.InitAgentDirectories(id)
	cfg := xdg.DefaultAgentIdentityConfig(id)
	cfg.ClanID = "pioneers"
	cfg.Profession = "EXPLORER"
	rt, err := agent.NewRuntime(agent.Config{AgentID: id, ClanID: "pioneers", IdentityConfig: cfg, Resolver: r, IntentQueueSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func drain(rt *agent.AgentRuntime) []*civquestv1.AgentIntent {
	var out []*civquestv1.AgentIntent
	for {
		select {
		case in := <-rt.IntentQueue():
			out = append(out, in)
		default:
			return out
		}
	}
}

func baseFrame(tick int64, hp float32) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{WorldTick: tick, TimeOfDay: 3000, Status: &civquestv1.AgentStatus{
		Health: hp, MaxHealth: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
		Inventory: []*civquestv1.ItemStackProto{{ItemId: "minecraft:wooden_sword", Count: 1}}}}
}

func creeperFrame(tick int64, dist float32, fuse int32) *civquestv1.TelemetryFrame {
	f := baseFrame(tick, 20)
	f.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 5, EntityType: "minecraft:creeper", IsHostile: true,
		Distance: dist, FuseTicks: fuse, Position: &civquestv1.Vec3{X: 0.5 + float64(dist), Y: 64, Z: 0.5}}}
	return f
}

// reflex.dodge: a hissing creeper inside the panic radius makes the agent flee at once, with
// zero LLM involvement (no System 2 attached); a silent or distant creeper does not.
func TestIntegration_Reflex_CreeperFuseDodge(t *testing.T) {
	rt := newReflexRuntime(t, "dodger")
	rt.HandleTelemetryFrame(creeperFrame(100, 3, 25))
	var fled bool
	for _, in := range drain(rt) {
		if a := in.GetAttack(); a != nil && a.TargetEntityId == 5 && a.FleeImmediately {
			fled = true
		}
	}
	if !fled {
		t.Fatal("hissing creeper at 3 blocks must trigger a flee_immediately reflex against it")
	}

	calm := newReflexRuntime(t, "calm")
	calm.HandleTelemetryFrame(creeperFrame(100, 20, 0))
	for _, in := range drain(calm) {
		if a := in.GetAttack(); a != nil && a.FleeImmediately {
			t.Fatalf("a distant, non-fusing creeper must not trigger a dodge: %v", in)
		}
	}
}

// reflex.hazard.fall_void_cactus (fall part): fall damage is environmental, so it must not be
// mistaken for a combat threat and cause a phantom fight-or-flee.
func TestIntegration_Reflex_FallDamageDoesNotTriggerCombatFlee(t *testing.T) {
	rt := newReflexRuntime(t, "faller")
	f := baseFrame(100, 9)
	f.LastDamageTaken = &civquestv1.DamageEvent{SourceType: civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_FALL, DamageAmount: 6}
	rt.HandleTelemetryFrame(f)
	for _, in := range drain(rt) {
		if a := in.GetAttack(); a != nil {
			t.Fatalf("fall damage produced a combat intent: %v", in)
		}
	}
}

// reflex.hazard.fall_void_cactus (cactus, void edge, suffocation): block-contact hazards derived
// in the emergency activity from adjacent hazard blocks, damage and position (bead cq-71l.13).
func TestIntegration_Reflex_BlockHazards(t *testing.T) {
	block := func(x, y, z int32, id string) *civquestv1.VisibleBlock {
		return &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: x, Y: y, Z: z}, BlockId: id}
	}
	envDamage := &civquestv1.DamageEvent{SourceType: civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED, DamageAmount: 1}
	firstNav := func(rt *agent.AgentRuntime, f *civquestv1.TelemetryFrame) *civquestv1.NavigateIntent {
		for tick := int64(0); tick < 3; tick++ {
			f.WorldTick = 100 + tick*5
			rt.HandleTelemetryFrame(f)
			for _, in := range drain(rt) {
				if n := in.GetNavigate(); n != nil {
					return n
				}
			}
		}
		return nil
	}
	t.Run("Cactus", func(t *testing.T) {
		f := baseFrame(100, 18)
		f.InterestingBlocks = []*civquestv1.VisibleBlock{block(0, 63, 0, "minecraft:grass_block"), block(1, 64, 0, "minecraft:cactus")}
		f.LastDamageTaken = envDamage
		n := firstNav(newReflexRuntime(t, "cactus"), f)
		if n == nil || n.Target.X >= f.Status.Position.X {
			t.Fatalf("must navigate away from the cactus (-X), got %v", n)
		}
	})
	t.Run("VoidEdge", func(t *testing.T) {
		f := baseFrame(100, 20)
		f.Status.Position = &civquestv1.Vec3{X: 0.9, Y: 64, Z: 0.5}
		f.InterestingBlocks = []*civquestv1.VisibleBlock{block(0, 63, 0, "minecraft:stone"), block(-1, 63, 0, "minecraft:stone")}
		n := firstNav(newReflexRuntime(t, "voidedge"), f)
		// Past the cell centre by the arrival tolerance, or the navigator would not move (cq-6l3.1).
		if n == nil || n.Target.X != -0.5 || n.ArrivalTolerance != 1 {
			t.Fatalf("must step back from the drop, got %v", n)
		}
	})
	t.Run("Suffocation", func(t *testing.T) {
		f := baseFrame(100, 18)
		f.InterestingBlocks = []*civquestv1.VisibleBlock{block(0, 64, 0, "minecraft:stone"), block(0, 65, 0, "minecraft:stone")}
		f.LastDamageTaken = envDamage
		n := firstNav(newReflexRuntime(t, "suffocate"), f)
		if n == nil {
			t.Fatal("must move out of the block (open neighbour cell)")
		}
	})
}
