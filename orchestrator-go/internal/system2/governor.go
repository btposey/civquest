package system2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultMaxConcurrentLLMRequests is the fallback concurrency limit.
const DefaultMaxConcurrentLLMRequests = 4

// Governor controls the concurrent execution of System 2 LLM requests
// across the agent process using a counting semaphore.
type Governor struct {
	sem          chan struct{}
	capacity     int
	queueTimeout time.Duration
	mu           sync.RWMutex
	closed       bool
	closedCh     chan struct{}
}

// NewGovernor creates a Governor bounded by maxConcurrent.
// If maxConcurrent <= 0, DefaultMaxConcurrentLLMRequests is used.
// If queueTimeout > 0, requests waiting for a slot fail after that duration.
func NewGovernor(maxConcurrent int, queueTimeout time.Duration) *Governor {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentLLMRequests
	}
	return &Governor{
		sem:          make(chan struct{}, maxConcurrent),
		capacity:     maxConcurrent,
		queueTimeout: queueTimeout,
		closedCh:     make(chan struct{}),
	}
}

// Acquire requests an execution slot from the worker pool.
// It blocks until a slot is granted, ctx is cancelled/timed out, the queue timeout
// expires, or the governor is closed.
func (g *Governor) Acquire(ctx context.Context) error {
	// Fast check for closed state
	g.mu.RLock()
	if g.closed {
		g.mu.RUnlock()
		return ErrClientClosed
	}
	closedCh := g.closedCh
	g.mu.RUnlock()

	// Fast check for pre-canceled context
	if err := ctx.Err(); err != nil {
		return err
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if g.queueTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, g.queueTimeout)
		defer cancel()
	}

	select {
	case g.sem <- struct{}{}:
		return nil
	case <-waitCtx.Done():
		if g.queueTimeout > 0 && ctx.Err() == nil && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: inference worker pool saturated (queue timed out after %v)", ErrWorkerPoolSaturated, g.queueTimeout)
		}
		return waitCtx.Err()
	case <-closedCh:
		return ErrClientClosed
	}
}

// TryAcquire attempts an immediate, non-blocking acquisition of a worker slot.
// Returns true if a slot was secured, or false if the pool is saturated or closed.
func (g *Governor) TryAcquire() bool {
	g.mu.RLock()
	if g.closed {
		g.mu.RUnlock()
		return false
	}
	g.mu.RUnlock()

	select {
	case g.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release yields a previously acquired slot back to the worker pool.
func (g *Governor) Release() {
	select {
	case <-g.sem:
	default:
	}
}

// Active returns the count of currently in-use inference slots.
func (g *Governor) Active() int {
	return len(g.sem)
}

// Available returns the count of free inference slots.
func (g *Governor) Available() int {
	return g.capacity - len(g.sem)
}

// Capacity returns the maximum concurrent requests permitted.
func (g *Governor) Capacity() int {
	return g.capacity
}

// Close closes the governor, waking any pending callers with ErrClientClosed.
func (g *Governor) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	close(g.closedCh)
	return nil
}
