package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Proxy is an HTTP reverse proxy that serializes llama.cpp requests through a Gateway.
type Proxy struct {
	gw       *Gateway
	upstream string // server root, e.g. http://127.0.0.1:8080
	client   *http.Client
	timeout  time.Duration
}

// ServerRoot strips a trailing /v1 from an OpenAI-style base URL.
func ServerRoot(baseURL string) string {
	b := strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(b, "/v1")
}

// Discover reads total_slots from the llama.cpp /props endpoint, returning
// fallback (min 1) when the server is unreachable or reports nothing usable.
func Discover(ctx context.Context, hc *http.Client, baseURL string, fallback int) int {
	if fallback <= 0 {
		fallback = 1
	}
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ServerRoot(baseURL)+"/props", nil)
	if err != nil {
		return fallback
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback
	}
	var props struct {
		TotalSlots int `json:"total_slots"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&props); err != nil || props.TotalSlots <= 0 {
		return fallback
	}
	return props.TotalSlots
}

// NewProxy builds a Proxy for the given upstream base URL. The gateway's
// RequestTimeout is applied per request after slot acquisition.
func NewProxy(gw *Gateway, upstreamBaseURL string) *Proxy {
	return &Proxy{
		gw:       gw,
		upstream: ServerRoot(upstreamBaseURL),
		client:   &http.Client{},
		timeout:  gw.cfg.RequestTimeout,
	}
}

// StatsPath serves gateway counters as JSON.
const StatsPath = "/cq/inference/stats"

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == StatsPath {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.gw.Stats())
		return
	}
	prio := ParsePriority(r.Header.Get(PriorityHeader))
	release, err := p.gw.Acquire(r.Context(), prio)
	if err != nil {
		switch err {
		case ErrDropped:
			w.Header().Set("Retry-After", "5")
			http.Error(w, `{"error":{"message":"stale ambient request dropped","type":"dropped"}}`, http.StatusTooManyRequests)
		case ErrClosed:
			http.Error(w, `{"error":{"message":"inference gateway closed"}}`, http.StatusServiceUnavailable)
		default: // client went away
			http.Error(w, err.Error(), 499)
		}
		return
	}
	defer release()

	// Deadline starts only now that a slot is held.
	ctx := r.Context()
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	out, err := http.NewRequestWithContext(ctx, r.Method, p.upstream+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Accept", "Authorization"} {
		if v := r.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	resp, err := p.client.Do(out)
	if err != nil {
		code := http.StatusBadGateway
		if ctx.Err() == context.DeadlineExceeded {
			code = http.StatusGatewayTimeout
		}
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), code)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
