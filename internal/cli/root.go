// Package cli wires the m365 command tree. It is intentionally thin: every
// command loads config, enforces the allowlist guardrails, and renders the
// stable JSON envelope. Business logic lives in the internal/* packages.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

// Global flags shared by every command (gog-style: stdout = data, stderr = human).
type globalFlags struct {
	configPath string
	json       bool
	plain      bool
}

var flags globalFlags

// NewRootCmd builds the root command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "m365",
		Short: "m365 — the gog-style CLI for Microsoft 365 (cloud) and on-premise Exchange",
		Long: "m365 is a single static binary for mail, calendar and files against\n" +
			"Microsoft 365 (Graph, cloud) or an on-premise Exchange server (EWS),\n" +
			"built for terminals, scripts, CI, and coding agents. Scoped by hard\n" +
			"allowlists. Made by makerslab.ai.\n\n" +
			"Independent project, not affiliated with or endorsed by Microsoft.\n" +
			"\"Microsoft\" and \"Microsoft 365\" are trademarks of the Microsoft group\n" +
			"of companies.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&flags.configPath, "config", defaultConfigPath(), "path to config.toml")
	root.PersistentFlags().BoolVar(&flags.json, "json", false, "emit the stable JSON envelope on stdout")
	root.PersistentFlags().BoolVar(&flags.plain, "plain", false, "emit plain TSV output on stdout")

	root.AddCommand(newDoctorCmd())
	root.AddCommand(newMailCmd())
	root.AddCommand(newCalendarCmd())
	root.AddCommand(newContactsCmd())
	root.AddCommand(newDriveCmd())
	root.AddCommand(newSpCmd())
	return root
}

// Execute runs the root command and maps errors to a non-zero exit code.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// defaultConfigPath resolves ~/.config/m365cli/config.toml, honouring XDG.
func defaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "config.toml"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "m365cli", "config.toml")
}

// loadConfig loads and validates the config from the resolved path.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
