package cli

import (
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

func TestParseIntervalAcceptsDurationsAndBareSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // String() of the expected duration
	}{
		{"30s", "30s"},
		{"2m", "2m0s"},
		{"45", "45s"}, // bare seconds
	} {
		d, err := parseInterval(tc.in)
		if err != nil {
			t.Errorf("parseInterval(%q): %v", tc.in, err)
			continue
		}
		if d.String() != tc.want {
			t.Errorf("parseInterval(%q) = %s, want %s", tc.in, d, tc.want)
		}
	}
}

func TestParseIntervalRejectsZeroAndNegative(t *testing.T) {
	for _, in := range []string{"0", "0s", "-5s"} {
		if _, err := parseInterval(in); err == nil {
			t.Errorf("parseInterval(%q) must reject non-positive interval", in)
		}
	}
}

func TestParseIntervalRejectsGarbage(t *testing.T) {
	if _, err := parseInterval("soon"); err == nil {
		t.Error("parseInterval(\"soon\") must error")
	}
}

func TestResolveWatchMailboxesAllHonoursWatchMailboxes(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"a@x.com", "b@x.com"}}
	got, err := resolveWatchMailboxes(cfg, nil, true)
	if err != nil || len(got) != 2 {
		t.Fatalf("default --all = %v, %v; want both mailboxes", got, err)
	}
	cfg.WatchMailboxes = []string{"b@x.com"}
	got, err = resolveWatchMailboxes(cfg, nil, true)
	if err != nil || len(got) != 1 || got[0] != "b@x.com" {
		t.Fatalf("--all with watch_mailboxes = %v, %v; want [b@x.com]", got, err)
	}
	cfg.WatchMailboxes = []string{"c@x.com"}
	if _, err := resolveWatchMailboxes(cfg, nil, true); err == nil {
		t.Error("watch_mailboxes outside allowed_mailboxes must fail the watcher")
	}
}
