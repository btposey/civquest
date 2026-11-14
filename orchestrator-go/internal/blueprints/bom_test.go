package blueprints

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func TestBOM_ComputeSurplusAndMissing(t *testing.T) {
	required := map[string]int{
		"minecraft:cobblestone":              50,
		"minecraft:oak_planks":               30,
		"minecraft:oak_stairs[facing=north]": 10,
		"minecraft:torch":                    4,
	}

	inventory := map[string]int{
		"minecraft:cobblestone": 60,  // surplus 10
		"minecraft:oak_planks":  20,  // missing 10
		"minecraft:oak_stairs":  5,   // missing 5 (normalized matching)
		"minecraft:torch":       4,   // exact match
		"minecraft:wheat":       100, // surplus 100 (unrelated)
	}

	report := ComputeBOMReport(required, inventory)

	if report.IsSatisfied {
		t.Fatalf("expected report not to be satisfied due to missing oak_planks and oak_stairs")
	}

	// Total required: 50 + 30 + 10 + 4 = 94
	if report.TotalRequired != 94 {
		t.Errorf("expected total required 94, got %d", report.TotalRequired)
	}

	// Missing checks
	if report.Missing["minecraft:oak_planks"] != 10 {
		t.Errorf("expected 10 missing oak_planks, got %d", report.Missing["minecraft:oak_planks"])
	}
	if report.Missing["minecraft:oak_stairs"] != 5 {
		t.Errorf("expected 5 missing oak_stairs, got %d", report.Missing["minecraft:oak_stairs"])
	}
	if report.TotalMissing != 15 {
		t.Errorf("expected 15 total missing, got %d", report.TotalMissing)
	}

	// Surplus checks
	if report.Surplus["minecraft:cobblestone"] != 10 {
		t.Errorf("expected 10 surplus cobblestone, got %d", report.Surplus["minecraft:cobblestone"])
	}
	if report.Surplus["minecraft:wheat"] != 100 {
		t.Errorf("expected 100 surplus wheat, got %d", report.Surplus["minecraft:wheat"])
	}

	// Exact match check (torch)
	if report.Missing["minecraft:torch"] != 0 {
		t.Errorf("torch should have 0 missing")
	}
	if report.Surplus["minecraft:torch"] != 0 {
		t.Errorf("torch should have 0 surplus")
	}

	// Fulfillment percentage: fulfilled = 50 + 20 + 5 + 4 = 79 / 94 = 84.04%
	if report.FulfillmentPercent < 84.0 || report.FulfillmentPercent > 85.0 {
		t.Errorf("unexpected fulfillment percent: %.2f", report.FulfillmentPercent)
	}
}

func TestBOM_SatisfiedWhenStockSufficient(t *testing.T) {
	required := map[string]int{
		"minecraft:cobblestone": 20,
		"minecraft:oak_planks":  10,
	}

	inventory := map[string]int{
		"minecraft:cobblestone": 25,
		"minecraft:oak_planks":  10,
	}

	report := ComputeBOMReport(required, inventory)
	if !report.IsSatisfied {
		t.Errorf("expected report to be satisfied")
	}
	if report.TotalMissing != 0 {
		t.Errorf("expected 0 missing, got %d", report.TotalMissing)
	}
	if report.FulfillmentPercent != 100.0 {
		t.Errorf("expected 100.0 percent fulfillment, got %.2f", report.FulfillmentPercent)
	}
}

func TestBOM_GranaryLiveEvaluation(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_granary_bom.db")

	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_bom_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	// Stock granary: 100 cobblestone, 20 reserved cobblestone -> 80 available
	if err := civicDB.AddGranaryStockContext(ctx, "minecraft:cobblestone", 100, 1); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}
	if err := civicDB.ReserveGranaryStockContext(ctx, "minecraft:cobblestone", 20, 2); err != nil {
		t.Fatalf("ReserveGranaryStock failed: %v", err)
	}

	// Stock 15 oak_planks (unreserved)
	if err := civicDB.AddGranaryStockContext(ctx, "minecraft:oak_planks", 15, 3); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}

	// Required: 50 cobblestone, 25 oak_planks
	required := map[string]int{
		"minecraft:cobblestone": 50,
		"minecraft:oak_planks":  25,
	}

	report, err := ComputeBOMAgainstGranary(ctx, required, civicDB)
	if err != nil {
		t.Fatalf("ComputeBOMAgainstGranary failed: %v", err)
	}

	// Available cobblestone should be 80
	if report.Available["minecraft:cobblestone"] != 80 {
		t.Errorf("expected 80 available cobblestone (100 - 20 reserved), got %d", report.Available["minecraft:cobblestone"])
	}
	// Surplus cobblestone should be 30
	if report.Surplus["minecraft:cobblestone"] != 30 {
		t.Errorf("expected 30 surplus cobblestone, got %d", report.Surplus["minecraft:cobblestone"])
	}

	// Missing oak_planks should be 10 (25 required - 15 available)
	if report.Missing["minecraft:oak_planks"] != 10 {
		t.Errorf("expected 10 missing oak_planks, got %d", report.Missing["minecraft:oak_planks"])
	}

	if report.IsSatisfied {
		t.Errorf("expected report not to be satisfied")
	}

	// Deposit 10 oak_planks into granary
	if err := civicDB.AddGranaryStockContext(ctx, "minecraft:oak_planks", 10, 4); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}

	report2, err := ComputeBOMAgainstGranary(ctx, required, civicDB)
	if err != nil {
		t.Fatalf("ComputeBOMAgainstGranary failed: %v", err)
	}
	if !report2.IsSatisfied {
		t.Errorf("expected report2 to be satisfied after stocking granary")
	}
}

func TestBOM_QuotasAndPrestigeBounties(t *testing.T) {
	projectID := "proj-tavern-1"
	missing := map[string]int{
		"minecraft:oak_planks":  30,
		"minecraft:cobblestone": 50,
		"minecraft:iron_ingot":  5,
	}

	settlers := []string{"agent-alice", "agent-bob"}

	quotas := GenerateGatheringQuotas(projectID, missing, settlers)
	if len(quotas) != 3 {
		t.Fatalf("expected 3 quotas, got %d", len(quotas))
	}

	// Quotas should be sorted and assigned round-robin
	for _, q := range quotas {
		if q.ProjectID != projectID {
			t.Errorf("expected projectID %s, got %s", projectID, q.ProjectID)
		}
		if q.TargetQuantity <= 0 {
			t.Errorf("expected positive target quantity, got %d", q.TargetQuantity)
		}
		if q.AssignedAgentID != "agent-alice" && q.AssignedAgentID != "agent-bob" {
			t.Errorf("unexpected assigned agent: %s", q.AssignedAgentID)
		}
		if q.Priority < 1 || q.Priority > 10 {
			t.Errorf("priority out of range: %d", q.Priority)
		}
	}

	bounties := GeneratePrestigeBounties(projectID, missing)
	if len(bounties) != 3 {
		t.Fatalf("expected 3 bounties, got %d", len(bounties))
	}

	for _, b := range bounties {
		if b.ProjectID != projectID {
			t.Errorf("expected projectID %s, got %s", projectID, b.ProjectID)
		}
		if b.PrestigeReward <= 0.0 {
			t.Errorf("expected positive prestige reward, got %f", b.PrestigeReward)
		}
		if b.Description == "" {
			t.Errorf("expected non-empty bounty description")
		}
	}
}
