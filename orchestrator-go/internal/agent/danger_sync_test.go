package agent_test

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-gey.2: a Drowned one member sees, the whole clan avoids for a game day.
func TestDanger_SharedWithTheClan(t *testing.T) {
	res := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	civic, err := db.OpenCivicRegistryDBForClan(res, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civic.Close()

	first := newSettler(t, res, civic, "aria")
	first.see(0, 0, func(f *civquestv1.TelemetryFrame) {
		f.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 7, EntityType: "minecraft:drowned", IsHostile: true,
			Position: &civquestv1.Vec3{X: 60, Y: 63, Z: 0}, Distance: 60}}
	})
	second := newSettler(t, res, civic, "bert")
	waitFor(t, "the clanmate to learn of the danger", func() bool {
		second.tick += 200
		second.frame(300, 300, nil)
		return second.rt.System1State().IsDestinationShunned(62, 63, 2, second.tick)
	})
}
