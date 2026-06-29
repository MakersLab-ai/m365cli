package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/auth"
	"github.com/MakersLab-ai/m365cli/internal/backend"
	ewsbackend "github.com/MakersLab-ai/m365cli/internal/backend/ews"
	graphbackend "github.com/MakersLab-ai/m365cli/internal/backend/graph"
	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/ews"
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
// the certificate-backed Microsoft Graph backend; "ews" builds the NTLM
// on-premise Exchange backend.
func newBackend(cfg *config.Config) (backend.Backend, error) {
	switch cfg.Backend {
	case "", "graph":
		client, err := newGraphClient(cfg)
		if err != nil {
			return nil, err
		}
		return graphbackend.New(client), nil
	case "ews":
		password, err := loadEWSPassword(cfg.EWSPasswordFile)
		if err != nil {
			return nil, err
		}
		return ewsbackend.New(ews.New(cfg, password)), nil
	default:
		return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
	}
}

// loadEWSPassword reads the NTLM service-account password from a 0600 file.
// Trailing newlines/whitespace are trimmed so an editor-added newline does not
// corrupt the credential. The secret never touches config or the command line.
func loadEWSPassword(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read ews_password_file %s: %w", path, err)
	}
	pw := strings.TrimRight(string(b), "\r\n \t")
	if pw == "" {
		return "", fmt.Errorf("ews_password_file %s is empty", path)
	}
	return pw, nil
}
