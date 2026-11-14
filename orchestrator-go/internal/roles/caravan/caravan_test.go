package caravan

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Helper to setup test environment with source and partner clan databases.
func setupCaravanTestEnv(t *testing.T) (*CaravanManager, *db.CivicRegistryDB, *db.DiplomacyDB, *db.CivicRegistryDB, *db.DiplomacyDB) {
	t.Helper()
	tempDir := t.TempDir()

	sourceCivicPath := filepath.Join(tempDir, "clans", "clan_pioneers", "civic_registry.db")
	sourceCivic, err := db.OpenCivicRegistryDB(sourceCivicPath, "clan_pioneers")
	if err != nil {
		t.Fatalf("failed to open source civic db: %v", err)
	}

	sourceDiploPath := filepath.Join(tempDir, "clans", "clan_pioneers", "diplomacy.db")
	sourceDiplo, err := db.OpenDiplomacyDB(sourceDiploPath, "clan_pioneers")
	if err != nil {
		t.Fatalf("failed to open source diplo db: %v", err)
	}

	partnerCivicPath := filepath.Join(tempDir, "clans", "clan_iron_haven", "civic_registry.db")
	partnerCivic, err := db.OpenCivicRegistryDB(partnerCivicPath, "clan_iron_haven")
	if err != nil {
		t.Fatalf("failed to open partner civic db: %v", err)
	}

	partnerDiploPath := filepath.Join(tempDir, "clans", "clan_iron_haven", "diplomacy.db")
	partnerDiplo, err := db.OpenDiplomacyDB(partnerDiploPath, "clan_iron_haven")
	if err != nil {
		t.Fatalf("failed to open partner diplo db: %v", err)
	}

	t.Cleanup(func() {
		sourceCivic.Close()
		sourceDiplo.Close()
		partnerCivic.Close()
		partnerDiplo.Close()
	})

	manager, err := NewCaravanManager(CaravanManagerConfig{
		SourceClanID:         "clan_pioneers",
		CaravanMasterAgentID: "agent_caravan_master",
		SourceCivicDB:        sourceCivic,
		SourceDiplomacyDB:    sourceDiplo,
		PartnerCivicDB:       partnerCivic,
		PartnerDiplomacyDB:   partnerDiplo,
		SurplusThreshold:     32,
		DeficitThreshold:     10,
	})
	if err != nil {
		t.Fatalf("failed to create caravan manager: %v", err)
	}

	return manager, sourceCivic, sourceDiplo, partnerCivic, partnerDiplo
}

func TestIsPackBeast(t *testing.T) {
	validBeasts := []string{
		"minecraft:mule", "mule",
		"minecraft:donkey", "donkey",
		"minecraft:llama", "llama",
		"minecraft:trader_llama", "trader_llama",
	}
	for _, b := range validBeasts {
		if !IsPackBeast(b) {
			t.Errorf("expected %q to be recognized as pack beast", b)
		}
	}

	invalidBeasts := []string{
		"minecraft:player", "minecraft:zombie", "minecraft:cow",
		"minecraft:sheep", "minecraft:horse", "minecraft:pig",
	}
	for _, b := range invalidBeasts {
		if IsPackBeast(b) {
			t.Errorf("expected %q NOT to be recognized as pack beast", b)
		}
	}
}

func TestCheckEconomicConditions(t *testing.T) {
	mgr, sourceCivic, sourceDiplo, partnerCivic, _ := setupCaravanTestEnv(t)
	ctx := context.Background()

	commodity := "minecraft:bread"
	targetClan := "clan_iron_haven"

	// 1. Initial state: no stock -> ineligible
	res, err := mgr.CheckEconomicConditions(ctx, targetClan, commodity, 1000)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected ineligible with 0 stock")
	}

	// 2. Add stock above surplus threshold (64 > 32), but 0 duration elapsed
	_ = sourceCivic.AddGranaryStockContext(ctx, commodity, 64, 1000)
	res, err = mgr.CheckEconomicConditions(ctx, targetClan, commodity, 1000)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected ineligible because surplus was not sustained for >10 days")
	}

	// 3. Fast-forward world tick to satisfy >10 days (240,000 ticks)
	futureTick := int64(1000 + MinSurplusDurationTicks + 100)
	res, err = mgr.CheckEconomicConditions(ctx, targetClan, commodity, futureTick)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if !res.Eligible {
		t.Errorf("expected eligible after 10 days of surplus, got: %s", res.Reason)
	}

	// 4. Test partner clan affinity too low (< -20.0) -> blocks expedition
	_ = sourceDiplo.SetAffinity(targetClan, -30.0, futureTick) // score is now -30.0 (SUSPICIOUS)
	res, err = mgr.CheckEconomicConditions(ctx, targetClan, commodity, futureTick)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected ineligible due to low partner affinity (-30.0 < -20.0)")
	}

	// Reset affinity to FRIENDLY (+25.0 >= -20.0)
	_ = sourceDiplo.SetAffinity(targetClan, 55.0, futureTick) // score is now +25.0

	// 5. Test partner clan not in deficit (stock >= deficit threshold 10)
	_ = partnerCivic.AddGranaryStockContext(ctx, commodity, 20, futureTick)
	res, err = mgr.CheckEconomicConditions(ctx, targetClan, commodity, futureTick)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected ineligible when partner has no deficit (stock 20 >= 10)")
	}

	// Consume partner stock so deficit is restored
	_ = partnerCivic.ConsumeGranaryStockContext(ctx, commodity, 15, futureTick) // 5 remaining < 10
	res, err = mgr.CheckEconomicConditions(ctx, targetClan, commodity, futureTick)
	if err != nil {
		t.Fatalf("CheckEconomicConditions failed: %v", err)
	}
	if !res.Eligible {
		t.Errorf("expected eligible after partner deficit restored, got: %s", res.Reason)
	}
}

func TestPlanPavedRoute(t *testing.T) {
	// Build graph with 4 nodes:
	// n1 (0,64,0) -> n2 (50,64,0) -> n3 (100,64,0) (PAVED)
	// n1 -> n4 (25,64,25) -> n3 (UNPAVED shortcut)
	nodes := []db.MapNode{
		{ID: "n1", X: 0, Y: 64, Z: 0, Label: "Home Granary"},
		{ID: "n2", X: 50, Y: 64, Z: 0, Label: "Paved Crossroads"},
		{ID: "n3", X: 100, Y: 64, Z: 0, Label: "Foreign Market"},
		{ID: "n4", X: 25, Y: 64, Z: 25, Label: "Wilderness Dirt Path"},
	}

	edges := []db.MapEdge{
		{ID: "e-1-2", FromNodeID: "n1", ToNodeID: "n2", Distance: 50.0, IsPaved: true},
		{ID: "e-2-3", FromNodeID: "n2", ToNodeID: "n3", Distance: 50.0, IsPaved: true},
		{ID: "e-1-4", FromNodeID: "n1", ToNodeID: "n4", Distance: 35.0, IsPaved: false}, // Unpaved shortcut
		{ID: "e-4-3", FromNodeID: "n4", ToNodeID: "n3", Distance: 75.0, IsPaved: false}, // Unpaved
	}

	// 1. Should follow n1 -> n2 -> n3 strictly along paved edges
	route, err := PlanPavedRoute(nodes, edges, "n1", "n3")
	if err != nil {
		t.Fatalf("PlanPavedRoute failed: %v", err)
	}
	if len(route) != 3 {
		t.Fatalf("expected 3 waypoints, got %d", len(route))
	}
	if route[0].ID != "n1" || route[1].ID != "n2" || route[2].ID != "n3" {
		t.Errorf("unexpected route: %v -> %v -> %v", route[0].ID, route[1].ID, route[2].ID)
	}

	// 2. Disconnected paved network should fail with ErrNoPavedPath
	edgesNoPaved := []db.MapEdge{
		{ID: "e-1-2", FromNodeID: "n1", ToNodeID: "n2", Distance: 50.0, IsPaved: false},
		{ID: "e-2-3", FromNodeID: "n2", ToNodeID: "n3", Distance: 50.0, IsPaved: false},
	}
	_, err = PlanPavedRoute(nodes, edgesNoPaved, "n1", "n3")
	if err != ErrNoPavedPath {
		t.Errorf("expected ErrNoPavedPath, got %v", err)
	}
}

func TestFormExpedition_LeashingAndCargo(t *testing.T) {
	mgr, sourceCivic, sourceDiplo, _, _ := setupCaravanTestEnv(t)
	ctx := context.Background()

	commodity := "minecraft:bread"
	targetClan := "clan_iron_haven"

	// Setup stock and sustained surplus
	_ = sourceCivic.AddGranaryStockContext(ctx, commodity, 100, 1000)
	mgr.SurplusTracker().SetSurplusStartTime("clan_pioneers", commodity, 1000)
	_ = sourceDiplo.SetAffinity(targetClan, 10.0, 1000) // NEUTRAL

	depTick := int64(1000 + MinSurplusDurationTicks + 100)

	packBeasts := []*civquestv1.VisibleEntity{
		{
			EntityId:   101,
			EntityType: "minecraft:mule",
		},
		{
			EntityId:   102,
			EntityType: "minecraft:llama",
		},
		{
			EntityId:   103,
			EntityType: "minecraft:cow", // should be ignored
		},
	}

	nodes := []db.MapNode{
		{ID: "node-home", X: 0, Y: 64, Z: 0},
		{ID: "node-foreign", X: 100, Y: 64, Z: 100},
	}
	edges := []db.MapEdge{
		{ID: "edge-1", FromNodeID: "node-home", ToNodeID: "node-foreign", Distance: 141.4, IsPaved: true},
	}

	req := FormExpeditionRequest{
		ExpeditionID:   "exp-alpha-001",
		TargetClanID:   targetClan,
		CargoCommodity: commodity,
		CargoQuantity:  32,
		GuardAgentIDs:  []string{"agent_guard_1", "agent_guard_2"},
		PackBeasts:     packBeasts,
		GraphNodes:     nodes,
		GraphEdges:     edges,
		StartNodeID:    "node-home",
		TargetNodeID:   "node-foreign",
		DepartureTick:  depTick,
		LocalRumors:    []string{"Pioneers settlement thrives with strong wheat harvests."},
	}

	exp, intents, err := mgr.FormExpedition(ctx, req)
	if err != nil {
		t.Fatalf("FormExpedition failed: %v", err)
	}

	// Check expedition state
	if exp.ID != "exp-alpha-001" || exp.Status != ExpeditionStatusInTransit {
		t.Errorf("unexpected expedition state: %+v", exp)
	}
	if len(exp.GuardAgentIDs) != 2 {
		t.Errorf("expected 2 guards, got %d", len(exp.GuardAgentIDs))
	}
	if len(exp.PackBeasts) != 2 {
		t.Errorf("expected 2 pack beasts (mule and llama), got %d", len(exp.PackBeasts))
	}

	// Verify granary stock decreased by 32
	stock, _ := sourceCivic.GetItemStockContext(ctx, commodity)
	if stock.Available() != 68 {
		t.Errorf("expected 68 available stock after 32 consumed for caravan, got %d", stock.Available())
	}

	// Verify generated intents: for each of the 2 beasts, 1 LEASH and 1 LOAD_BEAST_CHEST = 4 intents
	if len(intents) != 4 {
		t.Fatalf("expected 4 intents (2 leash, 2 load), got %d", len(intents))
	}

	leashCount := 0
	loadCount := 0
	for _, it := range intents {
		caravanIntent := it.GetCaravan()
		if caravanIntent == nil {
			t.Fatalf("expected caravan intent, got %v", it)
		}
		if caravanIntent.Action == civquestv1.CaravanIntent_LEASH_BEAST {
			leashCount++
		}
		if caravanIntent.Action == civquestv1.CaravanIntent_LOAD_BEAST_CHEST {
			loadCount++
			if len(caravanIntent.CargoItems) == 0 || caravanIntent.CargoItems[0].ItemId != commodity {
				t.Errorf("unexpected cargo item: %+v", caravanIntent.CargoItems)
			}
		}
	}
	if leashCount != 2 || loadCount != 2 {
		t.Errorf("expected 2 leash and 2 load intents, got leash=%d, load=%d", leashCount, loadCount)
	}

	// Verify expedition was logged to diplomacy db
	logEntry, err := sourceDiplo.GetExpeditionLog("exp-alpha-001")
	if err != nil || logEntry == nil {
		t.Fatalf("failed to retrieve logged expedition: %v", err)
	}
	if logEntry.Status != string(ExpeditionStatusInTransit) {
		t.Errorf("expected logged status IN_TRANSIT, got %s", logEntry.Status)
	}
}

func TestFullExpeditionLifecycle_NavigationTradeAndReturn(t *testing.T) {
	mgr, sourceCivic, sourceDiplo, partnerCivic, partnerDiplo := setupCaravanTestEnv(t)
	ctx := context.Background()

	commodity := "minecraft:bread"
	targetClan := "clan_iron_haven"

	// Register foreign Market zone in partner civic DB
	foreignMarket := &db.FunctionalZone{
		ZoneID:   "zone-iron-haven-market",
		ZoneType: db.ZoneTypeMarket,
		Name:     "Iron Haven Grand Bazaar",
		MinX:     90, MaxX: 110,
		MinY: 64, MaxY: 72,
		MinZ: 90, MaxZ: 110,
	}
	if err := partnerCivic.RegisterZoneContext(ctx, foreignMarket); err != nil {
		t.Fatalf("failed to register foreign market zone: %v", err)
	}

	// 1. Setup economic conditions
	_ = sourceCivic.AddGranaryStockContext(ctx, commodity, 100, 1000)
	mgr.SurplusTracker().SetSurplusStartTime("clan_pioneers", commodity, 1000)
	_ = sourceDiplo.SetAffinity(targetClan, 0.0, 1000) // NEUTRAL

	depTick := int64(1000 + MinSurplusDurationTicks + 100)

	nodes := []db.MapNode{
		{ID: "node-home", X: 0, Y: 64, Z: 0},
		{ID: "node-midway", X: 50, Y: 64, Z: 50},
		{ID: "node-foreign-market", X: 100, Y: 64, Z: 100},
	}
	edges := []db.MapEdge{
		{ID: "e1", FromNodeID: "node-home", ToNodeID: "node-midway", Distance: 70.7, IsPaved: true},
		{ID: "e2", FromNodeID: "node-midway", ToNodeID: "node-foreign-market", Distance: 70.7, IsPaved: true},
	}

	packBeasts := []*civquestv1.VisibleEntity{
		{EntityId: 201, EntityType: "minecraft:donkey"},
	}

	exp, _, err := mgr.FormExpedition(ctx, FormExpeditionRequest{
		ExpeditionID:   "exp-full-001",
		TargetClanID:   targetClan,
		CargoCommodity: commodity,
		CargoQuantity:  40,
		GuardAgentIDs:  []string{"guard-bob"},
		PackBeasts:     packBeasts,
		GraphNodes:     nodes,
		GraphEdges:     edges,
		StartNodeID:    "node-home",
		TargetNodeID:   "node-foreign-market",
		DepartureTick:  depTick,
		LocalRumors:    []string{"Pioneers discovered rich redstone deposits."},
	})
	if err != nil {
		t.Fatalf("FormExpedition failed: %v", err)
	}

	// 2. Navigate Step 1: at home (0,64,0) -> should emit navigate to node-home
	nav1, err := mgr.NavigateStep(exp.ID, &civquestv1.Vec3{X: 0, Y: 64, Z: 0})
	if err != nil {
		t.Fatalf("NavigateStep failed: %v", err)
	}
	if nav1 == nil || nav1.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent from nav1")
	}
	// Since at node-home (tolerance <= 2.0), it advances to node-midway
	nav2, err := mgr.NavigateStep(exp.ID, &civquestv1.Vec3{X: 10, Y: 64, Z: 10})
	if err != nil {
		t.Fatalf("NavigateStep failed: %v", err)
	}
	if nav2.GetNavigate().Target.X != 50 || nav2.GetNavigate().Target.Z != 50 {
		t.Errorf("expected navigation to midway node (50, 64, 50), got %+v", nav2.GetNavigate().Target)
	}

	// Arrive at node-midway (50, 64, 50) -> advances to node-foreign-market (100, 64, 100)
	_, _ = mgr.NavigateStep(exp.ID, &civquestv1.Vec3{X: 50, Y: 64, Z: 50})
	nav3, _ := mgr.NavigateStep(exp.ID, &civquestv1.Vec3{X: 60, Y: 64, Z: 60})
	if nav3.GetNavigate().Target.X != 100 || nav3.GetNavigate().Target.Z != 100 {
		t.Errorf("expected navigation to foreign market (100, 64, 100), got %+v", nav3.GetNavigate().Target)
	}

	// Arrive at foreign market -> transitions to TRADING
	_, _ = mgr.NavigateStep(exp.ID, &civquestv1.Vec3{X: 100, Y: 64, Z: 100})
	expState, _ := mgr.GetExpedition(exp.ID)
	if expState.Status != ExpeditionStatusTrading {
		t.Fatalf("expected status TRADING, got %s", expState.Status)
	}

	// 3. Execute Trade Negotiation
	tradeTick := depTick + 5000
	outcome, err := mgr.ExecuteTrade(ctx, exp.ID, tradeTick)
	if err != nil {
		t.Fatalf("ExecuteTrade failed: %v", err)
	}
	if !outcome.Accepted {
		t.Fatalf("expected trade to be accepted")
	}
	if len(outcome.GoodsReceived) == 0 || outcome.GoodsReceived[0].ItemId != "minecraft:emerald" {
		t.Errorf("expected emeralds received, got %+v", outcome.GoodsReceived)
	}
	if len(outcome.RumorsReceived) == 0 {
		t.Errorf("expected rumors received")
	}

	// Verify clan affinities updated in source diplomacy DB
	aff, err := sourceDiplo.GetAffinity(targetClan)
	if err != nil {
		t.Fatalf("failed to query source affinity: %v", err)
	}
	if aff.AffinityScore != 10.0 || aff.TradeVolume != 40 {
		t.Errorf("expected affinity 10.0 and trade volume 40, got score=%.1f vol=%d", aff.AffinityScore, aff.TradeVolume)
	}

	// Verify partner diplomacy DB reciprocal update
	pAff, err := partnerDiplo.GetAffinity("clan_pioneers")
	if err != nil {
		t.Fatalf("failed to query partner affinity: %v", err)
	}
	if pAff.AffinityScore != 10.0 || pAff.TradeVolume != 40 {
		t.Errorf("expected partner affinity 10.0 and trade volume 40, got score=%.1f vol=%d", pAff.AffinityScore, pAff.TradeVolume)
	}

	// Verify status transitioned to RETURNING and waypoints reversed
	expState, _ = mgr.GetExpedition(exp.ID)
	if expState.Status != ExpeditionStatusReturning {
		t.Fatalf("expected status RETURNING, got %s", expState.Status)
	}
	if expState.PavedWaypoints[0].ID != "node-foreign-market" || expState.PavedWaypoints[len(expState.PavedWaypoints)-1].ID != "node-home" {
		t.Errorf("expected reversed waypoints for return journey")
	}

	// 4. Return journey to home settlement and unload
	returnTick := tradeTick + 5000
	returnIntents, err := mgr.ReturnAndUnload(ctx, exp.ID, returnTick)
	if err != nil {
		t.Fatalf("ReturnAndUnload failed: %v", err)
	}

	// Verify UNLEASH_BEAST intent was emitted
	if len(returnIntents) != 1 || returnIntents[0].GetCaravan().Action != civquestv1.CaravanIntent_UNLEASH_BEAST {
		t.Fatalf("expected 1 UNLEASH_BEAST intent, got %+v", returnIntents)
	}

	// Verify received goods (emeralds) deposited into source clan granary
	emeraldStock, err := sourceCivic.GetItemStockContext(ctx, "minecraft:emerald")
	if err != nil || emeraldStock == nil {
		t.Fatalf("failed to query emerald stock: %v", err)
	}
	if emeraldStock.Quantity != 10 { // 40 bread / 4 = 10 emeralds
		t.Errorf("expected 10 emeralds deposited into granary, got %d", emeraldStock.Quantity)
	}

	// Verify final expedition log state
	finalLog, err := sourceDiplo.GetExpeditionLog(exp.ID)
	if err != nil || finalLog == nil {
		t.Fatalf("failed to query final expedition log: %v", err)
	}
	if finalLog.Status != string(ExpeditionStatusCompleted) {
		t.Errorf("expected final log status COMPLETED, got %s", finalLog.Status)
	}
	if finalLog.ReturnTick != returnTick {
		t.Errorf("expected return tick %d, got %d", returnTick, finalLog.ReturnTick)
	}
	if !strings.Contains(finalLog.GoodsExchangedJSON, "emerald") {
		t.Errorf("expected emerald in goods exchanged JSON, got %s", finalLog.GoodsExchangedJSON)
	}
}

func TestConcurrentCaravanOperations(t *testing.T) {
	mgr, sourceCivic, sourceDiplo, _, _ := setupCaravanTestEnv(t)
	ctx := context.Background()

	commodity := "minecraft:bread"
	targetClan := "clan_iron_haven"

	_ = sourceCivic.AddGranaryStockContext(ctx, commodity, 1000, 1000)
	mgr.SurplusTracker().SetSurplusStartTime("clan_pioneers", commodity, 1000)
	_ = sourceDiplo.SetAffinity(targetClan, 10.0, 1000)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = mgr.CheckEconomicConditions(ctx, targetClan, commodity, int64(1000+MinSurplusDurationTicks+int64(idx)))
			_ = mgr.ActiveExpedition()
		}(i)
	}
	wg.Wait()
}

func TestCaravanManager_StepCycle(t *testing.T) {
	mgr, sourceCivic, sourceDiplo, partnerCivic, _ := setupCaravanTestEnv(t)
	ctx := context.Background()

	commodity := "minecraft:bread"
	targetClan := "clan_iron_haven"

	// Register foreign market
	_ = partnerCivic.RegisterZoneContext(ctx, &db.FunctionalZone{
		ZoneID:   "iron-market",
		ZoneType: db.ZoneTypeMarket,
		Name:     "Iron Haven Market",
		MinX:     40, MaxX: 60,
		MinY: 64, MaxY: 72,
		MinZ: 40, MaxZ: 60,
	})

	_ = sourceCivic.AddGranaryStockContext(ctx, commodity, 100, 1000)
	mgr.SurplusTracker().SetSurplusStartTime("clan_pioneers", commodity, 1000)
	_ = sourceDiplo.SetAffinity(targetClan, 10.0, 1000)

	nodes := []db.MapNode{
		{ID: "home-node", X: 0, Y: 64, Z: 0},
		{ID: "foreign-node", X: 50, Y: 64, Z: 50},
	}
	edges := []db.MapEdge{
		{ID: "e-road", FromNodeID: "home-node", ToNodeID: "foreign-node", Distance: 70.7, IsPaved: true},
	}
	packBeasts := []*civquestv1.VisibleEntity{
		{EntityId: 301, EntityType: "minecraft:mule"},
	}

	depTick := int64(1000 + MinSurplusDurationTicks + 100)
	exp, _, err := mgr.FormExpedition(ctx, FormExpeditionRequest{
		ExpeditionID:   "exp-step-001",
		TargetClanID:   targetClan,
		CargoCommodity: commodity,
		CargoQuantity:  20,
		PackBeasts:     packBeasts,
		GraphNodes:     nodes,
		GraphEdges:     edges,
		StartNodeID:    "home-node",
		TargetNodeID:   "foreign-node",
		DepartureTick:  depTick,
	})
	if err != nil {
		t.Fatalf("FormExpedition failed: %v", err)
	}

	// 1. Step while departing home -> navigates toward foreign-node
	frameDepart := &civquestv1.TelemetryFrame{
		WorldTick: depTick + 10,
		Status: &civquestv1.AgentStatus{
			AgentId:  "agent_caravan_master",
			Position: &civquestv1.Vec3{X: 1, Y: 64, Z: 1},
		},
	}
	intents, err := mgr.Step(ctx, frameDepart)
	if err != nil {
		t.Fatalf("Step failed: %v", err)
	}
	if len(intents) != 1 || intents[0].GetNavigate() == nil {
		t.Fatalf("expected 1 NavigateIntent, got %+v", intents)
	}
	if intents[0].GetNavigate().Target.X != 50 || intents[0].GetNavigate().Target.Z != 50 {
		t.Errorf("expected navigate to (50, 64, 50), got %+v", intents[0].GetNavigate().Target)
	}

	// 2. Step arriving at foreign-node (within 2 blocks) -> triggers trade, transitions to RETURNING
	frameArrive := &civquestv1.TelemetryFrame{
		WorldTick: depTick + 500,
		Status: &civquestv1.AgentStatus{
			AgentId:  "agent_caravan_master",
			Position: &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
		},
	}
	_, err = mgr.Step(ctx, frameArrive)
	if err != nil {
		t.Fatalf("Step upon arrival failed: %v", err)
	}

	expState, _ := mgr.GetExpedition(exp.ID)
	if expState.Status != ExpeditionStatusReturning {
		t.Fatalf("expected status RETURNING after trade, got %s", expState.Status)
	}

	// 3. Step departing foreign market on return journey (at 50, 64, 50) -> advances to home-node (0, 64, 0)
	frameDepartReturn := &civquestv1.TelemetryFrame{
		WorldTick: depTick + 600,
		Status: &civquestv1.AgentStatus{
			AgentId:  "agent_caravan_master",
			Position: &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
		},
	}
	navReturn, err := mgr.Step(ctx, frameDepartReturn)
	if err != nil {
		t.Fatalf("Step departing return failed: %v", err)
	}
	if len(navReturn) != 1 || navReturn[0].GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent towards home-node, got %+v", navReturn)
	}
	if navReturn[0].GetNavigate().Target.X != 0 || navReturn[0].GetNavigate().Target.Z != 0 {
		t.Errorf("expected navigate target home (0, 64, 0), got %+v", navReturn[0].GetNavigate().Target)
	}

	// 4. Step arriving back at home (0, 64, 0) -> triggers unload and returns UNLEASH_BEAST
	frameReturnHome := &civquestv1.TelemetryFrame{
		WorldTick: depTick + 1000,
		Status: &civquestv1.AgentStatus{
			AgentId:  "agent_caravan_master",
			Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
		},
	}
	unloadIntents, err := mgr.Step(ctx, frameReturnHome)
	if err != nil {
		t.Fatalf("Step returning home failed: %v", err)
	}
	if len(unloadIntents) != 1 || unloadIntents[0].GetCaravan() == nil || unloadIntents[0].GetCaravan().Action != civquestv1.CaravanIntent_UNLEASH_BEAST {
		t.Fatalf("expected UNLEASH_BEAST intent upon home return, got %+v", unloadIntents)
	}

	expState, _ = mgr.GetExpedition(exp.ID)
	if expState.Status != ExpeditionStatusCompleted {
		t.Fatalf("expected status COMPLETED, got %s", expState.Status)
	}
}
