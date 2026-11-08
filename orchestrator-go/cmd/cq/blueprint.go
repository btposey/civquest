package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
)

func newBlueprintCmd() *cobra.Command {
	bpCmd := &cobra.Command{
		Use:   "blueprint",
		Short: "Manage architectural blueprints, BOMs, and procedural designs",
		Long: `Inspect official, player-imported, and procedural architectural blueprints
used by the settlement engine for multi-agent quadrant construction.`,
	}

	bpCmd.AddCommand(newBlueprintListCmd())
	bpCmd.AddCommand(newBlueprintValidateCmd())
	bpCmd.AddCommand(newBlueprintGenerateCmd())

	return bpCmd
}

type blueprintEntry struct {
	Category   string `json:"category"`
	Path       string `json:"path"`
	Filename   string `json:"filename"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Style      string `json:"style"`
	Dimensions string `json:"dimensions"`
	BlockCount int    `json:"block_count"`
}

func newBlueprintListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List official, player-imported, and LLM-generated blueprints",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			categories := []struct {
				name string
				dir  string
			}{
				{"official", r.OfficialBlueprintsDir()},
				{"player_imported", r.PlayerImportedBlueprintsDir()},
				{"llm_generated", r.LLMGeneratedBlueprintsDir()},
			}

			var entries []blueprintEntry

			for _, cat := range categories {
				_ = os.MkdirAll(cat.dir, 0755)
				files, err := os.ReadDir(cat.dir)
				if err != nil {
					continue
				}

				for _, f := range files {
					if f.IsDir() {
						continue
					}
					name := f.Name()
					if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
						continue
					}

					fullPath := filepath.Join(cat.dir, name)
					bp, err := blueprints.LoadYAML(fullPath)
					if err != nil {
						entries = append(entries, blueprintEntry{
							Category: cat.name,
							Path:     fullPath,
							Filename: name,
							Name:     "(invalid YAML: " + err.Error() + ")",
						})
						continue
					}

					dimStr := fmt.Sprintf("%dx%dx%d", bp.Dimensions.Width, bp.Dimensions.Height, bp.Dimensions.Length)
					bom := bp.CalculateBOM()
					totalBlocks := 0
					for _, count := range bom {
						totalBlocks += count
					}

					entries = append(entries, blueprintEntry{
						Category:   cat.name,
						Path:       fullPath,
						Filename:   name,
						Name:       bp.Name,
						Type:       bp.Type,
						Style:      bp.Style,
						Dimensions: dimStr,
						BlockCount: totalBlocks,
					})
				}
			}

			if flagJSON {
				data, err := json.MarshalIndent(entries, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "======================================================================")
			fmt.Fprintf(out, " Available CivQuest Architectural Blueprints (%d)\n", len(entries))
			fmt.Fprintln(out, "======================================================================")
			if len(entries) == 0 {
				fmt.Fprintln(out, "No blueprints found in official, player_imported, or llm_generated directories.")
				fmt.Fprintln(out, "Run 'cq blueprint generate' to create a new blueprint.")
			} else {
				fmt.Fprintf(out, " %-16s %-24s %-12s %-10s %s\n", "Category", "Name", "Type", "Dimensions", "Blocks")
				fmt.Fprintln(out, " --------------------------------------------------------------------")
				for _, e := range entries {
					fmt.Fprintf(out, " [%-14s] %-24s %-12s %-10s %d blocks\n",
						e.Category, e.Name, e.Type, e.Dimensions, e.BlockCount)
				}
			}
			fmt.Fprintln(out, "======================================================================")

			return nil
		},
	}
}

func newBlueprintValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file.yaml>",
		Short: "Validate the schema, palette, and bounds of a blueprint YAML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			bp, err := blueprints.LoadYAML(path)
			if err != nil {
				return fmt.Errorf("blueprint validation failed: %w", err)
			}

			bom := bp.CalculateBOM()
			totalBlocks := 0
			for _, count := range bom {
				totalBlocks += count
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"valid":        true,
					"name":         bp.Name,
					"type":         bp.Type,
					"style":        bp.Style,
					"dimensions":   bp.Dimensions,
					"total_blocks": totalBlocks,
					"bom":          bom,
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "✓ Blueprint is valid!")
			fmt.Fprintf(out, "  Name:         %s\n", bp.Name)
			fmt.Fprintf(out, "  Type:         %s\n", bp.Type)
			fmt.Fprintf(out, "  Style:        %s\n", bp.Style)
			fmt.Fprintf(out, "  Dimensions:   %d (W) x %d (H) x %d (L)\n",
				bp.Dimensions.Width, bp.Dimensions.Height, bp.Dimensions.Length)
			fmt.Fprintf(out, "  Total Blocks: %d\n", totalBlocks)
			fmt.Fprintln(out, "  Bill of Materials (BOM):")
			for state, count := range bom {
				fmt.Fprintf(out, "    • %-32s : %d\n", state, count)
			}

			return nil
		},
	}
}

func newBlueprintGenerateCmd() *cobra.Command {
	var (
		bType  string
		style  string
		output string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a structured architectural blueprint template",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := getResolver()

			bp := blueprints.GenerateTemplate(bType, style)

			targetPath := output
			if targetPath == "" {
				fileName := fmt.Sprintf("%s_%s.yaml", strings.ToLower(bp.Style), strings.ToLower(bp.Type))
				targetPath = filepath.Join(r.LLMGeneratedBlueprintsDir(), fileName)
			}

			if err := bp.SaveYAML(targetPath); err != nil {
				return fmt.Errorf("saving generated blueprint: %w", err)
			}

			if flagJSON {
				data, err := json.MarshalIndent(map[string]any{
					"success":      true,
					"name":         bp.Name,
					"path":         targetPath,
					"dimensions":   bp.Dimensions,
					"total_blocks": len(bp.CalculateBOM()),
				}, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "✓ Architectural blueprint generated successfully:")
			fmt.Fprintf(out, "  Name:       %s\n", bp.Name)
			fmt.Fprintf(out, "  Dimensions: %dx%dx%d\n", bp.Dimensions.Width, bp.Dimensions.Height, bp.Dimensions.Length)
			fmt.Fprintf(out, "  File:       %s\n", targetPath)

			return nil
		},
	}

	cmd.Flags().StringVar(&bType, "type", "tavern", "Structure type ('tavern', 'granary', 'house', 'watchtower')")
	cmd.Flags().StringVar(&style, "style", "pioneer", "Architectural style ('pioneer', 'nordic', 'stone')")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Destination file path (default: $XDG_CONFIG_HOME/civquest/blueprints/llm_generated/{style}_{type}.yaml)")

	return cmd
}
