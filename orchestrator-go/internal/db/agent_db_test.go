package db

import (
	"context"
	"errors"
	"fmt"
	"math"
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

	// Test migration idempotency and hasVec0 detection by reopening
	hasVec0Original := agentDB.HasVec0()
	agentDB2, err := OpenAgentDB(agentDir)
	if err != nil {
		t.Fatalf("re-opening AgentDB failed: %v", err)
	}
	if agentDB2.HasVec0() != hasVec0Original {
		t.Errorf("expected hasVec0=%v on reopen, got %v", hasVec0Original, agentDB2.HasVec0())
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

	// Test OpenAgentDBForAgent path traversal rejection
	maliciousNames := []string{
		"../escape",
		"../../etc/passwd",
		"agent/nested",
		"agent\\windows",
		"",
		"   ",
		".",
		"..",
		"agent_name_that_exceeds_the_maximum_allowed_length_of_32_characters",
		"-leading_hyphen",
	}
	for _, badName := range maliciousNames {
		_, pErr := OpenAgentDBForAgent(resolver, badName)
		if pErr == nil {
			t.Errorf("expected error for invalid/malicious agentName %q, got nil", badName)
		}
	}

	// Verify SQL CHECK constraints
	t.Run("check constraints on episodic_memories", func(t *testing.T) {
		// Invalid event_category
		badCatMem := EpisodicMemory{
			WorldTick:      100,
			EventCategory:  "UNKNOWN_CATEGORY",
			SaliencyScore:  0.5,
			ActorsJSON:     "[]",
			LocationCoords: "0,0,0",
			SummaryText:    "Invalid category test",
		}
		if _, cErr := agentDB.InsertEpisodicMemory(ctx, badCatMem, nil); cErr == nil {
			t.Errorf("expected error inserting memory with invalid event_category, got nil")
		}

		// Saliency score < 0
		lowSaliencyMem := EpisodicMemory{
			WorldTick:      100,
			EventCategory:  "COMBAT",
			SaliencyScore:  -0.1,
			ActorsJSON:     "[]",
			LocationCoords: "0,0,0",
			SummaryText:    "Negative saliency",
		}
		if _, cErr := agentDB.InsertEpisodicMemory(ctx, lowSaliencyMem, nil); cErr == nil {
			t.Errorf("expected error inserting memory with negative saliency score, got nil")
		}

		// Saliency score > 1.0
		highSaliencyMem := EpisodicMemory{
			WorldTick:      100,
			EventCategory:  "COMBAT",
			SaliencyScore:  1.5,
			ActorsJSON:     "[]",
			LocationCoords: "0,0,0",
			SummaryText:    "Too high saliency",
		}
		if _, cErr := agentDB.InsertEpisodicMemory(ctx, highSaliencyMem, nil); cErr == nil {
			t.Errorf("expected error inserting memory with saliency > 1.0, got nil")
		}
	})

	t.Run("check constraints on relationships", func(t *testing.T) {
		// Invalid disposition score > 100
		badRel := Relationship{
			EntityUUID:       "00000000-0000-0000-0000-000000000099",
			EntityName:       "BadRel",
			EntityType:       "PLAYER",
			DispositionScore: 105.0,
			Familiarity:      0.5,
			KinshipRole:      "NONE",
		}
		if rErr := agentDB.UpsertRelationship(ctx, badRel); rErr == nil {
			t.Errorf("expected error for disposition score > 100, got nil")
		}

		// Invalid familiarity > 1.0
		badRel.DispositionScore = 50.0
		badRel.Familiarity = 1.2
		if rErr := agentDB.UpsertRelationship(ctx, badRel); rErr == nil {
			t.Errorf("expected error for familiarity > 1.0, got nil")
		}

		// Invalid kinship role
		badRel.Familiarity = 0.5
		badRel.KinshipRole = "BEST_FRIEND"
		if rErr := agentDB.UpsertRelationship(ctx, badRel); rErr == nil {
			t.Errorf("expected error for invalid kinship role, got nil")
		}
	})

	t.Run("check constraints on map_nodes and map_edges", func(t *testing.T) {
		// Invalid safety score > 1.0
		badNode := MapNode{
			ID:          "node-bad",
			X:           10,
			Y:           64,
			Z:           10,
			Label:       "Bad",
			Category:    "CIVIC",
			SafetyScore: 1.5,
		}
		if nErr := agentDB.AddMapNode(ctx, badNode); nErr == nil {
			t.Errorf("expected error for map node safety_score > 1.0, got nil")
		}

		// Valid node for edge test
		goodNodeA := MapNode{ID: "node-a", X: 0, Y: 64, Z: 0, Label: "A", Category: "CIVIC", SafetyScore: 1.0}
		goodNodeB := MapNode{ID: "node-b", X: 10, Y: 64, Z: 0, Label: "B", Category: "CIVIC", SafetyScore: 1.0}
		_ = agentDB.AddMapNode(ctx, goodNodeA)
		_ = agentDB.AddMapNode(ctx, goodNodeB)

		// Negative distance edge
		badEdge := MapEdge{
			ID:          "edge-bad",
			FromNodeID:  "node-a",
			ToNodeID:    "node-b",
			Distance:    -10.0,
			DangerLevel: 0.0,
		}
		if eErr := agentDB.AddMapEdge(ctx, badEdge); eErr == nil {
			t.Errorf("expected error for map edge distance < 0, got nil")
		}
	})
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
		SaliencyScore:  0.80,
		ActorsJSON:     `["player_steve"]`,
		LocationCoords: "150,64,220",
		SummaryText:    "Bartered 12 emeralds for enchanted iron chestplate.",
		Chronicled:     false,
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

	// Test GetEpisodicMemory
	t.Run("GetEpisodicMemory", func(t *testing.T) {
		got, gErr := agentDB.GetEpisodicMemory(ctx, id1)
		if gErr != nil {
			t.Fatalf("GetEpisodicMemory failed: %v", gErr)
		}
		if got == nil {
			t.Fatalf("expected memory with ID %d, got nil", id1)
		}
		if got.SummaryText != mem1.SummaryText || got.EventCategory != "COMBAT" {
			t.Errorf("mismatched memory content: %+v", got)
		}

		missing, mErr := agentDB.GetEpisodicMemory(ctx, 999999)
		if mErr != nil {
			t.Fatalf("unexpected error on missing memory: %v", mErr)
		}
		if missing != nil {
			t.Errorf("expected nil for non-existent memory, got %+v", missing)
		}
	})

	// Test MarkMemoryChronicled
	t.Run("MarkMemoryChronicled", func(t *testing.T) {
		if cErr := agentDB.MarkMemoryChronicled(ctx, id1); cErr != nil {
			t.Fatalf("MarkMemoryChronicled failed: %v", cErr)
		}
		got, _ := agentDB.GetEpisodicMemory(ctx, id1)
		if got == nil || !got.Chronicled {
			t.Errorf("expected memory %d to have chronicled=true", id1)
		}

		// Non-existent ID returns ErrNotFound
		if cErr := agentDB.MarkMemoryChronicled(ctx, 999999); !errors.Is(cErr, ErrNotFound) {
			t.Errorf("expected ErrNotFound for non-existent memory, got %v", cErr)
		}
	})

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

	// Test invalid embedding validations
	t.Run("vector dimension and non-finite validation", func(t *testing.T) {
		// Insert with invalid dimension (100 instead of 768)
		_, err = agentDB.InsertEpisodicMemory(ctx, memNoVec, make([]float32, 100))
		if !errors.Is(err, ErrInvalidVectorDimension) {
			t.Errorf("expected ErrInvalidVectorDimension on insert, got %v", err)
		}

		// Insert with NaN element
		nanVec := make([]float32, 768)
		nanVec[50] = float32(math.NaN())
		_, err = agentDB.InsertEpisodicMemory(ctx, memNoVec, nanVec)
		if !errors.Is(err, ErrNonFiniteVector) {
			t.Errorf("expected ErrNonFiniteVector on insert, got %v", err)
		}

		// Search with empty vector
		_, err = agentDB.SearchEpisodicMemories(ctx, nil, 5)
		if err != ErrEmptyQueryEmbedding {
			t.Errorf("expected ErrEmptyQueryEmbedding, got %v", err)
		}

		// Search with invalid dimension
		_, err = agentDB.SearchEpisodicMemories(ctx, make([]float32, 100), 5)
		if !errors.Is(err, ErrInvalidVectorDimension) {
			t.Errorf("expected ErrInvalidVectorDimension on search, got %v", err)
		}

		// Search with NaN element
		_, err = agentDB.SearchEpisodicMemories(ctx, nanVec, 5)
		if !errors.Is(err, ErrNonFiniteVector) {
			t.Errorf("expected ErrNonFiniteVector on search, got %v", err)
		}
	})
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
		LastInteractionTick: 600,
	}
	if err := agentDB.UpsertRelationship(ctx, alexRel); err != nil {
		t.Fatalf("failed to insert Alex relationship: %v", err)
	}

	// Fetch Alex relationship
	gotAlex, err := agentDB.GetRelationship(ctx, alexUUID)
	if err != nil {
		t.Fatalf("failed to fetch Alex relationship: %v", err)
	}
	if gotAlex == nil {
		t.Fatalf("expected Alex relationship, got nil")
	}
	if gotAlex.KinshipRole != "SPOUSE" || gotAlex.DispositionScore != 40.0 {
		t.Errorf("mismatch in Alex relationship fields: %+v", gotAlex)
	}

	// Update Steve relationship
	steveRel.DispositionScore = 25.0
	steveRel.LastInteractionTick = 700
	if err := agentDB.UpsertRelationship(ctx, steveRel); err != nil {
		t.Fatalf("failed to update Steve relationship: %v", err)
	}

	// List relationships
	rels, err := agentDB.ListRelationships(ctx)
	if err != nil {
		t.Fatalf("ListRelationships failed: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	// Steve should be first due to last_interaction_tick = 700 vs Alex 600
	if rels[0].EntityUUID != steveUUID {
		t.Errorf("expected Steve to be first, got %s", rels[0].EntityName)
	}

	// Delete relationship
	if err := agentDB.DeleteRelationship(ctx, steveUUID); err != nil {
		t.Fatalf("failed to delete Steve relationship: %v", err)
	}

	deletedRel, err := agentDB.GetRelationship(ctx, steveUUID)
	if err != nil {
		t.Fatalf("error checking deleted relationship: %v", err)
	}
	if deletedRel != nil {
		t.Fatalf("expected nil for deleted relationship, got %+v", deletedRel)
	}

	// Validation checks
	invalidRel := Relationship{
		EntityUUID: "some-uuid",
		EntityType: "MONSTER", // invalid entity_type
	}
	if err := agentDB.UpsertRelationship(ctx, invalidRel); err == nil {
		t.Errorf("expected error for invalid entity_type, got nil")
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

	// Initial check on non-existent skill
	sk, err := agentDB.GetSkill(ctx, "blacksmithing")
	if err != nil {
		t.Fatalf("unexpected error querying non-existent skill: %v", err)
	}
	if sk != nil {
		t.Fatalf("expected nil for missing skill, got %+v", sk)
	}

	// Insert primary career skill
	bs := Skill{
		SkillName:      "blacksmithing",
		Category:       "PRIMARY",
		XP:             150,
		Level:          2,
		WorkstationPos: "102,65,204",
	}
	if err := agentDB.UpsertSkill(ctx, bs); err != nil {
		t.Fatalf("failed to insert blacksmithing: %v", err)
	}

	// Insert secondary hobby skill
	fish := Skill{
		SkillName:      "fishing",
		Category:       "SECONDARY_HOBBY",
		XP:             20,
		Level:          1,
		WorkstationPos: "",
	}
	if err := agentDB.UpsertSkill(ctx, fish); err != nil {
		t.Fatalf("failed to insert fishing: %v", err)
	}

	// Update blacksmithing XP and level
	bs.XP = 320
	bs.Level = 3
	if err := agentDB.UpsertSkill(ctx, bs); err != nil {
		t.Fatalf("failed to update blacksmithing: %v", err)
	}

	// Fetch updated skill
	gotBS, err := agentDB.GetSkill(ctx, "blacksmithing")
	if err != nil {
		t.Fatalf("failed to get blacksmithing: %v", err)
	}
	if gotBS == nil || gotBS.Level != 3 || gotBS.XP != 320 {
		t.Fatalf("unexpected skill values: %+v", gotBS)
	}

	// List skills
	skills, err := agentDB.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills failed: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	// PRIMARY should come before SECONDARY_HOBBY
	if skills[0].SkillName != "blacksmithing" {
		t.Errorf("expected blacksmithing first, got %s", skills[0].SkillName)
	}

	// Validation checks
	invalidSkill := Skill{
		SkillName: "invalid",
		Category:  "THIRD_HOBBY",
	}
	if err := agentDB.UpsertSkill(ctx, invalidSkill); err == nil {
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
		t.Fatalf("GetMapGraph on empty DB failed: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("expected empty graph, got %d nodes, %d edges", len(nodes), len(edges))
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

	// Test GetMapEdge
	gotEdge, err := agentDB.GetMapEdge(ctx, "edge-gate-tavern")
	if err != nil {
		t.Fatalf("GetMapEdge failed: %v", err)
	}
	if gotEdge == nil || gotEdge.Distance != 53.85 || !gotEdge.IsPaved {
		t.Fatalf("unexpected edge data: %+v", gotEdge)
	}

	missingEdge, err := agentDB.GetMapEdge(ctx, "nonexistent-edge")
	if err != nil {
		t.Fatalf("unexpected error on missing edge: %v", err)
	}
	if missingEdge != nil {
		t.Fatalf("expected nil for missing edge, got %+v", missingEdge)
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

	gotNodeUpdated, _ := agentDB.GetMapNode(ctx, "node-gate")
	if gotNodeUpdated.SafetyScore != 0.6 {
		t.Errorf("expected updated safety score 0.6, got %f", gotNodeUpdated.SafetyScore)
	}

	// Test DeleteMapEdge
	if dErr := agentDB.DeleteMapEdge(ctx, "edge-gate-tavern"); dErr != nil {
		t.Fatalf("DeleteMapEdge failed: %v", dErr)
	}
	if dErr := agentDB.DeleteMapEdge(ctx, "edge-gate-tavern"); !errors.Is(dErr, ErrNotFound) {
		t.Errorf("expected ErrNotFound on second DeleteMapEdge, got %v", dErr)
	}

	// Re-add edge to test cascade delete when deleting node
	if aErr := agentDB.AddMapEdge(ctx, e1); aErr != nil {
		t.Fatalf("re-adding edge failed: %v", aErr)
	}

	// Test DeleteMapNode with cascade
	if dErr := agentDB.DeleteMapNode(ctx, "node-gate"); dErr != nil {
		t.Fatalf("DeleteMapNode failed: %v", dErr)
	}
	if dErr := agentDB.DeleteMapNode(ctx, "node-gate"); !errors.Is(dErr, ErrNotFound) {
		t.Errorf("expected ErrNotFound on second DeleteMapNode, got %v", dErr)
	}

	// Incident edge should be cascaded away
	eAfter, _ := agentDB.GetMapEdge(ctx, "edge-gate-tavern")
	if eAfter != nil {
		t.Errorf("expected edge-gate-tavern to be cascade deleted, got %+v", eAfter)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "civquest_concurrent_test_*")
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

	dummyVec := make([]float32, 768)

	// Verify operations on closed AgentDB return ErrClosed
	if _, err := agentDB.InsertEpisodicMemory(ctx, EpisodicMemory{}, dummyVec); err != ErrClosed {
		t.Errorf("expected ErrClosed on InsertEpisodicMemory, got %v", err)
	}
	if _, err := agentDB.GetEpisodicMemory(ctx, 1); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetEpisodicMemory, got %v", err)
	}
	if err := agentDB.MarkMemoryChronicled(ctx, 1); err != ErrClosed {
		t.Errorf("expected ErrClosed on MarkMemoryChronicled, got %v", err)
	}
	if _, err := agentDB.SearchEpisodicMemories(ctx, dummyVec, 5); err != ErrClosed {
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
	if err := agentDB.DeleteMapNode(ctx, "n"); err != ErrClosed {
		t.Errorf("expected ErrClosed on DeleteMapNode, got %v", err)
	}
	if err := agentDB.AddMapEdge(ctx, MapEdge{ID: "e", FromNodeID: "a", ToNodeID: "b"}); err != ErrClosed {
		t.Errorf("expected ErrClosed on AddMapEdge, got %v", err)
	}
	if _, err := agentDB.GetMapEdge(ctx, "e"); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetMapEdge, got %v", err)
	}
	if err := agentDB.DeleteMapEdge(ctx, "e"); err != ErrClosed {
		t.Errorf("expected ErrClosed on DeleteMapEdge, got %v", err)
	}
	if _, _, err := agentDB.GetMapGraph(ctx); err != ErrClosed {
		t.Errorf("expected ErrClosed on GetMapGraph, got %v", err)
	}
}
