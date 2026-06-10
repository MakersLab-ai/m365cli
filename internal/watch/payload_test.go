package watch

import (
	"encoding/json"
	"strings"
	"testing"
)

func msg(id, from, subject, preview, body string, isRead bool) json.RawMessage {
	return json.RawMessage(`{
		"id":"` + id + `","conversationId":"c-` + id + `",
		"subject":"` + subject + `","bodyPreview":"` + preview + `",
		"receivedDateTime":"2026-06-09T10:00:00Z","isRead":` + boolStr(isRead) + `,
		"from":{"emailAddress":{"address":"` + from + `"}},
		"toRecipients":[{"emailAddress":{"address":"agent@x.com"}}],
		"body":{"content":"` + body + `"}
	}`)
}
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestBuildHookPayloadGogShape(t *testing.T) {
	out, err := BuildHookPayload("agent@x.com",
		[]json.RawMessage{msg("m1", "boss@x.com", "Hello", "hi there", "full body", false)},
		nil, Options{Folder: "inbox", IncludeBody: true, MaxBytes: 20000})
	if err != nil {
		t.Fatalf("BuildHookPayload: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if p["source"] != "m365" || p["account"] != "agent@x.com" {
		t.Errorf("envelope = %v", p)
	}
	msgs := p["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	if m0["from"] != "boss@x.com" || m0["subject"] != "Hello" || m0["snippet"] != "hi there" {
		t.Errorf("message = %v", m0)
	}
	if m0["threadId"] != "c-m1" || m0["to"] != "agent@x.com" {
		t.Errorf("threadId/to = %v", m0)
	}
	if m0["body"] != "full body" {
		t.Errorf("body = %v", m0["body"])
	}
	labels := m0["labels"].([]any)
	if labels[0] != "INBOX" || labels[1] != "UNREAD" {
		t.Errorf("labels = %v, want [INBOX UNREAD]", labels)
	}
}

func TestBuildHookPayloadOmitsBodyWhenNotRequested(t *testing.T) {
	out, _ := BuildHookPayload("a@x.com",
		[]json.RawMessage{msg("m1", "b@x.com", "S", "snip", "secret body", true)},
		nil, Options{Folder: "inbox", IncludeBody: false, MaxBytes: 20000})
	if strings.Contains(string(out), "secret body") {
		t.Error("body must not be included when IncludeBody is false")
	}
}

func TestBuildHookPayloadTruncatesBody(t *testing.T) {
	long := strings.Repeat("x", 100)
	out, _ := BuildHookPayload("a@x.com",
		[]json.RawMessage{msg("m1", "b@x.com", "S", "snip", long, true)},
		nil, Options{Folder: "inbox", IncludeBody: true, MaxBytes: 10})
	var p struct {
		Messages []struct {
			Body          string `json:"body"`
			BodyTruncated bool   `json:"bodyTruncated"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(out, &p)
	if len(p.Messages[0].Body) != 10 || !p.Messages[0].BodyTruncated {
		t.Errorf("body len=%d truncated=%v, want 10/true", len(p.Messages[0].Body), p.Messages[0].BodyTruncated)
	}
}

func TestBuildHookPayloadIncludesDeleted(t *testing.T) {
	out, _ := BuildHookPayload("a@x.com", nil, []string{"d1", "d2"}, Options{Folder: "inbox"})
	var p struct {
		DeletedMessageIds []string `json:"deletedMessageIds"`
	}
	_ = json.Unmarshal(out, &p)
	if len(p.DeletedMessageIds) != 2 {
		t.Errorf("deletedMessageIds = %v", p.DeletedMessageIds)
	}
}
