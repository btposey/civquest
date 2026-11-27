package system1

import (
	"math"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-gey.2: a Drowned stays registered for a game day and its ground is not walked into.
func TestDanger_LingeringMobIsRememberedForADay(t *testing.T) {
	st := NewAgentState()
	observeDangers(&FSMContext{State: st, Frame: rf(100, 3000, withPos(0, 64, 0), withEntities(
		&civquestv1.VisibleEntity{EntityId: 7, EntityType: "minecraft:drowned", IsHostile: true, Position: &civquestv1.Vec3{X: 40, Y: 63, Z: 0}},
		&civquestv1.VisibleEntity{EntityId: 8, EntityType: "minecraft:zombie", IsHostile: true, Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 60}},
	))})
	if !st.IsTargetUnreachable(45, 63, 3, 100+DangerMemoryTicks-1) {
		t.Error("the Drowned's water is still dangerous a day later")
	}
	if st.IsTargetUnreachable(45, 63, 3, 100+DangerMemoryTicks) {
		t.Error("an old danger is forgotten")
	}
	if st.IsTargetUnreachable(0, 64, 60, 200) {
		t.Error("a passing zombie that did no harm is not registered")
	}
	// Home stays reachable.
	st.SetCollectiveSettlementSite(48, 64, 0)
	if st.IsTargetUnreachable(48, 64, 0, 200) {
		t.Error("an agent must be able to get home")
	}
}

func TestDanger_AnyMobThatHurtsIsRegistered(t *testing.T) {
	st := NewAgentState()
	f := rf(100, 14000, withPos(10, 64, 10))
	f.LastDamageTaken = &civquestv1.DamageEvent{SourceType: civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB, AttackerName: "Zombie", AttackerEntityId: 9, DamageAmount: 3}
	observeDangers(&FSMContext{State: st, Frame: f})
	if got := st.Dangers(100); len(got) != 1 || got[0].Kind != "zombie" || got[0].X != 10 {
		t.Fatalf("dangers %+v", got)
	}
}

func TestDanger_WalksGoRound(t *testing.T) {
	st := NewAgentState()
	st.NoteDanger(Danger{Kind: "drowned", X: 50, Y: 63, Z: 1, Seen: 100})
	from, to := [3]float64{0, 64, 0}, [3]float64{100, 64, 0}
	via, ok := st.detour(from, to, 200)
	if !ok {
		t.Fatal("a walk through the danger must detour")
	}
	if via[2] >= 0 || math.Hypot(via[0]-50, via[2]-1) < dangerRadius {
		t.Errorf("the waypoint must pass outside, away from the centre: %v", via)
	}
	// Both legs by the waypoint stay clear.
	for _, leg := range [][2][3]float64{{from, via}, {via, to}} {
		for i := 0.0; i <= 1; i += 0.05 {
			x, z := leg[0][0]+(leg[1][0]-leg[0][0])*i, leg[0][2]+(leg[1][2]-leg[0][2])*i
			if d := math.Hypot(x-50, z-1); d < dangerRadius-detourGrid {
				t.Errorf("leg %v passes %.1f from the danger", leg, d)
			}
		}
	}
	if _, ok := st.detour(from, [3]float64{0, 64, 100}, 200); ok {
		t.Error("a walk that stays away needs no detour")
	}
}

// The play test failure: hit by a Drowned, the agent lost sight of it for a moment and walked
// straight back. It now keeps going until it is clear.
func TestDanger_HitAgentKeepsFleeingWhenTheAttackerDropsFromSight(t *testing.T) {
	fsm := NewAgentFSM(StageBootstrap, nil)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("fred")
	tick := func(at int64, x float64, opts ...fo) FSMResult {
		f := rf(at, 5000, append([]fo{withPos(x, 63, 0), withInv(survivalKit()...)}, opts...)...)
		return fsm.Tick(&FSMContext{AgentID: "fred", State: st, Config: cfg, Frame: f})
	}
	drowned := &civquestv1.VisibleEntity{EntityId: 77, EntityType: "minecraft:drowned", IsHostile: true, Position: &civquestv1.Vec3{X: 8, Y: 63, Z: 0}, Distance: 8}
	f := rf(100, 5000, withPos(0, 63, 0), withInv(survivalKit()...), withEntities(drowned), withHP(12))
	f.LastDamageTaken = &civquestv1.DamageEvent{SourceType: civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB, AttackerName: "Drowned", AttackerEntityId: 77, DamageAmount: 8}
	fsm.Tick(&FSMContext{AgentID: "fred", State: st, Config: cfg, Frame: f})

	res := tick(160, -2, withHP(12))
	if res.Stage != StageEmergency {
		t.Fatalf("still in the Drowned's reach after 3s unseen, stage %s", res.Stage)
	}
	var away bool
	for _, in := range res.Intents {
		if nav := in.Intent.GetNavigate(); nav != nil && nav.Target.X < -10 {
			away = true
		}
	}
	if !away {
		t.Errorf("expected a walk away from the danger, got %+v", res.Intents)
	}
	if res := tick(200, -19, withHP(12)); res.Stage == StageEmergency {
		t.Errorf("clear of the danger, the emergency should end")
	}
}
