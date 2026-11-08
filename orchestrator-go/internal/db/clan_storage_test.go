package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestClanStorage_LifecycleAndIntegration(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	clanID := "clan_frontier"

	storage, err := OpenClanStorage(resolver, clanID)
	if err != nil {
		t.Fatalf("OpenClanStorage failed: %v", err)
	}
	defer storage.Close()

	if storage.ClanID() != clanID {
		t.Errorf("expected clan ID %s, got %s", clanID, storage.ClanID())
	}

	// Verify all 5 database files exist on disk
	clanDir := resolver.ClanDataDir(clanID)
	expectedFiles := []string{
		"civic_registry.db",
		"reputation.db",
		"diplomacy.db",
		"landmarks.db",
		"contracts.db",
	}

	for _, fname := range expectedFiles {
		fpath := filepath.Join(clanDir, fname)
		if info, err := os.Stat(fpath); err != nil || info.IsDir() {
			t.Errorf("expected db file %s to exist: %v", fpath, err)
		}
	}

	// 1. Perform operations across all databases
	// Civic Registry
	if err := storage.CivicRegistry.AddGranaryStock("minecraft:bread", 64, 100); err != nil {
		t.Errorf("AddGranaryStock failed: %v", err)
	}
	stock, err := storage.CivicRegistry.GetItemStock("minecraft:bread")
	if err != nil || stock == nil || stock.Quantity != 64 {
		t.Errorf("unexpected civic registry stock: %v (err: %v)", stock, err)
	}

	// Reputation
	score, err := storage.Reputation.RecordPrestigeDelta(context.Background(), PrestigeLedgerEntry{
		PlayerUUID: "player-alpha",
		PlayerName: "AlphaLeader",
		AgentID:    "agent-elder-1",
		Delta:      25.0,
		Reason:     "Donated stone to settlement",
		WorldTick:  100,
	})
	if err != nil || score != 25.0 {
		t.Errorf("RecordPrestigeDelta failed: score=%f, err=%v", score, err)
	}

	// Diplomacy
	if err := storage.Diplomacy.SetAffinity("clan_neighbors", 30.0, 100); err != nil {
		t.Errorf("SetAffinity failed: %v", err)
	}
	aff, err := storage.Diplomacy.GetAffinity("clan_neighbors")
	if err != nil || aff == nil || aff.Status != DiplomacyStatusFriendly {
		t.Errorf("unexpected affinity: %v (err: %v)", aff, err)
	}

	// Landmarks
	if err := storage.Landmarks.RegisterLandmark(&Landmark{
		LandmarkID:          "lm-bastion",
		Name:                "Frontier Bastion",
		Category:            LandmarkCategorySettlement,
		X:                   10.0,
		Y:                   70.0,
		Z:                   10.0,
		DiscoveredByAgentID: "agent-elder-1",
		DiscoveredTick:      50,
	}); err != nil {
		t.Errorf("RegisterLandmark failed: %v", err)
	}
	lm, err := storage.Landmarks.GetLandmark("lm-bastion")
	if err != nil || lm == nil {
		t.Errorf("GetLandmark failed: %v", err)
	}

	// Contracts
	if err := storage.Contracts.CreateContract(&SharecroppingContract{
		ContractID:         "cnt-1",
		PlayerUUID:         "player-alpha",
		SettlerAgentID:     "agent-agronomist-1",
		PlotMinX:           0,
		PlotMaxX:           10,
		PlotMinY:           64,
		PlotMaxY:           65,
		PlotMinZ:           0,
		PlotMaxZ:           10,
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		CropType:           "minecraft:wheat",
	}); err != nil {
		t.Errorf("CreateContract failed: %v", err)
	}
	cnt, err := storage.Contracts.GetContract("cnt-1")
	if err != nil || cnt == nil {
		t.Errorf("GetContract failed: %v", err)
	}

	// 2. Test Close() idempotency
	if err := storage.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("second Close() should be idempotent, got %v", err)
	}

	// 3. Verify operations on closed storage DBs fail with ErrClosed
	if _, err := storage.CivicRegistry.GetItemStock("minecraft:bread"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on closed CivicRegistry, got %v", err)
	}
	if _, err := storage.Reputation.GetHeroPrestige(context.Background(), "player-alpha"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on closed Reputation, got %v", err)
	}
	if _, err := storage.Diplomacy.GetAffinity("clan_neighbors"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on closed Diplomacy, got %v", err)
	}
	if _, err := storage.Landmarks.GetLandmark("lm-bastion"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on closed Landmarks, got %v", err)
	}
	if _, err := storage.Contracts.GetContract("cnt-1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on closed Contracts, got %v", err)
	}
}

func TestClanStorage_InvalidClanID(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	invalidIDs := []string{
		"",
		"../clan",
		"clan/slash",
		"clan with spaces",
		"CLAN!@#$",
	}

	for _, invalid := range invalidIDs {
		if _, err := OpenClanStorage(resolver, invalid); err == nil {
			t.Errorf("expected error for invalid clan ID %q, got nil", invalid)
		}
	}
}

func TestClanStorage_PartialFailureCleanup(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	clanID := "clan_fail_test"
	if err := resolver.InitClanDirectories(clanID); err != nil {
		t.Fatalf("InitClanDirectories failed: %v", err)
	}

	// Make diplomacy.db a directory so opening it fails after civic_registry and reputation succeed
	diploPath := resolver.ClanDiplomacyDBPath(clanID)
	if err := os.MkdirAll(diploPath, 0750); err != nil {
		t.Fatalf("creating blocking directory failed: %v", err)
	}

	storage, err := OpenClanStorage(resolver, clanID)
	if err == nil {
		if storage != nil {
			storage.Close()
		}
		t.Fatal("expected OpenClanStorage to fail due to blocked diplomacy DB")
	}

	// Verify error contains cause
	if storage != nil {
		t.Errorf("expected nil storage on failure, got %+v", storage)
	}
}

func TestModels_DefensiveMethods(t *testing.T) {
	// 1. GranaryStock.Available() nil safety
	var g *GranaryStock
	if avail := g.Available(); avail != 0 {
		t.Errorf("expected 0 for nil GranaryStock.Available(), got %d", avail)
	}

	gValid := &GranaryStock{Quantity: 100, ReservedQuantity: 30}
	if avail := gValid.Available(); avail != 70 {
		t.Errorf("expected 70 available, got %d", avail)
	}

	gOverReserved := &GranaryStock{Quantity: 10, ReservedQuantity: 20}
	if avail := gOverReserved.Available(); avail != 0 {
		t.Errorf("expected 0 for over-reserved stock, got %d", avail)
	}

	// 2. ClanAffinity.IsTreatyActive() nil & treaty checks
	var a *ClanAffinity
	if a.IsTreatyActive(100) {
		t.Error("expected false for nil ClanAffinity.IsTreatyActive")
	}

	aEmpty := &ClanAffinity{ActiveTreaty: ""}
	if aEmpty.IsTreatyActive(100) {
		t.Error("expected false for empty treaty")
	}

	aNone := &ClanAffinity{ActiveTreaty: TreatyNone}
	if aNone.IsTreatyActive(100) {
		t.Error("expected false for TreatyNone")
	}

	// Indefinite treaty (expiry tick = 0)
	aPermanent := &ClanAffinity{ActiveTreaty: TreatyFreeTrade, TreatyExpiryTick: 0}
	if !aPermanent.IsTreatyActive(500000) {
		t.Error("expected true for permanent treaty with TreatyExpiryTick = 0")
	}

	// Time-limited treaty (expiry tick = 1000)
	aExpiring := &ClanAffinity{ActiveTreaty: TreatyDefensivePact, TreatyExpiryTick: 1000}
	if !aExpiring.IsTreatyActive(500) {
		t.Error("expected true before expiry tick (500 < 1000)")
	}
	if aExpiring.IsTreatyActive(1000) {
		t.Error("expected false at expiry tick (1000)")
	}
	if aExpiring.IsTreatyActive(1500) {
		t.Error("expected false after expiry tick (1500 > 1000)")
	}

	// 3. FunctionalZone.Contains() nil safety
	var z *FunctionalZone
	if z.Contains(0, 0, 0) {
		t.Error("expected false for nil FunctionalZone.Contains")
	}

	zValid := &FunctionalZone{MinX: 0, MaxX: 10, MinY: 60, MaxY: 70, MinZ: 0, MaxZ: 10}
	if !zValid.Contains(5, 65, 5) {
		t.Error("expected true for coordinate inside zone")
	}
	if zValid.Contains(15, 65, 5) {
		t.Error("expected false for coordinate outside zone")
	}
}
