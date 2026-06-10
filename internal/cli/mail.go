package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/graph"
	"github.com/MakersLab-ai/m365cli/internal/mail"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

// newMailCmd is the mail domain root (app-only, scoped by allowed_mailboxes).
func newMailCmd() *cobra.Command {
	mail := &cobra.Command{
		Use:   "mail",
		Short: "Mailbox operations (app-only, scoped by allowed_mailboxes)",
	}
	mail.AddCommand(
		newMailListCmd(), newMailReadCmd(), newMailSearchCmd(),
		newMailSendCmd(), newMailDraftCmd(), newMailReplyCmd(),
		newMailAttachmentsCmd(), newMailGetAttachmentCmd(), newMailWatchCmd(),
	)
	return mail
}

// mailContext loads config, resolves the mailbox (--mailbox or default_mailbox),
// and builds a Graph client. The allowlist is enforced again inside the client.
func mailContext(mailbox string) (*config.Config, *graph.Client, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, "", err
	}
	mbx := mailbox
	if mbx == "" {
		mbx = cfg.DefaultMailbox
	}
	if mbx == "" {
		return nil, nil, "", fmt.Errorf("no mailbox given (use --mailbox or set default_mailbox)")
	}
	client, err := newGraphClient(cfg)
	if err != nil {
		return nil, nil, "", err
	}
	return cfg, client, mbx, nil
}

func newMailListCmd() *cobra.Command {
	var mailbox string
	var max int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List messages in a mailbox",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			suffix := fmt.Sprintf("messages?$top=%d&$select=id,subject,from,receivedDateTime,isRead", max)
			return emitGraphValue(cmd.Context(), client, mbx, suffix)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().IntVar(&max, "max", 25, "maximum number of messages to return")
	return cmd
}

func newMailReadCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "read <message-id>",
		Short: "Read a single message (with body)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			suffix := "messages/" + url.PathEscape(args[0]) +
				"?$select=id,subject,from,toRecipients,ccRecipients,receivedDateTime,isRead,body"
			body, err := client.GetForMailbox(cmd.Context(), mbx, suffix)
			if err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, json.RawMessage(body))
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	return cmd
}

func newMailSearchCmd() *cobra.Command {
	var mailbox string
	var max int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search messages in a mailbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			// Graph $search requires the value to be a quoted string.
			q := url.QueryEscape(`"` + args[0] + `"`)
			suffix := fmt.Sprintf("messages?$search=%s&$top=%d&$select=id,subject,from,receivedDateTime,isRead", q, max)
			return emitGraphValue(cmd.Context(), client, mbx, suffix)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().IntVar(&max, "max", 25, "maximum number of results")
	return cmd
}

func newMailSendCmd() *cobra.Command {
	var mailbox, subject, bodyFile, cc string
	var to []string
	cmd := &cobra.Command{
		Use:   "send --to <addr> --subject <s> --body-file <f>",
		Short: "Send a message — falls back to a draft if any recipient is outside send_allow",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			msg, err := composeMessage(subject, bodyFile, to, cc)
			if err != nil {
				return err
			}

			recipients := append(append([]string{}, msg.To...), msg.Cc...)
			plan := mail.PlanSend(cfg, recipients)
			if plan.Action == mail.DraftOnly {
				fmt.Fprintf(os.Stderr, "send guardrail: %v not in send_allow — saving as draft for review\n", plan.Blocked)
				return createDraft(cmd.Context(), client, mbx, msg, plan.Blocked)
			}

			payload, err := mail.BuildSendMail(msg)
			if err != nil {
				return err
			}
			if _, err := client.PostForMailbox(cmd.Context(), mbx, "sendMail", payload); err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, map[string]any{"sent": true, "mailbox": mbx, "to": msg.To})
		},
	}
	addComposeFlags(cmd, &mailbox, &subject, &bodyFile, &cc, &to)
	return cmd
}

func newMailDraftCmd() *cobra.Command {
	var mailbox, subject, bodyFile, cc string
	var to []string
	cmd := &cobra.Command{
		Use:   "draft --to <addr> --subject <s> --body-file <f>",
		Short: "Create a draft message (never sends)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			msg, err := composeMessage(subject, bodyFile, to, cc)
			if err != nil {
				return err
			}
			return createDraft(cmd.Context(), client, mbx, msg, nil)
		},
	}
	addComposeFlags(cmd, &mailbox, &subject, &bodyFile, &cc, &to)
	return cmd
}

// --- shared compose helpers ---

func addComposeFlags(cmd *cobra.Command, mailbox, subject, bodyFile, cc *string, to *[]string) {
	cmd.Flags().StringVar(mailbox, "mailbox", "", "mailbox to send from (defaults to default_mailbox)")
	cmd.Flags().StringSliceVar(to, "to", nil, "recipient address (repeatable)")
	cmd.Flags().StringVar(cc, "cc", "", "cc address (comma-separated)")
	cmd.Flags().StringVar(subject, "subject", "", "message subject")
	cmd.Flags().StringVar(bodyFile, "body-file", "", "path to a file containing the message body (avoids shell escaping)")
}

func composeMessage(subject, bodyFile string, to []string, cc string) (mail.Message, error) {
	if len(to) == 0 {
		return mail.Message{}, fmt.Errorf("at least one --to recipient is required")
	}
	if bodyFile == "" {
		return mail.Message{}, fmt.Errorf("--body-file is required (use a file to avoid shell escaping)")
	}
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		return mail.Message{}, fmt.Errorf("read --body-file: %w", err)
	}
	var ccList []string
	if cc != "" {
		ccList = splitComma(cc)
	}
	return mail.Message{Subject: subject, Body: string(body), To: to, Cc: ccList}, nil
}

func createDraft(ctx context.Context, client *graph.Client, mbx string, msg mail.Message, blocked []string) error {
	payload, err := mail.BuildMessage(msg)
	if err != nil {
		return err
	}
	body, err := client.PostForMailbox(ctx, mbx, "messages", payload)
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	return output.WriteJSON(os.Stdout, map[string]any{
		"sent":        false,
		"draft":       true,
		"draft_id":    created.ID,
		"mailbox":     mbx,
		"blocked":     blocked,
		"draftReason": draftReason(blocked),
	})
}

func draftReason(blocked []string) string {
	if len(blocked) == 0 {
		return "draft requested"
	}
	return "recipients outside send_allow"
}

func emitGraphValue(ctx context.Context, client *graph.Client, mbx, suffix string) error {
	body, err := client.GetForMailbox(ctx, mbx, suffix)
	if err != nil {
		return err
	}
	var page struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return fmt.Errorf("parse Graph response: %w", err)
	}
	return output.WriteJSON(os.Stdout, json.RawMessage(page.Value))
}

func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
