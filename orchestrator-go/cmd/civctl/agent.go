package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
)

func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage and inspect autonomous settler agents",
		Long:  `Lists configured settler avatars, queries their active sockets, and checks status.`,
	}

	agentCmd.AddCommand(newAgentListCmd())
	agentCmd.AddCommand(newAgentStatusCmd())

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
				fmt.Fprintln(out, "Run './install.sh agents' or create YAML profiles to add agents.")
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
				// Delegate to list behavior if no specific agent was provided
				return newAgentListCmd().RunE(cmd, args)
			}

			agentName := args[0]
			agentCfg, err := r.LoadAgentConfig(agentName)
			if err != nil {
				return fmt.Errorf("agent %q not found or invalid in %s: %w", agentName, r.AgentsConfigDir(), err)
			}

			sockPath := r.AgentSocketPath(agentName)
			isActive := false
			if _, err := os.Stat(sockPath); err == nil {
				isActive = true
			}

			if flagJSON {
				resp := map[string]any{
					"identity":    agentCfg,
					"active":      isActive,
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
				stateStr = "Active (UNIX Socket Connected)"
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
