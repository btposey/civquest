package smoke

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// exportedCatalog mimics the ExportRecipeGraph payload the NeoForge RecipeCatalogExporter
// sends for the vanilla wood/tool chain: tag-expanded accepted ids, 2x2 vs 3x3 flag, workstation.
func exportedCatalog() []*civquestv1.RecipeNode {
	planks := []string{"minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"}
	slot := func(n int32, ids ...string) *civquestv1.RecipeIngredientSlot {
		return &civquestv1.RecipeIngredientSlot{AcceptedItemIds: ids, Count: n}
	}
	res := func(id string, n int32) *civquestv1.ItemStackProto {
		return &civquestv1.ItemStackProto{ItemId: id, Count: n}
	}
	tool := func(name string, planksN, sticks int32) *civquestv1.RecipeNode {
		return &civquestv1.RecipeNode{RecipeId: "minecraft:" + name, RecipeType: "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table", Requires_3X3: true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(planksN, planks...), slot(sticks, "minecraft:stick")},
			Result:          res("minecraft:"+name, 1)}
	}
	return []*civquestv1.RecipeNode{
		{RecipeId: "minecraft:oak_planks", RecipeType: "minecraft:crafting_shapeless",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(1, "minecraft:oak_log")}, Result: res("minecraft:oak_planks", 4)},
		{RecipeId: "minecraft:stick", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(2, planks...)}, Result: res("minecraft:stick", 4)},
		{RecipeId: "minecraft:crafting_table", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(4, planks...)}, Result: res("minecraft:crafting_table", 1)},
		tool("wooden_axe", 3, 2), tool("wooden_sword", 2, 1),
		{RecipeId: "minecraft:torch", RecipeType: "minecraft:crafting_shaped",
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{slot(1, "minecraft:charcoal", "minecraft:coal"), slot(1, "minecraft:stick")},
			Result:          res("minecraft:torch", 4)},
	}
}

func craftedRecipes(sim *eval.TimeWarpSimulation, steps int) []string {
	var out []string
	for i := 0; i < steps; i++ {
		_ = sim.Step(4)
		for _, in := range sim.IntentsThisStep {
			if c := in.GetCraftItem(); c != nil {
				out = append(out, c.RecipeId)
			}
		}
	}
	return out
}

type catalogServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer
	recipes []*civquestv1.RecipeNode
}

func (c *catalogServer) ExportRecipeGraph(context.Context, *civquestv1.ExportRecipeRequest) (*civquestv1.ExportRecipeResponse, error) {
	return &civquestv1.ExportRecipeResponse{Recipes: c.recipes}, nil
}

func logsAndCoal() []*civquestv1.ItemStackProto {
	return []*civquestv1.ItemStackProto{{ItemId: "minecraft:oak_log", Count: 8}, {ItemId: "minecraft:coal", Count: 2}}
}

// cq-71l.10: java.recipe_catalog. The catalog crosses the real gRPC bridge contract
// (ExportRecipeGraph over bufconn), is installed on System 1 state the way
// AgentRuntime.syncRecipeGraph does, and then drives crafting in the sim. Without a synced
// catalog crafting is fail-closed.
func TestSmoke_RecipeCatalogOverBridgeDrivesCrafting(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(srv, &catalogServer{recipes: exportedCatalog()})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	client, err := bridge.NewClient("passthrough:///bufnet", "",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Fail-closed: no catalog, no graph-driven crafting.
	bare := newSim(t, "crafter0", "EXPLORER", eval.TimeWarpConfig{InitialTimeOfDay: 2000, InitialInventory: logsAndCoal()})
	if got := craftedRecipes(bare, 300); len(got) != 0 {
		t.Fatalf("crafted %v without a synced recipe catalog", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.ExportRecipeGraph(ctx, &civquestv1.ExportRecipeRequest{})
	if err != nil {
		t.Fatalf("ExportRecipeGraph: %v", err)
	}
	if len(resp.Recipes) != len(exportedCatalog()) {
		t.Fatalf("catalog lost recipes over the wire: %d", len(resp.Recipes))
	}
	sim := newSim(t, "crafter1", "EXPLORER", eval.TimeWarpConfig{InitialTimeOfDay: 2000, InitialInventory: logsAndCoal()})
	sim.Runtime().System1State().SetRecipeGraph(crafting.NewRecipeGraph(resp.Recipes))
	got := craftedRecipes(sim, 300)
	if len(got) == 0 || got[0] != "minecraft:oak_planks" {
		t.Fatalf("expected the synced catalog to drive an oak_planks craft first, got %v", got)
	}
}

// cq-71l.10: feat.crafting.recipe_graph. Multi-step planning: logs become planks, then a
// stick (an intermediate the agent was not holding), then the crafting table, then tools.
func TestSmoke_RecipeGraphPlansMultiStepChain(t *testing.T) {
	sim := newSim(t, "crafter2", "EXPLORER", eval.TimeWarpConfig{InitialTimeOfDay: 2000, InitialInventory: logsAndCoal()})
	sim.Runtime().System1State().SetRecipeGraph(crafting.NewRecipeGraph(exportedCatalog()))
	got := craftedRecipes(sim, 400)
	idx := func(r string) int {
		for i, g := range got {
			if g == r {
				return i
			}
		}
		return -1
	}
	if idx("minecraft:oak_planks") != 0 {
		t.Fatalf("planks must be crafted first, got %v", got)
	}
	if idx("minecraft:stick") < 0 {
		t.Fatalf("planner never crafted the stick intermediate, got %v", got)
	}
}

// cq-71l.10: s1.fact_graph. Evaluators populate the blackboard through the real runtime tick:
// categories from several evaluators are present, carry evidence, and reflect world state.
func TestSmoke_FactGraphPopulatedByRuntimeTick(t *testing.T) {
	sim := newSim(t, "facts", "FARMER", eval.TimeWarpConfig{InitialTimeOfDay: 3000, InitialInventory: survivalKit()})
	for i := 0; i < 20; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
	}
	fg := sim.Runtime().AgentFSM().LastFacts()
	if fg == nil {
		t.Fatal("runtime produced no fact graph")
	}
	if !fg.GetBool("bootstrap.has_kit") {
		t.Fatal("bootstrap.has_kit should be true for an agent holding axe+sword+torch")
	}
	if fg.GetBool("circadian.is_dusk_or_night") {
		t.Fatal("circadian.is_dusk_or_night must be false at tod 3000")
	}
	if r := fg.GetString("circadian.routine"); r == "" {
		t.Fatal("circadian.routine fact missing")
	}
	cats := map[string]bool{}
	for _, f := range fg.AllFacts() {
		cats[f.Category] = true
		if f.Evidence == "" && f.Category == "Circadian" {
			t.Fatalf("fact %s has no evidence", f.Key)
		}
	}
	if len(cats) < 5 {
		t.Fatalf("expected facts from >= 5 evaluator categories, got %v", cats)
	}
}

// cq-71l.10: feat.blueprints.library. Starter library materialises on disk (hermetic XDG dirs),
// the runtime's blueprint evaluator recommends from it for an unhoused agent with materials,
// and the recommended blueprint is then built to completion and registered as a civic zone.
func TestSmoke_BlueprintLibraryRecommendsAndBuilds(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	inv := survivalKit()
	for _, bp := range blueprints.StarterBlueprints() {
		for id, n := range bp.CalculateBOM() {
			inv = append(inv, &civquestv1.ItemStackProto{ItemId: id, Count: int32(n)})
		}
	}
	sim := newSim(t, "arch", "BUILDER", eval.TimeWarpConfig{InitialTimeOfDay: 3000, InitialInventory: inv})
	for i := 0; i < 50; i++ {
		if err := sim.Step(4); err != nil {
			t.Fatal(err)
		}
	}
	rec := sim.Runtime().AgentFSM().LastFacts().GetString("blueprint.recommended")
	if rec == "" {
		t.Fatal("blueprint evaluator recommended nothing despite full materials")
	}
	lib, err := blueprints.ListLibraryBlueprints()
	if err != nil || len(lib) != len(blueprints.StarterBlueprints()) {
		t.Fatalf("library should hold all starters: n=%d err=%v", len(lib), err)
	}
	bp, err := blueprints.GetLibraryBlueprint(rec)
	if err != nil {
		t.Fatalf("recommended blueprint %q not in library: %v", rec, err)
	}

	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic.db"), "clan_lib")
	if err != nil {
		t.Fatal(err)
	}
	defer civicDB.Close()
	coord, err := blueprints.NewConstructionCoordinator(blueprints.CoordinatorConfig{
		ProjectID: "proj-lib", Blueprint: bp, AnchorX: 40, AnchorY: 64, AnchorZ: 40, CivicDB: civicDB,
	})
	if err != nil {
		t.Fatal(err)
	}
	skill := system1.NewConstructionSkill(coord)
	fg := system1.NewFactGraph()
	fg.SetBool("construction.active", "construction", true, "active build project")
	state, local := system1.NewAgentState(), &system1.ConstructionLocal{}
	pos := civquestv1.Vec3{X: 41.5, Y: 64, Z: 41.5}
	placed := 0
	for i := 0; i < 2000 && !coord.IsComplete(); i++ {
		frame := &civquestv1.TelemetryFrame{WorldTick: int64(200 + i*4), Status: &civquestv1.AgentStatus{
			AgentId: "arch", Health: 20, MaxHealth: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: pos.X, Y: pos.Y, Z: pos.Z}, Inventory: inv}}
		intents, err := skill.Execute(&system1.SkillContext{AgentID: "arch", Frame: frame, State: state, CivicDB: civicDB, Construction: local}, fg)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range intents {
			if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
				pos = civquestv1.Vec3{X: nav.Target.X, Y: nav.Target.Y, Z: nav.Target.Z}
			}
			if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
				placed++
			}
		}
	}
	if !coord.IsComplete() || placed == 0 {
		t.Fatalf("library blueprint %q not built: complete=%v placed=%d", rec, coord.IsComplete(), placed)
	}
	if zones, err := civicDB.ListZones(); err != nil || len(zones) == 0 {
		t.Fatalf("completed build should register a civic zone: zones=%d err=%v", len(zones), err)
	}
}

// cq-71l.10: s2.governor_clan. Agents of one clan share a System 2 inference governor; against a
// real HTTP gateway the in-flight request count never exceeds the cap, yet every request completes.
func TestSmoke_GovernorCapsClanInferenceConcurrency(t *testing.T) {
	const limit, callers = 2, 8
	var inflight, peak int32
	reached := make(chan struct{})
	var once sync.Once
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		if n >= limit {
			once.Do(func() { close(reached) })
		}
		select {
		case <-reached:
		case <-time.After(2 * time.Second):
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}}})
	}))
	defer gw.Close()

	gov := system2.NewGovernor(limit, 5*time.Second)
	mk := func() *system2.Client {
		c, err := system2.NewClientWithConfig(xdg.LLMGatewayConfig{BaseURL: gw.URL + "/v1", Model: "mock"},
			xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 64}, system2.WithGovernor(gov), system2.WithHTTPClient(gw.Client()))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	clients := []*system2.Client{mk(), mk()} // two clan members, one shared governor

	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(c *system2.Client) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if _, err := c.ChatCompletion(ctx, system2.ChatCompletionRequest{Model: "mock",
				Messages: []system2.ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
				atomic.AddInt32(&failures, 1)
				t.Errorf("request failed: %v", err)
			}
		}(clients[i%len(clients)])
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("%d requests failed", failures)
	}
	if p := atomic.LoadInt32(&peak); p > limit || p < limit {
		t.Fatalf("expected peak in-flight == %d (cap reached, never exceeded), got %d", limit, p)
	}
}
