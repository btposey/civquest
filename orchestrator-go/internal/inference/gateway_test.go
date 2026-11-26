package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestDiscoverSlots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"total_slots":2}`)
	}))
	defer srv.Close()
	if n := Discover(context.Background(), nil, srv.URL+"/v1", 7); n != 2 {
		t.Fatalf("got %d want 2", n)
	}
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if n := Discover(context.Background(), nil, bad.URL+"/v1", 4); n != 4 {
		t.Fatalf("fallback got %d want 4", n)
	}
	if n := Discover(context.Background(), nil, "http://127.0.0.1:1/v1", 0); n != 1 {
		t.Fatalf("fallback min got %d want 1", n)
	}
}

func TestPriorityOrdering(t *testing.T) {
	g := New(Config{Slots: 1})
	hold, err := g.Acquire(context.Background(), Tactical)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []Priority
	var wg sync.WaitGroup
	for _, p := range []Priority{Ambient, Dialogue, Tactical, Ambient} {
		wg.Add(1)
		want := g.Stats().Queued + 1
		go func() {
			defer wg.Done()
			rel, err := g.Acquire(context.Background(), p)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, p)
			mu.Unlock()
			rel()
		}()
		waitFor(t, func() bool { return g.Stats().Queued == want })
	}
	hold()
	wg.Wait()
	exp := []Priority{Tactical, Dialogue, Ambient, Ambient}
	for i := range exp {
		if order[i] != exp[i] {
			t.Fatalf("order %v want %v", order, exp)
		}
	}
}

func TestQueueWithoutTimeout(t *testing.T) {
	g := New(Config{Slots: 1, RequestTimeout: 20 * time.Millisecond})
	hold, _ := g.Acquire(context.Background(), Tactical)
	got := make(chan error, 1)
	go func() {
		rel, err := g.Acquire(context.Background(), Dialogue)
		if err == nil {
			rel()
		}
		got <- err
	}()
	waitFor(t, func() bool { return g.Stats().Queued == 1 })
	time.Sleep(100 * time.Millisecond) // well beyond RequestTimeout
	select {
	case err := <-got:
		t.Fatalf("queued request ended early: %v", err)
	default:
	}
	hold()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
}

func TestContextCancelWhileQueued(t *testing.T) {
	g := New(Config{Slots: 1})
	hold, _ := g.Acquire(context.Background(), Tactical)
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() { _, err := g.Acquire(ctx, Tactical); got <- err }()
	waitFor(t, func() bool { return g.Stats().Queued == 1 })
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if s := g.Stats(); s.Queued != 0 || s.InFlight != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestStaleAmbientDropped(t *testing.T) {
	g := New(Config{Slots: 1, StaleAge: 30 * time.Millisecond})
	hold, _ := g.Acquire(context.Background(), Tactical)
	defer hold()
	if _, err := g.Acquire(context.Background(), Ambient); !errors.Is(err, ErrDropped) {
		t.Fatalf("got %v want ErrDropped", err)
	}
	if s := g.Stats(); s.Dropped != 1 || s.Queued != 0 {
		t.Fatalf("stats %+v", s)
	}
	// Non-ambient waits are never dropped.
	got := make(chan error, 1)
	go func() { _, err := g.Acquire(context.Background(), Dialogue); got <- err }()
	waitFor(t, func() bool { return g.Stats().Queued == 1 })
	time.Sleep(80 * time.Millisecond)
	if g.Stats().Queued != 1 || g.Stats().Dropped != 1 {
		t.Fatalf("dialogue dropped: %+v", g.Stats())
	}
}

func TestProxyDeadlineStartsAtAcquisition(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slow") {
			<-release
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer up.Close()
	g := New(Config{Slots: 1, RequestTimeout: 150 * time.Millisecond})
	px := httptest.NewServer(NewProxy(g, up.URL+"/v1"))
	defer px.Close()

	// First request holds the only slot for ~100ms (under its own deadline).
	first := make(chan int, 1)
	go func() {
		resp, err := http.Post(px.URL+"/v1/slow", "application/json", strings.NewReader("{}"))
		if err != nil {
			first <- -1
			return
		}
		resp.Body.Close()
		first <- resp.StatusCode
	}()
	waitFor(t, func() bool { return g.Stats().InFlight == 1 })
	second := make(chan int, 1)
	go func() {
		resp, err := http.Post(px.URL+"/v1/fast", "application/json", strings.NewReader("{}"))
		if err != nil {
			second <- -1
			return
		}
		resp.Body.Close()
		second <- resp.StatusCode
	}()
	waitFor(t, func() bool { return g.Stats().Queued == 1 })
	time.Sleep(100 * time.Millisecond)
	close(release)
	if c := <-first; c != 200 {
		t.Fatalf("first %d", c)
	}
	if c := <-second; c != 200 {
		t.Fatalf("queued request got %d (deadline must not count queue time)", c)
	}
}

func TestProxyDroppedReturns429AndPriorityHeader(t *testing.T) {
	g := New(Config{Slots: 1, StaleAge: 20 * time.Millisecond})
	hold, _ := g.Acquire(context.Background(), Tactical)
	defer hold()
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	px := httptest.NewServer(NewProxy(g, up.URL))
	defer px.Close()
	req, _ := http.NewRequest(http.MethodPost, px.URL+"/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set(PriorityHeader, "ambient")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if g.Stats().Dropped != 1 {
		t.Fatalf("stats %+v", g.Stats())
	}
}
