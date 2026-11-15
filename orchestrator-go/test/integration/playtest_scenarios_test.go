//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/playtest"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// TestInWorldPlaytestScenarios_LiveServer executes the Automated In-World Headless Playtest
// Scenario Test Suite against the active NeoForge dedicated server with live AgentEntity avatars,
// Netty gRPC, Baritone navigation, and real Minecraft physics (cq-56x.5).
func TestInWorldPlaytestScenarios_LiveServer(t *testing.T) {
	discoveryTimeout := 5 * time.Second
	if tStr := os.Getenv("CIVQUEST_DISCOVERY_TIMEOUT"); tStr != "" {
		if d, err := time.ParseDuration(tStr); err == nil {
			discoveryTimeout = d
		}
	}

	// Strict isolation: Ensure integration tests only connect to the dedicated integration-server
	// and NEVER discover or collide with the developer's personal playtesting server in XDG paths.
	discoveryPath := os.Getenv("CIVQUEST_DISCOVERY_PATH")
	if discoveryPath == "" {
		candidate := filepath.Join("..", "..", "..", "mod-neoforge", "build", "integration-server", "civquest-server-meta.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			discoveryPath = abs
		}
	}

	t.Logf("Waiting for live server discovery metadata at %s (timeout %v)...", discoveryPath, discoveryTimeout)
	meta, err := bridge.WaitForDiscovery(discoveryPath, discoveryTimeout)
	if err != nil {
		if os.Getenv("CIVQUEST_REQUIRE_LIVE_SERVER") != "true" && os.Getenv("CIVQUEST_DISCOVERY_PATH") == "" {
			t.Skipf("Skipping live server playtest scenario test: %v (run headless server via './gradlew runIntegrationServer' or set CIVQUEST_REQUIRE_LIVE_SERVER=true)", err)
		}
		t.Fatalf("Failed to discover live server: %v (ensure headless server is running via './gradlew runIntegrationServer' or set CIVQUEST_DISCOVERY_PATH)", err)
	}

	t.Logf("Connecting playtest scenario suite to live server: addr=%s, transport=%s, pid=%d, key_set=%t",
		meta.Addr(), meta.Transport, meta.ServerPID, meta.SharedKey != "")

	client, err := bridge.NewClient(meta.Addr(), meta.SharedKey)
	if err != nil {
		t.Fatalf("Failed dialing live server gRPC bridge: %v", err)
	}
	defer client.Close()

	// Verify server health is SERVING
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hResp, err := client.HealthCheck(ctx)
	if err != nil || hResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("Live server bridge is not healthy: %v (status=%v)", err, hResp.GetStatus())
	}

	// Resolve output directory: default to build/playtest in repo root or fallback to temp dir
	repoRoot, err := filepath.Abs("../../../")
	outputDir := filepath.Join(repoRoot, "build", "playtest")
	if err != nil || repoRoot == "" {
		outputDir = filepath.Join(t.TempDir(), "build", "playtest")
	}
	reportPath := filepath.Join(outputDir, "playtest-report.md")

	runner, err := playtest.NewPlaytestRunner(playtest.RunnerConfig{
		OutputDir:        outputDir,
		ReportOutputPath: reportPath,
		ServerMeta:       meta,
		LiveClient:       client,
		UseLiveServer:    true,
		TotalWorldTicks:  24000,
	})
	if err != nil {
		t.Fatalf("Failed creating playtest runner: %v", err)
	}

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Playtest scenario execution failed: %v", err)
	}

	t.Logf("\n%s", summary.ReportMarkdown)

	// Verify all 5 critical in-game behavioral invariants passed
	for _, res := range summary.Results {
		if res.Status != playtest.StatusPassed {
			t.Errorf("FAIL [%s]: %s -> %v", res.Name, res.Description, res.Failures)
		} else {
			t.Logf("PASS [%s]: %s", res.Name, res.Description)
		}
	}

	if !summary.AllPassed {
		t.Fatalf("One or more in-world behavioral invariants failed verification")
	}

	// Verify generated artifacts exist and are non-empty
	assertTraceArtifactsExist(t, outputDir, reportPath)
}

// TestInWorldPlaytestScenarios_Synthetic runs the full multi-agent in-world playtest scenario suite
// over an in-process synthetic gRPC bridge server to guarantee deterministic CI validation anywhere.
func TestInWorldPlaytestScenarios_Synthetic(t *testing.T) {
	sharedKey := "synthetic-playtest-secret-key-9999"
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

	targetAddr := fmt.Sprintf("127.0.0.1:%d", tcpPort)
	client, err := bridge.NewClient(targetAddr, sharedKey)
	if err != nil {
		t.Fatalf("Failed creating bridge client for synthetic server: %v", err)
	}
	defer client.Close()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "playtest")
	reportPath := filepath.Join(outputDir, "playtest-report.md")

	meta := &bridge.ServerMeta{
		Status:      "READY",
		Transport:   "tcp",
		Host:        "127.0.0.1",
		Port:        tcpPort,
		ServerPID:   int64(os.Getpid()),
		SharedKey:   sharedKey,
		TimestampMs: time.Now().UnixMilli(),
	}

	runner, err := playtest.NewPlaytestRunner(playtest.RunnerConfig{
		OutputDir:        outputDir,
		ReportOutputPath: reportPath,
		ServerMeta:       meta,
		LiveClient:       client,
		UseLiveServer:    true,
		TotalWorldTicks:  24000,
	})
	if err != nil {
		t.Fatalf("Failed creating synthetic playtest runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Synthetic playtest scenario run failed: %v", err)
	}

	t.Logf("\n%s", summary.ReportMarkdown)

	for _, res := range summary.Results {
		if res.Status != playtest.StatusPassed {
			t.Errorf("FAIL [%s]: %s -> %v", res.Name, res.Description, res.Failures)
		} else {
			t.Logf("PASS [%s]: %s", res.Name, res.Description)
		}
	}

	if !summary.AllPassed {
		t.Fatalf("One or more synthetic in-world playtest invariants failed")
	}

	assertTraceArtifactsExist(t, outputDir, reportPath)
}

func assertTraceArtifactsExist(t *testing.T, outputDir, reportPath string) {
	t.Helper()

	// 1. Report file
	repFi, err := os.Stat(reportPath)
	if err != nil || repFi.Size() == 0 {
		t.Errorf("Expected non-empty playtest report at %s, got err=%v", reportPath, err)
	}

	// 2. Structured JSONL traces
	traces := []string{
		"puppeteering.jsonl",
		"cognitive_system1.jsonl",
		"cognitive_system2.jsonl",
		"playtest_trace.jsonl",
	}

	for _, traceName := range traces {
		p := filepath.Join(outputDir, traceName)
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			t.Errorf("Expected non-empty diagnostic trace %s, got err=%v", p, err)
		}
	}
}
