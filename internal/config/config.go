// Package config loads and validates the m365cli configuration file and exposes
// the allowlist guardrails (mailboxes, sites, send targets) enforced before any
// Graph call. Failing closed is intentional: an empty allowlist denies all.
package config

import (
	"fmt"
	"os"
	"path"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Config mirrors ~/.config/m365cli/config.toml. Secrets (the cert) live on disk
// at CertPath; they are never passed on the command line.
//
// All three allowlists (allowed_mailboxes, allowed_sites, send_allow) use the
// same matcher: exact, case-insensitive, plus shell-style globs so operators can
// write domain patterns like "*@contoso.com" or "contoso.sharepoint.com,*".
// Empty lists fail closed (deny all).
type Config struct {
	// Backend selects the transport: "graph" (default, Microsoft 365 cloud) or
	// "ews" (on-premise Exchange — accepted but not yet implemented). Empty is
	// treated as "graph".
	Backend          string   `toml:"backend"`
	TenantID         string   `toml:"tenant_id"`
	ClientID         string   `toml:"client_id"`
	CertPath         string   `toml:"cert_path"`
	DefaultMailbox   string   `toml:"default_mailbox"`
	AllowedMailboxes []string `toml:"allowed_mailboxes"`
	AllowedSites     []string `toml:"allowed_sites"`
	SendAllow        []string `toml:"send_allow"`
	// SendUnrestricted disables the external-send guardrail: when true, direct
	// send to any recipient is allowed regardless of send_allow. This turns off
	// the draft-review safety net — doctor flags it as a warning.
	SendUnrestricted bool `toml:"send_unrestricted"`
}

// Load reads and parses the TOML config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks that the core auth fields are present and that any configured
// default_mailbox is itself within the mailbox allowlist.
func (c *Config) Validate() error {
	switch c.Backend {
	case "", "graph", "ews":
		// known
	default:
		return fmt.Errorf("config: unknown backend %q (want \"graph\" or \"ews\")", c.Backend)
	}
	if strings.TrimSpace(c.TenantID) == "" {
		return fmt.Errorf("config: tenant_id is required")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("config: client_id is required")
	}
	if strings.TrimSpace(c.CertPath) == "" {
		return fmt.Errorf("config: cert_path is required")
	}
	if c.DefaultMailbox != "" && len(c.AllowedMailboxes) > 0 && !c.IsMailboxAllowed(c.DefaultMailbox) {
		return fmt.Errorf("config: default_mailbox %q is not in allowed_mailboxes", c.DefaultMailbox)
	}
	return nil
}

// IsMailboxAllowed reports whether addr matches allowed_mailboxes (exact or
// glob, case-insensitive). An empty allowlist fails closed.
func (c *Config) IsMailboxAllowed(addr string) bool {
	return matchAny(c.AllowedMailboxes, addr)
}

// IsSiteAllowed reports whether site matches allowed_sites (exact or glob,
// case-insensitive). An empty allowlist fails closed.
func (c *Config) IsSiteAllowed(site string) bool {
	return matchAny(c.AllowedSites, site)
}

// CanSendTo reports whether direct send to addr is permitted. SendUnrestricted
// allows any (non-empty) recipient; otherwise addr must match send_allow.
// An empty send_allow fails closed (external sends must go out as drafts).
func (c *Config) CanSendTo(addr string) bool {
	if c.SendUnrestricted {
		return normalize(addr) != ""
	}
	return matchAny(c.SendAllow, addr)
}

// matchAny reports whether value matches any pattern in list. Each pattern is
// compared exactly, then as a shell-style glob (path.Match) when it contains a
// "*". Matching is case-insensitive and whitespace-trimmed.
func matchAny(list []string, value string) bool {
	want := normalize(value)
	if want == "" {
		return false
	}
	for _, item := range list {
		pat := normalize(item)
		if pat == "" {
			continue
		}
		if pat == want {
			return true
		}
		if strings.Contains(pat, "*") {
			if ok, err := path.Match(pat, want); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
