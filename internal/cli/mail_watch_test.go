package cli

import (
	"strings"
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

func TestEngineCatchUpMaxMapsZeroToOff(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, -1}, {10, 10}, {3, 3}} {
		got, err := engineCatchUpMax(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("engineCatchUpMax(%d) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	if _, err := engineCatchUpMax(-1); err == nil {
		t.Error("a negative --catchup-max must be rejected")
	}
}

func TestWatchPollRejectsBadFlagsBeforeDoingAnything(t *testing.T) {
	for _, args := range [][]string{
		{"--max-posts-per-poll", "-1"},
		{"--max-posts-per-poll", "0"},
		{"--catchup-max", "-2"},
		{"--catchup-window", "-1h"},
		{"--catchup-max-age", "-1h"},
		{"--pending-max-age", "-1h"},
		{"--poll-deadline", "-1s"},
		{"--max-pages-per-poll", "-1"},
	} {
		root := NewRootCmd()
		root.SetArgs(append([]string{"--config", "/nonexistent/config.toml", "mail", "watch", "poll", "--hook-url", "http://127.0.0.1:1/h"}, args...))
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(args[0], "--")) {
			t.Errorf("%v: want a flag error, got %v", args, err)
		}
	}
}
