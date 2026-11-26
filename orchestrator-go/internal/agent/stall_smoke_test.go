package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// cq-yn1.4 smoke: a started runtime fed telemetry over gRPC reports a frame that hangs.
func TestSmoke_HungBrainIsReported(t *testing.T) {
	mockSvc, client, cleanup := setupTestBridge(t)
	defer cleanup()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()))
	_ = resolver.InitAgentDirectories("aria_stall")

	reports := make(chan agent.StallReport, 1)
	rt, err := agent.NewRuntime(agent.Config{
		AgentID: "aria_stall", ClanID: "pioneers", Resolver: resolver, BridgeClient: client,
		StallAfter: 100 * time.Millisecond,
		OnStall:    func(r agent.StallReport) { reports <- r },
	})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	rt.SetFramePipeline(agent.NewFramePipeline(agent.FrameStage{
		Name: "hang", Run: func(*agent.AgentRuntime, *agent.FrameContext) { <-release },
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Start(ctx) }()
	defer func() { close(release); cancel(); <-done }()

	select {
	case <-mockSvc.streamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("stream never connected")
	}
	if err := mockSvc.SendFrame(&civquestv1.TelemetryFrame{
		WorldTick: 77, Status: &civquestv1.AgentStatus{AgentId: "aria_stall", Health: 20, Position: &civquestv1.Vec3{Y: 64}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-reports:
		if r.Tick != 77 || r.DumpPath == "" {
			t.Errorf("report = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hung frame never reported")
	}
}
