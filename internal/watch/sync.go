package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// GraphDelta fetches a mailbox delta page (suffix or absolute deltaLink/nextLink).
type GraphDelta interface {
	MailboxDelta(ctx context.Context, mailbox, urlOrSuffix string) ([]byte, error)
}

// HookPoster delivers a payload to the downstream webhook.
type HookPoster interface {
	Post(ctx context.Context, payload []byte) error
}

// StateStore persists per-(mailbox,folder) delta cursor + seen-id set.
type StateStore interface {
	Get(mailbox, folder string) (State, error)
	Put(mailbox, folder string, st State) error
}

// Syncer runs one delta sync for a mailbox.
type Syncer struct {
	Graph GraphDelta
	Hook  HookPoster
	Store StateStore
	Opts  Options
	// Now is the clock used for the prime window; nil means time.Now.
	Now func() time.Time
}

// primeWindow bounds the first-run delta to recently received mail. Priming
// only needs a cursor plus the ids it must not re-deliver; paging the whole
// mailbox (with bodies) instead can take hours and gigabytes on a large inbox.
const primeWindow = time.Hour

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// isExpiredDeltaToken reports whether Graph rejected a stored cursor because
// its sync state is gone (410 Gone / SyncStateNotFound). Retrying the same
// cursor can never succeed; the only way out is to prime a fresh one.
func isExpiredDeltaToken(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SyncStateNotFound") || strings.Contains(msg, " 410 Gone")
}

const messageSelect = "id,conversationId,subject,bodyPreview,receivedDateTime,isRead,from,toRecipients" +
	",body"

// SyncOnce performs one delta sync for mailbox: page the cursor, classify
// new/changed, deliver, and (only on success) advance the cursor.
func (s *Syncer) SyncOnce(ctx context.Context, mailbox string) error {
	folder := s.Opts.Folder
	st, err := s.Store.Get(mailbox, folder)
	if err != nil {
		return err
	}
	firstRun := st.DeltaLink == ""

	next := st.DeltaLink
	if firstRun {
		st.Floor = s.now().Add(-primeWindow).UTC().Format(time.RFC3339)
		next = "mailFolders/" + folder + "/messages/delta?$select=" + messageSelect +
			"&$filter=receivedDateTime+ge+" + st.Floor
	}

	var items []rawWithID
	var removed []string
	var deltaLink string
	for {
		body, err := s.Graph.MailboxDelta(ctx, mailbox, next)
		if err != nil {
			if !firstRun && next == st.DeltaLink && isExpiredDeltaToken(err) {
				fmt.Fprintf(os.Stderr, "watch %s: delta cursor expired (%v); re-priming, mail received while it was dead is not delivered\n", mailbox, err)
				if err := s.Store.Put(mailbox, folder, State{Seen: st.Seen}); err != nil {
					return err
				}
				return s.SyncOnce(ctx, mailbox) // empty cursor → windowed first run, recurses at most once
			}
			return err
		}
		page, err := ParseDelta(body)
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			r := newRawWithID(it)
			if firstRun {
				r.raw = nil // priming only needs ids; don't hold bodies in memory
			}
			items = append(items, r)
		}
		removed = append(removed, page.Removed...)
		if page.NextLink != "" {
			next = page.NextLink
			continue
		}
		deltaLink = page.DeltaLink
		break
	}
	if deltaLink == "" {
		// Graph always returns a deltaLink on the final page; an empty one would
		// silently reset us to first-run on the next poll (re-priming and dropping
		// new mail). Fail loudly instead so the next interval retries from the
		// unchanged cursor.
		return fmt.Errorf("watch %s: delta response missing @odata.deltaLink on final page", mailbox)
	}

	seen := newStringSet(st.Seen)

	if firstRun {
		for _, it := range items {
			seen.add(it.id)
		}
		return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice(), Floor: st.Floor})
	}

	var emit []rawWithID
	for _, it := range items {
		if st.Floor != "" && it.received != "" && it.received < st.Floor {
			seen.add(it.id) // older than the prime window: a change event, never new mail
			continue
		}
		isNew := !seen.has(it.id)
		if isNew || s.Opts.IncludeChanged {
			emit = append(emit, it)
		}
		seen.add(it.id)
	}
	var del []string
	if s.Opts.IncludeDeleted {
		del = removed
	}

	if len(emit) == 0 && len(del) == 0 {
		return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice(), Floor: st.Floor})
	}

	raws := make([][]byte, len(emit))
	for i, it := range emit {
		raws[i] = it.raw
	}
	payload, err := BuildHookPayload(mailbox, toJSONList(raws), del, s.Opts)
	if err != nil {
		return err
	}
	if err := s.Hook.Post(ctx, payload); err != nil {
		return fmt.Errorf("deliver %s: %w", mailbox, err) // cursor NOT advanced
	}
	return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice(), Floor: st.Floor})
}

type rawWithID struct {
	raw      []byte
	id       string
	received string // receivedDateTime normalised to RFC 3339 UTC, "" if absent
}

func newRawWithID(raw json.RawMessage) rawWithID {
	var probe struct {
		ID       string `json:"id"`
		Received string `json:"receivedDateTime"`
	}
	_ = json.Unmarshal(raw, &probe)
	r := rawWithID{raw: raw, id: probe.ID}
	if t, err := time.Parse(time.RFC3339, probe.Received); err == nil {
		r.received = t.UTC().Format(time.RFC3339)
	}
	return r
}

func toJSONList(raws [][]byte) []json.RawMessage {
	out := make([]json.RawMessage, len(raws))
	for i, r := range raws {
		out[i] = r
	}
	return out
}

type stringSet struct {
	order []string
	m     map[string]bool
}

func newStringSet(init []string) *stringSet {
	s := &stringSet{m: map[string]bool{}}
	for _, v := range init {
		s.add(v)
	}
	return s
}
func (s *stringSet) has(v string) bool { return s.m[v] }
func (s *stringSet) add(v string) {
	if !s.m[v] {
		s.m[v] = true
		s.order = append(s.order, v)
	}
}
func (s *stringSet) slice() []string { return s.order }
