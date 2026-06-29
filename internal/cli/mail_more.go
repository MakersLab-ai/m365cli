package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/mail"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

func newMailReplyCmd() *cobra.Command {
	var mailbox, bodyFile string
	var replyAll bool
	cmd := &cobra.Command{
		Use:   "reply <message-id> --body-file <f>",
		Short: "Reply to a message — falls back to a reply-draft if a recipient is outside send_allow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			body, err := readBodyFile(bodyFile)
			if err != nil {
				return err
			}
			id := args[0]

			// Determine who the reply would reach, then apply the send guardrail.
			recipients, err := client.Mail().ReplyContext(cmd.Context(), mbx, id, replyAll)
			if err != nil {
				return err
			}

			if plan := mail.PlanSend(cfg, recipients); plan.Action == mail.DraftOnly {
				fmt.Fprintf(os.Stderr, "send guardrail: %v not in send_allow — saving as reply-draft for review\n", plan.Blocked)
				return createReplyDraft(cmd.Context(), client, mbx, id, body, replyAll, plan.Blocked)
			}

			if err := client.Mail().Reply(cmd.Context(), mbx, id, body, replyAll); err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, map[string]any{"sent": true, "mailbox": mbx, "replyAll": replyAll, "to": recipients})
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "path to a file containing the reply body")
	cmd.Flags().BoolVar(&replyAll, "reply-all", false, "reply to all original recipients")
	return cmd
}

// createReplyDraft creates a draft reply (createReply/createReplyAll) with its
// body set, leaving it unsent for human review.
func createReplyDraft(ctx context.Context, client backend.Backend, mbx, id, body string, replyAll bool, blocked []string) error {
	draftID, err := client.Mail().CreateReplyDraft(ctx, mbx, id, body, replyAll)
	if err != nil {
		return err
	}
	return output.WriteJSON(os.Stdout, map[string]any{
		"sent": false, "draft": true, "draft_id": draftID, "mailbox": mbx,
		"blocked": blocked, "draftReason": "recipients outside send_allow",
	})
}

func newMailAttachmentsCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "attachments <message-id>",
		Short: "List attachments on a message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Mail().Attachments(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	return cmd
}

func newMailGetAttachmentCmd() *cobra.Command {
	var mailbox, out string
	cmd := &cobra.Command{
		Use:   "get-attachment <message-id> <attachment-id>",
		Short: "Download a file attachment (decoded to --out, or raw JSON to stdout)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			body, err := client.Mail().GetAttachment(cmd.Context(), mbx, args[0], args[1])
			if err != nil {
				return err
			}
			if out == "" {
				return emitData(body)
			}
			name, content, err := mail.DecodeAttachment(body)
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, content, 0o600); err != nil {
				return fmt.Errorf("write attachment to %s: %w", out, err)
			}
			return output.WriteJSON(os.Stdout, map[string]any{"saved": out, "name": name, "bytes": len(content)})
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().StringVar(&out, "out", "", "write decoded attachment bytes to this file")
	return cmd
}

func readBodyFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--body-file is required (use a file to avoid shell escaping)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read --body-file: %w", err)
	}
	return string(b), nil
}
