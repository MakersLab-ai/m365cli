package ewsbackend

import (
	"context"
	"fmt"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/ews"
	"github.com/MakersLab-ai/m365cli/internal/mail"
)

type mailSvc struct{ c *ews.Client }

func (m mailSvc) List(ctx context.Context, mailbox string, opts backend.ListOpts) ([]byte, error) {
	items, err := m.c.FindInbox(ctx, mailbox, opts.Max)
	if err != nil {
		return nil, err
	}
	return summariesJSON(items)
}

func (m mailSvc) Read(ctx context.Context, mailbox, id string) ([]byte, error) {
	it, err := m.c.GetMessage(ctx, mailbox, id)
	if err != nil {
		return nil, err
	}
	return messageJSON(it)
}

func (m mailSvc) Search(ctx context.Context, mailbox string, opts backend.SearchOpts) ([]byte, error) {
	items, err := m.c.Search(ctx, mailbox, opts.Query, opts.Max)
	if err != nil {
		return nil, err
	}
	return summariesJSON(items)
}

func (m mailSvc) Send(ctx context.Context, mailbox string, msg mail.Message) error {
	return m.c.SendMessage(ctx, mailbox, outMessage(msg))
}

func (m mailSvc) CreateDraft(ctx context.Context, mailbox string, msg mail.Message) (string, error) {
	return m.c.CreateDraft(ctx, mailbox, outMessage(msg))
}

// outMessage maps the neutral compose input onto the EWS transport's input.
func outMessage(m mail.Message) ews.OutMessage {
	return ews.OutMessage{Subject: m.Subject, Body: m.Body, To: m.To, Cc: m.Cc}
}

// ReplyContext returns the addresses a reply/replyAll would reach (sender, plus
// the original to/cc when replyAll), so the CLI can apply the send guardrail.
func (m mailSvc) ReplyContext(ctx context.Context, mailbox, id string, replyAll bool) ([]string, error) {
	it, err := m.c.GetMessage(ctx, mailbox, id)
	if err != nil {
		return nil, err
	}
	if it.From.Address == "" {
		return nil, fmt.Errorf("message has no sender to reply to")
	}
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	add(it.From.Address)
	if replyAll {
		for _, r := range it.To {
			add(r.Address)
		}
		for _, r := range it.Cc {
			add(r.Address)
		}
	}
	return out, nil
}

func (m mailSvc) Reply(ctx context.Context, mailbox, id, body string, replyAll bool) error {
	return m.c.Reply(ctx, mailbox, id, body, replyAll)
}

func (m mailSvc) CreateReplyDraft(ctx context.Context, mailbox, id, body string, replyAll bool) (string, error) {
	return m.c.CreateReplyDraft(ctx, mailbox, id, body, replyAll)
}

func (m mailSvc) Attachments(ctx context.Context, mailbox, msgID string) ([]byte, error) {
	atts, err := m.c.ListAttachments(ctx, mailbox, msgID)
	if err != nil {
		return nil, err
	}
	return attachmentsJSON(atts)
}

// GetAttachment downloads a file attachment. msgID is unused on EWS (the
// attachment id is self-contained); the mailbox drives impersonation.
func (m mailSvc) GetAttachment(ctx context.Context, mailbox, msgID, attID string) ([]byte, error) {
	ac, err := m.c.GetAttachment(ctx, mailbox, attID)
	if err != nil {
		return nil, err
	}
	return attachmentContentJSON(ac)
}

// MailboxDelta backs `mail watch`: it runs one SyncFolderItems page and emits a
// Graph-delta-shaped document so the transport-neutral watch pipeline consumes
// it unchanged. The cursor encodes the EWS SyncState.
func (m mailSvc) MailboxDelta(ctx context.Context, mailbox, urlOrCursor string) ([]byte, error) {
	return mailboxDelta(ctx, m.c, mailbox, urlOrCursor)
}
