package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestRumorsDB_LifecycleAndOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_test", "rumors.db")

	rDB, err := OpenRumorsDB(dbPath, "clan_test")
	if err != nil {
		t.Fatalf("failed to open rumors database: %v", err)
	}
	defer rDB.Close()

	if rDB.ClanID() != "clan_test" {
		t.Errorf("expected clan ID clan_test, got %q", rDB.ClanID())
	}
	if rDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, rDB.Path())
	}

	ctx := context.Background()

	// 1. Add valid rumor
	rumor1 := &Rumor{
		RumorID:         "rumor-creeper-nest",
		Category:        RumorCategoryHazard,
		Content:         "Spotted charged creeper near ravines east of settlement",
		SourceAgentID:   "agent-scout-1",
		SourceAgentName: "ScoutAria",
		LocationX:       120.5,
		LocationY:       64.0,
		LocationZ:       -45.2,
		LocationName:    "Eastern Ravine",
		Credibility:     0.95,
		OriginTick:      12000,
		CreatedTick:     12050,
		ExpiresTick:     24000,
	}
	if err := rDB.AddRumor(ctx, rumor1); err != nil {
		t.Fatalf("AddRumor failed: %v", err)
	}

	// 2. Retrieve rumor
	got, err := rDB.GetRumor(ctx, "rumor-creeper-nest")
	if err != nil {
		t.Fatalf("GetRumor failed: %v", err)
	}
	if got == nil || got.Content != rumor1.Content {
		t.Fatalf("unexpected rumor retrieved: %+v", got)
	}
	if got.Category != RumorCategoryHazard {
		t.Errorf("expected category HAZARD, got %s", got.Category)
	}
	if got.Credibility != 0.95 {
		t.Errorf("expected credibility 0.95, got %f", got.Credibility)
	}

	// 3. Add second rumor
	rumor2 := &Rumor{
		RumorID:         "rumor-bee-colony",
		Category:        RumorCategoryResource,
		Content:         "Found large cluster of beehives in sunflower plains",
		SourceAgentID:   "agent-forager-2",
		SourceAgentName: "ForagerBob",
		LocationX:       -200.0,
		LocationY:       70.0,
		LocationZ:       350.0,
		LocationName:    "Sunflower Plains",
		Credibility:     0.88,
		OriginTick:      11500,
		CreatedTick:     11600,
		ExpiresTick:     0, // permanent
	}
	if err := rDB.AddRumor(ctx, rumor2); err != nil {
		t.Fatalf("AddRumor 2 failed: %v", err)
	}

	// 4. List rumors
	list, err := rDB.ListRumors(ctx, 10)
	if err != nil {
		t.Fatalf("ListRumors failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 rumors, got %d", len(list))
	}

	// 5. List active rumors
	active, err := rDB.ListActiveRumors(ctx, 20000, 10)
	if err != nil {
		t.Fatalf("ListActiveRumors failed: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active rumors at tick 20000, got %d", len(active))
	}

	// Active after rumor1 expires (tick 25000)
	activeAfterExpiry, err := rDB.ListActiveRumors(ctx, 25000, 10)
	if err != nil {
		t.Fatalf("ListActiveRumors failed: %v", err)
	}
	if len(activeAfterExpiry) != 1 || activeAfterExpiry[0].RumorID != "rumor-bee-colony" {
		t.Fatalf("expected only permanent rumor-bee-colony after tick 25000, got: %v", activeAfterExpiry)
	}

	// 6. List by category
	hazards, err := rDB.ListRumorsByCategory(ctx, RumorCategoryHazard, 10)
	if err != nil {
		t.Fatalf("ListRumorsByCategory failed: %v", err)
	}
	if len(hazards) != 1 || hazards[0].RumorID != "rumor-creeper-nest" {
		t.Fatalf("expected 1 hazard rumor, got %d", len(hazards))
	}

	// 7. Record diffusion
	diffusionRecord := RumorDiffusionRecord{
		RumorID:         "rumor-creeper-nest",
		SpeakerAgentID:  "agent-scout-1",
		ListenerAgentID: "agent-blacksmith-3",
		WorldTick:       12100,
	}
	if err := rDB.RecordDiffusion(ctx, diffusionRecord); err != nil {
		t.Fatalf("RecordDiffusion failed: %v", err)
	}

	// Verify spread count incremented
	refetched, err := rDB.GetRumor(ctx, "rumor-creeper-nest")
	if err != nil {
		t.Fatalf("GetRumor failed: %v", err)
	}
	if refetched.SpreadCount != 1 {
		t.Errorf("expected spread_count=1, got %d", refetched.SpreadCount)
	}

	// Verify diffusion history
	hist, err := rDB.GetDiffusionHistory(ctx, "rumor-creeper-nest")
	if err != nil {
		t.Fatalf("GetDiffusionHistory failed: %v", err)
	}
	if len(hist) != 1 || hist[0].ListenerAgentID != "agent-blacksmith-3" {
		t.Fatalf("unexpected diffusion history: %+v", hist)
	}

	// 8. Undiffused rumors for agent
	// agent-blacksmith-3 already heard rumor-creeper-nest, but NOT rumor-bee-colony
	undiffusedForBlacksmith, err := rDB.GetUndiffusedRumorsForAgent(ctx, "agent-blacksmith-3", 10)
	if err != nil {
		t.Fatalf("GetUndiffusedRumorsForAgent failed: %v", err)
	}
	if len(undiffusedForBlacksmith) != 1 || undiffusedForBlacksmith[0].RumorID != "rumor-bee-colony" {
		t.Fatalf("expected only rumor-bee-colony for blacksmith, got: %v", undiffusedForBlacksmith)
	}

	// agent-forager-2 is source of rumor-bee-colony, should only get rumor-creeper-nest
	undiffusedForForager, err := rDB.GetUndiffusedRumorsForAgent(ctx, "agent-forager-2", 10)
	if err != nil {
		t.Fatalf("GetUndiffusedRumorsForAgent for forager failed: %v", err)
	}
	if len(undiffusedForForager) != 1 || undiffusedForForager[0].RumorID != "rumor-creeper-nest" {
		t.Fatalf("expected only rumor-creeper-nest for forager, got: %v", undiffusedForForager)
	}

	// 9. Manual increment spread count
	if err := rDB.IncrementSpreadCount(ctx, "rumor-bee-colony"); err != nil {
		t.Fatalf("IncrementSpreadCount failed: %v", err)
	}
	refetchedBee, _ := rDB.GetRumor(ctx, "rumor-bee-colony")
	if refetchedBee.SpreadCount != 1 {
		t.Errorf("expected spread count 1, got %d", refetchedBee.SpreadCount)
	}
}

func TestRumorsDB_Validation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_test", "rumors.db")
	rDB, err := OpenRumorsDB(dbPath, "clan_test")
	if err != nil {
		t.Fatalf("OpenRumorsDB failed: %v", err)
	}
	defer rDB.Close()

	ctx := context.Background()

	// Nil rumor
	if err := rDB.AddRumor(ctx, nil); err == nil {
		t.Error("expected error on nil rumor")
	}

	// Empty ID
	if err := rDB.AddRumor(ctx, &Rumor{RumorID: "", Content: "A", SourceAgentID: "B", Category: RumorCategoryGossip}); err == nil {
		t.Error("expected error on empty rumor ID")
	}

	// Empty Content
	if err := rDB.AddRumor(ctx, &Rumor{RumorID: "r1", Content: "", SourceAgentID: "B", Category: RumorCategoryGossip}); err == nil {
		t.Error("expected error on empty content")
	}

	// Empty SourceAgentID
	if err := rDB.AddRumor(ctx, &Rumor{RumorID: "r1", Content: "A", SourceAgentID: "", Category: RumorCategoryGossip}); err == nil {
		t.Error("expected error on empty source agent ID")
	}

	// Invalid Category
	if err := rDB.AddRumor(ctx, &Rumor{RumorID: "r1", Content: "A", SourceAgentID: "B", Category: "INVALID_CAT"}); err == nil {
		t.Error("expected error on invalid category")
	}

	// Invalid Diffusion Record
	if err := rDB.RecordDiffusion(ctx, RumorDiffusionRecord{}); err == nil {
		t.Error("expected error on empty diffusion record")
	}
}

func TestRumorsDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	rDB, err := OpenRumorsDBForClan(resolver, "clan_valid")
	if err != nil {
		t.Fatalf("OpenRumorsDBForClan failed: %v", err)
	}
	defer rDB.Close()

	if _, err := OpenRumorsDBForClan(resolver, "invalid clan id!"); err == nil {
		t.Error("expected error with invalid clan ID")
	}
}
