package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleTOML = `
tenant_id = "11111111-1111-1111-1111-111111111111"
client_id = "22222222-2222-2222-2222-222222222222"
cert_path = "/etc/m365/cert.pem"
default_mailbox = "agent@example.com"
allowed_mailboxes = ["agent@example.com", "Shared@Example.com"]
allowed_sites = ["contoso.sharepoint.com,site-guid,web-guid"]
send_allow = ["partner@external.com"]
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

func TestLoadParsesAllFields(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleTOML))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.TenantID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("TenantID = %q", cfg.TenantID)
	}
	if cfg.ClientID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("ClientID = %q", cfg.ClientID)
	}
	if cfg.CertPath != "/etc/m365/cert.pem" {
		t.Errorf("CertPath = %q", cfg.CertPath)
	}
	if cfg.DefaultMailbox != "agent@example.com" {
		t.Errorf("DefaultMailbox = %q", cfg.DefaultMailbox)
	}
	if len(cfg.AllowedMailboxes) != 2 {
		t.Errorf("AllowedMailboxes = %v", cfg.AllowedMailboxes)
	}
	if len(cfg.SendAllow) != 1 || cfg.SendAllow[0] != "partner@external.com" {
		t.Errorf("SendAllow = %v", cfg.SendAllow)
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	_, err := Load("/no/such/path/config.toml")
	if err == nil {
		t.Fatal("Load: expected error for missing file, got nil")
	}
}

func TestValidateRequiresCoreFields(t *testing.T) {
	cases := map[string]Config{
		"missing tenant_id": {ClientID: "c", CertPath: "p"},
		"missing client_id": {TenantID: "t", CertPath: "p"},
		"missing cert_path": {TenantID: "t", ClientID: "c"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate(%s): expected error, got nil", name)
			}
		})
	}
}

func TestValidateAcceptsCompleteConfig(t *testing.T) {
	cfg := Config{TenantID: "t", ClientID: "c", CertPath: "p"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: unexpected error: %v", err)
	}
}

func TestValidateBackendField(t *testing.T) {
	// graph (and the empty default) require the cloud auth triple.
	for _, b := range []string{"", "graph"} {
		cfg := Config{Backend: b, TenantID: "t", ClientID: "c", CertPath: "p"}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate(backend=%q): unexpected error: %v", b, err)
		}
	}
	// ews requires the EWS triple, NOT the cloud one.
	ews := Config{Backend: "ews", EWSURL: "https://m/EWS/Exchange.asmx", EWSUser: "D\\u", EWSPasswordFile: "/p"}
	if err := ews.Validate(); err != nil {
		t.Errorf("Validate(ews complete): unexpected error: %v", err)
	}
	for name, cfg := range map[string]Config{
		"ews missing url":      {Backend: "ews", EWSUser: "D\\u", EWSPasswordFile: "/p"},
		"ews missing user":     {Backend: "ews", EWSURL: "https://m", EWSPasswordFile: "/p"},
		"ews missing pwd file": {Backend: "ews", EWSURL: "https://m", EWSUser: "D\\u"},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%s): expected error, got nil", name)
		}
	}
	unknown := Config{Backend: "imap", TenantID: "t", ClientID: "c", CertPath: "p"}
	if err := unknown.Validate(); err == nil {
		t.Error("Validate: expected error for unknown backend")
	}
}

func TestValidateRejectsDefaultMailboxOutsideAllowlist(t *testing.T) {
	cfg := Config{
		TenantID: "t", ClientID: "c", CertPath: "p",
		DefaultMailbox:   "ghost@example.com",
		AllowedMailboxes: []string{"agent@example.com"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate: expected error when default_mailbox not in allowed_mailboxes")
	}
}

func TestIsMailboxAllowedCaseInsensitive(t *testing.T) {
	cfg := Config{AllowedMailboxes: []string{"Agent@Example.com", "shared@example.com"}}
	allowed := []string{"agent@example.com", "AGENT@EXAMPLE.COM", "  shared@example.com  "}
	for _, addr := range allowed {
		if !cfg.IsMailboxAllowed(addr) {
			t.Errorf("IsMailboxAllowed(%q) = false, want true", addr)
		}
	}
}

func TestIsMailboxAllowedRejectsUnlisted(t *testing.T) {
	cfg := Config{AllowedMailboxes: []string{"agent@example.com"}}
	if cfg.IsMailboxAllowed("intruder@example.com") {
		t.Error("IsMailboxAllowed: unlisted mailbox must be rejected")
	}
}

func TestIsMailboxAllowedFailsClosedOnEmptyList(t *testing.T) {
	cfg := Config{} // no allowed_mailboxes configured
	if cfg.IsMailboxAllowed("anyone@example.com") {
		t.Error("IsMailboxAllowed: empty allowlist must fail closed (deny all)")
	}
}

func TestCanSendToHonoursSendAllow(t *testing.T) {
	cfg := Config{SendAllow: []string{"Partner@External.com"}}
	if !cfg.CanSendTo("partner@external.com") {
		t.Error("CanSendTo: listed address must be allowed (case-insensitive)")
	}
	if cfg.CanSendTo("stranger@external.com") {
		t.Error("CanSendTo: unlisted address must be denied")
	}
}

func TestCanSendToFailsClosedOnEmptyList(t *testing.T) {
	cfg := Config{}
	if cfg.CanSendTo("anyone@external.com") {
		t.Error("CanSendTo: empty send_allow must fail closed (deny all direct send)")
	}
}

func TestDomainGlobMatchesAddressesInDomain(t *testing.T) {
	cfg := Config{AllowedMailboxes: []string{"*@example.com"}}
	if !cfg.IsMailboxAllowed("anyone@example.com") {
		t.Error("*@example.com should match anyone@example.com")
	}
	if !cfg.IsMailboxAllowed("Other.Person@Example.com") {
		t.Error("domain glob must be case-insensitive")
	}
}

func TestDomainGlobRespectsAtBoundary(t *testing.T) {
	cfg := Config{AllowedMailboxes: []string{"*@example.com"}}
	// Must NOT match a different domain that merely ends in example.com.
	if cfg.IsMailboxAllowed("attacker@evil-example.com") {
		t.Error("*@example.com must not match attacker@evil-example.com")
	}
	if cfg.IsMailboxAllowed("attacker@sub.example.com") {
		t.Error("*@example.com must not match a subdomain (no @ boundary)")
	}
}

func TestSendAllowSupportsDomainGlob(t *testing.T) {
	cfg := Config{SendAllow: []string{"*@partner.com"}}
	if !cfg.CanSendTo("contact@partner.com") {
		t.Error("send_allow domain glob should permit contact@partner.com")
	}
	if cfg.CanSendTo("contact@other.com") {
		t.Error("send_allow domain glob must not permit other domains")
	}
}

func TestSendUnrestrictedOverridesAllowlist(t *testing.T) {
	cfg := Config{SendUnrestricted: true} // empty send_allow
	if !cfg.CanSendTo("anyone@anywhere.com") {
		t.Error("send_unrestricted must permit any recipient")
	}
	if cfg.CanSendTo("") {
		t.Error("send_unrestricted must still reject an empty address")
	}
}

func TestIsSiteAllowedExactAndGlob(t *testing.T) {
	cfg := Config{AllowedSites: []string{"contoso.sharepoint.com,site-guid,web-guid", "fabrikam.sharepoint.com,*"}}
	if !cfg.IsSiteAllowed("contoso.sharepoint.com,site-guid,web-guid") {
		t.Error("exact site id should be allowed")
	}
	if !cfg.IsSiteAllowed("fabrikam.sharepoint.com,abc,def") {
		t.Error("site glob should allow any site under the host")
	}
	if cfg.IsSiteAllowed("evil.sharepoint.com,abc,def") {
		t.Error("unlisted site host must be rejected")
	}
}

func TestIsSiteAllowedFailsClosedOnEmpty(t *testing.T) {
	cfg := Config{}
	if cfg.IsSiteAllowed("any.sharepoint.com,a,b") {
		t.Error("empty allowed_sites must fail closed")
	}
}

func TestWatchScopeDefaultsToAllConcreteAllowedMailboxes(t *testing.T) {
	cfg := &Config{AllowedMailboxes: []string{"a@x.com", "*@y.com", "b@x.com"}}
	got, globs, err := cfg.WatchScope()
	if err != nil {
		t.Fatalf("WatchScope: %v", err)
	}
	if len(got) != 2 || got[0] != "a@x.com" || got[1] != "b@x.com" {
		t.Errorf("default scope = %v, want [a@x.com b@x.com]", got)
	}
	if len(globs) != 1 || globs[0] != "*@y.com" {
		t.Errorf("skipped globs = %v", globs)
	}
}

func TestWatchScopeHonoursWatchMailboxesSubset(t *testing.T) {
	cfg := &Config{AllowedMailboxes: []string{"a@x.com", "b@x.com"}, WatchMailboxes: []string{"B@x.com"}}
	got, _, err := cfg.WatchScope()
	if err != nil {
		t.Fatalf("WatchScope: %v", err)
	}
	if len(got) != 1 || got[0] != "b@x.com" { // allowed_mailboxes spelling
		t.Errorf("scope = %v, want only the watch_mailboxes entry", got)
	}
}

func TestWatchScopeRejectsEntriesOutsideAllowlistOrGlobs(t *testing.T) {
	for _, wm := range [][]string{{"c@x.com"}, {"*@x.com"}} {
		cfg := &Config{AllowedMailboxes: []string{"a@x.com", "*@x.com"}, WatchMailboxes: wm}
		if wm[0] == "c@x.com" {
			cfg.AllowedMailboxes = []string{"a@x.com"}
		}
		if _, _, err := cfg.WatchScope(); err == nil {
			t.Errorf("watch_mailboxes %v must be rejected", wm)
		}
	}
}

func TestLoadParsesWatchMailboxes(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleTOML+`watch_mailboxes = ["agent@example.com"]`+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.WatchMailboxes) != 1 || cfg.WatchMailboxes[0] != "agent@example.com" {
		t.Errorf("WatchMailboxes = %v", cfg.WatchMailboxes)
	}
	if err := (&Config{TenantID: "t", ClientID: "c", CertPath: "p", AllowedMailboxes: []string{"a@x.com"}, WatchMailboxes: []string{"zzz@x.com"}}).Validate(); err != nil {
		t.Errorf("a watch_mailboxes typo must not fail Validate (it would break every command): %v", err)
	}
}

func TestWatchScopeNormalizesCaseAndDedupes(t *testing.T) {
	cfg := &Config{
		AllowedMailboxes: []string{"Office@Contoso.com", "*@contoso.com"},
		WatchMailboxes:   []string{"office@contoso.com", "OFFICE@contoso.com", "Info@Contoso.com"},
	}
	got, _, err := cfg.WatchScope()
	if err != nil {
		t.Fatalf("WatchScope: %v", err)
	}
	// The concrete allowed spelling wins (same state file name as v0.2.x);
	// glob-matched entries are lower-cased; duplicates collapse.
	if len(got) != 2 || got[0] != "Office@Contoso.com" || got[1] != "info@contoso.com" {
		t.Errorf("scope = %v, want [Office@Contoso.com info@contoso.com]", got)
	}
	cfg.WatchMailboxes = nil
	cfg.AllowedMailboxes = []string{"a@x.com", "A@X.com"}
	if got, _, _ := cfg.WatchScope(); len(got) != 1 {
		t.Errorf("default scope must dedupe case-insensitively, got %v", got)
	}
}
