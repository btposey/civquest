package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func newClanCmd() *cobra.Command {
	clanCmd := &cobra.Command{
		Use:   "clan",
		Short: "Inspect clan assets, hero prestige, and diplomatic relations",
		Long: `Provides administrative inspection of shared clan resources stored in SQLite:
granary stocks, functional zones, construction BOMs, human hero prestige, and inter-clan treaties.`,
	}

	clanCmd.AddCommand(newClanInspectCmd())
	clanCmd.AddCommand(newClanPrestigeCmd())
	clanCmd.AddCommand(newClanDiplomacyCmd())

	return clanCmd
}

func newClanInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <clan_id>",
		Short: "Display granary inventory, functional zones, and active construction BOMs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clanID := strings.TrimSpace(args[0])
			r := getResolver()

			storage, err := db.OpenClanStorage(r, clanID)
			if err != nil {
				return fmt.Errorf("opening clan storage for %s: %w", clanID, err)
			}
			defer storage.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// 1. Granary Inventory
			inventory, err := storage.CivicRegistry.GetGranaryInventoryContext(ctx)
			if err != nil {
				return fmt.Errorf("querying granary inventory: %w", err)
			}

			// 2. Functional Zones
			zones, err := storage.CivicRegistry.ListZonesContext(ctx)
			if err != nil {
				return fmt.Errorf("querying functional zones: %w", err)
			}

			// 3. Construction Projects
			projects, err := storage.CivicRegistry.ListProjectsContext(ctx)
			if err != nil {
				return fmt.Errorf("querying construction projects: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"clan_id":  clanID,
					"granary":  inventory,
					"zones":    zones,
					"projects": projects,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " CivQuest Clan Overview: %s\n", clanID)
			fmt.Fprintln(out, "======================================================================")

			fmt.Fprintf(out, "\n[1] Granary Inventory (%d items):\n", len(inventory))
			if len(inventory) == 0 {
				fmt.Fprintln(out, "    (granary storage is currently empty)")
			} else {
				for _, s := range inventory {
					avail := s.Available()
					fmt.Fprintf(out, "    • %-30s Total: %-5d Available: %-5d Reserved: %-5d\n",
						s.ItemID, s.Quantity, avail, s.ReservedQuantity)
				}
			}

			fmt.Fprintf(out, "\n[2] Functional Zones (%d zones):\n", len(zones))
			if len(zones) == 0 {
				fmt.Fprintln(out, "    (no functional zones registered)")
			} else {
				for _, z := range zones {
					fmt.Fprintf(out, "    • [%-10s] %-20s Bounds: (%d,%d,%d) to (%d,%d,%d)\n",
						z.ZoneType, z.Name, z.MinX, z.MinY, z.MinZ, z.MaxX, z.MaxY, z.MaxZ)
				}
			}

			fmt.Fprintf(out, "\n[3] Construction Projects (%d projects):\n", len(projects))
			if len(projects) == 0 {
				fmt.Fprintln(out, "    (no active architectural construction projects)")
			} else {
				for _, p := range projects {
					fmt.Fprintf(out, "    • %-16s Status: %-12s Progress: %5.1f%% Anchor: (%d,%d,%d)\n",
						p.BlueprintID, p.Status, p.ProgressPercent, p.AnchorX, p.AnchorY, p.AnchorZ)
				}
			}

			fmt.Fprintln(out, "======================================================================")
			return nil
		},
	}
}

func newClanPrestigeCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "prestige <clan_id>",
		Short: "Display the human player Hero Prestige leaderboard for a clan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clanID := strings.TrimSpace(args[0])
			r := getResolver()

			repDB, err := db.OpenReputationDBForClan(r, clanID)
			if err != nil {
				return fmt.Errorf("opening reputation DB for %s: %w", clanID, err)
			}
			defer repDB.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			leaderboard, err := repDB.ListHeroPrestigeContext(ctx, limit)
			if err != nil {
				return fmt.Errorf("listing hero prestige leaderboard: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"clan_id":     clanID,
					"leaderboard": leaderboard,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " Hero Prestige Leaderboard: Clan %s\n", clanID)
			fmt.Fprintln(out, "======================================================================")
			if len(leaderboard) == 0 {
				fmt.Fprintln(out, "No player prestige entries recorded yet.")
			} else {
				fmt.Fprintf(out, " %-4s %-20s %-12s %-10s %s\n", "Rank", "Player", "Title", "Score", "UUID")
				fmt.Fprintln(out, " --------------------------------------------------------------------")
				for idx, hp := range leaderboard {
					fmt.Fprintf(out, " #%-3d %-20s %-12s %+8.1f   %s\n",
						idx+1, hp.PlayerName, hp.Title, hp.Score, hp.PlayerUUID)
				}
			}
			fmt.Fprintln(out, "======================================================================")

			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 50, "Maximum number of leaderboard records to display")
	return cmd
}

func newClanDiplomacyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diplomacy <clan_id>",
		Short: "Display inter-clan affinity matrix, active treaties, and trade volumes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clanID := strings.TrimSpace(args[0])
			r := getResolver()

			diploDB, err := db.OpenDiplomacyDBForClan(r, clanID)
			if err != nil {
				return fmt.Errorf("opening diplomacy DB for %s: %w", clanID, err)
			}
			defer diploDB.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			affinities, err := diploDB.ListAffinitiesContext(ctx)
			if err != nil {
				return fmt.Errorf("listing clan affinities: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"clan_id":    clanID,
					"affinities": affinities,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " Diplomatic Relations & Treaties: Clan %s\n", clanID)
			fmt.Fprintln(out, "======================================================================")
			if len(affinities) == 0 {
				fmt.Fprintln(out, "No external clan diplomatic relations recorded.")
			} else {
				fmt.Fprintf(out, " %-16s %-10s %-12s %-16s %s\n",
					"Target Clan", "Affinity", "Status", "Active Treaty", "Trade Volume")
				fmt.Fprintln(out, " --------------------------------------------------------------------")
				for _, aff := range affinities {
					treatyStr := aff.ActiveTreaty
					if aff.IsTreatyActive(aff.LastUpdatedTick) && aff.TreatyExpiryTick > 0 {
						treatyStr = fmt.Sprintf("%s (exp tick %d)", aff.ActiveTreaty, aff.TreatyExpiryTick)
					}
					fmt.Fprintf(out, " %-16s %+7.1f    %-12s %-16s %d units\n",
						aff.TargetClanID, aff.AffinityScore, aff.Status, treatyStr, aff.TradeVolume)
				}
			}
			fmt.Fprintln(out, "======================================================================")

			return nil
		},
	}
}
