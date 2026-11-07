package main

import (
	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	flagConfigDir string
	flagDataDir   string
	flagStateDir  string
	flagJSON      bool
)

func getResolver() *xdg.Resolver {
	if flagConfigDir != "" || flagDataDir != "" || flagStateDir != "" {
		return xdg.NewResolver(xdg.WithExplicitDirs(flagConfigDir, flagDataDir, flagStateDir))
	}
	return xdg.DefaultResolver()
}

// NewRootCmd initializes and returns the top-level civctl Cobra command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "civctl",
		Short: "CivQuest Settlement Engine CLI & Supervisor",
		Long: `civctl is the management interface and process supervisor for CivQuest.
It provides control over the NeoForge server, autonomous settler agents,
clans, blueprints, and system health.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVar(&flagConfigDir, "config-dir", "", "Path to CivQuest config directory (default: $XDG_CONFIG_HOME/civquest)")
	cmd.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "Path to CivQuest data directory (default: $XDG_DATA_HOME/civquest)")
	cmd.PersistentFlags().StringVar(&flagStateDir, "state-dir", "", "Path to CivQuest state directory (default: $XDG_STATE_HOME/civquest)")
	cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output results in JSON format")

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newServerCmd())
	cmd.AddCommand(newAgentCmd())

	return cmd
}
