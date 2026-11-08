package db

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestLandmarksDB_LifecycleAndMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_lm_test", "landmarks.db")

	lmDB, err := OpenLandmarksDB(dbPath, "clan_lm_test")
	if err != nil {
		t.Fatalf("failed to open landmarks database: %v", err)
	}
	defer lmDB.Close()

	if lmDB.ClanID() != "clan_lm_test" {
		t.Errorf("expected clan ID clan_lm_test, got %q", lmDB.ClanID())
	}
	if lmDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, lmDB.Path())
	}
	if lmDB.DB() == nil {
		t.Fatal("expected non-nil *sql.DB handle")
	}

	ctx := context.Background()

	var count int
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name = 'landmarks';`
	if err := lmDB.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("failed to query table count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 table, found %d", count)
	}
}

func TestLandmarksDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	lmDB, err := OpenLandmarksDBForClan(resolver, "clan_explorers")
	if err != nil {
		t.Fatalf("OpenLandmarksDBForClan failed: %v", err)
	}
	defer lmDB.Close()

	expectedPath := resolver.ClanLandmarksDBPath("clan_explorers")
	if lmDB.Path() != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, lmDB.Path())
	}

	if _, err := OpenLandmarksDBForClan(resolver, "invalid/clan"); err == nil {
		t.Error("expected error for invalid clan ID")
	}
}

func TestLandmarksDB_CRUDAndNearestQuery(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "landmarks.db")

	lmDB, err := OpenLandmarksDB(dbPath, "clan_geo")
	if err != nil {
		t.Fatalf("failed to open landmarks database: %v", err)
	}
	defer lmDB.Close()

	// 1. Initial lookup is nil
	lm, err := lmDB.GetLandmark("lm-1")
	if err != nil || lm != nil {
		t.Fatalf("expected nil for nonexistent landmark, got %v (err: %v)", lm, err)
	}

	// 2. Nearest on empty returns nil
	nearest, err := lmDB.FindNearestLandmark(0, 64, 0, "")
	if err != nil || nearest != nil {
		t.Fatalf("expected nil nearest on empty DB, got %v (err: %v)", nearest, err)
	}

	// 3. Register landmarks
	l1 := &Landmark{
		LandmarkID:          "lm-canyon",
		Name:                "Echo Canyon",
		Category:            LandmarkCategoryNatural,
		X:                   100.0,
		Y:                   50.0,
		Z:                   100.0,
		SignpostLine1:       "Echo Canyon",
		SignpostLine2:       "Be wary of depths",
		LoreText:            "Carved by ancient rivers long before the agents arrived.",
		DiscoveredByAgentID: "agent-explorer-1",
		DiscoveredTick:      500,
	}
	l2 := &Landmark{
		LandmarkID:          "lm-ruins",
		Name:                "Sunken Bastion",
		Category:            LandmarkCategoryRuins,
		X:                   -200.0,
		Y:                   40.0,
		Z:                   -300.0,
		SignpostLine1:       "Sunken Bastion",
		SignpostLine2:       "Forbidden Sanctuary",
		LoreText:            "Remnants of a fallen dynasty submerged in the murky waters.",
		DiscoveredByAgentID: "agent-scout-2",
		DiscoveredTick:      750,
	}
	l3 := &Landmark{
		LandmarkID:          "lm-mine",
		Name:                "Iron Vein Quarry",
		Category:            LandmarkCategoryResource,
		X:                   120.0,
		Y:                   64.0,
		Z:                   90.0,
		SignpostLine1:       "Iron Quarry",
		SignpostLine2:       "Clan Mine",
		LoreText:            "A bountiful deposit of raw iron ore discovered in Year 1.",
		DiscoveredByAgentID: "agent-miner-1",
		DiscoveredTick:      1000,
	}

	if err := lmDB.RegisterLandmark(l1); err != nil {
		t.Fatalf("RegisterLandmark l1 failed: %v", err)
	}
	if err := lmDB.RegisterLandmark(l2); err != nil {
		t.Fatalf("RegisterLandmark l2 failed: %v", err)
	}
	if err := lmDB.RegisterLandmark(l3); err != nil {
		t.Fatalf("RegisterLandmark l3 failed: %v", err)
	}

	// 4. GetLandmark
	got, err := lmDB.GetLandmark("lm-canyon")
	if err != nil || got == nil {
		t.Fatalf("GetLandmark failed: %v", err)
	}
	if got.Name != "Echo Canyon" || got.Category != LandmarkCategoryNatural || got.DiscoveredByAgentID != "agent-explorer-1" {
		t.Errorf("unexpected landmark data: %+v", got)
	}

	// 5. Nearest query from (105, 55, 95) without category filter -> should be lm-canyon (dist ~8.6)
	// vs lm-mine (120,64,90 -> dist ~18)
	nearest, err = lmDB.FindNearestLandmark(105, 55, 95, "")
	if err != nil || nearest == nil {
		t.Fatalf("FindNearestLandmark failed: %v", err)
	}
	if nearest.LandmarkID != "lm-canyon" {
		t.Errorf("expected lm-canyon, got %s", nearest.LandmarkID)
	}

	// 6. Nearest query with category filter (RESOURCE) from same coords -> should be lm-mine
	nearestRes, err := lmDB.FindNearestLandmark(105, 55, 95, LandmarkCategoryResource)
	if err != nil || nearestRes == nil {
		t.Fatalf("FindNearestLandmark with category failed: %v", err)
	}
	if nearestRes.LandmarkID != "lm-mine" {
		t.Errorf("expected lm-mine, got %s", nearestRes.LandmarkID)
	}

	// 7. Non-existent category returns nil, nil
	nearestNone, err := lmDB.FindNearestLandmark(105, 55, 95, "NONEXISTENT")
	if err != nil || nearestNone != nil {
		t.Errorf("expected nil for nonexistent category, got %v (err: %v)", nearestNone, err)
	}

	// 8. ListLandmarks (alphabetical by name: Echo Canyon, Iron Vein Quarry, Sunken Bastion)
	list, err := lmDB.ListLandmarks()
	if err != nil {
		t.Fatalf("ListLandmarks failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 landmarks, got %d", len(list))
	}
	if list[0].LandmarkID != "lm-canyon" || list[1].LandmarkID != "lm-mine" || list[2].LandmarkID != "lm-ruins" {
		t.Errorf("unexpected ordering: %+v", list)
	}
}

func TestLandmarksDB_ValidationAndClosed(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "val_lm.db")

	lmDB, err := OpenLandmarksDB(dbPath, "clan_val")
	if err != nil {
		t.Fatalf("failed to open landmarks database: %v", err)
	}

	// 1. Validation
	if err := lmDB.RegisterLandmark(nil); err == nil {
		t.Error("expected error for nil landmark")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: ""}); err == nil {
		t.Error("expected error for empty landmark_id")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: "1", Name: ""}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: "1", Name: "N", Category: ""}); err == nil {
		t.Error("expected error for empty category")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: "1", Name: "N", Category: "INVALID_CAT", DiscoveredByAgentID: "A"}); err == nil {
		t.Error("expected error for invalid landmark category")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: "1", Name: "N", Category: LandmarkCategoryNatural, DiscoveredByAgentID: ""}); err == nil {
		t.Error("expected error for empty discovered_by")
	}
	if err := lmDB.RegisterLandmark(&Landmark{LandmarkID: "1", Name: "N", Category: LandmarkCategoryNatural, DiscoveredByAgentID: "A", X: math.NaN()}); err == nil {
		t.Error("expected error for NaN coord")
	}
	if _, err := lmDB.FindNearestLandmark(math.NaN(), 0, 0, ""); err == nil {
		t.Error("expected error for NaN in nearest query")
	}

	// 2. Closed
	if err := lmDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	validLm := &Landmark{LandmarkID: "1", Name: "N", Category: "C", DiscoveredByAgentID: "A"}
	if err := lmDB.RegisterLandmark(validLm); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on RegisterLandmark, got %v", err)
	}
	if _, err := lmDB.GetLandmark("1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetLandmark, got %v", err)
	}
	if _, err := lmDB.FindNearestLandmark(0, 0, 0, ""); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on FindNearestLandmark, got %v", err)
	}
	if _, err := lmDB.ListLandmarks(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListLandmarks, got %v", err)
	}
}

func TestLandmarksDB_ConcurrentOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent_lm.db")

	lmDB, err := OpenLandmarksDB(dbPath, "clan_conc")
	if err != nil {
		t.Fatalf("failed to open landmarks database: %v", err)
	}
	defer lmDB.Close()

	const numWorkers = 10
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for worker := 0; worker < numWorkers; worker++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				lmID := fmt.Sprintf("lm-%d-%d", w, i)
				lm := &Landmark{
					LandmarkID:          lmID,
					Name:                fmt.Sprintf("Waystone %d-%d", w, i),
					Category:            LandmarkCategoryMonument,
					X:                   float64(w * 10),
					Y:                   64.0,
					Z:                   float64(i * 10),
					DiscoveredByAgentID: fmt.Sprintf("agent-%d", w),
					DiscoveredTick:      int64(i * 100),
				}
				if err := lmDB.RegisterLandmark(lm); err != nil {
					t.Errorf("concurrent RegisterLandmark failed: %v", err)
					return
				}

				// Concurrent read
				fetched, err := lmDB.GetLandmark(lmID)
				if err != nil || fetched == nil {
					t.Errorf("concurrent GetLandmark failed for %s: %v", lmID, err)
					return
				}

				// Concurrent nearest query
				if _, err := lmDB.FindNearestLandmark(0, 64, 0, ""); err != nil {
					t.Errorf("concurrent FindNearestLandmark failed: %v", err)
					return
				}
			}
		}(worker)
	}

	wg.Wait()

	all, err := lmDB.ListLandmarks()
	if err != nil {
		t.Fatalf("ListLandmarks failed: %v", err)
	}
	expected := numWorkers * iterations
	if len(all) != expected {
		t.Fatalf("expected %d landmarks, got %d", expected, len(all))
	}
}
