package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
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

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/launcher"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage and inspect autonomous agents",
		Long: `Controls the creation, execution, logs, and live telemetry sockets
of process-isolated autonomous FakePlayer agent avatars.`,
	}

	agentCmd.AddCommand(newAgentListCmd())
	agentCmd.AddCommand(newAgentStatusCmd())
	agentCmd.AddCommand(newAgentCreateCmd())
	agentCmd.AddCommand(newAgentStartCmd())
	agentCmd.AddCommand(newAgentStopCmd())
	agentCmd.AddCommand(newAgentStartClanCmd())
	agentCmd.AddCommand(newAgentLogsCmd())
	agentCmd.AddCommand(newAgentSkillCmd())

	return agentCmd
}

func newAgentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured agents and their execution states",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			status, err := domain.DetectAgents(r)
			if err != nil {
				return err
			}

			if !status.Installed {
				return fmt.Errorf("agent domain is not installed in %s: run './install.sh agents' to install it", r.ConfigDir())
			}

			if flagJSON {
				data, err := json.MarshalIndent(status.ConfiguredAgents, "", "  ")
				if err != nil {
					return fmt.Errorf("failed marshaling json: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " Configured CivQuest Agents (%d)\n", len(status.ConfiguredAgents))
			fmt.Fprintln(out, "======================================================================")
			if len(status.ConfiguredAgents) == 0 {
				fmt.Fprintf(out, "No agent configurations found in %s.\n", r.AgentsConfigDir())
				fmt.Fprintln(out, "Run './install.sh agents' or 'cq agent create' to add agents.")
			} else {
				for _, a := range status.ConfiguredAgents {
					stateStr := "Inactive"
					if a.Active {
						stateStr = "Active (Live Socket)"
					}
					fmt.Fprintf(out, "  • %-12s Clan: %-12s Profession: %-12s [%s]\n",
						a.Name, a.ClanID, a.Profession, stateStr)
				}
			}
			fmt.Fprintln(out, "======================================================================")
			return nil
		},
	}
}

func newAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [agent_name]",
		Short: "Inspect the identity, traits, and socket status of an agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			status, err := domain.DetectAgents(r)
			if err != nil {
				return err
			}

			if !status.Installed {
				return fmt.Errorf("agent domain is not installed in %s: run './install.sh agents' to install it", r.ConfigDir())
			}

			if len(args) == 0 {
				return newAgentListCmd().RunE(cmd, args)
			}

			agentName := r.ResolveAgentName(args[0])
			agentCfg, err := r.LoadAgentConfig(agentName)
			if err != nil {
				return fmt.Errorf("agent %q not found or invalid in %s: %w", agentName, r.AgentsConfigDir(), err)
			}
			if agentCfg.Name != "" {
				agentName = agentCfg.Name
			}

			sockPath := r.AgentSocketPath(agentName)
			isActive := false
			if _, err := os.Stat(sockPath); err == nil {
				isActive = true
			}

			pid := 0
			pidPath := r.AgentPIDPath(agentName)
			if data, err := os.ReadFile(pidPath); err == nil {
				if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					pid = p
				}
			}

			watchdogPID := 0
			watchdogPIDPath := filepath.Join(r.AgentStateDir(agentName), "watchdog.pid")
			if data, err := os.ReadFile(watchdogPIDPath); err == nil {
				if wp, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					watchdogPID = wp
				}
			}

			var sockResp *agent.SocketStatusResponse
			if isActive {
				conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
				if err == nil {
					_ = conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
					scanner := bufio.NewScanner(conn)
					if scanner.Scan() {
						var parsed agent.SocketStatusResponse
						if err := json.Unmarshal(scanner.Bytes(), &parsed); err == nil {
							sockResp = &parsed
							if parsed.PID > 0 && pid == 0 {
								pid = parsed.PID
							}
						}
					}
					_ = conn.Close()
				}
			}

			if flagJSON {
				resp := map[string]any{
					"identity":     agentCfg,
					"active":       isActive,
					"pid":          pid,
					"watchdog_pid": watchdogPID,
					"socket_path":  sockPath,
				}
				if sockResp != nil {
					resp["telemetry"] = sockResp
					resp["socket_status"] = sockResp
				}
				data, err := json.MarshalIndent(resp, "", "  ")
				if err != nil {
					return fmt.Errorf("failed marshaling json: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			stateStr := "Inactive (Socket not found)"
			if isActive {
				if pid > 0 {
					stateStr = fmt.Sprintf("Active (PID %d, Live Socket)", pid)
				} else {
					stateStr = "Active (UNIX Socket Connected)"
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "==================================================")
			fmt.Fprintf(out, " CivQuest Agent Status: %s\n", agentCfg.Name)
			fmt.Fprintln(out, "==================================================")
			fmt.Fprintf(out, "Clan:            %s\n", agentCfg.ClanID)
			fmt.Fprintf(out, "Profession:      %s\n", agentCfg.Profession)
			if agentCfg.SecondaryHobby != "" {
				fmt.Fprintf(out, "Secondary Hobby: %s\n", agentCfg.SecondaryHobby)
			}
			fmt.Fprintf(out, "Status:          %s\n", stateStr)
			if pid > 0 {
				fmt.Fprintf(out, "PID:             %d\n", pid)
			}
			if watchdogPID > 0 {
				fmt.Fprintf(out, "Watchdog:        Active (PID %d)\n", watchdogPID)
			}
			fmt.Fprintf(out, "Socket:          %s\n", sockPath)

			if sockResp != nil {
				fmt.Fprintf(out, "Uptime:          %.1fs\n", sockResp.UptimeSeconds)
				fmt.Fprintf(out, "Memory RSS:      %.2f MB\n", sockResp.MemoryRSSMB)
				fmt.Fprintf(out, "Health:          %.1f\n", sockResp.Health)
				fmt.Fprintf(out, "Food Level:      %d / 20\n", sockResp.FoodLevel)
				fmt.Fprintf(out, "Navigation:      %s\n", sockResp.NavigationState)
				if sockResp.Position != nil {
					fmt.Fprintf(out, "Position:        (%.1f, %.1f, %.1f)\n", sockResp.Position.X, sockResp.Position.Y, sockResp.Position.Z)
				} else {
					fmt.Fprintln(out, "Position:        Unknown")
				}
				if sockResp.NavigationDestination != nil {
					fmt.Fprintf(out, "Nav Destination: (%.1f, %.1f, %.1f)\n", sockResp.NavigationDestination.X, sockResp.NavigationDestination.Y, sockResp.NavigationDestination.Z)
				} else {
					fmt.Fprintln(out, "Nav Destination: None (Idle)")
				}
				fmt.Fprintf(out, "Frame Latency:   %.2f ms\n", sockResp.FrameLatencyMs)
				if sockResp.ActiveLLMRequests > 0 {
					fmt.Fprintf(out, "Active LLM Reqs: %d\n", sockResp.ActiveLLMRequests)
				}
				if sockResp.UtilityScores != nil {
					fmt.Fprintln(out, "System 1 Utility Scores:")
					fmt.Fprintf(out, "  • Hunger:        %.2f\n", sockResp.UtilityScores.Hunger)
					fmt.Fprintf(out, "  • Threat:        %.2f\n", sockResp.UtilityScores.Threat)
					fmt.Fprintf(out, "  • Confidence:    %.2f\n", sockResp.UtilityScores.Confidence)
					fmt.Fprintf(out, "  • Exploration:   %.2f\n", sockResp.UtilityScores.Exploration)
					fmt.Fprintf(out, "  • Sleep Need:    %.2f\n", sockResp.UtilityScores.SleepNeed)
				}
				if sockResp.Goals != nil {
					fmt.Fprintln(out, "Synthesized Strategic Goals:")
					if sockResp.Goals.ImmediateGoal != "" {
						fmt.Fprintf(out, "  • Immediate:     %s\n", sockResp.Goals.ImmediateGoal)
					}
					if sockResp.Goals.ShortTermGoal != "" {
						fmt.Fprintf(out, "  • Short-Term:    %s\n", sockResp.Goals.ShortTermGoal)
					}
					if sockResp.Goals.LongTermGoal != "" {
						fmt.Fprintf(out, "  • Long-Term:     %s\n", sockResp.Goals.LongTermGoal)
					}
				}
			} else if agentCfg.ImmediateGoal != "" || agentCfg.ShortTermGoal != "" || agentCfg.LongTermGoal != "" {
				fmt.Fprintln(out, "Synthesized Strategic Goals:")
				if agentCfg.ImmediateGoal != "" {
					fmt.Fprintf(out, "  • Immediate:     %s\n", agentCfg.ImmediateGoal)
				}
				if agentCfg.ShortTermGoal != "" {
					fmt.Fprintf(out, "  • Short-Term:    %s\n", agentCfg.ShortTermGoal)
				}
				if agentCfg.LongTermGoal != "" {
					fmt.Fprintf(out, "  • Long-Term:     %s\n", agentCfg.LongTermGoal)
				}
			}
			fmt.Fprintln(out, "Personality Traits:")
			fmt.Fprintf(out, "  • Bravery:         %.2f\n", agentCfg.Traits.Bravery)
			fmt.Fprintf(out, "  • Industriousness: %.2f\n", agentCfg.Traits.Industriousness)
			fmt.Fprintf(out, "  • Sociability:     %.2f\n", agentCfg.Traits.Sociability)
			fmt.Fprintf(out, "  • Curiosity:       %.2f\n", agentCfg.Traits.Curiosity)
			fmt.Fprintf(out, "  • Aggressiveness:  %.2f\n", agentCfg.Traits.Aggressiveness)
			fmt.Fprintln(out, "Skin Model:")
			fmt.Fprintf(out, "  • Model:           %s\n", agentCfg.Skin.Model)
			if agentCfg.Skin.TexturePath != "" {
				fmt.Fprintf(out, "  • Texture:         %s\n", agentCfg.Skin.TexturePath)
			}
			fmt.Fprintln(out, "==================================================")

			return nil
		},
	}
}

func newAgentCreateCmd() *cobra.Command {
	var (
		name            string
		clanID          string
		role            string
		skinPath        string
		model           string
		bravery         float64
		industriousness float64
		sociability     float64
		curiosity       float64
		aggressiveness  float64
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new agent identity YAML configuration and skin assets",
		Long: `Defines a new autonomous agent avatar with personality traits,
clan membership, and skin textures, saving the profile to XDG configuration directories.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && name == "" {
				name = args[0]
			}
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("agent name is required (use --name or pass as first argument)")
			}

			r := getResolver()

			if _, err := r.CheckAgentNameUnique(name); err != nil {
				return err
			}

			// Prepare agent config
			cfg := xdg.DefaultAgentIdentityConfig(name)
			if clanID != "" {
				cfg.ClanID = clanID
			}
			if role != "" {
				cfg.Profession = strings.ToUpper(role)
			}
			if model != "" {
				cfg.Skin.Model = model
			}

			cfg.Traits.Bravery = bravery
			cfg.Traits.Industriousness = industriousness
			cfg.Traits.Sociability = sociability
			cfg.Traits.Curiosity = curiosity
			cfg.Traits.Aggressiveness = aggressiveness

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid agent configuration: %w", err)
			}

			// Destination skin path in agent data directory
			agentDataDir := filepath.Join(r.AgentsDataDir(), name)
			if err := os.MkdirAll(agentDataDir, 0755); err != nil {
				return fmt.Errorf("failed creating agent data directory %s: %w", agentDataDir, err)
			}
			destSkinPath := filepath.Join(agentDataDir, "skin.png")

			if skinPath != "" {
				// Copy specified skin file
				srcData, err := os.ReadFile(skinPath)
				if err != nil {
					return fmt.Errorf("reading source skin file %s: %w", skinPath, err)
				}
				if err := os.WriteFile(destSkinPath, srcData, 0644); err != nil {
					return fmt.Errorf("writing agent skin to %s: %w", destSkinPath, err)
				}
			} else {
				// Generate valid standard 64x64 PNG texture
				if err := createDefaultSkinPNG(destSkinPath); err != nil {
					return fmt.Errorf("generating default skin asset: %w", err)
				}
			}
			cfg.Skin.TexturePath = destSkinPath

			// Save agent YAML
			if err := r.SaveAgentConfig(cfg); err != nil {
				return fmt.Errorf("saving agent config: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"success":     true,
					"name":        cfg.Name,
					"clan_id":     cfg.ClanID,
					"profession":  cfg.Profession,
					"config_path": r.AgentConfigPath(cfg.Name),
					"skin_path":   destSkinPath,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "✓ CivQuest agent profile created successfully:")
			fmt.Fprintf(out, "  Name:        %s\n", cfg.Name)
			fmt.Fprintf(out, "  Clan:        %s\n", cfg.ClanID)
			fmt.Fprintf(out, "  Profession:  %s\n", cfg.Profession)
			fmt.Fprintf(out, "  Config YAML: %s\n", r.AgentConfigPath(cfg.Name))
			fmt.Fprintf(out, "  Skin Asset:  %s\n", destSkinPath)

			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Agent name identifier (required)")
	cmd.Flags().StringVarP(&clanID, "clan", "c", "pioneers", "Clan identifier")
	cmd.Flags().StringVarP(&role, "role", "r", "EXPLORER", "Agent profession role (EXPLORER, BUILDER, FARMER, etc.)")
	cmd.Flags().StringVar(&skinPath, "skin", "", "Path to custom 64x64 RGBA PNG skin texture")
	cmd.Flags().StringVar(&model, "model", "default", "Player skin model variant ('default' or 'slim')")
	cmd.Flags().Float64Var(&bravery, "bravery", 0.50, "Bravery trait [0.0 - 1.0]")
	cmd.Flags().Float64Var(&industriousness, "industriousness", 0.50, "Industriousness trait [0.0 - 1.0]")
	cmd.Flags().Float64Var(&sociability, "sociability", 0.50, "Sociability trait [0.0 - 1.0]")
	cmd.Flags().Float64Var(&curiosity, "curiosity", 0.50, "Curiosity trait [0.0 - 1.0]")
	cmd.Flags().Float64Var(&aggressiveness, "aggressiveness", 0.50, "Aggressiveness trait [0.0 - 1.0]")

	return cmd
}

func newAgentStartCmd() *cobra.Command {
	var (
		coords        string
		foreground    bool
		restartOnExit bool
	)

	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start an autonomous agent process",
		Long: `Forks an isolated 'civquest-agent' operating system worker process,
connects to the bridge server, and spawns the agent avatar into Minecraft.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]
			r := getResolver()

			cfg, err := r.LoadAgentConfig(agentName)
			if err != nil {
				return fmt.Errorf("agent %q configuration not found in %s: %w", agentName, r.AgentsConfigDir(), err)
			}
			if cfg.Name != "" {
				agentName = cfg.Name
			}

			// Invariant 4: Strict Permadeath Lifecycle. Never respawn an agent in the graveyard.
			graveyardDir := r.GraveyardDir()
			if matches, globErr := filepath.Glob(filepath.Join(graveyardDir, fmt.Sprintf("%s_*", agentName))); globErr == nil && len(matches) > 0 {
				return fmt.Errorf("agent %s is deceased (permadeath archive found in %s); permadeath invariant prevents respawning", agentName, matches[0])
			}

			// Check if already running
			pidPath := r.AgentPIDPath(agentName)
			if data, err := os.ReadFile(pidPath); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil {
						if err := proc.Signal(syscall.Signal(0)); err == nil {
							return fmt.Errorf("agent %s is already running with PID %d", agentName, pid)
						}
					}
				}
			}

			// Locate civquest-agent binary
			binPath, err := resolveAgentBinary(r)
			if err != nil {
				return err
			}

			agentArgs := []string{"--name", agentName}
			if flagServerID != "" {
				agentArgs = append(agentArgs, "--server-id", flagServerID)
			}
			if flagConfigDir != "" {
				agentArgs = append(agentArgs, "--config-dir", flagConfigDir)
			}
			if flagDataDir != "" {
				agentArgs = append(agentArgs, "--data-dir", flagDataDir)
			}
			if flagStateDir != "" {
				agentArgs = append(agentArgs, "--state-dir", flagStateDir)
			}

			if restartOnExit {
				opts := defaultWatchdogOptions(r, agentName)

				if foreground {
					sigCtx, stopSignals := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
					defer stopSignals()
					return runAgentWatchdog(sigCtx, r, cfg, binPath, agentArgs, coords, cmd.OutOrStdout(), opts, nil)
				}

				if isTestProcess() {
					readyChan := make(chan struct{})
					var startErr error
					var startErrMu sync.Mutex
					go func() {
						err := runAgentWatchdog(context.Background(), r, cfg, binPath, agentArgs, coords, nil, opts, readyChan)
						if err != nil {
							startErrMu.Lock()
							startErr = err
							startErrMu.Unlock()
						}
					}()

					select {
					case <-readyChan:
					case <-time.After(5 * time.Second):
						startErrMu.Lock()
						defer startErrMu.Unlock()
						if startErr != nil {
							return startErr
						}
						return fmt.Errorf("timed out waiting for agent %s watchdog startup", agentName)
					}

					startErrMu.Lock()
					if startErr != nil {
						err := startErr
						startErrMu.Unlock()
						return err
					}
					startErrMu.Unlock()

					pid := 0
					if data, err := os.ReadFile(pidPath); err == nil {
						pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
					}

					if flagJSON {
						data, _ := json.MarshalIndent(map[string]any{
							"success":  true,
							"name":     agentName,
							"pid":      pid,
							"log":      r.AgentLogPath(agentName),
							"watchdog": true,
						}, "", "  ")
						if err != nil {
							return err
						}
						fmt.Fprintln(cmd.OutOrStdout(), string(data))
						return nil
					}

					out := cmd.OutOrStdout()
					fmt.Fprintf(out, "✓ Agent '%s' started with supervisor watchdog (PID: %d)\n", agentName, pid)
					fmt.Fprintf(out, "  Logs:   %s\n", r.AgentLogPath(agentName))
					fmt.Fprintf(out, "  Socket: %s\n", r.AgentSocketPath(agentName))
					return nil
				}

				// Production CLI: Launch supervisor watchdog process in background
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("resolving executable: %w", err)
				}

				supervisorArgs := []string{"agent", "start", agentName, "--restart-on-exit", "--foreground"}
				if flagServerID != "" {
					supervisorArgs = append(supervisorArgs, "--server-id", flagServerID)
				}
				if flagConfigDir != "" {
					supervisorArgs = append(supervisorArgs, "--config-dir", flagConfigDir)
				}
				if flagDataDir != "" {
					supervisorArgs = append(supervisorArgs, "--data-dir", flagDataDir)
				}
				if flagStateDir != "" {
					supervisorArgs = append(supervisorArgs, "--state-dir", flagStateDir)
				}
				if coords != "" {
					supervisorArgs = append(supervisorArgs, "--coords", coords)
				}

				supLogPath := r.AgentLogPath(agentName)
				supLogFile, err := os.OpenFile(supLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
				if err != nil {
					return fmt.Errorf("opening log file %s: %w", supLogPath, err)
				}
				defer supLogFile.Close()

				supCmd := exec.Command(exe, supervisorArgs...)
				supCmd.Stdout = supLogFile
				supCmd.Stderr = supLogFile
				supCmd.SysProcAttr = &syscall.SysProcAttr{
					Setpgid: true,
				}

				if err := supCmd.Start(); err != nil {
					return fmt.Errorf("launching background watchdog: %w", err)
				}

				watchdogPID := supCmd.Process.Pid
				watchdogPIDPath := filepath.Join(r.AgentStateDir(agentName), "watchdog.pid")
				_ = os.WriteFile(watchdogPIDPath, []byte(fmt.Sprintf("%d\n", watchdogPID)), 0644)
				_ = supCmd.Process.Release()

				// Wait briefly for child process to write agent.pid
				pid := 0
				for i := 0; i < 30; i++ {
					time.Sleep(50 * time.Millisecond)
					if data, err := os.ReadFile(pidPath); err == nil {
						if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
							pid = p
							break
						}
					}
				}

				if flagJSON {
					data, err := json.MarshalIndent(map[string]any{
						"success":      true,
						"name":         agentName,
						"pid":          pid,
						"watchdog_pid": watchdogPID,
						"log":          supLogPath,
					}, "", "  ")
					if err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}

				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "✓ Agent '%s' started with supervisor watchdog (PID: %d, Watchdog PID: %d)\n", agentName, pid, watchdogPID)
				fmt.Fprintf(out, "  Logs:   %s\n", supLogPath)
				fmt.Fprintf(out, "  Socket: %s\n", r.AgentSocketPath(agentName))
				return nil
			}

			if foreground {
				execCmd := exec.Command(binPath, "--name", agentName)
				execCmd.Stdout = cmd.OutOrStdout()
				execCmd.Stderr = cmd.ErrOrStderr()
				execCmd.Stdin = os.Stdin
				return execCmd.Run()
			}

			// Prepare agent state dir and well-known logs dir
			if err := os.MkdirAll(r.AgentStateDir(agentName), 0755); err != nil {
				return fmt.Errorf("failed creating agent state dir: %w", err)
			}
			_ = os.MkdirAll(r.LogsDir(), 0750)

			logPath := r.AgentLogPath(agentName)
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
			if err != nil {
				return fmt.Errorf("failed opening agent log file %s: %w", logPath, err)
			}
			defer logFile.Close()

			execCmd := exec.Command(binPath, agentArgs...)
			execCmd.Stdout = logFile
			execCmd.Stderr = logFile
			execCmd.SysProcAttr = &syscall.SysProcAttr{
				Setpgid: true,
			}

			fmt.Fprintf(logFile, "=== [%s] Spawning CivQuest Agent: %s ===\n", time.Now().Format(time.RFC3339), agentName)

			if err := execCmd.Start(); err != nil {
				return fmt.Errorf("failed launching agent process %s: %w", binPath, err)
			}

			pid := execCmd.Process.Pid
			if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", pid)), 0644); err != nil {
				_ = execCmd.Process.Kill()
				return fmt.Errorf("failed writing agent PID file: %w", err)
			}

			// Detach process
			_ = execCmd.Process.Release()

			// Optionally notify bridge with custom coordinates if provided
			if coords != "" {
				if parsed, err := parseCoords(coords); err == nil && parsed != nil {
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						if admin, err := GetAdminClient(r); err == nil {
							defer admin.Close()
							_, _ = admin.SpawnAgentEntity(ctx, &civquestv1.SpawnAgentRequest{
								AgentId:     cfg.Name,
								DisplayName: cfg.Name,
								ClanId:      cfg.ClanID,
								SpawnCoords: parsed,
							})
						}
					}()
				}
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"success": true,
					"name":    agentName,
					"pid":     pid,
					"log":     logPath,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ Agent '%s' started in background (PID: %d)\n", agentName, pid)
			fmt.Fprintf(out, "  Logs:   %s\n", logPath)
			fmt.Fprintf(out, "  Socket: %s\n", r.AgentSocketPath(agentName))

			return nil
		},
	}

	cmd.Flags().StringVar(&coords, "coords", "", "Initial spawn coordinates formatted as 'X,Y,Z'")
	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground directly instead of background daemon")
	cmd.Flags().BoolVar(&restartOnExit, "restart-on-exit", false, "Enable supervisor watchdog to automatically restart agent if it exits unexpectedly")

	return cmd
}

func newAgentStopCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Gracefully stop an active agent process and despawn avatar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			agentName := r.ResolveAgentName(args[0])

			// Mark intentional stop so watchdog does not restart
			stopMarkerPath := filepath.Join(r.AgentStateDir(agentName), "stop.marker")
			_ = os.WriteFile(stopMarkerPath, []byte("stopped"), 0644)
			defer os.Remove(stopMarkerPath)

			// Cancel in-process watchdog if present
			stopInProcessWatchdog(agentName)

			// Signal watchdog process to terminate if running as external process
			watchdogPIDPath := filepath.Join(r.AgentStateDir(agentName), "watchdog.pid")
			if data, err := os.ReadFile(watchdogPIDPath); err == nil {
				if wpid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && wpid > 0 && wpid != os.Getpid() {
					if wproc, err := os.FindProcess(wpid); err == nil {
						_ = wproc.Signal(syscall.SIGTERM)
					}
				}
				_ = os.Remove(watchdogPIDPath)
			}

			pidPath := r.AgentPIDPath(agentName)
			pidData, err := os.ReadFile(pidPath)
			if err != nil && os.IsNotExist(err) {
				return fmt.Errorf("agent %s is not running (no PID file at %s)", agentName, pidPath)
			}

			pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
			if err != nil || pid <= 0 {
				_ = os.Remove(pidPath)
				return fmt.Errorf("invalid PID data in %s", pidPath)
			}

			// Despawn entity in Minecraft via administrative gRPC client
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if admin, err := GetAdminClient(r); err == nil {
				defer admin.Close()
				_, _ = admin.DespawnAgentEntity(ctx, &civquestv1.DespawnAgentRequest{
					AgentId: agentName,
					Reason:  reason,
				})
			}

			// Signal OS process
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGTERM)

				// Wait up to 3 seconds for graceful shutdown
				stopped := false
				for i := 0; i < 30; i++ {
					time.Sleep(100 * time.Millisecond)
					if err := proc.Signal(syscall.Signal(0)); err != nil {
						stopped = true
						break
					}
				}

				if !stopped {
					// Force kill if unresponsive
					_ = proc.Kill()
				}
			}

			_ = os.Remove(pidPath)
			_ = os.Remove(r.AgentSocketPath(agentName))

			if flagJSON {
				data, _ := json.MarshalIndent(map[string]any{
					"success": true,
					"name":    agentName,
					"stopped": true,
					"pid":     pid,
				}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Agent '%s' (PID %d) stopped successfully.\n", agentName, pid)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "Stopped via cq", "Despawn reason")
	return cmd
}

func newAgentStartClanCmd() *cobra.Command {
	var count int

	cmd := &cobra.Command{
		Use:   "start-clan <clan_id>",
		Short: "Batch start all configured agents belonging to a clan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clanID := args[0]
			r := getResolver()

			status, err := domain.DetectAgents(r)
			if err != nil {
				return err
			}

			var targets []domain.AgentSummary
			for _, a := range status.ConfiguredAgents {
				if strings.EqualFold(a.ClanID, clanID) {
					targets = append(targets, a)
				}
			}

			if len(targets) == 0 {
				return fmt.Errorf("no agents configured for clan %q in %s", clanID, r.AgentsConfigDir())
			}

			if count > 0 && count < len(targets) {
				targets = targets[:count]
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Starting %d agents for clan %q...\n", len(targets), clanID)

			started := 0
			for _, t := range targets {
				startCmd := newAgentStartCmd()
				startCmd.SetOut(out)
				startCmd.SetErr(cmd.ErrOrStderr())
				startCmd.SetArgs([]string{t.Name})
				if err := startCmd.Execute(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ Failed starting agent %s: %v\n", t.Name, err)
				} else {
					started++
				}
			}

			fmt.Fprintf(out, "✓ Started %d/%d agents for clan %q.\n", started, len(targets), clanID)
			return nil
		},
	}

	cmd.Flags().IntVar(&count, "count", 0, "Maximum number of clan agents to launch (default 0 = all)")
	return cmd
}

func newAgentLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
	)

	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "View or tail the log file of an autonomous agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			agentName := r.ResolveAgentName(args[0])

			logPath := r.AgentLogPath(agentName)
			if _, statErr := os.Stat(logPath); os.IsNotExist(statErr) {
				if _, centralErr := os.Stat(r.AgentHumanLogPath(agentName)); centralErr == nil {
					logPath = r.AgentHumanLogPath(agentName)
				} else if matches, _ := filepath.Glob(filepath.Join(r.StateDir(), "servers", "*", "agents", agentName, agentName+".log")); len(matches) > 0 {
					logPath = matches[len(matches)-1]
				}
			}
			file, err := os.Open(logPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("log file does not exist at %s (has agent %s ever run?)", logPath, agentName)
				}
				return fmt.Errorf("failed opening log file: %w", err)
			}
			defer file.Close()

			if lines <= 0 {
				lines = 50
			}

			reader := bufio.NewReader(file)
			var ring []string
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					line = strings.TrimRight(line, "\r\n")
					ring = append(ring, line)
					if len(ring) > lines {
						ring = ring[1:]
					}
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return fmt.Errorf("error reading log file: %w", err)
				}
			}

			out := cmd.OutOrStdout()
			for _, line := range ring {
				fmt.Fprintln(out, line)
			}

			if !follow {
				return nil
			}

			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			ctx := cmd.Context()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					for {
						line, err := reader.ReadString('\n')
						if len(line) > 0 {
							fmt.Fprintln(out, strings.TrimRight(line, "\r\n"))
						}
						if err != nil {
							break
						}
					}
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in real time")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Number of lines to display")

	return cmd
}

func resolveAgentBinary(r *xdg.Resolver) (string, error) {
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
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && (fi.Mode()&0111 != 0) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}

	return "", fmt.Errorf("civquest-agent binary not found in standard paths; build it via 'go build -o ~/.local/bin/civquest-agent ./cmd/civquest-agent'")
}

func createDefaultSkinPNG(targetPath string) error {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	tunicColor := color.RGBA{R: 139, G: 90, B: 43, A: 255}
	skinColor := color.RGBA{R: 210, G: 161, B: 125, A: 255}

	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if y < 16 {
				img.Set(x, y, skinColor)
			} else {
				img.Set(x, y, tunicColor)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return png.Encode(f, img)
}

// isTestProcess returns true if running under a go test runner.
func isTestProcess() bool {
	if os.Getenv("CIVQUEST_IN_TEST") != "" {
		return true
	}
	exe := os.Args[0]
	return strings.HasSuffix(exe, ".test") || strings.Contains(exe, "__debug_bin")
}

// WatchdogOptions configures backoff durations and retry counts for supervisor keep-alive.
type WatchdogOptions struct {
	CleanExitBackoff time.Duration
	CrashBackoffs    []time.Duration
	MaxRetries       int
	CheckDeath       func() bool
	OnRestart        func(attempt int, delay time.Duration, isCrash bool)
	OnPermadeath     func()
	OnMaxRetries     func(exitCode int)
}

func defaultWatchdogOptions(r *xdg.Resolver, agentName string) WatchdogOptions {
	cleanBackoff := 3 * time.Second
	crashBackoffs := []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second}
	maxRetries := 3

	if os.Getenv("CIVQUEST_WATCHDOG_TEST_FAST") != "" {
		cleanBackoff = 10 * time.Millisecond
		crashBackoffs = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	}

	return WatchdogOptions{
		CleanExitBackoff: cleanBackoff,
		CrashBackoffs:    crashBackoffs,
		MaxRetries:       maxRetries,
		CheckDeath: func() bool {
			return isDeathEventReceived(r, agentName)
		},
	}
}

// isDeathEventReceived detects if a terminal permadeath death event occurred for this agent.
func isDeathEventReceived(r *xdg.Resolver, agentName string) bool {
	// 1. Check if graveyard contains an archive for this agent
	graveyardDir := r.GraveyardDir()
	if matches, err := filepath.Glob(filepath.Join(graveyardDir, fmt.Sprintf("%s_*", agentName))); err == nil && len(matches) > 0 {
		return true
	}

	// 2. Check if death certificate or death event exists in agent data or state dir
	if _, err := os.Stat(filepath.Join(r.AgentDataDir(agentName), "death_certificate.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(r.AgentStateDir(agentName), "death_event.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(r.AgentStateDir(agentName), "permadeath.marker")); err == nil {
		return true
	}

	// 3. Check agent log file for permadeath log lines
	logPath := r.AgentLogPath(agentName)
	if data, err := os.ReadFile(logPath); err == nil {
		content := string(data)
		if strings.Contains(content, "Permadeath event recorded") ||
			strings.Contains(content, "Permadeath complete") ||
			strings.Contains(content, "Clean permadeath exit code 0") {
			return true
		}
	}

	return false
}

// runGraveyardMigration ensures deceased agent state is relocated to the graveyard directory.
func runGraveyardMigration(r *xdg.Resolver, agentName string) error {
	sourceDir := r.AgentDataDir(agentName)
	graveyardDir := r.GraveyardDir()
	_ = os.MkdirAll(graveyardDir, 0750)

	// If sourceDir still exists, relocate it to graveyard
	if fi, err := os.Stat(sourceDir); err == nil && fi.IsDir() {
		targetDir := filepath.Join(graveyardDir, fmt.Sprintf("%s_graveyard_%d", agentName, time.Now().Unix()))
		if renameErr := os.Rename(sourceDir, targetDir); renameErr != nil {
			_ = copyDirHelper(sourceDir, targetDir)
			_ = os.RemoveAll(sourceDir)
		}
	}

	// Clean up PID, socket, and watchdog state
	_ = os.Remove(r.AgentPIDPath(agentName))
	_ = os.Remove(r.AgentSocketPath(agentName))
	_ = os.Remove(filepath.Join(r.AgentStateDir(agentName), "watchdog.pid"))
	_ = os.Remove(filepath.Join(r.AgentStateDir(agentName), "stop.marker"))

	return nil
}

func copyDirHelper(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

var (
	activeWatchdogMu     sync.Mutex
	activeWatchdogCancel = make(map[string]context.CancelFunc)
)

func registerInProcessWatchdog(name string, cancel context.CancelFunc) {
	activeWatchdogMu.Lock()
	defer activeWatchdogMu.Unlock()
	activeWatchdogCancel[name] = cancel
}

func stopInProcessWatchdog(name string) {
	activeWatchdogMu.Lock()
	defer activeWatchdogMu.Unlock()
	if cancel, ok := activeWatchdogCancel[name]; ok {
		cancel()
		delete(activeWatchdogCancel, name)
	}
}

// runAgentWatchdog executes the supervisor watchdog loop for an agent.
func runAgentWatchdog(
	ctx context.Context,
	r *xdg.Resolver,
	cfg *xdg.AgentIdentityConfig,
	binPath string,
	agentArgs []string,
	coords string,
	out io.Writer,
	opts WatchdogOptions,
	readyChan chan struct{},
) error {
	agentName := cfg.Name
	pidPath := r.AgentPIDPath(agentName)
	logPath := r.AgentLogPath(agentName)
	stopMarkerPath := filepath.Join(r.AgentStateDir(agentName), "stop.marker")
	watchdogPIDPath := filepath.Join(r.AgentStateDir(agentName), "watchdog.pid")

	watchdogCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	registerInProcessWatchdog(agentName, cancel)
	defer stopInProcessWatchdog(agentName)

	if !isTestProcess() {
		_ = os.WriteFile(watchdogPIDPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
		defer os.Remove(watchdogPIDPath)
	}

	if err := os.MkdirAll(r.AgentStateDir(agentName), 0755); err != nil {
		return fmt.Errorf("failed creating agent state dir: %w", err)
	}
	_ = os.MkdirAll(r.LogsDir(), 0750)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("failed opening agent log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	retries := 0
	firstSpawn := true

	readySignaled := false
	signalReady := func() {
		if readyChan != nil && !readySignaled {
			readySignaled = true
			close(readyChan)
		}
	}

	for {
		select {
		case <-watchdogCtx.Done():
			signalReady()
			return nil
		default:
		}

		fmt.Fprintf(logFile, "=== [%s] Supervisor Watchdog Spawning Agent: %s ===\n",
			time.Now().Format(time.RFC3339), agentName)

		proc, err := launcher.LaunchProcess(launcher.ProcessOptions{
			Command: binPath,
			Args:    agentArgs,
			Stdout:  logFile,
			Stderr:  logFile,
			SysProcAttr: &syscall.SysProcAttr{
				Setpgid: true,
			},
		})
		if err != nil {
			signalReady()
			return fmt.Errorf("watchdog failed launching agent %s: %w", agentName, err)
		}

		pid := proc.PID()
		if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", pid)), 0644); err != nil {
			_ = proc.Kill()
			signalReady()
			return fmt.Errorf("watchdog failed writing agent PID file: %w", err)
		}

		startTime := time.Now()
		signalReady()

		if firstSpawn && coords != "" {
			firstSpawn = false
			if parsed, err := parseCoords(coords); err == nil && parsed != nil {
				go func() {
					cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if admin, err := GetAdminClient(r); err == nil {
						defer admin.Close()
						_, _ = admin.SpawnAgentEntity(cctx, &civquestv1.SpawnAgentRequest{
							AgentId:     cfg.Name,
							DisplayName: cfg.Name,
							ClanId:      cfg.ClanID,
							SpawnCoords: parsed,
						})
					}
				}()
			}
		}

		// Wait for process exit or context cancellation
		var exitCode int
		select {
		case <-watchdogCtx.Done():
			_ = proc.Signal(syscall.SIGTERM)
			select {
			case <-proc.ProcessExited():
			case <-time.After(2 * time.Second):
				_ = proc.Kill()
			}
			_ = os.Remove(pidPath)
			return nil

		case code := <-proc.ProcessExited():
			exitCode = code
		}

		// Process exited!
		_ = os.Remove(pidPath)
		_ = os.Remove(r.AgentSocketPath(agentName))

		// Check if intentionally stopped via cq stop
		if _, err := os.Stat(stopMarkerPath); err == nil {
			_ = os.Remove(stopMarkerPath)
			_ = os.Remove(watchdogPIDPath)
			fmt.Fprintf(logFile, "=== [%s] Agent '%s' intentionally stopped via cq. Watchdog exiting cleanly. ===\n",
				time.Now().Format(time.RFC3339), agentName)
			return nil
		}

		// Check if permadeath occurred
		hasDied := false
		if opts.CheckDeath != nil {
			hasDied = opts.CheckDeath()
		} else {
			hasDied = isDeathEventReceived(r, agentName)
		}

		if hasDied {
			// Permadeath!
			// Clean exit code 0 + DeathEvent
			// Do NOT restart. Run graveyard migration.
			_ = runGraveyardMigration(r, agentName)
			fmt.Fprintf(logFile, "=== [%s] Agent '%s' experienced permadeath (exit code %d). Not restarting. ===\n",
				time.Now().Format(time.RFC3339), agentName, exitCode)
			if out != nil {
				fmt.Fprintf(out, "• Agent '%s' experienced permadeath (exit code %d). Watchdog exiting without restart.\n", agentName, exitCode)
			}
			if opts.OnPermadeath != nil {
				opts.OnPermadeath()
			}
			return nil
		}

		// NO DeathEvent was received
		// Reset retry counter if process ran stably (> 30s)
		if time.Since(startTime) > 30*time.Second {
			retries = 0
		}

		if exitCode == 0 {
			// Clean exit or SIGTERM without DeathEvent
			// Restart after 3s backoff
			backoff := opts.CleanExitBackoff
			fmt.Fprintf(logFile, "=== [%s] Agent '%s' exited cleanly without DeathEvent (crash/SIGTERM, code 0). Restarting after %v... ===\n",
				time.Now().Format(time.RFC3339), agentName, backoff)
			if out != nil {
				fmt.Fprintf(out, "• Agent '%s' exited unexpectedly (code 0). Restarting after %v...\n", agentName, backoff)
			}
			if opts.OnRestart != nil {
				opts.OnRestart(0, backoff, false)
			}

			select {
			case <-watchdogCtx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}

		// Non-zero exit code: crash!
		if retries >= opts.MaxRetries {
			fmt.Fprintf(logFile, "=== [%s] Agent '%s' crashed with code %d. Max retries (%d) exceeded. Watchdog stopping. ===\n",
				time.Now().Format(time.RFC3339), agentName, exitCode, opts.MaxRetries)
			if out != nil {
				fmt.Fprintf(out, "✗ Agent '%s' crashed with code %d. Max restart retries (%d) exceeded.\n", cfg.Name, exitCode, opts.MaxRetries)
			}
			if opts.OnMaxRetries != nil {
				opts.OnMaxRetries(exitCode)
			}
			return fmt.Errorf("agent %s crashed with code %d; max restart retries (%d) exceeded", agentName, exitCode, opts.MaxRetries)
		}

		backoffIdx := retries
		if backoffIdx >= len(opts.CrashBackoffs) {
			backoffIdx = len(opts.CrashBackoffs) - 1
		}
		backoff := opts.CrashBackoffs[backoffIdx]
		retries++

		fmt.Fprintf(logFile, "=== [%s] Agent '%s' crashed with code %d (attempt %d/%d). Restarting after %v... ===\n",
			time.Now().Format(time.RFC3339), agentName, exitCode, retries, opts.MaxRetries, backoff)
		if out != nil {
			fmt.Fprintf(out, "• Agent '%s' crashed (code %d, attempt %d/%d). Restarting after %v...\n", agentName, exitCode, retries, opts.MaxRetries, backoff)
		}
		if opts.OnRestart != nil {
			opts.OnRestart(retries, backoff, true)
		}

		select {
		case <-watchdogCtx.Done():
			return nil
		case <-time.After(backoff):
		}
		continue
	}
}

func newAgentSkillCmd() *cobra.Command {
	skillCmd := &cobra.Command{
		Use:   "skill",
		Short: "Import and export agent sleep-state strategic reasoning prompts",
		Long: `Controls importing and exporting of specialized System 2 prompts
assigned per agent (or across all agents), used exclusively during
sleep-state strategic reasoning / nighttime contemplation.`,
	}

	skillCmd.AddCommand(newAgentSkillImportCmd())
	skillCmd.AddCommand(newAgentSkillExportCmd())

	return skillCmd
}

func parseAgentSkillArgs(args []string) (target string, filespec string, isAll bool, showHelp bool, err error) {
	var filtered []string
	for _, a := range args {
		trimmed := strings.TrimSpace(a)
		if trimmed == "--help" || trimmed == "-h" || trimmed == "help" {
			return "", "", false, true, nil
		}
		if trimmed == "--json" {
			flagJSON = true
			continue
		}
		if trimmed == "-all" || trimmed == "--all" || trimmed == "-a" || trimmed == "all" {
			isAll = true
			continue
		}
		if strings.HasPrefix(trimmed, "--all=") {
			isAll = true
			continue
		}
		filtered = append(filtered, trimmed)
	}

	if isAll {
		if len(filtered) != 1 {
			return "", "", true, false, errors.New("usage: cq agent skill [import|export] [<name>|-all] <filespec>")
		}
		return "", filtered[0], true, false, nil
	}

	if len(filtered) != 2 {
		return "", "", false, false, errors.New("usage: cq agent skill [import|export] [<name>|-all] <filespec>")
	}

	return filtered[0], filtered[1], false, false, nil
}

func newAgentSkillImportCmd() *cobra.Command {
	var allFlag bool
	cmd := &cobra.Command{
		Use:   "import [<name>|-all] <filespec>",
		Short: "Import a custom sleep-state strategic reasoning prompt from file",
		Long: `Imports a custom system prompt from <filespec> to be used exclusively
during sleep-state strategic contemplation.

Examples:
  cq agent skill import solas ./prompts/mason.prompt
  cq agent skill import -all ./prompts/shared.prompt
  cq agent skill import --all ./prompts/shared.prompt`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if allFlag {
				args = append([]string{"--all"}, args...)
			}
			target, filespec, isAll, showHelp, err := parseAgentSkillArgs(args)
			if showHelp {
				return cmd.Help()
			}
			if err != nil {
				return err
			}

			data, err := os.ReadFile(filespec)
			if err != nil {
				return fmt.Errorf("failed to read skill file %s: %w", filespec, err)
			}
			content := strings.TrimSpace(string(data))
			if content == "" {
				return fmt.Errorf("skill prompt file %s is empty", filespec)
			}

			r := getResolver()

			if !isAll {
				agentName := r.ResolveAgentName(target)
				cfg, err := r.LoadAgentConfig(agentName)
				if err != nil {
					return fmt.Errorf("agent %q not found or invalid in %s: %w", target, r.AgentsConfigDir(), err)
				}
				if cfg.Name != "" {
					agentName = cfg.Name
				}

				if err := r.SaveAgentSkill(agentName, content); err != nil {
					return fmt.Errorf("failed to save skill prompt for agent %q: %w", agentName, err)
				}

				if flagJSON {
					resp := map[string]any{
						"status":     "ok",
						"agent":      agentName,
						"file":       filespec,
						"bytes":      len(content),
						"skill_path": r.AgentSkillPath(agentName),
					}
					out, _ := json.MarshalIndent(resp, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(out))
					return nil
				}

				fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully imported sleep-state strategic skill prompt for agent %q from %s (%d bytes).\n", agentName, filespec, len(content))
				return nil
			}

			// Batch import across all configured agents
			var agentNames []string
			status, _ := domain.DetectAgents(r)
			for _, a := range status.ConfiguredAgents {
				if a.Name != "" {
					agentNames = append(agentNames, a.Name)
				}
			}
			if entries, err := os.ReadDir(r.AgentsConfigDir()); err == nil {
				seen := make(map[string]bool)
				for _, name := range agentNames {
					seen[strings.ToLower(name)] = true
				}
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					ename := entry.Name()
					if strings.HasSuffix(ename, ".yaml") || strings.HasSuffix(ename, ".yml") {
						stem := strings.TrimSuffix(strings.TrimSuffix(ename, ".yaml"), ".yml")
						if !seen[strings.ToLower(stem)] {
							if cfg, err := r.LoadAgentConfig(stem); err == nil && cfg.Name != "" {
								agentNames = append(agentNames, cfg.Name)
								seen[strings.ToLower(cfg.Name)] = true
								seen[strings.ToLower(stem)] = true
							}
						}
					}
				}
			}

			if len(agentNames) == 0 {
				return fmt.Errorf("no configured agents found in %s to import skill into", r.AgentsConfigDir())
			}

			var imported []string
			for _, name := range agentNames {
				if err := r.SaveAgentSkill(name, content); err == nil {
					imported = append(imported, name)
				}
			}

			if flagJSON {
				resp := map[string]any{
					"status":   "ok",
					"imported": imported,
					"file":     filespec,
					"bytes":    len(content),
				}
				out, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully imported sleep-state strategic skill prompt for %d agents from %s (%d bytes):\n", len(imported), filespec, len(content))
			for _, name := range imported {
				fmt.Fprintf(cmd.OutOrStdout(), "  • %s\n", name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Apply to all configured agents")
	cmd.Flags().BoolVarP(&allFlag, "all-alias", "l", false, "Alias for -all")
	_ = cmd.Flags().MarkHidden("all-alias")
	return cmd
}

func newAgentSkillExportCmd() *cobra.Command {
	var allFlag bool
	cmd := &cobra.Command{
		Use:   "export [<name>|-all] <filespec>",
		Short: "Export a sleep-state strategic reasoning prompt to file",
		Long: `Exports an agent's custom sleep-state strategic prompt (or default prompt
if none is set) to <filespec>. If -all is specified, per-agent files are exported
into the target directory.

Examples:
  cq agent skill export solas ./prompts/solas.prompt
  cq agent skill export solas -
  cq agent skill export -all ./prompts/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if allFlag {
				args = append([]string{"--all"}, args...)
			}
			target, filespec, isAll, showHelp, err := parseAgentSkillArgs(args)
			if showHelp {
				return cmd.Help()
			}
			if err != nil {
				return err
			}

			r := getResolver()

			if !isAll {
				agentName := r.ResolveAgentName(target)
				cfg, err := r.LoadAgentConfig(agentName)
				if err != nil {
					return fmt.Errorf("agent %q not found or invalid in %s: %w", target, r.AgentsConfigDir(), err)
				}
				if cfg.Name != "" {
					agentName = cfg.Name
				}

				prompt, err := r.LoadAgentSkill(agentName)
				isCustom := true
				if err != nil || strings.TrimSpace(prompt) == "" {
					prompt = system2.DefaultContemplationPrompt(agentName, cfg.Profession, cfg.ClanID)
					isCustom = false
				}

				if filespec == "-" {
					fmt.Fprint(cmd.OutOrStdout(), prompt)
					if !strings.HasSuffix(prompt, "\n") {
						fmt.Fprintln(cmd.OutOrStdout())
					}
					return nil
				}

				if err := os.MkdirAll(filepath.Dir(filespec), 0755); err != nil {
					return fmt.Errorf("failed to create directory for %s: %w", filespec, err)
				}
				if err := os.WriteFile(filespec, []byte(prompt+"\n"), 0644); err != nil {
					return fmt.Errorf("failed to write skill file %s: %w", filespec, err)
				}

				if flagJSON {
					resp := map[string]any{
						"status":    "ok",
						"agent":     agentName,
						"file":      filespec,
						"bytes":     len(prompt),
						"is_custom": isCustom,
					}
					out, _ := json.MarshalIndent(resp, "", "  ")
					fmt.Fprintln(cmd.OutOrStdout(), string(out))
					return nil
				}

				tag := "custom"
				if !isCustom {
					tag = "default"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully exported %s sleep-state strategic skill prompt for agent %q to %s (%d bytes).\n", tag, agentName, filespec, len(prompt))
				return nil
			}

			// Batch export across all configured agents
			var agentConfigs []*xdg.AgentIdentityConfig
			status, _ := domain.DetectAgents(r)
			seen := make(map[string]bool)
			for _, a := range status.ConfiguredAgents {
				if a.Name != "" && !seen[strings.ToLower(a.Name)] {
					if cfg, err := r.LoadAgentConfig(a.Name); err == nil && cfg != nil {
						agentConfigs = append(agentConfigs, cfg)
						seen[strings.ToLower(a.Name)] = true
					}
				}
			}
			if entries, err := os.ReadDir(r.AgentsConfigDir()); err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					ename := entry.Name()
					if strings.HasSuffix(ename, ".yaml") || strings.HasSuffix(ename, ".yml") {
						stem := strings.TrimSuffix(strings.TrimSuffix(ename, ".yaml"), ".yml")
						if !seen[strings.ToLower(stem)] {
							if cfg, err := r.LoadAgentConfig(stem); err == nil && cfg != nil {
								agentConfigs = append(agentConfigs, cfg)
								seen[strings.ToLower(cfg.Name)] = true
								seen[strings.ToLower(stem)] = true
							}
						}
					}
				}
			}

			if len(agentConfigs) == 0 {
				return fmt.Errorf("no configured agents found in %s to export skills from", r.AgentsConfigDir())
			}

			targetDir := filespec
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
			}

			type exportedItem struct {
				Agent    string `json:"agent"`
				Path     string `json:"path"`
				Bytes    int    `json:"bytes"`
				IsCustom bool   `json:"is_custom"`
			}
			var exportedList []exportedItem

			for _, cfg := range agentConfigs {
				prompt, err := r.LoadAgentSkill(cfg.Name)
				isCustom := true
				if err != nil || strings.TrimSpace(prompt) == "" {
					prompt = system2.DefaultContemplationPrompt(cfg.Name, cfg.Profession, cfg.ClanID)
					isCustom = false
				}

				outPath := filepath.Join(targetDir, fmt.Sprintf("%s.skill.md", cfg.Name))
				if err := os.WriteFile(outPath, []byte(prompt+"\n"), 0644); err != nil {
					return fmt.Errorf("failed to write export file %s: %w", outPath, err)
				}

				exportedList = append(exportedList, exportedItem{
					Agent:    cfg.Name,
					Path:     outPath,
					Bytes:    len(prompt),
					IsCustom: isCustom,
				})
			}

			if flagJSON {
				resp := map[string]any{
					"status":    "ok",
					"directory": targetDir,
					"exported":  exportedList,
				}
				out, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully exported sleep-state strategic skill prompts for %d agents to %s:\n", len(exportedList), targetDir)
			for _, item := range exportedList {
				tag := "custom"
				if !item.IsCustom {
					tag = "default"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  • %-12s [%s] -> %s (%d bytes)\n", item.Agent, tag, item.Path, item.Bytes)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Apply to all configured agents")
	cmd.Flags().BoolVarP(&allFlag, "all-alias", "l", false, "Alias for -all")
	_ = cmd.Flags().MarkHidden("all-alias")
	return cmd
}
