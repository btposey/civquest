package bridge

import (
	"context"
	"fmt"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// Client wraps the gRPC connection and service clients for CivQuestBridgeService and standard Health checks.
type Client struct {
	conn    *grpc.ClientConn
	bridge  civquestv1.CivQuestBridgeServiceClient
	health  grpc_health_v1.HealthClient
	hasAuth bool
	target  string
}

// FormatBearerToken ensures the token is prefixed with "Bearer ".
func FormatBearerToken(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		return trimmed
	}
	return "Bearer " + trimmed
}

// UnaryAuthInterceptor attaches the HTTP authorization Bearer header to unary RPC requests.
func UnaryAuthInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamAuthInterceptor attaches the HTTP authorization Bearer header to streaming RPC requests.
func StreamAuthInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// NewClient dials target and returns an initialized CivQuest bridge Client.
// When sharedKey is non-empty, gRPC client interceptors automatically inject authorization Bearer headers.
func NewClient(target string, sharedKey string, opts ...grpc.DialOption) (*Client, error) {
	var dialOpts []grpc.DialOption

	// Insecure credentials by default (caller can override via opts)
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	if sharedKey != "" {
		token := FormatBearerToken(sharedKey)
		dialOpts = append(dialOpts,
			grpc.WithChainUnaryInterceptor(UnaryAuthInterceptor(token)),
			grpc.WithChainStreamInterceptor(StreamAuthInterceptor(token)),
		)
	}

	dialOpts = append(dialOpts, opts...)

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for %s: %w", target, err)
	}

	return &Client{
		conn:    conn,
		bridge:  civquestv1.NewCivQuestBridgeServiceClient(conn),
		health:  grpc_health_v1.NewHealthClient(conn),
		hasAuth: sharedKey != "",
		target:  target,
	}, nil
}

// HealthCheck queries grpc.health.v1.Health/Check.
// If service is empty or omitted, checks overall server health.
func (c *Client) HealthCheck(ctx context.Context, service ...string) (*grpc_health_v1.HealthCheckResponse, error) {
	svc := ""
	if len(service) > 0 {
		svc = service[0]
	}
	return c.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: svc,
	})
}

// SpawnAgentEntity invokes SpawnAgentEntity RPC on the CivQuest bridge.
func (c *Client) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest, opts ...grpc.CallOption) (*civquestv1.SpawnAgentResponse, error) {
	return c.bridge.SpawnAgentEntity(ctx, req, opts...)
}

// DespawnAgentEntity invokes DespawnAgentEntity RPC on the CivQuest bridge.
func (c *Client) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest, opts ...grpc.CallOption) (*civquestv1.DespawnAgentResponse, error) {
	return c.bridge.DespawnAgentEntity(ctx, req, opts...)
}

// ConnectAgent establishes the bidirectional agent telemetry and intent stream.
func (c *Client) ConnectAgent(ctx context.Context, opts ...grpc.CallOption) (civquestv1.CivQuestBridgeService_ConnectAgentClient, error) {
	return c.bridge.ConnectAgent(ctx, opts...)
}

// ExportRecipeGraph requests the server recipe graph.
func (c *Client) ExportRecipeGraph(ctx context.Context, req *civquestv1.ExportRecipeRequest, opts ...grpc.CallOption) (*civquestv1.ExportRecipeResponse, error) {
	return c.bridge.ExportRecipeGraph(ctx, req, opts...)
}

// Conn returns the underlying gRPC ClientConn.
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// BridgeClient returns the raw CivQuestBridgeServiceClient interface.
func (c *Client) BridgeClient() civquestv1.CivQuestBridgeServiceClient {
	return c.bridge
}

// HealthClient returns the raw HealthClient interface.
func (c *Client) HealthClient() grpc_health_v1.HealthClient {
	return c.health
}

// HasAuth returns true if the client was initialized with a non-empty shared key.
func (c *Client) HasAuth() bool {
	return c.hasAuth
}

// Target returns the connection target string.
func (c *Client) Target() string {
	return c.target
}

// String implements fmt.Stringer to prevent credential leakage into logs.
func (c *Client) String() string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Client{target: %q, hasAuth: %t}", c.target, c.hasAuth)
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
