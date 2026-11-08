package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
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
