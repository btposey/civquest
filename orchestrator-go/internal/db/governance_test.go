package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestGovernance_SchemaAndIdempotentMigration(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_alpha", "civic_registry.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_alpha")
	if err != nil {
		t.Fatalf("failed to open civic registry db: %v", err)
	}
	defer civicDB.Close()

	ctx := context.Background()

	// Verify all 4 governance tables exist
	tables := []string{
		"clan_governance",
		"civic_assemblies",
		"civic_votes",
		"civic_edicts",
	}

	for _, tbl := range tables {
		var count int
		query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?;`
		if err := civicDB.DB().QueryRowContext(ctx, query, tbl).Scan(&count); err != nil {
			t.Fatalf("failed to query table %s: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected table %s to exist, count = %d", tbl, count)
		}
	}

	// Verify indices exist
	indices := []string{
		"idx_civic_assemblies_clan",
		"idx_civic_assemblies_status",
		"idx_civic_votes_assembly",
		"idx_civic_votes_proposal",
		"idx_civic_edicts_clan",
		"idx_civic_edicts_status",
	}

	for _, idx := range indices {
		var count int
		query := `SELECT count(*) FROM sqlite_master WHERE type='index' AND name = ?;`
		if err := civicDB.DB().QueryRowContext(ctx, query, idx).Scan(&count); err != nil {
			t.Fatalf("failed to query index %s: %v", idx, err)
		}
		if count != 1 {
			t.Errorf("expected index %s to exist, count = %d", idx, count)
		}
	}

	// Test idempotent migration: calling InitSchema repeatedly should not fail
	for i := 0; i < 3; i++ {
		if err := civicDB.InitSchema(ctx); err != nil {
			t.Fatalf("idempotent InitSchema iteration %d failed: %v", i, err)
		}
	}
}

func TestGovernance_RegimeUpdates(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_beta")
	if err != nil {
		t.Fatalf("failed to open civic db: %v", err)
	}
	defer civicDB.Close()

	ctx := context.Background()

	// 1. Initial lookup returns ErrGovernanceNotFound
	_, err = civicDB.GetClanGovernance()
	if !errors.Is(err, ErrGovernanceNotFound) {
		t.Fatalf("expected ErrGovernanceNotFound on initial get, got %v", err)
	}

	// 2. Validation errors
	if err := civicDB.SetClanGovernance(nil); err == nil {
		t.Error("expected error for nil governance")
	}

	invalidRegime := &ClanGovernance{
		ClanID:     "clan_beta",
		RegimeType: "ANARCHY",
	}
	if err := civicDB.SetClanGovernance(invalidRegime); !errors.Is(err, ErrInvalidRegimeType) {
		t.Errorf("expected ErrInvalidRegimeType, got %v", err)
	}

	invalidLeaderType := &ClanGovernance{
		ClanID:     "clan_beta",
		RegimeType: RegimeTypeDemocracy,
		LeaderType: "DEITY",
	}
	if err := civicDB.SetClanGovernance(invalidLeaderType); !errors.Is(err, ErrInvalidLeaderType) {
		t.Errorf("expected ErrInvalidLeaderType, got %v", err)
	}

	invalidScore := &ClanGovernance{
		ClanID:          "clan_beta",
		RegimeType:      RegimeTypeDemocracy,
		LegitimacyScore: 150.0,
	}
	if err := civicDB.SetClanGovernance(invalidScore); !errors.Is(err, ErrInvalidLegitimacyScore) {
		t.Errorf("expected ErrInvalidLegitimacyScore, got %v", err)
	}

	// 3. Test all 4 regime types in succession per ADR-016
	regimesToTest := []struct {
		regimeType      string
		leaderID        string
		leaderType      string
		mandateGoal     string
		legitimacyScore float64
		termStartTick   int64
	}{
		{
			regimeType:      RegimeTypeDemocracy,
			leaderID:        "",
			leaderType:      LeaderTypeAgent,
			mandateGoal:     "Consensus communal farming",
			legitimacyScore: 75.0,
			termStartTick:   1000,
		},
		{
			regimeType:      RegimeTypeBenevolentMonarchy,
			leaderID:        "agent-king-aldous",
			leaderType:      LeaderTypeAgent,
			mandateGoal:     "Paved roadways and granary expansion",
			legitimacyScore: 92.5,
			termStartTick:   5000,
		},
		{
			regimeType:      RegimeTypeWarlord,
			leaderID:        "agent-warlord-valtor",
			leaderType:      LeaderTypeAgent,
			mandateGoal:     "Fortify perimeter palisades against undead",
			legitimacyScore: 88.0,
			termStartTick:   12000,
		},
		{
			regimeType:      RegimeTypeOligarchy,
			leaderID:        "agent-elder-mora",
			leaderType:      LeaderTypeAgent,
			mandateGoal:     "Defensive consolidation and resource rationing",
			legitimacyScore: 65.0,
			termStartTick:   20000,
		},
	}

	for _, rt := range regimesToTest {
		gov := &ClanGovernance{
			ClanID:           "clan_beta",
			RegimeType:       rt.regimeType,
			LeaderAgentID:    rt.leaderID,
			LeaderType:       rt.leaderType,
			MandateGoal:      rt.mandateGoal,
			LegitimacyScore:  rt.legitimacyScore,
			TermStartTick:    rt.termStartTick,
			LastElectionTick: rt.termStartTick,
		}

		if err := civicDB.SetClanGovernance(gov); err != nil {
			t.Fatalf("failed to set governance to %s: %v", rt.regimeType, err)
		}

		fetched, err := civicDB.GetClanGovernance()
		if err != nil {
			t.Fatalf("failed to get governance: %v", err)
		}

		if fetched.RegimeType != rt.regimeType {
			t.Errorf("expected regime %s, got %s", rt.regimeType, fetched.RegimeType)
		}
		if fetched.LeaderAgentID != rt.leaderID {
			t.Errorf("expected leader %s, got %s", rt.leaderID, fetched.LeaderAgentID)
		}
		if fetched.MandateGoal != rt.mandateGoal {
			t.Errorf("expected mandate %s, got %s", rt.mandateGoal, fetched.MandateGoal)
		}
		if fetched.LegitimacyScore != rt.legitimacyScore {
			t.Errorf("expected legitimacy %f, got %f", rt.legitimacyScore, fetched.LegitimacyScore)
		}
		if fetched.TermStartTick != rt.termStartTick {
			t.Errorf("expected term start tick %d, got %d", rt.termStartTick, fetched.TermStartTick)
		}
		if fetched.UpdatedAt.IsZero() {
			t.Error("expected non-zero UpdatedAt timestamp")
		}
	}

	// 4. Test Player Chieftain election (fluid player integration per ADR-016)
	playerGov := &ClanGovernance{
		ClanID:           "clan_beta",
		RegimeType:       RegimeTypeDemocracy,
		LeaderAgentID:    "player-uuid-brian",
		LeaderType:       LeaderTypePlayer,
		MandateGoal:      "Expedition to Nether fortress",
		LegitimacyScore:  95.0,
		TermStartTick:    30000,
		LastElectionTick: 30000,
	}
	if err := civicDB.SetClanGovernance(playerGov); err != nil {
		t.Fatalf("failed to set player governance: %v", err)
	}

	fetchedPlayer, err := civicDB.GetClanGovernance()
	if err != nil {
		t.Fatalf("failed to get player governance: %v", err)
	}
	if fetchedPlayer.LeaderType != LeaderTypePlayer {
		t.Errorf("expected LeaderTypePlayer, got %s", fetchedPlayer.LeaderType)
	}
	if fetchedPlayer.LeaderAgentID != "player-uuid-brian" {
		t.Errorf("expected player-uuid-brian, got %s", fetchedPlayer.LeaderAgentID)
	}

	// 5. UpdateLegitimacyScore tests
	if err := civicDB.UpdateLegitimacyScoreContext(ctx, 99.5); err != nil {
		t.Fatalf("UpdateLegitimacyScore failed: %v", err)
	}
	fetchedUpdated, err := civicDB.GetClanGovernance()
	if err != nil {
		t.Fatalf("failed to fetch updated governance: %v", err)
	}
	if fetchedUpdated.LegitimacyScore != 99.5 {
		t.Errorf("expected legitimacy 99.5, got %f", fetchedUpdated.LegitimacyScore)
	}

	if err := civicDB.UpdateLegitimacyScore(-5.0); !errors.Is(err, ErrInvalidLegitimacyScore) {
		t.Errorf("expected ErrInvalidLegitimacyScore for negative score, got %v", err)
	}
	if err := civicDB.UpdateLegitimacyScore(105.0); !errors.Is(err, ErrInvalidLegitimacyScore) {
		t.Errorf("expected ErrInvalidLegitimacyScore for score > 100, got %v", err)
	}
}

func TestGovernance_AssemblyLifecycleAndTallying(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_gamma")
	if err != nil {
		t.Fatalf("failed to open civic db: %v", err)
	}
	defer civicDB.Close()

	// 1. Initial active assembly is nil
	active, err := civicDB.GetActiveAssembly()
	if err != nil {
		t.Fatalf("GetActiveAssembly failed: %v", err)
	}
	if active != nil {
		t.Errorf("expected nil active assembly, got %+v", active)
	}

	// 2. Create assembly with validation checks
	if err := civicDB.CreateAssembly(nil); err == nil {
		t.Error("expected error for nil assembly")
	}

	invalidAssembly := &CivicAssembly{
		ID:          "",
		TriggerAxis: TriggerAxisMaterialShock,
	}
	if err := civicDB.CreateAssembly(invalidAssembly); err == nil {
		t.Error("expected error for empty assembly ID")
	}

	assembly1 := &CivicAssembly{
		ID:          "assembly-famine-2026",
		ClanID:      "clan_gamma",
		TriggerAxis: TriggerAxisMaterialShock,
		Status:      AssemblyStatusActive,
		OpenedTick:  10000,
	}
	if err := civicDB.CreateAssembly(assembly1); err != nil {
		t.Fatalf("CreateAssembly failed: %v", err)
	}

	// Duplicate ID should fail
	if err := civicDB.CreateAssembly(assembly1); !errors.Is(err, ErrDuplicateRecord) {
		t.Errorf("expected ErrDuplicateRecord on duplicate assembly ID, got %v", err)
	}

	// 3. Verify active assembly
	active, err = civicDB.GetActiveAssembly()
	if err != nil {
		t.Fatalf("GetActiveAssembly failed: %v", err)
	}
	if active == nil || active.ID != "assembly-famine-2026" {
		t.Fatalf("unexpected active assembly: %+v", active)
	}
	if active.Status != AssemblyStatusActive {
		t.Errorf("expected status %s, got %s", AssemblyStatusActive, active.Status)
	}
	if active.TriggerAxis != TriggerAxisMaterialShock {
		t.Errorf("expected trigger axis %s, got %s", TriggerAxisMaterialShock, active.TriggerAxis)
	}
	if active.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}

	// 4. Record votes
	// Voter validation
	invalidVote := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-1",
		ProposalID: "prop-granary",
		VoteWeight: -1.0,
	}
	if err := civicDB.RecordVote(invalidVote); err == nil {
		t.Error("expected error for negative vote weight")
	}

	// Vote on non-existent assembly
	badAssyVote := &CivicVote{
		AssemblyID: "non-existent",
		AgentID:    "agent-1",
		ProposalID: "prop-granary",
		VoteWeight: 1.0,
	}
	if err := civicDB.RecordVote(badAssyVote); !errors.Is(err, ErrAssemblyNotFound) {
		t.Errorf("expected ErrAssemblyNotFound, got %v", err)
	}

	// Cast valid votes
	v1 := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-farmer-1",
		VoterType:  VoterTypeAgent,
		ProposalID: "prop-granary",
		VoteWeight: 1.0,
		Rationale:  "Granary is dangerously low, we need food reserves",
		WorldTick:  10100,
	}
	if err := civicDB.RecordVote(v1); err != nil {
		t.Fatalf("RecordVote v1 failed: %v", err)
	}

	v2 := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-elder-2",
		VoterType:  VoterTypeAgent,
		ProposalID: "prop-granary",
		VoteWeight: 2.5,
		Rationale:  "Oldest settler perspective: prioritize farming",
		WorldTick:  10150,
	}
	if err := civicDB.RecordVote(v2); err != nil {
		t.Fatalf("RecordVote v2 failed: %v", err)
	}

	v3 := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-guard-3",
		VoterType:  VoterTypeAgent,
		ProposalID: "prop-palisades",
		VoteWeight: 1.5,
		Rationale:  "Hostile skeletons near farms, wall them off",
		WorldTick:  10200,
	}
	if err := civicDB.RecordVote(v3); err != nil {
		t.Fatalf("RecordVote v3 failed: %v", err)
	}

	// Player vote with high prestige weight
	vPlayer := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "player-hero-uuid",
		VoterType:  VoterTypePlayer,
		ProposalID: "prop-palisades",
		VoteWeight: 3.0,
		Rationale:  "Perimeter defense will secure all future harvests",
		WorldTick:  10250,
	}
	if err := civicDB.RecordVote(vPlayer); err != nil {
		t.Fatalf("RecordVote player failed: %v", err)
	}

	// Verify GetVote and GetAssemblyVotes
	singleVote, err := civicDB.GetVote("assembly-famine-2026", "agent-elder-2")
	if err != nil {
		t.Fatalf("GetVote failed: %v", err)
	}
	if singleVote.VoteWeight != 2.5 || singleVote.ProposalID != "prop-granary" {
		t.Errorf("unexpected single vote: %+v", singleVote)
	}

	allVotes, err := civicDB.GetAssemblyVotes("assembly-famine-2026")
	if err != nil {
		t.Fatalf("GetAssemblyVotes failed: %v", err)
	}
	if len(allVotes) != 4 {
		t.Fatalf("expected 4 votes, got %d", len(allVotes))
	}

	// 5. Tally votes before updating
	tallies, err := civicDB.TallyAssemblyVotes("assembly-famine-2026")
	if err != nil {
		t.Fatalf("TallyAssemblyVotes failed: %v", err)
	}
	if len(tallies) != 2 {
		t.Fatalf("expected 2 proposals in tally, got %d", len(tallies))
	}
	// prop-palisades: 1.5 + 3.0 = 4.5
	// prop-granary: 1.0 + 2.5 = 3.5
	if tallies[0].ProposalID != "prop-palisades" || tallies[0].TotalWeight != 4.5 || tallies[0].VoteCount != 2 {
		t.Errorf("unexpected first tally: %+v", tallies[0])
	}
	if tallies[1].ProposalID != "prop-granary" || tallies[1].TotalWeight != 3.5 || tallies[1].VoteCount != 2 {
		t.Errorf("unexpected second tally: %+v", tallies[1])
	}

	// 6. Test voter updating ballot (switching proposal and rationale)
	v1Update := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-farmer-1",
		VoterType:  VoterTypeAgent,
		ProposalID: "prop-palisades", // Switched from granary to palisades!
		VoteWeight: 1.0,
		Rationale:  "Convinced by player hero rationale",
		WorldTick:  10300,
	}
	if err := civicDB.RecordVote(v1Update); err != nil {
		t.Fatalf("RecordVote update failed: %v", err)
	}

	updatedTallies, err := civicDB.TallyAssemblyVotes("assembly-famine-2026")
	if err != nil {
		t.Fatalf("TallyAssemblyVotes after update failed: %v", err)
	}
	// prop-palisades: 1.5 + 3.0 + 1.0 = 5.5 (3 votes)
	// prop-granary: 2.5 (1 vote)
	if updatedTallies[0].ProposalID != "prop-palisades" || updatedTallies[0].TotalWeight != 5.5 || updatedTallies[0].VoteCount != 3 {
		t.Errorf("unexpected updated first tally: %+v", updatedTallies[0])
	}
	if updatedTallies[1].ProposalID != "prop-granary" || updatedTallies[1].TotalWeight != 2.5 || updatedTallies[1].VoteCount != 1 {
		t.Errorf("unexpected updated second tally: %+v", updatedTallies[1])
	}

	// 7. Conclude assembly
	if err := civicDB.ConcludeAssembly("assembly-famine-2026", "prop-palisades", 10500); err != nil {
		t.Fatalf("ConcludeAssembly failed: %v", err)
	}

	concluded, err := civicDB.GetAssembly("assembly-famine-2026")
	if err != nil {
		t.Fatalf("GetAssembly failed: %v", err)
	}
	if concluded.Status != AssemblyStatusConcluded {
		t.Errorf("expected status %s, got %s", AssemblyStatusConcluded, concluded.Status)
	}
	if concluded.WinningProposalID != "prop-palisades" {
		t.Errorf("expected winning proposal prop-palisades, got %s", concluded.WinningProposalID)
	}
	if concluded.ClosedTick != 10500 {
		t.Errorf("expected closed tick 10500, got %d", concluded.ClosedTick)
	}

	// Active assembly should now be nil
	activeAfterConclusion, err := civicDB.GetActiveAssembly()
	if err != nil {
		t.Fatalf("GetActiveAssembly after conclusion failed: %v", err)
	}
	if activeAfterConclusion != nil {
		t.Errorf("expected nil active assembly after conclusion, got %+v", activeAfterConclusion)
	}

	// Voting on concluded assembly must fail with ErrAssemblyNotActive
	lateVote := &CivicVote{
		AssemblyID: "assembly-famine-2026",
		AgentID:    "agent-late",
		VoterType:  VoterTypeAgent,
		ProposalID: "prop-palisades",
		VoteWeight: 1.0,
	}
	if err := civicDB.RecordVote(lateVote); !errors.Is(err, ErrAssemblyNotActive) {
		t.Errorf("expected ErrAssemblyNotActive for late vote, got %v", err)
	}

	// 8. Test AbandonAssembly on a second assembly
	assembly2 := &CivicAssembly{
		ID:          "assembly-crisis-2",
		ClanID:      "clan_gamma",
		TriggerAxis: TriggerAxisSecurityCrisis,
		Status:      AssemblyStatusActive,
		OpenedTick:  12000,
	}
	if err := civicDB.CreateAssembly(assembly2); err != nil {
		t.Fatalf("CreateAssembly 2 failed: %v", err)
	}

	if err := civicDB.AbandonAssembly("assembly-crisis-2", 12500); err != nil {
		t.Fatalf("AbandonAssembly failed: %v", err)
	}

	abandoned, err := civicDB.GetAssembly("assembly-crisis-2")
	if err != nil {
		t.Fatalf("GetAssembly 2 failed: %v", err)
	}
	if abandoned.Status != AssemblyStatusAbandoned {
		t.Errorf("expected status %s, got %s", AssemblyStatusAbandoned, abandoned.Status)
	}

	// 9. ListAssemblies
	assemblies, err := civicDB.ListAssemblies(10)
	if err != nil {
		t.Fatalf("ListAssemblies failed: %v", err)
	}
	if len(assemblies) != 2 {
		t.Fatalf("expected 2 assemblies in list, got %d", len(assemblies))
	}
	if assemblies[0].ID != "assembly-crisis-2" {
		t.Errorf("expected newest assembly first, got %s", assemblies[0].ID)
	}
}

func TestGovernance_EdictsAndCompliance(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_delta")
	if err != nil {
		t.Fatalf("failed to open civic db: %v", err)
	}
	defer civicDB.Close()

	// 1. Initial lookups
	_, err = civicDB.GetEdict("edict-1")
	if !errors.Is(err, ErrEdictNotFound) {
		t.Fatalf("expected ErrEdictNotFound, got %v", err)
	}

	// 2. Validation
	if err := civicDB.CreateEdict(nil); err == nil {
		t.Error("expected error for nil edict")
	}

	invalidEdict := &CivicEdict{
		ID:            "edict-bad",
		LeaderAgentID: "leader",
		EdictTitle:    "Bad Regime",
		RegimeType:    "DICTATORSHIP",
	}
	if err := civicDB.CreateEdict(invalidEdict); !errors.Is(err, ErrInvalidRegimeType) {
		t.Errorf("expected ErrInvalidRegimeType, got %v", err)
	}

	// 3. Create Benevolent Monarchy welfare edict per ADR-016
	monarchEdict := &CivicEdict{
		ID:            "edict-granary-expansion",
		ClanID:        "clan_delta",
		LeaderAgentID: "agent-king-aldous",
		RegimeType:    RegimeTypeBenevolentMonarchy,
		EdictTitle:    "Communal Granary & Bakery Expansion",
		BlueprintID:   "blueprint-granary-mk2",
		RawDecree:     "All available wood and stone reserves shall be channeled into the eastern granary wing.",
		Status:        EdictStatusActive,
		IssuedTick:    15000,
	}
	if err := civicDB.CreateEdict(monarchEdict); err != nil {
		t.Fatalf("CreateEdict failed: %v", err)
	}

	// Duplicate ID should fail
	if err := civicDB.CreateEdict(monarchEdict); !errors.Is(err, ErrDuplicateRecord) {
		t.Errorf("expected ErrDuplicateRecord, got %v", err)
	}

	// 4. Create Warlord martial edict
	warlordEdict := &CivicEdict{
		ID:            "edict-watchtower-sentry",
		ClanID:        "clan_delta",
		LeaderAgentID: "agent-warlord-valtor",
		RegimeType:    RegimeTypeWarlord,
		EdictTitle:    "Mandatory Palisade Sentry Duty",
		BlueprintID:   "blueprint-watchtower-north",
		RawDecree:     "Every able-bodied settler must man the palisade battlements at dusk.",
		Status:        EdictStatusActive,
		IssuedTick:    16000,
	}
	if err := civicDB.CreateEdict(warlordEdict); err != nil {
		t.Fatalf("CreateEdict warlord failed: %v", err)
	}

	// 5. Verify GetEdict
	fetched, err := civicDB.GetEdict("edict-granary-expansion")
	if err != nil {
		t.Fatalf("GetEdict failed: %v", err)
	}
	if fetched.EdictTitle != "Communal Granary & Bakery Expansion" {
		t.Errorf("unexpected edict title: %s", fetched.EdictTitle)
	}
	if fetched.RegimeType != RegimeTypeBenevolentMonarchy {
		t.Errorf("expected regime %s, got %s", RegimeTypeBenevolentMonarchy, fetched.RegimeType)
	}
	if fetched.ComplianceCount != 0 || fetched.DissentCount != 0 {
		t.Errorf("expected zero compliance/dissent, got %d/%d", fetched.ComplianceCount, fetched.DissentCount)
	}
	if fetched.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}

	// 6. Test RecordEdictCompliance (tracking compliance vs underground dissent per ADR-016)
	// 3 agents comply with monarch edict
	for i := 0; i < 3; i++ {
		if err := civicDB.RecordEdictCompliance("edict-granary-expansion", true); err != nil {
			t.Fatalf("RecordEdictCompliance compliant failed: %v", err)
		}
	}
	// 1 grumbling dissenter
	if err := civicDB.RecordEdictCompliance("edict-granary-expansion", false); err != nil {
		t.Fatalf("RecordEdictCompliance dissent failed: %v", err)
	}

	fetched, err = civicDB.GetEdict("edict-granary-expansion")
	if err != nil {
		t.Fatalf("GetEdict failed: %v", err)
	}
	if fetched.ComplianceCount != 3 {
		t.Errorf("expected compliance 3, got %d", fetched.ComplianceCount)
	}
	if fetched.DissentCount != 1 {
		t.Errorf("expected dissent 1, got %d", fetched.DissentCount)
	}

	// Test UpdateEdictCompliance delta
	if err := civicDB.UpdateEdictCompliance("edict-granary-expansion", 5, 2); err != nil {
		t.Fatalf("UpdateEdictCompliance failed: %v", err)
	}
	fetched, err = civicDB.GetEdict("edict-granary-expansion")
	if err != nil {
		t.Fatalf("GetEdict failed: %v", err)
	}
	if fetched.ComplianceCount != 8 || fetched.DissentCount != 3 {
		t.Errorf("expected 8 compliance / 3 dissent, got %d / %d", fetched.ComplianceCount, fetched.DissentCount)
	}

	// 7. Update status to COMPLETED and RESCINDED
	if err := civicDB.UpdateEdictStatus("edict-granary-expansion", EdictStatusCompleted); err != nil {
		t.Fatalf("UpdateEdictStatus failed: %v", err)
	}
	fetched, err = civicDB.GetEdict("edict-granary-expansion")
	if err != nil {
		t.Fatalf("GetEdict failed: %v", err)
	}
	if fetched.Status != EdictStatusCompleted {
		t.Errorf("expected status %s, got %s", EdictStatusCompleted, fetched.Status)
	}

	if err := civicDB.UpdateEdictStatus("edict-watchtower-sentry", EdictStatusRescinded); err != nil {
		t.Fatalf("UpdateEdictStatus failed: %v", err)
	}
	fetchedWarlord, err := civicDB.GetEdict("edict-watchtower-sentry")
	if err != nil {
		t.Fatalf("GetEdict warlord failed: %v", err)
	}
	if fetchedWarlord.Status != EdictStatusRescinded {
		t.Errorf("expected status %s, got %s", EdictStatusRescinded, fetchedWarlord.Status)
	}

	// 8. ListEdicts filtering
	activeEdicts, err := civicDB.ListEdicts(EdictStatusActive, 10)
	if err != nil {
		t.Fatalf("ListEdicts active failed: %v", err)
	}
	if len(activeEdicts) != 0 {
		t.Errorf("expected 0 active edicts, got %d", len(activeEdicts))
	}

	allEdicts, err := civicDB.ListEdicts("", 10)
	if err != nil {
		t.Fatalf("ListEdicts all failed: %v", err)
	}
	if len(allEdicts) != 2 {
		t.Errorf("expected 2 edicts in list, got %d", len(allEdicts))
	}

	// Non-existent operations
	if err := civicDB.UpdateEdictStatus("non-existent", EdictStatusActive); !errors.Is(err, ErrEdictNotFound) {
		t.Errorf("expected ErrEdictNotFound, got %v", err)
	}
	if err := civicDB.RecordEdictCompliance("non-existent", true); !errors.Is(err, ErrEdictNotFound) {
		t.Errorf("expected ErrEdictNotFound, got %v", err)
	}
	if err := civicDB.UpdateEdictCompliance("non-existent", 1, 1); !errors.Is(err, ErrEdictNotFound) {
		t.Errorf("expected ErrEdictNotFound, got %v", err)
	}
}

func TestGovernance_ConcurrentWritesAndIntegrity(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_concurrent")
	if err != nil {
		t.Fatalf("failed to open civic db: %v", err)
	}
	defer civicDB.Close()

	// Setup initial governance
	initGov := &ClanGovernance{
		ClanID:          "clan_concurrent",
		RegimeType:      RegimeTypeDemocracy,
		LegitimacyScore: 50.0,
		TermStartTick:   1000,
	}
	if err := civicDB.SetClanGovernance(initGov); err != nil {
		t.Fatalf("initial SetClanGovernance failed: %v", err)
	}

	// Setup an active assembly for concurrent votes
	assembly := &CivicAssembly{
		ID:          "assembly-concurrent-1",
		ClanID:      "clan_concurrent",
		TriggerAxis: TriggerAxisGoalVacuum,
		Status:      AssemblyStatusActive,
		OpenedTick:  1000,
	}
	if err := civicDB.CreateAssembly(assembly); err != nil {
		t.Fatalf("CreateAssembly failed: %v", err)
	}

	// Setup an active edict for concurrent compliance logging
	edict := &CivicEdict{
		ID:            "edict-concurrent-1",
		ClanID:        "clan_concurrent",
		LeaderAgentID: "agent-chief",
		RegimeType:    RegimeTypeDemocracy,
		EdictTitle:    "Build Granary",
		Status:        EdictStatusActive,
		IssuedTick:    1000,
	}
	if err := civicDB.CreateEdict(edict); err != nil {
		t.Fatalf("CreateEdict failed: %v", err)
	}

	const workerCount = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workerCount*4)

	// Worker group 1: Concurrent voters
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			proposalID := fmt.Sprintf("prop-%d", workerID%3)
			vote := &CivicVote{
				AssemblyID: "assembly-concurrent-1",
				AgentID:    fmt.Sprintf("agent-%d", workerID),
				VoterType:  VoterTypeAgent,
				ProposalID: proposalID,
				VoteWeight: float64(workerID + 1),
				Rationale:  fmt.Sprintf("Concurrent voter %d", workerID),
				WorldTick:  int64(1000 + workerID),
			}
			if err := civicDB.RecordVote(vote); err != nil {
				errCh <- fmt.Errorf("worker %d RecordVote: %w", workerID, err)
			}
		}(i)
	}

	// Worker group 2: Concurrent edict compliance / dissent
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			compliant := workerID%2 == 0
			if err := civicDB.RecordEdictCompliance("edict-concurrent-1", compliant); err != nil {
				errCh <- fmt.Errorf("worker %d RecordEdictCompliance: %w", workerID, err)
			}
		}(i)
	}

	// Worker group 3: Concurrent legitimacy updates
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			score := float64(50 + (workerID % 50))
			if err := civicDB.UpdateLegitimacyScore(score); err != nil {
				errCh <- fmt.Errorf("worker %d UpdateLegitimacyScore: %w", workerID, err)
			}
		}(i)
	}

	// Worker group 4: Concurrent reads
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if _, err := civicDB.GetClanGovernance(); err != nil {
				errCh <- fmt.Errorf("worker %d GetClanGovernance: %w", workerID, err)
			}
			if _, err := civicDB.GetActiveAssembly(); err != nil {
				errCh <- fmt.Errorf("worker %d GetActiveAssembly: %w", workerID, err)
			}
			if _, err := civicDB.TallyAssemblyVotes("assembly-concurrent-1"); err != nil {
				errCh <- fmt.Errorf("worker %d TallyAssemblyVotes: %w", workerID, err)
			}
			if _, err := civicDB.GetEdict("edict-concurrent-1"); err != nil {
				errCh <- fmt.Errorf("worker %d GetEdict: %w", workerID, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}

	// Verify vote count
	votes, err := civicDB.GetAssemblyVotes("assembly-concurrent-1")
	if err != nil {
		t.Fatalf("GetAssemblyVotes failed: %v", err)
	}
	if len(votes) != workerCount {
		t.Errorf("expected %d votes, got %d", workerCount, len(votes))
	}

	// Verify edict compliance count
	finalEdict, err := civicDB.GetEdict("edict-concurrent-1")
	if err != nil {
		t.Fatalf("GetEdict failed: %v", err)
	}
	expectedCompliant := workerCount / 2
	expectedDissent := workerCount - expectedCompliant
	if finalEdict.ComplianceCount != expectedCompliant || finalEdict.DissentCount != expectedDissent {
		t.Errorf("expected %d compliant / %d dissent, got %d / %d",
			expectedCompliant, expectedDissent, finalEdict.ComplianceCount, finalEdict.DissentCount)
	}
}

func TestGovernance_ClosedDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "civic.db")

	civicDB, err := OpenCivicRegistryDB(dbPath, "clan_closed")
	if err != nil {
		t.Fatalf("failed to open civic db: %v", err)
	}

	// Close database
	if err := civicDB.Close(); err != nil {
		t.Fatalf("failed to close civic db: %v", err)
	}

	// Verify all governance operations return ErrClosed
	if _, err := civicDB.GetClanGovernance(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetClanGovernance, got %v", err)
	}
	if err := civicDB.SetClanGovernance(&ClanGovernance{RegimeType: RegimeTypeDemocracy}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on SetClanGovernance, got %v", err)
	}
	if err := civicDB.UpdateLegitimacyScore(50.0); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on UpdateLegitimacyScore, got %v", err)
	}

	if err := civicDB.CreateAssembly(&CivicAssembly{ID: "a", TriggerAxis: "t"}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on CreateAssembly, got %v", err)
	}
	if _, err := civicDB.GetAssembly("a"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetAssembly, got %v", err)
	}
	if _, err := civicDB.GetActiveAssembly(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetActiveAssembly, got %v", err)
	}
	if _, err := civicDB.ListAssemblies(10); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListAssemblies, got %v", err)
	}
	if err := civicDB.ConcludeAssembly("a", "p", 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ConcludeAssembly, got %v", err)
	}
	if err := civicDB.AbandonAssembly("a", 100); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on AbandonAssembly, got %v", err)
	}

	if err := civicDB.RecordVote(&CivicVote{AssemblyID: "a", AgentID: "1", ProposalID: "p"}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on RecordVote, got %v", err)
	}
	if _, err := civicDB.GetAssemblyVotes("a"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetAssemblyVotes, got %v", err)
	}
	if _, err := civicDB.GetVote("a", "1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetVote, got %v", err)
	}
	if _, err := civicDB.TallyAssemblyVotes("a"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on TallyAssemblyVotes, got %v", err)
	}

	if err := civicDB.CreateEdict(&CivicEdict{ID: "e", LeaderAgentID: "l", EdictTitle: "t", RegimeType: RegimeTypeDemocracy}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on CreateEdict, got %v", err)
	}
	if _, err := civicDB.GetEdict("e"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetEdict, got %v", err)
	}
	if _, err := civicDB.ListEdicts("", 10); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListEdicts, got %v", err)
	}
	if err := civicDB.UpdateEdictStatus("e", EdictStatusCompleted); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on UpdateEdictStatus, got %v", err)
	}
	if err := civicDB.RecordEdictCompliance("e", true); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on RecordEdictCompliance, got %v", err)
	}
	if err := civicDB.UpdateEdictCompliance("e", 1, 1); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on UpdateEdictCompliance, got %v", err)
	}
}
