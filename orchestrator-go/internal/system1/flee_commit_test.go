package system1

import (
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Live (cq-6l3.19): a fleeing agent was given a new destination every few ticks and barely moved.
func TestFlee_KeepsItsDestination(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("runner")
	nodes := []db.MapNode{{ID: "east", X: 40, Y: 64, Z: 20, SafetyScore: 1}, {ID: "north", X: 20, Y: 64, Z: -40, SafetyScore: 1},
		{ID: "south", X: 10, Y: 64, Z: 44, SafetyScore: 1}}
	targets := map[[2]int]bool{}
	moves := 0
	for i := int64(0); i < 20; i++ {
		tick := 100 + i*4
		frame := makeArbiterTestFrame(tick, 15000, 3, nil, nil)
		frame.Status.Health, frame.Status.Position = 8, &civquestv1.Vec3{X: 0.5 + float64(i)*0.2, Y: 64, Z: 0.5}
		// The attacker circles close by, which used to swing the choice of safe spot.
		angle := float64(i) * 1.3
		frame.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 7, EntityType: "minecraft:spider", IsHostile: true,
			Position: &civquestv1.Vec3{X: frame.Status.Position.X - 3 + math.Cos(angle), Y: 64, Z: 0.5 + math.Sin(angle)*2}, Distance: 3}}
		res := fsm.Tick(&FSMContext{AgentID: "runner", State: st, Config: cfg, Frame: frame, SafeNodes: nodes})
		for _, in := range res.Intents {
			if n := in.Intent.GetNavigate(); n != nil {
				moves++
				targets[[2]int{int(math.Round(n.Target.X / 4)), int(math.Round(n.Target.Z / 4))}] = true
			}
		}
	}
	if moves == 0 || len(targets) > 2 {
		t.Errorf("%d moves to %d different destinations in 80 ticks, want a committed flight", moves, len(targets))
	}
}

// Playtest 5 live runs: a pinned agent was sent to the same unreachable spot until it died.
func TestFlee_PinnedAgentTriesAnotherWay(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("pinned")
	nodes := []db.MapNode{{ID: "east", X: 40, Y: 64, Z: 0, SafetyScore: 1}}
	var targets [][2]float64
	for tick := int64(100); tick < 320; tick += 4 {
		frame := makeArbiterTestFrame(tick, 15000, 3, nil, nil)
		frame.Status.Health, frame.Status.Position = 8, &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}
		frame.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 7, EntityType: "minecraft:skeleton", IsHostile: true,
			Position: &civquestv1.Vec3{X: -8, Y: 64, Z: 0.5}, Distance: 8.5}}
		res := fsm.Tick(&FSMContext{AgentID: "pinned", State: st, Config: cfg, Frame: frame, SafeNodes: nodes})
		for _, in := range res.Intents {
			if n := in.Intent.GetNavigate(); n != nil {
				at := [2]float64{math.Round(n.Target.X), math.Round(n.Target.Z)}
				if len(targets) == 0 || targets[len(targets)-1] != at {
					targets = append(targets, at)
				}
			}
		}
	}
	if len(targets) < 3 {
		t.Errorf("an agent that cannot move kept the same way out: %v", targets)
	}
}
