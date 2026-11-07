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
