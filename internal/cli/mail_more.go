package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/graph"
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
			msgJSON, err := client.GetForMailbox(cmd.Context(), mbx, "messages/"+url.PathEscape(id)+"?$select=from,toRecipients,ccRecipients")
			if err != nil {
				return err
			}
			recipients, err := mail.ReplyRecipients(msgJSON, replyAll)
			if err != nil {
				return err
			}

			if plan := mail.PlanSend(cfg, recipients); plan.Action == mail.DraftOnly {
				fmt.Fprintf(os.Stderr, "send guardrail: %v not in send_allow — saving as reply-draft for review\n", plan.Blocked)
				return createReplyDraft(cmd.Context(), client, mbx, id, body, replyAll, plan.Blocked)
			}

			payload, err := mail.BuildReplyComment(body)
			if err != nil {
				return err
			}
			action := "reply"
			if replyAll {
				action = "replyAll"
			}
			if _, err := client.PostForMailbox(cmd.Context(), mbx, "messages/"+url.PathEscape(id)+"/"+action, payload); err != nil {
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

// createReplyDraft creates a draft reply (createReply/createReplyAll) and sets
// its body via PATCH, leaving it unsent for human review.
func createReplyDraft(ctx context.Context, client *graph.Client, mbx, id, body string, replyAll bool, blocked []string) error {
	create := "createReply"
	if replyAll {
		create = "createReplyAll"
	}
	draftJSON, err := client.PostForMailbox(ctx, mbx, "messages/"+url.PathEscape(id)+"/"+create, nil)
	if err != nil {
		return err
	}
	var draft struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(draftJSON, &draft); err != nil || draft.ID == "" {
		return fmt.Errorf("create reply draft: unexpected response: %s", string(draftJSON))
	}
	patch, err := json.Marshal(map[string]any{
		"body": map[string]string{"contentType": "Text", "content": body},
	})
	if err != nil {
		return err
	}
	if _, err := client.PatchForMailbox(ctx, mbx, "messages/"+url.PathEscape(draft.ID), patch); err != nil {
		return err
	}
	return output.WriteJSON(os.Stdout, map[string]any{
		"sent": false, "draft": true, "draft_id": draft.ID, "mailbox": mbx,
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
			suffix := "messages/" + url.PathEscape(args[0]) + "/attachments?$select=id,name,contentType,size"
			return emitGraphValue(cmd.Context(), client, mbx, suffix)
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
			suffix := "messages/" + url.PathEscape(args[0]) + "/attachments/" + url.PathEscape(args[1])
			body, err := client.GetForMailbox(cmd.Context(), mbx, suffix)
			if err != nil {
				return err
			}
			if out == "" {
				return output.WriteJSON(os.Stdout, json.RawMessage(body))
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
