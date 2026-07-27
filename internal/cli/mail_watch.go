package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Delta-poll mailbox(es) on an interval, forwarding changes to --hook-url",
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			be, err := newBackend(cfg)
			if err != nil {
				return err
			}
			syncer := &watch.Syncer{
				Graph: be.Mail(),
				Hook:  watch.NewHookClient(hookURL, hookToken),
				Store: watch.NewFileStore(filepath.Join(filepath.Dir(flags.configPath), "watch")),
				Opts: watch.Options{
					Folder: folder, IncludeBody: includeBody, MaxBytes: maxBytes,
					IncludeChanged: includeChanged, IncludeDeleted: includeDeleted,
				},
			}

			fmt.Fprintf(os.Stderr, "m365 mail watch: polling %v (folder %q) every %s → %s\n", boxes, folder, d, hookURL)
			for {
				for _, mbx := range boxes {
					if err := syncer.SyncOnce(cmd.Context(), mbx); err != nil {
						fmt.Fprintf(os.Stderr, "watch %s: %v\n", mbx, err)
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
	cmd.Flags().BoolVar(&all, "all", false, "watch every concrete entry in allowed_mailboxes (globs skipped)")
	cmd.Flags().StringVar(&folder, "folder", "inbox", "mail folder to poll (well-known name or folder id)")
	cmd.Flags().StringVar(&interval, "interval", "30s", "poll interval (e.g. 30s, 2m, or seconds as a number)")
	cmd.Flags().StringVar(&hookURL, "hook-url", "", "webhook URL to forward changes to (required)")
	cmd.Flags().StringVar(&hookToken, "hook-token", "", "bearer token for the webhook")
	cmd.Flags().BoolVar(&includeBody, "include-body", false, "include the message body in the payload")
	cmd.Flags().IntVar(&maxBytes, "max-bytes", 20000, "max body bytes (over cap: truncate + bodyTruncated=true)")
	cmd.Flags().BoolVar(&includeChanged, "include-changed", false, "also forward changed (not just new) messages")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "also forward deleted message ids")
	return cmd
}

// resolveWatchMailboxes returns the concrete mailbox set to poll.
func resolveWatchMailboxes(cfg *config.Config, flagMailboxes []string, all bool) ([]string, error) {
	if all {
		var concrete []string
		for _, m := range cfg.AllowedMailboxes {
			if strings.Contains(m, "*") {
				fmt.Fprintf(os.Stderr, "watch --all: skipping glob entry %q (cannot enumerate)\n", m)
				continue
			}
			concrete = append(concrete, m)
		}
		if len(concrete) == 0 {
			return nil, fmt.Errorf("--all: no concrete mailboxes in allowed_mailboxes")
		}
		return concrete, nil
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
