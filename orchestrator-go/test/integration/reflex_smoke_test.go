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

// Remaining hazards have no Go-side reflex yet (see bead cq-71l.13 "Missing reflexes: cactus/berry
// contact, void edge, suffocation"); these document the expected behaviour and skip until built.
func TestIntegration_Reflex_UnimplementedHazards(t *testing.T) {
	t.Run("Cactus", func(t *testing.T) {
		t.Skip("no cactus reflex: HandleTelemetryFrame emits no intents next to a cactus while taking damage (bead cq-71l.13)")
	})
	t.Run("VoidEdge", func(t *testing.T) {
		t.Skip("no void-edge reflex (bead cq-71l.13)")
	})
	t.Run("Suffocation", func(t *testing.T) {
		t.Skip("no suffocation reflex (bead cq-71l.13)")
	})
}
