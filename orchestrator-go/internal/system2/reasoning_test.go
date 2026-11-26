package system2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-yn1.10: structured requests must suppress the model's thought block, or it consumes the
// token limit and the reply comes back empty.
func TestStructuredRequestSuppressesReasoning(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{Choices: []ChatCompletionChoice{{
			Message: ChatMessage{Role: RoleAssistant, Content: `{"ok":true}`}, FinishReason: "stop",
		}}})
	}))
	defer srv.Close()
	c, err := NewClient(&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: srv.URL + "/v1", Model: "m"}},
		WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out struct{ OK bool }
	if err := c.CompleteStructured(context.Background(), []ChatMessage{{Role: RoleUser, Content: "hi"}}, &out); err != nil {
		t.Fatal(err)
	}
	if body["reasoning_format"] != "none" {
		t.Errorf("reasoning_format = %v, want none", body["reasoning_format"])
	}
	kwargs, _ := body["chat_template_kwargs"].(map[string]any)
	if kwargs["enable_thinking"] != false {
		t.Errorf("enable_thinking = %v, want false", kwargs["enable_thinking"])
	}
}
