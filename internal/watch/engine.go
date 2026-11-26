package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// GraphReader is the Graph surface the v2 engine needs: GET-only, mailbox
// scoped (the graph client enforces allowed_mailboxes on both calls).
type GraphReader interface {
	MailboxDeltaPage(ctx context.Context, mailbox, urlOrSuffix string, maxPageSize int) ([]byte, error)
	GetForMailbox(ctx context.Context, mailbox, suffix string) ([]byte, error)
}

// EngineOptions tunes the v2 engine. Zero values take the defaults below.
type EngineOptions struct {
	Folder      string
	IncludeBody bool
	MaxBytes    int

	PageSize      int           // Prefer: odata.maxpagesize (default 200)
	MaxPagesPoll  int           // delta pages per mailbox per poll (default 500 = 100k ids)
	PollDeadline  time.Duration // pagination time per mailbox per poll (default 3m)
	CatchUpMax    int           // catch-up count cap after a prime (0 = default 10; <0 = off)
	CatchUpWindow time.Duration // catch-up never reaches further back than this, and no mail received earlier is ever delivered (default 24h)
	CatchUpMaxAge time.Duration // anchor older than this → catch-up only from the prime start (default 48h)
	PendingMaxAge time.Duration // undelivered ids are dropped after this (default 24h)
	PendingCap    int           // max pending ids; oldest dropped beyond (default 500)
	MaxPostsPoll  int           // max hook POSTs (one message each) per mailbox per poll (default 20)
	FloorMargin   time.Duration // floor = prime start minus this (default 10m)
}

func (o EngineOptions) withDefaults() EngineOptions {
	def := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	defD := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if o.Folder == "" {
		o.Folder = "inbox"
	}
	def(&o.PageSize, 200)
	def(&o.MaxPagesPoll, 500)
	defD(&o.PollDeadline, 3*time.Minute)
	def(&o.CatchUpMax, 10)
	defD(&o.CatchUpWindow, 24*time.Hour)
	defD(&o.CatchUpMaxAge, 48*time.Hour)
	defD(&o.PendingMaxAge, 24*time.Hour)
	def(&o.PendingCap, 500)
	def(&o.MaxPostsPoll, 20)
	defD(&o.FloorMargin, 10*time.Minute)
	return o
}

// Engine is the v2 watcher (Graph backend): IDs-only delta with the cursor
// persisted after every page, per-message fetch for delivery, a pending list
// for undelivered ids, and automatic IDs-only re-prime + bounded catch-up when
// Graph loses the sync state.
type Engine struct {
	Graph GraphReader
	Hook  HookPoster
	Store *V2Store
	Opts  EngineOptions
	Log   *Logger
	Now   func() time.Time

	notBefore map[string]time.Time // mailbox → Retry-After horizon
	locks     map[string]*os.File  // mailbox → held state lock
	pollDrops int                  // messages dropped in the current Poll
}

// Close releases the per-mailbox state locks.
func (e *Engine) Close() {
	for k, f := range e.locks {
		f.Close()
		delete(e.locks, k)
	}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

const (
	catchUpMaxAttempts = 5
	// Backstop for lost-state answers Graph may phrase differently from the
	// documented codes: a stored link that has answered non-transient 4xx for
	// at least stuckWindow, over at least stuckMinFailures polls, is treated
	// as lost. A short 404 burst does not throw away a healthy cursor.
	stuckWindow      = 30 * time.Minute
	stuckMinFailures = 3
	// maxRejects hook 4xx answers for one message (poison message) → dropped.
	maxRejects = 5
	// maxRetryAfter caps a server-requested backoff.
	maxRetryAfter = 10 * time.Minute
)

// messageSelect is the production field list for the per-message fetch; the
// hook payload is built from exactly these fields (see BuildHookPayload).
func (e *Engine) messageSelect() string {
	if e.Opts.IncludeBody {
		return messageSelect
	}
	return "id,conversationId,subject,bodyPreview,receivedDateTime,isRead,from,toRecipients"
}

// initialDelta is the first request of a (re-)prime. changeType=created is a
// custom query option of message delta and, like $select, only goes on the
// initial request; Graph carries both inside the returned links.
func (e *Engine) initialDelta() string {
	return "mailFolders/" + url.PathEscape(e.Opts.Folder) + "/messages/delta?changeType=created&$select=id"
}

// Poll runs one watch cycle for mailbox: advance the cursor page by page,
// run a pending catch-up, then deliver due pending messages.
func (e *Engine) Poll(ctx context.Context, mailbox string) error {
	e.Opts = e.Opts.withDefaults()
	if nb, ok := e.notBefore[mailbox]; ok && e.now().Before(nb) {
		return nil // Graph asked us to back off (Retry-After)
	}
	if err := e.lock(mailbox); err != nil {
		return err
	}
	st, err := e.load(mailbox)
	if err != nil {
		return err
	}
	e.pollDrops = 0
	deadline := e.now().Add(e.Opts.PollDeadline)
	seen := newCappedSet(st.Seen, seenCap)
	syncErr := e.syncDelta(ctx, mailbox, st, seen, deadline)
	var catchErr error
	if !st.Priming && st.CatchUp != nil {
		catchErr = e.catchUp(ctx, mailbox, st, seen)
	}
	deliverErr := e.deliver(ctx, mailbox, st, deadline)
	return errors.Join(syncErr, catchErr, deliverErr)
}

func (e *Engine) lock(mailbox string) error {
	if _, ok := e.locks[mailbox]; ok {
		return nil
	}
	f, err := e.Store.Lock(mailbox, e.Opts.Folder)
	if err != nil {
		return err
	}
	if e.locks == nil {
		e.locks = map[string]*os.File{}
	}
	e.locks[mailbox] = f
	return nil
}

// load returns the state to work on, handling first run, v1 migration,
// rollback detection and corrupt files. The returned state always has a
// cursor or Priming=true.
func (e *Engine) load(mailbox string) (*StateV2, error) {
	folder := e.Opts.Folder
	st, status, mtime, err := e.Store.Load(mailbox, folder)
	if err != nil {
		return nil, fmt.Errorf("read watch state: %w", err)
	}
	v1, v1mtime, hasV1 := e.Store.LoadV1(mailbox, folder)

	switch status {
	case LoadOK:
		if hasV1 && v1mtime.After(st.V1SeenMtime) {
			// v0.2.x ran after us (rollback, then forward again): merge what it
			// saw so its deliveries are not repeated.
			s := newCappedSet(st.Seen, seenCap)
			for _, id := range v1.Seen {
				s.add(id)
			}
			st.Seen = s.slice()
			st.Pending = filterPending(st.Pending, newCappedSet(v1.Seen, 0))
			st.V1SeenMtime = v1mtime
			e.Log.Infof("%s: v1 state is newer than v2 (rollback detected); merged its seen ids", mailbox)
			return &st, e.Store.Save(mailbox, folder, &st)
		}
		return &st, nil

	case LoadCorrupt:
		dst, qerr := e.Store.Quarantine(mailbox, folder, e.now())
		if qerr != nil {
			return nil, fmt.Errorf("corrupt v2 state could not be moved aside: %w", qerr)
		}
		e.Log.Errorf(mailbox, "v2 state unreadable, moved aside to %s; re-priming", dst)
		fresh := &StateV2{}
		if hasV1 {
			fresh.Seen, fresh.V1SeenMtime = append([]string(nil), v1.Seen...), v1mtime
		}
		e.startPrime(mailbox, fresh, mtime, "corrupt v2 state")
		return fresh, e.Store.Save(mailbox, folder, fresh)

	default: // LoadMissing
		fresh := &StateV2{}
		if hasV1 {
			fresh.Seen, fresh.V1SeenMtime = append([]string(nil), v1.Seen...), v1mtime
			e.Log.Infof("%s: migrating from v1 state (v1 file left untouched); IDs-only prime", mailbox)
			e.startPrime(mailbox, fresh, v1mtime, "v1 migration")
		} else {
			e.Log.Infof("%s: no watch state; IDs-only prime (existing mail is not delivered)", mailbox)
			e.startPrime(mailbox, fresh, time.Time{}, "first run")
		}
		return fresh, e.Store.Save(mailbox, folder, fresh)
	}
}

// startPrime resets the cursor for an IDs-only prime and schedules the
// catch-up that runs once the prime has its deltaLink. anchor is the last time
// the cursor was known to be in sync; without one (first run), or when it is
// older than CatchUpMaxAge, the catch-up is anchored at the prime start so
// mail that arrives while the prime pages is still delivered.
func (e *Engine) startPrime(mailbox string, st *StateV2, anchor time.Time, why string) {
	now := e.now()
	st.DeltaLink, st.NextLink = "", ""
	st.Priming, st.PrimeStartedAt = true, now
	st.StuckLink, st.StuckSince, st.StuckCount = "", time.Time{}, 0
	if st.CatchUp != nil || e.Opts.CatchUpMax < 0 {
		return // a restarted prime keeps its original catch-up; or catch-up is off
	}
	if !anchor.IsZero() && now.Sub(anchor) > e.Opts.CatchUpMaxAge {
		e.Log.Infof("%s: %s: last good state %s is older than %s; resuming from now (no older mail delivered)",
			mailbox, why, anchor.UTC().Format(time.RFC3339), e.Opts.CatchUpMaxAge)
		anchor = time.Time{}
	}
	if anchor.IsZero() {
		anchor = now
	}
	st.CatchUp = &CatchUp{Anchor: anchor, Exclude: append([]string(nil), st.Seen...)}
}

// syncDelta pages the delta cursor, persisting after every page.
func (e *Engine) syncDelta(ctx context.Context, mailbox string, st *StateV2, seen *cappedSet, deadline time.Time) error {
	folder := e.Opts.Folder
	reprimed := false
	var exclude *cappedSet
	if st.CatchUp != nil {
		exclude = newCappedSet(st.CatchUp.Exclude, 0)
	}
	for pages := 0; ; pages++ {
		if pages >= e.Opts.MaxPagesPoll || e.now().After(deadline) {
			if st.Priming {
				e.Log.Infof("%s: prime paused after %d pages this poll; resumes from the saved page next poll", mailbox, pages)
			}
			return nil
		}
		link := st.NextLink
		if link == "" {
			link = st.DeltaLink
		}
		stored := link != ""
		if !stored {
			link = e.initialDelta()
		}
		body, err := e.Graph.MailboxDeltaPage(ctx, mailbox, link, e.Opts.PageSize)
		if err != nil {
			lost := syncStateLost(err)
			if !lost && stored && stuck4xx(err) {
				now := e.now()
				if h := shortHash(link); st.StuckLink == h {
					st.StuckCount++
				} else {
					st.StuckLink, st.StuckSince, st.StuckCount = h, now, 1
				}
				if st.StuckCount >= stuckMinFailures && now.Sub(st.StuckSince) >= stuckWindow {
					e.Log.Errorf(mailbox, "stored delta link has answered HTTP %d for %s (%d polls); treating the cursor as lost",
						httpStatus(err), now.Sub(st.StuckSince).Round(time.Second), st.StuckCount)
					lost = true
				}
			}
			if lost && !reprimed {
				reprimed = true
				e.Log.Errorf(mailbox, "delta sync state lost (%v); IDs-only re-prime", err)
				e.startPrime(mailbox, st, st.LastSuccessAt, "sync state lost")
				if st.CatchUp != nil {
					exclude = newCappedSet(st.CatchUp.Exclude, 0)
				}
				e.noteError(mailbox, st, err)
				if serr := e.Store.Save(mailbox, folder, st); serr != nil {
					return serr
				}
				continue
			}
			e.noteError(mailbox, st, err)
			_ = e.Store.Save(mailbox, folder, st) // error counters only; cursor unchanged
			return err
		}
		page, err := parseDeltaIDs(body)
		if err != nil {
			e.noteError(mailbox, st, err)
			return fmt.Errorf("parse delta page: %w", err)
		}
		if page.NextLink == "" && page.DeltaLink == "" {
			// Without a link the next poll would have nothing to resume from.
			// Keep the previous page's link and retry.
			err := fmt.Errorf("delta response has neither @odata.nextLink nor @odata.deltaLink")
			e.noteError(mailbox, st, err)
			return err
		}
		now := e.now()
		for _, id := range page.Created {
			if st.Priming {
				seen.add(id)
				continue
			}
			if exclude != nil {
				exclude.add(id) // queued here → the catch-up must not queue it again
			}
			if seen.has(id) {
				continue
			}
			seen.add(id)
			st.Pending = append(st.Pending, PendingMsg{ID: id, FoundAt: now, Floor: st.Floor})
		}
		st.Pending = e.capPending(mailbox, st)
		if page.NextLink != "" {
			st.NextLink = page.NextLink
		} else {
			st.DeltaLink, st.NextLink = page.DeltaLink, ""
			st.LastSuccessAt = now
			if st.Priming {
				st.Priming, st.PrimedAt = false, now
				// The 48 h rule is evaluated at completion too: a prime that
				// stalled for days resumes "from now", so its floor moves to
				// the completion time.
				base := st.PrimeStartedAt
				if now.Sub(base) > e.Opts.CatchUpMaxAge {
					base = now
				}
				st.Floor = base.Add(-e.Opts.FloorMargin)
				st.PrimeStartedAt = time.Time{}
				e.Log.Infof("%s: prime complete; watching from now", mailbox)
			} else {
				st.LastRoundCompletedAt = now
			}
		}
		st.ErrorClass, st.ErrorCount = "", 0
		st.StuckLink, st.StuckSince, st.StuckCount = "", time.Time{}, 0
		st.Seen = seen.slice()
		if st.CatchUp != nil && exclude != nil {
			st.CatchUp.Exclude = exclude.slice()
		}
		if err := e.Store.Save(mailbox, folder, st); err != nil {
			return fmt.Errorf("save watch state: %w", err)
		}
		if page.NextLink == "" {
			return nil
		}
	}
}

// catchUp lists mail received since the catch-up anchor (bounded by count and
// window) and queues what was not known before the cursor was lost.
func (e *Engine) catchUp(ctx context.Context, mailbox string, st *StateV2, seen *cappedSet) error {
	cu := st.CatchUp
	now := e.now()
	if age := now.Sub(cu.Anchor); age > e.Opts.CatchUpMaxAge {
		// Evaluated when the prime completes: a prime that stalled for days
		// (anchored at its own start) comes back "from now" as well.
		e.Log.Infof("%s: catch-up anchor %s is older than %s at prime completion; resuming from now, no catch-up",
			mailbox, cu.Anchor.UTC().Format(time.RFC3339), e.Opts.CatchUpMaxAge)
		st.CatchUp = nil
		return e.Store.Save(mailbox, e.Opts.Folder, st)
	}
	since := cu.Anchor
	if floor := now.Add(-e.Opts.CatchUpWindow); since.Before(floor) {
		since = floor
	}
	suffix := "mailFolders/" + url.PathEscape(e.Opts.Folder) + "/messages" +
		"?$filter=" + url.QueryEscape("receivedDateTime ge "+since.UTC().Format(time.RFC3339)) +
		"&$orderby=" + url.QueryEscape("receivedDateTime desc") +
		"&$top=" + strconv.Itoa(e.Opts.CatchUpMax) + "&$select=id,receivedDateTime"
	body, err := e.Graph.GetForMailbox(ctx, mailbox, suffix)
	if err == nil {
		var list struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		if err = json.Unmarshal(body, &list); err == nil {
			exclude := newCappedSet(cu.Exclude, 0)
			queued := 0
			for i := len(list.Value) - 1; i >= 0 && i >= len(list.Value)-e.Opts.CatchUpMax; i-- { // oldest first
				id := list.Value[i].ID
				if id == "" || exclude.has(id) {
					continue
				}
				exclude.add(id) // a duplicate list entry must not queue twice
				seen.add(id)
				st.Pending = append(st.Pending, PendingMsg{ID: id, FoundAt: now, CatchUp: true})
				queued++
			}
			e.Log.Infof("%s: catch-up since %s: %d of %d recent message(s) queued (cap %d)",
				mailbox, since.UTC().Format(time.RFC3339), queued, len(list.Value), e.Opts.CatchUpMax)
			st.CatchUp = nil
			st.Seen = seen.slice()
			return e.Store.Save(mailbox, e.Opts.Folder, st)
		}
	}
	cu.Attempts++
	if cu.Attempts >= catchUpMaxAttempts {
		e.Log.Errorf(mailbox, "catch-up failed %d times, giving up (no catch-up delivered): %v", cu.Attempts, err)
		st.CatchUp = nil
	}
	if serr := e.Store.Save(mailbox, e.Opts.Folder, st); serr != nil {
		return serr
	}
	return fmt.Errorf("catch-up: %w", err)
}

// deliver POSTs pending messages oldest first, exactly one message per POST
// (the fleet hook template renders only messages[0]), until the queue is
// empty, MaxPostsPoll is reached or the poll deadline passes. The cursor is
// never held back by delivery.
//
//   - Hook 5xx / connection error (gateway down): stop, back off the queue,
//     keep the order.
//   - Hook 4xx other than 401/403/404/429 (this message is rejected): retried
//     with the queue backoff, dropped as "rejected" after maxRejects.
//   - Message fetch 5xx / 429 / transport error: back off that message, stop
//     this poll's deliveries; 404: dropped as "fetch_404".
//   - Mail received before now-CatchUpWindow (or before the floor) is dropped
//     as "too_old": a valid cursor after a long stop does not replay days of
//     mail. Pending longer than PendingMaxAge: dropped as "expired_24h".
func (e *Engine) deliver(ctx context.Context, mailbox string, st *StateV2, deadline time.Time) error {
	if len(st.Pending) == 0 {
		return nil
	}
	folder := e.Opts.Folder
	now := e.now()
	changed := false

	// Drop what has waited too long (age only; retries are not counted).
	keep := st.Pending[:0:0]
	for _, p := range st.Pending {
		if now.Sub(p.FoundAt) > e.Opts.PendingMaxAge {
			e.Log.Errorf(mailbox, "dropping undelivered message #%s: pending since %s (%s > max age %s, %d failed attempt(s))",
				shortHash(p.ID), p.FoundAt.UTC().Format(time.RFC3339), now.Sub(p.FoundAt).Round(time.Second),
				e.Opts.PendingMaxAge, p.Attempts)
			e.recordDrop(st, dropExpired, 1)
			changed = true
			continue
		}
		keep = append(keep, p)
	}
	st.Pending = keep

	var errs []error
	posts := 0
	tooOld := now.Add(-e.Opts.CatchUpWindow)
	remove := func(i int) { st.Pending = append(st.Pending[:i], st.Pending[i+1:]...) }
	if now.Before(st.DeliverNotBefore) {
		goto done // hook backoff
	}
	for i := 0; i < len(st.Pending); {
		if posts >= e.Opts.MaxPostsPoll || e.now().After(deadline) {
			break
		}
		p := &st.Pending[i]
		if now.Before(p.NextAttemptAt) {
			i++ // this message's fetch is backing off; the rest may proceed
			continue
		}
		body, err := e.Graph.GetForMailbox(ctx, mailbox,
			"messages/"+url.PathEscape(p.ID)+"?$select="+e.messageSelect())
		if err != nil {
			changed = true
			if httpStatus(err) == 404 {
				e.Log.Infof("%s: message #%s is gone (404: moved or deleted); skipped", mailbox, shortHash(p.ID))
				e.recordDrop(st, dropFetch404, 1)
				remove(i)
				continue
			}
			p.Attempts++
			p.NextAttemptAt = now.Add(backoffFor(p.Attempts))
			errs = append(errs, fmt.Errorf("fetch message #%s: %s", shortHash(p.ID), hideID(err, p.ID)))
			if ra := retryAfter(err); ra > 0 {
				e.backoff(mailbox, now.Add(ra))
			}
			if s := httpStatus(err); s == 429 || s >= 500 || s == 0 {
				break // Graph busy or unreachable: don't hammer it with the rest
			}
			i++
			continue
		}
		var probe struct {
			Received time.Time `json:"receivedDateTime"`
		}
		if json.Unmarshal(body, &probe) == nil && !probe.Received.IsZero() {
			limit, why := tooOld, "older than the delivery window "+e.Opts.CatchUpWindow.String()
			if p.Floor.After(limit) {
				limit, why = p.Floor, "before the watch floor (moved/restored mail)"
			}
			if probe.Received.Before(limit) {
				e.Log.Infof("%s: message #%s was received %s, %s; not delivered",
					mailbox, shortHash(p.ID), probe.Received.UTC().Format(time.RFC3339), why)
				e.recordDrop(st, dropTooOld, 1)
				remove(i)
				changed = true
				continue
			}
		}
		payload, err := BuildHookPayload(mailbox, []json.RawMessage{body}, nil,
			Options{Folder: folder, IncludeBody: e.Opts.IncludeBody, MaxBytes: e.Opts.MaxBytes})
		if err == nil {
			err = e.Hook.Post(ctx, payload)
		}
		changed = true
		if err != nil {
			p.Attempts++
			st.DeliverFailures++
			st.DeliverNotBefore = now.Add(backoffFor(st.DeliverFailures))
			if rejectedByHook(err) {
				p.Rejected++
				if p.Rejected >= maxRejects {
					e.Log.Errorf(mailbox, "dropping message #%s: the hook rejected it %d times (%s)",
						shortHash(p.ID), p.Rejected, Redact(err.Error()))
					e.recordDrop(st, dropRejected, 1)
					remove(i)
					st.DeliverFailures, st.DeliverNotBefore = 0, time.Time{}
					continue // the poison message no longer blocks newer mail
				}
			}
			errs = append(errs, fmt.Errorf("deliver message #%s: %w (%d pending; cursor already advanced)",
				shortHash(p.ID), err, len(st.Pending)))
			break // keep the order: nothing newer goes out before this one
		}
		remove(i)
		st.DeliverFailures, st.DeliverNotBefore = 0, time.Time{}
		posts++
	}
done:
	if changed {
		if err := e.Store.Save(mailbox, folder, st); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// rejectedByHook: the hook answered 4xx for this message, other than auth or
// config problems (401/403/404) and throttling (429), which concern every
// message alike and must keep blocking the queue.
func rejectedByHook(err error) bool {
	var h interface{ HookStatus() int }
	if !errors.As(err, &h) {
		return false
	}
	s := h.HookStatus()
	return s >= 400 && s < 500 && s != 401 && s != 403 && s != 404 && s != 429
}

// backoffFor is 30s doubling per failure, capped at 5m.
func backoffFor(failures int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < failures && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// hideID renders err with the message id replaced by its hash: Graph errors
// carry the request path, and message ids do not belong in logs.
func hideID(err error, id string) string {
	s := err.Error()
	for _, form := range []string{url.PathEscape(id), id} {
		s = strings.ReplaceAll(s, form, "#"+shortHash(id))
	}
	return s
}

// stuck4xx: a client error that is neither auth (401/403) nor throttling (429).
func stuck4xx(err error) bool {
	s := httpStatus(err)
	return s >= 400 && s < 500 && s != 401 && s != 403 && s != 429
}

func (e *Engine) capPending(mailbox string, st *StateV2) []PendingMsg {
	p := st.Pending
	if over := len(p) - e.Opts.PendingCap; over > 0 {
		e.Log.Errorf(mailbox, "pending list over cap %d: dropping %d oldest undelivered message(s)", e.Opts.PendingCap, over)
		e.recordDrop(st, dropPendingCap, over)
		return append([]PendingMsg(nil), p[over:]...)
	}
	return p
}

// Drop reasons (keys of dropped_by_reason).
const (
	dropExpired    = "expired_24h" // pending longer than PendingMaxAge
	dropTooOld     = "too_old"     // received before now-CatchUpWindow or before the floor
	dropRejected   = "rejected"    // hook answered 4xx maxRejects times
	dropPendingCap = "pending_cap" // pending list over PendingCap
	dropFetch404   = "fetch_404"   // gone before delivery (moved/deleted)
)

// recordDrop updates the dropped-mail counters in st.
func (e *Engine) recordDrop(st *StateV2, reason string, n int) {
	if n <= 0 {
		return
	}
	e.pollDrops += n
	st.DroppedTotal += n
	if st.DroppedByReason == nil {
		st.DroppedByReason = map[string]int{}
	}
	st.DroppedByReason[reason] += n
	st.LastDroppedAt = e.now()
	st.LastDroppedCount = e.pollDrops
}

func (e *Engine) backoff(mailbox string, until time.Time) {
	if max := e.now().Add(maxRetryAfter); until.After(max) {
		until = max
	}
	if e.notBefore == nil {
		e.notBefore = map[string]time.Time{}
	}
	e.notBefore[mailbox] = until
}

// noteError classifies err into the state's error class/counter and honours a
// Retry-After for the mailbox.
func (e *Engine) noteError(mailbox string, st *StateV2, err error) {
	class := "other"
	switch s := httpStatus(err); {
	case syncStateLost(err):
		class = "sync_state_lost"
	case s == 429:
		class = "throttled"
	case s == 401 || s == 403:
		class = "auth"
	case s >= 500 || s == 0:
		class = "transient"
	}
	if st.ErrorClass == class {
		st.ErrorCount++
	} else {
		st.ErrorClass, st.ErrorCount = class, 1
	}
	st.LastErrorAt = e.now()
	if ra := retryAfter(err); ra > 0 {
		e.backoff(mailbox, e.now().Add(ra))
	}
}

func filterPending(p []PendingMsg, drop *cappedSet) []PendingMsg {
	var out []PendingMsg
	for _, x := range p {
		if !drop.has(x.ID) {
			out = append(out, x)
		}
	}
	return out
}

// Typed-error probes. The graph client's *graph.APIError implements these; the
// watch package stays transport-neutral by asking for behaviour, not a type.
func syncStateLost(err error) bool {
	var s interface{ SyncStateLost() bool }
	return errors.As(err, &s) && s.SyncStateLost()
}

func httpStatus(err error) int {
	var s interface{ HTTPStatus() int }
	if errors.As(err, &s) {
		return s.HTTPStatus()
	}
	return 0
}

func retryAfter(err error) time.Duration {
	var s interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &s) {
		return s.RetryAfterDuration()
	}
	return 0
}

// idPage is one delta page reduced to what the engine keeps: ids only.
type idPage struct {
	Created   []string
	NextLink  string
	DeltaLink string
}

// parseDeltaIDs decodes a delta page keeping only ids; @removed entries are
// ignored. Any other properties (should the server ignore $select) are
// discarded during decoding, so nothing larger than an id is retained.
func parseDeltaIDs(body []byte) (idPage, error) {
	var raw struct {
		NextLink  string `json:"@odata.nextLink"`
		DeltaLink string `json:"@odata.deltaLink"`
		Value     []struct {
			ID      string          `json:"id"`
			Removed json.RawMessage `json:"@removed"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return idPage{}, err
	}
	p := idPage{NextLink: raw.NextLink, DeltaLink: raw.DeltaLink}
	for _, v := range raw.Value {
		if len(v.Removed) > 0 || v.ID == "" {
			continue
		}
		p.Created = append(p.Created, v.ID)
	}
	return p, nil
}

// cappedSet is an insertion-ordered id set that keeps only the newest cap ids
// (cap 0 = unbounded). It bounds memory during a prime of a large mailbox.
type cappedSet struct {
	order []string
	m     map[string]bool
	cap   int
}

func newCappedSet(init []string, cap int) *cappedSet {
	s := &cappedSet{m: map[string]bool{}, cap: cap}
	for _, v := range init {
		s.add(v)
	}
	return s
}

func (s *cappedSet) has(v string) bool { return s.m[v] }

func (s *cappedSet) add(v string) {
	if s.m[v] {
		return
	}
	s.m[v] = true
	s.order = append(s.order, v)
	if s.cap > 0 && len(s.order) >= 2*s.cap {
		s.trim()
	}
}

func (s *cappedSet) trim() {
	if s.cap <= 0 || len(s.order) <= s.cap {
		return
	}
	drop := len(s.order) - s.cap
	for _, old := range s.order[:drop] {
		delete(s.m, old)
	}
	s.order = append([]string(nil), s.order[drop:]...)
}

func (s *cappedSet) slice() []string {
	s.trim()
	return s.order
}
