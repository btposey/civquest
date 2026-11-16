package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/studio"
)

func newStudioCmd() *cobra.Command {
	var (
		host      string
		port      int
		noBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch the CivQuest Blueprint Studio web UI and REST API",
		Long: `Starts the embedded Blueprint Studio HTTP server, providing a web-based 3D
blueprint editor, palette and taxonomy catalogs, and real-time agent telemetry streaming.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			srv := studio.NewServer(studio.Config{
				Host:     host,
				Port:     port,
				Resolver: r,
			})

			if err := srv.Start(); err != nil {
				return fmt.Errorf("starting blueprint studio: %w", err)
			}

			studioURL := srv.URL()
			fmt.Fprintf(cmd.OutOrStdout(), "CivQuest Blueprint Studio listening at %s\n", studioURL)

			if !noBrowser {
				go func() {
					time.Sleep(100 * time.Millisecond)
					_ = openBrowser(studioURL)
				}()
			}

			// Await interrupt / shutdown signal
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down CivQuest Blueprint Studio...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			return srv.Stop(ctx)
		},
	}

	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Studio HTTP bind address")
	cmd.Flags().IntVar(&port, "port", 8765, "Studio HTTP bind port")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not automatically launch web browser")

	return cmd
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
