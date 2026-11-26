package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/inference"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// gateway.inference smoke (cq-71l.9): a fake llama.cpp server advertising one slot, the real
// Gateway + Proxy in front of it, and two independent System 2 clients (two "agents") contending
// for the slot with different priorities.
func TestIntegration_Gateway_TwoClientsContendForDiscoveredSlot(t *testing.T) {
	var (
		mu        sync.Mutex
		order     []string
		inFlight  atomic.Int32
		maxFlight atomic.Int32
	)
	holderIn := make(chan struct{})
	releaseHolder := make(chan struct{})

	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			_, _ = w.Write([]byte(`{"total_slots":1}`))
		case "/v1/chat/completions":
			var req system2.ChatCompletionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			label := req.Messages[len(req.Messages)-1].Content
			n := inFlight.Add(1)
			if n > maxFlight.Load() {
				maxFlight.Store(n)
			}
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
			if label == "holder" {
				close(holderIn)
				<-releaseHolder
			}
			inFlight.Add(-1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{Choices: []system2.ChatCompletionChoice{
				{Message: system2.ChatMessage{Role: system2.RoleAssistant, Content: `{"ok":true}`}, FinishReason: "stop"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer llama.Close()

	slots := inference.Discover(context.Background(), nil, llama.URL+"/v1", 4)
	if slots != 1 {
		t.Fatalf("slot discovery: got %d want 1 (from /props)", slots)
	}
	gw := inference.New(inference.Config{Slots: slots})
	defer gw.Close()
	proxy := httptest.NewServer(inference.NewProxy(gw, llama.URL+"/v1"))
	defer proxy.Close()

	mkClient := func() *system2.Client {
		c, err := system2.NewClientWithConfig(xdg.LLMGatewayConfig{BaseURL: llama.URL + "/v1"},
			xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 8, RequestTimeoutSeconds: 10},
			system2.WithInferenceProxy(proxy.URL+"/v1"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	agentA, agentB := mkClient(), mkClient()

	call := func(c *system2.Client, p inference.Priority, label string, wg *sync.WaitGroup) {
		defer wg.Done()
		ctx := inference.WithPriority(context.Background(), p)
		if _, err := c.ChatCompletion(ctx, system2.ChatCompletionRequest{Messages: []system2.ChatMessage{
			{Role: "system", Content: "s"}, {Role: "user", Content: label}}}); err != nil {
			t.Errorf("%s failed: %v", label, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go call(agentA, inference.Tactical, "holder", &wg)
	select {
	case <-holderIn:
	case <-time.After(3 * time.Second):
		t.Fatal("holder never reached the fake llama server")
	}

	// Queue in worst-first order, waiting for each to be parked so the order is deterministic.
	queued := 0
	enqueue := func(c *system2.Client, p inference.Priority, label string) {
		wg.Add(1)
		go call(c, p, label, &wg)
		queued++
		deadline := time.Now().Add(3 * time.Second)
		for gw.Stats().Queued < queued && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if gw.Stats().Queued < queued {
			t.Fatalf("%s never queued: %+v", label, gw.Stats())
		}
	}
	enqueue(agentB, inference.Ambient, "ambient")
	enqueue(agentA, inference.Dialogue, "dialogue")
	enqueue(agentB, inference.Tactical, "tactical")

	if st := gw.Stats(); st.InFlight != 1 || st.Queued != 3 {
		t.Fatalf("with one slot busy, 3 must queue: %+v", st)
	}
	close(releaseHolder)
	wg.Wait()

	mu.Lock()
	got := fmt.Sprint(order)
	mu.Unlock()
	if want := "[holder tactical dialogue ambient]"; got != want {
		t.Fatalf("priority order: got %s want %s", got, want)
	}
	if maxFlight.Load() != 1 {
		t.Fatalf("one slot but %d upstream requests ran concurrently", maxFlight.Load())
	}
	if st := gw.Stats(); st.Completed != 4 || st.Dropped != 0 {
		t.Fatalf("stats: %+v", st)
	}
}
