package ewsbackend

import (
	"context"

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

// --- not yet implemented for EWS ---

func (m mailSvc) ReplyContext(context.Context, string, string, bool) ([]string, error) {
	return nil, backend.ErrUnsupported
}
func (m mailSvc) Reply(context.Context, string, string, string, bool) error {
	return backend.ErrUnsupported
}
func (m mailSvc) CreateReplyDraft(context.Context, string, string, string, bool) (string, error) {
	return "", backend.ErrUnsupported
}
func (m mailSvc) Attachments(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (m mailSvc) GetAttachment(context.Context, string, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (m mailSvc) MailboxDelta(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
