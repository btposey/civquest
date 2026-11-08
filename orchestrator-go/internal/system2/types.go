package system2

import (
	"errors"
	"fmt"
)

// Common Chat Completion roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Sentinel errors for System 2 LLM operations.
var (
	// ErrGatewayTimeout is returned when an inference request to the gateway exceeds its deadline.
	ErrGatewayTimeout = errors.New("llm gateway request timed out")

	// ErrWorkerPoolSaturated is returned when the concurrency governor cannot grant a slot.
	ErrWorkerPoolSaturated = errors.New("llm worker pool saturated")

	// ErrInvalidJSONResponse is returned when the model response cannot be parsed into the expected JSON schema.
	ErrInvalidJSONResponse = errors.New("invalid json response from llm")

	// ErrGatewayUnreachable is returned when the LLM gateway server cannot be contacted.
	ErrGatewayUnreachable = errors.New("llm gateway unreachable")

	// ErrClientClosed is returned when an operation is attempted on a closed client or governor.
	ErrClientClosed = errors.New("llm client is closed")

	// ErrNilConfig is returned when a nil configuration is passed to the constructor.
	ErrNilConfig = errors.New("configuration cannot be nil")

	// ErrEmptyInput is returned when no input messages are provided.
	ErrEmptyInput = errors.New("input messages cannot be empty")

	// ErrInvalidDestination is returned when the target struct pointer is nil or not a valid pointer.
	ErrInvalidDestination = errors.New("destination target must be a non-nil pointer")
)

// APIError represents an HTTP error response returned by an OpenAI-compatible gateway.
type APIError struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("gateway API error (status %d, type %s): %s", e.StatusCode, e.Type, e.Message)
	}
	return fmt.Sprintf("gateway API error (status %d): %s", e.StatusCode, e.Message)
}

// ChatMessage represents a single message turn in a chat completion prompt.
type ChatMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Name             string `json:"name,omitempty"`
}

// ResponseFormat specifies the schema format constraint for chat completions.
type ResponseFormat struct {
	Type string `json:"type"` // e.g. "json_object"
}

// ChatCompletionRequest represents an OpenAI-compatible /v1/chat/completions payload.
type ChatCompletionRequest struct {
	Model              string          `json:"model"`
	Messages           []ChatMessage   `json:"messages"`
	Temperature        float64         `json:"temperature"`
	MaxTokens          int             `json:"max_tokens,omitempty"`
	ReasoningBudget    *int            `json:"reasoning_budget,omitempty"`
	ResponseFormat     *ResponseFormat `json:"response_format,omitempty"`
	ChatTemplateKwargs map[string]any  `json:"chat_template_kwargs,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
}

// ChatCompletionChoice represents an individual choice in a chat completion response.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatCompletionUsage reports token consumption details.
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionResponse represents an OpenAI-compatible chat completion response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

// EmbeddingRequest represents an OpenAI-compatible /v1/embeddings payload.
type EmbeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

// EmbeddingData represents a single vector embedding in an embeddings response.
type EmbeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingResponse represents an OpenAI-compatible embeddings response.
type EmbeddingResponse struct {
	Object string              `json:"object"`
	Data   []EmbeddingData     `json:"data"`
	Model  string              `json:"model"`
	Usage  ChatCompletionUsage `json:"usage"`
}
