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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
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

			if flagJSON {
				resp := map[string]any{
					"identity":    agentCfg,
					"active":      isActive,
					"pid":         pid,
					"socket_path": sockPath,
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
			fmt.Fprintf(out, "Socket:          %s\n", sockPath)
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
		coords     string
		foreground bool
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
