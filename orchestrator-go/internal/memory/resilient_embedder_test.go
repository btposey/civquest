package memory

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

type failingEmbedder struct{}

func (f failingEmbedder) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return nil, errors.New("HTTP 501: embeddings not supported")
}

func TestResilientEmbedder_Fallback(t *testing.T) {
	emb := NewResilientEmbedder(failingEmbedder{})
	vec, err := emb.CreateEmbedding(context.Background(), "Fought a zombie at night near the hill")
	if err != nil {
		t.Fatalf("expected resilient fallback to succeed, got error: %v", err)
	}
	if len(vec) != db.VectorDimension {
		t.Fatalf("expected dimension %d, got %d", db.VectorDimension, len(vec))
	}
	var normSq float64
	for _, v := range vec {
		normSq += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(normSq)-1.0) > 1e-3 {
		t.Errorf("expected L2-normalized vector, got norm=%f", math.Sqrt(normSq))
	}
}
