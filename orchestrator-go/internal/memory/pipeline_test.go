package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// mockEmbedder implements Embedder for unit and integration testing.
type mockEmbedder struct {
	mu           sync.Mutex
	calls        int32
	embedFunc    func(ctx context.Context, input string) ([]float32, error)
	customVector []float32
}

func (m *mockEmbedder) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	atomic.AddInt32(&m.calls, 1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.embedFunc != nil {
		return m.embedFunc(ctx, input)
	}
	if len(m.customVector) > 0 {
		vec := make([]float32, len(m.customVector))
		copy(vec, m.customVector)
		return vec, nil
	}
	return createUnitVector(db.VectorDimension, 0, 50), nil
}

func (m *mockEmbedder) CallCount() int {
	return int(atomic.LoadInt32(&m.calls))
}

// createUnitVector constructs a normalized 768-dim vector with non-zero elements in [start, end).
func createUnitVector(dim, start, end int) []float32 {
	v := make([]float32, dim)
	for i := start; i < end && i < dim; i++ {
		v[i] = 1.0
	}
	normSq := db.NormSquared(v)
	if normSq > 0 {
		invNorm := float32(1.0 / math.Sqrt(float64(normSq)))
		for i := range v {
			v[i] *= invNorm
		}
	}
	return v
}

func openTestAgentDB(t *testing.T) (*db.AgentDB, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "civquest_memory_pipeline_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	agentDB, err := db.OpenAgentDB(tempDir)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		t.Fatalf("failed to open test agent db: %v", err)
	}
	cleanup := func() {
		_ = agentDB.Close()
		_ = os.RemoveAll(tempDir)
	}
	return agentDB, cleanup
}

func TestPipeline_HighSaliencyIngestionVsLowSaliencySkipping(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder, WithMinSaliency(0.50))

	if p.MinSaliency() != 0.50 {
		t.Errorf("expected MinSaliency=0.50, got %f", p.MinSaliency())
	}

	tests := []struct {
		name         string
		event        MemoryEvent
		expectIngest bool
		expectedSal  float64
	}{
		{
			name: "Permadeath default (1.00) -> High saliency",
			event: MemoryEvent{
				WorldTick:   100,
				Category:    CategoryDeath,
				SummaryText: "Agent perished in lava while mining obsidian.",
			},
			expectIngest: true,
			expectedSal:  1.00,
		},
		{
			name: "Betrayal default (0.95) -> High saliency",
			event: MemoryEvent{
				WorldTick:   200,
				Category:    CategoryBetrayal,
				SummaryText: "Partner clan broke non-aggression pact.",
			},
			expectIngest: true,
			expectedSal:  0.95,
		},
		{
			name: "Discovery default (0.85) -> High saliency",
			event: MemoryEvent{
				WorldTick:      300,
				Category:       CategoryDiscovery,
				LocationCoords: "120,64,300",
				SummaryText:    "Discovered underground ancient city.",
			},
			expectIngest: true,
			expectedSal:  0.85,
		},
		{
			name: "Combat default (0.75) -> High saliency",
			event: MemoryEvent{
				WorldTick:   400,
				Category:    CategoryCombat,
				SummaryText: "Repelled raid of pillagers at town border.",
			},
			expectIngest: true,
			expectedSal:  0.75,
		},
		{
			name: "Trade default (0.65) -> High saliency",
			event: MemoryEvent{
				WorldTick:   500,
				Category:    CategoryTrade,
				Actors:      []string{"player_steve"},
				SummaryText: "Sold 32 wheat for 4 emeralds.",
			},
			expectIngest: true,
			expectedSal:  0.65,
		},
		{
			name: "Chat default (0.60) -> High saliency",
			event: MemoryEvent{
				WorldTick:   600,
				Category:    CategoryChat,
				SummaryText: "Discussed shared granary project with elder.",
			},
			expectIngest: true,
			expectedSal:  0.60,
		},
		{
			name: "Explicit high saliency (Chat @ 0.80) -> High saliency",
			event: MemoryEvent{
				WorldTick:     700,
				Category:      CategoryChat,
				SaliencyScore: 0.80,
				SummaryText:   "Critical diplomatic ultimatum delivered.",
			},
			expectIngest: true,
			expectedSal:  0.80,
		},
		{
			name: "Routine chat (< 0.50) -> Low saliency skipped",
			event: MemoryEvent{
				WorldTick:     800,
				Category:      CategoryChat,
				SaliencyScore: 0.35,
				SummaryText:   "Exchanged generic morning pleasantries.",
			},
			expectIngest: false,
		},
		{
			name: "Routine combat skirmish (< 0.50) -> Low saliency skipped",
			event: MemoryEvent{
				WorldTick:     900,
				Category:      CategoryCombat,
				SaliencyScore: 0.20,
				SummaryText:   "Swatted away a lone spider in well-lit barn.",
			},
			expectIngest: false,
		},
		{
			name: "Custom low saliency trade (< 0.50) -> Low saliency skipped",
			event: MemoryEvent{
				WorldTick:     1000,
				Category:      CategoryTrade,
				SaliencyScore: 0.49,
				SummaryText:   "Traded 1 stick for 1 dirt with wanderer.",
			},
			expectIngest: false,
		},
	}

	ctx := context.Background()
	expectedIngestCount := 0

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem, err := p.Ingest(ctx, tc.event)
			if err != nil {
				t.Fatalf("unexpected error during Ingest: %v", err)
			}

			if tc.expectIngest {
				expectedIngestCount++
				if mem == nil {
					t.Fatalf("expected memory to be ingested, got nil")
				}
				if mem.ID <= 0 {
					t.Errorf("expected positive memory ID, got %d", mem.ID)
				}
				if math.Abs(mem.SaliencyScore-tc.expectedSal) > 1e-4 {
					t.Errorf("expected saliency %f, got %f", tc.expectedSal, mem.SaliencyScore)
				}
				if mem.EventCategory != string(tc.event.Category) {
					t.Errorf("expected category %s, got %s", tc.event.Category, mem.EventCategory)
				}
				if mem.SummaryText != tc.event.SummaryText {
					t.Errorf("expected summary %q, got %q", tc.event.SummaryText, mem.SummaryText)
				}

				// Verify presence in DB
				stored, gErr := agentDB.GetEpisodicMemory(ctx, mem.ID)
				if gErr != nil || stored == nil {
					t.Errorf("failed to retrieve stored memory %d: %v", mem.ID, gErr)
				}
			} else {
				if mem != nil {
					t.Errorf("expected low-saliency event to be skipped (return nil), got %+v", mem)
				}
			}
		})
	}

	// Verify total embedder calls equals high-saliency event count
	if embedder.CallCount() != expectedIngestCount {
		t.Errorf("expected %d embedder calls, got %d", expectedIngestCount, embedder.CallCount())
	}

	// Verify database count of episodic_memories matches expectedIngestCount
	var count int
	if err := agentDB.MemoryDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM episodic_memories;").Scan(&count); err != nil {
		t.Fatalf("querying episodic_memories count: %v", err)
	}
	if count != expectedIngestCount {
		t.Errorf("expected %d records in episodic_memories, got %d", expectedIngestCount, count)
	}

	// Verify database count of episodic_memories_vec matches expectedIngestCount
	var vecCount int
	if err := agentDB.MemoryDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM episodic_memories_vec;").Scan(&vecCount); err != nil {
		t.Fatalf("querying episodic_memories_vec count: %v", err)
	}
	if vecCount != expectedIngestCount {
		t.Errorf("expected %d records in episodic_memories_vec, got %d", expectedIngestCount, vecCount)
	}
}

func TestPipeline_StoreUnvectorizedOption(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder, WithMinSaliency(0.50), WithStoreUnvectorized(true))

	ctx := context.Background()

	// Ingest low-saliency event with WithStoreUnvectorized(true)
	lowSalEvent := MemoryEvent{
		WorldTick:     50,
		Category:      CategoryChat,
		SaliencyScore: 0.30,
		SummaryText:   "Routine greeting stored unvectorized.",
	}

	mem, err := p.Ingest(ctx, lowSalEvent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mem == nil {
		t.Fatalf("expected mem to be returned when storeUnvectorized=true, got nil")
	}

	// Embedder should NOT have been called for low-saliency event
	if embedder.CallCount() != 0 {
		t.Errorf("expected 0 embedder calls for unvectorized storage, got %d", embedder.CallCount())
	}

	// Relational table has 1 record, vector table has 0
	var count int
	_ = agentDB.MemoryDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM episodic_memories;").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 record in episodic_memories, got %d", count)
	}

	var vecCount int
	_ = agentDB.MemoryDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM episodic_memories_vec;").Scan(&vecCount)
	if vecCount != 0 {
		t.Errorf("expected 0 records in episodic_memories_vec, got %d", vecCount)
	}
}

func TestPipeline_KNNSearchOrdering(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	// Create 3 orthogonal 768-dim unit vectors
	vecCombat := createUnitVector(db.VectorDimension, 0, 100)
	vecTrade := createUnitVector(db.VectorDimension, 200, 300)
	vecDiscovery := createUnitVector(db.VectorDimension, 400, 500)

	embedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			lower := strings.ToLower(input)
			switch {
			case strings.Contains(lower, "combat") || strings.Contains(lower, "zombie") || strings.Contains(lower, "fight"):
				return vecCombat, nil
			case strings.Contains(lower, "trade") || strings.Contains(lower, "emerald") || strings.Contains(lower, "buy"):
				return vecTrade, nil
			case strings.Contains(lower, "discovery") || strings.Contains(lower, "ruins") || strings.Contains(lower, "explore"):
				return vecDiscovery, nil
			default:
				// Slightly noisy combat query vector
				noisy := make([]float32, len(vecCombat))
				copy(noisy, vecCombat)
				noisy[0] *= 0.9
				return noisy, nil
			}
		},
	}

	p := NewPipeline(agentDB, embedder)
	ctx := context.Background()

	// Ingest 3 distinct memories
	memCombat, err := p.Ingest(ctx, MemoryEvent{
		WorldTick:   100,
		Category:    CategoryCombat,
		SummaryText: "Combat: Defended village gate against nighttime zombie assault.",
	})
	if err != nil || memCombat == nil {
		t.Fatalf("failed to ingest combat memory: %v", err)
	}

	memTrade, err := p.Ingest(ctx, MemoryEvent{
		WorldTick:   200,
		Category:    CategoryTrade,
		SummaryText: "Trade: Bartered 12 emeralds for enchanted iron chestplate.",
	})
	if err != nil || memTrade == nil {
		t.Fatalf("failed to ingest trade memory: %v", err)
	}

	memDiscovery, err := p.Ingest(ctx, MemoryEvent{
		WorldTick:   300,
		Category:    CategoryDiscovery,
		SummaryText: "Discovery: Uncovered ancient jungle temple ruins.",
	})
	if err != nil || memDiscovery == nil {
		t.Fatalf("failed to ingest discovery memory: %v", err)
	}

	// 1. Query for combat
	matchesCombat, err := p.Retrieve(ctx, "tell me about zombie combat fights", 3)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(matchesCombat) < 3 {
		t.Fatalf("expected 3 matches, got %d", len(matchesCombat))
	}

	// Top match MUST be combat
	if matchesCombat[0].Memory.ID != memCombat.ID {
		t.Errorf("expected top match to be ID %d (combat), got %d (%s)",
			memCombat.ID, matchesCombat[0].Memory.ID, matchesCombat[0].Memory.SummaryText)
	}
	if matchesCombat[0].Similarity < 0.99 {
		t.Errorf("expected similarity near 1.0 for exact combat match, got %f", matchesCombat[0].Similarity)
	}
	// Other orthogonal memories should have near 0 similarity
	if matchesCombat[1].Similarity > 0.05 {
		t.Errorf("expected orthogonal similarity near 0.0, got %f", matchesCombat[1].Similarity)
	}

	// 2. Query for trade
	matchesTrade, err := p.Retrieve(ctx, "how many emeralds did we trade?", 1)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(matchesTrade) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matchesTrade))
	}
	if matchesTrade[0].Memory.ID != memTrade.ID {
		t.Errorf("expected top match to be ID %d (trade), got %d", memTrade.ID, matchesTrade[0].Memory.ID)
	}
	if matchesTrade[0].Similarity < 0.99 {
		t.Errorf("expected similarity near 1.0 for trade match, got %f", matchesTrade[0].Similarity)
	}

	// 3. Query with MinSimilarity filter
	pFiltered := NewPipeline(agentDB, embedder, WithMinSimilarity(0.50))
	filteredMatches, err := pFiltered.Retrieve(ctx, "zombie fight", 10)
	if err != nil {
		t.Fatalf("Retrieve with MinSimilarity failed: %v", err)
	}
	if len(filteredMatches) != 1 {
		t.Errorf("expected exactly 1 match above similarity 0.50, got %d", len(filteredMatches))
	}
}

func TestPipeline_FormatPromptContext(t *testing.T) {
	// Empty matches returns empty string
	if out := FormatPromptContext(nil); out != "" {
		t.Errorf("expected empty string for nil matches, got %q", out)
	}
	if out := FormatPromptContext([]db.EpisodicMemoryMatch{}); out != "" {
		t.Errorf("expected empty string for empty matches, got %q", out)
	}

	matches := []db.EpisodicMemoryMatch{
		{
			Memory: db.EpisodicMemory{
				ID:             1,
				WorldTick:      1200,
				EventCategory:  "COMBAT",
				SaliencyScore:  0.85,
				LocationCoords: "100,64,200",
				SummaryText:    "Defended village gate against nighttime zombie assault.",
			},
			Similarity: 0.95,
		},
		{
			Memory: db.EpisodicMemory{
				ID:             2,
				WorldTick:      1400,
				EventCategory:  "TRADE",
				SaliencyScore:  0.65,
				LocationCoords: "",
				SummaryText:    "Bartered 12 emeralds for enchanted iron armor.",
			},
			Similarity: 0.88,
		},
	}

	prompt := FormatPromptContext(matches)
	if !strings.Contains(prompt, "Relevant Memories:") {
		t.Errorf("expected header 'Relevant Memories:', got %q", prompt)
	}
	if !strings.Contains(prompt, "[COMBAT | Tick: 1200 | Loc: 100,64,200]: Defended village gate") {
		t.Errorf("expected combat line with location, got %q", prompt)
	}
	if !strings.Contains(prompt, "[TRADE | Tick: 1400]: Bartered 12 emeralds") {
		t.Errorf("expected trade line without loc, got %q", prompt)
	}

	// Verify instance method wrapper produces identical result
	p := NewPipeline(nil, nil)
	if p.FormatPromptContext(matches) != prompt {
		t.Errorf("instance method FormatPromptContext result mismatched package function")
	}
}

func TestPipeline_ContextCancellationAndErrorHandling(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder)

	// 1. Context already cancelled on Ingest
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Ingest(canceledCtx, MemoryEvent{
		Category:    CategoryCombat,
		SummaryText: "Test canceled",
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Ingest, got %v", err)
	}

	// 2. Context already cancelled on Retrieve
	_, err = p.Retrieve(canceledCtx, "Test query", 5)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Retrieve, got %v", err)
	}

	ctx := context.Background()

	// 3. Invalid Category
	_, err = p.Ingest(ctx, MemoryEvent{
		Category:    Category("INVALID_CATEGORY"),
		SummaryText: "Invalid category test",
	})
	if !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("expected ErrInvalidCategory, got %v", err)
	}

	// 4. Empty SummaryText
	_, err = p.Ingest(ctx, MemoryEvent{
		Category:    CategoryCombat,
		SummaryText: "   ",
	})
	if !errors.Is(err, ErrEmptySummaryText) {
		t.Errorf("expected ErrEmptySummaryText, got %v", err)
	}

	// 5. Invalid SaliencyScore
	_, err = p.Ingest(ctx, MemoryEvent{
		Category:      CategoryCombat,
		SaliencyScore: -0.5,
		SummaryText:   "Negative saliency",
	})
	if !errors.Is(err, ErrInvalidSaliencyScore) {
		t.Errorf("expected ErrInvalidSaliencyScore for negative score, got %v", err)
	}

	_, err = p.Ingest(ctx, MemoryEvent{
		Category:      CategoryCombat,
		SaliencyScore: 1.5,
		SummaryText:   "Over-unity saliency",
	})
	if !errors.Is(err, ErrInvalidSaliencyScore) {
		t.Errorf("expected ErrInvalidSaliencyScore for >1 score, got %v", err)
	}

	// 6. Empty Query on Retrieve
	_, err = p.Retrieve(ctx, "   ", 5)
	if !errors.Is(err, ErrEmptyQuery) {
		t.Errorf("expected ErrEmptyQuery, got %v", err)
	}

	// 7. Embedder error during Ingest
	failingEmbedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			return nil, errors.New("simulated gateway connection failure")
		},
	}
	pFailing := NewPipeline(agentDB, failingEmbedder)
	_, err = pFailing.Ingest(ctx, MemoryEvent{
		Category:    CategoryCombat,
		SummaryText: "Failing embedder test",
	})
	if err == nil || !strings.Contains(err.Error(), "generating memory embedding") {
		t.Errorf("expected memory embedding failure, got %v", err)
	}

	// 8. Embedder returns invalid dimensions (< 768)
	dimEmbedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			return []float32{0.1, 0.2}, nil // invalid dimensions
		},
	}
	pDim := NewPipeline(agentDB, dimEmbedder)
	_, err = pDim.Ingest(ctx, MemoryEvent{
		Category:    CategoryCombat,
		SummaryText: "Invalid dimension test",
	})
	if err == nil || !errors.Is(err, db.ErrInvalidVectorDimension) {
		t.Errorf("expected ErrInvalidVectorDimension, got %v", err)
	}

	// 9. Nil database and nil embedder guards
	pNilDB := NewPipeline(nil, embedder)
	if _, err := pNilDB.Ingest(ctx, MemoryEvent{Category: CategoryCombat, SummaryText: "x"}); !errors.Is(err, ErrNilDatabase) {
		t.Errorf("expected ErrNilDatabase on Ingest, got %v", err)
	}
	if _, err := pNilDB.Retrieve(ctx, "query", 5); !errors.Is(err, ErrNilDatabase) {
		t.Errorf("expected ErrNilDatabase on Retrieve, got %v", err)
	}

	pNilEmbedder := NewPipeline(agentDB, nil)
	if _, err := pNilEmbedder.Ingest(ctx, MemoryEvent{Category: CategoryCombat, SummaryText: "x"}); !errors.Is(err, ErrNilEmbedder) {
		t.Errorf("expected ErrNilEmbedder on Ingest, got %v", err)
	}
	if _, err := pNilEmbedder.Retrieve(ctx, "query", 5); !errors.Is(err, ErrNilEmbedder) {
		t.Errorf("expected ErrNilEmbedder on Retrieve, got %v", err)
	}
}

func TestPipeline_ConcurrentIngestAndRetrieve(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			return createUnitVector(db.VectorDimension, 0, 50), nil
		},
	}
	p := NewPipeline(agentDB, embedder)

	const workers = 8
	const iterations = 15

	var wg sync.WaitGroup
	wg.Add(workers * 2)

	// Ingestion workers
	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := p.Ingest(ctx, MemoryEvent{
					WorldTick:   int64(workerID*1000 + i),
					Category:    CategoryCombat,
					SummaryText: fmt.Sprintf("Worker %d battle event %d", workerID, i),
				})
				cancel()
				if err != nil {
					t.Errorf("concurrent Ingest error: %v", err)
					return
				}
			}
		}()
	}

	// Retrieval workers
	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := p.Retrieve(ctx, fmt.Sprintf("battle query from worker %d", workerID), 5)
				cancel()
				if err != nil {
					t.Errorf("concurrent Retrieve error: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestPipeline_RetrievalLatency_Under20ms(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	ctx := context.Background()

	// Seed database with 50 episodic memories and 768-dim embeddings
	const memoryCount = 50
	t.Logf("Seeding AgentDB with %d 768-dim memories...", memoryCount)

	for i := 0; i < memoryCount; i++ {
		startDim := (i * 7) % (db.VectorDimension - 30)
		vec := createUnitVector(db.VectorDimension, startDim, startDim+20)
		mem := db.EpisodicMemory{
			WorldTick:      int64(1000 + i),
			EventCategory:  "COMBAT",
			SaliencyScore:  0.80,
			ActorsJSON:     `["enemy_mob"]`,
			LocationCoords: fmt.Sprintf("%d,64,%d", i, i),
			SummaryText:    fmt.Sprintf("Episodic memory event #%d recorded in chronicler ledger.", i),
		}
		if _, err := agentDB.InsertEpisodicMemory(ctx, mem, vec); err != nil {
			t.Fatalf("failed to insert memory %d: %v", i, err)
		}
	}

	embedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			return createUnitVector(db.VectorDimension, 0, 20), nil
		},
	}
	p := NewPipeline(agentDB, embedder)

	// Warm-up query
	if _, err := p.Retrieve(ctx, "warmup query", 10); err != nil {
		t.Fatalf("warmup Retrieve failed: %v", err)
	}

	// Benchmark multiple query iterations and ensure each takes < 20ms
	const testRuns = 20
	var totalDuration time.Duration

	for r := 0; r < testRuns; r++ {
		query := fmt.Sprintf("query iteration %d for battle memories", r)
		start := time.Now()
		matches, err := p.Retrieve(ctx, query, 10)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Retrieve failed on run %d: %v", r, err)
		}
		if len(matches) != 10 {
			t.Errorf("expected 10 matches, got %d", len(matches))
		}

		if elapsed > 50*time.Millisecond {
			t.Logf("run %d had high latency spike: %v", r, elapsed)
		}
		totalDuration += elapsed
	}

	avgDuration := totalDuration / testRuns
	t.Logf("KNN retrieval average latency over %d runs against %d memories: %v (Requirement: <20ms)",
		testRuns, memoryCount, avgDuration)

	if avgDuration > 20*time.Millisecond {
		t.Fatalf("average retrieval duration %v exceeded strict <20ms requirement", avgDuration)
	}
}

func BenchmarkRetrieve(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "civquest_benchmark_*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	agentDB, err := db.OpenAgentDB(filepath.Join(tempDir, "agent_bench"))
	if err != nil {
		b.Fatalf("failed to open agent db: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	const seedCount = 200

	for i := 0; i < seedCount; i++ {
		start := (i * 3) % (db.VectorDimension - 30)
		vec := createUnitVector(db.VectorDimension, start, start+20)
		mem := db.EpisodicMemory{
			WorldTick:      int64(i),
			EventCategory:  "COMBAT",
			SaliencyScore:  0.8,
			ActorsJSON:     "[]",
			LocationCoords: "0,0,0",
			SummaryText:    fmt.Sprintf("Benchmark memory record %d", i),
		}
		if _, err := agentDB.InsertEpisodicMemory(ctx, mem, vec); err != nil {
			b.Fatalf("failed to seed memory: %v", err)
		}
	}

	embedder := &mockEmbedder{
		embedFunc: func(ctx context.Context, input string) ([]float32, error) {
			return createUnitVector(db.VectorDimension, 0, 20), nil
		},
	}
	p := NewPipeline(agentDB, embedder)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Retrieve(ctx, "benchmark query string", 10)
		if err != nil {
			b.Fatalf("Retrieve failed: %v", err)
		}
	}
}

func TestPipeline_NaNAndInfSaliencyValidation(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder)

	invalidSaliencies := []struct {
		name  string
		score float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"Negative", -0.01},
		{"AboveOne", 1.01},
	}

	for _, tc := range invalidSaliencies {
		t.Run(tc.name, func(t *testing.T) {
			ev := MemoryEvent{
				WorldTick:     1,
				Category:      CategoryCombat,
				SaliencyScore: tc.score,
				SummaryText:   "Combat skirmish",
			}

			_, err := p.Ingest(context.Background(), ev)
			if !errors.Is(err, ErrInvalidSaliencyScore) {
				t.Fatalf("expected ErrInvalidSaliencyScore for %s, got: %v", tc.name, err)
			}
		})
	}

	if embedder.CallCount() != 0 {
		t.Fatalf("embedder was invoked %d times during invalid saliency tests; expected 0", embedder.CallCount())
	}

	matches, err := p.Retrieve(context.Background(), "Combat", 10)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected 0 memories in database, found %d", len(matches))
	}
}

func TestPipeline_ExplicitZeroSaliencyIngestion(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	pDefault := NewPipeline(agentDB, embedder, WithMinSaliency(0.50))

	// 1. In Go struct with ExplicitSaliency: true and SaliencyScore: 0.0
	evExplicitZero := MemoryEvent{
		WorldTick:        10,
		Category:         CategoryCombat,
		SaliencyScore:    0.0,
		ExplicitSaliency: true,
		SummaryText:      "Trivial touch combat",
	}

	if eff := evExplicitZero.EffectiveSaliency(); eff != 0.0 {
		t.Fatalf("expected EffectiveSaliency() to be 0.0, got %f", eff)
	}

	// Should be skipped because 0.0 < 0.50 and storeUnvectorized=false
	res, err := pDefault.Ingest(context.Background(), evExplicitZero)
	if err != nil {
		t.Fatalf("unexpected error ingesting explicit zero saliency: %v", err)
	}
	if res != nil {
		t.Fatalf("expected event to be skipped (nil result), got: %+v", res)
	}

	// 2. With StoreUnvectorized: true, should insert into DB with score 0.0
	pUnvec := NewPipeline(agentDB, embedder, WithMinSaliency(0.50), WithStoreUnvectorized(true))
	resUnvec, err := pUnvec.Ingest(context.Background(), evExplicitZero)
	if err != nil {
		t.Fatalf("unexpected error ingesting unvectorized explicit zero: %v", err)
	}
	if resUnvec == nil {
		t.Fatal("expected unvectorized memory to be inserted, got nil")
	}
	if resUnvec.SaliencyScore != 0.0 {
		t.Fatalf("expected stored SaliencyScore to be 0.0, got %f", resUnvec.SaliencyScore)
	}

	// 3. Verify JSON unmarshaling sets ExplicitSaliency
	rawJSON := []byte(`{"world_tick":20,"category":"COMBAT","saliency_score":0.0,"summary_text":"Explicit zero via json"}`)
	var evJSON MemoryEvent
	if err := json.Unmarshal(rawJSON, &evJSON); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if !evJSON.ExplicitSaliency {
		t.Fatal("expected ExplicitSaliency to be true after unmarshaling explicit saliency_score: 0.0")
	}
	if evJSON.SaliencyScore != 0.0 {
		t.Fatalf("expected SaliencyScore 0.0, got %f", evJSON.SaliencyScore)
	}
	if eff := evJSON.EffectiveSaliency(); eff != 0.0 {
		t.Fatalf("expected EffectiveSaliency() 0.0 from unmarshaled event, got %f", eff)
	}

	// 4. Default uninitialized saliency (0.0 with ExplicitSaliency: false) should fall back to Category default
	evDefault := MemoryEvent{
		Category:    CategoryCombat,
		SummaryText: "Default combat",
	}
	if evDefault.ExplicitSaliency {
		t.Fatal("expected ExplicitSaliency to be false by default")
	}
	if eff := evDefault.EffectiveSaliency(); eff != SaliencyCombat {
		t.Fatalf("expected default saliency %f for COMBAT, got %f", SaliencyCombat, eff)
	}
}

func TestPipeline_ActorsJSONValidation(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder)

	invalidJSONs := []struct {
		name string
		raw  string
	}{
		{"InvalidSyntax", "not-json-content"},
		{"JSONObject", `{"actor": "Steve"}`},
		{"JSONArrayOfNumbers", `[1, 2, 3]`},
		{"JSONNull", "null"},
		{"JSONNumber", "42"},
		{"MixedTypes", `["Steve", 100]`},
	}

	for _, tc := range invalidJSONs {
		t.Run(tc.name, func(t *testing.T) {
			ev := MemoryEvent{
				WorldTick:   10,
				Category:    CategoryCombat,
				SummaryText: "Combat skirmish",
				ActorsJSON:  tc.raw,
			}
			_, err := p.Ingest(context.Background(), ev)
			if !errors.Is(err, ErrInvalidActorsJSON) {
				t.Fatalf("expected ErrInvalidActorsJSON for %s (%q), got: %v", tc.name, tc.raw, err)
			}
		})
	}

	// Verify unvectorized path also validates actors JSON
	pUnvec := NewPipeline(agentDB, embedder, WithMinSaliency(0.90), WithStoreUnvectorized(true))
	lowSalEv := MemoryEvent{
		WorldTick:     15,
		Category:      CategoryChat, // default 0.60 < 0.90
		SummaryText:   "Chat event",
		ActorsJSON:    "not-json",
		SaliencyScore: 0.1,
	}
	_, err := pUnvec.Ingest(context.Background(), lowSalEv)
	if !errors.Is(err, ErrInvalidActorsJSON) {
		t.Fatalf("expected ErrInvalidActorsJSON on unvectorized path, got: %v", err)
	}

	// Valid cases
	validCases := []struct {
		name     string
		ev       MemoryEvent
		expected string
	}{
		{
			name: "ValidArrayString",
			ev: MemoryEvent{
				Category:    CategoryCombat,
				SummaryText: "Combat",
				ActorsJSON:  `["Steve", "Alex"]`,
			},
			expected: `["Steve", "Alex"]`,
		},
		{
			name: "ActorsSlice",
			ev: MemoryEvent{
				Category:    CategoryCombat,
				SummaryText: "Combat",
				Actors:      []string{"Steve", "Villager"},
			},
			expected: `["Steve","Villager"]`,
		},
		{
			name: "EmptyActors",
			ev: MemoryEvent{
				Category:    CategoryCombat,
				SummaryText: "Combat",
			},
			expected: `[]`,
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			mem, err := p.Ingest(context.Background(), tc.ev)
			if err != nil {
				t.Fatalf("expected success for %s, got err: %v", tc.name, err)
			}
			if mem.ActorsJSON != tc.expected {
				t.Fatalf("expected actors_json %q, got %q", tc.expected, mem.ActorsJSON)
			}
		})
	}
}

func TestPipeline_RetrieveLimitBounding(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}
	p := NewPipeline(agentDB, embedder)
	ctx := context.Background()

	// Seed 110 memories
	totalMemories := 110
	for i := 0; i < totalMemories; i++ {
		ev := MemoryEvent{
			WorldTick:   int64(i),
			Category:    CategoryCombat,
			SummaryText: fmt.Sprintf("Combat encounter number %d", i),
		}
		if _, err := p.Ingest(ctx, ev); err != nil {
			t.Fatalf("seeding memory %d failed: %v", i, err)
		}
	}

	// Test limit <= 0 defaults to DefaultRetrieveLimit (10)
	matchesZero, err := p.Retrieve(ctx, "Combat", 0)
	if err != nil {
		t.Fatalf("Retrieve with limit 0 failed: %v", err)
	}
	if len(matchesZero) != DefaultRetrieveLimit {
		t.Fatalf("expected default %d matches for limit 0, got %d", DefaultRetrieveLimit, len(matchesZero))
	}

	matchesNeg, err := p.Retrieve(ctx, "Combat", -10)
	if err != nil {
		t.Fatalf("Retrieve with negative limit failed: %v", err)
	}
	if len(matchesNeg) != DefaultRetrieveLimit {
		t.Fatalf("expected default %d matches for negative limit, got %d", DefaultRetrieveLimit, len(matchesNeg))
	}

	// Test limit > MaxRetrieveLimit (100) is capped to MaxRetrieveLimit
	matchesCapped, err := p.Retrieve(ctx, "Combat", 250)
	if err != nil {
		t.Fatalf("Retrieve with large limit failed: %v", err)
	}
	if len(matchesCapped) != MaxRetrieveLimit {
		t.Fatalf("expected capped %d matches for limit 250, got %d", MaxRetrieveLimit, len(matchesCapped))
	}
}

func TestPipeline_FormatPromptContext_NewlineSanitization(t *testing.T) {
	matches := []db.EpisodicMemoryMatch{
		{
			Memory: db.EpisodicMemory{
				WorldTick:      100,
				EventCategory:  "COMBAT",
				LocationCoords: "10,64,-20",
				SummaryText:    "First line\r\nSecond line\nThird line\rFourth line",
			},
			Similarity: 0.95,
		},
		{
			Memory: db.EpisodicMemory{
				WorldTick:      105,
				EventCategory:  "CHAT",
				LocationCoords: "0,0,0",
				SummaryText:    "Dialogue line 1\nDialogue line 2",
			},
			Similarity: 0.88,
		},
	}

	formatted := FormatPromptContext(matches)

	expectedFlattened1 := "First line Second line Third line Fourth line"
	expectedFlattened2 := "Dialogue line 1 Dialogue line 2"

	if !strings.Contains(formatted, expectedFlattened1) {
		t.Fatalf("expected prompt context to contain flattened summary %q, got:\n%s", expectedFlattened1, formatted)
	}
	if !strings.Contains(formatted, expectedFlattened2) {
		t.Fatalf("expected prompt context to contain flattened summary %q, got:\n%s", expectedFlattened2, formatted)
	}

	// Split by newline and verify total bullet points
	lines := strings.Split(formatted, "\n")
	// Expected lines: "Relevant Memories:", bullet 1, bullet 2 => exactly 3 lines
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 lines in formatted context, got %d lines:\n%s", len(lines), formatted)
	}
}

func TestPipeline_ThreadSafeSettersAndOptionValidation(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()

	embedder := &mockEmbedder{}

	// 1. Option validation for WithMinSimilarity
	pNaN := NewPipeline(agentDB, embedder, WithMinSimilarity(float32(math.NaN())))
	if pNaN.MinSimilarity() != -1.0 {
		t.Fatalf("expected default MinSimilarity -1.0 after NaN option, got %f", pNaN.MinSimilarity())
	}

	pInf := NewPipeline(agentDB, embedder, WithMinSimilarity(float32(math.Inf(1))))
	if pInf.MinSimilarity() != -1.0 {
		t.Fatalf("expected default MinSimilarity -1.0 after Inf option, got %f", pInf.MinSimilarity())
	}

	pHigh := NewPipeline(agentDB, embedder, WithMinSimilarity(1.5))
	if pHigh.MinSimilarity() != -1.0 {
		t.Fatalf("expected default MinSimilarity -1.0 after >1.0 option, got %f", pHigh.MinSimilarity())
	}

	pLow := NewPipeline(agentDB, embedder, WithMinSimilarity(-2.0))
	if pLow.MinSimilarity() != -1.0 {
		t.Fatalf("expected default MinSimilarity -1.0 after <-1.0 option, got %f", pLow.MinSimilarity())
	}

	pValid := NewPipeline(agentDB, embedder, WithMinSimilarity(0.75))
	if pValid.MinSimilarity() != 0.75 {
		t.Fatalf("expected MinSimilarity 0.75, got %f", pValid.MinSimilarity())
	}

	// 2. Setters validation
	p := NewPipeline(agentDB, embedder)

	p.SetMinSimilarity(0.80)
	if p.MinSimilarity() != 0.80 {
		t.Fatalf("expected MinSimilarity 0.80, got %f", p.MinSimilarity())
	}
	p.SetMinSimilarity(float32(math.NaN()))
	if p.MinSimilarity() != 0.80 {
		t.Fatalf("expected MinSimilarity 0.80 unchanged after NaN setter, got %f", p.MinSimilarity())
	}

	p.SetMinSaliency(0.70)
	if p.MinSaliency() != 0.70 {
		t.Fatalf("expected MinSaliency 0.70, got %f", p.MinSaliency())
	}
	p.SetMinSaliency(math.NaN())
	if p.MinSaliency() != 0.70 {
		t.Fatalf("expected MinSaliency 0.70 unchanged after NaN setter, got %f", p.MinSaliency())
	}

	p.SetStoreUnvectorized(true)
	if !p.StoreUnvectorized() {
		t.Fatal("expected StoreUnvectorized to be true")
	}

	// 3. Concurrent read/write stress test
	var wg sync.WaitGroup
	ctx := context.Background()

	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sal := 0.1 + float64(j%8)*0.1
				sim := float32(-1.0 + float32(j%10)*0.2)
				p.SetMinSaliency(sal)
				p.SetMinSimilarity(sim)
				p.SetStoreUnvectorized(j%2 == 0)
			}
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = p.MinSaliency()
				_ = p.MinSimilarity()
				_ = p.StoreUnvectorized()
			}
		}(i)
	}

	// Ingesters and Retrievers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ev := MemoryEvent{
					WorldTick:   int64(j),
					Category:    CategoryCombat,
					SummaryText: fmt.Sprintf("Concurrent event %d-%d", workerID, j),
				}
				_, _ = p.Ingest(ctx, ev)
				_, _ = p.Retrieve(ctx, "Concurrent", 5)
			}
		}(i)
	}

	wg.Wait()
}
