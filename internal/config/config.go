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
	// "ews" (on-premise Exchange). Empty is treated as "graph".
	Backend  string `toml:"backend"`
	TenantID string `toml:"tenant_id"`
	ClientID string `toml:"client_id"`
	CertPath string `toml:"cert_path"`
	// EWS (on-premise Exchange) transport — used only when Backend == "ews".
	// EWSUser is the NTLM service account ("DOMAIN\\user" or UPN); the password
	// is read from EWSPasswordFile (a 0600 file), never from config or argv.
	EWSURL           string   `toml:"ews_url"`
	EWSUser          string   `toml:"ews_user"`
	EWSPasswordFile  string   `toml:"ews_password_file"`
	DefaultMailbox   string   `toml:"default_mailbox"`
	AllowedMailboxes []string `toml:"allowed_mailboxes"`
	AllowedSites     []string `toml:"allowed_sites"`
	SendAllow        []string `toml:"send_allow"`
	// SendUnrestricted disables the external-send guardrail: when true, direct
	// send to any recipient is allowed regardless of send_allow. This turns off
	// the draft-review safety net — doctor flags it as a warning.
	SendUnrestricted bool `toml:"send_unrestricted"`
	// WatchMailboxes narrows `mail watch poll --all` to a subset of
	// allowed_mailboxes (e.g. read access to two mailboxes, but only one
	// watched). Empty = every concrete allowed_mailboxes entry (the default).
	// Each entry must be a concrete address that allowed_mailboxes permits.
	WatchMailboxes []string `toml:"watch_mailboxes"`
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
	case "", "graph":
		if strings.TrimSpace(c.TenantID) == "" {
			return fmt.Errorf("config: tenant_id is required")
		}
		if strings.TrimSpace(c.ClientID) == "" {
			return fmt.Errorf("config: client_id is required")
		}
		if strings.TrimSpace(c.CertPath) == "" {
			return fmt.Errorf("config: cert_path is required")
		}
	case "ews":
		if strings.TrimSpace(c.EWSURL) == "" {
			return fmt.Errorf("config: ews_url is required for backend \"ews\"")
		}
		if strings.TrimSpace(c.EWSUser) == "" {
			return fmt.Errorf("config: ews_user is required for backend \"ews\"")
		}
		if strings.TrimSpace(c.EWSPasswordFile) == "" {
			return fmt.Errorf("config: ews_password_file is required for backend \"ews\"")
		}
	default:
		return fmt.Errorf("config: unknown backend %q (want \"graph\" or \"ews\")", c.Backend)
	}
	if c.DefaultMailbox != "" && len(c.AllowedMailboxes) > 0 && !c.IsMailboxAllowed(c.DefaultMailbox) {
		return fmt.Errorf("config: default_mailbox %q is not in allowed_mailboxes", c.DefaultMailbox)
	}
	return nil
}

// WatchScope returns the mailboxes `mail watch poll --all` watches: the
// watch_mailboxes subset when set, otherwise every concrete (non-glob)
// allowed_mailboxes entry; skipped globs are returned for the caller to report.
// It is checked here rather than in Validate so a watch_mailboxes typo stops
// only the watcher, not every mail/calendar command.
func (c *Config) WatchScope() (mailboxes, skippedGlobs []string, err error) {
	seen := map[string]bool{}
	add := func(m string) {
		if k := normalize(m); !seen[k] {
			seen[k] = true
			mailboxes = append(mailboxes, m)
		}
	}
	if len(c.WatchMailboxes) > 0 {
		for _, m := range c.WatchMailboxes {
			if strings.Contains(m, "*") {
				return nil, nil, fmt.Errorf("config: watch_mailboxes entry %q must be a concrete address, not a glob", m)
			}
			if !c.IsMailboxAllowed(m) {
				return nil, nil, fmt.Errorf("config: watch_mailboxes entry %q is not in allowed_mailboxes", m)
			}
			add(c.canonicalMailbox(m))
		}
		return mailboxes, nil, nil
	}
	for _, m := range c.AllowedMailboxes {
		if strings.Contains(m, "*") {
			skippedGlobs = append(skippedGlobs, m)
			continue
		}
		add(strings.TrimSpace(m))
	}
	if len(mailboxes) == 0 {
		return nil, skippedGlobs, fmt.Errorf("no concrete mailboxes in allowed_mailboxes")
	}
	return mailboxes, skippedGlobs, nil
}

// canonicalMailbox returns the allowed_mailboxes spelling of m when a concrete
// entry matches it case-insensitively, so the watch state file name is the one
// `--all` (and v0.2.x) always used; otherwise (matched via a glob) the
// lower-cased address.
func (c *Config) canonicalMailbox(m string) string {
	for _, a := range c.AllowedMailboxes {
		if !strings.Contains(a, "*") && normalize(a) == normalize(m) {
			return strings.TrimSpace(a)
		}
	}
	return normalize(m)
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
