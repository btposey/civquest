// Package inference implements the shared, cross-process inference gateway
// (ADR-038): a slot-aware priority queue in front of a llama.cpp server.
package inference

import (
	"container/heap"
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Priority orders queued requests; lower value is served first.
type Priority int

const (
	// Tactical requests gate survival/combat decisions and always go first.
	Tactical Priority = iota
	// Dialogue requests serve player-visible conversation.
	Dialogue
	// Ambient requests (lore, contemplation) may be dropped when stale.
	Ambient
)

// PriorityHeader carries the request priority from agent clients to the proxy.
const PriorityHeader = "X-CQ-Priority"

func (p Priority) String() string {
	switch p {
	case Tactical:
		return "tactical"
	case Dialogue:
		return "dialogue"
	default:
		return "ambient"
	}
}

// ParsePriority maps a header value to a Priority (unknown -> Dialogue).
func ParsePriority(s string) Priority {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tactical":
		return Tactical
	case "ambient":
		return Ambient
	default:
		return Dialogue
	}
}

type ctxKey struct{}

// WithPriority tags ctx so the client forwards the priority to the gateway.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PriorityFrom returns the priority on ctx, or def when none is set.
func PriorityFrom(ctx context.Context, def Priority) Priority {
	if p, ok := ctx.Value(ctxKey{}).(Priority); ok {
		return p
	}
	return def
}

// Errors returned by Acquire.
var (
	// ErrDropped means a stale ambient request was discarded while queued.
	ErrDropped = errors.New("inference: stale ambient request dropped")
	// ErrClosed means the gateway was closed.
	ErrClosed = errors.New("inference: gateway closed")
)

// Config configures a Gateway.
type Config struct {
	Slots          int           // concurrency; <=0 means 1 (use Discover to learn it)
	StaleAge       time.Duration // ambient queue age limit; <=0 disables dropping
	RequestTimeout time.Duration // per-request deadline starting at slot acquisition; <=0 none
}

// Stats is a snapshot of gateway counters.
type Stats struct {
	Slots     int   `json:"slots"`
	Queued    int   `json:"queued"`
	InFlight  int   `json:"in_flight"`
	Dropped   int64 `json:"dropped"`
	Completed int64 `json:"completed"`
}

type waiter struct {
	prio    Priority
	seq     uint64
	index   int
	ready   chan struct{} // closed when granted
	done    bool          // granted, removed or closed (guarded by Gateway.mu)
	granted bool
}

type waitHeap []*waiter

func (h waitHeap) Len() int { return len(h) }
func (h waitHeap) Less(i, j int) bool {
	if h[i].prio != h[j].prio {
		return h[i].prio < h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h waitHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *waitHeap) Push(x any) { w := x.(*waiter); w.index = len(*h); *h = append(*h, w) }
func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	*h = old[:n-1]
	return w
}

// Gateway is a priority-ordered counting semaphore. Queued callers never
// time out; only ctx cancellation, closure, or stale-ambient dropping ends a wait.
type Gateway struct {
	cfg Config

	mu        sync.Mutex
	slots     int
	inFlight  int
	queue     waitHeap
	seq       uint64
	dropped   int64
	completed int64
	closed    bool
}

// New creates a Gateway.
func New(cfg Config) *Gateway {
	slots := cfg.Slots
	if slots <= 0 {
		slots = 1
	}
	return &Gateway{cfg: cfg, slots: slots}
}

// SetSlots changes concurrency (e.g. after discovery) and wakes waiters.
func (g *Gateway) SetSlots(n int) {
	if n <= 0 {
		n = 1
	}
	g.mu.Lock()
	g.slots = n
	g.grantLocked()
	g.mu.Unlock()
}

// Stats returns a counter snapshot.
func (g *Gateway) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Stats{Slots: g.slots, Queued: len(g.queue), InFlight: g.inFlight, Dropped: g.dropped, Completed: g.completed}
}

// Close wakes all waiters with ErrClosed and rejects new work.
func (g *Gateway) Close() {
	g.mu.Lock()
	g.closed = true
	for len(g.queue) > 0 {
		w := heap.Pop(&g.queue).(*waiter)
		w.done = true
		w.index = -1
		close(w.ready)
	}
	g.mu.Unlock()
}

// grantLocked hands free slots to the best waiters. Caller holds g.mu.
func (g *Gateway) grantLocked() {
	for g.inFlight < g.slots && len(g.queue) > 0 {
		w := heap.Pop(&g.queue).(*waiter)
		w.done = true
		w.index = -1
		w.granted = true
		g.inFlight++
		close(w.ready)
	}
}

// Acquire blocks until a slot is granted and returns its release func.
// There is no queue timeout: the wait ends only on grant, ctx cancellation,
// Close, or (Ambient only) the stale age elapsing, which returns ErrDropped.
func (g *Gateway) Acquire(ctx context.Context, p Priority) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	g.seq++
	w := &waiter{prio: p, seq: g.seq, ready: make(chan struct{})}
	heap.Push(&g.queue, w)
	g.grantLocked()
	g.mu.Unlock()

	var stale <-chan time.Time
	if p == Ambient && g.cfg.StaleAge > 0 {
		t := time.NewTimer(g.cfg.StaleAge)
		defer t.Stop()
		stale = t.C
	}

	select {
	case <-w.ready:
		return g.result(w)
	case <-ctx.Done():
		if g.remove(w, false) {
			return nil, ctx.Err()
		}
		// Granted concurrently: give the slot back.
		if rel, err := g.result(w); err == nil {
			rel()
		}
		return nil, ctx.Err()
	case <-stale:
		if g.remove(w, true) {
			return nil, ErrDropped
		}
		return g.result(w)
	}
}

// result converts a woken waiter into a release func or an error.
func (g *Gateway) result(w *waiter) (func(), error) {
	g.mu.Lock()
	granted := w.granted
	g.mu.Unlock()
	if !granted {
		return nil, ErrClosed
	}
	var once sync.Once
	return func() { once.Do(g.release) }, nil
}

// remove takes a still-queued waiter out of the queue; false if already granted/closed.
func (g *Gateway) remove(w *waiter, drop bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w.done {
		return false
	}
	heap.Remove(&g.queue, w.index)
	w.done = true
	w.index = -1
	if drop {
		g.dropped++
	}
	return true
}

func (g *Gateway) release() {
	g.mu.Lock()
	g.inFlight--
	g.completed++
	g.grantLocked()
	g.mu.Unlock()
}
