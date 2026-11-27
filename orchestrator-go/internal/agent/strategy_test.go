package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// cq-507.7: the night's proposal waits for dawn, is applied there, and the strategy outlives a
// restart.
func TestRuntime_NightProposalAppliesAtDawnAndSurvivesRestart(t *testing.T) {
	mockSvc, bridgeClient, cleanup := setupTestBridge(t)
	defer cleanup()

	var offered strings.Builder
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		offered.Write(body)
		payload, _ := json.Marshal(system2.StrategicContemplationResponse{
			ImmediateGoal:    "BUILD_SHELTER: Repair the home",
			StrategyProposal: &system2.StrategyProposal{Strategy: "settle", Reason: "the storehouse burned down"},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{Choices: []system2.ChatCompletionChoice{{
			Message: system2.ChatMessage{Role: "assistant", Content: string(payload)}, FinishReason: "stop",
		}}})
	}))
	defer llmServer.Close()
	llmClient, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: llmServer.URL + "/v1", Model: "mock-model"}},
		system2.WithHTTPClient(llmServer.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer llmClient.Close()

	dir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(dir))
	agentDB, err := db.OpenAgentDB(res.AgentDataDir("vera"))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()
	start := func(socket string) *agent.AgentRuntime {
		rt, err := agent.NewRuntime(agent.Config{
			AgentID: "vera", ClanID: "pioneers", IdentityConfig: xdg.DefaultAgentIdentityConfig("vera"),
			Resolver: res, AgentDB: agentDB, BridgeClient: bridgeClient, SocketPath: filepath.Join(dir, socket),
			ContemplationService: system2.NewContemplationService(llmClient),
		})
		if err != nil {
			t.Fatalf("NewRuntime: %v", err)
		}
		return rt
	}
	rt := start("a.sock")
	rt.System1State().SetCollectiveSettlementSite(0, 64, 0)
	rt.System1State().AssignStrategy(system1.StrategyProfession, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rt.Start(ctx) }()
	waitFor(t, "the runtime", func() bool { return rt.Status() == "RUNNING" })

	send := func(tick, tod int64, asleep bool) {
		_ = mockSvc.SendFrame(&civquestv1.TelemetryFrame{WorldTick: tick, TimeOfDay: tod, Status: &civquestv1.AgentStatus{
			AgentId: "vera", Health: 20, MaxHealth: 20, FoodLevel: 20, IsSleeping: asleep, Position: &civquestv1.Vec3{Y: 64},
		}})
	}
	send(90000, 18000, true)
	waitFor(t, "the proposal", func() bool { return rt.System1State().StrategyRecord().Proposal != nil })
	if got := rt.System1State().Strategy(); got != system1.StrategyProfession {
		t.Fatalf("the proposal was obeyed during the night: %s", got)
	}
	if !strings.Contains(offered.String(), "[LONG-RUNNING STRATEGY]") {
		t.Error("the model was not offered a choice of strategy")
	}
	send(96600, 600, false)
	waitFor(t, "the morning review", func() bool { return rt.System1State().Strategy() == system1.StrategySettle })
	waitFor(t, "the saved strategy", func() bool {
		row, err := agentDB.LoadStrategy(context.Background())
		return err == nil && row != nil && row.Kind == string(system1.StrategySettle) && row.Chosen
	})
	rt.Stop() // closes the database, as at shutdown

	if agentDB, err = db.OpenAgentDB(res.AgentDataDir("vera")); err != nil {
		t.Fatalf("reopening AgentDB: %v", err)
	}
	defer agentDB.Close()
	again := start("b.sock")
	if got := again.System1State().StrategyRecord(); got.Kind != system1.StrategySettle || got.Since != 96600 || !got.Chosen {
		t.Errorf("after restart %+v", got)
	}
}
