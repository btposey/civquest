package db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// VectorDimension defines the standard 768-dimensional embedding size (e.g. nomic-embed-text).
const VectorDimension = 768

// Sentinel errors for vector operations.
var (
	ErrInvalidVectorDimension = errors.New("vector embedding must have exactly 768 dimensions")
	ErrNonFiniteVector        = errors.New("vector contains non-finite elements (NaN or Inf)")
)

// ValidateVector validates that a vector has exactly VectorDimension (768) elements
// and that all elements are finite (neither NaN nor infinite).
func ValidateVector(v []float32) error {
	if len(v) != VectorDimension {
		return fmt.Errorf("%w: got %d, expected %d", ErrInvalidVectorDimension, len(v), VectorDimension)
	}
	for i, val := range v {
		f := float64(val)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%w at index %d", ErrNonFiniteVector, i)
		}
	}
	return nil
}

// Float32SliceToBytes converts a slice of float32 into little-endian IEEE-754 bytes.
// Stored as BLOB in episodic_memories_vec (768 float32 values = 3072 bytes).
func Float32SliceToBytes(vec []float32) []byte {
	if vec == nil {
		return nil
	}
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// BytesToFloat32Slice converts little-endian IEEE-754 binary bytes into a slice of float32.
func BytesToFloat32Slice(buf []byte) []float32 {
	if len(buf) == 0 || len(buf)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}

// DotProduct calculates the scalar dot product of two float32 slices.
// Utilizes 4-way superscalar loop unrolling with independent accumulators to maximize instruction-level
// parallelism (ILP) and CPU pipeline utilization per ADR-013.
func DotProduct(a, b []float32) float32 {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0
	}
	var d0, d1, d2, d3 float32
	i := 0
	for ; i <= n-4; i += 4 {
		d0 += a[i] * b[i]
		d1 += a[i+1] * b[i+1]
		d2 += a[i+2] * b[i+2]
		d3 += a[i+3] * b[i+3]
	}
	dot := (d0 + d1) + (d2 + d3)
	for ; i < n; i++ {
		dot += a[i] * b[i]
	}
	return dot
}

// NormSquared computes the squared Euclidean norm of a vector.
// Utilizes 4-way superscalar loop unrolling with independent accumulators per ADR-013.
func NormSquared(v []float32) float32 {
	n := len(v)
	if n == 0 {
		return 0
	}
	var n0, n1, n2, n3 float32
	i := 0
	for ; i <= n-4; i += 4 {
		n0 += v[i] * v[i]
		n1 += v[i+1] * v[i+1]
		n2 += v[i+2] * v[i+2]
		n3 += v[i+3] * v[i+3]
	}
	normSq := (n0 + n1) + (n2 + n3)
	for ; i < n; i++ {
		normSq += v[i] * v[i]
	}
	return normSq
}

// CosineSimilarity computes the cosine similarity between two float32 slices.
// Leverages independent accumulators and 4-way superscalar loop unrolling to maximize CPU pipeline
// execution and instruction-level parallelism (ILP) per ADR-013 without competing for GPU VRAM.
// Returns a value in [-1.0, 1.0], or 0.0 if vectors differ in size, either norm is zero,
// or non-finite (NaN / Inf) values are encountered.
func CosineSimilarity(a, b []float32) float32 {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0
	}

	var d0, d1, d2, d3 float32
	var na0, na1, na2, na3 float32
	var nb0, nb1, nb2, nb3 float32

	i := 0
	for ; i <= n-4; i += 4 {
		ai0, ai1, ai2, ai3 := a[i], a[i+1], a[i+2], a[i+3]
		bi0, bi1, bi2, bi3 := b[i], b[i+1], b[i+2], b[i+3]

		d0 += ai0 * bi0
		d1 += ai1 * bi1
		d2 += ai2 * bi2
		d3 += ai3 * bi3

		na0 += ai0 * ai0
		na1 += ai1 * ai1
		na2 += ai2 * ai2
		na3 += ai3 * ai3

		nb0 += bi0 * bi0
		nb1 += bi1 * bi1
		nb2 += bi2 * bi2
		nb3 += bi3 * bi3
	}

	dot := (d0 + d1) + (d2 + d3)
	normA := (na0 + na1) + (na2 + na3)
	normB := (nb0 + nb1) + (nb2 + nb3)

	for ; i < n; i++ {
		ai := a[i]
		bi := b[i]
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	// Guard accumulators against NaN and Inf
	dotF := float64(dot)
	normAF := float64(normA)
	normBF := float64(normB)
	if math.IsNaN(dotF) || math.IsInf(dotF, 0) ||
		math.IsNaN(normAF) || math.IsInf(normAF, 0) ||
		math.IsNaN(normBF) || math.IsInf(normBF, 0) {
		return 0
	}

	if normA <= 0 || normB <= 0 {
		return 0
	}

	denom := float32(math.Sqrt(normAF) * math.Sqrt(normBF))
	denomF := float64(denom)
	if math.IsNaN(denomF) || math.IsInf(denomF, 0) || denom <= 0 {
		return 0
	}

	sim := dot / denom
	simF := float64(sim)
	if math.IsNaN(simF) || math.IsInf(simF, 0) {
		return 0
	}

	// Clamp to [-1.0, 1.0] to account for floating point imprecision
	if sim > 1.0 {
		return 1.0
	}
	if sim < -1.0 {
		return -1.0
	}
	return sim
}
