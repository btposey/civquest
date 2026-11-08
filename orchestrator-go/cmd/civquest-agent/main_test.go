package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
)

type mockTestServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer

	mu       sync.Mutex
	stream   civquestv1.CivQuestBridgeService_ConnectAgentServer
	readyCh  chan struct{}
	sendDead bool
}

func (s *mockTestServer) ConnectAgent(stream civquestv1.CivQuestBridgeService_ConnectAgentServer) error {
	s.mu.Lock()
	s.stream = stream
	select {
	case s.readyCh <- struct{}{}:
	default:
	}
	sendDead := s.sendDead
	s.mu.Unlock()

	// Read handshake
	_, err := stream.Recv()
	if err != nil {
		return err
	}

	if sendDead {
		_ = stream.Send(&civquestv1.TelemetryFrame{
			WorldTick: 50,
			DeathEvent: &civquestv1.DeathEvent{
				AgentId:      "test_dead",
				DamageSource: "lava",
			},
		})
	} else {
		_ = stream.Send(&civquestv1.TelemetryFrame{
			WorldTick: 10,
			Status: &civquestv1.AgentStatus{
				Health:    20.0,
				FoodLevel: 20,
			},
		})
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

func (s *mockTestServer) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	return &civquestv1.SpawnAgentResponse{
		Success:           true,
		MinecraftEntityId: 42,
		OfflineUuid:       "00000000-0000-0000-0000-000000000001",
	}, nil
}

func (s *mockTestServer) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest) (*civquestv1.DespawnAgentResponse, error) {
	return &civquestv1.DespawnAgentResponse{
		Success: true,
	}, nil
}

func startTestServer(t *testing.T, sendDead bool) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on tcp: %v", err)
	}

	mockSvc := &mockTestServer{
		readyCh:  make(chan struct{}, 1),
		sendDead: sendDead,
	}
	s := grpc.NewServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(s, mockSvc)

	go func() {
		_ = s.Serve(lis)
	}()

	cleanup := func() {
		s.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), cleanup
}

func TestCivquestAgent_BasicInvocationAndShutdown(t *testing.T) {
	serverAddr, cleanupServer := startTestServer(t, false)
	defer cleanupServer()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{
			"--agent-id", "aria_test",
			"--grpc-addr", serverAddr,
			"--shared-key", "test-key-123",
		}, &stdout, &stderr, resolver)
	}()

	// Allow agent to connect and initialize
	time.Sleep(100 * time.Millisecond)

	// Cancel context to simulate SIGINT/SIGTERM
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run returned error on shutdown: %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for agent run() to exit")
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Starting Settler Avatar: aria_test") {
		t.Errorf("expected avatar name in stdout, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Shared-Key Bearer Token") {
		t.Errorf("expected authentication confirmation in stdout, got:\n%s", outStr)
	}

	// Verify database was initialized in data dir
	agentDataDir := resolver.AgentDataDir("aria_test")
	memDB := filepath.Join(agentDataDir, "memory.db")
	if _, err := os.Stat(memDB); os.IsNotExist(err) {
		t.Errorf("expected memory.db to exist at %s", memDB)
	}
}

func TestCivquestAgent_PermadeathCleanExit(t *testing.T) {
	serverAddr, cleanupServer := startTestServer(t, true)
	defer cleanupServer()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	err := run(ctx, []string{
		"--agent-id", "test_dead",
		"--grpc-addr", serverAddr,
	}, &stdout, &stderr, resolver)

	if err != nil {
		t.Fatalf("expected clean exit code 0 on death, got error: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Clean permadeath exit code 0") {
		t.Errorf("expected permadeath exit confirmation, got:\n%s", outStr)
	}
}

func TestCivquestAgent_LegacyNameFlag(t *testing.T) {
	serverAddr, cleanupServer := startTestServer(t, false)
	defer cleanupServer()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{
			"--name", "legacy_avatar",
			"--grpc-addr", serverAddr,
			"--auth-token", "bearer-token-alias",
		}, &stdout, &stderr, resolver)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for agent run() to exit")
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Starting Settler Avatar: legacy_avatar") {
		t.Errorf("expected legacy avatar name in stdout, got:\n%s", outStr)
	}
}

func TestCivquestAgent_InvalidAgentIDValidation(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent-id", "../../bad_name",
	}, &stdout, &stderr, resolver)

	if err == nil {
		t.Fatal("expected error for invalid agent ID, got nil")
	}
	if !strings.Contains(err.Error(), "invalid agent identifier") {
		t.Errorf("expected invalid agent identifier error message, got: %v", err)
	}
}

func TestCivquestAgent_CorruptedConfigRefusal(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	configPath := resolver.AgentConfigPath("aria")
	if err := os.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("broken_yaml: [unclosed"), 0600); err != nil {
		t.Fatalf("failed to write corrupted config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--agent-id", "aria",
	}, &stdout, &stderr, resolver)

	if err == nil {
		t.Fatal("expected error for corrupted config, got nil")
	}
	if !strings.Contains(err.Error(), "loading agent config for aria") {
		t.Errorf("expected loading agent config error, got: %v", err)
	}
}

func TestCivquestAgent_ServerIDFlag(t *testing.T) {
	serverAddr, cleanupServer := startTestServer(t, false)
	defer cleanupServer()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{
			"--agent-id", "server_avatar",
			"--server-id", "server_cluster_1",
			"--grpc-addr", serverAddr,
			"--shared-key", "test-key-123",
		}, &stdout, &stderr, resolver)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run returned error: %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for agent run() to exit")
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Server ID:      server_cluster_1") {
		t.Errorf("expected Server ID in stdout banner, got:\n%s", outStr)
	}

	// Verify database was initialized in namespaced data dir
	expectedDB := filepath.Join(resolver.DataDir(), "servers", "server_cluster_1", "agents", "server_avatar", "memory.db")
	if _, err := os.Stat(expectedDB); os.IsNotExist(err) {
		t.Errorf("expected namespaced memory.db at %s", expectedDB)
	}
}
