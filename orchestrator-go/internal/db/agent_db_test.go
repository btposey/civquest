package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestAutoMigrationAndSchema(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_db_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDir := filepath.Join(tempDir, "agent_conan")
	agentDB, err := OpenAgentDB(agentDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	// Verify database files exist on disk
	memFile := filepath.Join(agentDir, "memory.db")
	if _, err := os.Stat(memFile); os.IsNotExist(err) {
		t.Fatalf("expected memory.db to exist at %s", memFile)
	}
	mapFile := filepath.Join(agentDir, "map_graph.db")
	if _, err := os.Stat(mapFile); os.IsNotExist(err) {
		t.Fatalf("expected map_graph.db to exist at %s", mapFile)
	}

	// Verify PRAGMAs on memory.db
	ctx := context.Background()
	var journalMode string
	if err := agentDB.MemoryDB().QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected memory.db journal_mode=wal, got %s", journalMode)
	}

	var busyTimeout int
	if err := agentDB.MemoryDB().QueryRowContext(ctx, "PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Errorf("expected memory.db busy_timeout >= 5000, got %d", busyTimeout)
	}

	var synchronous int
	if err := agentDB.MemoryDB().QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&synchronous); err != nil {
		t.Fatalf("failed to query synchronous: %v", err)
	}
	if synchronous != 1 { // 1 = NORMAL
		t.Errorf("expected memory.db synchronous=1 (NORMAL), got %d", synchronous)
	}

	// Verify tables in memory.db
	expectedMemTables := []string{"episodic_memories", "episodic_memories_vec", "relationships", "agent_skills"}
	for _, tbl := range expectedMemTables {
		var name string
		err := agentDB.MemoryDB().QueryRowContext(ctx, "SELECT name FROM sqlite_schema WHERE name = ?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("expected table %s in memory.db, got error: %v", tbl, err)
		}
	}

	// Verify tables in map_graph.db
	expectedMapTables := []string{"map_nodes", "map_edges"}
	for _, tbl := range expectedMapTables {
		var name string
		err := agentDB.MapDB().QueryRowContext(ctx, "SELECT name FROM sqlite_schema WHERE name = ?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("expected table %s in map_graph.db, got error: %v", tbl, err)
		}
	}

	// Test migration idempotency by opening again
	agentDB2, err := OpenAgentDB(agentDir)
	if err != nil {
		t.Fatalf("re-opening AgentDB failed: %v", err)
	}
	_ = agentDB2.Close()

	// Test OpenAgentDBForAgent with XDG resolver
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tempDir))
	agentDBXDG, err := OpenAgentDBForAgent(resolver, "agent_xdg")
	if err != nil {
		t.Fatalf("OpenAgentDBForAgent failed: %v", err)
	}
	defer agentDBXDG.Close()

	expectedPath := resolver.AgentDataDir("agent_xdg")
	if agentDBXDG.DataDir() != expectedPath {
		t.Errorf("expected DataDir %s, got %s", expectedPath, agentDBXDG.DataDir())
	}
}

func TestEpisodicMemoryCRUDAndKNN(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_memory_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()

	// Construct three 768-dim embeddings
	// vec1: High values at start (combat semantic vector)
	vecCombat := make([]float32, 768)
	for i := 0; i < 100; i++ {
		vecCombat[i] = 1.0
	}

	// vec2: High values in middle (trade semantic vector)
	vecTrade := make([]float32, 768)
	for i := 300; i < 400; i++ {
		vecTrade[i] = 1.0
	}

	// vec3: High values at end (farming semantic vector)
	vecFarming := make([]float32, 768)
	for i := 600; i < 700; i++ {
		vecFarming[i] = 1.0
	}

	// Insert memories
	mem1 := EpisodicMemory{
		WorldTick:      1000,
		EventCategory:  "COMBAT",
		SaliencyScore:  0.95,
		ActorsJSON:     `["zombie_1", "skeleton_2"]`,
		LocationCoords: "100,64,200",
		SummaryText:    "Defended village gate against nighttime zombie assault.",
		Chronicled:     false,
	}
	id1, err := agentDB.InsertEpisodicMemory(ctx, mem1, vecCombat)
	if err != nil {
		t.Fatalf("failed to insert mem1: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("expected positive memory id, got %d", id1)
	}

	mem2 := EpisodicMemory{
		WorldTick:      1200,
		EventCategory:  "TRADE",
		SaliencyScore:  0.75,
		ActorsJSON:     `["player_steve"]`,
		LocationCoords: "110,64,195",
		SummaryText:    "Traded 16 emeralds for enchanted iron sword with Steve.",
		Chronicled:     true,
	}
	id2, err := agentDB.InsertEpisodicMemory(ctx, mem2, vecTrade)
	if err != nil {
		t.Fatalf("failed to insert mem2: %v", err)
	}

	mem3 := EpisodicMemory{
		WorldTick:      1400,
		EventCategory:  "DISCOVERY",
		SaliencyScore:  0.60,
		ActorsJSON:     `[]`,
		LocationCoords: "200,64,400",
		SummaryText:    "Discovered fertile river basin for wheat fields.",
		Chronicled:     false,
	}
	id3, err := agentDB.InsertEpisodicMemory(ctx, mem3, vecFarming)
	if err != nil {
		t.Fatalf("failed to insert mem3: %v", err)
	}

	// Query with vector similar to combat
	queryCombat := make([]float32, 768)
	for i := 0; i < 100; i++ {
		queryCombat[i] = 0.95
	}
	queryCombat[0] = 1.2

	matches, err := agentDB.SearchEpisodicMemories(ctx, queryCombat, 2)
	if err != nil {
		t.Fatalf("SearchEpisodicMemories failed: %v", err)
	}

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches with limit=2, got %d", len(matches))
	}

	// Top match must be mem1 (combat) with very high similarity
	if matches[0].Memory.ID != id1 {
		t.Errorf("expected top match to be id %d (combat), got %d (%s)", id1, matches[0].Memory.ID, matches[0].Memory.EventCategory)
	}
	if matches[0].Similarity < 0.95 {
		t.Errorf("expected high similarity for combat match, got %f", matches[0].Similarity)
	}
	if matches[0].Memory.SummaryText != mem1.SummaryText {
		t.Errorf("expected summary %q, got %q", mem1.SummaryText, matches[0].Memory.SummaryText)
	}

	// Other matches should have near 0 similarity
	if matches[1].Similarity > 0.1 {
		t.Errorf("expected near zero similarity for orthogonal vector, got %f", matches[1].Similarity)
	}

	// Query with vector similar to trade
	queryTrade := make([]float32, 768)
	for i := 300; i < 400; i++ {
		queryTrade[i] = 1.0
	}
	matchesTrade, err := agentDB.SearchEpisodicMemories(ctx, queryTrade, 1)
	if err != nil {
		t.Fatalf("SearchEpisodicMemories for trade failed: %v", err)
	}
	if len(matchesTrade) != 1 || matchesTrade[0].Memory.ID != id2 {
		t.Errorf("expected top trade match to be id %d", id2)
	}

	// Test memory insertion without embedding (should still succeed in episodic_memories)
	memNoVec := EpisodicMemory{
		WorldTick:      1500,
		EventCategory:  "CHAT",
		SaliencyScore:  0.2,
		ActorsJSON:     `["npc_bob"]`,
		LocationCoords: "100,64,100",
		SummaryText:    "Said good morning to Bob.",
	}
	id4, err := agentDB.InsertEpisodicMemory(ctx, memNoVec, nil)
	if err != nil {
		t.Fatalf("inserting memory without embedding failed: %v", err)
	}
	if id4 <= id3 {
		t.Fatalf("expected id4 > id3, got %d <= %d", id4, id3)
	}

	// Test empty query embedding error
	_, err = agentDB.SearchEpisodicMemories(ctx, nil, 5)
	if err != ErrEmptyQueryEmbedding {
		t.Errorf("expected ErrEmptyQueryEmbedding, got %v", err)
	}
}

func TestRelationshipCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_rel_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()

	// Initial check on non-existent relationship
	rel, err := agentDB.GetRelationship(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("unexpected error on missing relationship: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected nil for non-existent relationship, got %+v", rel)
	}

	// Insert relationship with Steve (PLAYER)
	steveUUID := "c0a80001-0000-0000-0000-000000000001"
	steveRel := Relationship{
		EntityUUID:          steveUUID,
		EntityName:          "Steve",
		EntityType:          "PLAYER",
		DispositionScore:    15.0,
		Familiarity:         0.25,
		KinshipRole:         "NONE",
		LastInteractionTick: 500,
	}
	if err := agentDB.UpsertRelationship(ctx, steveRel); err != nil {
		t.Fatalf("failed to insert Steve relationship: %v", err)
	}

	// Insert relationship with Alex (SETTLER)
	alexUUID := "c0a80001-0000-0000-0000-000000000002"
	alexRel := Relationship{
		EntityUUID:          alexUUID,
		EntityName:          "Alex",
		EntityType:          "SETTLER",
		DispositionScore:    40.0,
		Familiarity:         0.60,
		KinshipRole:         "SPOUSE",
		LastInteractionTick: 1200,
	}
	if err := agentDB.UpsertRelationship(ctx, alexRel); err != nil {
		t.Fatalf("failed to insert Alex relationship: %v", err)
	}

	// Retrieve Steve
	gotSteve, err := agentDB.GetRelationship(ctx, steveUUID)
	if err != nil {
		t.Fatalf("failed to get Steve relationship: %v", err)
	}
	if gotSteve == nil {
		t.Fatalf("expected Steve relationship to be found")
	}
	if gotSteve.EntityName != "Steve" || gotSteve.EntityType != "PLAYER" || gotSteve.DispositionScore != 15.0 {
		t.Errorf("mismatched Steve fields: %+v", gotSteve)
	}

	// Update Steve (improved disposition and familiarity)
	steveRel.DispositionScore = 55.0
	steveRel.Familiarity = 0.80
	steveRel.LastInteractionTick = 2000
	if err := agentDB.UpsertRelationship(ctx, steveRel); err != nil {
		t.Fatalf("failed to update Steve relationship: %v", err)
	}

	updatedSteve, err := agentDB.GetRelationship(ctx, steveUUID)
	if err != nil {
		t.Fatalf("failed to get updated Steve: %v", err)
	}
	if updatedSteve.DispositionScore != 55.0 || updatedSteve.Familiarity != 0.80 || updatedSteve.LastInteractionTick != 2000 {
		t.Errorf("updated Steve fields not reflected: %+v", updatedSteve)
	}

	// List relationships: Steve (tick 2000) should be first, Alex (tick 1200) second
	list, err := agentDB.ListRelationships(ctx)
	if err != nil {
		t.Fatalf("failed to list relationships: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(list))
	}
	if list[0].EntityUUID != steveUUID || list[1].EntityUUID != alexUUID {
		t.Errorf("unexpected list ordering: %+v", list)
	}

	// Invalid entity type validation
	invalidRel := Relationship{
		EntityUUID: "invalid-type-uuid",
		EntityName: "Zombie",
		EntityType: "MONSTER", // Invalid, only PLAYER or SETTLER permitted
	}
	if err := agentDB.UpsertRelationship(ctx, invalidRel); err == nil {
		t.Errorf("expected error for invalid entity_type MONSTER, got nil")
	}

	// Delete relationship
	if err := agentDB.DeleteRelationship(ctx, steveUUID); err != nil {
		t.Fatalf("failed to delete Steve: %v", err)
	}
	afterDel, err := agentDB.GetRelationship(ctx, steveUUID)
	if err != nil || afterDel != nil {
		t.Errorf("expected Steve to be deleted, got err=%v, rel=%+v", err, afterDel)
	}
}

func TestSkillCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_skill_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()

	// Check non-existent skill
	s, err := agentDB.GetSkill(ctx, "WOODCUTTING")
	if err != nil {
		t.Fatalf("unexpected error on missing skill: %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil for missing skill, got %+v", s)
	}

	// Insert primary skill
	farmSkill := Skill{
		SkillName:      "FARMING",
		Category:       "PRIMARY",
		XP:             250,
		Level:          2,
		WorkstationPos: "120,64,150",
	}
	if err := agentDB.UpsertSkill(ctx, farmSkill); err != nil {
		t.Fatalf("failed to insert FARMING: %v", err)
	}

	// Insert secondary hobby skill
	fishSkill := Skill{
		SkillName:      "FISHING",
		Category:       "SECONDARY_HOBBY",
		XP:             50,
		Level:          1,
		WorkstationPos: "180,62,110",
	}
	if err := agentDB.UpsertSkill(ctx, fishSkill); err != nil {
		t.Fatalf("failed to insert FISHING: %v", err)
	}

	// Retrieve FARMING
	gotFarm, err := agentDB.GetSkill(ctx, "FARMING")
	if err != nil {
		t.Fatalf("failed to get FARMING: %v", err)
	}
	if gotFarm == nil || gotFarm.XP != 250 || gotFarm.Level != 2 || gotFarm.WorkstationPos != "120,64,150" {
		t.Fatalf("unexpected FARMING skill values: %+v", gotFarm)
	}

	// Update FARMING progression (level up to 3)
	farmSkill.Level = 3
	farmSkill.XP = 1000
	if err := agentDB.UpsertSkill(ctx, farmSkill); err != nil {
		t.Fatalf("failed to update FARMING: %v", err)
	}

	updatedFarm, err := agentDB.GetSkill(ctx, "FARMING")
	if err != nil || updatedFarm.Level != 3 || updatedFarm.XP != 1000 {
		t.Fatalf("FARMING update not applied: %+v (err=%v)", updatedFarm, err)
	}

	// List skills
	skills, err := agentDB.ListSkills(ctx)
	if err != nil {
		t.Fatalf("failed to list skills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Test invalid category
	badSkill := Skill{
		SkillName: "BLACKSMITHING",
		Category:  "TERTIARY", // Invalid
	}
	if err := agentDB.UpsertSkill(ctx, badSkill); err == nil {
		t.Errorf("expected error for invalid skill category, got nil")
	}
}

func TestMapGraphCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_map_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()

	// Initial empty graph
	nodes, edges, err := agentDB.GetMapGraph(ctx)
	if err != nil {
		t.Fatalf("GetMapGraph failed: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("expected empty map graph, got %d nodes and %d edges", len(nodes), len(edges))
	}

	// Add nodes
	n1 := MapNode{
		ID:             "node-gate",
		X:              100.0,
		Y:              64.0,
		Z:              200.0,
		Label:          "Town Gate",
		Category:       "DEFENSE",
		SafetyScore:    0.9,
		DiscoveredTick: 100,
	}
	if err := agentDB.AddMapNode(ctx, n1); err != nil {
		t.Fatalf("failed to add node-gate: %v", err)
	}

	n2 := MapNode{
		ID:             "node-tavern",
		X:              150.0,
		Y:              64.0,
		Z:              220.0,
		Label:          "The Drunken Creeper Tavern",
		Category:       "CIVIC",
		SafetyScore:    1.0,
		DiscoveredTick: 150,
	}
	if err := agentDB.AddMapNode(ctx, n2); err != nil {
		t.Fatalf("failed to add node-tavern: %v", err)
	}

	// Get node-gate
	gotNode, err := agentDB.GetMapNode(ctx, "node-gate")
	if err != nil {
		t.Fatalf("failed to get node-gate: %v", err)
	}
	if gotNode == nil || gotNode.Label != "Town Gate" || gotNode.X != 100.0 {
		t.Fatalf("unexpected node values: %+v", gotNode)
	}

	// Add edge
	e1 := MapEdge{
		ID:          "edge-gate-tavern",
		FromNodeID:  "node-gate",
		ToNodeID:    "node-tavern",
		Distance:    53.85,
		DangerLevel: 0.05,
		IsPaved:     true,
	}
	if err := agentDB.AddMapEdge(ctx, e1); err != nil {
		t.Fatalf("failed to add edge: %v", err)
	}

	// Retrieve graph
	nodes, edges, err = agentDB.GetMapGraph(ctx)
	if err != nil {
		t.Fatalf("GetMapGraph failed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].FromNodeID != "node-gate" || edges[0].ToNodeID != "node-tavern" || !edges[0].IsPaved {
		t.Fatalf("unexpected edge data: %+v", edges[0])
	}

	// Update node safety score
	n1.SafetyScore = 0.6
	if err := agentDB.AddMapNode(ctx, n1); err != nil {
		t.Fatalf("failed to update node safety score: %v", err)
	}
	updatedNode, _ := agentDB.GetMapNode(ctx, "node-gate")
	if updatedNode.SafetyScore != 0.6 {
		t.Errorf("expected updated safety score 0.6, got %f", updatedNode.SafetyScore)
	}
}

func TestConcurrencyAndWAL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_wal_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}
	defer agentDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const numWriters = 5
	const numReaders = 10
	const iterations = 20

	var wg sync.WaitGroup
	errCh := make(chan error, (numWriters+numReaders)*iterations)

	// Launch concurrent writers
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Insert memory
				vec := make([]float32, 768)
				vec[0] = float32(workerID + 1)
				vec[workerID+1] = float32(i + 1)

				mem := EpisodicMemory{
					WorldTick:      int64(1000 + i),
					EventCategory:  "COMBAT",
					SaliencyScore:  0.8,
					ActorsJSON:     `[]`,
					LocationCoords: fmt.Sprintf("%d,64,%d", workerID, i),
					SummaryText:    fmt.Sprintf("Worker %d action %d", workerID, i),
				}
				if _, err := agentDB.InsertEpisodicMemory(ctx, mem, vec); err != nil {
					errCh <- fmt.Errorf("writer %d mem insert error: %w", workerID, err)
					return
				}

				// Upsert relationship
				rel := Relationship{
					EntityUUID:          fmt.Sprintf("uuid-worker-%d", workerID),
					EntityName:          fmt.Sprintf("Settler_%d", workerID),
					EntityType:          "SETTLER",
					DispositionScore:    float64(i * 2),
					Familiarity:         float64(i) * 0.05,
					KinshipRole:         "NONE",
					LastInteractionTick: int64(i),
				}
				if err := agentDB.UpsertRelationship(ctx, rel); err != nil {
					errCh <- fmt.Errorf("writer %d rel upsert error: %w", workerID, err)
					return
				}

				// Add map node
				node := MapNode{
					ID:             fmt.Sprintf("node-w%d-i%d", workerID, i),
					X:              float64(workerID * 10),
					Y:              64.0,
					Z:              float64(i * 10),
					Label:          fmt.Sprintf("Waypoint %d-%d", workerID, i),
					Category:       "NAVIGATION",
					SafetyScore:    1.0,
					DiscoveredTick: int64(i),
				}
				if err := agentDB.AddMapNode(ctx, node); err != nil {
					errCh <- fmt.Errorf("writer %d node insert error: %w", workerID, err)
					return
				}
			}
		}(w)
	}

	// Launch concurrent readers
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			queryVec := make([]float32, 768)
			queryVec[0] = 1.0

			for i := 0; i < iterations; i++ {
				// Search memories
				_, err := agentDB.SearchEpisodicMemories(ctx, queryVec, 5)
				if err != nil {
					errCh <- fmt.Errorf("reader %d memory search error: %w", readerID, err)
					return
				}

				// List relationships
				_, err = agentDB.ListRelationships(ctx)
				if err != nil {
					errCh <- fmt.Errorf("reader %d rel list error: %w", readerID, err)
					return
				}

				// Get map graph
				_, _, err = agentDB.GetMapGraph(ctx)
				if err != nil {
					errCh <- fmt.Errorf("reader %d map graph error: %w", readerID, err)
					return
				}
			}
		}(r)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}

	// Verify all inserted memories were stored
	queryVec := make([]float32, 768)
	queryVec[0] = 1.0
	matches, err := agentDB.SearchEpisodicMemories(ctx, queryVec, 1000)
	if err != nil {
		t.Fatalf("final search failed: %v", err)
	}

	expectedCount := numWriters * iterations
	if len(matches) != expectedCount {
		t.Errorf("expected %d total episodic memories, got %d", expectedCount, len(matches))
	}
}

func TestCloseAndClosedErrors(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_close_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := OpenAgentDB(tempDir)
	if err != nil {
		t.Fatalf("OpenAgentDB failed: %v", err)
	}

	ctx := context.Background()

	// Verify Close succeeds cleanly
	if err := agentDB.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify idempotent Close
	if err := agentDB.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}

	// Verify operations on closed AgentDB return ErrClosed
	if _, err := agentDB.InsertEpisodicMemory(ctx, EpisodicMemory{}, []float32{1.0}); err != ErrClosed {
		t.Errorf("expected ErrClosed on InsertEpisodicMemory, got %v", err)
	}
	if _, err := agentDB.SearchEpisodicMemories(ctx, []float32{1.0}, 5); err != ErrClosed {
		t.Errorf("expected ErrClosed on SearchEpisodicMemories, got %v", err)
	}
	if err := agentDB.UpsertRelationship(ctx, Relationship{EntityUUID: "u", EntityType: "PLAYER"}); err != ErrClosed {
		t.Errorf("expected ErrClosed on UpsertRelationship, got %v", err)
	}
	if _, err := agentDB.GetRelationship(ctx, "u"); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetRelationship, got %v", err)
	}
	if _, err := agentDB.ListRelationships(ctx); err != ErrClosed {
		t.Errorf("expected ErrClosed on ListRelationships, got %v", err)
	}
	if err := agentDB.DeleteRelationship(ctx, "u"); err != ErrClosed {
		t.Errorf("expected ErrClosed on DeleteRelationship, got %v", err)
	}
	if err := agentDB.UpsertSkill(ctx, Skill{SkillName: "s", Category: "PRIMARY"}); err != ErrClosed {
		t.Errorf("expected ErrClosed on UpsertSkill, got %v", err)
	}
	if _, err := agentDB.GetSkill(ctx, "s"); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetSkill, got %v", err)
	}
	if _, err := agentDB.ListSkills(ctx); err != ErrClosed {
		t.Errorf("expected ErrClosed on ListSkills, got %v", err)
	}
	if err := agentDB.AddMapNode(ctx, MapNode{ID: "n"}); err != ErrClosed {
		t.Errorf("expected ErrClosed on AddMapNode, got %v", err)
	}
	if _, err := agentDB.GetMapNode(ctx, "n"); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetMapNode, got %v", err)
	}
	if err := agentDB.AddMapEdge(ctx, MapEdge{ID: "e", FromNodeID: "a", ToNodeID: "b"}); err != ErrClosed {
		t.Errorf("expected ErrClosed on AddMapEdge, got %v", err)
	}
	if _, _, err := agentDB.GetMapGraph(ctx); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetMapGraph, got %v", err)
	}
}
