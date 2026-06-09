// Package doctor performs offline sanity checks on a loaded config: required
// fields, certificate presence, and that the fail-closed allowlists are actually
// populated. Live token/scope checks against Graph are layered on top by the
// command once app-only auth is wired in.
package doctor

import "github.com/MakersLab-ai/m365cli/internal/config"

// Severity levels for a diagnostic check.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// Check is a single named diagnostic result. OK drives the overall pass/fail
// (and exit code); Level adds warn semantics for noteworthy-but-non-fatal state.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// Inspect runs the offline diagnostics. certExists reports whether the file at
// cfg.CertPath is present and readable (resolved by the caller, which owns the
// filesystem).
func Inspect(cfg *config.Config, certExists bool) []Check {
	var checks []Check

	if err := cfg.Validate(); err != nil {
		checks = append(checks, fail("config", err.Error()))
	} else {
		checks = append(checks, ok("config", "required fields present"))
	}

	if certExists {
		checks = append(checks, ok("cert", "certificate file found at "+cfg.CertPath))
	} else {
		checks = append(checks, fail("cert", "certificate file missing at "+cfg.CertPath))
	}

	if len(cfg.AllowedMailboxes) > 0 {
		checks = append(checks, ok("allowed_mailboxes", "mailbox allowlist configured"))
	} else {
		checks = append(checks, fail("allowed_mailboxes", "no mailboxes allowed — fail-closed denies all mailbox access"))
	}

	if cfg.SendUnrestricted {
		checks = append(checks, warn("send_guardrail", "send_unrestricted = true — external-send guardrail is OFF (no draft review)"))
	}

	return checks
}

func ok(name, detail string) Check   { return Check{name, true, LevelOK, detail} }
func warn(name, detail string) Check { return Check{name, true, LevelWarn, detail} }
func fail(name, detail string) Check { return Check{name, false, LevelFail, detail} }

// AllOK reports whether every check passed.
func AllOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}
