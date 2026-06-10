package graph

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

type fakeToken struct{ tok string }

func (f fakeToken) Token(context.Context) (string, error) { return f.tok, nil }

func newTestClient(t *testing.T, cfg *config.Config, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(cfg, fakeToken{tok: "secret-token"})
	c.BaseURL = srv.URL
	return c
}

func TestGetForMailboxRejectsDisallowedMailbox(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	hit := false
	c := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { hit = true })

	_, err := c.GetForMailbox(context.Background(), "intruder@example.com", "messages")
	if err == nil {
		t.Fatal("expected error for out-of-scope mailbox")
	}
	if hit {
		t.Error("server must NOT be called for a disallowed mailbox (fail before network)")
	}
}

func TestGetForMailboxBuildsScopedPathAndSendsBearer(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	var gotPath, gotAuth string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[]}`))
	})

	body, err := c.GetForMailbox(context.Background(), "agent@example.com", "messages")
	if err != nil {
		t.Fatalf("GetForMailbox: %v", err)
	}
	if gotPath != "/users/agent@example.com/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if string(body) != `{"value":[]}` {
		t.Errorf("body = %q", body)
	}
}

func TestGetForMailboxReturnsErrorOnGraphError(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	c := newTestClient(t, cfg, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"ErrorAccessDenied"}}`))
	})

	_, err := c.GetForMailbox(context.Background(), "agent@example.com", "messages")
	if err == nil {
		t.Fatal("expected error on 403 from Graph")
	}
}

func TestPostForMailboxRejectsDisallowedMailbox(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	hit := false
	c := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { hit = true })

	_, err := c.PostForMailbox(context.Background(), "intruder@example.com", "sendMail", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for out-of-scope mailbox")
	}
	if hit {
		t.Error("server must NOT be called for a disallowed mailbox")
	}
}

func TestPostForMailboxSendsBodyMethodAndBearer(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	var gotMethod, gotCT, gotAuth, gotBody, gotPath string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted) // Graph sendMail returns 202, empty body
	})

	if _, err := c.PostForMailbox(context.Background(), "agent@example.com", "sendMail", []byte(`{"hi":1}`)); err != nil {
		t.Fatalf("PostForMailbox: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/users/agent@example.com/sendMail" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody != `{"hi":1}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestPostForMailboxReturnsErrorOnGraphError(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	c := newTestClient(t, cfg, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"ErrorInvalidRecipients"}}`))
	})
	if _, err := c.PostForMailbox(context.Background(), "agent@example.com", "sendMail", []byte(`{}`)); err == nil {
		t.Fatal("expected error on 400 from Graph")
	}
}

func TestSearchSitesHitsSitesSearch(t *testing.T) {
	cfg := &config.Config{}
	var gotPath, gotQuery string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("search")
		_, _ = w.Write([]byte(`{"value":[]}`))
	})
	if _, err := c.SearchSites(context.Background(), "contoso"); err != nil {
		t.Fatalf("SearchSites: %v", err)
	}
	if gotPath != "/sites" || gotQuery != "contoso" {
		t.Errorf("path=%q search=%q", gotPath, gotQuery)
	}
}

func TestGetForSiteEnforcesSiteAllowlist(t *testing.T) {
	cfg := &config.Config{AllowedSites: []string{"contoso.sharepoint.com,*"}}

	blocked := false
	c0 := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { blocked = true })
	if _, err := c0.GetForSite(context.Background(), "evil.sharepoint.com,a,b", "drives"); err == nil || blocked {
		t.Fatal("GetForSite must reject out-of-scope site before I/O")
	}

	var gotPath string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"value":[]}`))
	})
	if _, err := c.GetForSite(context.Background(), "contoso.sharepoint.com,s,w", "drives"); err != nil {
		t.Fatalf("GetForSite: %v", err)
	}
	if gotPath != "/sites/contoso.sharepoint.com,s,w/drives" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestPutRawForMailboxSendsBytesAndContentType(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}

	blocked := false
	c0 := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { blocked = true })
	if _, err := c0.PutRawForMailbox(context.Background(), "x@evil.com", "drive/root:/f.txt:/content", "text/plain", []byte("x")); err == nil || blocked {
		t.Fatal("PutRawForMailbox must reject out-of-scope mailbox before I/O")
	}

	var gotMethod, gotCT, gotBody string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	if _, err := c.PutRawForMailbox(context.Background(), "agent@example.com", "drive/root:/f.txt:/content", "text/plain", []byte("hello")); err != nil {
		t.Fatalf("PutRawForMailbox: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotCT != "text/plain" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody != "hello" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestDeleteForMailboxUsesDeleteAndEnforcesAllowlist(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}

	blocked := false
	c0 := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { blocked = true })
	if err := c0.DeleteForMailbox(context.Background(), "x@evil.com", "events/1"); err == nil || blocked {
		t.Fatal("DeleteForMailbox must reject out-of-scope mailbox before I/O")
	}

	var gotMethod string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent) // Graph delete returns 204
	})
	if err := c.DeleteForMailbox(context.Background(), "agent@example.com", "events/1"); err != nil {
		t.Fatalf("DeleteForMailbox: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
}

func TestPatchForMailboxUsesPatchAndEnforcesAllowlist(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}

	// out-of-scope mailbox must fail before any network call
	blocked := false
	c0 := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { blocked = true })
	if _, err := c0.PatchForMailbox(context.Background(), "x@evil.com", "messages/1", []byte(`{}`)); err == nil || blocked {
		t.Fatal("PatchForMailbox must reject out-of-scope mailbox before I/O")
	}

	var gotMethod string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	if _, err := c.PatchForMailbox(context.Background(), "agent@example.com", "messages/1", []byte(`{}`)); err != nil {
		t.Fatalf("PatchForMailbox: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
}

func TestMailboxDeltaRejectsOutOfScopeMailbox(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	hit := false
	c := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) { hit = true })
	if _, err := c.MailboxDelta(context.Background(), "x@evil.com", "mailFolders/inbox/messages/delta"); err == nil || hit {
		t.Fatal("MailboxDelta must reject out-of-scope mailbox before I/O")
	}
}

func TestMailboxDeltaBuildsSuffixPath(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	var gotPath string
	c := newTestClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"value":[]}`))
	})
	if _, err := c.MailboxDelta(context.Background(), "agent@example.com", "mailFolders/inbox/messages/delta"); err != nil {
		t.Fatalf("MailboxDelta: %v", err)
	}
	if gotPath != "/users/agent@example.com/mailFolders/inbox/messages/delta" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestMailboxDeltaRejectsAbsoluteUrlOffHostOrForeignMailbox(t *testing.T) {
	cfg := &config.Config{AllowedMailboxes: []string{"agent@example.com"}}
	c := newTestClient(t, cfg, func(http.ResponseWriter, *http.Request) {})
	// off graph host
	if _, err := c.MailboxDelta(context.Background(), "agent@example.com", "https://evil.com/x"); err == nil {
		t.Error("must reject absolute URL not on the Graph host")
	}
	// graph host but a different mailbox
	if _, err := c.MailboxDelta(context.Background(), "agent@example.com",
		"https://graph.microsoft.com/v1.0/users/other@example.com/mailFolders/inbox/messages/delta?$deltatoken=x"); err == nil {
		t.Error("must reject absolute URL belonging to a different mailbox")
	}
}
