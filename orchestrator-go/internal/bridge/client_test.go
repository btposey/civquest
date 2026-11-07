package bridge_test

import (
	"context"
	"crypto/subtle"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// mockBridgeServer implements civquestv1.CivQuestBridgeServiceServer for testing.
type mockBridgeServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer
}

func (s *mockBridgeServer) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	return &civquestv1.SpawnAgentResponse{
		Success:           true,
		MinecraftEntityId: 1001,
		OfflineUuid:       "00000000-0000-0000-0000-000000000042",
	}, nil
}

func (s *mockBridgeServer) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest) (*civquestv1.DespawnAgentResponse, error) {
	return &civquestv1.DespawnAgentResponse{
		Success: true,
	}, nil
}

func (s *mockBridgeServer) ExportRecipeGraph(ctx context.Context, req *civquestv1.ExportRecipeRequest) (*civquestv1.ExportRecipeResponse, error) {
	return &civquestv1.ExportRecipeResponse{
		Recipes: []*civquestv1.RecipeNode{
			{RecipeId: "minecraft:iron_sword"},
		},
	}, nil
}

func (s *mockBridgeServer) ConnectAgent(stream civquestv1.CivQuestBridgeService_ConnectAgentServer) error {
	// Send baseline initial TelemetryFrame
	initialFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
		},
	}
	if err := stream.Send(initialFrame); err != nil {
		return err
	}

	// Read incoming intents until client closes
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// mockAuthUnaryInterceptor mimics Java SharedKeyAuthInterceptor for unary RPCs.
func mockAuthUnaryInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Exempt health check unary call per ADR-012
		if info.FullMethod == "/grpc.health.v1.Health/Check" || strings.HasSuffix(info.FullMethod, "Health/Check") {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		authHeader := md.Get("authorization")
		if len(authHeader) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		val := strings.TrimSpace(authHeader[0])
		if !strings.HasPrefix(strings.ToLower(val), "bearer ") {
			return nil, status.Error(codes.Unauthenticated, "missing Bearer prefix")
		}
		token := strings.TrimSpace(val[7:])
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid shared key")
		}

		return handler(ctx, req)
	}
}

// mockAuthStreamInterceptor mimics Java SharedKeyAuthInterceptor for streaming RPCs.
func mockAuthStreamInterceptor(expectedKey string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}
		authHeader := md.Get("authorization")
		if len(authHeader) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		val := strings.TrimSpace(authHeader[0])
		if !strings.HasPrefix(strings.ToLower(val), "bearer ") {
			return status.Error(codes.Unauthenticated, "missing Bearer prefix")
		}
		token := strings.TrimSpace(val[7:])
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			return status.Error(codes.Unauthenticated, "invalid shared key")
		}

		return handler(srv, ss)
	}
}

type testServerSetup struct {
	lis        *bufconn.Listener
	grpcServer *grpc.Server
	sharedKey  string
}

func startTestServer(t *testing.T, sharedKey string) *testServerSetup {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(mockAuthUnaryInterceptor(sharedKey)),
		grpc.StreamInterceptor(mockAuthStreamInterceptor(sharedKey)),
	)

	// Health service
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("civquest.v1.CivQuestBridgeService", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)

	// Bridge service
	civquestv1.RegisterCivQuestBridgeServiceServer(srv, &mockBridgeServer{})

	go func() {
		if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("Server error: %v", err)
		}
	}()

	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})

	return &testServerSetup{
		lis:        lis,
		grpcServer: srv,
		sharedKey:  sharedKey,
	}
}

func (s *testServerSetup) dialer() func(context.Context, string) (net.Conn, error) {
	return func(context.Context, string) (net.Conn, error) {
		return s.lis.Dial()
	}
}

func TestFormatBearerToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		{"my-token", "Bearer my-token"},
		{"Bearer my-token", "Bearer my-token"},
		{"bearer my-token", "bearer my-token"},
		{"BEARER my-token", "BEARER my-token"},
		{"  Bearer token-123  ", "Bearer token-123"},
	}

	for _, tc := range tests {
		actual := bridge.FormatBearerToken(tc.input)
		if actual != tc.expected {
			t.Errorf("FormatBearerToken(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}

func TestClientUnauthenticated(t *testing.T) {
	setup := startTestServer(t, "secret-key-123")

	client, err := bridge.NewClient(
		"passthrough://bufnet",
		"", // No shared key
		grpc.WithContextDialer(setup.dialer()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Health check should succeed without token (ADR-012 readiness probe exemption)
	healthResp, err := client.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("Expected unauthenticated health check to succeed, got: %v", err)
	}
	if healthResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("Expected SERVING, got %v", healthResp.GetStatus())
	}

	// CheckHealth alias
	checkResp, err := client.CheckHealth(ctx, "")
	if err != nil || checkResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("CheckHealth alias failed: %v", err)
	}

	// 2. SpawnAgentEntity should fail with Unauthenticated
	_, err = client.SpawnAgent(ctx, &civquestv1.SpawnAgentRequest{AgentId: "settler-1"})
	if err == nil {
		t.Fatalf("Expected unauthenticated SpawnAgent to fail")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Errorf("Expected codes.Unauthenticated, got status %v (err: %v)", st.Code(), err)
	}

	// 3. Connect stream should fail with Unauthenticated
	stream, err := client.Connect(ctx)
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unauthenticated {
			t.Errorf("Expected codes.Unauthenticated, got status %v (err: %v)", st.Code(), err)
		}
	} else {
		// If stream created lazily, recv should fail with Unauthenticated
		_, err := stream.Recv()
		if err == nil {
			t.Fatalf("Expected stream.Recv() without auth to fail")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unauthenticated {
			t.Errorf("Expected codes.Unauthenticated on stream, got status %v (err: %v)", st.Code(), err)
		}
	}
}

func TestClientInvalidSharedKey(t *testing.T) {
	setup := startTestServer(t, "correct-secret-key")

	client, err := bridge.NewClient(
		"passthrough://bufnet",
		"wrong-secret-key",
		grpc.WithContextDialer(setup.dialer()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.SpawnAgentEntity(ctx, &civquestv1.SpawnAgentRequest{AgentId: "agent-1"})
	if err == nil {
		t.Fatalf("Expected SpawnAgent with invalid key to fail")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Errorf("Expected codes.Unauthenticated, got %v (err: %v)", st.Code(), err)
	}
}

func TestClientAuthenticatedFullLifecycle(t *testing.T) {
	sharedKey := "probe-secret-key-456"
	setup := startTestServer(t, sharedKey)

	client, err := bridge.NewClient(
		"passthrough://bufnet",
		sharedKey,
		grpc.WithContextDialer(setup.dialer()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	if client.SharedKey() != sharedKey {
		t.Errorf("Expected SharedKey %s, got %s", sharedKey, client.SharedKey())
	}
	if client.Target() != "passthrough://bufnet" {
		t.Errorf("Expected Target passthrough://bufnet, got %s", client.Target())
	}
	if client.Conn() == nil {
		t.Errorf("Conn() should not be nil")
	}
	if client.BridgeClient() == nil {
		t.Errorf("BridgeClient() should not be nil")
	}
	if client.HealthClient() == nil {
		t.Errorf("HealthClient() should not be nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Health check
	healthResp, err := client.HealthCheck(ctx, "civquest.v1.CivQuestBridgeService")
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if healthResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("Expected SERVING, got: %v", healthResp.GetStatus())
	}

	// 2. SpawnAgent / SpawnAgentEntity
	spawnResp, err := client.SpawnAgent(ctx, &civquestv1.SpawnAgentRequest{
		AgentId:     "settler-alpha",
		DisplayName: "Alpha",
		ClanId:      "clan-1",
	})
	if err != nil {
		t.Fatalf("SpawnAgent failed: %v", err)
	}
	if !spawnResp.GetSuccess() {
		t.Errorf("Expected success true, got false")
	}
	if spawnResp.GetMinecraftEntityId() != 1001 {
		t.Errorf("Expected entity ID 1001, got %d", spawnResp.GetMinecraftEntityId())
	}
	if spawnResp.GetOfflineUuid() != "00000000-0000-0000-0000-000000000042" {
		t.Errorf("Unexpected UUID: %s", spawnResp.GetOfflineUuid())
	}

	// 3. Connect / ConnectAgent bidirectional stream
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("Failed to receive telemetry frame: %v", err)
	}
	if frame.GetStatus().GetHealth() != 20.0 {
		t.Errorf("Expected health 20.0, got: %f", frame.GetStatus().GetHealth())
	}

	// Send AgentIntent
	intent := &civquestv1.AgentIntent{
		AgentId:    "settler-alpha",
		SequenceId: 1,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:          &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
				SpeedMultiplier: 1.0,
			},
		},
	}
	if err := stream.Send(intent); err != nil {
		t.Fatalf("Failed sending intent: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("Failed closing send stream: %v", err)
	}

	// ConnectAgent alias test
	stream2, err := client.ConnectAgent(ctx)
	if err != nil {
		t.Fatalf("ConnectAgent alias failed: %v", err)
	}
	if _, err := stream2.Recv(); err != nil {
		t.Fatalf("stream2.Recv failed: %v", err)
	}
	_ = stream2.CloseSend()

	// 4. DespawnAgent / DespawnAgentEntity
	despawnResp, err := client.DespawnAgent(ctx, &civquestv1.DespawnAgentRequest{
		AgentId: "settler-alpha",
	})
	if err != nil {
		t.Fatalf("DespawnAgent failed: %v", err)
	}
	if !despawnResp.GetSuccess() {
		t.Errorf("Expected despawn success true, got false")
	}

	despawnResp2, err := client.DespawnAgentEntity(ctx, &civquestv1.DespawnAgentRequest{
		AgentId: "settler-alpha",
	})
	if err != nil || !despawnResp2.GetSuccess() {
		t.Fatalf("DespawnAgentEntity alias failed: %v", err)
	}

	// 5. ExportRecipeGraph
	recipeResp, err := client.ExportRecipeGraph(ctx, &civquestv1.ExportRecipeRequest{})
	if err != nil {
		t.Fatalf("ExportRecipeGraph failed: %v", err)
	}
	if len(recipeResp.GetRecipes()) != 1 || recipeResp.GetRecipes()[0].GetRecipeId() != "minecraft:iron_sword" {
		t.Errorf("Unexpected recipes response: %+v", recipeResp.GetRecipes())
	}
}

func TestInterceptorsDirectly(t *testing.T) {
	// 1. UnaryAuthInterceptor
	interceptor := bridge.UnaryAuthInterceptor("Bearer test-token")
	var capturedMD metadata.MD
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if ok {
			capturedMD = md
		}
		return nil
	}

	err := interceptor(context.Background(), "/test.Service/Method", nil, nil, nil, invoker)
	if err != nil {
		t.Fatalf("UnaryAuthInterceptor returned error: %v", err)
	}
	if auths := capturedMD.Get("authorization"); len(auths) == 0 || auths[0] != "Bearer test-token" {
		t.Errorf("Expected Bearer test-token metadata, got: %v", auths)
	}

	// 2. StreamAuthInterceptor
	streamInterceptor := bridge.StreamAuthInterceptor("Bearer stream-token")
	var capturedStreamMD metadata.MD
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if ok {
			capturedStreamMD = md
		}
		return nil, nil
	}

	_, err = streamInterceptor(context.Background(), &grpc.StreamDesc{}, nil, "/test.Service/Stream", streamer)
	if err != nil {
		t.Fatalf("StreamAuthInterceptor returned error: %v", err)
	}
	if auths := capturedStreamMD.Get("authorization"); len(auths) == 0 || auths[0] != "Bearer stream-token" {
		t.Errorf("Expected Bearer stream-token metadata, got: %v", auths)
	}
}
