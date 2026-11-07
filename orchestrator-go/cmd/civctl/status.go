package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"info"},
		Short:   "Display CivQuest host installation topology and runtime status",
		Long: `Inspects local XDG directories, detects installed domains (server, agents, clans),
and reports live process and configuration states.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			topology, err := domain.DetectTopology(r)
			if err != nil {
				return fmt.Errorf("failed detecting topology: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(topology, "", "  ")
				if err != nil {
					return fmt.Errorf("failed marshaling json: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			// Format human-readable output
			var b strings.Builder
			b.WriteString("======================================================================\n")
			b.WriteString(" CivQuest Host Installation Topology\n")
			b.WriteString("======================================================================\n")
			b.WriteString("XDG Directories:\n")
			b.WriteString(fmt.Sprintf("  Config: %s\n", topology.ConfigDir))
			b.WriteString(fmt.Sprintf("  Data:   %s\n", topology.DataDir))
			b.WriteString(fmt.Sprintf("  State:  %s\n\n", topology.StateDir))

			// Server Domain
			b.WriteString("[Server Domain]\n")
			if topology.Server.Installed {
				b.WriteString("  Installed: Yes\n")
				if topology.Server.Running {
					b.WriteString(fmt.Sprintf("  Status:    Running (PID: %d", topology.Server.PID))
					if topology.Server.Uptime != "" {
						b.WriteString(fmt.Sprintf(", Uptime: %s", topology.Server.Uptime))
					}
					b.WriteString(")\n")
				} else {
					b.WriteString("  Status:    Stopped\n")
				}
				b.WriteString(fmt.Sprintf("  Directory: %s\n", topology.Server.ServerDir))
				if topology.Server.Manifest != nil {
					b.WriteString(fmt.Sprintf("  Version:   %s (Minecraft %s / NeoForge %s)\n",
						topology.Server.Manifest.Version,
						topology.Server.Manifest.MinecraftVersion,
						topology.Server.Manifest.NeoForgeVersion,
					))
				}
				b.WriteString(fmt.Sprintf("  Log File:  %s\n\n", topology.Server.LogPath))
			} else {
				b.WriteString("  Installed: No (Run './install.sh server' to deploy NeoForge server)\n\n")
			}

			// Agent Domain
			b.WriteString("[Agent Domain]\n")
			if topology.Agents.Installed {
				b.WriteString("  Installed: Yes\n")
				if topology.Agents.BinDir != "" {
					b.WriteString(fmt.Sprintf("  Binaries:  %s\n", topology.Agents.BinDir))
				}
				b.WriteString(fmt.Sprintf("  Configured Agents (%d):\n", len(topology.Agents.ConfiguredAgents)))
				if len(topology.Agents.ConfiguredAgents) == 0 {
					b.WriteString("    (None configured yet)\n")
				} else {
					for _, a := range topology.Agents.ConfiguredAgents {
						statusStr := "Inactive"
						if a.Active {
							statusStr = "Active"
						}
						b.WriteString(fmt.Sprintf("    - %-12s Clan: %-12s Profession: %-12s Status: %s\n",
							a.Name, a.ClanID, a.Profession, statusStr))
					}
				}
				b.WriteString("\n")
			} else {
				b.WriteString("  Installed: No (Run './install.sh agents' to deploy Go orchestrator)\n\n")
			}

			// Clan Domain
			b.WriteString("[Clan Domain]\n")
			if topology.Clans.Initialized {
				b.WriteString(fmt.Sprintf("  Initialized: Yes (%d active clans)\n", topology.Clans.ClanCount))
				b.WriteString(fmt.Sprintf("  Clans:       %s\n", strings.Join(topology.Clans.Clans, ", ")))
			} else {
				b.WriteString("  Initialized: No (clans data directory not initialized)\n")
			}
			b.WriteString("======================================================================\n")

			fmt.Fprint(cmd.OutOrStdout(), b.String())
			return nil
		},
	}
}
