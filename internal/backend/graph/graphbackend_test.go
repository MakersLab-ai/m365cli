package graphbackend_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	graphbackend "github.com/MakersLab-ai/m365cli/internal/backend/graph"
	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/graph"
	"github.com/MakersLab-ai/m365cli/internal/mail"
)

type fakeToken struct{}

func (fakeToken) Token(context.Context) (string, error) { return "secret-token", nil }

const mbx = "agent@example.com"

// capture records what the backend asked Graph for, so golden tests can pin the
// exact REST surface the CLI used to build inline (proving no behavior change).
type capture struct {
	method string
	uri    string // path + raw query, as sent on the wire
	body   string
}

// newBackend wires a graphbackend over an httptest server whose handler both
// records the request into cap and writes resp with status code.
func newBackend(t *testing.T, cfg *config.Config, cap *capture, status int, resp string) backend.Backend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.uri = r.URL.RequestURI()
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
			cap.body = string(buf)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c := graph.New(cfg, fakeToken{})
	c.BaseURL = srv.URL
	return graphbackend.New(c)
}

func mailboxCfg() *config.Config {
	return &config.Config{AllowedMailboxes: []string{mbx}}
}

// --- Mail ---

func TestMailListSuffixAndValueUnwrap(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[{"id":"1"}]}`)

	got, err := be.Mail().List(context.Background(), mbx, backend.ListOpts{Max: 25})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantURI := "/users/agent@example.com/messages?$top=25&$select=id,subject,from,receivedDateTime,isRead"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
	if string(got) != `[{"id":"1"}]` {
		t.Errorf("List unwrap = %q, want the inner value array verbatim", got)
	}
}

func TestMailReadSuffixAndRawPassthrough(t *testing.T) {
	var cap capture
	raw := `{"id":"1","subject":"hi","body":{"contentType":"html","content":"x"}}`
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, raw)

	got, err := be.Mail().Read(context.Background(), mbx, "AAMk=")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantURI := "/users/agent@example.com/messages/AAMk=?$select=id,subject,from,toRecipients,ccRecipients,receivedDateTime,isRead,body"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
	if string(got) != raw {
		t.Errorf("Read = %q, want raw passthrough %q", got, raw)
	}
}

func TestMailSearchQuotesAndEscapes(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[]}`)

	if _, err := be.Mail().Search(context.Background(), mbx, backend.SearchOpts{Query: "invoice", Max: 10}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	wantURI := "/users/agent@example.com/messages?$search=%22invoice%22&$top=10&$select=id,subject,from,receivedDateTime,isRead"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
}

func TestMailSendPostsSendMail(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusAccepted, ``)

	err := be.Mail().Send(context.Background(), mbx, mail.Message{Subject: "s", Body: "b", To: []string{"x@y.com"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if cap.method != http.MethodPost || cap.uri != "/users/agent@example.com/sendMail" {
		t.Errorf("Send hit %s %q, want POST /users/agent@example.com/sendMail", cap.method, cap.uri)
	}
}

func TestMailCreateDraftReturnsID(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusCreated, `{"id":"DRAFT-7"}`)

	id, err := be.Mail().CreateDraft(context.Background(), mbx, mail.Message{Subject: "s", Body: "b", To: []string{"x@y.com"}})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if cap.method != http.MethodPost || cap.uri != "/users/agent@example.com/messages" {
		t.Errorf("CreateDraft hit %s %q, want POST /users/agent@example.com/messages", cap.method, cap.uri)
	}
	if id != "DRAFT-7" {
		t.Errorf("draft id = %q, want DRAFT-7", id)
	}
}

func TestMailAttachmentsUnwraps(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[{"id":"a1"}]}`)

	got, err := be.Mail().Attachments(context.Background(), mbx, "MID")
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	wantURI := "/users/agent@example.com/messages/MID/attachments?$select=id,name,contentType,size"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
	if string(got) != `[{"id":"a1"}]` {
		t.Errorf("Attachments unwrap = %q", got)
	}
}

func TestMailboxDeltaDelegates(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[]}`)

	if _, err := be.Mail().MailboxDelta(context.Background(), mbx, "mailFolders/inbox/messages/delta"); err != nil {
		t.Fatalf("MailboxDelta: %v", err)
	}
	if cap.uri != "/users/agent@example.com/mailFolders/inbox/messages/delta" {
		t.Errorf("delta uri = %q", cap.uri)
	}
}

// --- Calendar ---

func TestCalendarListEventsBranch(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[{"id":"e1"}]}`)

	got, err := be.Calendar().List(context.Background(), mbx, backend.CalListOpts{Max: 25})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantURI := "/users/agent@example.com/events?$top=25&$orderby=start/dateTime&$select=id,subject,start,end,location,organizer,attendees,isAllDay,onlineMeetingUrl"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
	if string(got) != `[{"id":"e1"}]` {
		t.Errorf("List unwrap = %q", got)
	}
}

func TestCalendarListCalendarViewBranch(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[]}`)

	_, err := be.Calendar().List(context.Background(), mbx, backend.CalListOpts{
		Start: "2026-06-10T00:00:00", End: "2026-06-11T00:00:00", Max: 25,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantURI := "/users/agent@example.com/calendarView?startDateTime=2026-06-10T00%3A00%3A00&endDateTime=2026-06-11T00%3A00%3A00&$top=25&$select=id,subject,start,end,location,organizer,attendees,isAllDay,onlineMeetingUrl&$orderby=start/dateTime"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
}

func TestCalendarFreeBusyUnwraps(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[{"scheduleId":"x"}]}`)

	got, err := be.Calendar().FreeBusy(context.Background(), mbx, backend.ScheduleQuery{
		Schedules: []string{"x@y.com"}, Start: "2026-06-10T09:00:00", End: "2026-06-10T17:00:00", Interval: 30,
	})
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if cap.method != http.MethodPost || cap.uri != "/users/agent@example.com/calendar/getSchedule" {
		t.Errorf("FreeBusy hit %s %q", cap.method, cap.uri)
	}
	if string(got) != `[{"scheduleId":"x"}]` {
		t.Errorf("FreeBusy unwrap = %q", got)
	}
}

func TestCalendarDeleteUsesDelete(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusNoContent, ``)

	if err := be.Calendar().Delete(context.Background(), mbx, "E1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if cap.method != http.MethodDelete || cap.uri != "/users/agent@example.com/events/E1" {
		t.Errorf("Delete hit %s %q", cap.method, cap.uri)
	}
}

// --- Contacts ---

func TestContactsListSuffix(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[]}`)

	if _, err := be.Contacts().List(context.Background(), mbx, backend.ListOpts{Max: 50}); err != nil {
		t.Fatalf("List: %v", err)
	}
	wantURI := "/users/agent@example.com/contacts?$top=50&$select=id,displayName,givenName,surname,emailAddresses,mobilePhone,businessPhones"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
}

// --- Drive ---

func TestDriveListRootAndPath(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, `{"value":[]}`)

	if _, err := be.Drive().List(context.Background(), mbx, ""); err != nil {
		t.Fatalf("List root: %v", err)
	}
	if cap.uri != "/users/agent@example.com/drive/root/children?$select=id,name,size,folder,file,lastModifiedDateTime,webUrl" {
		t.Errorf("root uri = %q", cap.uri)
	}

	if _, err := be.Drive().List(context.Background(), mbx, "Reports/2026"); err != nil {
		t.Fatalf("List path: %v", err)
	}
	if cap.uri != "/users/agent@example.com/drive/root:/Reports/2026:/children?$select=id,name,size,folder,file,lastModifiedDateTime,webUrl" {
		t.Errorf("path uri = %q", cap.uri)
	}
}

func TestDriveDownloadReturnsRawBytes(t *testing.T) {
	var cap capture
	be := newBackend(t, mailboxCfg(), &cap, http.StatusOK, "rawfilebytes")

	got, err := be.Drive().Download(context.Background(), mbx, "ITEM1")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if cap.uri != "/users/agent@example.com/drive/items/ITEM1/content" {
		t.Errorf("download uri = %q", cap.uri)
	}
	if string(got) != "rawfilebytes" {
		t.Errorf("download bytes = %q", got)
	}
}

// --- Sites ---

func TestSitesListDrivesUnwraps(t *testing.T) {
	cfg := &config.Config{AllowedSites: []string{"contoso.sharepoint.com,*"}}
	var cap capture
	be := newBackend(t, cfg, &cap, http.StatusOK, `{"value":[{"id":"d1"}]}`)

	got, err := be.Sites().ListDrives(context.Background(), "contoso.sharepoint.com,s,w")
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	wantURI := "/sites/contoso.sharepoint.com,s,w/drives?$select=id,name,webUrl,driveType"
	if cap.uri != wantURI {
		t.Errorf("request uri = %q, want %q", cap.uri, wantURI)
	}
	if string(got) != `[{"id":"d1"}]` {
		t.Errorf("ListDrives unwrap = %q", got)
	}
}

func TestSitesSearchIsUngatedDiscovery(t *testing.T) {
	var cap capture
	// Empty allowed_sites must NOT block discovery search.
	be := newBackend(t, &config.Config{}, &cap, http.StatusOK, `{"value":[]}`)

	if _, err := be.Sites().Search(context.Background(), "contoso"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if cap.uri != "/sites?search=contoso" {
		t.Errorf("search uri = %q", cap.uri)
	}
}
