package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

func uniq(t *testing.T, ids []string) map[string]bool {
	t.Helper()
	m := map[string]bool{}
	for _, id := range ids {
		if m[id] {
			t.Errorf("message %s delivered twice", id)
		}
		m[id] = true
	}
	return m
}

// The fake must reproduce the measured eviction, or the other tests prove nothing.
func TestFakeGraphEvictsALinkHeldTwoPagesBack(t *testing.T) {
	g := newFakeGraph(t, mbx)
	for i := 0; i < 10; i++ {
		g.add(fmt.Sprintf("m%d", i), time.Now())
	}
	c := graph.New(&config.Config{AllowedMailboxes: []string{mbx}}, fakeTokens{})
	c.BaseURL = g.srv.URL
	ctx := context.Background()
	next := func(link string) string {
		body, err := c.MailboxDeltaPage(ctx, mbx, link, 2)
		if err != nil {
			t.Fatalf("delta: %v", err)
		}
		p, _ := parseDeltaIDs(body)
		return p.NextLink
	}
	l1 := next("mailFolders/inbox/messages/delta?changeType=created&$select=id")
	l2 := next(l1)
	_ = next(l2)
	_, err := c.MailboxDeltaPage(ctx, mbx, l1, 2)
	if !graph.IsSyncStateLost(err) {
		t.Fatalf("a link two pages back must be 410 SyncStateNotFound, got %v", err)
	}
	var ae *graph.APIError
	if !errors.As(err, &ae) || ae.Status != 410 {
		t.Fatalf("want typed 410, got %#v", err)
	}
}

func TestPrimeIsIDsOnlyAndDeliversNothing(t *testing.T) {
	h := newHarness(t)
	h.seed("old", 450)
	h.mustPoll()

	if h.hook.count() != 0 {
		t.Fatalf("prime must not POST, got %d", h.hook.count())
	}
	if h.g.msgGets != 0 {
		t.Errorf("prime must not fetch messages, got %d GETs", h.g.msgGets)
	}
	if len(h.g.initialQuery) != 1 || !strings.Contains(unescapeQuery(h.g.initialQuery[0]), "changeType=created") ||
		!strings.Contains(unescapeQuery(h.g.initialQuery[0]), "$select=id") {
		t.Errorf("initial query = %v", h.g.initialQuery)
	}
	for _, p := range h.g.prefer {
		if p != "odata.maxpagesize=200" {
			t.Errorf("Prefer = %q, want odata.maxpagesize=200", p)
		}
	}
	if h.g.deltaReqs != 3 {
		t.Errorf("450 ids at 200/page = 3 pages, got %d requests", h.g.deltaReqs)
	}
	st := h.state()
	if st.DeltaLink == "" || st.NextLink != "" || st.Priming || st.PrimedAt.IsZero() || st.LastSuccessAt.IsZero() {
		t.Errorf("after prime: %+v", st)
	}
	if st.CatchUp != nil || len(st.Pending) != 0 {
		t.Errorf("first run has no catch-up and nothing pending: %+v", st)
	}
}

func TestSecondTickSendsZeroPosts(t *testing.T) {
	h := newHarness(t)
	h.seed("old", 5)
	h.mustPoll() // prime
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 0 {
		t.Fatalf("second tick after prime: %d POSTs, want 0", h.hook.count())
	}

	h.newMail("new1")
	h.advance(30 * time.Second)
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "new1" {
		t.Fatalf("delivered %v, want [new1]", got)
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 1 {
		t.Fatalf("second tick after delivery: %d POSTs, want still 1", h.hook.count())
	}

	h.restart()
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 1 {
		t.Fatalf("second tick after restart: %d POSTs, want still 1", h.hook.count())
	}
}

func TestPayloadMatchesV1ShapeByteForByte(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("n1")
	h.mustPoll()
	if h.hook.count() != 1 {
		t.Fatalf("want 1 POST, got %d", h.hook.count())
	}
	// v1 built the payload from the delta item; the same message through the
	// v1 path must produce identical bytes.
	m := h.g.find("n1")
	want, err := BuildHookPayload(mbx, []json.RawMessage{json.RawMessage(messageJSON(m, true))}, nil,
		Options{Folder: "inbox", IncludeBody: true, MaxBytes: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.hook.posts[0]; string(got) != string(want) {
		t.Errorf("payload differs from v1 shape:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(string(h.hook.posts[0]), "@odata") {
		t.Error("@odata extras of the per-message GET must not leak into the payload")
	}
	if sel := h.g.msgSelects[0]; sel != messageSelect {
		t.Errorf("per-message $select = %q, want production %q", sel, messageSelect)
	}
}

func TestWithoutIncludeBodyTheBodyIsNotFetched(t *testing.T) {
	h := newHarness(t)
	h.opts.IncludeBody = false
	h.restart()
	h.mustPoll()
	h.newMail("n1")
	h.mustPoll()
	if strings.Contains(h.g.msgSelects[0], "body,") || strings.HasSuffix(h.g.msgSelects[0], ",body") {
		t.Errorf("select without --include-body must not request body: %q", h.g.msgSelects[0])
	}
	if strings.Contains(string(h.hook.posts[0]), "BODY-SECRET") {
		t.Error("body leaked without --include-body")
	}
}

func TestMidPagination502ResumesFromPersistedPage(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.restart()
	h.mustPoll() // prime (empty mailbox)
	for i := 0; i < 6; i++ {
		h.newMail(fmt.Sprintf("n%d", i))
	}
	tokensBefore := len(h.g.deltaTokens)
	h.g.failNext = nil
	// Page 1 ok, page 2 → 502.
	h.g.onDelta = func() {
		if h.g.deltaReqs == 3 { // 1 prime + page 1 + this one
			h.g.failNext = []int{502}
		}
	}
	if err := h.poll(); err == nil {
		t.Fatal("a 502 must surface")
	}
	h.g.onDelta = nil
	st := h.state()
	if st.NextLink == "" {
		t.Fatal("the page before the 502 must be persisted as next_link")
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	h.advance(30 * time.Second)
	h.mustPoll()

	// The round-start deltaLink was requested exactly once, never replayed.
	roundStart := h.g.deltaTokens[tokensBefore]
	for _, tok := range h.g.deltaTokens[tokensBefore+1:] {
		if tok == roundStart {
			t.Errorf("round start %s was replayed", tok)
		}
	}
	if strings.Contains(h.log.String(), "sync state lost") {
		t.Errorf("no 410 expected:\n%s", h.log)
	}
	got := uniq(t, h.hook.deliveredIDs(t))
	if len(got) != 6 {
		t.Errorf("delivered %v, want all 6 exactly once", got)
	}
}

func TestHookDownCursorKeepsAdvancingAndPendingIsDeliveredLater(t *testing.T) {
	h := newHarness(t)
	h.mustPoll() // prime
	h.hook.setDown(true)
	var links []string
	for i := 0; i < 6; i++ {
		h.newMail(fmt.Sprintf("n%d", i))
		h.advance(10 * time.Minute) // past any retry backoff
		if err := h.poll(); err == nil {
			t.Fatal("hook down must surface as an error")
		}
		links = append(links, h.state().DeltaLink)
	}
	if strings.Contains(h.log.String(), "sync state lost") {
		t.Fatalf("hook outage must not kill the cursor:\n%s", h.log)
	}
	for i := 1; i < len(links); i++ {
		if links[i] == links[i-1] {
			t.Fatalf("cursor did not advance while the hook was down: %v", links)
		}
	}
	if p := h.state().Pending; len(p) != 6 {
		t.Fatalf("want 6 pending, got %d", len(p))
	}

	h.hook.setDown(false)
	h.advance(10 * time.Minute)
	h.mustPoll()
	if got := uniq(t, h.hook.deliveredIDs(t)); len(got) != 6 {
		t.Fatalf("pending not delivered after hook recovery: %v", got)
	}
	if p := h.state().Pending; len(p) != 0 {
		t.Errorf("pending must be empty after delivery, got %d", len(p))
	}
	if h.hook.count() != 6 {
		t.Errorf("want 6 POSTs (one per message), got %d", h.hook.count())
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 6 {
		t.Errorf("second tick after recovery: %d POSTs, want 6", h.hook.count())
	}
}

func TestPendingIsRetriedWithBackoffNotEveryPoll(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.hook.setDown(true)
	h.newMail("n1")
	_ = h.poll() // attempt 1 fails → next in 30s
	gets := h.g.msgGets
	h.advance(10 * time.Second)
	_ = h.poll()
	if h.g.msgGets != gets {
		t.Error("a pending message must not be retried before its backoff")
	}
	h.advance(30 * time.Second)
	_ = h.poll()
	if h.g.msgGets != gets+1 {
		t.Error("a pending message must be retried after its backoff")
	}
}

func TestPendingIsDroppedByAgeOnlyNotByAttempts(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.hook.setDown(true)
	h.newMail("n1")
	for i := 0; i < 120; i++ { // 20 h of polling every 10 min: ~120 failed attempts
		h.advance(10 * time.Minute)
		_ = h.poll()
	}
	st := h.state()
	if len(st.Pending) != 1 || st.Pending[0].Attempts < 100 {
		t.Fatalf("after 20 h of hook outage the message must still be pending (attempts are not a limit), got %+v", st.Pending)
	}
	if strings.Contains(h.log.String(), "dropping") {
		t.Fatalf("nothing may be dropped before 24 h:\n%s", h.log)
	}
	h.advance(4*time.Hour + 15*time.Minute) // found at +10 min → now > 24 h since found
	_ = h.poll()
	if p := h.state().Pending; len(p) != 0 {
		t.Fatalf("pending older than 24 h must be dropped, got %+v", p)
	}
	if !strings.Contains(h.log.String(), "dropping undelivered message #") || !strings.Contains(h.log.String(), "max age 24h0m0s") {
		t.Errorf("drop must be logged clearly:\n%s", h.log)
	}
	h.hook.setDown(false)
	h.advance(time.Minute)
	h.mustPoll()
	if h.hook.count() != 0 {
		t.Error("a dropped message must not be delivered later")
	}
}

func TestSyncStateLostAtSteadyStateRePrimesAndCatchesUpCapped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"410 SyncStateNotFound", 410, "SyncStateNotFound"},
		{"400 syncStateNotFound", 400, "syncStateNotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.g.evictSt, h.g.evictCd = tc.status, tc.code
			h.seed("old", 30)
			h.mustPoll() // prime at T0
			h.advance(time.Minute)
			h.newMail("delivered1")
			h.mustPoll()
			if h.hook.count() != 1 {
				t.Fatalf("setup: want 1 POST, got %d", h.hook.count())
			}

			// 15 mails arrive, then the cursor dies before we poll again.
			for i := 0; i < 15; i++ {
				h.advance(time.Minute)
				h.newMail(fmt.Sprintf("gap%02d", i))
			}
			h.g.evictAll()
			h.advance(time.Minute)
			h.mustPoll()

			if !strings.Contains(h.log.String(), "sync state lost") {
				t.Fatalf("expected the loss to be logged:\n%s", h.log)
			}
			if h.hook.count() != 11 {
				t.Fatalf("want 10 catch-up POSTs (one message each, none from the prime), got %d", h.hook.count()-1)
			}
			got := h.hook.deliveredIDs(t)[1:]
			if len(got) != 10 {
				t.Fatalf("catch-up must be capped at 10, got %d: %v", len(got), got)
			}
			for i, id := range got { // the newest 10, oldest first
				if want := fmt.Sprintf("gap%02d", i+5); id != want {
					t.Errorf("catch-up[%d] = %s, want %s", i, id, want)
				}
			}
			uniq(t, h.hook.deliveredIDs(t))
			st := h.state()
			if st.CatchUp != nil || st.Priming || len(st.Pending) != 0 {
				t.Errorf("state after catch-up: %+v", st)
			}

			h.advance(30 * time.Second)
			h.mustPoll()
			if h.hook.count() != 11 {
				t.Errorf("second tick after re-prime: %d POSTs, want 11", h.hook.count())
			}
		})
	}
}

func TestCatchUpDedupesAgainstAlreadyDelivered(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.advance(time.Minute)
	h.newMail("d1")
	h.mustPoll() // delivered d1
	h.advance(time.Minute)
	h.newMail("g1")
	h.g.evictAll()
	h.mustPoll()
	got := h.hook.deliveredIDs(t)
	if len(got) != 2 || got[1] != "g1" {
		t.Errorf("catch-up must deliver only g1 (d1 already delivered), got %v", got)
	}
}

func writeV1(t *testing.T, h *harness, seen []string, mtime time.Time) []byte {
	t.Helper()
	data, _ := json.Marshal(State{DeltaLink: h.g.srv.URL + "/users/" + mbx + "/mailFolders/inbox/messages/delta?$deltatoken=999", Seen: seen})
	p := h.store.V1Path(mbx, "inbox")
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return data
}

func assertV1Untouched(t *testing.T, h *harness, data []byte, mtime time.Time) {
	t.Helper()
	p := h.store.V1Path(mbx, "inbox")
	got, err := os.ReadFile(p)
	if err != nil || string(got) != string(data) {
		t.Errorf("v1 file was modified (err=%v)", err)
	}
	info, _ := os.Stat(p)
	if !info.ModTime().Equal(mtime) {
		t.Errorf("v1 mtime changed: %v → %v", mtime, info.ModTime())
	}
}

func TestV1MigrationWithRecentMtimeAnchorsCatchUp(t *testing.T) {
	h := newHarness(t)
	h.g.add("before", h.now.Add(-5*time.Hour))
	h.g.add("seenByV1", h.now.Add(-90*time.Minute))
	h.g.add("missed1", h.now.Add(-60*time.Minute))
	h.g.add("missed2", h.now.Add(-30*time.Minute))
	v1mtime := h.now.Add(-2 * time.Hour).Truncate(time.Second)
	data := writeV1(t, h, []string{"seenByV1"}, v1mtime)

	h.mustPoll()

	got := h.hook.deliveredIDs(t)
	if len(got) != 2 || got[0] != "missed1" || got[1] != "missed2" {
		t.Errorf("migration catch-up = %v, want [missed1 missed2]", got)
	}
	if len(h.g.listQueries) != 1 || !strings.Contains(unescapeQuery(h.g.listQueries[0]), "receivedDateTime ge "+v1mtime.UTC().Format(time.RFC3339)) {
		t.Errorf("catch-up must be anchored on the v1 mtime, got %v", h.g.listQueries)
	}
	for _, tok := range h.g.deltaTokens {
		if tok == "$deltatoken=999" {
			t.Error("the v1 token (body select) must not be reused")
		}
	}
	assertV1Untouched(t, h, data, v1mtime)
	st := h.state()
	if !st.V1SeenMtime.Equal(v1mtime) {
		t.Errorf("v1_seen_mtime = %v", st.V1SeenMtime)
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 2 {
		t.Errorf("second tick after migration: %d POSTs, want 2 (one per caught-up message)", h.hook.count())
	}
	assertV1Untouched(t, h, data, v1mtime)
}

func TestV1MigrationOlderThan48hDeliversNothing(t *testing.T) {
	h := newHarness(t)
	h.seed("old", 20)
	h.g.add("recent", h.now.Add(-time.Hour))
	v1mtime := h.now.Add(-72 * time.Hour).Truncate(time.Second)
	data := writeV1(t, h, []string{"old00000"}, v1mtime)

	h.mustPoll()
	h.advance(30 * time.Second)
	h.mustPoll()

	if h.hook.count() != 0 {
		t.Errorf("a v1 cursor dead for 72h must come back \"from now\": %d POSTs", h.hook.count())
	}
	// The only catch-up is the one for mail arriving during the prime itself.
	if len(h.g.listQueries) != 1 || !strings.Contains(unescapeQuery(h.g.listQueries[0]), "receivedDateTime ge "+h.now.Add(-30*time.Second).UTC().Format(time.RFC3339)) {
		t.Errorf("catch-up must be anchored at the prime start, got %v", h.g.listQueries)
	}
	assertV1Untouched(t, h, data, v1mtime)
}

func TestRollbackDetectedByNewerV1MergesItsSeen(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.hook.setDown(true)
	h.newMail("n1")
	_ = h.poll() // n1 pending in v2
	h.hook.setDown(false)
	// v0.2.x ran meanwhile and delivered n1 → its v1 file is newer.
	writeV1(t, h, []string{"n1"}, h.now.Add(time.Minute).Truncate(time.Second))
	h.advance(10 * time.Minute)
	h.mustPoll()
	if h.hook.count() != 0 {
		t.Errorf("n1 was delivered by the rolled-back version; must not be delivered again")
	}
}

func TestCorruptV2StateRePrimesInsteadOfCrashLooping(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":   "not json at all",
		"truncated": `{"version":2,"delta_link":"https://graph.microsoft.com/v1.0/us`,
		"empty":     "",
		"version":   `{"version":7,"delta_link":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.seed("old", 3)
			_ = os.MkdirAll(h.dir, 0o700)
			if err := os.WriteFile(h.store.Path(mbx, "inbox"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			// The corrupt file's mtime anchors the catch-up; the seeded mail is older.
			_ = os.Chtimes(h.store.Path(mbx, "inbox"), h.now, h.now)
			h.mustPoll()
			st := h.state()
			if st.DeltaLink == "" || st.Priming {
				t.Errorf("corrupt state must lead to a completed re-prime, got %+v", st)
			}
			matches, _ := os.ReadDir(h.dir)
			quarantined := false
			for _, m := range matches {
				if strings.Contains(m.Name(), ".corrupt-") {
					quarantined = true
				}
			}
			if !quarantined {
				t.Error("corrupt file must be moved aside for inspection")
			}
			if h.hook.count() != 0 {
				t.Errorf("re-prime must not deliver old mail: %d POSTs", h.hook.count())
			}
		})
	}
}

func TestSeenTrimKeepsTheNewestIDs(t *testing.T) {
	h := newHarness(t)
	h.seed("m", 5000)
	h.mustPoll()
	st := h.state()
	if len(st.Seen) != seenCap {
		t.Fatalf("seen len = %d, want %d", len(st.Seen), seenCap)
	}
	if st.Seen[0] != "m03000" || st.Seen[seenCap-1] != "m04999" {
		t.Errorf("seen must keep the newest ids in append order, got first=%s last=%s", st.Seen[0], st.Seen[seenCap-1])
	}
}

func TestCappedSetKeepsNewestInAppendOrder(t *testing.T) {
	s := newCappedSet(nil, 3)
	for _, v := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		s.add(v)
	}
	got := s.slice()
	if strings.Join(got, ",") != "e,f,g" {
		t.Errorf("cappedSet = %v, want [e f g]", got)
	}
	if s.has("a") || !s.has("g") {
		t.Error("trimmed ids must leave the set")
	}
}

func TestPerMessage404IsSkippedNotRetried(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("gone")
	h.newMail("ok")
	h.g.del("gone")
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "ok" {
		t.Errorf("delivered %v, want [ok]", got)
	}
	if p := h.state().Pending; len(p) != 0 {
		t.Errorf("a 404 must not stay pending: %+v", p)
	}
	if !strings.Contains(h.log.String(), "is gone (404") {
		t.Errorf("404 skip must be logged:\n%s", h.log)
	}
}

func TestRemovedEntriesAreIgnored(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("n1")
	h.g.removed = []string{"x-removed"}
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "n1" {
		t.Errorf("delivered %v, want [n1]", got)
	}
}

func TestMovedInOldMailBelowTheFloorIsNotDelivered(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.add("old-moved-in", h.now.Add(-30*24*time.Hour))
	h.newMail("fresh")
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "fresh" {
		t.Errorf("delivered %v, want [fresh]", got)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.retryAft = "120"
	h.g.failNext = []int{429}
	if err := h.poll(); err == nil {
		t.Fatal("429 must surface")
	}
	reqs := h.g.deltaReqs
	h.advance(30 * time.Second)
	_ = h.poll()
	if h.g.deltaReqs != reqs {
		t.Error("poll inside Retry-After must not call Graph")
	}
	h.advance(2 * time.Minute)
	h.mustPoll()
	if h.g.deltaReqs != reqs+1 {
		t.Error("poll after Retry-After must call Graph again")
	}
	if st := h.state(); st.ErrorClass != "" {
		t.Errorf("success must clear the error class, got %q", st.ErrorClass)
	}
}

func TestErrorClassAndCounterArePersisted(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.failNext = []int{503, 503}
	_ = h.poll()
	_ = h.poll()
	st := h.state()
	if st.ErrorClass != "transient" || st.ErrorCount != 2 || st.LastErrorAt.IsZero() {
		t.Errorf("error class/counter = %q/%d", st.ErrorClass, st.ErrorCount)
	}
}

func TestLogsNeverContainLinksTokensOrBodies(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.restart()
	h.mustPoll()
	for i := 0; i < 5; i++ {
		h.newMail(fmt.Sprintf("n%d", i))
	}
	h.hook.setDown(true)
	h.g.onDelta = func() {
		if h.g.deltaReqs == 3 {
			h.g.failNext = []int{502}
		}
	}
	err1 := h.poll()
	h.g.onDelta = nil
	h.g.evictAll()
	h.advance(time.Minute)
	err2 := h.poll()
	h.g.srv.Close() // transport errors carry the full request URL
	h.advance(time.Minute)
	err3 := h.poll()

	logger := NewLogger(h.log)
	for _, err := range []error{err1, err2, err3} {
		if err != nil {
			logger.Errorf(mbx, "%v", err)
		}
	}
	out := h.log.String()
	if !strings.Contains(out, "sync state lost") || err3 == nil {
		t.Fatalf("scenario did not produce the expected errors:\n%s", out)
	}
	for _, bad := range []string{"deltatoken=", "skiptoken=", "HOOKQUERY", "hook-SECRET", "graph-bearer-SECRET", "BODY-SECRET"} {
		if strings.Contains(out, bad) {
			t.Errorf("log leaks %q:\n%s", bad, out)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.Contains(line, "[ts=20") {
			t.Errorf("log line without ISO timestamp: %q", line)
		}
	}
}

func TestPrimeOfFiftyThousandIDsStaysStreamed(t *testing.T) {
	if testing.Short() {
		t.Skip("large prime")
	}
	h := newHarness(t)
	h.seed("m", 50000)
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	var peak uint64
	h.g.onDelta = func() {
		if h.g.deltaReqs%25 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak {
				peak = ms.HeapAlloc
			}
		}
	}
	h.mustPoll()
	h.g.onDelta = nil

	if h.g.deltaReqs != 250 || h.g.msgGets != 0 || h.hook.count() != 0 {
		t.Fatalf("prime: %d delta pages, %d message GETs, %d POSTs", h.g.deltaReqs, h.g.msgGets, h.hook.count())
	}
	if peak > base && peak-base > 3<<19 { // 1.5 MB; an unbounded seen set measures ~3.2 MB here
		t.Errorf("live heap grew by %d KB during the prime; ids must not accumulate", (peak-base)>>10)
	}
	if st := h.state(); len(st.Seen) != seenCap || st.Priming {
		t.Errorf("after prime: seen=%d priming=%v", len(st.Seen), st.Priming)
	}
	t.Logf("live heap growth during 50k-id prime: %d KB", (peak-base)>>10)
}

func TestLargePrimeIsSlicedAcrossPolls(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 10
	h.opts.MaxPagesPoll = 3
	h.restart()
	h.seed("m", 75) // 8 pages
	h.mustPoll()
	if st := h.state(); !st.Priming || st.NextLink == "" {
		t.Fatalf("prime must pause with a saved next_link, got %+v", st)
	}
	h.mustPoll()
	h.mustPoll()
	st := h.state()
	if st.Priming || st.DeltaLink == "" {
		t.Fatalf("prime must finish on the third poll, got %+v", st)
	}
	if h.hook.count() != 0 {
		t.Error("prime slices must not deliver")
	}
}

func TestLoggerRateLimitsIdenticalErrors(t *testing.T) {
	var buf strings.Builder
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	l := &Logger{W: &buf, Now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		l.Errorf(mbx, "boom")
		now = now.Add(5 * time.Second)
	}
	now = now.Add(time.Minute)
	l.Errorf(mbx, "boom")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines (first + one after the window), got %d:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "watch a@x.com: boom [ts=2026-11-26T12:00:00Z]") {
		t.Errorf("line format: %q", lines[0])
	}
	if !strings.Contains(lines[1], "repeated 4 more times") {
		t.Errorf("suppressed count missing: %q", lines[1])
	}
}

func TestRedact(t *testing.T) {
	in := `graph https://graph.microsoft.com/v1.0/users/a@x.com/mailFolders/inbox/messages/delta?$deltatoken=ABC&x=1: 410 ` +
		`Get "http://127.0.0.1:1/users/a/messages/delta?$skiptoken=XYZ": dial tcp; Authorization: Bearer eyJ.abc.def; $deltatoken=LOOSE`
	out := Redact(in)
	for _, bad := range []string{"ABC", "XYZ", "eyJ", "LOOSE"} {
		if strings.Contains(out, bad) {
			t.Errorf("Redact leaks %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, "/messages/delta?#") {
		t.Errorf("Redact must keep the path and a hash: %s", out)
	}
}

// Documents the v0.2.6 failure mode on the same Graph model: the v1 syncer keeps
// replaying the old deltaLink while the hook fails, each replay creates a new
// generation, and after two replays the link is gone for good.
func TestV1SyncerHitsTheEvictionLoopTheV2EngineAvoids(t *testing.T) {
	g := newFakeGraph(t, mbx)
	c := graph.New(&config.Config{AllowedMailboxes: []string{mbx}}, fakeTokens{})
	c.BaseURL = g.srv.URL
	body, err := c.MailboxDeltaPage(context.Background(), mbx, "mailFolders/inbox/messages/delta?changeType=created&$select=id", 0)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := parseDeltaIDs(body)
	store := NewFileStore(t.TempDir())
	_ = store.Put(mbx, "inbox", State{DeltaLink: p.DeltaLink})
	hook := newFakeHook(t)
	hook.setDown(true)
	s := &Syncer{Graph: c, Hook: NewHookClient(hook.srv.URL, ""), Store: store, Opts: Options{Folder: "inbox"}}
	g.add("n1", time.Now())

	var last error
	for i := 0; i < 4; i++ {
		last = s.SyncOnce(context.Background(), mbx)
	}
	if !graph.IsSyncStateLost(last) {
		t.Fatalf("expected the v1 syncer to end in SyncStateNotFound, got %v", last)
	}
}
