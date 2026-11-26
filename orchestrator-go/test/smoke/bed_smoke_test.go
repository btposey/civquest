package smoke

import (
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

type bedAgent struct {
	id         string
	sim        *eval.TimeWarpSimulation
	sawShelter bool
	placed     int
}

func newBedClan(t *testing.T, ids []string, tod int64, inv []*civquestv1.ItemStackProto, bed bool) (*db.CivicRegistryDB, []*bedAgent) {
	t.Helper()
	civic, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic_registry.db"), "pioneers")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = civic.Close() })
	var out []*bedAgent
	for i, id := range ids {
		sim := newSim(t, id, "EXPLORER", eval.TimeWarpConfig{
			InitialTimeOfDay: tod, InitialInventory: append([]*civquestv1.ItemStackProto(nil), inv...),
			CivicRegistryDB: civic,
			InitialPosition: &civquestv1.Vec3{X: 0.5 + float64(i)*1.5, Y: 64, Z: 0.5},
		})
		if bed {
			sim.AddBlock(4, 64, 0, "minecraft:red_bed")
		}
		out = append(out, &bedAgent{id: id, sim: sim})
	}
	return civic, out
}

func (a *bedAgent) step(t *testing.T) {
	t.Helper()
	if err := a.sim.Step(4); err != nil {
		t.Fatal(err)
	}
	if a.sim.Runtime().System1State().ActiveActivityKind() == "shelter" {
		a.sawShelter = true
	}
	for _, in := range a.sim.IntentsThisStep {
		if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
			a.placed++
		}
	}
}

// cq-390.1: three agents share ONE pioneer bed at dusk. Exactly one owns it; the other two must
// not be satisfied by it and instead build a shelter (the playtest-2 regression: nobody did).
func TestSmoke_SharedBedOwnerOnlyOthersBuildShelter(t *testing.T) {
	inv := append(survivalKit(), &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 40})
	civic, agents := newBedClan(t, []string{"able", "bert", "carl"}, 12200, inv, true)

	for i := 0; i < 500; i++ {
		for _, a := range agents {
			a.step(t)
		}
	}

	owner, _ := civic.BedOwner(4, 64, 0)
	if owner == "" {
		t.Fatal("nobody claimed the bed")
	}
	owners := 0
	for _, a := range agents {
		st := a.sim.Runtime().System1State()
		if b := st.GetAssignedBed(); b != nil {
			owners++
			if a.id != owner {
				t.Fatalf("%s holds the bed owned by %s", a.id, owner)
			}
		} else if !a.sawShelter && a.placed == 0 {
			t.Fatalf("%s has no bed and never started a shelter (shelter=%v placed=%d)", a.id, a.sawShelter, a.placed)
		}
	}
	if owners != 1 {
		t.Fatalf("expected exactly one agent assigned the shared bed, got %d", owners)
	}
}
