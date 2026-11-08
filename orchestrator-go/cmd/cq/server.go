package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/server"
)

func newServerCmd() *cobra.Command {
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the CivQuest NeoForge dedicated server lifecycle",
		Long: `Controls the background execution of the NeoForge server hosting
the CivQuest mod, sensor frustum broadcaster, and Baritone pathfinder.`,
	}

	serverCmd.AddCommand(newServerSetupCmd())
	serverCmd.AddCommand(newServerStartCmd())
	serverCmd.AddCommand(newServerStopCmd())
	serverCmd.AddCommand(newServerStatusCmd())
	serverCmd.AddCommand(newServerLogsCmd())
	serverCmd.AddCommand(newServerExportRecipesCmd())

	return serverCmd
}

func newServerSetupCmd() *cobra.Command {
	var (
		port       int
		onlineMode bool
		force      bool
		startAfter bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Prepare the NeoForge server environment for client playtesting",
		Long: `Prepares the server directory with pre-accepted EULA (eula=true),
server.properties configured for client connections (port 25565),
and runner scripts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if port < 1 || port > 65535 {
				return fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
			}

			r := getResolver()
			mgr := server.NewManager(r)

			propsExisted := false
			if _, err := os.Stat(r.ServerPropertiesPath()); err == nil {
				propsExisted = true
			}

			opts := server.DefaultSetupOptions()
			opts.Port = port
			opts.OnlineMode = onlineMode
			opts.Force = force

			if err := mgr.Setup(opts); err != nil {
				return fmt.Errorf("failed setting up server: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "✓ CivQuest server environment prepared successfully:")
			fmt.Fprintf(out, "  Directory:         %s\n", r.ServerDir())
			fmt.Fprintf(out, "  EULA:              %s (eula=true)\n", r.ServerEULAPath())
			if propsExisted && !force {
				fmt.Fprintf(out, "  server.properties: %s (preserved existing configuration; use --force to overwrite)\n", r.ServerPropertiesPath())
			} else {
				fmt.Fprintf(out, "  server.properties: %s (port=%d, online-mode=%t)\n", r.ServerPropertiesPath(), port, onlineMode)
			}

			if startAfter {
				fmt.Fprintln(out, "Starting CivQuest NeoForge server in background...")
				pid, err := mgr.Start()
				if err != nil {
					return fmt.Errorf("failed starting server: %w", err)
				}
				fmt.Fprintf(out, "✓ CivQuest server started in background (PID: %d)\n", pid)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 25565, "Dedicated server port for Minecraft clients")
	cmd.Flags().BoolVar(&onlineMode, "online-mode", false, "Enable Mojang online mode authentication")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing server configuration files")
	cmd.Flags().BoolVar(&startAfter, "start", false, "Immediately launch server in background after setup")

	return cmd
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

func newServerExportRecipesCmd() *cobra.Command {
	var outputFile string

	cmd := &cobra.Command{
		Use:   "export-recipes",
		Short: "Request and export Minecraft recipe graph from the server mod over gRPC",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			admin, err := GetAdminClient(r)
			if err != nil {
				return fmt.Errorf("connecting to administrative gRPC bridge: %w", err)
			}
			defer admin.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			resp, err := admin.ExportRecipeGraph(ctx, &civquestv1.ExportRecipeRequest{})
			if err != nil {
				return fmt.Errorf("failed exporting recipe graph from server: %w", err)
			}

			if outputFile != "" {
				data, err := json.MarshalIndent(resp.GetRecipes(), "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(outputFile, data, 0644); err != nil {
					return fmt.Errorf("writing recipes to %s: %w", outputFile, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ Exported %d recipes to %s\n", len(resp.GetRecipes()), outputFile)
				return nil
			}

			if flagJSON {
				data, err := json.MarshalIndent(resp.GetRecipes(), "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " CivQuest Server Recipe Graph (%d recipes)\n", len(resp.GetRecipes()))
			fmt.Fprintln(out, "======================================================================")
			for idx, rc := range resp.GetRecipes() {
				if idx >= 20 {
					fmt.Fprintf(out, " ... and %d more recipes (use --output or --json to export all)\n", len(resp.GetRecipes())-20)
					break
				}
				fmt.Fprintf(out, " • %-36s [%s] -> %s x%d\n",
					rc.GetRecipeId(), rc.GetRecipeType(), rc.GetResult().GetItemId(), rc.GetResult().GetCount())
			}
			fmt.Fprintln(out, "======================================================================")
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Destination JSON file for exported recipe graph")
	return cmd
}
