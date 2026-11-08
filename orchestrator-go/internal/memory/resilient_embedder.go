package memory

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// ResilientEmbedder wraps a primary Embedder (such as an OpenAI/llama.cpp HTTP client)
// and transparently falls back to a deterministic 768-dimensional local feature-hash
// embedding whenever the remote server does not support `/v1/embeddings` (e.g., HTTP 501)
// or is temporarily unreachable.
type ResilientEmbedder struct {
	primary           Embedder
	remoteUnsupported atomic.Bool
}

// NewResilientEmbedder constructs an Embedder that attempts primary first and falls back
// to deterministic 768-dim local feature hashing.
func NewResilientEmbedder(primary Embedder) *ResilientEmbedder {
	return &ResilientEmbedder{primary: primary}
}

// CreateEmbedding generates a 768-dimensional L2-normalized float32 vector.
func (r *ResilientEmbedder) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.primary != nil && !r.remoteUnsupported.Load() {
		vec, err := r.primary.CreateEmbedding(ctx, input)
		if err == nil && len(vec) == db.VectorDimension {
			return vec, nil
		}
		if err != nil && (strings.Contains(err.Error(), "501") || strings.Contains(strings.ToLower(err.Error()), "not support embeddings")) {
			r.remoteUnsupported.Store(true)
		}
	}
	return LocalFeatureHashEmbedding(input), nil
}

// LocalFeatureHashEmbedding computes a deterministic, L2-normalized 768-dimensional float32
// vector from text using signed Weinberger feature hashing over word unigrams, word bigrams,
// and character 3-grams in <50µs with zero network calls.
func LocalFeatureHashEmbedding(text string) []float32 {
	vec := make([]float32, db.VectorDimension)
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		vec[0] = 1.0
		return vec
	}

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	addFeature := func(feat string, weight float32) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feat))
		sum := h.Sum64()
		idx := int(sum % uint64(db.VectorDimension))
		sign := float32(1.0)
		if (sum>>32)&1 == 1 {
			sign = -1.0
		}
		vec[idx] += sign * weight
	}

	for i, w := range words {
		addFeature("w:"+w, 2.0)
		if len(w) >= 4 {
			addFeature("stem:"+w[:4], 1.2)
		}
		if i+1 < len(words) {
			addFeature("bg:"+w+"_"+words[i+1], 1.5)
		}
		runes := []rune(w)
		for j := 0; j+3 <= len(runes); j++ {
			addFeature("c3:"+string(runes[j:j+3]), 0.5)
		}
	}

	normSq := db.NormSquared(vec)
	if normSq <= 0 {
		vec[0] = 1.0
		return vec
	}
	invNorm := float32(1.0 / math.Sqrt(float64(normSq)))
	for i := range vec {
		vec[i] *= invNorm
	}
	return vec
}
