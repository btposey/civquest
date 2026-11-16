package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/viewer"
)

func newAgentViewerCmd() *cobra.Command {
	return buildViewerCmd("viewer [agent_name]", []string{"view"}, "Launch the real-time Agent Viewer Web UI")
}

func newViewerCmd() *cobra.Command {
	return buildViewerCmd("viewer [agent_name]", []string{"view"}, "Launch the real-time Agent Viewer Web UI (alias for cq agent viewer)")
}

func buildViewerCmd(use string, aliases []string, short string) *cobra.Command {
	var (
		host      string
		port      int
		noBrowser bool
	)

	cmd := &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Short:   short,
		Long: `Starts the embedded CivQuest Agent State Viewer HTTP server and real-time
socket telemetry streamer, displaying HTPDA state stack breadcrumbs, circadian shift clocks,
vitals, equipment and inventory matrices, FactGraph blackboard facts, and System 2 reflections.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			srv := viewer.NewServer(viewer.Config{
				Host:     host,
				Port:     port,
				Resolver: r,
			})

			if err := srv.Start(); err != nil {
				return fmt.Errorf("starting agent viewer: %w", err)
			}

			viewerURL := srv.URL()
			if len(args) > 0 && args[0] != "" {
				viewerURL += "?agent=" + args[0]
			}

			fmt.Fprintf(cmd.OutOrStdout(), "CivQuest Agent Viewer listening at %s\n", viewerURL)

			if !noBrowser {
				go func() {
					time.Sleep(100 * time.Millisecond)
					_ = openBrowser(viewerURL)
				}()
			}

			// Await interrupt / shutdown signal
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down CivQuest Agent Viewer...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			return srv.Stop(ctx)
		},
	}

	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Viewer HTTP bind address")
	cmd.Flags().IntVar(&port, "port", 8766, "Viewer HTTP bind port")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not automatically launch web browser")

	return cmd
}
