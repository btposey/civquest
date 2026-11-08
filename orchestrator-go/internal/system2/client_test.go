package system2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type sampleIntent struct {
	Action string `json:"action"`
	Target string `json:"target"`
	Quota  int    `json:"quota"`
}

func TestClient_CompleteStructured_Success(t *testing.T) {
	var capturedReq ChatCompletionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected URL path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Bearer test-api-key, got %s", r.Header.Get("Authorization"))
		}

		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		resp := ChatCompletionResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   capturedReq.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: `{"action": "GATHER", "target": "minecraft:oak_log", "quota": 64}`,
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &xdg.GlobalConfig{
		LLMGateway: xdg.LLMGatewayConfig{
			BaseURL:               server.URL + "/v1",
			APIKey:                "test-api-key",
			Model:                 "llama3.1",
			EmbeddingModel:        "nomic-embed-text",
			StructuredTemperature: 0.15,
			CreativeTemperature:   0.85,
		},
		InferenceGovernor: xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 2,
			RequestTimeoutSeconds:    5,
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	messages := []ChatMessage{
		{Role: RoleSystem, Content: "You are an autonomous settlement architect."},
		{Role: RoleUser, Content: "Plan lumber gathering."},
	}

	intent, err := CompleteStructured[sampleIntent](ctx, client, messages)
	if err != nil {
		t.Fatalf("unexpected CompleteStructured error: %v", err)
	}

	if intent.Action != "GATHER" || intent.Target != "minecraft:oak_log" || intent.Quota != 64 {
		t.Fatalf("unexpected unmarshaled struct: %+v", intent)
	}

	// Verify request parameters sent to gateway
	if capturedReq.Temperature != 0.15 {
		t.Fatalf("expected structured temperature 0.15, got %v", capturedReq.Temperature)
	}
	if capturedReq.ResponseFormat == nil || capturedReq.ResponseFormat.Type != "json_object" {
		t.Fatalf("expected response_format json_object, got %+v", capturedReq.ResponseFormat)
	}
}

func TestClient_CleanJSONMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean json object untouched",
			input:    `{"action": "BUILD"}`,
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "markdown fence with json language tag",
			input:    "```json\n{\"action\": \"BUILD\"}\n```",
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "markdown fence uppercase JSON",
			input:    "```JSON\n{\"action\": \"BUILD\"}\n```",
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "markdown fence without language tag",
			input:    "```\n{\"action\": \"BUILD\"}\n```",
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "fence with preamble and postamble prose",
			input:    "Here is your result:\n```json\n{\"action\": \"BUILD\"}\n```\nLet me know if you need anything else!",
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "surrounding prose without backticks",
			input:    "Certainly! Output: {\"action\": \"BUILD\"} Enjoy playing.",
			expected: `{"action": "BUILD"}`,
		},
		{
			name:     "json array with fences",
			input:    "```json\n[{\"id\": 1}, {\"id\": 2}]\n```",
			expected: `[{"id": 1}, {"id": 2}]`,
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   \n\t  ",
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanJSONMarkdown(tc.input)
			if got != tc.expected {
				t.Errorf("CleanJSONMarkdown(%q)\n got:  %q\nwant: %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestClient_CompleteStructured_MarkdownFences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: "Here is your plan:\n```json\n{\n  \"action\": \"PATROL\",\n  \"target\": \"minecraft:village\",\n  \"quota\": 10\n}\n```\nStay safe!",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	intent, err := CompleteStructured[sampleIntent](context.Background(), client, []ChatMessage{
		{Role: RoleUser, Content: "Patrol the village"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if intent.Action != "PATROL" || intent.Target != "minecraft:village" || intent.Quota != 10 {
		t.Fatalf("unexpected intent unmarshaled: %+v", intent)
	}
}

func TestClient_CompleteStructured_RetryOnMalformedJSON(t *testing.T) {
	var callCount int32
	var secondCallMessages []ChatMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)

		var req ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if count == 1 {
			// First call returns broken JSON
			resp := ChatCompletionResponse{
				Choices: []ChatCompletionChoice{
					{
						Message: ChatMessage{
							Role:    RoleAssistant,
							Content: `{"action": "MINE", "target": "minecraft:iron_ore", quota: BROKEN`,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second call receives feedback and returns valid JSON
		secondCallMessages = req.Messages
		resp := ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: `{"action": "MINE", "target": "minecraft:iron_ore", "quota": 16}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	initialMessages := []ChatMessage{
		{Role: RoleUser, Content: "Mine some iron"},
	}

	intent, err := CompleteStructured[sampleIntent](context.Background(), client, initialMessages)
	if err != nil {
		t.Fatalf("expected successful recovery after retry, got: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("expected exactly 2 gateway calls, got %d", atomic.LoadInt32(&callCount))
	}

	if intent.Action != "MINE" || intent.Target != "minecraft:iron_ore" || intent.Quota != 16 {
		t.Fatalf("unexpected unmarshaled struct: %+v", intent)
	}

	// Verify the second call received the error feedback in history
	if len(secondCallMessages) < 3 {
		t.Fatalf("expected feedback messages in second call, got %d messages", len(secondCallMessages))
	}
	if secondCallMessages[1].Role != RoleAssistant {
		t.Errorf("expected assistant response in feedback history, got %s", secondCallMessages[1].Role)
	}
	if secondCallMessages[2].Role != RoleUser {
		t.Errorf("expected user error prompt in feedback history, got %s", secondCallMessages[2].Role)
	}

	// Original input slice must remain unmutated
	if len(initialMessages) != 1 {
		t.Fatalf("caller's initialMessages was mutated! len=%d", len(initialMessages))
	}
}

func TestClient_CompleteStructured_ExhaustedRetries(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		resp := ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: `This is completely unparseable plain prose without any JSON.`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
		WithMaxRetries(2),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	_, err = CompleteStructured[sampleIntent](context.Background(), client, []ChatMessage{
		{Role: RoleUser, Content: "Make a plan"},
	})

	if err == nil {
		t.Fatal("expected ErrInvalidJSONResponse when retries exhausted, got nil")
	}
	if !errors.Is(err, ErrInvalidJSONResponse) {
		t.Fatalf("expected ErrInvalidJSONResponse, got: %v", err)
	}

	// Initial attempt + 2 retries = 3 calls
	if atomic.LoadInt32(&callCount) != 3 {
		t.Fatalf("expected 3 total attempts, got %d", atomic.LoadInt32(&callCount))
	}
}

func TestClient_CompleteCreative(t *testing.T) {
	var capturedReq ChatCompletionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		resp := ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: "The dawn breaks over the iron spires of Valenreach as the bell tolls.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &xdg.GlobalConfig{
		LLMGateway: xdg.LLMGatewayConfig{
			BaseURL:               server.URL,
			Model:                 "llama3.1",
			CreativeTemperature:   0.92,
			StructuredTemperature: 0.15,
		},
		InferenceGovernor: xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 2,
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	reply, err := client.CompleteCreative(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "Describe the sunrise over the village."},
	})
	if err != nil {
		t.Fatalf("unexpected CompleteCreative error: %v", err)
	}

	if reply != "The dawn breaks over the iron spires of Valenreach as the bell tolls." {
		t.Fatalf("unexpected prose reply: %s", reply)
	}

	// Verify creative temperature was applied and response_format was NOT set
	if capturedReq.Temperature != 0.92 {
		t.Fatalf("expected creative temperature 0.92, got %v", capturedReq.Temperature)
	}
	if capturedReq.ResponseFormat != nil {
		t.Fatalf("creative completion should not set response_format, got: %+v", capturedReq.ResponseFormat)
	}
}

func TestClient_CreateEmbedding(t *testing.T) {
	var capturedReq EmbeddingRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		mockVec := make([]float32, 768)
		for i := range mockVec {
			mockVec[i] = float32(i) * 0.001
		}

		resp := EmbeddingResponse{
			Object: "list",
			Model:  capturedReq.Model,
			Data: []EmbeddingData{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: mockVec,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{
			BaseURL:        server.URL,
			EmbeddingModel: "nomic-embed-text",
		},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 2},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	vec, err := client.CreateEmbedding(context.Background(), "settler gathered 32 wheat")
	if err != nil {
		t.Fatalf("unexpected CreateEmbedding error: %v", err)
	}

	if len(vec) != 768 {
		t.Fatalf("expected 768 dimensions, got %d", len(vec))
	}
	if vec[1] != 0.001 {
		t.Fatalf("expected vec[1] = 0.001, got %v", vec[1])
	}
	if capturedReq.Input != "settler gathered 32 wheat" {
		t.Fatalf("unexpected input sent: %v", capturedReq.Input)
	}
}

func TestClient_ConcurrencyGovernor_LimitsRequests(t *testing.T) {
	const maxConcurrent = 2
	const totalRequests = 6

	var activeRequests int64
	var maxActiveObserved int64
	releaseCh := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&activeRequests, 1)

		// Record maximum concurrency observed on server
		for {
			oldMax := atomic.LoadInt64(&maxActiveObserved)
			if current <= oldMax || atomic.CompareAndSwapInt64(&maxActiveObserved, oldMax, current) {
				break
			}
		}

		// Hold request until test releases all
		<-releaseCh

		atomic.AddInt64(&activeRequests, -1)
		resp := ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{Message: ChatMessage{Role: RoleAssistant, Content: `{"action": "DONE"}`}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: maxConcurrent,
			RequestTimeoutSeconds:    10,
		},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	ctx := context.Background()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dest map[string]string
			_ = client.CompleteStructured(ctx, []ChatMessage{
				{Role: RoleUser, Content: "work"},
			}, &dest)
		}()
	}

	// Give goroutines time to saturate the governor
	time.Sleep(100 * time.Millisecond)

	// Release in-flight requests
	close(releaseCh)
	wg.Wait()

	if maxActiveObserved > int64(maxConcurrent) {
		t.Fatalf("server observed %d concurrent requests, exceeded limit %d", maxActiveObserved, maxConcurrent)
	}
}

func TestClient_GatewayTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 1,
			RequestTimeoutSeconds:    1, // 1 second default
		},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	// Use context with 50ms timeout to force timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.CompleteCreative(ctx, []ChatMessage{
		{Role: RoleUser, Content: "Hello"},
	})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrGatewayTimeout) {
		t.Fatalf("expected ErrGatewayTimeout, got: %v", err)
	}
}

func TestClient_GatewayUnreachable(t *testing.T) {
	// Pick an address where nothing is listening
	unreachableURL := "http://127.0.0.1:59998/v1"

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: unreachableURL},
		xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 1,
			RequestTimeoutSeconds:    1,
		},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	_, err = client.CompleteCreative(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "ping"},
	})

	if err == nil {
		t.Fatal("expected unreachable error, got nil")
	}
	if !errors.Is(err, ErrGatewayUnreachable) {
		t.Fatalf("expected ErrGatewayUnreachable, got: %v", err)
	}
}

func TestClient_HTTPStatusErrors(t *testing.T) {
	cases := []struct {
		statusCode     int
		body           string
		expectSentinel error
	}{
		{
			statusCode:     http.StatusUnauthorized,
			body:           `{"error": {"message": "Invalid API Key", "type": "auth_error"}}`,
			expectSentinel: nil,
		},
		{
			statusCode:     http.StatusNotFound,
			body:           `{"error": {"message": "Model not found", "type": "model_error"}}`,
			expectSentinel: nil,
		},
		{
			statusCode:     http.StatusInternalServerError,
			body:           `{"error": {"message": "GPU out of memory", "type": "server_error"}}`,
			expectSentinel: nil,
		},
		{
			statusCode:     http.StatusGatewayTimeout,
			body:           `{"error": {"message": "Upstream timed out"}}`,
			expectSentinel: ErrGatewayTimeout,
		},
		{
			statusCode:     http.StatusBadGateway,
			body:           `{"error": {"message": "Bad gateway"}}`,
			expectSentinel: ErrGatewayUnreachable,
		},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("Status_%d", tc.statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := NewClientWithConfig(
				xdg.LLMGatewayConfig{BaseURL: server.URL},
				xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
			)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}
			defer client.Close()

			_, err = client.CompleteCreative(context.Background(), []ChatMessage{
				{Role: RoleUser, Content: "test"},
			})

			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tc.statusCode)
			}

			if tc.expectSentinel != nil {
				if !errors.Is(err, tc.expectSentinel) {
					t.Fatalf("expected sentinel error %v, got: %v", tc.expectSentinel, err)
				}
			} else {
				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("expected *APIError, got: %v", err)
				}
				if apiErr.StatusCode != tc.statusCode {
					t.Fatalf("expected status code %d, got %d", tc.statusCode, apiErr.StatusCode)
				}
			}
		})
	}
}

func TestClient_ClientClosed(t *testing.T) {
	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: "http://127.0.0.1:11434"},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	ctx := context.Background()
	_, err = client.CompleteCreative(ctx, []ChatMessage{{Role: RoleUser, Content: "hi"}})
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("expected ErrClientClosed, got: %v", err)
	}

	var dummy map[string]string
	err = client.CompleteStructured(ctx, []ChatMessage{{Role: RoleUser, Content: "hi"}}, &dummy)
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("expected ErrClientClosed, got: %v", err)
	}

	_, err = client.CreateEmbedding(ctx, "text")
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("expected ErrClientClosed, got: %v", err)
	}
}

func TestClient_NilConfig(t *testing.T) {
	_, err := NewClient(nil)
	if !errors.Is(err, ErrNilConfig) {
		t.Fatalf("expected ErrNilConfig, got: %v", err)
	}
}

func TestClient_EmptyInput(t *testing.T) {
	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: "http://127.0.0.1:11434"},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	_, err = client.CompleteCreative(ctx, nil)
	if !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("expected ErrEmptyInput, got: %v", err)
	}

	var dummy map[string]string
	err = client.CompleteStructured(ctx, []ChatMessage{}, &dummy)
	if !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("expected ErrEmptyInput, got: %v", err)
	}

	_, err = client.CreateEmbedding(ctx, "")
	if err == nil {
		t.Fatal("expected error on empty embedding input, got nil")
	}
}
