package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	flagConfigDir string
	flagDataDir   string
	flagStateDir  string
	flagServerID  string
	flagJSON      bool
)

func getResolver() *xdg.Resolver {
	var opts []xdg.Option
	if flagConfigDir != "" || flagDataDir != "" || flagStateDir != "" {
		cfg := flagConfigDir
		if cfg != "" {
			if abs, err := filepath.Abs(cfg); err == nil {
				cfg = abs
			}
		}
		data := flagDataDir
		if data != "" {
			if abs, err := filepath.Abs(data); err == nil {
				data = abs
			}
		}
		state := flagStateDir
		if state != "" {
			if abs, err := filepath.Abs(state); err == nil {
				state = abs
			}
		}
		opts = append(opts, xdg.WithExplicitDirs(cfg, data, state))
	}
	serverID := flagServerID
	if serverID == "" {
		if meta, err := bridge.ReadDiscovery(""); err == nil && meta.IsReady() && meta.ServerID != "" && meta.ServerID != "default" {
			serverID = meta.ServerID
		}
	}
	if serverID != "" {
		if err := xdg.ValidateServerID(serverID); err == nil {
			opts = append(opts, xdg.WithServerID(serverID))
		}
	}
	if len(opts) > 0 {
		return xdg.NewResolver(opts...)
	}
	return xdg.DefaultResolver()
}

// NewRootCmd initializes and returns the top-level cq Cobra command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cq",
		Short: "CivQuest Settlement Engine CLI & Supervisor",
		Long: `cq is the management interface and process supervisor for CivQuest.
It provides control over the NeoForge server, autonomous agents,
clans, blueprints, and system health.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if flagServerID != "" {
				if err := xdg.ValidateServerID(flagServerID); err != nil {
					return fmt.Errorf("invalid server ID %q: %w", flagServerID, err)
				}
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&flagConfigDir, "config-dir", "", "Path to CivQuest config directory (default: $XDG_CONFIG_HOME/civquest)")
	cmd.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "Path to CivQuest data directory (default: $XDG_DATA_HOME/civquest)")
	cmd.PersistentFlags().StringVar(&flagStateDir, "state-dir", "", "Path to CivQuest state directory (default: $XDG_STATE_HOME/civquest)")
	cmd.PersistentFlags().StringVar(&flagServerID, "server-id", "", "CivQuest server instance identifier")
	cmd.PersistentFlags().StringVar(&flagTarget, "target", "", "CivQuest server gRPC target address (e.g. 127.0.0.1:50051)")
	cmd.PersistentFlags().StringVar(&flagSharedKey, "shared-key", "", "Shared-key bearer token for gRPC authentication")
	cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output results in JSON format")

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newServerCmd())
	cmd.AddCommand(newAgentCmd())
	cmd.AddCommand(newClanCmd())
	cmd.AddCommand(newBlueprintCmd())
	cmd.AddCommand(newGatewayCmd())
	cmd.AddCommand(newClientCmd())

	return cmd
}
