package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateVersion is the schema version written to every v2 state file.
const StateVersion = 2

// PendingMsg is a message the delta cursor has already moved past but that was
// not yet delivered to the hook. It is retried until delivered, or dropped
// (logged) once it is older than PendingMaxAge.
type PendingMsg struct {
	ID       string    `json:"id"`
	FoundAt  time.Time `json:"found_at"`
	Attempts int       `json:"attempts,omitempty"`
	// NextAttemptAt delays only this message after a failed fetch (e.g. 5xx on
	// GET messages/{id}). Hook failures back off the whole queue instead
	// (StateV2.DeliverNotBefore) so delivery order is kept.
	NextAttemptAt time.Time `json:"next_attempt_at,omitzero"`
	// Floor is the state's floor when the id was found by the delta: if the
	// message turns out to be received before it, it is old mail moved or
	// restored into the folder and is not delivered. Zero = no check (ids
	// queued by the catch-up, which are older than the prime on purpose).
	Floor time.Time `json:"floor,omitzero"`
	// CatchUp marks ids queued by the post-re-prime catch-up list.
	CatchUp bool `json:"catch_up,omitempty"`
}

// CatchUp is a pending catch-up after a (re-)prime: once the prime has a
// deltaLink, list mail received since Anchor (bounded by count and window) and
// queue whatever is not in Exclude — the ids known before the cursor was lost.
type CatchUp struct {
	Anchor   time.Time `json:"anchor"`
	Exclude  []string  `json:"exclude,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
}

// StateV2 is the per-(mailbox, folder) watch state. It is written atomically
// after every delta page, so a crash or a 5xx mid-pagination resumes from the
// last persisted page instead of replaying the round start.
type StateV2 struct {
	Version int `json:"version"`
	// DeltaLink is the cursor of the last completed round. NextLink, when set,
	// is the resume point of a round in progress and takes precedence.
	DeltaLink string `json:"delta_link,omitempty"`
	NextLink  string `json:"next_link,omitempty"`
	// Priming is true while an IDs-only (re-)prime is paginating: ids are
	// marked seen, nothing is delivered.
	Priming        bool      `json:"priming,omitempty"`
	PrimeStartedAt time.Time `json:"prime_started_at,omitzero"`
	// Floor: mail received before it that shows up later as "created" (moved
	// or restored into the folder) is not delivered as new.
	Floor time.Time `json:"floor,omitzero"`
	// Seen holds recently seen ids in append order; Save keeps the newest
	// seenCap entries.
	Seen    []string     `json:"seen"`
	Pending []PendingMsg `json:"pending,omitempty"`
	CatchUp *CatchUp     `json:"catch_up,omitempty"`

	// LastSuccessAt is the last time the cursor was fully in sync (a round,
	// steady or prime, reached its deltaLink). It anchors the catch-up.
	LastSuccessAt        time.Time `json:"last_success_at,omitzero"`
	LastRoundCompletedAt time.Time `json:"last_round_completed_at,omitzero"`
	PrimedAt             time.Time `json:"primed_at,omitzero"`

	ErrorClass  string    `json:"error_class,omitempty"`
	ErrorCount  int       `json:"error_count,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`

	// StuckLink (hash) / StuckCount count consecutive non-transient 4xx answers
	// on the same stored link; at stuckLimit the cursor is treated as lost.
	StuckLink  string `json:"stuck_link,omitempty"`
	StuckCount int    `json:"stuck_count,omitempty"`

	// DeliverFailures / DeliverNotBefore: consecutive hook failures and the
	// backoff horizon of the pending queue (oldest first, one message per POST).
	DeliverFailures  int       `json:"deliver_failures,omitempty"`
	DeliverNotBefore time.Time `json:"deliver_not_before,omitzero"`

	// V1SeenMtime is the mtime of the v1 file whose seen ids were last merged
	// in. A newer v1 file means v0.2.x ran after us (rollback); its seen ids
	// are merged again so mail it delivered is not delivered twice.
	V1SeenMtime time.Time `json:"v1_seen_mtime,omitzero"`
}

// LoadStatus says what Load found on disk.
type LoadStatus int

const (
	LoadOK LoadStatus = iota
	LoadMissing
	LoadCorrupt
)

// V2Store keeps StateV2 files next to the v1 files of FileStore
// (<mbx>__<folder>.v2.json vs <mbx>__<folder>.json). It never writes, renames
// or deletes a v1 file, so rolling back to v0.2.x finds its cursor intact.
type V2Store struct {
	dir string
}

// NewV2Store returns a store rooted at dir (created on first Save).
func NewV2Store(dir string) *V2Store { return &V2Store{dir: dir} }

func (s *V2Store) base(mailbox, folder string) string {
	return sanitize(mailbox) + "__" + sanitize(folder)
}

// Path is the v2 state file for (mailbox, folder).
func (s *V2Store) Path(mailbox, folder string) string {
	return filepath.Join(s.dir, s.base(mailbox, folder)+".v2.json")
}

// V1Path is the v0.2.x state file for (mailbox, folder) — read-only here.
func (s *V2Store) V1Path(mailbox, folder string) string {
	return filepath.Join(s.dir, s.base(mailbox, folder)+".json")
}

// Load reads the v2 state. A missing file is LoadMissing; an unreadable,
// truncated or inconsistent one is LoadCorrupt (never an error that would
// crash-loop the watcher). mtime is the file's modification time.
func (s *V2Store) Load(mailbox, folder string) (st StateV2, status LoadStatus, mtime time.Time, err error) {
	p := s.Path(mailbox, folder)
	info, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return StateV2{}, LoadMissing, time.Time{}, nil
	}
	if err != nil {
		return StateV2{}, LoadOK, time.Time{}, err // e.g. permission: surface, don't re-prime
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return StateV2{}, LoadOK, time.Time{}, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return StateV2{}, LoadCorrupt, info.ModTime(), nil
	}
	if st.Version != StateVersion || (!st.Priming && st.DeltaLink == "" && st.NextLink == "") {
		return StateV2{}, LoadCorrupt, info.ModTime(), nil
	}
	return st, LoadOK, info.ModTime(), nil
}

// Save writes st atomically: temp file (0600) → fsync → rename → fsync dir.
// The seen list is trimmed to the newest seenCap ids.
func (s *V2Store) Save(mailbox, folder string, st *StateV2) error {
	st.Version = StateVersion
	st.Seen = trimSeen(st.Seen, seenCap)
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.dir, filepath.Base(s.Path(mailbox, folder)), data)
}

// Lock takes an exclusive, non-blocking lock for (mailbox, folder) so two
// watchers cannot race on the same state file. The lock lives as long as the
// returned file stays open (and is released by the OS when the process dies).
func (s *V2Store) Lock(mailbox, folder string) (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(s.dir, s.base(mailbox, folder)+".v2.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("another watcher holds %s: %w", p, err)
	}
	return f, nil
}

// Quarantine moves a corrupt v2 file aside (<file>.corrupt-<unix>) so it can
// be inspected and is not re-detected on every poll.
func (s *V2Store) Quarantine(mailbox, folder string, now time.Time) (string, error) {
	p := s.Path(mailbox, folder)
	dst := fmt.Sprintf("%s.corrupt-%d", p, now.Unix())
	return dst, os.Rename(p, dst)
}

// LoadV1 reads the v0.2.x state file without modifying it. ok=false when it
// does not exist or cannot be parsed.
func (s *V2Store) LoadV1(mailbox, folder string) (st State, mtime time.Time, ok bool) {
	p := s.V1Path(mailbox, folder)
	info, err := os.Stat(p)
	if err != nil {
		return State{}, time.Time{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(data, &st) != nil {
		return State{}, time.Time{}, false
	}
	return st, info.ModTime(), true
}

// writeFileAtomic replaces dir/name with data so a reader sees either the old
// or the new content, never a truncated file, even across a crash.
func writeFileAtomic(dir, name string, data []byte) (err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
