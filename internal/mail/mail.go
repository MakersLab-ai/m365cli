// Package mail holds mailbox business logic that is independent of HTTP: the
// send guardrail (external recipients fall back to a draft for human review) and
// construction of Graph message payloads.
package mail

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

// SendAction is the outcome of the send guardrail.
type SendAction int

const (
	// SendDirect: every recipient is permitted, send immediately.
	SendDirect SendAction = iota
	// DraftOnly: at least one recipient is not in send_allow — create a draft
	// for human review instead of sending.
	DraftOnly
)

func (a SendAction) String() string {
	if a == DraftOnly {
		return "DraftOnly"
	}
	return "SendDirect"
}

// SendPlan is the guardrail decision for a set of recipients.
type SendPlan struct {
	Action  SendAction
	Blocked []string // recipients that forced a draft (empty for SendDirect)
}

// PlanSend decides whether a message may be sent directly or must become a draft.
// A single recipient outside send_allow downgrades the whole message to a draft
// (we never partially send). send_unrestricted permits everything via CanSendTo.
func PlanSend(cfg *config.Config, recipients []string) SendPlan {
	var blocked []string
	for _, r := range recipients {
		if !cfg.CanSendTo(r) {
			blocked = append(blocked, r)
		}
	}
	if len(blocked) > 0 {
		return SendPlan{Action: DraftOnly, Blocked: blocked}
	}
	return SendPlan{Action: SendDirect}
}

// Message is a plain-text message to compose.
type Message struct {
	Subject string
	Body    string
	To      []string
	Cc      []string
}

type emailAddress struct {
	Address string `json:"address"`
}

type recipient struct {
	EmailAddress emailAddress `json:"emailAddress"`
}

type graphMessage struct {
	Subject string `json:"subject"`
	Body    struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	ToRecipients []recipient `json:"toRecipients"`
	CcRecipients []recipient `json:"ccRecipients,omitempty"`
}

func recipients(addrs []string) []recipient {
	out := make([]recipient, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, recipient{EmailAddress: emailAddress{Address: a}})
	}
	return out
}

func toGraph(m Message) (graphMessage, error) {
	if len(m.To) == 0 && len(m.Cc) == 0 {
		return graphMessage{}, fmt.Errorf("message has no recipients")
	}
	gm := graphMessage{
		Subject:      m.Subject,
		ToRecipients: recipients(m.To),
		CcRecipients: recipients(m.Cc),
	}
	gm.Body.ContentType = "Text"
	gm.Body.Content = m.Body
	return gm, nil
}

// BuildMessage renders a Graph message object (used to create a draft via
// POST /messages).
func BuildMessage(m Message) ([]byte, error) {
	gm, err := toGraph(m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(gm)
}

// BuildSendMail renders a Graph sendMail payload ({message, saveToSentItems}).
func BuildSendMail(m Message) ([]byte, error) {
	gm, err := toGraph(m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Message         graphMessage `json:"message"`
		SaveToSentItems bool         `json:"saveToSentItems"`
	}{Message: gm, SaveToSentItems: true})
}

// messageEnvelope is the subset of a Graph message we read to plan a reply.
type messageEnvelope struct {
	From         recipient   `json:"from"`
	ToRecipients []recipient `json:"toRecipients"`
	CcRecipients []recipient `json:"ccRecipients"`
}

// ReplyRecipients extracts the addresses a reply would go to: the sender for a
// plain reply, plus the original To/Cc (deduplicated) for reply-all. These feed
// the send guardrail so replies are subject to the same send_allow policy.
func ReplyRecipients(messageJSON []byte, replyAll bool) ([]string, error) {
	var env messageEnvelope
	if err := json.Unmarshal(messageJSON, &env); err != nil {
		return nil, fmt.Errorf("parse message for reply: %w", err)
	}
	if env.From.EmailAddress.Address == "" {
		return nil, fmt.Errorf("message has no sender to reply to")
	}
	seen := map[string]bool{}
	var out []string
	add := func(addr string) {
		if addr != "" && !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	add(env.From.EmailAddress.Address)
	if replyAll {
		for _, r := range env.ToRecipients {
			add(r.EmailAddress.Address)
		}
		for _, r := range env.CcRecipients {
			add(r.EmailAddress.Address)
		}
	}
	return out, nil
}

// BuildReplyComment renders the {comment} payload for reply / replyAll.
func BuildReplyComment(body string) ([]byte, error) {
	return json.Marshal(struct {
		Comment string `json:"comment"`
	}{Comment: body})
}

// DecodeAttachment extracts the file name and decoded bytes from a Graph
// fileAttachment. It errors for attachments without contentBytes (item or
// reference attachments cannot be downloaded this way).
func DecodeAttachment(attachmentJSON []byte) (string, []byte, error) {
	var att struct {
		Name         string `json:"name"`
		ContentBytes string `json:"contentBytes"`
	}
	if err := json.Unmarshal(attachmentJSON, &att); err != nil {
		return "", nil, fmt.Errorf("parse attachment: %w", err)
	}
	if att.ContentBytes == "" {
		return "", nil, fmt.Errorf("attachment %q has no contentBytes (not a file attachment)", att.Name)
	}
	content, err := base64.StdEncoding.DecodeString(att.ContentBytes)
	if err != nil {
		return "", nil, fmt.Errorf("decode attachment %q: %w", att.Name, err)
	}
	return att.Name, content, nil
}
