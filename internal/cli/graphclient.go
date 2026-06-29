package cli

import (
	"fmt"
	"path/filepath"

	"github.com/MakersLab-ai/m365cli/internal/auth"
	"github.com/MakersLab-ai/m365cli/internal/backend"
	graphbackend "github.com/MakersLab-ai/m365cli/internal/backend/graph"
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

// newBackend selects the transport backend for cfg. "graph" (or empty) builds
// the certificate-backed Microsoft Graph backend; "ews" is reserved for the
// planned on-premise Exchange transport and is not yet implemented.
func newBackend(cfg *config.Config) (backend.Backend, error) {
	switch cfg.Backend {
	case "", "graph":
		client, err := newGraphClient(cfg)
		if err != nil {
			return nil, err
		}
		return graphbackend.New(client), nil
	case "ews":
		return nil, fmt.Errorf("backend %q is not yet implemented", cfg.Backend)
	default:
		return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
	}
}
