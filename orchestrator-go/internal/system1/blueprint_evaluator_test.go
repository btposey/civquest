package system1

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestBlueprintEvaluator_Registration(t *testing.T) {
	eval, ok := DefaultRegistry.GetEvaluator("blueprint")
	if !ok || eval == nil {
		t.Fatalf("expected 'blueprint' evaluator registered in DefaultRegistry")
	}
	if eval.Name() != "blueprint" {
		t.Errorf("expected Name() = 'blueprint', got %q", eval.Name())
	}
	if eval.Category() != "Blueprint" {
		t.Errorf("expected Category() = 'Blueprint', got %q", eval.Category())
	}
}

func TestBlueprintEvaluator_TaxonomyBOMAndFacts(t *testing.T) {
	tmpDir := t.TempDir()
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "civic.db"), "clan_test")
	if err != nil {
		t.Fatalf("failed creating civic db: %v", err)
	}
	defer civicDB.Close()

	// Stock storehouse: 15 spruce planks, 10 birch planks (= 25 #minecraft:planks), 10 cobblestone
	if err := civicDB.AddGranaryStock("minecraft:spruce_planks", 15, 100); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}
	if err := civicDB.AddGranaryStock("minecraft:birch_planks", 10, 100); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}
	if err := civicDB.AddGranaryStock("minecraft:cobblestone", 10, 100); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	// 1. Shelter Blueprint (needs 20 #minecraft:planks and 5 cobblestone -> AFFORDABLE)
	shelterBP := &blueprints.Blueprint{
		ID:      "frontier_cabin",
		Name:    "Frontier Cabin",
		Purpose: blueprints.PurposeShelter,
		BOM: map[string]int{
			"#minecraft:planks":     20,
			"minecraft:cobblestone": 5,
		},
	}

	// 2. Tavern Blueprint (needs 30 #minecraft:planks and 20 cobblestone -> DEFICIENT in cobblestone)
	tavernBP := &blueprints.Blueprint{
		ID:      "pioneer_tavern",
		Name:    "Pioneer Tavern",
		Purpose: blueprints.PurposeTavern,
		BOM: map[string]int{
			"#minecraft:planks":     20,
			"minecraft:cobblestone": 20,
		},
	}

	eval := NewBlueprintEvaluator(shelterBP, tavernBP)
	evalCtx := &EvaluatorContext{
		CivicDB: civicDB,
	}

	fg := NewFactGraph()
	// Mark unhoused
	fg.SetBool("shelter.complete", "shelter", false, "No shelter")
	fg.SetBool("shelter.has_bed", "shelter", false, "No bed")

	if err := eval.Evaluate(evalCtx, fg); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// Assert frontier_cabin is affordable
	if !fg.GetBool("blueprint.affordable:frontier_cabin") {
		t.Errorf("expected frontier_cabin to be affordable")
	}
	if fg.GetString("blueprint.missing:frontier_cabin") != "" {
		t.Errorf("expected no missing materials for frontier_cabin, got: %q",
			fg.GetString("blueprint.missing:frontier_cabin"))
	}

	// Assert pioneer_tavern is NOT affordable
	if fg.GetBool("blueprint.affordable:pioneer_tavern") {
		t.Errorf("expected pioneer_tavern to NOT be affordable")
	}
	missingTavern := fg.GetString("blueprint.missing:pioneer_tavern")
	if !strings.Contains(missingTavern, "minecraft:cobblestone:10") {
		t.Errorf("expected missing 10 cobblestone, got: %q", missingTavern)
	}

	// Assert feasible facts
	feasibleShelter := fg.GetString("blueprint.feasible:SHELTER")
	if !strings.Contains(feasibleShelter, "frontier_cabin") {
		t.Errorf("expected frontier_cabin in feasible:SHELTER, got: %q", feasibleShelter)
	}
	feasibleTavern := fg.GetString("blueprint.feasible:TAVERN")
	if feasibleTavern != "" {
		t.Errorf("expected empty feasible:TAVERN, got: %q", feasibleTavern)
	}

	// Assert recommended: since agent is unhoused, should recommend frontier_cabin
	rec := fg.GetString("blueprint.recommended")
	if rec != "frontier_cabin" {
		t.Errorf("expected recommended blueprint 'frontier_cabin', got %q", rec)
	}
}

func TestBlueprintEvaluator_CivicDeficitRecommendations(t *testing.T) {
	tmpDir := t.TempDir()
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "civic.db"), "clan_test")
	if err != nil {
		t.Fatalf("failed creating civic db: %v", err)
	}
	defer civicDB.Close()

	// Stock lots of materials so all blueprints are affordable
	_ = civicDB.AddGranaryStock("minecraft:oak_planks", 200, 100)
	_ = civicDB.AddGranaryStock("minecraft:cobblestone", 200, 100)
	_ = civicDB.AddGranaryStock("minecraft:iron_ingot", 100, 100)

	shelterBP := &blueprints.Blueprint{
		ID:      "bp_shelter",
		Name:    "Shelter",
		Purpose: blueprints.PurposeShelter,
		BOM:     map[string]int{"minecraft:oak_planks": 10},
	}
	tavernBP := &blueprints.Blueprint{
		ID:      "bp_tavern",
		Name:    "Tavern",
		Purpose: blueprints.PurposeTavern,
		BOM:     map[string]int{"minecraft:oak_planks": 20},
	}
	armoryBP := &blueprints.Blueprint{
		ID:      "bp_armory",
		Name:    "Armory",
		Purpose: blueprints.PurposeArmory,
		BOM:     map[string]int{"minecraft:cobblestone": 20},
	}
	storehouseBP := &blueprints.Blueprint{
		ID:      "bp_storehouse",
		Name:    "Storehouse",
		Purpose: blueprints.PurposeStorehouse,
		BOM:     map[string]int{"minecraft:oak_planks": 30},
	}

	eval := NewBlueprintEvaluator(shelterBP, tavernBP, armoryBP, storehouseBP)
	evalCtx := &EvaluatorContext{CivicDB: civicDB}

	// Scenario 1: Unhoused -> Recommend SHELTER
	fg1 := NewFactGraph()
	fg1.SetBool("shelter.complete", "shelter", false, "unhoused")
	_ = eval.Evaluate(evalCtx, fg1)
	if fg1.GetString("blueprint.recommended") != "bp_shelter" {
		t.Errorf("Scenario 1: expected bp_shelter, got %q", fg1.GetString("blueprint.recommended"))
	}

	// Scenario 2: Housed, No Tavern -> Recommend TAVERN
	fg2 := NewFactGraph()
	fg2.SetBool("shelter.complete", "shelter", true, "housed")
	fg2.SetBool("shelter.has_bed", "shelter", true, "has bed")
	_ = eval.Evaluate(evalCtx, fg2)
	if fg2.GetString("blueprint.recommended") != "bp_tavern" {
		t.Errorf("Scenario 2: expected bp_tavern, got %q", fg2.GetString("blueprint.recommended"))
	}

	// Scenario 3: Housed, Tavern Exists, Militia Under-Equipped -> Recommend ARMORY
	// Register Tavern Zone in CivicDB
	if err := civicDB.RegisterZone(&db.FunctionalZone{
		ZoneID:   "z-tavern",
		Name:     "Rusty Tavern",
		ZoneType: db.ZoneTypeTavern,
		MinX:     0, MaxX: 10,
		MinY: 64, MaxY: 70,
		MinZ: 0, MaxZ: 10,
	}); err != nil {
		t.Fatalf("RegisterZone tavern failed: %v", err)
	}
	fg3 := NewFactGraph()
	fg3.SetBool("shelter.complete", "shelter", true, "housed")
	fg3.SetBool("shelter.has_bed", "shelter", true, "has bed")
	fg3.SetBool("armory.sufficient", "Armory", false, "militia under-equipped")
	_ = eval.Evaluate(evalCtx, fg3)
	if fg3.GetString("blueprint.recommended") != "bp_armory" {
		t.Errorf("Scenario 3: expected bp_armory, got %q", fg3.GetString("blueprint.recommended"))
	}

	// Scenario 4: Housed, Tavern Exists, Armory Sufficient, No Storehouse -> Recommend STOREHOUSE
	fg4 := NewFactGraph()
	fg4.SetBool("shelter.complete", "shelter", true, "housed")
	fg4.SetBool("shelter.has_bed", "shelter", true, "has bed")
	fg4.SetBool("armory.sufficient", "Armory", true, "armory sufficient")
	_ = eval.Evaluate(evalCtx, fg4)
	if fg4.GetString("blueprint.recommended") != "bp_storehouse" {
		t.Errorf("Scenario 4: expected bp_storehouse, got %q", fg4.GetString("blueprint.recommended"))
	}
}

func TestBlueprintEvaluator_CarriedInventoryEvaluation(t *testing.T) {
	// Evaluator works when CivicDB is nil using carried TelemetryFrame inventory
	bp := &blueprints.Blueprint{
		ID:      "carried_cabin",
		Name:    "Carried Cabin",
		Purpose: blueprints.PurposeShelter,
		BOM: map[string]int{
			"minecraft:oak_planks": 10,
		},
	}
	eval := NewBlueprintEvaluator(bp)
	evalCtx := &EvaluatorContext{
		Frame: &civquestv1.TelemetryFrame{
			Status: &civquestv1.AgentStatus{
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:oak_planks", Count: 10},
				},
			},
		},
	}
	fg := NewFactGraph()
	_ = eval.Evaluate(evalCtx, fg)

	if !fg.GetBool("blueprint.affordable:carried_cabin") {
		t.Errorf("expected carried_cabin to be affordable from player inventory")
	}
}
