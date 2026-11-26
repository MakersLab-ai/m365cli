package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/watch"
)

func newMailWatchCmd() *cobra.Command {
	w := &cobra.Command{
		Use:   "watch",
		Short: "Watch a mailbox for changes and forward them to a webhook",
	}
	w.AddCommand(newMailWatchPollCmd())
	return w
}

func newMailWatchPollCmd() *cobra.Command {
	var mailboxes []string
	var all bool
	var folder, hookURL, hookToken, interval string
	var includeBody, includeChanged, includeDeleted bool
	var maxBytes int
	var eo watch.EngineOptions
	var catchUpMax int

	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Delta-poll mailbox(es) on an interval, forwarding changes to --hook-url",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if eo.CatchUpMax, err = engineCatchUpMax(catchUpMax); err != nil {
				return err
			}
			if eo.MaxPostsPoll < 1 {
				return fmt.Errorf("--max-posts-per-poll must be >= 1, got %d", eo.MaxPostsPoll)
			}
			for name, d := range map[string]time.Duration{
				"--catchup-window": eo.CatchUpWindow, "--catchup-max-age": eo.CatchUpMaxAge,
				"--pending-max-age": eo.PendingMaxAge, "--poll-deadline": eo.PollDeadline,
			} {
				if d < 0 {
					return fmt.Errorf("%s must not be negative, got %s", name, d)
				}
			}
			if eo.MaxPagesPoll < 0 {
				return fmt.Errorf("--max-pages-per-poll must not be negative, got %d", eo.MaxPagesPoll)
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if hookURL == "" {
				return fmt.Errorf("--hook-url is required")
			}
			if all && len(mailboxes) > 0 {
				return fmt.Errorf("--all and --mailbox are mutually exclusive")
			}
			boxes, err := resolveWatchMailboxes(cfg, mailboxes, all)
			if err != nil {
				return err
			}
			d, err := parseInterval(interval)
			if err != nil {
				return err
			}

			log := watch.NewLogger(os.Stderr)
			hook := watch.NewHookClient(hookURL, hookToken)
			stateDir := filepath.Join(filepath.Dir(flags.configPath), "watch")
			poll, engine, err := newWatchPoller(cfg, hook, stateDir, log, watchSettings{
				folder: folder, includeBody: includeBody, maxBytes: maxBytes,
				includeChanged: includeChanged, includeDeleted: includeDeleted, engine: eo,
			})
			if err != nil {
				return err
			}

			log.Infof("polling %v (folder %q) every %s → %s [state %s]", boxes, folder, d, watch.Redact(hookURL), engine)
			for {
				for _, mbx := range boxes {
					if err := poll(cmd.Context(), mbx); err != nil {
						if cmd.Context().Err() != nil {
							return nil
						}
						log.Errorf(mbx, "%v", err)
					}
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(d):
				}
			}
		},
	}
	cmd.Flags().StringSliceVar(&mailboxes, "mailbox", nil, "mailbox to watch (repeatable; default: default_mailbox)")
	cmd.Flags().BoolVar(&all, "all", false, "watch watch_mailboxes, or every concrete entry in allowed_mailboxes (globs skipped)")
	cmd.Flags().StringVar(&folder, "folder", "inbox", "mail folder to poll (well-known name or folder id)")
	cmd.Flags().StringVar(&interval, "interval", "30s", "poll interval (e.g. 30s, 2m, or seconds as a number)")
	cmd.Flags().StringVar(&hookURL, "hook-url", "", "webhook URL to forward changes to (required)")
	cmd.Flags().StringVar(&hookToken, "hook-token", "", "bearer token for the webhook")
	cmd.Flags().BoolVar(&includeBody, "include-body", false, "include the message body in the payload")
	cmd.Flags().IntVar(&maxBytes, "max-bytes", 20000, "max body bytes (over cap: truncate + bodyTruncated=true)")
	cmd.Flags().BoolVar(&includeChanged, "include-changed", false, "also forward changed (not just new) messages (uses the v1 engine)")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "also forward deleted message ids (uses the v1 engine)")
	cmd.Flags().IntVar(&catchUpMax, "catchup-max", 10, "after a (re-)prime, deliver at most this many recent messages (0 = no catch-up)")
	cmd.Flags().DurationVar(&eo.CatchUpWindow, "catchup-window", 24*time.Hour, "after a re-prime, catch up at most this far back")
	cmd.Flags().DurationVar(&eo.CatchUpMaxAge, "catchup-max-age", 48*time.Hour, "no catch-up at all when the last good state is older than this")
	cmd.Flags().DurationVar(&eo.PendingMaxAge, "pending-max-age", 24*time.Hour, "drop an undelivered message after it waited this long")
	cmd.Flags().IntVar(&eo.MaxPostsPoll, "max-posts-per-poll", 20, "hook POSTs (one message each) per mailbox per poll; the rest waits for the next poll")
	cmd.Flags().IntVar(&eo.MaxPagesPoll, "max-pages-per-poll", 500, "delta pages per mailbox per poll (a large prime continues next poll)")
	cmd.Flags().DurationVar(&eo.PollDeadline, "poll-deadline", 3*time.Minute, "delta paging time per mailbox per poll")
	return cmd
}

// engineCatchUpMax maps the --catchup-max flag (0 = off) to the engine option
// (<0 = off, 0 = default).
func engineCatchUpMax(flag int) (int, error) {
	switch {
	case flag < 0:
		return 0, fmt.Errorf("--catchup-max must be >= 0 (0 = no catch-up), got %d", flag)
	case flag == 0:
		return -1, nil
	default:
		return flag, nil
	}
}

// watchSettings bundles the poll flags for newWatchPoller.
type watchSettings struct {
	folder                         string
	includeBody                    bool
	maxBytes                       int
	includeChanged, includeDeleted bool
	engine                         watch.EngineOptions
}

type pollFunc func(ctx context.Context, mailbox string) error

// newWatchPoller picks the watch engine. The Graph backend uses the v2 engine
// (state v2, IDs-only delta, per-page cursor, self-healing re-prime). The v1
// syncer stays for the EWS backend and for --include-changed/--include-deleted,
// which need change/delete events the IDs-only created-delta does not carry.
func newWatchPoller(cfg *config.Config, hook watch.HookPoster, stateDir string, log *watch.Logger, s watchSettings) (pollFunc, string, error) {
	legacy := cfg.Backend == "ews" || s.includeChanged || s.includeDeleted
	if legacy {
		be, err := newBackend(cfg)
		if err != nil {
			return nil, "", err
		}
		syncer := &watch.Syncer{
			Graph: be.Mail(),
			Hook:  hook,
			Store: watch.NewFileStore(stateDir),
			Opts: watch.Options{
				Folder: s.folder, IncludeBody: s.includeBody, MaxBytes: s.maxBytes,
				IncludeChanged: s.includeChanged, IncludeDeleted: s.includeDeleted,
			},
		}
		return syncer.SyncOnce, "v1", nil
	}
	client, err := newGraphClient(cfg)
	if err != nil {
		return nil, "", err
	}
	eo := s.engine
	eo.Folder, eo.IncludeBody, eo.MaxBytes = s.folder, s.includeBody, s.maxBytes
	engine := &watch.Engine{Graph: client, Hook: hook, Store: watch.NewV2Store(stateDir), Opts: eo, Log: log}
	return engine.Poll, "v2", nil
}

// resolveWatchMailboxes returns the concrete mailbox set to poll.
func resolveWatchMailboxes(cfg *config.Config, flagMailboxes []string, all bool) ([]string, error) {
	if all {
		boxes, globs, err := cfg.WatchScope()
		for _, g := range globs {
			fmt.Fprintf(os.Stderr, "watch --all: skipping glob entry %q (cannot enumerate)\n", g)
		}
		if err != nil {
			return nil, fmt.Errorf("--all: %w", err)
		}
		return boxes, nil
	}
	if len(flagMailboxes) > 0 {
		for _, m := range flagMailboxes {
			if !cfg.IsMailboxAllowed(m) {
				return nil, fmt.Errorf("mailbox %q is not in allowed_mailboxes", m)
			}
		}
		return flagMailboxes, nil
	}
	if cfg.DefaultMailbox == "" {
		return nil, fmt.Errorf("no mailbox given (use --mailbox/--all or set default_mailbox)")
	}
	return []string{cfg.DefaultMailbox}, nil
}

// parseInterval accepts a Go duration ("30s", "2m") or bare seconds ("30"). A
// zero or negative interval is rejected: time.After(0) fires immediately, which
// would busy-loop the poller and hammer Graph/the webhook with no back-pressure.
func parseInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		d, err = time.ParseDuration(s + "s")
	}
	if err != nil {
		return 0, fmt.Errorf("invalid --interval %q (use 30s, 2m, or a number of seconds)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--interval must be positive, got %q", s)
	}
	return d, nil
}
