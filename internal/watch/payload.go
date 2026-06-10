package watch

import (
	"encoding/json"
	"strings"
)

// Options controls watch behaviour shared by payload building and the syncer.
type Options struct {
	Folder         string
	IncludeBody    bool
	MaxBytes       int
	IncludeChanged bool
	IncludeDeleted bool
}

type graphMsg struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversationId"`
	Subject          string `json:"subject"`
	BodyPreview      string `json:"bodyPreview"`
	ReceivedDateTime string `json:"receivedDateTime"`
	IsRead           bool   `json:"isRead"`
	From             struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	ToRecipients []struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"toRecipients"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
}

type hookMessage struct {
	ID            string   `json:"id"`
	ThreadID      string   `json:"threadId"`
	From          string   `json:"from"`
	To            string   `json:"to"`
	Subject       string   `json:"subject"`
	Date          string   `json:"date"`
	Snippet       string   `json:"snippet"`
	Body          string   `json:"body,omitempty"`
	BodyTruncated bool     `json:"bodyTruncated,omitempty"`
	Labels        []string `json:"labels"`
}

type hookPayload struct {
	Source            string        `json:"source"`
	Account           string        `json:"account"`
	Messages          []hookMessage `json:"messages"`
	DeletedMessageIds []string      `json:"deletedMessageIds,omitempty"`
}

// BuildHookPayload maps Graph messages to the gog-compatible webhook payload.
func BuildHookPayload(account string, items []json.RawMessage, removed []string, opt Options) ([]byte, error) {
	payload := hookPayload{Source: "m365", Account: account}
	for _, raw := range items {
		var gm graphMsg
		if err := json.Unmarshal(raw, &gm); err != nil {
			return nil, err
		}
		hm := hookMessage{
			ID: gm.ID, ThreadID: gm.ConversationID,
			From: gm.From.EmailAddress.Address, To: joinRecipients(gm.ToRecipients),
			Subject: gm.Subject, Date: gm.ReceivedDateTime, Snippet: gm.BodyPreview,
			Labels: labelsFor(opt.Folder, gm.IsRead),
		}
		if opt.IncludeBody {
			body, truncated := truncate(gm.Body.Content, opt.MaxBytes)
			hm.Body, hm.BodyTruncated = body, truncated
		}
		payload.Messages = append(payload.Messages, hm)
	}
	payload.DeletedMessageIds = removed
	return json.Marshal(payload)
}

func joinRecipients(rs []struct {
	EmailAddress struct {
		Address string `json:"address"`
	} `json:"emailAddress"`
}) string {
	addrs := make([]string, 0, len(rs))
	for _, r := range rs {
		addrs = append(addrs, r.EmailAddress.Address)
	}
	return strings.Join(addrs, ", ")
}

func labelsFor(folder string, isRead bool) []string {
	labels := []string{strings.ToUpper(folder)}
	if !isRead {
		labels = append(labels, "UNREAD")
	}
	return labels
}

func truncate(s string, maxBytes int) (string, bool) {
	if maxBytes > 0 && len(s) > maxBytes {
		return s[:maxBytes], true
	}
	return s, false
}
