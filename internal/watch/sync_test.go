package watch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
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
