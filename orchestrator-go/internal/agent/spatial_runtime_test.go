package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestRuntime_SpatialIndexStartupIngestionAndPlacement(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "spatial_tester"
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	// Pre-populate 1 active and 1 inactive POI in SQLite before NewRuntime
	preActive := db.AgentPOI{
		POIID:            "poi_container_5_64_5",
		Category:         system1.POICategoryContainer,
		BlockID:          "minecraft:chest",
		X:                5.5,
		Y:                64.0,
		Z:                5.5,
		AccessX:          6.5,
		AccessY:          64.0,
		AccessZ:          5.5,
		ElevationLayer:   system1.ElevationSurface,
		SafetyScore:      1.0,
		DiscoveredTick:   10,
		LastVerifiedTick: 10,
		Reliability:      1.0,
		IsActive:         true,
	}
	preInactive := db.AgentPOI{
		POIID:            "poi_workstation_9_64_9",
		Category:         system1.POICategoryWorkstation,
		BlockID:          "minecraft:furnace",
		X:                9.5,
		Y:                64.0,
		Z:                9.5,
		AccessX:          10.5,
		AccessY:          64.0,
		AccessZ:          9.5,
		ElevationLayer:   system1.ElevationSurface,
		SafetyScore:      1.0,
		DiscoveredTick:   10,
		LastVerifiedTick: 10,
		Reliability:      1.0,
		IsActive:         false,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, preActive); err != nil {
		t.Fatalf("AddOrUpdatePOI preActive failed: %v", err)
	}
	if err := agentDB.AddOrUpdatePOI(ctx, preInactive); err != nil {
		t.Fatalf("AddOrUpdatePOI preInactive failed: %v", err)
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  agentID,
		ClanID:   "pioneers",
		Resolver: resolver,
		AgentDB:  agentDB,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	si := rt.System1State().SpatialIndex()
	if si == nil {
		t.Fatalf("expected non-nil SpatialIndex on System1State")
	}

	// 1. Startup loaded active POI and ignored inactive POI
	if got, ok := si.GetPOI(preActive.POIID); !ok || got.BlockID != "minecraft:chest" {
		t.Fatalf("expected pre-existing active POI loaded at startup, got ok=%v, %+v", ok, got)
	}
	if _, ok := si.GetPOI(preInactive.POIID); ok {
		t.Fatalf("expected inactive POI not to be loaded at startup")
	}

	// 2. Telemetry frame afferent ingestion updates SpatialIndex and persists asynchronously
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 250,
		TimeOfDay: 3000,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 20.0, Y: 64.0, Z: 20.0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 6},
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 4},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 22, Y: 64, Z: 20}, BlockId: "minecraft:oak_log"},
			{Pos: &civquestv1.BlockPosProto{X: 24, Y: 54, Z: 20}, BlockId: "minecraft:furnace"},
		},
	}
	// A settled agent does not fell the log it sees (which would remove it from the map at once).
	rt.System1State().SetCollectiveSettlementSite(20, 64, 20)
	rt.System1State().AssignStrategy(system1.StrategyProfession, 0)
	rt.HandleTelemetryFrame(frame)

	logPOI, ok := si.GetPOI("poi_resource_22_64_20")
	if !ok || logPOI.Category != system1.POICategoryResource {
		t.Fatalf("expected ingested oak_log POI in SpatialIndex, got ok=%v, %+v", ok, logPOI)
	}
	furnacePOI, ok := si.GetPOI("poi_workstation_24_54_20")
	if !ok || furnacePOI.ElevationLayer != system1.ElevationDugout {
		t.Fatalf("expected dugout furnace POI in SpatialIndex, got ok=%v, %+v", ok, furnacePOI)
	}

	// 3. Outbound PLACE_BLOCK intent immediately upserts POI into SpatialIndex
	err = rt.QueueIntent(&civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 21, Y: 64, Z: 21},
				InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
				BlockOrItemId:   "minecraft:crafting_table",
			},
		},
	})
	if err != nil {
		t.Fatalf("QueueIntent PLACE_BLOCK failed: %v", err)
	}

	placedPOI, ok := si.GetPOI("poi_workstation_21_64_21")
	if !ok || placedPOI.BlockID != "minecraft:crafting_table" || !placedPOI.IsActive {
		t.Fatalf("expected immediate placed crafting_table POI in SpatialIndex, got ok=%v, %+v", ok, placedPOI)
	}

	// Verify async SQLite persistence completes cleanly
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p1, _ := agentDB.GetPOI(ctx, "poi_resource_22_64_20")
		p2, _ := agentDB.GetPOI(ctx, "poi_workstation_21_64_21")
		if p1 != nil && p2 != nil {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async POI persistence to AgentDB")
}

func TestRuntime_HarvestPruning(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "harvest_tester"
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	harvestPOI := db.AgentPOI{
		POIID:            "poi_resource_10_64_10",
		Category:         system1.POICategoryResource,
		BlockID:          "minecraft:oak_log",
		X:                10.5,
		Y:                64.0,
		Z:                10.5,
		AccessX:          9.5,
		AccessY:          64.0,
		AccessZ:          10.5,
		ElevationLayer:   system1.ElevationSurface,
		SafetyScore:      1.0,
		DiscoveredTick:   50,
		LastVerifiedTick: 50,
		Reliability:      1.0,
		IsActive:         true,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, harvestPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI failed: %v", err)
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  agentID,
		ClanID:   "pioneers",
		Resolver: resolver,
		AgentDB:  agentDB,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	si := rt.System1State().SpatialIndex()
	if _, ok := si.GetPOI(harvestPOI.POIID); !ok {
		t.Fatalf("expected POI loaded at startup in SpatialIndex")
	}

	// 1. Dispatch outbound HARVEST_BREAK intent
	err = rt.QueueIntent(&civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10},
				InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
				BlockOrItemId:   "minecraft:oak_log",
			},
		},
	})
	if err != nil {
		t.Fatalf("QueueIntent HARVEST_BREAK failed: %v", err)
	}

	// Immediate removal from SpatialIndex
	if _, ok := si.GetPOI(harvestPOI.POIID); ok {
		t.Fatalf("expected POI to be immediately pruned from SpatialIndex upon HARVEST_BREAK dispatch")
	}

	// Asynchronous invalidation in AgentDB
	rt.AwaitPOIPersistence()
	persisted, err := agentDB.GetPOI(ctx, harvestPOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI failed: %v", err)
	}
	if persisted.IsActive {
		t.Fatalf("expected POI is_active=false in AgentDB after harvest pruning, got %+v", persisted)
	}

	// 2. Test receipt-confirmed pruning
	receiptPOI := db.AgentPOI{
		POIID:          "poi_resource_15_64_15",
		Category:       system1.POICategoryResource,
		BlockID:        "minecraft:birch_log",
		X:              15.5,
		Y:              64.0,
		Z:              15.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, receiptPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI receiptPOI failed: %v", err)
	}
	si.UpsertPOI(receiptPOI)

	// Queue harvest intent which records lastHarvestPos
	_ = rt.QueueIntent(&civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 15, Y: 64, Z: 15},
				InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
				BlockOrItemId:   "minecraft:birch_log",
			},
		},
	})

	// Even if re-inserted (e.g. race), an incoming completed receipt confirms destruction
	si.UpsertPOI(receiptPOI)
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 60,
		Receipt: &civquestv1.IntentReceipt{
			Status: civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
		},
	})

	if _, ok := si.GetPOI(receiptPOI.POIID); ok {
		t.Fatalf("expected receiptPOI to be pruned from SpatialIndex after receipt completion")
	}
	rt.AwaitPOIPersistence()
	persistedReceipt, err := agentDB.GetPOI(ctx, receiptPOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI receiptPOI failed: %v", err)
	}
	if persistedReceipt.IsActive {
		t.Fatalf("expected receiptPOI is_active=false in AgentDB")
	}
}

func TestRuntime_CloseRangeBeliefVerification(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "belief_tester"
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	tablePOI := db.AgentPOI{
		POIID:          "poi_workstation_10_64_10",
		Category:       system1.POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              10.5,
		Y:              64.0,
		Z:              10.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	distantPOI := db.AgentPOI{
		POIID:          "poi_workstation_40_64_40",
		Category:       system1.POICategoryWorkstation,
		BlockID:        "minecraft:furnace",
		X:              40.5,
		Y:              64.0,
		Z:              40.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, tablePOI); err != nil {
		t.Fatalf("AddOrUpdatePOI tablePOI failed: %v", err)
	}
	if err := agentDB.AddOrUpdatePOI(ctx, distantPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI distantPOI failed: %v", err)
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  agentID,
		ClanID:   "pioneers",
		Resolver: resolver,
		AgentDB:  agentDB,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	si := rt.System1State().SpatialIndex()

	// Agent arrives at (10.0, 64.0, 10.0) — distance to tablePOI is 0.7m (<= 3.5m).
	// The incoming sensory frame has NO blocks at (10, 64, 10) (table was destroyed/mined away).
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 6},
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{}, // Empty sensory frustum
	}

	rt.HandleTelemetryFrame(frame)

	// Close-range belief verification must have removed tablePOI
	if _, ok := si.GetPOI(tablePOI.POIID); ok {
		t.Fatalf("expected close-range missing POI %s to be pruned from SpatialIndex", tablePOI.POIID)
	}

	// Distant POI outside 3.5m must NOT be pruned
	if _, ok := si.GetPOI(distantPOI.POIID); !ok {
		t.Fatalf("expected distant POI %s to remain active in SpatialIndex", distantPOI.POIID)
	}

	// Invalidation in AgentDB must be complete
	rt.AwaitPOIPersistence()
	persisted, err := agentDB.GetPOI(ctx, tablePOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI tablePOI failed: %v", err)
	}
	if persisted.IsActive {
		t.Fatalf("expected tablePOI is_active=false in AgentDB")
	}

	persistedDistant, err := agentDB.GetPOI(ctx, distantPOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI distantPOI failed: %v", err)
	}
	if !persistedDistant.IsActive {
		t.Fatalf("expected distantPOI to remain active in AgentDB")
	}
}

func TestRuntime_NavigationStuckDecayAndInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "stuck_tester"
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	stuckPOI := db.AgentPOI{
		POIID:          "poi_workstation_30_64_30",
		Category:       system1.POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              30.5,
		Y:              64.0,
		Z:              30.5,
		AccessX:        29.5,
		AccessY:        64.0,
		AccessZ:        30.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, stuckPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI stuckPOI failed: %v", err)
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  agentID,
		ClanID:   "pioneers",
		Resolver: resolver,
		AgentDB:  agentDB,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	si := rt.System1State().SpatialIndex()

	// 1. Issue Navigate intent toward the POI access target
	err = rt.QueueIntent(&civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{X: 29.5, Y: 64.0, Z: 30.5},
			},
		},
	})
	if err != nil {
		t.Fatalf("QueueIntent Navigate failed: %v", err)
	}

	if rt.NavTargetPOIID() != stuckPOI.POIID {
		t.Fatalf("expected NavTargetPOIID=%s, got %s", stuckPOI.POIID, rt.NavTargetPOIID())
	}

	// 2. Navigation telemetry reports NAVIGATION_STATE_STUCK
	stuckFrame := &civquestv1.TelemetryFrame{
		WorldTick: 120,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 6},
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
			},
		},
	}
	rt.HandleTelemetryFrame(stuckFrame)
	if p, ok := si.GetPOI(stuckPOI.POIID); !ok || p.Reliability < 1.0 {
		t.Fatalf("telemetry stuck alone must not decay a POI (ADR-037), got ok=%v %+v", ok, p)
	}

	// A terrain Recover give-up on the POI's access target is the decay input.
	giveUp := func() {
		rt.AgentFSM().QueueRecoverGiveUp(system1.RecoverGiveUp{Target: [3]float64{29.5, 64.0, 30.5}, Cause: system1.CauseTerrain, Tick: 120})
		rt.HandleTelemetryFrame(stuckFrame)
	}
	giveUp()

	// First give-up decay: 1.0 -> 0.75
	p, ok := si.GetPOI(stuckPOI.POIID)
	if !ok {
		t.Fatalf("expected POI still present in SpatialIndex after first decay")
	}
	if p.Reliability < 0.74 || p.Reliability > 0.76 {
		t.Fatalf("expected reliability ~0.75 after first stuck decay, got %f", p.Reliability)
	}

	rt.AwaitPOIPersistence()
	dbPOI, err := agentDB.GetPOI(ctx, stuckPOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI failed: %v", err)
	}
	if dbPOI.Reliability < 0.74 || dbPOI.Reliability > 0.76 {
		t.Fatalf("expected AgentDB reliability ~0.75, got %f", dbPOI.Reliability)
	}

	// 3. Repeat navigation and stuck decay until reliability <= 0.1
	// Next attempt: 0.75 -> 0.50
	giveUp()
	p, _ = si.GetPOI(stuckPOI.POIID)
	if p.Reliability < 0.49 || p.Reliability > 0.51 {
		t.Fatalf("expected reliability ~0.50, got %f", p.Reliability)
	}

	// Next attempt: 0.50 -> 0.25
	giveUp()
	p, _ = si.GetPOI(stuckPOI.POIID)
	if p.Reliability < 0.24 || p.Reliability > 0.26 {
		t.Fatalf("expected reliability ~0.25, got %f", p.Reliability)
	}

	// Final attempt: 0.25 -> 0.00 <= 0.1 threshold -> POI invalidated & pruned!
	giveUp()

	// Should be removed from SpatialIndex
	if _, ok := si.GetPOI(stuckPOI.POIID); ok {
		t.Fatalf("expected POI to be removed from SpatialIndex when reliability <= 0.1")
	}

	// Should be invalidated (is_active=0) in AgentDB
	rt.AwaitPOIPersistence()
	dbPOI, err = agentDB.GetPOI(ctx, stuckPOI.POIID)
	if err != nil {
		t.Fatalf("GetPOI after invalidation failed: %v", err)
	}
	if dbPOI.IsActive {
		t.Fatalf("expected AgentDB is_active=false when reliability <= 0.1, got %+v", dbPOI)
	}
}

// TestRuntime_HabitTrailCorridorPersistenceAndNavigation tests corridor preloading at startup,
// long-distance navigation interception along known corridors, breadcrumb recording,
// consolidation upon arrival, and async persistence to AgentDB (ADR-023).
func TestRuntime_HabitTrailCorridorPersistenceAndNavigation(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "corridor_tester"
	if err := resolver.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories failed: %v", err)
	}

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()

	// 1. Seed two POIs > 16m apart and a connecting TrailCorridor in SQLite
	originPOI := db.AgentPOI{
		POIID:          "poi_habitation_0_64_0",
		Category:       system1.POICategoryHabitation,
		BlockID:        "minecraft:red_bed",
		X:              0.5,
		Y:              64.0,
		Z:              0.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	destPOI := db.AgentPOI{
		POIID:          "poi_resource_60_64_0",
		Category:       system1.POICategoryResource,
		BlockID:        "minecraft:iron_ore",
		X:              60.5,
		Y:              64.0,
		Z:              0.5,
		ElevationLayer: system1.ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	if err := agentDB.AddOrUpdatePOI(ctx, originPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI origin failed: %v", err)
	}
	if err := agentDB.AddOrUpdatePOI(ctx, destPOI); err != nil {
		t.Fatalf("AddOrUpdatePOI dest failed: %v", err)
	}

	seedCorridor := db.TrailCorridor{
		ID:            "corridor_poi_habitation_0_64_0_poi_resource_60_64_0",
		FromPOIID:     "poi_habitation_0_64_0",
		ToPOIID:       "poi_resource_60_64_0",
		WaypointsJSON: `[[30,64,15]]`,
		TotalDistance: 65.0,
		TraverseCount: 2,
		IsPaved:       false,
	}
	if err := agentDB.AddTrailCorridor(ctx, seedCorridor); err != nil {
		t.Fatalf("AddTrailCorridor seed failed: %v", err)
	}

	// 2. Start AgentRuntime: verifies loadSpatialPOIs pre-loads corridors into SpatialIndex
	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  agentID,
		ClanID:   "pioneers",
		Resolver: resolver,
		AgentDB:  agentDB,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	si := rt.System1State().SpatialIndex()
	if si == nil {
		t.Fatalf("expected non-nil SpatialIndex")
	}
	loadedCorridor, ok := si.GetCorridor("poi_habitation_0_64_0", "poi_resource_60_64_0")
	if !ok || loadedCorridor == nil {
		t.Fatalf("expected corridor to be preloaded into SpatialIndex at startup")
	}
	if loadedCorridor.TraverseCount != 2 {
		t.Errorf("expected traverse_count 2, got %d", loadedCorridor.TraverseCount)
	}

	// 3. Establish origin near poi_home
	frameAtHome := &civquestv1.TelemetryFrame{
		WorldTick: 10,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			Position:        &civquestv1.Vec3{X: 1.0, Y: 64.0, Z: 1.0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 0, Y: 64, Z: 0}, BlockId: "minecraft:red_bed"},
		},
	}
	rt.HandleTelemetryFrame(frameAtHome)

	tracker := rt.HabitTrailTracker()
	if tracker == nil {
		t.Fatalf("expected non-nil HabitTrailTracker")
	}
	if tracker.CurrentOriginPOIID() != "poi_habitation_0_64_0" {
		t.Fatalf("expected origin POI 'poi_habitation_0_64_0', got %q", tracker.CurrentOriginPOIID())
	}

	// 4. Initiate long-distance navigation to poi_mine (> 16m away)
	// Intent target is (60.5, 64.0, 0.5), but known corridor has waypoint W0 at (30, 64, 15).
	// Verify QueueIntent intercepts and redirects navigate target to W0!
	navIntent := &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{X: 60.5, Y: 64.0, Z: 0.5},
			},
		},
	}
	if err := rt.QueueIntent(navIntent); err != nil {
		t.Fatalf("QueueIntent failed: %v", err)
	}

	intents := rt.DrainIntents()
	if len(intents) != 1 || intents[0].GetNavigate() == nil {
		t.Fatalf("expected 1 intercepted NavigateIntent, got %+v", intents)
	}
	target := intents[0].GetNavigate().Target
	if target.X != 30.0 || target.Y != 64.0 || target.Z != 15.0 {
		t.Fatalf("expected navigation target redirected to corridor waypoint (30, 64, 15), got (%f, %f, %f)",
			target.X, target.Y, target.Z)
	}
	if !tracker.IsNavigatingCorridor() {
		t.Fatalf("expected tracker.IsNavigatingCorridor() to be true")
	}

	// 5. Move agent near W0 (at 29.0, 64.0, 15.0): UpdateNavigation should advance to direct destination (60.5, 64.0, 0.5)
	frameNearW0 := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			Position:        &civquestv1.Vec3{X: 29.0, Y: 64.0, Z: 15.0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
		},
	}
	rt.HandleTelemetryFrame(frameNearW0)

	nextWaypointIntents := rt.DrainIntents()
	if len(nextWaypointIntents) < 1 || nextWaypointIntents[0].GetNavigate() == nil {
		t.Fatalf("expected next leg intent to be dispatched, got %+v", nextWaypointIntents)
	}
	legTarget := nextWaypointIntents[0].GetNavigate().Target
	if legTarget.X != 60.5 || legTarget.Z != 0.5 {
		t.Fatalf("expected transition to direct destination (60.5, 64.0, 0.5), got %+v", legTarget)
	}

	// 6. Test async corridor persistence
	newCorridor := db.TrailCorridor{
		ID:            "corridor_test_async",
		FromPOIID:     "poi_habitation_0_64_0",
		ToPOIID:       "poi_resource_60_64_0",
		WaypointsJSON: `[[20,64,10],[40,64,10]]`,
		TotalDistance: 62.0,
		TraverseCount: 5,
		IsPaved:       true,
	}
	si.UpsertCorridor(newCorridor)
	// Trigger async persistence via public helper in runtime
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			Position:        &civquestv1.Vec3{X: 60.5, Y: 64.0, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
		},
	})
	rt.AwaitPOIPersistence()

	// Verify persistence in AgentDB SQLite
	corridors, err := agentDB.ListTrailCorridors(ctx, "poi_habitation_0_64_0", "poi_resource_60_64_0")
	if err != nil {
		t.Fatalf("ListTrailCorridors failed: %v", err)
	}
	if len(corridors) == 0 {
		t.Fatalf("expected at least 1 persisted corridor in SQLite")
	}
}
