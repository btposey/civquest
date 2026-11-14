package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/daemon"
	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	inProcessSupervisorMu sync.Mutex
	inProcessSupervisor   *daemon.DaemonSupervisor
)

func newDaemonCmd() *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"supervisor"},
		Short:   "Manage the CivQuest agent supervisor daemon",
		Long: `Controls the background supervisor daemon responsible for managing
process-isolated autonomous agent workers, structured JSONL logs,
and process health monitoring.`,
	}

	daemonCmd.AddCommand(newDaemonStartCmd())
	daemonCmd.AddCommand(newDaemonStopCmd())
	daemonCmd.AddCommand(newDaemonStatusCmd())

	return daemonCmd
}

func newDaemonStartCmd() *cobra.Command {
	var (
		foreground   bool
		clanID       string
		agentNames   []string
		agentBin     string
		pollInterval time.Duration
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the supervisor daemon managing agent worker processes",
		Long: `Launches the supervisor daemon to supervise configured agent processes.
By default, the daemon runs in the background and writes its PID to
$XDG_STATE_HOME/civquest/supervisor.pid. Use --foreground to run interactively.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			// Check if already running
			supPIDPath := r.SupervisorPIDPath()
			if data, err := os.ReadFile(supPIDPath); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
					if daemon.IsProcessAlive(pid) {
						return fmt.Errorf("supervisor daemon is already running (PID %d)", pid)
					}
				}
			}

			// Clean up stale files before startup
			_ = daemon.CleanupStaleFiles(r)

			cfg := daemon.SupervisorConfig{
				Resolver:            r,
				AgentBinPath:        agentBin,
				ServerID:            flagServerID,
				ConfigDir:           flagConfigDir,
				DataDir:             flagDataDir,
				StateDir:            flagStateDir,
				GRPCAddr:            flagTarget,
				SharedKey:           flagSharedKey,
				ClanID:              clanID,
				AgentNames:          agentNames,
				HealthCheckInterval: pollInterval,
				RestartOnCrash:      true,
				MaxRestarts:         5,
				CleanExitBackoff:    500 * time.Millisecond,
				CrashBackoff:        1 * time.Second,
				ShutdownTimeout:     5 * time.Second,
			}

			if foreground {
				cfg.Out = cmd.OutOrStdout()
				cfg.ErrOut = cmd.ErrOrStderr()
				sup, err := daemon.NewDaemonSupervisor(cfg)
				if err != nil {
					return fmt.Errorf("initializing supervisor: %w", err)
				}

				sigCtx, stopSignals := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
				defer stopSignals()

				return sup.Run(sigCtx)
			}

			// In test environments, run in-process background supervisor
			if isTestProcess() {
				sup, err := daemon.NewDaemonSupervisor(cfg)
				if err != nil {
					return fmt.Errorf("initializing supervisor: %w", err)
				}

				ctx, cancel := context.WithCancel(context.Background())
				_ = cancel

				if err := sup.Start(ctx); err != nil {
					return fmt.Errorf("starting in-process supervisor: %w", err)
				}

				inProcessSupervisorMu.Lock()
				inProcessSupervisor = sup
				inProcessSupervisorMu.Unlock()

				pid := sup.PID()
				supLogPath := filepath.Join(r.LogsDir(), "supervisor.log")

				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"success": true,
						"pid":     pid,
						"log":     supLogPath,
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}

				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "✓ CivQuest supervisor daemon started in background (PID: %d)\n", pid)
				fmt.Fprintf(out, "  Logs:   %s\n", supLogPath)
				fmt.Fprintf(out, "  State:  %s\n", r.StateDir())
				return nil
			}

			// Production CLI: Launch supervisor process in background
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving executable path: %w", err)
			}

			daemonArgs := []string{"daemon", "start", "--foreground"}
			if flagServerID != "" {
				daemonArgs = append(daemonArgs, "--server-id", flagServerID)
			}
			if flagConfigDir != "" {
				daemonArgs = append(daemonArgs, "--config-dir", flagConfigDir)
			}
			if flagDataDir != "" {
				daemonArgs = append(daemonArgs, "--data-dir", flagDataDir)
			}
			if flagStateDir != "" {
				daemonArgs = append(daemonArgs, "--state-dir", flagStateDir)
			}
			if flagTarget != "" {
				daemonArgs = append(daemonArgs, "--target", flagTarget)
			}
			if flagSharedKey != "" {
				daemonArgs = append(daemonArgs, "--shared-key", flagSharedKey)
			}
			if clanID != "" {
				daemonArgs = append(daemonArgs, "--clan", clanID)
			}
			if agentBin != "" {
				daemonArgs = append(daemonArgs, "--agent-bin", agentBin)
			}

			_ = os.MkdirAll(r.LogsDir(), 0o750)
			supLogPath := filepath.Join(r.LogsDir(), "supervisor.log")
			supLogFile, err := os.OpenFile(supLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
			if err != nil {
				return fmt.Errorf("opening supervisor log file %s: %w", supLogPath, err)
			}
			defer supLogFile.Close()

			bgCmd := exec.Command(exe, daemonArgs...)
			bgCmd.Stdout = supLogFile
			bgCmd.Stderr = supLogFile
			bgCmd.SysProcAttr = &syscall.SysProcAttr{
				Setpgid: true,
			}

			if err := bgCmd.Start(); err != nil {
				return fmt.Errorf("launching background supervisor: %w", err)
			}

			pid := bgCmd.Process.Pid
			_ = bgCmd.Process.Release()

			// Wait up to 2 seconds for supervisor.pid to be written and verified
			started := false
			for i := 0; i < 40; i++ {
				time.Sleep(50 * time.Millisecond)
				if data, err := os.ReadFile(supPIDPath); err == nil {
					if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
						pid = p
						started = true
						break
					}
				}
			}

			if !started && !daemon.IsProcessAlive(pid) {
				return fmt.Errorf("supervisor process failed to start; check logs at %s", supLogPath)
			}

			if flagJSON {
				data, _ := json.MarshalIndent(map[string]any{
					"success": true,
					"pid":     pid,
					"log":     supLogPath,
				}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ CivQuest supervisor daemon started in background (PID: %d)\n", pid)
			fmt.Fprintf(out, "  Logs:   %s\n", supLogPath)
			fmt.Fprintf(out, "  State:  %s\n", r.StateDir())
			return nil
		},
	}

	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run supervisor in the foreground")
	cmd.Flags().StringVar(&clanID, "clan", "", "Supervise only agents belonging to the specified clan")
	cmd.Flags().StringSliceVar(&agentNames, "agent", nil, "Supervise specific agents by name")
	cmd.Flags().StringVar(&agentBin, "agent-bin", "", "Explicit path to civquest-agent executable")
	cmd.Flags().DurationVar(&pollInterval, "interval", 2*time.Second, "Health check polling interval")

	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running supervisor daemon and all child agent processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			// Check in-process supervisor first
			inProcessSupervisorMu.Lock()
			inProc := inProcessSupervisor
			inProcessSupervisor = nil
			inProcessSupervisorMu.Unlock()

			if inProc != nil {
				pid := inProc.PID()
				_ = inProc.Stop()
				_ = os.Remove(r.SupervisorPIDPath())
				_ = daemon.CleanupStaleFiles(r)

				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"success": true,
						"stopped": true,
						"pid":     pid,
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ CivQuest supervisor daemon stopped (PID: %d)\n", pid)
				return nil
			}

			supPIDPath := r.SupervisorPIDPath()
			data, err := os.ReadFile(supPIDPath)
			if err != nil {
				_ = daemon.CleanupStaleFiles(r)
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"running": false,
						"message": "supervisor daemon is not running",
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Supervisor daemon is not running.")
				return nil
			}

			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				_ = os.Remove(supPIDPath)
				_ = daemon.CleanupStaleFiles(r)
				fmt.Fprintln(cmd.OutOrStdout(), "Cleaned up invalid supervisor PID file.")
				return nil
			}

			if !daemon.IsProcessAlive(pid) {
				_ = os.Remove(supPIDPath)
				_ = daemon.CleanupStaleFiles(r)
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"running": false,
						"message": fmt.Sprintf("cleaned up stale supervisor PID file (PID was %d)", pid),
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cleaned up stale supervisor PID file (PID was %d). Supervisor was not running.\n", pid)
				return nil
			}

			// Signal SIGTERM
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed delivering SIGTERM to supervisor PID %d: %w", pid, err)
			}

			// Wait for termination
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				if !daemon.IsProcessAlive(pid) {
					_ = os.Remove(supPIDPath)
					_ = daemon.CleanupStaleFiles(r)

					if flagJSON {
						data, _ := json.MarshalIndent(map[string]any{
							"success": true,
							"stopped": true,
							"pid":     pid,
						}, "", "  ")
						fmt.Fprintln(cmd.OutOrStdout(), string(data))
						return nil
					}
					fmt.Fprintf(cmd.OutOrStdout(), "✓ CivQuest supervisor daemon stopped (PID: %d)\n", pid)
					return nil
				}
			}

			// Force kill with SIGKILL if still alive
			_ = syscall.Kill(pid, syscall.SIGKILL)
			_ = os.Remove(supPIDPath)
			_ = daemon.CleanupStaleFiles(r)

			if flagJSON {
				data, _ := json.MarshalIndent(map[string]any{
					"success": true,
					"stopped": true,
					"killed":  true,
					"pid":     pid,
				}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "! Supervisor daemon did not exit within %v; sent SIGKILL (PID: %d)\n", timeout, pid)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "Graceful termination timeout before issuing SIGKILL")

	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Display runtime status of supervisor daemon and all child agent processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			// Check in-process supervisor first
			inProcessSupervisorMu.Lock()
			inProc := inProcessSupervisor
			inProcessSupervisorMu.Unlock()

			if inProc != nil {
				st, err := inProc.Status()
				if err != nil {
					return err
				}
				return printDaemonStatus(cmd, r, st)
			}

			supPIDPath := r.SupervisorPIDPath()
			data, err := os.ReadFile(supPIDPath)
			if err != nil {
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"running": false,
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "CivQuest supervisor daemon is inactive.")
				return nil
			}

			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 || !daemon.IsProcessAlive(pid) {
				_ = os.Remove(supPIDPath)
				_ = daemon.CleanupStaleFiles(r)
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]any{
						"running": false,
						"stale":   true,
					}, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "CivQuest supervisor daemon is inactive (cleaned up stale PID file).")
				return nil
			}

			// Supervisor is alive! Detect agents
			agentDomain, _ := domain.DetectAgents(r)

			st := &daemon.DaemonStatus{
				Running:       true,
				PID:           pid,
				ManagedAgents: make([]daemon.ManagedAgentStatus, 0),
			}

			for _, a := range agentDomain.ConfiguredAgents {
				var agentPID int
				pidPath := r.AgentPIDPath(a.Name)
				if pData, err := os.ReadFile(pidPath); err == nil {
					if p, err := strconv.Atoi(strings.TrimSpace(string(pData))); err == nil {
						agentPID = p
					}
				}

				isRunning := agentPID > 0 && daemon.IsProcessAlive(agentPID)
				st.ManagedAgents = append(st.ManagedAgents, daemon.ManagedAgentStatus{
					Name:       a.Name,
					ClanID:     a.ClanID,
					PID:        agentPID,
					Running:    isRunning,
					SocketPath: r.AgentSocketPath(a.Name),
					LogPath:    r.AgentHumanLogPath(a.Name),
					Deceased:   a.Deceased,
				})
			}

			return printDaemonStatus(cmd, r, st)
		},
	}
}

func printDaemonStatus(cmd *cobra.Command, r *xdg.Resolver, st *daemon.DaemonStatus) error {
	if flagJSON {
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "======================================================================")
	if !st.Running {
		fmt.Fprintln(out, " CivQuest Supervisor Daemon: Inactive")
		fmt.Fprintln(out, "======================================================================")
		return nil
	}

	activeCount := 0
	deceasedCount := 0
	for _, a := range st.ManagedAgents {
		if a.Running {
			activeCount++
		}
		if a.Deceased {
			deceasedCount++
		}
	}

	fmt.Fprintf(out, " CivQuest Supervisor Daemon Status (PID: %d)\n", st.PID)
	fmt.Fprintln(out, "======================================================================")
	if st.Uptime != "" {
		fmt.Fprintf(out, " Uptime:         %s\n", st.Uptime)
	}
	fmt.Fprintf(out, " Managed Agents: %d active (%d deceased, %d configured)\n",
		activeCount, deceasedCount, len(st.ManagedAgents))
	fmt.Fprintf(out, " Logs Directory: %s\n", r.LogsDir())
	fmt.Fprintf(out, " State Directory:%s\n", r.StateDir())
	fmt.Fprintln(out, "----------------------------------------------------------------------")
	if len(st.ManagedAgents) == 0 {
		fmt.Fprintln(out, " No configured agents found.")
	} else {
		fmt.Fprintln(out, " Agent Worker Processes:")
		for _, a := range st.ManagedAgents {
			statusStr := "Inactive"
			if a.Deceased {
				statusStr = "Deceased (Permadeath)"
			} else if a.Running {
				statusStr = fmt.Sprintf("Active (PID: %d)", a.PID)
			}
			uptimeStr := ""
			if a.Uptime != "" {
				uptimeStr = fmt.Sprintf(" Uptime: %s", a.Uptime)
			}
			fmt.Fprintf(out, "   • %-12s Clan: %-10s [%s]%s\n", a.Name, a.ClanID, statusStr, uptimeStr)
			fmt.Fprintf(out, "     Socket: %s\n", a.SocketPath)
			fmt.Fprintf(out, "     Log:    %s\n", a.LogPath)
		}
	}
	fmt.Fprintln(out, "======================================================================")
	return nil
}
