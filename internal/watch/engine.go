package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
	CatchUpMax    int           // catch-up count cap after a re-prime (default 10; <0 disables)
	CatchUpWindow time.Duration // catch-up never reaches further back than this (default 24h)
	CatchUpMaxAge time.Duration // anchor older than this → no catch-up at all (default 48h)
	PendingMaxAge time.Duration // undelivered ids are dropped after this (default 24h)
	PendingMaxTry int           // ... or after this many failed attempts (default 100)
	PendingCap    int           // max pending ids; oldest dropped beyond (default 500)
	BatchMax      int           // max messages per hook POST (default 50)
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
	def(&o.PendingMaxTry, 100)
	def(&o.PendingCap, 500)
	def(&o.BatchMax, 50)
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
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

const catchUpMaxAttempts = 5

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
	st, err := e.load(mailbox)
	if err != nil {
		return err
	}
	seen := newCappedSet(st.Seen, seenCap)
	syncErr := e.syncDelta(ctx, mailbox, st, seen)
	var catchErr error
	if !st.Priming && st.CatchUp != nil {
		catchErr = e.catchUp(ctx, mailbox, st, seen)
	}
	deliverErr := e.deliver(ctx, mailbox, st)
	return errors.Join(syncErr, catchErr, deliverErr)
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

// startPrime resets the cursor for an IDs-only prime. anchor is the last time
// the cursor was known to be in sync; when it is set and not older than
// CatchUpMaxAge a catch-up is scheduled for after the prime.
func (e *Engine) startPrime(mailbox string, st *StateV2, anchor time.Time, why string) {
	now := e.now()
	st.DeltaLink, st.NextLink = "", ""
	st.Priming, st.PrimeStartedAt = true, now
	if st.CatchUp != nil {
		return // a prime restarted before its catch-up ran keeps the original catch-up
	}
	switch {
	case anchor.IsZero():
	case e.Opts.CatchUpMax < 0:
	case now.Sub(anchor) > e.Opts.CatchUpMaxAge:
		e.Log.Infof("%s: %s: last good state %s is older than %s; resuming from now, no catch-up",
			mailbox, why, anchor.UTC().Format(time.RFC3339), e.Opts.CatchUpMaxAge)
	default:
		st.CatchUp = &CatchUp{Anchor: anchor, Exclude: append([]string(nil), st.Seen...)}
	}
}

// syncDelta pages the delta cursor, persisting after every page.
func (e *Engine) syncDelta(ctx context.Context, mailbox string, st *StateV2, seen *cappedSet) error {
	folder := e.Opts.Folder
	start := e.now()
	reprimed := false
	var exclude *cappedSet
	if st.CatchUp != nil {
		exclude = newCappedSet(st.CatchUp.Exclude, 0)
	}
	for pages := 0; ; pages++ {
		if pages >= e.Opts.MaxPagesPoll || e.now().Sub(start) > e.Opts.PollDeadline {
			if st.Priming {
				e.Log.Infof("%s: prime paused after %d pages this poll; resumes from the saved page next poll", mailbox, pages)
			}
			return nil
		}
		link := st.NextLink
		if link == "" {
			link = st.DeltaLink
		}
		if link == "" {
			link = e.initialDelta()
		}
		body, err := e.Graph.MailboxDeltaPage(ctx, mailbox, link, e.Opts.PageSize)
		if err != nil {
			if syncStateLost(err) && !reprimed {
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
		st.Pending = e.capPending(mailbox, st.Pending)
		if page.NextLink != "" {
			st.NextLink = page.NextLink
		} else {
			st.DeltaLink, st.NextLink = page.DeltaLink, ""
			st.LastSuccessAt = now
			if st.Priming {
				st.Priming, st.PrimedAt = false, now
				st.Floor = st.PrimeStartedAt.Add(-e.Opts.FloorMargin)
				st.PrimeStartedAt = time.Time{}
				e.Log.Infof("%s: prime complete; watching from now", mailbox)
			} else {
				st.LastRoundCompletedAt = now
			}
		}
		st.ErrorClass, st.ErrorCount = "", 0
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
				if id == "" || exclude.has(id) || hasPending(st.Pending, id) {
					continue
				}
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

// deliver fetches and POSTs due pending messages (one POST, at most BatchMax
// messages). The cursor is never held back by delivery: failures only
// reschedule the pending ids.
func (e *Engine) deliver(ctx context.Context, mailbox string, st *StateV2) error {
	if len(st.Pending) == 0 {
		return nil
	}
	now := e.now()
	folder := e.Opts.Folder
	changed := false

	// Drop what has waited too long.
	keep := st.Pending[:0:0]
	for _, p := range st.Pending {
		if now.Sub(p.FoundAt) > e.Opts.PendingMaxAge || p.Attempts >= e.Opts.PendingMaxTry {
			e.Log.Errorf(mailbox, "dropping undelivered message #%s after %d attempt(s) over %s (limits: %d attempts, %s)",
				shortHash(p.ID), p.Attempts, now.Sub(p.FoundAt).Round(time.Second), e.Opts.PendingMaxTry, e.Opts.PendingMaxAge)
			changed = true
			continue
		}
		keep = append(keep, p)
	}
	st.Pending = keep

	var batch []int // indexes into st.Pending
	for i, p := range st.Pending {
		if len(batch) >= e.Opts.BatchMax {
			break
		}
		if p.NextAttemptAt.IsZero() || !now.Before(p.NextAttemptAt) {
			batch = append(batch, i)
		}
	}
	if len(batch) == 0 {
		if changed {
			return e.Store.Save(mailbox, folder, st)
		}
		return nil
	}

	var raws []json.RawMessage
	var sent []int
	gone := map[int]bool{}
	var fetchErr error
	for _, i := range batch {
		p := &st.Pending[i]
		body, err := e.Graph.GetForMailbox(ctx, mailbox,
			"messages/"+url.PathEscape(p.ID)+"?$select="+e.messageSelect())
		if err != nil {
			if httpStatus(err) == 404 {
				e.Log.Infof("%s: message #%s is gone (404: moved or deleted); skipped", mailbox, shortHash(p.ID))
				gone[i] = true
				continue
			}
			e.retryLater(p, now)
			if ra := retryAfter(err); ra > 0 {
				e.backoff(mailbox, now.Add(ra))
			}
			fetchErr = fmt.Errorf("fetch message #%s: %w", shortHash(p.ID), err)
			if httpStatus(err) == 429 {
				break
			}
			continue
		}
		if !p.Floor.IsZero() {
			var probe struct {
				Received time.Time `json:"receivedDateTime"`
			}
			if json.Unmarshal(body, &probe) == nil && !probe.Received.IsZero() && probe.Received.Before(p.Floor) {
				e.Log.Infof("%s: message #%s was received %s, before the watch floor %s (moved/restored mail); not delivered",
					mailbox, shortHash(p.ID), probe.Received.UTC().Format(time.RFC3339), p.Floor.UTC().Format(time.RFC3339))
				gone[i] = true
				continue
			}
		}
		raws = append(raws, body)
		sent = append(sent, i)
	}

	var postErr error
	if len(raws) > 0 {
		payload, err := BuildHookPayload(mailbox, raws, nil,
			Options{Folder: folder, IncludeBody: e.Opts.IncludeBody, MaxBytes: e.Opts.MaxBytes})
		if err == nil {
			err = e.Hook.Post(ctx, payload)
		}
		if err != nil {
			for _, i := range sent {
				e.retryLater(&st.Pending[i], now)
			}
			postErr = fmt.Errorf("deliver %d message(s): %w (kept pending; cursor already advanced)", len(sent), err)
		} else {
			for _, i := range sent {
				gone[i] = true
			}
		}
	}
	if len(gone) > 0 {
		keep := st.Pending[:0:0]
		for i, p := range st.Pending {
			if !gone[i] {
				keep = append(keep, p)
			}
		}
		st.Pending = keep
	}
	if err := e.Store.Save(mailbox, folder, st); err != nil {
		return errors.Join(postErr, fetchErr, err)
	}
	return errors.Join(postErr, fetchErr)
}

// retryLater records a failed attempt and schedules the next one with
// exponential backoff (30s doubling, capped at 5m).
func (e *Engine) retryLater(p *PendingMsg, now time.Time) {
	p.Attempts++
	d := 30 * time.Second
	for i := 1; i < p.Attempts && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	p.NextAttemptAt = now.Add(d)
}

func (e *Engine) capPending(mailbox string, p []PendingMsg) []PendingMsg {
	if over := len(p) - e.Opts.PendingCap; over > 0 {
		e.Log.Errorf(mailbox, "pending list over cap %d: dropping %d oldest undelivered message(s)", e.Opts.PendingCap, over)
		return append([]PendingMsg(nil), p[over:]...)
	}
	return p
}

func (e *Engine) backoff(mailbox string, until time.Time) {
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

func hasPending(p []PendingMsg, id string) bool {
	for _, x := range p {
		if x.ID == id {
			return true
		}
	}
	return false
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
	var s interface{ SyncStateNotFound() bool }
	return errors.As(err, &s) && s.SyncStateNotFound()
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
