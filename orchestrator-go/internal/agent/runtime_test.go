package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
