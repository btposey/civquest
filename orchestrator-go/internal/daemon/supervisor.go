package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/governor"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// LogEntry represents a structured JSONL log record captured from an agent worker.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Agent     string `json:"agent"`
	PID       int    `json:"pid,omitempty"`
	Stream    string `json:"stream"` // "stdout" or "stderr"
	Level     string `json:"level"`  // "INFO", "WARN", "ERROR"
	Message   string `json:"message"`
}

// JSONLLogWriter captures stdout/stderr streams and formats them as structured JSONL lines.
type JSONLLogWriter struct {
	mu    sync.Mutex
	agent string
	pid   int
	files []*os.File
}

// NewJSONLLogWriter opens or creates the specified log files and returns a JSONLLogWriter.
func NewJSONLLogWriter(agent string, pid int, paths ...string) (*JSONLLogWriter, error) {
	var files []*os.File
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			closeFiles(files)
			return nil, fmt.Errorf("creating directory for %s: %w", p, err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			closeFiles(files)
			return nil, fmt.Errorf("opening log file %s: %w", p, err)
		}
		files = append(files, f)
	}

	return &JSONLLogWriter{
		agent: agent,
		pid:   pid,
		files: files,
	}, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// SetPID updates the recorded process ID on log entries.
func (w *JSONLLogWriter) SetPID(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pid = pid
}

// WriteLine writes a single structured JSONL entry to all managed files.
func (w *JSONLLogWriter) WriteLine(stream, level, message string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Agent:     w.agent,
		PID:       w.pid,
		Stream:    stream,
		Level:     level,
		Message:   message,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	var firstErr error
	for _, f := range w.files {
		if _, err := f.Write(data); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PipeStream scans lines from r and writes each as a JSONL entry.
func (w *JSONLLogWriter) PipeStream(stream, level string, r io.Reader, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_ = w.WriteLine(stream, level, line)
	}
}

// Close closes all managed log files.
func (w *JSONLLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var firstErr error
	for _, f := range w.files {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	w.files = nil
	return firstErr
}

// ReadJSONLLogs reads all LogEntry lines from a JSONL log file.
func ReadJSONLLogs(logPath string) ([]LogEntry, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing jsonl line %q: %w", string(line), err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ManagedAgentStatus holds runtime health and telemetry information for an agent worker.
type ManagedAgentStatus struct {
	Name       string    `json:"name"`
	ClanID     string    `json:"clan_id"`
	PID        int       `json:"pid"`
	Running    bool      `json:"running"`
	Restarts   int       `json:"restarts"`
	StartTime  time.Time `json:"start_time,omitempty"`
	Uptime     string    `json:"uptime,omitempty"`
	SocketPath string    `json:"socket_path"`
	LogPath    string    `json:"log_path"`
	Deceased   bool      `json:"deceased"`
}

// DaemonStatus represents the overall supervisor daemon status.
type DaemonStatus struct {
	Running       bool                 `json:"running"`
	PID           int                  `json:"pid,omitempty"`
	StartTime     time.Time            `json:"start_time,omitempty"`
	Uptime        string               `json:"uptime,omitempty"`
	ManagedAgents []ManagedAgentStatus `json:"managed_agents"`
}

// SupervisorConfig specifies configuration parameters for DaemonSupervisor.
type SupervisorConfig struct {
	Resolver            *xdg.Resolver
	AgentBinPath        string
	ServerID            string
	ConfigDir           string
	DataDir             string
	StateDir            string
	GRPCAddr            string
	SharedKey           string
	ClanID              string
	AgentNames          []string
	HealthCheckInterval time.Duration
	RestartOnCrash      bool
	MaxRestarts         int
	CleanExitBackoff    time.Duration
	CrashBackoff        time.Duration
	ShutdownTimeout     time.Duration
	Out                       io.Writer
	ErrOut                    io.Writer
	CheckDeath                func(agentName string) bool
	EnableReproduction        bool
	ReproductionCheckInterval time.Duration
	ReproductionGovernor      *governor.ReproductionGovernor
	MaxActiveAgents           int
	MinFoodSurplusDays        int
	DailyFoodPerAgent         int
}

// DefaultSupervisorConfig returns standard supervisor configuration defaults.
func DefaultSupervisorConfig(r *xdg.Resolver) SupervisorConfig {
	if r == nil {
		r = xdg.DefaultResolver()
	}
	return SupervisorConfig{
		Resolver:                  r,
		HealthCheckInterval:       2 * time.Second,
		RestartOnCrash:            true,
		MaxRestarts:               5,
		CleanExitBackoff:          500 * time.Millisecond,
		CrashBackoff:              1 * time.Second,
		ShutdownTimeout:           5 * time.Second,
		Out:                       os.Stdout,
		ErrOut:                    os.Stderr,
		EnableReproduction:        true,
		ReproductionCheckInterval: 1 * time.Minute,
	}
}

// AgentWorker coordinates an individual agent OS worker process.
type AgentWorker struct {
	Name       string
	ClanID     string
	PID        int
	StartTime  time.Time
	Restarts   int
	Running    bool
	Restarting bool
	Deceased   bool
	LogPath    string
	SocketPath string
	Cmd        *exec.Cmd
	LogWriter  *JSONLLogWriter
	StdoutPipe *os.File
	StderrPipe *os.File
	LogWG      sync.WaitGroup
	ExitChan   chan int
	StopChan   chan struct{}
	mu         sync.RWMutex
}

// DaemonSupervisor orchestrates the lifecycle, health monitoring, and structured logging
// of process-isolated autonomous agent workers.
type DaemonSupervisor struct {
	resolver             *xdg.Resolver
	config               SupervisorConfig
	mu                   sync.RWMutex
	logMu                sync.Mutex
	workers              map[string]*AgentWorker
	pid                  int
	startTime            time.Time
	ctx                  context.Context
	cancel               context.CancelFunc
	done                 chan struct{}
	stopped              bool
	readyChan            chan struct{}
	reproductionGovernor *governor.ReproductionGovernor
}

// NewDaemonSupervisor creates a new supervisor instance.
func NewDaemonSupervisor(cfg SupervisorConfig) (*DaemonSupervisor, error) {
	if cfg.Resolver == nil {
		cfg.Resolver = xdg.DefaultResolver()
	}
	if cfg.HealthCheckInterval <= 0 {
		cfg.HealthCheckInterval = 2 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 5
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.ErrOut == nil {
		cfg.ErrOut = io.Discard
	}

	sup := &DaemonSupervisor{
		resolver:  cfg.Resolver,
		config:    cfg,
		workers:   make(map[string]*AgentWorker),
		done:      make(chan struct{}),
		readyChan: make(chan struct{}),
	}

	if cfg.EnableReproduction {
		reproGov := cfg.ReproductionGovernor
		if reproGov == nil {
			var err error
			reproGov, err = governor.NewReproductionGovernor(governor.ReproductionGovernorConfig{
				Resolver:           cfg.Resolver,
				MaxActiveAgents:    cfg.MaxActiveAgents,
				MinFoodSurplusDays: cfg.MinFoodSurplusDays,
				DailyFoodPerAgent:  cfg.DailyFoodPerAgent,
				ClanID:             cfg.ClanID,
			})
			if err != nil {
				return nil, fmt.Errorf("initializing reproduction governor: %w", err)
			}
		}
		reproGov.SetLauncher(sup)
		reproGov.SetActiveAgentsProvider(sup.ActiveAgentCount)
		sup.reproductionGovernor = reproGov
	}

	return sup, nil
}

// Ready returns a channel that is closed once the supervisor has initialized, cleaned stale files,
// written its PID file, and spawned the initial worker cohort.
func (s *DaemonSupervisor) Ready() <-chan struct{} {
	return s.readyChan
}

// Resolver returns the underlying XDG resolver.
func (s *DaemonSupervisor) Resolver() *xdg.Resolver {
	return s.resolver
}

// PID returns the supervisor PID.
func (s *DaemonSupervisor) PID() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pid
}

// CleanupStaleFiles safely cleans up stale supervisor PID files, child PID files,
// and dead UNIX domain sockets.
func CleanupStaleFiles(r *xdg.Resolver) error {
	if r == nil {
		r = xdg.DefaultResolver()
	}

	// 1. Clean stale supervisor PID file
	supPIDPath := r.SupervisorPIDPath()
	if data, err := os.ReadFile(supPIDPath); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 || !IsProcessAlive(pid) {
			_ = os.Remove(supPIDPath)
		}
	}

	// 2. Scan agents directory for stale sockets and PID files
	agentsStateDir := r.AgentsStateDir()
	entries, err := os.ReadDir(agentsStateDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".sock") {
				sockPath := filepath.Join(agentsStateDir, name)
				if IsSocketStale(sockPath) {
					_ = os.Remove(sockPath)
				}
			} else if entry.IsDir() {
				agentName := name
				pidPath := r.AgentPIDPath(agentName)
				if data, err := os.ReadFile(pidPath); err == nil {
					pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
					if err != nil || pid <= 0 || !IsProcessAlive(pid) {
						_ = os.Remove(pidPath)
					}
				}
			}
		}
	}

	return nil
}

// IsProcessAlive reports whether a process with the given PID is actively running.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// IsSocketStale reports whether a UNIX domain socket has no active listener.
func IsSocketStale(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return true
}

// Run starts the supervisor loop in the current thread and blocks until ctx is canceled or Stop() is called.
func (s *DaemonSupervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errors.New("supervisor already stopped")
	}
	s.pid = os.Getpid()
	s.startTime = time.Now()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()
	defer close(s.done)

	// Clean up any stale files from previous unclean shutdowns
	_ = CleanupStaleFiles(s.resolver)

	// Ensure state and logs directories exist
	if err := os.MkdirAll(s.resolver.StateDir(), 0o750); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	if err := os.MkdirAll(s.resolver.LogsDir(), 0o750); err != nil {
		return fmt.Errorf("creating logs dir: %w", err)
	}

	// Write supervisor PID file
	supPIDPath := s.resolver.SupervisorPIDPath()
	if err := os.WriteFile(supPIDPath, []byte(fmt.Sprintf("%d\n", s.pid)), 0o644); err != nil {
		return fmt.Errorf("writing supervisor pid file: %w", err)
	}
	defer func() {
		_ = os.Remove(supPIDPath)
	}()

	s.logSupervisorEvent("Supervisor daemon started (PID %d)", s.pid)

	// Discover and start configured agents
	if err := s.discoverAndStartAgents(); err != nil {
		s.logSupervisorEvent("Warning discovering agents: %v", err)
	}

	// Signal readiness
	select {
	case <-s.readyChan:
	default:
		close(s.readyChan)
	}

	// Health monitoring ticker loop
	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	// Dynastic reproduction governor ticker loop
	var reproTicker *time.Ticker
	var reproChan <-chan time.Time
	if s.config.EnableReproduction && s.config.ReproductionCheckInterval > 0 {
		reproTicker = time.NewTicker(s.config.ReproductionCheckInterval)
		defer reproTicker.Stop()
		reproChan = reproTicker.C
	}

	for {
		select {
		case <-s.ctx.Done():
			s.logSupervisorEvent("Supervisor shutdown signal received; stopping all managed workers...")
			s.shutdownWorkers()
			s.logSupervisorEvent("All managed workers stopped cleanly.")
			return nil

		case <-ticker.C:
			s.healthCheck()

		case <-reproChan:
			s.evaluateReproduction()
		}
	}
}

// Start launches Run in a background goroutine.
func (s *DaemonSupervisor) Start(ctx context.Context) error {
	var runErr error
	var errMu sync.Mutex
	started := make(chan struct{})

	go func() {
		close(started)
		if err := s.Run(ctx); err != nil {
			errMu.Lock()
			runErr = err
			errMu.Unlock()
		}
	}()

	<-started
	select {
	case <-s.readyChan:
		return nil
	case <-s.done:
		errMu.Lock()
		defer errMu.Unlock()
		return runErr
	case <-time.After(5 * time.Second):
		errMu.Lock()
		defer errMu.Unlock()
		if runErr != nil {
			return runErr
		}
		return errors.New("timed out waiting for supervisor startup")
	}
}

// Stop gracefully stops all child processes and halts the supervisor daemon.
func (s *DaemonSupervisor) Stop() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	select {
	case <-s.done:
		return nil
	case <-time.After(s.config.ShutdownTimeout + 2*time.Second):
		return errors.New("timed out waiting for supervisor shutdown")
	}
}

func (s *DaemonSupervisor) logSupervisorEvent(format string, args ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()

	msg := fmt.Sprintf("[%s] [supervisor] "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
	if s.config.Out != nil {
		_, _ = io.WriteString(s.config.Out, msg)
	}
	supLogPath := filepath.Join(s.resolver.LogsDir(), "supervisor.log")
	if f, err := os.OpenFile(supLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
		defer f.Close()
		_, _ = io.WriteString(f, msg)
	}
}

// discoverAndStartAgents identifies configured agents and spawns independent worker processes.
func (s *DaemonSupervisor) discoverAndStartAgents() error {
	status, err := domain.DetectAgents(s.resolver)
	if err != nil {
		return err
	}

	var candidates []domain.AgentSummary
	for _, a := range status.ConfiguredAgents {
		// Filter by clan if requested
		if s.config.ClanID != "" && !strings.EqualFold(a.ClanID, s.config.ClanID) {
			continue
		}
		// Filter by agent names if specified
		if len(s.config.AgentNames) > 0 {
			matched := false
			for _, n := range s.config.AgentNames {
				if strings.EqualFold(a.Name, n) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Invariant 4: Strict Permadeath Lifecycle. Never respawn an agent in the graveyard.
		if a.Deceased || s.isAgentDeceased(a.Name) {
			s.logSupervisorEvent("Agent %s is deceased (permadeath invariant prevents spawning)", a.Name)
			continue
		}

		candidates = append(candidates, a)
	}

	for _, c := range candidates {
		if err := s.StartAgent(c.Name); err != nil {
			s.logSupervisorEvent("Error starting agent worker %s: %v", c.Name, err)
		}
	}
	return nil
}

func (s *DaemonSupervisor) isAgentDeceased(name string) bool {
	if s.config.CheckDeath != nil && s.config.CheckDeath(name) {
		return true
	}
	graveyardDir := s.resolver.GraveyardDir()
	matches, globErr := filepath.Glob(filepath.Join(graveyardDir, fmt.Sprintf("%s_*", name)))
	return globErr == nil && len(matches) > 0
}

// StartAgent launches an OS worker process for the named agent.
func (s *DaemonSupervisor) StartAgent(agentName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isAgentDeceased(agentName) {
		return fmt.Errorf("agent %s is deceased; permadeath prevents spawning", agentName)
	}

	restarts := 0
	if w, exists := s.workers[agentName]; exists {
		w.mu.RLock()
		running := w.Running
		restarts = w.Restarts
		w.mu.RUnlock()
		if running {
			return fmt.Errorf("agent %s is already running (PID %d)", agentName, w.PID)
		}
	}

	binPath, err := resolveAgentBinary(s.resolver, s.config.AgentBinPath)
	if err != nil {
		return err
	}

	agentArgs := []string{"--name", agentName}
	if s.config.ServerID != "" {
		agentArgs = append(agentArgs, "--server-id", s.config.ServerID)
	}
	if s.config.GRPCAddr != "" {
		agentArgs = append(agentArgs, "--grpc-addr", s.config.GRPCAddr)
	}
	if s.config.SharedKey != "" {
		agentArgs = append(agentArgs, "--shared-key", s.config.SharedKey)
	}
	cfgPath := s.resolver.AgentConfigPath(agentName)
	if _, err := os.Stat(cfgPath); err == nil {
		agentArgs = append(agentArgs, "--config", cfgPath)
	}

	// Prepare structured JSONL logging
	humanLogPath := s.resolver.AgentHumanLogPath(agentName)
	agentStateLogPath := s.resolver.AgentLogPath(agentName)

	logWriter, err := NewJSONLLogWriter(agentName, 0, humanLogPath, agentStateLogPath)
	if err != nil {
		return fmt.Errorf("creating jsonl log writer for %s: %w", agentName, err)
	}

	// Create OS pipes for stdout and stderr to capture structured output
	stdoutPipeR, stdoutPipeW, err := os.Pipe()
	if err != nil {
		_ = logWriter.Close()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderrPipeR, stderrPipeW, err := os.Pipe()
	if err != nil {
		_ = stdoutPipeR.Close()
		_ = stdoutPipeW.Close()
		_ = logWriter.Close()
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	// Prepare child process environment with XDG base directory variables
	childEnv := os.Environ()
	if s.resolver.ConfigDir() != "" {
		childEnv = append(childEnv, "XDG_CONFIG_HOME="+filepath.Dir(s.resolver.ConfigDir()))
	}
	if s.resolver.DataDir() != "" {
		childEnv = append(childEnv, "XDG_DATA_HOME="+filepath.Dir(s.resolver.DataDir()))
	}
	if s.resolver.StateDir() != "" {
		childEnv = append(childEnv, "XDG_STATE_HOME="+filepath.Dir(s.resolver.StateDir()))
	}

	cmd := exec.Command(binPath, agentArgs...)
	cmd.Stdout = stdoutPipeW
	cmd.Stderr = stderrPipeW
	cmd.Env = childEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		_ = stdoutPipeR.Close()
		_ = stdoutPipeW.Close()
		_ = stderrPipeR.Close()
		_ = stderrPipeW.Close()
		_ = logWriter.Close()
		return fmt.Errorf("starting agent process %s: %w", agentName, err)
	}

	pid := cmd.Process.Pid
	logWriter.SetPID(pid)

	// Write agent PID file
	pidPath := s.resolver.AgentPIDPath(agentName)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err == nil {
		_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
	}

	worker := &AgentWorker{
		Name:       agentName,
		PID:        pid,
		StartTime:  time.Now(),
		Restarts:   restarts,
		Running:    true,
		Restarting: false,
		LogPath:    humanLogPath,
		SocketPath: s.resolver.AgentSocketPath(agentName),
		Cmd:        cmd,
		LogWriter:  logWriter,
		StdoutPipe: stdoutPipeW,
		StderrPipe: stderrPipeW,
		ExitChan:   make(chan int, 1),
		StopChan:   make(chan struct{}),
	}

	// Spawn stream scanner goroutines
	worker.LogWG.Add(2)
	go logWriter.PipeStream("stdout", "INFO", stdoutPipeR, &worker.LogWG)
	go logWriter.PipeStream("stderr", "WARN", stderrPipeR, &worker.LogWG)

	// Close the read ends when scanning is complete
	go func() {
		worker.LogWG.Wait()
		_ = stdoutPipeR.Close()
		_ = stderrPipeR.Close()
	}()

	// Monitor child process exit
	go s.monitorWorkerExit(worker, stdoutPipeW, stderrPipeW)

	s.workers[agentName] = worker
	s.logSupervisorEvent("Forked independent OS process for agent %s (PID %d)", agentName, pid)
	return nil
}

func (s *DaemonSupervisor) monitorWorkerExit(w *AgentWorker, stdoutW, stderrW *os.File) {
	err := w.Cmd.Wait()
	var exitCode int
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					exitCode = 128 + int(ws.Signal())
				}
			}
		} else {
			exitCode = 1
		}
	}

	// Close pipe writers so scanners observe EOF
	_ = stdoutW.Close()
	_ = stderrW.Close()
	w.LogWG.Wait()
	_ = w.LogWriter.Close()

	w.mu.Lock()
	w.Running = false
	w.Cmd = nil
	w.mu.Unlock()

	w.ExitChan <- exitCode
	close(w.StopChan)

	// Clean up child PID and socket files
	_ = os.Remove(s.resolver.AgentPIDPath(w.Name))
	_ = os.Remove(s.resolver.AgentSocketPath(w.Name))

	s.logSupervisorEvent("Agent worker %s (PID %d) exited with code %d", w.Name, w.PID, exitCode)
}

// StopAgent gracefully terminates an individual agent worker.
func (s *DaemonSupervisor) StopAgent(agentName string) error {
	s.mu.Lock()
	w, exists := s.workers[agentName]
	s.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent %s is not managed by supervisor", agentName)
	}

	return s.terminateWorker(w, s.config.ShutdownTimeout)
}

func (s *DaemonSupervisor) terminateWorker(w *AgentWorker, timeout time.Duration) error {
	w.mu.Lock()
	if !w.Running || w.Cmd == nil || w.Cmd.Process == nil {
		w.mu.Unlock()
		return nil
	}
	proc := w.Cmd.Process
	pid := w.PID
	w.mu.Unlock()

	// 1. Deliver SIGTERM
	_ = proc.Signal(syscall.SIGTERM)

	// 2. Wait for graceful exit
	select {
	case <-w.StopChan:
		return nil
	case <-time.After(timeout):
		// 3. Force kill if graceful shutdown timed out
		s.logSupervisorEvent("Agent %s (PID %d) did not terminate in %v; issuing SIGKILL", w.Name, pid, timeout)
		_ = proc.Kill()
		select {
		case <-w.StopChan:
			return nil
		case <-time.After(1 * time.Second):
			return fmt.Errorf("timed out force killing agent %s (PID %d)", w.Name, pid)
		}
	}
}

// shutdownWorkers cleanly terminates all managed child processes.
func (s *DaemonSupervisor) shutdownWorkers() {
	s.mu.RLock()
	var activeWorkers []*AgentWorker
	for _, w := range s.workers {
		w.mu.RLock()
		if w.Running {
			activeWorkers = append(activeWorkers, w)
		}
		w.mu.RUnlock()
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	for _, w := range activeWorkers {
		wg.Add(1)
		go func(worker *AgentWorker) {
			defer wg.Done()
			_ = s.terminateWorker(worker, s.config.ShutdownTimeout)
		}(w)
	}
	wg.Wait()
}

// healthCheck inspects worker processes and triggers restarts on unhandled crashes.
func (s *DaemonSupervisor) healthCheck() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, w := range s.workers {
		w.mu.Lock()
		running := w.Running
		restarts := w.Restarts
		restarting := w.Restarting
		if running || restarting {
			w.mu.Unlock()
			continue
		}

		// Invariant 4: Strict Permadeath Lifecycle. If deceased, never restart.
		if s.isAgentDeceased(name) {
			w.Deceased = true
			w.mu.Unlock()
			s.logSupervisorEvent("Agent %s is deceased (permadeath archive found); will not restart", name)
			continue
		}

		// If configured to restart on crash and below max restarts
		if s.config.RestartOnCrash && restarts < s.config.MaxRestarts {
			w.Restarts++
			attempt := w.Restarts
			w.Restarting = true
			w.mu.Unlock()

			s.logSupervisorEvent("Restarting crashed agent %s (attempt %d/%d) after backoff...", name, attempt, s.config.MaxRestarts)
			go func(agentName string, delay time.Duration) {
				time.Sleep(delay)
				if err := s.StartAgent(agentName); err != nil {
					s.logSupervisorEvent("Failed restarting agent %s: %v", agentName, err)
					s.mu.Lock()
					if worker, ok := s.workers[agentName]; ok {
						worker.mu.Lock()
						worker.Restarting = false
						worker.mu.Unlock()
					}
					s.mu.Unlock()
				}
			}(name, s.config.CrashBackoff)
		} else {
			w.mu.Unlock()
		}
	}
}

// Status returns the comprehensive runtime status of the supervisor and all child workers.
func (s *DaemonSupervisor) Status() (*DaemonStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := &DaemonStatus{
		Running:       s.pid > 0 && !s.stopped,
		PID:           s.pid,
		StartTime:     s.startTime,
		ManagedAgents: make([]ManagedAgentStatus, 0, len(s.workers)),
	}

	if status.Running && !s.startTime.IsZero() {
		status.Uptime = time.Since(s.startTime).Round(time.Second).String()
	}

	for _, w := range s.workers {
		w.mu.RLock()
		as := ManagedAgentStatus{
			Name:       w.Name,
			ClanID:     w.ClanID,
			PID:        w.PID,
			Running:    w.Running,
			Restarts:   w.Restarts,
			StartTime:  w.StartTime,
			SocketPath: w.SocketPath,
			LogPath:    w.LogPath,
			Deceased:   w.Deceased,
		}
		if as.Running && !w.StartTime.IsZero() {
			as.Uptime = time.Since(w.StartTime).Round(time.Second).String()
		}
		w.mu.RUnlock()
		status.ManagedAgents = append(status.ManagedAgents, as)
	}

	return status, nil
}

// ActiveAgentCount returns the number of actively running managed agents.
func (s *DaemonSupervisor) ActiveAgentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, w := range s.workers {
		w.mu.RLock()
		if w.Running {
			count++
		}
		w.mu.RUnlock()
	}
	return count
}

// ReproductionGovernor returns the underlying ReproductionGovernor instance, or nil if disabled.
func (s *DaemonSupervisor) ReproductionGovernor() *governor.ReproductionGovernor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reproductionGovernor
}

// EvaluateReproduction immediately evaluates and triggers dynastic reproduction for clanID.
func (s *DaemonSupervisor) EvaluateReproduction(ctx context.Context, clanID string) (*governor.ReproductionResult, error) {
	s.mu.RLock()
	gov := s.reproductionGovernor
	s.mu.RUnlock()

	if gov == nil {
		return nil, errors.New("reproduction governor is not enabled")
	}
	return gov.Trigger(ctx, clanID)
}

func (s *DaemonSupervisor) evaluateReproduction() {
	s.mu.RLock()
	gov := s.reproductionGovernor
	s.mu.RUnlock()

	if gov == nil {
		return
	}

	results, err := gov.Tick(s.ctx)
	if err != nil {
		s.logSupervisorEvent("Reproduction governor evaluation error: %v", err)
		return
	}
	for _, res := range results {
		if res != nil && res.Success {
			s.logSupervisorEvent("Dynastic reproduction succeeded: Child '%s' (Generation %d) born to %s & %s in clan %s",
				res.ChildID, res.Generation, res.ParentA, res.ParentB, s.config.ClanID)
		}
	}
}

func resolveAgentBinary(r *xdg.Resolver, explicitPath string) (string, error) {
	if explicitPath != "" {
		if fi, err := os.Stat(explicitPath); err == nil && !fi.IsDir() && (fi.Mode()&0o111 != 0) {
			abs, err := filepath.Abs(explicitPath)
			if err == nil {
				return abs, nil
			}
			return explicitPath, nil
		}
		return "", fmt.Errorf("specified agent binary not found or not executable: %s", explicitPath)
	}

	var candidates []string

	if envPath := os.Getenv("CIVQUEST_AGENT_BIN"); envPath != "" {
		candidates = append(candidates, envPath)
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "civquest-agent"))
	}

	if r != nil {
		candidates = append(candidates, filepath.Join(r.BinDir(), "civquest-agent"))
	}

	if path, err := exec.LookPath("civquest-agent"); err == nil {
		candidates = append(candidates, path)
	}

	candidates = append(candidates, filepath.Join("bin", "civquest-agent"))

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && (fi.Mode()&0o111 != 0) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}

	return "", fmt.Errorf("civquest-agent binary not found in standard paths; build it via 'go build -o ~/.local/bin/civquest-agent ./cmd/civquest-agent'")
}
