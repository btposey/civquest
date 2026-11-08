package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// mockBridgeServer simulates the Minecraft NeoForge gRPC server.
type mockBridgeServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer

	mu              sync.Mutex
	receivedIntents []*civquestv1.AgentIntent
	intentCh        chan *civquestv1.AgentIntent
	serverStream    civquestv1.CivQuestBridgeService_ConnectAgentServer
	streamReady     chan struct{}
}

func newMockBridgeServer() *mockBridgeServer {
	return &mockBridgeServer{
		intentCh:    make(chan *civquestv1.AgentIntent, 100),
		streamReady: make(chan struct{}, 1),
	}
}

func (s *mockBridgeServer) ConnectAgent(stream civquestv1.CivQuestBridgeService_ConnectAgentServer) error {
	s.mu.Lock()
	s.serverStream = stream
	select {
	case s.streamReady <- struct{}{}:
	default:
	}
	s.mu.Unlock()

	for {
		intent, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		s.mu.Lock()
		s.receivedIntents = append(s.receivedIntents, intent)
		s.mu.Unlock()

		select {
		case s.intentCh <- intent:
		default:
		}
	}
}

func (s *mockBridgeServer) SendFrame(frame *civquestv1.TelemetryFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverStream == nil {
		return io.ErrClosedPipe
	}
	return s.serverStream.Send(frame)
}

func setupTestBridge(t *testing.T) (*mockBridgeServer, *bridge.Client, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	mockSvc := newMockBridgeServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(s, mockSvc)

	go func() {
		_ = s.Serve(lis)
	}()

	client, err := bridge.NewClient("passthrough://bufnet", "",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		s.Stop()
		_ = lis.Close()
		t.Fatalf("failed to create test bridge client: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		s.Stop()
		_ = lis.Close()
	}

	return mockSvc, client, cleanup
}

func TestRuntime_LifecycleAndTelemetryIngestion(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	cfg := agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	// Wait for stream to be connected on server side
	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ConnectAgent stream readiness")
	}

	// Wait for handshake intent
	select {
	case handshake := <-mockSvc.intentCh:
		if handshake.AgentId != "aria" {
			t.Errorf("expected handshake agent_id 'aria', got %q", handshake.AgentId)
		}
		if handshake.SequenceId != 1 {
			t.Errorf("expected handshake sequence_id 1, got %d", handshake.SequenceId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial handshake intent")
	}

	// Send initial telemetry frame
	testFrame := &civquestv1.TelemetryFrame{
		WorldTick: 1200,
		Status: &civquestv1.AgentStatus{
			AgentId:   "aria",
			Health:    18.5,
			MaxHealth: 20.0,
			FoodLevel: 19,
			Position: &civquestv1.Vec3{
				X: 10.5,
				Y: 64.0,
				Z: -20.5,
			},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			Inventory: []*civquestv1.ItemStackProto{
				{
					Slot:   0,
					ItemId: "minecraft:iron_sword",
					Count:  1,
				},
				{
					Slot:   1,
					ItemId: "minecraft:bread",
					Count:  16,
				},
			},
		},
		LastProcessedSequenceId: 1,
	}

	if err := mockSvc.SendFrame(testFrame); err != nil {
		t.Fatalf("failed sending telemetry frame: %v", err)
	}

	// Wait for frame to be processed
	var ok bool
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		if rt.Status() == "RUNNING" && rt.Health() == 18.5 && rt.WorldTick() == 1200 {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("timed out waiting for telemetry state update: status=%s, health=%.1f, tick=%d",
			rt.Status(), rt.Health(), rt.WorldTick())
	}

	if rt.FoodLevel() != 19 {
		t.Errorf("expected food 19, got %d", rt.FoodLevel())
	}
	if rt.NavigationState() != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		t.Errorf("expected state PATHING, got %v", rt.NavigationState())
	}
	if rt.LastProcessedSequenceID() != 1 {
		t.Errorf("expected last processed seq 1, got %d", rt.LastProcessedSequenceID())
	}
	pos := rt.Position()
	if pos == nil || pos.X != 10.5 || pos.Y != 64.0 || pos.Z != -20.5 {
		t.Errorf("unexpected position: %+v", pos)
	}

	// Verify inventory_cache.json written to disk atomically
	cachePath := resolver.AgentInventoryCachePath("aria")
	var cache *agent.InventoryCache
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		cache, err = agent.ReadInventoryCache(cachePath)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("failed reading inventory cache: %v", err)
	}
	if cache.AgentID != "aria" {
		t.Errorf("expected cached agent 'aria', got %q", cache.AgentID)
	}
	if cache.WorldTick != 1200 {
		t.Errorf("expected cached tick 1200, got %d", cache.WorldTick)
	}
	if len(cache.Items) != 2 {
		t.Fatalf("expected 2 cached items, got %d", len(cache.Items))
	}
	if cache.Items[0].ItemID != "minecraft:iron_sword" || cache.Items[1].ItemID != "minecraft:bread" {
		t.Errorf("unexpected cached items: %+v", cache.Items)
	}

	// Test outbound intent queue
	navIntent := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target:          &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
				SpeedMultiplier: 1.0,
			},
		},
	}
	if err := rt.QueueIntent(navIntent); err != nil {
		t.Fatalf("failed queueing intent: %v", err)
	}

	select {
	case received := <-mockSvc.intentCh:
		if received.AgentId != "aria" {
			t.Errorf("expected intent agent_id 'aria', got %q", received.AgentId)
		}
		if received.SequenceId != 2 {
			t.Errorf("expected intent sequence_id 2, got %d", received.SequenceId)
		}
		if received.GetNavigate() == nil {
			t.Errorf("expected navigate intent, got %+v", received.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatched navigate intent")
	}

	// Stop runtime and verify clean exit
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Errorf("expected nil error on clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime shutdown")
	}

	if rt.Status() != "STOPPED" {
		t.Errorf("expected status STOPPED, got %s", rt.Status())
	}
}

func TestRuntime_UnixDomainSocket(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Send frame to populate state
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 550,
		Status: &civquestv1.AgentStatus{
			Health:          20.0,
			FoodLevel:       18,
			Position:        &civquestv1.Vec3{X: 1.5, Y: 2.0, Z: 3.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})

	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		if rt.WorldTick() == 550 {
			break
		}
	}

	// Connect to UNIX socket and read status payload
	sockPath := rt.SocketPath()
	fi, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("failed to stat socket file %s: %v", sockPath, err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected socket permissions 0600, got %#o", fi.Mode().Perm())
	}

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("failed dialing agent unix socket %s: %v", sockPath, err)
	}
	defer conn.Close()

	var resp agent.SocketStatusResponse
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("failed decoding socket JSON payload: %v", err)
	}

	if resp.Agent != "aria" {
		t.Errorf("expected agent 'aria', got %q", resp.Agent)
	}
	if resp.Clan != "pioneers" {
		t.Errorf("expected clan 'pioneers', got %q", resp.Clan)
	}
	if resp.Status != "RUNNING" {
		t.Errorf("expected status 'RUNNING', got %q", resp.Status)
	}
	if resp.WorldTick != 550 {
		t.Errorf("expected tick 550, got %d", resp.WorldTick)
	}
	if resp.Health != 20.0 {
		t.Errorf("expected health 20.0, got %.1f", resp.Health)
	}
	if resp.FoodLevel != 18 {
		t.Errorf("expected food 18, got %d", resp.FoodLevel)
	}
	if resp.Position == nil || resp.Position.X != 1.5 || resp.Position.Z != 3.5 {
		t.Errorf("unexpected position: %+v", resp.Position)
	}
	if resp.NavigationState != "NAVIGATION_STATE_IDLE" {
		t.Errorf("expected nav state NAVIGATION_STATE_IDLE, got %q", resp.NavigationState)
	}
	if resp.PID <= 0 {
		t.Errorf("expected valid positive PID, got %d", resp.PID)
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("expected positive uptime, got %f", resp.UptimeSeconds)
	}

	cancel()
	_ = <-runErrCh

	// Verify socket removed after shutdown
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("expected socket file %s to be removed after shutdown", sockPath)
	}
}

func TestRuntime_DeathEventHandling(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	deathCh := make(chan *civquestv1.DeathEvent, 1)

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
		OnDeath: func(e *civquestv1.DeathEvent) {
			deathCh <- e
		},
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx := context.Background()
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	deathEv := &civquestv1.DeathEvent{
		AgentId:           "aria",
		WorldTick:         3450,
		DamageSource:      "minecraft:creeper",
		KillerDisplayName: "Creeper",
	}

	// Send frame with DeathEvent
	if err := mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick:  3450,
		DeathEvent: deathEv,
	}); err != nil {
		t.Fatalf("failed sending death frame: %v", err)
	}

	select {
	case receivedDeath := <-deathCh:
		if receivedDeath.DamageSource != "minecraft:creeper" {
			t.Errorf("expected damage source creeper, got %s", receivedDeath.DamageSource)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onDeath callback")
	}

	// Runtime should cleanly exit with nil error (code 0)
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Errorf("expected nil error on death teardown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime to terminate after death")
	}

	if !rt.IsDead() {
		t.Error("expected runtime.IsDead() to be true")
	}
	if rt.Status() != "DEAD" {
		t.Errorf("expected status 'DEAD', got %q", rt.Status())
	}
}

func TestRuntime_QueueValidation(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "aria",
		ClanID:          "pioneers",
		Resolver:        resolver,
		IntentQueueSize: 2,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	// 1. Nil intent
	if err := rt.QueueIntent(nil); err != agent.ErrNilIntent {
		t.Errorf("expected ErrNilIntent, got %v", err)
	}

	// 2. Enqueue up to capacity
	i1 := &civquestv1.AgentIntent{}
	i2 := &civquestv1.AgentIntent{}
	if err := rt.QueueIntent(i1); err != nil {
		t.Fatalf("failed enqueueing i1: %v", err)
	}
	if err := rt.EnqueueIntent(i2); err != nil {
		t.Fatalf("failed enqueueing i2: %v", err)
	}

	// 3. Queue full
	i3 := &civquestv1.AgentIntent{}
	if err := rt.QueueIntent(i3); err != agent.ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}

	// Verify sequence auto-increment
	if i1.SequenceId != 1 || i1.AgentId != "aria" {
		t.Errorf("unexpected i1: id=%d, agent=%s", i1.SequenceId, i1.AgentId)
	}
	if i2.SequenceId != 2 || i2.AgentId != "aria" {
		t.Errorf("unexpected i2: id=%d, agent=%s", i2.SequenceId, i2.AgentId)
	}

	// 4. Stop and test ErrRuntimeStopped
	rt.Stop()
	if err := rt.QueueIntent(&civquestv1.AgentIntent{}); err != agent.ErrRuntimeStopped {
		t.Errorf("expected ErrRuntimeStopped, got %v", err)
	}
}

func TestInventoryCache_FileHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/test_cache.json"

	items := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:apple", Count: 5},
		{Slot: 1, ItemId: "minecraft:shield", Count: 1, Durability: 300, MaxDurability: 336},
	}

	cache := agent.NewInventoryCache("aria", 100, items)
	if err := agent.WriteInventoryCache(filePath, cache); err != nil {
		t.Fatalf("failed writing inventory cache: %v", err)
	}

	loaded, err := agent.ReadInventoryCache(filePath)
	if err != nil {
		t.Fatalf("failed reading inventory cache: %v", err)
	}

	if loaded.AgentID != "aria" || loaded.WorldTick != 100 || len(loaded.Items) != 2 {
		t.Fatalf("unexpected loaded cache: %+v", loaded)
	}
	if !agent.InventoryEqual(cache.Items, loaded.Items) {
		t.Errorf("expected InventoryEqual to be true")
	}

	// Non-equal comparison
	modified := []agent.CachedItem{
		{Slot: 0, ItemID: "minecraft:apple", Count: 6},
		{Slot: 1, ItemID: "minecraft:shield", Count: 1},
	}
	if agent.InventoryEqual(cache.Items, modified) {
		t.Errorf("expected InventoryEqual to be false for modified count")
	}
}

func TestRuntime_ConcurrentStartStop(t *testing.T) {
	_, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = rt.Start(context.Background())
	}()

	// Multiple concurrent goroutines calling Stop() and inspecting runtime state
	const numStoppers = 8
	for i := 0; i < numStoppers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(2 * time.Millisecond)
			_ = rt.Status()
			_ = rt.Position()
			_ = rt.LatestFrame()
			rt.Stop()
		}()
	}

	wg.Wait()
	if rt.Status() != "STOPPED" {
		t.Errorf("expected STOPPED status, got %s", rt.Status())
	}
}

type mockFailingStream struct {
	sendCount atomic.Int32
	ctx       context.Context
}

func (m *mockFailingStream) Send(intent *civquestv1.AgentIntent) error {
	if m.sendCount.Add(1) > 1 {
		return errors.New("simulated broken pipe during send")
	}
	return nil
}

func (m *mockFailingStream) Recv() (*civquestv1.TelemetryFrame, error) {
	<-m.ctx.Done()
	return nil, m.ctx.Err()
}

func (m *mockFailingStream) Header() (metadata.MD, error) { return nil, nil }
func (m *mockFailingStream) Trailer() metadata.MD         { return nil }
func (m *mockFailingStream) CloseSend() error             { return nil }
func (m *mockFailingStream) Context() context.Context     { return m.ctx }
func (m *mockFailingStream) SendMsg(any) error            { return nil }
func (m *mockFailingStream) RecvMsg(any) error            { return nil }

type mockFailingConnector struct {
	stream *mockFailingStream
}

func (c *mockFailingConnector) ConnectAgent(ctx context.Context, opts ...grpc.CallOption) (civquestv1.CivQuestBridgeService_ConnectAgentClient, error) {
	c.stream.ctx = ctx
	return c.stream, nil
}

func TestRuntime_SendLoopErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	failingStream := &mockFailingStream{}
	connector := &mockFailingConnector{stream: failingStream}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "aria",
		ClanID:          "pioneers",
		Resolver:        resolver,
		StreamConnector: connector,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(context.Background())
	}()

	// Wait until runtime reaches RUNNING state
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		if rt.Status() == "RUNNING" {
			break
		}
	}
	if rt.Status() != "RUNNING" {
		t.Fatalf("runtime failed to reach RUNNING state: status=%s", rt.Status())
	}

	// Enqueue intent which triggers send failure in sendLoop
	if err := rt.QueueIntent(&civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{X: 1, Y: 2, Z: 3},
			},
		},
	}); err != nil {
		t.Fatalf("failed to queue intent: %v", err)
	}

	// sendLoop error must cancel stream context and exit Start() cleanly without hanging
	select {
	case runErr := <-runErrCh:
		if runErr == nil {
			t.Fatal("expected non-nil error when sendLoop fails, got nil")
		}
		if !strings.Contains(runErr.Error(), "simulated broken pipe during send") {
			t.Errorf("expected simulated broken pipe error, got: %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime to terminate after sendLoop error")
	}

	if rt.Status() != "STOPPED" {
		t.Errorf("expected STOPPED status after send failure, got %s", rt.Status())
	}
}

func TestRuntime_InventoryCacheClearing(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// 1. Send frame with 1 item
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			AgentId:   "aria",
			Health:    20.0,
			FoodLevel: 20,
			Inventory: []*civquestv1.ItemStackProto{
				{Slot: 0, ItemId: "minecraft:golden_apple", Count: 3},
			},
		},
	})

	cachePath := resolver.AgentInventoryCachePath("aria")
	var cache *agent.InventoryCache
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		cache, err = agent.ReadInventoryCache(cachePath)
		if err == nil && len(cache.Items) == 1 {
			break
		}
	}
	if err != nil || cache == nil || len(cache.Items) != 1 {
		t.Fatalf("expected 1 cached item initially, got %+v (err: %v)", cache, err)
	}

	// 2. Send frame with empty inventory (Status != nil, Inventory is nil)
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 105,
		Status: &civquestv1.AgentStatus{
			AgentId:   "aria",
			Health:    20.0,
			FoodLevel: 20,
			Inventory: nil,
		},
	})

	var clearedCache *agent.InventoryCache
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		clearedCache, err = agent.ReadInventoryCache(cachePath)
		if err == nil && clearedCache.WorldTick == 105 && len(clearedCache.Items) == 0 {
			break
		}
	}
	if err != nil || clearedCache == nil || len(clearedCache.Items) != 0 {
		t.Fatalf("expected cleared inventory cache with 0 items, got %+v (err: %v)", clearedCache, err)
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_InvalidAgentIDValidation(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	invalidNames := []string{
		"../../bad",
		"..",
		".",
		"",
		"bad/name",
		"bad:name",
		"bad*name",
		"this_name_exceeds_the_maximum_32_characters_allowed",
	}

	for _, name := range invalidNames {
		t.Run("invalid_"+name, func(t *testing.T) {
			_, err := agent.NewRuntime(agent.Config{
				AgentID:  name,
				Resolver: resolver,
			})
			if err == nil {
				t.Fatalf("expected error for invalid agent ID %q, got nil", name)
			}
			if !errors.Is(err, xdg.ErrInvalidAgentName) {
				t.Errorf("expected error wrapping ErrInvalidAgentName for %q, got: %v", name, err)
			}
		})
	}
}

func TestRuntime_DefensiveCopies(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "aria",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10, Y: 20, Z: 30},
		},
	})

	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		if rt.WorldTick() == 50 {
			break
		}
	}

	// Mutating Position() copy must not alter runtime internal state
	pos := rt.Position()
	if pos == nil || pos.X != 10 {
		t.Fatalf("unexpected position: %+v", pos)
	}
	pos.X = 999.0
	pos2 := rt.Position()
	if pos2.X != 10 {
		t.Errorf("internal position was mutated: expected 10, got %f", pos2.X)
	}

	// Mutating LatestFrame() copy must not alter runtime internal state
	frame := rt.LatestFrame()
	if frame == nil || frame.WorldTick != 50 {
		t.Fatalf("unexpected latest frame: %+v", frame)
	}
	frame.WorldTick = 9999
	frame2 := rt.LatestFrame()
	if frame2.WorldTick != 50 {
		t.Errorf("internal latestFrame was mutated: expected 50, got %d", frame2.WorldTick)
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_System1_PanicCombatReflexes(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("panic_agent")

	cfg := agent.Config{
		AgentID:      "panic_agent",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain initial handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Send frame with a hissing creeper 3 blocks away and agent low health with steak
	creeperFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			AgentId:   "panic_agent",
			Health:    12.0,
			MaxHealth: 20.0,
			FoodLevel: 10,
			Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:steak", Count: 4, Slot: 2},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   55,
				EntityType: "minecraft:creeper",
				Distance:   3.0,
				IsHostile:  true,
				FuseTicks:  15, // hissing!
				Position:   &civquestv1.Vec3{X: 103, Y: 64, Z: 100},
			},
		},
	}

	if err := mockSvc.SendFrame(creeperFrame); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}

	// Expect panic intents: Attack (raise shield + flee), Navigate (sprint away), UseItem (eat steak)
	var gotAttack, gotNavigate, gotUseItem bool
	deadline := time.After(2 * time.Second)

	for !gotAttack || !gotNavigate || !gotUseItem {
		select {
		case intent := <-mockSvc.intentCh:
			if att := intent.GetAttack(); att != nil {
				gotAttack = true
				if !att.RaiseShield || !att.FleeImmediately {
					t.Errorf("expected panic attack intent to raise shield and flee immediately, got %+v", att)
				}
				if att.TargetEntityId != 55 {
					t.Errorf("expected target entity ID 55, got %d", att.TargetEntityId)
				}
			}
			if nav := intent.GetNavigate(); nav != nil {
				gotNavigate = true
				if !nav.Sprint {
					t.Errorf("expected panic navigate intent to sprint")
				}
				if nav.Target.X >= 100 {
					t.Errorf("expected flee target away from creeper (X < 100), got %f", nav.Target.X)
				}
			}
			if use := intent.GetUseItem(); use != nil {
				gotUseItem = true
				if use.InventorySlot != 2 || !use.IsEatingOrDrinking {
					t.Errorf("expected UseItem on slot 2 with is_eating=true, got %+v", use)
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for panic intents: gotAttack=%v, gotNav=%v, gotUseItem=%v", gotAttack, gotNavigate, gotUseItem)
		}
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_System1_ConfidentStandReflexes(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("confident_agent")

	cfg := agent.Config{
		AgentID:  "confident_agent",
		ClanID:   "pioneers",
		Resolver: resolver,
		IdentityConfig: &xdg.AgentIdentityConfig{
			Name:   "confident_agent",
			ClanID: "pioneers",
			Traits: xdg.AgentTraits{
				Bravery:         0.9,
				Industriousness: 0.5,
				Sociability:     0.5,
				Curiosity:       0.5,
				Aggressiveness:  0.8,
			},
		},
		BridgeClient: client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain initial handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Send frame with full diamond gear facing 1 skeleton (ranged)
	skeletonFrame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		Status: &civquestv1.AgentStatus{
			AgentId:   "confident_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:diamond_helmet", Count: 1, Slot: 103},
				{ItemId: "minecraft:diamond_chestplate", Count: 1, Slot: 102},
				{ItemId: "minecraft:diamond_leggings", Count: 1, Slot: 101},
				{ItemId: "minecraft:diamond_boots", Count: 1, Slot: 100},
				{ItemId: "minecraft:diamond_sword", Count: 1, Slot: 0},
				{ItemId: "minecraft:shield", Count: 1, Slot: 40},
			},
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   88,
				EntityType: "minecraft:skeleton",
				Distance:   10.0,
				IsHostile:  true,
				Position:   &civquestv1.Vec3{X: 60, Y: 64, Z: 50},
				HeldItemId: "minecraft:bow",
			},
		},
	}

	if err := mockSvc.SendFrame(skeletonFrame); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}

	// Expect Confident Stand AttackIntent: target_entity_id=88, raise_shield=true (ranged), keep_distance_kite=true, flee_immediately=false
	select {
	case intent := <-mockSvc.intentCh:
		att := intent.GetAttack()
		if att == nil {
			t.Fatalf("expected AttackIntent, got %+v", intent)
		}
		if att.TargetEntityId != 88 {
			t.Errorf("expected target entity ID 88, got %d", att.TargetEntityId)
		}
		if !att.RaiseShield {
			t.Errorf("expected raise_shield = true when facing ranged skeleton")
		}
		if !att.KeepDistanceKite {
			t.Errorf("expected keep_distance_kite = true")
		}
		if att.FleeImmediately {
			t.Errorf("expected flee_immediately = false for confident stand")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for confident stand AttackIntent")
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_System1_LowHPPanic(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("low_hp_agent")

	cfg := agent.Config{
		AgentID:      "low_hp_agent",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain initial handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Send frame with low HP (5.0 <= 0.35 * 20.0 = 7.0) and cooked porkchop in slot 1
	lowHPFrame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			AgentId:   "low_hp_agent",
			Health:    5.0,
			MaxHealth: 20.0,
			FoodLevel: 15,
			Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_porkchop", Count: 8, Slot: 1},
			},
		},
	}

	if err := mockSvc.SendFrame(lowHPFrame); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}

	// Low HP triggers panic override: should produce AttackIntent, NavigateIntent, and UseItemIntent
	var gotUseItem bool
	deadline := time.After(2 * time.Second)

	for !gotUseItem {
		select {
		case intent := <-mockSvc.intentCh:
			if use := intent.GetUseItem(); use != nil {
				gotUseItem = true
				if use.InventorySlot != 1 || !use.IsEatingOrDrinking {
					t.Errorf("expected UseItemIntent on slot 1, got %+v", use)
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for eating intent on low HP")
		}
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_QueuePriorityIntent_Eviction(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "priority_agent",
		ClanID:          "pioneers",
		Resolver:        resolver,
		IntentQueueSize: 2, // Small queue to force eviction
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	// 1. Fill queue to capacity with routine intents
	r1 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 1, Y: 1, Z: 1}},
		},
	}
	r2 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 2, Y: 2, Z: 2}},
		},
	}
	if err := rt.QueueIntent(r1); err != nil {
		t.Fatalf("failed queueing r1: %v", err)
	}
	if err := rt.QueueIntent(r2); err != nil {
		t.Fatalf("failed queueing r2: %v", err)
	}

	// 2. Normal QueueIntent now fails with ErrQueueFull
	r3 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 3, Y: 3, Z: 3}},
		},
	}
	if err := rt.QueueIntent(r3); err != agent.ErrQueueFull {
		t.Fatalf("expected ErrQueueFull on full queue, got %v", err)
	}

	// 3. QueuePriorityIntent must evict r1 (head-of-line) and successfully enqueue reflex intent
	reflexIntent := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{
			Attack: &civquestv1.AttackIntent{TargetEntityId: 99, RaiseShield: true, FleeImmediately: true},
		},
	}
	if err := rt.QueuePriorityIntent(reflexIntent); err != nil {
		t.Fatalf("QueuePriorityIntent failed on full queue: %v", err)
	}
}

func TestRuntime_SafeNodesDefensiveCopy(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:  "safenode_agent",
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	original := []db.MapNode{
		{ID: "node_1", X: 10, Y: 64, Z: 10, SafetyScore: 0.95},
	}
	rt.SetSafeNodes(original)

	// Mutate input slice: internal cache must remain unchanged
	original[0].ID = "corrupted"
	cached := rt.SafeNodes()
	if len(cached) != 1 || cached[0].ID != "node_1" {
		t.Fatalf("internal cache was mutated via caller slice: %+v", cached)
	}

	// Mutate returned slice: internal cache must remain unchanged
	cached[0].ID = "corrupted_again"
	cached2 := rt.SafeNodes()
	if len(cached2) != 1 || cached2[0].ID != "node_1" {
		t.Fatalf("internal cache was mutated via return slice: %+v", cached2)
	}
}

func TestRuntime_System1_BaritonePathChurnMitigation(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("churn_agent")

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "churn_agent",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	makePanicFrame := func(tick int64, agentX, creeperX float64) *civquestv1.TelemetryFrame {
		return &civquestv1.TelemetryFrame{
			WorldTick: tick,
			Status: &civquestv1.AgentStatus{
				AgentId:   "churn_agent",
				Health:    10.0,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: agentX, Y: 64, Z: 100},
			},
			VisibleEntities: []*civquestv1.VisibleEntity{
				{
					EntityId:   50,
					EntityType: "minecraft:creeper",
					Distance:   3.0,
					FuseTicks:  20,
					Position:   &civquestv1.Vec3{X: creeperX, Y: 64, Z: 100},
				},
			},
		}
	}

	// Frame 1 at tick 100: Initial panic triggers NavigateIntent
	if err := mockSvc.SendFrame(makePanicFrame(100, 100.0, 103.0)); err != nil {
		t.Fatalf("failed sending frame 1: %v", err)
	}

	var gotNav1 bool
	deadline := time.After(2 * time.Second)
	for !gotNav1 {
		select {
		case intent := <-mockSvc.intentCh:
			if intent.GetNavigate() != nil {
				gotNav1 = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for initial NavigateIntent at tick 100")
		}
	}

	// Frame 2 at tick 102 (only 2 ticks elapsed, target shifted < 2.0 blocks)
	if err := mockSvc.SendFrame(makePanicFrame(102, 100.1, 103.1)); err != nil {
		t.Fatalf("failed sending frame 2: %v", err)
	}

	// AttackIntent (shield) should be emitted, but NavigateIntent should be suppressed
	var gotNav2 bool
	drainDeadline := time.After(200 * time.Millisecond)
drainLoop:
	for {
		select {
		case intent := <-mockSvc.intentCh:
			if intent.GetNavigate() != nil {
				gotNav2 = true
			}
		case <-drainDeadline:
			break drainLoop
		}
	}

	if gotNav2 {
		t.Error("expected NavigateIntent to be suppressed on minor position shift within 10 ticks")
	}

	// Frame 3 at tick 115 (> 10 ticks elapsed since tick 100) -> NavigateIntent MUST be re-emitted
	if err := mockSvc.SendFrame(makePanicFrame(115, 100.2, 103.2)); err != nil {
		t.Fatalf("failed sending frame 3: %v", err)
	}

	var gotNav3 bool
	deadline3 := time.After(2 * time.Second)
	for !gotNav3 {
		select {
		case intent := <-mockSvc.intentCh:
			if intent.GetNavigate() != nil {
				gotNav3 = true
			}
		case <-deadline3:
			t.Fatal("timed out waiting for re-emitted NavigateIntent after >10 ticks elapsed")
		}
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_AsynchronousDialogueDispatch(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	// Mock LLM server with deliberate 50ms delay to prove non-blocking execution of System 1 reflexes
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)

		var capturedReq system2.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		userContent := ""
		for _, m := range capturedReq.Messages {
			if m.Role == system2.RoleUser {
				userContent = m.Content
			}
		}

		var mockResp system2.DialogueResponse
		if strings.Contains(strings.ToLower(userContent), "emerald") || strings.Contains(strings.ToLower(userContent), "trade") {
			mockResp = system2.DialogueResponse{
				SpokenReply:   "Trade accepted from System 2!",
				TradeDecision: "ACCEPT",
				PrestigeDelta: 5.0,
				MemorySummary: "Accepted emeralds trade.",
			}
		} else {
			mockResp = system2.DialogueResponse{
				SpokenReply:   "Greetings from System 2!",
				TradeDecision: "NONE",
				PrestigeDelta: 1.0,
				MemorySummary: "Friendly greeting.",
			}
		}

		respBytes, _ := json.Marshal(mockResp)
		resp := system2.ChatCompletionResponse{
			ID:      "chatcmpl-async",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   capturedReq.Model,
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    system2.RoleAssistant,
						Content: string(respBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	llmClient, err := system2.NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: llmServer.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 2},
		system2.WithHTTPClient(llmServer.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating llm client: %v", err)
	}
	defer llmClient.Close()

	agentCfg := &xdg.AgentIdentityConfig{
		Name:       "aria",
		ClanID:     "clan_async",
		Profession: "FARMER",
		Traits: xdg.AgentTraits{
			Bravery:         0.8,
			Sociability:     0.7,
			Industriousness: 0.6,
		},
	}

	dialogueSvc, err := system2.NewDialogueService(llmClient, agentCfg)
	if err != nil {
		t.Fatalf("failed creating dialogue service: %v", err)
	}

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("aria")

	cfg := agent.Config{
		AgentID:         "aria",
		ClanID:          "clan_async",
		IdentityConfig:  agentCfg,
		Resolver:        resolver,
		BridgeClient:    bridgeClient,
		DialogueService: dialogueSvc,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	if rt.DialogueService() != dialogueSvc {
		t.Errorf("expected DialogueService to be assigned")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// 1. Send frame containing both:
	//    - A hissing Creeper (triggers immediate System 1 combat panic)
	//    - An incoming chat message from Steve (triggers asynchronous System 2 dialogue)
	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			Health:    20.0,
			FoodLevel: 20,
		},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{
				EntityId:   99,
				EntityType: "minecraft:creeper",
				Position:   &civquestv1.Vec3{X: 102, Y: 64, Z: 100},
				Distance:   2.0,
				FuseTicks:  10, // hissing!
			},
		},
		IncomingChats: []*civquestv1.ChatEvent{
			{
				SenderUuid: "player-steve",
				SenderName: "Steve",
				Message:    "Hello Aria!",
				Distance:   2.0,
			},
		},
	}

	if err := mockSvc.SendFrame(frame); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}

	// First intents received MUST be System 1 reflexes (Attack + Navigate), completed immediately without waiting for LLM
	var gotAttackReflex, gotNavigateReflex bool
	reflexDeadline := time.After(500 * time.Millisecond)
	for !gotAttackReflex || !gotNavigateReflex {
		select {
		case reflexIntent := <-mockSvc.intentCh:
			if reflexIntent.GetAttack() != nil {
				gotAttackReflex = true
				if !reflexIntent.GetAttack().RaiseShield {
					t.Errorf("expected shield raised in reflex")
				}
			}
			if reflexIntent.GetNavigate() != nil {
				gotNavigateReflex = true
			}
		case <-reflexDeadline:
			t.Fatalf("timed out waiting for System 1 reflexes (gotAttack=%v, gotNavigate=%v)", gotAttackReflex, gotNavigateReflex)
		}
	}

	// Subsequent intent received is asynchronous System 2 SendChatIntent (after 50ms LLM delay)
	select {
	case chatIntent := <-mockSvc.intentCh:
		if chatIntent.GetChat() == nil {
			t.Fatalf("expected System 2 ChatIntent, got: %T", chatIntent.Action)
		}
		if chatIntent.GetChat().Message != "Greetings from System 2!" {
			t.Errorf("expected chat message 'Greetings from System 2!', got %q", chatIntent.GetChat().Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for System 2 chat intent")
	}

	// 2. Send frame with PendingTrade
	tradeFrame := &civquestv1.TelemetryFrame{
		WorldTick: 510,
		Status: &civquestv1.AgentStatus{
			Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			Health:    20.0,
			FoodLevel: 20,
		},
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 42,
			PlayerUuid:     "player-steve",
			PlayerName:     "Steve",
			SpokenProposal: "Trade emerald for wheat?",
			PlayerOfferedItems: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:emerald", Count: 1},
			},
		},
	}

	if err := mockSvc.SendFrame(tradeFrame); err != nil {
		t.Fatalf("failed sending trade frame: %v", err)
	}

	// Expect RespondTradeIntent
	select {
	case tradeIntent := <-mockSvc.intentCh:
		if tradeIntent.GetTradeResponse() == nil {
			t.Fatalf("expected RespondTradeIntent, got: %T", tradeIntent.Action)
		}
		resp := tradeIntent.GetTradeResponse()
		if !resp.AcceptDeal {
			t.Errorf("expected AcceptDeal true")
		}
		if resp.PlayerEntityId != 42 {
			t.Errorf("expected player entity ID 42, got %d", resp.PlayerEntityId)
		}
		if resp.SpokenResponse != "Trade accepted from System 2!" {
			t.Errorf("expected spoken response 'Trade accepted from System 2!', got %q", resp.SpokenResponse)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trade response intent")
	}

	cancel()
	_ = <-runErrCh
}

func TestRuntime_DeadAgentIntentSuppression(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("dead_agent")

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("dead_%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:      "dead_agent",
		ClanID:       "pioneers",
		Resolver:     resolver,
		SocketPath:   sockPath,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case err := <-runErrCh:
		t.Fatalf("runtime exited prematurely: %v", err)
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge connect")
	}

	// 1. Deliver DeathEvent frame
	deathFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		DeathEvent: &civquestv1.DeathEvent{
			AgentId:           "dead_agent",
			KillerDisplayName: "Creeper",
			DamageSource:      "minecraft:creeper",
			WorldTick:         100,
		},
	}
	if err := mockSvc.SendFrame(deathFrame); err != nil {
		t.Fatalf("failed sending death frame: %v", err)
	}

	// Wait for runtime to mark dead and exit
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("unexpected runtime error on death: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime to terminate on death")
	}

	if !rt.IsDead() {
		t.Fatal("expected runtime.IsDead() to be true")
	}

	// 2. Outbound QueueIntent and QueuePriorityIntent MUST fail with ErrAgentDead
	intent := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Chat{
			Chat: &civquestv1.SendChatIntent{Message: "I am dead"},
		},
	}
	if err := rt.QueueIntent(intent); !errors.Is(err, agent.ErrAgentDead) {
		t.Errorf("expected ErrAgentDead on QueueIntent, got %v", err)
	}
	if err := rt.QueuePriorityIntent(intent); !errors.Is(err, agent.ErrAgentDead) {
		t.Errorf("expected ErrAgentDead on QueuePriorityIntent, got %v", err)
	}
}

func TestRuntime_GracefulShutdownWithoutWaitGroupPanic(t *testing.T) {
	// Verify that rapid incoming dialogue events racing with Stop()
	// never cause a sync.WaitGroup panic because r.dialogueWg.Add(1)
	// is properly guarded under r.mu.RLock() checking r.status == "RUNNING".
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("hammer_agent")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			Choices: []system2.ChatCompletionChoice{
				{
					Message: system2.ChatMessage{
						Role:    system2.RoleAssistant,
						Content: `{"spoken_reply":"hi","trade_decision":"NONE","counter_offer_items":[],"prestige_delta":0.0}`,
					},
				},
			},
		})
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	llmClient, _ := system2.NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: srv.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 10, RequestTimeoutSeconds: 2},
	)
	defer llmClient.Close()
	dialogueSvc, _ := system2.NewDialogueService(llmClient, &xdg.AgentIdentityConfig{Name: "hammer_agent"})
	defer dialogueSvc.Close()

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("hammer_%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "hammer_agent",
		ClanID:          "pioneers",
		Resolver:        resolver,
		SocketPath:      sockPath,
		BridgeClient:    client,
		DialogueService: dialogueSvc,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case err := <-runErrCh:
		t.Fatalf("runtime exited prematurely: %v", err)
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge connect")
	}

	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake")
	}

	var sendWg sync.WaitGroup
	const numSenders = 30
	for i := 0; i < numSenders; i++ {
		sendWg.Add(1)
		go func(idx int) {
			defer sendWg.Done()
			_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
				WorldTick: int64(100 + idx),
				IncomingChats: []*civquestv1.ChatEvent{
					{
						SenderUuid: fmt.Sprintf("player-%d", idx),
						SenderName: "Rapid",
						Message:    "Hello rapid",
					},
				},
			})
		}(i)
	}

	// Concurrently stop while frames are being dispatched
	time.Sleep(2 * time.Millisecond)
	rt.Stop()

	sendWg.Wait()
	_ = <-runErrCh
}

func TestRuntime_TradeDebounceTracking(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("trade_debounce_agent")

	var callCount atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			Choices: []system2.ChatCompletionChoice{
				{
					Message: system2.ChatMessage{
						Role:    system2.RoleAssistant,
						Content: `{"spoken_reply":"Accepted","trade_decision":"ACCEPT","counter_offer_items":[],"prestige_delta":1.0}`,
					},
				},
			},
		})
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	llmClient, _ := system2.NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: srv.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 5, RequestTimeoutSeconds: 2},
	)
	defer llmClient.Close()
	dialogueSvc, _ := system2.NewDialogueService(llmClient, &xdg.AgentIdentityConfig{Name: "trade_debounce_agent"})
	defer dialogueSvc.Close()

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("tr_deb_%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "trade_debounce_agent",
		ClanID:          "pioneers",
		Resolver:        resolver,
		SocketPath:      sockPath,
		BridgeClient:    client,
		DialogueService: dialogueSvc,
	})
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case err := <-runErrCh:
		t.Fatalf("runtime exited prematurely: %v", err)
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge connect")
	}

	// Wait for handshake
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake")
	}

	// 1. Initial trade proposal at tick 100 for player-A
	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 1,
			PlayerUuid:     "player-A",
			PlayerName:     "PlayerA",
			SpokenProposal: "Trade wheat",
		},
	}
	if err := mockSvc.SendFrame(frame1); err != nil {
		t.Fatalf("failed sending frame1: %v", err)
	}

	// Wait for trade response
	select {
	case intent := <-mockSvc.intentCh:
		if intent.GetTradeResponse() == nil {
			t.Fatalf("expected trade response, got %T", intent.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trade response 1")
	}

	countAfterFirst := callCount.Load()
	if countAfterFirst != 1 {
		t.Fatalf("expected 1 LLM call, got %d", countAfterFirst)
	}

	// 2. Duplicate trade proposal at tick 120 (worldTick - lastTradeTick = 20 < 100) -> DEBOUNCED
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick: 120,
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 1,
			PlayerUuid:     "player-A",
			PlayerName:     "PlayerA",
			SpokenProposal: "Trade wheat",
		},
	}
	if err := mockSvc.SendFrame(frame2); err != nil {
		t.Fatalf("failed sending frame2: %v", err)
	}

	// Brief pause to allow processing
	time.Sleep(100 * time.Millisecond)
	if callCount.Load() != 1 {
		t.Errorf("expected debounced trade to NOT make LLM call, got %d calls", callCount.Load())
	}

	// 3. Different player (player-B) at tick 130 -> NOT debounced
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick: 130,
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 2,
			PlayerUuid:     "player-B",
			PlayerName:     "PlayerB",
			SpokenProposal: "Trade iron",
		},
	}
	if err := mockSvc.SendFrame(frame3); err != nil {
		t.Fatalf("failed sending frame3: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		if intent.GetTradeResponse() == nil {
			t.Fatalf("expected trade response, got %T", intent.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trade response 3")
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 LLM calls after player-B, got %d", callCount.Load())
	}

	// 4. Player-A again at tick 250 (tick 250 - tick 100 = 150 >= 100) -> NOT debounced
	frame4 := &civquestv1.TelemetryFrame{
		WorldTick: 250,
		PendingTrade: &civquestv1.TradeRequest{
			PlayerEntityId: 1,
			PlayerUuid:     "player-A",
			PlayerName:     "PlayerA",
			SpokenProposal: "Trade wheat again",
		},
	}
	if err := mockSvc.SendFrame(frame4); err != nil {
		t.Fatalf("failed sending frame4: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		if intent.GetTradeResponse() == nil {
			t.Fatalf("expected trade response, got %T", intent.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trade response 4")
	}

	if callCount.Load() != 3 {
		t.Errorf("expected 3 LLM calls after player-A at tick 250, got %d", callCount.Load())
	}

	rt.Stop()
	cancel()
	_ = <-runErrCh
}

func TestRuntime_System1_DailyUtilityAndBiologicalNeeds(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("needs_agent")

	agentCfg := &xdg.AgentIdentityConfig{
		Name:           "needs_agent",
		Profession:     "Farmer",
		SecondaryHobby: "APIARIST",
	}

	cfg := agent.Config{
		AgentID:        "needs_agent",
		ClanID:         "pioneers",
		IdentityConfig: agentCfg,
		Resolver:       resolver,
		BridgeClient:   client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	bedPos := &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}
	rt.SetAssignedBed(bedPos)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// 1. Critical hunger trigger at midday (time_of_day = 4000, food_level = 5)
	hungerFrame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 4000,
		Status: &civquestv1.AgentStatus{
			AgentId:   "needs_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 5, // Critical hunger <= 6
			Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:cooked_beef", Count: 5, Slot: 3},
			},
		},
	}
	if err := mockSvc.SendFrame(hungerFrame); err != nil {
		t.Fatalf("failed sending hungerFrame: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		useItem := intent.GetUseItem()
		if useItem == nil || useItem.InventorySlot != 3 || !useItem.IsEatingOrDrinking {
			t.Fatalf("expected eating UseItemIntent for cooked beef in slot 3, got %+v", intent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hunger UseItem intent")
	}

	// 2. Bedtime navigation trigger at night (time_of_day = 14000, agent at 50, 64, 50, far from bed 10, 64, 10)
	nightFarFrame := &civquestv1.TelemetryFrame{
		WorldTick: 200,
		TimeOfDay: 14000,
		Status: &civquestv1.AgentStatus{
			AgentId:   "needs_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
		},
	}
	if err := mockSvc.SendFrame(nightFarFrame); err != nil {
		t.Fatalf("failed sending nightFarFrame: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		nav := intent.GetNavigate()
		if nav == nil || nav.Target == nil {
			t.Fatalf("expected NavigateIntent to bed, got %+v", intent)
		}
		if nav.Target.X != 10.5 || nav.Target.Z != 10.5 {
			t.Errorf("expected bed target (10.5, 64, 10.5), got %+v", nav.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bed NavigateIntent")
	}

	// 3. Close to bed at night (agent at 11, 64, 10) -> SLEEP_IN_BED
	nightCloseFrame := &civquestv1.TelemetryFrame{
		WorldTick: 210,
		TimeOfDay: 14000,
		Status: &civquestv1.AgentStatus{
			AgentId:   "needs_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 11.0, Y: 64, Z: 10.0}, // dist = 1.0 block
		},
	}
	if err := mockSvc.SendFrame(nightCloseFrame); err != nil {
		t.Fatalf("failed sending nightCloseFrame: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			t.Fatalf("expected SLEEP_IN_BED intent, got %+v", intent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SLEEP_IN_BED intent")
	}

	// 4. Dawn awakening (time_of_day = 23600, IsSleeping = true) -> WAKE_UP
	dawnFrame := &civquestv1.TelemetryFrame{
		WorldTick: 300,
		TimeOfDay: 23600,
		Status: &civquestv1.AgentStatus{
			AgentId:    "needs_agent",
			Health:     20.0,
			MaxHealth:  20.0,
			FoodLevel:  20,
			Position:   &civquestv1.Vec3{X: 10.0, Y: 64, Z: 10.0},
			IsSleeping: true,
		},
	}
	if err := mockSvc.SendFrame(dawnFrame); err != nil {
		t.Fatalf("failed sending dawnFrame: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_WAKE_UP {
			t.Fatalf("expected WAKE_UP intent at dawn, got %+v", intent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WAKE_UP intent")
	}

	rt.Stop()
	cancel()
	_ = <-runErrCh
}

func TestRuntime_System1_RoadPavingIntegration(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	_ = resolver.InitAgentDirectories("paving_agent")

	cfg := agent.Config{
		AgentID:      "paving_agent",
		ClanID:       "pioneers",
		Resolver:     resolver,
		BridgeClient: client,
	}

	rt, err := agent.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("failed creating runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Move back and forth across (10.5, 64, 10.5) [below is (10, 63, 10)] and (11.5, 64, 10.5)
	framePos1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 2000,
		Status: &civquestv1.AgentStatus{
			AgentId:   "paving_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 10}, BlockId: "minecraft:grass_block"},
		},
	}

	framePos2 := &civquestv1.TelemetryFrame{
		WorldTick: 105,
		TimeOfDay: 2005,
		Status: &civquestv1.AgentStatus{
			AgentId:   "paving_agent",
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 11.5, Y: 64.0, Z: 10.5},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 63, Z: 10}, BlockId: "minecraft:grass_block"},
		},
	}

	// Step 1 on (10, 63, 10)
	if err := mockSvc.SendFrame(framePos1); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}
	// Step 1 on (11, 63, 10)
	if err := mockSvc.SendFrame(framePos2); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}
	// Step 2 on (10, 63, 10)
	framePos1.WorldTick = 110
	if err := mockSvc.SendFrame(framePos1); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}
	// Step 2 on (11, 63, 10)
	framePos2.WorldTick = 115
	if err := mockSvc.SendFrame(framePos2); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}
	// Step 3 on (10, 63, 10) -> Should trigger PAVE_ROAD_PATH!
	framePos1.WorldTick = 120
	if err := mockSvc.SendFrame(framePos1); err != nil {
		t.Fatalf("failed sending frame: %v", err)
	}

	select {
	case intent := <-mockSvc.intentCh:
		interact := intent.GetInteractBlock()
		if interact == nil || interact.InteractionType != civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
			t.Fatalf("expected PAVE_ROAD_PATH intent, got %+v", intent)
		}
		if interact.Pos.X != 10 || interact.Pos.Y != 63 || interact.Pos.Z != 10 {
			t.Fatalf("expected pave pos (10, 63, 10), got %+v", interact.Pos)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PAVE_ROAD_PATH intent")
	}

	rt.Stop()
	cancel()
	_ = <-runErrCh
}

func TestRuntime_System2_ClanChroniclerBookSynthesis(t *testing.T) {
	mockSvc, client, cleanupBridge := setupPermadeathMockBridge(t)
	defer cleanupBridge()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "chronicler_agent"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}
	defer agentDB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed high-saliency event
	memID, err := agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
		WorldTick:      5000,
		EventCategory:  "DISCOVERY",
		SaliencyScore:  0.95,
		SummaryText:    "Discovered the Ancient Ruins of the First Settlers.",
		ActorsJSON:     `["chronicler_agent"]`,
		LocationCoords: "50.0,64.0,50.0",
		Chronicled:     false,
	}, nil)
	if err != nil {
		t.Fatalf("failed inserting memory: %v", err)
	}

	chroniclerSvc := system2.NewChroniclerService(nil) // Deterministic synthesis fallback

	agentCfg := xdg.DefaultAgentIdentityConfig(agentID)
	agentCfg.Profession = "CHRONICLER"
	agentCfg.ClanID = "pioneers"

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:           agentID,
		ClanID:            "pioneers",
		IdentityConfig:    agentCfg,
		Resolver:          resolver,
		AgentDB:           agentDB,
		BridgeClient:      client,
		ChroniclerService: chroniclerSvc,
	})
	if err != nil {
		t.Fatalf("failed creating agent runtime: %v", err)
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// Send dusk frame with lectern in interesting blocks
	duskFrame := &civquestv1.TelemetryFrame{
		WorldTick: 5500,
		TimeOfDay: 12000, // Dusk
		Status: &civquestv1.AgentStatus{
			AgentId:   agentID,
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 5.0, Y: 64.0, Z: 5.0},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				Pos:     &civquestv1.BlockPosProto{X: 5, Y: 64, Z: 6},
				BlockId: "minecraft:lectern",
			},
		},
	}

	if err := mockSvc.SendFrame(duskFrame); err != nil {
		t.Fatalf("failed sending dusk frame: %v", err)
	}

	// Await CreateWrittenBookIntent
	select {
	case intent := <-mockSvc.intentCh:
		createBook := intent.GetCreateBook()
		if createBook == nil {
			t.Fatalf("expected CreateWrittenBookIntent, got %+v", intent)
		}
		if createBook.Title != "Chronicle of Pioneers" {
			t.Errorf("expected title 'Chronicle of Pioneers', got %q", createBook.Title)
		}
		if createBook.Author != agentID {
			t.Errorf("expected author %s, got %s", agentID, createBook.Author)
		}
		if len(createBook.Pages) == 0 {
			t.Errorf("expected non-empty pages in book")
		}
		if createBook.TargetLecternOrShelf == nil {
			t.Fatalf("expected non-nil TargetLecternOrShelf")
		}
		if createBook.TargetLecternOrShelf.X != 5 || createBook.TargetLecternOrShelf.Y != 64 || createBook.TargetLecternOrShelf.Z != 6 {
			t.Errorf("unexpected target lectern coords: %+v", createBook.TargetLecternOrShelf)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for CreateWrittenBookIntent")
	}

	// Verify memory was marked as chronicled
	mem, err := agentDB.GetEpisodicMemory(ctx, memID)
	if err != nil || mem == nil || !mem.Chronicled {
		t.Errorf("expected episodic memory to be marked chronicled in DB")
	}

	rt.Stop()
	cancel()
	_ = <-runErrCh
}

func TestRuntime_System2_ToponymyLandmarkNamingAndSignpost(t *testing.T) {
	mockSvc, client, cleanupBridge := setupPermadeathMockBridge(t)
	defer cleanupBridge()

	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "scout_agent"
	clanID := "explorers"

	_ = resolver.InitAgentDirectories(agentID)
	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}
	defer agentDB.Close()

	landmarksDB, err := db.OpenLandmarksDBForClan(resolver, clanID)
	if err != nil {
		t.Fatalf("failed opening landmarks DB: %v", err)
	}
	defer landmarksDB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	toponymySvc := system2.NewToponymyService(nil) // Deterministic fallback

	agentCfg := xdg.DefaultAgentIdentityConfig(agentID)
	agentCfg.Profession = "EXPLORER"
	agentCfg.ClanID = clanID

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         agentID,
		ClanID:          clanID,
		IdentityConfig:  agentCfg,
		Resolver:        resolver,
		AgentDB:         agentDB,
		LandmarksDB:     landmarksDB,
		BridgeClient:    client,
		ToponymyService: toponymySvc,
	})
	if err != nil {
		t.Fatalf("failed creating agent runtime: %v", err)
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Start(ctx)
	}()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream readiness")
	}

	// Drain handshake intent
	select {
	case <-mockSvc.intentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake intent")
	}

	// 1. Direct TriggerToponymy test
	triggerReq := system2.ToponymRequest{
		AgentID:  agentID,
		ClanID:   clanID,
		Category: db.LandmarkCategoryMemorial,
		Coordinates: system2.Vec3Coords{
			X: 25.0,
			Y: 64.0,
			Z: -40.0,
		},
		ContextPrompt: "Fallen scout memorial cairn",
		WorldTick:     6000,
	}

	res, err := rt.TriggerToponymy(ctx, triggerReq)
	if err != nil {
		t.Fatalf("TriggerToponymy failed: %v", err)
	}
	if len([]rune(res.DisplayName)) > system2.MaxToponymNameLen {
		t.Errorf("DisplayName %q exceeds %d runes", res.DisplayName, system2.MaxToponymNameLen)
	}
	for _, line := range []string{res.Line1, res.Line2, res.Line3, res.Line4} {
		if len([]rune(line)) > system2.MaxSignpostLineLen {
			t.Errorf("Sign line %q exceeds %d runes", line, system2.MaxSignpostLineLen)
		}
	}

	// Verify intent on bridge
	select {
	case intent := <-mockSvc.intentCh:
		sign := intent.GetPlaceSignpost()
		if sign == nil {
			t.Fatalf("expected PlaceSignpostIntent, got %+v", intent)
		}
		if sign.Pos.X != 25 || sign.Pos.Y != 64 || sign.Pos.Z != -40 {
			t.Errorf("unexpected sign pos: %+v", sign.Pos)
		}
		if sign.Line1 != res.Line1 {
			t.Errorf("expected line1 %q, got %q", res.Line1, sign.Line1)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PlaceSignpostIntent on bridge")
	}

	// Verify landmark in landmarks.db
	lm, err := landmarksDB.GetLandmark(res.LandmarkID)
	if err != nil || lm == nil {
		t.Fatalf("expected landmark %q in landmarks.db: %v", res.LandmarkID, err)
	}
	if lm.Category != db.LandmarkCategoryMemorial {
		t.Errorf("expected category MEMORIAL, got %s", lm.Category)
	}

	// 2. Exploration Telemetry Frame Trigger
	exploreFrame := &civquestv1.TelemetryFrame{
		WorldTick: 7000,
		BiomeId:   "minecraft:cherry_grove",
		Status: &civquestv1.AgentStatus{
			AgentId:   agentID,
			Health:    20.0,
			MaxHealth: 20.0,
			FoodLevel: 20,
			Position:  &civquestv1.Vec3{X: 180.0, Y: 72.0, Z: 240.0},
		},
	}

	if err := mockSvc.SendFrame(exploreFrame); err != nil {
		t.Fatalf("failed sending explore frame: %v", err)
	}

	// Await exploration signpost intent
	select {
	case intent := <-mockSvc.intentCh:
		sign := intent.GetPlaceSignpost()
		if sign == nil {
			t.Fatalf("expected PlaceSignpostIntent from exploration, got %+v", intent)
		}
		if sign.Pos.X != 180 || sign.Pos.Y != 72 || sign.Pos.Z != 240 {
			t.Errorf("unexpected exploration sign pos: %+v", sign.Pos)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for exploration PlaceSignpostIntent")
	}

	// Verify nearest landmark in DB
	nearest, err := landmarksDB.FindNearestLandmark(180.0, 72.0, 240.0, "")
	if err != nil || nearest == nil {
		t.Fatalf("expected nearest landmark to be found in landmarks.db: %v", err)
	}
	if nearest.X != 180.0 || nearest.Z != 240.0 {
		t.Errorf("unexpected nearest landmark coords: (%.1f, %.1f)", nearest.X, nearest.Z)
	}

	rt.Stop()
	cancel()
	_ = <-runErrCh
}
