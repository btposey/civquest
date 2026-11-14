package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupMockAgent(t *testing.T, r *xdg.Resolver, name, clan string) {
	t.Helper()
	if err := os.MkdirAll(r.AgentsConfigDir(), 0o755); err != nil {
		t.Fatalf("failed creating agents config dir: %v", err)
	}

	cfg := &xdg.AgentIdentityConfig{
		Name:       name,
		ClanID:     clan,
		Profession: "FARMER",
		Skin: xdg.AgentSkinConfig{
			Model: "default",
		},
	}
	if err := r.SaveAgentConfig(cfg); err != nil {
		t.Fatalf("failed saving test agent config: %v", err)
	}
}

func writeMockScript(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed creating script dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed writing mock script: %v", err)
	}
}

func TestJSONLLogWriter(t *testing.T) {
	tmpDir := t.TempDir()
	logPath1 := filepath.Join(tmpDir, "logs", "aria.log")
	logPath2 := filepath.Join(tmpDir, "agents", "aria", "aria.log")

	writer, err := NewJSONLLogWriter("aria", 1234, logPath1, logPath2)
	if err != nil {
		t.Fatalf("failed creating JSONLLogWriter: %v", err)
	}

	// Concurrent writes
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stream := "stdout"
			level := "INFO"
			if idx%2 == 1 {
				stream = "stderr"
				level = "WARN"
			}
			msg := fmt.Sprintf("message %d from worker", idx)
			if err := writer.WriteLine(stream, level, msg); err != nil {
				t.Errorf("write error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if err := writer.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}

	// Verify entries in both log files
	for _, lp := range []string{logPath1, logPath2} {
		entries, err := ReadJSONLLogs(lp)
		if err != nil {
			t.Fatalf("failed reading JSONL logs from %s: %v", lp, err)
		}
		if len(entries) != 20 {
			t.Fatalf("expected 20 log entries in %s, got %d", lp, len(entries))
		}
		for _, e := range entries {
			if e.Agent != "aria" {
				t.Errorf("expected agent aria, got %s", e.Agent)
			}
			if e.PID != 1234 {
				t.Errorf("expected PID 1234, got %d", e.PID)
			}
			if e.Timestamp == "" {
				t.Error("expected non-empty timestamp")
			}
		}
	}
}

func TestCleanupStaleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	_ = os.MkdirAll(r.StateDir(), 0o750)
	_ = os.MkdirAll(r.AgentsStateDir(), 0o750)

	// Create stale supervisor.pid with non-existent PID
	supPIDPath := r.SupervisorPIDPath()
	if err := os.WriteFile(supPIDPath, []byte("9999999\n"), 0o644); err != nil {
		t.Fatalf("failed writing stale supervisor pid: %v", err)
	}

	// Create stale agent pid file
	ariaPIDPath := r.AgentPIDPath("aria")
	_ = os.MkdirAll(filepath.Dir(ariaPIDPath), 0o750)
	if err := os.WriteFile(ariaPIDPath, []byte("9999998\n"), 0o644); err != nil {
		t.Fatalf("failed writing stale agent pid: %v", err)
	}

	// Create stale socket file (no listener)
	ariaSockPath := r.AgentSocketPath("aria")
	if err := os.WriteFile(ariaSockPath, []byte("stale socket"), 0o644); err != nil {
		t.Fatalf("failed creating stale socket: %v", err)
	}

	// Create an active socket with an actual listener
	balinSockPath := r.AgentSocketPath("balin")
	listener, err := net.Listen("unix", balinSockPath)
	if err != nil {
		t.Fatalf("failed creating real listener on %s: %v", balinSockPath, err)
	}
	defer listener.Close()

	// Run cleanup
	if err := CleanupStaleFiles(r); err != nil {
		t.Fatalf("CleanupStaleFiles failed: %v", err)
	}

	// Assert stale supervisor PID was removed
	if _, err := os.Stat(supPIDPath); !os.IsNotExist(err) {
		t.Errorf("expected stale supervisor PID file to be removed, but it still exists")
	}

	// Assert stale agent PID was removed
	if _, err := os.Stat(ariaPIDPath); !os.IsNotExist(err) {
		t.Errorf("expected stale agent PID file to be removed, but it still exists")
	}

	// Assert stale socket was removed
	if _, err := os.Stat(ariaSockPath); !os.IsNotExist(err) {
		t.Errorf("expected stale socket to be removed, but it still exists")
	}

	// Assert active socket was NOT removed
	if _, err := os.Stat(balinSockPath); err != nil {
		t.Errorf("expected active socket to remain, but got error: %v", err)
	}
}

func TestSupervisor_Lifecycle_MultipleAgents(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	setupMockAgent(t, r, "aria", "pioneers")
	setupMockAgent(t, r, "balin", "pioneers")

	mockScriptPath := filepath.Join(tmpDir, "bin", "civquest-agent")
	mockScriptContent := `#!/usr/bin/env bash
AGENT_NAME=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            AGENT_NAME="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

echo "[$AGENT_NAME] Agent avatar starting up"
>&2 echo "[$AGENT_NAME] Standard error debug warning"
echo "[$AGENT_NAME] Telemetry loop active"

trap "echo '[$AGENT_NAME] Received SIGTERM, exiting cleanly'; exit 0" SIGTERM SIGINT

while true; do
    sleep 0.05
done
`
	writeMockScript(t, mockScriptPath, mockScriptContent)

	cfg := DefaultSupervisorConfig(r)
	cfg.AgentBinPath = mockScriptPath
	cfg.HealthCheckInterval = 100 * time.Millisecond
	cfg.ShutdownTimeout = 3 * time.Second

	sup, err := NewDaemonSupervisor(cfg)
	if err != nil {
		t.Fatalf("failed creating supervisor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatalf("failed starting supervisor: %v", err)
	}

	// 1. Verify supervisor PID file exists
	supPIDPath := r.SupervisorPIDPath()
	data, err := os.ReadFile(supPIDPath)
	if err != nil {
		t.Fatalf("supervisor.pid not found: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid != sup.PID() || pid <= 0 {
		t.Errorf("expected PID %d in supervisor.pid, got %d", sup.PID(), pid)
	}

	// 2. Poll status to ensure both workers have started
	var st *DaemonStatus
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		st, err = sup.Status()
		if err == nil && len(st.ManagedAgents) == 2 && st.ManagedAgents[0].Running && st.ManagedAgents[1].Running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if st == nil || len(st.ManagedAgents) != 2 {
		t.Fatalf("expected 2 managed agents, got %v", st)
	}

	ariaAgent := st.ManagedAgents[0]
	balinAgent := st.ManagedAgents[1]
	if ariaAgent.Name != "aria" {
		ariaAgent, balinAgent = balinAgent, ariaAgent
	}

	// Invariant 1: Process-Per-Agent Isolation
	if ariaAgent.PID == balinAgent.PID {
		t.Errorf("violation of Process-Per-Agent Isolation: both agents have same PID %d", ariaAgent.PID)
	}
	if ariaAgent.PID <= 0 || balinAgent.PID <= 0 {
		t.Errorf("invalid agent PIDs: aria=%d balin=%d", ariaAgent.PID, balinAgent.PID)
	}

	// Verify child PID files on disk
	for _, name := range []string{"aria", "balin"} {
		pPath := r.AgentPIDPath(name)
		pData, err := os.ReadFile(pPath)
		if err != nil {
			t.Errorf("missing agent PID file for %s at %s", name, pPath)
		} else {
			p, _ := strconv.Atoi(strings.TrimSpace(string(pData)))
			if p <= 0 {
				t.Errorf("invalid PID %d for agent %s in %s", p, name, pPath)
			}
		}
	}

	// 3. Verify structured JSONL logging
	time.Sleep(300 * time.Millisecond)
	for _, name := range []string{"aria", "balin"} {
		logPath := r.AgentHumanLogPath(name)
		entries, err := ReadJSONLLogs(logPath)
		if err != nil {
			t.Fatalf("failed reading structured JSONL logs for %s from %s: %v", name, logPath, err)
		}
		if len(entries) < 2 {
			t.Fatalf("expected at least 2 JSONL entries for %s, got %d", name, len(entries))
		}

		hasStdout := false
		hasStderr := false
		for _, e := range entries {
			if e.Agent != name {
				t.Errorf("expected agent %s in log entry, got %s", name, e.Agent)
			}
			if e.Stream == "stdout" && strings.Contains(e.Message, "Agent avatar starting up") {
				hasStdout = true
			}
			if e.Stream == "stderr" && strings.Contains(e.Message, "Standard error debug warning") {
				hasStderr = true
			}
		}
		if !hasStdout {
			t.Errorf("expected stdout message in structured JSONL log for %s", name)
		}
		if !hasStderr {
			t.Errorf("expected stderr message in structured JSONL log for %s", name)
		}
	}

	// 4. Graceful Shutdown
	if err := sup.Stop(); err != nil {
		t.Fatalf("failed stopping supervisor: %v", err)
	}

	// Verify supervisor PID file is cleaned up
	if _, err := os.Stat(supPIDPath); !os.IsNotExist(err) {
		t.Errorf("expected supervisor PID file to be removed after shutdown")
	}

	// Verify child processes terminated
	if IsProcessAlive(ariaAgent.PID) {
		t.Errorf("expected agent aria (PID %d) to have terminated", ariaAgent.PID)
	}
	if IsProcessAlive(balinAgent.PID) {
		t.Errorf("expected agent balin (PID %d) to have terminated", balinAgent.PID)
	}
}

func TestSupervisor_CrashAndRestart(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	setupMockAgent(t, r, "crashy", "pioneers")

	countFile := filepath.Join(tmpDir, "run_count")
	mockScriptPath := filepath.Join(tmpDir, "bin", "civquest-agent")
	mockScriptContent := fmt.Sprintf(`#!/usr/bin/env bash
COUNT_FILE=%q
COUNT=0
if [ -f "$COUNT_FILE" ]; then
    COUNT=$(cat "$COUNT_FILE")
fi
COUNT=$((COUNT + 1))
echo "$COUNT" > "$COUNT_FILE"

if [ "$COUNT" -eq 1 ]; then
    # First invocation: crash unexpectedly
    echo "Simulating unhandled panic / crash"
    sleep 0.05
    exit 1
else
    # Second invocation: remain alive
    echo "Restarted successfully"
    trap "exit 0" SIGTERM
    while true; do sleep 0.05; done
fi
`, countFile)
	writeMockScript(t, mockScriptPath, mockScriptContent)

	cfg := DefaultSupervisorConfig(r)
	cfg.AgentBinPath = mockScriptPath
	cfg.HealthCheckInterval = 50 * time.Millisecond
	cfg.CrashBackoff = 50 * time.Millisecond
	cfg.RestartOnCrash = true
	cfg.MaxRestarts = 3
	cfg.ShutdownTimeout = 2 * time.Second

	sup, err := NewDaemonSupervisor(cfg)
	if err != nil {
		t.Fatalf("failed creating supervisor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatalf("failed starting supervisor: %v", err)
	}
	defer func() { _ = sup.Stop() }()

	// Wait for restart
	restarted := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if data, err := os.ReadFile(countFile); err == nil {
			if cnt, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && cnt >= 2 {
				restarted = true
				break
			}
		}
	}

	if !restarted {
		t.Fatalf("expected supervisor to restart crashed agent worker")
	}

	st, err := sup.Status()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if len(st.ManagedAgents) != 1 {
		t.Fatalf("expected 1 managed agent, got %d", len(st.ManagedAgents))
	}
	if st.ManagedAgents[0].Restarts < 1 {
		t.Errorf("expected restarts >= 1, got %d", st.ManagedAgents[0].Restarts)
	}
}

func TestSupervisor_Permadeath_NoRestart(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	setupMockAgent(t, r, "mortal", "pioneers")

	mockScriptPath := filepath.Join(tmpDir, "bin", "civquest-agent")
	mockScriptContent := `#!/usr/bin/env bash
echo "Agent mortal running briefly"
sleep 0.05
exit 0
`
	writeMockScript(t, mockScriptPath, mockScriptContent)

	// Simulate permadeath by placing a graveyard record
	graveyardDir := r.GraveyardDir()
	_ = os.MkdirAll(graveyardDir, 0o755)
	archiveDir := filepath.Join(graveyardDir, "mortal_20261114T120000Z")
	_ = os.MkdirAll(archiveDir, 0o755)

	cfg := DefaultSupervisorConfig(r)
	cfg.AgentBinPath = mockScriptPath
	cfg.HealthCheckInterval = 50 * time.Millisecond
	cfg.RestartOnCrash = true
	cfg.ShutdownTimeout = 2 * time.Second

	sup, err := NewDaemonSupervisor(cfg)
	if err != nil {
		t.Fatalf("failed creating supervisor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatalf("failed starting supervisor: %v", err)
	}
	defer func() { _ = sup.Stop() }()

	time.Sleep(300 * time.Millisecond)

	st, err := sup.Status()
	if err != nil {
		t.Fatalf("failed getting status: %v", err)
	}

	// Deceased agent should not have been spawned or restarted
	for _, a := range st.ManagedAgents {
		if a.Name == "mortal" && a.Running {
			t.Errorf("permadeath invariant violation: deceased agent was started or restarted")
		}
	}
}

func TestSupervisor_DynasticReproductionIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	mockScriptPath := filepath.Join(tmpDir, "bin", "civquest-agent")
	mockScriptContent := `#!/usr/bin/env bash
echo "Agent $2 starting"
sleep 10
`
	writeMockScript(t, mockScriptPath, mockScriptContent)

	cfg := DefaultSupervisorConfig(r)
	cfg.AgentBinPath = mockScriptPath
	cfg.ClanID = "pioneers"
	cfg.HealthCheckInterval = 100 * time.Millisecond
	cfg.EnableReproduction = true
	cfg.ReproductionCheckInterval = 500 * time.Millisecond
	cfg.MaxActiveAgents = 16
	cfg.MinFoodSurplusDays = 7
	cfg.DailyFoodPerAgent = 3

	sup, err := NewDaemonSupervisor(cfg)
	if err != nil {
		t.Fatalf("failed creating supervisor: %v", err)
	}

	if sup.ReproductionGovernor() == nil {
		t.Fatalf("expected non-nil ReproductionGovernor")
	}

	ctx := context.Background()

	// Seed Parents
	setupMockAgent(t, r, "father", "pioneers")
	setupMockAgent(t, r, "mother", "pioneers")

	// Set up relationship databases for father and mother
	dbF, err := r.LoadAgentConfig("father")
	if err != nil {
		t.Fatalf("loading father: %v", err)
	}
	dbM, err := r.LoadAgentConfig("mother")
	if err != nil {
		t.Fatalf("loading mother: %v", err)
	}
	dbF.UUID = "00000000-0000-0000-0000-000000000001"
	dbM.UUID = "00000000-0000-0000-0000-000000000002"
	_ = r.SaveAgentConfig(dbF)
	_ = r.SaveAgentConfig(dbM)

	// Open agent DBs and record mutual marriage
	agentDBFather, err := db.OpenAgentDBForAgent(r, "father")
	if err != nil {
		t.Fatalf("opening father DB: %v", err)
	}
	_ = agentDBFather.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       dbM.UUID,
		EntityName:       "mother",
		EntityType:       "AGENT",
		DispositionScore: 85.0,
		KinshipRole:      "SPOUSE",
	})
	_ = agentDBFather.Close()

	agentDBMother, err := db.OpenAgentDBForAgent(r, "mother")
	if err != nil {
		t.Fatalf("opening mother DB: %v", err)
	}
	_ = agentDBMother.UpsertRelationship(ctx, db.Relationship{
		EntityUUID:       dbF.UUID,
		EntityName:       "father",
		EntityType:       "AGENT",
		DispositionScore: 90.0,
		KinshipRole:      "SPOUSE",
	})
	_ = agentDBMother.Close()

	// 1. Direct reproduction evaluation test via supervisor before civic setup:
	res, err := sup.EvaluateReproduction(ctx, "pioneers")
	if err != nil {
		t.Fatalf("EvaluateReproduction error: %v", err)
	}
	if res.Success {
		t.Errorf("expected reproduction to fail before granary/housing setup")
	}

	// 2. Setup granary food and residential zone
	civicDB, err := db.OpenCivicRegistryDBForClan(r, "pioneers")
	if err != nil {
		t.Fatalf("opening civic DB: %v", err)
	}
	_ = civicDB.AddGranaryStockContext(ctx, "minecraft:bread", 100, 100)
	_ = civicDB.RegisterZoneContext(ctx, &db.FunctionalZone{
		ZoneID:       "zone_cabin_1",
		ZoneType:     db.ZoneTypeResidential,
		MinX:         0,
		MinY:         64,
		MinZ:         0,
		MaxX:         10,
		MaxY:         70,
		MaxZ:         10,
		OwnerAgentID: "",
		Name:         "Homestead 1",
	})
	_ = civicDB.Close()

	// 3. Evaluate reproduction again via supervisor: should succeed!
	resSuccess, err := sup.EvaluateReproduction(ctx, "pioneers")
	if err != nil {
		t.Fatalf("EvaluateReproduction error: %v", err)
	}
	if !resSuccess.Success {
		t.Fatalf("expected reproduction to succeed, got reason: %s", resSuccess.CheckResult.Reason)
	}
	if resSuccess.ChildID == "" {
		t.Fatalf("expected child ID")
	}
	if resSuccess.Generation != 2 {
		t.Errorf("child generation = %d, want 2", resSuccess.Generation)
	}

	// 4. Verify child process was started via supervisor
	if !resSuccess.ProcessStarted {
		t.Errorf("expected child process to be started by supervisor")
	}
	defer func() {
		_ = sup.StopAgent(resSuccess.ChildID)
	}()

	st, err := sup.Status()
	if err != nil {
		t.Fatalf("getting status: %v", err)
	}
	foundChild := false
	for _, a := range st.ManagedAgents {
		if a.Name == resSuccess.ChildID {
			foundChild = true
			if a.PID <= 0 {
				t.Errorf("child agent PID was invalid: %d", a.PID)
			}
			break
		}
	}
	if !foundChild {
		t.Errorf("child %s not found in supervisor managed agents", resSuccess.ChildID)
	}
}
