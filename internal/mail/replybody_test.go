package mail

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpliceIntoQuotedPutsReplyInsideTheBodyTag(t *testing.T) {
	// The quote createReply hands back is a full HTML document. A reply glued
	// in front of <html> renders outside the body — Outlook shows it, Gmail
	// and some clients drop it. It belongs right after the opening <body>.
	quoted := `<html><head><style>p{}</style></head><body lang="DE" style="margin:0"><div>Von: Julian</div></body></html>`

	got := SpliceIntoQuoted("<p>Danke!</p>", quoted)

	want := `<html><head><style>p{}</style></head><body lang="DE" style="margin:0"><p>Danke!</p><div>Von: Julian</div></body></html>`
	if got != want {
		t.Errorf("splice = %q,\nwant %q", got, want)
	}
}

func TestSpliceIntoQuotedKeepsTheWholeQuote(t *testing.T) {
	// The regression this guards: the reply used to REPLACE the quote, which
	// is where "mal ohne Thread, mal ohne Bilder" came from.
	quoted := `<html><body><div>Thread</div><img src="cid:image001.png@01DD"></body></html>`

	got := SpliceIntoQuoted("<p>kurz</p>", quoted)

	for _, keep := range []string{"<div>Thread</div>", `cid:image001.png@01DD`} {
		if !strings.Contains(got, keep) {
			t.Errorf("splice dropped %q from the quoted original: %q", keep, got)
		}
	}
	if strings.Index(got, "<p>kurz</p>") > strings.Index(got, "<div>Thread</div>") {
		t.Errorf("reply must sit above the quote, got %q", got)
	}
}

func TestSpliceIntoQuotedFragmentAndEmptyQuote(t *testing.T) {
	if got := SpliceIntoQuoted("<p>hi</p>", "<div>zitat</div>"); got != "<p>hi</p><div>zitat</div>" {
		t.Errorf("fragment quote = %q, want reply then fragment", got)
	}
	if got := SpliceIntoQuoted("<p>hi</p>", "   "); got != "<p>hi</p>" {
		t.Errorf("empty quote = %q, want the reply alone", got)
	}
}

func TestBuildReplyBodyPatchKeepsQuotedThread(t *testing.T) {
	payload, err := BuildReplyBodyPatch(Body{Content: "eins\nzwei"}, `<html><body><div>Original</div></body></html>`)
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
	want := `<html><body><p>eins<br>zwei</p><div>Original</div></body></html>`
	if body["content"] != want {
		t.Errorf("content = %v,\nwant %v", body["content"], want)
	}
}

func TestBuildInlineAttachmentIsAnInlineFileAttachment(t *testing.T) {
	img, err := NewInlineImage("image001.png@01DD24CD", "/tmp/logo.png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatalf("NewInlineImage: %v", err)
	}
	payload, err := BuildInlineAttachment(img)
	if err != nil {
		t.Fatalf("BuildInlineAttachment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if m["@odata.type"] != "#microsoft.graph.fileAttachment" {
		t.Errorf("@odata.type = %v", m["@odata.type"])
	}
	if m["isInline"] != true || m["contentId"] != "image001.png@01DD24CD" {
		t.Errorf("inline marker/cid wrong: %v", m)
	}
	if m["name"] != "logo.png" || m["contentType"] != "image/png" {
		t.Errorf("name/contentType = %v / %v, want logo.png / image/png", m["name"], m["contentType"])
	}
	if m["contentBytes"] != "iVBORw==" {
		t.Errorf("contentBytes = %v, want base64 of the file", m["contentBytes"])
	}
}

func TestNewInlineImageRejectsMissingCidAndEmptyFile(t *testing.T) {
	if _, err := NewInlineImage("", "/tmp/logo.png", []byte{1}); err == nil {
		t.Error("empty content id accepted, want error")
	}
	if _, err := NewInlineImage("cid1", "/tmp/logo.png", nil); err == nil {
		t.Error("empty file accepted, want error")
	}
}
