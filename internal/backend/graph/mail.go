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

func (m mailSvc) Reply(ctx context.Context, mailbox, id string, body mail.Body, replyAll bool) error {
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

func (m mailSvc) CreateReplyDraft(ctx context.Context, mailbox, id string, body mail.Body, replyAll bool) (string, error) {
	create := "createReply"
	if replyAll {
		create = "createReplyAll"
	}
	draftJSON, err := m.c.PostForMailbox(ctx, mailbox, "messages/"+url.PathEscape(id)+"/"+create, nil)
	if err != nil {
		return "", err
	}
	var draft struct {
		ID   string `json:"id"`
		Body struct {
			Content string `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal(draftJSON, &draft); err != nil || draft.ID == "" {
		return "", fmt.Errorf("create reply draft: unexpected response: %s", string(draftJSON))
	}
	// The draft createReply hands back is an HTML message (it carries the quoted
	// original), so the body is written as HTML — patching it as Text converts
	// nothing and loses every line break. The quote is kept: the reply goes on
	// top of it, the thread and its inline images stay below.
	quoted := draft.Body.Content
	if quoted == "" {
		quoted = m.draftBody(ctx, mailbox, draft.ID)
	}
	patch, err := mail.BuildReplyBodyPatch(body, quoted)
	if err != nil {
		return "", err
	}
	if _, err := m.c.PatchForMailbox(ctx, mailbox, "messages/"+url.PathEscape(draft.ID), patch); err != nil {
		return "", err
	}
	return draft.ID, nil
}

// draftBody reads a draft's body back. createReply answers with the full
// message resource, so this is a fallback for a response that omits the body
// projection: better one extra GET than a silently dropped thread.
func (m mailSvc) draftBody(ctx context.Context, mailbox, id string) string {
	raw, err := m.c.GetForMailbox(ctx, mailbox, "messages/"+url.PathEscape(id)+"?$select=body")
	if err != nil {
		return ""
	}
	var got struct {
		Body struct {
			Content string `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return ""
	}
	return got.Body.Content
}

// AddInlineImage attaches an image to an existing draft and marks it inline, so
// the body can reference it as <img src="cid:…">.
func (m mailSvc) AddInlineImage(ctx context.Context, mailbox, msgID string, img mail.InlineImage) error {
	payload, err := mail.BuildInlineAttachment(img)
	if err != nil {
		return err
	}
	_, err = m.c.PostForMailbox(ctx, mailbox, "messages/"+url.PathEscape(msgID)+"/attachments", payload)
	return err
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
