package system2

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGovernor_Lifecycle(t *testing.T) {
	gov := NewGovernor(3, 0)
	if gov.Capacity() != 3 {
		t.Fatalf("expected capacity 3, got %d", gov.Capacity())
	}
	if gov.Available() != 3 {
		t.Fatalf("expected available 3, got %d", gov.Available())
	}
	if gov.Active() != 0 {
		t.Fatalf("expected active 0, got %d", gov.Active())
	}

	ctx := context.Background()
	if err := gov.Acquire(ctx); err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}

	if gov.Active() != 1 {
		t.Fatalf("expected active 1, got %d", gov.Active())
	}
	if gov.Available() != 2 {
		t.Fatalf("expected available 2, got %d", gov.Available())
	}

	gov.Release()
	if gov.Active() != 0 {
		t.Fatalf("expected active 0, got %d", gov.Active())
	}
}

func TestGovernor_TryAcquire(t *testing.T) {
	gov := NewGovernor(1, 0)
	if !gov.TryAcquire() {
		t.Fatal("first TryAcquire should succeed")
	}
	if gov.TryAcquire() {
		t.Fatal("second TryAcquire should fail when capacity is 1")
	}

	gov.Release()
	if !gov.TryAcquire() {
		t.Fatal("TryAcquire should succeed after release")
	}
	gov.Release()
}

func TestGovernor_QueueTimeout_Saturation(t *testing.T) {
	gov := NewGovernor(1, 50*time.Millisecond)

	ctx := context.Background()
	if err := gov.Acquire(ctx); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer gov.Release()

	// Second acquire should saturate the queue and fail after queueTimeout
	start := time.Now()
	err := gov.Acquire(ctx)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected queue timeout saturation error, got nil")
	}
	if !errors.Is(err, ErrWorkerPoolSaturated) {
		t.Fatalf("expected ErrWorkerPoolSaturated, got %v", err)
	}
	if duration < 40*time.Millisecond {
		t.Fatalf("timeout returned too quickly: %v", duration)
	}
}

func TestGovernor_ContextCancellation(t *testing.T) {
	gov := NewGovernor(1, 0)

	ctx := context.Background()
	if err := gov.Acquire(ctx); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer gov.Release()

	cancelCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := gov.Acquire(cancelCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestGovernor_ConcurrencyLimit(t *testing.T) {
	const limit = 3
	const totalRoutines = 12

	gov := NewGovernor(limit, 0)
	var activeCount int64
	var maxObserved int64
	var wg sync.WaitGroup

	ctx := context.Background()

	for i := 0; i < totalRoutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gov.Acquire(ctx); err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			curr := atomic.AddInt64(&activeCount, 1)

			// Record max concurrent observed
			for {
				oldMax := atomic.LoadInt64(&maxObserved)
				if curr <= oldMax || atomic.CompareAndSwapInt64(&maxObserved, oldMax, curr) {
					break
				}
			}

			// Simulate work
			time.Sleep(10 * time.Millisecond)

			atomic.AddInt64(&activeCount, -1)
			gov.Release()
		}()
	}

	wg.Wait()

	if maxObserved > limit {
		t.Fatalf("max concurrent observed %d exceeded limit %d", maxObserved, limit)
	}
}

func TestGovernor_Close(t *testing.T) {
	gov := NewGovernor(2, 0)

	ctx := context.Background()
	if err := gov.Acquire(ctx); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	if err := gov.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Idempotent close
	if err := gov.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	// New acquire should fail with ErrClientClosed
	err := gov.Acquire(ctx)
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("expected ErrClientClosed, got %v", err)
	}

	// TryAcquire should fail when closed
	if gov.TryAcquire() {
		t.Fatal("TryAcquire should return false when governor is closed")
	}
}

func TestGovernor_PreCanceledContext(t *testing.T) {
	gov := NewGovernor(1, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	err := gov.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if errors.Is(err, ErrWorkerPoolSaturated) {
		t.Fatalf("pre-canceled context must not return ErrWorkerPoolSaturated")
	}
}

func TestGovernor_CallerDeadlineExceeded(t *testing.T) {
	gov := NewGovernor(1, 0)
	// Saturate the single slot
	if err := gov.Acquire(context.Background()); err != nil {
		t.Fatalf("failed initial acquire: %v", err)
	}
	defer gov.Release()

	// Caller context with small deadline
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := gov.Acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	// With queueTimeout=0, caller deadline exceeded must return ctx.Err() directly, not ErrWorkerPoolSaturated
	if errors.Is(err, ErrWorkerPoolSaturated) {
		t.Fatalf("caller deadline exceeded should return ctx.Err(), not ErrWorkerPoolSaturated")
	}
}
