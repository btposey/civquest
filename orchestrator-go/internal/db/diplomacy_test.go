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

func TestDiplomacyDB_LifecycleAndMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_diplo_test", "diplomacy.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_diplo_test")
	if err != nil {
		t.Fatalf("failed to open diplomacy database: %v", err)
	}
	defer diploDB.Close()

	if diploDB.ClanID() != "clan_diplo_test" {
		t.Errorf("expected clan ID clan_diplo_test, got %q", diploDB.ClanID())
	}
	if diploDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, diploDB.Path())
	}
	if diploDB.DB() == nil {
		t.Fatal("expected non-nil *sql.DB handle")
	}

	ctx := context.Background()

	var count int
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name = 'clan_affinities';`
	if err := diploDB.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("failed to query table count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 table, found %d", count)
	}
}

func TestDiplomacyDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	diploDB, err := OpenDiplomacyDBForClan(resolver, "clan_diplomats")
	if err != nil {
		t.Fatalf("OpenDiplomacyDBForClan failed: %v", err)
	}
	defer diploDB.Close()

	expectedPath := resolver.ClanDiplomacyDBPath("clan_diplomats")
	if diploDB.Path() != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, diploDB.Path())
	}

	if _, err := OpenDiplomacyDBForClan(resolver, "invalid/clan"); err == nil {
		t.Error("expected error for invalid clan ID")
	}
}

func TestDiplomacyDB_AffinityTransitionsAndClamping(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "affinities.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_test")
	if err != nil {
		t.Fatalf("failed to open diplomacy database: %v", err)
	}
	defer diploDB.Close()

	targetClan := "clan_neighbors"

	// 1. Initial lookup is nil
	aff, err := diploDB.GetAffinity(targetClan)
	if err != nil {
		t.Fatalf("GetAffinity failed: %v", err)
	}
	if aff != nil {
		t.Errorf("expected nil for non-existent clan affinity, got %+v", aff)
	}

	// 2. Initial delta of +25.0 -> Score 25.0, Status FRIENDLY
	if err := diploDB.SetAffinity(targetClan, 25.0, 100); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, err = diploDB.GetAffinity(targetClan)
	if err != nil || aff == nil {
		t.Fatalf("expected non-nil affinity: %v", err)
	}
	if aff.AffinityScore != 25.0 || aff.Status != DiplomacyStatusFriendly || aff.LastUpdatedTick != 100 {
		t.Errorf("unexpected affinity: %+v", aff)
	}

	// 3. Further increase by +40.0 -> Score 65.0, Status ALLIED
	if err := diploDB.SetAffinity(targetClan, 40.0, 200); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != 65.0 || aff.Status != DiplomacyStatusAllied {
		t.Errorf("expected 65.0 ALLIED, got %+v", aff)
	}

	// 4. Test max clamping (+50.0 should clamp to 100.0)
	if err := diploDB.SetAffinity(targetClan, 50.0, 300); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != 100.0 {
		t.Errorf("expected clamped score 100.0, got %f", aff.AffinityScore)
	}

	// 5. Huge negative delta (-150.0) -> Score -50.0, Status SUSPICIOUS
	if err := diploDB.SetAffinity(targetClan, -150.0, 400); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != -50.0 || aff.Status != DiplomacyStatusSuspicious {
		t.Errorf("expected -50.0 SUSPICIOUS, got %+v", aff)
	}

	// 6. Test HOSTILE transition (-20.0 from -50.0 -> -70.0, HOSTILE)
	if err := diploDB.SetAffinity(targetClan, -20.0, 450); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != -70.0 || aff.Status != DiplomacyStatusHostile {
		t.Errorf("expected -70.0 HOSTILE, got %+v", aff)
	}

	// 7. Test min clamping & WAR transition (-60.0 should clamp to -100.0, WAR)
	if err := diploDB.SetAffinity(targetClan, -60.0, 500); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != -100.0 || aff.Status != DiplomacyStatusWar {
		t.Errorf("expected clamped score -100.0 WAR, got %+v", aff)
	}

	// 8. Neutral transition (+100.0 from -100.0 -> 0.0 NEUTRAL, breaks WAR)
	if err := diploDB.SetAffinity(targetClan, 100.0, 600); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != 0.0 || aff.Status != DiplomacyStatusNeutral {
		t.Errorf("expected 0.0 NEUTRAL, got %+v", aff)
	}
}

func TestDiplomacyDB_TreatiesAndTradeVolume(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "treaties.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_trade")
	if err != nil {
		t.Fatalf("failed to open diplomacy database: %v", err)
	}
	defer diploDB.Close()

	targetClan := "clan_merchants"

	// 1. Establish Free Trade treaty for 24000 ticks (1 in-game day)
	if err := diploDB.SetTreaty(targetClan, TreatyFreeTrade, 24000, 1000); err != nil {
		t.Fatalf("SetTreaty failed: %v", err)
	}
	aff, err := diploDB.GetAffinity(targetClan)
	if err != nil || aff == nil {
		t.Fatalf("expected non-nil affinity: %v", err)
	}
	if aff.ActiveTreaty != TreatyFreeTrade || aff.TreatyExpiryTick != 25000 {
		t.Errorf("expected TreatyFreeTrade expiring at 25000, got %+v", aff)
	}

	// 2. Record trade volume
	if err := diploDB.RecordTradeVolume(targetClan, 64, 1500); err != nil {
		t.Fatalf("RecordTradeVolume failed: %v", err)
	}
	if err := diploDB.RecordTradeVolume(targetClan, 32, 1600); err != nil {
		t.Fatalf("RecordTradeVolume failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.TradeVolume != 96 {
		t.Errorf("expected total trade volume 96, got %d", aff.TradeVolume)
	}
	// Verify treaty wasn't overwritten by trade volume
	if aff.ActiveTreaty != TreatyFreeTrade {
		t.Errorf("expected treaty to remain preserved, got %s", aff.ActiveTreaty)
	}

	// 3. Clear treaty
	if err := diploDB.SetTreaty(targetClan, TreatyNone, 0, 2000); err != nil {
		t.Fatalf("SetTreaty clear failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.ActiveTreaty != TreatyNone || aff.TreatyExpiryTick != 0 {
		t.Errorf("expected treaty NONE with 0 expiry, got %+v", aff)
	}

	// 4. List affinities
	_ = diploDB.SetAffinity("clan_alpha", 10.0, 100)
	affs, err := diploDB.ListAffinities()
	if err != nil {
		t.Fatalf("ListAffinities failed: %v", err)
	}
	if len(affs) != 2 {
		t.Fatalf("expected 2 affinities, got %d", len(affs))
	}
	if affs[0].TargetClanID != "clan_alpha" || affs[1].TargetClanID != "clan_merchants" {
		t.Errorf("unexpected ordering: %+v", affs)
	}
}

func TestDiplomacyDB_ConcurrentAffinityUpdates(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent_diplo.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_concurrent")
	if err != nil {
		t.Fatalf("failed to open diplomacy database: %v", err)
	}
	defer diploDB.Close()

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			tick := int64(100 + idx)
			_ = diploDB.SetAffinity("clan_shared", 1.0, tick)
			_ = diploDB.RecordTradeVolume("clan_shared", 5, tick)
		}(i)
	}

	wg.Wait()

	aff, err := diploDB.GetAffinity("clan_shared")
	if err != nil || aff == nil {
		t.Fatalf("expected non-nil affinity: %v", err)
	}
	if aff.AffinityScore != float64(workers)*1.0 {
		t.Errorf("expected score %f, got %f", float64(workers)*1.0, aff.AffinityScore)
	}
	if aff.TradeVolume != workers*5 {
		t.Errorf("expected trade volume %d, got %d", workers*5, aff.TradeVolume)
	}
}

func TestDiplomacyDB_ValidationAndClosed(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "val_diplo.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_val")
	if err != nil {
		t.Fatalf("failed to open diplomacy database: %v", err)
	}

	// 1. Validations
	if err := diploDB.SetAffinity("", 10.0, 100); err == nil {
		t.Error("expected error for empty clan ID")
	}
	if err := diploDB.SetAffinity("clan_x", math.NaN(), 100); err == nil {
		t.Error("expected error for NaN delta")
	}
	if err := diploDB.SetAffinity("clan_x", math.Inf(1), 100); err == nil {
		t.Error("expected error for Inf delta")
	}
	if err := diploDB.SetTreaty("", TreatyNonAggression, 100, 100); err == nil {
		t.Error("expected error for empty target clan in SetTreaty")
	}
	if err := diploDB.SetTreaty("clan_x", TreatyNonAggression, -10, 100); err == nil {
		t.Error("expected error for negative duration in SetTreaty")
	}
	if err := diploDB.RecordTradeVolume("clan_x", 0, 100); err == nil {
		t.Error("expected error for non-positive trade volume")
	}
	if err := diploDB.SetTreaty("clan_x", "SECRET_CONSPIRACY", 100, 100); err == nil {
		t.Error("expected error for invalid treaty type")
	}
	if err := diploDB.SetDiplomaticStatus("", DiplomacyStatusNeutral, 100); err == nil {
		t.Error("expected error for empty target clan in SetDiplomaticStatus")
	}
	if err := diploDB.SetDiplomaticStatus("clan_x", "BOGUS_STATUS", 100); err == nil {
		t.Error("expected error for invalid diplomatic status")
	}

	// 2. Closed database
	if err := diploDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := diploDB.SetDiplomaticStatus("clan_x", DiplomacyStatusWar, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on SetDiplomaticStatus, got %v", err)
	}
	if err := diploDB.SetAffinity("clan_x", 1.0, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on SetAffinity, got %v", err)
	}
	if err := diploDB.SetTreaty("clan_x", TreatyNone, 0, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on SetTreaty, got %v", err)
	}
	if err := diploDB.RecordTradeVolume("clan_x", 10, 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on RecordTradeVolume, got %v", err)
	}
	if _, err := diploDB.GetAffinity("clan_x"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetAffinity, got %v", err)
	}
	if _, err := diploDB.ListAffinities(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListAffinities, got %v", err)
	}
}

func TestDiplomacyDB_Remediations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "diplo_remediations.db")

	diploDB, err := OpenDiplomacyDB(dbPath, "clan_remediate")
	if err != nil {
		t.Fatalf("OpenDiplomacyDB failed: %v", err)
	}
	defer diploDB.Close()

	// 1. Issue 3: ComputeAffinityStatus thresholds
	testCases := []struct {
		score          float64
		expectedStatus string
	}{
		{65.0, DiplomacyStatusAllied},
		{60.0, DiplomacyStatusAllied},
		{59.9, DiplomacyStatusFriendly},
		{20.0, DiplomacyStatusFriendly},
		{19.9, DiplomacyStatusNeutral},
		{-20.0, DiplomacyStatusNeutral},
		{-20.1, DiplomacyStatusSuspicious},
		{-60.0, DiplomacyStatusSuspicious},
		{-60.1, DiplomacyStatusHostile},
		{-89.9, DiplomacyStatusHostile},
		{-90.0, DiplomacyStatusWar},
		{-95.0, DiplomacyStatusWar},
		{-100.0, DiplomacyStatusWar},
	}
	for _, tc := range testCases {
		status := ComputeAffinityStatus(tc.score)
		if status != tc.expectedStatus {
			t.Errorf("ComputeAffinityStatus(%f): expected %s, got %s", tc.score, tc.expectedStatus, status)
		}
	}

	// 2. Issue 3: SetDiplomaticStatus & War preservation across negative affinities
	targetClan := "clan_warlords"
	if err := diploDB.SetAffinity(targetClan, -50.0, 100); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	// Explicitly declare war
	if err := diploDB.SetDiplomaticStatus(targetClan, DiplomacyStatusWar, 110); err != nil {
		t.Fatalf("SetDiplomaticStatus failed: %v", err)
	}
	aff, err := diploDB.GetAffinity(targetClan)
	if err != nil || aff.Status != DiplomacyStatusWar {
		t.Fatalf("expected WAR status, got %+v", aff)
	}

	// Minor affinity improvement (+20.0 from -50.0 -> -30.0)
	// Without war preservation, -30.0 would normally be SUSPICIOUS. But since current is WAR and newScore < 0.0 with no treaty, WAR is preserved!
	if err := diploDB.SetAffinity(targetClan, 20.0, 120); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != -30.0 || aff.Status != DiplomacyStatusWar {
		t.Errorf("expected score -30.0 and preserved WAR status, got %+v", aff)
	}

	// Affinity crosses to non-negative (+40.0 from -30.0 -> +10.0 >= 0.0)
	// Breaks WAR status and becomes NEUTRAL (score 10.0 is NEUTRAL)
	if err := diploDB.SetAffinity(targetClan, 40.0, 130); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff, _ = diploDB.GetAffinity(targetClan)
	if aff.AffinityScore != 10.0 || aff.Status != DiplomacyStatusNeutral {
		t.Errorf("expected score 10.0 and NEUTRAL status upon crossing non-negative, got %+v", aff)
	}

	// 3. Issue 3: Treaty breaks WAR
	targetClan2 := "clan_raiders"
	_ = diploDB.SetAffinity(targetClan2, -70.0, 100)
	_ = diploDB.SetDiplomaticStatus(targetClan2, DiplomacyStatusWar, 110)

	// Sign Non-Aggression pact for 5000 ticks
	if err := diploDB.SetTreaty(targetClan2, TreatyNonAggression, 5000, 150); err != nil {
		t.Fatalf("SetTreaty failed: %v", err)
	}
	aff2, _ := diploDB.GetAffinity(targetClan2)
	// Treaty must break WAR and evaluate status (at -70.0, ComputeAffinityStatus gives HOSTILE)
	if aff2.Status != DiplomacyStatusHostile || aff2.ActiveTreaty != TreatyNonAggression {
		t.Errorf("expected treaty to break WAR to HOSTILE, got %+v", aff2)
	}

	// Subsequent affinity change with active treaty does not revert to WAR even if negative
	if err := diploDB.SetAffinity(targetClan2, 10.0, 160); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}
	aff2, _ = diploDB.GetAffinity(targetClan2)
	if aff2.AffinityScore != -60.0 || aff2.Status != DiplomacyStatusSuspicious {
		t.Errorf("expected score -60.0 and SUSPICIOUS status, got %+v", aff2)
	}
}
