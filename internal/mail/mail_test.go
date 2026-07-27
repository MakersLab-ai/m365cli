package mail

import (
	"encoding/json"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

func TestPlanSendDirectWhenAllRecipientsAllowed(t *testing.T) {
	cfg := &config.Config{SendAllow: []string{"a@p.com", "b@p.com"}}
	plan := PlanSend(cfg, []string{"a@p.com", "b@p.com"})
	if plan.Action != SendDirect {
		t.Errorf("Action = %v, want SendDirect", plan.Action)
	}
	if len(plan.Blocked) != 0 {
		t.Errorf("Blocked = %v, want empty", plan.Blocked)
	}
}

func TestPlanSendFallsBackToDraftWhenAnyRecipientBlocked(t *testing.T) {
	cfg := &config.Config{SendAllow: []string{"a@p.com"}}
	plan := PlanSend(cfg, []string{"a@p.com", "stranger@x.com"})
	if plan.Action != DraftOnly {
		t.Errorf("Action = %v, want DraftOnly (one recipient out of send_allow)", plan.Action)
	}
	if len(plan.Blocked) != 1 || plan.Blocked[0] != "stranger@x.com" {
		t.Errorf("Blocked = %v, want [stranger@x.com]", plan.Blocked)
	}
}

func TestPlanSendUnrestrictedAlwaysDirect(t *testing.T) {
	cfg := &config.Config{SendUnrestricted: true}
	plan := PlanSend(cfg, []string{"anyone@anywhere.com"})
	if plan.Action != SendDirect {
		t.Errorf("Action = %v, want SendDirect when send_unrestricted", plan.Action)
	}
}

func TestPlanSendHonoursDomainGlob(t *testing.T) {
	cfg := &config.Config{SendAllow: []string{"*@partner.com"}}
	if PlanSend(cfg, []string{"contact@partner.com"}).Action != SendDirect {
		t.Error("domain glob in send_allow should permit direct send")
	}
}

func TestBuildMessagePayloadShape(t *testing.T) {
	payload, err := BuildMessage(Message{
		Subject: "Hi",
		Body:    Body{Content: "Hello world"},
		To:      []string{"a@p.com"},
		Cc:      []string{"c@p.com"},
	})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if m["subject"] != "Hi" {
		t.Errorf("subject = %v", m["subject"])
	}
	body, _ := m["body"].(map[string]any)
	if body["contentType"] != "Text" || body["content"] != "Hello world" {
		t.Errorf("body = %v", body)
	}
	to, _ := m["toRecipients"].([]any)
	if len(to) != 1 {
		t.Fatalf("toRecipients = %v", m["toRecipients"])
	}
	addr, _ := to[0].(map[string]any)["emailAddress"].(map[string]any)
	if addr["address"] != "a@p.com" {
		t.Errorf("to address = %v", addr["address"])
	}
}

func TestBuildSendMailPayloadWrapsMessageAndSaves(t *testing.T) {
	payload, err := BuildSendMail(Message{Subject: "S", Body: Body{Content: "B"}, To: []string{"a@p.com"}})
	if err != nil {
		t.Fatalf("BuildSendMail: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if _, ok := m["message"]; !ok {
		t.Error("sendMail payload must wrap the message under 'message'")
	}
	if m["saveToSentItems"] != true {
		t.Errorf("saveToSentItems = %v, want true", m["saveToSentItems"])
	}
}

func TestBuildMessageRequiresRecipient(t *testing.T) {
	if _, err := BuildMessage(Message{Subject: "S", Body: Body{Content: "B"}}); err == nil {
		t.Error("BuildMessage must reject a message with no recipients")
	}
}

const sampleMessageJSON = `{
  "from": {"emailAddress": {"address": "sender@x.com"}},
  "toRecipients": [{"emailAddress": {"address": "agent@example.com"}}, {"emailAddress": {"address": "a@x.com"}}],
  "ccRecipients": [{"emailAddress": {"address": "c@x.com"}}]
}`

func TestReplyRecipientsReplyOnlySender(t *testing.T) {
	got, err := ReplyRecipients([]byte(sampleMessageJSON), false)
	if err != nil {
		t.Fatalf("ReplyRecipients: %v", err)
	}
	if len(got) != 1 || got[0] != "sender@x.com" {
		t.Errorf("reply recipients = %v, want [sender@x.com]", got)
	}
}

func TestReplyRecipientsReplyAllIncludesToAndCcDeduped(t *testing.T) {
	got, err := ReplyRecipients([]byte(sampleMessageJSON), true)
	if err != nil {
		t.Fatalf("ReplyRecipients: %v", err)
	}
	want := map[string]bool{"sender@x.com": true, "agent@example.com": true, "a@x.com": true, "c@x.com": true}
	if len(got) != len(want) {
		t.Fatalf("reply-all recipients = %v, want %d unique", got, len(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected recipient %q", g)
		}
	}
}

func TestDecodeAttachmentReturnsNameAndBytes(t *testing.T) {
	att := `{"name":"hello.txt","contentType":"text/plain","contentBytes":"aGVsbG8="}`
	name, content, err := DecodeAttachment([]byte(att))
	if err != nil {
		t.Fatalf("DecodeAttachment: %v", err)
	}
	if name != "hello.txt" {
		t.Errorf("name = %q", name)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want hello", content)
	}
}

func TestDecodeAttachmentRejectsMissingContent(t *testing.T) {
	att := `{"name":"x","contentType":"text/plain"}`
	if _, _, err := DecodeAttachment([]byte(att)); err == nil {
		t.Error("DecodeAttachment must error when contentBytes is absent (e.g. item/reference attachment)")
	}
}

func TestBuildReplyCommentConvertsPlainTextToHTML(t *testing.T) {
	// Graph injects `comment` into the reply's HTML body, so a plain-text
	// comment loses every line break. Convert before handing it over.
	payload, err := BuildReplyComment(Body{Content: "Hallo Sandra,\n\ndanke dir."})
	if err != nil {
		t.Fatalf("BuildReplyComment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if m["comment"] != "<p>Hallo Sandra,</p><p>danke dir.</p>" {
		t.Errorf("comment = %v, want paragraphs", m["comment"])
	}
}

func TestBuildReplyCommentPassesHTMLThrough(t *testing.T) {
	payload, err := BuildReplyComment(Body{Content: "<p>Hallo</p>", HTML: true})
	if err != nil {
		t.Fatalf("BuildReplyComment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if m["comment"] != "<p>Hallo</p>" {
		t.Errorf("comment = %v, want verbatim HTML", m["comment"])
	}
}

func TestBuildMessageMarksHTMLBodies(t *testing.T) {
	payload, err := BuildMessage(Message{Subject: "Hi", Body: Body{Content: "<p>Hello</p>", HTML: true}, To: []string{"a@p.com"}})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	body, _ := m["body"].(map[string]any)
	if body["contentType"] != "HTML" || body["content"] != "<p>Hello</p>" {
		t.Errorf("body = %v, want HTML content type and verbatim content", body)
	}
}

func TestBuildReplyBodyPatchIsAlwaysHTML(t *testing.T) {
	// The reply draft Graph hands back is an HTML message; patching it with a
	// Text body collapses the text into a single paragraph.
	payload, err := BuildReplyBodyPatch(Body{Content: "eins\nzwei"})
	if err != nil {
		t.Fatalf("BuildReplyBodyPatch: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	body, _ := m["body"].(map[string]any)
	if body["contentType"] != "HTML" {
		t.Errorf("contentType = %v, want HTML", body["contentType"])
	}
	if body["content"] != "<p>eins<br>zwei</p>" {
		t.Errorf("content = %v, want line break preserved", body["content"])
	}
}

func TestBuildMessageSendsDetectedHTMLAsHTML(t *testing.T) {
	// The reported bug: the agent wrote an HTML document into the body file,
	// it went out as contentType Text, and the recipient read the tags.
	payload, err := BuildMessage(Message{
		Subject: "Ihre Praktikumsanfrage",
		Body:    Body{Content: "<html><body>\n<p>Sehr geehrte Frau Marko,</p>\n</body></html>"},
		To:      []string{"a@p.com"},
	})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	body, _ := m["body"].(map[string]any)
	if body["contentType"] != "HTML" {
		t.Errorf("contentType = %v, want HTML (body is an HTML document)", body["contentType"])
	}
}

func TestBuildMessageKeepsProseAsText(t *testing.T) {
	payload, err := BuildMessage(Message{Subject: "Hi", Body: Body{Content: "Hallo,\n\ndanke."}, To: []string{"a@p.com"}})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	body, _ := m["body"].(map[string]any)
	if body["contentType"] != "Text" || body["content"] != "Hallo,\n\ndanke." {
		t.Errorf("body = %v, want untouched plain text", body)
	}
}

func TestBuildReplyCommentKeepsHTMLFragmentUnescaped(t *testing.T) {
	// Agents learned to hand-write HTML fragments for replies. Escaping those
	// now would show tags where the old behaviour accidentally rendered.
	payload, err := BuildReplyComment(Body{Content: "<p>Hallo Sandra,</p><p>danke dir.</p>"})
	if err != nil {
		t.Fatalf("BuildReplyComment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if m["comment"] != "<p>Hallo Sandra,</p><p>danke dir.</p>" {
		t.Errorf("comment = %v, want verbatim", m["comment"])
	}
}
