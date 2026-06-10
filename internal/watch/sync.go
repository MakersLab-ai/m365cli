package watch

import (
	"context"
	"encoding/json"
	"fmt"
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
		next = "mailFolders/" + folder + "/messages/delta?$select=" + messageSelect
	}

	var items []rawWithID
	var removed []string
	var deltaLink string
	for {
		body, err := s.Graph.MailboxDelta(ctx, mailbox, next)
		if err != nil {
			return err
		}
		page, err := ParseDelta(body)
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			items = append(items, newRawWithID(it))
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
		return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice()})
	}

	var emit []rawWithID
	for _, it := range items {
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
		return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice()})
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
	return s.Store.Put(mailbox, folder, State{DeltaLink: deltaLink, Seen: seen.slice()})
}

type rawWithID struct {
	raw []byte
	id  string
}

func newRawWithID(raw json.RawMessage) rawWithID {
	var probe struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	return rawWithID{raw: raw, id: probe.ID}
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
