package db

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestReputationDB_LifecycleAndMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_test", "reputation.db")

	repDB, err := OpenReputationDB(dbPath, "clan_test")
	if err != nil {
		t.Fatalf("failed to open reputation database: %v", err)
	}
	defer repDB.Close()

	if repDB.ClanID() != "clan_test" {
		t.Errorf("expected clan ID clan_test, got %q", repDB.ClanID())
	}
	if repDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, repDB.Path())
	}
	if repDB.DB() == nil {
		t.Fatal("expected non-nil *sql.DB handle")
	}

	ctx := context.Background()

	// Check table existence
	var count int
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('hero_prestige', 'prestige_ledger');`
	if err := repDB.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("failed to query table count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 tables, found %d", count)
	}
}

func TestReputationDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	repDB, err := OpenReputationDBForClan(resolver, "clan_42")
	if err != nil {
		t.Fatalf("OpenReputationDBForClan failed: %v", err)
	}
	defer repDB.Close()

	expectedPath := resolver.ClanReputationDBPath("clan_42")
	if repDB.Path() != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, repDB.Path())
	}

	// Invalid clan ID
	if _, err := OpenReputationDBForClan(resolver, "clan/invalid"); err == nil {
		t.Error("expected error for invalid clan ID")
	}
}

func TestReputationDB_TitleProgression(t *testing.T) {
	tests := []struct {
		score         float64
		expectedTitle string
	}{
		{score: 100.0, expectedTitle: PrestigeTitleHero},
		{score: 50.0, expectedTitle: PrestigeTitleHero},
		{score: 49.9, expectedTitle: PrestigeTitleAlly},
		{score: 20.0, expectedTitle: PrestigeTitleAlly},
		{score: 19.9, expectedTitle: PrestigeTitleFriend},
		{score: 5.0, expectedTitle: PrestigeTitleFriend},
		{score: 4.9, expectedTitle: PrestigeTitleStranger},
		{score: 0.0, expectedTitle: PrestigeTitleStranger},
		{score: -4.9, expectedTitle: PrestigeTitleStranger},
		{score: -5.0, expectedTitle: PrestigeTitleSuspect},
		{score: -19.9, expectedTitle: PrestigeTitleSuspect},
		{score: -20.0, expectedTitle: PrestigeTitleVillain},
		{score: -50.0, expectedTitle: PrestigeTitleVillain},
	}

	for _, tc := range tests {
		title := ComputePrestigeTitle(tc.score)
		if title != tc.expectedTitle {
			t.Errorf("score %.1f: expected title %s, got %s", tc.score, tc.expectedTitle, title)
		}
	}
}

func TestReputationDB_DeltaAccumulationAndLedger(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "reputation.db")

	repDB, err := OpenReputationDB(dbPath, "clan_alpha")
	if err != nil {
		t.Fatalf("OpenReputationDB failed: %v", err)
	}
	defer repDB.Close()

	ctx := context.Background()
	playerUUID := "player-uuid-1234"

	// 1. Initial query for non-existent player
	hp, err := repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil {
		t.Fatalf("unexpected error on GetHeroPrestige: %v", err)
	}
	if hp != nil {
		t.Errorf("expected nil for non-existent player, got %+v", hp)
	}

	// Default query
	defHP, err := repDB.GetHeroPrestigeOrDefault(ctx, playerUUID)
	if err != nil {
		t.Fatalf("unexpected error on GetHeroPrestigeOrDefault: %v", err)
	}
	if defHP.Score != 0.0 || defHP.Title != PrestigeTitleStranger {
		t.Errorf("expected default 0.0 / STRANGER, got score %.1f, title %s", defHP.Score, defHP.Title)
	}

	// 2. First interaction: player helps clan (+10.0 delta) -> FRIEND
	newScore, err := repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{
		PlayerUUID: playerUUID,
		PlayerName: "HeroPlayer",
		AgentID:    "aria",
		Delta:      10.0,
		Reason:     "Donated wheat to granary",
		WorldTick:  1000,
	})
	if err != nil {
		t.Fatalf("RecordPrestigeDelta failed: %v", err)
	}
	if newScore != 10.0 {
		t.Errorf("expected score 10.0, got %.1f", newScore)
	}

	hp, err = repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil {
		t.Fatalf("GetHeroPrestige failed: %v", err)
	}
	if hp == nil || hp.Score != 10.0 || hp.Title != PrestigeTitleFriend || hp.PlayerName != "HeroPlayer" {
		t.Errorf("expected FRIEND (10.0), got %+v", hp)
	}

	// 3. Second interaction: player slays raid boss (+45.0 delta) -> score 55.0 -> HERO
	newScore, err = repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{
		PlayerUUID: playerUUID,
		PlayerName: "HeroPlayer",
		AgentID:    "boris",
		Delta:      45.0,
		Reason:     "Defeated pillager raid captain",
		WorldTick:  1500,
	})
	if err != nil {
		t.Fatalf("RecordPrestigeDelta failed: %v", err)
	}
	if newScore != 55.0 {
		t.Errorf("expected score 55.0, got %.1f", newScore)
	}

	hp, err = repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil || hp.Title != PrestigeTitleHero {
		t.Errorf("expected HERO title, got %s (err: %v)", hp.Title, err)
	}

	// 4. Third interaction: player accidentally damages villager (-60.0 delta) -> score -5.0 -> SUSPECT
	newScore, err = repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{
		PlayerUUID: playerUUID,
		PlayerName: "HeroPlayer",
		AgentID:    "aria",
		Delta:      -60.0,
		Reason:     "Struck settler during skirmish",
		WorldTick:  2000,
	})
	if err != nil {
		t.Fatalf("RecordPrestigeDelta failed: %v", err)
	}
	if newScore != -5.0 {
		t.Errorf("expected score -5.0, got %.1f", newScore)
	}

	hp, err = repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil || hp.Title != PrestigeTitleSuspect {
		t.Errorf("expected SUSPECT title, got %s (err: %v)", hp.Title, err)
	}

	// 5. Fourth interaction: player attacks granary (-20.0 delta) -> score -25.0 -> VILLAIN
	newScore, err = repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{
		PlayerUUID: playerUUID,
		AgentID:    "aria",
		Delta:      -20.0,
		Reason:     "Pillaged granary chest",
		WorldTick:  2500,
	})
	if err != nil {
		t.Fatalf("RecordPrestigeDelta failed: %v", err)
	}
	if newScore != -25.0 {
		t.Errorf("expected score -25.0, got %.1f", newScore)
	}

	hp, err = repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil || hp.Title != PrestigeTitleVillain {
		t.Errorf("expected VILLAIN title, got %s (err: %v)", hp.Title, err)
	}

	// Verify ledger entries
	ledger, err := repDB.GetPrestigeLedger(ctx, playerUUID, 10)
	if err != nil {
		t.Fatalf("GetPrestigeLedger failed: %v", err)
	}
	if len(ledger) != 4 {
		t.Fatalf("expected 4 ledger entries, got %d", len(ledger))
	}
	// Ordered by ID DESC: newest first
	if ledger[0].Delta != -20.0 || ledger[0].Reason != "Pillaged granary chest" {
		t.Errorf("unexpected top ledger entry: %+v", ledger[0])
	}
	if ledger[3].Delta != 10.0 || ledger[3].Reason != "Donated wheat to granary" {
		t.Errorf("unexpected initial ledger entry: %+v", ledger[3])
	}
}

func TestReputationDB_ConcurrentDeltas(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "reputation_concurrent.db")

	repDB, err := OpenReputationDB(dbPath, "clan_concurrent")
	if err != nil {
		t.Fatalf("OpenReputationDB failed: %v", err)
	}
	defer repDB.Close()

	ctx := context.Background()
	playerUUID := "concurrent-player"

	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(iterations)

	for i := 0; i < iterations; i++ {
		go func(idx int) {
			defer wg.Done()
			_, dErr := repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{
				PlayerUUID: playerUUID,
				PlayerName: "FastPlayer",
				AgentID:    "worker",
				Delta:      1.0,
				Reason:     "Concurrent contribution",
				WorldTick:  int64(100 + idx),
			})
			if dErr != nil {
				t.Errorf("concurrent delta error: %v", dErr)
			}
		}(i)
	}

	wg.Wait()

	hp, err := repDB.GetHeroPrestige(ctx, playerUUID)
	if err != nil {
		t.Fatalf("GetHeroPrestige failed: %v", err)
	}
	expectedScore := float64(iterations)
	if hp.Score != expectedScore {
		t.Errorf("expected score %.1f, got %.1f", expectedScore, hp.Score)
	}
	if hp.Title != PrestigeTitleHero {
		t.Errorf("expected title HERO, got %s", hp.Title)
	}

	ledger, err := repDB.GetPrestigeLedger(ctx, playerUUID, 100)
	if err != nil {
		t.Fatalf("GetPrestigeLedger failed: %v", err)
	}
	if len(ledger) != iterations {
		t.Errorf("expected %d ledger entries, got %d", iterations, len(ledger))
	}
}

func TestReputationDB_ValidationAndClosed(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "reputation_closed.db")

	repDB, err := OpenReputationDB(dbPath, "clan_closed")
	if err != nil {
		t.Fatalf("OpenReputationDB failed: %v", err)
	}

	ctx := context.Background()

	// Empty UUID validation
	if _, err := repDB.GetHeroPrestige(ctx, ""); err == nil {
		t.Error("expected error for empty playerUUID in GetHeroPrestige")
	}
	if _, err := repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{}); err == nil {
		t.Error("expected error for empty playerUUID in RecordPrestigeDelta")
	}

	// Close database
	if err := repDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Calls on closed database return ErrClosed
	if _, err := repDB.GetHeroPrestige(ctx, "any-uuid"); err != ErrClosed {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := repDB.RecordPrestigeDelta(ctx, PrestigeLedgerEntry{PlayerUUID: "any-uuid"}); err != ErrClosed {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := repDB.GetPrestigeLedger(ctx, "any-uuid", 10); err != ErrClosed {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}
