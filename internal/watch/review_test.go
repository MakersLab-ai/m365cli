package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Review round 1 (PR #15): lost-state variants, backstop, one message per
// POST, and tests that kill the mutants an independent review found alive.

func TestAnyLostStateAnswerOnAStoredLinkRePrimes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
		body   string
	}{
		{"410 resyncRequired", 410, "resyncRequired", ""},
		{"400 SyncStateInvalid", 400, "SyncStateInvalid", ""},
		{"400 resyncRequired", 400, "resyncRequired", ""},
		{"410 empty body", 410, "", " "},
		{"410 non-JSON body", 410, "", "<html>Gone</html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.g.evictSt, h.g.evictCd, h.g.evictBody = tc.status, tc.code, tc.body
			h.mustPoll()
			h.g.evictAll()
			h.advance(time.Minute)
			h.mustPoll()
			if !strings.Contains(h.log.String(), "delta sync state lost") {
				t.Fatalf("expected a re-prime:\n%s", h.log)
			}
			if st := h.state(); st.Priming || st.DeltaLink == "" {
				t.Errorf("re-prime must complete in the same poll: %+v", st)
			}
		})
	}
}

func TestRepeated4xxOnTheSameStoredLinkTriggersRePrime(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.tokenFail, h.g.tokenCode = 400, "BadRequest"
	for i := 1; i < stuckLimit; i++ {
		h.advance(30 * time.Second)
		if err := h.poll(); err == nil {
			t.Fatalf("poll %d: 400 must surface", i)
		}
		if st := h.state(); st.StuckCount != i || st.Priming {
			t.Fatalf("poll %d: stuck_count=%d priming=%v", i, st.StuckCount, st.Priming)
		}
	}
	h.advance(30 * time.Second)
	h.mustPoll() // 10th 400 → backstop → fresh prime (initial requests are not failed)
	h.g.tokenFail = 0
	if !strings.Contains(h.log.String(), "consecutive HTTP 400 answers on the same stored delta link") {
		t.Errorf("backstop must be logged:\n%s", h.log)
	}
	if st := h.state(); st.Priming || st.StuckCount != 0 || len(h.g.initialQuery) != 2 {
		t.Errorf("backstop must re-prime: %+v, initial requests %d", st, len(h.g.initialQuery))
	}
}

func TestAuthAndThrottlingErrorsDoNotTriggerTheBackstop(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		h := newHarness(t)
		h.mustPoll()
		h.g.tokenFail, h.g.tokenCode = status, "Nope"
		for i := 0; i < stuckLimit+3; i++ {
			h.advance(11 * time.Minute) // past any Retry-After
			_ = h.poll()
		}
		if len(h.g.initialQuery) != 1 {
			t.Errorf("HTTP %d must never re-prime, got %d initial requests", status, len(h.g.initialQuery))
		}
	}
}

func TestRePrimeIsAttemptedOncePerPoll(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.alwaysGone = true
	reqs := h.g.deltaReqs
	if err := h.poll(); err == nil {
		t.Fatal("a 410 on the fresh prime must surface")
	}
	if got := h.g.deltaReqs - reqs; got != 2 {
		t.Errorf("want stored link + one re-prime attempt = 2 requests, got %d", got)
	}
}

func TestBacklogIsDeliveredOneMessagePerPostOldestFirst(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.hook.setDown(true)
	var want []string
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("n%02d", i)
		want = append(want, id)
		h.newMail(id)
		if i%20 == 19 {
			h.advance(time.Minute)
			_ = h.poll()
		}
	}
	h.hook.setDown(false)
	polls := 0
	for h.hook.count() < 60 && polls < 10 {
		h.advance(10 * time.Minute)
		h.mustPoll()
		polls++
	}
	if polls != 3 {
		t.Errorf("60 messages at 20 POSTs per poll = 3 polls, took %d", polls)
	}
	for i, p := range h.hook.posts {
		var doc struct {
			Messages []struct{ ID string } `json:"messages"`
		}
		_ = jsonUnmarshal(p, &doc)
		if len(doc.Messages) != 1 {
			t.Fatalf("POST %d carries %d messages, want exactly 1", i, len(doc.Messages))
		}
	}
	got := h.hook.deliveredIDs(t)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("delivery order:\n got %v\nwant %v", got, want)
	}
}

func TestHookFailureKeepsOrderAcrossPolls(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("a")
	h.newMail("b")
	h.newMail("c")
	h.hook.setDown(true)
	_ = h.poll() // a fails, b/c not tried
	h.hook.setDown(false)
	h.advance(10 * time.Second) // still inside the queue backoff
	h.mustPoll()
	if h.hook.count() != 0 {
		t.Fatalf("nothing may overtake the failed head of the queue during its backoff, got %v", h.hook.deliveredIDs(t))
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	if got := strings.Join(h.hook.deliveredIDs(t), ","); got != "a,b,c" {
		t.Errorf("order = %s, want a,b,c", got)
	}
}

func TestMailArrivingDuringAFirstRunPrimeIsDelivered(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.restart()
	h.seed("old", 6)
	h.g.liveRounds = true
	h.g.onDelta = func() {
		if h.g.deltaReqs == 2 {
			h.g.add("during-prime", h.now) // lands on a later page of the prime
		}
	}
	h.mustPoll()
	h.g.onDelta = nil
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "during-prime" {
		t.Errorf("mail that arrived during the prime must be delivered by the catch-up, got %v", got)
	}
	h.advance(30 * time.Second)
	h.mustPoll()
	if h.hook.count() != 1 {
		t.Errorf("second tick: %d POSTs, want 1", h.hook.count())
	}
}

func TestCatchUpOffMeansNoCatchUp(t *testing.T) {
	h := newHarness(t)
	h.opts.CatchUpMax = -1
	h.restart()
	h.mustPoll()
	h.g.add("g1", h.now)
	h.g.evictAll()
	h.mustPoll()
	if h.hook.count() != 0 || len(h.g.listQueries) != 0 {
		t.Errorf("catch-up off: %d POSTs, %d list queries", h.hook.count(), len(h.g.listQueries))
	}
}

func TestRetryAfterIsCappedAtTenMinutes(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.retryAft = "86400"
	h.g.failNext = []int{503}
	_ = h.poll()
	reqs := h.g.deltaReqs
	h.advance(9 * time.Minute)
	_ = h.poll()
	if h.g.deltaReqs != reqs {
		t.Fatal("must still back off inside the cap")
	}
	h.advance(2 * time.Minute)
	h.mustPoll()
	if h.g.deltaReqs != reqs+1 {
		t.Error("a day-long Retry-After must be capped at 10 min")
	}
}

func TestFetchErrorLogsDoNotContainMessageIDs(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("AAMkSECRETMESSAGEID")
	h.g.msgFail = []int{500}
	err := h.poll()
	if err == nil {
		t.Fatal("fetch 500 must surface")
	}
	h.eng.Log.Errorf(mbx, "%v", err)
	if strings.Contains(h.log.String(), "AAMkSECRETMESSAGEID") {
		t.Errorf("message id leaked:\n%s", h.log)
	}
}

func TestThrottledFetchStopsThePollsDeliveries(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("a")
	h.newMail("b")
	h.g.msgFail = []int{429}
	gets := h.g.msgGets
	_ = h.poll()
	if h.g.msgGets-gets != 1 {
		t.Errorf("after a 429 no further message is fetched this poll, got %d GETs", h.g.msgGets-gets)
	}
}

func TestPendingCapDropsOldest(t *testing.T) {
	h := newHarness(t)
	h.opts.PendingCap = 3
	h.restart()
	h.mustPoll()
	h.hook.setDown(true)
	for i := 0; i < 5; i++ {
		h.newMail(fmt.Sprintf("n%d", i))
	}
	_ = h.poll()
	var ids []string
	for _, p := range h.state().Pending {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "n2,n3,n4" {
		t.Errorf("pending = %v, want the newest 3", ids)
	}
	if !strings.Contains(h.log.String(), "over cap 3") {
		t.Errorf("cap drop must be logged:\n%s", h.log)
	}
}

func TestPollDeadlineStopsPaging(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.opts.PollDeadline = 150 * time.Second
	h.restart()
	h.seed("m", 20) // 10 pages
	h.g.onDelta = func() { h.advance(time.Minute) }
	h.mustPoll()
	h.g.onDelta = nil
	st := h.state()
	if !st.Priming || st.NextLink == "" || h.g.deltaReqs != 3 {
		t.Errorf("deadline must pause the prime after 3 pages: reqs=%d %+v", h.g.deltaReqs, st)
	}
}

func TestCatchUpWindowClampsAnOldAnchorTo24h(t *testing.T) {
	h := newHarness(t)
	v1mtime := h.now.Add(-30 * time.Hour).Truncate(time.Second)
	writeV1(t, h, nil, v1mtime)
	h.mustPoll()
	want := "receivedDateTime ge " + h.now.Add(-24*time.Hour).UTC().Format(time.RFC3339)
	if len(h.g.listQueries) != 1 || !strings.Contains(unescapeQuery(h.g.listQueries[0]), want) {
		t.Errorf("catch-up since must be clamped to now-24h (%s), got %v", want, h.g.listQueries)
	}
}

func TestCatchUpSkipsWhatTheDeltaAlreadyQueuedMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.advance(time.Minute)
	h.g.evictAll()
	h.g.listFail = 2 // catch-up fails on this poll and the next
	_ = h.poll()
	h.advance(time.Minute)
	h.newMail("x") // found by the delta, delivered, while the catch-up is still due
	_ = h.poll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "x" {
		t.Fatalf("setup: delivered %v", got)
	}
	h.advance(time.Minute)
	h.mustPoll() // catch-up succeeds; its list contains x
	if got := h.hook.deliveredIDs(t); len(got) != 1 {
		t.Errorf("x was delivered twice: %v", got)
	}
}

func TestCatchUpDuplicateListEntriesQueueOnce(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.advance(time.Minute)
	h.newMail("g1")
	h.g.evictAll()
	h.g.listDup = true
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 {
		t.Errorf("delivered %v, want g1 once", got)
	}
}

func TestRestartedPrimeKeepsItsCatchUpAnchor(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.restart()
	h.seed("old", 6)
	v1mtime := h.now.Add(-2 * time.Hour).Truncate(time.Second)
	writeV1(t, h, nil, v1mtime)
	h.g.onDelta = func() {
		if h.g.deltaReqs == 2 {
			h.g.evictAll() // the prime's own next link dies
		}
	}
	h.mustPoll()
	h.g.onDelta = nil
	if len(h.g.initialQuery) != 2 {
		t.Fatalf("setup: expected a restarted prime, got %d initial requests", len(h.g.initialQuery))
	}
	want := "receivedDateTime ge " + v1mtime.UTC().Format(time.RFC3339)
	if len(h.g.listQueries) != 1 || !strings.Contains(unescapeQuery(h.g.listQueries[0]), want) {
		t.Errorf("restarted prime must keep the v1 anchor (%s), got %v", want, h.g.listQueries)
	}
}

func TestRollbackMergedSeenSuppressesWhatV1Delivered(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("y") // v2 is stopped; v0.2.x runs and delivers y
	writeV1(t, h, []string{"y"}, h.now.Add(time.Minute).Truncate(time.Second))
	h.advance(5 * time.Minute)
	h.restart()
	h.mustPoll() // v2's own (still valid) cursor reports y as created
	if h.hook.count() != 0 {
		t.Errorf("y was delivered by the rolled-back version; got %v", h.hook.deliveredIDs(t))
	}
}

func TestCorruptStateAnchorsCatchUpOnItsMtime(t *testing.T) {
	h := newHarness(t)
	h.g.add("before", h.now.Add(-3*time.Hour))
	h.g.add("after", h.now.Add(-1*time.Hour))
	_ = os.MkdirAll(h.dir, 0o700)
	p := h.store.Path(mbx, "inbox")
	_ = os.WriteFile(p, []byte("{"), 0o600)
	_ = os.Chtimes(p, h.now.Add(-2*time.Hour), h.now.Add(-2*time.Hour))
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "after" {
		t.Errorf("delivered %v, want [after]", got)
	}
}

func TestSecondWatcherOnTheSameMailboxIsLockedOut(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	other := *h.eng
	other.locks, other.notBefore = nil, nil
	err := other.Poll(t.Context(), mbx)
	if err == nil || !strings.Contains(err.Error(), "another watcher holds") {
		t.Fatalf("second watcher must be locked out, got %v", err)
	}
	h.eng.Close()
	if err := other.Poll(t.Context(), mbx); err != nil {
		t.Errorf("after the first watcher exits the lock must be free: %v", err)
	}
	other.Close()
}

func TestHookPayloadGoldenBytes(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.add("n1", time.Date(2026, 11, 26, 11, 59, 0, 0, time.UTC))
	h.mustPoll()
	const golden = `{"source":"m365","account":"a@x.com","messages":[{"id":"n1","threadId":"conv-n1","from":"sender@ext.com","to":"a@x.com","subject":"subject n1","date":"2026-11-26T11:59:00Z","snippet":"preview n1","body":"` + "\\u003cp\\u003eBODY-SECRET n1\\u003c/p\\u003e" + `","labels":["INBOX","UNREAD"]}]}`
	if got := string(h.hook.posts[0]); got != golden {
		t.Errorf("payload bytes changed:\n got %s\nwant %s", got, golden)
	}
}

func TestRedactDoesNotEatTheSeparator(t *testing.T) {
	for in, want := range map[string]string{
		"graph /users/a/messages/delta?#3fa2c1d0: 410 Gone": "graph /users/a/messages/delta?#3fa2c1d0: 410 Gone",
		"get https://h/p?a=1: 410 Gone":                     "get https://h/p?#" + shortHash("a=1") + ": 410 Gone",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
