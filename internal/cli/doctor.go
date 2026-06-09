package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/auth"
	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/doctor"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

func newDoctorCmd() *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check config, certificate, and allowlist guardrails",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			checks := doctor.Inspect(cfg, fileExists(cfg.CertPath))
			if live {
				checks = append(checks, liveTokenCheck(cfg))
			}
			ok := doctor.AllOK(checks)

			if flags.json {
				if err := output.WriteJSON(os.Stdout, map[string]any{
					"healthy": ok,
					"checks":  checks,
				}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					fmt.Fprintf(os.Stdout, "[%-4s] %-18s %s\n", strings.ToUpper(c.Level), c.Name, c.Detail)
				}
			}

			if !ok {
				return fmt.Errorf("doctor: one or more checks failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "also acquire a real Graph token to verify cert + consent")
	return cmd
}

// liveTokenCheck attempts an app-only token acquisition against Graph, surfacing
// cert/consent/tenant problems. Network-touching, so it is opt-in via --live.
func liveTokenCheck(cfg *config.Config) doctor.Check {
	authn, err := auth.NewGraphAuthenticator(cfg.TenantID, cfg.ClientID, cfg.CertPath, tokenCachePath())
	if err != nil {
		return doctor.Check{Name: "graph_token", OK: false, Level: doctor.LevelFail, Detail: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := authn.Token(ctx); err != nil {
		return doctor.Check{Name: "graph_token", OK: false, Level: doctor.LevelFail, Detail: err.Error()}
	}
	return doctor.Check{Name: "graph_token", OK: true, Level: doctor.LevelOK, Detail: "acquired app-only Graph token"}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
