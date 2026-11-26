package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Review round 3 (PR #15 at 3080ea1).

func TestPrimeWhoseLinksKeepDyingKeepsItsStartAndCountsRestarts(t *testing.T) {
	h := newHarness(t)
	h.opts.PageSize = 2
	h.restart()
	h.seed("m", 6)
	h.g.tokenFail, h.g.tokenCode = 410, "SyncStateNotFound" // every next link is gone
	start := h.now
	for i := 1; i <= 6; i++ {
		_ = h.poll()
		st := h.state()
		if !st.Priming || !st.PrimeStartedAt.Equal(start) {
			t.Fatalf("poll %d: prime_started_at must stay %v, got %+v", i, start, st.PrimeStartedAt)
		}
		if st.PrimeRestarts != i {
			t.Fatalf("poll %d: prime_restarts = %d, want %d", i, st.PrimeRestarts, i)
		}
		h.advance(time.Minute)
	}
	if !strings.Contains(h.log.String(), "prime restarted") {
		t.Errorf("restarts must be logged:\n%s", h.log)
	}
	h.g.tokenFail = 0
	h.mustPoll()
	if st := h.state(); st.Priming || st.PrimeRestarts != 0 || !st.PrimeStartedAt.IsZero() {
		t.Errorf("a completed prime resets the restart counter: %+v", st)
	}
}

func TestFetch401And403StopThePoll(t *testing.T) {
	for _, s := range []int{401, 403} {
		h := pendingHarness(t, 300)
		h.g.msgFail = []int{s}
		gets := h.g.msgGets
		_ = h.poll()
		if n := h.g.msgGets - gets; n != 1 {
			t.Errorf("fetch %d: want 1 GET this poll, got %d", s, n)
		}
	}
}

func TestFetchTransportErrorStopsThePoll(t *testing.T) {
	h := pendingHarness(t, 5)
	h.g.msgFail = []int{-1, -1} // net/http retries an idempotent GET once on a dropped reused connection
	gets := h.g.gets()
	if err := h.poll(); err == nil {
		t.Fatal("transport error must surface")
	}
	if n := h.g.gets() - gets; n > 2 || h.hook.count() != 0 {
		t.Errorf("a dropped connection must end the poll's deliveries, got %d GETs, %d POSTs", n, h.hook.count())
	}
}

func TestFetch400SkipsTheMessageAndDropsItAfterThree(t *testing.T) {
	h := pendingHarness(t, 2)
	h.g.msgFail = []int{400}
	h.mustPollIgnoringErr()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "p001" {
		t.Fatalf("a 400 skips only that message, got %v", got)
	}
	for i := 0; i < 2; i++ {
		h.advance(10 * time.Minute)
		h.g.msgFail = []int{400}
		h.mustPollIgnoringErr()
	}
	st := h.state()
	if st.DroppedByReason[dropFetch400] != 1 || len(st.Pending) != 0 {
		t.Errorf("after 3 × 400: by_reason=%v pending=%v", st.DroppedByReason, st.Pending)
	}
}

func TestManyFetch400sEndThePoll(t *testing.T) {
	h := pendingHarness(t, 300)
	for i := 0; i < 20; i++ {
		h.g.msgFail = append(h.g.msgFail, 400)
	}
	gets := h.g.msgGets
	_ = h.poll()
	if n := h.g.msgGets - gets; n != maxBadRequestsPoll {
		t.Errorf("want %d GETs before the poll stops, got %d", maxBadRequestsPoll, n)
	}
}

func TestLongErrorLinesAreTruncated(t *testing.T) {
	out := Redact(strings.Repeat("x", 40000))
	if len(out) > maxLogLine+20 {
		t.Errorf("log line of %d bytes, want ≤ %d", len(out), maxLogLine+20)
	}
}

func TestValidCursorStaleFor72hDeliversOnlyTheNewestTen(t *testing.T) {
	h := newHarness(t)
	h.mustPoll() // last_success_at = t0; then 72 h without a poll, cursor still valid
	h.now = h.now.Add(72 * time.Hour)
	for i := 0; i < 700; i++ {
		h.g.add(fmt.Sprintf("r%03d", i), h.now.Add(-20*time.Hour+time.Duration(i)*time.Minute))
	}
	h.restart()
	h.mustPoll()
	h.advance(30 * time.Second)
	h.mustPoll()
	got := h.hook.deliveredIDs(t)
	if len(got) != 10 {
		t.Fatalf("want at most 10 POSTs, got %d", len(got))
	}
	if got[0] != "r690" || got[9] != "r699" {
		t.Errorf("want the newest 10 (r690..r699), got %v", got)
	}
	st := h.state()
	if st.DroppedByReason[dropTooOld] != 690 || st.StaleRound || st.LastRoundCompletedAt.IsZero() {
		t.Errorf("too_old=%d stale=%v %+v", st.DroppedByReason[dropTooOld], st.StaleRound, st.DroppedByReason)
	}
	if !strings.Contains(h.log.String(), "this round delivers at most the newest 10") {
		t.Errorf("stale round must be logged:\n%s", h.log)
	}
}

func TestStaleRoundCatchUpGivingUpCountsEverything(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.now = h.now.Add(72 * time.Hour)
	for i := 0; i < 30; i++ {
		h.g.add(fmt.Sprintf("r%02d", i), h.now.Add(-time.Hour))
	}
	h.g.listFail = catchUpMaxAttempts
	for i := 0; i < catchUpMaxAttempts; i++ {
		_ = h.poll()
		h.advance(time.Minute)
	}
	st := h.state()
	if st.CatchUp != nil || st.DroppedByReason[dropTooOld] != 30 || h.hook.count() != 0 {
		t.Errorf("catch-up gave up: too_old=%d catch_up=%v posts=%d", st.DroppedByReason[dropTooOld], st.CatchUp, h.hook.count())
	}
}

func TestHookAuthConfigAndThrottlingAnswersNeverDropAsRejected(t *testing.T) {
	for _, s := range []int{401, 403, 404, 429} {
		h := newHarness(t)
		h.mustPoll()
		h.newMail("a")
		h.hook.status = s
		for i := 0; i < 8; i++ {
			_ = h.poll()
			h.advance(10 * time.Minute)
		}
		if st := h.state(); len(st.Pending) != 1 || st.DroppedTotal != 0 {
			t.Errorf("hook %d must keep blocking, not drop: pending=%d dropped=%d", s, len(st.Pending), st.DroppedTotal)
		}
	}
}

func TestBackstopNeedsThreeFailures(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.g.tokenFail, h.g.tokenCode = 400, "BadRequest"
	_ = h.poll()
	h.advance(31 * time.Minute) // watcher was down in between
	_ = h.poll()
	if len(h.g.initialQuery) != 1 {
		t.Errorf("two failures 31 min apart must not re-prime (%d initial requests)", len(h.g.initialQuery))
	}
}

func TestQueueIsFreeRightAfterAPoisonDrop(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	h.newMail("bad")
	h.hook.reject = "bad"
	for i := 0; i < 5; i++ {
		if i > 0 {
			h.advance(10 * time.Minute) // past the queue backoff
		}
		_ = h.poll()
	}
	if st := h.state(); st.DroppedByReason[dropRejected] != 1 {
		t.Fatalf("setup: %+v", st.DroppedByReason)
	}
	h.newMail("next")
	h.advance(10 * time.Second)
	h.mustPoll()
	if got := h.hook.deliveredIDs(t); len(got) != 1 || got[0] != "next" {
		t.Errorf("the queue must not stay backed off after the poison drop, got %v", got)
	}
}

func TestLastRoundCompletedAtIsSetBySteadyRoundsOnly(t *testing.T) {
	h := newHarness(t)
	h.mustPoll()
	if st := h.state(); !st.LastRoundCompletedAt.IsZero() {
		t.Fatalf("a prime is not a steady round: %v", st.LastRoundCompletedAt)
	}
	h.advance(time.Minute)
	h.mustPoll()
	if st := h.state(); !st.LastRoundCompletedAt.Equal(h.now) {
		t.Errorf("last_round_completed_at = %v, want %v", st.LastRoundCompletedAt, h.now)
	}
}

func TestCorruptStateCarriesCountersAndCountsLostPending(t *testing.T) {
	h := newHarness(t)
	_ = os.MkdirAll(h.dir, 0o700)
	old := map[string]any{
		"version": 7, "dropped_total": 5, "dropped_by_reason": map[string]int{"rejected": 5},
		"pending": []map[string]any{{"id": "a"}, {"id": "b"}, {"id": "c"}},
	}
	b, _ := json.Marshal(old)
	_ = os.WriteFile(h.store.Path(mbx, "inbox"), b, 0o600)
	h.mustPoll()
	st := h.state()
	if st.DroppedTotal != 8 || st.DroppedByReason[dropRejected] != 5 || st.DroppedByReason[dropStateReset] != 3 {
		t.Errorf("carried counters: total=%d %v", st.DroppedTotal, st.DroppedByReason)
	}

	h2 := newHarness(t)
	_ = os.MkdirAll(h2.dir, 0o700)
	_ = os.WriteFile(h2.store.Path(mbx, "inbox"), []byte("{garbage"), 0o600)
	h2.mustPoll()
	if st := h2.state(); st.DroppedByReason[dropStateReset] != 1 {
		t.Errorf("an unreadable state must still show a state_reset: %v", st.DroppedByReason)
	}
}

func (h *harness) mustPollIgnoringErr() { _ = h.poll() }
