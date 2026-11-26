package integration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
)

// feat.clan_coordination governance smoke (cq-71l.9): several agents deliberate in a council
// (assembly + weighted votes), the winning proposal is recorded in the clan civic registry, the
// clan makes a diplomatic treaty, and the System 2 strategic ledger that agents reason from
// observes both decisions.
func TestIntegration_Clan_CouncilVoteAndDiplomacyObservedInClanDBs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	civic, err := db.OpenCivicRegistryDB(filepath.Join(dir, "civic.db"), "clan_alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer civic.Close()
	diplo, err := db.OpenDiplomacyDB(filepath.Join(dir, "diplomacy.db"), "clan_alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer diplo.Close()

	if err := civic.SetClanGovernance(&db.ClanGovernance{ClanID: "clan_alpha", RegimeType: db.RegimeTypeDemocracy,
		LeaderType: db.LeaderTypeAgent, MandateGoal: "Survive the winter", LegitimacyScore: 70, TermStartTick: 0}); err != nil {
		t.Fatal(err)
	}

	if err := civic.CreateAssembly(&db.CivicAssembly{ID: "asm-1", ClanID: "clan_alpha",
		TriggerAxis: "MATERIAL_SHOCK", Status: db.AssemblyStatusActive, OpenedTick: 1000}); err != nil {
		t.Fatal(err)
	}
	ballots := []struct{ agent, proposal string }{
		{"alder", "build_granary"}, {"birch", "build_granary"}, {"cedar", "raid_neighbor"},
	}
	for i, b := range ballots {
		if err := civic.RecordVote(&db.CivicVote{AssemblyID: "asm-1", AgentID: b.agent, VoterType: db.VoterTypeAgent,
			ProposalID: b.proposal, VoteWeight: 1, Rationale: "smoke", WorldTick: int64(1100 + i)}); err != nil {
			t.Fatalf("vote %s: %v", b.agent, err)
		}
	}
	tallies, err := civic.TallyAssemblyVotes("asm-1")
	if err != nil || len(tallies) == 0 {
		t.Fatalf("tally: %v %v", tallies, err)
	}
	winner := tallies[0]
	for _, tl := range tallies {
		if tl.TotalWeight > winner.TotalWeight {
			winner = tl
		}
	}
	if winner.ProposalID != "build_granary" || winner.VoteCount != 2 {
		t.Fatalf("council should pick the 2-vote granary: %+v", tallies)
	}
	if err := civic.ConcludeAssembly("asm-1", winner.ProposalID, 1200); err != nil {
		t.Fatal(err)
	}
	asm, err := civic.GetAssembly("asm-1")
	if err != nil || asm.Status != db.AssemblyStatusConcluded || asm.WinningProposalID != "build_granary" {
		t.Fatalf("decision not persisted in clan civic DB: %+v err=%v", asm, err)
	}
	if active, _ := civic.GetActiveAssembly(); active != nil {
		t.Fatalf("no assembly should remain active: %+v", active)
	}

	// Diplomacy decision: warm relations, then a free-trade treaty.
	if err := diplo.SetAffinity("clan_beta", 60, 1300); err != nil {
		t.Fatal(err)
	}
	if err := diplo.SetTreaty("clan_beta", db.TreatyFreeTrade, 24000, 1300); err != nil {
		t.Fatal(err)
	}

	ledger, err := system2.AssembleStrategicLedger(ctx, nil, civic, nil, diplo, nil,
		system2.PersonalProfile{AgentID: "alder", ClanID: "clan_alpha", Profession: "BUILDER"}, 1400)
	if err != nil {
		t.Fatal(err)
	}
	d := ledger.Diplomacy()
	if d.ClanAffinities["clan_beta"] != 60 {
		t.Fatalf("ledger affinity: %+v", d.ClanAffinities)
	}
	if len(d.RecentTreaties) != 1 {
		t.Fatalf("ledger must surface the FREE_TRADE treaty: %+v", d.RecentTreaties)
	}
}
