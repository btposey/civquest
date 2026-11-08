package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

var newlineSanitizer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// Option defines a functional configuration option for Pipeline.
type Option func(*Pipeline)

// WithMinSaliency configures the minimum saliency score required for vectorization and storage.
// Events with saliency below this threshold are skipped (or stored unvectorized if configured).
func WithMinSaliency(minSaliency float64) Option {
	return func(p *Pipeline) {
		if !math.IsNaN(minSaliency) && !math.IsInf(minSaliency, 0) && minSaliency >= 0.0 && minSaliency <= 1.0 {
			p.minSaliency = minSaliency
		}
	}
}

// WithMinSimilarity configures a minimum cosine similarity threshold for retrieved results.
func WithMinSimilarity(minSimilarity float32) Option {
	return func(p *Pipeline) {
		f := float64(minSimilarity)
		if !math.IsNaN(f) && !math.IsInf(f, 0) && minSimilarity >= -1.0 && minSimilarity <= 1.0 {
			p.minSimilarity = minSimilarity
		}
	}
}

// WithStoreUnvectorized configures whether low-saliency events (< minSaliency) should still be
// inserted into the relational episodic_memories table without vector embeddings, or skipped entirely.
// Default is false (skipped entirely to keep memory clean).
func WithStoreUnvectorized(store bool) Option {
	return func(p *Pipeline) {
		p.storeUnvectorized = store
	}
}

// WithEmbedTextFunc customizes the text extracted from MemoryEvent to produce its vector embedding.
func WithEmbedTextFunc(fn func(MemoryEvent) string) Option {
	return func(p *Pipeline) {
		if fn != nil {
			p.embedTextFunc = fn
		}
	}
}

// Pipeline coordinates episodic memory ingestion, embedding generation,
// vector similarity retrieval, and prompt context formatting.
type Pipeline struct {
	mu                sync.RWMutex
	agentDB           *db.AgentDB
	embedder          Embedder
	minSaliency       float64
	minSimilarity     float32
	storeUnvectorized bool
	embedTextFunc     func(MemoryEvent) string
}

// NewPipeline creates an episodic vector RAG memory pipeline.
func NewPipeline(agentDB *db.AgentDB, embedder Embedder, opts ...Option) *Pipeline {
	p := &Pipeline{
		agentDB:           agentDB,
		embedder:          embedder,
		minSaliency:       DefaultMinSaliency,
		minSimilarity:     -1.0, // Return all top-k matches regardless of similarity
		storeUnvectorized: false,
		embedTextFunc: func(ev MemoryEvent) string {
			return ev.SummaryText
		},
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// MinSaliency returns the configured minimum saliency threshold.
func (p *Pipeline) MinSaliency() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.minSaliency
}

// MinSimilarity returns the configured minimum similarity threshold.
func (p *Pipeline) MinSimilarity() float32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.minSimilarity
}

// StoreUnvectorized returns whether unvectorized low-saliency events are stored.
func (p *Pipeline) StoreUnvectorized() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.storeUnvectorized
}

// SetMinSaliency dynamically updates the minimum saliency threshold with thread-safety.
func (p *Pipeline) SetMinSaliency(minSaliency float64) {
	if math.IsNaN(minSaliency) || math.IsInf(minSaliency, 0) || minSaliency < 0.0 || minSaliency > 1.0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.minSaliency = minSaliency
}

// SetMinSimilarity dynamically updates the minimum similarity threshold with thread-safety.
func (p *Pipeline) SetMinSimilarity(minSimilarity float32) {
	f := float64(minSimilarity)
	if math.IsNaN(f) || math.IsInf(f, 0) || minSimilarity < -1.0 || minSimilarity > 1.0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.minSimilarity = minSimilarity
}

// SetStoreUnvectorized dynamically updates the unvectorized storage setting with thread-safety.
func (p *Pipeline) SetStoreUnvectorized(store bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.storeUnvectorized = store
}

// AgentDB returns the underlying AgentDB.
func (p *Pipeline) AgentDB() *db.AgentDB {
	return p.agentDB
}

// Embedder returns the underlying Embedder.
func (p *Pipeline) Embedder() Embedder {
	return p.embedder
}

// resolveActorsJSON validates and normalizes the actors JSON string representation.
// It verifies that explicit actors_json is valid JSON and unmarshals into a slice of strings.
func resolveActorsJSON(ev MemoryEvent) (string, error) {
	if ev.ActorsJSON != "" {
		raw := strings.TrimSpace(ev.ActorsJSON)
		if !json.Valid([]byte(raw)) {
			return "", ErrInvalidActorsJSON
		}
		if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
			return "", ErrInvalidActorsJSON
		}
		var actors []string
		if err := json.Unmarshal([]byte(raw), &actors); err != nil {
			return "", ErrInvalidActorsJSON
		}
		return raw, nil
	}
	if len(ev.Actors) > 0 {
		b, err := json.Marshal(ev.Actors)
		if err != nil {
			return "", fmt.Errorf("marshaling actors: %w", err)
		}
		return string(b), nil
	}
	return "[]", nil
}

// Ingest processes a memory event: validates category and saliency, filters out low-saliency events (< minSaliency),
// generates a 768-dim vector embedding via the configured Embedder for high-saliency events,
// and atomically stores the episodic memory and its vector in AgentDB.
//
// Returns (nil, nil) if the event was skipped due to low saliency.
func (p *Pipeline) Ingest(ctx context.Context, ev MemoryEvent) (*db.EpisodicMemory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p.agentDB == nil {
		return nil, ErrNilDatabase
	}

	if !ev.Category.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidCategory, ev.Category)
	}

	trimmedSummary := strings.TrimSpace(ev.SummaryText)
	if trimmedSummary == "" {
		return nil, ErrEmptySummaryText
	}

	saliency := ev.EffectiveSaliency()
	if math.IsNaN(saliency) || math.IsInf(saliency, 0) || saliency < 0.0 || saliency > 1.0 {
		return nil, fmt.Errorf("%w: got %f", ErrInvalidSaliencyScore, saliency)
	}

	actorsJSON, err := resolveActorsJSON(ev)
	if err != nil {
		return nil, err
	}

	p.mu.RLock()
	minSal := p.minSaliency
	storeUnvec := p.storeUnvectorized
	embedFn := p.embedTextFunc
	p.mu.RUnlock()

	// Filter low-saliency events
	if saliency < minSal {
		if !storeUnvec {
			// Skipped completely to keep memory and vector index clean
			return nil, nil
		}
		// Low-saliency relational-only storage (no embedding generation)
		return p.insertRelationalOnly(ctx, ev, saliency, trimmedSummary)
	}

	// High-saliency event: vectorize and insert
	if p.embedder == nil {
		return nil, ErrNilEmbedder
	}

	embedInput := embedFn(ev)
	if strings.TrimSpace(embedInput) == "" {
		embedInput = trimmedSummary
	}

	embedding, err := p.embedder.CreateEmbedding(ctx, embedInput)
	if err != nil {
		return nil, fmt.Errorf("generating memory embedding: %w", err)
	}

	if err := db.ValidateVector(embedding); err != nil {
		return nil, fmt.Errorf("validating memory embedding: %w", err)
	}

	mem := db.EpisodicMemory{
		WorldTick:      ev.WorldTick,
		EventCategory:  string(ev.Category),
		SaliencyScore:  saliency,
		ActorsJSON:     actorsJSON,
		LocationCoords: ev.LocationCoords,
		SummaryText:    trimmedSummary,
		Chronicled:     ev.Chronicled,
	}

	id, err := p.agentDB.InsertEpisodicMemory(ctx, mem, embedding)
	if err != nil {
		return nil, fmt.Errorf("inserting episodic memory into db: %w", err)
	}
	mem.ID = id

	return &mem, nil
}

func (p *Pipeline) insertRelationalOnly(ctx context.Context, ev MemoryEvent, saliency float64, summary string) (*db.EpisodicMemory, error) {
	actorsJSON, err := resolveActorsJSON(ev)
	if err != nil {
		return nil, err
	}

	mem := db.EpisodicMemory{
		WorldTick:      ev.WorldTick,
		EventCategory:  string(ev.Category),
		SaliencyScore:  saliency,
		ActorsJSON:     actorsJSON,
		LocationCoords: ev.LocationCoords,
		SummaryText:    summary,
		Chronicled:     ev.Chronicled,
	}

	id, err := p.agentDB.InsertEpisodicMemory(ctx, mem, nil)
	if err != nil {
		return nil, fmt.Errorf("inserting unvectorized episodic memory: %w", err)
	}
	mem.ID = id
	return &mem, nil
}

// Retrieve embeds the query string and performs a KNN search on episodic_memories_vec in AgentDB,
// returning top-k matching memories sorted by cosine similarity descending.
func (p *Pipeline) Retrieve(ctx context.Context, query string, limit int) ([]db.EpisodicMemoryMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p.agentDB == nil {
		return nil, ErrNilDatabase
	}
	if p.embedder == nil {
		return nil, ErrNilEmbedder
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, ErrEmptyQuery
	}

	if limit <= 0 {
		limit = DefaultRetrieveLimit
	} else if limit > MaxRetrieveLimit {
		limit = MaxRetrieveLimit
	}

	queryEmbedding, err := p.embedder.CreateEmbedding(ctx, trimmedQuery)
	if err != nil {
		return nil, fmt.Errorf("generating query embedding: %w", err)
	}

	matches, err := p.agentDB.SearchEpisodicMemories(ctx, queryEmbedding, limit)
	if err != nil {
		return nil, fmt.Errorf("searching episodic memories: %w", err)
	}

	p.mu.RLock()
	minSim := p.minSimilarity
	p.mu.RUnlock()

	if minSim > -1.0 {
		filtered := make([]db.EpisodicMemoryMatch, 0, len(matches))
		for _, m := range matches {
			if m.Similarity >= minSim {
				filtered = append(filtered, m)
			}
		}
		return filtered, nil
	}

	return matches, nil
}

// FormatPromptContext formats a slice of EpisodicMemoryMatch records into a clean,
// structured context block suitable for inclusion in System 2 LLM prompts.
// Embedded newlines (\r\n, \r, \n) are replaced by single spaces.
// Returns an empty string if matches is empty.
func FormatPromptContext(matches []db.EpisodicMemoryMatch) string {
	if len(matches) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Relevant Memories:\n")
	for _, m := range matches {
		mem := m.Memory
		var locPart string
		if mem.LocationCoords != "" && mem.LocationCoords != "0,0,0" {
			locPart = fmt.Sprintf(" | Loc: %s", mem.LocationCoords)
		}
		cleanSummary := newlineSanitizer.Replace(mem.SummaryText)
		sb.WriteString(fmt.Sprintf("- [%s | Tick: %d%s]: %s\n", mem.EventCategory, mem.WorldTick, locPart, cleanSummary))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// FormatPromptContext is an instance method convenience wrapper.
func (p *Pipeline) FormatPromptContext(matches []db.EpisodicMemoryMatch) string {
	return FormatPromptContext(matches)
}
