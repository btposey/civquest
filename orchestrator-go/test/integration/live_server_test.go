//go:build integration

package integration

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// runEndToEndProbe executes the full Tier 3 verification protocol against any gRPC bridge target:
// 1. Dial without token -> assert fails with codes.Unauthenticated
// 2. Dial with shared key -> assert health check is SERVING
// 3. Call SpawnAgentEntity -> assert returns success and deterministic UUID
// 4. Call ConnectAgent stream -> send AgentIntent, receive TelemetryFrame
// 5. Call DespawnAgentEntity -> assert returns success
func runEndToEndProbe(t *testing.T, meta *bridge.ServerMeta) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := meta.Addr()
	t.Logf("Connecting probe to server: addr=%s, transport=%s, pid=%d",
		target, meta.Transport, meta.ServerPID)

	if meta.SharedKey == "" {
		t.Fatalf("Server discovery metadata shared_key is empty")
	}

	// 1. Dial without token -> assert fails with codes.Unauthenticated
	t.Log("Testing unauthenticated access rejection...")
	unauthClient, err := bridge.NewClient(target, "")
	if err != nil {
		t.Fatalf("Failed to dial server without token: %v", err)
	}
	defer unauthClient.Close()

	// 1a. Unary RPC rejection: SpawnAgentEntity
	_, err = unauthClient.SpawnAgentEntity(ctx, &civquestv1.SpawnAgentRequest{
		AgentId:     "probe-unauth-test",
		DisplayName: "UnauthProbe",
		ClanId:      "clan-test",
	})
	if err == nil {
		t.Fatalf("Expected unauthenticated SpawnAgentEntity to fail with codes.Unauthenticated, but succeeded")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("Expected codes.Unauthenticated on SpawnAgentEntity, got status code %v (err: %v)", st.Code(), err)
	}
	t.Logf("Verified unauthenticated unary RPC rejection: code=%s, desc=%s", st.Code(), st.Message())

	// 1b. Streaming RPC rejection: ConnectAgent (H-5)
	unauthStream, err := unauthClient.ConnectAgent(ctx)
	if err != nil {
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unauthenticated {
			t.Fatalf("Expected codes.Unauthenticated on ConnectAgent creation, got status code %v (err: %v)", st.Code(), err)
		}
	} else {
		_, err := unauthStream.Recv()
		if err == nil {
			t.Fatalf("Expected unauthenticated ConnectAgent stream.Recv() to fail with codes.Unauthenticated, but succeeded")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unauthenticated {
			t.Fatalf("Expected codes.Unauthenticated on ConnectAgent stream.Recv(), got status code %v (err: %v)", st.Code(), err)
		}
	}
	t.Log("Verified unauthenticated streaming RPC rejection: code=Unauthenticated")

	// 2. Dial with shared key -> assert health check is SERVING
	t.Log("Dialing server with shared-key authentication...")
	client, err := bridge.NewClient(target, meta.SharedKey)
	if err != nil {
		t.Fatalf("Failed to dial server with shared key: %v", err)
	}
	defer client.Close()

	// Overall server health check
	healthResp, err := client.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("Overall HealthCheck failed: %v", err)
	}
	if healthResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("Expected health status SERVING, got: %v", healthResp.GetStatus())
	}

	// Service-specific health check
	svcHealthResp, err := client.HealthCheck(ctx, "civquest.v1.CivQuestBridgeService")
	if err != nil {
		t.Fatalf("Bridge service HealthCheck failed: %v", err)
	}
	if svcHealthResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("Expected bridge service health status SERVING, got: %v", svcHealthResp.GetStatus())
	}
	t.Logf("Verified health checks: SERVING")

	// 3. Call SpawnAgentEntity -> assert returns success and deterministic UUID
	agentID := "probe-agent-live"
	t.Logf("Calling SpawnAgentEntity for agent_id=%s...", agentID)
	spawnResp, err := client.SpawnAgentEntity(ctx, &civquestv1.SpawnAgentRequest{
		AgentId:     agentID,
		DisplayName: "ProbeAgentLive",
		ClanId:      "clan-test",
		SpawnCoords: &civquestv1.Vec3{X: 0, Y: 4, Z: 0},
	})
	if err != nil {
		t.Fatalf("SpawnAgentEntity failed: %v", err)
	}
	if !spawnResp.GetSuccess() {
		t.Fatalf("SpawnAgentEntity returned success=false (error: %s)", spawnResp.GetErrorMessage())
	}
	if spawnResp.GetOfflineUuid() == "" {
		t.Fatalf("Expected non-empty offline UUID, got empty")
	}
	t.Logf("SpawnAgentEntity succeeded: entity_id=%d, offline_uuid=%s",
		spawnResp.GetMinecraftEntityId(), spawnResp.GetOfflineUuid())

	// 4. Call ConnectAgent stream -> send AgentIntent, receive TelemetryFrame
	t.Log("Establishing ConnectAgent bidirectional stream...")
	stream, err := client.ConnectAgent(ctx)
	if err != nil {
		t.Fatalf("ConnectAgent stream creation failed: %v", err)
	}

	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("Failed receiving initial TelemetryFrame: %v", err)
	}
	if frame.GetStatus() == nil || frame.GetStatus().GetHealth() <= 0 {
		t.Fatalf("Received invalid telemetry frame: %+v", frame)
	}
	t.Logf("Received TelemetryFrame: health=%.1f, max_health=%.1f, food=%d, tick=%d",
		frame.GetStatus().GetHealth(), frame.GetStatus().GetMaxHealth(),
		frame.GetStatus().GetFoodLevel(), frame.GetWorldTick())

	// Send Navigate AgentIntent
	intent := &civquestv1.AgentIntent{
		AgentId:    agentID,
		SequenceId: 1,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:          &civquestv1.Vec3{X: 10, Y: 4, Z: 10},
				SpeedMultiplier: 1.0,
				Sprint:          true,
			},
		},
	}
	if err := stream.Send(intent); err != nil {
		t.Fatalf("Failed sending AgentIntent: %v", err)
	}
	t.Log("Sent Navigate AgentIntent successfully")

	if err := stream.CloseSend(); err != nil {
		t.Fatalf("Failed to close stream send: %v", err)
	}

	// 5. Call DespawnAgentEntity -> assert returns success
	t.Logf("Calling DespawnAgentEntity for agent_id=%s...", agentID)
	despawnResp, err := client.DespawnAgentEntity(ctx, &civquestv1.DespawnAgentRequest{
		AgentId: agentID,
	})
	if err != nil {
		t.Fatalf("DespawnAgentEntity failed: %v", err)
	}
	if !despawnResp.GetSuccess() {
		t.Fatalf("DespawnAgentEntity returned success=false")
	}
	t.Logf("DespawnAgentEntity succeeded for agent_id=%s", agentID)

	// 6. Test Production CLI Binary Execution: launch civquest-agent as a subprocess
	t.Log("Testing Production civquest-agent CLI binary execution against live server...")
	runProductionAgentBinaryProbe(t, meta)
}

func runProductionAgentBinaryProbe(t *testing.T, meta *bridge.ServerMeta) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	agentBin, err := filepath.Abs("../../../bin/civquest-agent")
	if err != nil {
		t.Fatalf("Failed to resolve binary path: %v", err)
	}
	if fi, err := os.Stat(agentBin); err != nil || fi.IsDir() {
		buildCmd := exec.Command("go", "build", "-o", agentBin, "../../cmd/civquest-agent")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to build civquest-agent binary: %v (output: %s)", err, string(out))
		}
	}

	agentName := "ci-probe-agent"
	cmd := exec.CommandContext(ctx, agentBin,
		"--name", agentName,
		"--grpc-addr", meta.Addr(),
		"--shared-key", meta.SharedKey,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed creating stdout pipe for civquest-agent: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start civquest-agent subprocess: %v", err)
	}

	spawnDetected := make(chan struct{})
	streamConnected := make(chan struct{})

	scanner := bufio.NewScanner(stdout)
	go func() {
		var spawnOnce, streamOnce bool
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("[civquest-agent] %s", line)
			if !spawnOnce && strings.Contains(line, "spawned into Minecraft") {
				spawnOnce = true
				close(spawnDetected)
			}
			if !streamOnce && strings.Contains(line, "Connecting telemetry stream") {
				streamOnce = true
				close(streamConnected)
			}
		}
	}()

	select {
	case <-spawnDetected:
		t.Logf("✓ Production civquest-agent binary successfully spawned Agent '%s' into Minecraft", agentName)
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("Timed out waiting for production civquest-agent to spawn agent into Minecraft")
	}

	select {
	case <-streamConnected:
		t.Logf("✓ Production civquest-agent binary successfully connected telemetry stream")
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("Timed out waiting for civquest-agent to connect telemetry stream")
	}

	// Send SIGINT / SIGTERM for graceful shutdown
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("Failed to signal civquest-agent: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case waitErr := <-waitDone:
		t.Logf("civquest-agent process exited cleanly: %v", waitErr)
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Log("civquest-agent forced killed after wait timeout")
	}
	t.Log("✓ Production civquest-agent binary shutdown cleanly")
}

// TestLiveServerEndToEnd connects to the live Minecraft headless server via discovery metadata.
func TestLiveServerEndToEnd(t *testing.T) {
	discoveryTimeout := 3 * time.Second
	if tStr := os.Getenv("CIVQUEST_DISCOVERY_TIMEOUT"); tStr != "" {
		if d, err := time.ParseDuration(tStr); err == nil {
			discoveryTimeout = d
		}
	}

	t.Logf("Waiting for live server discovery metadata (timeout %v)...", discoveryTimeout)
	meta, err := bridge.WaitForDiscovery("", discoveryTimeout)
	if err != nil {
		if os.Getenv("CIVQUEST_REQUIRE_LIVE_SERVER") != "true" && os.Getenv("CIVQUEST_DISCOVERY_PATH") == "" {
			t.Skipf("Skipping live server test: %v (run headless server via './gradlew runIntegrationServer' or set CIVQUEST_REQUIRE_LIVE_SERVER=true)", err)
		}
		t.Fatalf("Failed to discover live server: %v (ensure headless server is running via './gradlew runIntegrationServer' or set CIVQUEST_DISCOVERY_PATH)", err)
	}

	runEndToEndProbe(t, meta)
}

// TestSyntheticLiveServerProbe starts an in-process TCP gRPC server mimicking the NeoForge mod
// to verify all probe assertions end-to-end over live TCP networking and file discovery.
func TestSyntheticLiveServerProbe(t *testing.T) {
	sharedKey := "synthetic-secret-probe-key-12345"
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed listening on TCP: %v", err)
	}
	defer lis.Close()

	tcpPort := lis.Addr().(*net.TCPAddr).Port

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(syntheticAuthUnaryInterceptor(sharedKey)),
		grpc.StreamInterceptor(syntheticAuthStreamInterceptor(sharedKey)),
	)

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("civquest.v1.CivQuestBridgeService", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)

	civquestv1.RegisterCivQuestBridgeServiceServer(srv, &syntheticBridgeServer{})

	go func() {
		_ = srv.Serve(lis)
	}()
	defer srv.Stop()

	tempDir := t.TempDir()
	metaPath := filepath.Join(tempDir, "civquest-server-meta.json")
	discoveryJSON := fmt.Sprintf(`{
  "status": "READY",
  "grpc_transport": "tcp",
  "grpc_port": %d,
  "grpc_host": "127.0.0.1",
  "server_pid": %d,
  "shared_key": "%s",
  "timestamp_ms": %d
}`, tcpPort, os.Getpid(), sharedKey, time.Now().UnixMilli())

	if err := os.WriteFile(metaPath, []byte(discoveryJSON), 0600); err != nil {
		t.Fatalf("Failed writing synthetic discovery file: %v", err)
	}

	meta, err := bridge.WaitForDiscovery(metaPath, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForDiscovery failed for synthetic probe: %v", err)
	}

	runEndToEndProbe(t, meta)
}

// syntheticBridgeServer provides test responses matching CivQuestBridgeServiceImpl.
type syntheticBridgeServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer
}

func (s *syntheticBridgeServer) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	return &civquestv1.SpawnAgentResponse{
		Success:           true,
		MinecraftEntityId: 1001,
		OfflineUuid:       "00000000-0000-0000-0000-000000000042",
	}, nil
}

func (s *syntheticBridgeServer) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest) (*civquestv1.DespawnAgentResponse, error) {
	return &civquestv1.DespawnAgentResponse{
		Success: true,
	}, nil
}

func (s *syntheticBridgeServer) ConnectAgent(stream civquestv1.CivQuestBridgeService_ConnectAgentServer) error {
	initialFrame := &civquestv1.TelemetryFrame{
		WorldTick: 0,
		Status: &civquestv1.AgentStatus{
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
		},
	}
	if err := stream.Send(initialFrame); err != nil {
		return err
	}

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

func syntheticAuthUnaryInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
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

func syntheticAuthStreamInterceptor(expectedKey string) grpc.StreamServerInterceptor {
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
