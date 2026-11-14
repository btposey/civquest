package tavern

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupTestDBs(t *testing.T) (*db.CivicRegistryDB, *db.RumorsDB) {
	tempDir := t.TempDir()
	civicPath := filepath.Join(tempDir, "civic_registry.db")
	rumorsPath := filepath.Join(tempDir, "rumors.db")

	civicDB, err := db.OpenCivicRegistryDB(civicPath, "clan_test")
	if err != nil {
		t.Fatalf("OpenCivicRegistryDB failed: %v", err)
	}

	rumorsDB, err := db.OpenRumorsDB(rumorsPath, "clan_test")
	if err != nil {
		civicDB.Close()
		t.Fatalf("OpenRumorsDB failed: %v", err)
	}

	t.Cleanup(func() {
		civicDB.Close()
		rumorsDB.Close()
	})

	return civicDB, rumorsDB
}

func setupAgentDB(t *testing.T, name string) *db.AgentDB {
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, name)

	agentDB, err := db.OpenAgentDB(agentDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	t.Cleanup(func() {
		agentDB.Close()
	})

	return agentDB
}

func TestTavernGathering_EveningSocialHub(t *testing.T) {
	civicDB, rumorsDB := setupTestDBs(t)
	manager := NewTavernManagerWithDBs(civicDB, rumorsDB)
	ctx := context.Background()

	// 1. Declare Tavern functional zone: X in [10, 30], Y in [64, 70], Z in [10, 30]
	tavernZone := &db.FunctionalZone{
		ZoneID:       "zone-tavern-main",
		ZoneType:     db.ZoneTypeTavern,
		MinX:         10,
		MaxX:         30,
		MinY:         64,
		MaxY:         70,
		MinZ:         10,
		MaxZ:         30,
		Name:         "The Rusty Pickaxe Tavern",
		OwnerAgentID: "agent-innkeeper-1",
		CreatedTick:  1000,
	}
	if err := civicDB.RegisterZone(tavernZone); err != nil {
		t.Fatalf("RegisterZone failed: %v", err)
	}

	foundZone, err := manager.FindTavernZone(ctx)
	if err != nil {
		t.Fatalf("FindTavernZone failed: %v", err)
	}
	if foundZone == nil || foundZone.ZoneID != tavernZone.ZoneID {
		t.Fatalf("expected zone %s, got %+v", tavernZone.ZoneID, foundZone)
	}

	// 2. Off-duty agent far outside the tavern (X: 100, Y: 64, Z: 100) -> Navigates to Tavern Center
	posOutside := &civquestv1.Vec3{X: 100.0, Y: 64.0, Z: 100.0}
	intent, err := manager.EvaluateGatheringIntent(ctx, "agent-settler-1", "FARMER", posOutside, foundZone)
	if err != nil {
		t.Fatalf("EvaluateGatheringIntent outside failed: %v", err)
	}
	if intent == nil || intent.GetNavigate() == nil {
		t.Fatalf("expected NavigateIntent when outside tavern zone, got nil")
	}
	nav := intent.GetNavigate()
	expectedCenterX := 20.0 // (10 + 30) / 2
	expectedCenterZ := 20.0
	if nav.Target.X != expectedCenterX || nav.Target.Z != expectedCenterZ {
		t.Errorf("expected navigation to center (20, 20), got (%.1f, %.1f)", nav.Target.X, nav.Target.Z)
	}

	// 3. Tavernkeeper inside the tavern (at counter X: 20, Y: 64, Z: 20) -> Stays at post
	keeperPos := &civquestv1.Vec3{X: 20.0, Y: 64.0, Z: 20.0}
	keeperIntent, err := manager.EvaluateGatheringIntent(ctx, "agent-innkeeper-1", ProfessionTavernkeeper, keeperPos, foundZone)
	if err != nil {
		t.Fatalf("EvaluateGatheringIntent for tavernkeeper failed: %v", err)
	}
	if keeperIntent != nil {
		t.Errorf("expected tavernkeeper to have nil intent at post, got %+v", keeperIntent)
	}
	if manager.GetTavernkeeperPos() == nil {
		t.Fatal("expected tavernkeeper position to be registered")
	}

	// 4. Patron inside the tavern, but far from Tavernkeeper (X: 12, Y: 64, Z: 12, dist ~ 11.3 blocks > 3)
	patronFarPos := &civquestv1.Vec3{X: 12.0, Y: 64.0, Z: 12.0}
	patronFarIntent, err := manager.EvaluateGatheringIntent(ctx, "agent-settler-1", "FARMER", patronFarPos, foundZone)
	if err != nil {
		t.Fatalf("EvaluateGatheringIntent for far patron failed: %v", err)
	}
	if patronFarIntent == nil || patronFarIntent.GetNavigate() == nil {
		t.Fatalf("expected patron to navigate towards Tavernkeeper, got nil")
	}
	patronNav := patronFarIntent.GetNavigate()
	if patronNav.Target.X != keeperPos.X || patronNav.Target.Z != keeperPos.Z {
		t.Errorf("expected patron to target tavernkeeper pos (20, 20), got (%.1f, %.1f)", patronNav.Target.X, patronNav.Target.Z)
	}

	// 5. Patron inside the tavern, gathered near Tavernkeeper (X: 21.5, Y: 64, Z: 20.5, dist ~ 1.58 blocks <= 3)
	patronGatheredPos := &civquestv1.Vec3{X: 21.5, Y: 64.0, Z: 20.5}
	gatheredIntent, err := manager.EvaluateGatheringIntent(ctx, "agent-settler-1", "FARMER", patronGatheredPos, foundZone)
	if err != nil {
		t.Fatalf("EvaluateGatheringIntent for gathered patron failed: %v", err)
	}
	if gatheredIntent != nil {
		t.Errorf("expected gathered patron to have nil intent (idle social hub), got %+v", gatheredIntent)
	}
}

func TestRumorDiffusion_NetworkAndDiffusion(t *testing.T) {
	_, rumorsDB := setupTestDBs(t)
	ctx := context.Background()

	// 1. Create rumors via observation helpers
	hostileZombie := &civquestv1.VisibleEntity{
		EntityId:   101,
		EntityType: "minecraft:zombie",
		Position:   &civquestv1.Vec3{X: 45.0, Y: 64.0, Z: -30.0},
	}
	hazardRumor, err := CreateHazardRumor(ctx, rumorsDB, "agent-patrol-1", "PatrolDan", hostileZombie, 11200)
	if err != nil {
		t.Fatalf("CreateHazardRumor failed: %v", err)
	}
	if hazardRumor == nil || hazardRumor.Category != db.RumorCategoryHazard {
		t.Fatalf("expected valid hazard rumor, got: %+v", hazardRumor)
	}

	beehiveBlock := &civquestv1.VisibleBlock{
		Pos:     &civquestv1.BlockPosProto{X: -80, Y: 68, Z: 120},
		BlockId: "minecraft:beehive",
	}
	resourceRumor, err := CreateResourceRumor(ctx, rumorsDB, "agent-forager-2", "ForagerFaye", beehiveBlock, 11300)
	if err != nil {
		t.Fatalf("CreateResourceRumor failed: %v", err)
	}
	if resourceRumor == nil || resourceRumor.Category != db.RumorCategoryResource {
		t.Fatalf("expected valid resource rumor, got: %+v", resourceRumor)
	}

	// 2. Diffuse rumors to agent-innkeeper-1
	diffused, err := DiffuseRumors(ctx, rumorsDB, "agent-patrol-1", "agent-innkeeper-1", 11400, 10)
	if err != nil {
		t.Fatalf("DiffuseRumors failed: %v", err)
	}
	// agent-patrol-1 tells agent-innkeeper-1. Since agent-innkeeper-1 hasn't heard either, they receive both!
	if len(diffused) != 2 {
		t.Fatalf("expected 2 rumors diffused to innkeeper, got %d", len(diffused))
	}

	// Verify spread counts updated
	refetchedHazard, _ := rumorsDB.GetRumor(ctx, hazardRumor.RumorID)
	if refetchedHazard.SpreadCount != 1 {
		t.Errorf("expected hazard rumor spread_count=1, got %d", refetchedHazard.SpreadCount)
	}

	// 3. Innkeeper diffuses to patron settler (agent-settler-3)
	diffusedToPatron, err := DiffuseRumors(ctx, rumorsDB, "agent-innkeeper-1", "agent-settler-3", 11500, 10)
	if err != nil {
		t.Fatalf("DiffuseRumors to patron failed: %v", err)
	}
	if len(diffusedToPatron) != 2 {
		t.Fatalf("expected 2 rumors diffused to patron, got %d", len(diffusedToPatron))
	}

	// Second diffusion to the same patron should yield 0 new rumors
	diffusedAgain, err := DiffuseRumors(ctx, rumorsDB, "agent-innkeeper-1", "agent-settler-3", 11600, 10)
	if err != nil {
		t.Fatalf("DiffuseRumors again failed: %v", err)
	}
	if len(diffusedAgain) != 0 {
		t.Fatalf("expected 0 new rumors on duplicate diffusion, got %d", len(diffusedAgain))
	}
}

func TestCartographySync_MergeNodesAndEdges(t *testing.T) {
	dbA := setupAgentDB(t, "agent_alice")
	dbB := setupAgentDB(t, "agent_bob")
	ctx := context.Background()

	// Alice has Node 1 and Node 2, and Edge 1-2 (unpaved)
	node1 := db.MapNode{ID: "node-1", X: 0, Y: 64, Z: 0, Label: "Settlement Well", Category: "LANDMARK", SafetyScore: 0.9, DiscoveredTick: 100}
	node2 := db.MapNode{ID: "node-2", X: 50, Y: 64, Z: 0, Label: "Eastern Gate", Category: "GATE", SafetyScore: 0.8, DiscoveredTick: 200}
	edge12 := db.MapEdge{ID: "edge-1-2", FromNodeID: "node-1", ToNodeID: "node-2", Distance: 50.0, DangerLevel: 0.2, IsPaved: false}

	if err := dbA.AddMapNode(ctx, node1); err != nil {
		t.Fatalf("dbA AddMapNode 1 failed: %v", err)
	}
	if err := dbA.AddMapNode(ctx, node2); err != nil {
		t.Fatalf("dbA AddMapNode 2 failed: %v", err)
	}
	if err := dbA.AddMapEdge(ctx, edge12); err != nil {
		t.Fatalf("dbA AddMapEdge 1-2 failed: %v", err)
	}

	// Bob has Node 2 (with higher safety score) and Node 3 (new to Alice), and Edge 2-3 (paved)
	node2Bob := db.MapNode{ID: "node-2", X: 50, Y: 64, Z: 0, Label: "Eastern Gate", Category: "GATE", SafetyScore: 0.95, DiscoveredTick: 300}
	node3 := db.MapNode{ID: "node-3", X: 120, Y: 64, Z: 20, Label: "Forester Lodge", Category: "BUILDING", SafetyScore: 0.85, DiscoveredTick: 400}
	edge23 := db.MapEdge{ID: "edge-2-3", FromNodeID: "node-2", ToNodeID: "node-3", Distance: 72.8, DangerLevel: 0.1, IsPaved: true}

	if err := dbB.AddMapNode(ctx, node2Bob); err != nil {
		t.Fatalf("dbB AddMapNode 2 failed: %v", err)
	}
	if err := dbB.AddMapNode(ctx, node3); err != nil {
		t.Fatalf("dbB AddMapNode 3 failed: %v", err)
	}
	if err := dbB.AddMapEdge(ctx, edge23); err != nil {
		t.Fatalf("dbB AddMapEdge 2-3 failed: %v", err)
	}

	// Sync cartography at the tavern!
	report, err := SyncCartography(ctx, dbA, dbB)
	if err != nil {
		t.Fatalf("SyncCartography failed: %v", err)
	}

	if report.NodesAddedToA != 1 { // Node 3 added to A
		t.Errorf("expected 1 node added to A, got %d", report.NodesAddedToA)
	}
	if report.NodesAddedToB != 1 { // Node 1 added to B
		t.Errorf("expected 1 node added to B, got %d", report.NodesAddedToB)
	}
	if report.EdgesAddedToA != 1 { // Edge 2-3 added to A
		t.Errorf("expected 1 edge added to A, got %d", report.EdgesAddedToA)
	}
	if report.EdgesAddedToB != 1 { // Edge 1-2 added to B
		t.Errorf("expected 1 edge added to B, got %d", report.EdgesAddedToB)
	}
	if report.NodesUpdatedA != 1 { // Node 2 updated in A with higher safety score
		t.Errorf("expected 1 node updated in A, got %d", report.NodesUpdatedA)
	}

	// Verify Alice now has all 3 nodes and both edges
	nodesA, edgesA, err := dbA.GetMapGraph(ctx)
	if err != nil {
		t.Fatalf("dbA GetMapGraph failed: %v", err)
	}
	if len(nodesA) != 3 {
		t.Errorf("expected Alice to have 3 nodes, got %d", len(nodesA))
	}
	if len(edgesA) != 2 {
		t.Errorf("expected Alice to have 2 edges, got %d", len(edgesA))
	}

	// Verify Bob now has all 3 nodes and both edges
	nodesB, edgesB, err := dbB.GetMapGraph(ctx)
	if err != nil {
		t.Fatalf("dbB GetMapGraph failed: %v", err)
	}
	if len(nodesB) != 3 {
		t.Errorf("expected Bob to have 3 nodes, got %d", len(nodesB))
	}
	if len(edgesB) != 2 {
		t.Errorf("expected Bob to have 2 edges, got %d", len(edgesB))
	}
}

func TestEmergentDualCareerHobbies_DynamicUnlocking(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		blocks        []*civquestv1.VisibleBlock
		items         []*civquestv1.ItemStackProto
		traits        xdg.AgentTraits
		expectedHobby string
	}{
		{
			name: "Beehives abundance unlocks APIARIST",
			blocks: []*civquestv1.VisibleBlock{
				{BlockId: "minecraft:beehive", Pos: &civquestv1.BlockPosProto{X: 1, Y: 64, Z: 1}},
				{BlockId: "minecraft:bee_nest", Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 1}},
			},
			items: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:honeycomb", Count: 3},
			},
			traits:        xdg.AgentTraits{Curiosity: 0.9, Industriousness: 0.7},
			expectedHobby: HobbyApiarist,
		},
		{
			name: "Sweet berry bushes abundance unlocks JAM_MAKER",
			blocks: []*civquestv1.VisibleBlock{
				{BlockId: "minecraft:sweet_berry_bush", Pos: &civquestv1.BlockPosProto{X: 5, Y: 64, Z: 5}},
				{BlockId: "minecraft:sweet_berry_bush", Pos: &civquestv1.BlockPosProto{X: 6, Y: 64, Z: 5}},
			},
			items: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:sweet_berries", Count: 16},
			},
			traits:        xdg.AgentTraits{Industriousness: 0.8, Sociability: 0.8},
			expectedHobby: HobbyJamMaker,
		},
		{
			name: "Barrels and apples abundance unlocks CIDER_BREWER",
			blocks: []*civquestv1.VisibleBlock{
				{BlockId: "minecraft:barrel", Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}},
			},
			items: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:apple", Count: 12},
				{ItemId: "minecraft:glass_bottle", Count: 6},
			},
			traits:        xdg.AgentTraits{Sociability: 0.9, Industriousness: 0.6},
			expectedHobby: HobbyCiderBrewer,
		},
		{
			name: "Candles and string abundance unlocks CHANDLER",
			blocks: []*civquestv1.VisibleBlock{
				{BlockId: "minecraft:candle", Pos: &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 12}},
			},
			items: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:honeycomb", Count: 8},
				{ItemId: "minecraft:string", Count: 8},
			},
			traits:        xdg.AgentTraits{Industriousness: 0.95, Curiosity: 0.6},
			expectedHobby: HobbyChandler,
		},
		{
			name: "Mushrooms abundance unlocks MYCOLOGIST",
			blocks: []*civquestv1.VisibleBlock{
				{BlockId: "minecraft:red_mushroom", Pos: &civquestv1.BlockPosProto{X: -20, Y: 40, Z: -15}},
				{BlockId: "minecraft:brown_mushroom", Pos: &civquestv1.BlockPosProto{X: -21, Y: 40, Z: -15}},
				{BlockId: "minecraft:red_mushroom_block", Pos: &civquestv1.BlockPosProto{X: -25, Y: 42, Z: -15}},
			},
			items: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:brown_mushroom", Count: 10},
			},
			traits:        xdg.AgentTraits{Curiosity: 0.95, Bravery: 0.8},
			expectedHobby: HobbyMycologist,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agentDB := setupAgentDB(t, "agent_hobby_"+tc.expectedHobby)
			agentCfg := xdg.DefaultAgentIdentityConfig("test-agent")
			agentCfg.SecondaryHobby = HobbyNone
			agentCfg.Traits = tc.traits

			unlocked, ok, err := UnlockEmergentHobby(ctx, agentDB, agentCfg, tc.blocks, tc.items)
			if err != nil {
				t.Fatalf("UnlockEmergentHobby failed: %v", err)
			}
			if !ok {
				t.Fatalf("expected hobby to unlock, got ok=false")
			}
			if unlocked != tc.expectedHobby {
				t.Errorf("expected hobby %s, got %s", tc.expectedHobby, unlocked)
			}
			if agentCfg.SecondaryHobby != tc.expectedHobby {
				t.Errorf("expected config secondary hobby %s, got %s", tc.expectedHobby, agentCfg.SecondaryHobby)
			}

			// Verify persisted in agent_skills
			skill, err := agentDB.GetSkill(ctx, tc.expectedHobby)
			if err != nil {
				t.Fatalf("GetSkill failed: %v", err)
			}
			if skill == nil || skill.Category != "SECONDARY_HOBBY" {
				t.Errorf("expected skill stored in SECONDARY_HOBBY category, got: %+v", skill)
			}

			// Verify already assigned hobby is preserved
			secondUnlocked, secondOk, err := UnlockEmergentHobby(ctx, agentDB, agentCfg, tc.blocks, tc.items)
			if err != nil || secondOk || secondUnlocked != tc.expectedHobby {
				t.Errorf("expected already assigned hobby to be preserved: secondUnlocked=%s, secondOk=%v, err=%v", secondUnlocked, secondOk, err)
			}
		})
	}
}

func TestOffShiftTransition_DutyAndEvening(t *testing.T) {
	agentCfg := xdg.DefaultAgentIdentityConfig("agent-worker")
	agentCfg.Profession = "MINER"
	agentCfg.SecondaryHobby = HobbyMycologist

	state := system1.NewAgentState()

	// 1. Daytime primary duty hours (tick 4000)
	routineDay := HandleOffShiftTransition(agentCfg, state, 4000, false)
	if routineDay != system1.RoutinePrimaryDuty {
		t.Errorf("expected RoutinePrimaryDuty at tick 4000, got %s", routineDay)
	}

	// 2. Daytime but primary duty is blocked (tick 4000, isPrimaryBlocked=true) -> fallback to Secondary Hobby
	routineBlocked := HandleOffShiftTransition(agentCfg, state, 4000, true)
	if routineBlocked != system1.RoutineSecondaryHobby {
		t.Errorf("expected RoutineSecondaryHobby when primary duty blocked, got %s", routineBlocked)
	}

	// 3. Afternoon leisure hobby hours (tick 9500)
	routineHobby := HandleOffShiftTransition(agentCfg, state, 9500, false)
	if routineHobby != system1.RoutineSecondaryHobby {
		t.Errorf("expected RoutineSecondaryHobby at tick 9500, got %s", routineHobby)
	}

	// 4. Evening tavern gathering hours (tick 12000)
	routineEvening := HandleOffShiftTransition(agentCfg, state, 12000, false)
	if routineEvening != system1.RoutineTavernSocial {
		t.Errorf("expected RoutineTavernSocial at tick 12000, got %s", routineEvening)
	}
}

func TestTavernExchangeIntel_Combined(t *testing.T) {
	civicDB, rumorsDB := setupTestDBs(t)
	manager := NewTavernManagerWithDBs(civicDB, rumorsDB)
	ctx := context.Background()

	dbA := setupAgentDB(t, "agent_settler_a")
	dbB := setupAgentDB(t, "agent_settler_b")

	// Setup nodes on each agent
	_ = dbA.AddMapNode(ctx, db.MapNode{ID: "node-a", X: 10, Y: 64, Z: 10, Label: "North Farm", Category: "FARM", SafetyScore: 0.9, DiscoveredTick: 100})
	_ = dbB.AddMapNode(ctx, db.MapNode{ID: "node-b", X: -10, Y: 64, Z: -10, Label: "South Mine", Category: "MINE", SafetyScore: 0.8, DiscoveredTick: 200})

	// Setup rumors in rumorsDB
	_ = rumorsDB.AddRumor(ctx, &db.Rumor{
		RumorID:       "rumor-trade-caravan",
		Category:      db.RumorCategoryTrade,
		Content:       "Caravan from Iron Clan arriving tomorrow",
		SourceAgentID: "agent_settler_a",
		OriginTick:    11000,
		CreatedTick:   11100,
	})

	report, err := manager.ExchangeTavernIntel(ctx, "agent_settler_a", "agent_settler_b", dbA, dbB, 11500)
	if err != nil {
		t.Fatalf("ExchangeTavernIntel failed: %v", err)
	}

	if report.Cartography == nil || report.Cartography.TotalExchanged() == 0 {
		t.Errorf("expected cartography nodes exchanged, got %+v", report.Cartography)
	}
	if len(report.RumorsToB) != 1 || report.RumorsToB[0].RumorID != "rumor-trade-caravan" {
		t.Errorf("expected 1 rumor diffused to agent B, got: %+v", report.RumorsToB)
	}
}
