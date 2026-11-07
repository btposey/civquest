package db

import (
	"errors"
	"math"
	"sort"
	"testing"
)

func TestVectorSerialization(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		if b := Float32SliceToBytes(nil); b != nil {
			t.Fatalf("expected nil bytes, got %v", b)
		}
		if v := BytesToFloat32Slice(nil); v != nil {
			t.Fatalf("expected nil float slice, got %v", v)
		}
	})

	t.Run("invalid byte length", func(t *testing.T) {
		if v := BytesToFloat32Slice([]byte{1, 2, 3}); v != nil {
			t.Fatalf("expected nil for non-multiple-of-4 bytes, got %v", v)
		}
	})

	t.Run("round trip 768 floats", func(t *testing.T) {
		original := make([]float32, 768)
		for i := range original {
			original[i] = float32(i) * 0.1234
		}

		rawBytes := Float32SliceToBytes(original)
		if len(rawBytes) != 768*4 {
			t.Fatalf("expected %d bytes, got %d", 768*4, len(rawBytes))
		}

		decoded := BytesToFloat32Slice(rawBytes)
		if len(decoded) != len(original) {
			t.Fatalf("expected %d floats, got %d", len(original), len(decoded))
		}

		for i := range original {
			if decoded[i] != original[i] {
				t.Fatalf("at index %d: expected %f, got %f", i, original[i], decoded[i])
			}
		}
	})
}

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical vectors", func(t *testing.T) {
		a := []float32{1.0, 2.0, 3.0, 4.0, 5.0}
		sim := CosineSimilarity(a, a)
		if math.Abs(float64(sim-1.0)) > 1e-6 {
			t.Fatalf("expected similarity ~1.0, got %f", sim)
		}
	})

	t.Run("orthogonal vectors", func(t *testing.T) {
		a := []float32{1.0, 0.0, 0.0, 0.0}
		b := []float32{0.0, 1.0, 0.0, 0.0}
		sim := CosineSimilarity(a, b)
		if math.Abs(float64(sim)) > 1e-6 {
			t.Fatalf("expected similarity ~0.0, got %f", sim)
		}
	})

	t.Run("opposite vectors", func(t *testing.T) {
		a := []float32{2.0, 4.0, 6.0}
		b := []float32{-2.0, -4.0, -6.0}
		sim := CosineSimilarity(a, b)
		if math.Abs(float64(sim-(-1.0))) > 1e-6 {
			t.Fatalf("expected similarity ~ -1.0, got %f", sim)
		}
	})

	t.Run("zero vector", func(t *testing.T) {
		a := []float32{0.0, 0.0, 0.0}
		b := []float32{1.0, 2.0, 3.0}
		sim := CosineSimilarity(a, b)
		if sim != 0.0 {
			t.Fatalf("expected similarity 0.0 for zero vector, got %f", sim)
		}
	})

	t.Run("mismatched lengths", func(t *testing.T) {
		a := []float32{1.0, 2.0}
		b := []float32{1.0, 2.0, 3.0}
		sim := CosineSimilarity(a, b)
		if sim != 0.0 {
			t.Fatalf("expected similarity 0.0 for mismatched lengths, got %f", sim)
		}
	})

	t.Run("768-dim normalized vectors", func(t *testing.T) {
		a := make([]float32, 768)
		b := make([]float32, 768)
		for i := range a {
			a[i] = float32(i + 1)
			b[i] = float32(i + 1)
		}
		// Slightly perturb b
		b[0] += 0.5

		sim := CosineSimilarity(a, b)
		if sim < 0.99 || sim > 1.0 {
			t.Fatalf("expected high similarity > 0.99, got %f", sim)
		}
	})
}

func TestCosineSimilarity_NaNAndInf(t *testing.T) {
	valid := []float32{1.0, 2.0, 3.0, 4.0, 5.0}

	testCases := []struct {
		name string
		a    []float32
		b    []float32
	}{
		{
			name: "NaN in vector A at unrolled index",
			a:    []float32{float32(math.NaN()), 2.0, 3.0, 4.0, 5.0},
			b:    valid,
		},
		{
			name: "NaN in vector A at tail index",
			a:    []float32{1.0, 2.0, 3.0, 4.0, float32(math.NaN())},
			b:    valid,
		},
		{
			name: "NaN in vector B",
			a:    valid,
			b:    []float32{1.0, float32(math.NaN()), 3.0, 4.0, 5.0},
		},
		{
			name: "+Inf in vector A",
			a:    []float32{float32(math.Inf(1)), 2.0, 3.0, 4.0, 5.0},
			b:    valid,
		},
		{
			name: "-Inf in vector B",
			a:    valid,
			b:    []float32{1.0, 2.0, 3.0, float32(math.Inf(-1)), 5.0},
		},
		{
			name: "both Inf",
			a:    []float32{float32(math.Inf(1)), 2.0, 3.0, 4.0, 5.0},
			b:    []float32{float32(math.Inf(-1)), 2.0, 3.0, 4.0, 5.0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sim := CosineSimilarity(tc.a, tc.b)
			if sim != 0.0 {
				t.Fatalf("expected similarity 0.0 for non-finite inputs, got %f", sim)
			}
			if math.IsNaN(float64(sim)) || math.IsInf(float64(sim), 0) {
				t.Fatalf("expected finite 0.0, got non-finite %f", sim)
			}
		})
	}
}

func TestValidateVector(t *testing.T) {
	t.Run("valid 768 dimensions", func(t *testing.T) {
		v := make([]float32, 768)
		for i := range v {
			v[i] = 0.5
		}
		if err := ValidateVector(v); err != nil {
			t.Fatalf("expected valid vector, got: %v", err)
		}
	})

	t.Run("mismatched dimensions - 767", func(t *testing.T) {
		v := make([]float32, 767)
		err := ValidateVector(v)
		if !errors.Is(err, ErrInvalidVectorDimension) {
			t.Fatalf("expected ErrInvalidVectorDimension, got: %v", err)
		}
	})

	t.Run("mismatched dimensions - 769", func(t *testing.T) {
		v := make([]float32, 769)
		err := ValidateVector(v)
		if !errors.Is(err, ErrInvalidVectorDimension) {
			t.Fatalf("expected ErrInvalidVectorDimension, got: %v", err)
		}
	})

	t.Run("empty vector", func(t *testing.T) {
		err := ValidateVector([]float32{})
		if !errors.Is(err, ErrInvalidVectorDimension) {
			t.Fatalf("expected ErrInvalidVectorDimension, got: %v", err)
		}
	})

	t.Run("contains NaN at index 0", func(t *testing.T) {
		v := make([]float32, 768)
		v[0] = float32(math.NaN())
		err := ValidateVector(v)
		if !errors.Is(err, ErrNonFiniteVector) {
			t.Fatalf("expected ErrNonFiniteVector, got: %v", err)
		}
	})

	t.Run("contains +Inf at index 500", func(t *testing.T) {
		v := make([]float32, 768)
		v[500] = float32(math.Inf(1))
		err := ValidateVector(v)
		if !errors.Is(err, ErrNonFiniteVector) {
			t.Fatalf("expected ErrNonFiniteVector, got: %v", err)
		}
	})

	t.Run("contains -Inf at index 767", func(t *testing.T) {
		v := make([]float32, 768)
		v[767] = float32(math.Inf(-1))
		err := ValidateVector(v)
		if !errors.Is(err, ErrNonFiniteVector) {
			t.Fatalf("expected ErrNonFiniteVector, got: %v", err)
		}
	})
}

func TestVectorSortStability(t *testing.T) {
	// Test that matches with identical similarity scores have deterministic ordering
	// when secondary sorting by ID is used.
	matches := []EpisodicMemoryMatch{
		{Memory: EpisodicMemory{ID: 3}, Similarity: 0.85},
		{Memory: EpisodicMemory{ID: 1}, Similarity: 0.95},
		{Memory: EpisodicMemory{ID: 5}, Similarity: 0.85},
		{Memory: EpisodicMemory{ID: 2}, Similarity: 0.85},
		{Memory: EpisodicMemory{ID: 4}, Similarity: 0.50},
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity != matches[j].Similarity {
			return matches[i].Similarity > matches[j].Similarity
		}
		return matches[i].Memory.ID < matches[j].Memory.ID
	})

	expectedIDs := []int64{1, 2, 3, 5, 4}
	for i, exp := range expectedIDs {
		if matches[i].Memory.ID != exp {
			t.Errorf("at index %d: expected ID %d, got %d", i, exp, matches[i].Memory.ID)
		}
	}
}
