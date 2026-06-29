package graphbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/graph"
	"github.com/MakersLab-ai/m365cli/internal/mail"
)

type mailSvc struct{ c *graph.Client }

func (m mailSvc) List(ctx context.Context, mailbox string, opts backend.ListOpts) ([]byte, error) {
	suffix := fmt.Sprintf("messages?$top=%d&$select=id,subject,from,receivedDateTime,isRead", opts.Max)
	return unwrapValue(m.c.GetForMailbox(ctx, mailbox, suffix))
}

func (m mailSvc) Read(ctx context.Context, mailbox, id string) ([]byte, error) {
	suffix := "messages/" + url.PathEscape(id) +
		"?$select=id,subject,from,toRecipients,ccRecipients,receivedDateTime,isRead,body"
	return m.c.GetForMailbox(ctx, mailbox, suffix)
}

func (m mailSvc) Search(ctx context.Context, mailbox string, opts backend.SearchOpts) ([]byte, error) {
	// Graph $search requires the value to be a quoted string.
	q := url.QueryEscape(`"` + opts.Query + `"`)
	suffix := fmt.Sprintf("messages?$search=%s&$top=%d&$select=id,subject,from,receivedDateTime,isRead", q, opts.Max)
	return unwrapValue(m.c.GetForMailbox(ctx, mailbox, suffix))
}

func (m mailSvc) Send(ctx context.Context, mailbox string, msg mail.Message) error {
	payload, err := mail.BuildSendMail(msg)
	if err != nil {
		return err
	}
	_, err = m.c.PostForMailbox(ctx, mailbox, "sendMail", payload)
	return err
}

func (m mailSvc) CreateDraft(ctx context.Context, mailbox string, msg mail.Message) (string, error) {
	payload, err := mail.BuildMessage(msg)
	if err != nil {
		return "", err
	}
	body, err := m.c.PostForMailbox(ctx, mailbox, "messages", payload)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	return created.ID, nil
}

func (m mailSvc) ReplyContext(ctx context.Context, mailbox, id string, replyAll bool) ([]string, error) {
	msgJSON, err := m.c.GetForMailbox(ctx, mailbox, "messages/"+url.PathEscape(id)+"?$select=from,toRecipients,ccRecipients")
	if err != nil {
		return nil, err
	}
	return mail.ReplyRecipients(msgJSON, replyAll)
}

func (m mailSvc) Reply(ctx context.Context, mailbox, id, body string, replyAll bool) error {
	payload, err := mail.BuildReplyComment(body)
	if err != nil {
		return err
	}
	action := "reply"
	if replyAll {
		action = "replyAll"
	}
	_, err = m.c.PostForMailbox(ctx, mailbox, "messages/"+url.PathEscape(id)+"/"+action, payload)
	return err
}

func (m mailSvc) CreateReplyDraft(ctx context.Context, mailbox, id, body string, replyAll bool) (string, error) {
	create := "createReply"
	if replyAll {
		create = "createReplyAll"
	}
	draftJSON, err := m.c.PostForMailbox(ctx, mailbox, "messages/"+url.PathEscape(id)+"/"+create, nil)
	if err != nil {
		return "", err
	}
	var draft struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(draftJSON, &draft); err != nil || draft.ID == "" {
		return "", fmt.Errorf("create reply draft: unexpected response: %s", string(draftJSON))
	}
	patch, err := json.Marshal(map[string]any{
		"body": map[string]string{"contentType": "Text", "content": body},
	})
	if err != nil {
		return "", err
	}
	if _, err := m.c.PatchForMailbox(ctx, mailbox, "messages/"+url.PathEscape(draft.ID), patch); err != nil {
		return "", err
	}
	return draft.ID, nil
}

func (m mailSvc) Attachments(ctx context.Context, mailbox, msgID string) ([]byte, error) {
	suffix := "messages/" + url.PathEscape(msgID) + "/attachments?$select=id,name,contentType,size"
	return unwrapValue(m.c.GetForMailbox(ctx, mailbox, suffix))
}

func (m mailSvc) GetAttachment(ctx context.Context, mailbox, msgID, attID string) ([]byte, error) {
	suffix := "messages/" + url.PathEscape(msgID) + "/attachments/" + url.PathEscape(attID)
	return m.c.GetForMailbox(ctx, mailbox, suffix)
}

func (m mailSvc) MailboxDelta(ctx context.Context, mailbox, urlOrCursor string) ([]byte, error) {
	return m.c.MailboxDelta(ctx, mailbox, urlOrCursor)
}
