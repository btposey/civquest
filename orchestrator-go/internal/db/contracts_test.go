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

func TestContractsDB_LifecycleAndMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clans", "clan_agr_test", "contracts.db")

	contractsDB, err := OpenContractsDB(dbPath, "clan_agr_test")
	if err != nil {
		t.Fatalf("failed to open contracts database: %v", err)
	}
	defer contractsDB.Close()

	if contractsDB.ClanID() != "clan_agr_test" {
		t.Errorf("expected clan ID clan_agr_test, got %q", contractsDB.ClanID())
	}
	if contractsDB.Path() != dbPath {
		t.Errorf("expected path %q, got %q", dbPath, contractsDB.Path())
	}
	if contractsDB.DB() == nil {
		t.Fatal("expected non-nil *sql.DB handle")
	}

	ctx := context.Background()

	var count int
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name = 'sharecropping_contracts';`
	if err := contractsDB.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("failed to query table count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 table, found %d", count)
	}
}

func TestContractsDB_OpenForClan(t *testing.T) {
	tempRoot := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempRoot))

	contractsDB, err := OpenContractsDBForClan(resolver, "clan_farmers")
	if err != nil {
		t.Fatalf("OpenContractsDBForClan failed: %v", err)
	}
	defer contractsDB.Close()

	expectedPath := resolver.ClanContractsDBPath("clan_farmers")
	if contractsDB.Path() != expectedPath {
		t.Errorf("expected %s, got %s", expectedPath, contractsDB.Path())
	}

	if _, err := OpenContractsDBForClan(resolver, "invalid/clan"); err == nil {
		t.Error("expected error for invalid clan ID")
	}
}

func TestContractsDB_CRUDAndFiltering(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "contracts.db")

	contractsDB, err := OpenContractsDB(dbPath, "clan_sharecropping")
	if err != nil {
		t.Fatalf("failed to open contracts database: %v", err)
	}
	defer contractsDB.Close()

	// 1. Initial lookup is nil
	c, err := contractsDB.GetContract("cnt-1")
	if err != nil || c != nil {
		t.Fatalf("expected nil for nonexistent contract, got %v (err: %v)", c, err)
	}

	// 2. Create contracts
	c1 := &SharecroppingContract{
		ContractID:         "cnt-wheat-1",
		PlayerUUID:         "player-uuid-1",
		PlayerName:         "FarmerBob",
		SettlerAgentID:     "agent-agronomist-1",
		PlotMinX:           10,
		PlotMinY:           64,
		PlotMinZ:           10,
		PlotMaxX:           30,
		PlotMaxY:           65,
		PlotMaxZ:           30,
		ChestX:             10,
		ChestY:             64,
		ChestZ:             10,
		SplitPlayerPercent: 60.0,
		SplitClanPercent:   40.0,
		CropType:           "minecraft:wheat",
		Status:             ContractStatusActive,
		CreatedTick:        1000,
		ExpiresTick:        25000,
	}

	c2 := &SharecroppingContract{
		ContractID:         "cnt-carrot-2",
		PlayerUUID:         "player-uuid-1",
		PlayerName:         "FarmerBob",
		SettlerAgentID:     "agent-agronomist-2",
		PlotMinX:           40,
		PlotMinY:           64,
		PlotMinZ:           10,
		PlotMaxX:           60,
		PlotMaxY:           65,
		PlotMaxZ:           30,
		ChestX:             40,
		ChestY:             64,
		ChestZ:             10,
		SplitPlayerPercent: 70.0,
		SplitClanPercent:   30.0,
		CropType:           "minecraft:carrots",
		Status:             ContractStatusActive,
		CreatedTick:        1500,
		ExpiresTick:        30000,
	}

	c3 := &SharecroppingContract{
		ContractID:         "cnt-potato-3",
		PlayerUUID:         "player-uuid-2",
		PlayerName:         "Alice",
		SettlerAgentID:     "agent-agronomist-1",
		PlotMinX:           70,
		PlotMinY:           64,
		PlotMinZ:           10,
		PlotMaxX:           90,
		PlotMaxY:           65,
		PlotMaxZ:           30,
		ChestX:             70,
		ChestY:             64,
		ChestZ:             10,
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		CropType:           "minecraft:potatoes",
		Status:             ContractStatusActive,
		CreatedTick:        2000,
		ExpiresTick:        35000,
	}

	if err := contractsDB.CreateContract(c1); err != nil {
		t.Fatalf("CreateContract c1 failed: %v", err)
	}
	if err := contractsDB.CreateContract(c2); err != nil {
		t.Fatalf("CreateContract c2 failed: %v", err)
	}
	if err := contractsDB.CreateContract(c3); err != nil {
		t.Fatalf("CreateContract c3 failed: %v", err)
	}

	// 3. GetContract
	got, err := contractsDB.GetContract("cnt-wheat-1")
	if err != nil || got == nil {
		t.Fatalf("GetContract failed: %v", err)
	}
	if got.PlayerName != "FarmerBob" || got.SplitPlayerPercent != 60.0 || got.SplitClanPercent != 40.0 {
		t.Errorf("unexpected contract data: %+v", got)
	}

	// 4. Update status
	if err := contractsDB.UpdateContractStatus("cnt-wheat-1", ContractStatusFulfilled); err != nil {
		t.Fatalf("UpdateContractStatus failed: %v", err)
	}
	got, _ = contractsDB.GetContract("cnt-wheat-1")
	if got.Status != ContractStatusFulfilled {
		t.Errorf("expected FULFILLED status, got %s", got.Status)
	}

	// Update nonexistent
	if err := contractsDB.UpdateContractStatus("nonexistent", ContractStatusTerminated); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent contract, got %v", err)
	}

	// 5. ListContractsForPlayer (player-uuid-1 should have 2 contracts, c2 newest first then c1)
	p1Contracts, err := contractsDB.ListContractsForPlayer("player-uuid-1")
	if err != nil {
		t.Fatalf("ListContractsForPlayer failed: %v", err)
	}
	if len(p1Contracts) != 2 {
		t.Fatalf("expected 2 contracts for player-uuid-1, got %d", len(p1Contracts))
	}
	if p1Contracts[0].ContractID != "cnt-carrot-2" || p1Contracts[1].ContractID != "cnt-wheat-1" {
		t.Errorf("unexpected player contract ordering: %+v", p1Contracts)
	}

	// 6. ListContractsForAgent (agent-agronomist-1 should have 2 contracts: c3 newest then c1)
	agentContracts, err := contractsDB.ListContractsForAgent("agent-agronomist-1")
	if err != nil {
		t.Fatalf("ListContractsForAgent failed: %v", err)
	}
	if len(agentContracts) != 2 {
		t.Fatalf("expected 2 contracts for agent-agronomist-1, got %d", len(agentContracts))
	}
	if agentContracts[0].ContractID != "cnt-potato-3" || agentContracts[1].ContractID != "cnt-wheat-1" {
		t.Errorf("unexpected agent contract ordering: %+v", agentContracts)
	}

	// 7. ListContracts (all 3, newest first)
	all, err := contractsDB.ListContracts()
	if err != nil {
		t.Fatalf("ListContracts failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total contracts, got %d", len(all))
	}
	if all[0].ContractID != "cnt-potato-3" || all[1].ContractID != "cnt-carrot-2" || all[2].ContractID != "cnt-wheat-1" {
		t.Errorf("unexpected all contracts ordering: %+v", all)
	}
}

func TestContractsDB_ValidationsAndClosed(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "val_contracts.db")

	contractsDB, err := OpenContractsDB(dbPath, "clan_val")
	if err != nil {
		t.Fatalf("failed to open contracts database: %v", err)
	}

	// 1. Invalid split percentages (sum != 100%)
	badSplit := &SharecroppingContract{
		ContractID:         "bad-split",
		PlayerUUID:         "p1",
		SettlerAgentID:     "a1",
		CropType:           "wheat",
		SplitPlayerPercent: 60.0,
		SplitClanPercent:   50.0, // 110%
	}
	if err := contractsDB.CreateContract(badSplit); !errors.Is(err, ErrInvalidSplitPercent) {
		t.Errorf("expected ErrInvalidSplitPercent, got %v", err)
	}

	// 2. Invalid coordinates (min > max)
	badCoords := &SharecroppingContract{
		ContractID:         "bad-coords",
		PlayerUUID:         "p1",
		SettlerAgentID:     "a1",
		CropType:           "wheat",
		PlotMinX:           100,
		PlotMaxX:           50, // min > max
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
	}
	if err := contractsDB.CreateContract(badCoords); !errors.Is(err, ErrInvalidCoordinates) {
		t.Errorf("expected ErrInvalidCoordinates, got %v", err)
	}

	// 3. Empty fields
	if err := contractsDB.CreateContract(&SharecroppingContract{ContractID: ""}); err == nil {
		t.Error("expected error for empty contract_id")
	}
	if err := contractsDB.CreateContract(&SharecroppingContract{ContractID: "1", PlayerUUID: ""}); err == nil {
		t.Error("expected error for empty player_uuid")
	}

	// 4. Closed DB
	if err := contractsDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	validContract := &SharecroppingContract{
		ContractID:         "c-valid",
		PlayerUUID:         "p1",
		SettlerAgentID:     "a1",
		CropType:           "wheat",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
	}
	if err := contractsDB.CreateContract(validContract); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on CreateContract, got %v", err)
	}
	if err := contractsDB.UpdateContractStatus("c-valid", ContractStatusFulfilled); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on UpdateContractStatus, got %v", err)
	}
	if _, err := contractsDB.GetContract("c-valid"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on GetContract, got %v", err)
	}
	if _, err := contractsDB.ListContractsForPlayer("p1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListContractsForPlayer, got %v", err)
	}
	if _, err := contractsDB.ListContractsForAgent("a1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListContractsForAgent, got %v", err)
	}
	if _, err := contractsDB.ListContracts(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListContracts, got %v", err)
	}
	if err := contractsDB.ReassignContractSettler("c-valid", "agent-bob"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ReassignContractSettler, got %v", err)
	}
	if _, err := contractsDB.ListContractsByStatus(ContractStatusActive); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListContractsByStatus, got %v", err)
	}
	if _, err := contractsDB.ListActiveContractsForAgent("a1"); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed on ListActiveContractsForAgent, got %v", err)
	}
}

func TestContractsDB_Remediations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "contracts_remediations.db")

	contractsDB, err := OpenContractsDB(dbPath, "clan_remediate")
	if err != nil {
		t.Fatalf("OpenContractsDB failed: %v", err)
	}
	defer contractsDB.Close()

	// 1. Issue 1: NaN/Inf validation in CreateContract
	nanSplit1 := &SharecroppingContract{
		ContractID:         "c-nan-1",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-agronomist-1",
		CropType:           "wheat",
		SplitPlayerPercent: math.NaN(),
		SplitClanPercent:   50.0,
	}
	if err := contractsDB.CreateContract(nanSplit1); !errors.Is(err, ErrInvalidSplitPercent) {
		t.Errorf("expected ErrInvalidSplitPercent for NaN SplitPlayerPercent, got: %v", err)
	}

	nanSplit2 := &SharecroppingContract{
		ContractID:         "c-nan-2",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-agronomist-1",
		CropType:           "wheat",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   math.NaN(),
	}
	if err := contractsDB.CreateContract(nanSplit2); !errors.Is(err, ErrInvalidSplitPercent) {
		t.Errorf("expected ErrInvalidSplitPercent for NaN SplitClanPercent, got: %v", err)
	}

	infSplit := &SharecroppingContract{
		ContractID:         "c-inf",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-agronomist-1",
		CropType:           "wheat",
		SplitPlayerPercent: math.Inf(1),
		SplitClanPercent:   50.0,
	}
	if err := contractsDB.CreateContract(infSplit); !errors.Is(err, ErrInvalidSplitPercent) {
		t.Errorf("expected ErrInvalidSplitPercent for Inf SplitPlayerPercent, got: %v", err)
	}

	// 2. Issue 4: ContractStatus validation
	badStatusContract := &SharecroppingContract{
		ContractID:         "c-bad-status",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-agronomist-1",
		CropType:           "wheat",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		Status:             "FOO_BAR",
	}
	if err := contractsDB.CreateContract(badStatusContract); err == nil {
		t.Error("expected error for invalid contract status in CreateContract")
	}

	// Valid Proposed Contract
	proposedContract := &SharecroppingContract{
		ContractID:         "c-proposed",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-agronomist-1",
		CropType:           "wheat",
		SplitPlayerPercent: 60.0,
		SplitClanPercent:   40.0,
		Status:             ContractStatusProposed,
		CreatedTick:        100,
	}
	if err := contractsDB.CreateContract(proposedContract); err != nil {
		t.Fatalf("expected ContractStatusProposed to be valid, got: %v", err)
	}

	// Invalid status in UpdateContractStatus
	if err := contractsDB.UpdateContractStatus("c-proposed", "INVALID_STATUS"); err == nil {
		t.Error("expected error for invalid status in UpdateContractStatus")
	}

	// 3. Issue 7: Contract Permadeath Handoff - ReassignContractSettler
	// Invalid agent name (contains invalid path chars)
	if err := contractsDB.ReassignContractSettler("c-proposed", "../malicious"); err == nil {
		t.Error("expected error for invalid agent name")
	}
	if err := contractsDB.ReassignContractSettler("c-proposed", ""); err == nil {
		t.Error("expected error for empty agent name")
	}

	// Nonexistent contract
	if err := contractsDB.ReassignContractSettler("c-nonexistent", "agent-successor-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent contract reassignment, got: %v", err)
	}

	// Valid reassignment
	if err := contractsDB.ReassignContractSettler("c-proposed", "agent-successor-1"); err != nil {
		t.Fatalf("ReassignContractSettler failed: %v", err)
	}
	updated, err := contractsDB.GetContract("c-proposed")
	if err != nil || updated.SettlerAgentID != "agent-successor-1" {
		t.Fatalf("expected settler agent 'agent-successor-1', got: %+v", updated)
	}

	// 4. Issue 8: Contract Filtering (ListContractsByStatus & ListActiveContractsForAgent)
	cActive1 := &SharecroppingContract{
		ContractID:         "c-act-1",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-successor-1",
		CropType:           "carrots",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		Status:             ContractStatusActive,
		CreatedTick:        200,
	}
	cActive2 := &SharecroppingContract{
		ContractID:         "c-act-2",
		PlayerUUID:         "p2",
		SettlerAgentID:     "agent-successor-1",
		CropType:           "potatoes",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		Status:             ContractStatusActive,
		CreatedTick:        300,
	}
	cOtherAgentActive := &SharecroppingContract{
		ContractID:         "c-other-act",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-other-2",
		CropType:           "wheat",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		Status:             ContractStatusActive,
		CreatedTick:        250,
	}
	cBreached := &SharecroppingContract{
		ContractID:         "c-breached",
		PlayerUUID:         "p1",
		SettlerAgentID:     "agent-successor-1",
		CropType:           "wheat",
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		Status:             ContractStatusBreached,
		CreatedTick:        150,
	}
	for _, c := range []*SharecroppingContract{cActive1, cActive2, cOtherAgentActive, cBreached} {
		if err := contractsDB.CreateContract(c); err != nil {
			t.Fatalf("CreateContract failed: %v", err)
		}
	}

	// ListContractsByStatus
	activeContracts, err := contractsDB.ListContractsByStatus(ContractStatusActive)
	if err != nil {
		t.Fatalf("ListContractsByStatus failed: %v", err)
	}
	if len(activeContracts) != 3 {
		t.Fatalf("expected 3 active contracts, got %d", len(activeContracts))
	}
	// Ordered newest first: c-act-2 (300), c-other-act (250), c-act-1 (200)
	if activeContracts[0].ContractID != "c-act-2" || activeContracts[1].ContractID != "c-other-act" || activeContracts[2].ContractID != "c-act-1" {
		t.Errorf("unexpected ordering for active contracts: %+v", activeContracts)
	}

	// Invalid status query in ListContractsByStatus
	if _, err := contractsDB.ListContractsByStatus("INVALID"); err == nil {
		t.Error("expected error for invalid status query in ListContractsByStatus")
	}

	// ListActiveContractsForAgent
	successorActive, err := contractsDB.ListActiveContractsForAgent("agent-successor-1")
	if err != nil {
		t.Fatalf("ListActiveContractsForAgent failed: %v", err)
	}
	if len(successorActive) != 2 {
		t.Fatalf("expected 2 active contracts for agent-successor-1, got %d", len(successorActive))
	}
	// Newest first: c-act-2 then c-act-1 (c-breached and c-proposed are not ACTIVE)
	if successorActive[0].ContractID != "c-act-2" || successorActive[1].ContractID != "c-act-1" {
		t.Errorf("unexpected ordering for agent active contracts: %+v", successorActive)
	}

	// Empty agent ID
	if _, err := contractsDB.ListActiveContractsForAgent(""); err == nil {
		t.Error("expected error for empty agent ID")
	}
}

func TestContractsDB_ConcurrentOperations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent_contracts.db")

	contractsDB, err := OpenContractsDB(dbPath, "clan_concurrent")
	if err != nil {
		t.Fatalf("failed to open contracts database: %v", err)
	}
	defer contractsDB.Close()

	const count = 20
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			c := &SharecroppingContract{
				ContractID:         "cnt-test-" + string(rune('a'+idx)),
				PlayerUUID:         "p-concurrent",
				SettlerAgentID:     "a-concurrent",
				CropType:           "wheat",
				SplitPlayerPercent: 50.0,
				SplitClanPercent:   50.0,
				CreatedTick:        int64(100 + idx),
			}
			_ = contractsDB.CreateContract(c)
			_ = contractsDB.UpdateContractStatus(c.ContractID, ContractStatusActive)
		}(i)
	}

	wg.Wait()

	contracts, err := contractsDB.ListContractsForPlayer("p-concurrent")
	if err != nil {
		t.Fatalf("ListContractsForPlayer failed: %v", err)
	}
	if len(contracts) != count {
		t.Errorf("expected %d contracts created concurrently, got %d", count, len(contracts))
	}
}
