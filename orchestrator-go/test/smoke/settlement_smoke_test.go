package smoke

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// cq-71l.8: s1.skill.armory_militia. An off-watch guard with an armory deficit and materials
// places a crafting table, then crafts a weapon, and clears the maintenance flag.
func TestSmoke_ArmoryCraftsWeapon(t *testing.T) {
	inv := append(survivalKit(),
		&civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: 6},
		&civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4},
		&civquestv1.ItemStackProto{ItemId: "minecraft:crafting_table", Count: 1},
	)
	sim := newSim(t, "armorer", "GUARD", eval.TimeWarpConfig{InitialTimeOfDay: 1200, InitialInventory: inv})
	st := sim.Runtime().System1State()
	st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
	st.SetArmoryMaintenanceNeeded(true)

	var placedTable, crafted bool
	var recipe string
	for i := 0; i < 200 && !crafted; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		for _, in := range sim.IntentsThisStep {
			if ib := in.GetInteractBlock(); ib != nil && ib.BlockOrItemId == "minecraft:crafting_table" &&
				ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
				placedTable = true
			}
			if c := in.GetCraftItem(); c != nil {
				crafted, recipe = true, c.RecipeId
			}
		}
	}
	if !placedTable {
		t.Fatal("armory never placed the crafting table")
	}
	if !crafted || recipe != "minecraft:stone_sword" {
		t.Fatalf("expected a stone_sword craft from cobblestone+stick, got crafted=%v recipe=%q", crafted, recipe)
	}
	if n := sim.InventoryItemCount("minecraft:stone_sword"); n < 1 {
		t.Fatalf("crafted weapon missing from inventory (stone_sword=%d)", n)
	}
	// Materials are exhausted for other categories: the flag must clear so the guard goes back to duty.
	for i := 0; i < 20 && st.IsArmoryMaintenanceNeeded(); i++ {
		_ = sim.Step(4)
	}
	if st.IsArmoryMaintenanceNeeded() {
		t.Fatal("armory maintenance flag never cleared after crafting")
	}
}

// cq-71l.8: s1.evaluator.colonize. An agent flagged as establishing a new settlement enters
// the COLONIZE stage, scouts away, and claims a new site (home origin relocates, outpost added).
func TestSmoke_ColonizePicksNewSite(t *testing.T) {
	sim := newSim(t, "pioneer", "EXPLORER", eval.TimeWarpConfig{InitialTimeOfDay: 1500, InitialInventory: survivalKit()})
	st := sim.Runtime().System1State()
	st.SetEstablishingNewSettlement(true)
	st.SetHomeOrigin(&civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5})
	home0 := civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}

	sawColonize := false
	relocated := false
	for i := 0; i < 3500 && !relocated; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
		if sim.CurrentFSMState() == system1.StageColonize {
			sawColonize = true
		}
		if h := st.GetHomeOrigin(); h != nil && math.Hypot(h.X-home0.X, h.Z-home0.Z) > 50 && len(st.GetOutposts()) >= 2 {
			relocated = true
		}
	}
	if !sawColonize {
		t.Fatal("agent never entered the COLONIZE stage")
	}
	if !relocated {
		t.Fatalf("colonize never picked a new site: home=%v outposts=%d", st.GetHomeOrigin(), len(st.GetOutposts()))
	}
}

// cq-71l.8: feat.construction.multi_builder. Several builders interleave on one blueprint
// project (shared coordinator + civic registry) in a shared voxel world: they claim disjoint
// quadrants, never place the same block twice, and together finish the structure.
func TestSmoke_MultiBuilderPlacesDisjointBlocks(t *testing.T) {
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic.db"), "clan_multibuild")
	if err != nil {
		t.Fatal(err)
	}
	defer civicDB.Close()

	bp := &blueprints.Blueprint{
		ID: "smoke_hall", Name: "Smoke Hall", Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{Width: 4, Height: 1, Length: 4},
		Palette:    map[int]string{1: "minecraft:oak_planks"},
		Layers: []blueprints.Layer{{Y: 0, Matrix: [][]int{
			{1, 1, 1, 1}, {1, 1, 1, 1}, {1, 1, 1, 1}, {1, 1, 1, 1},
		}}},
	}
	const ax, ay, az = 20, 64, 20
	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-smoke-hall", Blueprint: bp, AnchorX: ax, AnchorY: ay, AnchorZ: az, CivicDB: civicDB,
	})
	if err != nil {
		t.Fatal(err)
	}
	skill := system1.NewConstructionSkill(coord)
	fg := system1.NewFactGraph()
	fg.SetBool("construction.active", "construction", true, "active build project")

	type builder struct {
		id       string
		state    *system1.AgentState
		local    *system1.ConstructionLocal
		pos      civquestv1.Vec3
		placed   map[[3]int32]bool
		tickBase int64
	}
	builders := []*builder{}
	for _, id := range []string{"builder-a", "builder-b", "builder-c"} {
		builders = append(builders, &builder{id: id, state: system1.NewAgentState(), local: &system1.ConstructionLocal{},
			pos: civquestv1.Vec3{X: ax + 1.5, Y: ay, Z: az + 1.5}, placed: map[[3]int32]bool{}})
	}
	world := map[[3]int32]string{}
	owner := map[[3]int32]string{}

	for round := 0; round < 200 && !coord.IsComplete(); round++ {
		for _, b := range builders {
			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(200 + round*4),
				Status: &civquestv1.AgentStatus{
					AgentId: b.id, Health: 20, MaxHealth: 20, FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: b.pos.X, Y: b.pos.Y, Z: b.pos.Z},
					Inventory: []*civquestv1.ItemStackProto{{ItemId: "minecraft:oak_planks", Count: 64}},
				},
			}
			intents, err := skill.Execute(&system1.SkillContext{
				AgentID: b.id, Frame: frame, State: b.state, CivicDB: civicDB, Construction: b.local,
			}, fg)
			if err != nil {
				t.Fatalf("%s: %v", b.id, err)
			}
			for _, in := range intents {
				if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
					b.pos = civquestv1.Vec3{X: nav.Target.X, Y: nav.Target.Y, Z: nav.Target.Z}
				}
				if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
					k := [3]int32{ib.Pos.X, ib.Pos.Y, ib.Pos.Z}
					if prev, dup := owner[k]; dup {
						t.Fatalf("block %v placed twice (by %s then %s)", k, prev, b.id)
					}
					owner[k] = b.id
					world[k] = ib.BlockOrItemId
					b.placed[k] = true
				}
			}
		}
	}

	if !coord.IsComplete() {
		t.Fatalf("shared project not complete after interleaved building: %d/16 blocks", len(world))
	}
	if len(world) != 16 {
		t.Fatalf("expected 16 distinct blocks placed, got %d", len(world))
	}
	active := 0
	for _, b := range builders {
		if len(b.placed) > 0 {
			active++
		}
	}
	if active < 2 {
		t.Fatalf("expected at least 2 builders to contribute, got %d", active)
	}
}

// cq-71l.8: feat.sapling_replant (ADR-025) lives on branch wip/ladders-replant-carry and is
// tracked as `wip` in the coverage matrix; its smoke test lands with that branch.
