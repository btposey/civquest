package smoke

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
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
			// No hunger within a day: night foraging is not what these scenarios test.
			HungerDecayRateTicks: 1 << 30,
			InitialPosition:      &civquestv1.Vec3{X: 0.5 + float64(i)*1.5, Y: 64, Z: 0.5},
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

func bedRecipes() *crafting.RecipeGraph { return eval.VanillaRecipeGraph() }

// cq-390.2: three agents, ONE pioneer bed. The first owns it; the other two each craft their own
// bed from wool + planks at a table, place it, own it, and only ever sleep in their own bed.
func TestSmoke_ThreeAgentsOneBedEachEndsWithOwnBed(t *testing.T) {
	inv := append(survivalKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 16},
		&civquestv1.ItemStackProto{ItemId: "minecraft:white_wool", Count: 3},
	)
	civic, agents := newBedClan(t, []string{"able", "bert", "carl"}, 9000, inv, true)
	for _, a := range agents {
		a.sim.Runtime().System1State().SetRecipeGraph(bedRecipes())
	}

	// bert already has a shelter: its bed goes there.
	shelter := &civquestv1.BlockPosProto{X: 14, Y: 64, Z: 8}
	agents[1].sim.Runtime().System1State().SetShelterPos(shelter)

	sleptAt := map[string][]*civquestv1.BlockPosProto{}
	sawPlan := map[string]bool{}
	// Afternoon through the following night (TickDelta 4: tick 9000 -> 23000).
	for i := 0; i < 3500; i++ {
		for _, a := range agents {
			a.step(t)
			if a.sim.Runtime().System1State().ActiveActivityKind() == "bed" {
				sawPlan[a.id] = true
			}
			for _, in := range a.sim.IntentsThisStep {
				if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_SLEEP_IN_BED {
					sleptAt[a.id] = append(sleptAt[a.id], ib.Pos)
				}
			}
		}
	}

	seen := map[[3]int32]string{}
	for _, a := range agents {
		owned, err := civic.BedOwnedBy(a.id)
		if err != nil {
			t.Fatal(err)
		}
		b := a.sim.Runtime().System1State().GetAssignedBed()
		if len(owned) == 0 || b == nil {
			t.Fatalf("%s ends without its own bed (claims=%v assigned=%v planSeen=%v)", a.id, owned, b, sawPlan[a.id])
		}
		key := [3]int32{b.X, b.Y, b.Z}
		if o, _ := civic.BedOwner(b.X, b.Y, b.Z); o != a.id {
			t.Fatalf("%s is assigned a bed owned by %q", a.id, o)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("%s and %s share bed %v", a.id, other, key)
		}
		seen[key] = a.id
		if id, ok := a.sim.HasBlock(b.X, b.Y, b.Z); !ok || !strings.HasSuffix(id, "_bed") {
			t.Fatalf("%s assigned bed %v is not a bed block (%q)", a.id, key, id)
		}
		for _, p := range sleptAt[a.id] {
			if o, _ := civic.BedOwner(p.X, p.Y, p.Z); o != a.id {
				t.Fatalf("%s tried to sleep in a bed owned by %q at %v", a.id, o, p)
			}
		}
		if len(sleptAt[a.id]) == 0 {
			t.Errorf("%s never slept", a.id)
		}
	}
	if b := agents[1].sim.Runtime().System1State().GetAssignedBed(); math.Hypot(float64(b.X-shelter.X), float64(b.Z-shelter.Z)) > 2 {
		t.Errorf("bert's bed %v was not placed in its shelter %v", b, shelter)
	}
	for _, id := range []string{"bert", "carl"} {
		if !sawPlan[id] {
			t.Errorf("%s never ran the bed plan", id)
		}
	}
}

// cq-390.2: bed-less agents with no wool still hold an active bed plan: they harvest the timber
// for the planks while the wool is outstanding.
func TestSmoke_BedlessAgentWithoutWoolRunsBedPlan(t *testing.T) {
	civic, agents := newBedClan(t, []string{"able", "bert"}, 1000, survivalKit(), true)
	for _, a := range agents {
		a.sim.Runtime().System1State().SetRecipeGraph(bedRecipes())
		a.sim.AddBlock(-3, 64, 0, "minecraft:oak_log")
	}
	var bertPlanned, bertHarvested bool
	for i := 0; i < 400; i++ {
		for _, a := range agents {
			a.step(t)
		}
		b := agents[1]
		if b.sim.Runtime().System1State().ActiveActivityKind() == "bed" {
			bertPlanned = true
			for _, in := range b.sim.IntentsThisStep {
				if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK {
					bertHarvested = true
				}
			}
		}
	}
	if o, _ := civic.BedOwner(4, 64, 0); o != "able" {
		t.Fatalf("pioneer bed owner = %q, want able", o)
	}
	if !bertPlanned || !bertHarvested {
		t.Fatalf("bert (no bed, no wool) should run the bed plan: planned=%v harvested=%v", bertPlanned, bertHarvested)
	}
}

// bedOwnerAfter steps the agents until the bed at (4,64,0) has the wanted owner, returning the
// world ticks it took.
func bedOwnerAfter(t *testing.T, civic *db.CivicRegistryDB, want string, agents ...*bedAgent) int64 {
	t.Helper()
	start := agents[0].sim.BuildTelemetryFrame().WorldTick
	var owner string
	for i := 0; i < 200; i++ {
		var err error
		if owner, err = civic.BedOwner(4, 64, 0); err != nil {
			t.Fatal(err)
		}
		if owner == want {
			return agents[0].sim.BuildTelemetryFrame().WorldTick - start
		}
		for _, a := range agents {
			a.step(t)
		}
	}
	t.Fatalf("bed owner = %q, want %q", owner, want)
	return 0
}

// cq-390.5: permadeath releases the owner's claim and a living agent takes the bed.
func TestSmoke_DeadOwnersBedBecomesClaimable(t *testing.T) {
	civic, agents := newBedClan(t, []string{"able", "bert"}, 1000, survivalKit(), true)
	bedOwnerAfter(t, civic, "able", agents[0], agents[1])

	rt := agents[0].sim.Runtime()
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{DeathEvent: &civquestv1.DeathEvent{DamageSource: "fall", WorldTick: 100}})
	if !rt.IsDead() {
		t.Fatal("able should be dead")
	}
	if owned, _ := civic.BedOwnedBy("able"); len(owned) != 0 {
		t.Fatalf("the dead keep claims: %v", owned)
	}
	bedOwnerAfter(t, civic, "bert", agents[1])
}

// cq-390.5: a claim left behind by another world at the same coordinates is ignored.
func TestSmoke_OtherWorldsBedClaimIgnored(t *testing.T) {
	civic, agents := newBedClan(t, []string{"able"}, 1000, survivalKit(), true)
	civic.SetWorldID("world-old")
	if _, err := civic.ClaimBed(4, 64, 0, "ghost", 1); err != nil {
		t.Fatal(err)
	}
	civic.SetWorldID("world-new")
	bedOwnerAfter(t, civic, "able", agents[0])
}

// cq-390.5: a broken bed is released after a sustained observation, not on the first frame.
func TestSmoke_BrokenBedReleased(t *testing.T) {
	// Night: the owner stays by its bed (an owner out of sight keeps its claim).
	civic, agents := newBedClan(t, []string{"able"}, 13500, survivalKit(), true)
	able := agents[0]
	able.sim.AddBlock(0, 63, 0, "minecraft:grass_block")
	bedOwnerAfter(t, civic, "able", able)

	able.sim.RemoveBlock(4, 64, 0)
	if took := bedOwnerAfter(t, civic, "", able); took < 200 {
		t.Fatalf("claim released after only %d ticks", took)
	}
	if b := able.sim.Runtime().System1State().GetAssignedBed(); b != nil {
		t.Fatalf("able still assigned the broken bed: %v", b)
	}
}

// cq-390.5: a restarted agent takes its own bed back, not a nearer free one.
func TestSmoke_RestartedAgentKeepsOwnBed(t *testing.T) {
	civic, agents := newBedClan(t, []string{"able"}, 1000, survivalKit(), false)
	if _, err := civic.ClaimBed(14, 64, 0, "able", 1); err != nil {
		t.Fatal(err)
	}
	able := agents[0]
	able.sim.AddBlock(14, 64, 0, "minecraft:red_bed")
	able.sim.AddBlock(2, 64, 2, "minecraft:blue_bed")
	for i := 0; i < 20; i++ {
		able.step(t)
	}
	if b := able.sim.Runtime().System1State().GetAssignedBed(); b == nil || b.X != 14 {
		t.Fatalf("able should resume its bed at x=14, got %v", b)
	}
	if owned, _ := civic.BedOwnedBy("able"); len(owned) != 1 {
		t.Fatalf("able must hold exactly its old claim: %v", owned)
	}
}
