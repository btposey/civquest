package db

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestCivicRegistryDB_LifecycleAndMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_civic_test", "civic_registry.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_civic_test")
	if err != nil {
		t.Fatalf("failed to open civic registry database: %v", err)
	}
	defer civicDB.Close()

	if civicDB.ClanID() != "clan_civic_test" {
		t.Errorf("expected clan ID clan_civic_test, got %q", civicDB.ClanID())
	}
	if civicDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, civicDB.Path())
	}
	if civicDB.DB() == nil {
		t.Fatal("expected non-nil *sql.DB handle")
	}

	ctx := context.Background()

	// Verify required tables exist
	var count int
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('settlement_granary', 'construction_projects', 'functional_zones');`
	if err := civicDB.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("failed to query table count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 tables, found %d", count)
	}
}

func TestCivicRegistryDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	civicDB, err := OpenCivicRegistryDBForClan(resolver, "clan_pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan failed: %v", err)
	}
	defer civicDB.Close()

	expectedPath := resolver.ClanCivicRegistryDBPath("clan_pioneers")
	if civicDB.Path() != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, civicDB.Path())
	}

	if _, err := OpenCivicRegistryDBForClan(resolver, "invalid/clan"); err == nil {
		t.Error("expected error for invalid clan ID")
	}
}

func TestCivicRegistryDB_GranaryStockOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_granary")
	if err != nil {
		t.Fatalf("failed to open civic database: %v", err)
	}
	defer civicDB.Close()

	// 1. Initial stock is nil
	stock, err := civicDB.GetItemStock("minecraft:wheat")
	if err != nil {
		t.Fatalf("GetItemStock failed: %v", err)
	}
	if stock != nil {
		t.Errorf("expected nil stock for wheat, got %+v", stock)
	}

	// 2. Add stock
	if err := civicDB.AddGranaryStock("minecraft:wheat", 100, 1000); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}

	stock, err = civicDB.GetItemStock("minecraft:wheat")
	if err != nil {
		t.Fatalf("GetItemStock failed: %v", err)
	}
	if stock == nil || stock.Quantity != 100 || stock.ReservedQuantity != 0 || stock.Available() != 100 {
		t.Fatalf("unexpected stock state: %+v", stock)
	}

	// 3. Accumulate stock
	if err := civicDB.AddGranaryStock("minecraft:wheat", 50, 1050); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}
	stock, err = civicDB.GetItemStock("minecraft:wheat")
	if err != nil || stock.Quantity != 150 {
		t.Fatalf("expected 150 quantity, got %v (err: %v)", stock, err)
	}

	// 4. Reserve stock
	if err := civicDB.ReserveGranaryStock("minecraft:wheat", 40, 1100); err != nil {
		t.Fatalf("ReserveGranaryStock failed: %v", err)
	}
	stock, _ = civicDB.GetItemStock("minecraft:wheat")
	if stock.ReservedQuantity != 40 || stock.Available() != 110 {
		t.Fatalf("expected 40 reserved, 110 available; got %+v", stock)
	}

	// 5. Fail to over-reserve (available is 110, requesting 120)
	if err := civicDB.ReserveGranaryStock("minecraft:wheat", 120, 1150); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock on over-reserve, got %v", err)
	}

	// 6. Release reserved stock
	if err := civicDB.ReleaseGranaryStock("minecraft:wheat", 15, 1200); err != nil {
		t.Fatalf("ReleaseGranaryStock failed: %v", err)
	}
	stock, _ = civicDB.GetItemStock("minecraft:wheat")
	if stock.ReservedQuantity != 25 || stock.Available() != 125 {
		t.Fatalf("expected 25 reserved, 125 available; got %+v", stock)
	}

	// 7. Fail to over-release (only 25 reserved, releasing 30)
	if err := civicDB.ReleaseGranaryStock("minecraft:wheat", 30, 1250); err == nil {
		t.Error("expected error releasing more than reserved quantity")
	}

	// 8. Consume available stock (available = 125, consume 50)
	if err := civicDB.ConsumeGranaryStock("minecraft:wheat", 50, 1300); err != nil {
		t.Fatalf("ConsumeGranaryStock failed: %v", err)
	}
	stock, _ = civicDB.GetItemStock("minecraft:wheat")
	if stock.Quantity != 100 || stock.ReservedQuantity != 25 || stock.Available() != 75 {
		t.Fatalf("expected qty 100, res 25, avail 75; got %+v", stock)
	}

	// 9. Fail to consume more than available (available = 75, try to consume 80)
	if err := civicDB.ConsumeGranaryStock("minecraft:wheat", 80, 1350); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock, got %v", err)
	}

	// 10. List inventory
	if err := civicDB.AddGranaryStock("minecraft:baked_potato", 64, 1400); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}
	inventory, err := civicDB.GetGranaryInventory()
	if err != nil {
		t.Fatalf("GetGranaryInventory failed: %v", err)
	}
	if len(inventory) != 2 {
		t.Fatalf("expected 2 inventory entries, got %d", len(inventory))
	}
	if inventory[0].ItemID != "minecraft:baked_potato" || inventory[1].ItemID != "minecraft:wheat" {
		t.Errorf("unexpected inventory order: %+v", inventory)
	}

	// 11. Validation errors
	if err := civicDB.AddGranaryStock("", 10, 100); err == nil {
		t.Error("expected error for empty item_id")
	}
	if err := civicDB.AddGranaryStock("minecraft:bread", 0, 100); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("expected ErrInvalidQuantity for 0 quantity, got %v", err)
	}
	if err := civicDB.AddGranaryStock("minecraft:bread", -5, 100); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("expected ErrInvalidQuantity for negative quantity, got %v", err)
	}
	if err := civicDB.ConsumeGranaryStock("minecraft:bread", 10, 100); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock for unstocked item, got %v", err)
	}
}

func TestCivicRegistryDB_ConcurrentGranaryOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent_granary.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_concurrent")
	if err != nil {
		t.Fatalf("failed to open civic database: %v", err)
	}
	defer civicDB.Close()

	if err := civicDB.AddGranaryStock("minecraft:bread", 1000, 1); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			tick := int64(10 + idx)
			// Each worker adds 10, reserves 5, releases 5, consumes 5
			_ = civicDB.AddGranaryStock("minecraft:bread", 10, tick)
			_ = civicDB.ReserveGranaryStock("minecraft:bread", 5, tick)
			_ = civicDB.ReleaseGranaryStock("minecraft:bread", 5, tick)
			_ = civicDB.ConsumeGranaryStock("minecraft:bread", 5, tick)
		}(i)
	}

	wg.Wait()

	stock, err := civicDB.GetItemStock("minecraft:bread")
	if err != nil {
		t.Fatalf("GetItemStock failed: %v", err)
	}
	// Initial 1000 + (20 * 10) - (20 * 5) = 1100
	expectedQty := 1000 + (goroutines * 10) - (goroutines * 5)
	if stock.Quantity != expectedQty {
		t.Errorf("expected quantity %d, got %d", expectedQty, stock.Quantity)
	}
	if stock.ReservedQuantity != 0 {
		t.Errorf("expected reserved quantity 0, got %d", stock.ReservedQuantity)
	}
}

func TestCivicRegistryDB_ConstructionProjects(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "projects.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_build")
	if err != nil {
		t.Fatalf("failed to open civic database: %v", err)
	}
	defer civicDB.Close()

	// 1. Create project
	proj := &ConstructionProject{
		ProjectID:             "proj-watchtower-1",
		BlueprintID:           "blueprint:stone_watchtower",
		Status:                ProjectStatusPlanned,
		AnchorX:               120,
		AnchorY:               64,
		AnchorZ:               -250,
		Quadrant:              QuadrantNE,
		ProgressPercent:       0.0,
		MaterialsReservedJSON: `{"minecraft:cobblestone": 128}`,
		CreatedTick:           500,
		UpdatedTick:           500,
	}

	if err := civicDB.CreateProject(proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	// 2. Get project
	got, err := civicDB.GetProject("proj-watchtower-1")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil project")
	}
	if got.BlueprintID != "blueprint:stone_watchtower" || got.Quadrant != QuadrantNE || got.AnchorX != 120 {
		t.Errorf("unexpected project data: %+v", got)
	}

	// 3. Update status & progress
	if err := civicDB.UpdateProjectStatus("proj-watchtower-1", ProjectStatusInProgress, 600); err != nil {
		t.Fatalf("UpdateProjectStatus failed: %v", err)
	}
	if err := civicDB.UpdateProjectProgress("proj-watchtower-1", 45.5, 650); err != nil {
		t.Fatalf("UpdateProjectProgress failed: %v", err)
	}

	got, err = civicDB.GetProject("proj-watchtower-1")
	if err != nil || got.Status != ProjectStatusInProgress || got.ProgressPercent != 45.5 || got.UpdatedTick != 650 {
		t.Fatalf("expected in-progress with 45.5%% at tick 650; got %+v", got)
	}

	// 4. Invalid progress
	if err := civicDB.UpdateProjectProgress("proj-watchtower-1", 105.0, 700); err == nil {
		t.Error("expected error for progress > 100.0")
	}
	if err := civicDB.UpdateProjectProgress("proj-watchtower-1", -1.0, 700); err == nil {
		t.Error("expected error for progress < 0.0")
	}

	// 5. Update nonexistent project returns ErrNotFound
	if err := civicDB.UpdateProjectStatus("nonexistent", ProjectStatusCompleted, 800); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent project, got %v", err)
	}
	if err := civicDB.UpdateProjectProgress("nonexistent", 50.0, 800); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent project, got %v", err)
	}

	// 6. List projects
	proj2 := &ConstructionProject{
		ProjectID:   "proj-granary-1",
		BlueprintID: "blueprint:timber_granary",
		Quadrant:    QuadrantSW,
		CreatedTick: 600,
	}
	if err := civicDB.CreateProject(proj2); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	list, err := civicDB.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}
	if list[0].ProjectID != "proj-watchtower-1" || list[1].ProjectID != "proj-granary-1" {
		t.Errorf("unexpected project list ordering: %+v", list)
	}
}

func TestCivicRegistryDB_FunctionalZones(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "zones.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_zones")
	if err != nil {
		t.Fatalf("failed to open civic database: %v", err)
	}
	defer civicDB.Close()

	// 1. Invalid coordinates (min > max)
	badZone := &FunctionalZone{
		ZoneID:   "bad-zone",
		ZoneType: ZoneTypeFarm,
		MinX:     100,
		MaxX:     50, // invalid
		MinY:     64,
		MaxY:     70,
		MinZ:     10,
		MaxZ:     20,
		Name:     "Bad Zone",
	}
	if err := civicDB.RegisterZone(badZone); !errors.Is(err, ErrInvalidCoordinates) {
		t.Errorf("expected ErrInvalidCoordinates, got %v", err)
	}

	// 2. Register valid zones
	farmZone := &FunctionalZone{
		ZoneID:       "zone-farm-central",
		ZoneType:     ZoneTypeFarm,
		MinX:         -50,
		MinY:         60,
		MinZ:         -50,
		MaxX:         50,
		MaxY:         80,
		MaxZ:         50,
		OwnerAgentID: "agent-agronomist-1",
		Name:         "Central Crop Fields",
		CreatedTick:  100,
	}
	if err := civicDB.RegisterZone(farmZone); err != nil {
		t.Fatalf("RegisterZone failed: %v", err)
	}

	workshopZone := &FunctionalZone{
		ZoneID:       "zone-blacksmith-1",
		ZoneType:     ZoneTypeWorkshop,
		MinX:         100,
		MinY:         60,
		MinZ:         100,
		MaxX:         120,
		MaxY:         75,
		MaxZ:         120,
		OwnerAgentID: "agent-smith-1",
		Name:         "Ironworks Foundry",
		CreatedTick:  200,
	}
	if err := civicDB.RegisterZone(workshopZone); err != nil {
		t.Fatalf("RegisterZone failed: %v", err)
	}

	// 3. Get zone
	got, err := civicDB.GetZone("zone-farm-central")
	if err != nil {
		t.Fatalf("GetZone failed: %v", err)
	}
	if got == nil || got.ZoneType != ZoneTypeFarm || got.OwnerAgentID != "agent-agronomist-1" {
		t.Fatalf("unexpected zone data: %+v", got)
	}

	// Nonexistent zone
	none, err := civicDB.GetZone("nonexistent")
	if err != nil || none != nil {
		t.Errorf("expected nil, nil for nonexistent zone; got %v, %v", none, err)
	}

	// 4. FindZoneAt
	found, err := civicDB.FindZoneAt(0, 64, 0)
	if err != nil {
		t.Fatalf("FindZoneAt failed: %v", err)
	}
	if found == nil || found.ZoneID != "zone-farm-central" {
		t.Errorf("expected zone-farm-central at (0,64,0), got %+v", found)
	}

	foundSmith, err := civicDB.FindZoneAt(110, 65, 115)
	if err != nil {
		t.Fatalf("FindZoneAt failed: %v", err)
	}
	if foundSmith == nil || foundSmith.ZoneID != "zone-blacksmith-1" {
		t.Errorf("expected zone-blacksmith-1 at (110,65,115), got %+v", foundSmith)
	}

	// Outside any zone
	outside, err := civicDB.FindZoneAt(500, 64, 500)
	if err != nil {
		t.Fatalf("FindZoneAt failed: %v", err)
	}
	if outside != nil {
		t.Errorf("expected nil for outside coordinate, got %+v", outside)
	}

	// 5. List zones
	zones, err := civicDB.ListZones()
	if err != nil {
		t.Fatalf("ListZones failed: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(zones))
	}
}

func TestCivicRegistryDB_ClosedOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "closed.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_closed")
	if err != nil {
		t.Fatalf("failed to open civic database: %v", err)
	}

	if err := civicDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Operations on closed DB return ErrClosed
	if err := civicDB.AddGranaryStock("minecraft:wheat", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on AddGranaryStock, got %v", err)
	}
	if err := civicDB.ConsumeGranaryStock("minecraft:wheat", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ConsumeGranaryStock, got %v", err)
	}
	if err := civicDB.ReserveGranaryStock("minecraft:wheat", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ReserveGranaryStock, got %v", err)
	}
	if err := civicDB.ReleaseGranaryStock("minecraft:wheat", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ReleaseGranaryStock, got %v", err)
	}
	if _, err := civicDB.GetItemStock("minecraft:wheat"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetItemStock, got %v", err)
	}
	if _, err := civicDB.GetGranaryInventory(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetGranaryInventory, got %v", err)
	}
	if err := civicDB.CreateProject(&ConstructionProject{ProjectID: "p", BlueprintID: "b", Quadrant: QuadrantNW}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on CreateProject, got %v", err)
	}
	if err := civicDB.RegisterZone(&FunctionalZone{ZoneID: "z", ZoneType: "T", Name: "N", MinX: 0, MaxX: 10}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on RegisterZone, got %v", err)
	}
	if err := civicDB.ConsumeReservedGranaryStock("minecraft:wheat", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ConsumeReservedGranaryStock, got %v", err)
	}
	if _, err := civicDB.FindZonesAt(0, 0, 0); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on FindZonesAt, got %v", err)
	}
	if _, err := civicDB.FindZoneAtByType(0, 0, 0, ZoneTypeGranary); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on FindZoneAtByType, got %v", err)
	}
}

func TestCivicRegistryDB_Remediations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic_remediations.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_remediate")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	// 1. Issue 2: Atomic ConsumeReservedGranaryStock
	if err := civicDB.AddGranaryStock("minecraft:oak_log", 100, 10); err != nil {
		t.Fatalf("AddGranaryStock failed: %v", err)
	}
	if err := civicDB.ReserveGranaryStock("minecraft:oak_log", 40, 20); err != nil {
		t.Fatalf("ReserveGranaryStock failed: %v", err)
	}
	// Verify stock before consumption
	s, err := civicDB.GetItemStock("minecraft:oak_log")
	if err != nil || s.Quantity != 100 || s.ReservedQuantity != 40 {
		t.Fatalf("unexpected stock before consume reserved: %+v", s)
	}

	// Over-consumption of reserved stock (reserved is 40, requesting 45) -> ErrInsufficientStock
	if err := civicDB.ConsumeReservedGranaryStock("minecraft:oak_log", 45, 30); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock for over-consumption of reserved stock, got: %v", err)
	}

	// Consume non-existent item -> ErrInsufficientStock
	if err := civicDB.ConsumeReservedGranaryStock("minecraft:diamond", 5, 30); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock for nonexistent item, got: %v", err)
	}

	// Invalid quantity (<= 0)
	if err := civicDB.ConsumeReservedGranaryStock("minecraft:oak_log", 0, 30); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("expected ErrInvalidQuantity for 0 quantity, got: %v", err)
	}

	// Valid consume reserved (30 logs)
	if err := civicDB.ConsumeReservedGranaryStock("minecraft:oak_log", 30, 40); err != nil {
		t.Fatalf("ConsumeReservedGranaryStock failed: %v", err)
	}
	s, err = civicDB.GetItemStock("minecraft:oak_log")
	if err != nil || s.Quantity != 70 || s.ReservedQuantity != 10 {
		t.Fatalf("expected quantity 70, reserved 10; got %+v", s)
	}

	// 2. Issue 6: ReleaseGranaryStock sentinel errors
	// Over-release -> ErrInsufficientStock
	if err := civicDB.ReleaseGranaryStock("minecraft:oak_log", 20, 50); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock on over-release, got: %v", err)
	}
	// Nonexistent item release -> ErrInsufficientStock
	if err := civicDB.ReleaseGranaryStock("minecraft:emerald", 1, 50); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock on nonexistent item release, got: %v", err)
	}

	// 3. Issue 1 & Issue 4: Projects NaN/Inf & Enum validations
	// NaN / Inf progress in CreateProject
	nanProj := &ConstructionProject{
		ProjectID:       "proj-nan",
		BlueprintID:     "bp-1",
		Quadrant:        QuadrantNW,
		ProgressPercent: math.NaN(),
	}
	if err := civicDB.CreateProject(nanProj); err == nil {
		t.Error("expected error for NaN progress_percent in CreateProject")
	}

	infProj := &ConstructionProject{
		ProjectID:       "proj-inf",
		BlueprintID:     "bp-1",
		Quadrant:        QuadrantNW,
		ProgressPercent: math.Inf(1),
	}
	if err := civicDB.CreateProject(infProj); err == nil {
		t.Error("expected error for Inf progress_percent in CreateProject")
	}

	// Invalid Quadrant
	badQuadProj := &ConstructionProject{
		ProjectID:   "proj-badquad",
		BlueprintID: "bp-1",
		Quadrant:    "NORTH",
	}
	if err := civicDB.CreateProject(badQuadProj); err == nil {
		t.Error("expected error for invalid quadrant")
	}

	// Valid QuadrantAll
	validAllProj := &ConstructionProject{
		ProjectID:   "proj-allquad",
		BlueprintID: "bp-all",
		Quadrant:    QuadrantAll,
	}
	if err := civicDB.CreateProject(validAllProj); err != nil {
		t.Fatalf("expected QuadrantAll to be valid, got: %v", err)
	}

	// Invalid ProjectStatus
	badStatusProj := &ConstructionProject{
		ProjectID:   "proj-badstatus",
		BlueprintID: "bp-1",
		Quadrant:    QuadrantSE,
		Status:      "INVALID_STATUS",
	}
	if err := civicDB.CreateProject(badStatusProj); err == nil {
		t.Error("expected error for invalid project status")
	}

	// NaN / Inf progress in UpdateProjectProgress
	if err := civicDB.UpdateProjectProgress("proj-allquad", math.NaN(), 100); err == nil {
		t.Error("expected error for NaN in UpdateProjectProgress")
	}
	if err := civicDB.UpdateProjectProgress("proj-allquad", math.Inf(1), 100); err == nil {
		t.Error("expected error for Inf in UpdateProjectProgress")
	}

	// Issue 6: ErrDuplicateRecord on duplicate CreateProject
	if err := civicDB.CreateProject(validAllProj); !errors.Is(err, ErrDuplicateRecord) {
		t.Errorf("expected ErrDuplicateRecord on duplicate CreateProject, got: %v", err)
	}

	// 4. Issue 4: ZoneType validation & Issue 6: ErrDuplicateRecord on RegisterZone
	badZoneType := &FunctionalZone{
		ZoneID:   "z-bad-type",
		ZoneType: "DISCO_FLOOR",
		Name:     "Fun Zone",
		MinX:     0, MaxX: 10,
		MinY: 0, MaxY: 10,
		MinZ: 0, MaxZ: 10,
	}
	if err := civicDB.RegisterZone(badZoneType); err == nil {
		t.Error("expected error for invalid zone type")
	}

	validHousingZone := &FunctionalZone{
		ZoneID:   "z-housing-1",
		ZoneType: ZoneTypeHousing,
		Name:     "Residential Quarters",
		MinX:     -20, MaxX: 20,
		MinY: 60, MaxY: 80,
		MinZ: -20, MaxZ: 20,
	}
	if err := civicDB.RegisterZone(validHousingZone); err != nil {
		t.Fatalf("expected ZoneTypeHousing to be valid, got: %v", err)
	}

	// Duplicate RegisterZone -> ErrDuplicateRecord
	if err := civicDB.RegisterZone(validHousingZone); !errors.Is(err, ErrDuplicateRecord) {
		t.Errorf("expected ErrDuplicateRecord on duplicate RegisterZone, got: %v", err)
	}

	// 5. Issue 5: Deterministic Spatial Zones ordering & queries
	// Create an outer big settlement zone (vol: 100x100x100)
	outerZone := &FunctionalZone{
		ZoneID:   "z-settlement-outer",
		ZoneType: ZoneTypeGranary,
		Name:     "Greater Settlement",
		MinX:     -50, MaxX: 50,
		MinY: 50, MaxY: 150,
		MinZ: -50, MaxZ: 50,
		CreatedTick: 10,
	}
	// Create an inner specific tavern zone (vol: 10x10x10)
	innerTavern := &FunctionalZone{
		ZoneID:   "z-tavern-inner",
		ZoneType: ZoneTypeTavern,
		Name:     "Cozy Inn",
		MinX:     -5, MaxX: 5,
		MinY: 60, MaxY: 70,
		MinZ: -5, MaxZ: 5,
		CreatedTick: 20,
	}
	// Create an inner specific granary zone (vol: 4x4x4)
	innerGranary := &FunctionalZone{
		ZoneID:   "z-granary-core",
		ZoneType: ZoneTypeGranary,
		Name:     "Core Granary Silo",
		MinX:     -2, MaxX: 2,
		MinY: 62, MaxY: 66,
		MinZ: -2, MaxZ: 2,
		CreatedTick: 30,
	}
	if err := civicDB.RegisterZone(outerZone); err != nil {
		t.Fatalf("RegisterZone outer failed: %v", err)
	}
	if err := civicDB.RegisterZone(innerTavern); err != nil {
		t.Fatalf("RegisterZone inner tavern failed: %v", err)
	}
	if err := civicDB.RegisterZone(innerGranary); err != nil {
		t.Fatalf("RegisterZone inner granary failed: %v", err)
	}

	// Point (0, 64, 0) is inside all 3 zones:
	// Vol innerGranary = 4*4*4 = 64
	// Vol innerTavern = 10*10*10 = 1000
	// Vol outerZone = 100*100*100 = 1000000
	// FindZoneAt must return smallest volume first (innerGranary)!
	mostSpecific, err := civicDB.FindZoneAt(0, 64, 0)
	if err != nil {
		t.Fatalf("FindZoneAt failed: %v", err)
	}
	if mostSpecific == nil || mostSpecific.ZoneID != "z-granary-core" {
		t.Errorf("expected most specific zone 'z-granary-core', got %+v", mostSpecific)
	}

	// FindZonesAt must return all 4 matching zones (housing-1 is 40x20x40 = 32000) ordered by volume ASC
	allZonesAtCoord, err := civicDB.FindZonesAt(0, 64, 0)
	if err != nil {
		t.Fatalf("FindZonesAt failed: %v", err)
	}
	if len(allZonesAtCoord) != 4 {
		t.Fatalf("expected 4 zones containing (0,64,0), got %d", len(allZonesAtCoord))
	}
	expectedOrder := []string{"z-granary-core", "z-tavern-inner", "z-housing-1", "z-settlement-outer"}
	for i, expID := range expectedOrder {
		if allZonesAtCoord[i].ZoneID != expID {
			t.Errorf("at index %d: expected %s, got %s", i, expID, allZonesAtCoord[i].ZoneID)
		}
	}

	// FindZoneAtByType: find TAVERN at (0,64,0)
	foundTavern, err := civicDB.FindZoneAtByType(0, 64, 0, ZoneTypeTavern)
	if err != nil {
		t.Fatalf("FindZoneAtByType failed: %v", err)
	}
	if foundTavern == nil || foundTavern.ZoneID != "z-tavern-inner" {
		t.Errorf("expected 'z-tavern-inner', got %+v", foundTavern)
	}

	// FindZoneAtByType: find CEMETERY at (0,64,0) -> nil, nil
	foundCemetery, err := civicDB.FindZoneAtByType(0, 64, 0, ZoneTypeCemetery)
	if err != nil || foundCemetery != nil {
		t.Errorf("expected nil for nonexistent zone type, got: %v (err: %v)", foundCemetery, err)
	}
}
