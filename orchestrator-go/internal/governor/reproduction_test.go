package governor

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/genetics"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type mockLauncher struct {
	mu           sync.Mutex
	startedAgents []string
}

func (m *mockLauncher) StartAgent(agentName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startedAgents = append(m.startedAgents, agentName)
	return nil
}

type mockSpawner struct {
	mu          sync.Mutex
	spawnReqs   []*civquestv1.SpawnAgentRequest
	nextEntityID int32
}

func (m *mockSpawner) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spawnReqs = append(m.spawnReqs, req)
	m.nextEntityID++
	return &civquestv1.SpawnAgentResponse{
		Success:           true,
		MinecraftEntityId: m.nextEntityID,
		OfflineUuid:       agent.OfflinePlayerUUID(req.AgentId),
	}, nil
}

func setupTestEnvironment(t *testing.T) (*xdg.Resolver, string, func()) {
	t.Helper()
	tempDir := t.TempDir()

	configHome := filepath.Join(tempDir, "config")
	dataHome := filepath.Join(tempDir, "data")
	stateHome := filepath.Join(tempDir, "state")

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	resolver := xdg.NewResolver()

	clanID := "pioneers"
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	return resolver, clanID, cleanup
}

func seedParents(t *testing.T, r *xdg.Resolver, clanID string, parentAName, parentBName string, mutualDisposition float64, areSpouses bool) {
	t.Helper()

	cfgA := &xdg.AgentIdentityConfig{
		Name:       parentAName,
		UUID:       agent.OfflinePlayerUUID(parentAName),
		ClanID:     clanID,
		Profession: "FARMER",
		Generation: 1,
		Traits: xdg.AgentTraits{
			Bravery:         0.80,
			Industriousness: 0.90,
			Sociability:     0.70,
			Curiosity:       0.60,
			Aggressiveness:  0.20,
		},
		Skin: xdg.AgentSkinConfig{Model: "default"},
	}
	cfgB := &xdg.AgentIdentityConfig{
		Name:       parentBName,
		UUID:       agent.OfflinePlayerUUID(parentBName),
		ClanID:     clanID,
		Profession: "BLACKSMITH",
		Generation: 1,
		Traits: xdg.AgentTraits{
			Bravery:         0.50,
			Industriousness: 0.70,
			Sociability:     0.80,
			Curiosity:       0.70,
			Aggressiveness:  0.30,
		},
		Skin: xdg.AgentSkinConfig{Model: "slim"},
	}

	if err := r.SaveAgentConfig(cfgA); err != nil {
		t.Fatalf("failed saving parent A: %v", err)
	}
	if err := r.SaveAgentConfig(cfgB); err != nil {
		t.Fatalf("failed saving parent B: %v", err)
	}

	// Create skins
	skinPathA := r.AgentSkinPath(parentAName)
	skinPathB := r.AgentSkinPath(parentBName)
	_ = genetics.GenerateSynthesizedSkin(skinPathA, cfgA.Traits, rand.New(rand.NewSource(1)))
	_ = genetics.GenerateSynthesizedSkin(skinPathB, cfgB.Traits, rand.New(rand.NewSource(2)))
	cfgA.Skin.TexturePath = skinPathA
	cfgB.Skin.TexturePath = skinPathB
	_ = r.SaveAgentConfig(cfgA)
	_ = r.SaveAgentConfig(cfgB)

	// Set up relationship DBs
	ctx := context.Background()
	roleA := "NONE"
	roleB := "NONE"
	if areSpouses {
		roleA = KinshipRoleSpouse
		roleB = KinshipRoleSpouse
	}

	dbA, err := db.OpenAgentDBForAgent(r, parentAName)
	if err != nil {
		t.Fatalf("opening dbA: %v", err)
	}
	defer dbA.Close()
	_ = dbA.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:          cfgB.UUID,
		EntityName:          cfgB.Name,
		EntityType:          "AGENT",
		DispositionScore:    mutualDisposition,
		Familiarity:         0.9,
		KinshipRole:         roleA,
		LastInteractionTick: 100,
	})

	dbB, err := db.OpenAgentDBForAgent(r, parentBName)
	if err != nil {
		t.Fatalf("opening dbB: %v", err)
	}
	defer dbB.Close()
	_ = dbB.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:          cfgA.UUID,
		EntityName:          cfgA.Name,
		EntityType:          "AGENT",
		DispositionScore:    mutualDisposition,
		Familiarity:         0.9,
		KinshipRole:         roleB,
		LastInteractionTick: 100,
	})
}

func seedCivicInfrastructure(t *testing.T, r *xdg.Resolver, clanID string, foodQuantity int, hasHousing bool) *db.FunctionalZone {
	t.Helper()
	ctx := context.Background()

	civicDB, err := db.OpenCivicRegistryDBForClan(r, clanID)
	if err != nil {
		t.Fatalf("failed opening civic db: %v", err)
	}
	defer civicDB.Close()

	if foodQuantity > 0 {
		_ = civicDB.AddGranaryStockContext(ctx, "minecraft:bread", foodQuantity, 100)
	}

	var zone *db.FunctionalZone
	if hasHousing {
		zone = &db.FunctionalZone{
			ZoneID:       "zone_residential_cabin_1",
			ZoneType:     db.ZoneTypeResidential,
			MinX:         10,
			MinY:         64,
			MinZ:         10,
			MaxX:         20,
			MaxY:         70,
			MaxZ:         20,
			OwnerAgentID: "",
			Name:         "Frontier Cabin 1",
			CreatedTick:  100,
		}
		if err := civicDB.RegisterZoneContext(ctx, zone); err != nil {
			t.Fatalf("failed registering residential zone: %v", err)
		}
	}

	return zone
}

func TestReproductionGovernor_Invariants(t *testing.T) {
	r, clanID, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()
	launcher := &mockLauncher{}
	spawner := &mockSpawner{}

	// 1. Invariant 1: Global active agent limit
	gov, err := NewReproductionGovernor(ReproductionGovernorConfig{
		Resolver:        r,
		ClanID:          clanID,
		MaxActiveAgents: 2,
		Launcher:        launcher,
		Spawner:         spawner,
		ActiveAgentsProvider: func() int {
			return 2 // At limit
		},
	})
	if err != nil {
		t.Fatalf("NewReproductionGovernor failed: %v", err)
	}

	res, err := gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected Invariant 1 rejection due to max active agents limit")
	}

	// 2. Invariant 2: Food surplus check (< 7 days)
	gov.maxActiveAgents = 16
	gov.activeAgentsProvider = func() int { return 2 } // 2 active agents
	// Required food: 2 agents * 7 days * 3 food/day = 42 food units.
	// We seed only 20 food units (insufficient).
	seedCivicInfrastructure(t, r, clanID, 20, true)
	seedParents(t, r, clanID, "aria", "brynn", 90.0, true)

	res, err = gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected Invariant 2 rejection due to food deficit (20 < 42)")
	}

	// 3. Invariant 3: Housing check (no unclaimed bed)
	// Add food to clear Invariant 2 (need >= 42, add 50 -> 70 total)
	civicDB, _ := db.OpenCivicRegistryDBForClan(r, clanID)
	_ = civicDB.AddGranaryStockContext(ctx, "minecraft:cooked_beef", 50, 100)
	// Claim the existing residential zone so no unclaimed bed remains
	_, _ = civicDB.DB().ExecContext(ctx, "UPDATE functional_zones SET owner_agent_id = 'someone' WHERE zone_id = 'zone_residential_cabin_1'")
	_ = civicDB.Close()

	res, err = gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected Invariant 3 rejection due to no unclaimed beds")
	}

	// 4. Invariant 4: Married pair check
	// Free up housing
	civicDB, _ = db.OpenCivicRegistryDBForClan(r, clanID)
	_, _ = civicDB.DB().ExecContext(ctx, "UPDATE functional_zones SET owner_agent_id = '' WHERE zone_id = 'zone_residential_cabin_1'")
	_ = civicDB.Close()

	// 4a. Break marriage (role = NONE)
	dbA, _ := db.OpenAgentDBForAgent(r, "aria")
	_ = dbA.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       agent.OfflinePlayerUUID("brynn"),
		EntityName:       "brynn",
		EntityType:       "AGENT",
		DispositionScore: 90.0,
		KinshipRole:      "NONE",
	})
	_ = dbA.Close()

	res, err = gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected Invariant 4 rejection when agents are not SPOUSE")
	}

	// 4b. Low disposition (<80.0)
	dbA, _ = db.OpenAgentDBForAgent(r, "aria")
	_ = dbA.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       agent.OfflinePlayerUUID("brynn"),
		EntityName:       "brynn",
		EntityType:       "AGENT",
		DispositionScore: 75.0, // < 80.0
		KinshipRole:      KinshipRoleSpouse,
	})
	_ = dbA.Close()

	res, err = gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if res.Eligible {
		t.Errorf("expected Invariant 4 rejection when disposition < 80.0")
	}

	// 4c. Restore valid mutual marriage with disposition >= 80.0
	dbA, _ = db.OpenAgentDBForAgent(r, "aria")
	_ = dbA.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       agent.OfflinePlayerUUID("brynn"),
		EntityName:       "brynn",
		EntityType:       "AGENT",
		DispositionScore: 88.0,
		KinshipRole:      KinshipRoleSpouse,
	})
	_ = dbA.Close()

	res, err = gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !res.Eligible {
		t.Fatalf("expected eligible reproduction, got reason: %s", res.Reason)
	}
}

func TestReproductionGovernor_TriggerSuccess(t *testing.T) {
	r, clanID, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()
	launcher := &mockLauncher{}
	spawner := &mockSpawner{}
	rng := rand.New(rand.NewSource(12345))

	seedParents(t, r, clanID, "aria", "brynn", 92.0, true)
	seedCivicInfrastructure(t, r, clanID, 100, true) // 100 bread > 42 required

	gov, err := NewReproductionGovernor(ReproductionGovernorConfig{
		Resolver:             r,
		ClanID:               clanID,
		MaxActiveAgents:      16,
		MinFoodSurplusDays:   7,
		DailyFoodPerAgent:    3,
		Launcher:             launcher,
		Spawner:              spawner,
		RNG:                  rng,
		ActiveAgentsProvider: func() int { return 2 },
		WorldTickProvider:   func() int64 { return 24000 },
	})
	if err != nil {
		t.Fatalf("NewReproductionGovernor failed: %v", err)
	}

	result, err := gov.Trigger(ctx, clanID)
	if err != nil {
		t.Fatalf("Trigger failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("reproduction was not successful: %s", result.CheckResult.Reason)
	}

	// Verify child metadata
	childID := result.ChildID
	if childID == "" {
		t.Fatalf("expected non-empty child ID")
	}
	if result.Generation != 2 {
		t.Errorf("child generation = %d, want 2 (parents were Gen 1)", result.Generation)
	}

	// Verify directory structures
	childDataDir := r.AgentDataDir(childID)
	if fi, err := os.Stat(childDataDir); err != nil || !fi.IsDir() {
		t.Fatalf("child data dir missing: %s", childDataDir)
	}
	childConfigDir := filepath.Join(r.AgentsConfigDir(), childID)
	if fi, err := os.Stat(childConfigDir); err != nil || !fi.IsDir() {
		t.Fatalf("child config dir missing: %s", childConfigDir)
	}

	// Verify child identity.yaml
	identityPath := filepath.Join(childConfigDir, "identity.yaml")
	childCfg, err := xdg.LoadAgentConfig(identityPath)
	if err != nil {
		t.Fatalf("failed loading child identity.yaml: %v", err)
	}
	if !childCfg.IsChild {
		t.Errorf("child is_child flag was false")
	}
	if childCfg.Generation != 2 {
		t.Errorf("child config generation = %d, want 2", childCfg.Generation)
	}
	if len(childCfg.Lineage.Parents) != 2 {
		t.Errorf("child lineage parents count = %d, want 2", len(childCfg.Lineage.Parents))
	}
	if childCfg.Lineage.BirthTick != 24000 {
		t.Errorf("child lineage birth tick = %d, want 24000", childCfg.Lineage.BirthTick)
	}

	// Verify blended traits
	if err := childCfg.Traits.Validate(); err != nil {
		t.Errorf("child blended traits invalid: %v", err)
	}

	// Verify child skin asset
	childSkinPath := r.AgentSkinPath(childID)
	if fi, err := os.Stat(childSkinPath); err != nil || fi.Size() == 0 {
		t.Errorf("child skin asset missing or empty: %s", childSkinPath)
	}

	// Verify housing zone was claimed
	civicDB, _ := db.OpenCivicRegistryDBForClan(r, clanID)
	zone, _ := civicDB.GetZone("zone_residential_cabin_1")
	_ = civicDB.Close()
	if zone.OwnerAgentID != childID {
		t.Errorf("zone owner_agent_id = %s, want %s", zone.OwnerAgentID, childID)
	}

	// Verify kinship recorded in SQLite
	childDB, err := db.OpenAgentDBForAgent(r, childID)
	if err != nil {
		t.Fatalf("opening childDB: %v", err)
	}
	childRels, _ := childDB.ListRelationships(ctx)
	_ = childDB.Close()
	foundParents := 0
	for _, rel := range childRels {
		if rel.KinshipRole == KinshipRoleParent {
			foundParents++
		}
	}
	if foundParents != 2 {
		t.Errorf("child recorded %d parents, want 2", foundParents)
	}

	// Verify spawner and launcher interactions
	if !result.EntitySpawned {
		t.Errorf("expected entity to be spawned")
	}
	if len(spawner.spawnReqs) != 1 {
		t.Fatalf("spawner received %d spawn requests, want 1", len(spawner.spawnReqs))
	}
	spawnReq := spawner.spawnReqs[0]
	if !spawnReq.IsChild {
		t.Errorf("spawn request is_child was false")
	}
	if spawnReq.AgentId != childID {
		t.Errorf("spawn request agent_id = %s, want %s", spawnReq.AgentId, childID)
	}

	if !result.ProcessStarted {
		t.Errorf("expected child process to be started")
	}
	if len(launcher.startedAgents) != 1 || launcher.startedAgents[0] != childID {
		t.Errorf("launcher started agents = %+v, want [%s]", launcher.startedAgents, childID)
	}

	// Second evaluation immediately after: should be ineligible due to no unclaimed beds
	res2, err := gov.Evaluate(ctx, clanID)
	if err != nil {
		t.Fatalf("Evaluate error on second check: %v", err)
	}
	if res2.Eligible {
		t.Errorf("expected second evaluation to be ineligible after housing was claimed")
	}
}
