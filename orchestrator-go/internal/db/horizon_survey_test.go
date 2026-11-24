package db

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func buildTestHorizonSweep(ox, oy, oz float64, tick int64, forestCount, waterCount, plainsCount, stoneCount, ridgeCount, occludedCount int) []HorizonSector {
	sectors := make([]HorizonSector, 0, 36)
	idx := 0
	add := func(count int, feat, blockID string, dist float64, occ, open bool) {
		for i := 0; i < count && idx < 36; i++ {
			bearing := float64(idx * 10)
			rad := bearing * math.Pi / 180.0
			dx := -math.Sin(rad)
			dz := math.Cos(rad)
			sectors = append(sectors, HorizonSector{
				SectorIndex:    idx,
				BearingDeg:     bearing,
				HitDistance:    dist,
				FeatureType:    feat,
				HitBlockID:     blockID,
				HitX:           ox + dx*dist,
				HitY:           oy,
				HitZ:           oz + dz*dist,
				ElevationDelta: 0,
				Occluded:       occ,
				OpenHorizon:    open,
				SurveyedTick:   tick,
			})
			idx++
		}
	}
	add(forestCount, "FOREST_CANOPY", "minecraft:oak_leaves", 42.0, false, false)
	add(waterCount, "WATER", "minecraft:water", 38.0, false, false)
	add(stoneCount, "STONE_OUTCROP", "minecraft:stone", 28.0, false, false)
	add(ridgeCount, "RIDGE_OBSTRUCTION", "minecraft:stone", 8.0, true, false)
	remOcc := occludedCount - ridgeCount
	if remOcc > 0 {
		add(remOcc, "OPEN_PLAINS", "minecraft:dirt", 10.0, true, false)
	}
	add(plainsCount, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	for idx < 36 {
		add(1, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	}
	return sectors
}

func TestAgentDB_HorizonSectorsPersistence(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, "agents", "aria")

	adb, err := OpenAgentDB(agentDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer adb.Close()

	sweep1 := buildTestHorizonSweep(0, 64, 0, 1000, 8, 4, 20, 2, 2, 2)
	if err := adb.InsertHorizonSurveyBatch(ctx, 0, 64, 0, 1000, sweep1); err != nil {
		t.Fatalf("InsertHorizonSurveyBatch(sweep1) failed: %v", err)
	}

	sweep2 := buildTestHorizonSweep(48, 66, -32, 2500, 12, 2, 18, 4, 0, 0)
	if err := adb.InsertHorizonSurveyBatch(ctx, 48, 66, -32, 2500, sweep2); err != nil {
		t.Fatalf("InsertHorizonSurveyBatch(sweep2) failed: %v", err)
	}

	recent, err := adb.GetRecentHorizonSectors(ctx, 36)
	if err != nil {
		t.Fatalf("GetRecentHorizonSectors failed: %v", err)
	}
	if len(recent) != 36 {
		t.Fatalf("expected 36 recent sectors, got %d", len(recent))
	}
	// Newest sweep (tick 2500) must come first, ordered by sector_index ascending
	if recent[0].SurveyedTick != 2500 || recent[0].SectorIndex != 0 {
		t.Fatalf("expected newest sweep tick=2500 sector=0 first, got tick=%d sector=%d",
			recent[0].SurveyedTick, recent[0].SectorIndex)
	}
	if recent[35].SurveyedTick != 2500 || recent[35].SectorIndex != 35 {
		t.Fatalf("expected sector 35 last in recent batch, got tick=%d sector=%d",
			recent[35].SurveyedTick, recent[35].SectorIndex)
	}
	if recent[0].OriginX != 48 || recent[0].OriginY != 66 || recent[0].OriginZ != -32 {
		t.Fatalf("unexpected origin coords: (%v, %v, %v)", recent[0].OriginX, recent[0].OriginY, recent[0].OriginZ)
	}

	// Query all 72
	all, err := adb.GetRecentHorizonSectors(ctx, 100)
	if err != nil {
		t.Fatalf("GetRecentHorizonSectors(100) failed: %v", err)
	}
	if len(all) != 72 {
		t.Fatalf("expected 72 total sectors, got %d", len(all))
	}
}

func TestCivicRegistryDB_MergeExplorerHorizonSurveyAndScoreCandidates(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "pioneers", "civic_registry.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "pioneers")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}
	defer civicDB.Close()

	// Empty DB returns nil, nil
	emptyCand, err := civicDB.ScoreCollectiveSettlementCandidates(ctx)
	if err != nil {
		t.Fatalf("ScoreCollectiveSettlementCandidates on empty DB failed: %v", err)
	}
	if emptyCand != nil {
		t.Fatalf("expected nil candidate on empty DB, got %+v", emptyCand)
	}

	// Region A (120, 64, -80): rich site corroborated by 2 explorers (forest + water + plains + stone)
	sweepA1 := buildTestHorizonSweep(120, 64, -80, 1200, 9, 4, 18, 4, 1, 1)
	sweepA2 := buildTestHorizonSweep(132, 64, -74, 1400, 8, 5, 20, 3, 0, 0)

	// Region B (-100, 72, 100): heavily occluded ridge cliffside
	sweepB := buildTestHorizonSweep(-100, 72, 100, 1300, 0, 0, 4, 2, 20, 30)

	// Region C (0, 64, 150): barren open plains without forest or water
	sweepC := buildTestHorizonSweep(0, 64, 150, 1350, 0, 0, 36, 0, 0, 0)

	if err := civicDB.MergeExplorerHorizonSurvey(ctx, "aria", 120, 64, -80, 1200, sweepA1); err != nil {
		t.Fatalf("MergeExplorerHorizonSurvey(aria A) failed: %v", err)
	}
	if err := civicDB.MergeExplorerHorizonSurvey(ctx, "bran", 132, 64, -74, 1400, sweepA2); err != nil {
		t.Fatalf("MergeExplorerHorizonSurvey(bran A) failed: %v", err)
	}
	if err := civicDB.MergeExplorerHorizonSurvey(ctx, "cora", -100, 72, 100, 1300, sweepB); err != nil {
		t.Fatalf("MergeExplorerHorizonSurvey(cora B) failed: %v", err)
	}
	if err := civicDB.MergeExplorerHorizonSurvey(ctx, "dorn", 0, 64, 150, 1350, sweepC); err != nil {
		t.Fatalf("MergeExplorerHorizonSurvey(dorn C) failed: %v", err)
	}

	top, err := civicDB.ScoreCollectiveSettlementCandidates(ctx)
	if err != nil {
		t.Fatalf("ScoreCollectiveSettlementCandidates failed: %v", err)
	}
	if top == nil {
		t.Fatal("expected non-nil top settlement candidate")
	}
	if top.ExplorerCount != 2 {
		t.Fatalf("expected top region to have ExplorerCount=2, got %d (%+v)", top.ExplorerCount, top)
	}
	if math.Hypot(top.X-126, top.Z-(-77)) > 5.0 {
		t.Fatalf("expected top centroid near (126, 64, -77), got (%v, %v, %v)", top.X, top.Y, top.Z)
	}
	if top.Score <= 60.0 {
		t.Fatalf("expected high suitability score for Region A, got %.1f", top.Score)
	}
	if top.Summary == "" {
		t.Fatal("expected non-empty summary")
	}

	ranked, err := civicDB.RankCollectiveSettlementCandidates(ctx)
	if err != nil {
		t.Fatalf("RankCollectiveSettlementCandidates failed: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("expected 3 clustered regions, got %d", len(ranked))
	}
	if ranked[0].Score <= ranked[1].Score || ranked[1].Score <= ranked[2].Score {
		t.Fatalf("expected strictly descending scores across A, C, B: %+v", ranked)
	}

	// Test UpsertSettlementPOIsBatch & ListSettlementPOIs
	err = civicDB.UpsertSettlementPOIsBatch(ctx, "pioneers", "aria", []AgentPOI{
		{
			POIID:            "poi_resource_120_64_-80",
			Category:         "RESOURCE",
			BlockID:          "minecraft:oak_leaves",
			X:                120,
			Y:                70,
			Z:                -80,
			AccessX:          120,
			AccessY:          64,
			AccessZ:          -80,
			ElevationLayer:   "SURFACE",
			DiscoveredTick:   1200,
			LastVerifiedTick: 1200,
			IsActive:         true,
		},
	})
	if err != nil {
		t.Fatalf("UpsertSettlementPOIsBatch failed: %v", err)
	}
	pois, err := civicDB.ListSettlementPOIs(ctx, "pioneers")
	if err != nil {
		t.Fatalf("ListSettlementPOIs failed: %v", err)
	}
	if len(pois) != 1 || pois[0].CreatorAgentID != "aria" {
		t.Fatalf("expected 1 settlement POI from aria, got %+v", pois)
	}
}
