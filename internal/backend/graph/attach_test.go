package graphbackend_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphbackend "github.com/MakersLab-ai/m365cli/internal/backend/graph"
	"github.com/MakersLab-ai/m365cli/internal/graph"
	"github.com/MakersLab-ai/m365cli/internal/mail"
)

func TestMailAddAttachmentSmallPostsRegularFileAttachment(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusCreated, `{"id":"ATT-1"}`)

	att := mail.FileAttachment{Name: "UVA-2026-10.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.7")}
	if err := be.Mail().AddAttachment(context.Background(), mbx, "DRAFT-9", att); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if cap.method != http.MethodPost || cap.uri != "/users/agent@example.com/messages/DRAFT-9/attachments" {
		t.Errorf("request = %s %s", cap.method, cap.uri)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(cap.body), &got); err != nil {
		t.Fatalf("payload not JSON: %v (%s)", err, cap.body)
	}
	if got["isInline"] != false || got["name"] != "UVA-2026-10.pdf" || got["contentType"] != "application/pdf" {
		t.Errorf("payload = %v, want a regular (not inline) PDF attachment", got)
	}
	if _, has := got["contentId"]; has {
		t.Errorf("regular attachment must not carry a contentId: %v", got)
	}
}

// A file above 3 MB goes through createUploadSession and chunked PUTs to the
// upload URL — without a bearer token (Microsoft rejects it there).
func TestMailAddAttachmentLargeUsesUploadSession(t *testing.T) {
	type put struct {
		rng  string
		auth string
		n    int
	}
	var puts []put
	var sessionURI string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attachments/createUploadSession"):
			sessionURI = r.URL.RequestURI()
			var req map[string]map[string]any
			_ = json.Unmarshal(body, &req)
			if req["AttachmentItem"]["attachmentType"] != "file" {
				t.Errorf("session payload = %s", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"uploadUrl":"` + srv.URL + `/upload/abc"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload/abc":
			puts = append(puts, put{rng: r.Header.Get("Content-Range"), auth: r.Header.Get("Authorization"), n: len(body)})
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	c := graph.New(mailboxCfg(), fakeToken{})
	c.BaseURL = srv.URL
	be := graphbackend.New(c)

	size := mail.UploadChunkSize + 1000 // above the 3 MB single-POST limit, two chunks
	att := mail.FileAttachment{Name: "scan.pdf", ContentType: "application/pdf", Data: make([]byte, size)}
	if err := be.Mail().AddAttachment(context.Background(), mbx, "DRAFT-9", att); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if sessionURI != "/users/agent@example.com/messages/DRAFT-9/attachments/createUploadSession" {
		t.Errorf("session uri = %q", sessionURI)
	}
	if len(puts) != 2 {
		t.Fatalf("puts = %d, want 2 chunks", len(puts))
	}
	total := 0
	for _, p := range puts {
		if p.auth != "" {
			t.Errorf("upload PUT carried Authorization %q", p.auth)
		}
		total += p.n
	}
	if total != size {
		t.Errorf("uploaded %d bytes, want %d", total, size)
	}
	chunk := mail.UploadChunkSize
	if want := fmt.Sprintf("bytes 0-%d/%d", chunk-1, size); puts[0].rng != want {
		t.Errorf("first range = %q, want %q", puts[0].rng, want)
	}
	if want := fmt.Sprintf("bytes %d-%d/%d", chunk, size-1, size); puts[1].rng != want {
		t.Errorf("second range = %q, want %q", puts[1].rng, want)
	}
}

func TestMailAddAttachmentRefusesForeignUploadHost(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusCreated, `{"uploadUrl":"https://evil.example.com/up"}`)
	att := mail.FileAttachment{Name: "big.pdf", ContentType: "application/pdf", Data: make([]byte, mail.SmallAttachmentLimit+1)}
	err := be.Mail().AddAttachment(context.Background(), mbx, "DRAFT-9", att)
	if err == nil || !strings.Contains(err.Error(), "not on a Microsoft host") {
		t.Fatalf("err = %v, want refusal of a non-Microsoft upload url", err)
	}
}

func TestMailSendDraftPostsSend(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusAccepted, ``)
	if err := be.Mail().SendDraft(context.Background(), mbx, "DRAFT-9"); err != nil {
		t.Fatalf("SendDraft: %v", err)
	}
	if cap.method != http.MethodPost || cap.uri != "/users/agent@example.com/messages/DRAFT-9/send" {
		t.Errorf("request = %s %s", cap.method, cap.uri)
	}
}

func TestMailAddAttachmentRespectsMailboxAllowlist(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusCreated, `{}`)
	att := mail.FileAttachment{Name: "a.pdf", ContentType: "application/pdf", Data: []byte("x")}
	if err := be.Mail().AddAttachment(context.Background(), "other@example.com", "D", att); err == nil {
		t.Fatal("want allowlist refusal")
	}
	if cap.uri != "" {
		t.Errorf("request went out: %s", cap.uri)
	}
}
