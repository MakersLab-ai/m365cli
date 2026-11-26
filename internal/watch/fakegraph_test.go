package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

// fakeGraph models Graph message delta as measured on a real 35k-mail mailbox
// (2026-11-26): every page served creates one sync-state generation, Graph
// keeps only the newest few, and a link held two pages back answers
// 410 Gone / SyncStateNotFound. An empty poll returns the same deltaLink
// without creating a generation.
type fakeGraphSrv struct {
	t       *testing.T
	mailbox string
	srv     *httptest.Server

	mu       sync.Mutex
	msgs     []fakeMsg // creation order
	gen      int
	gens     map[int]fakeCursor
	keep     int // a token of generation g is valid while gen-g < keep (measured: 2)
	evictSt  int // status for an evicted token (410 default; 400 variant)
	evictCd  string
	failNext []int // statuses for the next delta requests (e.g. 502)
	retryAft string
	removed  []string // ids to report as @removed on the next delta page

	evictBody  string // raw body for an evicted token (overrides the JSON error)
	tokenFail  int    // status for EVERY token (stored-link) request; 0 = off
	tokenCode  string
	alwaysGone bool  // every delta request, initial included, answers 410
	liveRounds bool  // a round in progress also picks up mail created meanwhile
	msgFail    []int // statuses for the next per-message GETs
	listFail   int   // fail the next n catch-up list requests with 503
	listDup    bool  // list every hit twice
	initFail   int   // status for every INITIAL delta request (deleted mailbox); 0 = off
	onList     func()
	onMsgGet   func()

	deltaReqs    int
	deltaTokens  []string // the token of every token request, in order
	initialQuery []string
	prefer       []string
	msgGets      int
	msgSelects   []string
	listQueries  []string
	onDelta      func() // test hook, called on every delta request
}

type fakeMsg struct {
	ID       string
	Received time.Time
	Subject  string
	Deleted  bool
}

type fakeCursor struct {
	pos, end int
}

func newFakeGraph(t *testing.T, mailbox string) *fakeGraphSrv {
	f := &fakeGraphSrv{t: t, mailbox: mailbox, gens: map[int]fakeCursor{}, keep: 2, evictSt: 410, evictCd: "SyncStateNotFound"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGraphSrv) add(id string, received time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, fakeMsg{ID: id, Received: received, Subject: "subject " + id})
}

func (f *fakeGraphSrv) del(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.msgs {
		if f.msgs[i].ID == id {
			f.msgs[i].Deleted = true
		}
	}
}

// evictAll moves the generation counter far ahead, so every link a client
// holds is gone (what other writers / time do to an idle cursor).
func (f *fakeGraphSrv) evictAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen += 10
}

func (f *fakeGraphSrv) link(kind string, g int) string {
	return fmt.Sprintf("%s/users/%s/mailFolders/inbox/messages/delta?%s=%d", f.srv.URL, f.mailbox, kind, g)
}

func (f *fakeGraphSrv) writeErr(w http.ResponseWriter, status int, code string) {
	if f.retryAft != "" && (status == 429 || status == 503) {
		w.Header().Set("Retry-After", f.retryAft)
	}
	if code == "\x00raw" {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(f.evictBody))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if f.retryAft != "" && (status == 429 || status == 503) {
		w.Header().Set("Retry-After", f.retryAft)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":%q,"message":"fake %d"}}`, code, status)
}

func (f *fakeGraphSrv) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := "/users/" + f.mailbox + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		f.writeErr(w, 403, "ErrorAccessDenied")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	switch {
	case strings.HasSuffix(rest, "/messages/delta"):
		f.serveDelta(w, r)
	case strings.HasPrefix(rest, "messages/"):
		f.serveMessage(w, r, strings.TrimPrefix(rest, "messages/"))
	case strings.HasSuffix(rest, "/messages"):
		f.serveList(w, r)
	default:
		f.writeErr(w, 400, "BadRequest")
	}
}

func (f *fakeGraphSrv) serveDelta(w http.ResponseWriter, r *http.Request) {
	f.deltaReqs++
	if f.onDelta != nil {
		f.mu.Unlock()
		f.onDelta()
		f.mu.Lock()
	}
	f.prefer = append(f.prefer, r.Header.Get("Prefer"))
	if len(f.failNext) > 0 {
		st := f.failNext[0]
		f.failNext = f.failNext[1:]
		f.writeErr(w, st, "ServiceUnavailable")
		return
	}
	if f.alwaysGone {
		f.writeErr(w, 410, "SyncStateNotFound")
		return
	}
	pageSize := 10
	if p := r.Header.Get("Prefer"); strings.HasPrefix(p, "odata.maxpagesize=") {
		pageSize, _ = strconv.Atoi(strings.TrimPrefix(p, "odata.maxpagesize="))
	}
	q := r.URL.Query()
	var cur fakeCursor
	tok, kind := q.Get("$skiptoken"), "$skiptoken"
	if tok == "" {
		tok, kind = q.Get("$deltatoken"), "$deltatoken"
	}
	if tok == "" { // initial request
		f.initialQuery = append(f.initialQuery, r.URL.RawQuery)
		if f.initFail != 0 {
			f.writeErr(w, f.initFail, "ErrorInvalidUser")
			return
		}
		if q.Get("changeType") != "created" || q.Get("$select") != "id" {
			f.t.Errorf("initial delta must be IDs-only created delta, got %q", r.URL.RawQuery)
		}
		cur = fakeCursor{pos: 0, end: len(f.msgs)}
	} else {
		f.deltaTokens = append(f.deltaTokens, kind+"="+tok)
		if f.tokenFail != 0 {
			f.writeErr(w, f.tokenFail, f.tokenCode)
			return
		}
		g, _ := strconv.Atoi(tok)
		c, ok := f.gens[g]
		if !ok || f.gen-g >= f.keep {
			code := f.evictCd
			if f.evictBody != "" {
				code = "\x00raw"
			}
			f.writeErr(w, f.evictSt, code)
			return
		}
		cur = c
		if f.liveRounds {
			cur.end = len(f.msgs)
		}
		if kind == "$deltatoken" {
			cur.end = len(f.msgs)
			if cur.pos == cur.end && len(f.removed) == 0 {
				// Quiet mailbox: same deltaLink back, no new generation.
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, f.link("$deltatoken", g))
				return
			}
		}
	}
	end := cur.pos + pageSize
	if end > cur.end {
		end = cur.end
	}
	var vals []string
	for _, m := range f.msgs[cur.pos:end] {
		vals = append(vals, fmt.Sprintf(`{"id":%q}`, m.ID))
	}
	for _, id := range f.removed {
		vals = append(vals, fmt.Sprintf(`{"id":%q,"@removed":{"reason":"deleted"}}`, id))
	}
	f.removed = nil
	f.gen++
	doc := `{"value":[` + strings.Join(vals, ",") + `],`
	if end < cur.end {
		f.gens[f.gen] = fakeCursor{pos: end, end: cur.end}
		doc += fmt.Sprintf(`"@odata.nextLink":%q}`, f.link("$skiptoken", f.gen))
	} else {
		f.gens[f.gen] = fakeCursor{pos: cur.end, end: cur.end}
		doc += fmt.Sprintf(`"@odata.deltaLink":%q}`, f.link("$deltatoken", f.gen))
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(doc))
}

func (f *fakeGraphSrv) find(id string) *fakeMsg {
	for i := range f.msgs {
		if f.msgs[i].ID == id {
			return &f.msgs[i]
		}
	}
	return nil
}

// messageJSON is what GET /messages/{id} returns, @odata extras included.
func messageJSON(m *fakeMsg, withBody bool) string {
	doc := map[string]any{
		"@odata.context":   "https://graph.microsoft.com/v1.0/$metadata#users('x')/messages/$entity",
		"@odata.etag":      `W/"CQAAABYAAAA"`,
		"id":               m.ID,
		"conversationId":   "conv-" + m.ID,
		"subject":          m.Subject,
		"bodyPreview":      "preview " + m.ID,
		"receivedDateTime": m.Received.UTC().Format(time.RFC3339),
		"isRead":           false,
		"from":             map[string]any{"emailAddress": map[string]any{"name": "Sender", "address": "sender@ext.com"}},
		"toRecipients":     []any{map[string]any{"emailAddress": map[string]any{"name": "Agent", "address": "a@x.com"}}},
	}
	if withBody {
		doc["body"] = map[string]any{"contentType": "html", "content": "<p>BODY-SECRET " + m.ID + "</p>"}
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func (f *fakeGraphSrv) serveMessage(w http.ResponseWriter, r *http.Request, id string) {
	f.msgGets++
	if f.onMsgGet != nil {
		f.mu.Unlock()
		f.onMsgGet()
		f.mu.Lock()
	}
	sel := r.URL.Query().Get("$select")
	f.msgSelects = append(f.msgSelects, sel)
	if len(f.msgFail) > 0 {
		st := f.msgFail[0]
		f.msgFail = f.msgFail[1:]
		if st == -1 { // transport error: drop the connection
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		f.writeErr(w, st, "ServerBusy")
		return
	}
	m := f.find(id)
	if m == nil || m.Deleted {
		f.writeErr(w, 404, "ErrorItemNotFound")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(messageJSON(m, strings.Contains(sel, "body,") || strings.HasSuffix(sel, ",body"))))
}

func (f *fakeGraphSrv) serveList(w http.ResponseWriter, r *http.Request) {
	f.listQueries = append(f.listQueries, r.URL.RawQuery)
	if f.onList != nil {
		f.mu.Unlock()
		f.onList()
		f.mu.Lock()
	}
	if f.listFail > 0 {
		f.listFail--
		f.writeErr(w, 503, "ServiceUnavailable")
		return
	}
	q := r.URL.Query()
	filter := q.Get("$filter")
	since, err := time.Parse(time.RFC3339, strings.TrimPrefix(filter, "receivedDateTime ge "))
	if err != nil || q.Get("$orderby") != "receivedDateTime desc" {
		f.writeErr(w, 400, "BadRequest")
		return
	}
	top, _ := strconv.Atoi(q.Get("$top"))
	var hits []fakeMsg
	for _, m := range f.msgs {
		if !m.Deleted && !m.Received.Before(since) {
			hits = append(hits, m)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Received.After(hits[j].Received) })
	if top > 0 && len(hits) > top {
		hits = hits[:top]
	}
	var vals []string
	for _, m := range hits {
		v := fmt.Sprintf(`{"id":%q,"receivedDateTime":%q}`, m.ID, m.Received.UTC().Format(time.RFC3339))
		vals = append(vals, v)
		if f.listDup {
			vals = append(vals, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"value":[` + strings.Join(vals, ",") + `]}`))
}

// fakeHookSrv is the downstream webhook.
type fakeHookSrv struct {
	srv   *httptest.Server
	mu    sync.Mutex
	down  bool
	posts [][]byte
	// reject: a POST whose payload contains this id is answered 422.
	reject string
	status int // answer every POST with this status (0 = normal)
	tries  int
}

func newFakeHook(t *testing.T) *fakeHookSrv {
	h := &fakeHookSrv{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.down {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("gateway restarting"))
			return
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		h.tries++
		if h.status != 0 {
			w.WriteHeader(h.status)
			return
		}
		if h.reject != "" && strings.Contains(buf.String(), `"id":"`+h.reject+`"`) {
			w.WriteHeader(422)
			_, _ = w.Write([]byte("unprocessable"))
			return
		}
		h.posts = append(h.posts, buf.Bytes())
		w.WriteHeader(200)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHookSrv) setDown(v bool) { h.mu.Lock(); h.down = v; h.mu.Unlock() }
func (h *fakeHookSrv) count() int     { h.mu.Lock(); defer h.mu.Unlock(); return len(h.posts) }

// deliveredIDs returns every message id POSTed so far, in order.
func (h *fakeHookSrv) deliveredIDs(t *testing.T) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ids []string
	for _, p := range h.posts {
		var doc struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(p, &doc); err != nil {
			t.Fatalf("hook payload is not JSON: %v", err)
		}
		for _, m := range doc.Messages {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

type fakeTokens struct{}

func (fakeTokens) Token(context.Context) (string, error) { return "graph-bearer-SECRET", nil }

// harness wires a v2 Engine to the fakes with a controllable clock.
type harness struct {
	t     *testing.T
	g     *fakeGraphSrv
	hook  *fakeHookSrv
	dir   string
	store *V2Store
	log   *bytes.Buffer
	now   time.Time
	opts  EngineOptions
	eng   *Engine
}

const mbx = "a@x.com"

func newHarness(t *testing.T) *harness {
	h := &harness{
		t: t, g: newFakeGraph(t, mbx), hook: newFakeHook(t), dir: t.TempDir(),
		log: &bytes.Buffer{}, now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		opts: EngineOptions{Folder: "inbox", IncludeBody: true, MaxBytes: 20000},
	}
	h.store = NewV2Store(h.dir)
	h.restart()
	return h
}

// restart builds a fresh Engine on the same state dir (a process restart).
func (h *harness) restart() {
	if h.eng != nil {
		h.eng.Close()
	}
	client := graph.New(&config.Config{AllowedMailboxes: []string{mbx}}, fakeTokens{})
	client.BaseURL = h.g.srv.URL
	logger := NewLogger(h.log)
	logger.Now = func() time.Time { return h.now }
	h.eng = &Engine{
		Graph: client, Hook: NewHookClient(h.hook.srv.URL+"/hooks/m365?tok=HOOKQUERY", "hook-SECRET"),
		Store: h.store, Opts: h.opts, Log: logger, Now: func() time.Time { return h.now },
	}
}

func (h *harness) poll() error { return h.eng.Poll(context.Background(), mbx) }

func (h *harness) mustPoll() {
	h.t.Helper()
	if err := h.poll(); err != nil {
		h.t.Fatalf("poll: %v\nlog:\n%s", err, h.log)
	}
}

func (h *harness) advance(d time.Duration) { h.now = h.now.Add(d) }

func (h *harness) state() StateV2 {
	h.t.Helper()
	st, status, _, err := h.store.Load(mbx, "inbox")
	if err != nil || status != LoadOK {
		h.t.Fatalf("load v2: status=%v err=%v", status, err)
	}
	return st
}

// seed adds n messages received before "now".
func (h *harness) seed(prefix string, n int) {
	for i := 0; i < n; i++ {
		h.g.add(fmt.Sprintf("%s%05d", prefix, i), h.now.Add(-time.Duration(n-i)*time.Minute))
	}
}

func (h *harness) newMail(id string) { h.g.add(id, h.now) }

func unescapeQuery(s string) string { u, _ := url.QueryUnescape(s); return u }

// gets reads the per-message GET counter under the lock (a hijacked
// connection lets the client see the error before the handler has returned).
func (f *fakeGraphSrv) gets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgGets
}
