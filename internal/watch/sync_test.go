package watch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeGraph returns a queued response per call, keyed by call order.
type fakeGraph struct {
	responses [][]byte
	calls     int
}

func (g *fakeGraph) MailboxDelta(_ context.Context, _, _ string) ([]byte, error) {
	r := g.responses[g.calls]
	g.calls++
	return r, nil
}

type fakeHook struct {
	posts       int
	lastPayload []byte
	fail        bool
}

func (h *fakeHook) Post(_ context.Context, payload []byte) error {
	h.posts++
	h.lastPayload = payload
	if h.fail {
		return errors.New("hook down")
	}
	return nil
}

func memStore() *FileStore { return NewFileStore("") } // dir "" → writes to cwd-relative; override below

func TestSyncOnceFirstRunPrimesWithoutDelivering(t *testing.T) {
	g := &fakeGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/mailFolders/inbox/messages/delta?$deltatoken=D1","value":[` + string(msg("m1", "b@x.com", "S", "p", "body", false)) + `]}`),
	}}
	h := &fakeHook{}
	st := NewFileStore(t.TempDir())
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if h.posts != 0 {
		t.Error("first run must NOT deliver the backlog")
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink == "" || len(got.Seen) != 1 {
		t.Errorf("first run must persist deltaLink + seed seen-set, got %+v", got)
	}
}

func TestSyncOnceDeliversNewMessageOnSecondRun(t *testing.T) {
	st := NewFileStore(t.TempDir())
	_ = st.Put("a@x.com", "inbox", State{DeltaLink: "https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=OLD", Seen: []string{"old1"}})
	g := &fakeGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=NEW","value":[` + string(msg("m2", "b@x.com", "New", "p", "body", false)) + `]}`),
	}}
	h := &fakeHook{}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if h.posts != 1 {
		t.Fatalf("expected 1 delivery, got %d", h.posts)
	}
	var p struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(h.lastPayload, &p)
	if len(p.Messages) != 1 || p.Messages[0].ID != "m2" {
		t.Errorf("payload messages = %v", p.Messages)
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink == "" || got.DeltaLink[len(got.DeltaLink)-3:] != "NEW" {
		t.Errorf("cursor must advance to NEW, got %q", got.DeltaLink)
	}
}

func TestSyncOnceDoesNotAdvanceCursorWhenHookFails(t *testing.T) {
	st := NewFileStore(t.TempDir())
	_ = st.Put("a@x.com", "inbox", State{DeltaLink: "OLD", Seen: []string{"old1"}})
	g := &fakeGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"NEW","value":[` + string(msg("m2", "b@x.com", "S", "p", "body", false)) + `]}`),
	}}
	h := &fakeHook{fail: true}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err == nil {
		t.Fatal("expected error when hook fails")
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink != "OLD" {
		t.Errorf("cursor must stay OLD on hook failure, got %q", got.DeltaLink)
	}
}

func TestSyncOnceDefaultSkipsChangedMessages(t *testing.T) {
	st := NewFileStore(t.TempDir())
	_ = st.Put("a@x.com", "inbox", State{DeltaLink: "OLD", Seen: []string{"m2"}}) // m2 already seen
	g := &fakeGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"NEW","value":[` + string(msg("m2", "b@x.com", "S", "p", "body", true)) + `]}`),
	}}
	h := &fakeHook{}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}} // IncludeChanged=false

	_ = s.SyncOnce(context.Background(), "a@x.com")
	if h.posts != 0 {
		t.Error("a previously-seen (changed) message must not be delivered by default")
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink != "NEW" {
		t.Error("cursor must still advance even when nothing is delivered")
	}
}

func TestSyncOnceErrorsWhenFinalPageHasNoDeltaLink(t *testing.T) {
	st := NewFileStore(t.TempDir())
	g := &fakeGraph{responses: [][]byte{
		[]byte(`{"value":[` + string(msg("m1", "b@x.com", "S", "p", "body", false)) + `]}`),
	}}
	h := &fakeHook{}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err == nil {
		t.Fatal("expected error when the final page has no deltaLink")
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink != "" {
		t.Errorf("nothing must be persisted on a missing deltaLink, got %+v", got)
	}
}

// recordingGraph records requested URLs and can fail a call with a given error.
type recordingGraph struct {
	responses [][]byte
	errs      []error
	urls      []string
}

func (g *recordingGraph) MailboxDelta(_ context.Context, _, u string) ([]byte, error) {
	i := len(g.urls)
	g.urls = append(g.urls, u)
	if i < len(g.errs) && g.errs[i] != nil {
		return nil, g.errs[i]
	}
	return g.responses[i], nil
}

func fixedNow() time.Time { return time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC) }

func msgAt(id, received string) []byte {
	return []byte(`{"id":"` + id + `","subject":"S","receivedDateTime":"` + received + `","from":{"emailAddress":{"address":"b@x.com"}}}`)
}

func TestFirstRunPrimesOnlyRecentWindowNotWholeMailbox(t *testing.T) {
	g := &recordingGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=D1","value":[]}`),
	}}
	st := NewFileStore(t.TempDir())
	s := &Syncer{Graph: g, Hook: &fakeHook{}, Store: st, Opts: Options{Folder: "inbox"}, Now: fixedNow}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	want := "$filter=receivedDateTime+ge+2026-11-26T08:00:00Z"
	if !strings.Contains(g.urls[0], want) {
		t.Errorf("first-run request must be limited to the prime window (%s), got %s", want, g.urls[0])
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.Floor != "2026-11-26T08:00:00Z" {
		t.Errorf("first run must persist the floor, got %q", got.Floor)
	}
}

func TestOlderThanFloorIsNeverDeliveredAsNew(t *testing.T) {
	st := NewFileStore(t.TempDir())
	_ = st.Put("a@x.com", "inbox", State{DeltaLink: "https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=OLD", Floor: "2026-11-26T08:00:00Z"})
	g := &recordingGraph{responses: [][]byte{
		[]byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=NEW","value":[` +
			string(msgAt("old", "2025-01-01T10:00:00Z")) + `,` + string(msgAt("new", "2026-11-26T09:10:00Z")) + `]}`),
	}}
	h := &fakeHook{}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}, Now: fixedNow}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	var p struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(h.lastPayload, &p)
	if h.posts != 1 || len(p.Messages) != 1 || p.Messages[0].ID != "new" {
		t.Fatalf("only the mail after the floor may be delivered, got posts=%d payload=%s", h.posts, h.lastPayload)
	}
}

func TestExpiredDeltaTokenRePrimesInsteadOfLoopingForever(t *testing.T) {
	st := NewFileStore(t.TempDir())
	_ = st.Put("a@x.com", "inbox", State{DeltaLink: "https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=DEAD", Seen: []string{"s1"}})
	g := &recordingGraph{
		errs: []error{errors.New(`graph https://graph.microsoft.com/v1.0/users/a@x.com/d: 410 Gone: {"error":{"code":"SyncStateNotFound","message":"The sync state generation is not found."}}`)},
		responses: [][]byte{nil,
			[]byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=FRESH","value":[` + string(msgAt("r1", "2026-11-26T08:30:00Z")) + `]}`),
		},
	}
	h := &fakeHook{}
	s := &Syncer{Graph: g, Hook: h, Store: st, Opts: Options{Folder: "inbox"}, Now: fixedNow}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err != nil {
		t.Fatalf("an expired token must be recovered, not returned: %v", err)
	}
	if h.posts != 0 {
		t.Error("re-priming must not deliver the gap")
	}
	if len(g.urls) != 2 || !strings.Contains(g.urls[1], "$filter=receivedDateTime+ge+") {
		t.Fatalf("expected a fresh, windowed first-run request after the 410, got %v", g.urls)
	}
	got, _ := st.Get("a@x.com", "inbox")
	if !strings.Contains(got.DeltaLink, "FRESH") || got.Floor == "" {
		t.Errorf("cursor must be replaced with the fresh deltaLink + floor, got %+v", got)
	}
}

func TestOtherDeltaErrorsStillFailWithoutTouchingTheCursor(t *testing.T) {
	st := NewFileStore(t.TempDir())
	old := State{DeltaLink: "https://graph.microsoft.com/v1.0/users/a@x.com/d?$deltatoken=OLD"}
	_ = st.Put("a@x.com", "inbox", old)
	g := &recordingGraph{errs: []error{errors.New("graph x: 503 Service Unavailable: busy")}, responses: [][]byte{nil}}
	s := &Syncer{Graph: g, Hook: &fakeHook{}, Store: st, Opts: Options{Folder: "inbox"}, Now: fixedNow}

	if err := s.SyncOnce(context.Background(), "a@x.com"); err == nil {
		t.Fatal("a transient error must surface")
	}
	got, _ := st.Get("a@x.com", "inbox")
	if got.DeltaLink != old.DeltaLink {
		t.Errorf("cursor must stay untouched on transient errors, got %+v", got)
	}
}
