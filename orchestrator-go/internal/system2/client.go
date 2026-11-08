package system2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const (
	// DefaultStructuredTemperature is the default sampling temperature for deterministic JSON output.
	DefaultStructuredTemperature = 0.20

	// DefaultCreativeTemperature is the default sampling temperature for dialogue and narrative lore.
	DefaultCreativeTemperature = 0.85

	// DefaultRequestTimeout is the fallback per-request timeout.
	DefaultRequestTimeout = 30 * time.Second

	// DefaultMaxRetries is the maximum number of validation retries for malformed JSON responses.
	DefaultMaxRetries = 2

	// MaxResponseBodyBytes limits response ingestion to 10MB to prevent heap exhaustion.
	MaxResponseBodyBytes int64 = 10 * 1024 * 1024

	// MaxFeedbackRawRunes limits raw LLM response echo in retry prompts.
	MaxFeedbackRawRunes = 1000
)

// Option defines a functional configuration option for Client.
type Option func(*Client)

// WithHTTPClient replaces the internal HTTP client with a custom instance (useful for tests).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithBaseURL overrides the gateway base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithMaxRetries configures the maximum validation retries on JSON unmarshal errors.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// WithQueueTimeout configures a queue timeout on the inference governor.
func WithQueueTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.governor != nil {
			c.governor.queueTimeout = d
		}
	}
}

// WithGovernor overrides the internal concurrency governor.
func WithGovernor(gov *Governor) Option {
	return func(c *Client) {
		if gov != nil {
			c.governor = gov
		}
	}
}

// WithStructuredTemperature configures the sampling temperature for structured JSON completions.
// A temperature of 0.0 enables deterministic greedy decoding.
func WithStructuredTemperature(t float64) Option {
	return func(c *Client) {
		if t >= 0 {
			c.structuredTemp = t
		}
	}
}

// WithCreativeTemperature configures the sampling temperature for creative dialogue and lore completions.
// A temperature of 0.0 enables deterministic greedy decoding.
func WithCreativeTemperature(t float64) Option {
	return func(c *Client) {
		if t >= 0 {
			c.creativeTemp = t
		}
	}
}

// Client is an OpenAI-compatible HTTP client for Ollama and vLLM gateways,
// featuring dual-temperature sampling, structured JSON validation with retries,
// connection pooling, and worker pool concurrency control.
type Client struct {
	httpClient     *http.Client
	baseURL        string
	apiKey         string
	model          string
	embeddingModel string
	structuredTemp float64
	creativeTemp   float64
	requestTimeout time.Duration
	governor       *Governor
	maxRetries     int
	mu             sync.RWMutex
	closed         bool
}

// NewClient initializes a Client from a GlobalConfig reference.
func NewClient(cfg *xdg.GlobalConfig, opts ...Option) (*Client, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}
	return NewClientWithConfig(cfg.LLMGateway, cfg.InferenceGovernor, opts...)
}

// NewClientWithConfig initializes a Client from explicit gateway and governor configurations.
func NewClientWithConfig(
	gatewayCfg xdg.LLMGatewayConfig,
	governorCfg xdg.InferenceGovernorConfig,
	opts ...Option,
) (*Client, error) {
	baseURL := strings.TrimRight(gatewayCfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434/v1"
	}

	model := gatewayCfg.Model
	if model == "" {
		model = "llama3.1"
	}

	embeddingModel := gatewayCfg.EmbeddingModel
	if embeddingModel == "" {
		embeddingModel = "nomic-embed-text"
	}

	structuredTemp := gatewayCfg.StructuredTemperature
	if structuredTemp <= 0 {
		structuredTemp = DefaultStructuredTemperature
	}

	creativeTemp := gatewayCfg.CreativeTemperature
	if creativeTemp <= 0 {
		creativeTemp = DefaultCreativeTemperature
	}

	timeoutSec := governorCfg.RequestTimeoutSeconds
	var requestTimeout time.Duration
	if timeoutSec <= 0 {
		requestTimeout = DefaultRequestTimeout
	} else {
		requestTimeout = time.Duration(timeoutSec) * time.Second
	}

	maxConcurrent := governorCfg.MaxConcurrentLLMRequests
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentLLMRequests
	}

	// Connection pooling configuration tailored for local LLM gateways
	maxIdleConnsPerHost := maxConcurrent * 2
	if maxIdleConnsPerHost < 10 {
		maxIdleConnsPerHost = 10
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
	}

	gov := NewGovernor(maxConcurrent, 0)

	c := &Client{
		httpClient:     httpClient,
		baseURL:        baseURL,
		apiKey:         gatewayCfg.APIKey,
		model:          model,
		embeddingModel: embeddingModel,
		structuredTemp: structuredTemp,
		creativeTemp:   creativeTemp,
		requestTimeout: requestTimeout,
		governor:       gov,
		maxRetries:     DefaultMaxRetries,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Governor returns the internal concurrency governor for monitoring or testing.
func (c *Client) Governor() *Governor {
	return c.governor
}

// HTTPClient returns the underlying http.Client.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// StructuredTemperature returns the configured temperature for structured completions.
func (c *Client) StructuredTemperature() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.structuredTemp
}

// CreativeTemperature returns the configured temperature for creative completions.
func (c *Client) CreativeTemperature() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.creativeTemp
}

// Close closes any idle HTTP connections and shuts down the concurrency governor.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	if c.httpClient != nil {
		if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	if c.governor != nil {
		return c.governor.Close()
	}
	return nil
}

// buildURL formats the endpoint path ensuring /v1 is appropriately prefixed.
func (c *Client) buildURL(endpoint string) string {
	base := strings.TrimRight(c.baseURL, "/")
	cleanEndpoint := "/" + strings.TrimLeft(endpoint, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + cleanEndpoint
	}
	return base + "/v1" + cleanEndpoint
}

// executeRequest performs an HTTP POST against the gateway, decoding JSON into respTarget.
func (c *Client) executeRequest(ctx context.Context, endpoint string, reqPayload any, respTarget any) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return ErrClientClosed
	}
	c.mu.RUnlock()

	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	fullURL := c.buildURL(endpoint)

	reqCtx := ctx
	var cancel context.CancelFunc
	if c.requestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, fullURL, bytes.NewReader(reqBytes))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return c.classifyNetworkError(ctx, reqCtx, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return c.handleHTTPError(resp)
	}

	if respTarget != nil {
		limited := io.LimitReader(resp.Body, MaxResponseBodyBytes)
		if err := json.NewDecoder(limited).Decode(respTarget); err != nil {
			if reqCtx.Err() != nil || ctx.Err() != nil {
				return c.classifyNetworkError(ctx, reqCtx, err)
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return c.classifyNetworkError(ctx, reqCtx, err)
			}
			return fmt.Errorf("%w: failed to decode response body: %v", ErrInvalidJSONResponse, err)
		}
	}
	return nil
}

// classifyNetworkError categorizes transport and context errors into sentinel errors.
func (c *Client) classifyNetworkError(parentCtx, reqCtx context.Context, err error) error {
	// 1. Caller explicit cancellation
	if parentCtx.Err() != nil && errors.Is(parentCtx.Err(), context.Canceled) {
		return parentCtx.Err()
	}

	// 2. Gateway timeout (parent deadline or per-request timeout exceeded)
	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) || os.IsTimeout(err) {
		return fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
	}

	// 3. Unreachable / dial failure
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
		}
		return fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
	}

	return fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
}

// handleHTTPError converts non-200 HTTP responses into *APIError or wrapped sentinel errors.
func (c *Client) handleHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Message:    strings.TrimSpace(string(body)),
	}

	var openAIErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &openAIErr); err == nil && openAIErr.Error.Message != "" {
		apiErr.Message = openAIErr.Error.Message
		apiErr.Type = openAIErr.Error.Type
		apiErr.Code = openAIErr.Error.Code
	} else {
		var ollamaErr struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &ollamaErr); err == nil && ollamaErr.Error != "" {
			apiErr.Message = ollamaErr.Error
		}
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %v", ErrWorkerPoolSaturated, apiErr)
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return fmt.Errorf("%w: %v", ErrGatewayTimeout, apiErr)
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %v", ErrGatewayUnreachable, apiErr)
	default:
		return apiErr
	}
}

// executeChatCompletion performs the raw chat completion request over HTTP.
func (c *Client) executeChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	var resp ChatCompletionResponse
	if err := c.executeRequest(ctx, "/chat/completions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ChatCompletion executes a raw ChatCompletionRequest bounded by the concurrency governor.
func (c *Client) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, ErrClientClosed
	}
	c.mu.RUnlock()

	if req.Model == "" {
		req.Model = c.model
	}

	if err := c.governor.Acquire(ctx); err != nil {
		return nil, err
	}
	defer c.governor.Release()

	return c.executeChatCompletion(ctx, req)
}

// CompleteStructured executes a structured JSON chat completion, unmarshaling the result into dest.
// dest must be a non-nil pointer (e.g. &myStruct).
//
// It uses StructuredTemperature (~0.20), sets response_format: {"type": "json_object"},
// strips markdown fences, and retries up to 2 times on validation error by feeding back
// the syntax or unmarshal error to the LLM.
// CompleteStructured executes a structured JSON chat completion with retries, unmarshaling the result into dest.
func (c *Client) CompleteStructured(ctx context.Context, messages []ChatMessage, dest any) error {
	c.mu.RLock()
	temp := c.structuredTemp
	c.mu.RUnlock()
	return c.CompleteStructuredWithTemp(ctx, messages, dest, temp)
}

// CompleteStructuredWithTemp executes a structured JSON chat completion with an explicit sampling temperature.
func (c *Client) CompleteStructuredWithTemp(ctx context.Context, messages []ChatMessage, dest any, temperature float64) error {
	if len(messages) == 0 {
		return ErrEmptyInput
	}
	if dest == nil {
		return ErrInvalidDestination
	}

	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Pointer || destVal.IsNil() {
		return ErrInvalidDestination
	}

	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return ErrClientClosed
	}
	c.mu.RUnlock()

	// Acquire a worker slot for the entire structured completion and retry cycle
	if err := c.governor.Acquire(ctx); err != nil {
		return err
	}
	defer c.governor.Release()

	// Defensive copy to append retry feedback without mutating caller's slice
	history := make([]ChatMessage, len(messages))
	copy(history, messages)

	maxRetries := c.maxRetries
	if maxRetries < 0 {
		maxRetries = DefaultMaxRetries
	}

	var lastErr error
	var lastRaw string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		req := ChatCompletionRequest{
			Model:          c.model,
			Messages:       history,
			Temperature:    temperature,
			ResponseFormat: &ResponseFormat{Type: "json_object"},
		}

		resp, err := c.executeChatCompletion(ctx, req)
		if err != nil {
			return err
		}

		if len(resp.Choices) == 0 {
			lastErr = errors.New("gateway returned no completion choices")
			continue
		}

		rawContent := resp.Choices[0].Message.Content
		lastRaw = rawContent

		cleaned := CleanJSONMarkdown(rawContent)

		// Unmarshal into a temporary value first to avoid corrupting dest on failed attempts
		temp := reflect.New(destVal.Elem().Type()).Interface()
		if err := json.Unmarshal([]byte(cleaned), temp); err == nil {
			destVal.Elem().Set(reflect.ValueOf(temp).Elem())
			return nil
		} else {
			lastErr = err
		}

		// Truncate rawContent to MaxFeedbackRawRunes to avoid prompt blowup
		feedbackContent := rawContent
		runes := []rune(rawContent)
		if len(runes) > MaxFeedbackRawRunes {
			feedbackContent = string(runes[:MaxFeedbackRawRunes]) + "... [truncated]"
		}

		feedbackPrompt := fmt.Sprintf(
			"Your previous response was not valid JSON conforming to the required schema: %v. Please return ONLY a valid, raw JSON object matching the requested schema without markdown fences or extraneous prose.",
			lastErr,
		)
		if resp.Choices[0].FinishReason == "length" {
			feedbackPrompt = "Your previous response was cut off because it exceeded the maximum token length. Please provide a more concise and brief JSON response conforming to the required schema without unnecessary verbose fields, markdown fences, or extraneous prose."
		}

		// Validation failed: feedback error to LLM for next attempt
		history = append(history,
			ChatMessage{
				Role:    RoleAssistant,
				Content: feedbackContent,
			},
			ChatMessage{
				Role:    RoleUser,
				Content: feedbackPrompt,
			},
		)
	}

	return fmt.Errorf("%w: failed to parse valid JSON after %d retries: %v (last raw response: %q)",
		ErrInvalidJSONResponse, maxRetries, lastErr, lastRaw)
}

// CompleteStructuredJSON is an alias for CompleteStructured.
func (c *Client) CompleteStructuredJSON(ctx context.Context, messages []ChatMessage, dest any) error {
	return c.CompleteStructured(ctx, messages, dest)
}

// CompleteStructured executes a structured JSON chat completion on the given client, returning typed T.
func CompleteStructured[T any](ctx context.Context, c *Client, messages []ChatMessage) (T, error) {
	var result T
	if c == nil {
		return result, ErrNilConfig
	}
	if err := c.CompleteStructured(ctx, messages, &result); err != nil {
		return result, err
	}
	return result, nil
}

// CompleteStructuredWithClient is a helper allowing client-first argument ordering.
func CompleteStructuredWithClient[T any](c *Client, ctx context.Context, messages []ChatMessage) (T, error) {
	return CompleteStructured[T](ctx, c, messages)
}

// CompleteCreative executes a creative completion (dialogue, chronicler books, lore)
// using CreativeTemperature (~0.85) without JSON schema restrictions.
func (c *Client) CompleteCreative(ctx context.Context, messages []ChatMessage) (string, error) {
	if len(messages) == 0 {
		return "", ErrEmptyInput
	}

	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return "", ErrClientClosed
	}
	c.mu.RUnlock()

	if err := c.governor.Acquire(ctx); err != nil {
		return "", err
	}
	defer c.governor.Release()

	req := ChatCompletionRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: c.creativeTemp,
	}

	resp, err := c.executeChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%w: no completion choices returned", ErrInvalidJSONResponse)
	}

	return resp.Choices[0].Message.Content, nil
}

// CreateEmbedding generates a 768-dimensional vector embedding for the input text
// via the /v1/embeddings endpoint.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}

	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, ErrClientClosed
	}
	c.mu.RUnlock()

	if err := c.governor.Acquire(ctx); err != nil {
		return nil, err
	}
	defer c.governor.Release()

	req := EmbeddingRequest{
		Model: c.embeddingModel,
		Input: input,
	}

	var resp EmbeddingResponse
	if err := c.executeRequest(ctx, "/embeddings", req, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("%w: empty embedding data in gateway response", ErrInvalidJSONResponse)
	}

	return resp.Data[0].Embedding, nil
}

// CleanJSONMarkdown strips markdown code fences (e.g. ```json ... ```) and
// extracts the JSON payload from surrounding prose if present.
func CleanJSONMarkdown(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}

	// 1. Strip triple-backtick markdown blocks if present
	if startIdx := strings.Index(trimmed, "```"); startIdx != -1 {
		rest := trimmed[startIdx+3:]
		newlineIdx := strings.IndexByte(rest, '\n')
		var body string
		if newlineIdx != -1 {
			body = rest[newlineIdx+1:]
		} else {
			body = strings.TrimLeft(rest, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
		}

		if endIdx := strings.LastIndex(body, "```"); endIdx != -1 {
			body = body[:endIdx]
		}
		trimmed = strings.TrimSpace(body)
	}

	// 2. Direct validation: if already valid JSON, return it
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}

	// 3. Scan for candidate outermost balanced {...} and [...]
	candObj, okObj := findBalancedJSON(trimmed, '{', '}')
	candArr, okArr := findBalancedJSON(trimmed, '[', ']')

	if okObj && okArr {
		if len(candArr) > len(candObj) {
			return candArr
		}
		return candObj
	}
	if okObj {
		return candObj
	}
	if okArr {
		return candArr
	}

	return trimmed
}

// findBalancedJSON scans s for the candidate outermost balanced JSON delimited by open and close bytes.
func findBalancedJSON(s string, open, close byte) (string, bool) {
	var best string
	for start := 0; start < len(s); start++ {
		if s[start] != open {
			continue
		}
		depth := 0
		inString := false
		escaped := false
		for i := start; i < len(s); i++ {
			c := s[i]
			if inString {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
				continue
			}

			if c == '"' {
				inString = true
				continue
			}

			if c == open {
				depth++
			} else if c == close {
				depth--
				if depth == 0 {
					candidate := s[start : i+1]
					if json.Valid([]byte(candidate)) {
						if len(candidate) > len(best) {
							best = candidate
						}
					}
					break
				}
			}
		}
	}
	if len(best) > 0 {
		return best, true
	}
	return "", false
}
