package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Common domain errors.
var (
	ErrServerNotInstalled = errors.New("server domain is not installed: run './install.sh server' to install it")
	ErrAgentsNotInstalled = errors.New("agent domain is not installed: run './install.sh agents' to install it")
)

// ServerManifest describes the metadata written during './install.sh server'.
type ServerManifest struct {
	Version          string   `json:"version"`
	MinecraftVersion string   `json:"minecraft_version"`
	NeoForgeVersion  string   `json:"neoforge_version"`
	InstalledAt      string   `json:"installed_at"`
	ServerDir        string   `json:"server_dir"`
	ModsDir          string   `json:"mods_dir"`
	ModJar           string   `json:"mod_jar"`
	JavaOptions      []string `json:"java_options"`
	RunnerScript     string   `json:"runner_script"`
}

// AgentsManifest describes the metadata written during './install.sh agents'.
type AgentsManifest struct {
	Version     string   `json:"version"`
	InstalledAt string   `json:"installed_at"`
	BinDir      string   `json:"bin_dir"`
	Binaries    []string `json:"binaries"`
	ConfigDir   string   `json:"config_dir"`
	DataDir     string   `json:"data_dir"`
	StateDir    string   `json:"state_dir"`
}

// ServerStatus holds the detected runtime and installation status of the server.
type ServerStatus struct {
	Installed    bool            `json:"installed"`
	Running      bool            `json:"running"`
	PID          int             `json:"pid,omitempty"`
	StartTime    *time.Time      `json:"start_time,omitempty"`
	Uptime       string          `json:"uptime,omitempty"`
	ServerDir    string          `json:"server_dir,omitempty"`
	Manifest     *ServerManifest `json:"manifest,omitempty"`
	LogPath      string          `json:"log_path,omitempty"`
	StatusReason string          `json:"status_reason,omitempty"`
}

// AgentSummary represents basic metadata for a configured agent avatar.
type AgentSummary struct {
	Name       string  `json:"name"`
	ClanID     string  `json:"clan_id"`
	Profession string  `json:"profession"`
	Active     bool    `json:"active"`
	Deceased   bool    `json:"deceased"`
	SocketPath string  `json:"socket_path"`
	Bravery    float64 `json:"bravery"`
	Curiosity  float64 `json:"curiosity"`
}

// AgentDomainStatus represents the status of the agent domain.
type AgentDomainStatus struct {
	Installed        bool            `json:"installed"`
	Manifest         *AgentsManifest `json:"manifest,omitempty"`
	BinDir           string          `json:"bin_dir,omitempty"`
	ConfiguredAgents []AgentSummary  `json:"configured_agents"`
}

// ClanDomainStatus represents the status of the clans domain.
type ClanDomainStatus struct {
	Initialized bool     `json:"initialized"`
	ClanCount   int      `json:"clan_count"`
	Clans       []string `json:"clans"`
}

// Topology encapsulates the complete detected installation topology of a CivQuest host.
type Topology struct {
	ConfigDir string            `json:"config_dir"`
	DataDir   string            `json:"data_dir"`
	StateDir  string            `json:"state_dir"`
	Server    ServerStatus      `json:"server"`
	Agents    AgentDomainStatus `json:"agents"`
	Clans     ClanDomainStatus  `json:"clans"`
}

// DetectTopology inspects the XDG directories and processes to determine the host's installation topology.
func DetectTopology(r *xdg.Resolver) (*Topology, error) {
	if r == nil {
		r = xdg.DefaultResolver()
	}

	serverStatus, err := DetectServer(r)
	if err != nil {
		return nil, fmt.Errorf("failed detecting server status: %w", err)
	}

	agentsStatus, err := DetectAgents(r)
	if err != nil {
		return nil, fmt.Errorf("failed detecting agents status: %w", err)
	}

	clansStatus, err := DetectClans(r)
	if err != nil {
		return nil, fmt.Errorf("failed detecting clans status: %w", err)
	}

	return &Topology{
		ConfigDir: r.ConfigDir(),
		DataDir:   r.DataDir(),
		StateDir:  r.StateDir(),
		Server:    serverStatus,
		Agents:    agentsStatus,
		Clans:     clansStatus,
	}, nil
}

// DetectServer checks if the server domain is installed and if the server process is currently running.
func DetectServer(r *xdg.Resolver) (ServerStatus, error) {
	status := ServerStatus{
		ServerDir: r.ServerDir(),
		LogPath:   r.ServerLogPath(),
	}

	manifestPath := r.ServerManifestPath()
	var manifest ServerManifest
	hasManifest := false
	if data, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(data, &manifest); err == nil {
			hasManifest = true
			status.Manifest = &manifest
		}
	}

	serverDirExists := false
	hasRunnerScript := false
	hasJarFile := false

	if info, err := os.Stat(r.ServerDir()); err == nil && info.IsDir() {
		serverDirExists = true
		if _, err := os.Stat(r.ServerRunScriptPath()); err == nil {
			hasRunnerScript = true
		}
		// Check for any .jar in server dir or mods dir
		if entries, err := os.ReadDir(r.ServerDir()); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jar") {
					hasJarFile = true
					break
				}
			}
		}
		if !hasJarFile {
			if entries, err := os.ReadDir(r.ServerModsDir()); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jar") {
						hasJarFile = true
						break
					}
				}
			}
		}
	}

	if hasManifest || (serverDirExists && (hasRunnerScript || hasJarFile)) {
		status.Installed = true
	} else {
		status.StatusReason = "Server directory, manifest, or runner not found"
		return status, nil
	}

	// Check PID file
	pidPath := r.ServerPIDPath()
	pidData, err := os.ReadFile(pidPath)
	if err == nil {
		pidStr := strings.TrimSpace(string(pidData))
		pid, err := strconv.Atoi(pidStr)
		if err == nil && pid > 1 {
			if IsProcessRunning(pid) {
				status.Running = true
				status.PID = pid
				if startTime, uptime := getProcessUptime(pidPath, pid); startTime != nil {
					status.StartTime = startTime
					status.Uptime = uptime
				}
				status.StatusReason = fmt.Sprintf("Running (PID %d)", pid)
			} else {
				status.Running = false
				status.StatusReason = fmt.Sprintf("Stale PID file detected (%d not running)", pid)
			}
		} else {
			status.Running = false
			status.StatusReason = fmt.Sprintf("Invalid PID file content (%s)", pidStr)
		}
	} else {
		status.StatusReason = "Stopped"
	}

	return status, nil
}

// DetectAgents checks if the agent domain is installed and lists configured agents.
func DetectAgents(r *xdg.Resolver) (AgentDomainStatus, error) {
	status := AgentDomainStatus{
		ConfiguredAgents: make([]AgentSummary, 0),
	}

	manifestPath := r.AgentsManifestPath()
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest AgentsManifest
		if err := json.Unmarshal(data, &manifest); err == nil {
			status.Manifest = &manifest
			status.BinDir = manifest.BinDir
			status.Installed = true
		}
	}

	// Also check presence of binaries or config directories
	if !status.Installed {
		if info, err := os.Stat(r.AgentsConfigDir()); err == nil && info.IsDir() {
			status.Installed = true
		} else if _, err := os.Stat(r.GlobalConfigPath()); err == nil {
			status.Installed = true
		} else if _, err := os.Stat(filepath.Join(r.BinDir(), "civctl")); err == nil {
			status.Installed = true
			status.BinDir = r.BinDir()
		} else if _, err := exec.LookPath("civctl"); err == nil {
			status.Installed = true
		}
	}

	// Scan configured agents
	agentsConfigDir := r.AgentsConfigDir()
	entries, err := os.ReadDir(agentsConfigDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			agentName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			agentCfg, err := r.LoadAgentConfig(agentName)
			if err != nil {
				continue
			}

			// Check if agent socket exists and is active
			sockPath := r.AgentSocketPath(agentName)
			isActive := false
			if _, err := os.Stat(sockPath); err == nil {
				conn, dialErr := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
				if dialErr == nil {
					isActive = true
					_ = conn.Close()
				} else {
					// Connection failed (dead/stale socket) - clean up socket file
					_ = os.Remove(sockPath)
				}
			}

			// Check if agent is deceased in graveyard
			isDeceased := false
			graveyardDir := r.GraveyardDir()
			if matches, _ := filepath.Glob(filepath.Join(graveyardDir, fmt.Sprintf("%s_*", agentName))); len(matches) > 0 {
				isDeceased = true
			}
			if isDeceased {
				isActive = false
			}

			status.ConfiguredAgents = append(status.ConfiguredAgents, AgentSummary{
				Name:       agentCfg.Name,
				ClanID:     agentCfg.ClanID,
				Profession: agentCfg.Profession,
				Active:     isActive,
				Deceased:   isDeceased,
				SocketPath: sockPath,
				Bravery:    agentCfg.Traits.Bravery,
				Curiosity:  agentCfg.Traits.Curiosity,
			})
		}
	}

	return status, nil
}

// DetectClans checks if clan directories exist and counts them.
func DetectClans(r *xdg.Resolver) (ClanDomainStatus, error) {
	status := ClanDomainStatus{
		Clans: make([]string, 0),
	}

	clansDir := r.ClansDataDir()
	entries, err := os.ReadDir(clansDir)
	if err != nil {
		return status, nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			status.Clans = append(status.Clans, entry.Name())
		}
	}

	if len(status.Clans) > 0 {
		status.Initialized = true
		status.ClanCount = len(status.Clans)
	}

	return status, nil
}

// IsProcessRunning checks if a PID is alive and not a zombie on Linux.
func IsProcessRunning(pid int) bool {
	if pid <= 1 {
		return false
	}

	// On Linux, inspect /proc/<pid>/cmdline if available before sending signals.
	// A terminated or recycled zombie process has an empty cmdline.
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if len(cmdline) == 0 {
			return false
		}
	} else if os.IsNotExist(err) {
		return false
	}

	// Attempt non-blocking reap in case it is a child of the current process
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	if err == nil && wpid == pid {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, sending signal 0 checks for process existence without actually sending a signal.
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			return true
		}
		return false
	}

	// On Linux, check if process is in zombie state
	if statusData, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "State:") {
				if strings.Contains(line, "Z (zombie)") {
					return false
				}
				break
			}
		}
	}

	return true
}

func getProcessUptime(pidPath string, pid int) (*time.Time, string) {
	// Try stat on /proc/<pid> if on Linux
	var modTime time.Time
	if procStat, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		modTime = procStat.ModTime()
	} else if pidStat, err := os.Stat(pidPath); err == nil {
		modTime = pidStat.ModTime()
	} else {
		return nil, ""
	}

	uptimeDuration := time.Since(modTime)
	uptimeStr := formatDuration(uptimeDuration)
	return &modTime, uptimeStr
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
