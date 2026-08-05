package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/mail"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

func newMailReplyCmd() *cobra.Command {
	var mailbox, bodyFile string
	var replyAll, asHTML, asDraft bool
	var inlineImages []string
	cmd := &cobra.Command{
		Use:   "reply <message-id> --body-file <f>",
		Short: "Reply to a message — with --draft it only prepares a reply-draft and never sends",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			text, err := readBodyFile(bodyFile)
			if err != nil {
				return err
			}
			images, err := loadInlineImages(inlineImages)
			if err != nil {
				return err
			}
			noteAutoHTML(text, asHTML)
			body := mail.Body{Content: text, HTML: asHTML}
			id := args[0]

			// "Reply All, then save as a draft instead of sending" is a request
			// of its own — not an accident of the guardrail. Asking for it must
			// not depend on who the recipients happen to be.
			if asDraft {
				return createReplyDraft(cmd.Context(), client, mbx, id, body, replyAll, nil, images)
			}

			// Determine who the reply would reach, then apply the send guardrail.
			recipients, err := client.Mail().ReplyContext(cmd.Context(), mbx, id, replyAll)
			if err != nil {
				return err
			}

			if plan := mail.PlanSend(cfg, recipients); plan.Action == mail.DraftOnly {
				fmt.Fprintf(os.Stderr, "send guardrail: %v not in send_allow — saving as reply-draft for review\n", plan.Blocked)
				return createReplyDraft(cmd.Context(), client, mbx, id, body, replyAll, plan.Blocked, images)
			}

			if len(images) > 0 {
				return fmt.Errorf("--inline-image needs a draft to attach to: add --draft (a sent reply cannot be amended)")
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
	cmd.Flags().BoolVar(&asHTML, "html", false, "the body file already contains HTML — use it verbatim instead of converting the plain text")
	cmd.Flags().BoolVar(&asDraft, "draft", false, "never send: prepare the reply as a draft (keeps the quoted thread and its inline images)")
	cmd.Flags().StringArrayVar(&inlineImages, "inline-image", nil, "attach an inline image as cid=path, referenced from the body as <img src=\"cid:…\"> (repeatable)")
	return cmd
}

// createReplyDraft creates a draft reply (createReply/createReplyAll) with its
// body set, leaving it unsent for human review. blocked is empty when the draft
// was asked for rather than forced by the guardrail.
func createReplyDraft(ctx context.Context, client backend.Backend, mbx, id string, body mail.Body, replyAll bool, blocked []string, images []mail.InlineImage) error {
	draftID, err := client.Mail().CreateReplyDraft(ctx, mbx, id, body, replyAll)
	if err != nil {
		return err
	}
	if err := attachInlineImages(ctx, client, mbx, draftID, images); err != nil {
		return err
	}
	return output.WriteJSON(os.Stdout, map[string]any{
		"sent": false, "draft": true, "draft_id": draftID, "mailbox": mbx,
		"replyAll": replyAll, "blocked": blocked, "draftReason": draftReason(blocked),
		"inlineImages": len(images),
	})
}

// loadInlineImages reads --inline-image cid=path specs from disk.
func loadInlineImages(specs []string) ([]mail.InlineImage, error) {
	var out []mail.InlineImage
	for _, spec := range specs {
		cid, path, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, fmt.Errorf("--inline-image %q: expected cid=path", spec)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --inline-image %q: %w", path, err)
		}
		img, err := mail.NewInlineImage(strings.TrimSpace(cid), path, data)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, nil
}

// attachInlineImages adds the images to a draft that already exists. A failure
// here is reported, not swallowed: a body referencing a cid that never arrived
// shows the reader a broken image.
func attachInlineImages(ctx context.Context, client backend.Backend, mbx, draftID string, images []mail.InlineImage) error {
	for _, img := range images {
		if err := client.Mail().AddInlineImage(ctx, mbx, draftID, img); err != nil {
			return fmt.Errorf("attach inline image %q to draft %s: %w", img.ContentID, draftID, err)
		}
	}
	return nil
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
