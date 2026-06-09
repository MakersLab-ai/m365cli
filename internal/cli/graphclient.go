package cli

import (
	"path/filepath"

	"github.com/MakersLab-ai/m365cli/internal/auth"
	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

// tokenCachePath stores tokens next to the config file (same 0600 directory).
func tokenCachePath() string {
	return filepath.Join(filepath.Dir(flags.configPath), "tokens.json")
}

// newGraphClient builds a certificate-backed Graph client for cfg.
func newGraphClient(cfg *config.Config) (*graph.Client, error) {
	authn, err := auth.NewGraphAuthenticator(cfg.TenantID, cfg.ClientID, cfg.CertPath, tokenCachePath())
	if err != nil {
		return nil, err
	}
	return graph.New(cfg, authn), nil
}
