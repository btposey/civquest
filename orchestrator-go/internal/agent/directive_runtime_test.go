package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestRuntime_DaytimeIdleContemplation_TriggersAndPreservesNighttime(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	var callCount atomic.Int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		action := system2.DirectiveMineStone
		goal := "MINE_STONE: Gather coal and stone from ridge"
		if n > 1 {
			action = system2.DirectiveScoutFrontier
			goal = "SCOUT_FRONTIER: Chart eastern valley at dawn"
		}
		payload, _ := json.Marshal(system2.StrategicContemplationResponse{
			ImmediateGoal:    goal,
			ShortTermGoal:    "EXPAND_OUTPOST",
			LongTermGoal:     "FOUND_KINGDOM",
			ReflectiveMemory: "Idle reflection produced a new directive.",
			Directive: &system2.StrategicDirective{
				Action: action,
				Target: "eastern ridge",
				Reason: "idle daytime deliberation",
			},
			Rationale: "Need productive agenda.",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "idle-contemplate",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))
	defer llmServer.Close()

	llmClient, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: llmServer.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(llmServer.Client()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer llmClient.Close()

	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentDB, err := db.OpenAgentDB(r.AgentDataDir("aria_idle"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:              "aria_idle",
		ClanID:               "pioneers",
		IdentityConfig:       xdg.DefaultAgentIdentityConfig("aria_idle"),
		Resolver:             r,
		AgentDB:              agentDB,
		BridgeClient:         bridgeClient,
		SocketPath:           filepath.Join(tmpDir, "aria_idle.sock"),
		ContemplationService: system2.NewContemplationService(llmClient),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Status() == "RUNNING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. Daytime idle frames before 300 ticks -> should NOT trigger contemplation
	for _, tick := range []int64{1000, 1200, 1299} {
		_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "aria_idle",
				Health:          20,
				FoodLevel:       20,
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		})
	}
	time.Sleep(80 * time.Millisecond)
	if callCount.Load() != 0 {
		t.Fatalf("expected 0 contemplation calls before 300 idle ticks, got %d", callCount.Load())
	}

	// 2. Tick 1300 (300 idle ticks elapsed) -> triggers daytime idle contemplation & publishes directive
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 1300,
		TimeOfDay: 4300,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria_idle",
			Health:          20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 0.2, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})

	waitDeadline := time.Now().Add(3 * time.Second)
	var dir system1.StrategicDirectiveState
	var active bool
	for time.Now().Before(waitDeadline) {
		dir, active = rt.System1State().ActiveStrategicDirective(1300)
		if active {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !active || dir.Action != system2.DirectiveMineStone {
		t.Fatalf("expected active MINE_STONE directive published to system1State, got active=%v dir=%+v", active, dir)
	}
	if dir.ExpiresTick != 7300 {
		t.Fatalf("expected ExpiresTick=7300 (6000-tick TTL), got %d", dir.ExpiresTick)
	}
	if rt.LastContemplatedDay() != -1 {
		t.Fatalf("expected daytime idle contemplation NOT to suppress nighttime day stamp, got %d", rt.LastContemplatedDay())
	}

	// 3. Clear directive and remain idle within 3600-tick cooldown -> must NOT re-trigger
	rt.System1State().ClearStrategicDirective()
	for _, tick := range []int64{1400, 1800, 2200} {
		_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 5000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "aria_idle",
				Health:          20,
				FoodLevel:       20,
				Position:        &civquestv1.Vec3{X: 0.2, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		})
	}
	time.Sleep(80 * time.Millisecond)
	if callCount.Load() != 1 {
		t.Fatalf("expected 3600-tick cooldown to block second daytime idle contemplation, got %d calls", callCount.Load())
	}

	// 4. Nighttime on Day 0 still fires normally!
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 14000,
		TimeOfDay: 14000,
		Status: &civquestv1.AgentStatus{
			AgentId:         "aria_idle",
			Health:          20,
			FoodLevel:       20,
			IsSleeping:      true,
			Position:        &civquestv1.Vec3{X: 0.2, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	})

	nightDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(nightDeadline) {
		if callCount.Load() >= 2 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected nighttime contemplation to still fire after daytime idle deliberation, got %d", callCount.Load())
	}
	if rt.LastContemplatedDay() != 0 {
		t.Fatalf("expected LastContemplatedDay=0 after nighttime contemplation, got %d", rt.LastContemplatedDay())
	}
}

func TestRuntime_PeerChatContention_GlobalCooldownMemoryAndHumanPriority(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	var mu sync.Mutex
	var llmCallers []string
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		joined := ""
		for _, m := range req.Messages {
			joined += m.Content + "\n"
		}
		mu.Lock()
		llmCallers = append(llmCallers, joined)
		mu.Unlock()

		payload, _ := json.Marshal(system2.DialogueResponse{
			SpokenReply:   "Understood, staying alert.",
			TradeDecision: "NONE",
			PrestigeDelta: 0.1,
			MemorySummary: "Replied to speaker",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "peer-contention",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))
	defer llmServer.Close()

	llmClient, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: llmServer.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(llmServer.Client()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer llmClient.Close()

	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentDB, err := db.OpenAgentDB(r.AgentDataDir("aria_chat"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	cfg := xdg.DefaultAgentIdentityConfig("aria_chat")
	diagSvc, err := system2.NewDialogueService(llmClient, cfg)
	if err != nil {
		t.Fatalf("NewDialogueService: %v", err)
	}
	defer diagSvc.Close()

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:         "aria_chat",
		ClanID:          "pioneers",
		IdentityConfig:  cfg,
		Resolver:        r,
		AgentDB:         agentDB,
		BridgeClient:    bridgeClient,
		SocketPath:      filepath.Join(tmpDir, "aria_chat.sock"),
		DialogueService: diagSvc,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.SetPeerReplyJitterDisabled(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Status() == "RUNNING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. Directly addressed peer chat from Bran -> allowed (LLM call #1)
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 1000,
		TimeOfDay: 4000,
		Status:    &civquestv1.AgentStatus{AgentId: "aria_chat", Health: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0}},
		IncomingChats: []*civquestv1.ChatEvent{{
			SenderName: "Bran",
			SenderUuid: agent.OfflinePlayerUUID("Bran"),
			Message:    "Hey aria_chat, I am chopping oak trees.",
			Distance:   5,
		}},
	})
	waitCalls := func(want int) {
		dl := time.Now().Add(3 * time.Second)
		for time.Now().Before(dl) {
			mu.Lock()
			got := len(llmCallers)
			mu.Unlock()
			if got >= want {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	waitCalls(1)
	mu.Lock()
	if len(llmCallers) != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 LLM call after direct peer address, got %d", len(llmCallers))
	}
	mu.Unlock()

	// 2. Second peer Cora directly addresses Aria 10s later (200 ticks < 600-tick global cooldown):
	//    LLM reply MUST be suppressed, while Cora's statement IS ingested into episodic memory!
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 1200,
		TimeOfDay: 4200,
		Status:    &civquestv1.AgentStatus{AgentId: "aria_chat", Health: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0}},
		IncomingChats: []*civquestv1.ChatEvent{{
			SenderName: "Cora",
			SenderUuid: agent.OfflinePlayerUUID("Cora"),
			Message:    "aria_chat, heading to mine coal east.",
			Distance:   6,
		}},
	})
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	if len(llmCallers) != 1 {
		mu.Unlock()
		t.Fatalf("expected global 30s cooldown to suppress Cora LLM reply, got %d calls", len(llmCallers))
	}
	mu.Unlock()

	// 3. Human player Steve speaks immediately at tick 1210 -> NEVER throttled by peer contention!
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 1210,
		TimeOfDay: 4210,
		Status:    &civquestv1.AgentStatus{AgentId: "aria_chat", Health: 20, FoodLevel: 20, Position: &civquestv1.Vec3{X: 0, Y: 64, Z: 0}},
		IncomingChats: []*civquestv1.ChatEvent{{
			SenderName: "Steve",
			SenderUuid: "11111111-1111-1111-1111-111111111111",
			Message:    "Status report please!",
			Distance:   4,
		}},
	})
	waitCalls(2)
	mu.Lock()
	gotCalls := len(llmCallers)
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("expected human player chat to bypass peer contention gates (2 total calls), got %d", gotCalls)
	}

	// Verify both Bran and Cora peer messages were deterministically logged into [PEER_DIALOGUE] episodic memory
	memories, err := agentDB.GetRecentMemories(context.Background(), 20)
	if err != nil {
		t.Fatalf("GetRecentMemories: %v", err)
	}
	var sawBran, sawCora bool
	for _, m := range memories {
		if strings.Contains(m.SummaryText, "[PEER_DIALOGUE]") && strings.Contains(m.SummaryText, "Bran") {
			sawBran = true
		}
		if strings.Contains(m.SummaryText, "[PEER_DIALOGUE]") && strings.Contains(m.SummaryText, "Cora") {
			sawCora = true
		}
	}
	if !sawBran || !sawCora {
		t.Fatalf("expected both Bran and Cora logged in [PEER_DIALOGUE] episodic memory (sawBran=%v sawCora=%v)", sawBran, sawCora)
	}
}

func TestRuntime_WorldTickZeroAndStalledActiveTaskContemplation(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	var callCount atomic.Int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		payload, _ := json.Marshal(system2.StrategicContemplationResponse{
			ImmediateGoal:    "SCOUT_FRONTIER: Abandon stalled task",
			ShortTermGoal:    "EXPLORE_OUTER_SECTOR",
			LongTermGoal:     "CHART_ATLAS",
			ReflectiveMemory: "Stalled at spawn, pivoting to scout.",
			Directive: &system2.StrategicDirective{
				Action: system2.DirectiveScoutFrontier,
				Target: "eastern woods",
				Reason: "stalled active task",
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "stalled-contemplate",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))
	defer llmServer.Close()

	llmClient, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: llmServer.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(llmServer.Client()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer llmClient.Close()

	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentDB, err := db.OpenAgentDB(r.AgentDataDir("carl_stalled"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:              "carl_stalled",
		ClanID:               "pioneers",
		IdentityConfig:       xdg.DefaultAgentIdentityConfig("carl_stalled"),
		Resolver:             r,
		AgentDB:              agentDB,
		BridgeClient:         bridgeClient,
		SocketPath:           filepath.Join(tmpDir, "carl_stalled.sock"),
		ContemplationService: system2.NewContemplationService(llmClient),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Status() == "RUNNING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. Handshake WorldTick=0 frame must NOT poison idleStartTick
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 0,
		TimeOfDay: 0,
		Status: &civquestv1.AgentStatus{
			AgentId:   "carl_stalled",
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
		},
	})

	// 2. Active task making 0 physical displacement across 299 ticks -> 0 calls before 300 ticks
	task := system1.NewTaskSubroutine(system1.TaskDescriptor{
		ID:              "stalled_task",
		Kind:            "harvest_tree",
		Stage:           system1.StageBootstrap,
		Routine:         system1.RoutineHarvestTree,
		ProgressTimeout: 1000,
		SelectSubTarget: func(_ *system1.AgentState, _ *civquestv1.TelemetryFrame, _ *system1.TaskSubroutine) (*civquestv1.Vec3, *civquestv1.BlockPosProto, string, bool) {
			return &civquestv1.Vec3{X: 20.5, Y: 64, Z: 0.5}, &civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0}, "minecraft:oak_log", true
		},
	}, 336500)
	rt.AgentFSM().StartTask(rt.System1State(), task)

	for _, tick := range []int64{336500, 336650, 336799} {
		_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 6000,
			Status: &civquestv1.AgentStatus{
				AgentId:         "carl_stalled",
				Health:          20,
				FoodLevel:       20,
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
			},
		})
	}
	time.Sleep(80 * time.Millisecond)
	if callCount.Load() != 0 {
		t.Fatalf("expected 0 contemplation calls before 300 ticks (WorldTick=0 must not poison idleStartTick), got %d", callCount.Load())
	}

	// 3. At tick 336800 (300 ticks of 0 displacement despite active task), daytime_idle fires
	_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 336800,
		TimeOfDay: 6300,
		Status: &civquestv1.AgentStatus{
			AgentId:         "carl_stalled",
			Health:          20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 0.2, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
		},
	})

	dl := time.Now().Add(3 * time.Second)
	for time.Now().Before(dl) && callCount.Load() == 0 {
		time.Sleep(15 * time.Millisecond)
	}
	if callCount.Load() != 1 {
		t.Fatalf("expected daytime_idle contemplation to trigger at 300 ticks of 0-progress active task, got %d", callCount.Load())
	}
}
