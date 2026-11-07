package db

import (
	"math"
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
