package watch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Review round 2 (PR #15 at d3199d4): age guard, bounded fetch failures,
// poison messages, dropped-mail counters, surviving mutants.

func TestValidCursorAfterLongStopDoesNotReplayOldMail(t *testing.T) {
	h := newHarness(t)
	h.mustPoll() // primed; the watcher then stops for 5 days, the cursor stays valid
	t0 := h.now
	for i := 0; i < 40; i++ { // received 2-5 days into the outage... all older than 24 h at restart
		h.g.add(fmt.Sprintf("old%02d", i), t0.Add(time.Duration(i)*time.Hour))
	}
	h.now = t0.Add(5 * 24 * time.Hour)
	for i := 0; i < 10; i++ { // received within the last 24 h
		h.g.add(fmt.Sprintf("new%02d", i), h.now.Add(-time.Duration(20-i)*time.Hour))
	}
	h.restart()
	h.mustPoll()
	h.advance(30 * time.Second)
	h.mustPoll()

	got := h.hook.deliveredIDs(t)
	if len(got) != 10 {
		t.Fatalf("want only the 10 recent messages, got %d: %v", len(got), got)
	}
	for _, id := range got {
		if !strings.HasPrefix(id, "new") {
			t.Errorf("mail older than 24 h was delivered: %s", id)
		}
	}
	st := h.state()
	if st.DroppedByReason[dropTooOld] != 40 || st.DroppedTotal != 40 || len(st.Pending) != 0 {
		t.Errorf("dropped=%d by_reason=%v pending=%d", st.DroppedTotal, st.DroppedByReason, len(st.Pending))
	}
}

func TestPrimeStalledLongerThan48hComesBackFromNow(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.opts.MaxPagesPoll = 1
	h.restart()
	h.seed("old", 4) // 2 pages + final
	h.mustPoll()     // first slice; then the prime stalls (watcher down)
	h.advance(75 * time.Hour)
	h.g.add("recent", h.now.Add(-time.Hour))
	for i := 0; i < 5 && h.state().Priming; i++ {
		h.mustPoll()
	}
	h.mustPoll()
	if h.hook.count() != 0 || len(h.g.listQueries) != 0 {
		t.Errorf("a prime that completed 75 h after it started must not catch up: %d POSTs, %d list queries",
			h.hook.count(), len(h.g.listQueries))
	}
	if st := h.state(); st.DroppedByReason[dropTooOld] != 1 {
		t.Errorf("mail from before the completion floor must be dropped as too_old: %v", st.DroppedByReason)
	}
	if !strings.Contains(h.log.String(), "at prime completion; resuming from now") {
		t.Errorf("the 48 h rule at completion must be logged:\n%s", h.log)
	}
}

// pendingHarness returns a harness with n undelivered messages queued.
func pendingHarness(t *testing.T, n int) *harness {
	h := newHarness(t)
	h.mustPoll()
	h.hook.setDown(true)
	for i := 0; i < n; i++ {
		h.newMail(fmt.Sprintf("p%03d", i))
	}
	_ = h.poll()
	h.hook.setDown(false)
	if got := len(h.state().Pending); got != n {
		t.Fatalf("setup: %d pending, want %d", got, n)
	}
	h.advance(10 * time.Minute) // past the queue backoff
	return h
}

func TestFetch5xxStopsThePollsDeliveries(t *testing.T) {
	h := pendingHarness(t, 300)
	h.g.msgFail = []int{503}
	gets := h.g.msgGets
	_ = h.poll()
	if n := h.g.msgGets - gets; n != 1 {
		t.Errorf("after a 503 on GET no further message is fetched this poll, got %d GETs", n)
	}
}

func TestDeliveryLoopRespectsThePollDeadlineEvenWithoutPosts(t *testing.T) {
	h := pendingHarness(t, 300)
	for i := 0; i < 300; i++ {
		h.g.del(fmt.Sprintf("p%03d", i)) // every GET is a 404: no POST ever happens
	}
	h.g.onMsgGet = func() { h.advance(time.Minute) } // slow Graph
	gets := h.g.msgGets
	_ = h.poll()
	h.g.onMsgGet = nil
	if n := h.g.msgGets - gets; n > 4 {
		t.Errorf("a 3 min poll deadline must bound slow fetches, got %d GETs", n)
	}
}

func TestFetchRetryAfterBacksOffTheMailbox(t *testing.T) {
	h := pendingHarness(t, 1)
	h.g.retryAft = "120"
	h.g.msgFail = []int{503}
	_ = h.poll()
	reqs, gets := h.g.deltaReqs, h.g.msgGets
	h.advance(60 * time.Second)
	_ = h.poll()
	if h.g.deltaReqs != reqs || h.g.msgGets != gets {
		t.Error("a Retry-After on the message fetch must back off the whole mailbox")
	}
}

func TestFailedFetchBacksOffOnlyThatMessage(t *testing.T) {
	h := pendingHarness(t, 2)
	h.g.msgFail = []int{500}
	_ = h.poll() // p000 fetch fails → p000 waits 30 s, poll stops
	h.advance(10 * time.Second)
	gets := h.g.msgGets
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "p001" {
		t.Fatalf("p001 may go while p000 backs off, got %v", got)
	}
	if h.g.msgGets-gets != 1 {
		t.Errorf("p000 must not be fetched again inside its backoff (%d GETs)", h.g.msgGets-gets)
	}
	h.advance(60 * time.Second) // 2nd failed attempt (1st was the hook outage) → 60 s
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 2 || got[1] != "p000" {
		t.Errorf("p000 must be delivered after its backoff, got %v", got)
	}
}

func TestHookSuccessResetsTheQueueBackoff(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("a")
	h.hook.setDown(true)
	_ = h.poll()
	h.advance(time.Minute)
	_ = h.poll() // 2 failures → next backoff would be 2 min
	h.hook.setDown(false)
	h.advance(5 * time.Minute)
	h.mustPoll()
	h.newMail("b")
	h.hook.setDown(true)
	_ = h.poll()
	if d := h.state().DeliverNotBefore.Sub(h.now); d != 30*time.Second {
		t.Errorf("after a success the backoff must start at 30 s again, got %s", d)
	}
}

func TestPoisonMessageIsDroppedAfterFiveRejections(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("bad")
	h.newMail("good")
	h.hook.reject = "bad"
	for i := 0; i < 4; i++ {
		_ = h.poll()
		h.advance(10 * time.Minute)
	}
	if h.hook.count() != 0 {
		t.Fatalf("before the 5th rejection the queue stays blocked (order), got %v", h.hook.deliveredIDs(t))
	}
	h.mustPoll() // 5th rejection → dropped; good goes out in the same poll
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "good" {
		t.Errorf("delivered %v, want [good]", got)
	}
	st := h.state()
	if st.DroppedByReason[dropRejected] != 1 || len(st.Pending) != 0 {
		t.Errorf("by_reason=%v pending=%v", st.DroppedByReason, st.Pending)
	}
	if !strings.Contains(h.log.String(), "the hook rejected it 5 times") {
		t.Errorf("rejection drop must be logged:\n%s", h.log)
	}
}

func TestHook5xxNeverDropsAsRejected(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("a")
	h.hook.setDown(true) // 503
	for i := 0; i < 10; i++ {
		_ = h.poll()
		h.advance(10 * time.Minute)
	}
	if st := h.state(); len(st.Pending) != 1 || st.DroppedTotal != 0 {
		t.Errorf("gateway down must keep blocking, not drop: %+v", st)
	}
}

func TestDropCountersPerReasonAndPoll(t *testing.T) {
	h := newHarness(t)
	h.opts.PendingCap = 2
	h.restart()
	h.mustPoll()
	h.hook.setDown(true)
	for i := 0; i < 4; i++ {
		h.newMail(fmt.Sprintf("c%d", i))
	}
	_ = h.poll() // 2 dropped by the cap
	st := h.state()
	if st.DroppedByReason[dropPendingCap] != 2 || st.LastDroppedCount != 2 || st.LastDroppedAt.IsZero() {
		t.Fatalf("pending_cap: %+v", st)
	}
	h.hook.setDown(false)
	h.g.del("c2")
	h.g.del("c3")
	h.advance(10 * time.Minute)
	h.mustPoll() // c2 and c3 → 404: two separate drops in one poll
	st = h.state()
	if st.DroppedByReason[dropFetch404] != 2 || st.LastDroppedCount != 2 || st.DroppedTotal != 4 {
		t.Errorf("fetch_404: %+v", st)
	}
	if !st.LastDroppedAt.Equal(h.now) {
		t.Errorf("last_dropped_at = %v, want %v", st.LastDroppedAt, h.now)
	}

	// expired_24h
	h.hook.setDown(true)
	h.newMail("e1")
	_ = h.poll()
	h.advance(25 * time.Hour)
	_ = h.poll()
	st = h.state()
	if st.DroppedByReason[dropExpired] != 1 || st.DroppedTotal != 5 || st.LastDroppedCount != 1 {
		t.Errorf("expired_24h: total=%d last=%d %+v", st.DroppedTotal, st.LastDroppedCount, st.DroppedByReason)
	}
}

func TestNoCatchUpWhilePrimeIsStillRunning(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.opts.MaxPagesPoll = 1
	h.restart()
	h.seed("old", 6)
	writeV1(t, h, nil, h.now.Add(-time.Hour).Truncate(time.Second))
	h.mustPoll()
	if !h.state().Priming || len(h.g.listQueries) != 0 {
		t.Errorf("catch-up must wait for the prime to finish (list queries %d)", len(h.g.listQueries))
	}
}

func TestCatchUpQueuedMailIsNotDeliveredAgainByTheDelta(t *testing.T) {
	h := newHarness(t)
	h.g.onList = func() { h.g.add("late", h.now) } // arrives after the prime's deltaLink
	h.mustPoll()
	h.g.onList = nil
	h.advance(30 * time.Second)
	h.mustPoll() // the delta now reports "late" as created
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "late" {
		t.Errorf("delivered %v, want [late] once", got)
	}
}
