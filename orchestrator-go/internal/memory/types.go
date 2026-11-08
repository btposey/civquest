package memory

import (
	"context"
	"errors"
)

// Category defines the classification of an episodic memory event.
// Corresponds directly to the CHECK constraint in episodic_memories schema:
// CHECK(event_category IN ('COMBAT', 'TRADE', 'CHAT', 'DEATH', 'DISCOVERY', 'BETRAYAL'))
type Category string

const (
	CategoryCombat    Category = "COMBAT"
	CategoryTrade     Category = "TRADE"
	CategoryChat      Category = "CHAT"
	CategoryDeath     Category = "DEATH"
	CategoryDiscovery Category = "DISCOVERY"
	CategoryBetrayal  Category = "BETRAYAL"
)

// Valid checks if the category is one of the recognized database categories.
func (c Category) Valid() bool {
	switch c {
	case CategoryCombat, CategoryTrade, CategoryChat, CategoryDeath, CategoryDiscovery, CategoryBetrayal:
		return true
	default:
		return false
	}
}

// String returns the string representation of Category.
func (c Category) String() string {
	return string(c)
}

// Default saliency score rules per architecture specifications:
// - Permadeath: 1.00
// - Betrayal: 0.95
// - Discovery: 0.85
// - Combat: 0.75
// - Trade: 0.65
// - Chat: 0.60
// - Routine: < 0.50 (default 0.30)
const (
	SaliencyPermadeath = 1.00
	SaliencyBetrayal   = 0.95
	SaliencyDiscovery  = 0.85
	SaliencyCombat     = 0.75
	SaliencyTrade      = 0.65
	SaliencyChat       = 0.60
	SaliencyRoutine    = 0.30

	DefaultMinSaliency   = 0.50
	DefaultRetrieveLimit = 10
)

// DefaultSaliency returns the canonical default saliency score for a memory category.
func DefaultSaliency(cat Category) float64 {
	switch cat {
	case CategoryDeath:
		return SaliencyPermadeath
	case CategoryBetrayal:
		return SaliencyBetrayal
	case CategoryDiscovery:
		return SaliencyDiscovery
	case CategoryCombat:
		return SaliencyCombat
	case CategoryTrade:
		return SaliencyTrade
	case CategoryChat:
		return SaliencyChat
	default:
		return SaliencyRoutine
	}
}

// MemoryEvent encapsulates raw experiential telemetry, observation, or narrative
// event to be processed and ingested by the episodic memory pipeline.
type MemoryEvent struct {
	WorldTick      int64    `json:"world_tick"`
	Category       Category `json:"category"`
	SaliencyScore  float64  `json:"saliency_score,omitempty"`
	Actors         []string `json:"actors,omitempty"`
	ActorsJSON     string   `json:"actors_json,omitempty"`
	LocationCoords string   `json:"location_coords,omitempty"`
	SummaryText    string   `json:"summary_text"`
	Chronicled     bool     `json:"chronicled,omitempty"`
}

// EffectiveSaliency returns the saliency score for this event, falling back to
// DefaultSaliency(Category) if SaliencyScore is 0.
func (ev MemoryEvent) EffectiveSaliency() float64 {
	if ev.SaliencyScore != 0 {
		return ev.SaliencyScore
	}
	return DefaultSaliency(ev.Category)
}

// Embedder generates a fixed-dimension vector embedding (e.g. 768-dim nomic-embed-text)
// for input text. Implemented by system2.Client.
type Embedder interface {
	CreateEmbedding(ctx context.Context, input string) ([]float32, error)
}

// Common sentinel errors for the memory pipeline.
var (
	ErrInvalidCategory      = errors.New("invalid memory event category")
	ErrEmptySummaryText     = errors.New("memory summary text cannot be empty")
	ErrInvalidSaliencyScore = errors.New("saliency score must be between 0.0 and 1.0")
	ErrEmptyQuery           = errors.New("query cannot be empty")
	ErrNilDatabase          = errors.New("agent database cannot be nil")
	ErrNilEmbedder          = errors.New("embedder cannot be nil")
)
