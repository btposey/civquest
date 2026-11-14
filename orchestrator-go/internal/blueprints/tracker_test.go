package blueprints

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func TestProjectTracker_StateTransitionsAndMaterialGating(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_projects.db")

	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_tracker_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	tracker := NewProjectTracker(civicDB)

	// 1. Generate test blueprint
	bp := GenerateTemplate("house", "pioneer")
	bom := bp.CalculateBOM()
	if len(bom) == 0 {
		t.Fatalf("blueprint BOM empty")
	}

	// 2. Register project
	projectID := "proj-house-alpha"
	req := RegisterProjectRequest{
		ProjectID:   projectID,
		BlueprintID: "blueprint:pioneer_house",
		AnchorX:     100,
		AnchorY:     64,
		AnchorZ:     -50,
		Quadrant:    db.QuadrantNW,
		WorldTick:   100,
	}

	p, err := tracker.RegisterProject(ctx, req)
	if err != nil {
		t.Fatalf("RegisterProject failed: %v", err)
	}

	if p.Status != StateSurveying {
		t.Errorf("expected initial state SURVEYING, got %s", p.Status)
	}

	// 3. Attempt direct transition to BUILDING with EMPTY granary -> MUST FAIL with ErrInsufficientMaterials
	_, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 105, bp)
	if !errors.Is(err, ErrInsufficientMaterials) {
		t.Fatalf("expected ErrInsufficientMaterials, got %v", err)
	}

	// Status should still be SURVEYING
	curProj, err := tracker.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if curProj.Status != StateSurveying {
		t.Errorf("status should remain SURVEYING, got %s", curProj.Status)
	}

	// 4. Transition to AWAITING_BOM -> should succeed
	p, err = tracker.TransitionProject(ctx, projectID, StateAwaitingBOM, 110, bp)
	if err != nil {
		t.Fatalf("transition to AWAITING_BOM failed: %v", err)
	}
	if p.Status != StateAwaitingBOM {
		t.Errorf("expected state AWAITING_BOM, got %s", p.Status)
	}

	// 5. Transition to CLEARING -> should succeed
	p, err = tracker.TransitionProject(ctx, projectID, StateClearing, 120, bp)
	if err != nil {
		t.Fatalf("transition to CLEARING failed: %v", err)
	}
	if p.Status != StateClearing {
		t.Errorf("expected state CLEARING, got %s", p.Status)
	}

	// 6. Attempt transition to BUILDING from CLEARING without materials -> MUST FAIL
	_, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 125, bp)
	if !errors.Is(err, ErrInsufficientMaterials) {
		t.Fatalf("expected ErrInsufficientMaterials, got %v", err)
	}

	// 7. Stock granary with all required materials
	for stateStr, count := range bom {
		itemID := NormalizeItemID(stateStr)
		if err := civicDB.AddGranaryStockContext(ctx, itemID, count, 130); err != nil {
			t.Fatalf("AddGranaryStock failed for %s: %v", itemID, err)
		}
	}

	// 8. Now transition to BUILDING -> MUST SUCCEED
	p, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 140, bp)
	if err != nil {
		t.Fatalf("transition to BUILDING with full materials failed: %v", err)
	}
	if p.Status != StateBuilding {
		t.Errorf("expected state BUILDING, got %s", p.Status)
	}

	// Verify materials were reserved in granary
	for stateStr, count := range bom {
		itemID := NormalizeItemID(stateStr)
		stock, err := civicDB.GetItemStockContext(ctx, itemID)
		if err != nil {
			t.Fatalf("GetItemStock failed for %s: %v", itemID, err)
		}
		if stock.ReservedQuantity < count {
			t.Errorf("expected at least %d reserved for %s, got %d", count, itemID, stock.ReservedQuantity)
		}
	}

	// 9. Update construction progress
	if err := tracker.UpdateProgress(ctx, projectID, 50.0, 150); err != nil {
		t.Fatalf("UpdateProgress failed: %v", err)
	}
	p, _ = tracker.GetProject(ctx, projectID)
	if p.ProgressPercent != 50.0 {
		t.Errorf("expected progress 50.0, got %f", p.ProgressPercent)
	}

	// 10. Transition to COMPLETED -> consumes reserved stock and sets progress 100%
	p, err = tracker.TransitionProject(ctx, projectID, StateCompleted, 160, bp)
	if err != nil {
		t.Fatalf("transition to COMPLETED failed: %v", err)
	}
	if p.Status != StateCompleted {
		t.Errorf("expected state COMPLETED, got %s", p.Status)
	}
	if p.ProgressPercent != 100.0 {
		t.Errorf("expected progress 100.0, got %f", p.ProgressPercent)
	}

	// Verify stock was consumed (reserved should now be 0, quantity reduced)
	for stateStr := range bom {
		itemID := NormalizeItemID(stateStr)
		stock, err := civicDB.GetItemStockContext(ctx, itemID)
		if err != nil {
			t.Fatalf("GetItemStock failed for %s: %v", itemID, err)
		}
		if stock.ReservedQuantity != 0 {
			t.Errorf("expected 0 reserved for %s after completion, got %d", itemID, stock.ReservedQuantity)
		}
	}

	// 11. Cannot transition from terminal state COMPLETED
	_, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 170, bp)
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("expected ErrInvalidStateTransition from COMPLETED, got %v", err)
	}
}

func TestProjectTracker_AbandonReleasesReservedStock(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "civic_abandon.db")

	civicDB, err := db.OpenCivicRegistryDB(dbPath, "clan_abandon_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	tracker := NewProjectTracker(civicDB)

	bp := GenerateTemplate("tavern", "pioneer")
	bom := bp.CalculateBOM()

	// Stock granary
	for stateStr, count := range bom {
		itemID := NormalizeItemID(stateStr)
		_ = civicDB.AddGranaryStockContext(ctx, itemID, count, 10)
	}

	projectID := "proj-tavern-abandon"
	_, err = tracker.RegisterProject(ctx, RegisterProjectRequest{
		ProjectID:   projectID,
		BlueprintID: "blueprint:tavern",
		Quadrant:    db.QuadrantNE,
		WorldTick:   20,
	})
	if err != nil {
		t.Fatalf("RegisterProject failed: %v", err)
	}

	// Transition directly to BUILDING (materials are stocked)
	_, err = tracker.TransitionProject(ctx, projectID, StateBuilding, 30, bp)
	if err != nil {
		t.Fatalf("transition to BUILDING failed: %v", err)
	}

	// Now abandon the project
	p, err := tracker.TransitionProject(ctx, projectID, StateAbandoned, 40, bp)
	if err != nil {
		t.Fatalf("transition to ABANDONED failed: %v", err)
	}
	if p.Status != StateAbandoned {
		t.Errorf("expected state ABANDONED, got %s", p.Status)
	}

	// Verify stock reservations were released back to available
	for stateStr := range bom {
		itemID := NormalizeItemID(stateStr)
		stock, err := civicDB.GetItemStockContext(ctx, itemID)
		if err != nil {
			t.Fatalf("GetItemStock failed for %s: %v", itemID, err)
		}
		if stock.ReservedQuantity != 0 {
			t.Errorf("expected 0 reserved for %s after abandonment, got %d", itemID, stock.ReservedQuantity)
		}
	}
}
