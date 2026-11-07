package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/server"
)

func newServerCmd() *cobra.Command {
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the CivQuest NeoForge dedicated server lifecycle",
		Long: `Controls the background execution of the NeoForge server hosting
the CivQuest mod, sensor frustum broadcaster, and Baritone pathfinder.`,
	}

	serverCmd.AddCommand(newServerStartCmd())
	serverCmd.AddCommand(newServerStopCmd())
	serverCmd.AddCommand(newServerStatusCmd())
	serverCmd.AddCommand(newServerLogsCmd())

	return serverCmd
}

func newServerStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the NeoForge server in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			mgr := server.NewManager(r)

			status, err := mgr.Status()
			if err != nil {
				return err
			}

			if !status.Installed {
				return fmt.Errorf("server domain is not installed in %s: run './install.sh server' to install it", r.ServerDir())
			}

			if status.Running {
				fmt.Fprintf(cmd.OutOrStdout(), "Server is already running with PID %d.\n", status.PID)
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Starting CivQuest NeoForge server in background...")
			pid, err := mgr.Start()
			if err != nil {
				return fmt.Errorf("failed starting server: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ CivQuest server started in background (PID: %d)\n", pid)
			fmt.Fprintf(cmd.OutOrStdout(), "  Server Dir: %s\n", r.ServerDir())
			fmt.Fprintf(cmd.OutOrStdout(), "  Log File:   %s\n", r.ServerLogPath())
			return nil
		},
	}
}

func newServerStopCmd() *cobra.Command {
	var timeoutSeconds int
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Gracefully stop the running NeoForge server",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			mgr := server.NewManager(r)

			status, err := mgr.Status()
			if err != nil {
				return err
			}

			if !status.Installed {
				return fmt.Errorf("server domain is not installed in %s: run './install.sh server' to install it", r.ServerDir())
			}

			if !status.Running {
				fmt.Fprintln(cmd.OutOrStdout(), "Server is not currently running.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Stopping CivQuest server (PID %d)...\n", status.PID)
			timeout := time.Duration(timeoutSeconds) * time.Second
			if err := mgr.Stop(cmd.Context(), timeout); err != nil {
				if errors.Is(err, server.ErrServerNotRunning) {
					fmt.Fprintln(cmd.OutOrStdout(), "Server is not currently running.")
					return nil
				}
				return fmt.Errorf("error stopping server: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "✓ CivQuest server stopped gracefully.")
			return nil
		},
	}

	cmd.Flags().IntVarP(&timeoutSeconds, "timeout", "t", 15, "Seconds to wait gracefully before sending SIGKILL")
	return cmd
}

func newServerStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check whether the NeoForge server is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			mgr := server.NewManager(r)

			status, err := mgr.Status()
			if err != nil {
				return err
			}

			if flagJSON {
				data, err := json.MarshalIndent(status, "", "  ")
				if err != nil {
					return fmt.Errorf("failed marshaling json: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			if !status.Installed {
				return fmt.Errorf("server domain is not installed in %s: run './install.sh server' to install it", r.ServerDir())
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "==================================================")
			fmt.Fprintln(out, " CivQuest NeoForge Server Status")
			fmt.Fprintln(out, "==================================================")
			if status.Running {
				fmt.Fprintf(out, "Status:    RUNNING (PID: %d)\n", status.PID)
				if status.Uptime != "" {
					fmt.Fprintf(out, "Uptime:    %s\n", status.Uptime)
				}
			} else {
				fmt.Fprintln(out, "Status:    STOPPED")
			}
			fmt.Fprintf(out, "Directory: %s\n", status.ServerDir)
			if status.Manifest != nil {
				fmt.Fprintf(out, "Version:   %s (Minecraft %s / NeoForge %s)\n",
					status.Manifest.Version,
					status.Manifest.MinecraftVersion,
					status.Manifest.NeoForgeVersion,
				)
			}
			fmt.Fprintf(out, "Log File:  %s\n", status.LogPath)
			fmt.Fprintln(out, "==================================================")

			return nil
		},
	}
}

func newServerLogsCmd() *cobra.Command {
	var follow bool
	var lines int

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "View or tail the NeoForge server log",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			mgr := server.NewManager(r)

			status, err := mgr.Status()
			if err != nil {
				return err
			}

			if !status.Installed {
				return fmt.Errorf("server domain is not installed in %s: run './install.sh server' to install it", r.ServerDir())
			}

			return mgr.Logs(cmd.Context(), lines, follow, cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in real time")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Number of lines to display")
	return cmd
}
