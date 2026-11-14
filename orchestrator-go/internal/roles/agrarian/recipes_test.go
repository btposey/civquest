package agrarian

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultAgrarianRecipeDAG(t *testing.T) {
	dag, err := DefaultAgrarianRecipeDAG()
	if err != nil {
		t.Fatalf("failed to load default agrarian recipe DAG: %v", err)
	}

	recipes := dag.AllRecipes()
	if len(recipes) == 0 {
		t.Fatal("expected non-empty recipe catalog")
	}

	// Verify all 4 mod ecosystems are represented
	modsFound := make(map[string]bool)
	stationsFound := make(map[ProcessingStation]bool)
	for _, r := range recipes {
		modsFound[r.ModID] = true
		stationsFound[r.Station] = true
	}

	expectedMods := []string{ModVanilla, ModFarmersDelight, ModVinery, ModBrewinAndChewin}
	for _, mod := range expectedMods {
		if !modsFound[mod] {
			t.Errorf("expected mod %q in catalog, but was not found", mod)
		}
	}

	expectedStations := []ProcessingStation{
		StationCraftingTable,
		StationSmoker,
		StationCookingPot,
		StationCuttingBoard,
		StationWinePress,
		StationFermentationBarrel,
		StationKeg,
	}
	for _, st := range expectedStations {
		if !stationsFound[st] {
			t.Errorf("expected station %q in catalog, but was not found", st)
		}
	}
}

func TestRecipeCycleDetection(t *testing.T) {
	dag := NewRecipeDAG()

	// Recipe 1: A -> B
	r1 := &Recipe{
		RecipeID:    "test:a_to_b",
		DisplayName: "A to B",
		ModID:       ModVanilla,
		Station:     StationCraftingTable,
		Inputs:      []IngredientRequirement{{ItemID: "item:a", Count: 1}},
		Outputs:     []RecipeOutput{{ItemID: "item:b", Count: 1}},
	}
	if err := dag.AddRecipe(r1); err != nil {
		t.Fatalf("unexpected error adding r1: %v", err)
	}

	// Recipe 2: B -> C
	r2 := &Recipe{
		RecipeID:    "test:b_to_c",
		DisplayName: "B to C",
		ModID:       ModVanilla,
		Station:     StationCraftingTable,
		Inputs:      []IngredientRequirement{{ItemID: "item:b", Count: 1}},
		Outputs:     []RecipeOutput{{ItemID: "item:c", Count: 1}},
	}
	if err := dag.AddRecipe(r2); err != nil {
		t.Fatalf("unexpected error adding r2: %v", err)
	}

	// Recipe 3: C -> A (Creates cycle A -> B -> C -> A)
	r3 := &Recipe{
		RecipeID:    "test:c_to_a",
		DisplayName: "C to A",
		ModID:       ModVanilla,
		Station:     StationCraftingTable,
		Inputs:      []IngredientRequirement{{ItemID: "item:c", Count: 1}},
		Outputs:     []RecipeOutput{{ItemID: "item:a", Count: 1}},
	}
	err := dag.AddRecipe(r3)
	if err == nil {
		t.Fatal("expected ErrRecipeCycleDetected, got nil")
	}
	if err != ErrRecipeCycleDetected {
		t.Fatalf("expected ErrRecipeCycleDetected, got %v", err)
	}

	// Recipe 4: Self loop X -> X
	r4 := &Recipe{
		RecipeID:    "test:self_loop",
		DisplayName: "Self Loop",
		ModID:       ModVanilla,
		Station:     StationCraftingTable,
		Inputs:      []IngredientRequirement{{ItemID: "item:x", Count: 1}},
		Outputs:     []RecipeOutput{{ItemID: "item:x", Count: 1}},
	}
	if err := dag.AddRecipe(r4); err != ErrRecipeCycleDetected {
		t.Fatalf("expected ErrRecipeCycleDetected on self loop, got %v", err)
	}
}

func TestQueryRecipes(t *testing.T) {
	dag, err := DefaultAgrarianRecipeDAG()
	if err != nil {
		t.Fatalf("failed to load default catalog: %v", err)
	}

	// Check recipes producing vegetable noodles
	noodlesRecipes := dag.RecipesProducing("farmersdelight:vegetable_noodles")
	if len(noodlesRecipes) == 0 {
		t.Fatal("expected at least one recipe producing vegetable noodles")
	}
	if noodlesRecipes[0].Station != StationCookingPot {
		t.Errorf("expected cooking pot station, got %s", noodlesRecipes[0].Station)
	}

	// Check recipes consuming wheat
	wheatConsumers := dag.RecipesConsuming("minecraft:wheat")
	if len(wheatConsumers) == 0 {
		t.Fatal("expected multiple recipes consuming wheat")
	}

	// Check non-existent item
	if len(dag.RecipesProducing("minecraft:bedrock")) != 0 {
		t.Error("expected 0 recipes producing bedrock")
	}
}

func TestCanCraft(t *testing.T) {
	dag, err := DefaultAgrarianRecipeDAG()
	if err != nil {
		t.Fatalf("failed to load default catalog: %v", err)
	}

	// Bread requires 3 wheat
	invSufficient := map[string]int{
		"minecraft:wheat": 5,
	}
	if !dag.CanCraft("minecraft:bread", invSufficient) {
		t.Error("expected CanCraft to return true with 5 wheat")
	}

	invInsufficient := map[string]int{
		"minecraft:wheat": 2,
	}
	if dag.CanCraft("minecraft:bread", invInsufficient) {
		t.Error("expected CanCraft to return false with 2 wheat")
	}

	if dag.CanCraft("non_existent_recipe", invSufficient) {
		t.Error("expected CanCraft to return false for non-existent recipe")
	}
}

func TestPlanCraftingPath(t *testing.T) {
	dag, err := DefaultAgrarianRecipeDAG()
	if err != nil {
		t.Fatalf("failed to load default catalog: %v", err)
	}

	// Test multi-step crafting path for Farmers Delight vegetable noodles:
	// Inputs needed: raw_pasta (from wheat_dough <- wheat + water_bucket), tomato, onion, carrot, bowl
	available := map[string]int{
		"minecraft:wheat":        3,
		"minecraft:water_bucket": 1,
		"farmersdelight:tomato":  1,
		"farmersdelight:onion":   1,
		"minecraft:carrot":       1,
		"minecraft:bowl":         1,
	}

	plan, err := dag.PlanCraftingPath("farmersdelight:vegetable_noodles", available)
	if err != nil {
		t.Fatalf("expected successful plan for vegetable noodles, got %v", err)
	}

	if plan.TargetItemID != "farmersdelight:vegetable_noodles" {
		t.Errorf("target item mismatch: got %s", plan.TargetItemID)
	}

	// Should have 3 steps: wheat dough -> raw pasta -> vegetable noodles
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps in plan, got %d", len(plan.Steps))
	}

	if plan.Steps[0].Recipe.RecipeID != "farmersdelight:wheat_dough" {
		t.Errorf("expected step 0 to be wheat dough, got %s", plan.Steps[0].Recipe.RecipeID)
	}
	if plan.Steps[1].Recipe.RecipeID != "farmersdelight:raw_pasta" {
		t.Errorf("expected step 1 to be raw pasta, got %s", plan.Steps[1].Recipe.RecipeID)
	}
	if plan.Steps[2].Recipe.RecipeID != "farmersdelight:vegetable_noodles" {
		t.Errorf("expected step 2 to be vegetable noodles, got %s", plan.Steps[2].Recipe.RecipeID)
	}
}

func TestExportRecipeGraph(t *testing.T) {
	dag, err := DefaultAgrarianRecipeDAG()
	if err != nil {
		t.Fatalf("failed to load default catalog: %v", err)
	}

	jsonBytes, err := dag.ExportRecipeGraph()
	if err != nil {
		t.Fatalf("failed to export recipe graph: %v", err)
	}

	var exported RecipeGraphExport
	if err := json.Unmarshal(jsonBytes, &exported); err != nil {
		t.Fatalf("failed to parse exported JSON: %v", err)
	}

	if exported.Version != 1 {
		t.Errorf("expected version 1, got %d", exported.Version)
	}
	if exported.RecipeCount != len(dag.AllRecipes()) {
		t.Errorf("recipe count mismatch: %d vs %d", exported.RecipeCount, len(dag.AllRecipes()))
	}
	if len(exported.Nodes) == 0 {
		t.Error("expected non-empty nodes list")
	}
	if len(exported.Edges) == 0 {
		t.Error("expected non-empty edges list")
	}

	// Check string export
	jsonStr, err := dag.ExportRecipeGraphJSON()
	if err != nil {
		t.Fatalf("failed to export JSON string: %v", err)
	}
	if !strings.Contains(jsonStr, "farmersdelight:vegetable_noodles") {
		t.Error("exported JSON string missing vegetable noodles")
	}
	if !strings.Contains(jsonStr, "vinery:claret_wine") {
		t.Error("exported JSON string missing claret wine")
	}
	if !strings.Contains(jsonStr, "brewinandchewin:beer") {
		t.Error("exported JSON string missing beer")
	}
}
