package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/launcher"
)

func newClientCmd() *cobra.Command {
	clientCmd := &cobra.Command{
		Use:   "client",
		Short: "Configure the Minecraft launcher to play CivQuest",
	}
	clientCmd.AddCommand(newClientInstallCmd())
	return clientCmd
}

func newClientInstallCmd() *cobra.Command {
	var (
		minecraftDir  string
		modJar        string
		installerPath string
		javaArgs      string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the NeoForge client, stage the mod, and add a CivQuest launcher profile",
		Long: `Prepares an isolated game directory ($XDG_DATA_HOME/civquest/client) containing the
installed CivQuest mod, ensures the NeoForge client version exists in the Minecraft
launcher directory, and adds a "CivQuest" profile to launcher_profiles.json.
Your existing launcher profiles and worlds are left untouched.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()
			out := cmd.OutOrStdout()

			status, err := domain.DetectServer(r)
			if err != nil {
				return err
			}
			if !status.Installed || status.Manifest == nil {
				return fmt.Errorf("server domain is not installed in %s: run './install.sh server' first", r.ServerDir())
			}
			m := status.Manifest

			if modJar == "" {
				modJar = filepath.Join(r.ServerModsDir(), m.ModJar)
			}
			if _, err := os.Stat(modJar); err != nil {
				return fmt.Errorf("mod jar not found at %s: %w", modJar, err)
			}

			if minecraftDir == "" {
				if minecraftDir, err = launcher.DefaultMinecraftDir(); err != nil {
					return err
				}
			}

			cacheDir := filepath.Join(r.DataDir(), "cache")
			if err := launcher.EnsureNeoForgeClient(cmd.Context(), minecraftDir, m.NeoForgeVersion, installerPath, cacheDir, out); err != nil {
				return err
			}

			gameDir := r.ClientDir()
			staged, err := launcher.StageMod(gameDir, modJar)
			if err != nil {
				return err
			}

			versionID := launcher.NeoForgeVersionID(m.NeoForgeVersion)
			if err := launcher.UpsertProfile(minecraftDir, gameDir, versionID, javaArgs); err != nil {
				return err
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"success":       true,
					"profile":       launcher.ProfileName,
					"version_id":    versionID,
					"game_dir":      gameDir,
					"mod":           staged,
					"minecraft_dir": minecraftDir,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			fmt.Fprintln(out, "✓ CivQuest launcher profile installed:")
			fmt.Fprintf(out, "  Profile:   %s (%s)\n", launcher.ProfileName, versionID)
			fmt.Fprintf(out, "  Game dir:  %s\n", gameDir)
			fmt.Fprintf(out, "  Mod:       %s\n", staged)
			fmt.Fprintln(out, "Open the Minecraft launcher and select the \"CivQuest\" profile.")
			return nil
		},
	}

	cmd.Flags().StringVar(&minecraftDir, "minecraft-dir", "", "Minecraft launcher directory (default: ~/.minecraft)")
	cmd.Flags().StringVar(&modJar, "mod-jar", "", "Mod jar to stage (default: the jar recorded in the installed server manifest)")
	cmd.Flags().StringVar(&installerPath, "neoforge-installer", "", "Local NeoForge installer jar (default: download from maven.neoforged.net)")
	cmd.Flags().StringVar(&javaArgs, "java-args", "", "JVM arguments for the launcher profile (default: launcher default)")
	return cmd
}
