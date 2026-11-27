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
	payload, err := BuildReplyComment(Body{Content: "Hallo Sandra,\n\ndanke dir."}, nil)
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
	payload, err := BuildReplyComment(Body{Content: "<p>Hallo</p>", HTML: true}, nil)
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
	payload, err := BuildReplyBodyPatch(Body{Content: "eins\nzwei"}, "", nil)
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
	payload, err := BuildReplyComment(Body{Content: "<p>Hallo Sandra,</p><p>danke dir.</p>"}, nil)
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

// --- BCC (GC ac4ceb28) ---

func TestBuildSendMailCarriesBcc(t *testing.T) {
	payload, err := BuildSendMail(Message{Subject: "S", Body: Body{Content: "B"}, To: []string{"a@p.com"}, Bcc: []string{"boss@p.com"}})
	if err != nil {
		t.Fatalf("BuildSendMail: %v", err)
	}
	var m struct {
		Message struct {
			Bcc []recipient `json:"bccRecipients"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if len(m.Message.Bcc) != 1 || m.Message.Bcc[0].EmailAddress.Address != "boss@p.com" {
		t.Errorf("bccRecipients = %+v, want [boss@p.com]", m.Message.Bcc)
	}
}

func TestBuildMessageOmitsEmptyBcc(t *testing.T) {
	payload, err := BuildMessage(Message{Subject: "S", Body: Body{Content: "B"}, To: []string{"a@p.com"}})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if _, ok := m["bccRecipients"]; ok {
		t.Errorf("empty bcc must be omitted: %s", payload)
	}
}

func TestAllRecipientsIncludesBcc(t *testing.T) {
	// The guardrail is fed from AllRecipients: a BCC that skipped it would be
	// the one recipient nobody else on the mail can see.
	got := Message{To: []string{"a@p.com"}, Cc: []string{"c@p.com"}, Bcc: []string{"b@x.com"}}.AllRecipients()
	if len(got) != 3 || got[2] != "b@x.com" {
		t.Errorf("AllRecipients = %v, want To, Cc and Bcc", got)
	}
}

func TestPlanSendDraftsWhenOnlyBccIsOutsideSendAllow(t *testing.T) {
	cfg := &config.Config{SendAllow: []string{"a@p.com"}}
	msg := Message{To: []string{"a@p.com"}, Bcc: []string{"stranger@x.com"}}
	plan := PlanSend(cfg, msg.AllRecipients())
	if plan.Action != DraftOnly || len(plan.Blocked) != 1 || plan.Blocked[0] != "stranger@x.com" {
		t.Errorf("plan = %+v, want DraftOnly blocked on the BCC", plan)
	}
}

func TestBuildReplyCommentCarriesBcc(t *testing.T) {
	payload, err := BuildReplyComment(Body{Content: "ok"}, []string{"boss@p.com"})
	if err != nil {
		t.Fatalf("BuildReplyComment: %v", err)
	}
	var m struct {
		Comment string `json:"comment"`
		Message struct {
			Bcc  []recipient    `json:"bccRecipients"`
			Body map[string]any `json:"body"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if len(m.Message.Bcc) != 1 || m.Message.Bcc[0].EmailAddress.Address != "boss@p.com" {
		t.Errorf("message.bccRecipients = %+v", m.Message.Bcc)
	}
	// Graph rejects comment + message.body together (400) — never set both.
	if m.Message.Body != nil {
		t.Errorf("message.body must not be set alongside comment: %s", payload)
	}
	if m.Comment == "" {
		t.Errorf("comment lost: %s", payload)
	}
}

func TestBuildReplyCommentWithoutBccHasNoMessage(t *testing.T) {
	payload, err := BuildReplyComment(Body{Content: "ok"}, nil)
	if err != nil {
		t.Fatalf("BuildReplyComment: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if _, ok := m["message"]; ok {
		t.Errorf("no bcc ⇒ payload unchanged ({comment} only): %s", payload)
	}
}

func TestBuildReplyBodyPatchCarriesBcc(t *testing.T) {
	payload, err := BuildReplyBodyPatch(Body{Content: "ok"}, "", []string{"boss@p.com"})
	if err != nil {
		t.Fatalf("BuildReplyBodyPatch: %v", err)
	}
	var m struct {
		Bcc []recipient `json:"bccRecipients"`
	}
	_ = json.Unmarshal(payload, &m)
	if len(m.Bcc) != 1 || m.Bcc[0].EmailAddress.Address != "boss@p.com" {
		t.Errorf("bccRecipients = %+v (%s)", m.Bcc, payload)
	}
}

func TestWithBccFeedsReplyGuardrail(t *testing.T) {
	// reply: the recipients come from the original message, the BCC from the
	// caller — both must reach PlanSend, and the input must not be mutated.
	visible := []string{"a@p.com"}
	cfg := &config.Config{SendAllow: []string{"a@p.com"}}
	plan := PlanSend(cfg, WithBcc(visible, []string{"stranger@x.com"}))
	if plan.Action != DraftOnly {
		t.Errorf("reply with external BCC = %v, want DraftOnly", plan.Action)
	}
	if len(visible) != 1 {
		t.Errorf("WithBcc mutated its input: %v", visible)
	}
}
