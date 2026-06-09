package doctor

import (
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

func find(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestInspectFlagsMissingCert(t *testing.T) {
	cfg := &config.Config{
		TenantID: "t", ClientID: "c", CertPath: "/etc/m365/cert.pem",
		DefaultMailbox:   "agent@example.com",
		AllowedMailboxes: []string{"agent@example.com"},
	}
	checks := Inspect(cfg, false /* certExists */)
	c, ok := find(checks, "cert")
	if !ok {
		t.Fatal("expected a 'cert' check")
	}
	if c.OK {
		t.Error("cert check must fail when the certificate file is absent")
	}
}

func TestInspectFlagsEmptyMailboxAllowlist(t *testing.T) {
	cfg := &config.Config{TenantID: "t", ClientID: "c", CertPath: "p"}
	checks := Inspect(cfg, true)
	c, ok := find(checks, "allowed_mailboxes")
	if !ok {
		t.Fatal("expected an 'allowed_mailboxes' check")
	}
	if c.OK {
		t.Error("allowlist check must fail (fail-closed) when no mailboxes are configured")
	}
}

func TestInspectAllGreenForCompleteConfig(t *testing.T) {
	cfg := &config.Config{
		TenantID: "t", ClientID: "c", CertPath: "p",
		DefaultMailbox:   "agent@example.com",
		AllowedMailboxes: []string{"agent@example.com"},
		SendAllow:        []string{"partner@external.com"},
	}
	for _, c := range Inspect(cfg, true) {
		if !c.OK {
			t.Errorf("check %q unexpectedly failed: %s", c.Name, c.Detail)
		}
	}
}

func TestAllOKReflectsCheckResults(t *testing.T) {
	cfg := &config.Config{TenantID: "t", ClientID: "c", CertPath: "p"}
	checks := Inspect(cfg, false)
	if AllOK(checks) {
		t.Error("AllOK must be false when any check fails")
	}
}

func TestInspectWarnsWhenSendUnrestricted(t *testing.T) {
	cfg := &config.Config{
		TenantID: "t", ClientID: "c", CertPath: "p",
		DefaultMailbox:   "agent@example.com",
		AllowedMailboxes: []string{"agent@example.com"},
		SendUnrestricted: true,
	}
	checks := Inspect(cfg, true)
	c, ok := find(checks, "send_guardrail")
	if !ok {
		t.Fatal("expected a 'send_guardrail' check")
	}
	if c.Level != LevelWarn {
		t.Errorf("send_guardrail level = %q, want warn", c.Level)
	}
	// A warning must not flip the overall health to failed.
	if !AllOK(checks) {
		t.Error("a warning must not cause AllOK to be false")
	}
}

func TestInspectFailingCheckHasFailLevel(t *testing.T) {
	cfg := &config.Config{TenantID: "t", ClientID: "c", CertPath: "p"}
	checks := Inspect(cfg, false)
	c, _ := find(checks, "cert")
	if c.Level != LevelFail {
		t.Errorf("failed cert check level = %q, want fail", c.Level)
	}
}
